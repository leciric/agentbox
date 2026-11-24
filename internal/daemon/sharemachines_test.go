package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// shareMachines are two paired daemons: alice shares hello-stack, which has a
// remote on GitHub, with bob, whose computer takes the agents her project's
// chat makes for him, cloning the project the first time ("clones" a fixture).
type shareMachines struct {
	alice, bob testDaemon
	peer       api.SharePeer   // bob, on alice
	remote     api.ShareRemote // alice, on bob
	local      string          // hello-stack's name on bob: <remote>~hello-stack
	lead       *api.Client     // alice's hello-stack lead
	push       *atomic.Bool    // whether bob's GitHub account may push
}

func startShareMachines(t *testing.T) shareMachines {
	t.Helper()
	ctx := context.Background()
	alice := startTestDaemon(t, t.TempDir(), fakeIncus)
	bob := startTestDaemon(t, t.TempDir(), readyOneAgentIncus)
	bob.srv.chat.Launch = (&hangTool{}).launch
	creds := credentials.Store{Dir: bob.paths.Credentials()}
	if err := creds.SaveClaudeToken("default", "sk-ant-oat01-bob"); err != nil {
		t.Fatal(err)
	}
	if err := creds.SaveGitHubToken("bob", "ghp_bob"); err != nil {
		t.Fatal(err)
	}
	push := new(atomic.Bool)
	push.Store(true)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/hello-stack" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"default_branch":"main","permissions":{"push":%v}}`, push.Load())
	}))
	t.Cleanup(gh.Close)
	bob.setGitHub(t, gh.URL)
	fixture := bob.fixtureRepo(t, "hello-stack")
	bob.srv.cloneHook = func(_ context.Context, repo, dest string) error {
		if out, err := exec.Command("git", "clone", "--quiet", fixture, dest).CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %w: %s", err, out)
		}
		if out, err := exec.Command("git", "-C", dest, "remote", "set-url", "origin", repo).CombinedOutput(); err != nil {
			return fmt.Errorf("git remote: %w: %s", err, out)
		}
		return nil
	}
	bob.srv.cloneRoot = filepath.Join(bob.root, "src")

	repo := alice.fixtureRepo(t, "hello-stack")
	gitIn(t, repo, "remote", "add", "origin", helloStackRepo)
	if _, err := alice.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	alice.srv.lan.mu.Lock()
	alice.srv.lan.own = true
	alice.srv.lan.addresses = func() []string { return []string{"127.0.0.1"} }
	alice.srv.lan.mu.Unlock()
	port, on, name := freePort(t), true, "Alice"
	if _, err := alice.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &on, Port: &port}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.client.UpdateShare(ctx, api.UpdateShareRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	bobName := "Bob"
	if _, err := bob.client.UpdateShare(ctx, api.UpdateShareRequest{Name: &bobName}); err != nil {
		t.Fatal(err)
	}
	pairing, err := alice.client.PairShare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := bob.client.AddShareRemote(ctx, pairing.Link)
	if err != nil {
		t.Fatal(err)
	}
	st, err := alice.client.Share(ctx)
	if err != nil || len(st.Peers) != 1 {
		t.Fatalf("Share() = %+v, %v", st, err)
	}
	peer := st.Peers[0]
	if !peer.OwnMachine {
		t.Errorf("a new peer = %+v; want it allowed to run agents on its own computer", peer)
	}
	if peer, err = alice.client.UpdateSharePeer(ctx, peer.ID, api.UpdateSharePeerRequest{Projects: &[]string{"hello-stack"}}); err != nil {
		t.Fatal(err)
	}
	m := shareMachines{alice: alice, bob: bob, peer: peer, remote: remote, local: remote.ID + "~hello-stack",
		lead: api.NewClient(alice.srv.leadSocketPath("hello-stack")), push: push}
	waitFor(t, "bob's computer to connect to alice", func() bool {
		st, err := alice.client.Share(ctx)
		return err == nil && len(st.Peers) == 1 && st.Peers[0].Machine
	})
	waitFor(t, "bob to say it is connected", func() bool {
		st, err := bob.client.Share(ctx)
		return err == nil && len(st.Remotes) == 1 && st.Remotes[0].Machine
	})
	return m
}

// TestShareGuestRunsAgents: a lead makes agents for the paired AgentBox on
// its own computer, by name or by the choice its composer sent, reaches only
// those there, and hears from them; both sides see them, marked.
func TestShareGuestRunsAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := startShareMachines(t)

	// The composer's "On my PC": allowed, with nothing in its way.
	if got, err := m.bob.client.ShareRunOn(ctx, m.local); err != nil || !got.Here || got.Owner != "Alice" {
		t.Errorf("ShareRunOn() = %+v, %v; want it possible", got, err)
	}

	machines, err := m.lead.ProjectMachines(ctx)
	if err != nil || len(machines) != 1 || machines[0].Name != "Bob" || !machines[0].Shared || !machines[0].Online || machines[0].HasProject {
		t.Fatalf("machines = %+v, %v; want Bob's computer, online, without the project", machines, err)
	}

	// Named by the lead, as a hub machine is: the agent runs on Bob's
	// computer, in the project cloned there, on Bob's accounts.
	job, err := m.lead.OnMachine("Bob").CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders", Task: "add it", Branch: "reminders", ClaudeAccount: "alice-work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.bob.client.FollowJobLog(ctx, job.ID, io.Discard); err != nil {
		t.Fatal(err)
	}
	if j, err := m.bob.client.Job(ctx, job.ID); err != nil || j.Status != api.JobSucceeded {
		t.Fatalf("the job on Bob's computer = %+v, %v", j, err)
	}
	origin, err := m.bob.srv.store.PeerAgent(ctx, "hello-stack", "agent-01")
	if err != nil || origin.OriginID != shareOriginPrefix+m.remote.ID || origin.OriginName != "Alice" || origin.OriginProject != "hello-stack" {
		t.Errorf("Bob's record of who made agent-01 = %+v, %v", origin, err)
	}
	if a, err := m.bob.srv.store.Agent(ctx, "hello-stack", "agent-01"); err != nil || a.ClaudeAccount == "alice-work" {
		t.Errorf("agent-01 on Bob = %+v, %v; want none of Alice's accounts", a, err)
	}

	// Both sides see it, marked with its machine: Alice's fleet, and Bob's
	// view of the project, where it is his own.
	remoteOn := func(d testDaemon, project string) api.RemoteAgent {
		fleet, err := d.client.Fleet(ctx, project)
		if err != nil || len(fleet.Remote) != 1 {
			return api.RemoteAgent{}
		}
		return fleet.Remote[0]
	}
	waitFor(t, "Alice to hear agent-01 started on Bob's computer", func() bool {
		r := remoteOn(m.alice, "hello-stack")
		return r.Name == "agent-01" && r.Machine == "Bob" && r.Shared && !r.Here && r.Status == state.RemoteWorking && r.Title == "Reminders"
	})
	if r := remoteOn(m.bob, m.local); r.Name != "agent-01" || !r.Here || !r.Shared {
		t.Errorf("agent-01 in Bob's view of the project = %+v; want it marked as his", r)
	}
	if st, _ := m.alice.client.Share(ctx); st.Peers[0].Running != 0 {
		t.Errorf("Bob's slots on Alice count %d; want none for an agent on his computer", st.Peers[0].Running)
	}

	// Its finish reaches Alice's lead, with its pull request.
	a, _ := m.bob.srv.store.Agent(ctx, "hello-stack", "agent-01")
	m.bob.srv.tellLeadAbout(ctx, a, "agent-01 finished.", chat.NoticeOptions{}, peerEvent{Status: state.RemoteDone, Summary: "Opened #12", PRURL: "https://github.com/acme/hello-stack/pull/12"})
	waitFor(t, "Alice to hear agent-01 finished", func() bool {
		r := remoteOn(m.alice, "hello-stack")
		return r.Status == state.RemoteDone && r.PRURL == "https://github.com/acme/hello-stack/pull/12"
	})

	// Alice's lead reaches the agents it made there, and none of Bob's own.
	if err := m.bob.srv.store.AddAgent(ctx, state.Agent{Project: "hello-stack", Name: "agent-09", Instance: "ab-hello-stack-agent-09", AI: "claude", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	fleet, err := m.lead.OnMachine("Bob").ProjectFleet(ctx)
	if err != nil || len(fleet.Agents) != 1 || fleet.Agents[0].Name != "agent-01" {
		t.Errorf("list_agents on Bob = %+v, %v; want agent-01 alone", fleet, err)
	}
	if _, err := m.lead.OnMachine("Bob").AgentChat(ctx, "agent-09"); err == nil || !strings.Contains(err.Error(), "isn't one of the agents your lead made") {
		t.Errorf("reading Bob's own agent's chat: %v", err)
	}
	if _, err := m.lead.OnMachine("Bob").ProjectNotes(ctx); err == nil {
		t.Error("notes reached Bob's computer")
	}
	if res, err := m.lead.OnMachine("Bob").RetireProject(ctx, api.RetireRequest{Agents: []string{"agent-09"}}); err == nil {
		t.Errorf("retiring Bob's own agent = %+v", res)
	}

	// The composer's choice: a message from Bob with "On my PC" sends the
	// session's create_agent to his computer with no machine named, until
	// someone at Alice's writes there.
	peer, _ := m.alice.srv.store.SharePeer(ctx, m.peer.ID)
	pctx := context.WithValue(ctx, sharePeerKey{}, peer)
	lead := state.Agent{Project: "hello-stack", Name: state.LeadSessionName(state.MigratedSession), Role: state.RoleLead}
	m.alice.srv.heardInChat(pctx, lead)
	if err := m.alice.srv.choseRunOn(pctx, lead, api.RunOnPeer); err != nil {
		t.Fatal(err)
	}
	job, err = m.lead.CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Login", Task: "fix it", Branch: "login"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.bob.client.Job(ctx, job.ID); err != nil {
		t.Errorf("the session's create didn't go to Bob's computer: %v", err)
	}
	m.alice.srv.heardInChat(ctx, lead) // Alice writes
	if got := m.alice.srv.runsOnPeer("hello-stack", state.MigratedSession); got != "" {
		t.Errorf("after Alice wrote, the session's agents go to %q", got)
	}

	// Alice turns it off: the choice is refused, Bob's computer is let go,
	// and his composer says why.
	off := false
	if _, err := m.alice.client.UpdateSharePeer(ctx, m.peer.ID, api.UpdateSharePeerRequest{OwnMachine: &off}); err != nil {
		t.Fatal(err)
	}
	peer, _ = m.alice.srv.store.SharePeer(ctx, m.peer.ID)
	pctx = context.WithValue(ctx, sharePeerKey{}, peer)
	var refused shareRefused
	if err := m.alice.srv.choseRunOn(pctx, lead, api.RunOnPeer); !errors.As(err, &refused) {
		t.Errorf("choosing Bob's computer once Alice turned it off = %v", err)
	}
	waitFor(t, "Bob's computer to be let go", func() bool {
		st, err := m.alice.client.Share(ctx)
		return err == nil && !st.Peers[0].Machine
	})
	if _, err := m.lead.OnMachine("Bob").ProjectFleet(ctx); err == nil || !strings.Contains(err.Error(), "isn't allowed") {
		t.Errorf("list_agents on Bob once turned off: %v", err)
	}
	waitFor(t, "Bob's composer to say why", func() bool {
		got, err := m.bob.client.ShareRunOn(ctx, m.local)
		return err == nil && !got.Here && strings.Contains(got.Why, "doesn't let this AgentBox run agents on its own computer")
	})
}

// TestShareRunOnSaysWhyNot: "On my PC" is refused with the reason when Bob's
// GitHub account can't push to the project.
func TestShareRunOnSaysWhyNot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := startShareMachines(t)
	m.push.Store(false)
	got, err := m.bob.client.ShareRunOn(ctx, m.local)
	if err != nil || got.Here || !strings.Contains(got.Why, "can't push to acme/hello-stack") {
		t.Errorf("ShareRunOn() without push access = %+v, %v", got, err)
	}
}

// The gateway's own paths a peer's computer uses are the peer's alone.
func TestShareMachinePathsNeedTheToken(t *testing.T) {
	t.Parallel()
	m := startShareMachines(t)
	st, _ := m.bob.srv.store.ShareRemotes(context.Background())
	gw := newPhone(t, st[0].URL, nil)
	for _, c := range []struct{ method, path string }{
		{"GET", "/share/machine"},
		{"GET", "/share/run-on?project=hello-stack"},
		{"POST", "/share/agent-events"},
	} {
		if code, body := gw.do(c.method, c.path, `{"project":"hello-stack","agent":"agent-01"}`, "Authorization", "Bearer share_peer_x.nope"); code != http.StatusUnauthorized {
			t.Errorf("%s %s with a wrong token = %d %s", c.method, c.path, code, body)
		}
	}
}
