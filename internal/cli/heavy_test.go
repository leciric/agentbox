package cli

import (
	"bytes"
	"context"
	"os"
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
	policy api.TestPolicy
}

func (f *fakeHeavy) TestPolicy(context.Context) (api.TestPolicy, error) { return f.policy, nil }

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
		"go test ./internal/...":                           true,
		"cd x && go build -o bin/agentbox ./cmd/ab":        true,
		"npm --prefix desktop test":                        true,
		"pnpm run build":                                   true,
		"npx vitest run src":                               true,
		"./gradlew assembleDebug":                          true,
		"cargo test -p core":                               true,
		"cargo +nightly miri test":                         true,
		"cargo +1.80.0 build --release":                    true,
		"cargo -q build":                                   true,
		"cargo --locked test":                              true,
		"cargo --manifest-path x/Cargo.toml build":         true,
		"cargo --manifest-path=x/Cargo.toml build":         true,
		"cargo -C sub -Z unstable-options nextest run":     true,
		"cargo nextest run":                                true,
		"cargo install --path .":                           true,
		"cargo xtask dist":                                 true,
		"cargo doc --no-deps":                              true,
		"cargo miri test":                                  true,
		"cross build --target aarch64-unknown-linux-gnu":   true,
		"cd x && rustc main.rs":                            true,
		"just test":                                        true,
		"RUSTFLAGS=-Dwarnings cargo clippy":                true,
		"cargo fmt":                                        false,
		"cargo --version":                                  false,
		"cargo +nightly fmt":                               false,
		"cargo -q":                                         false,
		"echo cargo build":                                 false,
		"echo just rustc":                                  false,
		"cat <<EOF\ncargo build\njust test\nEOF":           false,
		`git commit -m "cargo build"`:                      false,
		"git commit -m 'run just now'":                     false,
		"docker compose up -d":                             true,
		"(make -j8)":                                       true,
		"git status":                                       false,
		"ls -la":                                           false,
		"go vet ./...":                                     false,
		"cat makefile":                                     false,
		"npm install":                                      false,
		"gofmt -l internal":                                false,
		"sudo make install":                                true,
		"CGO_ENABLED=0 go build ./cmd/agentbox":            true,
		"timeout 600 npx vp test run --maxWorkers=2":       true,
		"bash -c 'go test ./...'":                          true,
		"agentbox heavy -- pytest -x":                      true,
		"ls\nnpm test":                                     true,
		"cat <<EOF > notes.md\nmake sure\nEOF\nmake build": true,
		`git commit -m "fix: make the tsc build pass"`:     false,
		"npx vp lint":                                      false,
		"echo jest":                                        false,
		"grep -rn 'go test' .":                             false,
		"cat <<< \"make\"":                                 false,
		// 2026-10-12: a script fed to python3 on stdin, which says "make".
		"python3 - <<'EOF'\nimport re\n# make sure the file ends in a newline\ns = open('a').read()\nEOF": false,
		"cat > x.sh <<-END\n\tmake all\n\tEND\nchmod +x x.sh":                                             false,
		// 2026-10-21: dev servers and a build script run without npm, which
		// filled the VM unseen.
		"vite":       true,
		"vite build": true,
		"./node_modules/.bin/vite --host 0.0.0.0":    true,
		"cd desktop && vite --config vite.config.ts": true,
		"node scripts/build.mjs":                     true,
		"node --max-old-space-size=4096 build.js":    true,
		"node build-renderer.cjs":                    true,
		"vite --version":                             false,
		"vite-node x.ts":                             false,
		"cat vite.config.ts":                         false,
		"node scripts/rebuild.js":                    false,
		"node check-build.js":                        false,
		"node -e 'console.log(1)'":                   false,
	} {
		if got := isHeavy(cmd); got != heavy {
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

// The hook asks nothing for a heavy command any more, the shims do, for
// every tool alike; it says nothing, and empties a join line an earlier hook
// left.
func TestHeavyHook(t *testing.T) {
	t.Parallel()
	envFile := filepath.Join(t.TempDir(), ".cache", "agentbox", "heavy.env")
	f := &fakeHeavy{start: true}
	for _, call := range []string{
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"git diff"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"go test ./..."}}`,
		`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"go test ./..."}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"mcp__playwright__browser_navigate","tool_use_id":"t3"}`,
	} {
		if code, out := hook(t, f, envFile, call); code != 0 || out != "" {
			t.Fatalf("%s: exit %d, %q", call, code, out)
		}
	}
	if len(f.asked)+len(f.ended) != 0 {
		t.Errorf("asked %+v, ended %v: want nothing", f.asked, f.ended)
	}
	writeJoin(envFile, "bash-old")
	if code, _ := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t4","tool_input":{"command":"ls"}}`); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if env, _ := os.ReadFile(envFile); len(env) != 0 {
		t.Errorf("an earlier hook's join line stayed: %q", env)
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
	if strings.Count(stderr.String(), "waits for memory") != 1 || !strings.Contains(stderr.String(), "2 commands are ahead") {
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
