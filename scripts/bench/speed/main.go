// Command speed measures how much slower a coding task is in AgentBox than in
// plain Claude Code, and where the extra time goes.
//
// # What it does
//
// It runs the same fixed tasks, with the same prompt, model and effort,
// permissions bypassed and nothing interactive, three ways:
//
//   - host: `claude -p` on this computer, in a checkout of the fixture.
//   - exec: the same `claude -p` inside an agent's machine, through
//     `agentbox exec` (incus exec in the VM), in the agent's worktree.
//   - agent: a real AgentBox agent made through the daemon's API
//     (POST /v1/agents) with the prompt as its task, timed from asking for it
//     until its turn ended with nothing left running.
//
// The tasks run on a fixture repository the benchmark writes itself, a Go
// module of about 95,000 generated lines (testdata/fixture and fixture.go),
// whose one commit is pinned (fixtureCommit), so every machine runs the
// same code:
//
//   - edit, mostly the model: add a function, its test and its README line,
//     reading and editing files only, no commands.
//   - build, mostly building and testing: run ./check.sh (go vet, go build and
//     go test -count=1 from a cold build cache, about 100 CPU-seconds), fix the
//     bug the failing test points at, and run it again until it passes.
//
// Every task can run alone, or with N other agents busy (-load 0,4): each
// load agent runs as many busy loops as it has CPUs (-load-kind busy), or
// check.sh over and over (-load-kind build).
//
// # What it records
//
// For every run: the wall time, and where it went, from Claude Code's own
// messages (its stream-json output, stamped as each line arrives, in the host
// and exec modes; the session transcript in the agent's machine in the agent
// mode, which holds the same messages with timestamps): setup (the agent
// mode's machine, worktree and AI tool), startup (until Claude Code had the
// prompt), model time, tool time (and how much of it was builds and tests,
// and hooks), the model calls and the tokens they read, and the commands
// that went on in the background (a build that outlasts the Bash tool's
// two-minute timeout does, and the model then waits on it). check.sh times itself
// (vet, build, test) and records what its build saw: nproc, the cgroup's CPU
// limit and weight, GOMAXPROCS, GOFLAGS, MAKEFLAGS, CARGO_BUILD_JOBS, the
// load average, available memory, whether AgentBox said memory is short, and
// the filesystems of the worktree and of TMPDIR. On top of the task runs, at
// every load: check.sh alone on the host and in the machine (the build with
// no model), and the round trip of `agentbox exec … -- true`.
//
// Each cell (task × load × mode) runs -runs times; the modes take turns
// going first. The report gives the median and the spread as a table, the
// difference from the host part by part, and JSON with every number.
//
// # How to run it
//
// On the computer AgentBox runs on (its VM's front end, or a Linux host
// install), from this directory, with Go, git and Claude Code logged in:
//
//	cd scripts/bench/speed
//	go run . -dry-run                       # check what it needs, print the plan; runs nothing
//	go run .                                # every mode and task, 3 runs each, no load
//	go run . -load 0,4 -load-kind build     # and again with 4 agents building
//	go run . -modes host,exec -tasks build -runs 5
//	go run . -model claude-sonnet-5-5 -effort medium   # cheaper and quicker
//	go run . report ~/agentbox-bench/results/<time>    # the report again, worked out anew from the raw files
//	go run . fixture /tmp/benchfix                     # just write the fixture and print its commit
//
// It keeps its files in ~/agentbox-bench (-dir), under your home so that
// AgentBox's VM sees the fixture at the same path: the fixture (registered as
// the project bench-speed, -project, whose finishing agents wake no chat,
// don't push, and whose history isn't distilled), the host's checkout, and
// results/<time>/ with runs.jsonl (every run, as it ends), summary.json,
// report.md, and raw/ (each run's stream, transcript, chat). The report is
// also printed at the end.
//
// The agents it makes are bench-exec (the exec mode's machine), bench-load-N
// and one per agent-mode run; all are destroyed when it ends, Ctrl-C
// included, unless -keep. Each run is a real model run on your account:
// the default, 2 tasks × 3 modes × 3 runs, is 18 of them, and each load
// level adds as many again. The host mode uses
// this computer's Claude Code login; the agents use the project's Claude
// account in AgentBox.
//
// # Reading it
//
//   - exec against host is what the machine costs, with everything AgentBox
//     gives Claude Code there: slower builds (the CPUs and worker counts it
//     sees, a worktree shared from the host), the hooks around every Bash
//     call, the brief (it is the machine's ~/.claude/CLAUDE.md, so claude -p
//     reads it too: prompt tok/call), the MCP servers, and the round trip in.
//   - agent against exec is what running it as an agent adds: making the
//     machine and worktree (setup), starting the ACP adapter (startup), the
//     adapter's own way of driving Claude Code, the project's memory in the
//     task, and any extra calls an agent makes (the tools table).
//   - The build-alone table separates a slower build from a model that took
//     more steps; s/call and prompt tok/call separate slower model calls
//     from more of them.
//
// The two Claude Codes can differ: the host's is whatever is installed here,
// the machine's is the one AgentBox pins (internal/image/tools.txt). The
// report warns when the versions or the models the runs used differ.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "report":
			if len(args) != 2 {
				return errors.New("usage: speed report <results-dir>")
			}
			return rereport(args[1], stdout)
		case "fixture":
			if len(args) != 2 {
				return errors.New("usage: speed fixture <dir>")
			}
			commit, err := writeFixture(args[1])
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, commit)
			if commit != fixtureCommit {
				return fmt.Errorf("not the pinned commit %s", fixtureCommit)
			}
			return nil
		}
	}
	cfg, err := parseFlags(args, stderr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// The first Ctrl-C stops the run and destroys its agents; a second
		// one stops at once.
		<-ctx.Done()
		stop()
	}()
	b := &bench{cfg: cfg, ab: agentbox{bin: cfg.Agentbox}, log: stderr, hooks: map[string]bool{}, created: map[string]bool{}}
	if cfg.needsDaemon() {
		b.d = newDaemon(cfg.Socket)
	}
	if cfg.DryRun {
		return dryRun(ctx, b, stdout)
	}
	if problems := preflight(ctx, b); len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	b.out = cfg.Out
	if b.out == "" {
		b.out = filepath.Join(cfg.Dir, "results", time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(b.out, 0o755); err != nil {
		return err
	}
	if j, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(b.out, "config.json"), j, 0o644)
	}
	if b.runs, err = os.Create(filepath.Join(b.out, "runs.jsonl")); err != nil {
		return err
	}
	defer b.runs.Close()
	b.logf("results in %s", b.out)

	defer b.cleanup()
	err = b.setup(ctx)
	if err == nil {
		_ = os.WriteFile(filepath.Join(b.out, "commit"), []byte(b.commit+"\n"), 0o644)
		err = b.run(ctx)
	}
	b.cleanup()
	if len(b.records)+len(b.probes) > 0 {
		if rerr := writeResults(b.out, summarizeAll(cfg, b.commit, b.records, b.probes), stdout); rerr != nil {
			return errors.Join(err, rerr)
		}
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("stopped; the report has the runs that ended")
	}
	return err
}

func parseFlags(args []string, stderr io.Writer) (config, error) {
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("speed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := config{}
	var modes, taskList, loads string
	fs.StringVar(&cfg.Dir, "dir", filepath.Join(home, "agentbox-bench"), "where the fixture, the host's checkout and the results go; under your home, which AgentBox's VM shares")
	fs.StringVar(&cfg.Out, "out", "", "the results directory (default: <dir>/results/<time>)")
	fs.StringVar(&modes, "modes", strings.Join(allModes, ","), "modes to run: host, exec, agent")
	fs.StringVar(&taskList, "tasks", strings.Join(allTasks, ","), "tasks to run: edit, build")
	fs.IntVar(&cfg.Runs, "runs", 3, "runs per task, load and mode")
	fs.StringVar(&loads, "load", "0", "load levels: how many other agents are kept busy, like 0,4")
	fs.StringVar(&cfg.LoadKind, "load-kind", "busy", "what the load agents run: busy (a busy loop per CPU) or build (check.sh over and over)")
	fs.IntVar(&cfg.LoadProcs, "load-procs", 0, "busy loops per load agent (default: its machine's nproc)")
	fs.DurationVar(&cfg.LoadWarmup, "load-warmup", 20*time.Second, "how long the load runs before the first measurement")
	fs.IntVar(&cfg.BareRuns, "bare-runs", -1, "runs of check.sh alone per place and load (default: -runs; 0: none)")
	fs.StringVar(&cfg.Model, "model", "claude-opus-5-5", "the model every mode runs, as Claude Code names it; a full id, since an alias can mean another model in another Claude Code version")
	fs.StringVar(&cfg.Effort, "effort", "high", "the effort every mode runs at (AgentBox's agents start at high); \"\" leaves it to each")
	fs.StringVar(&cfg.Project, "project", "bench-speed", "the AgentBox project the fixture is registered as")
	fs.StringVar(&cfg.Claude, "claude", "claude", "the host's claude")
	fs.StringVar(&cfg.Agentbox, "agentbox", "agentbox", "the agentbox command")
	fs.StringVar(&cfg.Socket, "socket", defaultSocket(), "the daemon's socket")
	fs.DurationVar(&cfg.Timeout, "timeout", 20*time.Minute, "the most one run may take")
	fs.DurationVar(&cfg.Poll, "poll", time.Second, "how often an agent-mode run asks the daemon whether it is done")
	fs.BoolVar(&cfg.Keep, "keep", false, "keep the agents it makes, to look at afterwards")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "check what the run needs and print its plan, running nothing and making no agent")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if fs.NArg() > 0 {
		return cfg, fmt.Errorf("unexpected %q: flags only, or the report and fixture commands", fs.Arg(0))
	}
	cfg.Modes = splitList(modes)
	for _, m := range cfg.Modes {
		if !contains(allModes, m) {
			return cfg, fmt.Errorf("unknown mode %q: host, exec or agent", m)
		}
	}
	cfg.Tasks = splitList(taskList)
	for _, t := range cfg.Tasks {
		if _, ok := tasks[t]; !ok {
			return cfg, fmt.Errorf("unknown task %q: edit or build", t)
		}
	}
	for _, l := range splitList(loads) {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 {
			return cfg, fmt.Errorf("bad load level %q: a number of agents, like 0 or 4", l)
		}
		cfg.Loads = append(cfg.Loads, n)
	}
	if len(cfg.Modes) == 0 || len(cfg.Tasks) == 0 || len(cfg.Loads) == 0 || cfg.Runs < 1 {
		return cfg, errors.New("nothing to run: name a mode, a task and a load level, and at least one run")
	}
	if cfg.LoadKind != "busy" && cfg.LoadKind != "build" {
		return cfg, fmt.Errorf("unknown load kind %q: busy or build", cfg.LoadKind)
	}
	if cfg.BareRuns < 0 {
		cfg.BareRuns = cfg.Runs
	}
	dir, err := filepath.Abs(cfg.Dir)
	cfg.Dir = dir
	return cfg, err
}

func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if x == y {
			return true
		}
	}
	return false
}

// preflight finds what would stop the run before it makes anything.
func preflight(ctx context.Context, b *bench) []string {
	var problems []string
	need := func(bin, why string) {
		if _, err := exec.LookPath(bin); err != nil {
			problems = append(problems, fmt.Sprintf("%s isn't on PATH: %s", bin, why))
		}
	}
	need("git", "the fixture is a git repository")
	need("bash", "the fixture's scripts are bash")
	if b.cfg.has(modeHost) {
		need(b.cfg.Claude, "the host mode runs it (-claude)")
		if contains(b.cfg.Tasks, "build") {
			need("go", "the build task builds the fixture with it on the host")
		}
	}
	if b.cfg.needsDaemon() {
		need(b.cfg.Agentbox, "the exec mode and the load go through agentbox exec (-agentbox)")
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := b.d.projects(cctx); err != nil {
			problems = append(problems, fmt.Sprintf("can't reach AgentBox's daemon: %v (is AgentBox running? -socket)", err))
		}
		if home, err := os.UserHomeDir(); err == nil && !strings.HasPrefix(b.cfg.Dir+"/", home+"/") {
			problems = append(problems, fmt.Sprintf("-dir %s isn't under your home, which is what AgentBox's VM shares: it couldn't see the fixture", b.cfg.Dir))
		}
	}
	return problems
}

// dryRun checks what the run needs and prints what it would do.
func dryRun(ctx context.Context, b *bench, w io.Writer) error {
	cfg := b.cfg
	fmt.Fprintf(w, "Dry run: nothing is run and no agent is made.\n\n")
	problems := preflight(ctx, b)
	tmp, err := os.MkdirTemp("", "benchfix-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	commit, err := writeFixture(filepath.Join(tmp, "fixture"))
	switch {
	case err != nil:
		problems = append(problems, "writing the fixture: "+err.Error())
	case commit != fixtureCommit:
		problems = append(problems, fmt.Sprintf("the fixture comes out as %s here, not the pinned %s", commit, fixtureCommit))
	default:
		fmt.Fprintf(w, "Fixture: %s, at the pinned commit %s.\n", filepath.Join(cfg.Dir, "fixture"), commit)
	}
	if cfg.has(modeHost) {
		if out, err := exec.CommandContext(ctx, cfg.Claude, "--version").Output(); err == nil {
			fmt.Fprintf(w, "Host Claude Code: %s.\n", strings.TrimSpace(string(out)))
		}
	}
	if cfg.needsDaemon() {
		fmt.Fprintf(w, "Daemon: %s.\n", cfg.Socket)
	}
	b.commit = fixtureCommit
	b.host = hostPlace{dir: filepath.Join(cfg.Dir, "work", "host"), binary: cfg.Claude}
	b.machine = machinePlace{ab: b.ab, ref: b.ref(execAgent)}

	fmt.Fprintf(w, "\nThe prompts, the same in every mode:\n")
	for _, tn := range cfg.Tasks {
		fmt.Fprintf(w, "- %s: %s\n", tn, tasks[tn].Prompt)
	}
	fmt.Fprintf(w, "\nPlan:\n")
	step := 1
	say := func(format string, args ...any) {
		fmt.Fprintf(w, "%2d. %s\n", step, fmt.Sprintf(format, args...))
		step++
	}
	say("Write the fixture into %s, and check it is at %s.", filepath.Join(cfg.Dir, "fixture"), short(fixtureCommit))
	if cfg.has(modeHost) {
		say("Clone it to %s for the host mode.", b.host.dir)
	}
	if cfg.needsDaemon() {
		say("Register it as project %s if it isn't (POST /v1/projects), and set finishNotices off, agentPRs off, consolidation 0 (PATCH).", cfg.Project)
	}
	if cfg.needsMachine() {
		say("Create %s, the exec mode's machine: POST /v1/agents %s", b.ref(execAgent), plainJSON(b.createRequest(execAgent, "", "claude")))
	}
	models := 0
	for _, load := range cfg.Loads {
		if load > 0 {
			say("Load %d: create bench-load-1…%d (ai none) and run in each, through agentbox exec: %s; wait %s.", load, load, b.loadCommand(), cfg.LoadWarmup)
		}
		if cfg.needsMachine() {
			say("Load %d: time `%s exec %s -- true` 5 times.", load, cfg.Agentbox, b.machine.ref)
		}
		if contains(cfg.Tasks, "build") && cfg.BareRuns > 0 {
			var where []string
			if cfg.has(modeHost) {
				where = append(where, "on the host")
			}
			if cfg.needsMachine() {
				where = append(where, "in "+b.machine.ref)
			}
			say("Load %d: run bench/reset.sh then ./check.sh alone %d times %s.", load, cfg.BareRuns, strings.Join(where, " and "))
		}
		for _, tn := range cfg.Tasks {
			prompt := "<the " + tn + " prompt>"
			for _, mode := range cfg.Modes {
				models += cfg.Runs
				switch mode {
				case modeHost:
					say("Load %d, %s, host ×%d: in %s, bench/reset.sh, bench/probe.sh, then %s %s, then bench/verify.sh %s.", load, tn, cfg.Runs, b.host.dir, cfg.Claude, shellJoin(b.claudeArgs(prompt, true)), tn)
				case modeExec:
					say("Load %d, %s, exec ×%d: the same through `%s exec %s -- claude %s`.", load, tn, cfg.Runs, cfg.Agentbox, b.machine.ref, shellJoin(b.claudeArgs(prompt, true)))
				case modeAgent:
					req := plainJSON(b.createRequest(fmt.Sprintf("bench-%s-l%d-r1", tn, load), prompt, "claude"))
					say("Load %d, %s, agent ×%d: POST /v1/agents %s; poll its chat every %s until its turn ends; read its transcript, probe and verify through agentbox exec; destroy it.", load, tn, cfg.Runs, req, cfg.Poll)
				}
			}
		}
		if load > 0 {
			say("Load %d: destroy the load agents.", load)
		}
	}
	if cfg.needsMachine() {
		say("Destroy %s.", b.machine.ref)
	}
	say("Write summary.json and report.md into %s, and print the report.", filepath.Join(cfg.Dir, "results", "<time>"))
	fmt.Fprintf(w, "\n%d model runs in all, each on your Claude account.\n", models)
	if len(problems) > 0 {
		fmt.Fprintf(w, "\nWhat would stop it:\n")
		for _, p := range problems {
			fmt.Fprintf(w, "- %s\n", p)
		}
		return errors.New("the run can't start as things are")
	}
	fmt.Fprintf(w, "\nNothing in the way.\n")
	return nil
}

// plainJSON is v as one line of JSON, with <, > and & as they are.
func plainJSON(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSpace(b.String())
}

func writeResults(dir string, s summary, stdout io.Writer) error {
	j, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), j, 0o644); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "report.md"))
	if err != nil {
		return err
	}
	writeReport(io.MultiWriter(f, stdout), s)
	return f.Close()
}

// rereport writes the report again from a results directory, working each
// run's breakdown out anew from its raw files.
func rereport(dir string, stdout io.Writer) error {
	var cfg config
	if b, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	commit := ""
	if b, err := os.ReadFile(filepath.Join(dir, "commit")); err == nil {
		commit = strings.TrimSpace(string(b))
	}
	f, err := os.Open(filepath.Join(dir, "runs.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	var records []record
	var probes []probeRun
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var p struct {
			Probe *probeRun `json:"probe"`
		}
		if json.Unmarshal(sc.Bytes(), &p) == nil && p.Probe != nil {
			probes = append(probes, *p.Probe)
			continue
		}
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err == nil && r.Mode != "" {
			if r.Raw != "" && !r.Began.IsZero() {
				account(&r, filepath.Join(dir, r.Raw))
			}
			records = append(records, r)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return writeResults(dir, summarizeAll(cfg, commit, records, probes), stdout)
}
