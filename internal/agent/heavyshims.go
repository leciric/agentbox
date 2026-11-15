package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agentbox/internal/state"
)

// Heavy commands are gated where every AI tool, and the terminal, goes
// through: the commands themselves. Each machine has a directory of small
// shims named after the build and test tools (go, cargo, npm, pnpm, make,
// docker…), first in PATH. A shim runs `agentbox heavy-shim`, which runs
// the real command at once unless it is a heavy one (a build, a test run,
// a dev server), and otherwise asks the daemon first, waits its turn, joins
// a cgroup of its own and starts with as many jobs as the VM's memory
// allows (WorkerEnv), as `agentbox heavy` does. Inside a heavy command
// (AGENTBOX_HEAVY set) a shim runs the real one straight from the shell.
//
// Claude Code's hook around its Bash tool did this for Claude Code alone,
// and only when its Bash tool's shell read BASH_ENV, which joins the
// command to its cgroup: on 2026-11-06 it didn't in four calls out of four
// in an agent, then did (Claude Code treats BASH_ENV as a way to run code
// unsandboxed), and a run that never joined could never be paused. A
// command run in the background ended its run at once (PostToolUse), and no
// other AI tool, nor the terminal, asked at all.

// ShimDir is where, under the agent user's home, the shims are.
const ShimDir = ".config/agentbox/shims"

// HeavyEnv is set, to its run's key, in a heavy command and everything it
// starts: a shim inside one runs the real command.
const HeavyEnv = "AGENTBOX_HEAVY"

// ShimmedCommands are the commands with a shim: those whose builds and test
// runs spike an agent's memory. Only some of what they do is heavy, which
// heavy-shim sorts (cli.isHeavy).
var ShimmedCommands = []string{
	"go", "cargo", "cross", "rustc", "just",
	"npm", "pnpm", "yarn", "bun", "npx", "pnpx", "bunx",
	"vitest", "jest", "playwright", "tsc", "vite",
	"make", "cmake", "ninja", "bazel", "gradle", "mvn", "dotnet",
	"docker", "pytest", "tox",
}

// quickGo are go's subcommands that are never heavy and that editors run
// all the time (gopls runs `go list` and `go env`): the shim runs them
// straight from the shell, without starting agentbox.
const quickGo = "go:list|go:env|go:version|go:mod|go:help|go:fmt|go:doc|go:tool|go:telemetry"

// shimFile is the one shim, under ShimDir, every command's name links to.
const shimFile = "agentbox-shim"

// ShimScript is the shim: it is run by the name of the command it stands
// for, through a link.
func ShimScript() string {
	return fmt.Sprintf(`#!/bin/sh
# Written by AgentBox: builds and tests wait their turn for the VM's memory,
# and run with fewer jobs while it is short (agentbox heavy).
d=${0%%/*}
n=${0##*/}
real() {
  IFS=:
  for p in $PATH; do
    [ "$p" = "$d" ] && continue
    if [ -f "$p/$n" ] && [ -x "$p/$n" ]; then unset IFS; exec "$p/$n" "$@"; fi
  done
  unset IFS
  echo "$n: command not found" >&2
  exit 127
}
[ -n "${%[1]s-}" ] && real "$@"
case $n:${1-} in %[3]s) real "$@" ;; esac
[ -x %[2]s ] || real "$@"
exec %[2]s heavy-shim "$n" "$d" "$@"
`, HeavyEnv, AgentBinaryPath, quickGo)
}

// shimLinks is the shell that links every shimmed command's name to the
// shim, in its directory.
func shimLinks(dir string) string {
	script := "cd " + shellQuote(dir)
	for _, name := range ShimmedCommands {
		script += " && ln -sfn " + shimFile + " " + name
	}
	return script
}

// shimPathLine is what puts the shims first in a login shell's PATH.
const shimPathLine = `case ":$PATH:" in *":$HOME/` + ShimDir + `:"*) ;; *) export PATH="$HOME/` + ShimDir + `:$PATH" ;; esac` + "\n"

// withShimsFirst is a command line run with the shims first in PATH, after
// whatever put its own directories in front (mise exec).
func withShimsFirst(cmdline string) string {
	return `sh -c 'PATH="$HOME/` + ShimDir + `:$PATH"; exec "$@"' agentbox-shims ` + cmdline
}

// writeHeavyShims writes the shim and its links into a's machine, every
// start, so they are always this daemon's own.
func (m *Manager) writeHeavyShims(ctx context.Context, a state.Agent) error {
	dir := "/home/" + m.User.Name + "/" + ShimDir
	if err := m.Incus.WriteFile(ctx, a.Instance, dir+"/"+shimFile, []byte(ShimScript()), m.User.UID, m.User.GID, 0o755); err != nil {
		return fmt.Errorf("writing the heavy commands' shim into %s: %w", a.Ref(), err)
	}
	var out bytes.Buffer
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, shimLinks(dir), nil, &out, &out); err != nil {
		return fmt.Errorf("linking the heavy commands' shims in %s: %w: %s", a.Ref(), err, strings.TrimSpace(out.String()))
	}
	return nil
}

// FindReal is the command name resolves to in path, skipping the shims'
// directory: what a shim runs.
func FindReal(name, shimDir, path string) (string, bool) {
	skip, _ := filepath.EvalSymlinks(shimDir)
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || dir == shimDir {
			continue
		}
		if real, err := filepath.EvalSymlinks(dir); err == nil && skip != "" && real == skip {
			continue
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// cargoJobMemory is what one of cargo's jobs is given, as in workersScript:
// a rustc takes a gigabyte or two.
const cargoJobMemory = 3 << 29 // 1.5 GiB

// WorkerEnv is env with the worker counts a heavy command starts with, as
// workersScript sets them in a shell: cpus, the machine's share of the VM's
// cores, halved while memory is short, and cargo's jobs capped by the memory
// available too. A count somebody chose stays; one AgentBox set before
// (equal to AGENTBOX_CPUS) is replaced.
func WorkerEnv(env []string, cpus int, memAvailable int64, short bool) []string {
	if cpus <= 0 {
		return env
	}
	n := cpus
	if short {
		n = (n + 1) / 2
	}
	vars := map[string]string{}
	order := []string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, seen := vars[k]; !seen {
			order = append(order, k)
		}
		vars[k] = v
	}
	get := func(k string) (string, bool) { v, ok := vars[k]; return v, ok }
	set := func(k, v string) {
		if _, ok := vars[k]; !ok {
			order = append(order, k)
		}
		vars[k] = v
	}
	ours, _ := get("AGENTBOX_CPUS")
	replace := func(k, want, oursWas string) {
		if v, ok := get(k); !ok || v == "" || v == oursWas {
			set(k, want)
		}
	}
	replace("GOMAXPROCS", strconv.Itoa(n), ours)
	replace("VITEST_MAX_WORKERS", strconv.Itoa(n), ours)
	jobs := n
	if memAvailable > 0 {
		jobs = min(jobs, max(1, int(memAvailable/cargoJobMemory)))
	}
	if v, ok := get("CARGO_BUILD_JOBS"); !ok || v == "" || v == vars["AGENTBOX_CARGO_JOBS"] || v == ours {
		set("CARGO_BUILD_JOBS", strconv.Itoa(jobs))
		set("AGENTBOX_CARGO_JOBS", strconv.Itoa(jobs))
	}
	half := max(n/2, 1)
	oursHalf := ""
	if c, err := strconv.Atoi(ours); err == nil {
		oursHalf = strconv.Itoa(max(c/2, 1))
	}
	for _, k := range []string{"TURBO_CONCURRENCY", "NX_PARALLEL", "npm_config_workspace_concurrency"} {
		replace(k, strconv.Itoa(half), oursHalf)
	}
	if v, ok := get("MAKEFLAGS"); !ok || v == "" || v == "-j"+ours {
		set("MAKEFLAGS", "-j"+strconv.Itoa(n))
	}
	var flags []string
	hasP := false
	for _, w := range strings.Fields(vars["GOFLAGS"]) {
		if w == "-p="+ours {
			continue
		}
		hasP = hasP || strings.HasPrefix(w, "-p=")
		flags = append(flags, w)
	}
	if !hasP {
		flags = append(flags, "-p="+strconv.Itoa(n))
	}
	set("GOFLAGS", strings.Join(flags, " "))
	set("AGENTBOX_CPUS", strconv.Itoa(n))
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+vars[k])
	}
	return out
}
