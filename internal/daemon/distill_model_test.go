package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// Which model a distillation runs on, and what happens when it can't (D78).
//
// The pass itself is TestDistillationTriggeredByWatermark's business. What is
// here is only the choice of model: that a project's own setting is what runs,
// that the chat's session is what it falls back to, and that either way the
// pass log says which one really read the events.

// emptyDistillation is a valid answer that writes nothing: enough for a pass
// to succeed, so a test about models isn't also a test about memories.
const emptyDistillation = `{"memories": [], "supersede": [], "resolve": []}`

// dueProject is a project with more events than its setting, ready to distil.
// Its lead runs Claude Code, which is what a real one runs (EnsureLead): the
// AI tool is what "cheap" is resolved against, so a lead without one would
// take the chat's own session whatever the project asked for.
func dueProject(t *testing.T) (testDaemon, state.Agent) {
	t.Helper()
	d, lead := consolidationProject(t)
	lead.AI = "claude"
	if _, err := d.client.SetConsolidation(context.Background(), "hello-stack", 20); err != nil {
		t.Fatal(err)
	}
	fill(t, d, "hello-stack", 25, time.Now().Add(-24*time.Hour))
	return d, lead
}

// lastPass is the newest pass of the project, or a failure if there is none.
func lastPass(t *testing.T, d testDaemon) memory.Pass {
	t.Helper()
	passes, err := d.srv.memory().Passes(context.Background(), "hello-stack", 1)
	if err != nil || len(passes) == 0 {
		t.Fatalf("Passes() = %+v, %v; want one", passes, err)
	}
	return passes[0]
}

// A project on the default setting distils in a session of its own, on its
// tool's cheap model — never on the chat's. What the pass records is the model
// the session reported, not the one it was asked for: a tool resolves an alias
// like "haiku" to whatever the account really runs, and the pass log is only
// worth reading if it says what was actually spent.
func TestDistillationRunsOnTheProjectsCheapModel(t *testing.T) {
	ctx := context.Background()
	d, lead := dueProject(t)

	var asked []string
	d.srv.askAside = func(_ context.Context, a state.Agent, model, ask string) (string, string, error) {
		if a.Name != state.LeadName {
			t.Errorf("the aside ran as %q, want the lead", a.Name)
		}
		if !strings.Contains(ask, "AgentBox asking") {
			t.Error("the aside was sent something other than the distillation prompt")
		}
		asked = append(asked, model)
		return emptyDistillation, "claude-haiku-4-5", nil
	}
	d.srv.askLead = func(context.Context, state.Agent, string) (string, error) {
		t.Error("the chat's own session was asked to distil, on a project with a model of its own")
		return "", nil
	}

	if !d.srv.distillIfDue(ctx, lead) {
		t.Fatal("a project over its threshold didn't distil")
	}
	if len(asked) != 1 || asked[0] != state.CheapModelFor("claude") {
		t.Fatalf("the aside was asked for %v, want one pass on %q", asked, state.CheapModelFor("claude"))
	}
	if pass := lastPass(t, d); pass.Model != "claude-haiku-4-5" {
		t.Errorf("the pass says it ran on %q, want the model the session reported", pass.Model)
	}
}

// A cheap model the account won't run, or a session that won't start, is a
// reason to consolidate differently and never a reason not to consolidate:
// the pass falls back to the chat's own session, exactly as D76 did it, and
// the pass log says it ran on something else than the project asked for.
func TestDistillationFallsBackToTheChatsSession(t *testing.T) {
	ctx := context.Background()
	d, lead := dueProject(t)

	d.srv.askAside = func(context.Context, state.Agent, string, string) (string, string, error) {
		return "", "", errors.New("Claude Code wouldn't distil on \"haiku\": this account's menu doesn't list it")
	}
	asks := 0
	d.srv.askLead = func(context.Context, state.Agent, string) (string, error) {
		asks++
		return emptyDistillation, nil
	}

	if !d.srv.distillIfDue(ctx, lead) {
		t.Fatal("a refused cheap model lost the consolidation instead of falling back")
	}
	if asks != 1 {
		t.Fatalf("the chat's session was asked %d times, want once", asks)
	}
	// "" is the chat's own model, which is what the fallback ran on: the app
	// reads it as "the chat's model", against a project whose setting says
	// cheap. That difference is the whole record that a fallback happened.
	pass := lastPass(t, d)
	if pass.Model != "" {
		t.Errorf("the pass says it ran on %q, want the chat's own model", pass.Model)
	}
	if pass.Error != "" || pass.ThroughAt.IsZero() {
		t.Errorf("the fallback recorded a failed pass (%q) or didn't move the watermark", pass.Error)
	}
	if got, err := d.srv.memory().Watermark(ctx, "hello-stack"); err != nil || got.IsZero() {
		t.Errorf("Watermark() = %v, %v; want it moved by the fallback", got, err)
	}
}

// A project that asks for the chat's own model never starts a second session:
// that is D76's behaviour, kept as a choice.
func TestDistillationOnTheChatsModelStartsNoSession(t *testing.T) {
	ctx := context.Background()
	d, lead := dueProject(t)
	if _, err := d.client.SetConsolidationModel(ctx, "hello-stack", state.ConsolidationModelChat); err != nil {
		t.Fatal(err)
	}
	d.srv.askAside = func(context.Context, state.Agent, string, string) (string, string, error) {
		t.Error("a project set to the chat's own model started a session of its own")
		return "", "", nil
	}
	asks := 0
	d.srv.askLead = func(context.Context, state.Agent, string) (string, error) {
		asks++
		return emptyDistillation, nil
	}
	if !d.srv.distillIfDue(ctx, lead) || asks != 1 {
		t.Fatalf("the chat's session was asked %d times, want one pass", asks)
	}
}

// An AI tool AgentBox knows no cheap model for gets the chat's own session
// rather than a guess: the setting says "the cheap one for this tool", and
// inventing a model id for a tool nobody has measured is how an account ends
// up answering on something nobody chose.
func TestCheapWithNoCheapModelKnownUsesTheChat(t *testing.T) {
	ctx := context.Background()
	d, lead := dueProject(t)
	lead.AI = "codex"
	d.srv.askAside = func(context.Context, state.Agent, string, string) (string, string, error) {
		t.Error("a tool with no cheap model started an aside session anyway")
		return "", "", nil
	}
	asks := 0
	d.srv.askLead = func(context.Context, state.Agent, string) (string, error) {
		asks++
		return emptyDistillation, nil
	}
	if !d.srv.distillIfDue(ctx, lead) || asks != 1 {
		t.Fatalf("the chat's session was asked %d times, want one pass", asks)
	}
}

// A named model is a model, and goes to the aside session as it was typed.
func TestNamedConsolidationModelIsUsedAsItIs(t *testing.T) {
	ctx := context.Background()
	d, lead := dueProject(t)
	if _, err := d.client.SetConsolidationModel(ctx, "hello-stack", "claude-fable-5-1"); err != nil {
		t.Fatal(err)
	}
	var asked string
	d.srv.askAside = func(_ context.Context, _ state.Agent, model, _ string) (string, string, error) {
		asked = model
		return emptyDistillation, "", nil
	}
	if !d.srv.distillIfDue(ctx, lead) {
		t.Fatal("a project with a named model didn't distil")
	}
	if asked != "claude-fable-5-1" {
		t.Errorf("the aside was asked for %q, want the model the project named", asked)
	}
	// A session that reports no model of its own still records what it was
	// asked for: it is the best thing known about what ran.
	if pass := lastPass(t, d); pass.Model != "claude-fable-5-1" {
		t.Errorf("the pass says it ran on %q, want the model that was asked for", pass.Model)
	}
}

// One pass per project at a time. Ask enforced this by accident — a chat can
// only be asked one thing at once — and a session of its own can't, so the
// window would be read twice and everything written twice.
func TestOnlyOneDistillationPerProjectAtATime(t *testing.T) {
	ctx := context.Background()
	d, lead := dueProject(t)

	running, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	d.srv.askAside = func(context.Context, state.Agent, string, string) (string, string, error) {
		once.Do(func() { close(running) })
		<-release
		return emptyDistillation, "haiku", nil
	}
	var first bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		first = d.srv.distillIfDue(ctx, lead)
	}()
	<-running

	if d.srv.distillIfDue(ctx, lead) {
		t.Error("a second distillation ran while the first one held the window")
	}
	close(release)
	<-done
	if !first {
		t.Error("the first distillation didn't finish")
	}
	if passes, err := d.srv.memory().Passes(ctx, "hello-stack", 5); err != nil || len(passes) != 1 {
		t.Errorf("Passes() = %d, %v; want the one pass that ran", len(passes), err)
	}
}

// The setting is a project setting like the others: a default that is cheap
// rather than whatever the chat costs, a round trip through the API, and a
// refusal for what is plainly not a model id.
func TestConsolidationModelRoundTrips(t *testing.T) {
	ctx := context.Background()
	d, _ := consolidationProject(t)
	p, err := d.client.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if p.ConsolidationModel != state.DefaultConsolidationModel {
		t.Errorf("a new project distils on %q, want %q", p.ConsolidationModel, state.DefaultConsolidationModel)
	}
	if p, err = d.client.SetConsolidationModel(ctx, "hello-stack", "haiku"); err != nil || p.ConsolidationModel != "haiku" {
		t.Fatalf("SetConsolidationModel(haiku) = %+v, %v", p, err)
	}
	if p, err = d.client.SetConsolidationModel(ctx, "hello-stack", state.ConsolidationModelChat); err != nil || p.ConsolidationModel != "" {
		t.Fatalf("SetConsolidationModel(chat) = %+v, %v", p, err)
	}
	if again, err := d.client.Project(ctx, "hello-stack"); err != nil || again.ConsolidationModel != "" {
		t.Errorf("Project() = %+v, %v; want it still on the chat's model", again, err)
	}
	if _, err := d.client.SetConsolidationModel(ctx, "hello-stack", "the cheap one please"); err == nil {
		t.Error("a sentence was accepted as a model id")
	}
}

// What "cheap" resolves to is per AI tool, and a tool with no entry resolves
// to the chat's own model rather than to another vendor's.
func TestConsolidationModelResolvesPerTool(t *testing.T) {
	t.Parallel()
	cheap := state.Project{ConsolidationModel: state.ConsolidationModelCheap}
	if got := cheap.ConsolidationModelFor("claude"); got != "haiku" {
		t.Errorf("cheap on Claude Code = %q, want haiku", got)
	}
	if got := cheap.ConsolidationModelFor("opencode"); got != "" {
		t.Errorf("cheap on a tool with no cheap model = %q, want the chat's own", got)
	}
	named := state.Project{ConsolidationModel: "claude-fable-5-1"}
	if got := named.ConsolidationModelFor("claude"); got != "claude-fable-5-1" {
		t.Errorf("a named model resolved to %q", got)
	}
	chat := state.Project{ConsolidationModel: state.ConsolidationModelChat}
	if got := chat.ConsolidationModelFor("claude"); got != "" {
		t.Errorf("the chat's own model resolved to %q", got)
	}
}
