package daemon

import (
	"context"
	"fmt"
	"testing"

	"agentbox/internal/api"
)

// The v2 sidebar's groups and hidden projects over the real routes: stored
// as sent where it makes sense, tidied where it doesn't, and a removed
// project leaves both.
func TestSidebarLayoutKeepsGroupsAndHidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	addProjects(t, d, "apples", "mangoes", "pears")

	got, err := d.client.SidebarLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Groups == nil || got.Hidden == nil || len(got.Groups)+len(got.Hidden) != 0 {
		t.Fatalf("a new installation's layout = %+v, want empty lists", got)
	}

	got, err = d.client.SetSidebarLayout(ctx, api.SidebarLayout{
		Groups: []api.SidebarGroup{
			{ID: "work", Name: " Work ", Projects: []string{"pears", "apples", "gone"}},
			// apples is in Work already, and an ID twice keeps the first.
			{ID: "", Name: "Play", Projects: []string{"apples", "mangoes"}},
			{ID: "work", Name: "Again", Projects: []string{}},
			{ID: "blank", Name: "  ", Projects: []string{}},
		},
		Hidden: []string{"mangoes", "mangoes", "gone"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 2 || got.Groups[1].ID == "" {
		t.Fatalf("groups = %+v, want Work and Play with an ID", got.Groups)
	}
	if s := fmt.Sprint(got.Groups[0].Name, got.Groups[0].Projects, got.Groups[1].Name, got.Groups[1].Projects, got.Hidden); s != "Work[pears apples]Play[mangoes] [mangoes]" {
		t.Errorf("stored layout = %s", s)
	}

	if err := d.client.RemoveProject(ctx, "mangoes"); err != nil {
		t.Fatal(err)
	}
	got, err = d.client.SidebarLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s := fmt.Sprint(got.Groups[1].Name, got.Groups[1].Projects, got.Hidden); s != "Play[] []" {
		t.Errorf("after removing mangoes = %s, want its group empty and nothing hidden", s)
	}
}
