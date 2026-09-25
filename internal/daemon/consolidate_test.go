package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// consolidationProject is a daemon with one project, and the lead the
// distillation pass runs as. The lead is a row, not a machine: nothing here
// starts a session, because askLead is what a session would be.
func consolidationProject(t *testing.T) (testDaemon, state.Agent) {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if _, err := d.client.AddProject(context.Background(), api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	return d, state.Agent{Project: "hello-stack", Name: state.LeadName, Role: state.RoleLead}
}

// fill appends n events, spaced a minute apart, and answers the last one.
func fill(t *testing.T, d testDaemon, project string, n int, from time.Time) memory.Event {
	t.Helper()
	var last memory.Event
	for i := range n {
		e, err := d.srv.memory().AppendEvent(context.Background(), memory.Event{
			Project: project, Agent: "agent-04", Type: "agent_finished",
			At: from.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		last = e
	}
	return last
}

const distillationAnswer = `{
	"memories": [
		{"kind": "discovery", "title": "The seed script writes 10,000 rows", "detail": "It is why the page was slow.", "importance": 4},
		{"kind": "project", "title": "Every agent branches from main"}
	],
	"supersede": [
		{"replaces": "%s", "kind": "project", "title": "The API listens on 7777 and 8080 in CI"}
	],
	"resolve": [
		{"id": "%s", "why": "fixed in #81"}
	]
}`

// The watermark trigger, end to end with a fake session behind it: a project
// that has gathered more events than its setting gets one hidden prompt, what
// the model says becomes memories, and the next check reads nothing it has
// already folded in.
func TestDistillationTriggeredByWatermark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, lead := consolidationProject(t)
	store := d.srv.memory()
	start := time.Now().Add(-24 * time.Hour).Truncate(time.Minute)

	stale, err := store.AddMemory(ctx, memory.Memory{Project: "hello-stack", Kind: memory.KindProject,
		Title: "The API listens on 7777"})
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := store.AddMemory(ctx, memory.Memory{Project: "hello-stack", Kind: memory.KindIssue,
		Title: "The count query is unindexed"})
	if err != nil {
		t.Fatal(err)
	}

	// Under the threshold, nothing is asked.
	if _, err := d.client.SetConsolidation(ctx, "hello-stack", 20); err != nil {
		t.Fatal(err)
	}
	answer := "Here you go:\n```json\n" +
		strings.Replace(strings.Replace(distillationAnswer, "%s", stale.ID, 1), "%s", fixed.ID, 1) + "\n```"
	var asks []string
	d.srv.askLead = func(_ context.Context, a state.Agent, ask string) (string, error) {
		if a.Name != state.LeadName {
			t.Errorf("the hidden prompt went to %q, want the lead", a.Name)
		}
		asks = append(asks, ask)
		return answer, nil
	}
	fill(t, d, "hello-stack", 5, start)
	if d.srv.distillIfDue(ctx, lead) {
		t.Fatal("five events set off a consolidation that asks for twenty")
	}
	if len(asks) != 0 {
		t.Fatalf("%d prompts were sent under the threshold", len(asks))
	}

	// Over it, exactly one prompt, carrying what the project already knows
	// and what has happened since.
	last := fill(t, d, "hello-stack", 20, start.Add(time.Hour))
	if !d.srv.distillIfDue(ctx, lead) {
		t.Fatal("twenty-five events didn't set off a consolidation")
	}
	if len(asks) != 1 {
		t.Fatalf("%d prompts were sent, want one", len(asks))
	}
	for _, want := range []string{"The API listens on 7777", "agent_finished", "AgentBox asking"} {
		if !strings.Contains(asks[0], want) {
			t.Errorf("the prompt doesn't mention %q", want)
		}
	}

	// What it said is memories, written through the same door everything
	// else writes through: sourced to one event, and the replacement points
	// at what it replaced.
	memories, err := store.Memories(ctx, "hello-stack", nil)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]memory.Memory{}
	for _, m := range memories {
		byTitle[m.Title] = m
	}
	if len(memories) != 3 {
		t.Errorf("%d live memories, want 3: %+v", len(memories), memories)
	}
	seed, ok := byTitle["The seed script writes 10,000 rows"]
	if !ok || seed.Kind != memory.KindDiscovery || seed.Importance != 4 {
		t.Errorf("the discovery = %+v", seed)
	}
	if seed.SourceEventID == "" {
		t.Error("a distilled memory names no event it came from")
	}
	if got, err := store.Event(ctx, "hello-stack", seed.SourceEventID); err != nil || got.Type != memory.EventMemoryConsolidated {
		t.Errorf("its event = %+v, %v; want a %s", got, err, memory.EventMemoryConsolidated)
	}
	replacement, ok := byTitle["The API listens on 7777 and 8080 in CI"]
	if !ok || replacement.SupersedesID != stale.ID {
		t.Errorf("the replacement = %+v, want it superseding %s", replacement, stale.ID)
	}
	if was, err := store.Memory(ctx, "hello-stack", fixed.ID); err != nil || !was.Resolved() || was.ResolvedBy != "fixed in #81" {
		t.Errorf("the closed issue = %+v, %v", was, err)
	}

	// The pass says what it did, and the watermark is where it got to.
	passes, err := store.Passes(ctx, "hello-stack", 5)
	if err != nil || len(passes) != 1 {
		t.Fatalf("Passes() = %+v, %v; want one", passes, err)
	}
	pass := passes[0]
	if pass.Kind != memory.PassDistill || pass.EventsRead != 25 || pass.MemoriesWritten != 2 ||
		pass.MemoriesSuperseded != 1 || pass.MemoriesResolved != 1 {
		t.Errorf("the pass = %+v", pass)
	}
	if pass.InputBytes == 0 || pass.OutputBytes == 0 {
		t.Errorf("the pass cost %d in and %d out, want both counted", pass.InputBytes, pass.OutputBytes)
	}
	if pass.ThroughEventID != last.ID {
		t.Errorf("the pass read through %q, want the last event %q", pass.ThroughEventID, last.ID)
	}

	// Nothing has happened since, so nothing is asked again — and the event
	// the pass wrote itself doesn't count as history to fold in.
	if d.srv.distillIfDue(ctx, lead) {
		t.Error("a second check re-read a window it had already folded in")
	}
	if len(asks) != 1 {
		t.Errorf("%d prompts in total, want one", len(asks))
	}
}

// A pass that the chat couldn't answer costs the project nothing but a
// recorded failure: no memories, and a watermark that hasn't moved, so the
// same window is read again when there is a session that can read it.
func TestFailedDistillationKeepsWatermark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, lead := consolidationProject(t)
	if _, err := d.client.SetConsolidation(ctx, "hello-stack", 20); err != nil {
		t.Fatal(err)
	}
	fill(t, d, "hello-stack", 25, time.Now().Add(-24*time.Hour))

	d.srv.askLead = func(context.Context, state.Agent, string) (string, error) {
		return "I'd rather not.", nil
	}
	if d.srv.distillIfDue(ctx, lead) {
		t.Error("a pass that couldn't be read reported success")
	}
	if got, err := d.srv.memory().Watermark(ctx, "hello-stack"); err != nil || !got.IsZero() {
		t.Errorf("Watermark() = %v, %v; want it unmoved", got, err)
	}
	passes, err := d.srv.memory().Passes(ctx, "hello-stack", 5)
	if err != nil || len(passes) != 1 || passes[0].Error == "" {
		t.Fatalf("Passes() = %+v, %v; want one recorded failure", passes, err)
	}

	// A chat that is busy is not a failure at all: nothing was asked, so
	// nothing is recorded.
	d.srv.askLead = func(context.Context, state.Agent, string) (string, error) { return "", chat.ErrBusy }
	if d.srv.distillIfDue(ctx, lead) {
		t.Error("a busy chat reported a pass")
	}
	if passes, err := d.srv.memory().Passes(ctx, "hello-stack", 5); err != nil || len(passes) != 1 {
		t.Errorf("Passes() = %d, %v; want still one", len(passes), err)
	}
}

// Off means off: neither half runs, and the on-demand route says so rather
// than quietly doing nothing.
func TestConsolidationCanBeSwitchedOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, lead := consolidationProject(t)
	fill(t, d, "hello-stack", 30, time.Now().Add(-24*time.Hour))
	if _, err := d.srv.memory().AddMemory(ctx, memory.Memory{Project: "hello-stack", Kind: memory.KindEpisodic,
		Title: "The demo was recorded", Importance: 4, CreatedAt: time.Now().Add(-90 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetConsolidation(ctx, "hello-stack", state.ConsolidationOff); err != nil {
		t.Fatal(err)
	}
	d.srv.askLead = func(context.Context, state.Agent, string) (string, error) {
		t.Error("a project with consolidation off was asked to distil")
		return "", nil
	}
	if d.srv.distillIfDue(ctx, lead) {
		t.Error("a project with consolidation off distilled")
	}
	d.srv.consolidateAll(ctx)
	if passes, err := d.srv.memory().Passes(ctx, "hello-stack", 5); err != nil || len(passes) != 0 {
		t.Errorf("Passes() = %+v, %v; want none", passes, err)
	}
	if _, err := d.client.ProjectMemory("hello-stack").Consolidate(ctx, false); err == nil {
		t.Error("consolidating on demand with the setting off was accepted")
	}
}

// The setting is a project setting like the others: a default, a round trip
// through the API, an off, and a refusal for what makes no sense.
func TestConsolidationSettingRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, _ := consolidationProject(t)
	p, err := d.client.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if p.Consolidation != state.DefaultConsolidation {
		t.Errorf("a new project consolidates every %d events, want %d", p.Consolidation, state.DefaultConsolidation)
	}
	if p, err = d.client.SetConsolidation(ctx, "hello-stack", 50); err != nil || p.Consolidation != 50 {
		t.Fatalf("SetConsolidation(50) = %+v, %v", p, err)
	}
	if p, err = d.client.SetConsolidation(ctx, "hello-stack", state.ConsolidationOff); err != nil || p.Consolidation != 0 {
		t.Fatalf("SetConsolidation(off) = %+v, %v", p, err)
	}
	if again, err := d.client.Project(ctx, "hello-stack"); err != nil || again.Consolidation != 0 {
		t.Errorf("Project() = %+v, %v; want it still off", again, err)
	}
	if _, err := d.client.SetConsolidation(ctx, "hello-stack", 3); err == nil {
		t.Error("a window below the minimum was accepted")
	}
	if _, err := d.client.SetConsolidation(ctx, "hello-stack", 100000); err == nil {
		t.Error("a window over the maximum was accepted")
	}
}

// The mechanical pass on demand, and the numbers the app will show a
// compression ratio from, over the memory routes.
func TestConsolidationRoutes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, _ := consolidationProject(t)
	m := d.client.ProjectMemory("hello-stack")
	fill(t, d, "hello-stack", 40, time.Now().Add(-24*time.Hour))

	old := time.Now().Add(-90 * 24 * time.Hour)
	for _, want := range []memory.Memory{
		{Kind: memory.KindDiscovery, Title: "FTS5 is in the driver", Content: "modernc.org/sqlite has it.", CreatedAt: old},
		{Kind: memory.KindDiscovery, Title: "FTS5 is in the driver", Content: "modernc.org/sqlite has it. No LIKE fallback.", CreatedAt: old.Add(time.Hour)},
		{Kind: memory.KindProject, Title: "Agent briefs are capped at 3000 words", CreatedAt: old},
		{Kind: memory.KindProject, Title: "The agent brief is capped at 3000 words", CreatedAt: old.Add(time.Hour)},
		{Kind: memory.KindIssue, Title: "The count query is unindexed", Importance: 4, CreatedAt: old},
	} {
		want.Project = "hello-stack"
		if _, err := d.srv.memory().AddMemory(ctx, want); err != nil {
			t.Fatal(err)
		}
	}

	passes, err := m.Consolidate(ctx, false)
	if err != nil || len(passes) != 1 {
		t.Fatalf("Consolidate() = %+v, %v; want one mechanical pass", passes, err)
	}
	pass := passes[0]
	if pass.Kind != api.ConsolidationMechanical {
		t.Errorf("pass kind = %q, want %q", pass.Kind, api.ConsolidationMechanical)
	}
	if pass.MemoriesSuperseded != 1 || pass.DuplicatesFound != 1 || pass.MemoriesDecayed != 1 {
		t.Errorf("the pass = %+v; want one merge, one candidate and one decay", pass)
	}
	if pass.InputBytes != 0 || pass.OutputBytes != 0 {
		t.Errorf("the mechanical pass cost %d in and %d out, want nothing", pass.InputBytes, pass.OutputBytes)
	}

	pairs, err := m.Duplicates(ctx)
	if err != nil || len(pairs) != 1 {
		t.Fatalf("Duplicates() = %+v, %v; want the two brief memories", pairs, err)
	}
	if pairs[0].Similarity < 0.7 {
		t.Errorf("similarity = %v, want it over the threshold", pairs[0].Similarity)
	}

	state, err := m.Consolidation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Events != 40 || state.Memories != 4 || state.Superseded != 1 {
		t.Errorf("Consolidation() = %+v; want 40 events and 4 live memories", state)
	}
	if state.Pending != 40 {
		t.Errorf("Pending = %d, want the 40 nothing has distilled", state.Pending)
	}
	if state.Setting != 200 || state.Passes != 1 || len(state.Recent) != 1 {
		t.Errorf("Consolidation() = %+v", state)
	}

	// An issue closed over the route is out of listings and out of search,
	// and nothing had to invent a memory of a fix.
	issues, err := m.Memories(ctx, api.MemoryKindIssue)
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues = %+v, %v", issues, err)
	}
	closed, err := m.ResolveMemory(ctx, issues[0].ID, "indexed in #81")
	if err != nil || closed.ResolvedBy != "indexed in #81" {
		t.Fatalf("ResolveMemory() = %+v, %v", closed, err)
	}
	if again, err := m.Memories(ctx, api.MemoryKindIssue); err != nil || len(again) != 0 {
		t.Errorf("open issues = %+v, %v; want none left", again, err)
	}
	if found, err := m.Search(ctx, "count query unindexed", 10); err != nil || len(found.Memories) != 0 {
		t.Errorf("a closed issue still comes back from search: %+v, %v", found.Memories, err)
	}
}
