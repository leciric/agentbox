package memory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"agentbox/internal/memory"
)

// fill gives a project one of everything a context can be built from, so a
// build that keeps all of it has something from every section to show.
func fill(t *testing.T, s *memory.Store, project string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.SetWorkingMemory(ctx, project, memory.WorkingMemoryPatch{
		Goal:         ptr("Ship the reminders page"),
		CurrentTask:  ptr("Pagination"),
		ActiveAgents: &[]string{"agent-04"},
	}); err != nil {
		t.Fatal(err)
	}
	event, err := s.AppendEvent(ctx, memory.Event{
		Project: project, Type: memory.EventConversationCompacted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent(ctx, memory.Event{
		Project: project, Agent: "agent-04", Type: "pr_merged",
		Payload: json.RawMessage(`{"branch":"agentbox/agent-04","pagination":"merged"}`),
	}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []memory.Memory{
		{Kind: memory.KindEpisodic, Title: "The chat up to today",
			Content: "The user asked for pagination; agent-04 is on it.", SourceEventID: event.ID},
		{Kind: memory.KindIssue, Title: "The count query is unindexed", Content: "It scans the whole table."},
		{Kind: memory.KindProject, Title: "Reminders live in internal/reminders", Importance: 5,
			Content: "The page reads them through the API."},
		{Kind: memory.KindDecision, Title: "Pagination is cursor-based", Importance: 5,
			Content: "Offsets drift while reminders are being written."},
	} {
		m.Project = project
		if _, err := s.AddMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddReport(ctx, memory.Report{
		Project: project, Agent: "agent-04", Task: "Paginate the reminders page",
		Status: memory.StatusPartial, Summary: "The list paginates; the count query is still unindexed.",
		RemainingIssues: []string{"The count query scans the table"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddArtifact(ctx, memory.Artifact{
		Project: project, Agent: "agent-04", Type: "branch", Path: "agentbox/agent-04",
	}); err != nil {
		t.Fatal(err)
	}
	// The plan: one task being worked on, waiting on one that isn't (D77).
	index, err := s.AddTask(ctx, memory.Task{Project: project, Goal: "Index the count query"})
	if err != nil {
		t.Fatal(err)
	}
	paginate, err := s.AddTask(ctx, memory.Task{Project: project, Agent: "agent-04",
		Status: memory.TaskActive, Goal: "Paginate the reminders page"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LinkTasks(ctx, project, paginate.ID, index.ID); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

// A project that remembers nothing builds an empty context rather than an
// error or a page of empty headings: that is every project before its first
// memory, and its consumers render no section at all for it.
func TestBuildContextOnAnEmptyProject(t *testing.T) {
	s := open(t)
	built, err := s.BuildContext(context.Background(), memory.ContextRequest{Project: "empty-project"})
	if err != nil {
		t.Fatal(err)
	}
	if !built.Empty() || built.Text != "" {
		t.Errorf("an empty project built %q", built.Text)
	}
	if built.Stats.Rows != 0 || built.Stats.Tokens != 0 {
		t.Errorf("an empty project cost %+v", built.Stats)
	}
}

// A generous budget keeps everything, in the order the architecture asks for.
func TestBuildContextKeepsEverythingItCan(t *testing.T) {
	s := open(t)
	fill(t, s, "everything")
	built, err := s.BuildContext(context.Background(), memory.ContextRequest{
		Project: "everything", Query: "pagination", Budget: memory.MaxBudgetTokens, For: memory.ForLead,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Everything but what was produced, which is an agent's to find and
	// noise in the lead's recap (D90).
	want := []string{
		memory.SectionWorking, memory.SectionTasks, memory.SectionStory, memory.SectionOpen,
		memory.SectionKnowledge, memory.SectionEvents, memory.SectionReports,
	}
	if got := kinds(built); !equal(got, want) {
		t.Errorf("sections = %v, want %v", got, want)
	}
	forAgent, err := s.BuildContext(context.Background(), memory.ContextRequest{
		Project: "everything", Query: "pagination", Budget: memory.MaxBudgetTokens, For: memory.ForAgent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(forAgent); !equal(got, append(want, memory.SectionArtifacts)) {
		t.Errorf("an agent's sections = %v, want %v", got, append(want, memory.SectionArtifacts))
	}
	if !strings.Contains(forAgent.Text, "**branch** agentbox/agent-04") { // what it produced
		t.Errorf("an agent's context doesn't say what was produced:\n%s", forAgent.Text)
	}
	for _, phrase := range []string{
		"Ship the reminders page",       // working memory
		"agent-04",                      // ...and who is on it
		"The user asked for pagination", // the narrative
		"count query is unindexed",      // what is still open
		"Paginate the reminders page",   // the plan
		"waiting on Index the count",    // ...and what it is blocked on
		"Pagination is cursor-based",    // what the project decided
		"pr_merged",                     // what happened
		"The list paginates",            // what an agent reported
		"Still wrong: The count query",  // ...and what it left undone
		"The conversation so far",       // the lead's own words for the narrative
	} {
		if !strings.Contains(built.Text, phrase) {
			t.Errorf("the context doesn't mention %q:\n%s", phrase, built.Text)
		}
	}
	if built.Stats.Dropped != 0 || built.Stats.Truncated {
		t.Errorf("a context that fits dropped something: %+v", built.Stats)
	}
	if built.Stats.Rows != built.Stats.Considered {
		t.Errorf("kept %d of %d rows with room for all of them", built.Stats.Rows, built.Stats.Considered)
	}
}

// The budget is real: what a build renders stays inside it, and what it gives
// up it gives up in the order the architecture asks for — artifacts first,
// then reports, then past events, then the knowledge that matched the query,
// and never what the project is doing and where it stands.
func TestBuildContextBudgetAndDropOrder(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	fill(t, s, "tight")
	// Enough memories to make any section expensive, so the budget rather
	// than an empty store is what decides.
	for i := range 40 {
		if _, err := s.AddMemory(ctx, memory.Memory{
			Project: "tight", Kind: memory.KindDecision, Importance: 5,
			Title:   fmt.Sprintf("Pagination decision %d", i),
			Content: strings.Repeat("Cursors rather than offsets, because offsets drift. ", 8),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// A budget that starves everything below the sections a build never drops.
	tightest, err := s.BuildContext(ctx, memory.ContextRequest{
		Project: "tight", Query: "pagination", Budget: memory.MinBudgetTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(tightest); !equal(got, []string{
		memory.SectionWorking, memory.SectionTasks, memory.SectionStory, memory.SectionOpen}) {
		t.Errorf("the tightest budget kept %v, want only what is never dropped", got)
	}
	if !strings.Contains(tightest.Text, "Ship the reminders page") {
		t.Error("the tightest budget dropped what the project is doing")
	}

	// Every budget between the two, so the order is checked at each step
	// rather than at one convenient number: a section is never kept when
	// something above it in the order was given up.
	order := []string{
		memory.SectionWorking, memory.SectionTasks, memory.SectionStory, memory.SectionOpen,
		memory.SectionKnowledge, memory.SectionEvents, memory.SectionReports, memory.SectionArtifacts,
	}
	for budget := memory.MinBudgetTokens; budget <= 4000; budget += 100 {
		built, err := s.BuildContext(ctx, memory.ContextRequest{
			Project: "tight", Query: "pagination", Budget: budget,
		})
		if err != nil {
			t.Fatal(err)
		}
		if spent := memory.EstimateTokens(built.Text); spent > budget {
			t.Errorf("a %d-token budget rendered %d tokens", budget, spent)
		}
		if built.Stats.Tokens > budget {
			t.Errorf("a %d-token budget accounted for %d tokens", budget, built.Stats.Tokens)
		}
		got := kinds(built)
		// What survived is a prefix of the order, always: a build gives up
		// the tail, never something from the middle.
		if !equal(got, order[:len(got)]) {
			t.Fatalf("a %d-token budget kept %v, which isn't a prefix of %v", budget, got, order)
		}
		// And what it gave up, it says it gave up.
		if want := order[len(got):]; !equal(built.Stats.DroppedSections, want) {
			t.Errorf("a %d-token budget dropped %v, want %v", budget, built.Stats.DroppedSections, want)
		}
		if built.Stats.Ratio >= 1 {
			t.Errorf("a %d-token budget of a corpus of %d tokens compressed to a ratio of %v",
				budget, built.Stats.CorpusTokens, built.Stats.Ratio)
		}
		if built.Stats.Dropped != built.Stats.Considered-built.Stats.Rows {
			t.Errorf("a %d-token budget counted %d dropped rows of %d considered and %d kept",
				budget, built.Stats.Dropped, built.Stats.Considered, built.Stats.Rows)
		}
	}
}

// A context that has outgrown even the sections it never drops is cut on a
// word boundary and says so: one that stops mid-sentence with no explanation
// reads like the project's memory is broken rather than the slice of it.
func TestBuildContextTruncatesWhatItCannotDrop(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, err := s.SetWorkingMemory(ctx, "long", memory.WorkingMemoryPatch{
		Goal: ptr("Ship the reminders page"),
	}); err != nil {
		t.Fatal(err)
	}
	// A narrative long enough to overflow on its own. It is one of the
	// sections a build never drops, so the only way to fit it is to cut it.
	event, err := s.AppendEvent(ctx, memory.Event{Project: "long", Type: memory.EventConversationCompacted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMemory(ctx, memory.Memory{
		Project: "long", Kind: memory.KindEpisodic, Title: "The chat up to today",
		Content:       strings.TrimSpace(strings.Repeat("something worth remembering ", 500)),
		SourceEventID: event.ID,
	}); err != nil {
		t.Fatal(err)
	}
	built, err := s.BuildContext(ctx, memory.ContextRequest{Project: "long", Budget: memory.MinBudgetTokens})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(built.Text, "Ship the reminders page") {
		t.Error("the cut took what the project is doing with it")
	}
	if !built.Stats.Truncated {
		t.Error("a context that overflowed its budget doesn't say it was cut")
	}
	if spent := memory.EstimateTokens(built.Text); spent > memory.MinBudgetTokens {
		t.Errorf("a cut context is still %d tokens, over a %d budget", spent, memory.MinBudgetTokens)
	}
	if !strings.Contains(built.Text, "search it rather than asking for it again") {
		t.Errorf("the cut isn't explained:\n%s", built.Text[max(0, len(built.Text)-300):])
	}
	if !strings.HasSuffix(built.Text, ")") {
		t.Error("the cut context doesn't end with its note")
	}
}

// What a build cost, how much it retrieved and what it left out — including
// the compression it got against the whole of what the project remembers,
// which is the number that says whether any of this is worth doing.
func TestContextAccounting(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	fill(t, s, "accounted")
	built, err := s.BuildContext(ctx, memory.ContextRequest{
		Project: "accounted", Query: "pagination", Budget: memory.MinBudgetTokens, For: memory.ForAgent,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := built.Stats
	switch {
	case st.Project != "accounted" || st.For != memory.ForAgent:
		t.Errorf("a build doesn't say who it was for: %+v", st)
	case st.Query != "pagination":
		t.Errorf("a build doesn't say what it looked for: %q", st.Query)
	case st.CorpusTokens <= 0:
		t.Errorf("a project with memories has a corpus of %d tokens", st.CorpusTokens)
	case st.Ratio <= 0:
		t.Errorf("the compression ratio is %v, which can't be right", st.Ratio)
	case st.Tokens == 0:
		t.Error("a build that kept something cost nothing")
	}
	// The ratio is what was kept against what there was to keep. On a corpus
	// this small it is above 1 — the headings and labels a context is written
	// in cost more than the handful of rows they draw from — which is the
	// honest answer: compression is what a build does for a project that
	// remembers a lot, and this one remembers almost nothing.
	if want := float64(st.Tokens) / float64(st.CorpusTokens); st.Ratio != want {
		t.Errorf("ratio = %v, want tokens over corpus = %v", st.Ratio, want)
	}

	account := memory.ContextBuilds("accounted")
	if account.Builds != 1 || account.Tokens != st.Tokens {
		t.Errorf("the accounting is %+v after one build of %d tokens", account, st.Tokens)
	}
	if len(account.Recent) != 1 || account.Recent[0].Query != "pagination" {
		t.Errorf("the last build isn't in the accounting: %+v", account.Recent)
	}
	if _, err := s.BuildContext(ctx, memory.ContextRequest{Project: "accounted", Query: "indexes"}); err != nil {
		t.Fatal(err)
	}
	if account = memory.ContextBuilds("accounted"); account.Builds != 2 || account.Recent[0].Query != "indexes" {
		t.Errorf("the accounting doesn't have the newest build first: %+v", account)
	}
}

// An empty query falls back to what the project says it is working on, which
// is what the lead's recap has always searched for.
func TestBuildContextFallsBackToWorkingMemory(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	fill(t, s, "fallback")
	built, err := s.BuildContext(ctx, memory.ContextRequest{Project: "fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if built.Stats.Query != "Ship the reminders page Pagination" {
		t.Errorf("an empty query became %q", built.Stats.Query)
	}
	if !strings.Contains(built.Text, "Reminders live in internal/reminders") {
		t.Errorf("the fallback query found nothing:\n%s", built.Text)
	}
}

// A worker's share is smaller than its project's, and both stay inside the
// bounds whatever they are handed.
func TestBudgets(t *testing.T) {
	if got := memory.BudgetTokens(0); got != memory.DefaultBudgetTokens {
		t.Errorf("BudgetTokens(0) = %d, want the default", got)
	}
	if got := memory.BudgetTokens(1 << 20); got != memory.MaxBudgetTokens {
		t.Errorf("BudgetTokens(huge) = %d, want the maximum", got)
	}
	if got := memory.BudgetTokens(-5); got != memory.DefaultBudgetTokens {
		t.Errorf("BudgetTokens(-5) = %d, want the default", got)
	}
	if got := memory.AgentBudget(memory.DefaultBudgetTokens); got >= memory.DefaultBudgetTokens {
		t.Errorf("an agent's share is %d of %d, and should be smaller", got, memory.DefaultBudgetTokens)
	}
	if got := memory.AgentBudget(memory.MinBudgetTokens); got != memory.MinBudgetTokens {
		t.Errorf("an agent's share of the smallest budget is %d, want %d", got, memory.MinBudgetTokens)
	}
}

// The token estimate is four bytes to a token, and nothing cleverer. It is
// documented as an estimate; this is what it estimates.
func TestEstimateTokens(t *testing.T) {
	if got := memory.EstimateTokens(""); got != 0 {
		t.Errorf("EstimateTokens(\"\") = %d", got)
	}
	if got := memory.EstimateTokens("abcd"); got != 1 {
		t.Errorf("EstimateTokens(4 bytes) = %d, want 1", got)
	}
	// Rounded up, so a short string is never free.
	if got := memory.EstimateTokens("a"); got != 1 {
		t.Errorf("EstimateTokens(1 byte) = %d, want 1", got)
	}
	// Counted in bytes, so text outside the Latin scripts costs what it
	// really does rather than what its rune count suggests.
	if memory.EstimateTokens("日本語") <= memory.EstimateTokens("abc") {
		t.Error("multi-byte text is estimated as cheaply as ASCII")
	}
}

func kinds(c memory.Context) []string {
	out := make([]string, 0, len(c.Sections))
	for _, s := range c.Sections {
		out = append(out, s.Kind)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
