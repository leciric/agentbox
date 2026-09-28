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
	"strconv"
	"strings"
	"sync"
	"time"
)

// benchProject is the project every agentbox-driven mode registers, from a
// clone of the repository: its agents, branches and worktrees are the
// harness's own, and nothing of the user's is touched.
const benchProject = "agentbox-bench"

// agentboxDriver drives today's AgentBox, agents as Incus containers on the
// host, through its own command-line tool, as a user would. vmDriver (vm.go)
// is the same with the Cloud Hypervisor VM's front end, and shares its agent
// operations.
type agentboxDriver struct {
	mode string
	o    *options
	work string // this mode's directory under the cache: the project's clone
	bin  string // the agentbox it runs
	sock string // the daemon's API socket, as that agentbox finds it
	r    *runner

	mu        sync.Mutex
	instances map[string]string // agent → Incus instance, for its cgroup
	cgroups   map[string]string // instance → its memory.current
}

func newAgentboxDriver(o *options) *agentboxDriver {
	d := &agentboxDriver{mode: "containers", o: o, work: filepath.Join(o.cache, "containers"), bin: o.agentbox,
		instances: map[string]string{}, cgroups: map[string]string{}}
	d.sock = defaultSocket(os.Getenv("XDG_DATA_HOME"))
	d.r = &runner{env: []string{"AGENTBOX=" + d.bin, "AGENTBOX_NO_UPDATE_CHECK=1"}}
	return d
}

func (d *agentboxDriver) name() string { return d.mode }

// setRunner takes the mode's runner, which logs, keeping this driver's
// environment.
func (d *agentboxDriver) setRunner(r *runner) {
	r.env, r.unset = d.r.env, d.r.unset
	d.r = r
}

func (d *agentboxDriver) describe() map[string]any {
	c := map[string]any{"agentbox": d.bin, "project": benchProject, "repo": d.o.repo}
	if out, err := d.r.run(context.Background(), "", d.bin, "version"); err == nil {
		c["version"] = strings.TrimSpace(out)
	}
	return c
}

func (d *agentboxDriver) ab(ctx context.Context, args ...string) (string, error) {
	return d.r.run(ctx, "", append([]string{d.bin}, args...)...)
}

func (d *agentboxDriver) preflight(ctx context.Context) error {
	if _, err := d.ab(ctx, "version"); err != nil {
		return fmt.Errorf("can't run %s (pass --agentbox): %w", d.bin, err)
	}
	if _, err := d.ab(ctx, "projects"); err != nil {
		return fmt.Errorf("the AgentBox daemon doesn't answer: %w", err)
	}
	var ps []struct{ Name string }
	if err := d.api(ctx, "/v1/projects", &ps); err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}
	for _, p := range ps {
		if p.Name == benchProject {
			return fmt.Errorf("project %s is already there, left by an earlier run: run %s --cleanup --modes %s first", benchProject, os.Args[0], d.mode)
		}
	}
	return checkFree(d.work, d.o.writeGiB+20)
}

func (d *agentboxDriver) setup(ctx context.Context, m *modeRun) error {
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
	return d.addProject(ctx, m)
}

// addProject registers a clone of the repository as the bench project.
func (d *agentboxDriver) addProject(ctx context.Context, m *modeRun) error {
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

func (d *agentboxDriver) vm() vmOps { return nil }

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
	errs := []error{d.removeProject(ctx)}
	if err := os.RemoveAll(filepath.Join(d.work, "repo")); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// removeProject destroys the bench agents and removes the bench project, if
// the daemon has them.
func (d *agentboxDriver) removeProject(ctx context.Context) error {
	var agents []struct {
		Name string `json:"name"`
	}
	if err := d.api(ctx, "/v1/agents?project="+url.QueryEscape(benchProject), &agents); err != nil {
		var he httpError
		if errors.As(err, &he) && he.code == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("listing the bench agents: %w", err)
	}
	var errs []error
	for _, a := range agents {
		if _, err := d.ab(ctx, "destroy", "--force", "--delete-branch", "--delete-media", d.ref(a.Name)); err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := d.ab(ctx, "remove", benchProject); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// defaultSocket is the daemon's API socket as agentbox finds it with
// XDG_DATA_HOME set to dataHome ("" for none).
func defaultSocket(dataHome string) string {
	if s := os.Getenv("AGENTBOX_SOCKET"); s != "" && dataHome == os.Getenv("XDG_DATA_HOME") {
		return s
	}
	if dataHome == "" {
		home, _ := os.UserHomeDir()
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "agentbox", "run", "agentbox.sock")
}

type httpError struct {
	code int
	body string
}

func (e httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.body) }

// api GETs path from the daemon's API.
func (d *agentboxDriver) api(ctx context.Context, path string, out any) error {
	sock := d.sock
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
		return httpError{code: resp.StatusCode, body: resp.Status}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// processRSS is the resident memory of the process with pid.
func processRSS(pid string) int64 {
	b, err := os.ReadFile(filepath.Join("/proc", pid, "statm"))
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.ParseInt(f[1], 10, 64)
	return n * int64(os.Getpagesize())
}
