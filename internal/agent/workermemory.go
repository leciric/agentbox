package agent

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Worker counts that count memory too: "Fewer build workers when memory is
// short", on unless turned off (26 Nov 2026, the user's decision, which
// narrows the 20 Nov one that memory doesn't change the count). Off, a heavy
// command gets HeavyWorkers and WorkerEnv's counts exactly as before. On, it
// gets MemoryWorkers: its share of the cores, no more than the memory it can
// start in holds at what one worker of its tool takes, and at least one. That
// only lowers the count as it starts: nothing waits, and a command that has
// started is never touched again.
//
// What one worker takes is learned for each project and tool from the runs
// that finished (LearnPerWorker, from the run's cgroup: RunMemory), starting
// from WorkerDefault, which errs high.

// HeavyTool is the tool a heavy command runs, by which what one of its
// workers takes is learned and defaulted: "go test", "go build", "cargo
// test", "cargo build", "vitest", "jest", "tsc", "gradle", "playwright",
// "make", "npm test" for a package script (whichever package manager runs
// it, and whatever the script runs), "docker build"… The program's own name
// for one it doesn't know; "" for nothing at all.
func HeavyTool(command string) string {
	words := strings.Fields(command)
	for i := 0; i < len(words); i++ {
		w := strings.Trim(words[i], `'"`)
		name, _, isVar := strings.Cut(w, "=")
		switch {
		case isVar && name != "" && !strings.ContainsAny(name, "/-"):
			continue // VAR=value
		case w == "sudo" || w == "time" || w == "nice" || w == "nohup" || w == "env" || w == "exec" || w == "command" || w == "--":
			continue
		case w == "timeout":
			i++ // and its duration
			continue
		case w == "agentbox" && i+1 < len(words) && words[i+1] == "heavy":
			i++
			continue
		}
		return toolOf(filepath.Base(w), words[i+1:])
	}
	return ""
}

// toolOf is HeavyTool for program run with args.
func toolOf(program string, args []string) string {
	switch program {
	case "go":
		switch sub := firstArg(args, nil); sub {
		case "test":
			return "go test"
		case "build", "install", "vet", "generate":
			return "go build"
		case "":
			return "go"
		default:
			return "go " + sub
		}
	case "cargo", "cross":
		switch sub := firstArg(args, cargoValueFlags); sub {
		case "test", "nextest", "bench":
			return "cargo test"
		case "run":
			return "cargo run"
		default:
			return "cargo build"
		}
	case "npm", "pnpm", "yarn", "bun":
		rest := argsAfter(args, packageValueFlags)
		if len(rest) == 0 {
			return "npm"
		}
		switch sub := rest[0]; sub {
		case "run", "run-script":
			if len(rest) > 1 {
				return "npm " + rest[1]
			}
			return "npm"
		case "exec", "x", "dlx":
			if len(rest) > 1 {
				return toolOf(filepath.Base(rest[1]), rest[2:])
			}
			return "npm"
		case "t", "tst":
			return "npm test"
		default:
			if program == "yarn" || program == "bun" || program == "pnpm" {
				// yarn jest, pnpm vitest: a binary of the project's.
				if t := toolOf(sub, rest[1:]); t != sub || knownTools[sub] {
					return t
				}
			}
			return "npm " + sub
		}
	case "npx", "pnpx", "bunx":
		rest := argsAfter(args, nil)
		if len(rest) == 0 {
			return program
		}
		return toolOf(filepath.Base(rest[0]), rest[1:])
	case "vp":
		switch firstArg(args, nil) {
		case "test":
			return "vitest"
		case "build":
			return "vite build"
		default:
			return "vite"
		}
	case "vite":
		if firstArg(args, nil) == "build" {
			return "vite build"
		}
		return "vite"
	case "gradle", "gradlew":
		return "gradle"
	case "make", "gmake", "cmake", "ninja":
		return "make"
	case "docker":
		switch firstArg(args, nil) {
		case "build", "buildx":
			return "docker build"
		case "compose":
			return "docker compose"
		}
		return "docker"
	case "tox":
		return "pytest"
	}
	return program
}

// knownTools are the programs with a default of their own in WorkerDefault.
var knownTools = map[string]bool{"vitest": true, "jest": true, "tsc": true, "playwright": true, "webpack": true, "next": true, "electron": true}

// cargoValueFlags and packageValueFlags are the flags before a subcommand
// that take the next word as their value.
var (
	cargoValueFlags   = map[string]bool{"--manifest-path": true, "--config": true, "--color": true, "--target-dir": true, "--target": true, "--features": true, "--package": true, "-C": true, "-Z": true, "-p": true}
	packageValueFlags = map[string]bool{"--prefix": true, "-C": true, "--dir": true, "--filter": true, "-F": true, "--workspace": true, "-w": true, "--cwd": true}
)

// argsAfter is args from the first that isn't a flag (or a +toolchain), the
// flags in valueFlags taking the word after them.
func argsAfter(args []string, valueFlags map[string]bool) []string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+") {
			if valueFlags[a] {
				i++
			}
			continue
		}
		return args[i:]
	}
	return nil
}

func firstArg(args []string, valueFlags map[string]bool) string {
	if rest := argsAfter(args, valueFlags); len(rest) > 0 {
		return rest[0]
	}
	return ""
}

// serverTools are the heavy commands that serve rather than finish: a dev
// server, an app, compose. They run for hours at what they hold, so the
// memory they might still take isn't kept aside for them (WorkerClaim).
var serverTools = map[string]bool{
	"go run": true, "cargo run": true, "vite": true, "next": true, "electron": true, "docker compose": true,
	"npm dev": true, "npm start": true, "npm preview": true, "npm serve": true, "npm watch": true,
}

// ServerTool reports whether tool serves rather than finishes (serverTools).
func ServerTool(tool string) bool { return serverTools[tool] }

const (
	wMiB = int64(1) << 20
	wGiB = int64(1) << 30
)

// workerDefaults is what one worker of each tool is taken to need until a
// run of it has finished in the project: on the high side, as a build that
// starts too few workers is slower and one that starts too many is killed.
// A go test worker compiles, links and runs a package's test binary; a
// cargo job is a rustc with LLVM behind it; a vitest, jest or Playwright
// worker is a Node process with its own module graph (and Playwright's a
// browser); a tsc checker is a type checker over the whole program.
var workerDefaults = map[string]int64{
	"go test": wGiB, "go build": 768 * wMiB,
	"cargo test": 1536 * wMiB, "cargo build": 1536 * wMiB,
	"vitest": 768 * wMiB, "jest": 768 * wMiB, "playwright": wGiB,
	"tsc": wGiB, "webpack": wGiB, "vite build": 768 * wMiB,
	"gradle": wGiB, "mvn": wGiB, "bazel": wGiB, "dotnet": wGiB,
	"make": 512 * wMiB, "docker build": wGiB, "pytest": 512 * wMiB,
}

// defaultWorker is WorkerDefault for a tool not in workerDefaults: a package
// script's (npm test, whatever it runs), or anything else.
const defaultWorker = 768 * wMiB

// WorkerDefault is what one worker of tool is taken to need before the
// project has finished a run of it.
func WorkerDefault(tool string) int64 {
	if n, ok := workerDefaults[tool]; ok {
		return n
	}
	return defaultWorker
}

// WorkerLeast is the least LearnPerWorker brings a tool's figure down to: a
// quarter of its default. A run that had little to do (one package's tests,
// with a worker per core) used next to nothing a worker, which says little
// of the next run of the whole suite.
func WorkerLeast(tool string) int64 { return WorkerDefault(tool) / 4 }

// workerMost is the most a figure is taken to be: past it, a reading is
// wrong (a run's cgroup holding what isn't its own).
const workerMost = 64 * wGiB

// LearnPerWorker is what one worker of tool is known to take after a run
// that peaked at peak with workers workers, from what was known before
// (known, 0 for nothing: WorkerDefault). A run that took more a worker
// raises it to that at once; one that took less brings it down a quarter of
// the way, and only when it succeeded (ok), as a failed one may have stopped
// before it got going; never under WorkerLeast. changed is false when there
// is nothing to keep.
func LearnPerWorker(tool string, known, peak int64, workers int, ok bool) (next int64, changed bool) {
	if peak <= 0 || workers <= 0 {
		return known, false
	}
	if known <= 0 {
		known = WorkerDefault(tool)
	}
	seen := min(peak/int64(workers), workerMost)
	switch {
	case seen > known:
		next = seen
	case ok:
		next = max(known-(known-seen)/4, WorkerLeast(tool))
	default:
		return known, false
	}
	return next, true
}

// Why a heavy command got the count it did (MemoryWorkers, api.HeavyWorkers.Limit).
const (
	// LimitCPU: its share of the cores; memory had room for more.
	LimitCPU = "cpu"
	// LimitMemory: what the VM has free, and can still grow to, less what
	// the other heavy commands running will likely still take.
	LimitMemory = "memory"
	// LimitAgent: what its agent's own memory limit leaves.
	LimitAgent = "agent"
	// LimitLeast: memory had room for none, and it starts with the fewest.
	LimitLeast = "least"
)

// leastWorkers is the fewest workers a heavy command starts with when memory
// counts: one. The one count that breaks at one, turbo's (a task runner that
// refuses to start persistent tasks at a concurrency of one), is held at two
// by itself (workerVars).
const leastWorkers = 1

// MemoryWorkers is how many workers a heavy command starts with when memory
// counts: its share of the cores (cpu, HeavyWorkers), no more than free
// holds at perWorker each, and at least one. limit says which of those it
// is: LimitCPU or LimitMemory (the caller makes the latter LimitAgent when
// its agent's limit is what made free).
func MemoryWorkers(cpu int, free, perWorker int64) (n int, limit string) {
	cpu = max(cpu, leastWorkers)
	if perWorker <= 0 {
		return cpu, LimitCPU
	}
	fits := max(free, 0) / perWorker
	switch {
	case fits >= int64(cpu):
		return cpu, LimitCPU
	case fits < leastWorkers:
		return leastWorkers, LimitLeast
	default:
		return int(fits), LimitMemory
	}
}

// WorkerClaim is what a heavy command still running will likely take on top
// of what it holds (used): what workers workers of perWorker each come to,
// less what it holds; nothing for one that serves rather than finishes
// (ServerTool), which holds what it needs.
func WorkerClaim(tool string, workers int, perWorker, used int64) int64 {
	if ServerTool(tool) || workers <= 0 || perWorker <= 0 {
		return 0
	}
	return max(int64(workers)*perWorker-used, 0)
}

// tscCheckers is how many type checkers TypeScript 7's tsc starts by itself.
const tscCheckers = 4

// WorkerArgs is a heavy command's argv with the worker flag of the tools
// that read one only from their command line, when memory counts and it
// lowers what the tool would start by itself: jest's --maxWorkers (a worker
// per core but one, and no variable for it), and --checkers for TypeScript 7's
// tsc (tsc7; earlier ones refuse it), which starts four. A tool run directly,
// or through npx, pnpm exec, yarn or bunx; a package script's isn't seen
// (npm test running jest), nor one already given a count. cores is how many
// the tool sees. argv itself is never changed.
func WorkerArgs(argv []string, n, cores int, tsc7 bool) []string {
	at, tool := toolAt(argv)
	if at < 0 || n <= 0 {
		return argv
	}
	var flag []string
	switch tool {
	case "jest":
		if n >= max(cores-1, 1) || hasArg(argv[at+1:], "--maxWorkers", "--max-workers", "-w", "--runInBand", "-i") {
			return argv
		}
		flag = []string{"--maxWorkers=" + itoa(n)}
	case "tsc":
		if !tsc7 || n >= tscCheckers || hasArg(argv[at+1:], "--checkers", "--singleThreaded", "--help", "-h", "--version", "-v", "--init", "--showConfig", "--all") {
			return argv
		}
		// tsc takes a flag's value as the next word, never after "=".
		flag = []string{"--checkers", itoa(n)}
	default:
		return argv
	}
	out := make([]string, 0, len(argv)+len(flag))
	end := len(argv)
	for i := at + 1; i < len(argv); i++ {
		if argv[i] == "--" {
			end = i
			break
		}
	}
	out = append(out, argv[:end]...)
	out = append(out, flag...)
	return append(out, argv[end:]...)
}

// toolAt is where in argv the program WorkerArgs knows starts, and which:
// jest or tsc, run as itself or through a package runner. -1 for neither.
func toolAt(argv []string) (int, string) {
	if len(argv) == 0 {
		return -1, ""
	}
	at := 0
	switch filepath.Base(argv[0]) {
	case "npx", "pnpx", "bunx":
		at = len(argv) - len(argsAfter(argv[1:], nil))
	case "pnpm", "yarn", "bun":
		at = len(argv) - len(argsAfter(argv[1:], packageValueFlags))
		if at < len(argv) && (argv[at] == "exec" || argv[at] == "dlx") {
			at++
		}
	}
	if at >= len(argv) {
		return -1, ""
	}
	switch name := filepath.Base(argv[at]); name {
	case "jest", "tsc":
		return at, name
	}
	return -1, ""
}

// hasArg reports whether args has one of flags, alone or as flag=value.
func hasArg(args []string, flags ...string) bool {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		for _, f := range flags {
			if name == f {
				return true
			}
		}
	}
	return false
}

func itoa(n int) string { return strconv.Itoa(n) }

// ArgsTool is the tool WorkerArgs would give a flag to in argv: "jest",
// "tsc", or "" for none.
func ArgsTool(argv []string) string {
	_, tool := toolAt(argv)
	return tool
}
