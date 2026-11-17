package state_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"agentbox/internal/state"
)

// tabIDs is a project's open sessions in their tabs' order.
func tabIDs(t *testing.T, st *state.Store, project string) []string {
	t.Helper()
	open, err := st.LeadSessions(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	state.SortTabs(open)
	var ids []string
	for _, ls := range open {
		ids = append(ids, ls.ID)
	}
	return ids
}

// A chat's tabs are in the order they opened until dragged; pinned ones come
// first whatever the order; a tab opened or reopened after a drag goes last.
func TestLeadSessionTabsPinAndOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	addProject(t, st, "web")
	at := time.Now()
	for i, topic := range []string{"a", "b", "c"} {
		if _, err := st.AddLeadSession(ctx, "web", topic, at.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if got := tabIDs(t, st, "web"); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("before any drag = %v", got)
	}

	if err := st.PinLeadSession(ctx, "web", "c", true); err != nil {
		t.Fatal(err)
	}
	if got := tabIDs(t, st, "web"); !slices.Equal(got, []string{"c", "a", "b"}) {
		t.Errorf("c pinned = %v", got)
	}
	if err := st.PinLeadSession(ctx, "web", "nope", true); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("pinning a session that isn't there = %v", err)
	}

	// b dragged before a; c, left out, keeps its place after them, but is
	// still first while pinned.
	if err := st.OrderLeadSessions(ctx, "web", []string{"b", "a"}); err != nil {
		t.Fatal(err)
	}
	if got := tabIDs(t, st, "web"); !slices.Equal(got, []string{"c", "b", "a"}) {
		t.Errorf("after the drag = %v", got)
	}
	if err := st.PinLeadSession(ctx, "web", "c", false); err != nil {
		t.Fatal(err)
	}
	if got := tabIDs(t, st, "web"); !slices.Equal(got, []string{"b", "a", "c"}) {
		t.Errorf("c unpinned = %v", got)
	}

	// A new one goes last, as does one reopened.
	if _, err := st.AddLeadSession(ctx, "web", "d", at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseLeadSession(ctx, "web", "b", at); err != nil {
		t.Fatal(err)
	}
	if err := st.ReopenLeadSession(ctx, "web", "b"); err != nil {
		t.Fatal(err)
	}
	if got := tabIDs(t, st, "web"); !slices.Equal(got, []string{"a", "c", "d", "b"}) {
		t.Errorf("d opened and b reopened = %v", got)
	}

	// A closed session can't be ordered among the tabs.
	if err := st.CloseLeadSession(ctx, "web", "a", at); err != nil {
		t.Fatal(err)
	}
	if err := st.OrderLeadSessions(ctx, "web", []string{"a", "b"}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ordering a closed session = %v", err)
	}
	if got, err := st.LeadSession(ctx, "web", "a"); err != nil || got.Pinned {
		t.Errorf("a = %+v, %v", got, err)
	}
}
