package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/state"
)

func openStore(t *testing.T) *state.Store {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestSecretScopes checks the one thing this layer decides: a project's secret
// and an agent's own secret of the same name are two rows, and neither listing
// shows the other's.
func TestSecretScopes(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	for _, sec := range []state.Secret{
		{Project: "pawly", Name: "OPENAI_API_KEY", Value: []byte("sealed-project"), UpdatedAt: now},
		{Project: "pawly", Agent: "agent-01", Name: "OPENAI_API_KEY", Value: []byte("sealed-agent"), UpdatedAt: now},
		{Project: "pawly", Agent: "agent-01", Name: "STRIPE_SECRET_KEY", Value: []byte("sealed-stripe"), UpdatedAt: now},
		{Project: "other", Name: "OPENAI_API_KEY", Value: []byte("sealed-other"), UpdatedAt: now},
	} {
		if err := st.SetSecret(ctx, sec); err != nil {
			t.Fatal(err)
		}
	}

	scoped, err := st.Secrets(ctx, "pawly", "")
	if err != nil || len(scoped) != 1 || scoped[0].Name != "OPENAI_API_KEY" {
		t.Fatalf("Secrets(pawly, project scope) = %+v, %v; want only the project's own", scoped, err)
	}
	if scoped[0].Scope() != state.ScopeProject {
		t.Errorf("a secret with no agent has scope %q", scoped[0].Scope())
	}
	if string(scoped[0].Value) != "sealed-project" {
		t.Errorf("the project's row holds the agent's value: %q", scoped[0].Value)
	}
	own, err := st.Secrets(ctx, "pawly", "agent-01")
	if err != nil || len(own) != 2 {
		t.Fatalf("Secrets(pawly/agent-01) = %+v, %v; want its two", own, err)
	}
	if own[0].Scope() != state.ScopeAgent {
		t.Errorf("a secret with an agent has scope %q", own[0].Scope())
	}
	// Project ones first, so a view of the whole project reads top-down.
	all, err := st.ProjectSecrets(ctx, "pawly")
	if err != nil || len(all) != 3 || all[0].Agent != "" {
		t.Fatalf("ProjectSecrets(pawly) = %+v, %v", all, err)
	}
	if other, _ := st.Secrets(ctx, "other", ""); len(other) != 1 || string(other[0].Value) != "sealed-other" {
		t.Errorf("another project's secret of the same name leaked: %+v", other)
	}
}

// TestSetSecretReplaces checks that setting a secret again replaces the value
// and moves the time, rather than failing on the key or keeping both.
func TestSetSecretReplaces(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	first := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := st.SetSecret(ctx, state.Secret{Project: "pawly", Name: "TOKEN", Value: []byte("one"), UpdatedAt: first}); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Truncate(time.Second)
	if err := st.SetSecret(ctx, state.Secret{Project: "pawly", Name: "TOKEN", Value: []byte("two"), UpdatedAt: later}); err != nil {
		t.Fatal(err)
	}
	secrets, err := st.Secrets(ctx, "pawly", "")
	if err != nil || len(secrets) != 1 {
		t.Fatalf("Secrets() = %+v, %v; want one row", secrets, err)
	}
	if string(secrets[0].Value) != "two" || !secrets[0].UpdatedAt.Equal(later) {
		t.Errorf("Secrets() = %+v; want the second value and its time", secrets[0])
	}
}

func TestRemoveSecret(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if err := st.SetSecret(ctx, state.Secret{Project: "pawly", Name: "TOKEN", Value: []byte("x"), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveSecret(ctx, "pawly", "", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Secret(ctx, "pawly", "", "TOKEN"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Secret() after removal = %v, want ErrNotFound", err)
	}
	// Removing what isn't there says so, so the API can answer 404 rather
	// than pretending it did something.
	if err := st.RemoveSecret(ctx, "pawly", "", "TOKEN"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("RemoveSecret() twice = %v, want ErrNotFound", err)
	}
}

// TestSecretsGoWithTheirOwner checks the cleanup that keeps a destroyed agent's
// keys from outliving it, and a removed project's from outliving it: they are
// the one thing an agent held that isn't on its branch.
func TestSecretsGoWithTheirOwner(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{Project: "pawly", Name: "agent-01", Instance: "ab-pawly-agent-01", AI: "none",
		Branch: "agentbox/agent-01", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now()}
	if err := st.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	for _, sec := range []state.Secret{
		{Project: "pawly", Name: "SHARED", Value: []byte("x"), UpdatedAt: time.Now()},
		{Project: "pawly", Agent: "agent-01", Name: "MINE", Value: []byte("y"), UpdatedAt: time.Now()},
	} {
		if err := st.SetSecret(ctx, sec); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.RemoveAgent(ctx, "pawly", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if own, _ := st.Secrets(ctx, "pawly", "agent-01"); len(own) != 0 {
		t.Errorf("a destroyed agent's secrets are still stored: %+v", own)
	}
	if scoped, _ := st.Secrets(ctx, "pawly", ""); len(scoped) != 1 {
		t.Errorf("destroying an agent took its project's secrets: %+v", scoped)
	}

	if err := st.RemoveProject(ctx, "pawly"); err != nil {
		t.Fatal(err)
	}
	if all, _ := st.ProjectSecrets(ctx, "pawly"); len(all) != 0 {
		t.Errorf("a removed project's secrets are still stored: %+v", all)
	}
}
