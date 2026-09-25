package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// When a chat is compacted, and — the case worth the test — when it is not.
// A context nobody has reported is unknown, not empty: read either way round
// it would compact at the wrong moment, and reading it as empty would compact
// every fresh session the instant it started.
func TestNeedsRollover(t *testing.T) {
	for _, tc := range []struct {
		what      string
		used      int64
		size      int64
		threshold int
		want      bool
	}{
		{"nothing said yet", 0, 0, 80, false},
		{"a size but no usage", 0, 200000, 80, false},
		{"usage but no size", 160000, 0, 80, false},
		{"room to spare", 100000, 200000, 80, false},
		{"just under", 159999, 200000, 80, false},
		{"exactly at the threshold", 160000, 200000, 80, true},
		{"over it", 190000, 200000, 80, true},
		{"switched off", 199000, 200000, state.RolloverOff, false},
		{"switched off, with nothing said", 0, 0, state.RolloverOff, false},
		{"a low threshold", 30000, 200000, 10, true},
	} {
		if got := needsRollover(tc.used, tc.size, tc.threshold); got != tc.want {
			t.Errorf("%s: needsRollover(%d, %d, %d) = %v, want %v", tc.what, tc.used, tc.size, tc.threshold, got, tc.want)
		}
	}
}

// The threshold is a project setting like any other: it has a default, it
// round-trips through the API, it can be switched off, and what makes no sense
// is refused.
func TestRolloverThresholdRoundTrips(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	p, err := d.client.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if p.RolloverThreshold != state.DefaultRolloverThreshold {
		t.Errorf("a new project compacts at %d%%, want %d%%", p.RolloverThreshold, state.DefaultRolloverThreshold)
	}
	if p, err = d.client.SetRolloverThreshold(ctx, "hello-stack", 60); err != nil || p.RolloverThreshold != 60 {
		t.Fatalf("SetRolloverThreshold(60) = %+v, %v", p, err)
	}
	if p, err = d.client.SetRolloverThreshold(ctx, "hello-stack", state.RolloverOff); err != nil || p.RolloverThreshold != 0 {
		t.Fatalf("SetRolloverThreshold(off) = %+v, %v", p, err)
	}
	if again, err := d.client.Project(ctx, "hello-stack"); err != nil || again.RolloverThreshold != 0 {
		t.Errorf("Project() = %+v, %v; want it still off", again, err)
	}
	if _, err := d.client.SetRolloverThreshold(ctx, "hello-stack", 5); err == nil {
		t.Error("a threshold below the minimum was accepted")
	}
	if _, err := d.client.SetRolloverThreshold(ctx, "hello-stack", 140); err == nil {
		t.Error("a threshold over 100% was accepted")
	}
}

// What the session says about itself becomes the project's memory: one event,
// the narrative the next session is started with, and a memory of each kind.
func TestStoringAConsolidationWritesMemories(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	lead := state.Agent{Project: "hello-stack", Name: state.LeadName, Role: state.RoleLead}
	answer := "Here you go:\n```json\n" + `{
		"summary": "The user wanted the reminders page to paginate. agent-04 is building it.",
		"decisions": [{"title": "Paginate at 50", "detail": "Loading everything was slow on the demo data."}],
		"discoveries": ["The seed script writes 10,000 rows. It is why the page was slow."],
		"facts": [{"title": "The API listens on 7777"}],
		"issues": [{"title": "The count query is unindexed", "detail": "It scans the whole table."}],
		"working": {"goal": "Ship the reminders page", "currentTask": "Pagination", "activeAgents": ["agent-04"]}
	}` + "\n```"
	parsed, err := parseConsolidation(answer)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.srv.storeConsolidation(ctx, lead, parsed, answer); err != nil {
		t.Fatal(err)
	}

	store := d.srv.memory()
	events, err := store.Events(ctx, "hello-stack", memory.EventFilter{})
	if err != nil || len(events) != 1 || events[0].Type != memory.EventConversationCompacted {
		t.Fatalf("events = %+v, %v; want one %s", events, err, memory.EventConversationCompacted)
	}
	memories, err := store.Memories(ctx, "hello-stack", nil)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]memory.Memory{}
	for _, m := range memories {
		byKind[m.Kind] = m
		if m.SourceEventID != events[0].ID {
			t.Errorf("the %s memory %q came from %q, want the compaction's event", m.Kind, m.Title, m.SourceEventID)
		}
	}
	if len(memories) != 5 {
		t.Errorf("%d memories written, want 5: %+v", len(memories), memories)
	}
	for _, kind := range []string{memory.KindEpisodic, memory.KindDecision, memory.KindDiscovery, memory.KindProject, memory.KindIssue} {
		if _, ok := byKind[kind]; !ok {
			t.Errorf("nothing was remembered as a %s", kind)
		}
	}
	// A discovery written as a bare sentence is still a memory, with the first
	// sentence as its title: a model asked for objects sometimes answers with
	// strings.
	if got := byKind[memory.KindDiscovery]; got.Title != "The seed script writes 10,000 rows" || got.Content == "" {
		t.Errorf("the discovery = %q / %q", got.Title, got.Content)
	}
	// The narrative is what the recap finds again, by the event it came from.
	summary, err := store.LatestFrom(ctx, "hello-stack", memory.KindEpisodic, memory.EventConversationCompacted)
	if err != nil || !strings.Contains(summary.Content, "reminders page to paginate") {
		t.Fatalf("LatestFrom() = %+v, %v", summary, err)
	}
	working, err := store.WorkingMemory(ctx, "hello-stack")
	if err != nil || working.Goal != "Ship the reminders page" || working.CurrentTask != "Pagination" {
		t.Fatalf("working memory = %+v, %v", working, err)
	}
}

// A model asked for JSON answers with JSON, eventually. What it wraps that in
// is not worth losing a whole conversation's summary over.
func TestParseConsolidation(t *testing.T) {
	for _, tc := range []struct {
		what   string
		answer string
		want   string // the summary, or "" when it should be refused
	}{
		{"plain", `{"summary":"we talked"}`, "we talked"},
		{"fenced", "```json\n{\"summary\":\"we talked\"}\n```", "we talked"},
		{"with a preamble", "Sure! Here it is:\n{\"summary\":\"we talked\"}\nHope that helps.", "we talked"},
		{"no summary, but memories", `{"decisions":[{"title":"Paginate"}]}`, ""},
		{"nothing at all", `{}`, ""},
		{"not json", "I'd rather not.", ""},
		{"empty", "", ""},
	} {
		got, err := parseConsolidation(tc.answer)
		switch {
		case tc.what == "no summary, but memories":
			if err != nil || len(got.Decisions) != 1 {
				t.Errorf("%s: parseConsolidation() = %+v, %v; want the decision kept", tc.what, got, err)
			}
		case tc.want == "":
			if err == nil {
				t.Errorf("%s: parseConsolidation(%q) = %+v, want an error", tc.what, tc.answer, got)
			}
		default:
			if err != nil || got.Summary != tc.want {
				t.Errorf("%s: parseConsolidation() = %+v, %v; want %q", tc.what, got, err, tc.want)
			}
		}
	}
}
