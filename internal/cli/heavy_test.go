package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
)

// fakeHeavy is the in-agent API's heavy-command endpoints, starting them or
// not.
type fakeHeavy struct {
	mu     sync.Mutex
	start  bool
	asked  []api.HeavyRequest
	joined []string // key=pid
	ended  []string
}

func (f *fakeHeavy) StartHeavy(_ context.Context, req api.HeavyRequest) (api.HeavyStart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, req)
	if !f.start {
		return api.HeavyStart{Why: "the VM's memory is under pressure (34% of the last 10 seconds stalled on it), and 2 commands are ahead of it"}, nil
	}
	return api.HeavyStart{Started: true}, nil
}

func (f *fakeHeavy) JoinHeavy(_ context.Context, key string, pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joined = append(f.joined, key+"="+strconv.Itoa(pid))
	return nil
}

func (f *fakeHeavy) EndHeavy(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, key)
	return nil
}

func TestHeavyCommand(t *testing.T) {
	t.Parallel()
	for cmd, heavy := range map[string]bool{
		"go test ./internal/...":                    true,
		"cd x && go build -o bin/agentbox ./cmd/ab": true,
		"npm --prefix desktop test":                 true,
		"pnpm run build":                            true,
		"npx vitest run src":                        true,
		"./gradlew assembleDebug":                   true,
		"cargo test -p core":                        true,
		"docker compose up -d":                      true,
		"(make -j8)":                                true,
		"git status":                                false,
		"ls -la":                                    false,
		"go vet ./...":                              false,
		"cat makefile":                              false,
		"npm install":                               false,
		"gofmt -l internal":                         false,
	} {
		if got := heavyCommand.MatchString(cmd); got != heavy {
			t.Errorf("%q heavy = %v, want %v", cmd, got, heavy)
		}
	}
}

func hook(t *testing.T, f *fakeHeavy, envFile, call string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	code := heavyHook(context.Background(), strings.NewReader(call), &stderr, f, envFile)
	return code, stderr.String()
}

// The hook says nothing when it isn't needed or its command may start, and
// one line, blocking the tool, only when the wait runs out. A command that
// starts leaves its join line for the shell the Bash tool starts next.
func TestHeavyHook(t *testing.T) {
	t.Parallel()
	envFile := filepath.Join(t.TempDir(), ".cache", "agentbox", "heavy.env")
	f := &fakeHeavy{start: true}

	if code, out := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"git diff"}}`); code != 0 || out != "" || len(f.asked) != 0 {
		t.Fatalf("a light command: exit %d, %q, %d asked", code, out, len(f.asked))
	}
	code, out := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"go test ./..."}}`)
	if code != 0 || out != "" || len(f.asked) != 1 || f.asked[0].Key != "bash-t2" || f.asked[0].Command != "go test ./..." {
		t.Fatalf("a test run: exit %d, %q, asked %+v", code, out, f.asked)
	}
	env, _ := os.ReadFile(envFile)
	if string(env) != joinLine("bash-t2") || !strings.Contains(string(env), `heavy-join 'bash-t2' "$$"`) {
		t.Errorf("the env file = %q", env)
	}
	// The command's shell runs it, and what it runs empties the file, so the
	// shells after it don't join too.
	if _, err := exec.LookPath("bash"); err == nil {
		bin := filepath.Join(t.TempDir(), "agentbox")
		script := "#!/bin/sh\necho \"$@\" > " + bin + ".args\n: > " + envFile + "\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		line := strings.Replace(joinLine("bash-t2"), "/usr/local/bin/agentbox", bin, 1)
		if err := os.WriteFile(envFile, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", "-c", `echo $$`)
		cmd.Env = append(os.Environ(), "BASH_ENV="+envFile)
		pid, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		args, _ := os.ReadFile(bin + ".args")
		if got, want := strings.TrimSpace(string(args)), "heavy-join bash-t2 "+strings.TrimSpace(string(pid)); got != want {
			t.Errorf("the shell ran %q, want %q", got, want)
		}
		writeJoin(envFile, "bash-t2")
	}
	if code, out := hook(t, f, envFile, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"go test ./..."}}`); code != 0 || out != "" || len(f.ended) != 1 || f.ended[0] != "bash-t2" {
		t.Fatalf("after: exit %d, %q, ended %v", code, out, f.ended)
	}
	if env, _ := os.ReadFile(envFile); len(env) != 0 {
		t.Errorf("after it ended, the env file = %q", env)
	}

	// The browser isn't a command the hook holds.
	if code, _ := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"mcp__playwright__browser_navigate","tool_use_id":"t3"}`); code != 0 || len(f.asked) != 1 {
		t.Fatalf("the browser: exit %d, asked %+v", code, f.asked)
	}

	f.start = false
	code, out = hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t4","tool_input":{"command":"npm test"}}`)
	if code != 2 || strings.Count(out, "\n") != 1 || !strings.Contains(out, "2 commands are ahead of it") {
		t.Errorf("a wait that ran out: exit %d, %q; want 2 and one line", code, out)
	}
	if code, out := hook(t, f, envFile, `not json`); code != 0 || out != "" {
		t.Errorf("garbage: exit %d, %q", code, out)
	}
}

// heavy-join empties the env file before it joins, and only if the file
// still names its key.
func TestHeavyJoin(t *testing.T) {
	t.Parallel()
	envFile := filepath.Join(t.TempDir(), "heavy.env")
	f := &fakeHeavy{start: true}
	writeJoin(envFile, "bash-new")
	heavyJoin(context.Background(), f, envFile, "bash-old", 41)
	if env, _ := os.ReadFile(envFile); string(env) != joinLine("bash-new") {
		t.Errorf("joining an older key emptied the newer one's line: %q", env)
	}
	heavyJoin(context.Background(), f, envFile, "bash-new", 42)
	if env, _ := os.ReadFile(envFile); len(env) != 0 {
		t.Errorf("after joining, the env file = %q", env)
	}
	if len(f.joined) != 2 || f.joined[1] != "bash-new=42" {
		t.Errorf("joined %v", f.joined)
	}
}

// agentbox heavy waits, saying why once, joins its run, runs the command and
// says it ended; outside an agent it just runs it.
func TestRunHeavy(t *testing.T) {
	t.Parallel()
	f := &fakeHeavy{start: true}
	var stderr bytes.Buffer
	code, err := runHeavy(context.Background(), f, []string{"--", "sh", "-c", "exit 3"}, &stderr)
	if err != nil || code != 3 {
		t.Fatalf("exit %d, %v", code, err)
	}
	if len(f.asked) != 1 || f.asked[0].Command != "sh -c exit 3" || len(f.joined) != 1 || !strings.HasSuffix(f.joined[0], "="+strconv.Itoa(os.Getpid())) || len(f.ended) != 1 {
		t.Errorf("asked %+v, joined %v, ended %v", f.asked, f.joined, f.ended)
	}
	if code, err := runHeavy(context.Background(), nil, []string{"true"}, &stderr); code != 0 || err != nil {
		t.Errorf("outside an agent: exit %d, %v", code, err)
	}
	if _, err := runHeavy(context.Background(), nil, []string{"--"}, &stderr); err == nil {
		t.Error("no command ran without an error")
	}
}

// A command that waits says why once, and starts once it may.
func TestRunHeavyWaitsItsTurn(t *testing.T) {
	t.Parallel()
	f := &fakeHeavy{}
	w := &waitThenStart{fakeHeavy: f, after: 3}
	var stderr bytes.Buffer
	if code, err := runHeavy(context.Background(), w, []string{"true"}, &stderr); code != 0 || err != nil {
		t.Fatalf("exit %d, %v", code, err)
	}
	if strings.Count(stderr.String(), "waiting for memory") != 1 || !strings.Contains(stderr.String(), "2 commands are ahead") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if len(f.asked) != 3 || f.asked[0].Key != f.asked[2].Key {
		t.Errorf("asked %+v: want three asks with one key", f.asked)
	}
}

type waitThenStart struct {
	*fakeHeavy
	after int
}

func (w *waitThenStart) StartHeavy(ctx context.Context, req api.HeavyRequest) (api.HeavyStart, error) {
	out, _ := w.fakeHeavy.StartHeavy(ctx, req)
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.asked) >= w.after {
		return api.HeavyStart{Started: true}, nil
	}
	return out, nil
}
