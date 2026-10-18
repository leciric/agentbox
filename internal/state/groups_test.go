package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentbox/internal/state"
)

func twoMembers() []state.GroupMember {
	return []state.GroupMember{{AI: "claude", Model: "opus"}, {AI: "claude", Model: "sonnet"}}
}

// A group asks for its agents before they exist, learns their names as they
// are made, and is decided once: on one of them, or on none.
func TestAGroupIsDecidedOnceOnOneOfItsAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "shop")

	if _, err := st.AddGroup(ctx, state.Group{Project: "shop", Title: "One", Members: twoMembers()[:1]}); err == nil {
		t.Error("a group of one agent was made")
	}
	g, err := st.AddGroup(ctx, state.Group{Project: "shop", Title: "Login", Task: "Fix it", Members: twoMembers()})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetGroupMember(ctx, g.ID, 1, "job-1", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGroupMember(ctx, g.ID, 1, "", "agent-01", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGroupMember(ctx, g.ID, 2, "", "agent-02", ""); err != nil {
		t.Fatal(err)
	}
	got, err := st.GroupByID(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m := got.Members[0]; m.Job != "job-1" || m.Agent != "agent-01" || m.Model != "opus" || m.Position != 1 {
		t.Errorf("member A = %+v, want job-1's agent-01 on opus", m)
	}
	if of, ok, err := st.GroupOf(ctx, "shop", "agent-02"); err != nil || !ok || of.ID != g.ID {
		t.Errorf("GroupOf(agent-02) = %v %v %v, want %s", of.ID, ok, err, g.ID)
	}

	// Only the first finish counts: it is what the agents are compared by.
	first, err := st.GroupMemberFinished(ctx, "shop", "agent-01", time.UnixMilli(1000))
	if err != nil || !first {
		t.Fatalf("first finish = %v %v, want true", first, err)
	}
	if again, _ := st.GroupMemberFinished(ctx, "shop", "agent-01", time.UnixMilli(2000)); again {
		t.Error("a second finish was recorded")
	}

	if err := st.KeepInGroup(ctx, g.ID, "agent-09"); err == nil {
		t.Error("an agent outside the group was kept")
	}
	if err := st.RecommendInGroup(ctx, g.ID, "agent-02", "It tested the redirect."); err != nil {
		t.Fatal(err)
	}
	if err := st.KeepInGroup(ctx, g.ID, "agent-01"); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseGroup(ctx, g.ID); err == nil {
		t.Error("a decided group was closed again")
	}
	got, _ = st.GroupByID(ctx, g.ID)
	if got.Kept != "agent-01" || !got.Settled() || got.Recommended != "agent-02" || got.Members[0].FinishedAt.UnixMilli() != 1000 {
		t.Errorf("decided group = %+v", got)
	}
	// A settled group takes no more finishes.
	if late, _ := st.GroupMemberFinished(ctx, "shop", "agent-02", time.Now()); late {
		t.Error("a finish was recorded in a decided group")
	}
}

// The list holds every open group and the recently decided, newest first, and
// a removed project takes its groups with it.
func TestGroupsAreListedAndGoWithTheirProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "shop")
	addProject(t, st, "blog")
	old, _ := st.AddGroup(ctx, state.Group{Project: "shop", Title: "Old", Members: twoMembers(), CreatedAt: time.Now().Add(-time.Hour)})
	recent, _ := st.AddGroup(ctx, state.Group{Project: "shop", Title: "New", Members: twoMembers()})
	if _, err := st.AddGroup(ctx, state.Group{Project: "blog", Title: "Elsewhere", Members: twoMembers()}); err != nil {
		t.Fatal(err)
	}
	groups, err := st.Groups(ctx, "shop", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].ID != recent.ID || groups[1].ID != old.ID || len(groups[0].Members) != 2 {
		t.Fatalf("shop's groups = %+v, want New then Old with two members each", groups)
	}
	if all, _ := st.Groups(ctx, "", 0); len(all) != 3 {
		t.Errorf("every group = %d, want 3", len(all))
	}

	if err := st.RemoveProject(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GroupByID(ctx, recent.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a removed project's group = %v, want not found", err)
	}
	if all, _ := st.Groups(ctx, "", 0); len(all) != 1 {
		t.Errorf("groups left = %d, want blog's one", len(all))
	}
}
