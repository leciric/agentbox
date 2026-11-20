package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// The modes: where Claude Code runs, and how it is started.
const (
	modeHost  = "host"  // claude -p on this machine
	modeExec  = "exec"  // claude -p in an agent's machine, through agentbox exec (incus exec)
	modeAgent = "agent" // an AgentBox agent created through the daemon's API, with the prompt as its task
)

var allModes = []string{modeHost, modeExec, modeAgent}

// task is one of the fixed tasks, the same prompt in every mode.
type task struct {
	Name   string
	Prompt string
	Build  bool // its work is mostly building and testing
}

var tasks = map[string]task{
	"edit": {
		Name: "edit",
		Prompt: "Add a function `Title(s string) string` to strutil/strutil.go that upper-cases the first letter of " +
			"each word and lower-cases the rest of it, words being separated by spaces, and keeps the spaces as " +
			"they are. Give it a doc comment like the other functions have, add a table-driven test for it to " +
			"strutil/strutil_test.go like the existing ones, and add it to the list of functions in README.md. " +
			"Only read and edit files: don't run any commands, builds or tests. Don't commit.",
	},
	"build": {
		Name: "build",
		Prompt: "Run ./check.sh: it vets, builds and tests this Go repository, and a test in the stats package " +
			"fails. Fix the bug in the code (don't change any test), then run ./check.sh again and make sure it " +
			"passes. Don't commit.",
		Build: true,
	},
}

var allTasks = []string{"edit", "build"}

type config struct {
	Dir        string        `json:"dir"`
	Out        string        `json:"out"`
	Modes      []string      `json:"modes"`
	Tasks      []string      `json:"tasks"`
	Runs       int           `json:"runs"`
	Loads      []int         `json:"loads"`
	LoadKind   string        `json:"loadKind"`
	LoadProcs  int           `json:"loadProcs"`
	LoadWarmup time.Duration `json:"loadWarmup"`
	BareRuns   int           `json:"bareRuns"`
	Model      string        `json:"model"`
	Effort     string        `json:"effort"`
	Project    string        `json:"project"`
	Claude     string        `json:"claude"`
	Agentbox   string        `json:"agentbox"`
	Socket     string        `json:"socket"`
	Timeout    time.Duration `json:"timeout"`
	Poll       time.Duration `json:"poll"`
	Keep       bool          `json:"keep"`
	DryRun     bool          `json:"dryRun"`
}

func (c config) has(mode string) bool { return slices.Contains(c.Modes, mode) }

// needsDaemon says whether the run needs AgentBox at all: the host mode alone,
// with no load, doesn't.
func (c config) needsDaemon() bool {
	return c.has(modeExec) || c.has(modeAgent) || slices.ContainsFunc(c.Loads, func(n int) bool { return n > 0 })
}

// needsMachine says whether the run needs the long-lived agent machine the
// exec mode runs in, which also takes the machine's bare builds and its exec
// round trips.
func (c config) needsMachine() bool { return c.has(modeExec) || c.has(modeAgent) }

// probeEnv is what bench/lib.sh's probe saw.
type probeEnv struct {
	Host            string            `json:"host"`
	OS              string            `json:"os"`
	Nproc           string            `json:"nproc"`
	CPUMax          string            `json:"cpuMax"`
	CPUWeight       string            `json:"cpuWeight"`
	MemoryMax       string            `json:"memoryMax"`
	MemoryHigh      string            `json:"memoryHigh"`
	Loadavg         string            `json:"loadavg"`
	MemTotalKiB     string            `json:"memTotalKiB"`
	MemAvailableKiB string            `json:"memAvailableKiB"`
	MemoryShort     string            `json:"memoryShort"`
	InAgent         bool              `json:"inAgent"`
	WorktreeFs      string            `json:"worktreeFs"`
	TmpFs           string            `json:"tmpFs"`
	GoVersion       string            `json:"goVersion"`
	Env             map[string]string `json:"env"`
}

// checkRun is one run of check.sh, as it measured itself.
type checkRun struct {
	N       int      `json:"n"`
	VetMS   float64  `json:"vetMs"`
	BuildMS float64  `json:"buildMs"`
	TestMS  float64  `json:"testMs"`
	TotalMS float64  `json:"totalMs"`
	Exit    int      `json:"exit"`
	Env     probeEnv `json:"env"`
}

type verifyResult struct {
	OK      bool       `json:"ok"`
	Reason  string     `json:"reason"`
	Changed string     `json:"changed"`
	Checks  []checkRun `json:"checks"`
}

// record is one run of one task in one mode.
type record struct {
	Mode      string    `json:"mode"`
	Task      string    `json:"task"`
	Load      int       `json:"load"`
	Run       int       `json:"run"`
	Agent     string    `json:"agent,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	// Error says the run itself failed: Claude Code didn't start, the agent
	// wasn't made, it timed out. OK says the task was done.
	Error  string `json:"error,omitempty"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`

	// WallMS is the whole run: from starting claude, or asking the daemon for
	// the agent, until it was done. It is SetupMS + StartupMS + the active
	// span (Breakdown.ActiveMS) + TailMS.
	WallMS float64 `json:"wallMs"`
	// SetupMS is the agent mode's: from asking for the agent until its task
	// was in its chat, the machine, worktree and AI tool made and started
	// (and up to one poll of the daemon).
	SetupMS float64 `json:"setupMs"`
	// StartupMS is until Claude Code had the prompt: from starting it (host,
	// exec: through agentbox exec and a login shell) or from the task
	// arriving in the agent's chat (agent).
	StartupMS float64 `json:"startupMs"`
	// TailMS is from Claude Code's last message until the run was seen done.
	TailMS float64 `json:"tailMs"`
	// Began and Ended are the run's span on the benchmark's clock: claude
	// started and exited (host, exec), the agent asked for and seen done
	// (agent).
	Began     time.Time  `json:"began"`
	Ended     time.Time  `json:"ended"`
	Breakdown breakdown  `json:"breakdown"`
	Source    string     `json:"source"` // stream, transcript or chat: where Breakdown came from
	Checks    []checkRun `json:"checks,omitempty"`
	// CheckMS is check.sh's own time, all its runs added up.
	CheckMS float64 `json:"checkMs"`
	// Env is what the run's builds saw: the first check.sh's probe, or the
	// probe the benchmark ran just before the run (in the agent mode, once
	// it had started).
	Env *probeEnv `json:"env,omitempty"`
	Raw string    `json:"raw,omitempty"` // its raw files, relative to the results
}

// probeRun is a measurement that isn't a task: check.sh run on its own
// (bare-host, bare-machine), or a command's round trip into the machine
// (exec-rtt).
type probeRun struct {
	Kind   string    `json:"kind"`
	Load   int       `json:"load"`
	Run    int       `json:"run"`
	WallMS float64   `json:"wallMs"`
	Check  *checkRun `json:"check,omitempty"`
	Env    *probeEnv `json:"env,omitempty"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

// place is where commands run: the host's checkout, or an agent's worktree.
type place interface {
	name() string
	// sh runs a shell command in the checkout and returns its stdout.
	sh(ctx context.Context, command string) (string, error)
	// claude is the command that runs claude -p there with these arguments.
	claude(ctx context.Context, args []string) *exec.Cmd
}

type hostPlace struct {
	dir    string
	binary string
}

func (h hostPlace) name() string { return "host" }

func (h hostPlace) sh(ctx context.Context, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = h.dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s: %w: %s", command, err, truncate(errb.String(), 500))
	}
	return out.String(), nil
}

func (h hostPlace) claude(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, h.binary, args...)
	cmd.Dir = h.dir
	cmd.Env = cleanEnv(os.Environ())
	return cmd
}

// cleanEnv drops what a Claude Code session the benchmark may be started
// from passes its children (CLAUDECODE, its session, its effort…), which
// would change how the claude it starts behaves, keeping logins and config.
func cleanEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "CLAUDE") && k != "CLAUDE_CODE_OAUTH_TOKEN" && k != "CLAUDE_CONFIG_DIR" &&
			!strings.HasPrefix(k, "CLAUDE_CODE_USE_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

type machinePlace struct {
	ab  agentbox
	ref string
}

func (m machinePlace) name() string { return m.ref }

func (m machinePlace) sh(ctx context.Context, command string) (string, error) {
	return m.ab.exec(ctx, m.ref, command)
}

func (m machinePlace) claude(ctx context.Context, args []string) *exec.Cmd {
	return m.ab.cmd(ctx, "exec", m.ref, "--", "claude "+shellJoin(args)+" </dev/null")
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:,+@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin quotes arguments for a shell; a long one (the prompt) goes
// through base64, so no quoting can go wrong on its way through agentbox
// exec, the VM's front end and the login shell.
func shellJoin(args []string) string {
	var out []string
	for _, a := range args {
		if len(a) > 64 {
			out = append(out, `"$(printf %s `+base64.StdEncoding.EncodeToString([]byte(a))+` | base64 -d)"`)
			continue
		}
		out = append(out, shellQuote(a))
	}
	return strings.Join(out, " ")
}

// bench is one run of the benchmark.
type bench struct {
	cfg    config
	d      *daemon
	ab     agentbox
	log    io.Writer
	out    string // the results directory
	commit string

	host    hostPlace
	machine machinePlace // the exec mode's machine
	hooks   map[string]bool

	mu      sync.Mutex
	created map[string]bool // agents to destroy at the end
	records []record
	probes  []probeRun
	runs    *os.File // runs.jsonl, appended as runs finish
}

func (b *bench) logf(format string, args ...any) {
	fmt.Fprintf(b.log, "%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func (b *bench) ref(name string) string { return b.cfg.Project + "/" + name }

const execAgent = "bench-exec"

// claudeArgs is the same claude -p in every mode: the model and effort the
// agent gets, permissions bypassed as an autonomous agent's are, and its
// messages as stream-json (with hooks', when this Claude Code has them).
func (b *bench) claudeArgs(prompt string, hooks bool) []string {
	args := []string{"-p", prompt, "--model", b.cfg.Model}
	if b.cfg.Effort != "" {
		args = append(args, "--effort", b.cfg.Effort)
	}
	args = append(args, "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose")
	if hooks {
		args = append(args, "--include-hook-events")
	}
	return args
}

// hookEvents says whether the claude in a place takes --include-hook-events.
func (b *bench) hookEvents(ctx context.Context, p place) bool {
	b.mu.Lock()
	v, ok := b.hooks[p.name()]
	b.mu.Unlock()
	if ok {
		return v
	}
	var help string
	if hp, isHost := p.(hostPlace); isHost {
		out, _ := exec.CommandContext(ctx, hp.binary, "--help").CombinedOutput()
		help = string(out)
	} else {
		help, _ = p.sh(ctx, "claude --help 2>&1")
	}
	v = strings.Contains(help, "--include-hook-events")
	b.mu.Lock()
	b.hooks[p.name()] = v
	b.mu.Unlock()
	return v
}

// setup writes the fixture, the host's checkout, and with AgentBox in the
// run, the project and the exec mode's machine.
func (b *bench) setup(ctx context.Context) error {
	fixture := filepath.Join(b.cfg.Dir, "fixture")
	commit, err := ensureFixture(fixture)
	if err != nil {
		return err
	}
	b.commit = commit
	b.logf("fixture %s at %s", fixture, commit)
	if b.cfg.has(modeHost) {
		dir := filepath.Join(b.cfg.Dir, "work", "host")
		if fixtureHead(dir) == "" {
			_ = os.RemoveAll(dir)
			if _, err := git(b.cfg.Dir, nil, "clone", "-q", fixture, dir); err != nil {
				return err
			}
		}
		b.host = hostPlace{dir: dir, binary: b.cfg.Claude}
		if _, err := b.host.sh(ctx, "git fetch -q origin && bench/reset.sh "+commit); err != nil {
			return err
		}
	}
	if !b.cfg.needsDaemon() {
		return nil
	}
	if err := b.ensureProject(ctx, fixture); err != nil {
		return err
	}
	if b.cfg.needsMachine() {
		b.logf("creating %s, the machine the exec mode runs claude -p in", b.ref(execAgent))
		if err := b.createAgent(ctx, execAgent, "", "claude"); err != nil {
			return fmt.Errorf("creating %s: %w", b.ref(execAgent), err)
		}
		b.machine = machinePlace{ab: b.ab, ref: b.ref(execAgent)}
	}
	return nil
}

func (b *bench) ensureProject(ctx context.Context, fixture string) error {
	ps, err := b.d.projects(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, p := range ps {
		if p.Name == b.cfg.Project {
			if filepath.Clean(p.Root) != filepath.Clean(fixture) {
				return fmt.Errorf("project %s is %s, not the fixture %s: pick another name with -project", p.Name, p.Root, fixture)
			}
			found = true
		}
	}
	if !found {
		b.logf("adding project %s (%s)", b.cfg.Project, fixture)
		if err := b.d.do(ctx, "POST", "/v1/projects", map[string]any{"path": fixture, "name": b.cfg.Project}, nil); err != nil {
			return err
		}
	}
	// Its agents' finishing wakes no chat, they don't push, and its history
	// isn't distilled: the benchmark spends no tokens outside its runs.
	off, no := "off", false
	zero := 0
	return b.d.do(ctx, "PATCH", "/v1/projects/"+b.cfg.Project, map[string]any{
		"finishNotices": &off, "agentPRs": &no, "consolidation": &zero,
	}, nil)
}

// createAgent creates one of the benchmark's agents and waits for its
// machine. An earlier run's agent of the same name is destroyed first.
func (b *bench) createAgent(ctx context.Context, name, prompt, ai string) error {
	if _, err := b.d.agent(ctx, b.cfg.Project, name); err == nil {
		_ = b.d.destroy(ctx, b.cfg.Project, name)
	}
	req := b.createRequest(name, prompt, ai)
	b.track(name, true)
	return b.d.createAgent(ctx, req, b.cfg.Poll)
}

func (b *bench) createRequest(name, prompt, ai string) createRequest {
	none := []string{}
	req := createRequest{
		Project: b.cfg.Project, Name: name, Title: "Speed benchmark", Branch: name, Task: prompt, AI: ai,
		From: b.commit, NoEnv: true, FinishNotice: "off", Connectors: &none,
	}
	if ai == "claude" {
		req.Model = &b.cfg.Model
		if b.cfg.Effort != "" {
			req.Effort = &b.cfg.Effort
		}
	}
	return req
}

func (b *bench) track(name string, on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if on {
		b.created[name] = true
	} else {
		delete(b.created, name)
	}
}

func (b *bench) destroy(ctx context.Context, name string) {
	if b.cfg.Keep {
		return
	}
	if err := b.d.destroy(ctx, b.cfg.Project, name); err != nil {
		b.logf("destroying %s: %v", b.ref(name), err)
		return
	}
	b.track(name, false)
}

// cleanup destroys every agent the benchmark made that is still there.
func (b *bench) cleanup() {
	if b.d == nil || b.cfg.Keep {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	b.mu.Lock()
	var names []string
	for n := range b.created {
		names = append(names, n)
	}
	b.mu.Unlock()
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Go(func() { b.destroy(ctx, n) })
	}
	wg.Wait()
}

// probe runs bench/probe.sh in a place.
func probe(ctx context.Context, p place) *probeEnv {
	out, err := p.sh(ctx, "bench/probe.sh")
	if err != nil {
		return nil
	}
	var e probeEnv
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &e) != nil {
		return nil
	}
	return &e
}

func verify(ctx context.Context, p place, t task) (verifyResult, error) {
	out, err := p.sh(ctx, "bench/verify.sh "+t.Name)
	var v verifyResult
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); jerr != nil {
		if err == nil {
			err = fmt.Errorf("verify.sh: %w", jerr)
		}
		return v, err
	}
	return v, nil
}

// streamClaude runs claude -p, stamping each line as it comes and keeping
// them in raw, and returns when it started and ended.
func streamClaude(cmd *exec.Cmd, raw string) (spawned, exited time.Time, err error) {
	f, err := os.Create(raw)
	if err != nil {
		return spawned, exited, err
	}
	defer f.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return spawned, exited, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 64 << 10}
	spawned = time.Now()
	if err := cmd.Start(); err != nil {
		return spawned, time.Now(), err
	}
	r := bufio.NewReaderSize(stdout, 1<<20)
	w := bufio.NewWriter(f)
	for {
		line, rerr := r.ReadBytes('\n')
		at := time.Now()
		if l := bytes.TrimSpace(line); len(l) > 0 {
			ev := json.RawMessage(l)
			if !json.Valid(l) {
				ev, _ = json.Marshal(string(l))
			}
			s, _ := json.Marshal(stamped{At: at, Event: ev})
			w.Write(append(s, '\n'))
		}
		if rerr != nil {
			break
		}
	}
	err = cmd.Wait()
	exited = time.Now()
	w.Flush()
	if stderr.Len() > 0 {
		_ = os.WriteFile(raw+".stderr", stderr.Bytes(), 0o644)
	}
	if err != nil {
		err = fmt.Errorf("%w: %s", err, truncate(stderr.String(), 500))
	}
	return spawned, exited, err
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		q = q[:l.n]
	}
	l.n -= len(q)
	_, err := l.w.Write(q)
	return len(p), err
}

// runStream is a run of the host or exec mode: reset the checkout, probe it,
// run claude -p with the task's prompt and check what it did.
func (b *bench) runStream(ctx context.Context, rec *record, p place, t task, raw string) {
	ctx, cancel := context.WithTimeout(ctx, b.cfg.Timeout)
	defer cancel()
	if _, err := p.sh(ctx, "bench/reset.sh "+b.commit); err != nil {
		rec.Error = "reset: " + err.Error()
		return
	}
	before := probe(ctx, p)
	cmd := p.claude(ctx, b.claudeArgs(t.Prompt, b.hookEvents(ctx, p)))
	spawned, exited, err := streamClaude(cmd, filepath.Join(raw, "stream.jsonl"))
	rec.Began, rec.Ended = spawned, exited
	rec.WallMS = ms(exited.Sub(spawned))
	if err != nil {
		rec.Error = "claude: " + err.Error()
		if ctx.Err() != nil && rec.Mode == modeExec {
			// Killing agentbox exec needn't stop what it started.
			stop, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, _ = p.sh(stop, "pkill -f 'claude -p' || true")
			cancel()
		}
	}
	account(rec, raw)
	if rec.Breakdown.IsError && rec.Error == "" {
		rec.Error = "claude: " + rec.Breakdown.Result
	}
	b.finish(ctx, rec, p, t, before)
}

// account works out where a run's time went from the raw files it left in
// raw: claude's stream (host, exec), or the agent's chat and the transcript
// from its machine (agent). The benchmark runs it after each run, and the
// report command again, so that runs can be looked at anew without being
// made again.
func account(rec *record, raw string) {
	rec.SetupMS, rec.StartupMS, rec.TailMS = 0, 0, 0
	if rec.Mode != modeAgent {
		f, err := os.Open(filepath.Join(raw, "stream.jsonl"))
		if err != nil {
			return
		}
		defer f.Close()
		events, _ := readStamped(f)
		bd := analyze(events)
		rec.Breakdown, rec.Source = bd, "stream"
		if !bd.Start.IsZero() && !rec.Began.IsZero() {
			rec.StartupMS = ms(bd.Start.Sub(rec.Began))
			rec.TailMS = ms(rec.Ended.Sub(bd.End))
		}
		return
	}
	var thread chatThread
	if b, err := os.ReadFile(filepath.Join(raw, "chat.json")); err == nil {
		_ = json.Unmarshal(b, &thread)
	}
	taskAt, endedAt := turnSpan(thread)
	if taskAt.IsZero() || endedAt.IsZero() {
		return
	}
	// The daemon's clock against the benchmark's: the agent's turn by the
	// former, the whole run by the latter.
	rec.SetupMS = rec.WallMS - ms(endedAt.Sub(taskAt))
	if f, err := os.Open(filepath.Join(raw, "transcript.jsonl")); err == nil {
		events, _ := readTranscript(f)
		f.Close()
		rec.Breakdown, rec.Source = analyze(events), "transcript"
	}
	if rec.Breakdown.Start.IsZero() {
		rec.Breakdown, rec.Source = chatBreakdown(thread, taskAt, endedAt), "chat"
	}
	if bd := rec.Breakdown; !bd.Start.IsZero() {
		rec.StartupMS = ms(bd.Start.Sub(taskAt))
		rec.TailMS = ms(endedAt.Sub(bd.End))
	}
}

// finish checks what a run did, and takes what check.sh measured; probed is
// the place's probe, for a task that doesn't run check.sh.
func (b *bench) finish(ctx context.Context, rec *record, p place, t task, probed *probeEnv) {
	v, err := verify(ctx, p, t)
	if err != nil {
		if rec.Error == "" {
			rec.Error = "verify: " + err.Error()
		}
		return
	}
	rec.OK, rec.Reason, rec.Checks = v.OK, v.Reason, v.Checks
	for _, c := range v.Checks {
		rec.CheckMS += c.TotalMS
	}
	rec.Env = probed
	if len(v.Checks) > 0 {
		e := v.Checks[0].Env
		rec.Env = &e
	}
}

// runAgent is a run of the agent mode: an agent created through the API with
// the prompt as its task, timed until its turn ended and it was done.
func (b *bench) runAgent(ctx context.Context, rec *record, t task, raw string) {
	ctx, cancel := context.WithTimeout(ctx, b.cfg.Timeout)
	defer cancel()
	name := fmt.Sprintf("bench-%s-l%d-r%d", t.Name, rec.Load, rec.Run)
	rec.Agent = b.ref(name)
	if _, err := b.d.agent(ctx, b.cfg.Project, name); err == nil {
		_ = b.d.destroy(ctx, b.cfg.Project, name)
	}
	b.track(name, true)
	defer b.destroy(context.WithoutCancel(ctx), name)
	start := time.Now()
	rec.Began = start
	if err := b.d.createAgent(ctx, b.createRequest(name, t.Prompt, "claude"), b.cfg.Poll); err != nil {
		rec.Error = "create: " + err.Error()
		rec.Ended = time.Now()
		rec.WallMS = ms(rec.Ended.Sub(start))
		return
	}
	thread, err := b.waitDone(ctx, name)
	rec.Ended = time.Now()
	rec.WallMS = ms(rec.Ended.Sub(start))
	if j, jerr := json.MarshalIndent(thread, "", " "); jerr == nil {
		_ = os.WriteFile(filepath.Join(raw, "chat.json"), j, 0o644)
	}
	if err != nil {
		rec.Error = err.Error()
	}
	p := machinePlace{ab: b.ab, ref: b.ref(name)}
	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer scancel()
	// The agent has started on its task by the time its machine takes a
	// command, so this probe comes after; check.sh's own, when the task runs
	// it, is what its builds saw.
	after := probe(sctx, p)
	if transcript, err := p.sh(sctx, transcriptCommand); err == nil && strings.TrimSpace(transcript) != "" {
		_ = os.WriteFile(filepath.Join(raw, "transcript.jsonl"), []byte(transcript), 0o644)
	}
	account(rec, raw)
	b.finish(sctx, rec, p, t, after)
}

// transcriptCommand prints the newest Claude Code session transcript of the
// worktree it runs in: the agent's chat, in a machine that has had no other.
const transcriptCommand = `d="$HOME/.claude/projects/$(pwd | sed 's/[^a-zA-Z0-9]/-/g')"; ` +
	`f=$(ls -t "$d"/*.jsonl 2>/dev/null | head -n 1); ` +
	`[ -n "$f" ] || f=$(ls -t "$HOME"/.claude/projects/*/*.jsonl 2>/dev/null | head -n 1); ` +
	`[ -n "$f" ] && cat "$f"`

// waitDone polls an agent's chat until its task's turn, and any it woke for,
// ended with nothing left running.
func (b *bench) waitDone(ctx context.Context, name string) (chatThread, error) {
	var last chatThread
	for {
		t, err := b.d.chat(ctx, b.cfg.Project, name)
		if err == nil {
			last = t
			if done, failed := turnDone(t); done {
				if failed != "" {
					return t, errors.New(failed)
				}
				return t, nil
			}
		}
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("not done after %s (chat %s)", b.cfg.Timeout, last.Session.State)
		case <-time.After(b.cfg.Poll):
		}
	}
}

// turnDone says whether an agent is done with its task: it has had a turn,
// every turn ended, and its chat is neither running nor waiting on work it
// left in the background. failed says why, when it stopped on something.
func turnDone(t chatThread) (done bool, failed string) {
	switch t.Session.State {
	case "error":
		return true, "chat error: " + t.Session.Error
	case "waiting":
		return true, "waiting for an answer"
	case "running", "starting", "":
		return false, ""
	}
	var last *chatItem
	for i := range t.Items {
		it := &t.Items[i]
		if it.Kind != "user" || it.Hidden {
			continue
		}
		if it.Result == nil {
			return false, ""
		}
		last = it
	}
	if last == nil || len(t.Session.Background) > 0 || len(last.Result.Background) > 0 {
		return false, ""
	}
	if last.Result.State == "failed" {
		return true, "its turn failed (" + last.Result.StopReason + ")"
	}
	return true, ""
}

// turnSpan is from the task's message to the end of the last turn, on the
// daemon's clock.
func turnSpan(t chatThread) (taskAt, endedAt time.Time) {
	for _, it := range t.Items {
		if it.Kind != "user" || it.Hidden {
			continue
		}
		if taskAt.IsZero() {
			taskAt = it.CreatedAt
		}
		if it.Result != nil && it.Result.EndedAt.After(endedAt) {
			endedAt = it.Result.EndedAt
		}
	}
	return taskAt, endedAt
}

// chatBreakdown is the breakdown from the chat's own items, for when the
// transcript can't be read: a tool call lasts from when the chat showed it
// to its last update.
func chatBreakdown(t chatThread, from, to time.Time) breakdown {
	b := breakdown{Tools: map[string]toolStat{}, Start: from, End: to}
	if from.IsZero() || to.IsZero() {
		return b
	}
	var all, builds []span
	for _, it := range t.Items {
		switch {
		case it.Kind == "assistant":
			b.Turns++ // messages, not model calls: the chat doesn't say
		case it.Kind == "tool" && it.Tool != nil:
			s := span{it.CreatedAt, it.UpdatedAt}
			all = append(all, s)
			name := it.Tool.Name
			if name == "" {
				name = it.Tool.Title
			}
			st := b.Tools[name]
			st.Calls++
			st.MS += ms(s.to.Sub(s.from))
			b.Tools[name] = st
			b.ToolCalls++
			if it.Tool.Command != "" && buildCommand.MatchString(it.Tool.Command) {
				builds = append(builds, s)
				b.Builds = append(b.Builds, buildCall{Command: truncate(it.Tool.Command, 200), MS: ms(s.to.Sub(s.from))})
			}
		}
	}
	tool := union(all, from, to)
	b.ActiveMS = ms(to.Sub(from))
	b.ToolMS = ms(tool)
	b.ModelMS = b.ActiveMS - b.ToolMS
	b.BuildToolMS = ms(union(builds, from, to))
	return b
}

// bare runs check.sh on its own in a place: how long the build takes there
// with no model in the way.
func (b *bench) bare(ctx context.Context, p place, kind string, load, run int) probeRun {
	r := probeRun{Kind: kind, Load: load, Run: run, At: time.Now()}
	ctx, cancel := context.WithTimeout(ctx, b.cfg.Timeout)
	defer cancel()
	if _, err := p.sh(ctx, "bench/reset.sh "+b.commit); err != nil {
		r.Error = err.Error()
		return r
	}
	start := time.Now()
	out, err := p.sh(ctx, "./check.sh >/dev/null 2>&1; cat .bench/check-1.json")
	r.WallMS = ms(time.Since(start))
	if err != nil {
		r.Error = err.Error()
		return r
	}
	var c checkRun
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		r.Error = "check.sh left no measurement: " + err.Error()
		return r
	}
	r.Check, r.Env = &c, &c.Env
	return r
}

// rtt times a command that does nothing, through agentbox exec: what every
// command into the machine costs before it starts.
func (b *bench) rtt(ctx context.Context, load, run int) probeRun {
	r := probeRun{Kind: "exec-rtt", Load: load, Run: run, At: time.Now()}
	start := time.Now()
	if _, err := b.machine.sh(ctx, "true"); err != nil {
		r.Error = err.Error()
	}
	r.WallMS = ms(time.Since(start))
	return r
}

// loadCommand is what each load agent runs, detached so that agentbox exec
// returns: busy loops, as many as its machine has CPUs (or -load-procs), or
// check.sh over and over from a cold cache.
func (b *bench) loadCommand() string {
	var body string
	if b.cfg.LoadKind == "build" {
		body = "while :; do bench/reset.sh " + b.commit + "; ./check.sh; done"
	} else {
		n := "$(nproc)"
		if b.cfg.LoadProcs > 0 {
			n = fmt.Sprint(b.cfg.LoadProcs)
		}
		body = "n=" + n + "; i=0; while [ $i -lt $n ]; do (while :; do :; done) & i=$((i+1)); done; wait"
	}
	return "setsid -f sh -c " + shellQuote(body) + " >/dev/null 2>&1 </dev/null"
}

func loadName(i int) string { return fmt.Sprintf("bench-load-%d", i) }

// startLoad makes n agents and sets each of them busy.
func (b *bench) startLoad(ctx context.Context, n int) error {
	if n == 0 {
		return nil
	}
	b.logf("load %d: creating %d agents running %s", n, n, b.cfg.LoadKind)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			name := loadName(i + 1)
			if err := b.createAgent(ctx, name, "", "none"); err != nil {
				errs[i] = fmt.Errorf("creating %s: %w", b.ref(name), err)
				return
			}
			if _, err := b.ab.exec(ctx, b.ref(name), b.loadCommand()); err != nil {
				errs[i] = fmt.Errorf("starting the load in %s: %w", b.ref(name), err)
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	b.logf("load %d: running; waiting %s for it to settle", n, b.cfg.LoadWarmup)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(b.cfg.LoadWarmup):
	}
	return nil
}

func (b *bench) stopLoad(ctx context.Context, n int) {
	if n == 0 || b.cfg.Keep {
		return
	}
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { b.destroy(ctx, loadName(i+1)) })
	}
	wg.Wait()
}

// rotate is the modes in the order run i takes them: each run starts with a
// different one, so that none always goes first (onto a cold prompt cache) or
// last.
func rotate(modes []string, i int) []string {
	if len(modes) == 0 {
		return nil
	}
	k := i % len(modes)
	return append(slices.Clone(modes[k:]), modes[:k]...)
}

func (b *bench) save(rec record) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records = append(b.records, rec)
	if b.runs != nil {
		j, _ := json.Marshal(rec)
		b.runs.Write(append(j, '\n'))
	}
}

func (b *bench) saveProbe(p probeRun) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probes = append(b.probes, p)
	if b.runs != nil {
		j, _ := json.Marshal(map[string]any{"probe": p})
		b.runs.Write(append(j, '\n'))
	}
}

// run is the whole benchmark, load level by load level.
func (b *bench) run(ctx context.Context) error {
	for _, load := range b.cfg.Loads {
		if err := b.startLoad(ctx, load); err != nil {
			b.stopLoad(context.WithoutCancel(ctx), load)
			return err
		}
		err := b.runLevel(ctx, load)
		b.stopLoad(context.WithoutCancel(ctx), load)
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *bench) runLevel(ctx context.Context, load int) error {
	if b.cfg.needsMachine() {
		if err := b.d.ensureRunning(ctx, b.cfg.Project, execAgent); err != nil {
			return fmt.Errorf("%s isn't running: %w", b.machine.ref, err)
		}
		for i := range 5 {
			b.saveProbe(b.rtt(ctx, load, i+1))
		}
	}
	if slices.Contains(b.cfg.Tasks, "build") {
		for i := range b.cfg.BareRuns {
			if b.cfg.has(modeHost) {
				p := b.bare(ctx, b.host, "bare-host", load, i+1)
				b.logf("load %d: check.sh alone on the host: %.1fs %s", load, p.WallMS/1000, p.Error)
				b.saveProbe(p)
			}
			if b.cfg.needsMachine() {
				p := b.bare(ctx, b.machine, "bare-machine", load, i+1)
				b.logf("load %d: check.sh alone in %s: %.1fs %s", load, b.machine.ref, p.WallMS/1000, p.Error)
				b.saveProbe(p)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	for _, tn := range b.cfg.Tasks {
		t := tasks[tn]
		for run := range b.cfg.Runs {
			for _, mode := range rotate(b.cfg.Modes, run) {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				rec := record{Mode: mode, Task: tn, Load: load, Run: run + 1, StartedAt: time.Now()}
				rec.Raw = filepath.Join("raw", fmt.Sprintf("%s-l%d-%s-r%d", tn, load, mode, run+1))
				raw := filepath.Join(b.out, rec.Raw)
				if err := os.MkdirAll(raw, 0o755); err != nil {
					return err
				}
				switch mode {
				case modeHost:
					b.runStream(ctx, &rec, b.host, t, raw)
				case modeExec:
					if err := b.d.ensureRunning(ctx, b.cfg.Project, execAgent); err != nil {
						rec.Error = "starting " + b.machine.ref + ": " + err.Error()
						break
					}
					b.runStream(ctx, &rec, b.machine, t, raw)
				case modeAgent:
					b.runAgent(ctx, &rec, t, raw)
				}
				b.logf("%s load %d %s run %d: %.1fs (model %.1fs, tools %.1fs, build %.1fs) ok=%v %s%s",
					tn, load, mode, run+1, rec.WallMS/1000, rec.Breakdown.ModelMS/1000, rec.Breakdown.ToolMS/1000,
					rec.Breakdown.BuildToolMS/1000, rec.OK, rec.Reason, prefixed(" error: ", rec.Error))
				b.save(rec)
			}
		}
	}
	return nil
}

func prefixed(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}
