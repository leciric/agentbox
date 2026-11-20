package agent

import (
	"slices"
	"strconv"
	"strings"
)

// Every agent's machine sees every core of the VM, and the CPU is shared
// between the agents busy at once by cpu.weight (AgentCPUWeight, caps.go),
// which divides only what more than one of them wants: a busy agent among
// idle ones has the whole VM. Earlier releases pinned each running agent to
// ceil(cores / agents running) cores (limits.cpu, now taken off:
// oldlimits.go), counted by machines running rather than working, so one
// busy agent among five idle ones built on 2 of 10 cores.
//
// What is still split is how many workers a build or test run starts, as
// test runners and build tools start one per core they see (go test, vitest,
// jest, cargo, Playwright): several agents' builds at once would each start
// a worker per core, each with its own memory. So as a heavy command starts
// (cli.runHeavyCommand, from a shim or `agentbox heavy`), the daemon, which
// hears of every heavy command start and end in every agent, answers
// HeavyWorkers for the heavy commands running at that moment, and the
// command starts with WorkerEnv's counts. A command that isn't heavy is
// given none: its tools count the cores themselves. Memory doesn't change
// the count (20 Nov 2026, the user's decision): the kernel's limits protect
// it (memory.max, agentbox.agents' memory.high and memory.max).

// minWorkers is the fewest workers a heavy command is given, however many
// run at once.
const minWorkers = 2

// HeavyWorkers is how many workers a heavy command starts with: the VM's
// cores split between the heavy commands running, this one counted, rounded
// up so no core is left to nobody; halved while the user's computer is busy
// (hostBusy, hostload), as its own programs come first; never fewer than
// minWorkers nor more than every core.
func HeavyWorkers(cores, running int, hostBusy bool) int {
	cores, running = max(cores, 1), max(running, 1)
	n := (cores + running - 1) / running
	if hostBusy {
		n = (n + 1) / 2
	}
	return min(max(n, minWorkers), cores)
}

// workersMark is set beside the worker counts WorkerEnv sets, to n: a count
// equal to it is AgentBox's, which the next heavy command started inside
// (`agentbox heavy` in a build) replaces. A shell of an earlier release, which
// set the counts in every shell, set it too.
const workersMark = "AGENTBOX_CPUS"

// oldCargoMark is what an earlier release's shells set beside a
// CARGO_BUILD_JOBS they capped by the memory available.
const oldCargoMark = "AGENTBOX_CARGO_JOBS"

// WorkerEnv is env with the worker counts for n workers (HeavyWorkers):
// GOMAXPROCS, VITEST_MAX_WORKERS (the only one vitest 4 reads),
// CARGO_BUILD_JOBS, make's -j and go's -p (GOFLAGS), and half of n for the
// task runners that start one package per slot, each with a worker pool of
// its own (turbo, nx, pnpm; lerna and yarn have no variable for it). A count
// the user, the agent or a project's scripts set stays; one AgentBox set
// (equal to workersMark) is replaced. n of 0 or less, a daemon that didn't
// say, changes nothing.
func WorkerEnv(env []string, n int) []string {
	if n <= 0 {
		return env
	}
	vars := map[string]string{}
	var order []string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, seen := vars[k]; !seen {
			order = append(order, k)
		}
		vars[k] = v
	}
	set := func(k, v string) {
		if _, ok := vars[k]; !ok {
			order = append(order, k)
		}
		vars[k] = v
	}
	ours, hadOurs := vars[workersMark]
	ourHalf := ""
	if c, err := strconv.Atoi(ours); err == nil {
		ourHalf = strconv.Itoa(max(c/2, 1))
	}
	replace := func(k, want string, oursWere ...string) {
		v, ok := vars[k]
		if !ok || v == "" || hadOurs && slices.Contains(oursWere, v) {
			set(k, want)
		}
	}
	count := strconv.Itoa(n)
	replace("GOMAXPROCS", count, ours)
	replace("VITEST_MAX_WORKERS", count, ours)
	if cargo, ok := vars[oldCargoMark]; ok {
		replace("CARGO_BUILD_JOBS", count, ours, cargo)
		delete(vars, oldCargoMark)
	} else {
		replace("CARGO_BUILD_JOBS", count, ours)
	}
	for _, k := range []string{"TURBO_CONCURRENCY", "NX_PARALLEL", "npm_config_workspace_concurrency"} {
		replace(k, strconv.Itoa(max(n/2, 1)), ourHalf)
	}
	replace("MAKEFLAGS", "-j"+count, "-j"+ours)
	var flags []string
	hasP := false
	for _, w := range strings.Fields(vars["GOFLAGS"]) {
		if hadOurs && w == "-p="+ours {
			continue
		}
		hasP = hasP || strings.HasPrefix(w, "-p=")
		flags = append(flags, w)
	}
	if !hasP {
		flags = append(flags, "-p="+count)
	}
	set("GOFLAGS", strings.Join(flags, " "))
	set(workersMark, count)
	out := make([]string, 0, len(order))
	for _, k := range order {
		if v, ok := vars[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}
