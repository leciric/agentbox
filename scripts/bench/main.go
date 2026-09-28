// Command bench compares ways of running AgentBox's agents on one host:
// today's Incus containers on the host, AgentBox inside one Cloud Hypervisor
// VM through agentbox's front end, and a prototype of that VM driven
// directly. Each mode runs the same plan — install, VM and agent lifecycle,
// builds, memory, and the host's responsiveness under a sustained write and
// under three builds at once — while the host is sampled from here, on the
// host. It writes one markdown table and the raw JSON.
//
// scripts/bench/run.sh is the way to run it; README.md says what it needs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type options struct {
	modes                                []string
	out, cache, repo, agentbox, probeDir string
	quick, keep, cleanupOnly             bool
	writeGiB                             float64
	reps, bootReps                       int
	settle, idleWindow, writeTail        time.Duration
	reclaimWait, modeGap                 time.Duration
	skip                                 map[string]bool
	goCold, goWarm, npm                  string

	vmEnv, vmAgentbox, vmRef                     string
	vmSetup                                      []string
	vmStart, vmStop, vmPause, vmResume, vmDelete string
	vmStatus, vmShell                            string

	chMemory, chDisk, chBridge, chBinary string
	chCPUs                               int
	chBalloon                            bool
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, "; ") }
func (l *listFlag) Set(s string) error {
	*l = append(*l, s)
	return nil
}

func parseFlags(args []string) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, _ := os.UserHomeDir()
		cache = filepath.Join(home, ".cache")
	}
	var modes, skip string
	var vmSetup listFlag
	fs.StringVar(&modes, "modes", "containers,vm", "the modes to run, in order: containers (today's), vm (agentbox's Cloud Hypervisor front end), chproto (Cloud Hypervisor driven directly)")
	fs.StringVar(&o.out, "out", "", "where the results go (default bench-results/<time> in the current directory)")
	fs.StringVar(&o.cache, "cache", filepath.Join(cache, "agentbox-bench"), "downloads, the project's clone and the prototype's VM")
	fs.StringVar(&o.repo, "repo", "", "the repository the agents build (default: the one this harness is in)")
	fs.StringVar(&o.agentbox, "agentbox", "agentbox", "the agentbox command, for containers and vm")
	fs.StringVar(&o.probeDir, "probe-dir", "", "where the fsync probe writes: on the host's own filesystem (default: the cache)")
	fs.BoolVar(&o.quick, "quick", false, "a short run to check the harness works: 1 GiB write, a one-package Go build, no desktop build, short windows")
	fs.BoolVar(&o.keep, "keep", false, "leave the agents and the VM when done, to look at them (then --cleanup)")
	fs.BoolVar(&o.cleanupOnly, "cleanup", false, "only remove what an earlier run left in --modes, and exit")
	fs.Float64Var(&o.writeGiB, "write-gib", 10, "the sustained write, in GiB")
	fs.IntVar(&o.reps, "reps", 3, "rounds of each agent and VM pause/resume and stop/start: the median is kept")
	fs.IntVar(&o.bootReps, "boot-reps", 2, "rounds of VM shutdown and boot")
	fs.DurationVar(&o.settle, "settle", 15*time.Second, "wait before reading memory")
	fs.DurationVar(&o.idleWindow, "idle-window", 30*time.Second, "how long the host is sampled with nothing running")
	fs.DurationVar(&o.writeTail, "write-tail", 60*time.Second, "how long the host is still sampled after the write, for its writeback")
	fs.DurationVar(&o.reclaimWait, "reclaim-wait", 60*time.Second, "how long after the agents stop memory is read again")
	fs.DurationVar(&o.modeGap, "mode-gap", 30*time.Second, "the rest between modes")
	fs.StringVar(&skip, "skip", "", "parts to skip: build (one agent's), build3, write")
	fs.StringVar(&o.goCold, "go-cold", "go test ./...", "one agent's cold Go build")
	fs.StringVar(&o.goWarm, "go-warm", "go test -count=1 ./...", "the warm Go build: -count=1 runs the tests again rather than reading their cached results")
	fs.StringVar(&o.npm, "npm", "npm --prefix desktop ci && npm --prefix desktop run build", `the desktop build, cold then warm ("" skips it)`)

	fs.StringVar(&o.vmAgentbox, "vm-agentbox", "", "the agentbox for vm mode (default: built from --vm-ref)")
	fs.StringVar(&o.vmRef, "vm-ref", "origin/agentbox/feat-cloud-hypervisor-vm", "the commit vm mode's agentbox is built from, in --repo")
	fs.StringVar(&o.vmEnv, "vm-env", "", "more environment for agentbox in vm mode, space-separated")
	fs.Var(&vmSetup, "vm-setup", `a command that sets vm mode up from nothing, timed as one setup step; repeat it for each (default "$AGENTBOX" vm init, then "$AGENTBOX" image build)`)
	fs.StringVar(&o.vmStart, "vm-start", `"$AGENTBOX" vm start`, "starts the VM (vm mode)")
	fs.StringVar(&o.vmStop, "vm-stop", `"$AGENTBOX" vm stop`, "shuts the VM down (vm mode)")
	fs.StringVar(&o.vmPause, "vm-pause", `"$AGENTBOX" vm pause`, "pauses the VM (vm mode)")
	fs.StringVar(&o.vmResume, "vm-resume", `"$AGENTBOX" vm resume`, "resumes the VM (vm mode)")
	fs.StringVar(&o.vmDelete, "vm-delete", `"$AGENTBOX" vm delete --yes`, "deletes the VM in the harness's data directory (vm mode)")
	fs.StringVar(&o.vmStatus, "vm-status", `"$AGENTBOX" vm status --json`, `prints the VM's state, with its "memory" (vm mode)`)
	fs.StringVar(&o.vmShell, "vm-shell", `"$AGENTBOX" vm shell --`, "runs a command in the VM itself, for its page cache and memory pressure (vm mode; \"\" for none)")

	fs.StringVar(&o.chMemory, "ch-memory", "16G", "the prototype VM's memory")
	fs.IntVar(&o.chCPUs, "ch-cpus", runtime.NumCPU(), "the prototype VM's CPUs")
	fs.StringVar(&o.chDisk, "ch-disk", "direct=on", "options for the prototype VM's disks, added to Cloud Hypervisor's --disk (direct=on: O_DIRECT, no host page cache)")
	fs.BoolVar(&o.chBalloon, "ch-balloon", true, "give the prototype VM a balloon with free page reporting, so memory it frees goes back to the host")
	fs.StringVar(&o.chBridge, "ch-bridge", "", "the bridge the prototype VM's tap joins, with DHCP and NAT (default: Incus's default profile's)")
	fs.StringVar(&o.chBinary, "ch-binary", "", "a cloud-hypervisor to use rather than downloading "+chVersion)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	for m := range strings.SplitSeq(modes, ",") {
		m = strings.TrimSpace(m)
		switch m {
		case "containers", "vm", "chproto":
			o.modes = append(o.modes, m)
		case "":
		default:
			return nil, fmt.Errorf("unknown mode %q: containers, vm or chproto", m)
		}
	}
	o.skip = map[string]bool{}
	for s := range strings.SplitSeq(skip, ",") {
		if s = strings.TrimSpace(s); s != "" {
			o.skip[s] = true
		}
	}
	o.vmSetup = vmSetup
	if len(o.vmSetup) == 0 {
		o.vmSetup = []string{`"$AGENTBOX" vm init`, `"$AGENTBOX" image build`}
	}
	if o.quick {
		quick := map[string]func(){
			"write-gib":    func() { o.writeGiB = 1 },
			"boot-reps":    func() { o.bootReps = 1 },
			"settle":       func() { o.settle = 3 * time.Second },
			"idle-window":  func() { o.idleWindow = 5 * time.Second },
			"write-tail":   func() { o.writeTail = 10 * time.Second },
			"reclaim-wait": func() { o.reclaimWait = 10 * time.Second },
			"mode-gap":     func() { o.modeGap = 5 * time.Second },
			"go-cold":      func() { o.goCold = "go build -o /dev/null ./cmd/agentbox" },
			"go-warm":      func() { o.goWarm = "go build -o /dev/null ./cmd/agentbox" },
			"npm":          func() { o.npm = "" },
		}
		for name, apply := range quick {
			if !set[name] {
				apply()
			}
		}
	}
	if o.repo == "" {
		o.repo = os.Getenv("BENCH_REPO") // run.sh's: the repository it's in
	}
	if o.repo == "" {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return nil, errors.New("not in a git repository: pass --repo")
		}
		o.repo = strings.TrimSpace(string(out))
	}
	if o.out == "" {
		o.out = filepath.Join("bench-results", time.Now().Format("20060102-150405"))
	}
	if o.probeDir == "" {
		o.probeDir = filepath.Join(o.cache, "probe")
	}
	var err error
	for _, p := range []*string{&o.out, &o.cache, &o.repo, &o.probeDir} {
		if *p, err = filepath.Abs(*p); err != nil {
			return nil, err
		}
	}
	if o.agentbox != "" && !strings.Contains(o.agentbox, "/") {
		if p, err := exec.LookPath(o.agentbox); err == nil {
			o.agentbox = p
		}
	}
	return o, nil
}

func newDriver(mode string, o *options) driver {
	switch mode {
	case "chproto":
		return newCHDriver(o)
	case "vm":
		return newVMDriver(o)
	}
	return newAgentboxDriver(o)
}

type results struct {
	Host     map[string]any `json:"host"`
	Options  map[string]any `json:"options"`
	Started  time.Time      `json:"started"`
	Finished time.Time      `json:"finished"`
	Modes    []*modeResult  `json:"modes"`
}

func main() {
	o, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			fmt.Println("\nStopping: cleaning up what the harness made (Ctrl-C again to abandon the cleanup)…")
		case <-finished:
		}
	}()

	if o.cleanupOnly {
		os.Exit(cleanupOnly(o))
	}
	if err := os.MkdirAll(filepath.Join(o.out, "logs"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
	res := &results{Host: hostInfo(o.probeDir), Options: optionsJSON(o), Started: time.Now()}
	fmt.Printf("Results and logs go to %s\n", o.out)
	for i, mode := range o.modes {
		if ctx.Err() != nil {
			break
		}
		if i > 0 {
			fmt.Printf("Resting %s before the next mode…\n", o.modeGap)
			_ = sleepCtx(ctx, o.modeGap)
		}
		mr := runMode(ctx, o, newDriver(mode, o))
		res.Modes = append(res.Modes, mr)
		res.Finished = time.Now()
		if err := writeResults(o.out, res); err != nil {
			fmt.Fprintln(os.Stderr, "bench: writing results:", err)
		}
	}
	res.Finished = time.Now()
	if err := writeResults(o.out, res); err != nil {
		fmt.Fprintln(os.Stderr, "bench: writing results:", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Print(markdown(res))
	fmt.Printf("\nWritten: %s, %s, and each mode's log in %s\n",
		filepath.Join(o.out, "results.md"), filepath.Join(o.out, "results.json"), filepath.Join(o.out, "logs"))
	if ctx.Err() != nil {
		os.Exit(130)
	}
	for _, m := range res.Modes {
		if len(m.Errors) > 0 {
			os.Exit(1)
		}
	}
}

func cleanupOnly(o *options) int {
	if err := os.MkdirAll(o.cache, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		return 1
	}
	logf, err := os.OpenFile(filepath.Join(o.cache, "cleanup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		return 1
	}
	defer func() { _ = logf.Close() }()
	code := 0
	for _, mode := range o.modes {
		d := newDriver(mode, o)
		r := &runner{log: logf}
		if lr, ok := d.(interface{ setRunner(*runner) }); ok {
			lr.setRunner(r)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		if err := d.cleanup(ctx); err != nil {
			fmt.Printf("[%s] cleanup: %v (log: %s)\n", mode, err, logf.Name())
			code = 1
		} else {
			fmt.Printf("[%s] nothing of the harness's is left\n", mode)
		}
		cancel()
	}
	return code
}

// cleanupSignals gives cleanup a context that a further Ctrl-C ends.
func cleanupSignals(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-ch:
			fmt.Printf("\nAbandoning the cleanup: run %s --cleanup to finish it.\n", os.Args[0])
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() { signal.Stop(ch); cancel() }
}

func writeResults(dir string, res *results) error {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "results.json"), b, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "results.md"), []byte(markdown(res)), 0o644)
}

func optionsJSON(o *options) map[string]any {
	return map[string]any{
		"modes": o.modes, "repo": o.repo, "quick": o.quick, "writeGiB": o.writeGiB, "reps": o.reps, "bootReps": o.bootReps,
		"settle": o.settle.String(), "idleWindow": o.idleWindow.String(), "writeTail": o.writeTail.String(),
		"reclaimWait": o.reclaimWait.String(), "skip": o.skip, "goCold": o.goCold, "goWarm": o.goWarm, "npm": o.npm,
		"probeDir": o.probeDir,
	}
}

// checkFree fails unless the filesystem dir is on (or would be on) has
// wantGiB free.
func checkFree(dir string, wantGiB float64) error {
	for p := dir; ; p = filepath.Dir(p) {
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err == nil {
			free := gib(int64(st.Bavail) * st.Bsize)
			if free < wantGiB {
				return fmt.Errorf("%s has %.0f GiB free, and this mode needs about %.0f GiB", p, free, wantGiB)
			}
			return nil
		}
		if p == "/" {
			return nil
		}
	}
}
