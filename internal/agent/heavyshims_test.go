package agent

import (
	"slices"
	"testing"
)

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				out[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return out
}

// The worker counts follow the machine's cores, halved while memory is
// short, cargo's capped by the memory available too; a count somebody chose
// stays, and one AgentBox set before is replaced.
func TestWorkerEnv(t *testing.T) {
	t.Parallel()
	got := envMap(WorkerEnv([]string{"HOME=/h", "GOFLAGS=-mod=mod"}, 8, 3<<30, false))
	for k, want := range map[string]string{
		"GOMAXPROCS": "8", "VITEST_MAX_WORKERS": "8", "MAKEFLAGS": "-j8", "GOFLAGS": "-mod=mod -p=8",
		"CARGO_BUILD_JOBS": "2", "TURBO_CONCURRENCY": "4", "AGENTBOX_CPUS": "8", "HOME": "/h",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	// Short: halved, replacing what it set before; the user's own stays.
	again := WorkerEnv([]string{"GOMAXPROCS=8", "AGENTBOX_CPUS=8", "GOFLAGS=-p=8", "MAKEFLAGS=-j3"}, 8, 0, true)
	got = envMap(again)
	for k, want := range map[string]string{"GOMAXPROCS": "4", "GOFLAGS": "-p=4", "MAKEFLAGS": "-j3", "CARGO_BUILD_JOBS": "4"} {
		if got[k] != want {
			t.Errorf("short: %s = %q, want %q", k, got[k], want)
		}
	}
	if n := len(slices.DeleteFunc(slices.Clone(again), func(kv string) bool { return len(kv) < 9 || kv[:9] != "GOFLAGS=-" })); n != 1 {
		t.Errorf("GOFLAGS set %d times", n)
	}
}

// Every shimmed command is linked, and none is the shim's own file.
func TestShimLinks(t *testing.T) {
	t.Parallel()
	script := shimLinks("/home/u/" + ShimDir)
	for _, name := range ShimmedCommands {
		if !slices.Contains(ShimmedCommands, name) || name == shimFile {
			t.Fatalf("%s", name)
		}
		if want := " && ln -sfn " + shimFile + " " + name; !contains(script, want) {
			t.Errorf("no link for %s", name)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
