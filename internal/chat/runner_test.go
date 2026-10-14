package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

var testLead = state.Agent{Project: "hello", Name: state.LeadName, AI: "claude", Worktree: "/work/hello", Role: state.RoleLead}

// toolRecorder wraps a fake tool's launch to remember which AI tool each
// launch was for.
type toolRecorder struct {
	mu    sync.Mutex
	tools []string
}

func (r *toolRecorder) launch(f *fakeTool) Launcher {
	return func(ctx context.Context, a state.Agent, status func(string)) (*Process, error) {
		r.mu.Lock()
		r.tools = append(r.tools, a.AI)
		r.mu.Unlock()
		return f.launch(ctx, a, status)
	}
}

func (r *toolRecorder) launched() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.tools)
}

func newRunnerManager(t *testing.T, f *fakeTool) (*Manager, *state.Store, *toolRecorder) {
	t.Helper()
	store := openStore(t)
	m, _ := newManager(t, store, f)
	rec := &toolRecorder{}
	m.Launch = rec.launch(f)
	return m, store, rec
}

// promptText is the text of the n-th prompt the fake tool was sent.
func promptText(t *testing.T, f *fakeTool, n int) string {
	t.Helper()
	prompts := f.called(acp.MethodSessionPrompt)
	if len(prompts) <= n {
		t.Fatalf("%d prompts sent, want at least %d", len(prompts), n+1)
	}
	var req acp.PromptRequest
	if err := json.Unmarshal(prompts[n], &req); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, b := range req.Prompt {
		text.WriteString(b.Text)
	}
	return text.String()
}

// A chat that has never run a session has nothing to roll: the chosen tool is
// simply the one it runs from now, and its first message starts it.
func TestRunnerWithNoSessionSwitchesAtOnce(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, store, rec := newRunnerManager(t, f)
	s, err := m.SetRunner(testLead, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Tool != "codex" || s.NextTool != "" {
		t.Fatalf("session tool %q, next %q; want codex, none pending", s.Tool, s.NextTool)
	}
	if _, err := m.Send(testLead, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the turn", turnsEnded(1))
	if got := rec.launched(); !slices.Equal(got, []string{"codex"}) {
		t.Errorf("launched %v, want codex", got)
	}
	stored, err := store.Chat(context.Background(), testLead.Project, testLead.Name)
	if err != nil || stored.Tool != "codex" || stored.NextTool != "" {
		t.Errorf("stored tool %q, next %q (%v); want codex kept", stored.Tool, stored.NextTool, err)
	}
}

// With a session behind it, the switch waits for the next message, which
// rolls the session over: the new tool's session is a new one, and its first
// prompt carries the conversation it never saw.
func TestRunnerSwitchesToolAtTheNextMessage(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, store, rec := newRunnerManager(t, f)
	if _, err := m.Send(testLead, "remember the word pelican"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the first turn", turnsEnded(1))

	s, err := m.SetRunner(testLead, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Tool != "claude" || s.NextTool != "codex" {
		t.Fatalf("session tool %q, next %q; want claude until the next message, codex pending", s.Tool, s.NextTool)
	}
	if got := m.PendingTool(testLead); got != "codex" {
		t.Errorf("PendingTool() = %q, want codex", got)
	}
	if n := len(rec.launched()); n != 1 {
		t.Errorf("%d launches before the next message, want 1: choosing doesn't switch", n)
	}

	if _, err := m.Send(testLead, "what was the word?"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testLead, "the second turn", turnsEnded(2))
	if got := rec.launched(); !slices.Equal(got, []string{"claude", "codex"}) {
		t.Errorf("launched %v, want claude then codex", got)
	}
	if n := len(f.called(acp.MethodSessionNew)); n != 2 {
		t.Errorf("%d sessions started, want 2: another tool can't resume the old one", n)
	}
	if n := len(f.called(acp.MethodSessionResume)); n != 0 {
		t.Errorf("%d sessions resumed, want none", n)
	}
	prompt := promptText(t, f, 1)
	if !strings.Contains(prompt, "from Claude Code to Codex") || !strings.Contains(prompt, "pelican") || !strings.HasSuffix(prompt, "what was the word?") {
		t.Errorf("the new tool's first prompt = %q, want the conversation handed over before the message", prompt)
	}
	if notice := find(th, "notice", 0); !strings.Contains(notice.Text, "Switched from Claude Code to Codex") {
		t.Errorf("notice = %q, want it to say the tool changed", notice.Text)
	}
	if th.Session.Tool != "codex" || th.Session.NextTool != "" {
		t.Errorf("session tool %q, next %q; want codex, none pending", th.Session.Tool, th.Session.NextTool)
	}
	stored, err := store.Chat(context.Background(), testLead.Project, testLead.Name)
	if err != nil || stored.Tool != "codex" || stored.NextTool != "" || stored.Handoff != "" {
		t.Errorf("stored tool %q, next %q, handoff %d bytes (%v); want codex, the handoff spent", stored.Tool, stored.NextTool, len(stored.Handoff), err)
	}
}

// Choosing the tool the chat already runs calls a pending switch off.
func TestRunnerChoosingTheSameToolCallsTheSwitchOff(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, _, rec := newRunnerManager(t, f)
	if _, err := m.Send(testLead, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the turn", turnsEnded(1))
	if _, err := m.SetRunner(testLead, "opencode", ""); err != nil {
		t.Fatal(err)
	}
	s, err := m.SetRunner(testLead, "claude", "work")
	if err != nil {
		t.Fatal(err)
	}
	if s.NextTool != "" {
		t.Errorf("next tool %q, want none", s.NextTool)
	}
	if _, err := m.Send(testLead, "again"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the second turn", turnsEnded(2))
	if got := rec.launched(); !slices.Equal(got, []string{"claude"}) {
		t.Errorf("launched %v, want the one claude session carrying on", got)
	}
}

// A model or a mode means nothing to another tool: each tool's choices are
// put aside while another runs, and come back with it.
func TestRunnerKeepsEachToolsChoices(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, store, _ := newRunnerManager(t, f)
	ctx := context.Background()
	if err := store.SaveChat(ctx, testLead.Project, testLead.Name, state.Chat{Options: map[string]string{"model": "opus", "mode": "plan"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetRunner(testLead, "codex", ""); err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Chat(ctx, testLead.Project, testLead.Name)
	if _, ok := stored.Options["model"]; ok {
		t.Errorf("codex's options %v, want Claude Code's model put aside", stored.Options)
	}
	stored.Options["model"] = "gpt-5.5"
	if err := store.SaveChat(ctx, testLead.Project, testLead.Name, stored); err != nil {
		t.Fatal(err)
	}
	m.Forget(testLead.Ref()) // read the store afresh

	if _, err := m.SetRunner(testLead, "claude", ""); err != nil {
		t.Fatal(err)
	}
	stored, _ = store.Chat(ctx, testLead.Project, testLead.Name)
	if stored.Options["model"] != "opus" || stored.Options["mode"] != "plan" || stored.Options["codex/model"] != "gpt-5.5" {
		t.Errorf("options %v, want Claude Code's back and Codex's put aside", stored.Options)
	}
}

// Clearing the conversation leaves nothing to carry over, so a pending tool
// is simply the one the chat runs next.
func TestRunnerClearAppliesThePendingTool(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, _, _ := newRunnerManager(t, f)
	if _, err := m.Send(testLead, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the turn", turnsEnded(1))
	if _, err := m.SetRunner(testLead, "cursor", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Clear(testLead); err != nil {
		t.Fatal(err)
	}
	th, err := m.Thread(testLead)
	if err != nil {
		t.Fatal(err)
	}
	if th.Session.Tool != "cursor" || th.Session.NextTool != "" {
		t.Errorf("session tool %q, next %q; want cursor now", th.Session.Tool, th.Session.NextTool)
	}
}

// The summary path: the old session is asked for its summary, the session
// rolls, and the chosen tool takes over with no transcript to read, since the
// summary went into its brief.
func TestBeginSwitchHandsOverAfterTheSummary(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, store, rec := newRunnerManager(t, f)
	m.LeadAccount = func(a state.Agent) (string, error) { return "work", nil }
	if _, err := m.Send(testLead, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the turn", turnsEnded(1))
	if _, err := m.BeginSwitch(context.Background(), testLead, "sum up", func(string, error) error { return nil }); !IsNoSession(err) {
		t.Fatalf("BeginSwitch() with no switch chosen = %v, want IsNoSession", err)
	}
	if _, err := m.SetRunner(testLead, "opencode", ""); err != nil {
		t.Fatal(err)
	}
	var summarised bool
	finish, err := m.BeginSwitch(context.Background(), testLead, "sum up", func(answer string, askErr error) error {
		summarised = askErr == nil && answer == "Hello"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	if !summarised {
		t.Error("settle wasn't given the old session's summary")
	}
	th, err := m.Thread(testLead)
	if err != nil {
		t.Fatal(err)
	}
	if th.Session.Tool != "opencode" {
		t.Errorf("session tool %q, want opencode", th.Session.Tool)
	}
	if !slices.ContainsFunc(th.Items, func(it api.ChatItem) bool {
		return it.Kind == "notice" && strings.Contains(it.Text, "starts from the summary")
	}) {
		t.Errorf("items %v, want a notice saying the new tool starts from the summary", kinds(th))
	}
	stored, _ := store.Chat(context.Background(), testLead.Project, testLead.Name)
	if stored.Tool != "opencode" || stored.SessionID != "" || stored.Handoff != "" {
		t.Errorf("stored tool %q, session %q, handoff %d bytes; want opencode, no session, no transcript", stored.Tool, stored.SessionID, len(stored.Handoff))
	}
	if _, err := m.Send(testLead, "carry on"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the turn after the switch", turnsEnded(2))
	if got := rec.launched(); !slices.Equal(got, []string{"claude", "opencode"}) {
		t.Errorf("launched %v, want claude then opencode", got)
	}

	// Back to Claude Code, read through a record from before the switch, as
	// the lead's row is until the daemon syncs it: the account comes from the
	// hook, not the machine's default, and the stale record doesn't undo it.
	stale := testLead
	stale.AI = "opencode"
	if _, err := m.SetRunner(stale, "claude", "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(stale, "back"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, stale, "the turn back on claude", turnsEnded(3))
	if got := f.launchedOn(); got[len(got)-1] != "work" {
		t.Errorf("launched on %q, want the chosen account last", got)
	}
	if th.Session.Account != "work" {
		t.Errorf("session account %q, want work", th.Session.Account)
	}
}

// An agent runs the tool it was created with.
func TestRunnerIsOnlyALeads(t *testing.T) {
	t.Parallel()
	m, _, _ := newRunnerManager(t, newFakeTool(answerHello))
	if _, err := m.SetRunner(testAgent, "codex", ""); err == nil {
		t.Error("SetRunner() on an agent succeeded, want it refused")
	}
	if _, err := m.SetRunner(testLead, "gemini", ""); err == nil {
		t.Error("SetRunner() with an unknown tool succeeded, want it refused")
	}
}
