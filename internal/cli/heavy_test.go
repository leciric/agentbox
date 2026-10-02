package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
)

// fakeBurst is the in-agent API's lease endpoints, granting or not.
type fakeBurst struct {
	mu       sync.Mutex
	grant    bool
	acquired []api.BurstRequest
	released []string
}

func (f *fakeBurst) AcquireBurst(_ context.Context, req api.BurstRequest) (api.BurstLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquired = append(f.acquired, req)
	if !f.grant {
		return api.BurstLease{Why: "it needs 3 GB of the VM's memory; 2 other agents' tests and builds hold 9 GB and 1 GB is free"}, nil
	}
	return api.BurstLease{Granted: true, Bytes: 3 << 30, Env: map[string]string{"GOFLAGS": "-p=4", "VITEST_MAX_THREADS": "4"}}, nil
}

func (f *fakeBurst) ReleaseBurst(_ context.Context, key string) (api.BurstLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, key)
	return api.BurstLease{}, nil
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

func hook(t *testing.T, f *fakeBurst, envFile, call string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	code := heavyHook(context.Background(), strings.NewReader(call), &stderr, f, envFile)
	return code, stderr.String()
}

// The hook says nothing when it isn't needed or gets its lease, and one line,
// blocking the tool, only when the wait runs out.
func TestHeavyHook(t *testing.T) {
	t.Parallel()
	envFile := filepath.Join(t.TempDir(), ".cache", "agentbox", "heavy.env")
	f := &fakeBurst{grant: true}

	if code, out := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"git diff"}}`); code != 0 || out != "" || len(f.acquired) != 0 {
		t.Fatalf("a light command: exit %d, %q, %d leases", code, out, len(f.acquired))
	}
	code, out := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"go test ./..."}}`)
	if code != 0 || out != "" || len(f.acquired) != 1 || f.acquired[0].Key != "bash-t2" {
		t.Fatalf("a test run: exit %d, %q, leases %+v", code, out, f.acquired)
	}
	env, _ := os.ReadFile(envFile)
	if !strings.Contains(string(env), `export GOFLAGS="${GOFLAGS:+$GOFLAGS }-p=4"`) || !strings.Contains(string(env), `export VITEST_MAX_THREADS="4"`) {
		t.Errorf("the env file = %q", env)
	}
	// The file is a script bash reads.
	if _, err := exec.LookPath("bash"); err == nil {
		cmd := exec.Command("bash", "-c", `echo "$GOFLAGS"`)
		cmd.Env = append(os.Environ(), "BASH_ENV="+envFile, "GOFLAGS=-mod=mod")
		got, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(got)) != "-mod=mod -p=4" {
			t.Errorf("bash with BASH_ENV: GOFLAGS = %q, %v", got, err)
		}
	}
	if code, out := hook(t, f, envFile, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"go test ./..."}}`); code != 0 || out != "" || len(f.released) != 1 || f.released[0] != "bash-t2" {
		t.Fatalf("after: exit %d, %q, released %v", code, out, f.released)
	}
	if env, _ := os.ReadFile(envFile); len(env) != 0 {
		t.Errorf("with no lease, the env file = %q", env)
	}

	// The browser holds one key, which stays after each call.
	if code, _ := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"mcp__playwright__browser_navigate","tool_use_id":"t3"}`); code != 0 || f.acquired[1].Key != browserKey {
		t.Fatalf("the browser: exit %d, leases %+v", code, f.acquired)
	}
	hook(t, f, envFile, `{"hook_event_name":"PostToolUse","tool_name":"mcp__playwright__browser_navigate","tool_use_id":"t3"}`)
	if len(f.released) != 1 {
		t.Error("a browser call gave its lease back")
	}

	f.grant = false
	code, out = hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t4","tool_input":{"command":"npm test"}}`)
	if code != 2 || strings.Count(out, "\n") != 1 || !strings.Contains(out, "2 other agents' tests and builds hold 9 GB") {
		t.Errorf("a wait that ran out: exit %d, %q; want 2 and one line", code, out)
	}
	if code, out := hook(t, f, envFile, `not json`); code != 0 || out != "" {
		t.Errorf("garbage: exit %d, %q", code, out)
	}
}

func TestRunHeavy(t *testing.T) {
	t.Parallel()
	f := &fakeBurst{grant: true}
	out := filepath.Join(t.TempDir(), "out")
	var stderr bytes.Buffer
	code, err := runHeavy(context.Background(), f, []string{"--", "sh", "-c", `echo "$VITEST_MAX_THREADS" > ` + out + `; exit 3`}, &stderr)
	if err != nil || code != 3 {
		t.Fatalf("exit %d, %v; want the command's 3", code, err)
	}
	if got, _ := os.ReadFile(out); strings.TrimSpace(string(got)) != "4" {
		t.Errorf("the command ran with VITEST_MAX_THREADS=%q", got)
	}
	if len(f.released) != 1 || f.released[0] != f.acquired[0].Key || stderr.Len() != 0 {
		t.Errorf("released %v of %+v, said %q", f.released, f.acquired, stderr.String())
	}

	f = &fakeBurst{}
	if _, err := runHeavy(context.Background(), f, []string{"true"}, &stderr); err == nil || !strings.Contains(stderr.String(), "waiting for memory") {
		t.Errorf("no room: %v, said %q", err, stderr.String())
	}
	// Outside an agent it just runs.
	if code, err := runHeavy(context.Background(), nil, []string{"true"}, &stderr); code != 0 || err != nil {
		t.Errorf("outside an agent: %d, %v", code, err)
	}
}

func TestWithBurstEnv(t *testing.T) {
	t.Parallel()
	got := withBurstEnv([]string{"PATH=/bin", "GOFLAGS=-mod=mod"}, map[string]string{"GOFLAGS": "-p=2", "CARGO_BUILD_JOBS": "2"})
	want := []string{"PATH=/bin", "GOFLAGS=-mod=mod -p=2", "CARGO_BUILD_JOBS=2"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("env = %v, want %v", got, want)
	}
}
