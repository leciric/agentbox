package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var workerVars = []string{"GOMAXPROCS", "CARGO_BUILD_JOBS", "VITEST_MAX_WORKERS", "VITEST_MAX_THREADS", "VITEST_MAX_FORKS", "MAKEFLAGS", "GOFLAGS", "AGENTBOX_CPUS"}

// runWorkers runs workersScript in shell with nproc answering cpus and env set
// on top of an environment without any of workerVars, and returns what each
// of them ends up as.
func runWorkers(t *testing.T, shell, cpus string, env ...string) map[string]string {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "nproc"), []byte("#!/bin/sh\necho "+cpus+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := workersScript
	for _, name := range workerVars {
		script += `printf '%s=%s\n' ` + name + ` "${` + name + `-}"` + "\n"
	}
	cmd := exec.Command(shell, "-c", script)
	cmd.Env = append([]string{"PATH=" + bin + ":" + os.Getenv("PATH")}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", shell, err, out)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, value, _ := strings.Cut(line, "=")
		got[name] = value
	}
	return got
}

func TestWorkersScript(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"sh", "bash"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		// A fresh shell gets every count from nproc.
		got := runWorkers(t, shell, "4")
		for _, name := range []string{"GOMAXPROCS", "CARGO_BUILD_JOBS", "VITEST_MAX_WORKERS", "VITEST_MAX_THREADS", "VITEST_MAX_FORKS", "AGENTBOX_CPUS"} {
			if got[name] != "4" {
				t.Errorf("%s: %s = %q, want 4", shell, name, got[name])
			}
		}
		if got["MAKEFLAGS"] != "-j4" || got["GOFLAGS"] != "-p=4" {
			t.Errorf("%s: MAKEFLAGS = %q, GOFLAGS = %q; want -j4, -p=4", shell, got["MAKEFLAGS"], got["GOFLAGS"])
		}

		// A shell started from one that set them, after the share shrank,
		// follows it, and keeps the rest of GOFLAGS.
		got = runWorkers(t, shell, "2", "AGENTBOX_CPUS=4", "GOMAXPROCS=4", "VITEST_MAX_FORKS=4", "MAKEFLAGS=-j4", "GOFLAGS=-mod=mod -p=4 -tags=x")
		if got["GOMAXPROCS"] != "2" || got["VITEST_MAX_FORKS"] != "2" || got["MAKEFLAGS"] != "-j2" || got["GOFLAGS"] != "-mod=mod -tags=x -p=2" {
			t.Errorf("%s: after the share shrank: %v", shell, got)
		}

		// What somebody else set stays: the user's GOMAXPROCS, a lease's -p,
		// and make's own MAKEFLAGS in a recipe's shell.
		got = runWorkers(t, shell, "2", "AGENTBOX_CPUS=4", "GOMAXPROCS=7", "GOFLAGS=-p=4 -p=3", "MAKEFLAGS=-j4 --jobserver-auth=fifo:/t/x")
		if got["GOMAXPROCS"] != "7" || got["GOFLAGS"] != "-p=3" || got["MAKEFLAGS"] != "-j4 --jobserver-auth=fifo:/t/x" {
			t.Errorf("%s: somebody else's values: %v", shell, got)
		}
	}
}

// The Bash tool's shells read the worker counts, then a heavy phase's lease,
// whose GOFLAGS comes last and so wins.
func TestBashEnvSourcesTheLease(t *testing.T) {
	t.Parallel()
	m := &Manager{}
	m.User.Name = "dev"
	got := m.bashEnv()
	if !strings.HasPrefix(got, "# Written by AgentBox.\n"+workersScript) {
		t.Errorf("bash_env doesn't start with the worker counts:\n%s", got)
	}
	if !strings.HasSuffix(got, "if [ -r '/home/dev/"+HeavyEnvFile+"' ]; then . '/home/dev/"+HeavyEnvFile+"'; fi\n") {
		t.Errorf("bash_env doesn't end with the lease:\n%s", got)
	}
}
