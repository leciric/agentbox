package state_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"agentbox/internal/state"
)

// TestUserPresence counts what the user typed to a lead and the verdicts they
// gave, and not what a lead or an AI tool wrote in their name.
func TestUserPresence(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	at := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	item := func(id string, minutes int, extra string) state.ChatItem {
		return state.ChatItem{ID: id, Data: fmt.Appendf(nil, `{"id":%q,"kind":"user","createdAt":%q%s}`, id, at.Add(time.Duration(minutes)*time.Minute).Format(time.RFC3339Nano), extra)}
	}
	if err := st.AddProject(ctx, state.Project{Name: "acme", Root: "/src/acme", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChatItems(ctx, "acme", state.LeadName, []state.ChatItem{
		item("typed", 0, ""),
		item("woken", 1, `,"woken":true`),
		item("hidden", 2, `,"hidden":true`),
		item("other-lead", 3, `,"projectMessage":{"project":"zeta"}`),
		item("old", -600, ""),
	}); err != nil {
		t.Fatal(err)
	}
	// Home's chat is a lead too, though no project.
	if err := st.SaveChatItems(ctx, state.HomeProject, state.LeadName, []state.ChatItem{item("home", 30, "")}); err != nil {
		t.Fatal(err)
	}
	// What a lead tells a worker looks the same as the user's message.
	if err := st.SaveChatItems(ctx, "acme", "agent-1", []state.ChatItem{item("told", 5, "")}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentReview(ctx, state.AgentReview{Project: "acme", Agent: "agent-1", Verdict: "reviewed", At: at.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	got, err := st.UserPresence(ctx, at.Add(-time.Hour), at.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(got, time.Time.Compare)
	if len(got) != 3 || !got[0].Equal(at) || !got[1].Equal(at.Add(30*time.Minute)) || !got[2].Equal(at.Add(time.Hour)) {
		t.Fatalf("presence = %v, want the typed message, Home's and the verdict", got)
	}
}
