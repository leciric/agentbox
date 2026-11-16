package state_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"agentbox/internal/state"
)

// groupOf is each open session's group, by id.
func groupOf(t *testing.T, st *state.Store, project string) map[string]string {
	t.Helper()
	open, err := st.LeadSessions(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, ls := range open {
		out[ls.ID] = ls.Group
	}
	return out
}

func groupIDs(t *testing.T, st *state.Store, project string) []string {
	t.Helper()
	gs, err := st.SessionGroups(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, g := range gs {
		ids = append(ids, g.ID)
	}
	return ids
}

// Tab groups: made of sessions, sorted after the pinned tabs and before the
// loose ones, renamed, recoloured, folded, reordered and ungrouped; pinned
// and closed sessions can't join, pinning leaves one, and a group no session
// is in goes.
func TestSessionGroups(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	at := time.Now()
	for i, topic := range []string{"a", "b", "c", "d", "e"} {
		if _, err := st.AddLeadSession(ctx, "web", topic, at.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PinLeadSession(ctx, "web", "a", true); err != nil {
		t.Fatal(err)
	}

	if _, err := st.AddSessionGroup(ctx, "web", "x", "", nil); err == nil {
		t.Error("a group of no sessions was made")
	}
	if _, err := st.AddSessionGroup(ctx, "web", "x", "", []string{"a"}); err == nil {
		t.Error("a pinned session joined a group")
	}
	if _, err := st.AddSessionGroup(ctx, "web", "x", "teal", []string{"b"}); err == nil {
		t.Error("a group took a colour that isn't one")
	}
	if got := groupIDs(t, st, "web"); len(got) != 0 {
		t.Fatalf("failed adds left groups: %v", got)
	}

	ui, err := st.AddSessionGroup(ctx, "web", "  Desktop   UI ", "sky", []string{"d", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if ui.Name != "Desktop UI" || ui.Color != "sky" || !ui.Collapsed || ui.Position != 1 {
		t.Errorf("group = %+v", ui)
	}
	docs, err := st.AddSessionGroup(ctx, "web", "", "", []string{"e"})
	if err != nil {
		t.Fatal(err)
	}
	if docs.Position != 2 || docs.Color == "sky" || docs.Color == "" {
		t.Errorf("second group = %+v, wants position 2 and a colour of its own", docs)
	}
	// Pinned, then ui's (as they opened, none ever dragged), docs', then
	// loose b.
	if got := tabIDs(t, st, "web"); !slices.Equal(got, []string{"a", "c", "d", "e", "b"}) {
		t.Errorf("tabs = %v", got)
	}

	if err := st.OrderSessionGroups(ctx, "web", []string{docs.ID}); err != nil {
		t.Fatal(err)
	}
	if got := tabIDs(t, st, "web"); !slices.Equal(got, []string{"a", "e", "c", "d", "b"}) {
		t.Errorf("tabs, docs first = %v", got)
	}
	if err := st.OrderSessionGroups(ctx, "web", []string{"nope"}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ordering a group that isn't there = %v", err)
	}

	name, color, folded := "Docs", "mint", false
	g, err := st.UpdateSessionGroup(ctx, "web", docs.ID, state.SessionGroupPatch{Name: &name, Color: &color, Collapsed: &folded})
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "Docs" || g.Color != "mint" || g.Collapsed {
		t.Errorf("updated = %+v", g)
	}
	bad := "plaid"
	if _, err := st.UpdateSessionGroup(ctx, "web", docs.ID, state.SessionGroupPatch{Color: &bad}); err == nil {
		t.Error("recoloured to a colour that isn't one")
	}
	if _, err := st.UpdateSessionGroup(ctx, "web", "nope", state.SessionGroupPatch{Name: &name}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("updating a group that isn't there = %v", err)
	}

	// b joins ui, last; pinned a can't; moving e out empties docs, which goes.
	if err := st.SetLeadSessionGroup(ctx, "web", "b", ui.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLeadSessionGroup(ctx, "web", "a", ui.ID); err == nil {
		t.Error("a pinned session joined a group")
	}
	if err := st.SetLeadSessionGroup(ctx, "web", "b", "nope"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("joining a group that isn't there = %v", err)
	}
	if err := st.SetLeadSessionGroup(ctx, "web", "e", ""); err != nil {
		t.Fatal(err)
	}
	if got := groupIDs(t, st, "web"); !slices.Equal(got, []string{ui.ID}) {
		t.Errorf("groups after docs emptied = %v", got)
	}

	// Pinning takes c out of its group.
	if err := st.PinLeadSession(ctx, "web", "c", true); err != nil {
		t.Fatal(err)
	}
	if got := groupOf(t, st, "web"); got["c"] != "" || got["d"] != ui.ID || got["b"] != ui.ID {
		t.Errorf("groups after c pinned = %v", got)
	}

	// A closed session keeps its group, hidden while all of it is closed;
	// deleting the last one in it takes the group.
	for _, id := range []string{"d", "b"} {
		if err := st.CloseLeadSession(ctx, "web", id, at); err != nil {
			t.Fatal(err)
		}
	}
	if got := groupIDs(t, st, "web"); !slices.Equal(got, []string{ui.ID}) {
		t.Errorf("group of closed sessions = %v, wants it kept", got)
	}
	if err := st.ReopenLeadSession(ctx, "web", "d"); err != nil {
		t.Fatal(err)
	}
	if got := groupOf(t, st, "web"); got["d"] != ui.ID {
		t.Errorf("reopened d's group = %q", got["d"])
	}
	if _, err := st.DeleteLeadSession(ctx, "web", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteLeadSession(ctx, "web", "d"); err != nil {
		t.Fatal(err)
	}
	if got := groupIDs(t, st, "web"); len(got) != 0 {
		t.Errorf("groups with no sessions = %v", got)
	}

	// Ungrouping leaves its sessions loose.
	g2, err := st.AddSessionGroup(ctx, "web", "two", "", []string{"e"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSessionGroup(ctx, "web", g2.ID); err != nil {
		t.Fatal(err)
	}
	if got := groupOf(t, st, "web"); got["e"] != "" {
		t.Errorf("e after ungrouping = %q", got["e"])
	}
	if err := st.DeleteSessionGroup(ctx, "web", g2.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ungrouping twice = %v", err)
	}
}
