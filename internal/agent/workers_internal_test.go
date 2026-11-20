package agent

import (
	"slices"
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// A heavy command's workers are the cores split between the heavy commands
// running, itself counted: one alone has every core, whatever else runs
// idle; never fewer than two, nor more than the cores; halved while the
// user's computer is busy.
func TestHeavyWorkers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		cores, running int
		busy           bool
		want           int
	}{
		{10, 1, false, 10}, // one busy agent among idle ones: every core
		{10, 0, false, 10}, // a count that missed itself is still one
		{10, 2, false, 5},
		{10, 3, false, 4}, // rounded up: no core left to nobody
		{10, 5, false, 2},
		{10, 9, false, 2}, // never fewer than two
		{16, 4, false, 4},
		{1, 3, false, 1}, // never more than the cores
		{0, 1, false, 1}, // cores unread: one
		{10, 1, true, 5}, // the user's computer is busy: half
		{10, 3, true, 2}, // half of four
		{10, 6, true, 2}, // and still at least two
		{3, 1, true, 2},  // half of three, rounded up
		{64, 2, false, 32},
	} {
		if got := HeavyWorkers(c.cores, c.running, c.busy); got != c.want {
			t.Errorf("HeavyWorkers(%d cores, %d running, busy %v) = %d, want %d", c.cores, c.running, c.busy, got, c.want)
		}
	}
}

// WorkerEnv sets every count from n, half of it for the task runners; a
// count somebody chose stays, and one AgentBox set (an outer heavy
// command's, or an earlier release's shell's) is replaced.
func TestWorkerEnv(t *testing.T) {
	t.Parallel()
	got := envMap(WorkerEnv([]string{"HOME=/h", "GOFLAGS=-mod=mod"}, 8))
	for k, want := range map[string]string{
		"GOMAXPROCS": "8", "VITEST_MAX_WORKERS": "8", "CARGO_BUILD_JOBS": "8", "MAKEFLAGS": "-j8",
		"GOFLAGS": "-mod=mod -p=8", "TURBO_CONCURRENCY": "4", "NX_PARALLEL": "4",
		"npm_config_workspace_concurrency": "4", "HOME": "/h",
		"AGENTBOX_WORKERS": "GOMAXPROCS=8 VITEST_MAX_WORKERS=8 CARGO_BUILD_JOBS=8 TURBO_CONCURRENCY=4 NX_PARALLEL=4 npm_config_workspace_concurrency=4 MAKEFLAGS=-j8 GOFLAGS=-p=8",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}

	// What the user, the agent or a project set stays.
	mine := []string{"GOMAXPROCS=3", "VITEST_MAX_WORKERS=1", "CARGO_BUILD_JOBS=2", "MAKEFLAGS=-j3", "GOFLAGS=-p=1", "TURBO_CONCURRENCY=7"}
	got = envMap(WorkerEnv(mine, 8))
	for k, want := range map[string]string{
		"GOMAXPROCS": "3", "VITEST_MAX_WORKERS": "1", "CARGO_BUILD_JOBS": "2", "MAKEFLAGS": "-j3",
		"GOFLAGS": "-p=1", "TURBO_CONCURRENCY": "7", "NX_PARALLEL": "4",
	} {
		if got[k] != want {
			t.Errorf("set by somebody: %s = %q, want %q", k, got[k], want)
		}
	}

	// A heavy command started inside one replaces what the outer one set,
	// but not a count somebody set to the very number it was given.
	outer := WorkerEnv([]string{"GOMAXPROCS=4", "GOFLAGS=-mod=mod"}, 4)
	got = envMap(WorkerEnv(outer, 2))
	for k, want := range map[string]string{"GOMAXPROCS": "4", "VITEST_MAX_WORKERS": "2", "MAKEFLAGS": "-j2", "GOFLAGS": "-mod=mod -p=2"} {
		if got[k] != want {
			t.Errorf("nested: %s = %q, want %q", k, got[k], want)
		}
	}

	// GOFLAGS is left exactly as it was but for the -p it adds, quoted
	// values' spaces included; a -p in any form Go reads is somebody's.
	quoted := `'-modfile=/tmp/team  build/go.mod'  -mod=mod`
	got = envMap(WorkerEnv([]string{"GOFLAGS=" + quoted}, 6))
	if want := quoted + " -p=6"; got["GOFLAGS"] != want {
		t.Errorf("quoted: GOFLAGS = %q, want %q", got["GOFLAGS"], want)
	}
	if got = envMap(WorkerEnv([]string{"GOFLAGS=" + got["GOFLAGS"]}, 6)); got["GOFLAGS"] != quoted+" -p=6" {
		t.Errorf("quoted, without the mark: GOFLAGS = %q", got["GOFLAGS"])
	}
	for _, p := range []string{"--p=1", "'-p=1'", `"--p=1"`, "-mod=mod -p=1"} {
		if got := envMap(WorkerEnv([]string{"GOFLAGS=" + p}, 8)); got["GOFLAGS"] != p {
			t.Errorf("GOFLAGS=%s became %q", p, got["GOFLAGS"])
		}
	}

	// An earlier release's shell set the counts from its share, cargo's
	// capped by memory: replaced, and its marks go.
	old := []string{
		"AGENTBOX_CPUS=2", "GOMAXPROCS=2", "VITEST_MAX_WORKERS=2", "CARGO_BUILD_JOBS=1", "AGENTBOX_CARGO_JOBS=1",
		"MAKEFLAGS=-j2", "GOFLAGS=-mod=mod -p=2", "TURBO_CONCURRENCY=1",
	}
	again := WorkerEnv(old, 10)
	got = envMap(again)
	for k, want := range map[string]string{
		"GOMAXPROCS": "10", "VITEST_MAX_WORKERS": "10", "CARGO_BUILD_JOBS": "10", "MAKEFLAGS": "-j10",
		"GOFLAGS": "-mod=mod -p=10", "TURBO_CONCURRENCY": "5",
	} {
		if got[k] != want {
			t.Errorf("an earlier release's: %s = %q, want %q", k, got[k], want)
		}
	}
	for _, k := range []string{"AGENTBOX_CPUS", "AGENTBOX_CARGO_JOBS"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s stayed", k)
		}
	}
	// What it set then that somebody else would have set too stays theirs.
	got = envMap(WorkerEnv([]string{"AGENTBOX_CPUS=2", "GOMAXPROCS=3", "CARGO_BUILD_JOBS=1"}, 10))
	if got["GOMAXPROCS"] != "3" || got["CARGO_BUILD_JOBS"] != "1" {
		t.Errorf("an earlier release's, unmarked: GOMAXPROCS = %q, CARGO_BUILD_JOBS = %q; want 3 and 1", got["GOMAXPROCS"], got["CARGO_BUILD_JOBS"])
	}
	if n := len(slices.DeleteFunc(slices.Clone(again), func(kv string) bool { return !strings.HasPrefix(kv, "GOFLAGS=") })); n != 1 {
		t.Errorf("GOFLAGS set %d times", n)
	}

	// A value that only happens to equal a count, with no mark beside it,
	// is somebody's.
	got = envMap(WorkerEnv([]string{"GOMAXPROCS=", "MAKEFLAGS=-j"}, 4))
	if got["GOMAXPROCS"] != "4" || got["MAKEFLAGS"] != "-j" {
		t.Errorf("unmarked: GOMAXPROCS = %q, MAKEFLAGS = %q; want 4 and -j", got["GOMAXPROCS"], got["MAKEFLAGS"])
	}

	// No count from the daemon: nothing changes.
	if got := WorkerEnv([]string{"HOME=/h"}, 0); !slices.Equal(got, []string{"HOME=/h"}) {
		t.Errorf("n of 0: %q", got)
	}
}
