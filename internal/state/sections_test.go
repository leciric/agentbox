package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/state"
)

func addProject(t *testing.T, st *state.Store, name string) {
	t.Helper()
	if err := st.AddProject(context.Background(), state.Project{Name: name, Root: "/src/" + name, CreatedAt: time.Unix(1700000000, 0)}); err != nil {
		t.Fatal(err)
	}
}

func projectNames(t *testing.T, st *state.Store) []string {
	t.Helper()
	projects, err := st.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range projects {
		names = append(names, p.Name)
	}
	return names
}

// A project's place, as "section/name@position", which is what every ordering
// assertion below is really about.
func places(t *testing.T, st *state.Store) []string {
	t.Helper()
	projects, err := st.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sections, err := st.Sections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	name := map[string]string{"": "-"}
	for _, sec := range sections {
		name[sec.ID] = sec.Name
	}
	var out []string
	for _, p := range projects {
		out = append(out, name[p.Section]+"/"+p.Name+"@"+strconv.Itoa(p.Position))
	}
	return out
}

// An installation that has organised nothing reads exactly as it did before
// sections existed: alphabetical, every project in no section.
func TestProjectsWithoutSectionsAreAlphabetical(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	for _, name := range []string{"zebra", "apples", "mangoes"} {
		addProject(t, st, name)
	}
	if got, want := strings.Join(projectNames(t, st), " "), "apples mangoes zebra"; got != want {
		t.Errorf("Projects() = %q, want %q", got, want)
	}
	projects, err := st.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if p.Section != "" || p.Position != 0 {
			t.Errorf("%s starts at %q/%d, want no section and no position", p.Name, p.Section, p.Position)
		}
	}
}

// A layout is the whole order at once: sections first in theirs, then the
// projects in none, and every position 1..n.
func TestSetProjectLayoutOrdersEverything(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	for _, name := range []string{"apples", "mangoes", "pears", "zebra"} {
		addProject(t, st, name)
	}
	work, err := st.AddSection(ctx, "Work", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	play, err := st.AddSection(ctx, "Play", time.Unix(1700000001, 0))
	if err != nil {
		t.Fatal(err)
	}
	if work.Position != 1 || play.Position != 2 {
		t.Errorf("new sections are at %d and %d, want 1 and 2", work.Position, play.Position)
	}

	// Play above Work, two projects in each order, one left loose.
	layout := state.Layout{
		Sections: []state.SectionProjects{
			{ID: play.ID, Projects: []string{"zebra"}},
			{ID: work.ID, Projects: []string{"pears", "apples"}},
		},
		Loose: []string{"mangoes"},
	}
	if err := st.SetProjectLayout(ctx, layout); err != nil {
		t.Fatal(err)
	}
	want := "Play/zebra@1 Work/pears@1 Work/apples@2 -/mangoes@1"
	if got := strings.Join(places(t, st), " "); got != want {
		t.Errorf("after the layout: %q, want %q", got, want)
	}

	// Applying it again changes nothing: a layout is idempotent.
	if err := st.SetProjectLayout(ctx, layout); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(places(t, st), " "); got != want {
		t.Errorf("applying the same layout twice: %q, want %q", got, want)
	}

	// A section the layout doesn't name keeps its place after the ones it
	// does, and a project it doesn't name stays in its list, at the end.
	addProject(t, st, "quinces")
	if err := st.SetProjectLayout(ctx, state.Layout{
		Sections: []state.SectionProjects{{ID: work.ID, Projects: []string{"apples"}}},
	}); err != nil {
		t.Fatal(err)
	}
	want = "Work/apples@1 Work/pears@2 Play/zebra@1 -/mangoes@1 -/quinces@2"
	if got := strings.Join(places(t, st), " "); got != want {
		t.Errorf("after a partial layout: %q, want %q", got, want)
	}
}

// A layout drawn before a project was removed still applies, and leaves no
// hole where the project was.
func TestSetProjectLayoutSurvivesAProjectRemovedMeanwhile(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	for _, name := range []string{"apples", "mangoes", "pears"} {
		addProject(t, st, name)
	}
	sec, err := st.AddSection(ctx, "Work", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectLayout(ctx, state.Layout{
		Sections: []state.SectionProjects{{ID: sec.ID, Projects: []string{"apples", "mangoes", "pears"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveProject(ctx, "mangoes"); err != nil {
		t.Fatal(err)
	}
	// Removing closed the hole up by itself.
	if got, want := strings.Join(places(t, st), " "), "Work/apples@1 Work/pears@2"; got != want {
		t.Errorf("after a removal: %q, want %q", got, want)
	}
	// And a layout still naming it applies to what is left.
	if err := st.SetProjectLayout(ctx, state.Layout{
		Sections: []state.SectionProjects{{ID: sec.ID, Projects: []string{"pears", "mangoes", "apples"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(places(t, st), " "), "Work/pears@1 Work/apples@2"; got != want {
		t.Errorf("after a layout naming a removed project: %q, want %q", got, want)
	}
}

func TestSetProjectLayoutRefusesNonsense(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	addProject(t, st, "apples")
	sec, err := st.AddSection(ctx, "Work", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectLayout(ctx, state.Layout{
		Sections: []state.SectionProjects{{ID: "sec_nope", Projects: []string{"apples"}}},
	}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a layout naming an unknown section: %v, want ErrNotFound", err)
	}
	if err := st.SetProjectLayout(ctx, state.Layout{
		Sections: []state.SectionProjects{{ID: sec.ID, Projects: []string{"apples"}}},
		Loose:    []string{"apples"},
	}); err == nil {
		t.Error("a layout with the same project in two lists was accepted")
	}
	// A refused layout leaves the order alone.
	if got, want := strings.Join(places(t, st), " "), "-/apples@0"; got != want {
		t.Errorf("after two refusals: %q, want %q", got, want)
	}
}

// Deleting a section keeps every project that was in it: they go back to
// being in no section, and nothing about them is deleted.
func TestRemoveSectionKeepsItsProjects(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	for _, name := range []string{"apples", "mangoes", "pears"} {
		addProject(t, st, name)
	}
	work, err := st.AddSection(ctx, "Work", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	play, err := st.AddSection(ctx, "Play", time.Unix(1700000001, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectLayout(ctx, state.Layout{
		Sections: []state.SectionProjects{
			{ID: work.ID, Projects: []string{"apples", "mangoes"}},
			{ID: play.ID, Projects: []string{"pears"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveSection(ctx, work.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Section(ctx, work.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the section is still there: %v", err)
	}
	// Both projects survive, in no section, numbered from 1; and the section
	// that is left has closed up to position 1.
	if got, want := strings.Join(places(t, st), " "), "Play/pears@1 -/apples@1 -/mangoes@2"; got != want {
		t.Errorf("after deleting a section: %q, want %q", got, want)
	}
	sections, err := st.Sections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 1 || sections[0].Position != 1 {
		t.Errorf("Sections() = %+v, want one section at position 1", sections)
	}
	if err := st.RemoveSection(ctx, work.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("deleting it twice: %v, want ErrNotFound", err)
	}
}

func TestSectionNamesAndCollapsing(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if _, err := st.AddSection(ctx, "   ", time.Now()); err == nil {
		t.Error("a section with no name was accepted")
	}
	if _, err := st.AddSection(ctx, strings.Repeat("x", state.MaxSectionNameLen+1), time.Now()); err == nil {
		t.Error("an over-long section name was accepted")
	}
	sec, err := st.AddSection(ctx, "  Work  ", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sec.Name != "Work" {
		t.Errorf("AddSection kept the padding: %q", sec.Name)
	}
	collapsed := true
	if sec, err = st.UpdateSection(ctx, sec.ID, nil, &collapsed); err != nil {
		t.Fatal(err)
	}
	if !sec.Collapsed || sec.Name != "Work" {
		t.Errorf("collapsing changed something else: %+v", sec)
	}
	renamed := "Side projects"
	if sec, err = st.UpdateSection(ctx, sec.ID, &renamed, nil); err != nil {
		t.Fatal(err)
	}
	if sec.Name != renamed || !sec.Collapsed {
		t.Errorf("renaming unfolded it: %+v", sec)
	}
	// Renaming is one write, whatever is in the section: the read-back agrees.
	stored, err := st.Section(ctx, sec.ID)
	if err != nil || stored != sec {
		t.Errorf("Section() = %+v, %v, want %+v", stored, err, sec)
	}
	if _, err := st.UpdateSection(ctx, "sec_nope", &renamed, nil); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("renaming an unknown section: %v, want ErrNotFound", err)
	}
}
