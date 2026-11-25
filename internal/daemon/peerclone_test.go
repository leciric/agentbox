package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// readyFailingCopyIncus is readyOneAgentIncus where the base can't be copied,
// so a create's job fails once it has begun: after the clone it was for.
var readyFailingCopyIncus = strings.Replace(readyOneAgentIncus, "  delete)",
	"  copy) echo \"Error: simulated copy failure\" >&2; exit 1 ;;\n  delete)", 1)

// peerCloneDaemon is a daemon that "clones" hello-stack by copying a fixture,
// as a hub peer's or a guest's does the first time an agent is asked for there.
func peerCloneDaemon(t *testing.T) testDaemon {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	fixture := d.fixtureRepo(t, "hello-stack")
	d.srv.cloneHook = func(_ context.Context, repo, dest string) error {
		if out, err := exec.Command("git", "clone", "--quiet", fixture, dest).CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %w: %s", err, out)
		}
		if out, err := exec.Command("git", "-C", dest, "remote", "set-url", "origin", repo).CombinedOutput(); err != nil {
			return fmt.Errorf("git remote: %w: %s", err, out)
		}
		return nil
	}
	d.srv.cloneRoot = filepath.Join(d.root, "src")
	return d
}

func hasProject(d testDaemon, name string) bool {
	_, err := d.srv.store.Project(context.Background(), name)
	return err == nil
}

// clonedProjects are the projects on d's own disk: not the other AgentBox's
// that a guest sees beside them.
func clonedProjects(t *testing.T, d testDaemon) []string {
	t.Helper()
	projects, err := d.client.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range projects {
		if p.Remote == nil {
			names = append(names, p.Name)
		}
	}
	return names
}

// waitNoClone waits for d to have taken back a project it cloned: its row and
// its files, which go in that order.
func waitNoClone(t *testing.T, d testDaemon) {
	t.Helper()
	waitFor(t, "the clone to be taken back", func() bool {
		entries, _ := os.ReadDir(filepath.Join(d.root, "src"))
		return len(clonedProjects(t, d)) == 0 && len(entries) == 0
	})
}

// A project cloned for a peer stays only while something uses it: a create
// still under way, an agent made, or anything else in it (claimPeerProject).
func TestPeerClone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("last hold takes it", func(t *testing.T) {
		d := peerCloneDaemon(t)
		p, err := d.srv.claimPeerProject(ctx, helloStackRepo)
		if err != nil {
			t.Fatal(err)
		}
		d.srv.holdPeerProject(p.Name) // a group's second agent
		d.srv.releasePeerProject(ctx, p.Name, false)
		if !hasProject(d, p.Name) {
			t.Fatal("the clone went with a create still under way in it")
		}
		d.srv.releasePeerProject(ctx, p.Name, false)
		if hasProject(d, p.Name) {
			t.Error("the clone stayed after every create in it failed")
		}
		if _, err := os.Stat(p.Root); !os.IsNotExist(err) {
			t.Errorf("the clone's files stayed: %v", err)
		}
	})

	t.Run("made keeps it", func(t *testing.T) {
		d := peerCloneDaemon(t)
		p, err := d.srv.claimPeerProject(ctx, helloStackRepo)
		if err != nil {
			t.Fatal(err)
		}
		d.srv.releasePeerProject(ctx, p.Name, true)
		if !hasProject(d, p.Name) {
			t.Fatal("the clone went after an agent was made in it")
		}
		again, err := d.srv.claimPeerProject(ctx, helloStackRepo)
		if err != nil || again.Name != p.Name {
			t.Fatalf("claiming it again = %q, %v", again.Name, err)
		}
		d.srv.releasePeerProject(ctx, again.Name, false)
		if !hasProject(d, p.Name) {
			t.Error("a later create that failed took back a clone an agent had been made in")
		}
	})

	t.Run("finder holds it", func(t *testing.T) {
		d := peerCloneDaemon(t)
		cloner, err := d.srv.claimPeerProject(ctx, helloStackRepo)
		if err != nil {
			t.Fatal(err)
		}
		finder, err := d.srv.claimPeerProject(ctx, "git@github.com:acme/hello-stack.git")
		if err != nil || finder.Name != cloner.Name {
			t.Fatalf("the second claim = %q, %v; want the clone found", finder.Name, err)
		}
		d.srv.releasePeerProject(ctx, cloner.Name, false)
		if !hasProject(d, cloner.Name) {
			t.Fatal("the clone went while a create that found it was still under way")
		}
		d.srv.releasePeerProject(ctx, finder.Name, false)
		if hasProject(d, cloner.Name) {
			t.Error("the clone stayed after both creates failed")
		}
	})

	t.Run("agent keeps it", func(t *testing.T) {
		d := peerCloneDaemon(t)
		p, err := d.srv.claimPeerProject(ctx, helloStackRepo)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.srv.store.AddAgent(ctx, state.Agent{Project: p.Name, Name: "agent-09", Instance: "ab-hello-stack-agent-09", AI: "claude", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		d.srv.releasePeerProject(ctx, p.Name, false)
		if !hasProject(d, p.Name) {
			t.Error("the clone went with an agent in it")
		}
	})

	t.Run("users own stays", func(t *testing.T) {
		d := peerCloneDaemon(t)
		repo := d.fixtureRepo(t, "hello-stack")
		gitIn(t, repo, "remote", "add", "origin", helloStackRepo)
		if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
			t.Fatal(err)
		}
		p, err := d.srv.claimPeerProject(ctx, helloStackRepo)
		if err != nil {
			t.Fatal(err)
		}
		d.srv.releasePeerProject(ctx, p.Name, false)
		if !hasProject(d, p.Name) {
			t.Error("a project the user added was taken back")
		}
	})
}

// A create whose job fails after the clone it was for leaves no project
// behind, whether the lead reached the computer through the hub or as a paired
// AgentBox's, and the next create clones again.
func TestPeerCreateFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	failed := func(t *testing.T, d testDaemon, job api.Job) {
		t.Helper()
		if err := d.client.FollowJobLog(ctx, job.ID, io.Discard); err != nil {
			t.Fatal(err)
		}
		if j, err := d.client.Job(ctx, job.ID); err != nil || j.Status != api.JobFailed || !strings.Contains(j.Error, "simulated copy failure") {
			t.Fatalf("the job = %+v, %v; want it failed at the copy", j, err)
		}
	}

	t.Run("hub", func(t *testing.T) {
		m := startPeerMachinesOn(t, readyFailingCopyIncus)
		for range 2 {
			job, err := m.lead.OnMachine("vps").CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders", Task: "add it", Branch: "reminders"})
			if err != nil {
				t.Fatal(err)
			}
			failed(t, m.vps, job)
			waitNoClone(t, m.vps)
		}
		if *m.clones != 2 {
			t.Errorf("vps cloned %d times, want once for each create", *m.clones)
		}
	})

	t.Run("paired AgentBox", func(t *testing.T) {
		m := startShareMachinesOn(t, readyFailingCopyIncus)
		for range 2 {
			job, err := m.lead.OnMachine("Bob").CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders", Task: "add it", Branch: "reminders"})
			if err != nil {
				t.Fatal(err)
			}
			failed(t, m.bob, job)
			waitNoClone(t, m.bob)
		}
	})
}

// The guest's routes are the owner's lead's agents, questions and a group, and
// nothing else of the project.
func TestShareGuestRoutes(t *testing.T) {
	t.Parallel()
	allowed := []string{"POST /v1/project/agents", "POST /v1/project/groups", "GET /v1/project/agents", "POST /v1/project/retire"}
	refused := []string{"GET /v1/project/groups", "POST /v1/project/groups/grp_1/recommend", "GET /v1/project/groups/grp_1",
		"GET /v1/project/notes", "GET /v1/project/secrets", "POST /v1/project/copy"}
	for _, r := range slices.Concat(allowed, refused) {
		method, route, _ := strings.Cut(r, " ")
		req, err := http.NewRequest(method, "http://agentbox"+route, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := shareGuestRoute(req) != "", slices.Contains(allowed, r); got != want {
			t.Errorf("shareGuestRoute(%s) allowed = %v, want %v", r, got, want)
		}
	}
}

// With nobody choosing another computer, a lead's fan_out makes its group on
// the project's own machine, as it always has.
func TestLeadFanOutStaysHereByDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), recordingIncus, testConfig{instances: runningAgents01And02})
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	out, err := lead.ProjectFanOut(ctx, api.FanOutRequest{Title: "Reminders page", Task: "Add it", Variants: []api.FanOutVariant{{AI: "none"}, {AI: "none"}}})
	if err != nil {
		t.Fatal(err)
	}
	if groups, err := d.client.Groups(ctx, "hello-stack"); err != nil || len(groups) != 1 || groups[0].ID != out.Group.ID || !groups[0].ByLead {
		t.Errorf("groups = %+v, %v; want the lead's group here", groups, err)
	}
}

// A guest's "On my PC" is followed by a fan_out as by a create_agent: the
// group is made on the guest's computer, its agents the owner's lead's, on the
// guest's accounts; the owner's lead hears when they have all finished; a
// project cloned for a group none of whose agents is made goes.
func TestShareFanOutFollowsOnMyPC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := startShareMachinesOn(t, recordingIncus, testConfig{env: map[string]string{"INCUS_FILES": t.TempDir()}, instances: runningAgents01And02})

	aliceLead := leadReadyToChat(t, m.alice, "hello-stack")

	// Bob's composer sent "On my PC", as in TestShareGuestRunsAgents.
	peer, _ := m.alice.srv.store.SharePeer(ctx, m.peer.ID)
	pctx := context.WithValue(ctx, sharePeerKey{}, peer)
	lead := state.Agent{Project: "hello-stack", Name: state.LeadSessionName(state.MigratedSession), Role: state.RoleLead}
	m.alice.srv.heardInChat(pctx, lead)
	if err := m.alice.srv.choseRunOn(pctx, lead, api.RunOnPeer); err != nil {
		t.Fatal(err)
	}

	// A group the lead is to judge can't be: compare_group can't reach it.
	// Nothing is made, not even the clone.
	if _, err := m.lead.ProjectFanOut(ctx, api.FanOutRequest{Title: "Reminders page", Task: "Add it", Judge: true,
		Variants: []api.FanOutVariant{{AI: "claude"}, {AI: "claude"}}}); err == nil || !strings.Contains(err.Error(), "judge") {
		t.Fatalf("a fan_out to judge on Bob's computer: %v", err)
	}
	if cloned := clonedProjects(t, m.bob); len(cloned) != 0 {
		t.Fatalf("a refused group left %v on Bob's computer", cloned)
	}

	out, err := m.lead.ProjectFanOut(ctx, api.FanOutRequest{Title: "Reminders page", Task: "Add the reminders page",
		Variants: []api.FanOutVariant{{AI: "claude", ClaudeAccount: "alice-work"}, {AI: "claude"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Jobs) != 2 || len(out.Group.Members) != 2 || out.Group.ID == "" {
		t.Fatalf("fan_out = %+v; want a group of two with its jobs", out)
	}
	if !strings.Contains(strings.Join(out.Notices, " "), "Bob's computer") {
		t.Errorf("notices = %q; want the lead told the group is on Bob's computer", out.Notices)
	}
	if groups, _ := m.alice.client.Groups(ctx, ""); len(groups) != 0 {
		t.Errorf("Alice has groups %+v; the group should be Bob's", groups)
	}
	for _, j := range out.Jobs {
		if err := m.bob.client.FollowJobLog(ctx, j.ID, io.Discard); err != nil {
			t.Fatal(err)
		}
		if got, err := m.bob.client.Job(ctx, j.ID); err != nil || got.Status != api.JobSucceeded {
			t.Fatalf("a job on Bob's computer = %+v, %v", got, err)
		}
	}

	// Bob has the group, in the project cloned there, its agents Alice's
	// lead's and on none of her accounts.
	groups, err := m.bob.client.Groups(ctx, "hello-stack")
	if err != nil || len(groups) != 1 || groups[0].ID != out.Group.ID {
		t.Fatalf("Bob's groups = %+v, %v; want the lead's group", groups, err)
	}
	g, err := m.bob.client.Group(ctx, out.Group.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, member := range g.Members {
		origin, err := m.bob.srv.store.PeerAgent(ctx, "hello-stack", member.Agent)
		if err != nil || origin.OriginID != shareOriginPrefix+m.remote.ID || origin.OriginName != "Alice" || origin.OriginProject != "hello-stack" {
			t.Errorf("Bob's record of who made %s (%s) = %+v, %v", member.Agent, member.Label, origin, err)
		}
		if a, err := m.bob.srv.store.Agent(ctx, "hello-stack", member.Agent); err != nil || a.ClaudeAccount == "alice-work" {
			t.Errorf("member %d on Bob = %+v, %v; want none of Alice's accounts", i, a, err)
		}
	}
	waitFor(t, "Alice to hear both agents started on Bob's computer", func() bool {
		fleet, err := m.alice.client.Fleet(ctx, "hello-stack")
		return err == nil && len(fleet.Remote) == 2 && fleet.Remote[0].Machine == "Bob" && fleet.Remote[0].Shared
	})

	// They finish: Alice's lead hears of the group, once.
	for _, member := range g.Members {
		st, err := m.bob.srv.store.Agent(ctx, "hello-stack", member.Agent)
		if err != nil {
			t.Fatal(err)
		}
		if id := m.bob.srv.groupMemberFinished(ctx, st); id != g.ID {
			t.Fatalf("%s's first finish reports group %q, want %s", member.Agent, id, g.ID)
		}
		m.bob.srv.groupMoved(ctx, g.ID)
	}
	waitFor(t, "Alice's lead to hear the group finished", func() bool {
		th, err := m.alice.srv.chat.Thread(aliceLead)
		if err != nil {
			return false
		}
		return slices.ContainsFunc(th.Items, func(it api.ChatItem) bool {
			// It names the group's first agent, whichever of the two jobs
			// that run at once was numbered agent-01.
			return it.Kind == "notice" && strings.Contains(it.Text, "have finished") && strings.Contains(it.Text, "compare_group can't reach them") &&
				strings.Contains(it.Text, g.Members[0].Agent+"@Bob")
		})
	})
	// Bob's own chat of the project is left alone: a notice there would open
	// a lead on his computer for a group Alice's lead is the one waiting on.
	if agents, _ := m.bob.srv.store.Agents(ctx, "hello-stack"); slices.ContainsFunc(agents, func(a state.Agent) bool { return a.IsLead() }) {
		t.Errorf("Bob's project has a lead chat after the group finished: %+v", agents)
	}
}

// A group none of whose agents is made on the guest's computer leaves no clone.
func TestShareFanOutThatFailsLeavesNoClone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := startShareMachinesOn(t, readyFailingCopyIncus)
	peer, _ := m.alice.srv.store.SharePeer(ctx, m.peer.ID)
	pctx := context.WithValue(ctx, sharePeerKey{}, peer)
	lead := state.Agent{Project: "hello-stack", Name: state.LeadSessionName(state.MigratedSession), Role: state.RoleLead}
	m.alice.srv.heardInChat(pctx, lead)
	if err := m.alice.srv.choseRunOn(pctx, lead, api.RunOnPeer); err != nil {
		t.Fatal(err)
	}
	out, err := m.lead.ProjectFanOut(ctx, api.FanOutRequest{Title: "Reminders page", Task: "Add it", Variants: []api.FanOutVariant{{AI: "claude"}, {AI: "claude"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range out.Jobs {
		if err := m.bob.client.FollowJobLog(ctx, j.ID, io.Discard); err != nil {
			t.Fatal(err)
		}
		if got, err := m.bob.client.Job(ctx, j.ID); err != nil || got.Status != api.JobFailed {
			t.Fatalf("a job on Bob's computer = %+v, %v; want it failed", got, err)
		}
	}
	waitNoClone(t, m.bob)
}
