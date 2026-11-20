package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClaude stands in for claude -p: it answers --help and --version, and
// otherwise writes the stream a short session would, keeping its transcript
// where Claude Code does. For the edit task it makes the edit.
const fakeClaude = `#!/usr/bin/env bash
case "$1" in
--help) echo "  --include-hook-events  Include all hook lifecycle events"; exit 0 ;;
--version) echo "9.9.9 (Claude Code)"; exit 0 ;;
esac
prompt=$2
echo "$*" >"${FAKE_ROOT:?}/claude-args"
dir="$HOME/.claude/projects/$(pwd | sed 's/[^a-zA-Z0-9]/-/g')"
mkdir -p "$dir"
tr="$dir/session-$$.jsonl"
both() {
	printf '%s\n' "$1"
	printf '%s,"timestamp":"%s"}\n' "${1%\}}" "$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)" >>"$tr"
}
printf '%s\n' '{"type":"system","subtype":"init","model":"claude-test","claude_code_version":"9.9.9","apiKeySource":"none","mcp_servers":[]}'
both '{"type":"user","message":{"role":"user","content":"the prompt"}}'
sleep 0.2
both '{"type":"assistant","message":{"id":"m1","model":"claude-test","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"./check.sh"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":1000,"output_tokens":20}}}'
printf '%s\n' '{"type":"system","subtype":"hook_started","hook_id":"h1"}'
sleep 0.2
printf '%s\n' '{"type":"system","subtype":"hook_response","hook_id":"h1"}'
both '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}'
sleep 0.1
both '{"type":"assistant","message":{"id":"m2","model":"claude-test","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{}}],"usage":{"input_tokens":10,"cache_read_input_tokens":1100,"output_tokens":30}}}'
case $prompt in *Title*)
	printf '\n// Title is a stand-in.\nfunc Title(s string) string { return s }\n' >>strutil/strutil.go
	printf '\n// Title(s) is tested here.\n' >>strutil/strutil_test.go
	printf -- '- %sTitle(s)%s: a stand-in.\n' '` + "`" + `' '` + "`" + `' >>README.md
	;;
esac
both '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2"}]}}'
sleep 0.1
both '{"type":"assistant","message":{"id":"m3","model":"claude-test","content":[{"type":"text","text":"Done."}],"usage":{"input_tokens":1,"cache_read_input_tokens":1200,"output_tokens":5}}}'
printf '%s\n' '{"type":"result","subtype":"success","duration_ms":700,"duration_api_ms":300,"num_turns":3,"total_cost_usd":0.01,"is_error":false,"result":"Done."}'
`

// fakeAgentbox stands in for the agentbox command: exec runs the command in
// the fake agent's worktree with its own HOME, and only notes the detached
// load command instead of spinning the test machine's CPUs.
const fakeAgentbox = `#!/usr/bin/env bash
case "$1" in
exec)
	ref=$2
	shift 3
	name=${ref#*/}
	case $* in setsid*)
		echo "$ref $*" >>"$FAKE_ROOT/load.log"
		exit 0
		;;
	esac
	cd "$FAKE_ROOT/agents/$name/worktree" 2>/dev/null || { echo "no agent $ref" >&2; exit 1; }
	HOME="$FAKE_ROOT/agents/$name/home" exec bash -lc "export PATH=\"\$FAKE_ROOT/bin:\$PATH\"; $*"
	;;
*) exit 0 ;;
esac
`

// fakeDaemon serves the few routes the benchmark uses, keeping agents as
// directories, and plays an agent's turn by running the fake claude with its
// task in the agent's worktree.
type fakeDaemon struct {
	t       *testing.T
	root    string
	mu      sync.Mutex
	project map[string]any
	patched map[string]any
	agents  map[string]*fakeAgent
	made    []string
	jobs    int
	adds    int
}

type fakeAgent struct {
	req    createRequest
	thread chatThread
}

func (f *fakeDaemon) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // v1 agents p a [chat]
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/projects":
		if f.project == nil {
			reply([]any{})
		} else {
			reply([]any{f.project})
		}
	case r.Method == "POST" && r.URL.Path == "/v1/projects":
		var req struct{ Path, Name string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.adds++
		f.project = map[string]any{"name": req.Name, "root": req.Path}
		reply(f.project)
	case r.Method == "PATCH" && len(parts) == 3 && parts[1] == "projects":
		_ = json.NewDecoder(r.Body).Decode(&f.patched)
		reply(f.project)
	case r.Method == "POST" && r.URL.Path == "/v1/agents":
		var req createRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := f.create(req); err != nil {
			w.WriteHeader(500)
			reply(map[string]string{"error": err.Error()})
			return
		}
		f.jobs++
		reply(apiJob{ID: fmt.Sprint(f.jobs), Status: "running"})
	case r.Method == "GET" && len(parts) == 3 && parts[1] == "jobs":
		reply(apiJob{ID: parts[2], Status: "succeeded"})
	case len(parts) >= 4 && parts[1] == "agents":
		a := f.agents[parts[3]]
		if a == nil {
			w.WriteHeader(404)
			reply(map[string]string{"error": "no such agent"})
			return
		}
		switch {
		case r.Method == "GET" && len(parts) == 4:
			reply(apiAgent{Project: parts[2], Name: parts[3], State: "running", Worktree: filepath.Join(f.root, "agents", parts[3], "worktree")})
		case r.Method == "GET" && parts[4] == "chat":
			reply(a.thread)
		case r.Method == "DELETE":
			delete(f.agents, parts[3])
			_ = os.RemoveAll(filepath.Join(f.root, "agents", parts[3]))
			reply(nil)
		}
	default:
		w.WriteHeader(404)
		reply(map[string]string{"error": "no route " + r.Method + " " + r.URL.Path})
	}
}

func (f *fakeDaemon) create(req createRequest) error {
	dir := filepath.Join(f.root, "agents", req.Name)
	wt := filepath.Join(dir, "worktree")
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0o755); err != nil {
		return err
	}
	src, _ := f.project["root"].(string)
	if out, err := exec.Command("git", "clone", "-q", src, wt).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", wt, "checkout", "-q", req.From).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	a := &fakeAgent{req: req}
	a.thread.Session.State = "ready"
	f.agents[req.Name] = a
	f.made = append(f.made, req.Name)
	if req.Task != "" {
		a.thread.Session.State = "running"
		a.thread.Items = []chatItem{{ID: "u1", Kind: "user", Text: req.Task, CreatedAt: time.Now()}}
		go f.turn(a, wt, filepath.Join(dir, "home"))
	}
	return nil
}

func (f *fakeDaemon) turn(a *fakeAgent, wt, home string) {
	time.Sleep(150 * time.Millisecond) // the adapter starting
	cmd := exec.Command(filepath.Join(f.root, "bin", "claude"), "-p", a.req.Task)
	cmd.Dir = wt
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	f.mu.Lock()
	defer f.mu.Unlock()
	a.thread.Session.State = "ready"
	if err != nil {
		f.t.Errorf("the fake agent's claude: %v: %s", err, out)
	}
	a.thread.Items = append(a.thread.Items, chatItem{ID: "a1", Kind: "assistant", Text: "Done.", CreatedAt: time.Now()})
	a.thread.Items[0].Result = &turnResult{State: "completed", StopReason: "end_turn", EndedAt: time.Now()}
}

// TestEndToEnd runs the whole benchmark, every mode at two load levels,
// against the fakes, and checks the runs, the cleanup and the report.
func TestEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"claude": fakeClaude, "agentbox": fakeAgentbox} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKE_ROOT", root)
	f := &fakeDaemon{t: t, root: root, agents: map[string]*fakeAgent{}}
	sock := filepath.Join(root, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.serve)}
	go srv.Serve(ln)
	defer srv.Close()

	// The benchmark's dir must be under HOME, as AgentBox's VM only sees that.
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "agentbox-bench")
	out := filepath.Join(root, "results")
	var stdout, stderr bytes.Buffer
	err = run([]string{
		"-dir", dir, "-out", out, "-tasks", "edit", "-runs", "2", "-load", "0,1",
		"-claude", filepath.Join(bin, "claude"), "-agentbox", filepath.Join(bin, "agentbox"), "-socket", sock,
		"-poll", "50ms", "-load-warmup", "0s", "-model", "claude-test", "-effort", "low",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr.String())
	}

	if f.patched["finishNotices"] != "off" || f.patched["agentPRs"] != false || f.patched["consolidation"] != float64(0) {
		t.Errorf("project settings = %v", f.patched)
	}
	if len(f.agents) != 0 {
		t.Errorf("agents left behind: %v", f.agents)
	}
	want := []string{"bench-exec", "bench-edit-l0-r1", "bench-edit-l0-r2", "bench-load-1", "bench-edit-l1-r1", "bench-edit-l1-r2"}
	for _, n := range want {
		if !strings.Contains(strings.Join(f.made, " "), n) {
			t.Errorf("never made %s (made %v)", n, f.made)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "load.log")); !strings.Contains(string(b), "bench-speed/bench-load-1 setsid -f sh -c") {
		t.Errorf("the load wasn't started: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "claude-args")); !strings.Contains(string(b), "--model claude-test --effort low --dangerously-skip-permissions --output-format stream-json --verbose --include-hook-events") {
		t.Errorf("claude was run as %q", b)
	}

	var s summary
	if b, err := os.ReadFile(filepath.Join(out, "summary.json")); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s.Commit != fixtureCommit {
		t.Errorf("commit %s, want %s", s.Commit, fixtureCommit)
	}
	if len(s.Cells) != 6 {
		t.Fatalf("%d cells, want 6:\n%s", len(s.Cells), stdout.String())
	}
	for _, c := range s.Cells {
		if c.Runs != 2 || c.OK != 2 || c.Fails != 0 {
			t.Errorf("%s load %d: %d runs, %d ok, %d failed\n%s", c.Mode, c.Load, c.Runs, c.OK, c.Fails, stderr.String())
		}
		if c.Wall.Median < 600 || c.Turns.Median != 3 || c.ToolCalls.Median != 2 || c.BuildTool.Median < 150 {
			t.Errorf("%s load %d: wall %v, %v model calls, %v tool calls, build %v", c.Mode, c.Load, c.Wall.Median, c.Turns.Median, c.ToolCalls.Median, c.BuildTool.Median)
		}
		if c.Mode != modeAgent && c.Hook.Median < 150 {
			t.Errorf("%s load %d: hooks %v ms, want the 200 ms one", c.Mode, c.Load, c.Hook.Median)
		}
		if c.Mode == modeAgent && (c.Setup.Median <= 0 || c.Startup.Median < 100) {
			t.Errorf("agent load %d: setup %v, startup %v", c.Load, c.Setup.Median, c.Startup.Median)
		}
		if len(c.CPUs) == 0 {
			t.Errorf("%s load %d: no CPU count seen", c.Mode, c.Load)
		}
	}
	rtts := 0
	for _, p := range s.Probes {
		if p.Kind == "exec-rtt" {
			rtts += p.Runs - p.Fails
		}
	}
	if rtts != 10 {
		t.Errorf("%d exec round trips, want 5 per load", rtts)
	}
	for _, f := range []string{"runs.jsonl", "report.md", "config.json", "raw/edit-l0-host-r1/stream.jsonl", "raw/edit-l1-agent-r2/transcript.jsonl", "raw/edit-l1-agent-r2/chat.json"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("no %s: %v", f, err)
		}
	}
	if !strings.Contains(stdout.String(), "| edit | 1 | agent | 2/2 |") {
		t.Errorf("the report printed lacks the agent row:\n%s", stdout.String())
	}

	t.Logf("the report:\n%s", stdout.String())

	// The report again, worked out anew from the raw files.
	var again bytes.Buffer
	if err := run([]string{"report", out}, &again, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again.String(), "| edit | 1 | agent | 2/2 |") {
		t.Errorf("the report written again lacks the agent row:\n%s", again.String())
	}
	var s2 summary
	if b, err := os.ReadFile(filepath.Join(out, "summary.json")); err == nil {
		_ = json.Unmarshal(b, &s2)
	}
	for i, c := range s2.Cells {
		if c.Wall != s.Cells[i].Wall || c.Model.Median != s.Cells[i].Model.Median || c.Startup.Median != s.Cells[i].Startup.Median {
			t.Errorf("%s load %d worked out anew: wall %v model %v startup %v, at the time %v %v %v", c.Mode, c.Load,
				c.Wall.Median, c.Model.Median, c.Startup.Median, s.Cells[i].Wall.Median, s.Cells[i].Model.Median, s.Cells[i].Startup.Median)
		}
	}

	// A second run finds the fixture and the project there already.
	stderr.Reset()
	if err := run([]string{
		"-dir", dir, "-out", filepath.Join(root, "results2"), "-tasks", "edit", "-modes", "exec", "-runs", "1",
		"-agentbox", filepath.Join(bin, "agentbox"), "-socket", sock, "-poll", "50ms", "-model", "claude-test",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("a second run: %v\n%s", err, stderr.String())
	}
	if f.adds != 1 {
		t.Errorf("the project was added %d times, want once", f.adds)
	}
	if strings.Contains(stderr.String(), "failed") {
		t.Errorf("the second run:\n%s", stderr.String())
	}
}

func TestDryRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	var stdout, stderr bytes.Buffer
	err := run([]string{"-dry-run", "-dir", filepath.Join(root, "bench"), "-load", "0,2", "-claude", "true",
		"-agentbox", "true", "-socket", filepath.Join(root, "nothing.sock")}, &stdout, &stderr)
	out := stdout.String()
	if err == nil || !strings.Contains(out, "can't reach AgentBox's daemon") {
		t.Errorf("a dry run with no daemon should say so: %v\n%s", err, out)
	}
	for _, want := range []string{
		"at the pinned commit " + fixtureCommit,
		"create bench-load-1…2 (ai none)",
		`"name":"bench-exec"`,
		"bench-speed/bench-exec -- claude -p",
		"36 model runs in all",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "bench")); err == nil {
		t.Error("a dry run wrote into -dir")
	}
}

// The prompt has to reach claude whole through agentbox exec and a shell.
func TestShellJoin(t *testing.T) {
	args := []string{"-p", tasks["edit"].Prompt + " It's `quoted` and $HOME stays.", "--model", "claude-opus-5-5", "x y"}
	out, err := exec.Command("bash", "-c", `printf '%s\n' `+shellJoin(args)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"); strings.Join(got, "|") != strings.Join(args, "|") {
		t.Errorf("got %q", got)
	}
}

func TestCleanEnv(t *testing.T) {
	got := cleanEnv([]string{"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_EFFORT=max", "CLAUDE_CODE_OAUTH_TOKEN=t",
		"CLAUDE_CONFIG_DIR=/c", "CLAUDE_CODE_USE_BEDROCK=1", "ANTHROPIC_API_KEY=k", "PATH=/bin"})
	if strings.Join(got, " ") != "CLAUDE_CODE_OAUTH_TOKEN=t CLAUDE_CONFIG_DIR=/c CLAUDE_CODE_USE_BEDROCK=1 ANTHROPIC_API_KEY=k PATH=/bin" {
		t.Errorf("got %v", got)
	}
}

func TestTurnDone(t *testing.T) {
	ended := func(bg ...string) *turnResult { return &turnResult{State: "completed", Background: bg} }
	thread := func(state string, items ...chatItem) chatThread {
		var t chatThread
		t.Session.State = state
		t.Items = items
		return t
	}
	for _, c := range []struct {
		name   string
		t      chatThread
		done   bool
		failed bool
	}{
		{"no turn yet", thread("ready"), false, false},
		{"starting", thread("starting", chatItem{Kind: "user"}), false, false},
		{"running", thread("running", chatItem{Kind: "user"}), false, false},
		{"ended", thread("ready", chatItem{Kind: "user", Result: ended()}), true, false},
		{"ended but left work running", thread("ready", chatItem{Kind: "user", Result: ended("go test")}), false, false},
		{"woken again", thread("ready", chatItem{Kind: "user", Result: ended()}, chatItem{Kind: "user", Woken: true}), false, false},
		{"error", thread("error", chatItem{Kind: "user"}), true, true},
		{"asking", thread("waiting", chatItem{Kind: "user"}), true, true},
	} {
		done, failed := turnDone(c.t)
		if done != c.done || (failed != "") != c.failed {
			t.Errorf("%s: done %v, failed %q", c.name, done, failed)
		}
	}
}

func TestChatBreakdown(t *testing.T) {
	var th chatThread
	th.Items = []chatItem{
		{Kind: "user", CreatedAt: at(0)},
		{Kind: "assistant", CreatedAt: at(2)},
		{Kind: "tool", CreatedAt: at(3), UpdatedAt: at(13), Tool: &chatTool{Name: "Bash", Command: "./check.sh"}},
	}
	b := chatBreakdown(th, at(0), at(20))
	near(t, "tool", b.ToolMS, 10000)
	near(t, "build", b.BuildToolMS, 10000)
	near(t, "model", b.ModelMS, 10000)
	if b.ToolCalls != 1 || b.Tools["Bash"].Calls != 1 {
		t.Errorf("got %+v", b)
	}
}
