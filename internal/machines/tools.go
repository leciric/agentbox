package machines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/desktop"
	"agentbox/internal/machinesmedia"
	"agentbox/internal/mcp"
)

// Session is one AI tool's session: its worktree's machine, and the tools it
// is given for it.
type Session struct {
	Backend  Backend
	Worktree string
	Media    machinesmedia.Store
	// Tool is the AI tool, for the media it takes: claude, codex or other.
	Tool string
	// ID names the session in its media's sidecars.
	ID string
	// Progress hears an image build's output, which can take minutes.
	Progress func(string)
	// Load reads the machine's configuration; Load by default.
	Load func(worktree string) (Config, error)

	mu sync.Mutex
	pw *mcpClient
}

// Limits of what run answers with and waits for.
const (
	runTimeout    = 2 * time.Minute
	maxRunTimeout = 30 * time.Minute
	tailLines     = 100
	tailBytes     = 12 << 10
	// backgroundPeek is how long a background command runs before run
	// answers with the start of its log: long enough for a dev server to say
	// where it listens, or why it can't.
	backgroundPeek = 3 * time.Second
	// recordingLimit ends a recording nobody stopped.
	recordingLimit = 30 * time.Minute
	jobsDir        = "/tmp/agentbox-jobs"
	recordingFile  = "/tmp/agentbox-recording"
)

// notStarted is what every tool but machine_start says without a machine.
const notStarted = "this worktree's machine isn't running: call machine_start"

// Tools are the session's tools. Their descriptions are short: every one is
// resent with each model call.
func (s *Session) Tools(ctx context.Context) []mcp.Tool {
	dctx := desktop.OnTarget(ctx, desktop.Target{
		Command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return s.Backend.Command(ctx, s.Worktree, name, args...)
		},
		Running: func(ctx context.Context) bool {
			return s.Backend.Command(ctx, s.Worktree, "test", "-S", "/tmp/.X11-unix/X99").Run() == nil
		},
		NotRunning: notStarted,
	})
	tools := []mcp.Tool{
		{
			Name: "machine_start",
			Description: "Start this worktree's desktop machine: a Linux container with the worktree at the same path, " +
				"a display, Chromium and Playwright. The first start builds its image, which takes minutes.",
			Schema: object(nil, map[string]any{}),
			Wait: func(ctx context.Context, _ json.RawMessage) (string, error) {
				c, err := s.load()
				if err != nil {
					return "", err
				}
				st, err := s.Backend.Start(ctx, s.Worktree, c, s.Progress)
				if err != nil {
					return "", err
				}
				return describe(st), nil
			},
		},
		{
			Name:        "machine_stop",
			Description: "Stop this worktree's machine.",
			Schema:      object(nil, map[string]any{}),
			Wait: func(ctx context.Context, _ json.RawMessage) (string, error) {
				s.closeBrowser()
				if err := s.Backend.Stop(ctx, s.Worktree); err != nil {
					return "", err
				}
				return "Stopped.", nil
			},
		},
		{
			Name:        "machine_status",
			Description: "Whether this worktree's machine runs, and its published ports.",
			Schema:      object(nil, map[string]any{}),
			Wait: func(ctx context.Context, _ json.RawMessage) (string, error) {
				st, err := s.Backend.Status(ctx, s.Worktree)
				if err != nil {
					return "", err
				}
				return describe(st), nil
			},
		},
		{
			Name: "run",
			Description: "Run a shell command in the machine, in the worktree. background keeps it running (a dev server) " +
				"and answers with its job id and first output; job gives a background command's latest output.",
			Schema: object(nil, map[string]any{
				"command":    str("the command, run by bash"),
				"background": map[string]any{"type": "boolean", "description": "keep it running"},
				"timeout":    map[string]any{"type": "integer", "description": "seconds to wait in the foreground; 120 by default"},
				"job":        str("a background command's job id, to read its output instead"),
			}),
			Wait: s.run,
		},
		{
			Name: "preview_url",
			Description: "The URL on the user's machine of a port published from the machine. A server must listen " +
				"on 0.0.0.0, not localhost, to be reached there; the machine's own Chromium reaches localhost.",
			Schema: object([]string{"port"}, map[string]any{"port": map[string]any{"type": "integer", "description": "the port in the machine"}}),
			Wait:   s.previewURL,
		},
		{
			Name: "screenshot",
			Description: "Look at the machine's display. Saved to AgentBox's media; answers with the image and its path. " +
				"Coordinates for the other tools are in this image's pixels.",
			Schema:     object(nil, map[string]any{"caption": str("what it shows, for the user")}),
			RunContent: func(raw json.RawMessage) ([]mcp.Content, error) { return s.screenshot(dctx, raw) },
		},
		{
			Name:        "record_start",
			Description: "Start recording the machine's display as a video.",
			Schema:      object(nil, map[string]any{}),
			Wait:        func(ctx context.Context, _ json.RawMessage) (string, error) { return s.recordStart(ctx) },
		},
		{
			Name:        "record_stop",
			Description: "Stop the recording and save it to AgentBox's media; answers with its path.",
			Schema:      object(nil, map[string]any{"caption": str("what it shows, for the user")}),
			Wait:        s.recordStop,
		},
	}
	// The desktop's own tools, run on the machine's display, with short
	// descriptions instead of theirs.
	short := map[string]string{
		"click":  "Click at a point of the display, in the last screenshot's pixels.",
		"type":   "Type text into what has focus, or click x,y first; key presses a key after (Return).",
		"key":    "Press a key or combination: Return, Escape, ctrl+l.",
		"scroll": "Turn the mouse wheel over a point.",
	}
	for _, t := range desktop.Tools(dctx) {
		if d, ok := short[t.Name]; ok {
			t.Description = d + " Answers with a screenshot."
			tools = append(tools, t)
		}
	}
	for _, b := range browserTools {
		tools = append(tools, mcp.Tool{Name: b.name, Description: b.description, Schema: b.schema, RunContent: s.browserTool(ctx, b.name, b.defaults)})
	}
	return tools
}

func (s *Session) load() (Config, error) {
	if s.Load != nil {
		return s.Load(s.Worktree)
	}
	return Load(s.Worktree)
}

func (s *Session) requireRunning(ctx context.Context) error {
	st, err := s.Backend.Status(ctx, s.Worktree)
	if err != nil {
		return err
	}
	if !st.Running {
		return fmt.Errorf("%s", notStarted)
	}
	return nil
}

// describe is a machine's status, said for the model.
func describe(st Status) string {
	if !st.Exists || !st.Running {
		return fmt.Sprintf("The machine (%s) isn't running. Start it with machine_start.", st.Backend)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Running on %s as %s, with %s at the same path", st.Backend, st.Name, st.Worktree)
	if st.Memory != "" {
		fmt.Fprintf(&b, ", %s of memory", st.Memory)
	}
	if st.DockerInside {
		b.WriteString(", Docker inside")
	}
	b.WriteString(".")
	if len(st.Ports) > 0 {
		ports := make([]int, 0, len(st.Ports))
		for p := range st.Ports {
			ports = append(ports, p)
		}
		sort.Ints(ports)
		b.WriteString(" Published ports:")
		for _, p := range ports {
			fmt.Fprintf(&b, " %d→http://%s", p, st.Ports[p])
		}
		b.WriteString(".")
	}
	return b.String()
}

// exec runs a shell script in the machine and returns its combined output.
func (s *Session) exec(ctx context.Context, script string, args ...string) (string, error) {
	cmd := s.Backend.Command(ctx, s.Worktree, "sh", append([]string{"-c", script, "sh"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return string(out), fmt.Errorf("%s", msg)
		}
		return string(out), err
	}
	return string(out), nil
}

type runArgs struct {
	Command    string `json:"command"`
	Background bool   `json:"background"`
	Timeout    int    `json:"timeout"`
	Job        string `json:"job"`
}

func (s *Session) run(ctx context.Context, raw json.RawMessage) (string, error) {
	var in runArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", err
		}
	}
	if err := s.requireRunning(ctx); err != nil {
		return "", err
	}
	switch {
	case in.Job != "":
		return s.jobLog(ctx, in.Job)
	case strings.TrimSpace(in.Command) == "":
		return "", fmt.Errorf("give a command, or a job to read")
	case in.Background:
		out, err := s.exec(ctx, `set -e
mkdir -p `+jobsDir+`
log=$(mktemp `+jobsDir+`/XXXXXX)
setsid bash -lc "$1" >"$log" 2>&1 </dev/null &
echo $! >"$log.pid"
echo "${log##*/}"`, in.Command)
		if err != nil {
			return "", err
		}
		job := strings.TrimSpace(out)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(backgroundPeek):
		}
		log, err := s.jobLog(ctx, job)
		if err != nil {
			return "", err
		}
		return "Started as job " + job + ". " + log, nil
	}
	timeout := runTimeout
	if in.Timeout > 0 {
		timeout = min(time.Duration(in.Timeout)*time.Second, maxRunTimeout)
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := s.Backend.Command(rctx, s.Worktree, "bash", "-lc", in.Command)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := tail(out.String())
	switch {
	case rctx.Err() == context.DeadlineExceeded:
		return text + fmt.Sprintf("\n[stopped after %s: run it with background for longer]", timeout), nil
	case err != nil:
		if ee, ok := err.(*exec.ExitError); ok {
			return text + fmt.Sprintf("\n[exit %d]", ee.ExitCode()), nil
		}
		return "", err
	}
	return text + "\n[exit 0]", nil
}

func (s *Session) jobLog(ctx context.Context, job string) (string, error) {
	if strings.ContainsAny(job, "/ ") || job == "" {
		return "", fmt.Errorf("%q isn't a job id", job)
	}
	out, err := s.exec(ctx, `log=`+jobsDir+`/$1; [ -e "$log" ] || { echo "no job $1"; exit 1; }
if kill -0 "$(cat "$log.pid")" 2>/dev/null; then echo "[running]"; else echo "[exited]"; fi
tail -c `+strconv.Itoa(tailBytes)+` "$log" | tail -n `+strconv.Itoa(tailLines), job)
	if err != nil {
		return "", err
	}
	state, log, _ := strings.Cut(out, "\n")
	return fmt.Sprintf("%s Output (%s):\n%s", state, jobsDir+"/"+job, log), nil
}

// tail is the end of a command's output, which is what says how it went.
func tail(out string) string {
	cut := false
	if len(out) > tailBytes {
		out, cut = out[len(out)-tailBytes:], true
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > tailLines {
		lines, cut = lines[len(lines)-tailLines:], true
	}
	text := strings.Join(lines, "\n")
	if cut {
		text = "[…]\n" + text
	}
	return text
}

func (s *Session) previewURL(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct{ Port int }
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	st, err := s.Backend.Status(ctx, s.Worktree)
	if err != nil {
		return "", err
	}
	if !st.Running {
		return "", fmt.Errorf("%s", notStarted)
	}
	if addr, ok := st.Ports[in.Port]; ok {
		return "http://" + addr, nil
	}
	return "", fmt.Errorf("port %d isn't published (%s). Add it to \"ports\" in %s and call machine_start again, "+
		"or open http://localhost:%d in the machine's Chromium", in.Port, describe(st), ConfigFile, in.Port)
}

// media is a new item's sidecar, as much of it as is known before the file.
func (s *Session) media(ctx context.Context, kind, caption string) machinesmedia.Item {
	it := machinesmedia.Item{Kind: kind, Tool: s.Tool, Session: s.ID, Caption: caption}
	if it.Tool == "" {
		it.Tool = machinesmedia.ToolOther
	}
	it.Worktree, it.Repo, it.Branch = machinesmedia.Describe(ctx, s.Worktree)
	return it
}

// saved is where the store put an item.
func (s *Session) saved(it machinesmedia.Item, ext string) string {
	return filepath.Join(s.Media.Dir, it.ID+ext)
}

func (s *Session) screenshot(ctx context.Context, raw json.RawMessage) ([]mcp.Content, error) {
	var in struct{ Caption string }
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
	}
	jpg, sc, err := desktop.Screenshot(ctx)
	if err != nil {
		return nil, err
	}
	content := []mcp.Content{mcp.Image(jpg, "image/jpeg")}
	note := fmt.Sprintf("The screenshot is %s: give coordinates in its pixels.", sc.Shown)
	// What is kept is the display at its real size, as a PNG.
	path, err := s.saveScreenshot(ctx, sc.Real, in.Caption)
	if err != nil {
		return append(content, mcp.Text(note+" Saving it failed: "+err.Error())), nil
	}
	return append(content, mcp.Text(note+" Saved as "+path)), nil
}

func (s *Session) saveScreenshot(ctx context.Context, size desktop.Size, caption string) (string, error) {
	png, err := s.Backend.Command(ctx, s.Worktree, "ffmpeg", "-loglevel", "error", "-f", "x11grab", "-i", desktop.Display,
		"-frames:v", "1", "-f", "image2", "-c:v", "png", "-").Output()
	if err != nil {
		return "", fmt.Errorf("grabbing the display: %w", err)
	}
	it := s.media(ctx, machinesmedia.Screenshot, caption)
	it.Width, it.Height = size.Width, size.Height
	it, err = s.Media.Write(it, ".png", bytes.NewReader(png))
	if err != nil {
		return "", err
	}
	return s.saved(it, ".png"), nil
}

// recordStart starts ffmpeg in the machine, recording into the machine's own
// /tmp; record_stop copies the video out into the media store.
func (s *Session) recordStart(ctx context.Context) (string, error) {
	if err := s.requireRunning(ctx); err != nil {
		return "", err
	}
	_, err := s.exec(ctx, fmt.Sprintf(`f=%[1]s
if [ -e "$f.pid" ] && kill -0 "$(cat "$f.pid")" 2>/dev/null; then echo "already recording: call record_stop"; exit 1; fi
rm -f "$f.mp4" "$f.log"
setsid ffmpeg -hide_banner -loglevel error -f x11grab -draw_mouse 1 -framerate 15 -i %[2]s -t %[3]d %[4]s "$f.mp4" >"$f.log" 2>&1 </dev/null &
echo $! >"$f.pid"
for i in $(seq 50); do
  [ -e "$f.mp4" ] && exit 0
  kill -0 "$(cat "$f.pid")" 2>/dev/null || break
  sleep 0.1
done
echo "the recording didn't start: $(tail -n 5 "$f.log")"; exit 1`,
		recordingFile, desktop.Display, int(recordingLimit.Seconds()), agent.RecordingOutputArgs()))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Recording, for up to %s. Call record_stop to save it.", recordingLimit), nil
}

func (s *Session) recordStop(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct{ Caption string }
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", err
		}
	}
	out, err := s.exec(ctx, fmt.Sprintf(`f=%s
[ -e "$f.pid" ] || { echo "nothing is recording: call record_start"; exit 1; }
pid=$(cat "$f.pid")
kill -INT "$pid" 2>/dev/null || true
for i in $(seq 300); do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done
rm -f "$f.pid"
[ -s "$f.mp4" ] || { echo "the recording is empty: $(tail -n 5 "$f.log")"; exit 1; }
ffprobe -v error -select_streams v:0 -show_entries stream=width,height:format=duration -of default=nw=1:nk=1 "$f.mp4"`, recordingFile))
	if err != nil {
		return "", err
	}
	it := s.media(ctx, machinesmedia.Recording, in.Caption)
	if f := strings.Fields(out); len(f) >= 3 {
		it.Width, _ = strconv.Atoi(f[0])
		it.Height, _ = strconv.Atoi(f[1])
		secs, _ := strconv.ParseFloat(f[2], 64)
		it.DurationMs = int64(secs * 1000)
	}
	// Streamed out of the machine into the store, which keeps the item only
	// once all of it is there.
	cmd := s.Backend.Command(ctx, s.Worktree, "cat", recordingFile+".mp4")
	video, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("copying the recording out: %w", err)
	}
	it, err = s.Media.Write(it, ".mp4", video)
	if werr := cmd.Wait(); err == nil && werr != nil {
		_ = s.Media.Delete(it.ID)
		err = fmt.Errorf("copying the recording out: %w", werr)
	}
	if err != nil {
		return "", err
	}
	_, _ = s.exec(ctx, `rm -f "$1.mp4" "$1.log"`, recordingFile)
	path := s.saved(it, ".mp4")
	return fmt.Sprintf("Saved a %.1fs recording as %s", float64(it.DurationMs)/1000, path), nil
}
