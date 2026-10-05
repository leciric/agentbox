package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// TestRewindRestartsFromATurn: rolling back to turn 1 of three drops turns 2
// and 3, forgets the session, says so in a notice that shows the transfer,
// and the next turn's fresh session is told the conversation up to turn 1,
// once.
func TestRewindRestartsFromATurn(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	var mu sync.Mutex
	var prompts []string
	f := newFakeTool(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		mu.Lock()
		prompts = append(prompts, text)
		mu.Unlock()
		return answerHello(f, sessionID, text)
	})
	m, rec := newManager(t, store, f)
	var ended []string
	m.TurnEnded = func(_ state.Agent, turn string) {
		mu.Lock()
		ended = append(ended, turn)
		mu.Unlock()
	}
	var first string
	for i, text := range []string{"build the page", "add a test", "rename it"} {
		it, err := m.Send(testAgent, text)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = it.ID
		}
		waitThread(t, m, testAgent, "the turn", turnsEnded(i+1))
	}
	if n, prompt, ok := m.Turn(testAgent, first); !ok || n != 1 || prompt != "build the page" {
		t.Fatalf("Turn(first) = %d, %q, %v", n, prompt, ok)
	}
	if err := m.Rewind(testAgent, first, "You were rolled back.", "Rolled back to turn 1."); err != nil {
		t.Fatal(err)
	}
	th, _ := m.Thread(testAgent)
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant", "notice"}) {
		t.Fatalf("after Rewind: %v", got)
	}
	notice := th.Items[2]
	if notice.Text != "Rolled back to turn 1." || !strings.Contains(notice.Handoff, "User: build the page") || strings.Contains(notice.Handoff, "add a test") {
		t.Fatalf("notice %q with handoff %q", notice.Text, notice.Handoff)
	}
	if !slices.ContainsFunc(rec.all(), func(ev api.ChatEvent) bool { return ev.After == th.Items[1].ID }) {
		t.Error("no event said what came after the turn was removed")
	}
	if rows, _ := store.ChatItems(context.Background(), testAgent.Project, testAgent.Name); len(rows) != 3 {
		t.Errorf("%d items stored, want 3", len(rows))
	}
	if _, _, ok := m.Turn(testAgent, th.Items[0].ID); !ok {
		t.Error("turn 1 is gone")
	}

	if _, err := m.Send(testAgent, "try again"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(2))
	if _, err := m.Send(testAgent, "and again"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(3))
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 5 || !strings.HasPrefix(prompts[3], "[AgentBox: context transfer] You were rolled back.") ||
		!strings.HasSuffix(prompts[3], "try again") || prompts[4] != "and again" {
		t.Fatalf("prompts after the rollback: %q", prompts[3:])
	}
	if n := len(f.called(acp.MethodSessionNew)); n != 2 {
		t.Errorf("%d sessions started, want 2", n)
	}
	if len(ended) != 5 || ended[0] != first {
		t.Errorf("TurnEnded for %v", ended)
	}
	if stored, _ := store.Chat(context.Background(), testAgent.Project, testAgent.Name); stored.Handoff != "" {
		t.Error("the handoff is still stored after it was sent")
	}
}

// TestRewindRefusesARunningTurn: rolling back mid-turn would put files back
// under the tool's feet.
func TestRewindRefusesARunningTurn(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	release := make(chan struct{})
	f := newFakeTool(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		<-release
		return answerHello(f, sessionID, text)
	})
	m, _ := newManager(t, store, f)
	defer close(release)
	it, err := m.Send(testAgent, "build it")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Rewind(testAgent, it.ID, "", ""); !errors.Is(err, ErrTurnRunning) {
		t.Fatalf("Rewind mid-turn = %v, want ErrTurnRunning", err)
	}
	if err := m.Rewind(testAgent, "nope", "", ""); err == nil {
		t.Fatal("Rewind to no turn worked")
	}
}

// TestSeedAndUpTo: a fork's conversation starts as a copy of its source's up
// to the turn, its first prompt telling the fresh session what that was.
func TestSeedAndUpTo(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	var mu sync.Mutex
	var prompts []string
	f := newFakeTool(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		mu.Lock()
		prompts = append(prompts, text)
		mu.Unlock()
		return answerHello(f, sessionID, text)
	})
	m, _ := newManager(t, store, f)
	var turns []string
	for i, text := range []string{"one", "two"} {
		it, err := m.Send(testAgent, text)
		if err != nil {
			t.Fatal(err)
		}
		turns = append(turns, it.ID)
		waitThread(t, m, testAgent, "the turn", turnsEnded(i+1))
	}
	items, err := m.UpTo(testAgent, turns[0])
	if err != nil || len(items) != 2 || items[0].Text != "one" {
		t.Fatalf("UpTo(turn 1) = %+v, %v", items, err)
	}
	if all, _ := m.UpTo(testAgent, ""); len(all) != 4 {
		t.Fatalf("UpTo(\"\") has %d items", len(all))
	}
	fork := state.Agent{Project: "hello", Name: "agent-02", AI: "claude", Worktree: "/work/fork"}
	handoff := Handoff(itemPointers(items), "You are a fork.")
	if err := m.Seed(fork, testAgent, items, handoff, "Forked from agent-01 at turn 1."); err != nil {
		t.Fatal(err)
	}
	if err := m.Seed(fork, testAgent, items, handoff, ""); err == nil {
		t.Error("seeded a conversation twice")
	}
	if _, err := m.Send(fork, "three"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, fork, "the fork's turn", turnsEnded(2))
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant", "notice", "user", "assistant"}) {
		t.Fatalf("fork's items %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	last := prompts[len(prompts)-1]
	if !strings.Contains(last, "You are a fork.") || !strings.Contains(last, "User: one") || strings.Contains(last, "User: two") {
		t.Fatalf("fork's first prompt %q", last)
	}
}

// TestHandoff: each turn says what the user asked, what was done and what was
// answered; past the budget the first turn stays and the latest follow.
func TestHandoff(t *testing.T) {
	t.Parallel()
	items := []*api.ChatItem{
		{ID: "u1", Kind: "user", Text: "fix the bug", Result: &api.ChatTurnResult{State: "completed"}},
		{ID: "t1", Kind: "tool", Tool: &api.ChatTool{Kind: "execute"}},
		{ID: "t2", Kind: "tool", Tool: &api.ChatTool{Kind: "execute"}},
		{ID: "t3", Kind: "tool", Tool: &api.ChatTool{Kind: "edit", Paths: []string{"a.go"}}},
		{ID: "t4", Kind: "tool", Tool: &api.ChatTool{Kind: "read"}},
		{ID: "a1", Kind: "assistant", Text: "Fixed."},
		{ID: "u2", Kind: "user", Text: "stop", Result: &api.ChatTurnResult{State: "cancelled"}},
	}
	got := Handoff(items, "Intro.")
	for _, want := range []string{"Intro.", "### Turn 1", "User: fix the bug", "You ran 2 commands, edited 1 file and read 1 file.", "You answered: Fixed.", "(This turn cancelled.)"} {
		if !strings.Contains(got, want) {
			t.Errorf("handoff lacks %q:\n%s", want, got)
		}
	}
	var long []*api.ChatItem
	for i := range 40 {
		long = append(long, &api.ChatItem{ID: "u", Kind: "user", Text: strings.Repeat("x", 3000) + string(rune('a'+i%26))})
	}
	got = Handoff(long, "")
	if len(got) > handoffBudget || !strings.Contains(got, "### Turn 1\n") || !strings.Contains(got, "### Turn 40\n") || !strings.Contains(got, "left out for length") {
		t.Errorf("long handoff is %d bytes:\n%.300s", len(got), got)
	}
}

func itemPointers(items []api.ChatItem) []*api.ChatItem {
	out := make([]*api.ChatItem, len(items))
	for i := range items {
		out[i] = &items[i]
	}
	return out
}
