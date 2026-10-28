package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/hubapi"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/hubtest"
	"agentbox/internal/remote"
	"agentbox/internal/state"
)

const helloStackRepo = "https://github.com/acme/hello-stack.git"

// peerMachines are two daemons connected to one stand-in hub as the same
// account's environments: laptop, whose project hello-stack has a remote on
// GitHub, and vps, which has no project yet and "clones" one by copying a
// fixture.
type peerMachines struct {
	hub         *hubtest.Hub
	laptop, vps testDaemon
	vpsID       string
	lead        *api.Client // laptop's hello-stack lead
	clones      *int
	failClone   *bool
	laptopToken string
	laptopEnvID string
}

func startPeerMachines(t *testing.T) peerMachines {
	t.Helper()
	ctx := context.Background()
	h := hubtest.New(t)
	laptopEnv, vpsEnv := h.AddEnvironment("laptop"), h.AddEnvironment("vps")
	laptop := startTestDaemon(t, t.TempDir(), fakeIncus)
	vps := startTestDaemon(t, t.TempDir(), readyOneAgentIncus)
	if err := (credentials.Store{Dir: vps.paths.Credentials()}).SaveClaudeToken("default", "sk-ant-oat01-vps"); err != nil {
		t.Fatal(err)
	}

	repo := laptop.fixtureRepo(t, "hello-stack")
	gitIn(t, repo, "remote", "add", "origin", helloStackRepo)
	if _, err := laptop.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	m := peerMachines{hub: h, laptop: laptop, vps: vps, vpsID: vpsEnv.Environment.ID, clones: new(int), failClone: new(bool),
		laptopToken: laptopEnv.Token, laptopEnvID: laptopEnv.Environment.ID}
	fixture := vps.fixtureRepo(t, "hello-stack")
	vps.srv.peerClone = func(_ context.Context, repo, dest string) error {
		*m.clones++
		if *m.failClone {
			return errors.New("cloning: repository not found")
		}
		if out, err := exec.Command("git", "clone", "--quiet", fixture, dest).CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %w: %s", err, out)
		}
		if out, err := exec.Command("git", "-C", dest, "remote", "set-url", "origin", repo).CombinedOutput(); err != nil {
			return fmt.Errorf("git remote: %w: %s", err, out)
		}
		return nil
	}
	vps.srv.peerCloneRoot = filepath.Join(vps.root, "src")
	for _, d := range []struct {
		d     testDaemon
		token string
	}{{laptop, laptopEnv.Token}, {vps, vpsEnv.Token}} {
		if st, err := d.d.client.ConnectRemote(ctx, api.RemoteConnectRequest{Hub: h.URL, Token: d.token}); err != nil || !st.Connected {
			t.Fatalf("ConnectRemote = %+v, %v", st, err)
		}
	}
	m.lead = api.NewClient(laptop.srv.leadSocketPath("hello-stack"))
	return m
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestLeadDirectsAgentsOnAnotherMachine is a lead on laptop making and
// directing an agent on vps through the hub: vps clones the project the first
// time, the agent runs there, and its notices come back to laptop.
func TestLeadDirectsAgentsOnAnotherMachine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := startPeerMachines(t)

	machines, err := m.lead.ProjectMachines(ctx)
	if err != nil || len(machines) != 1 || machines[0].Name != "vps" || !machines[0].Online || machines[0].HasProject || machines[0].Error != "" {
		t.Fatalf("machines = %+v, %v; want vps, online, without the project", machines, err)
	}

	t.Run("a create refused after cloning leaves nothing behind", func(t *testing.T) {
		_, err := m.lead.OnMachine("vps").CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders", Task: "add it", Branch: "Not A Branch!"})
		if err == nil {
			t.Fatal("a bad branch was accepted")
		}
		if projects, _ := m.vps.client.Projects(ctx); len(projects) != 0 {
			t.Errorf("vps kept %+v", projects)
		}
	})

	t.Run("a failed clone says why", func(t *testing.T) {
		*m.failClone = true
		defer func() { *m.failClone = false }()
		_, err := m.lead.OnMachine("vps").CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders", Task: "add it"})
		if err == nil || !strings.Contains(err.Error(), "repository not found") {
			t.Errorf("create with a failing clone: %v", err)
		}
	})

	job, err := m.lead.OnMachine("vps").CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders", Task: "add it", Branch: "reminders"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.vps.client.FollowJobLog(ctx, job.ID, io.Discard); err != nil {
		t.Fatal(err)
	}
	if j, err := m.vps.client.Job(ctx, job.ID); err != nil || j.Status != api.JobSucceeded {
		t.Fatalf("the job on vps = %+v, %v", j, err)
	}
	projects, err := m.vps.client.Projects(ctx)
	if err != nil || len(projects) != 1 || projects[0].Name != "hello-stack" {
		t.Fatalf("vps's projects = %+v, %v", projects, err)
	}
	origin, err := m.vps.srv.store.PeerAgent(ctx, "hello-stack", "agent-01")
	if err != nil || origin.OriginID != m.laptopEnvID || origin.OriginName != "laptop" || origin.OriginProject != "hello-stack" {
		t.Errorf("vps's record of who made agent-01 = %+v, %v", origin, err)
	}

	remoteAgent := func() api.RemoteAgent {
		fleet, err := m.laptop.client.Fleet(ctx, "hello-stack")
		if err != nil || len(fleet.Remote) != 1 {
			return api.RemoteAgent{}
		}
		return fleet.Remote[0]
	}
	waitFor(t, "laptop to hear agent-01 started on vps", func() bool {
		r := remoteAgent()
		return r.Name == "agent-01" && r.Machine == "vps" && r.Status == state.RemoteWorking && r.Title == "Reminders"
	})

	if machines, err := m.lead.ProjectMachines(ctx); err != nil || len(machines) != 1 || !machines[0].HasProject || machines[0].Project != "hello-stack" {
		t.Errorf("machines after the clone = %+v, %v", machines, err)
	}
	fleet, err := m.lead.OnMachine("vps").ProjectFleet(ctx)
	if err != nil || len(fleet.Agents) != 1 || fleet.Agents[0].Name != "agent-01" {
		t.Errorf("list_agents on vps = %+v, %v", fleet, err)
	}
	// The refused create's clone, taken back, the failed one, and this one's.
	if *m.clones != 3 {
		t.Errorf("vps cloned %d times, want 3", *m.clones)
	}
	if p, cloned, err := m.vps.srv.peerProjectFor(ctx, "git@github.com:acme/hello-stack.git", true); err != nil || cloned || p.Name != "hello-stack" {
		t.Errorf("the project again, by its ssh URL = %s, cloned %v, %v; want it found", p.Name, cloned, err)
	}

	t.Run("its question reaches the lead that made it, and the lead answers it there", func(t *testing.T) {
		a, err := m.vps.srv.store.Agent(ctx, "hello-stack", "agent-01")
		if err != nil {
			t.Fatal(err)
		}
		answered := make(chan api.Question, 1)
		go func() {
			q, _ := m.vps.srv.askForTest(ctx, a, "Paginate the reminders?", "building the page")
			answered <- q
		}()
		id := waitForQuestion(t, m.vps, "hello-stack")
		waitFor(t, "laptop to hear agent-01 asks", func() bool {
			r := remoteAgent()
			return r.Status == state.RemoteAsking && r.Summary == "Paginate the reminders?"
		})
		questions, err := m.lead.OnMachine("vps").ProjectQuestions(ctx)
		if err != nil || len(questions) != 1 || questions[0].ID != id {
			t.Fatalf("questions on vps = %+v, %v", questions, err)
		}
		if _, err := m.lead.OnMachine("vps").LeadAnswerQuestion(ctx, id, "Yes, 20 a page."); err != nil {
			t.Fatal(err)
		}
		if q := <-answered; q.Answer != "Yes, 20 a page." {
			t.Errorf("the agent got %+v", q)
		}
	})

	t.Run("its finish comes back with its pull request", func(t *testing.T) {
		a, _ := m.vps.srv.store.Agent(ctx, "hello-stack", "agent-01")
		m.vps.srv.tellLeadAbout(ctx, a, "agent-01 finished.", chat.NoticeOptions{}, peerEvent{Status: state.RemoteDone, Summary: "Opened #12", PRURL: "https://github.com/acme/hello-stack/pull/12"})
		waitFor(t, "laptop to hear agent-01 finished", func() bool {
			r := remoteAgent()
			return r.Status == state.RemoteDone && r.PRURL == "https://github.com/acme/hello-stack/pull/12" && r.Title == "Reminders"
		})
	})

	t.Run("only the lead routes its tools use reach the other machine", func(t *testing.T) {
		_, err := m.lead.OnMachine("vps").ProjectNotes(ctx)
		if err == nil || !strings.Contains(err.Error(), "can't be sent to another machine") {
			t.Errorf("notes on vps: %v", err)
		}
	})

	t.Run("a machine that isn't there", func(t *testing.T) {
		_, err := m.lead.OnMachine("nope").ProjectFleet(ctx)
		if err == nil || !strings.Contains(err.Error(), `no machine is called "nope"`) || !strings.Contains(err.Error(), "vps") {
			t.Errorf("list_agents on nope: %v", err)
		}
	})

	t.Run("an offline machine: a clear error, and nothing made", func(t *testing.T) {
		if err := m.vps.client.DisconnectRemote(ctx); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "vps to go offline", func() bool {
			_, peers, err := m.laptop.srv.hubPeers(ctx, true)
			return err == nil && len(peers) == 1 && !peers[0].Online
		})
		before, _ := m.vps.client.Agents(ctx, "hello-stack")
		_, err := m.lead.OnMachine("vps").CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Offline", Task: "nothing"})
		if err == nil || !strings.Contains(err.Error(), "vps is offline") || !strings.Contains(err.Error(), "nothing was sent") {
			t.Errorf("create on an offline machine: %v", err)
		}
		if after, _ := m.vps.client.Agents(ctx, "hello-stack"); len(after) != len(before) {
			t.Errorf("vps has %d agents, had %d", len(after), len(before))
		}
		machines, err := m.lead.ProjectMachines(ctx)
		if err != nil || len(machines) != 1 || machines[0].Online {
			t.Errorf("machines with vps offline = %+v, %v", machines, err)
		}
	})
}

// TestPeersAreTheAccountsOnly: a machine refuses a peer the hub passed on
// that isn't its own account's, and a device can't pass for a peer.
func TestPeersAreTheAccountsOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := startPeerMachines(t)
	theirs := m.hub.AddEnvironmentOf("usr_other", "theirs")

	get := func(base, token string, header ...string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/peer/project", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(headerPeerRepo, helloStackRepo)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var e api.Error
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return resp.StatusCode, e.Error
	}
	if status, msg := get(remote.PeerURL(m.hub.URL, m.vpsID), theirs.Token); status != http.StatusForbidden || !strings.Contains(msg, "theirs isn't connected to this machine's hub account") {
		t.Errorf("another account's machine: %d %q", status, msg)
	}
	if status, _ := get(remote.PeerURL(m.hub.URL, m.vpsID), m.laptopToken); status != http.StatusOK {
		t.Errorf("laptop, of the same account: %d", status)
	}
	device := m.hub.URL + hubapi.EnvironmentAPI(m.vpsID)
	if status, msg := get(device, m.hub.Token(), hubapi.HeaderPeer, m.laptopEnvID); status != http.StatusForbidden || !strings.Contains(msg, "only another machine") {
		t.Errorf("a device passing for laptop: %d %q", status, msg)
	}
}

func TestRepoKey(t *testing.T) {
	t.Parallel()
	for _, u := range []string{
		"https://github.com/Acme/hello-stack.git",
		"https://github.com/acme/hello-stack",
		"https://someone@github.com/acme/hello-stack/",
		"git@github.com:acme/hello-stack.git",
		"ssh://git@github.com:22/acme/hello-stack.git",
	} {
		if !sameRepo(u, helloStackRepo) {
			t.Errorf("%s isn't %s: %q", u, helloStackRepo, repoKey(u))
		}
	}
	for _, u := range []string{"https://github.com/acme/other", "https://gitlab.com/acme/hello-stack", ""} {
		if sameRepo(u, helloStackRepo) {
			t.Errorf("%s is %s", u, helloStackRepo)
		}
	}
	for u, ok := range map[string]bool{
		helloStackRepo: true, "git@github.com:acme/x.git": true, "ssh://h/x": true,
		"/home/lint/src/x": false, "file:///home/x": false, "C:\\src\\x": false, "../x": false,
	} {
		if err := cloneable(u); (err == nil) != ok {
			t.Errorf("cloneable(%q) = %v", u, err)
		}
	}
}

func TestPeerLeadRoutes(t *testing.T) {
	t.Parallel()
	allowed := []string{"GET /v1/project/agents", "POST /v1/project/agents", "POST /v1/project/agents/agent-01/chat/messages",
		"GET /v1/project/agents/agent-01/diff", "POST /v1/project/retire", "POST /v1/project/questions/q1/answer"}
	refused := []string{"GET /v1/project/notes", "POST /v1/project/groups", "GET /v1/project/secrets", "POST /v1/project/copy-from-host",
		"POST /v1/project/questions/q1/fulfil", "DELETE /v1/project/agents", "GET /v1/projects"}
	for _, r := range slices.Concat(allowed, refused) {
		method, route, _ := strings.Cut(r, " ")
		if got, want := peerLeadRoute(method, route), slices.Contains(allowed, r); got != want {
			t.Errorf("peerLeadRoute(%s) = %v, want %v", r, got, want)
		}
	}
}
