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
// given none: its tools count the cores themselves. The user's computer
// being busy doesn't change the count (24 Nov 2026, the user's decision).
// Memory does, unless "Fewer build workers when memory is short" is turned
// off (26 Nov 2026, narrowing the 20 Nov decision that it doesn't): then the
// count is MemoryWorkers' and the command starts with MemoryWorkerEnv's
// counts and WorkerArgs' flags (workermemory.go). The kernel's limits still
// protect the VM (memory.max, agentbox.agents' memory.high and memory.max).

// minWorkers is the fewest workers a heavy command is given, however many
// run at once.
const minWorkers = 2

// HeavyWorkers is how many workers a heavy command starts with: the VM's
// cores split between the heavy commands running, this one counted, rounded
// up so no core is left to nobody; never fewer than minWorkers nor more than
// every core. The user's computer being busy doesn't change it (24 Nov 2026,
// the user's decision): it was halved then, and a host that sat at its
// reserve turned busy every minute and halved every build with it.
func HeavyWorkers(cores, running int) int {
	cores, running = max(cores, 1), max(running, 1)
	n := (cores + running - 1) / running
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
func workerVars(n int) [][2]string { return countVars(n, false) }

// countVars is workerVars, and with memory counting (more), turbo's
// concurrency never under 2: turbo refuses to start a persistent task (a dev
// server) at a concurrency of 1, which MemoryWorkers' single worker would
// make it.
func countVars(n int, more bool) [][2]string {
	count, half := strconv.Itoa(n), strconv.Itoa(max(n/2, 1))
	turbo := half
	if more {
		turbo = strconv.Itoa(max(n/2, 2))
	}
	return [][2]string{
		{"GOMAXPROCS", count}, {"VITEST_MAX_WORKERS", count}, {"CARGO_BUILD_JOBS", count},
		{"TURBO_CONCURRENCY", turbo}, {"NX_PARALLEL", half}, {"npm_config_workspace_concurrency", half},
		{"MAKEFLAGS", "-j" + count},
	}
}

// WorkerEnv is env with the worker counts for n workers (HeavyWorkers,
// workerVars) and go's -p=n in GOFLAGS. A count somebody else set stays;
// one AgentBox set (workersMark) is replaced. n of 0 or less, a daemon that
// didn't say, changes nothing.
func WorkerEnv(env []string, n int) []string { return workerEnv(env, n, false, false) }

// MemoryWorkerEnv is WorkerEnv for a daemon that counted memory too
// (MemoryWorkers): turbo's concurrency held at 2 at least (countVars), and
// Gradle's org.gradle.workers.max as a -D in GRADLE_OPTS, which a Gradle
// client reads into its build's options (it reaches ./gradlew too, which no
// shim sees), unless gradleSet, a gradle.properties setting it already,
// which a -D would override.
func MemoryWorkerEnv(env []string, n int, gradleSet bool) []string {
	return workerEnv(env, n, true, !gradleSet)
}

// gradleWorkers is the system property that caps a Gradle build's workers.
const gradleWorkers = "-Dorg.gradle.workers.max="

func workerEnv(env []string, n int, more, gradle bool) []string {
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
	for _, kv := range countVars(n, more) {
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
	if ours := owned["GRADLE_OPTS"]; gradle || ours != "" {
		// Ours from an earlier start goes either way; a new one only when
		// memory counts.
		opts := vars["GRADLE_OPTS"]
		if ours != "" {
			opts = withoutWord(opts, ours)
		}
		d := gradleWorkers + strconv.Itoa(n)
		switch {
		case gradle && !hasWordPrefix(opts, gradleWorkers):
			set("GRADLE_OPTS", appendWord(opts, d))
			mark = append(mark, "GRADLE_OPTS="+d)
		case opts != vars["GRADLE_OPTS"]:
			set("GRADLE_OPTS", opts)
		}
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
	return appendWord(flags, p), p, true
}

// appendWord is s with w after its last word, s left as it was otherwise.
func appendWord(s, w string) string {
	if strings.TrimSpace(s) == "" {
		return w
	}
	return strings.TrimRight(s, " ") + " " + w
}

// hasWordPrefix reports whether a word of s, quoted or not, starts with
// prefix.
func hasWordPrefix(s, prefix string) bool {
	for _, w := range strings.Fields(s) {
		if strings.HasPrefix(strings.TrimLeft(w, `'"`), prefix) {
			return true
		}
	}
	return false
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
