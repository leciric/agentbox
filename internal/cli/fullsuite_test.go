package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// The commands that run a whole test suite, and those that don't: a part
// of one, a commit message or an echo naming one, or the override.
func TestFullSuite(t *testing.T) {
	t.Parallel()
	whole := map[string]string{
		"go test ./...":                                  "go",
		"go test -race -count=1 ./...":                   "go",
		"go test -tags integration ./...":                "go",
		"go -C . test ./...":                             "go",
		"go test all":                                    "go",
		"cd /repo && go test ./... 2>&1 | tail -20":      "go",
		"agentbox heavy -- go test ./...":                "go",
		"timeout 600 go test ./... > /t/out.log 2>&1":    "go",
		"GOFLAGS=-count=1 go test ./...":                 "go",
		"bash -c 'go test ./...'":                        "go",
		"git add -A && go test ./... && git commit -m x": "go",
		"npm test":                         "npm",
		"npm t":                            "npm",
		"npm run test":                     "npm",
		"npm --prefix desktop test":        "npm",
		"npm test -- --coverage":           "npm",
		"pnpm test":                        "npm",
		"pnpm -r test":                     "pnpm",
		"pnpm --recursive run test":        "pnpm",
		"yarn test":                        "npm",
		"yarn workspaces foreach run test": "pnpm",
		"bun test":                         "bun",
		"npm test 2>&1 | tail -5":          "npm",
		"vitest":                           "vitest",
		"vitest run":                       "vitest",
		"npx vitest run --reporter dot":    "vitest",
		"pnpm exec vitest run":             "vitest",
		"pnpm vitest run":                  "vitest",
		"./node_modules/.bin/vitest run":   "vitest",
		"jest":                             "jest",
		"npx jest --ci":                    "jest",
		"pytest":                           "pytest",
		"pytest -x -q":                     "pytest",
		"python -m pytest":                 "pytest",
		"uv run pytest -n 4":               "pytest",
		"cargo test --workspace":           "cargo",
		"cargo test":                       "cargo",
		"cargo nextest run":                "cargo",
		"turbo run test":                   "turbo",
		"npx turbo test":                   "turbo",
		"nx run-many -t test":              "nx",
		"lerna run test":                   "lerna",
		"make test":                        "make",
		"make -j4 check":                   "make",
		"just test":                        "make",
		"./gradlew test":                   "gradle",
		"mvn -q verify":                    "mvn",
		"bazel test //...":                 "bazel",
		"npx playwright test":              "playwright",
		"sudo -E go test ./...":            "go",
	}
	for cmd, runner := range whole {
		run, ok := fullSuite(cmd, "")
		if !ok || run.Runner != runner {
			t.Errorf("%q: %v, runner %q; want a whole %s suite", cmd, ok, run.Runner, runner)
		}
	}
	parts := []string{
		"go test ./internal/cli",
		"go test ./internal/...",
		"go test",
		"go test -run TestFullSuite ./...",
		"go test ./internal/cli ./internal/brief",
		"go build ./...",
		"go vet ./...",
		"npm test -- src/a.test.ts",
		"npm run test:unit",
		"npm run build",
		"npm install",
		"pnpm --filter web test -- src/x.test.ts",
		"pnpm -r --filter ...[origin/main] test",
		"yarn workspaces foreach --since run test",
		"bun test src/a.test.ts",
		"vitest related --run src/a.ts",
		"vitest run src/a.test.ts",
		"vitest run --changed",
		"npx vitest bench",
		"jest --findRelatedTests src/a.ts",
		"jest -o",
		"jest src/a.test.ts",
		"pytest tests/test_a.py",
		"pytest -k parse",
		"pytest --lf",
		"cargo test -p core",
		"cargo test parse_",
		"cargo build",
		"turbo run test --affected",
		"turbo run test --filter=web",
		"turbo run build",
		"nx affected -t test",
		"nx test web",
		"lerna run test --since origin/main",
		"make build",
		"make",
		"./gradlew test --tests com.x.FooTest",
		"mvn test -Dtest=FooTest",
		"mvn -pl core test",
		"bazel test //pkg:all",
		"npx playwright test e2e/a.spec.ts",
		`git commit -m "go test ./..."`,
		"echo go test ./...",
		"grep -rn 'npm test' .",
		"# go test ./...",
		"rg 'pytest' docs",
		"AGENTBOX_FULL_SUITE=1 go test ./...",
		"export AGENTBOX_FULL_SUITE=1; npm test",
		"cat <<'EOF'\ngo test ./...\nEOF",
		"ls go test",
	}
	for _, cmd := range parts {
		if run, ok := fullSuite(cmd, ""); ok {
			t.Errorf("%q: a whole %s suite (%q), want not", cmd, run.Runner, run.Command)
		}
	}
}

// Where a command runs counts: ./... below a module's root is a part of it,
// a package's test script says its runner, and cargo test is whole only at
// a workspace's root.
func TestFullSuiteDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "internal/a/a.go", "package a\n")
	write(t, root, "web/package.json", `{"scripts":{"test":"vitest run"}}`)
	write(t, root, "app/package.json", `{"scripts":{"test":"node --test \"src/**/*.test.ts\""}}`)
	write(t, root, "rs/Cargo.toml", "[workspace]\nmembers = [\"core\"]\n")
	write(t, root, "rs/core/Cargo.toml", "[package]\nname = \"core\"\n")

	if _, ok := fullSuite("go test ./...", root); !ok {
		t.Error("go test ./... at the module's root isn't the whole suite")
	}
	if _, ok := fullSuite("cd internal && go test ./...", root); ok {
		t.Error("go test ./... below the module's root is the whole suite")
	}
	if _, ok := fullSuite("go test ./...", filepath.Join(root, "internal")); ok {
		t.Error("go test ./... in a package is the whole suite")
	}
	for cmd, runner := range map[string]string{"npm --prefix web test": "vitest", "cd app && npm test": "node", "npm test": "npm"} {
		if run, ok := fullSuite(cmd, root); !ok || run.Runner != runner {
			t.Errorf("%q: %v %q, want %s", cmd, ok, run.Runner, runner)
		}
	}
	if run, ok := fullSuite("cd web && npm test", root); !ok || run.Dir != filepath.Join(root, "web") {
		t.Errorf("cd web && npm test runs in %q", run.Dir)
	}
	if _, ok := fullSuite("cd rs && cargo test", root); !ok {
		t.Error("cargo test at the workspace's root isn't the whole suite")
	}
	if _, ok := fullSuite("cd rs/core && cargo test", root); ok {
		t.Error("cargo test in a member crate is the whole suite")
	}
}

// The message names what to run instead, from the files changed since the
// agent's base, committed or not, and new ones.
func TestFullSuiteMessage(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "internal/a/a.go", "package a\n")
	write(t, root, "internal/b/b.go", "package b\n")
	write(t, root, "internal/b/testdata/x.golden", "x\n")
	write(t, root, "README.md", "x\n")
	write(t, root, "web/package.json", `{"scripts":{"test":"vitest run"}}`)
	write(t, root, "web/src/a.ts", "export {}\n")
	write(t, root, "app/package.json", `{"scripts":{"test":"node --test"}}`)
	write(t, root, "app/src/c.ts", "export {}\n")
	write(t, root, "app/src/c.test.ts", "export {}\n")
	write(t, root, "py/pkg/parse.py", "x = 1\n")
	write(t, root, "py/tests/test_parse.py", "x = 1\n")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	write(t, root, "internal/a/a.go", "package a\n\nvar X = 1\n")
	git("commit", "-qam", "a")
	write(t, root, "internal/b/testdata/x.golden", "y\n")  // uncommitted
	write(t, root, "internal/c/c.go", "package c\n")       // new
	write(t, root, "README.md", "y\n")                     // no package
	write(t, root, "web/src/a.ts", "export const a = 1\n") // vitest
	write(t, root, "app/src/c.ts", "export const c = 1\n") // node --test
	write(t, root, "py/pkg/parse.py", "x = 2\n")           // pytest

	ctx := context.Background()
	for cmd, want := range map[string]string{
		"go test ./...":         "go test ./internal/a ./internal/b ./internal/c",
		"npm --prefix web test": "cd web && npx vitest related --run src/a.ts",
		"cd web && npx jest":    "cd web && npx jest --findRelatedTests src/a.ts",
		"cd app && npm test":    "cd app && node --test src/c.test.ts",
		"pytest":                "pytest py/tests/test_parse.py",
		"turbo run test":        "turbo run test --affected",
	} {
		run, ok := fullSuite(cmd, root)
		if !ok {
			t.Fatalf("%q isn't a whole suite", cmd)
		}
		msg := fullSuiteMessage(ctx, run, base)
		if !strings.Contains(msg, "\n  "+want+"\n") {
			t.Errorf("%q: the message doesn't suggest %q:\n%s", cmd, want, msg)
		}
		if !strings.Contains(msg, "`AGENTBOX_FULL_SUITE=1 "+run.Command+"`") {
			t.Errorf("%q: the message doesn't say how to run it anyway:\n%s", cmd, msg)
		}
	}
	// With nothing it can work out, it says what to do in words.
	if msg := fullSuiteMessage(ctx, suiteRun{Runner: "make", Command: "make test", Dir: root}, base); !strings.Contains(msg, "by file or package") {
		t.Errorf("make test: %s", msg)
	}
}

// The hook refuses a whole suite only while the daemon says it's blocked,
// and asks the daemon only about a command that is one.
func TestHeavyHookFullSuite(t *testing.T) {
	t.Parallel()
	envFile := filepath.Join(t.TempDir(), "heavy.env")
	f := &fakeHeavy{start: true, policy: api.TestPolicy{BlockFullSuite: true}}
	code, out := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"go test ./..."}}`)
	if code != 2 || !strings.Contains(out, "runs the whole test suite") || len(f.asked) != 0 {
		t.Fatalf("blocked: exit %d, %q, %d asked to start", code, out, len(f.asked))
	}
	if code, out := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"AGENTBOX_FULL_SUITE=1 go test ./..."}}`); code != 0 || out != "" || len(f.asked) != 1 {
		t.Fatalf("asked for by name: exit %d, %q", code, out)
	}
	if code, out := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t3","tool_input":{"command":"go test ./internal/cli"}}`); code != 0 || out != "" {
		t.Fatalf("a part: exit %d, %q", code, out)
	}
	f.policy.BlockFullSuite = false
	if code, out := hook(t, f, envFile, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t4","tool_input":{"command":"npm test"}}`); code != 0 || out != "" {
		t.Fatalf("not blocked: exit %d, %q", code, out)
	}
}

func write(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
