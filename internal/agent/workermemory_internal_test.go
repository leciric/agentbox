package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A heavy command's tool is what its workers' memory is learned by: the
// subcommand where it decides (go test, cargo build), the tool a package
// runner runs, and a package script by its name, whichever manager runs it.
func TestHeavyTool(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]string{
		"go test ./...":                                      "go test",
		"GOFLAGS=-p=2 go test -race ./...":                   "go test",
		"/usr/local/go/bin/go build ./cmd/x":                 "go build",
		"go vet ./...":                                       "go build",
		"go run ./cmd/server":                                "go run",
		"cargo +nightly test --workspace":                    "cargo test",
		"cargo --manifest-path a/Cargo.toml build --release": "cargo build",
		"cargo clippy":                                       "cargo build",
		"cargo nextest run":                                  "cargo test",
		"cross build --target aarch64":                       "cargo build",
		"npm test":                                           "npm test",
		"npm run test -- --watch=false":                      "npm test",
		"npm --prefix desktop run build":                     "npm build",
		"pnpm --filter web build":                            "npm build",
		"yarn test":                                          "npm test",
		"yarn jest --ci":                                     "jest",
		"pnpm vitest run":                                    "vitest",
		"pnpm exec tsc -p .":                                 "tsc",
		"npm exec vitest":                                    "vitest",
		"npx vitest run":                                     "vitest",
		"npx --yes jest":                                     "jest",
		"bunx playwright test":                               "playwright",
		"vitest run src":                                     "vitest",
		"/w/node_modules/.bin/jest -w 2":                     "jest",
		"tsc --noEmit":                                       "tsc",
		"./gradlew assembleDebug":                            "gradle",
		"agentbox heavy -- ./gradlew test":                   "gradle",
		"gradle build":                                       "gradle",
		"make -C sub all":                                    "make",
		"cmake --build build":                                "make",
		"docker build -t x .":                                "docker build",
		"docker compose up":                                  "docker compose",
		"timeout 60 go test ./internal/agent":                "go test",
		"vite build":                                         "vite build",
		"vite --port 5173":                                   "vite",
		"vp test":                                            "vitest",
		"tox -e py":                                          "pytest",
		"mvn package":                                        "mvn",
		"":                                                   "",
	} {
		if got := HeavyTool(command); got != want {
			t.Errorf("HeavyTool(%q) = %q, want %q", command, got, want)
		}
	}
}

// MemoryWorkers is the share of the cores, no more than free memory holds,
// and at least one; and says which of those made it.
func TestMemoryWorkers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		cpu       int
		free, per int64
		want      int
		limit     string
	}{
		{12, 64 * wGiB, wGiB, 12, LimitCPU},           // room for every core
		{12, 8 * wGiB, wGiB, 8, LimitMemory},          // room for 8
		{12, 8*wGiB + wGiB/2, wGiB, 8, LimitMemory},   // rounded down
		{4, 4 * wGiB, wGiB, 4, LimitCPU},              // exactly the share
		{12, wGiB / 2, wGiB, 1, LimitLeast},           // room for none: one
		{12, 0, wGiB, 1, LimitLeast},                  // none at all
		{12, -5 * wGiB, wGiB, 1, LimitLeast},          // claimed past what there is
		{6, 3 * wGiB, 768 * wMiB, 4, LimitMemory},     // 3 GiB of 768 MiB
		{12, 8 * wGiB, 0, 12, LimitCPU},               // nothing known of a worker
		{0, 8 * wGiB, wGiB, 1, LimitCPU},              // no share said: one
		{2, 1536 * wMiB, 1536 * wMiB, 1, LimitMemory}, // a cargo job
	} {
		n, limit := MemoryWorkers(c.cpu, c.free, c.per)
		if n != c.want || limit != c.limit {
			t.Errorf("MemoryWorkers(%d, %d MiB, %d MiB) = %d %s, want %d %s", c.cpu, c.free>>20, c.per>>20, n, limit, c.want, c.limit)
		}
	}
}

// What one worker takes rises at once to a run that took more, falls a
// quarter of the way to one that took less and succeeded, never under a
// quarter of the tool's default, and a failed run that took less, or one
// that says nothing, teaches nothing.
func TestLearnPerWorker(t *testing.T) {
	t.Parallel()
	def := WorkerDefault("go test") // 1 GiB
	for _, c := range []struct {
		name        string
		known, peak int64
		workers     int
		ok          bool
		want        int64
		changed     bool
	}{
		{"first run, less", 0, 8 * 512 * wMiB, 8, true, def - (def-512*wMiB)/4, true},
		{"first run, more", 0, 8 * 2 * wGiB, 8, true, 2 * wGiB, true},
		{"more, failed", wGiB, 4 * 3 * wGiB, 4, false, 3 * wGiB, true},
		{"less, failed", wGiB, 4 * 100 * wMiB, 4, false, wGiB, false},
		{"less, ok", 2 * wGiB, 2 * wGiB, 2, true, 2*wGiB - wGiB/4, true},
		{"floor", 300 * wMiB, 8 * wMiB, 8, true, def / 4, true},
		{"no peak", wGiB, 0, 8, true, wGiB, false},
		{"no workers", wGiB, wGiB, 0, true, wGiB, false},
		{"absurd", wGiB, 1 << 50, 1, true, workerMost, true},
	} {
		got, changed := LearnPerWorker("go test", c.known, c.peak, c.workers, c.ok)
		if got != c.want || changed != c.changed {
			t.Errorf("%s: LearnPerWorker = %d MiB, %v; want %d MiB, %v", c.name, got>>20, changed, c.want>>20, c.changed)
		}
	}
	if WorkerDefault("npm test") != defaultWorker || WorkerDefault("cargo build") <= WorkerDefault("make") {
		t.Error("defaults: a package script gets the general one, and a cargo job more than a make job")
	}
}

// What a heavy command running will likely still take: its workers at what
// one takes, less what it holds; nothing for a dev server, which holds what
// it needs.
func TestWorkerClaim(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		tool      string
		workers   int
		per, used int64
		want      int64
	}{
		{"go test", 8, wGiB, 2 * wGiB, 6 * wGiB},
		{"go test", 2, wGiB, 3 * wGiB, 0}, // took more than thought
		{"npm dev", 8, wGiB, 0, 0},
		{"vite", 8, wGiB, 0, 0},
		{"docker compose", 8, wGiB, 0, 0},
		{"vitest", 0, wGiB, 0, 0},
	} {
		if got := WorkerClaim(c.tool, c.workers, c.per, c.used); got != c.want {
			t.Errorf("WorkerClaim(%s, %d) = %d MiB, want %d MiB", c.tool, c.workers, got>>20, c.want>>20)
		}
	}
}

// jest and TypeScript 7's tsc are given a count on their command line, only
// when it is lower than what they would start by themselves, and never over
// one already given.
func TestWorkerArgs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		argv string
		n    int
		tsc7 bool
		want string
	}{
		{"jest", 3, false, "jest --maxWorkers=3"},
		{"jest src --ci", 3, false, "jest src --ci --maxWorkers=3"},
		{"npx jest --coverage", 2, false, "npx jest --coverage --maxWorkers=2"},
		{"npx --yes jest", 2, false, "npx --yes jest --maxWorkers=2"},
		{"pnpm exec jest", 2, false, "pnpm exec jest --maxWorkers=2"},
		{"yarn jest", 2, false, "yarn jest --maxWorkers=2"},
		{"jest -- foo", 2, false, "jest --maxWorkers=2 -- foo"},
		{"jest --maxWorkers=50%", 2, false, "jest --maxWorkers=50%"},
		{"jest -w 4", 2, false, "jest -w 4"},
		{"jest -i", 2, false, "jest -i"},
		{"jest --runInBand", 2, false, "jest --runInBand"},
		{"jest", 7, false, "jest"}, // 8 cores: jest starts 7 by itself
		{"npm test", 2, false, "npm test"},
		{"npm exec jest", 2, false, "npm exec jest"}, // npm would take the flag for its own
		{"tsc -p .", 2, true, "tsc -p . --checkers 2"},
		{"npx tsc --noEmit", 1, true, "npx tsc --noEmit --checkers 1"},
		{"tsc -p .", 2, false, "tsc -p ."}, // TypeScript 5 refuses --checkers
		{"tsc", 4, true, "tsc"},            // what it starts by itself
		{"tsc --checkers 8", 2, true, "tsc --checkers 8"},
		{"tsc --singleThreaded", 2, true, "tsc --singleThreaded"},
		{"tsc --init", 2, true, "tsc --init"},
		{"tsc --version", 2, true, "tsc --version"},
		{"go test ./...", 2, true, "go test ./..."},
		{"jest", 0, false, "jest"},
	} {
		argv := strings.Fields(c.argv)
		before := slices.Clone(argv)
		got := strings.Join(WorkerArgs(argv, c.n, 8, c.tsc7), " ")
		if got != c.want {
			t.Errorf("WorkerArgs(%q, %d) = %q, want %q", c.argv, c.n, got, c.want)
		}
		if !slices.Equal(argv, before) {
			t.Errorf("WorkerArgs(%q) changed its argv", c.argv)
		}
	}
}

// With memory counting, Gradle's count goes in GRADLE_OPTS unless somebody
// set one, turbo's never goes under 2, and a nested heavy command replaces
// what an outer one set; WorkerEnv sets neither.
func TestMemoryWorkerEnv(t *testing.T) {
	t.Parallel()
	got := envMap(MemoryWorkerEnv([]string{"GRADLE_OPTS=-Xmx2g"}, 1, false))
	for k, want := range map[string]string{
		"GOMAXPROCS": "1", "GRADLE_OPTS": "-Xmx2g -Dorg.gradle.workers.max=1", "TURBO_CONCURRENCY": "2", "NX_PARALLEL": "1",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if !strings.Contains(got["AGENTBOX_WORKERS"], "GRADLE_OPTS=-Dorg.gradle.workers.max=1") {
		t.Errorf("the mark %q doesn't say GRADLE_OPTS is AgentBox's", got["AGENTBOX_WORKERS"])
	}
	// Somebody's -D stays, and so does a gradle.properties' count.
	if got := envMap(MemoryWorkerEnv([]string{"GRADLE_OPTS='-Dorg.gradle.workers.max=6'"}, 2, false)); got["GRADLE_OPTS"] != "'-Dorg.gradle.workers.max=6'" {
		t.Errorf("somebody's: GRADLE_OPTS = %q", got["GRADLE_OPTS"])
	}
	if got := envMap(MemoryWorkerEnv(nil, 2, true)); got["GRADLE_OPTS"] != "" {
		t.Errorf("gradle.properties sets it: GRADLE_OPTS = %q", got["GRADLE_OPTS"])
	}
	// Nested: the outer one's goes, the inner one's comes.
	outer := MemoryWorkerEnv([]string{"GRADLE_OPTS=-Xmx2g"}, 4, false)
	if got := envMap(MemoryWorkerEnv(outer, 2, false)); got["GRADLE_OPTS"] != "-Xmx2g -Dorg.gradle.workers.max=2" {
		t.Errorf("nested: GRADLE_OPTS = %q", got["GRADLE_OPTS"])
	}
	// And inside it with the setting since turned off, the outer one's goes
	// and nothing comes.
	if got := envMap(WorkerEnv(outer, 2)); got["GRADLE_OPTS"] != "-Xmx2g" {
		t.Errorf("nested, off: GRADLE_OPTS = %q", got["GRADLE_OPTS"])
	}
	// Off: no GRADLE_OPTS, turbo at half.
	if got := envMap(WorkerEnv(nil, 2)); got["GRADLE_OPTS"] != "" || got["TURBO_CONCURRENCY"] != "1" {
		t.Errorf("WorkerEnv: GRADLE_OPTS = %q, TURBO_CONCURRENCY = %q; want what it always set", got["GRADLE_OPTS"], got["TURBO_CONCURRENCY"])
	}
}

// A run's memory without its caches, now and at most; an agent's room under
// its own limit.
func TestRunMemory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, AgentsCgroup, "lxc.payload.ab-x")
	run := filepath.Join(dir, RunCgroupName("heavy-1"))
	write := func(dir, name, value string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, ok := runMemory(run); ok {
		t.Error("a run with no cgroup was read")
	}
	write(run, "memory.current", "3000\n")
	write(run, "memory.stat", "anon 1000\nactive_file 1200\ninactive_file 500\nslab_reclaimable 300\nslab 400\n")
	used, peak, ok := runMemory(run)
	if !ok || used != 1000 || peak != 0 {
		t.Errorf("without memory.peak: %d, %d, %v; want 1000 now, and no peak", used, peak, ok)
	}
	write(run, "memory.peak", "9000\n")
	if used, peak, ok = runMemory(run); !ok || used != 1000 || peak != 7000 {
		t.Errorf("runMemory = %d, %d, %v; want 1000 now, 7000 at most", used, peak, ok)
	}

	if _, ok := agentMemoryRoom(root, "ab-x"); ok {
		t.Error("an agent with no memory.max has room")
	}
	write(dir, "memory.max", "max\n")
	if _, ok := agentMemoryRoom(root, "ab-x"); ok {
		t.Error("an agent with no limit has room")
	}
	write(dir, "memory.max", "10000\n")
	write(dir, "memory.current", "6000\n")
	write(dir, "memory.stat", "active_file 1000\ninactive_file 1000\nslab_reclaimable 0\n")
	if room, ok := agentMemoryRoom(root, "ab-x"); !ok || room != 6000 {
		t.Errorf("agentMemoryRoom = %d, %v; want 6000", room, ok)
	}
}
