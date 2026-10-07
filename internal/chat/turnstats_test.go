package chat

import (
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/state"
)

// TestTurnFinishedTellsWhatATurnSpent: every turn that ends tells the usage
// stats how it ended, how long it took and what it spent, its reasoning and
// subagents' tokens included, and nothing it said.
func TestTurnFinishedTellsWhatATurnSpent(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, sessionID, text string) acp.PromptResponse {
		res := answerHello(f, sessionID, text)
		res.Meta = &acp.PromptMeta{Quota: &acp.Quota{ModelUsage: []acp.ModelUsage{
			{Model: "claude-opus-5-5", TokenCount: acp.TokenCount{TotalTokens: 180, InputTokens: 100, CachedInputTokens: 50, CachedWriteTokens: 10, OutputTokens: 20, ReasoningOutputTokens: 5}},
			{Model: "claude-haiku-4-5", TokenCount: acp.TokenCount{TotalTokens: 12, InputTokens: 10, OutputTokens: 2, ReasoningOutputTokens: 1}},
		}}}
		return res
	})
	m, _ := newManager(t, store, f)
	var mu sync.Mutex
	var got []TurnStats
	m.TurnFinished = func(a state.Agent, st TurnStats) {
		mu.Lock()
		defer mu.Unlock()
		if a.Name == testAgent.Name {
			got = append(got, st)
		}
	}
	if _, err := m.Send(testAgent, "build the page"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("TurnFinished called %d times, want 1", len(got))
	}
	st := got[0]
	if st.State != "completed" || st.Input != 110 || st.Cached != 50 || st.CacheCreation != 10 || st.Output != 22 || st.Reasoning != 6 || st.Subagents {
		t.Errorf("stats = %+v", st)
	}
	if st.Duration <= 0 {
		t.Errorf("duration = %v", st.Duration)
	}
}

func TestReasoningTokensReadsEveryShape(t *testing.T) {
	t.Parallel()
	whole := acp.PromptResponse{Meta: &acp.PromptMeta{Quota: &acp.Quota{TokenCount: &acp.TokenCount{ReasoningOutputTokens: 7}}}}
	usage := acp.PromptResponse{Usage: &acp.Usage{ThoughtTokens: 3}}
	if reasoningTokens(whole) != 7 || reasoningTokens(usage) != 3 || reasoningTokens(acp.PromptResponse{}) != 0 {
		t.Errorf("reasoning = %d, %d", reasoningTokens(whole), reasoningTokens(usage))
	}
}
