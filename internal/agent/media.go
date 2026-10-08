package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/desktop"
	"agentbox/internal/state"
)

// Media is what agents, and you, keep as proof of an agent's work. The daemon
// writes every item, under <data>/media/<project>/<agent>/<id>/. An agent can add
// items to its own media, but never change or delete them.

const (
	MediaScreenshot = "screenshot"
	MediaRecording  = "recording"
	MediaReport     = "report"
	MediaLog        = "log"
	MediaNote       = "note"
	MediaFile       = "file"

	// DefaultRecordLimit stops a recording someone forgot about.
	DefaultRecordLimit = 10 * time.Minute
	maxRecordLimit     = time.Hour
	maxMediaBytes      = 1 << 30
	maxNoteBytes       = 20_000
	agentStateDir      = ".local/state/agentbox" // in the agent user's home
)

var MediaKinds = []string{MediaScreenshot, MediaRecording, MediaReport, MediaLog, MediaNote, MediaFile}

// MediaMeta holds the details that depend on an item's kind.
type MediaMeta struct {
	Width    int         `json:"width,omitempty"`
	Height   int         `json:"height,omitempty"`
	Duration float64     `json:"duration,omitempty"` // seconds
	Target   string      `json:"target,omitempty"`   // browser or display, for a screenshot
	URL      string      `json:"url,omitempty"`      // the page a browser screenshot shows
	Entry    string      `json:"entry,omitempty"`    // the file to open in a directory, like index.html
	Tests    *TestCounts `json:"tests,omitempty"`
}

type TestCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// MediaDir is where an agent's media is kept on the host.
func (m *Manager) MediaDir(project, agent string) string {
	return filepath.Join(m.Paths.Media(), project, agent)
}

// MediaPath is the host path of an item's file or directory, or "" for a note.
func (m *Manager) MediaPath(item state.Media) string {
	if item.File == "" {
		return ""
	}
	path := filepath.Join(m.MediaDir(item.Project, item.Agent), item.File)
	if old := m.legacyMediaDir(); old != "" {
		// Not yet moved there by MoveMedia: it is still where it was made.
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			legacy := filepath.Join(old, item.Project, item.Agent, item.File)
			if _, err := os.Stat(legacy); err == nil {
				return legacy
			}
		}
	}
	return path
}

// pendingMedia is an item whose content is being written into dir.
type pendingMedia struct {
	item state.Media
	dir  string
}

func (m *Manager) startMedia(a state.Agent, kind, name, source string) (*pendingMedia, error) {
	var random [3]byte
	rand.Read(random[:])
	id := time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(random[:])
	dir := filepath.Join(m.MediaDir(a.Project, a.Name), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if source == "" {
		source = "user"
	}
	return &pendingMedia{dir: dir, item: state.Media{
		ID: id, Project: a.Project, Agent: a.Name, Kind: kind, Name: name, Source: source, Meta: "{}", CreatedAt: time.Now(),
	}}, nil
}

func (p *pendingMedia) discard() { _ = os.RemoveAll(p.dir) }

// saveMedia records the item. file is its file or directory inside p.dir, or ""
// for a note.
func (m *Manager) saveMedia(ctx context.Context, p *pendingMedia, file string, meta MediaMeta) (state.Media, error) {
	item := p.item
	fail := func(err error) (state.Media, error) {
		p.discard()
		return state.Media{}, err
	}
	if file != "" {
		rel, err := filepath.Rel(m.MediaDir(item.Project, item.Agent), file)
		if err != nil {
			return fail(err)
		}
		item.File = rel
		info, err := os.Stat(file)
		if err != nil {
			return fail(err)
		}
		if info.IsDir() {
			item.Size, err = dirSize(file)
		} else {
			item.Size = info.Size()
			item.Mime = mimeType(file)
			item.SHA256, err = fileSHA256(file)
		}
		if err != nil {
			return fail(err)
		}
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return fail(err)
	}
	item.Meta = string(encoded)
	if err := m.Store.AddMedia(ctx, item); err != nil {
		return fail(err)
	}
	return item, nil
}

type ScreenshotOptions struct {
	Target   string // "browser" (the current page; the default), "display" (the whole screen) or "android" (the emulator)
	FullPage bool   // the whole page, not just what's visible (browser only)
	Name     string
	Source   string
}

// Screenshot captures the agent's browser page, or its whole display.
func (m *Manager) Screenshot(ctx context.Context, a state.Agent, opts ScreenshotOptions) (state.Media, error) {
	if err := m.requireRunning(ctx, a); err != nil {
		return state.Media{}, err
	}
	target := cmpOr(opts.Target, "browser")
	if opts.Target == "" {
		// Nothing asked for: the browser's page, unless it has none worth
		// shooting (an app on the display, a browser left on about:blank),
		// where the page would come out blank.
		if status, err := m.BrowserStatus(ctx, a); err == nil && status.Display && !hasRealPage(status.Pages) {
			target = "display"
		}
	}
	if target != "browser" && target != "display" && target != "android" {
		return state.Media{}, fmt.Errorf("unknown screenshot target %q: use browser, display or android", target)
	}
	p, err := m.startMedia(a, MediaScreenshot, cmpOr(strings.TrimSpace(opts.Name), target+" screenshot"), opts.Source)
	if err != nil {
		return state.Media{}, err
	}
	file := filepath.Join(p.dir, fileName(opts.Name, "screenshot", ".png"))
	meta := MediaMeta{Target: target}
	switch target {
	case "browser":
		meta.URL, err = m.browserScreenshot(ctx, a, file, opts.FullPage)
	case "display":
		err = m.displayScreenshot(ctx, a, file, p.item.ID)
	case "android":
		err = m.androidScreenshot(ctx, a, file, p.item.ID)
	}
	if err != nil {
		p.discard()
		return state.Media{}, err
	}
	meta.Width, meta.Height = pngSize(file)
	return m.saveMedia(ctx, p, file, meta)
}

func (m *Manager) browserScreenshot(ctx context.Context, a state.Agent, file string, fullPage bool) (string, error) {
	status, err := m.BrowserStatus(ctx, a)
	if err != nil {
		return "", err
	}
	if !status.Running || len(status.Pages) == 0 {
		return "", fmt.Errorf("%s's browser isn't running: start it, or take a display screenshot", a.Ref())
	}
	page, ok := firstRealPage(status.Pages)
	if !ok {
		return "", fmt.Errorf("%s's browser has no page open, only about:blank, which would come out blank: to capture an app on the display, use --target display", a.Ref())
	}
	err = m.withPage(ctx, a, page, func(ctx context.Context, session *devtoolsSession) error {
		params := map[string]any{"format": "png"}
		if fullPage {
			var metrics struct {
				CSSContentSize struct{ Width, Height float64 }
			}
			if err := session.call(ctx, "Page.getLayoutMetrics", map[string]any{}, &metrics); err != nil {
				return err
			}
			params["captureBeyondViewport"] = true
			params["clip"] = map[string]any{"x": 0, "y": 0, "width": metrics.CSSContentSize.Width, "height": metrics.CSSContentSize.Height, "scale": 1}
		}
		var shot struct{ Data string }
		if err := session.call(ctx, "Page.captureScreenshot", params, &shot); err != nil {
			return err
		}
		content, err := base64.StdEncoding.DecodeString(shot.Data)
		if err != nil {
			return err
		}
		return os.WriteFile(file, content, 0o600)
	})
	return page.URL, err
}

// firstRealPage returns the first page that isn't about:blank.
func firstRealPage(pages []BrowserPage) (BrowserPage, bool) {
	for _, p := range pages {
		if p.URL != "about:blank" {
			return p, true
		}
	}
	return BrowserPage{}, false
}

func hasRealPage(pages []BrowserPage) bool {
	_, ok := firstRealPage(pages)
	return ok
}

func (m *Manager) displayScreenshot(ctx context.Context, a state.Agent, file, id string) error {
	tmp := "/tmp/agentbox-screenshot-" + id + ".png"
	script := `[ -e /tmp/.X11-unix/X99 ] || { echo "the display isn't running: start the browser first" >&2; exit 1; }
ffmpeg -loglevel error -f x11grab -i :99 -frames:v 1 -y ` + tmp
	if _, err := m.agentShell(ctx, a, script); err != nil {
		return fmt.Errorf("taking the screenshot: %w", err)
	}
	defer func() { _, _ = m.Incus.Exec(context.WithoutCancel(ctx), a.Instance, "rm", "-f", tmp) }()
	err := m.Incus.PullFile(ctx, a.Instance, tmp, file)
	return err
}

// withPage runs fn with a DevTools protocol connection to one of the browser's pages.
func (m *Manager) withPage(ctx context.Context, a state.Agent, page BrowserPage, fn func(context.Context, *devtoolsSession) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, _, err := websocketDial(ctx, page.webSocket, m.devtools(a))
	if err != nil {
		return fmt.Errorf("connecting to the browser: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(128 << 20)
	return fn(ctx, &devtoolsSession{conn: conn})
}

// RecordingStatus describes the recording running in an agent, if any.
type RecordingStatus struct {
	Recording bool
	Target    string // display, browser or android
	Input     string // playwright (the default) or desktop, which shows the keys pressed
	Name      string
	Source    string
	StartedAt time.Time
	Limit     time.Duration
}

type recordingState struct {
	Target    string    `json:"target"`
	Input     string    `json:"input,omitempty"`
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	StartedAt time.Time `json:"startedAt"`
	Limit     int       `json:"limit"` // seconds
	// Staged is a recording written straight into the agent's media, through
	// the recordings device, rather than inside the agent and copied out.
	Staged bool `json:"staged,omitempty"`
}

// StartRecording records until StopRecording, or until limit: the agent's
// display or its browser's page, with the recorder in the agentbox binary
// (desktop.Record), or its Android emulator's screen with scrcpy. The target
// "auto", the default, takes the page when the browser is all there is to see.
func (m *Manager) StartRecording(ctx context.Context, a state.Agent, target, input, name string, limit time.Duration, source string) (RecordingStatus, error) {
	if err := m.requireRunning(ctx, a); err != nil {
		return RecordingStatus{}, err
	}
	if limit <= 0 {
		limit = DefaultRecordLimit
	}
	limit = min(limit, maxRecordLimit)
	st := recordingState{Target: cmpOr(target, desktop.RecordAuto), Input: cmpOr(input, RecordInputPlaywright), Name: cmpOr(strings.TrimSpace(name), "recording"), Source: cmpOr(source, "user"), StartedAt: time.Now().UTC(), Limit: int(limit.Seconds())}
	if st.Input != RecordInputPlaywright && st.Input != RecordInputDesktop {
		return RecordingStatus{}, fmt.Errorf("unknown recording input %q: use playwright or desktop", st.Input)
	}
	if st.Input == RecordInputDesktop && st.Target == "android" {
		return RecordingStatus{}, errors.New("--input desktop records the display or the browser, not android")
	}
	staged, err := m.ensureRecordingStage(ctx, a)
	if err != nil {
		m.logf("recording %s: copying it out instead of writing it to its media: %v", a.Ref(), err)
	}
	st.Staged = staged
	script, err := startRecordingScript(st)
	if err != nil {
		return RecordingStatus{}, err
	}
	out, err := m.agentShell(ctx, a, script)
	if err != nil {
		return RecordingStatus{}, fmt.Errorf("starting the recording: %w", err)
	}
	if t := strings.TrimSpace(out); t != "" {
		st.Target = t
	}
	return RecordingStatus{Recording: true, Target: st.Target, Input: st.Input, Name: st.Name, Source: st.Source, StartedAt: st.StartedAt, Limit: limit}, nil
}

// RecordingOutputArgs are what a display recording is encoded with, after its
// input, for one made outside an agent (`agentbox machines`).
func RecordingOutputArgs() string {
	return "-vf '" + desktop.EvenSize + "' " + strings.Join(desktop.EncodeArgs, " ")
}

// recordingDevice mounts the agent's recording stage at recordingOut, so that
// a recording is written where it will be kept, and stopping it is a rename
// rather than a copy out of the agent. The stage is in the agent's media
// directory, on the same filesystem as its items, and only ever holds the
// recording in progress: the agent can write to it, but an item, once kept,
// is out of its reach. required=false lets the agent start without it, should
// its media be deleted or moved.
const (
	recordingDevice = "recordings"
	recordingOut    = agentStateDir + "/out"
)

func (m *Manager) recordingStage(a state.Agent) string {
	return filepath.Join(m.MediaDir(a.Project, a.Name), ".recording")
}

// ensureRecordingStage mounts the recording stage into the agent, unless it
// is already, and says whether a recording can be written to it.
func (m *Manager) ensureRecordingStage(ctx context.Context, a state.Agent) (bool, error) {
	stage := m.recordingStage(a)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return false, err
	}
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return false, err
	}
	if device, ok := devices[recordingDevice]; ok {
		if device["source"] == stage {
			return true, nil
		}
		// The media moved (agentbox data move), or this is a copy of another agent.
		if err := m.Incus.RemoveDevice(ctx, a.Instance, recordingDevice); err != nil {
			return false, err
		}
	}
	err = m.Incus.AddDevice(ctx, a.Instance, recordingDevice, "disk",
		"source="+stage, "path=/home/"+m.User.Name+"/"+recordingOut, "required=false")
	return err == nil, err
}

// startRecordingScript is what runs inside the agent to start a recording, and
// what the integration test runs against a display of its own. It prints what
// is being recorded.
func startRecordingScript(st recordingState) (string, error) {
	// started waits until the recorder is running, so a recorder that can't
	// start is caught here rather than at stop. The desktop recorder says so
	// once ffmpeg has written its first frames, about 150 ms in (more for the
	// browser, which waits for the page's first frame); scrcpy may take
	// longer to connect to the device before it writes anything, so it keeps
	// a second.
	var recorder, started string
	switch st.Target {
	case desktop.RecordAuto, desktop.RecordDisplay, desktop.RecordBrowser:
		recorder = fmt.Sprintf(`command -v agentbox >/dev/null || { echo "agentbox isn't installed in this machine" >&2; exit 1; }
setsid agentbox desktop record --target %s --input %s --limit %ds --bottom %d --ready "$dir/recording.ready" "$out" >"$dir/recording.log" 2>&1 </dev/null &`,
			st.Target, st.Input, st.Limit, dockMargin+dockHeight/2)
		started = `i=0
while [ ! -s "$dir/recording.ready" ] && kill -0 "$(cat "$dir/recording.pid")" 2>/dev/null && [ "$i" -lt 200 ]; do sleep 0.05; i=$((i + 1)); done
target=$(cat "$dir/recording.ready" 2>/dev/null || true)`
	case "android":
		// The device's own screen, at its resolution, rather than the display showing it.
		recorder = fmt.Sprintf(`[ -x %[1]s ] || { echo "the emulator isn't running: start it with agentbox android start" >&2; exit 1; }
setsid %[1]s record "$out" %[2]d >"$dir/recording.log" 2>&1 </dev/null &`, androidScriptPath, st.Limit)
		started = "sleep 1\ntarget=android"
	default:
		return "", fmt.Errorf("unknown recording target %q: use auto, display, browser or android", st.Target)
	}
	encoded, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	out := `"$dir/recording.mp4"`
	if st.Staged {
		out = `"$dir/out/recording.mp4"`
	}
	return fmt.Sprintf(`set -eu
dir="$HOME/%[1]s"
out=%[2]s
mkdir -p "$dir"
if [ -f "$dir/recording.pid" ] && kill -0 "$(cat "$dir/recording.pid")" 2>/dev/null; then echo "a recording is already running: stop it first" >&2; exit 1; fi
rm -f "$dir"/recording.* "$out"
%[3]s
echo $! >"$dir/recording.pid"
%[5]s
kill -0 "$(cat "$dir/recording.pid")" 2>/dev/null && [ -n "$target" ] || { echo "the recording didn't start:" >&2; tail -n 5 "$dir/recording.log" >&2; kill "$(cat "$dir/recording.pid")" 2>/dev/null || true; rm -f "$dir"/recording.* "$out"; exit 1; }
printf '%%s' %[4]s | sed "s/\"target\":\"[a-z]*\"/\"target\":\"$target\"/" >"$dir/recording.json"
echo "$target"`,
		agentStateDir, out, recorder, shellQuote(string(encoded)), started), nil
}

// Recording inputs: how the flow being recorded is driven, which decides
// whether there is anything for an overlay to show.
const (
	// RecordInputPlaywright is the default: Chromium synthesizes the input
	// itself over the DevTools protocol, so X sees neither key nor pointer.
	RecordInputPlaywright = "playwright"
	// RecordInputDesktop is for a flow driven through X, with xdotool: the
	// cursor really moves, and the keys and clicks are drawn onto the video.
	RecordInputDesktop = "desktop"
)

// dockHeight and dockMargin are the dock's height and the gap under it, from
// the tint2 config browser.sh writes: its panel_size height and panel_margin.
// The key captions of a desktop recording are centred on the dock, over
// nothing but the dock itself, and TestDockSizeMatchesBrowserScript keeps
// these in step with the script.
const (
	dockHeight = 56
	dockMargin = 10
)

func (m *Manager) Recording(ctx context.Context, a state.Agent) (RecordingStatus, error) {
	if m.requireRunning(ctx, a) != nil {
		return RecordingStatus{}, nil
	}
	out, err := m.agentShell(ctx, a, fmt.Sprintf(`dir="$HOME/%s"
if [ -f "$dir/recording.json" ] && [ -f "$dir/recording.pid" ] && kill -0 "$(cat "$dir/recording.pid")" 2>/dev/null; then cat "$dir/recording.json"; fi`, agentStateDir))
	if err != nil || strings.TrimSpace(out) == "" {
		return RecordingStatus{}, err
	}
	var st recordingState
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return RecordingStatus{}, err
	}
	return RecordingStatus{Recording: true, Target: cmpOr(st.Target, "display"), Input: cmpOr(st.Input, RecordInputPlaywright), Name: st.Name, Source: st.Source, StartedAt: st.StartedAt, Limit: time.Duration(st.Limit) * time.Second}, nil
}

// stopRecordingScript ends the recording inside the agent and prints its
// details, then ffprobe's line about the file it left behind.
func stopRecordingScript() string {
	return fmt.Sprintf(`set -eu
dir="$HOME/%s"
[ -f "$dir/recording.json" ] || { echo "no recording is running" >&2; exit 1; }
out="$dir/recording.mp4"
if grep -q '"staged":true' "$dir/recording.json"; then out="$dir/out/recording.mp4"; fi
pid=$(cat "$dir/recording.pid" 2>/dev/null || true)
if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
  # Both finish the file when told to stop. scrcpy ignores INT, which a
  # background process starts out ignoring, so it gets TERM.
  if grep -q '"target":"android"' "$dir/recording.json"; then kill -TERM "$pid"; else kill -INT "$pid"; fi
  i=0
  while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 500 ]; do sleep 0.05; i=$((i + 1)); done
fi
[ -s "$out" ] || { echo "the recording is empty:" >&2; tail -n 5 "$dir/recording.log" >&2; rm -f "$dir"/recording.* "$out"; exit 1; }
cat "$dir/recording.json"
echo
ffprobe -v error -show_entries stream=width,height:format=duration -of csv=p=0 "$out" || true`, agentStateDir)
}

// StopRecording finishes the recording and keeps it as a media item.
func (m *Manager) StopRecording(ctx context.Context, a state.Agent) (state.Media, error) {
	if err := m.requireRunning(ctx, a); err != nil {
		return state.Media{}, err
	}
	home := "/home/" + m.User.Name
	out, err := m.agentShell(ctx, a, stopRecordingScript())
	if err != nil {
		return state.Media{}, fmt.Errorf("stopping the recording: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var st recordingState
	if err := json.Unmarshal([]byte(lines[0]), &st); err != nil {
		return state.Media{}, fmt.Errorf("reading the recording's details: %w", err)
	}
	meta := MediaMeta{Target: cmpOr(st.Target, "display")}
	for _, line := range lines[1:] {
		line = strings.Trim(strings.TrimSpace(line), ",")
		if w, h, ok := strings.Cut(line, ","); ok {
			meta.Width, _ = strconv.Atoi(w)
			meta.Height, _ = strconv.Atoi(h)
		} else if d, err := strconv.ParseFloat(line, 64); err == nil {
			meta.Duration = d
		}
	}

	p, err := m.startMedia(a, MediaRecording, st.Name, st.Source)
	if err != nil {
		return state.Media{}, err
	}
	p.item.CreatedAt = st.StartedAt
	file := filepath.Join(p.dir, fileName(st.Name, "recording", ".mp4"))
	remote := home + "/" + agentStateDir
	defer func() {
		_, _ = m.Incus.Exec(context.WithoutCancel(ctx), a.Instance, "sh", "-c", "rm -f "+remote+"/recording.* "+remote+"/input.*")
	}()
	if st.Staged {
		err = takeStaged(filepath.Join(m.recordingStage(a), "recording.mp4"), file)
	} else {
		err = m.Incus.PullFile(ctx, a.Instance, remote+"/recording.mp4", file)
	}
	if err != nil {
		p.discard()
		return state.Media{}, err
	}
	return m.saveMedia(ctx, p, file, meta)
}

// takeStaged moves a recording the agent wrote to its stage into the item it
// is kept as. The agent could have left anything there, so it has to be a
// file of its own: not a link to one of the host's, which saving and serving
// it would follow.
func takeStaged(staged, file string) error {
	info, err := os.Lstat(staged)
	if err != nil {
		return fmt.Errorf("the recording isn't in the agent's media: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = os.Remove(staged)
		return errors.New("the recording the agent left isn't a file")
	}
	return os.Rename(staged, file)
}

type AddMediaOptions struct {
	Path   string // in the agent; relative paths are relative to its worktree
	Kind   string // default: guessed from the file
	Name   string
	Source string
}

// AddMedia copies a file or directory out of the agent: a report, a log, an image.
func (m *Manager) AddMedia(ctx context.Context, a state.Agent, opts AddMediaOptions) (state.Media, error) {
	if err := m.requireRunning(ctx, a); err != nil {
		return state.Media{}, err
	}
	if opts.Kind != "" && (opts.Kind == MediaNote || !slices.Contains(MediaKinds, opts.Kind)) {
		return state.Media{}, fmt.Errorf("unknown kind %q: use screenshot, recording, report, log or file", opts.Kind)
	}
	path := opts.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.Worktree, path)
	}
	out, err := m.agentShell(ctx, a, fmt.Sprintf(`p=%s
[ -e "$p" ] || { echo "$p doesn't exist" >&2; exit 1; }
if [ -d "$p" ]; then echo dir; du -sb "$p" | cut -f1; [ -f "$p/index.html" ] && echo index.html || true
else echo file; stat -c %%s "$p"; fi`, shellQuote(path)))
	if err != nil {
		return state.Media{}, err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return state.Media{}, fmt.Errorf("couldn't read %s in the agent", path)
	}
	isDir, hasIndex := fields[0] == "dir", len(fields) > 2
	if isDir && !hasIndex {
		return state.Media{}, fmt.Errorf("%s is a directory: zip it, point at the one file that matters, or take a screenshot or recording instead", path)
	}
	if size, _ := strconv.ParseInt(fields[1], 10, 64); size > maxMediaBytes {
		return state.Media{}, fmt.Errorf("%s is %d MiB, more than the %d MiB limit", path, size>>20, maxMediaBytes>>20)
	}

	base := filepath.Base(path)
	kind := cmpOr(opts.Kind, guessKind(base, isDir, hasIndex))
	p, err := m.startMedia(a, kind, cmpOr(strings.TrimSpace(opts.Name), base), opts.Source)
	if err != nil {
		return state.Media{}, err
	}
	target := filepath.Join(p.dir, base)
	if isDir {
		err = m.Incus.PullDir(ctx, a.Instance, path, p.dir)
	} else {
		err = m.Incus.PullFile(ctx, a.Instance, path, target)
	}
	if err != nil {
		p.discard()
		return state.Media{}, err
	}
	var meta MediaMeta
	switch {
	case isDir && hasIndex:
		meta.Entry = "index.html"
	case strings.EqualFold(filepath.Ext(base), ".xml"):
		meta.Tests = junitCounts(target)
	case kind == MediaScreenshot:
		meta.Width, meta.Height = pngSize(target)
	}
	return m.saveMedia(ctx, p, target, meta)
}

// AddNote keeps a short text, like a summary of what the agent did.
func (m *Manager) AddNote(ctx context.Context, a state.Agent, text, name, source string) (state.Media, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return state.Media{}, errors.New("the note is empty")
	}
	if len(text) > maxNoteBytes {
		return state.Media{}, fmt.Errorf("the note is longer than %d bytes: add it as a file instead", maxNoteBytes)
	}
	title, _, _ := strings.Cut(text, "\n")
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:60]) + "…"
	}
	p, err := m.startMedia(a, MediaNote, cmpOr(strings.TrimSpace(name), title), source)
	if err != nil {
		return state.Media{}, err
	}
	p.item.Text = text
	item, err := m.saveMedia(ctx, p, "", MediaMeta{})
	_ = os.Remove(p.dir) // a note has no file
	return item, err
}

type LogsOptions struct {
	Service  string // a Docker Compose service in the worktree
	Since    string // for a service or logcat, like 10m (default 30m, and 10m for logcat)
	Terminal bool   // the tmux session's scrollback instead
	Window   string // for the terminal; default: the AI tool's window, or the shell
	Android  bool   // the emulator's logcat instead
	Package  string // for logcat: only this app's lines
	Name     string
	Source   string
}

// AddLogs keeps a Compose service's logs, the agent's terminal scrollback, or
// its emulator's logcat.
func (m *Manager) AddLogs(ctx context.Context, a state.Agent, opts LogsOptions) (state.Media, error) {
	if err := m.requireRunning(ctx, a); err != nil {
		return state.Media{}, err
	}
	var script, label string
	switch {
	case opts.Terminal:
		window := cmpOr(opts.Window, "shell")
		if opts.Window == "" && a.AI != "none" {
			window = a.AI
		}
		script = "tmux capture-pane -p -J -S -10000 -t " + shellQuote(session+":"+window)
		label = window + " terminal"
	case opts.Service != "":
		script = fmt.Sprintf("cd %s && docker compose logs --no-color --timestamps --since %s %s",
			shellQuote(a.Worktree), shellQuote(cmpOr(opts.Since, "30m")), shellQuote(opts.Service))
		label = opts.Service + " logs"
	case opts.Android:
		since, err := time.ParseDuration(cmpOr(opts.Since, "10m"))
		if err != nil || since <= 0 {
			return state.Media{}, fmt.Errorf("invalid time %q: use a duration like 10m", opts.Since)
		}
		script = fmt.Sprintf("%s logcat %d", androidScriptPath, int(since.Seconds()))
		label = "logcat"
		if opts.Package != "" {
			script += " " + shellQuote(opts.Package)
			label = opts.Package + " logcat"
		}
	default:
		return state.Media{}, errors.New("choose a Compose service, the terminal, or the Android emulator")
	}
	out, err := m.agentShell(ctx, a, script)
	if err != nil {
		return state.Media{}, fmt.Errorf("reading the logs: %w", err)
	}
	p, err := m.startMedia(a, MediaLog, cmpOr(strings.TrimSpace(opts.Name), label), opts.Source)
	if err != nil {
		return state.Media{}, err
	}
	file := filepath.Join(p.dir, fileName(cmpOr(opts.Name, label), "logs", ".log"))
	if err := os.WriteFile(file, []byte(out), 0o600); err != nil {
		p.discard()
		return state.Media{}, err
	}
	return m.saveMedia(ctx, p, file, MediaMeta{})
}

func (m *Manager) DeleteMedia(ctx context.Context, item state.Media) error {
	if err := m.Store.DeleteMedia(ctx, item.ID); err != nil {
		return err
	}
	if old := m.legacyMediaDir(); old != "" {
		_ = os.RemoveAll(filepath.Join(old, item.Project, item.Agent, item.ID))
	}
	return os.RemoveAll(filepath.Join(m.MediaDir(item.Project, item.Agent), item.ID))
}

// deleteAgentMedia deletes an agent's media as it goes, but for its
// favorites: those are kept as if the agent's media were, their retention
// clock started for when they stop being favorites.
func (m *Manager) deleteAgentMedia(ctx context.Context, a state.Agent) error {
	kept, err := m.Store.DeleteAgentMedia(ctx, a.Project, a.Name)
	if err != nil {
		return err
	}
	if old := m.legacyMediaDir(); old != "" {
		removeAllBut(filepath.Join(old, a.Project, a.Name), kept)
	}
	if len(kept) == 0 {
		return os.RemoveAll(m.MediaDir(a.Project, a.Name))
	}
	removeAllBut(m.MediaDir(a.Project, a.Name), kept)
	return m.keepAgentMedia(ctx, a)
}

// removeAllBut empties an agent's media directory of everything but the
// kept items' own directories, each named for its item's ID.
func removeAllBut(dir string, kept []state.Media) {
	if len(kept) == 0 {
		_ = os.RemoveAll(dir)
		return
	}
	keep := make(map[string]bool, len(kept))
	for _, item := range kept {
		keep[item.ID] = true
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !keep[e.Name()] {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// keepAgentMedia starts the retention clock on an agent's media, now that its
// agent is going: from this moment, not from when each item was made, so an
// item already older than the retention window doesn't expire the instant it
// outlives its agent. The files themselves are untouched: MediaDir is
// never inside the worktree or instance Destroy just removed.
func (m *Manager) keepAgentMedia(ctx context.Context, a state.Agent) error {
	return m.Store.OrphanAgentMedia(ctx, a.Project, a.Name, time.Now())
}

// ExportMedia copies an agent's media into a new folder under dir, with a
// README.md listing everything, ready to attach to a pull request.
func (m *Manager) ExportMedia(ctx context.Context, a state.Agent, dir string) (string, int, error) {
	items, err := m.Store.Media(ctx, a.Project, a.Name)
	if err != nil {
		return "", 0, err
	}
	if len(items) == 0 {
		return "", 0, fmt.Errorf("%s has no media to export", a.Ref())
	}
	slices.Reverse(items) // oldest first
	out := filepath.Join(dir, fmt.Sprintf("%s-%s-media-%s", a.Project, a.Name, time.Now().Format("20060102-150405")))
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", 0, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", cmpOr(a.Title, a.Ref()))
	fmt.Fprintf(&b, "Media from AgentBox agent `%s`, branch `%s`, exported %s.\n", a.Ref(), a.Branch, time.Now().Format("2006-01-02 15:04"))
	for _, item := range items {
		from := "the agent"
		if item.Source == "user" {
			from = "you"
		}
		fmt.Fprintf(&b, "\n## %s\n\n_%s, %s, from %s_\n\n", item.Name, item.Kind, item.CreatedAt.Format("2006-01-02 15:04:05"), from)
		if item.Kind == MediaNote {
			fmt.Fprintf(&b, "%s\n", item.Text)
			continue
		}
		src := m.MediaPath(item)
		name := item.ID + "-" + filepath.Base(src)
		if err := copyPath(src, filepath.Join(out, name)); err != nil {
			return "", 0, err
		}
		var meta MediaMeta
		_ = json.Unmarshal([]byte(item.Meta), &meta)
		link := name
		if meta.Entry != "" {
			link = name + "/" + meta.Entry
		}
		switch item.Kind {
		case MediaScreenshot:
			fmt.Fprintf(&b, "![%s](%s)\n", item.Name, link)
		case MediaRecording:
			fmt.Fprintf(&b, "[%s](%s) (%.0f s)\n", item.Name, link, meta.Duration)
		default:
			fmt.Fprintf(&b, "[%s](%s)\n", item.Name, link)
		}
		if meta.Tests != nil {
			fmt.Fprintf(&b, "\nTests: %d passed, %d failed, %d skipped\n", meta.Tests.Passed, meta.Tests.Failed, meta.Tests.Skipped)
		}
	}
	if err := os.WriteFile(filepath.Join(out, "README.md"), []byte(b.String()), 0o644); err != nil {
		return "", 0, err
	}
	return out, len(items), nil
}

// agentShell runs a shell script as the agent's user and returns its output. A
// failure's error holds what the script printed to stderr.
func (m *Manager) agentShell(ctx context.Context, a state.Agent, script string) (string, error) {
	var stdout, stderr bytes.Buffer
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, script, nil, &stdout, &stderr); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.String(), errors.New(msg)
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func fileName(name, fallback, ext string) string {
	base := strings.Trim(unsafeFileChars.ReplaceAllString(strings.TrimSpace(name), "-"), "-.")
	if base == "" {
		base = fallback
	}
	if len(base) > 80 {
		base = base[:80]
	}
	if ext != "" && !strings.HasSuffix(strings.ToLower(base), ext) {
		base += ext
	}
	return base
}

func guessKind(name string, isDir, hasIndex bool) string {
	if isDir {
		// AddMedia refuses a bare directory before calling this, so a
		// directory here always has an index.html: a report.
		return MediaReport
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return MediaScreenshot
	case ".mp4", ".webm", ".mov", ".mkv":
		return MediaRecording
	case ".xml", ".html":
		return MediaReport
	case ".log", ".txt", ".out":
		return MediaLog
	}
	return MediaFile
}

var mimeTypes = map[string]string{
	".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime", ".mkv": "video/x-matroska",
	".log": "text/plain; charset=utf-8", ".txt": "text/plain; charset=utf-8", ".out": "text/plain; charset=utf-8",
	".md": "text/markdown; charset=utf-8",
}

func mimeType(file string) string {
	ext := strings.ToLower(filepath.Ext(file))
	if t, ok := mimeTypes[ext]; ok {
		return t
	}
	return mime.TypeByExtension(ext)
}

func pngSize(file string) (int, int) {
	f, err := os.Open(file)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = f.Close() }()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// junitCounts counts a JUnit XML file's test cases by outcome; nil if it has
// none. Counting cases, rather than adding up suites' attributes, handles nested
// suites (Jest, JUnit 5) and Node's reporter, which puts top-level tests
// outside any suite.
func junitCounts(file string) *TestCounts {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var counts *TestCounts
	inCase, outcome := false, ""
	decoder := xml.NewDecoder(f)
	for {
		token, err := decoder.Token()
		if err != nil {
			return counts
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "testcase":
				inCase, outcome = true, "passed"
			case "failure", "error":
				if inCase {
					outcome = "failed"
				}
			case "skipped":
				if inCase && outcome != "failed" {
					outcome = "skipped"
				}
			}
		case xml.EndElement:
			if t.Name.Local != "testcase" || !inCase {
				continue
			}
			inCase = false
			if counts == nil {
				counts = &TestCounts{}
			}
			switch outcome {
			case "failed":
				counts.Failed++
			case "skipped":
				counts.Skipped++
			default:
				counts.Passed++
			}
		}
	}
}

func fileSHA256(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info, err := d.Info(); err == nil && !d.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.CopyFS(dst, os.DirFS(src))
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
