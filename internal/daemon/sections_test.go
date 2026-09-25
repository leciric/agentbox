package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// addProjects adds one project per name, each its own repository, and returns
// the daemon they are in.
func addProjects(t *testing.T, d testDaemon, names ...string) {
	t.Helper()
	for _, name := range names {
		repo := d.fixtureRepo(t, "hello-stack")
		if _, err := d.client.AddProject(context.Background(), api.AddProjectRequest{Path: repo, Name: name}); err != nil {
			t.Fatal(err)
		}
	}
}

func projectOrder(t *testing.T, d testDaemon) string {
	t.Helper()
	projects, err := d.client.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sections, err := d.client.Sections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	name := map[string]string{"": "-"}
	for _, sec := range sections {
		name[sec.ID] = sec.Name
	}
	var out []string
	for _, p := range projects {
		out = append(out, name[p.Section]+"/"+p.Name)
	}
	return strings.Join(out, " ")
}

// The whole of the feature over the real routes: make a section, move
// projects into it, reorder both, collapse it, and delete it without losing a
// project.
func TestSectionsOrganiseTheProjectsList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	addProjects(t, d, "apples", "mangoes", "pears")

	// Before anything is organised, the list is what it always was.
	if got, want := projectOrder(t, d), "-/apples -/mangoes -/pears"; got != want {
		t.Fatalf("with no sections: %q, want %q", got, want)
	}

	work, err := d.client.AddSection(ctx, "Work")
	if err != nil {
		t.Fatal(err)
	}
	play, err := d.client.AddSection(ctx, "Play")
	if err != nil {
		t.Fatal(err)
	}
	// An empty section exists and is allowed to: nothing is in Play yet.
	if got, want := projectOrder(t, d), "-/apples -/mangoes -/pears"; got != want {
		t.Errorf("two empty sections changed the list: %q, want %q", got, want)
	}

	// Two projects into Work, one left loose.
	projects, err := d.client.SetProjectLayout(ctx, api.ProjectLayout{
		Sections: []api.SectionProjects{
			{ID: work.ID, Projects: []string{"pears", "apples"}},
			{ID: play.ID, Projects: []string{}},
		},
		Loose: []string{"mangoes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 || projects[0].Name != "pears" || projects[0].Section != work.ID {
		t.Errorf("the layout answered with %+v, want the projects in their new order", projects)
	}
	if got, want := projectOrder(t, d), "Work/pears Work/apples -/mangoes"; got != want {
		t.Errorf("after moving two into Work: %q, want %q", got, want)
	}

	// Sections reorder among themselves, and the projects in them come along.
	if _, err := d.client.SetProjectLayout(ctx, api.ProjectLayout{
		Sections: []api.SectionProjects{
			{ID: play.ID, Projects: []string{"mangoes"}},
			{ID: work.ID, Projects: []string{"apples", "pears"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := projectOrder(t, d), "Play/mangoes Work/apples Work/pears"; got != want {
		t.Errorf("after reordering both: %q, want %q", got, want)
	}

	// Collapsing is remembered by the daemon, not by the window that did it.
	collapsed := true
	if _, err := d.client.UpdateSection(ctx, work.ID, api.UpdateSectionRequest{Collapsed: &collapsed}); err != nil {
		t.Fatal(err)
	}
	// So is a rename, which touches the section and no project in it.
	renamed := "Side projects"
	if _, err := d.client.UpdateSection(ctx, work.ID, api.UpdateSectionRequest{Name: &renamed}); err != nil {
		t.Fatal(err)
	}
	sections, err := d.client.Sections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 2 || sections[0].ID != play.ID || sections[1].Name != renamed || !sections[1].Collapsed {
		t.Errorf("Sections() = %+v, want Play first and a renamed, collapsed Work", sections)
	}
	if got, want := projectOrder(t, d), "Play/mangoes Side projects/apples Side projects/pears"; got != want {
		t.Errorf("a rename moved something: %q, want %q", got, want)
	}

	// Deleting a section keeps its projects: they go back to being loose.
	if err := d.client.RemoveSection(ctx, work.ID); err != nil {
		t.Fatal(err)
	}
	if got, want := projectOrder(t, d), "Play/mangoes -/apples -/pears"; got != want {
		t.Errorf("after deleting a section: %q, want %q", got, want)
	}
	if sections, err = d.client.Sections(ctx); err != nil || len(sections) != 1 {
		t.Errorf("Sections() = %+v, %v, want only Play", sections, err)
	}
}

// A project added while the sidebar was being dragged is neither lost nor
// allowed to corrupt the order.
func TestProjectLayoutToleratesAProjectAddedMeanwhile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	addProjects(t, d, "apples", "mangoes")
	sec, err := d.client.AddSection(ctx, "Work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetProjectLayout(ctx, api.ProjectLayout{
		Sections: []api.SectionProjects{{ID: sec.ID, Projects: []string{"mangoes", "apples"}}},
	}); err != nil {
		t.Fatal(err)
	}

	addProjects(t, d, "pears")
	// A layout drawn before pears existed, sent after it did.
	if _, err := d.client.SetProjectLayout(ctx, api.ProjectLayout{
		Sections: []api.SectionProjects{{ID: sec.ID, Projects: []string{"apples", "mangoes"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := projectOrder(t, d), "Work/apples Work/mangoes -/pears"; got != want {
		t.Errorf("after a stale layout: %q, want %q", got, want)
	}
	projects, err := d.client.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range projects {
		key := p.Section + "#" + strings.Repeat("x", p.Position)
		if seen[key] {
			t.Errorf("two projects share a position: %+v", projects)
		}
		seen[key] = true
		if p.Position == 0 {
			t.Errorf("%s has no position after a layout: %+v", p.Name, p)
		}
	}
}

func TestSectionRoutesRefuseNonsense(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if _, err := d.client.AddSection(ctx, "  "); err == nil {
		t.Error("a section with no name was accepted")
	}
	if err := d.client.RemoveSection(ctx, "sec_nope"); err == nil {
		t.Error("deleting an unknown section was accepted")
	}
	if _, err := d.client.SetProjectLayout(ctx, api.ProjectLayout{
		Sections: []api.SectionProjects{{ID: "sec_nope"}},
	}); err == nil {
		t.Error("a layout naming an unknown section was accepted")
	}
}

// Two writes at once — a reorder and a section folding away — are what the
// app itself sends when a project is moved into a collapsed section, and what
// two windows send when they are both being used. Both must land: a
// transaction that reads before it writes is where SQLite would otherwise
// fail one of them outright rather than make it wait (see Open's _txlock).
func TestConcurrentLayoutAndSectionWritesAllLand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	addProjects(t, d, "apples", "mangoes", "pears")
	work, err := d.client.AddSection(ctx, "Work")
	if err != nil {
		t.Fatal(err)
	}
	play, err := d.client.AddSection(ctx, "Play")
	if err != nil {
		t.Fatal(err)
	}

	collapsed := true
	errs := make(chan error, 3)
	go func() {
		_, err := d.client.SetProjectLayout(ctx, api.ProjectLayout{
			Sections: []api.SectionProjects{
				{ID: work.ID, Projects: []string{"pears", "apples"}},
				{ID: play.ID, Projects: []string{"mangoes"}},
			},
		})
		errs <- err
	}()
	go func() {
		_, err := d.client.UpdateSection(ctx, work.ID, api.UpdateSectionRequest{Collapsed: &collapsed})
		errs <- err
	}()
	go func() {
		_, err := d.client.AddSection(ctx, "Later")
		errs <- err
	}()
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatalf("one of three writes at once failed: %v", err)
		}
	}

	if got, want := projectOrder(t, d), "Work/pears Work/apples Play/mangoes"; got != want {
		t.Errorf("after three writes at once: %q, want %q", got, want)
	}
	sections, err := d.client.Sections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 3 {
		t.Fatalf("Sections() = %+v, want three", sections)
	}
	for i, sec := range sections {
		if sec.Position != i+1 {
			t.Errorf("section %q is at position %d, want %d: positions must stay 1..n", sec.Name, sec.Position, i+1)
		}
		if sec.ID == work.ID && !sec.Collapsed {
			t.Error("the fold was lost to the reorder")
		}
	}
}
