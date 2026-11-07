//go:build pressurelab

package agent

// The load lab: many real agents, made by a real daemon, all working at once,
// measured from outside them the way the user's desktop and the app feel it.
// It is the repeatable run resource work is compared on: the same mix, the
// same length, a report in the same shape every time.
//
// It needs a daemon of its own running on this machine with Incus (a project
// with nesting on, or scripts/pressurelab.sh, which sets one up), and root,
// to read the agents' cgroups:
//
//	sudo -E env PATH=$PATH LAB_SOCKET=/home/lab/.local/share/agentbox/run/agentbox.sock \
//	    go test -tags pressurelab -run TestLoadLab -v -timeout 3h ./internal/agent
//
// It makes LAB_AGENTS agents (30) named load-01… in LAB_PROJECT ("loadlab",
// added from LAB_REPO when missing), prepares their work, then for
// LAB_DURATION (10m) has each run one kind of work in a loop, LAB_MIX's
// shares of them ("go=30,cargo=20,npm=30,idle=20"):
//
//   - go: go test -count=1 ./... in its worktree (LAB_REPO's tests);
//   - cargo: cargo clean, then cargo build of a crate with common dependencies;
//   - npm: rm -rf node_modules, npm install, then vitest run;
//   - idle: nothing beyond the agent's own AI tool sitting there.
//
// LAB_GATE is "hook" (the default) or "none". With "hook", what Claude Code's
// heavy hook would hold (go test, cargo build, vitest: cli.isHeavy; npm
// install isn't one) runs as `agentbox heavy -- …`, which asks the daemon
// exactly as the hook does; with "none" everything runs as the other four AI
// tools run it, without asking.
//
// While the work runs it measures: the daemon's API (GET /v1/agents every
// second, a chat message every LAB_CHAT_EVERY, the event stream's lag), a
// stand-in for the user's desktop outside every agent (write+fsync and a 10ms
// timer's lateness every 100ms, its thread's run-queue wait, and CPU, memory
// and IO PSI), memory over time, the heavy-command gate (GET /v1/pressure
// every second, and the daemon's log when LAB_DAEMON_LOG names it), OOM kills
// at and beyond each agent's limit, each agent's disk reads and writes, and
// the disk space used. It writes report.json and report.md to LAB_OUT
// (./loadlab-<time>), and fails when a target is missed: API p99 under
// LAB_P99_MS (200), no kill outside an agent's own limit.
//
// LAB_KEEP=1 keeps the agents for the next run (their preparation is then
// skipped); otherwise they are destroyed at the end.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"agentbox/internal/api"
)

func TestLoadLab(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root, to read the agents' cgroups and run in them")
	}
	socket := os.Getenv("LAB_SOCKET")
	if socket == "" {
		t.Skip("LAB_SOCKET names the lab daemon's socket (scripts/pressurelab.sh sets one up)")
	}
	cfg := loadConfig{
		agents:   labInt("LAB_AGENTS", 30),
		project:  labString("LAB_PROJECT", "loadlab"),
		repo:     os.Getenv("LAB_REPO"),
		duration: labDuration("LAB_DURATION", 10*time.Minute),
		gate:     labString("LAB_GATE", "hook"),
		chat:     labDuration("LAB_CHAT_EVERY", 5*time.Second),
		ai:       labString("LAB_AI", "claude"),
		pool:     labString("LAB_POOL", "/var/lib/incus/storage-pools/default"),
		probeDir: labString("LAB_PROBE_DIR", "/var/tmp"),
		log:      os.Getenv("LAB_DAEMON_LOG"),
		p99:      time.Duration(labInt("LAB_P99_MS", 200)) * time.Millisecond,
		out:      labString("LAB_OUT", "loadlab-"+time.Now().Format("20060102-150405")),
		minFree:  int64(labInt("LAB_MIN_FREE_GB", 40)) << 30,
		ioMax:    float64(labInt("LAB_IO_MAX", 40)),
		jobs:     labInt("LAB_JOBS", 0),
		parallel: labInt("LAB_CREATE_PARALLEL", 1),
	}
	cfg.mix = loadMix(labString("LAB_MIX", "go=30,cargo=20,npm=30,idle=20"), cfg.agents)
	if cfg.gate != "hook" && cfg.gate != "none" {
		t.Fatalf("LAB_GATE is hook or none, not %q", cfg.gate)
	}
	c := newLabAPI(socket)
	ctx := context.Background()

	agents := setUpLoadAgents(t, ctx, c, cfg)
	if os.Getenv("LAB_KEEP") != "1" {
		t.Cleanup(func() {
			for _, a := range agents {
				_ = c.do(context.Background(), http.MethodDelete, "/v1/agents/"+a.Project+"/"+a.Name+"?force=true&deleteBranch=true&deleteMedia=true", nil, nil)
			}
		})
	}
	prepareLoadAgents(t, ctx, agents, cfg)

	r := runLoad(t, ctx, c, agents, cfg)
	if err := os.MkdirAll(cfg.out, 0o755); err != nil {
		t.Fatal(err)
	}
	js, _ := json.MarshalIndent(r, "", "  ")
	md := r.markdown()
	if err := os.WriteFile(filepath.Join(cfg.out, "report.json"), js, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.out, "report.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("report in %s:\n%s", cfg.out, md)
	for _, tg := range r.Targets {
		if !tg.Pass {
			t.Errorf("target missed: %s: want %s, got %s", tg.Name, tg.Want, tg.Got)
		}
	}
}

type loadConfig struct {
	agents                        int
	project, repo, gate, ai, pool string
	probeDir, log, out            string
	duration, chat, p99           time.Duration
	minFree                       int64
	// ioMax is the VM's IO PSI some avg10, in percent, the lab holds back
	// over: no machine is made or prepared, and its agents are frozen while
	// they work, until it falls to half that. 0 is never.
	ioMax float64
	// jobs is how many jobs each build or test runs at once (go test -p,
	// cargo -j, vitest --maxWorkers); 0 leaves each tool's default.
	jobs, parallel int
	mix            []string // each agent's kind, by index
}

// loadMix spreads n agents over the kinds by their shares, in a fixed order
// so a run's agent load-07 does the same work every time.
func loadMix(spec string, n int) []string {
	type share struct {
		kind string
		w    int
	}
	var shares []share
	total := 0
	for _, part := range strings.Split(spec, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		w, _ := strconv.Atoi(v)
		if w > 0 {
			shares = append(shares, share{k, w})
			total += w
		}
	}
	out := make([]string, 0, n)
	// Largest remainder, then interleaved: go, cargo, npm, idle, go, …
	counts := make([]int, len(shares))
	type rem struct{ i, r int }
	var rems []rem
	given := 0
	for i, s := range shares {
		counts[i] = s.w * n / total
		given += counts[i]
		rems = append(rems, rem{i, s.w * n % total})
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].r > rems[b].r })
	for i := 0; given < n; i++ {
		counts[rems[i%len(rems)].i]++
		given++
	}
	for len(out) < n {
		for i := range shares {
			if counts[i] > 0 {
				out = append(out, shares[i].kind)
				counts[i]--
			}
		}
	}
	return out
}

// labAPI is the daemon's API over its socket, timing every call.
type labAPI struct{ http *http.Client }

func newLabAPI(socket string) labAPI {
	return labAPI{&http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}}
}

func (c labAPI) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://agentbox"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// loadAgent is one lab agent and what its work needs inside its machine.
type loadAgent struct {
	api.Agent
	Kind     string
	uid, gid string
	home     string
}

func setUpLoadAgents(t *testing.T, ctx context.Context, c labAPI, cfg loadConfig) []*loadAgent {
	var projects []api.Project
	if err := c.do(ctx, http.MethodGet, "/v1/projects", nil, &projects); err != nil {
		t.Fatalf("the lab daemon: %v", err)
	}
	if !slices.ContainsFunc(projects, func(p api.Project) bool { return p.Name == cfg.project }) {
		if cfg.repo == "" {
			t.Fatalf("no project %s: LAB_REPO names a git repository to add as it", cfg.project)
		}
		var p api.Project
		if err := c.do(ctx, http.MethodPost, "/v1/projects", api.AddProjectRequest{Path: cfg.repo, Name: cfg.project}, &p); err != nil {
			t.Fatal(err)
		}
		cfg.project = p.Name
	}
	var have []api.Agent
	if err := c.do(ctx, http.MethodGet, "/v1/agents?project="+url.QueryEscape(cfg.project), nil, &have); err != nil {
		t.Fatal(err)
	}
	// An agent is found by its title, "Load lab load-NN", and named for the
	// run that made it (load-NN-<run>): a destroyed agent's name stays taken.
	run := strconv.FormatInt(time.Now().Unix()%46656, 36)
	bySlot := map[string]api.Agent{}
	for _, a := range have {
		if slot, ok := strings.CutPrefix(a.Title, "Load lab "); ok {
			bySlot[slot] = a
		}
	}
	// Made a few at a time: making them is its own load, not what this
	// measures. How long each took is logged.
	//
	// Each machine is a copy of the base image: on a dir pool (nesting can
	// have no other: btrfs needs privileges a nested container lacks) that is
	// gigabytes each, on a disk other agents share. Where that disk is btrfs
	// and duperemove is installed, each new machine is deduplicated against
	// the base as it is made, which leaves it costing about what a btrfs
	// pool's snapshot does on a real install, when LAB_DEDUPE=1: it reads
	// every copy back, which on 2026-11-06 helped take a shared VM's disk to
	// 80-90% IO pressure, so it is off by default. Either
	// way, a machine is only started while LAB_AGENT_GB more leaves
	// LAB_MIN_FREE_GB free.
	dedupe := os.Getenv("LAB_DEDUPE") == "1" && canDedupe(cfg.pool)
	per := int64(labInt("LAB_AGENT_GB", 6)) << 30
	var room sync.Mutex // held while waiting for room and starting a copy
	inflight := 0
	var dmu sync.Mutex // one duperemove at a time
	waitRoom := func(name string) error {
		deadline := time.Now().Add(30 * time.Minute)
		for {
			room.Lock()
			used, size := diskUsed(cfg.pool)
			if size-used-int64(inflight+1)*per >= cfg.minFree {
				inflight++
				room.Unlock()
				return nil
			}
			n := inflight
			room.Unlock()
			if n == 0 || time.Now().After(deadline) {
				return fmt.Errorf("making %s would leave under LAB_MIN_FREE_GB (%s) free of the pool's disk (%s free now): run fewer agents (LAB_AGENTS) or free some",
					name, HumanBytes(cfg.minFree), HumanBytes(size-used))
			}
			time.Sleep(10 * time.Second)
		}
	}
	done := func() { room.Lock(); inflight--; room.Unlock() }
	// A copy under way can't wait: while machines are made, Incus itself
	// (its rsync) is frozen whenever the VM's IO pressure is over the limit.
	if cfg.ioMax > 0 {
		copying, stopGuard := context.WithCancel(ctx)
		guarded := make(chan struct{})
		defer func() { stopGuard(); <-guarded }()
		go func() {
			defer close(guarded)
			incus := filepath.Join(cgroupRoot, "system.slice", "incus.service", "cgroup.freeze")
			frozen := false
			set := func(on bool) {
				v := "0"
				if on {
					v = "1"
				}
				if os.WriteFile(incus, []byte(v), 0o644) == nil {
					frozen = on
				}
			}
			defer func() {
				if frozen {
					set(false)
				}
			}()
			for copying.Err() == nil {
				switch avg := ioAvg10(); {
				case !frozen && avg > cfg.ioMax:
					set(true)
				case frozen && avg < cfg.ioMax/2:
					set(false)
				}
				time.Sleep(2 * time.Second)
			}
		}()
	}
	sem := make(chan struct{}, max(cfg.parallel, 1))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var took []time.Duration
	for i := range cfg.agents {
		slot := fmt.Sprintf("load-%02d", i+1)
		if _, ok := bySlot[slot]; ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			name := slot + "-" + run
			waitIO(t, cfg.ioMax, "making "+name)
			if err := waitRoom(name); err != nil {
				t.Error(err)
				return
			}
			defer done()
			start := time.Now()
			var job api.Job
			no := false
			create := func() error {
				return c.do(ctx, http.MethodPost, "/v1/agents", api.CreateAgentRequest{
					Project: cfg.project, Name: name, Title: "Load lab " + slot, AI: cfg.ai, Autonomous: &no,
				}, &job)
			}
			err := create()
			for err == nil && !job.Done() {
				time.Sleep(2 * time.Second)
				err = c.do(ctx, http.MethodGet, "/v1/jobs/"+job.ID, nil, &job)
			}
			if err == nil && job.Status == api.JobFailed {
				err = errors.New(job.Error)
			}
			if err != nil {
				t.Errorf("making %s: %v", name, err)
				return
			}
			mu.Lock()
			took = append(took, time.Since(start))
			mu.Unlock()
			t.Logf("made %s in %s", name, time.Since(start).Round(time.Second))
			if dedupe {
				dmu.Lock()
				defer dmu.Unlock()
				before, _ := diskUsed(cfg.pool)
				start := time.Now()
				var a api.Agent
				if err := c.do(ctx, http.MethodGet, "/v1/agents/"+cfg.project+"/"+name, nil, &a); err != nil {
					t.Errorf("%s: %v", name, err)
					return
				}
				if out, err := exec.CommandContext(ctx, "duperemove", "-dr", "-q", "--hashfile=/var/tmp/loadlab-dedupe.hash",
					filepath.Join(cfg.pool, "containers", "agentbox-base", "rootfs"),
					filepath.Join(cfg.pool, "containers", a.Instance, "rootfs")).CombinedOutput(); err != nil {
					t.Logf("deduplicating %s: %v: %s", name, err, lastLines(string(out), 3))
					return
				}
				after, _ := diskUsed(cfg.pool)
				t.Logf("deduplicated %s in %s: %s back", name, time.Since(start).Round(time.Second), HumanBytes(before-after))
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	if len(took) > 0 {
		slices.Sort(took)
		t.Logf("making %d agents: median %s, slowest %s", len(took), took[len(took)/2].Round(time.Second), took[len(took)-1].Round(time.Second))
	}
	if err := c.do(ctx, http.MethodGet, "/v1/agents?project="+url.QueryEscape(cfg.project), nil, &have); err != nil {
		t.Fatal(err)
	}
	var out []*loadAgent
	for i := range cfg.agents {
		name := fmt.Sprintf("load-%02d", i+1)
		i := slices.IndexFunc(have, func(a api.Agent) bool { return a.Title == "Load lab "+name })
		if i < 0 {
			t.Fatalf("%s isn't there after making it", name)
		}
		if have[i].State != "running" {
			waitIO(t, cfg.ioMax, "starting "+name)
			if err := c.do(ctx, http.MethodPost, "/v1/agents/"+have[i].Project+"/"+have[i].Name+"/start", nil, &have[i]); err != nil {
				t.Fatalf("starting %s: %v", name, err)
			}
			// A start that must wait for memory answers at once and boots
			// aside: the work needs its machine running.
			for deadline := time.Now().Add(15 * time.Minute); have[i].State != "running"; {
				if time.Now().After(deadline) {
					t.Fatalf("%s is still %s after 15 minutes", name, have[i].State)
				}
				time.Sleep(2 * time.Second)
				var now []api.Agent
				if err := c.do(ctx, http.MethodGet, "/v1/agents?project="+url.QueryEscape(cfg.project), nil, &now); err != nil {
					t.Fatal(err)
				}
				if j := slices.IndexFunc(now, func(a api.Agent) bool { return a.Name == have[i].Name }); j >= 0 {
					have[i] = now[j]
				}
			}
		}
		out = append(out, &loadAgent{Agent: have[i], Kind: cfg.mix[len(out)]})
	}
	return out
}

// waitIO waits while the VM's IO pressure is over max (some avg10), so what
// the lab sets up doesn't add to a disk that is already saturated: on a VM
// shared with other agents, that stalls their Incus too.
func waitIO(t *testing.T, limit float64, what string) {
	if limit <= 0 {
		return
	}
	logged := false
	for {
		avg := ioAvg10()
		if avg < limit {
			return
		}
		if !logged {
			t.Logf("%s: waiting for IO pressure (some avg10 %.0f%%) to fall under %.0f%%", what, avg, limit)
			logged = true
		}
		time.Sleep(5 * time.Second)
	}
}

// ioAvg10 is this machine's IO pressure, some avg10: its own cgroup's in a
// container, where /proc/pressure/io is the whole VM's. Backing off on the
// VM's froze the lab's Incus for as long as other agents' work kept the
// shared disk busy (18 minutes on 2026-11-06), with nothing of the lab's
// left to back off.
func ioAvg10() float64 {
	b, err := os.ReadFile("/sys/fs/cgroup/io.pressure")
	if err != nil || len(b) == 0 {
		b, _ = os.ReadFile("/proc/pressure/io")
	}
	for _, f := range strings.Fields(string(b)) {
		if v, ok := strings.CutPrefix(f, "avg10="); ok {
			n, _ := strconv.ParseFloat(v, 64)
			return n
		}
	}
	return 0
}

// inAgent runs script in a's machine as its user, in dir, with a login
// shell's environment (mise's tools on PATH).
func inAgent(ctx context.Context, a *loadAgent, dir, script string) *exec.Cmd {
	args := []string{"exec", a.Instance}
	if a.uid != "" {
		args = append(args, "--user", a.uid, "--group", a.gid, "--env", "HOME="+a.home, "--env", "USER="+filepath.Base(a.home))
	}
	if dir != "" {
		args = append(args, "--cwd", dir)
	}
	args = append(args, "--", "bash", "-lc", script)
	return exec.CommandContext(ctx, "incus", args...)
}

// The work's files. The crate and the package are small, but their
// dependencies are what real projects' builds spend their time on.
const (
	loadCargoToml = `[package]
name = "loadlab"
version = "0.1.0"
edition = "2021"

[dependencies]
serde = { version = "1", features = ["derive"] }
serde_json = "1"
regex = "1"
clap = { version = "4", features = ["derive"] }
tokio = { version = "1", features = ["full"] }
`
	loadMainRs = `use clap::Parser;
use serde::{Deserialize, Serialize};
#[derive(Parser, Serialize, Deserialize, Debug)]
struct Args { #[arg(long, default_value = "x")] name: String }
#[tokio::main]
async fn main() {
    let a = Args::parse();
    let re = regex::Regex::new(r"^[a-z]+$").unwrap();
    println!("{} {}", serde_json::to_string(&a).unwrap(), re.is_match(&a.name));
}
`
	loadPackageJSON = `{
  "name": "loadlab",
  "private": true,
  "type": "module",
  "devDependencies": {
    "vitest": "3.2.4",
    "typescript": "5.9.3",
    "react": "19.2.0",
    "react-dom": "19.2.0",
    "@testing-library/react": "16.3.0",
    "jsdom": "27.0.1",
    "lodash-es": "4.17.21",
    "zod": "4.1.12",
    "date-fns": "4.1.0"
  }
}
`
	loadVitestConfig = `export default { test: { environment: "jsdom" } }
`
)

// loadTest is one vitest file: a little of everything the deps do.
func loadTest(i int) string {
	return fmt.Sprintf(`import { describe, it, expect } from "vitest";
import { z } from "zod";
import { chunk, sortBy } from "lodash-es";
import { addDays, format } from "date-fns";
import { createElement } from "react";
import { render } from "@testing-library/react";
describe("file %d", () => {
  for (let n = 0; n < 40; n++) {
    it("case " + n, () => {
      const s = z.object({ a: z.number(), b: z.string() });
      expect(s.parse({ a: n, b: "x" }).a).toBe(n);
      expect(chunk(sortBy([...Array(200).keys()].map(x => (x * %d) %% 97)), 7).length).toBe(29);
      expect(format(addDays(new Date(2026, 0, 1), n), "yyyy")).toMatch(/20\d\d/);
      const { container } = render(createElement("div", null, "row " + n));
      expect(container.textContent).toBe("row " + n);
    });
  }
});
`, i, i+3)
}

// prepareLoadAgents puts each agent's work in place and runs it once, so the
// measured run starts warm the way a working agent is: dependencies in the
// package cache, toolchains installed. One of each kind first, so the rest
// find the shared caches filled.
func prepareLoadAgents(t *testing.T, ctx context.Context, agents []*loadAgent, cfg loadConfig) {
	for _, a := range agents {
		out, err := exec.CommandContext(ctx, "incus", "exec", a.Instance, "--", "sh", "-c", `getent passwd "$(stat -c %u "$0")"`, a.Worktree).Output()
		if err != nil {
			t.Fatalf("%s: its user: %v", a.Name, err)
		}
		f := strings.Split(strings.TrimSpace(string(out)), ":")
		if len(f) < 6 {
			t.Fatalf("%s: its user: %q", a.Name, out)
		}
		a.uid, a.gid, a.home = f[2], f[3], f[5]
	}
	prep := func(a *loadAgent) error {
		var script string
		switch a.Kind {
		case "go":
			script = `cd "$WT" && go mod download && go test -count=1 ${J:+-p $J} -run XXX_none ./... >/dev/null`
		case "cargo":
			script = `set -e
command -v cargo >/dev/null || [ -x ~/.cargo/bin/cargo ] || curl -sSf https://sh.rustup.rs | sh -s -- -y -q --profile minimal >/dev/null
mkdir -p ~/loadlab/rs/src && cd ~/loadlab/rs
cat > Cargo.toml <<'EOF'
` + loadCargoToml + `EOF
cat > src/main.rs <<'EOF'
` + loadMainRs + `EOF
PATH=~/.cargo/bin:$PATH cargo fetch -q`
		case "npm":
			var tests strings.Builder
			for i := range 12 {
				fmt.Fprintf(&tests, "cat > t%02d.test.js <<'EOF'\n%sEOF\n", i, loadTest(i))
			}
			script = `set -e
mkdir -p ~/loadlab/js && cd ~/loadlab/js
cat > package.json <<'EOF'
` + loadPackageJSON + `EOF
cat > vitest.config.js <<'EOF'
` + loadVitestConfig + `EOF
` + tests.String() + `npm install --no-audit --no-fund --loglevel=error >/dev/null`
		default:
			return nil
		}
		cmd := inAgent(ctx, a, "", script)
		j := ""
		if cfg.jobs > 0 {
			j = strconv.Itoa(cfg.jobs)
		}
		cmd.Args = slices.Insert(cmd.Args, 3, "--env", "WT="+a.Worktree, "--env", "J="+j)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s (%s): %v: %s", a.Name, a.Kind, err, lastLines(string(out), 5))
		}
		return nil
	}
	firsts := map[string]bool{}
	for _, a := range agents {
		if !firsts[a.Kind] {
			firsts[a.Kind] = true
			waitIO(t, cfg.ioMax, "preparing "+a.Name)
			start := time.Now()
			if err := prep(a); err != nil {
				t.Fatalf("preparing: %v", err)
			}
			t.Logf("prepared %s (%s) in %s", a.Name, a.Kind, time.Since(start).Round(time.Second))
		}
	}
	sem := make(chan struct{}, max(cfg.parallel, 1))
	var wg sync.WaitGroup
	for _, a := range agents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			waitIO(t, cfg.ioMax, "preparing "+a.Name)
			if err := prep(a); err != nil {
				t.Errorf("preparing: %v", err)
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
}

// loadSteps is what an agent of a kind runs, in a loop: (name, script, heavy).
func loadSteps(kind string) [][3]string {
	switch kind {
	case "go":
		return [][3]string{{"go test", `cd "$WT" && $H go test -count=1 ${J:+-p $J -parallel $J} ./...`, "1"}}
	case "cargo":
		return [][3]string{
			{"cargo clean", `cd ~/loadlab/rs && PATH=~/.cargo/bin:$PATH cargo clean -q`, ""},
			{"cargo build", `cd ~/loadlab/rs && PATH=~/.cargo/bin:$PATH $H cargo build -q ${J:+-j $J}`, "1"},
		}
	case "npm":
		return [][3]string{
			{"npm install", `cd ~/loadlab/js && rm -rf node_modules && npm install --no-audit --no-fund --loglevel=error`, ""},
			{"vitest", `cd ~/loadlab/js && $H npx vitest run --reporter=dot ${J:+--maxWorkers=$J --minWorkers=1}`, "1"},
		}
	case "memhog":
		return [][3]string{{"memory hog", loadMemHog, ""}}
	case "cpuhog":
		return [][3]string{{"cpu hog", `for i in $(seq 16); do timeout 60 sh -c 'while :; do :; done' & done; wait`, ""}}
	case "forkbomb":
		return [][3]string{{"fork bomb", loadForkBomb, "root"}}
	}
	return nil
}

// loadMemHog fills its machine's memory.max and a GiB more with pages that
// compress about as well as a build's (a quarter random), then keeps
// touching every one of them for a minute: past memory.max, each touch is a
// page swapped back in and another out. Without memory.swap.max the GiB goes
// to the VM's swap and the machine thrashes; with it, the hog is killed.
const loadMemHog = `exec node -e '
const fs = require("fs"), c = require("crypto");
const m = fs.readFileSync("/sys/fs/cgroup/memory.max", "utf8").trim();
const want = (m === "max" ? 4 << 30 : +m) + (1 << 30), chunk = 64 << 20, bufs = [];
for (let n = 0; n < want; n += chunk) {
  const b = Buffer.allocUnsafe(chunk);
  for (let o = 0; o < chunk; o += 4096) { c.randomFillSync(b, o, 1024); b.fill(o & 255, o + 1024, o + 4096); }
  bufs.push(b);
}
const end = Date.now() + 60000;
while (Date.now() < end) for (const b of bufs) for (let o = 0; o < chunk; o += 4096) b[o + 2000]++;
'`

// loadForkBomb runs the classic fork bomb, as root in its machine, in a
// cgroup of its own there, which it kills after 30 seconds, and prints the
// most tasks it reached. That cgroup's pids.max is the lab's own safety net,
// so a run without the agents' pids.max can't take a shared VM down:
// LAB_BOMB_CAP (10000), or 256 under the machine's own pids.max when it has
// one, as this script needs a few tasks of its own to watch the bomb and
// kill it. Past that it forks nothing: it reads files with builtins and
// waits with read -t.
const loadForkBomb = `cg=/sys/fs/cgroup/loadlab-bomb
read -r own < /sys/fs/cgroup/pids.max
cap=${LAB_BOMB_CAP:-10000}
[ "$own" != max ] && [ $((own - 256)) -lt $cap ] && cap=$((own - 256))
mkdir -p $cg && echo $cap > $cg/pids.max
bash -c "echo \\$\\$ > $cg/cgroup.procs; exec bash -c ':(){ :|:& };:'" 2>/dev/null &
exec 3<> <(:)
peak=0; for i in $(seq 30); do read -t 1 -u 3; read -r n < $cg/pids.current; [ "$n" -gt "$peak" ] && peak=$n; done
echo 1 > $cg/cgroup.kill
for i in $(seq 30); do read -r n < $cg/pids.current; [ "$n" = 0 ] && break; read -t 1 -u 3; done
rmdir $cg
echo "fork bomb reached $peak tasks (its cap $cap, machine pids.max $own)"`

// ---- the run ----

type loadReport struct {
	Started  time.Time      `json:"started"`
	Duration string         `json:"duration"`
	Commit   string         `json:"commit,omitempty"`
	Agents   int            `json:"agents"`
	Mix      map[string]int `json:"mix"`
	Gate     string         `json:"gate"`
	CPUs     int            `json:"cpus"`

	API     map[string]latency `json:"api"`
	Desktop desktopStats       `json:"desktop"`
	OOM     oomStats           `json:"oom"`
	Heavy   gateStats          `json:"heavy_gate"`
	Memory  memoryStats        `json:"memory"`
	Disk    diskStats          `json:"disk"`
	Work    map[string]*work   `json:"work"`
	Targets []target           `json:"targets"`
	// IO is the IO guard's doing (LAB_IO_MAX).
	IO struct {
		Max       float64 `json:"max_avg10"`
		PeakAvg10 float64 `json:"peak_avg10"`
		Backoffs  int     `json:"backoffs"`
		FrozenS   float64 `json:"frozen_s"`
	} `json:"io_guard"`
	Jobs int `json:"jobs,omitempty"`
	// StoppedEarly says why the run ended before its time, "" when it didn't.
	StoppedEarly string `json:"stopped_early,omitempty"`
}

type latency struct {
	N      int     `json:"n"`
	Errors int     `json:"errors"`
	P50ms  float64 `json:"p50_ms"`
	P99ms  float64 `json:"p99_ms"`
	MaxMs  float64 `json:"max_ms"`
	Error  string  `json:"last_error,omitempty"`
	// Slow are the calls over a second; SlowIO those of them that ended
	// while the VM's IO PSI some avg10 was over 40%.
	Slow   int `json:"slow,omitempty"`
	SlowIO int `json:"slow_under_io,omitempty"`
}

type pressurePeak struct {
	// Peak and Mean are the share of time stalled, in percent, over each
	// second (from PSI's total), for "some" task.
	CPUPeak  float64 `json:"cpu_some_peak_pct"`
	CPUMean  float64 `json:"cpu_some_mean_pct"`
	MemPeak  float64 `json:"memory_some_peak_pct"`
	MemMean  float64 `json:"memory_some_mean_pct"`
	MemFull  float64 `json:"memory_full_peak_pct"`
	IOPeak   float64 `json:"io_some_peak_pct"`
	IOMean   float64 `json:"io_some_mean_pct"`
	Source   string  `json:"source"`
	Readable bool    `json:"readable"`
}

type desktopStats struct {
	Fsync latency `json:"write_fsync"`
	// TimerLate is how late a 10ms timer fired: what a frame or a keystroke
	// waits for a CPU.
	TimerLate latency `json:"timer_late"`
	// RunDelayMsPerS is the probe thread's run-queue wait (schedstat), in ms
	// per second.
	RunDelayPeak float64 `json:"run_delay_peak_ms_per_s"`
	RunDelayMean float64 `json:"run_delay_mean_ms_per_s"`
	// VM is /proc/pressure, what the heavy gate reads; Lab is this machine's
	// own cgroup, which differs from it when the lab runs inside a container
	// (nesting), where /proc/pressure is the outer VM's.
	VM  pressurePeak  `json:"psi_vm"`
	Lab *pressurePeak `json:"psi_lab,omitempty"`
}

type oomStats struct {
	// AtOwnLimit are kills inside an agent at its own memory.max; Outside
	// are kills inside agents with no own-limit hit to account for them (the
	// VM ran out), and OutsideAgents kills of anything that isn't an agent's.
	AtOwnLimit    int64            `json:"at_own_limit"`
	OutsideLimit  int64            `json:"outside_own_limit"`
	OutsideAgents int64            `json:"outside_agents"`
	ByAgent       map[string]int64 `json:"by_agent,omitempty"`
	Limit         int64            `json:"agent_limit_bytes"`
}

type gateStats struct {
	// Asked is how many distinct heavy runs the daemon knew of; Waited and
	// Paused how many of those it ever held.
	Asked      int            `json:"asked"`
	Waited     int            `json:"waited"`
	Paused     int            `json:"paused"`
	MaxWaiting int            `json:"max_waiting"`
	MaxPaused  int            `json:"max_paused"`
	MaxRunning int            `json:"max_running"`
	Placed     int            `json:"placed"`
	SomePeak   float64        `json:"daemon_memory_some_peak"`
	FullPeak   float64        `json:"daemon_memory_full_peak"`
	Why        map[string]int `json:"why_seconds,omitempty"`
	StepWaits  int            `json:"step_waits"`
	LogPauses  int            `json:"log_pauses"`
	LogLines   []string       `json:"log_lines,omitempty"`
	FleetOn    bool           `json:"pause_whole_agents"`
	Fired      bool           `json:"fired"`
	Verdict    string         `json:"verdict"`
}

type memPoint struct {
	S         int   `json:"s"`
	Available int64 `json:"available"`
	SwapUsed  int64 `json:"swap_used"`
}

type memoryStats struct {
	Total        int64      `json:"total"`
	MinAvailable int64      `json:"min_available"`
	PeakSwapUsed int64      `json:"peak_swap_used"`
	SwapTotal    int64      `json:"swap_total"`
	Series       []memPoint `json:"series"`
}

type agentDisk struct {
	Agent     string `json:"agent"`
	Kind      string `json:"kind"`
	Read      int64  `json:"read"`
	Written   int64  `json:"written"`
	MemPeak   int64  `json:"memory_peak,omitempty"`
	Available bool   `json:"io_stat"`
}

type diskStats struct {
	PerAgent  []agentDisk `json:"per_agent"`
	UsedStart int64       `json:"pool_used_start"`
	UsedEnd   int64       `json:"pool_used_end"`
	UsedPeak  int64       `json:"pool_used_peak"`
	Size      int64       `json:"pool_size"`
}

type work struct {
	Runs    int     `json:"runs"`
	OK      int     `json:"ok"`
	Failed  int     `json:"failed"`
	Killed  int     `json:"killed"`
	Waited  int     `json:"waited_for_memory"`
	MedianS float64 `json:"median_s"`
	MaxS    float64 `json:"max_s"`
	times   []time.Duration
}

type target struct {
	Name string `json:"name"`
	Want string `json:"want"`
	Got  string `json:"got"`
	Pass bool   `json:"pass"`
}

// recorder is a list of durations and errors, safe to add to from many
// goroutines.
type recorder struct {
	mu           sync.Mutex
	ds           []time.Duration
	errs         int
	last         string
	slow, slowIO int
}

func (r *recorder) add(d time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.errs++
		r.last = err.Error()
		return
	}
	r.ds = append(r.ds, d)
	if d > time.Second {
		r.slow++
		if ioAvg10() > 40 {
			r.slowIO++
		}
	}
}

func (r *recorder) latency() latency {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := latency{N: len(r.ds), Errors: r.errs, Error: r.last, Slow: r.slow, SlowIO: r.slowIO}
	if len(r.ds) == 0 {
		return l
	}
	s := slices.Clone(r.ds)
	slices.Sort(s)
	at := func(f float64) float64 { return ms(s[min(int(f*float64(len(s))), len(s)-1)]) }
	l.P50ms, l.P99ms, l.MaxMs = at(0.5), at(0.99), ms(s[len(s)-1])
	return l
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func runLoad(t *testing.T, ctx context.Context, c labAPI, agents []*loadAgent, cfg loadConfig) *loadReport {
	r := &loadReport{
		Started: time.Now(), Duration: cfg.duration.String(), Agents: len(agents), Gate: cfg.gate,
		CPUs: runtime.NumCPU(), Mix: map[string]int{}, Work: map[string]*work{},
	}
	if out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		r.Commit = strings.TrimSpace(string(out))
	}
	for _, a := range agents {
		r.Mix[a.Kind]++
	}
	var settings struct {
		AgentFreeze bool `json:"agentFreeze"`
	}
	_ = c.do(ctx, http.MethodGet, "/v1/settings", nil, &settings)
	r.Heavy.FleetOn = settings.AgentFreeze
	r.IO.Max, r.Jobs = cfg.ioMax, cfg.jobs

	startEvents := map[string]MemoryCounts{}
	startIO := map[string][2]int64{}
	for _, a := range agents {
		startEvents[a.Instance], _ = MemoryEvents(a.Instance)
		rd, wr, _ := labIO(a.Instance)
		startIO[a.Instance] = [2]int64{rd, wr}
		_ = os.WriteFile(filepath.Join(agentCgroup(cgroupRoot, a.Instance), "memory.peak"), []byte("0"), 0o644)
	}
	rootKills := rootOOMKills()
	r.Disk.UsedStart, r.Disk.Size = diskUsed(cfg.pool)
	r.Disk.UsedPeak = r.Disk.UsedStart
	logStart := fileSize(cfg.log)

	runCtx, stop := context.WithTimeout(ctx, cfg.duration)
	defer stop()
	// The work's own files grow too: stop early rather than fill the disk.
	go func() {
		for runCtx.Err() == nil {
			if used, size := diskUsed(cfg.pool); size > 0 && size-used < cfg.minFree/2 {
				r.StoppedEarly = fmt.Sprintf("after %s: %s free on the pool's disk", time.Since(r.Started).Round(time.Second), HumanBytes(size-used))
				t.Errorf("stopping the run early, %s", r.StoppedEarly)
				stop()
				return
			}
			time.Sleep(5 * time.Second)
		}
	}()
	// The IO guard: the lab's agents frozen while the VM's disk is
	// saturated, thawed at half the limit; the run ends if it stays
	// saturated two minutes with them frozen (the load is someone else's).
	freeze := func(on bool) {
		v := "0"
		if on {
			v = "1"
		}
		for _, a := range agents {
			_ = os.WriteFile(filepath.Join(agentCgroup(cgroupRoot, a.Instance), "cgroup.freeze"), []byte(v), 0o644)
		}
	}
	defer freeze(false)
	guardDone := make(chan struct{})
	if cfg.ioMax <= 0 {
		close(guardDone)
	} else {
		go func() {
			defer close(guardDone)
			var since time.Time
			for runCtx.Err() == nil {
				avg := ioAvg10()
				switch {
				case since.IsZero() && avg > cfg.ioMax:
					since = time.Now()
					freeze(true)
					r.IO.Backoffs++
					t.Logf("IO pressure %.0f%% over %.0f%%: the lab's agents frozen", avg, cfg.ioMax)
				case !since.IsZero() && avg < cfg.ioMax/2:
					r.IO.FrozenS += time.Since(since).Seconds()
					since = time.Time{}
					freeze(false)
				case !since.IsZero() && time.Since(since) > 2*time.Minute:
					r.IO.FrozenS += time.Since(since).Seconds()
					r.StoppedEarly = fmt.Sprintf("after %s: IO pressure stayed over %.0f%% for 2 minutes with the lab's agents frozen", time.Since(r.Started).Round(time.Second), cfg.ioMax)
					t.Errorf("stopping the run early, %s", r.StoppedEarly)
					stop()
					return
				}
				r.IO.PeakAvg10 = max(r.IO.PeakAvg10, avg)
				time.Sleep(2 * time.Second)
			}
			if !since.IsZero() {
				r.IO.FrozenS += time.Since(since).Seconds()
			}
		}()
	}
	var wg sync.WaitGroup
	every := func(d time.Duration, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tick := time.NewTicker(d)
			defer tick.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-tick.C:
					fn()
				}
			}
		}()
	}

	// The daemon's API.
	var list, chat, lag recorder
	every(time.Second, func() {
		start := time.Now()
		var out []api.Agent
		err := c.do(runCtx, http.MethodGet, "/v1/agents", nil, &out)
		if runCtx.Err() == nil { // cut off by the run's end isn't a result
			list.add(time.Since(start), err)
		}
	})
	n := 0
	every(cfg.chat, func() {
		a := agents[n%len(agents)]
		n++
		start := time.Now()
		err := c.do(runCtx, http.MethodPost, "/v1/agents/"+a.Project+"/"+a.Name+"/chat/messages",
			api.ChatMessageRequest{Text: fmt.Sprintf("Load lab ping %d: no need to answer.", n)}, nil)
		if runCtx.Err() == nil {
			chat.add(time.Since(start), err)
		}
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for runCtx.Err() == nil {
			err := newLabEvents(c).events(runCtx, func(ev api.Event) {
				if !ev.Time.IsZero() {
					lag.add(time.Since(ev.Time), nil)
				}
			})
			if runCtx.Err() == nil {
				lag.add(0, fmt.Errorf("event stream ended: %v", err))
				time.Sleep(time.Second)
			}
		}
	}()

	// The heavy-command gate.
	var gmu sync.Mutex
	seen, waited, paused, placed := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	r.Heavy.Why = map[string]int{}
	every(time.Second, func() {
		var p api.PressureStatus
		if err := c.do(runCtx, http.MethodGet, "/v1/pressure", nil, &p); err != nil {
			return
		}
		gmu.Lock()
		defer gmu.Unlock()
		r.Heavy.SomePeak = max(r.Heavy.SomePeak, p.Some)
		r.Heavy.FullPeak = max(r.Heavy.FullPeak, p.Full)
		if p.Why != "" {
			r.Heavy.Why[p.Why]++
		}
		var w, pz, run int
		for _, h := range p.Runs {
			id := h.Agent + "/" + h.Key
			seen[id] = true
			if h.Placed {
				placed[id] = true
			}
			switch h.State {
			case "waiting":
				waited[id] = true
				w++
			case "paused":
				paused[id] = true
				pz++
			default:
				run++
			}
		}
		r.Heavy.MaxWaiting = max(r.Heavy.MaxWaiting, w)
		r.Heavy.MaxPaused = max(r.Heavy.MaxPaused, pz)
		r.Heavy.MaxRunning = max(r.Heavy.MaxRunning, run)
	})

	// The desktop's stand-in: outside every agent, on the VM's own disk.
	var fsync, late recorder
	every(100*time.Millisecond, func() {
		path := filepath.Join(cfg.probeDir, "loadlab-probe")
		start := time.Now()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err == nil {
			_, _ = f.WriteString(start.String())
			err = f.Sync()
			_ = f.Close()
		}
		fsync.add(time.Since(start), err)
	})
	var runDelay []float64
	wg.Add(1)
	go func() {
		defer wg.Done()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		lastWait, lastAt := threadWait(), time.Now()
		for runCtx.Err() == nil {
			start := time.Now()
			time.Sleep(10 * time.Millisecond)
			late.add(max(time.Since(start)-10*time.Millisecond, 0), nil)
			if time.Since(lastAt) >= time.Second {
				w := threadWait()
				runDelay = append(runDelay, float64(w-lastWait)/1e6/time.Since(lastAt).Seconds())
				lastWait, lastAt = w, time.Now()
			}
		}
	}()
	vmPSI := newPSIReader("/proc/pressure/%s", "/proc/pressure")
	var labPSI *psiReader
	if !samePSI() {
		labPSI = newPSIReader(cgroupRoot+"/%s.pressure", "this machine's cgroup ("+cgroupRoot+")")
	}
	every(time.Second, func() {
		vmPSI.read()
		if labPSI != nil {
			labPSI.read()
		}
	})

	// Memory and disk space over time.
	var mmu sync.Mutex
	every(2*time.Second, func() {
		m := readMeminfo()
		used, _ := diskUsed(cfg.pool)
		mmu.Lock()
		defer mmu.Unlock()
		r.Memory.Total, r.Memory.SwapTotal = m["MemTotal"], m["SwapTotal"]
		avail, swap := m["MemAvailable"], m["SwapTotal"]-m["SwapFree"]
		if r.Memory.MinAvailable == 0 || avail < r.Memory.MinAvailable {
			r.Memory.MinAvailable = avail
		}
		r.Memory.PeakSwapUsed = max(r.Memory.PeakSwapUsed, swap)
		if len(r.Memory.Series) == 0 || time.Since(r.Started) >= time.Duration(r.Memory.Series[len(r.Memory.Series)-1].S+10)*time.Second {
			r.Memory.Series = append(r.Memory.Series, memPoint{int(time.Since(r.Started).Seconds()), avail, swap})
		}
		r.Disk.UsedPeak = max(r.Disk.UsedPeak, used)
	})

	// The work.
	var wmu sync.Mutex
	for _, a := range agents {
		steps := loadSteps(a.Kind)
		if steps == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Staggered over the first 20 seconds, as agents never all start
			// at the same instant.
			select {
			case <-runCtx.Done():
				return
			case <-time.After(time.Duration(rand.IntN(20000)) * time.Millisecond):
			}
			for runCtx.Err() == nil {
				for _, st := range steps {
					left, _ := runCtx.Deadline()
					secs := int(time.Until(left).Seconds())
					if secs < 5 {
						return
					}
					h := ""
					if st[2] == "1" && cfg.gate == "hook" {
						h = "agentbox heavy --"
					}
					j := ""
					if cfg.jobs > 0 {
						j = strconv.Itoa(cfg.jobs)
					}
					script := fmt.Sprintf("export WT=%q H=%q J=%q; timeout -k 10 %d bash -c %s", a.Worktree, h, j, secs, shellQuote(st[1]))
					start := time.Now()
					who := a
					if st[2] == "root" {
						who = &loadAgent{Agent: a.Agent, Kind: a.Kind}
					}
					out, err := inAgent(context.WithoutCancel(runCtx), who, "", script).CombinedOutput()
					if a.Kind == "forkbomb" {
						t.Logf("%s: %s", a.Name, lastLines(string(out), 1))
					}
					d := time.Since(start)
					code := 0
					var exit *exec.ExitError
					if errors.As(err, &exit) {
						code = exit.ExitCode()
					}
					if code == 124 || runCtx.Err() != nil {
						return // cut off by the run's end, not a result
					}
					key := a.Kind + ": " + st[0]
					wmu.Lock()
					w := r.Work[key]
					if w == nil {
						w = &work{}
						r.Work[key] = w
					}
					w.Runs++
					switch {
					case code == 0:
						w.OK++
						w.times = append(w.times, d)
					case code == 137 || bytes.Contains(out, []byte("Killed")):
						w.Killed++
					default:
						w.Failed++
					}
					if bytes.Contains(out, []byte("agentbox heavy: waiting for memory")) {
						w.Waited++
					}
					wmu.Unlock()
					if code != 0 && os.Getenv("LAB_VERBOSE") == "1" {
						t.Logf("%s %s: exit %d: %s", a.Name, st[0], code, lastLines(string(out), 3))
					}
				}
			}
		}()
	}
	<-runCtx.Done()
	wg.Wait()
	<-guardDone

	// What it left.
	r.API = map[string]latency{"agents_list": list.latency(), "chat_send": chat.latency(), "event_lag": lag.latency()}
	// Each machine's caps (agent/caps.go) and the most it reached under them.
	for _, a := range agents {
		dir := agentCgroup(cgroupRoot, a.Instance)
		read := func(f string) string { return strings.TrimSpace(readString(filepath.Join(dir, f))) }
		t.Logf("%s (%s): memory.max %s swap.max %s swap.peak %s cpu.weight %s pids.max %s pids.peak %s",
			a.Name, a.Kind, read("memory.max"), read("memory.swap.max"), read("memory.swap.peak"),
			read("cpu.weight"), read("pids.max"), read("pids.peak"))
	}
	r.Desktop.Fsync, r.Desktop.TimerLate = fsync.latency(), late.latency()
	r.Desktop.RunDelayPeak, r.Desktop.RunDelayMean = peakMean(runDelay)
	r.Desktop.VM = vmPSI.peak()
	if labPSI != nil {
		p := labPSI.peak()
		r.Desktop.Lab = &p
	}
	r.OOM.ByAgent = map[string]int64{}
	var agentKills int64
	for _, a := range agents {
		now, ok := MemoryEvents(a.Instance)
		if !ok {
			continue
		}
		was := startEvents[a.Instance]
		kills, own := now.Kills-was.Kills, now.OwnLimit-was.OwnLimit
		r.OOM.Limit = now.Limit
		agentKills += kills
		if kills > 0 {
			r.OOM.ByAgent[a.Name] = kills
		}
		// A kill counts as at the agent's own limit as far as its cgroup
		// reached that limit with nothing to reclaim; the rest were the VM's.
		atOwn := min(kills, own)
		r.OOM.AtOwnLimit += atOwn
		r.OOM.OutsideLimit += kills - atOwn
		rd, wr, ok := labIO(a.Instance)
		peak, _ := strconv.ParseInt(strings.TrimSpace(readString(filepath.Join(agentCgroup(cgroupRoot, a.Instance), "memory.peak"))), 10, 64)
		r.Disk.PerAgent = append(r.Disk.PerAgent, agentDisk{a.Name, a.Kind, rd - startIO[a.Instance][0], wr - startIO[a.Instance][1], peak, ok})
	}
	r.OOM.OutsideAgents = max(rootOOMKills()-rootKills-agentKills, 0)
	r.Disk.UsedEnd, _ = diskUsed(cfg.pool)
	for _, w := range r.Work {
		if len(w.times) > 0 {
			slices.Sort(w.times)
			w.MedianS = w.times[len(w.times)/2].Seconds()
			w.MaxS = w.times[len(w.times)-1].Seconds()
		}
		r.Heavy.StepWaits += w.Waited
	}
	r.Heavy.Asked, r.Heavy.Waited, r.Heavy.Paused, r.Heavy.Placed = len(seen), len(waited), len(paused), len(placed)
	r.Heavy.LogPauses, r.Heavy.LogLines = daemonLogPressure(cfg.log, logStart)
	r.Heavy.Fired = r.Heavy.Waited+r.Heavy.Paused+r.Heavy.LogPauses+r.Heavy.StepWaits > 0
	r.Heavy.Verdict = gateVerdict(r)

	worst := max(r.API["agents_list"].P99ms, r.API["chat_send"].P99ms, r.API["event_lag"].P99ms)
	r.Targets = []target{
		{"API p99 (agent list, chat send, event lag)", fmt.Sprintf("< %d ms", cfg.p99.Milliseconds()), fmt.Sprintf("%.0f ms", worst), worst < ms(cfg.p99) && r.API["agents_list"].N > 0},
		{"OOM kills outside an agent's own limit", "0", strconv.FormatInt(r.OOM.OutsideLimit+r.OOM.OutsideAgents, 10), r.OOM.OutsideLimit+r.OOM.OutsideAgents == 0},
	}
	return r
}

// gateVerdict says whether the heavy-command gate fired, and if it didn't,
// what of its inputs kept it from it.
func gateVerdict(r *loadReport) string {
	g := r.Heavy
	pol := "memory PSI some avg10 ≥ 10% to hold new runs, ≥ 20% for 5s to pause one, or available memory near its 1.5 GiB floor with swap full"
	switch {
	case g.Paused > 0 || g.LogPauses > 0:
		return fmt.Sprintf("Fired: %d heavy run(s) waited to start and %d were paused (the daemon's log: %d pauses).", g.Waited, g.Paused, g.LogPauses)
	case g.Waited > 0 || g.StepWaits > 0:
		return fmt.Sprintf("Fired, holding only: %d heavy run(s) waited to start; none was paused.", max(g.Waited, g.StepWaits))
	case r.Gate == "none":
		return "Couldn't fire: LAB_GATE=none runs every command without asking, as Codex, OpenCode, Cursor and Gemini agents do. The gate only sees commands that ask (Claude Code's hook, or agentbox heavy)."
	case g.Asked == 0:
		return "Didn't fire: no heavy command reached the daemon, so it had nothing to hold. `agentbox heavy` in the agents ran the commands without asking (no in-agent API socket, or it couldn't reach the daemon)."
	case g.FleetOn:
		return "Didn't fire as runs: pausing whole agents is on, which starts every run at once and pauses machines instead."
	}
	why := fmt.Sprintf("Didn't fire: %d heavy runs asked and all started at once. Its rule is %s. The daemon read memory PSI some at most %.1f%% (full %.1f%%), and the VM's available memory never went under %s of %s",
		g.Asked, pol, g.SomePeak, g.FullPeak, HumanBytes(r.Memory.MinAvailable), HumanBytes(r.Memory.Total))
	cpu, io := r.Desktop.VM.CPUPeak, r.Desktop.VM.IOPeak
	if r.Desktop.Lab != nil {
		cpu, io = max(cpu, r.Desktop.Lab.CPUPeak), max(io, r.Desktop.Lab.IOPeak)
	}
	why += fmt.Sprintf(", while CPU PSI some peaked at %.0f%% and IO at %.0f%%: the gate reads memory only, so a load that stalls on CPU or disk never trips it.", cpu, io)
	if r.Desktop.Lab != nil {
		why += fmt.Sprintf(" In this lab the daemon ran inside a container, where /proc/pressure is the outer VM's, not the lab's (the lab's own memory PSI peaked at %.1f%%).", r.Desktop.Lab.MemPeak)
	}
	return why
}

func (r *loadReport) markdown() string {
	var b strings.Builder
	p := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	mix := []string{}
	for _, k := range []string{"go", "cargo", "npm", "idle"} {
		if r.Mix[k] > 0 {
			mix = append(mix, fmt.Sprintf("%d %s", r.Mix[k], k))
		}
	}
	p("## Load lab: %d agents, %s, gate %s\n\n", r.Agents, r.Duration, r.Gate)
	jobs := "each tool's default jobs"
	if r.Jobs > 0 {
		jobs = fmt.Sprintf("%d job(s) per build or test", r.Jobs)
	}
	p("%s · commit %s · %d CPUs · %s memory · mix %s · %s\n\n", r.Started.Format("2006-01-02 15:04"), r.Commit, r.CPUs, HumanBytes(r.Memory.Total), strings.Join(mix, ", "), jobs)
	if r.IO.Max > 0 {
		p("IO guard: VM IO PSI some avg10 peaked at %.0f%%; the lab froze its agents %d time(s) over %.0f%%, %.0f s in all.\n\n", r.IO.PeakAvg10, r.IO.Backoffs, r.IO.Max, r.IO.FrozenS)
	}
	if r.StoppedEarly != "" {
		p("**Stopped early**, %s.\n\n", r.StoppedEarly)
	}
	p("| Target | Want | Got | |\n|---|---|---|---|\n")
	for _, t := range r.Targets {
		ok := "✅"
		if !t.Pass {
			ok = "❌"
		}
		p("| %s | %s | %s | %s |\n", t.Name, t.Want, t.Got, ok)
	}
	p("\n**Daemon API**\n\n| | n | errors | p50 | p99 | max | over 1 s (of them under IO PSI > 40%%) |\n|---|---|---|---|---|---|---|\n")
	for _, k := range []string{"agents_list", "chat_send", "event_lag"} {
		l := r.API[k]
		p("| %s | %d | %d | %.0f ms | %.0f ms | %.0f ms | %d (%d) |\n", k, l.N, l.Errors, l.P50ms, l.P99ms, l.MaxMs, l.Slow, l.SlowIO)
	}
	d := r.Desktop
	p("\n**Desktop stand-in** (outside every agent)\n\n")
	p("- write+fsync: p50 %.1f ms, p99 %.0f ms, max %.0f ms\n", d.Fsync.P50ms, d.Fsync.P99ms, d.Fsync.MaxMs)
	p("- a 10 ms timer, late by: p50 %.1f ms, p99 %.0f ms, max %.0f ms; run-queue wait %.0f ms/s mean, %.0f peak\n", d.TimerLate.P50ms, d.TimerLate.P99ms, d.TimerLate.MaxMs, d.RunDelayMean, d.RunDelayPeak)
	psi := func(name string, x pressurePeak) {
		p("- PSI some, %s: CPU %.0f%% mean / %.0f%% peak, memory %.1f%% / %.1f%% (full %.1f%%), IO %.0f%% / %.0f%%\n", name, x.CPUMean, x.CPUPeak, x.MemMean, x.MemPeak, x.MemFull, x.IOMean, x.IOPeak)
	}
	psi(d.VM.Source+", what the gate reads", d.VM)
	if d.Lab != nil {
		psi(d.Lab.Source, *d.Lab)
	}
	g := r.Heavy
	p("\n**Heavy-command gate**: %s\n\n", g.Verdict)
	p("- runs asked %d (in a cgroup of their own %d), waited %d, paused %d; at once at most %d running, %d waiting, %d paused; steps that printed \"waiting for memory\": %d\n", g.Asked, g.Placed, g.Waited, g.Paused, g.MaxRunning, g.MaxWaiting, g.MaxPaused, g.StepWaits)
	p("- daemon's memory PSI some peak %.1f%%, full %.1f%%; pausing whole agents %v\n", g.SomePeak, g.FullPeak, g.FleetOn)
	p("\n**Memory**: %s total, lowest available %s, swap used at most %s of %s\n\n", HumanBytes(r.Memory.Total), HumanBytes(r.Memory.MinAvailable), HumanBytes(r.Memory.PeakSwapUsed), HumanBytes(r.Memory.SwapTotal))
	p("**OOM kills**: %d at an agent's own limit (%s), %d beyond it, %d outside agents\n\n", r.OOM.AtOwnLimit, HumanBytes(r.OOM.Limit), r.OOM.OutsideLimit, r.OOM.OutsideAgents)
	var rd, wr int64
	for _, a := range r.Disk.PerAgent {
		rd, wr = rd+a.Read, wr+a.Written
	}
	p("**Disk**: agents read %s and wrote %s (busiest below); space used %s → %s (peak %s) of %s\n\n", HumanBytes(rd), HumanBytes(wr), HumanBytes(r.Disk.UsedStart), HumanBytes(r.Disk.UsedEnd), HumanBytes(r.Disk.UsedPeak), HumanBytes(r.Disk.Size))
	top := slices.Clone(r.Disk.PerAgent)
	slices.SortFunc(top, func(a, b agentDisk) int { return int((b.Read + b.Written - a.Read - a.Written) >> 20) })
	p("| agent | kind | read | written | memory peak |\n|---|---|---|---|---|\n")
	for _, a := range top[:min(5, len(top))] {
		p("| %s | %s | %s | %s | %s |\n", a.Agent, a.Kind, HumanBytes(a.Read), HumanBytes(a.Written), HumanBytes(a.MemPeak))
	}
	p("\n**Work done**\n\n| step | runs | ok | failed | killed | waited | median | max |\n|---|---|---|---|---|---|---|---|\n")
	keys := make([]string, 0, len(r.Work))
	for k := range r.Work {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		w := r.Work[k]
		p("| %s | %d | %d | %d | %d | %d | %.0f s | %.0f s |\n", k, w.Runs, w.OK, w.Failed, w.Killed, w.Waited, w.MedianS, w.MaxS)
	}
	return b.String()
}

// ---- reading the machine ----

// psiReader turns PSI's totals into the share of each second stalled.
type psiReader struct {
	pattern, source string
	last            map[string][2]int64 // resource → some, full totals (µs)
	at              time.Time
	series          map[string][]float64
	ok              bool
}

func newPSIReader(pattern, source string) *psiReader {
	return &psiReader{pattern: pattern, source: source, last: map[string][2]int64{}, series: map[string][]float64{}}
}

func (p *psiReader) read() {
	now := time.Now()
	for _, res := range []string{"cpu", "memory", "io"} {
		some, full, ok := psiTotals(fmt.Sprintf(p.pattern, res))
		if !ok {
			continue
		}
		p.ok = true
		if was, seen := p.last[res]; seen && !p.at.IsZero() {
			span := float64(now.Sub(p.at).Microseconds())
			p.series[res] = append(p.series[res], float64(some-was[0])/span*100)
			p.series[res+".full"] = append(p.series[res+".full"], float64(full-was[1])/span*100)
		}
		p.last[res] = [2]int64{some, full}
	}
	p.at = now
}

func (p *psiReader) peak() pressurePeak {
	out := pressurePeak{Source: p.source, Readable: p.ok}
	out.CPUPeak, out.CPUMean = peakMean(p.series["cpu"])
	out.MemPeak, out.MemMean = peakMean(p.series["memory"])
	out.MemFull, _ = peakMean(p.series["memory.full"])
	out.IOPeak, out.IOMean = peakMean(p.series["io"])
	return out
}

func psiTotals(path string) (some, full int64, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		v, _ := strconv.ParseInt(strings.TrimPrefix(f[4], "total="), 10, 64)
		switch f[0] {
		case "some":
			some, ok = v, true
		case "full":
			full = v
		}
	}
	return some, full, ok
}

// samePSI reports whether /proc/pressure is this machine's own cgroup's: not
// so in a container, where it is the whole VM's.
func samePSI() bool {
	a, _, ok1 := psiTotals("/proc/pressure/cpu")
	b, _, ok2 := psiTotals(cgroupRoot + "/cpu.pressure")
	if !ok2 {
		return true
	}
	return ok1 && a > 0 && float64(b)/float64(a) > 0.95
}

func peakMean(xs []float64) (peak, mean float64) {
	for _, x := range xs {
		peak = max(peak, x)
		mean += x
	}
	if len(xs) > 0 {
		mean /= float64(len(xs))
	}
	return peak, mean
}

// threadWait is the calling thread's time waiting on a run queue, in ns
// (schedstat's second field).
func threadWait() int64 {
	b, _ := os.ReadFile(fmt.Sprintf("/proc/self/task/%d/schedstat", syscall.Gettid()))
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.ParseInt(f[1], 10, 64)
	return n
}

func readMeminfo() map[string]int64 {
	out := map[string]int64{}
	b, _ := os.ReadFile("/proc/meminfo")
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) > 0 {
			n, _ := strconv.ParseInt(f[0], 10, 64)
			out[k] = n << 10
		}
	}
	return out
}

// canDedupe reports whether a dir pool's copies can be deduplicated: its
// disk is btrfs and duperemove is installed.
func canDedupe(pool string) bool {
	var st syscall.Statfs_t
	if syscall.Statfs(pool, &st) != nil || st.Type != 0x9123683e { // BTRFS_SUPER_MAGIC
		return false
	}
	_, err := exec.LookPath("duperemove")
	return err == nil
}

// labIO is what an agent's machine read and wrote, from its io.stat.
func labIO(instance string) (read, written int64, ok bool) {
	b, err := os.ReadFile(filepath.Join(agentCgroup(cgroupRoot, instance), "io.stat"))
	if err != nil {
		return 0, 0, false
	}
	for _, f := range strings.Fields(string(b)) {
		if v, found := strings.CutPrefix(f, "rbytes="); found {
			n, _ := strconv.ParseInt(v, 10, 64)
			read += n
		} else if v, found := strings.CutPrefix(f, "wbytes="); found {
			n, _ := strconv.ParseInt(v, 10, 64)
			written += n
		}
	}
	return read, written, true
}

func rootOOMKills() int64 {
	b, _ := os.ReadFile(filepath.Join(cgroupRoot, "memory.events"))
	return eventCount(b, "oom_kill")
}

func diskUsed(path string) (used, size int64) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0, 0
	}
	return int64(st.Blocks-st.Bfree) * st.Bsize, int64(st.Blocks) * st.Bsize
}

func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

// daemonLogPressure is the daemon's log lines about memory pressure written
// since offset: pauses counted, the first few kept.
func daemonLogPressure(path string, offset int64) (pauses int, lines []string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Seek(offset, io.SeekStart)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		if !strings.Contains(l, "memory pressure") && !strings.Contains(l, "killed for memory") && !strings.Contains(l, "freez") {
			continue
		}
		if strings.Contains(l, "memory pressure: pausing") || strings.Contains(l, "freezing") {
			pauses++
		}
		if len(lines) < 20 {
			lines = append(lines, l)
		}
	}
	return pauses, lines
}

// labEvents reads the daemon's event stream.
type labEvents struct{ c labAPI }

func newLabEvents(c labAPI) labEvents { return labEvents{c} }

func (e labEvents) events(ctx context.Context, fn func(api.Event)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agentbox/v1/events", nil)
	if err != nil {
		return err
	}
	resp, err := e.c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev api.Event
		if json.Unmarshal([]byte(data), &ev) == nil {
			fn(ev)
		}
	}
	return sc.Err()
}

func labString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func labDuration(name string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(name)); err == nil && d > 0 {
		return d
	}
	return def
}
