package daemon

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/paths"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// fakeIncus stands in for incus: the base image is ready, instances have no
// devices, `copy` can be slowed down or made to fail, and deletions are logged.
const fakeIncus = `case "$1" in
  list) echo '[]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
  copy)
    sleep "${COPY_DELAY:-0}"
    if [ -n "$COPY_FAILS" ]; then echo "Error: simulated copy failure" >&2; exit 1; fi ;;
  delete) echo "$*" >> "$INCUS_LOG" ;;
esac
exit 0
`

type testDaemon struct {
	srv    *Server
	client *api.Client
	paths  paths.Paths
}

func startTestDaemon(t *testing.T, root, script string) testDaemon {
	t.Helper()
	t.Setenv("AGENTBOX_SOCKET", "")
	t.Setenv("INCUS_LOG", filepath.Join(root, "incus.log"))
	// Token checks must not leave the machine: a port nothing listens on
	// stands in for Anthropic, and a check against it answers "not checked".
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")
	if os.Getenv("AGENTBOX_PREVIEW_ADDR") == "" {
		t.Setenv("AGENTBOX_PREVIEW_ADDR", "off")
	}
	p := paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}
	bin := filepath.Join(root, "incus")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The update check must not leave the machine either. Tests run as a
	// "dev" build, which never checks; the ones about the check change the
	// version and put a fake server in AGENTBOX_UPDATE_URL.
	updateURL := cmp.Or(os.Getenv("AGENTBOX_UPDATE_URL"), "http://127.0.0.1:1")
	srv, err := New(Config{Paths: p, Incus: incus.Client{Bin: bin}, User: image.User{Name: "dev", UID: 1000, GID: 1000}, UpdateURL: updateURL})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing in a test may start a real AI tool. askLead already can't — a
	// lead with no adapter answers "no session" — but a distillation's aside
	// session (D78) would launch Claude Code on the host, so it is stubbed
	// here and overridden by the tests that are about it.
	srv.askAside = func(context.Context, state.Agent, string, string) (string, string, error) {
		return "", "", errors.New("this test starts no AI tool")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run() = %v", err)
		}
	})
	c := api.NewClient(p.Socket())
	waitFor(t, "the daemon to answer", func() bool { return c.Ping(context.Background()) == nil })
	return testDaemon{srv: srv, client: c, paths: p}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestProjectsAPI(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")

	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "hello-stack" || p.Branch != "main" || p.Root != repo {
		t.Errorf("AddProject() = %+v", p)
	}
	_, err = d.client.AddProject(ctx, api.AddProjectRequest{Path: repo})
	var se *api.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusConflict {
		t.Errorf("adding a project twice: got %v, want 409", err)
	}
	if projects, err := d.client.Projects(ctx); err != nil || len(projects) != 1 {
		t.Errorf("Projects() = %+v, %v", projects, err)
	}
	if brief, err := d.client.Brief(ctx, "hello-stack", "agent-07"); err != nil || !strings.Contains(brief, "`agentbox/agent-07`") {
		t.Errorf("Brief() = %q, %v", brief, err)
	}
	if _, ok, err := d.client.Base(ctx, "hello-stack"); ok || err != nil {
		t.Errorf("Base() = %v, %v; want no base", ok, err)
	}
	if err := d.client.RemoveProject(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if err := d.client.RemoveProject(ctx, "hello-stack"); !api.IsNotFound(err) {
		t.Errorf("removing a missing project: got %v, want 404", err)
	}
}

func TestClaudeAccountsAPI(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}

	auth, err := d.client.Auth(ctx)
	if err != nil || auth.Claude || len(auth.ClaudeAccounts) != 0 {
		t.Fatalf("Auth() with no login = %+v, %v", auth, err)
	}
	for _, account := range []string{"personal", "work"} {
		if err := d.client.SaveClaudeToken(ctx, api.ClaudeTokenRequest{Account: account, Token: "sk-ant-oat01-" + account}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.client.SetDefaultClaudeAccount(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	auth, err = d.client.Auth(ctx)
	if err != nil || !auth.Claude || len(auth.ClaudeAccounts) != 2 {
		t.Fatalf("Auth() = %+v, %v", auth, err)
	}
	if auth.ClaudeAccounts[1].Name != "work" || !auth.ClaudeAccounts[1].Default {
		t.Errorf("accounts = %+v", auth.ClaudeAccounts)
	}

	// A project picks an account, and only one that exists.
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{ClaudeAccount: ptr("nope")}); err == nil {
		t.Error("a project accepted an account that isn't stored")
	}
	p, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{ClaudeAccount: ptr("personal")})
	if err != nil || p.ClaudeAccount != "personal" {
		t.Fatalf("UpdateProject() = %+v, %v", p, err)
	}
	if p, err := d.client.Project(ctx, "hello-stack"); err != nil || p.ClaudeAccount != "personal" {
		t.Errorf("Project() = %+v, %v", p, err)
	}
	if p, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{ClaudeAccount: ptr("")}); err != nil || p.ClaudeAccount != "" {
		t.Errorf("clearing the project's account = %+v, %v", p, err)
	}

	// Removing an account leaves the other one as the default.
	if err := d.client.RemoveClaudeAccount(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	if auth, err = d.client.Auth(ctx); err != nil || len(auth.ClaudeAccounts) != 1 || !auth.ClaudeAccounts[0].Default {
		t.Errorf("Auth() after the removal = %+v, %v", auth, err)
	}
	if err := d.client.RemoveClaudeAccount(ctx, "work"); err == nil {
		t.Error("removing an unknown account succeeded")
	}
}

// TestGitHubAccountsAPI mirrors TestClaudeAccountsAPI. Accounts are seeded by
// writing tokens directly, since SaveGitHubToken checks them against GitHub
// over the network before storing them, which this test doesn't want to need.
func TestGitHubAccountsAPI(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}

	auth, err := d.client.Auth(ctx)
	if err != nil || auth.GitHub || len(auth.GitHubAccounts) != 0 {
		t.Fatalf("Auth() with no accounts = %+v, %v", auth, err)
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, account := range []string{"personal", "work"} {
		if err := creds.SaveGitHubToken(account, "gho_"+account); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.client.SetDefaultGitHubAccount(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	auth, err = d.client.Auth(ctx)
	if err != nil || !auth.GitHub || len(auth.GitHubAccounts) != 2 {
		t.Fatalf("Auth() = %+v, %v", auth, err)
	}
	if auth.GitHubAccounts[1].Name != "work" || !auth.GitHubAccounts[1].Default {
		t.Errorf("accounts = %+v", auth.GitHubAccounts)
	}

	// A project picks an account, and only one that exists.
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{GitHubAccount: ptr("nope")}); err == nil {
		t.Error("a project accepted an account that isn't stored")
	}
	p, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{GitHubAccount: ptr("personal")})
	if err != nil || p.GitHubAccount != "personal" {
		t.Fatalf("UpdateProject() = %+v, %v", p, err)
	}
	if p, err := d.client.Project(ctx, "hello-stack"); err != nil || p.GitHubAccount != "personal" {
		t.Errorf("Project() = %+v, %v", p, err)
	}
	if p, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{GitHubAccount: ptr("")}); err != nil || p.GitHubAccount != "" {
		t.Errorf("clearing the project's account = %+v, %v", p, err)
	}

	// Removing an account leaves the other one as the default.
	if err := d.client.RemoveGitHubAccount(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	if auth, err = d.client.Auth(ctx); err != nil || len(auth.GitHubAccounts) != 1 || !auth.GitHubAccounts[0].Default {
		t.Errorf("Auth() after the removal = %+v, %v", auth, err)
	}
	if err := d.client.RemoveGitHubAccount(ctx, "work"); err == nil {
		t.Error("removing an unknown account succeeded")
	}
}

// A project's accounts can be chosen as it is added, rather than picked
// afterwards on its page: the same names, checked the same way, so a typo is
// refused before the project exists rather than leaving it on the wrong
// account.
func TestAddProjectPicksItsAccounts(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("work", "sk-work"); err != nil {
		t.Fatal(err)
	}
	if err := creds.SaveGitHubToken("work", "gho_work"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		req  api.AddProjectRequest
		want string
	}{
		{req: api.AddProjectRequest{ClaudeAccount: "nope"}, want: `no Claude Code account named "nope"`},
		{req: api.AddProjectRequest{GitHubAccount: "nope"}, want: `no GitHub account named "nope"`},
	} {
		req := tc.req
		req.Path = testutil.FixtureRepo(t, "hello-stack")
		req.Name = "refused"
		_, err := d.client.AddProject(ctx, req)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("AddProject(%+v) = %v, want %s", tc.req, err, tc.want)
		}
		if _, err := d.client.Project(ctx, "refused"); err == nil {
			t.Error("the project was added anyway")
		}
	}

	p, err := d.client.AddProject(ctx, api.AddProjectRequest{
		Path: testutil.FixtureRepo(t, "hello-stack"), Name: "pawly", ClaudeAccount: "work", GitHubAccount: "work",
	})
	if err != nil || p.ClaudeAccount != "work" || p.GitHubAccount != "work" {
		t.Fatalf("AddProject() = %+v, %v", p, err)
	}
	// It is stored, not only echoed back.
	if stored, err := d.client.Project(ctx, "pawly"); err != nil || stored.ClaudeAccount != "work" || stored.GitHubAccount != "work" {
		t.Errorf("Project() = %+v, %v", stored, err)
	}
	// Nothing chosen still means the machine's default account.
	plain, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack"), Name: "plain"})
	if err != nil || plain.ClaudeAccount != "" || plain.GitHubAccount != "" {
		t.Errorf("AddProject() with no accounts = %+v, %v", plain, err)
	}
}

func TestFailedCreateJobRollsBack(t *testing.T) {
	t.Setenv("COPY_FAILS", "1")
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}

	events := make(chan api.Event, 256)
	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	go d.client.Events(eventsCtx, func(ev api.Event) error {
		events <- ev
		return nil
	})
	waitFor(t, "an event subscriber", func() bool { return d.srv.events.subscribers() > 0 })

	j, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", AI: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != api.JobRunning || j.Kind != "create" {
		t.Errorf("CreateAgent() = %+v", j)
	}
	var log bytes.Buffer
	if err := d.client.FollowJobLog(ctx, j.ID, &log); err != nil {
		t.Fatal(err)
	}
	if j, err = d.client.Job(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if j.Status != api.JobFailed || !strings.Contains(j.Error, "simulated copy failure") {
		t.Errorf("job after following it = %+v", j)
	}
	if !strings.Contains(log.String(), "instance failed; rolling back") {
		t.Errorf("job log:\n%s", log.String())
	}
	if agents, err := d.client.Agents(ctx, ""); err != nil || len(agents) != 0 {
		t.Errorf("agents after the rollback: %+v, %v", agents, err)
	}
	record, err := d.srv.store.Job(ctx, j.ID)
	if err != nil || record.Status != api.JobFailed || !strings.Contains(record.Log, "rolling back") {
		t.Errorf("stored job = %+v, %v", record, err)
	}

	var statuses []string
	for len(statuses) < 2 {
		select {
		case ev := <-events:
			var info api.Job
			if ev.Type == api.EventJob && json.Unmarshal(ev.Data, &info) == nil && info.ID == j.ID {
				statuses = append(statuses, info.Status)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("job events so far: %v", statuses)
		}
	}
	if statuses[0] != api.JobRunning || statuses[1] != api.JobFailed {
		t.Errorf("job events = %v, want [running failed]", statuses)
	}
}

func TestCancelJobRollsBack(t *testing.T) {
	t.Setenv("COPY_DELAY", "1")
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}

	j, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", AI: "none", Name: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the copy to start", func() bool {
		log, _ := d.client.JobLog(ctx, j.ID)
		return strings.Contains(log, "Creating instance")
	})
	j, err = d.client.CancelJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != api.JobCancelled {
		t.Errorf("CancelJob() = %+v, want cancelled", j)
	}
	if deleted, _ := os.ReadFile(filepath.Join(root, "incus.log")); !strings.Contains(string(deleted), "delete --force ab-hello-stack-slow") {
		t.Errorf("the rollback didn't delete the instance; incus deletions:\n%s", deleted)
	}
	if agents, _ := d.client.Agents(ctx, ""); len(agents) != 0 {
		t.Errorf("agents after cancelling: %+v", agents)
	}
}

// oneAgentIncus reports a single running instance, ab-hello-stack-agent-01.
const oneAgentIncus = `case "$1" in
  list) echo '[{"name": "ab-hello-stack-agent-01", "status": "Running", "state": {"network": {"eth0": {"addresses": [{"family": "inet", "address": "10.1.2.3"}]}}}}]' ;;
  query) echo '[]' ;;
esac`

// addTestAgent records hello-stack/agent-01 without creating anything.
func addTestAgent(t *testing.T, d testDaemon) state.Agent {
	t.Helper()
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", AI: "none",
		Branch: "agentbox/agent-01", Worktree: "/worktrees/agent-01", Status: state.AgentReady, CreatedAt: time.Now(),
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	return a
}

// busyBinaryIncus is fakeIncus for agents whose agentbox binary is running:
// like the real `incus file push`, which opens its target for writing, a push
// straight onto /usr/local/bin/agentbox fails with "text file busy".
const busyBinaryIncus = `case "$1" in
  query) echo '{"config": {}, "devices": {"agentbox": {}}}' ;;
  file)
    case "$4" in
      */usr/local/bin/agentbox) echo "Error: open /usr/local/bin/agentbox: text file busy" >&2; exit 1 ;;
    esac
    echo "$*" >> "$INCUS_LOG" ;;
  exec) echo "$*" >> "$INCUS_LOG" ;;
esac
exit 0
`

// A daemon started on a new build must hand that build to every running
// agent, even though each one is running the old binary as its MCP servers.
func TestReconcileUpdatesEveryReadyAgentsBinary(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, busyBinaryIncus)
	ctx := context.Background()
	addTestAgent(t, d)
	for _, a := range []state.Agent{
		{Name: "agent-02", Status: state.AgentReady},
		{Name: "agent-03", Status: state.AgentCreating},
	} {
		a.Project, a.AI, a.Instance, a.Branch, a.CreatedAt = "hello-stack", "none", "ab-hello-stack-"+a.Name, "agentbox/"+a.Name, time.Now()
		if err := d.srv.store.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	var log bytes.Buffer
	d.srv.cfg.Log = &log
	d.srv.cfg.Binary = filepath.Join(root, "agentbox")
	os.Remove(filepath.Join(root, "incus.log"))

	d.srv.reconcile(ctx) // what the daemon does when it starts

	if strings.Contains(log.String(), "in-agent API") {
		t.Errorf("reconcile logged a failure:\n%s", log.String())
	}
	got, _ := os.ReadFile(filepath.Join(root, "incus.log"))
	for _, inst := range []string{"ab-hello-stack-agent-01", "ab-hello-stack-agent-02"} {
		for _, want := range []string{
			"file push " + d.srv.cfg.Binary + " " + inst + "/usr/local/bin/agentbox.new --mode 0755",
			"exec " + inst + " -- mv -f /usr/local/bin/agentbox.new /usr/local/bin/agentbox",
		} {
			if !strings.Contains(string(got), want) {
				t.Errorf("incus calls lack %q:\n%s", want, got)
			}
		}
	}
	if strings.Contains(string(got), "agent-03") {
		t.Errorf("reconcile touched an agent still being created:\n%s", got)
	}
}

func TestNewSubscriberGetsEachAgentOnce(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	addTestAgent(t, d)

	events := make(chan api.Event, 64)
	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	go d.client.Events(eventsCtx, func(ev api.Event) error {
		events <- ev
		return nil
	})
	waitFor(t, "an event subscriber", func() bool { return d.srv.events.subscribers() > 0 })
	d.srv.refreshAgents(ctx) // what the watcher does every two seconds

	var changes []api.AgentChange
	for timeout := time.After(500 * time.Millisecond); ; {
		select {
		case ev := <-events:
			var change api.AgentChange
			if ev.Type == api.EventAgent && json.Unmarshal(ev.Data, &change) == nil {
				changes = append(changes, change)
			}
			continue
		case <-timeout:
		}
		break
	}
	if len(changes) != 1 || changes[0].Ref != "hello-stack/agent-01" || changes[0].State != "running" {
		t.Errorf("agent events for a new subscriber = %+v, want hello-stack/agent-01 running once", changes)
	}
}

func TestInAgentAPIOnlyDescribesItsAgent(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}

	socket := d.srv.agentSocketPath(a.Instance)
	if info, err := os.Stat(socket); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("in-agent socket %s: %v, %v", socket, info, err)
	}
	inAgent := api.NewClient(socket)
	self, err := inAgent.Self(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if self.Ref != "hello-stack/agent-01" || self.IP != "10.1.2.3" || self.State != "running" {
		t.Errorf("Self() = %+v", self)
	}
	if _, err := inAgent.Projects(ctx); err == nil || !strings.Contains(err.Error(), "not available inside an agent") {
		t.Errorf("listing projects from inside an agent: got %v", err)
	}
	if _, err := inAgent.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack"}); err == nil {
		t.Error("creating an agent from inside an agent succeeded")
	}
}

func TestPreviewProxyReachesAnAgentsPort(t *testing.T) {
	var sawHost string
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHost = r.Host
		fmt.Fprint(w, "hello from the agent")
	}))
	defer app.Close()
	_, appPort, _ := net.SplitHostPort(app.Listener.Addr().String())

	t.Setenv("AGENTBOX_PREVIEW_ADDR", "127.0.0.1:0")
	d := startTestDaemon(t, t.TempDir(), strings.ReplaceAll(oneAgentIncus, "10.1.2.3", "127.0.0.1"))
	addTestAgent(t, d)
	info, err := d.client.Preview(context.Background())
	if err != nil || info.Addr == "" {
		t.Fatalf("Preview() = %+v, %v", info, err)
	}
	_, previewPort, _ := net.SplitHostPort(info.Addr)

	get := func(host string) (int, string) {
		req, err := http.NewRequest(http.MethodGet, "http://"+info.Addr+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	host := appPort + ".agent-01.hello-stack.localhost:" + previewPort
	if code, body := get(host); code != http.StatusOK || body != "hello from the agent" || sawHost != host {
		t.Errorf("preview of %s = %d %q, and the agent's server saw Host %q", host, code, body, sawHost)
	}
	if code, body := get(appPort + ".agent-09.hello-stack.localhost"); code != http.StatusBadGateway {
		t.Errorf("preview of a missing agent = %d %q, want 502", code, body)
	}
	if code, _ := get("localhost:" + previewPort); code != http.StatusNotFound {
		t.Errorf("preview without an agent in the host name = %d, want 404", code)
	}
}

func TestRefusesSocketPathsTooLongForUnixSockets(t *testing.T) {
	t.Setenv("AGENTBOX_SOCKET", "")
	root := filepath.Join(t.TempDir(), strings.Repeat("d", 100))
	p := paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}
	srv, err := New(Config{Paths: p, Incus: incus.Client{Bin: "false"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "longer than unix sockets allow") {
		t.Errorf("Run() with a %d-byte socket path = %v", len(p.Socket()), err)
	}
}

func TestRestartFailsInterruptedJobs(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}
	st, err := state.Open(p.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddJob(context.Background(), state.Job{ID: "deadbeef", Kind: "create", Target: "hello-stack", Status: api.JobRunning, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	d := startTestDaemon(t, root, fakeIncus)
	j, err := d.client.Job(context.Background(), "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != api.JobFailed || !strings.Contains(j.Error, "daemon stopped") {
		t.Errorf("interrupted job after a restart = %+v", j)
	}

	if err := d.client.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the daemon to stop", func() bool { return d.client.Ping(context.Background()) != nil })
}

// A stored token Anthropic no longer takes is worth as much as no token at
// all: every agent on it fails with a 401 that explains nothing. The Setup
// page says so where the login is set up, and the account carries it to the
// app, which badges it.
func TestSetupReportsARejectedClaudeToken(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if err := d.client.SaveClaudeToken(ctx, api.ClaudeTokenRequest{Token: "sk-ant-oat01-example"}); err != nil {
		t.Fatal(err)
	}
	if c := setupCheck(t, d, "claude"); c.Status != api.SetupOK {
		t.Fatalf("a login nothing has refused is %+v", c)
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	// Let the check the page just asked for settle, so the rejection below is
	// the last word rather than a race with it.
	if _, err := creds.CheckClaudeAccount(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// What an agent's chat reports when its own turn is refused.
	if err := creds.RejectClaudeAccount("", "OAuth access token is invalid."); err != nil {
		t.Fatal(err)
	}
	c := setupCheck(t, d, "claude")
	// A rejection is a warning, not a reason to hold up the wizard: a stored
	// token still counts as a login, and the wizard's own skippable() check
	// never blocks on the claude step, only on Required checks.
	if c.Status != api.SetupWarn || !strings.Contains(c.Detail, "rejected") || c.Fix != "agentbox auth claude" {
		t.Errorf("a rejected login is %+v", c)
	}
	auth, err := d.client.Auth(ctx)
	if err != nil || len(auth.ClaudeAccounts) != 1 || auth.ClaudeAccounts[0].Valid != "rejected" {
		t.Fatalf("Auth() = %+v, %v", auth, err)
	}

	// Storing a new token is a fresh start: nothing has refused this one.
	if err := d.client.SaveClaudeToken(ctx, api.ClaudeTokenRequest{Token: "sk-ant-oat01-new"}); err != nil {
		t.Fatal(err)
	}
	if c := setupCheck(t, d, "claude"); c.Status != api.SetupOK {
		t.Errorf("after logging in again, the check is %+v", c)
	}
	if auth, err := d.client.Auth(ctx); err != nil || auth.ClaudeAccounts[0].Valid != "" {
		t.Errorf("Auth() after logging in again = %+v, %v", auth, err)
	}
	if auth.ClaudeAccounts[0].SavedAt.IsZero() {
		t.Error("the new token has no saved date")
	}
}
