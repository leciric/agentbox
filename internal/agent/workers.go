package agent

import (
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

// workersMark is set beside the worker counts WorkerEnv sets, to what it
// set: "GOMAXPROCS=8 MAKEFLAGS=-j8 GOFLAGS=-p=8…" (for GOFLAGS, the one flag
// it added). A variable that still holds what the mark says is AgentBox's,
// which the next heavy command started inside (`agentbox heavy` in a build)
// replaces; one the user, the agent or a project set, even to the same
// number, isn't in it and stays.
const workersMark = "AGENTBOX_WORKERS"

// An earlier release's shells set every count from their share, beside
// oldMark, its number, and oldCargoMark, a CARGO_BUILD_JOBS they capped by
// the memory available. Where an AI tool started then still runs, a count
// equal to them is taken for AgentBox's (oldOwned), and both go once
// workersMark replaces them.
const (
	oldMark      = "AGENTBOX_CPUS"
	oldCargoMark = "AGENTBOX_CARGO_JOBS"
)

// workerVars are the variables WorkerEnv sets, with the value each gets for
// n workers: GOMAXPROCS, VITEST_MAX_WORKERS (the only one vitest 4 reads),
// CARGO_BUILD_JOBS, make's -j, and half of n for the task runners that start
// one package per slot, each with a worker pool of its own (turbo, nx, pnpm;
// lerna and yarn have no variable for it). Go's -p is GOFLAGS', a flag among
// others (goFlags).
func workerVars(n int) [][2]string {
	count, half := strconv.Itoa(n), strconv.Itoa(max(n/2, 1))
	return [][2]string{
		{"GOMAXPROCS", count}, {"VITEST_MAX_WORKERS", count}, {"CARGO_BUILD_JOBS", count},
		{"TURBO_CONCURRENCY", half}, {"NX_PARALLEL", half}, {"npm_config_workspace_concurrency", half},
		{"MAKEFLAGS", "-j" + count},
	}
}

// WorkerEnv is env with the worker counts for n workers (HeavyWorkers,
// workerVars) and go's -p=n in GOFLAGS. A count somebody else set stays;
// one AgentBox set (workersMark) is replaced. n of 0 or less, a daemon that
// didn't say, changes nothing.
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
	owned := oldOwned(vars)
	if mark, ok := vars[workersMark]; ok {
		owned = map[string]string{}
		for _, kv := range strings.Fields(mark) {
			k, v, _ := strings.Cut(kv, "=")
			owned[k] = v
		}
	}
	set := func(k, v string) {
		if _, ok := vars[k]; !ok {
			order = append(order, k)
		}
		vars[k] = v
	}
	var mark []string
	for _, kv := range workerVars(n) {
		k, want := kv[0], kv[1]
		v, ok := vars[k]
		ours, marked := owned[k]
		if ok && v != "" && (!marked || v != ours) {
			continue // somebody else's
		}
		set(k, want)
		mark = append(mark, k+"="+want)
	}
	if flags, p, ok := goFlags(vars["GOFLAGS"], owned["GOFLAGS"], n); ok {
		set("GOFLAGS", flags)
		mark = append(mark, "GOFLAGS="+p)
	}
	set(workersMark, strings.Join(mark, " "))
	delete(vars, oldMark)
	delete(vars, oldCargoMark)
	out := make([]string, 0, len(order))
	for _, k := range order {
		if v, ok := vars[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// oldOwned is what an earlier release's shell set, from its marks: every
// count from oldMark's number, and CARGO_BUILD_JOBS from oldCargoMark when it
// capped it. Nothing when there is no oldMark.
func oldOwned(vars map[string]string) map[string]string {
	c, err := strconv.Atoi(vars[oldMark])
	if err != nil || c <= 0 {
		return nil
	}
	owned := map[string]string{"GOFLAGS": "-p=" + strconv.Itoa(c)}
	for _, kv := range workerVars(c) {
		owned[kv[0]] = kv[1]
	}
	if cargo := vars[oldCargoMark]; cargo != "" && vars["CARGO_BUILD_JOBS"] == cargo {
		owned["CARGO_BUILD_JOBS"] = cargo
	}
	return owned
}

// goFlags is GOFLAGS with -p=n, and the flag it added, unless it holds a -p
// of somebody else's: then ok is false and it stays as it is. ours is the -p
// flag AgentBox added before, taken out first. The rest is left exactly as
// it was, spaces and quotes included: Go reads quoted values in it.
func goFlags(flags, ours string, n int) (out, p string, ok bool) {
	if ours != "" {
		flags = withoutWord(flags, ours)
	}
	for _, w := range strings.Fields(flags) {
		// -p=1, --p=1, and either quoted.
		w = strings.TrimLeft(w, `'"`)
		if strings.HasPrefix(w, "-p=") || strings.HasPrefix(w, "--p=") {
			return "", "", false
		}
	}
	p = "-p=" + strconv.Itoa(n)
	if strings.TrimSpace(flags) == "" {
		return p, p, true
	}
	return strings.TrimRight(flags, " ") + " " + p, p, true
}

// withoutWord is s without the first occurrence of w as a whole
// space-separated word, and the space before it (or after it, when it comes
// first); the rest stays exactly as it was.
func withoutWord(s, w string) string {
	for i := 0; i+len(w) <= len(s); {
		j := strings.Index(s[i:], w)
		if j < 0 {
			break
		}
		at := i + j
		end := at + len(w)
		if (at == 0 || s[at-1] == ' ') && (end == len(s) || s[end] == ' ') {
			switch {
			case at > 0:
				return s[:at-1] + s[end:]
			case end < len(s):
				return s[end+1:]
			default:
				return ""
			}
		}
		i = at + 1
	}
	return s
}
