package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
)

const runningAgents01And02 = `[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}},` +
	`{"name":"ab-hello-stack-agent-02","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}}}}]`

// One task on two agents: each is made on a branch of its own with its
// letter, told it is one of two, compared, and once one is kept the other is
// retired with its branch kept.
func TestFanOutComparesTwoAgentsAndKeepsOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), recordingIncus, testConfig{instances: runningAgents01And02})
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}

	out, err := d.client.FanOut(ctx, api.FanOutRequest{
		Project: "hello-stack", Title: "Reminders page", Task: "Add the reminders page",
		Variants: []api.FanOutVariant{{AI: "none"}, {AI: "none"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Jobs) != 2 || len(out.Group.Members) != 2 || out.Group.Members[1].Label != "B" {
		t.Fatalf("fan-out = %+v, want two jobs and members A and B", out)
	}
	for _, j := range out.Jobs {
		waitFor(t, "the group's agents to be made", func() bool {
			got, err := d.client.Job(ctx, j.ID)
			return err == nil && got.Done()
		})
		if got, _ := d.client.Job(ctx, j.ID); got.Status != api.JobSucceeded {
			t.Fatalf("creating a group's agent = %s: %s", got.Status, got.Error)
		}
	}

	g, err := d.client.Group(ctx, out.Group.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, b := g.Members[0], g.Members[1]
	if a.Agent == "" || b.Agent == "" || a.Agent == b.Agent {
		t.Fatalf("members = %+v, want two agents", g.Members)
	}
	if !strings.HasSuffix(a.Branch, "reminders-page-a") || !strings.HasSuffix(b.Branch, "reminders-page-b") {
		t.Errorf("branches = %q, %q, want the slug with each one's letter", a.Branch, b.Branch)
	}
	if a.Title != "Reminders page · A" || a.Changes == nil || a.StartedAt == nil {
		t.Errorf("member A compared = %+v", a)
	}
	if g.Finished != 0 {
		t.Errorf("finished = %d before either finished", g.Finished)
	}

	// Each finish is recorded; the lead hears once, at the last.
	for _, m := range g.Members {
		st, err := d.srv.store.Agent(ctx, "hello-stack", m.Agent)
		if err != nil {
			t.Fatal(err)
		}
		if id := d.srv.groupMemberFinished(ctx, st); id != g.ID {
			t.Fatalf("%s's first finish reports group %q, want %s", m.Agent, id, g.ID)
		}
		if again := d.srv.groupMemberFinished(ctx, st); again != "" {
			t.Errorf("%s's second finish reports group %q, want none", m.Agent, again)
		}
		d.srv.groupMoved(ctx, g.ID)
	}
	if g, _ = d.client.Group(ctx, g.ID); g.Finished != 2 || g.Members[0].FinishedAt == nil {
		t.Errorf("after both finished: %+v", g)
	}
	d.srv.mu.Lock()
	told := d.srv.groupsDone[g.ID]
	d.srv.mu.Unlock()
	if !told {
		t.Error("the lead wasn't told the group finished")
	}

	if _, err := d.client.KeepInGroup(ctx, g.ID, "agent-99"); err == nil {
		t.Error("an agent outside the group was kept")
	}
	kept, err := d.client.KeepInGroup(ctx, g.ID, a.Agent)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Kept != a.Agent || kept.SettledAt == nil {
		t.Errorf("kept = %+v", kept)
	}
	d.srv.mu.Lock()
	retired, keptRetired := d.srv.retired[b.Ref], d.srv.retired[a.Ref]
	d.srv.mu.Unlock()
	if !retired || keptRetired {
		t.Errorf("retired: B %v, A %v; want only B", retired, keptRetired)
	}
	if _, err := d.client.KeepInGroup(ctx, g.ID, b.Agent); err == nil {
		t.Error("a decided group was decided again")
	}
	listed, err := d.client.Groups(ctx, "hello-stack")
	if err != nil || len(listed) != 1 || listed[0].Kept != a.Agent {
		t.Errorf("groups = %+v, %v", listed, err)
	}
}

// A group is checked whole before any of its agents is made: one agent it
// could never make refuses the group, with nothing started.
func TestFanOutRefusesTheWholeGroupForOneBadAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	model := "opus"
	_, err := d.client.FanOut(ctx, api.FanOutRequest{
		Project: "hello-stack", Title: "Reminders page", Task: "Add it",
		Variants: []api.FanOutVariant{{AI: "none"}, {AI: "none", Model: &model}},
	})
	if err == nil || !strings.Contains(err.Error(), "agent B") {
		t.Fatalf("fan-out with a model for no AI tool = %v, want agent B refused", err)
	}
	if _, err := d.client.FanOut(ctx, api.FanOutRequest{Project: "hello-stack", Title: "x", Task: "y", Variants: []api.FanOutVariant{{AI: "none"}}}); err == nil {
		t.Error("a group of one was made")
	}
	if groups, _ := d.client.Groups(ctx, ""); len(groups) != 0 {
		t.Errorf("groups = %d, want none", len(groups))
	}
}

func TestGroupBranchFitsTheSlugLimit(t *testing.T) {
	long := strings.Repeat("abcdefgh-", 6)[:48]
	got := groupBranch(long, 3)
	if len(got) > 48 || !strings.HasSuffix(got, "-c") || strings.Contains(got, "--") {
		t.Errorf("groupBranch = %q", got)
	}
}
