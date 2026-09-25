package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// answerJSON is a tool that answers a hidden prompt the way a distillation's
// model does, and says which model it was on while it did.
func answerJSON(f *fakeTool, sessionID, _ string) acp.PromptResponse {
	f.mu.Lock()
	model := f.values["model"]
	f.mu.Unlock()
	f.update(sessionID, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"{\"memories\":[],\"model\":\"`+model+`\"}"}}`)
	return acp.PromptResponse{StopReason: "end_turn"}
}

// asideManager is a manager whose first launch is the chat's own adapter and
// whose every later launch is an aside's: two tools, so a test can tell what
// each session was asked, and prove the aside never touched the other one.
func asideManager(t *testing.T, store *state.Store, live, aside *fakeTool) *Manager {
	t.Helper()
	launched := 0
	m := &Manager{Store: store, Publish: func(api.ChatEvent) {}, Version: "test"}
	m.Launch = func(ctx context.Context, a state.Agent, status func(string)) (*Process, error) {
		launched++
		if launched == 1 {
			return live.launch(ctx, a, status)
		}
		return aside.launch(ctx, a, status)
	}
	t.Cleanup(m.Close)
	return m
}

// The aside is a second session, on the model it was told to use, and the
// chat it belongs to doesn't notice it happened: no items, no turn, and the
// session the user is talking to is still the one they were talking to.
func TestAskAsideRunsInItsOwnSessionOnItsOwnModel(t *testing.T) {
	store := openStore(t)
	live, aside := newFakeTool(answerHello), newFakeTool(answerJSON)
	m := asideManager(t, store, live, aside)

	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))
	before, err := store.Chat(context.Background(), testAgent.Project, testAgent.Name)
	if err != nil {
		t.Fatal(err)
	}

	answer, ranOn, err := m.AskAside(context.Background(), testAgent, "haiku", "distil this")
	if err != nil {
		t.Fatalf("AskAside() = %v", err)
	}
	if !strings.Contains(answer, `"model":"haiku"`) {
		t.Errorf("answer = %q, want the one the aside's model gave", answer)
	}
	if ranOn != "haiku" {
		t.Errorf("ran on %q, want haiku", ranOn)
	}

	// The aside's own session: started, set to the model, asked once.
	if n := len(aside.called(acp.MethodSessionNew)); n != 1 {
		t.Errorf("the aside started %d sessions, want one of its own", n)
	}
	if n := len(aside.called(acp.MethodSessionResume)) + len(aside.called(acp.MethodSessionLoad)); n != 0 {
		t.Errorf("the aside resumed %d sessions, want none: it must never read the conversation", n)
	}
	if n := len(aside.called(acp.MethodSessionPrompt)); n != 1 {
		t.Errorf("the aside was prompted %d times, want once", n)
	}
	if got := setModels(t, aside); !slices.Equal(got, []string{"haiku"}) {
		t.Errorf("the aside set the model to %v, want [haiku]", got)
	}

	// The chat's own session: untouched. One turn, one session, and the
	// stored session id is the one it had.
	if n := len(live.called(acp.MethodSessionPrompt)); n != 1 {
		t.Errorf("the chat's session was prompted %d times, want only the user's turn", n)
	}
	if got := setModels(t, live); len(got) != 0 {
		t.Errorf("the chat's session had its model set to %v: an aside must not move it", got)
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant"}) {
		t.Errorf("items %v, want the turn and nothing the aside added", got)
	}
	after, err := store.Chat(context.Background(), testAgent.Project, testAgent.Name)
	if err != nil || after.SessionID != before.SessionID || before.SessionID == "" {
		t.Errorf("stored session = %q, %v; want the chat's own %q", after.SessionID, err, before.SessionID)
	}
}

// An aside runs without any chat at all: nothing has to have been started,
// which is the point of not borrowing the chat's session. It is also how a
// project whose chat is mid-turn still gets consolidated.
func TestAskAsideNeedsNoChatSession(t *testing.T) {
	store := openStore(t)
	live, aside := newFakeTool(answerHello), newFakeTool(answerJSON)
	m := asideManager(t, store, aside, live) // the first launch is the aside's
	if _, _, err := m.AskAside(context.Background(), testAgent, "", "distil this"); err != nil {
		t.Fatalf("AskAside() = %v", err)
	}
	if n := len(aside.called(acp.MethodSessionNew)); n != 1 {
		t.Errorf("%d sessions started, want one", n)
	}
	// With no model named, nothing is set and the session's own is reported.
	if got := setModels(t, aside); len(got) != 0 {
		t.Errorf("the model was set to %v, want it left alone", got)
	}
	if _, err := m.Thread(testAgent); err != nil {
		t.Fatal(err)
	}
	if n := len(live.called(acp.MethodInitialize)); n != 0 {
		t.Errorf("the chat's adapter was started %d times by an aside", n)
	}
}

// A model the account's menu doesn't list is refused before the prompt is
// sent, not quietly swapped for something else. The caller falls back with
// what it cost: nothing.
func TestAskAsideRefusesAModelTheMenuDoesNotOffer(t *testing.T) {
	store := openStore(t)
	aside := newFakeTool(answerJSON)
	m := asideManager(t, store, aside, aside)
	_, _, err := m.AskAside(context.Background(), testAgent, "brand-new-model", "distil this")
	if err == nil {
		t.Fatal("AskAside() took a model the session never offered")
	}
	if !strings.Contains(err.Error(), "brand-new-model") {
		t.Errorf("error = %v, want it to name the model", err)
	}
	if n := len(aside.called(acp.MethodSessionPrompt)); n != 0 {
		t.Errorf("%d prompts were sent on a model that was refused, want none", n)
	}
}

// An aside answers no permission request. Nobody is watching this session, so
// there is nobody to say yes on the user's behalf — and a distillation has no
// business running tools in the lead's worktree.
func TestAskAsideCancelsPermissionRequests(t *testing.T) {
	store := openStore(t)
	var outcome string
	aside := newFakeTool(func(f *fakeTool, sessionID, _ string) acp.PromptResponse {
		outcome = f.ask(sessionID, `{"toolCallId":"call-1","title":"Run a script"}`)
		f.update(sessionID, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"{}"}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m := asideManager(t, store, aside, aside)
	if _, _, err := m.AskAside(context.Background(), testAgent, "", "distil this"); err != nil {
		t.Fatalf("AskAside() = %v", err)
	}
	if outcome != "cancelled" {
		t.Errorf("the aside answered a permission request with %q, want cancelled", outcome)
	}
}

// A session that says nothing is an error rather than an empty answer: the
// caller must not read "" as "this stretch of history established nothing".
func TestAskAsideSaysWhenTheSessionSaidNothing(t *testing.T) {
	store := openStore(t)
	aside := newFakeTool(func(*fakeTool, string, string) acp.PromptResponse {
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m := asideManager(t, store, aside, aside)
	if _, _, err := m.AskAside(context.Background(), testAgent, "", "distil this"); err == nil {
		t.Fatal("AskAside() answered with nothing and no error")
	}
}

// Model is what the chat is running on, for a pass that fell back to it: what
// its tool last said, and the stored preference before a session has started.
func TestModelIsWhatTheChatRunsOn(t *testing.T) {
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if got := m.Model(testAgent); got != "" {
		t.Errorf("Model() = %q before any conversation exists, want nothing known", got)
	}
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))
	if got := m.Model(testAgent); got != "sonnet" {
		t.Errorf("Model() = %q, want the sonnet the session reports", got)
	}
}

// setModels are the values every set_config_option call gave the model, in
// order.
func setModels(t *testing.T, f *fakeTool) []string {
	t.Helper()
	var out []string
	for _, params := range f.called(acp.MethodSetConfigOption) {
		var req struct {
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatal(err)
		}
		if req.ConfigID == "model" {
			out = append(out, req.Value)
		}
	}
	return out
}
