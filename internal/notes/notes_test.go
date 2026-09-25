package notes_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/notes"
)

var on = time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)

func TestAppend(t *testing.T) {
	cases := map[string]struct{ current, entry, want string }{
		"empty notes get the heading": {
			current: "",
			entry:   "the e2e tests need FOO=1",
			want:    "## From the lead\n\n- 2026-09-18: the e2e tests need FOO=1\n",
		},
		"notes without the heading keep what the user wrote": {
			current: "The app is a pnpm monorepo.\n",
			entry:   "migrations run with pnpm db:migrate",
			want:    "The app is a pnpm monorepo.\n\n## From the lead\n\n- 2026-09-18: migrations run with pnpm db:migrate\n",
		},
		"a second entry joins the first, newest last": {
			current: "## From the lead\n\n- 2026-09-17: the e2e tests need FOO=1\n",
			entry:   "the build needs Java 21",
			want:    "## From the lead\n\n- 2026-09-17: the e2e tests need FOO=1\n- 2026-09-18: the build needs Java 21\n",
		},
		"a section the user wrote after the lead's stays after it": {
			current: "## From the lead\n\n- 2026-09-17: the e2e tests need FOO=1\n\n## House rules\n\nSquash before merging.\n",
			entry:   "the build needs Java 21",
			want: "## From the lead\n\n- 2026-09-17: the e2e tests need FOO=1\n- 2026-09-18: the build needs Java 21\n\n" +
				"## House rules\n\nSquash before merging.\n",
		},
		"an entry is one line": {
			current: "",
			entry:   "  the tests need\n\na running Postgres  ",
			want:    "## From the lead\n\n- 2026-09-18: the tests need a running Postgres\n",
		},
		"a deeper heading inside the section isn't the end of it": {
			current: "## From the lead\n\n### Build\n\n- 2026-09-17: mise provides Go\n",
			entry:   "and Node",
			want:    "## From the lead\n\n### Build\n\n- 2026-09-17: mise provides Go\n- 2026-09-18: and Node\n",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := notes.Append(c.current, c.entry, on); got != c.want {
				t.Errorf("Append()\n--- got ---\n%s\n--- want ---\n%s", got, c.want)
			}
		})
	}
}

// The file is the storage: writing, reading it back and appending to it leave
// markdown a person can read, and emptying the notes removes the file.
func TestReadWriteAppendFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "pawly", "notes.md")

	if text, err := notes.Read(path); err != nil || text != "" {
		t.Fatalf("Read() of a project with no notes = %q, %v; want empty", text, err)
	}
	if err := notes.Write(path, "  The app is a pnpm monorepo.\n\n\n"); err != nil {
		t.Fatal(err)
	}
	text, err := notes.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if text != "The app is a pnpm monorepo.\n" {
		t.Errorf("Read() = %q, want the notes trimmed with one trailing newline", text)
	}

	text, err = notes.AppendToFile(path, "the e2e tests need FOO=1", on)
	if err != nil {
		t.Fatal(err)
	}
	want := "The app is a pnpm monorepo.\n\n## From the lead\n\n- 2026-09-18: the e2e tests need FOO=1\n"
	if text != want {
		t.Errorf("AppendToFile() = %q, want %q", text, want)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != want {
		t.Errorf("the file holds %q, want %q", onDisk, want)
	}

	// Emptying the notes removes the file, so no brief gets an empty section.
	if err := notes.Write(path, "  \n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the notes file is still there (stat: %v)", err)
	}
	if text, err := notes.Read(path); err != nil || text != "" {
		t.Errorf("Read() = %q, %v; want empty", text, err)
	}
}

// An entry is named by quoting it, because the file has no ids in it to name
// one by and a hand edit must not break the naming (D82). Editing keeps the
// bullet and the date the entry was written under.
func TestEdit(t *testing.T) {
	const lead = "## From the lead\n\n- 2026-09-17: the build needs Java 21\n- 2026-09-18: the e2e tests need FOO=1\n"
	cases := map[string]struct{ current, match, text, want string }{
		"a quote of what the entry says": {
			current: lead,
			match:   "the build needs Java 21",
			text:    "the build needs Java 25",
			want:    "## From the lead\n\n- 2026-09-17: the build needs Java 25\n- 2026-09-18: the e2e tests need FOO=1\n",
		},
		"a quote of part of it": {
			current: lead,
			match:   "FOO=1",
			text:    "the e2e tests need FOO=2",
			want:    "## From the lead\n\n- 2026-09-17: the build needs Java 21\n- 2026-09-18: the e2e tests need FOO=2\n",
		},
		"the whole bullet, date and all": {
			current: lead,
			match:   "- 2026-09-17: the build needs Java 21",
			text:    "the build needs Java 25",
			want:    "## From the lead\n\n- 2026-09-17: the build needs Java 25\n- 2026-09-18: the e2e tests need FOO=1\n",
		},
		"case and spacing don't have to match": {
			current: lead,
			match:   "  THE BUILD\n needs   java 21 ",
			text:    "the build needs Java 25",
			want:    "## From the lead\n\n- 2026-09-17: the build needs Java 25\n- 2026-09-18: the e2e tests need FOO=1\n",
		},
		// The user reorders and reformats the file by hand: an entry is found
		// by what it says, wherever it has ended up.
		"an entry the user moved is still named by what it says": {
			current: "## House rules\n\n* the build needs Java 21\n\n## From the lead\n\n- 2026-09-18: the e2e tests need FOO=1\n",
			match:   "the build needs Java 21",
			text:    "the build needs Java 25",
			want:    "## House rules\n\n* the build needs Java 25\n\n## From the lead\n\n- 2026-09-18: the e2e tests need FOO=1\n",
		},
		"a paragraph the user wrote": {
			current: "The app is a pnpm monorepo.\n\n## From the lead\n\n- 2026-09-18: the e2e tests need FOO=1\n",
			match:   "The app is a pnpm monorepo.",
			text:    "The app is a pnpm workspace.",
			want:    "The app is a pnpm workspace.\n\n## From the lead\n\n- 2026-09-18: the e2e tests need FOO=1\n",
		},
		// A model that quotes the bullet back as its replacement shouldn't end
		// up with two markers and two dates.
		"a replacement written as a bullet keeps one marker": {
			current: lead,
			match:   "FOO=1",
			text:    "- 2026-09-20: the e2e tests need FOO=2",
			want:    "## From the lead\n\n- 2026-09-17: the build needs Java 21\n- 2026-09-18: the e2e tests need FOO=2\n",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, change, err := notes.Edit(c.current, c.match, c.text)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Edit()\n--- got ---\n%s\n--- want ---\n%s", got, c.want)
			}
			if change.Was == "" || change.Now == "" || change.Was == change.Now {
				t.Errorf("Edit() reported %+v, want the entry as it was and as it now is", change)
			}
			if !strings.Contains(c.want, change.Now) {
				t.Errorf("Edit() reported %q, which isn't in the notes it wrote", change.Now)
			}
		})
	}
}

// Removing takes the entry and nothing else, and the lead's heading goes with
// its last entry rather than leaving an empty section in every brief.
func TestRemove(t *testing.T) {
	cases := map[string]struct{ current, match, want string }{
		"one of the lead's own": {
			current: "## From the lead\n\n- 2026-09-17: the build needs Java 21\n- 2026-09-18: the e2e tests need FOO=1\n",
			match:   "Java 21",
			want:    "## From the lead\n\n- 2026-09-18: the e2e tests need FOO=1\n",
		},
		"the last of the lead's own takes the heading with it": {
			current: "The app is a pnpm monorepo.\n\n## From the lead\n\n- 2026-09-18: the e2e tests need FOO=1\n",
			match:   "the e2e tests need FOO=1",
			want:    "The app is a pnpm monorepo.\n",
		},
		"and leaves the section the user wrote after it": {
			current: "## From the lead\n\n- 2026-09-18: the e2e tests need FOO=1\n\n## House rules\n\nSquash before merging.\n",
			match:   "the e2e tests need FOO=1",
			want:    "## House rules\n\nSquash before merging.\n",
		},
		"a paragraph of the user's, without leaving a hole": {
			current: "The app is a pnpm monorepo.\n\nSquash before merging.\n\n## From the lead\n\n- 2026-09-18: FOO=1\n",
			match:   "Squash before merging.",
			want:    "The app is a pnpm monorepo.\n\n## From the lead\n\n- 2026-09-18: FOO=1\n",
		},
		"a bullet takes the lines indented under it": {
			current: "## From the lead\n\n- 2026-09-17: the build needs Java 21\n  - and Node 22\n- 2026-09-18: FOO=1\n",
			match:   "the build needs Java 21",
			want:    "## From the lead\n\n- 2026-09-18: FOO=1\n",
		},
		"the only note there was leaves none": {
			current: "## From the lead\n\n- 2026-09-18: FOO=1\n",
			match:   "FOO=1",
			want:    "",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, change, err := notes.Remove(c.current, c.match)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Remove()\n--- got ---\n%s\n--- want ---\n%s", got, c.want)
			}
			if change.Was == "" || change.Now != "" {
				t.Errorf("Remove() reported %+v, want the entry that went and nothing in its place", change)
			}
			if strings.Contains(got, change.Was) {
				t.Errorf("Remove() reported %q as gone, and it is still there:\n%s", change.Was, got)
			}
		})
	}
}

// A quote that doesn't pick out exactly one entry changes nothing and says so.
// Guessing which note was meant is worse than refusing: the wrong guess is a
// line of the user's own that nobody asked to lose.
func TestEditAndRemoveRefuseWhatTheyCannotName(t *testing.T) {
	const current = "## From the lead\n\n- 2026-09-17: the build needs Java 21\n- 2026-09-18: the build needs Java 21 on CI too\n" +
		"- 2026-09-19: the e2e tests need FOO=1\n"

	// More than one entry matches: both are named back, and neither changes.
	_, _, err := notes.Remove(current, "the build needs Java")
	if !errors.Is(err, notes.ErrAmbiguous) {
		t.Fatalf("Remove() with an ambiguous quote = %v, want ErrAmbiguous", err)
	}
	for _, want := range []string{"2026-09-17", "2026-09-18", "nothing was changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal doesn't mention %q:\n%s", want, err)
		}
	}
	if got, _, err := notes.Edit(current, "the build needs Java", "whatever"); !errors.Is(err, notes.ErrAmbiguous) || got != current {
		t.Errorf("Edit() with an ambiguous quote changed the notes:\n%s", got)
	}

	// A quote of one entry in full is not ambiguous, even when another entry
	// has it inside: it is the more exact answer rather than a guess.
	got, change, err := notes.Remove(current, "the build needs Java 21")
	if err != nil {
		t.Fatal(err)
	}
	if change.Was != "- 2026-09-17: the build needs Java 21" {
		t.Errorf("Remove() took %q, want the entry quoted in full", change.Was)
	}
	if !strings.Contains(got, "on CI too") {
		t.Errorf("Remove() took the longer entry too:\n%s", got)
	}

	// Nothing matches: the notes are untouched, and the tool says so rather
	// than removing the nearest thing.
	for _, match := range []string{"the build needs Maven", "## From the lead", "  "} {
		if got, _, err := notes.Remove(current, match); err == nil || got != current {
			t.Errorf("Remove(%q) = %q, %v; want the notes untouched and a refusal", match, got, err)
		}
	}
	if !errors.Is(mustErr(t, current, "the build needs Maven"), notes.ErrNoMatch) {
		t.Error("a quote matching nothing isn't ErrNoMatch")
	}
}

func mustErr(t *testing.T, current, match string) error {
	t.Helper()
	_, _, err := notes.Remove(current, match)
	return err
}

// Editing and removing report which section the entry was in, so the lead can
// say when it has changed the user's own text rather than its own.
func TestChangeSaysWhoseTextItWas(t *testing.T) {
	const current = "## House rules\n\nSquash before merging.\n\n## From the lead\n\n- 2026-09-18: FOO=1\n"

	_, change, err := notes.Remove(current, "FOO=1")
	if err != nil {
		t.Fatal(err)
	}
	if !change.FromLead || change.Section != notes.LeadHeading {
		t.Errorf("removing the lead's own entry reported %+v, want it under %q", change, notes.LeadHeading)
	}
	if _, change, err = notes.Edit(current, "Squash before merging.", "Rebase before merging."); err != nil {
		t.Fatal(err)
	}
	if change.FromLead || change.Section != "## House rules" {
		t.Errorf("editing the user's own text reported %+v, want it named as theirs", change)
	}
}

// The file is the storage for editing too: the notes on disk are what the edit
// left, and a project with no notes has nothing to name.
func TestEditAndRemoveInFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "pawly", "notes.md")
	if _, _, err := notes.RemoveFromFile(path, "anything"); !errors.Is(err, notes.ErrNoMatch) {
		t.Errorf("RemoveFromFile() on a project with no notes = %v, want ErrNoMatch", err)
	}
	if _, err := notes.AppendToFile(path, "the build needs Java 21", on); err != nil {
		t.Fatal(err)
	}
	text, change, err := notes.EditInFile(path, "Java 21", "the build needs Java 25")
	if err != nil {
		t.Fatal(err)
	}
	if change.Now != "- 2026-09-18: the build needs Java 25" {
		t.Errorf("EditInFile() wrote %q, want the date it was written kept", change.Now)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != text || !strings.Contains(text, "Java 25") {
		t.Errorf("the file holds %q, want %q", onDisk, text)
	}
	if _, _, err := notes.RemoveFromFile(path, "Java 25"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the notes file is still there after its last entry went (stat: %v)", err)
	}
}
