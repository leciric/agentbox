package state_test

import (
	"context"
	"testing"

	"agentbox/internal/state"
)

func TestAnIssueRemembersItsAgentAndPullRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "shop")

	if err := st.RecordIssueAgent(ctx, state.IssueAgent{Project: "shop", Repo: "acme/shop", Number: 7, Title: "Crash", Job: "job-1", Auto: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordIssueAgent(ctx, state.IssueAgent{Project: "shop", Repo: "acme/shop", Number: 8, Job: "job-2"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIssueAgentName(ctx, "job-1", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIssueAgentPR(ctx, "shop", "acme/shop", 7, 12, "https://github.com/acme/shop/pull/12", "open"); err != nil {
		t.Fatal(err)
	}
	// job-2 failed: the issue may be handed again.
	if err := st.ForgetIssueAgent(ctx, "job-2"); err != nil {
		t.Fatal(err)
	}
	got, err := st.IssueAgents(ctx, "shop", "acme/shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d issues, want only #7", len(got))
	}
	ia := got[7]
	if ia.Agent != "agent-01" || !ia.Auto || ia.PRNumber != 12 || ia.PRState != "open" || ia.Title != "Crash" {
		t.Errorf("#7 = %+v", ia)
	}
	if other, _ := st.IssueAgents(ctx, "shop", "acme/other"); len(other) != 0 {
		t.Errorf("another repository's #7 came back: %+v", other)
	}

	if err := st.SetProjectPickUpIssues(ctx, "shop", true); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.Project(ctx, "shop"); !p.PickUpIssues {
		t.Error("PickUpIssues didn't stick")
	}
}
