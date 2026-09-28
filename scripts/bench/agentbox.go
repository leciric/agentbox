package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// benchProject is the project every agentbox-driven mode registers, from a
// clone of the repository: its agents, branches and worktrees are the
// harness's own, and nothing of the user's is touched.
const benchProject = "agentbox-bench"

// agentboxDriver drives AgentBox through its own command-line tool, as a user
// would: today's containers on the host ("containers"), or the Cloud
// Hypervisor VM through agentbox's front end ("vm"), which forwards the same
// commands into the VM.
type agentboxDriver struct {
	mode string
	o    *options
	work string // this mode's directory under the cache: the project's clone
	env  []string
	r    *runner

	// vm mode only
	reuseVM bool // the VM was there before the run: it isn't set up, or deleted, by the harness

	mu        sync.Mutex
	instances map[string]string // agent → Incus instance, for its cgroup
	cgroups   map[string]string // instance → its memory.current
}

func newAgentboxDriver(mode string, o *options) *agentboxDriver {
	d := &agentboxDriver{mode: mode, o: o, work: filepath.Join(o.cache, mode), instances: map[string]string{}, cgroups: map[string]string{}}
	d.env = append(d.env, "AGENTBOX="+o.agentbox, "AGENTBOX_NO_UPDATE_CHECK=1")
	if mode == "vm" {
		d.env = append(d.env, strings.Fields(o.vmEnv)...)
	}
	d.r = &runner{env: d.env}
	return d
}

func (d *agentboxDriver) name() string        { return d.mode }
func (d *agentboxDriver) setRunner(r *runner) { r.env = d.env; d.r = r }

func (d *agentboxDriver) describe() map[string]any {
	c := map[string]any{"agentbox": d.o.agentbox, "project": benchProject, "repo": d.o.repo}
	if out, err := d.r.run(context.Background(), "", d.o.agentbox, "version"); err == nil {
		c["version"] = strings.TrimSpace(out)
	}
	if d.mode == "vm" {
		c["env"] = d.o.vmEnv
		c["commands"] = map[string]any{"setup": d.o.vmSetup, "start": d.o.vmStart, "stop": d.o.vmStop,
			"pause": d.o.vmPause, "resume": d.o.vmResume, "delete": d.o.vmDelete, "status": d.o.vmStatus, "process": d.o.vmProcess, "shell": d.o.vmShell}
	}
	return c
}

func (d *agentboxDriver) ab(ctx context.Context, args ...string) (string, error) {
	return d.r.run(ctx, "", append([]string{d.o.agentbox}, args...)...)
}

func (d *agentboxDriver) preflight(ctx context.Context) error {
	if _, err := d.ab(ctx, "version"); err != nil {
		return fmt.Errorf("can't run %s (pass --agentbox): %w", d.o.agentbox, err)
	}
	if d.mode == "vm" {
		exists, err := d.vmExists(ctx)
		if err != nil {
			return fmt.Errorf("the VM front end doesn't answer (%s): %w; is agentbox/feat-cloud-hypervisor-vm's agentbox the one on --agentbox, and --vm-env right?", d.o.vmStatus, err)
		}
		d.reuseVM = exists
		if exists && d.o.freshVM {
			return errors.New("the VM already exists, and --vm-fresh wants to set one up from nothing: the harness never deletes a VM it didn't make, so delete it yourself first or drop --vm-fresh")
		}
	} else if _, err := d.ab(ctx, "projects"); err != nil {
		return fmt.Errorf("the AgentBox daemon doesn't answer: %w", err)
	}
	if d.mode != "vm" || d.reuseVM {
		var ps []struct{ Name string }
		if err := d.api(ctx, "/v1/projects", &ps); err != nil {
			return fmt.Errorf("listing projects: %w", err)
		}
		for _, p := range ps {
			if p.Name == benchProject {
				return fmt.Errorf("project %s is already there, left by an earlier run: run %s --cleanup --modes %s first", benchProject, os.Args[0], d.mode)
			}
		}
	}
	return checkFree(d.work, d.o.writeGiB+20)
}

func (d *agentboxDriver) setup(ctx context.Context, m *modeRun) error {
	if d.mode == "containers" {
		// Today's install is `sudo agentbox host setup` and `agentbox image
		// build`. Neither is run again: the build would replace the base image
		// the user's own agents are copied from. The daemon keeps its jobs, so
		// the last image build on this host says how long that step took.
		m.recordStep("setup.step", "sudo agentbox host setup (not re-run: it installs Incus and changes the host)", 0, "not timed")
		var jobs []struct {
			Kind       string     `json:"kind"`
			Status     string     `json:"status"`
			CreatedAt  time.Time  `json:"createdAt"`
			FinishedAt *time.Time `json:"finishedAt"`
		}
		secs, note := 0.0, "no image build in the daemon's job history: not timed"
		if err := d.api(ctx, "/v1/jobs", &jobs); err == nil {
			var last time.Time
			for _, j := range jobs {
				if j.Kind == "image-build" && j.Status == "succeeded" && j.FinishedAt != nil && j.CreatedAt.After(last) {
					last = j.CreatedAt
					secs = j.FinishedAt.Sub(j.CreatedAt).Seconds()
					note = "image build time is the last one in the daemon's job history (" + last.Local().Format("2006-01-02 15:04") + "), not re-run: it would replace your base image"
				}
			}
		}
		m.recordStep("setup.step", "agentbox image build", secs, note)
		m.note("setup.total", note)
	} else if d.reuseVM {
		m.recordStep("setup.step", "VM set-up (not timed: the VM was already there)", 0, "not timed")
		m.note("setup.total", "the VM was already there, so its set-up wasn't timed; its boot is")
	} else {
		if err := os.MkdirAll(d.work, 0o755); err != nil {
			return err
		}
		// A marker, so that cleanup only ever deletes a VM this harness made,
		// even after a run that was killed.
		if err := os.WriteFile(filepath.Join(d.work, "vm-made-by-bench"), nil, 0o644); err != nil {
			return err
		}
		for _, line := range d.o.vmSetup {
			if _, err := m.setupStep(ctx, line, func(ctx context.Context) error { _, err := d.r.sh(ctx, line); return err }); err != nil {
				return err
			}
		}
	}
	clone := filepath.Join(d.work, "repo")
	if err := m.step0(ctx, "clone the repository for the bench project", func(ctx context.Context) error {
		if err := os.RemoveAll(clone); err != nil {
			return err
		}
		_, err := d.r.run(ctx, "", "git", "clone", "-q", d.o.repo, clone)
		return err
	}); err != nil {
		return err
	}
	if _, err := m.setupStep(ctx, "agentbox add (the project)", func(ctx context.Context) error {
		_, err := d.ab(ctx, "add", clone, "--name", benchProject)
		return err
	}); err != nil {
		return err
	}
	// Nothing of the bench project should reach a model: no consolidation of
	// its events.
	_, _ = d.ab(ctx, "consolidation", benchProject, "off")
	return nil
}

// ownsVM says whether the harness may shut the VM down and delete it: only
// one it set up itself.
func (d *agentboxDriver) ownsVM() bool { return d.mode == "vm" && !d.reuseVM }

func (d *agentboxDriver) vm() vmOps {
	if d.mode != "vm" {
		return nil
	}
	return d
}

func (d *agentboxDriver) boot(ctx context.Context) error {
	if _, err := d.r.sh(ctx, d.o.vmStart); err != nil {
		return err
	}
	return d.waitDaemon(ctx)
}

func (d *agentboxDriver) shutdown(ctx context.Context) error {
	_, err := d.r.sh(ctx, d.o.vmStop)
	return err
}

func (d *agentboxDriver) pause(ctx context.Context) error {
	_, err := d.r.sh(ctx, d.o.vmPause)
	return err
}

// resume counts until the daemon in the VM answers again, which is what the
// app waits for.
func (d *agentboxDriver) resume(ctx context.Context) error {
	if _, err := d.r.sh(ctx, d.o.vmResume); err != nil {
		return err
	}
	return d.waitDaemon(ctx)
}

func (d *agentboxDriver) waitDaemon(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for {
		var ps []struct{ Name string }
		err := d.api(ctx, "/v1/projects", &ps)
		if err == nil {
			return nil
		}
		if sleepCtx(ctx, 100*time.Millisecond) != nil {
			return fmt.Errorf("the daemon didn't answer: %w", err)
		}
	}
}

// guestCounters reads the VM's own page cache and memory pressure counters,
// in vm mode, when there's a way into it.
func (d *agentboxDriver) guestCounters(ctx context.Context) (vmCounters, error) {
	if d.mode != "vm" || d.o.vmShell == "" {
		return vmCounters{}, errors.New("no way into the VM")
	}
	out, err := d.r.sh(ctx, d.o.vmShell+" sh -c "+shq(vmCountersScript))
	return parseGuestCounters(out), err
}

func (d *agentboxDriver) vmExists(ctx context.Context) (bool, error) {
	out, err := d.r.sh(ctx, d.o.vmStatus)
	if err != nil {
		return false, err
	}
	var st struct {
		Exists bool `json:"exists"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return false, fmt.Errorf("reading %q: %w", strings.TrimSpace(out), err)
	}
	return st.Exists, nil
}

func (d *agentboxDriver) ref(agent string) string { return benchProject + "/" + agent }

func (d *agentboxDriver) create(ctx context.Context, agent string) error {
	if _, err := d.ab(ctx, "create", benchProject, "--name", agent, "--ai", "none", "--no-env"); err != nil {
		return err
	}
	var a struct {
		Instance string `json:"instance"`
	}
	if err := d.api(ctx, "/v1/agents/"+benchProject+"/"+agent, &a); err == nil && a.Instance != "" {
		d.mu.Lock()
		d.instances[agent] = a.Instance
		d.mu.Unlock()
	}
	return nil
}

func (d *agentboxDriver) action(ctx context.Context, agent, action string) error {
	_, err := d.ab(ctx, action, d.ref(agent))
	return err
}

func (d *agentboxDriver) exec(ctx context.Context, agent, script string) error {
	// agentbox exec runs its words as one command line in the worktree, so
	// the script goes as one quoted word.
	_, err := d.ab(ctx, "exec", d.ref(agent), "--", "bash -c "+shq(script))
	return err
}

func (d *agentboxDriver) held() int64 {
	if d.mode == "vm" {
		return processRSS(d.o.vmProcess, "")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var total int64
	for _, inst := range d.instances {
		path, ok := d.cgroups[inst]
		if !ok {
			path = containerCgroup(inst)
			if path == "" {
				continue
			}
			d.cgroups[inst] = path
		}
		b, err := os.ReadFile(path)
		if err != nil {
			delete(d.cgroups, inst) // stopped: found again when it starts
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		total += n
	}
	return total
}

// containerCgroup finds an Incus container's memory.current: at the cgroup
// root, or under the shared agent budget's /agentbox parent.
func containerCgroup(instance string) string {
	for _, pat := range []string{"/sys/fs/cgroup/lxc.payload.%s/memory.current", "/sys/fs/cgroup/*/lxc.payload.%s/memory.current", "/sys/fs/cgroup/*/*/lxc.payload.%s/memory.current"} {
		if m, _ := filepath.Glob(fmt.Sprintf(pat, instance)); len(m) > 0 {
			return m[0]
		}
	}
	return ""
}

func (d *agentboxDriver) cleanup(ctx context.Context) error {
	var errs []error
	var agents []struct {
		Name string `json:"name"`
	}
	projectThere := true
	if err := d.api(ctx, "/v1/agents?project="+url.QueryEscape(benchProject), &agents); err != nil {
		var he httpError
		if errors.As(err, &he) && he.code == http.StatusNotFound {
			projectThere = false
		} else if d.mode != "vm" || !d.madeVM() {
			errs = append(errs, fmt.Errorf("listing the bench agents: %w", err))
			projectThere = false
		}
	}
	for _, a := range agents {
		if _, err := d.ab(ctx, "destroy", "--force", "--delete-branch", "--delete-media", d.ref(a.Name)); err != nil {
			errs = append(errs, err)
		}
	}
	if projectThere {
		if _, err := d.ab(ctx, "remove", benchProject); err != nil && len(agents) > 0 {
			errs = append(errs, err)
		}
	}
	if d.mode == "vm" && d.madeVM() {
		if _, err := d.r.sh(ctx, d.o.vmDelete); err != nil {
			errs = append(errs, err)
		} else {
			_ = os.Remove(filepath.Join(d.work, "vm-made-by-bench"))
		}
	}
	if err := os.RemoveAll(filepath.Join(d.work, "repo")); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (d *agentboxDriver) madeVM() bool {
	_, err := os.Stat(filepath.Join(d.work, "vm-made-by-bench"))
	return err == nil
}

// socket is the daemon's API socket, as agentbox finds it: through the front
// end's forward in vm mode, at the same path.
func (d *agentboxDriver) socket() string {
	for _, kv := range slices.Backward(d.env) {
		if v, ok := strings.CutPrefix(kv, "AGENTBOX_SOCKET="); ok {
			return v
		}
	}
	if s := os.Getenv("AGENTBOX_SOCKET"); s != "" {
		return s
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "agentbox", "run", "agentbox.sock")
}

type httpError struct {
	code int
	body string
}

func (e httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.body) }

// api GETs path from the daemon's API.
func (d *agentboxDriver) api(ctx context.Context, path string, out any) error {
	sock := d.socket()
	c := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}, Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agentbox"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var b strings.Builder
		_, _ = fmt.Fprint(&b, resp.Status)
		return httpError{code: resp.StatusCode, body: b.String()}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// processRSS is the resident memory of the processes named comm, or of the
// one with pid when pid isn't "".
func processRSS(comm, pid string) int64 {
	pids := []string{pid}
	if pid == "" {
		pids = nil
		entries, _ := os.ReadDir("/proc")
		for _, e := range entries {
			if e.Name()[0] < '0' || e.Name()[0] > '9' {
				continue
			}
			b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
			// comm is cut to 15 bytes: cloud-hypervisor is "cloud-hyperviso".
			if err == nil && strings.TrimSpace(string(b)) == comm[:min(len(comm), 15)] {
				pids = append(pids, e.Name())
			}
		}
	}
	var total int64
	for _, p := range pids {
		b, err := os.ReadFile(filepath.Join("/proc", p, "statm"))
		if err != nil {
			continue
		}
		f := strings.Fields(string(b))
		if len(f) > 1 {
			n, _ := strconv.ParseInt(f[1], 10, 64)
			total += n * int64(os.Getpagesize())
		}
	}
	return total
}
