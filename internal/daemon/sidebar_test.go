package daemon

import (
	"context"
	"fmt"
	"testing"

	"agentbox/internal/api"
)

// The v2 sidebar's hidden projects over the real routes: stored once each,
// unknown names dropped, and a removed project leaves the list.
func TestSidebarLayoutKeepsHidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	addProjects(t, d, "apples", "mangoes", "pears")

	got, err := d.client.SidebarLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hidden == nil || len(got.Hidden) != 0 {
		t.Fatalf("a new installation's layout = %+v, want an empty list", got)
	}

	got, err = d.client.SetSidebarLayout(ctx, api.SidebarLayout{Hidden: []string{"mangoes", "pears", "mangoes", "gone"}})
	if err != nil {
		t.Fatal(err)
	}
	if s := fmt.Sprint(got.Hidden); s != "[mangoes pears]" {
		t.Errorf("stored hidden = %s, want [mangoes pears]", s)
	}

	if err := d.client.RemoveProject(ctx, "mangoes"); err != nil {
		t.Fatal(err)
	}
	got, err = d.client.SidebarLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s := fmt.Sprint(got.Hidden); s != "[pears]" {
		t.Errorf("after removing mangoes = %s, want [pears]", s)
	}
}
