package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentbox/internal/state"
)

// TestAgentTokenRoundTrip checks that a token issued for an agent resolves to
// that agent, that an unknown one is refused, and that removing it takes it
// away: a pod's token is the only thing standing between it and the gateway.
func TestAgentTokenRoundTrip(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	token := "token-for-pawly-agent-01-0123456789abcdef"

	if err := st.SetAgentToken(ctx, "pawly", "agent-01", token, time.Time{}); err != nil {
		t.Fatal(err)
	}
	project, agent, err := st.AgentForToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if project != "pawly" || agent != "agent-01" {
		t.Errorf("AgentForToken() = %s/%s, want pawly/agent-01", project, agent)
	}
	if _, _, err := st.AgentForToken(ctx, "someone-elses-token-0123456789abcdef"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("AgentForToken(unknown) = %v, want ErrNotFound", err)
	}
	if n := len(state.AgentTokenHash(token)); n != 64 {
		t.Errorf("AgentTokenHash() is %d characters, want 64 for a SHA-256 hex", n)
	}
	if state.AgentTokenHash(token) == token {
		t.Error("AgentTokenHash() returned the token itself")
	}

	if err := st.RemoveAgentToken(ctx, "pawly", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AgentForToken(ctx, token); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a removed token still works: %v", err)
	}
	if err := st.RemoveAgentToken(ctx, "pawly", "agent-01"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("RemoveAgentToken() twice = %v, want ErrNotFound", err)
	}
}

// TestAgentTokenReplace checks that issuing a second token for an agent
// retires the first: a pod whose Secret was replaced must not keep opening the
// gateway with the token the user rotated away.
func TestAgentTokenReplace(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	oldToken := "old-token-for-pawly-agent-01-0123456789"
	newToken := "new-token-for-pawly-agent-01-0123456789"

	if err := st.SetAgentToken(ctx, "pawly", "agent-01", oldToken, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentToken(ctx, "pawly", "agent-01", newToken, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AgentForToken(ctx, oldToken); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the replaced token still works: %v", err)
	}
	project, agent, err := st.AgentForToken(ctx, newToken)
	if err != nil || project != "pawly" || agent != "agent-01" {
		t.Fatalf("AgentForToken(new) = %s/%s, %v; want pawly/agent-01", project, agent, err)
	}
}

// TestAgentTokenExpiry checks both ends of expires_at: a deadline in the
// future is honoured, one that has passed is refused while its row waits for
// the sweep, and zero never expires because the pod's lifetime decides.
func TestAgentTokenExpiry(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now()
	expired := "expired-token-for-pawly-0123456789abcdef"
	live := "live-token-for-pawly-0123456789abcdef"
	pod := "pod-token-for-pawly-0123456789abcdef"

	if err := st.SetAgentToken(ctx, "pawly", "agent-01", expired, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentToken(ctx, "pawly", "agent-02", live, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentToken(ctx, "pawly", "agent-03", pod, time.Time{}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := st.AgentForToken(ctx, expired); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("an expired token resolved: %v", err)
	}
	if _, _, err := st.AgentForToken(ctx, live); err != nil {
		t.Errorf("a token with an hour left was refused: %v", err)
	}
	if _, _, err := st.AgentForToken(ctx, pod); err != nil {
		t.Errorf("a token with no deadline was refused: %v", err)
	}
}

// TestAgentTokenPrune checks that the sweep removes only what has a deadline
// that has passed: a pod's token has none, so it is the reconciler's row to
// delete, and one still in the future is still in use.
func TestAgentTokenPrune(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now()
	soon := "soon-token-for-pawly-0123456789abcdef"
	later := "later-token-for-pawly-0123456789abcdef"
	pod := "pod-token-for-pawly-0123456789abcdef"

	if err := st.SetAgentToken(ctx, "pawly", "agent-01", soon, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentToken(ctx, "pawly", "agent-02", later, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentToken(ctx, "pawly", "agent-03", pod, time.Time{}); err != nil {
		t.Fatal(err)
	}

	if n, err := st.PruneAgentTokens(ctx, now); err != nil || n != 0 {
		t.Fatalf("PruneAgentTokens(before any deadline) = %d, %v; want 0", n, err)
	}
	if n, err := st.PruneAgentTokens(ctx, now.Add(2*time.Minute)); err != nil || n != 1 {
		t.Fatalf("PruneAgentTokens(two minutes on) = %d, %v; want 1", n, err)
	}
	if _, _, err := st.AgentForToken(ctx, soon); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the pruned token still works: %v", err)
	}
	if _, _, err := st.AgentForToken(ctx, later); err != nil {
		t.Errorf("pruning took a token with an hour left: %v", err)
	}
	if _, _, err := st.AgentForToken(ctx, pod); err != nil {
		t.Errorf("pruning took a token with no deadline: %v", err)
	}
}

// TestAgentTokenWithoutAgentRow checks that a token is stored even when no
// agents row exists yet: a pod can be made before the row is read, which is
// why this table deliberately has no foreign key to agents.
func TestAgentTokenWithoutAgentRow(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	token := "pod-first-token-for-pawly-0123456789abcdef"

	if err := st.SetAgentToken(ctx, "pawly", "agent-01", token, time.Time{}); err != nil {
		t.Fatalf("SetAgentToken() without a project or agent row = %v", err)
	}
	project, agent, err := st.AgentForToken(ctx, token)
	if err != nil || project != "pawly" || agent != "agent-01" {
		t.Errorf("AgentForToken() = %s/%s, %v; want pawly/agent-01", project, agent, err)
	}
}
