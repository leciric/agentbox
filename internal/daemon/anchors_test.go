package daemon

import (
	"context"
	"slices"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// An issue that waits on pull requests is closed once every one of them is
// merged or closed, and says which and how.
func TestOpenItemsCloseWhenTheirPullRequestsDo(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	pullsProject(t, d)
	ctx := context.Background()
	store := d.srv.memory()
	waiting, err := store.AddMemory(ctx, memory.Memory{Project: "hello-stack", Kind: memory.KindIssue,
		Title: "PRs awaiting the user's merge", Content: "#9 then #10 (stacked)"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.srv.store.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}

	gh.set(9, "aaaa", "MERGED", "UNKNOWN", "", "")
	gh.set(10, "bbbb", "OPEN", "MERGEABLE", "", "")
	if n := d.srv.closeAnchored(ctx, p); n != 0 {
		t.Fatalf("closed %d with #10 still open", n)
	}
	gh.set(10, "bbbb", "CLOSED", "UNKNOWN", "", "")
	if n := d.srv.closeAnchored(ctx, p); n != 1 {
		t.Fatalf("closed %d, want the one", n)
	}
	got, err := store.Memory(ctx, "hello-stack", waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResolvedBy != "auto: #9 merged, #10 closed" {
		t.Errorf("resolved_by = %q", got.ResolvedBy)
	}
}

// Answering a question closes the open item that was waiting on it, without
// anybody running a pass.
func TestAnsweringAQuestionClosesWhatWaitedOnIt(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	pullsProject(t, d, "agent-01")
	ctx := context.Background()
	q := state.Question{ID: "1a2b3c4d", Project: "hello-stack", Agent: "agent-01", Text: "Paginate the reminders page?",
		Status: state.QuestionPending, CreatedAt: time.Now()}
	if err := d.srv.store.AddQuestion(ctx, q); err != nil {
		t.Fatal(err)
	}
	waiting, err := d.srv.memory().AddMemory(ctx, memory.Memory{Project: "hello-stack", Kind: memory.KindIssue,
		Title: "agent-01 is waiting on the user", Content: "Should the reminders page paginate? (id 1a2b3c4d)"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.answerQuestion(ctx, q.ID, "Yes, twenty a page.", "user"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := d.srv.memory().Memory(ctx, "hello-stack", waiting.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Resolved() {
			if got.ResolvedBy != "auto: question 1a2b3c4d answered" {
				t.Errorf("resolved_by = %q", got.ResolvedBy)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the answered question didn't close the item waiting on it")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// What an agent's event names keeps an open item in the lead's recap; the
// lead's own turns don't, or a recap would keep its own stale issues alive.
func TestEventsMentionTheItemsTheyName(t *testing.T) {
	t.Parallel()
	got := mentionedIn([]byte(`{"pr":{"number":240,"url":"u"},"questionId":"1a2b3c4d","summary":"rebased agentbox/feat-x"}`))
	var names []string
	for _, a := range got {
		names = append(names, a.String())
	}
	slices.Sort(names)
	if want := []string{"#240", "agentbox/feat-x", "question 1a2b3c4d"}; !slices.Equal(names, want) {
		t.Errorf("mentionedIn = %v, want %v", names, want)
	}

	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	pullsProject(t, d)
	ctx := context.Background()
	old := time.Now().Add(-40 * 24 * time.Hour)
	m, err := d.srv.memory().AddMemory(ctx, memory.Memory{Project: "hello-stack", Kind: memory.KindIssue,
		Title: "PR #240 conflicts with main", CreatedAt: old})
	if err != nil {
		t.Fatal(err)
	}
	d.srv.captureEvent(ctx, "hello-stack", "", "lead_turn", map[string]any{"assistantReply": "#240 still conflicts"}, "")
	if got, _ := d.srv.memory().Memory(ctx, "hello-stack", m.ID); !got.MentionedAt.IsZero() {
		t.Errorf("the lead's own turn counted as a mention: %v", got.MentionedAt)
	}
	d.srv.captureEvent(ctx, "hello-stack", "agent-01", "pr_broken", map[string]any{"number": 240, "problems": []string{"conflict"}}, "")
	if got, _ := d.srv.memory().Memory(ctx, "hello-stack", m.ID); time.Since(got.LastMentioned()) > time.Minute {
		t.Errorf("an agent's event naming #240 didn't count: %v", got.MentionedAt)
	}
}

// The tidy route is a dry run unless asked.
func TestTidyRoute(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	pullsProject(t, d)
	ctx := context.Background()
	old, err := d.srv.memory().AddMemory(ctx, memory.Memory{Project: "hello-stack", Kind: memory.KindIssue,
		Title: "Stale --model help text", CreatedAt: time.Now().Add(-10 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := d.client.ProjectMemory("hello-stack").Tidy(ctx, api.TidyMemoryRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Applied || len(plan.Resolved) != 1 || plan.Resolved[0].ID != old.ID {
		t.Fatalf("dry run = %+v", plan)
	}
	if got, _ := d.srv.memory().Memory(ctx, "hello-stack", old.ID); got.Resolved() {
		t.Fatal("a dry run resolved it")
	}
	if plan, err = d.client.ProjectMemory("hello-stack").Tidy(ctx, api.TidyMemoryRequest{OlderThanHours: 24 * 30, Apply: true}); err != nil {
		t.Fatal(err)
	}
	if len(plan.Resolved) != 0 {
		t.Errorf("a 30-day cutoff resolved %+v", plan.Resolved)
	}
	if plan, err = d.client.ProjectMemory("hello-stack").Tidy(ctx, api.TidyMemoryRequest{Apply: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.srv.memory().Memory(ctx, "hello-stack", old.ID); got.ResolvedBy != memory.ResolvedByTidy {
		t.Errorf("resolved_by = %q", got.ResolvedBy)
	}
}

// A distillation may name what would close an issue; what names nothing is
// dropped rather than stored.
func TestDistilledIssuesKeepTheAnchorsTheModelNamed(t *testing.T) {
	t.Parallel()
	d, err := parseDistillation(`{"memories":[{"kind":"issue","title":"Reload tools waits on review",
		"anchors":["#235","branch agentbox/reload-tools","question 1a2b3c4d","soon"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range d.Memories[0].anchors() {
		got = append(got, a.Kind+":"+a.Value)
	}
	want := []string{"pr:235", "branch:agentbox/reload-tools", "question:1a2b3c4d"}
	if !slices.Equal(got, want) {
		t.Errorf("anchors = %v, want %v", got, want)
	}
}
