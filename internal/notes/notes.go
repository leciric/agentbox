// Package notes keeps a project's notes: what every agent of it should know
// before it starts. They are one markdown file per project (paths.ProjectNotes),
// written by the user in the app and added to by the project's lead, and folded
// into every agent's brief.
package notes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// LeadHeading is the section the lead adds its own entries under, so what it
// learned stays apart from what the user wrote.
const LeadHeading = "## From the lead"

// Read returns a project's notes, empty when it has none yet. A project with
// no notes file is the ordinary case, not an error.
func Read(path string) (string, error) {
	text, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(text), nil
}

// Write replaces a project's notes. Notes that are only whitespace remove the
// file: no notes at all, rather than an empty section in every brief.
func Write(path, text string) error {
	text = normalize(text)
	if text == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// AppendToFile adds one dated entry under the lead's heading and returns the
// notes as they now are.
func AppendToFile(path, entry string, on time.Time) (string, error) {
	current, err := Read(path)
	if err != nil {
		return "", err
	}
	text := Append(current, entry, on)
	if err := Write(path, text); err != nil {
		return "", err
	}
	return text, nil
}

// Append returns the notes with entry added as a dated bullet under the lead's
// heading, which it creates when the notes don't have one yet. The entry goes
// at the end of that section, so the lead's own entries stay in order and
// nothing the user wrote after them is disturbed. It is one line: an entry is a
// fact, not a report.
func Append(current, entry string, on time.Time) string {
	bullet := "- " + on.Format(time.DateOnly) + ": " + oneLine(entry)
	current = normalize(current)
	if current == "" {
		return LeadHeading + "\n\n" + bullet + "\n"
	}
	lines := strings.Split(strings.TrimSuffix(current, "\n"), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == LeadHeading {
			start = i
			break
		}
	}
	if start < 0 {
		return current + "\n" + LeadHeading + "\n\n" + bullet + "\n"
	}
	// The section ends at the next heading of the same level, or at the end.
	next := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			next = i
			break
		}
	}
	// The entry goes after the last line of the section, not after the blank
	// lines that separate it from what follows.
	last := next
	for last > start+1 && strings.TrimSpace(lines[last-1]) == "" {
		last--
	}
	out := make([]string, 0, len(lines)+2)
	out = append(out, lines[:last]...)
	out = append(out, bullet)
	if next < len(lines) {
		out = append(out, "") // the blank line before the section that follows
		out = append(out, lines[next:]...)
	}
	return strings.Join(out, "\n") + "\n"
}

// normalize trims the blank lines around notes and gives them one trailing
// newline, so appending to them and folding them into a brief are predictable.
func normalize(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	return text + "\n"
}

// oneLine folds an entry onto a single line, so it stays a bullet in a list.
func oneLine(entry string) string {
	return strings.Join(strings.Fields(entry), " ")
}

// An entry is what the lead names when it is asked to change one note rather
// than add one. Notes are a markdown file a person edits by hand
// (D60), so there are no ids in
// it to name an entry by: an entry is named by quoting it, and a quote that
// picks out one entry picks out the same one however the file has been
// reordered since. A quote that picks out none, or more than one, changes
// nothing.
var (
	// ErrNoMatch is nothing in the notes matching the quote.
	ErrNoMatch = errors.New("no note matches")
	// ErrAmbiguous is more than one matching it. Guessing which was meant is
	// worse than refusing.
	ErrAmbiguous = errors.New("more than one note matches")
)

// Entry is one addressable piece of the notes: a bullet with the lines
// indented under it, or a paragraph outside a list. Headings are not entries —
// they are the structure around them — so a quote names a fact rather than a
// section, and nothing the lead does takes a heading out from under the
// entries that sit beneath it.
type Entry struct {
	Text     string // the entry as it stands, bullet marker, date and all
	Section  string // the heading it sits under, empty when it is under none
	FromLead bool   // it is under LeadHeading: the lead's own section
	start    int    // its first line
	end      int    // one past its last
}

// Change is what an edit or a removal did, in the words the lead reports back:
// the entry as it was, and as it now is.
type Change struct {
	Was      string // the entry before the change
	Now      string // after it, empty when the entry was removed
	Section  string // the heading it sits under
	FromLead bool   // it was the lead's own entry, not the user's
}

// Entries returns the notes as the entries a quote can name, in the order they
// appear in the file.
func Entries(current string) []Entry {
	current = normalize(current)
	if current == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(current, "\n"), "\n")
	var out []Entry
	section, fromLead := "", false
	for i := 0; i < len(lines); {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			continue
		}
		if strings.HasPrefix(line, "#") {
			section = line
			// A heading of the lead's own level ends its section; a deeper one
			// inside it doesn't, the way appending already treats them.
			if line == LeadHeading {
				fromLead = true
			} else if level(line) <= 2 {
				fromLead = false
			}
			i++
			continue
		}
		start := i
		bullet := bulletAt(lines[start])
		i++
		for i < len(lines) {
			next := strings.TrimSpace(lines[i])
			if next == "" || strings.HasPrefix(next, "#") {
				break
			}
			// A bullet at the same level or shallower starts the next entry;
			// anything indented under this one is part of it. A paragraph ends
			// where a list begins.
			if bulletAt(lines[i]) != "" && (bullet == "" || indent(lines[i]) <= indent(lines[start])) {
				break
			}
			i++
		}
		out = append(out, Entry{
			Text:     strings.Join(lines[start:i], "\n"),
			Section:  section,
			FromLead: fromLead,
			start:    start,
			end:      i,
		})
	}
	return out
}

// Find returns the one entry a quote names, matched with whitespace folded and
// case ignored. An entry the user reordered by hand is still named by what it
// says; one they reworded stops matching rather than matching its neighbour.
func Find(current, match string) (Entry, error) {
	// A quote of a whole bullet, marker and all, names the same entry as a
	// quote of what it says.
	needle := strings.ToLower(entryText(match))
	if needle == "" {
		return Entry{}, errors.New("name the note to change by quoting it")
	}
	var exact, partial []Entry
	for _, e := range Entries(current) {
		line, body := strings.ToLower(e.line()), strings.ToLower(e.Body())
		switch {
		case needle == line || needle == body:
			exact = append(exact, e)
		case strings.Contains(line, needle):
			partial = append(partial, e)
		}
	}
	// A quote of a whole entry wins over one that happens to be inside
	// another: it is the more exact of two answers, not a guess between them.
	found := exact
	if len(found) == 0 {
		found = partial
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return Entry{}, fmt.Errorf("%w %q, so nothing was changed", ErrNoMatch, oneLine(match))
	default:
		quoted := make([]string, 0, len(found))
		for _, e := range found {
			quoted = append(quoted, "  "+oneLine(e.Text))
		}
		return Entry{}, fmt.Errorf("%w %q, so nothing was changed. Quote more of the one you mean:\n%s",
			ErrAmbiguous, oneLine(match), strings.Join(quoted, "\n"))
	}
}

// Edit returns the notes with the entry a quote names saying something else,
// and what changed. The entry keeps its bullet marker and the date it was
// written: an edit corrects a note, it doesn't write a new one.
func Edit(current, match, text string) (string, Change, error) {
	e, err := Find(current, match)
	if err != nil {
		return current, Change{}, err
	}
	// A replacement written as a bullet, the way the entry reads now, keeps
	// one marker and one date rather than two.
	text = stripDate(entryText(text))
	if text == "" {
		return current, Change{}, errors.New("an edited note needs some text: removing one is its own tool")
	}
	now := e.prefix() + text
	lines := strings.Split(strings.TrimSuffix(normalize(current), "\n"), "\n")
	out := make([]string, 0, len(lines))
	out = append(out, lines[:e.start]...)
	out = append(out, now)
	out = append(out, lines[e.end:]...)
	return normalize(strings.Join(out, "\n")), e.change(now), nil
}

// Remove returns the notes without the entry a quote names, and what went. The
// lead's heading goes with its last entry, so no brief carries an empty
// section.
func Remove(current, match string) (string, Change, error) {
	e, err := Find(current, match)
	if err != nil {
		return current, Change{}, err
	}
	lines := strings.Split(strings.TrimSuffix(normalize(current), "\n"), "\n")
	out := make([]string, 0, len(lines))
	out = append(out, lines[:e.start]...)
	out = append(out, lines[e.end:]...)
	// Without this, the blank line that separated the entry from what follows
	// is left doubled up against the one before it.
	if e.start > 0 && e.start < len(out) && strings.TrimSpace(out[e.start-1]) == "" && strings.TrimSpace(out[e.start]) == "" {
		out = append(out[:e.start], out[e.start+1:]...)
	}
	if e.FromLead {
		out = dropEmptyLeadSection(out)
	}
	return normalize(strings.Join(out, "\n")), e.change(""), nil
}

// EditInFile edits one entry of a project's notes and returns the notes as
// they now are, with what changed.
func EditInFile(path, match, text string) (string, Change, error) {
	return change(path, func(current string) (string, Change, error) {
		return Edit(current, match, text)
	})
}

// RemoveFromFile removes one entry of them, the same way.
func RemoveFromFile(path, match string) (string, Change, error) {
	return change(path, func(current string) (string, Change, error) {
		return Remove(current, match)
	})
}

func change(path string, apply func(string) (string, Change, error)) (string, Change, error) {
	current, err := Read(path)
	if err != nil {
		return "", Change{}, err
	}
	if strings.TrimSpace(current) == "" {
		return "", Change{}, fmt.Errorf("%w: this project has no notes yet", ErrNoMatch)
	}
	text, done, err := apply(current)
	if err != nil {
		return "", Change{}, err
	}
	if err := Write(path, text); err != nil {
		return "", Change{}, err
	}
	return text, done, nil
}

// Body is the entry without its bullet marker and its date, which is what it
// says rather than how it is written down.
func (e Entry) Body() string { return stripDate(e.line()) }

// line is the entry folded onto one line, without its bullet marker.
func (e Entry) line() string { return entryText(e.Text) }

// entryText folds an entry, or a quote of one, onto a line without its bullet
// marker: how an entry is written down is not part of what it says.
func entryText(text string) string { return oneLine(stripBullet(oneLine(text))) }

// prefix is what an edited entry keeps: its indentation, its bullet marker and
// the date it was written under.
func (e Entry) prefix() string {
	first := e.Text
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	marker := bulletAt(first)
	rest := strings.TrimPrefix(first, marker)
	return marker + strings.TrimSuffix(rest, stripDate(rest))
}

func (e Entry) change(now string) Change {
	return Change{Was: e.Text, Now: now, Section: e.Section, FromLead: e.FromLead}
}

// dropEmptyLeadSection takes out the lead's heading when nothing of the lead's
// is left under it.
func dropEmptyLeadSection(lines []string) []string {
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == LeadHeading {
			start = i
			break
		}
	}
	if start < 0 {
		return lines
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "## ") {
			end = i
			break
		}
		if line != "" {
			return lines // the section still has something in it
		}
	}
	out := make([]string, 0, len(lines))
	out = append(out, lines[:start]...)
	return append(out, lines[end:]...)
}

// bulletAt is the bullet marker a line starts with, indentation and all, or
// empty when the line is not a bullet.
func bulletAt(line string) string { return bulletRE.FindString(line) }

func stripBullet(line string) string { return strings.TrimPrefix(line, bulletAt(line)) }

func stripDate(text string) string { return dateRE.ReplaceAllString(text, "") }

func indent(line string) int { return len(line) - len(strings.TrimLeft(line, " \t")) }

func level(heading string) int { return len(heading) - len(strings.TrimLeft(heading, "#")) }

var (
	bulletRE = regexp.MustCompile(`^[ \t]*([-*+]|\d+[.)])[ \t]+`)
	dateRE   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}:[ \t]*`)
)
