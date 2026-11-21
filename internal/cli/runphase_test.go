package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/mcp"
)

func TestRunPhaseCommand(t *testing.T) {
	t.Parallel()
	for cmd, run := range map[string]bool{
		"npm run dev":                           true,
		"npm --prefix desktop start":            true,
		"pnpm dev":                              true,
		"yarn preview --port 4173":              true,
		"npm run e2e":                           true,
		"npx playwright test tests/login.spec":  true,
		"playwright test":                       true,
		"npx electron .":                        true,
		"cd desktop && electron .":              true,
		"npx vite":                              true,
		"vite --host 0.0.0.0":                   true,
		"npx next dev":                          true,
		"docker compose up -d":                  true,
		"docker compose -f dev.yml up":          true,
		"docker-compose up":                     true,
		"docker run --rm -p 5432:5432 postgres": true,
		"FOO=1 nohup npm run dev &":             true,
		"npx cypress run":                       true,
		// The code phase: builds, unit tests, tools.
		"npm run build":                   false,
		"npm test":                        false,
		"npx vitest run src":              false,
		"vite build":                      false,
		"npx electron-builder --linux":    false,
		"npx playwright install chromium": false,
		"docker compose build":            false,
		"docker build -t x .":             false,
		"docker ps":                       false,
		"go test ./internal/runslots":     false,
		"cargo run -p codegen":            false,
		"go run ./cmd/gen":                false,
		"npm run devtools":                false,
		`git commit -m "npm run dev"`:     false,
		"echo docker compose up":          false,
		"python3 - <<'EOF'\nimport os  # npm run dev\nEOF": false,
	} {
		if got := isRunPhase(cmd); got != run {
			t.Errorf("isRunPhase(%q) = %v, want %v", cmd, got, run)
		}
	}
	for cmd, test := range map[string]bool{
		"npx playwright test": true, "npm run e2e": true, "npx cypress run": true,
		"npm run dev": false, "docker compose up": false,
	} {
		if got := isRunTest(cmd); got != test {
			t.Errorf("isRunTest(%q) = %v, want %v", cmd, got, test)
		}
	}
}

// fakeRun is the in-agent API with run slots: its heavy endpoints, and a
// run slot granted after the answers in waits.
type fakeRun struct {
	fakeHeavy
	waits   []int // the ahead counts answered before it is granted
	asked   []api.RunUpRequest
	runJoin []int
	upErr   error
}

func (f *fakeRun) RunUp(_ context.Context, req api.RunUpRequest) (api.RunPhase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, req)
	if f.upErr != nil {
		return api.RunPhase{}, f.upErr
	}
	if len(f.waits) > 0 {
		ahead := f.waits[0]
		f.waits = f.waits[1:]
		if req.Try {
			return api.RunPhase{Ahead: ahead}, nil
		}
		return api.RunPhase{State: api.RunSlotWaiting, Ahead: ahead}, nil
	}
	return api.RunPhase{State: api.RunSlotHolding, Key: "slot-1"}, nil
}

func (f *fakeRun) RunStatus(context.Context) (api.RunPhase, error) { return api.RunPhase{}, nil }

func (f *fakeRun) RunJoin(_ context.Context, pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runJoin = append(f.runJoin, pid)
	return nil
}

func (f *fakeRun) RunDown(context.Context) (api.RunPhase, error) { return api.RunPhase{}, nil }

// A run phase's command waits for a run slot, saying where it waits as
// that changes, then runs in the run phase's cgroup rather than one of its
// own; the daemon still counts it as a heavy command.
func TestRunPhaseCommandWaitsForASlotAndJoinsIt(t *testing.T) {
	t.Parallel()
	f := &fakeRun{fakeHeavy: fakeHeavy{start: true}, waits: []int{2, 2, 1}}
	var stderr bytes.Buffer
	run := heavyCommandRun{path: "true", argv: []string{"true"}, command: "npm run dev", run: true}
	if code, err := runHeavyCommand(context.Background(), f, run, &stderr); code != 0 || err != nil {
		t.Fatalf("exit %d, %v", code, err)
	}
	out := stderr.String()
	if strings.Count(out, "(2 ahead)") != 1 || strings.Count(out, "(1 ahead)") != 1 || !strings.Contains(out, "you have a run slot now") {
		t.Errorf("stderr %q", out)
	}
	if len(f.asked) != 4 || f.asked[0].WaitSeconds != runWaitSeconds || f.asked[0].Command != "npm run dev" {
		t.Errorf("asked %+v", f.asked)
	}
	if len(f.runJoin) != 1 || f.runJoin[0] != os.Getpid() || len(f.joined) != 0 {
		t.Errorf("joined the run phase %v and a heavy cgroup %v, want only the run phase", f.runJoin, f.joined)
	}
	if len(f.fakeHeavy.asked) != 1 || len(f.ended) != 1 {
		t.Errorf("heavy asked %+v, ended %v", f.fakeHeavy.asked, f.ended)
	}
	// A build is the code phase: it asks for no slot.
	f2 := &fakeRun{fakeHeavy: fakeHeavy{start: true}}
	build := heavyCommandRun{path: "true", argv: []string{"true"}, command: "npm run build"}
	if code, err := runHeavyCommand(context.Background(), f2, build, &stderr); code != 0 || err != nil || len(f2.asked) != 0 || len(f2.joined) != 1 {
		t.Errorf("a build: exit %d, %v, asked %+v, joined %v", code, err, f2.asked, f2.joined)
	}
}

// On a daemon from before run slots, a run phase's command runs at once,
// and says nothing.
func TestRunPhaseCommandOnAnOlderDaemon(t *testing.T) {
	t.Parallel()
	f := &fakeRun{fakeHeavy: fakeHeavy{start: true}, upErr: &api.StatusError{Code: 403, Message: "not available inside an agent"}}
	var stderr bytes.Buffer
	run := heavyCommandRun{path: "true", argv: []string{"true"}, command: "npm run dev", run: true}
	if code, err := runHeavyCommand(context.Background(), f, run, &stderr); code != 0 || err != nil {
		t.Fatalf("exit %d, %v", code, err)
	}
	if stderr.Len() != 0 || len(f.joined) != 1 || len(f.runJoin) != 0 {
		t.Errorf("stderr %q, joined %v, run joined %v", stderr.String(), f.joined, f.runJoin)
	}
}

// The desktop's tools need a run slot: one held is renewed at most every
// runBeat, and with none free the call is refused, saying what to do.
func TestDesktopToolsNeedARunSlot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	f := &fakeRun{waits: []int{3}}
	g := &runSlotGate{c: f, now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }}
	err := g.check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "3 agents are waiting") || !strings.Contains(err.Error(), "agentbox run up") {
		t.Fatalf("with no slot free: %v", err)
	}
	if err := g.check(context.Background()); err != nil {
		t.Fatalf("with one free: %v", err)
	}
	if !f.asked[1].Try || !f.asked[1].Browser {
		t.Errorf("asked %+v, want a try for the browser", f.asked[1])
	}
	mu.Lock()
	now = now.Add(10 * time.Second)
	mu.Unlock()
	_ = g.check(context.Background())
	if len(f.asked) != 2 {
		t.Errorf("asked %d times within %s", len(f.asked), runBeat)
	}
	mu.Lock()
	now = now.Add(runBeat)
	mu.Unlock()
	_ = g.check(context.Background())
	if len(f.asked) != 3 {
		t.Errorf("asked %d times, want it renewed after %s", len(f.asked), runBeat)
	}
	// No daemon to ask: the tool runs.
	g = &runSlotGate{c: &fakeRun{upErr: &api.StatusError{Code: 500}}, now: time.Now}
	if err := g.check(context.Background()); err != nil {
		t.Errorf("with no daemon: %v", err)
	}
	// Each tool asks before it runs.
	ran := 0
	tools := needingRunSlot(desktopTestTools(&ran), &runSlotGate{c: &fakeRun{waits: []int{1}}, now: time.Now})
	if _, err := tools[0].Run(nil); err == nil || ran != 0 {
		t.Errorf("a tool ran without a slot: %v, ran %d", err, ran)
	}
	if _, err := tools[0].Run(nil); err != nil || ran != 1 {
		t.Errorf("a tool with a slot: %v, ran %d", err, ran)
	}
}

func TestRunEnded(t *testing.T) {
	t.Parallel()
	got := runEnded(api.RunPhase{Stopped: []string{"2 containers started since `agentbox run up`", "the browser"}, Peak: 3 << 30})
	if !strings.Contains(got, "stopped 2 containers") || !strings.Contains(got, "the browser") || !strings.Contains(got, "3.0 GiB") {
		t.Errorf("runEnded = %q", got)
	}
	if got := runEnded(api.RunPhase{}); got != "Run phase ended." {
		t.Errorf("runEnded with nothing = %q", got)
	}
}

func desktopTestTools(ran *int) []mcp.Tool {
	return []mcp.Tool{{Name: "screenshot", Run: func(json.RawMessage) (string, error) { *ran++; return "ok", nil }}}
}
