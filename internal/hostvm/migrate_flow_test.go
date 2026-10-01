package hostvm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/paths"
)

// fakeDaemon answers on p's socket the way the host's daemon, and then the
// VM's, answer vm migrate: jobs whose logs are one line, a shutdown that
// stops it answering, and a recreate that fails for the refs in failOnce the
// first time it's asked.
type fakeDaemon struct {
	up        atomic.Bool
	mu        sync.Mutex
	calls     []string
	recreated map[string]api.RecreateRequest
	failOnce  map[string]bool
	jobs      map[string]api.Job
	// restart brings it back this long after a shutdown, the way the VM's
	// daemon answers once the host's has stopped.
	restart time.Duration
}

func startFakeDaemon(t *testing.T, p paths.Paths) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{recreated: map[string]api.RecreateRequest{}, failOnce: map[string]bool{}, jobs: map[string]api.Job{}}
	d.up.Store(true)
	job := func(w http.ResponseWriter, id string, failed string) {
		j := api.Job{ID: id, Status: api.JobSucceeded}
		if failed != "" {
			j.Status, j.Error = api.JobFailed, failed
		}
		d.jobs[id] = j
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.Job{ID: id, Status: api.JobRunning})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if !d.up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		d.calls = append(d.calls, r.Method+" "+r.URL.Path)
		switch path := r.URL.Path; {
		case path == "/v1/version":
			_, _ = w.Write([]byte(`{"version":"dev"}`))
		case path == "/v1/jobs":
			_, _ = w.Write([]byte(`[]`))
		case path == "/v1/agents/stop":
			job(w, "stop", "")
		case path == "/v1/shutdown":
			d.up.Store(false)
			if d.restart > 0 {
				time.AfterFunc(d.restart, func() { d.up.Store(true) })
			}
		case path == "/v1/image" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"ready":false}`))
		case path == "/v1/image/build":
			job(w, "image", "")
		case strings.HasSuffix(path, "/recreate"):
			ref := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/agents/"), "/recreate")
			var req api.RecreateRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if d.failOnce[ref] {
				d.failOnce[ref] = false
				job(w, "recreate-"+ref, "simulated: the machine didn't start")
				return
			}
			d.recreated[ref] = req
			job(w, "recreate-"+ref, "")
		case path == "/v1/migration/check":
			var req api.MigrationCheckRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(api.MigrationCheck{OK: req.Backup != "", Found: []string{"2 project(s): blog, shop"}})
		case strings.HasSuffix(path, "/log"):
			_, _ = w.Write([]byte("did " + strings.TrimSuffix(strings.TrimPrefix(path, "/v1/jobs/"), "/log") + "\n"))
		case strings.HasPrefix(path, "/v1/jobs/"):
			_ = json.NewEncoder(w).Encode(d.jobs[strings.TrimPrefix(path, "/v1/jobs/")])
		default:
			http.NotFound(w, r)
		}
	})
	if err := os.MkdirAll(filepath.Dir(p.Socket()), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", p.Socket())
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return d
}

func (d *fakeDaemon) called(what string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c == what {
			return true
		}
	}
	return false
}

// The steps of vm migrate on either side of the VM: the host's AgentBox
// stopped with what its agents were recorded first, its state.db backed up,
// the chat sessions copied out, then the base image, each agent's machine (a
// failed one tried again by the next run, and only it), and the check.
func TestMigrationSteps(t *testing.T) {
	p := clearEnv(t)
	hostStateDB(t, p)
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	calls := fakeHostIncus(t, `[
		{"name": "ab-shop-agent-01", "status": "Running", "config": {"limits.cpu": "2", "user.agentbox.cpu.configured": "2", "limits.memory": "3GiB"}},
		{"name": "ab-blog-agent-01", "status": "Stopped", "config": {"limits.cpu": "1"}},
		{"name": "kind-control-plane", "status": "Running", "config": {}}
	]`)
	// The fake's `incus file pull -r <instance>/<path> <dir>` makes what a
	// pull of a Claude Code session would, for shop's agent only.
	bin := hostIncus.Bin
	script, _ := os.ReadFile(bin)
	pull := "case \"$1 $2\" in \"file pull\") case \"$4\" in ab-shop-agent-01/*) mkdir -p \"$5/projects/-wt\" && echo '{}' > \"$5/projects/-wt/s.jsonl\" ;; *) echo 'Error: not found' >&2; exit 1 ;; esac ;; esac\n"
	if err := os.WriteFile(bin, append(script, []byte(pull)...), 0o755); err != nil {
		t.Fatal(err)
	}
	d := startFakeDaemon(t, p)
	ctx := context.Background()
	var log bytes.Buffer
	rec := &Migration{}

	if err := stopHost(ctx, p, rec, &log); err != nil {
		t.Fatalf("stopHost: %v\n%s", err, log.String())
	}
	if !d.called("POST /v1/agents/stop") || !d.called("POST /v1/shutdown") {
		t.Errorf("the host's daemon wasn't asked to stop its agents and itself: %v", d.calls)
	}
	saved, err := LoadMigration(p)
	if err != nil || saved == nil || len(saved.Agents) != 2 {
		t.Fatalf("what the agents were isn't recorded before anything is copied: %+v, %v", saved, err)
	}
	byRef := map[string]MovedAgent{}
	for _, a := range rec.Agents {
		byRef[a.Ref()] = a
	}
	shop, blog := byRef["shop/agent-01"], byRef["blog/agent-01"]
	if !shop.Running || blog.Running {
		t.Errorf("running: shop %v, blog %v; want shop only", shop.Running, blog.Running)
	}
	if !shop.hasMachine() || !blog.hasMachine() {
		t.Errorf("machines: shop %v, blog %v; want both", shop.hasMachine(), blog.hasMachine())
	}
	incusCalls, _ := os.ReadFile(calls)
	if !strings.Contains(string(incusCalls), "stop ab-shop-agent-01") {
		t.Errorf("shop's old machine wasn't stopped:\n%s", incusCalls)
	}
	if strings.Contains(string(incusCalls), "kind-control-plane") {
		t.Errorf("an instance of the user's own was touched:\n%s", incusCalls)
	}

	if err := backUp(ctx, p, rec, &log); err != nil {
		t.Fatal(err)
	}
	backup, err := readInstall(ctx, rec.Backup)
	if err != nil || len(backup.Agents) != 2 {
		t.Fatalf("the backup: %+v, %v", backup, err)
	}

	if err := stageHomes(ctx, p, rec, &log); err != nil {
		t.Fatal(err)
	}
	byRef = map[string]MovedAgent{}
	for _, a := range rec.Agents {
		byRef[a.Ref()] = a
	}
	if home := byRef["shop/agent-01"].Home; home == "" {
		t.Error("shop's chat sessions weren't staged")
	} else if _, err := os.Stat(filepath.Join(home, ".claude/projects/-wt/s.jsonl")); err != nil {
		t.Errorf("shop's session: %v", err)
	}
	if byRef["blog/agent-01"].Home != "" {
		t.Error("blog has sessions staged, and had none to copy")
	}
	incusCalls, _ = os.ReadFile(calls)
	if !strings.Contains(string(incusCalls), "file pull -r ab-shop-agent-01/home/"+u.Username+"/.claude/projects") {
		t.Errorf("no pull of shop's sessions:\n%s", incusCalls)
	}

	// The VM's daemon, from here on.
	d.up.Store(true)
	c := api.NewClient(p.Socket())
	if err := ensureImage(ctx, c, &log); err != nil {
		t.Fatal(err)
	}
	if !d.called("POST /v1/image/build") {
		t.Error("the VM's base image wasn't built")
	}
	d.failOnce["blog/agent-01"] = true
	err = makeMachines(ctx, p, c, rec, &log)
	if err == nil || !strings.Contains(err.Error(), "blog/agent-01") {
		t.Fatalf("makeMachines with blog failing = %v", err)
	}
	if req, ok := d.recreated["shop/agent-01"]; !ok || req.Stopped || req.Home == "" {
		t.Errorf("shop's recreate: %+v", req)
	}
	delete(d.recreated, "shop/agent-01")
	if err := makeMachines(ctx, p, c, rec, &log); err != nil {
		t.Fatal(err)
	}
	if _, again := d.recreated["shop/agent-01"]; again {
		t.Error("the rerun made shop's machine again")
	}
	if req, ok := d.recreated["blog/agent-01"]; !ok || !req.Stopped {
		t.Errorf("blog's recreate on the rerun: %+v, %v", req, ok)
	}

	if err := verify(ctx, p, c, rec, &log); err != nil {
		t.Fatal(err)
	}
	if saved, _ := LoadMigration(p); saved.Verified.IsZero() || len(saved.Found) == 0 {
		t.Errorf("the check isn't recorded: %+v", saved)
	}
	log.Reset()
	printMoved(&log, rec)
	for _, want := range []string{"Running in the VM, as they were: shop/agent-01", "Stopped, as they were: blog/agent-01", "What didn't come along", "agentbox vm migrate --remove-old"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("the summary doesn't say %q:\n%s", want, log.String())
		}
	}
}

// A migration whose check finds something missing isn't verified, so nothing
// of the host's can be removed.
func TestVerifyWithProblemsIsNotVerified(t *testing.T) {
	p := clearEnv(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/migration/check", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.MigrationCheck{Problems: []string{"shop/agent-01 has no machine here yet"}})
	})
	_ = os.MkdirAll(filepath.Dir(p.Socket()), 0o700)
	l, err := net.Listen("unix", p.Socket())
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	rec := &Migration{Backup: "/b.db"}
	var log bytes.Buffer
	err = verify(context.Background(), p, api.NewClient(p.Socket()), rec, &log)
	if err == nil || !rec.Verified.IsZero() || !strings.Contains(log.String(), "missing: shop/agent-01 has no machine here yet") {
		t.Errorf("verify = %v, verified %v\n%s", err, rec.Verified, log.String())
	}
}

// A state.db from any release is read with only the columns every release had.
func TestReadInstallOfAnOldRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	rw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE projects (name TEXT PRIMARY KEY, root TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL)`,
		`CREATE TABLE agents (project TEXT NOT NULL, name TEXT NOT NULL, instance TEXT NOT NULL UNIQUE, ai TEXT NOT NULL, autonomous INTEGER NOT NULL,
			branch TEXT NOT NULL, base_ref TEXT NOT NULL, base_commit TEXT NOT NULL, worktree TEXT NOT NULL, status TEXT NOT NULL, created_at INTEGER NOT NULL)`,
		`INSERT INTO projects VALUES ('app', '/home/u/app', 1)`,
		`INSERT INTO agents VALUES ('app', 'agent-01', 'ab-app-agent-01', 'claude', 1, 'agentbox/agent-01', 'main', 'abc', '/w', 'ready', 1)`,
	} {
		if _, err := rw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = rw.Close()
	inst, err := readInstall(context.Background(), path)
	if err != nil || len(inst.Projects) != 1 || len(inst.Agents) != 1 || inst.Agents[0].Instance != "ab-app-agent-01" {
		t.Errorf("readInstall = %+v, %v", inst, err)
	}
}

// vm migrate from start to end, with a VM whose ssh and Cloud Hypervisor
// steps are stand-ins: the host stopped and backed up, the VM set up without
// its daemon, the host's files copied in, then the daemon, the image, the
// machines and the check; and run again, it says it's done.
func TestMigrateCHV(t *testing.T) {
	vmState, steps := api.VMOff, []string{}
	vm, dir := fakeCHV(t, &vmState, &steps)
	p := vm.Paths
	t.Setenv("HOME", filepath.Dir(p.Data))
	hostStateDB(t, p)
	fakeHostIncus(t, `[{"name": "ab-shop-agent-01", "status": "Stopped", "config": {}}, {"name": "ab-blog-agent-01", "status": "Stopped", "config": {}}]`)
	d := startFakeDaemon(t, p)
	d.restart = 1500 * time.Millisecond
	var log bytes.Buffer
	opts := MigrateOptions{Want: vm.CHV.Config, Log: &log}
	if err := migrateCHV(context.Background(), p, opts); err != nil {
		t.Fatalf("migrateCHV: %v\n%s", err, log.String())
	}
	if want := []string{"tools", "disks", "start", "provisioned"}; !slices.Equal(steps, want) {
		t.Errorf("VM steps %q, want %q", steps, want)
	}
	ssh := strings.Join(calls(t, dir), "\n")
	if !strings.Contains(ssh, "state.db.moving") {
		t.Errorf("the host's files weren't copied into the VM:\n%s", ssh)
	}
	rec, err := LoadMigration(p)
	if err != nil || rec.Copied.IsZero() || rec.Verified.IsZero() || rec.Backup == "" {
		t.Fatalf("the migration: %+v, %v", rec, err)
	}
	for _, a := range rec.Agents {
		if !a.Made {
			t.Errorf("%s has no machine", a.Ref())
		}
	}
	if _, err := os.Stat(p.StateDB()); err != nil {
		t.Errorf("the host's state.db went: %v", err)
	}
	log.Reset()
	if err := migrateCHV(context.Background(), p, opts); err != nil || !strings.Contains(log.String(), "in its VM already") {
		t.Errorf("run again: %v\n%s", err, log.String())
	}
	st, err := migrationStatus(context.Background(), p)
	if err != nil || st.State != "verified" || !strings.Contains(describeMigration(st), "--remove-old") {
		t.Errorf("status: %+v, %v", st, err)
	}
}

func TestMigrateCHVWithNothingToMove(t *testing.T) {
	p := clearEnv(t)
	t.Setenv("HOME", filepath.Dir(p.Data))
	c := DefaultConfig("agentbox", "alice", 1000, 1000, filepath.Dir(p.Data), 8, 32<<30)
	err := migrateCHV(context.Background(), p, MigrateOptions{Want: c, Log: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "agentbox vm init") {
		t.Errorf("migrateCHV = %v", err)
	}
	for _, st := range []MigrationStatus{{State: "none"}, {State: "available", Projects: []string{"a"}}, {State: "started"}, {State: "verified"}, {State: "removed"}} {
		if describeMigration(st) == "" {
			t.Errorf("nothing to say of %s", st.State)
		}
	}
}
