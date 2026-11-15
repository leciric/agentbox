package state_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/state"
)

func TestMergeWhenGreen(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	if err := st.AddProject(ctx, state.Project{Name: "acme", Root: "/src/acme", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	mark := state.MergeWhenGreen{Project: "acme", Number: 7, Agent: "agent-1", Method: "squash", HeadSHA: "abc", Fingerprint: `{"f":"1"}`, At: at}
	if err := st.SetMergeWhenGreen(ctx, mark); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.MergeWhenGreenOf(ctx, "acme", 7)
	if err != nil || !ok || !got.Pending() || got.Method != "squash" || got.HeadSHA != "abc" || !got.At.Equal(at) {
		t.Fatalf("MergeWhenGreenOf = %+v, %v, %v", got, ok, err)
	}
	if _, ok, _ := st.MergeWhenGreenOf(ctx, "acme", 8); ok {
		t.Fatal("a pull request never marked has a mark")
	}

	// Given up once: a second failure doesn't overwrite why.
	if failed, err := st.FailMergeWhenGreen(ctx, "acme", 7, "checks failed", at.Add(time.Minute)); err != nil || !failed {
		t.Fatalf("FailMergeWhenGreen = %v, %v", failed, err)
	}
	if failed, _ := st.FailMergeWhenGreen(ctx, "acme", 7, "later", at); failed {
		t.Fatal("a mark given up was given up again")
	}
	all, err := st.MergesWhenGreen(ctx, "acme")
	if err != nil || len(all) != 1 || all[0].Pending() || all[0].Error != "checks failed" || all[0].FailedAt.IsZero() {
		t.Fatalf("MergesWhenGreen = %+v, %v", all, err)
	}

	// Marking it again starts over.
	if err := st.SetMergeWhenGreen(ctx, mark); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := st.MergeWhenGreenOf(ctx, "acme", 7); !got.Pending() || !got.FailedAt.IsZero() {
		t.Fatalf("marked again: %+v", got)
	}

	if err := st.ForgetMergeWhenGreen(ctx, "acme", 7); err != nil {
		t.Fatal(err)
	}
	if all, _ := st.MergesWhenGreen(ctx, "acme"); len(all) != 0 {
		t.Fatalf("forgotten, still %+v", all)
	}
}

func TestTakeBackReview(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	if err := st.SetAgentReview(ctx, state.AgentReview{Project: "acme", Agent: "agent-1", Verdict: "reviewed", Fingerprint: "new", At: at}); err != nil {
		t.Fatal(err)
	}
	// Another verdict since: left alone.
	if err := st.TakeBackReview(ctx, "acme", "agent-1", "old"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.AgentReviewOf(ctx, "acme", "agent-1"); !ok {
		t.Fatal("a later verdict was taken back")
	}
	if err := st.TakeBackReview(ctx, "acme", "agent-1", "new"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.AgentReviewOf(ctx, "acme", "agent-1"); ok {
		t.Fatal("the verdict wasn't taken back")
	}
}
