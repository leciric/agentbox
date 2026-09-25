package daemon

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// The summary a finish notice carries is the agent's own last message, kept
// whole when it is short and cut when it isn't — and nothing at all when the
// message was never a summary.
func TestSummaryOf(t *testing.T) {
	t.Parallel()
	prose := "Added pagination to the reminders list, twenty a page, with the page in the query " +
		"string. The API already took limit and offset, so nothing changed server side. Two " +
		"snapshot tests covered the old unpaginated list; I updated them rather than adding new " +
		"ones, which is worth a look before this is merged."
	cases := []struct {
		name    string
		message string
		want    string
		wantCut bool
	}{
		{"a summary the size it was asked for", prose, prose, false},
		{"nothing said at all", "", "", false},
		{"only whitespace", "   \n\n\t", "", false},
		{"a bare acknowledgement", "Done.", "", false},
		{"a slightly less bare one", "All done — tests pass!", "", false},
		{"a terse but real summary", "Fixed the typo in the README's install step.", "Fixed the typo in the README's install step.", false},
		{"surrounding whitespace goes", "\n\n" + prose + "\n\n", prose, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, cut := summaryOf(c.message)
			if got != c.want || cut != c.wantCut {
				t.Errorf("summaryOf(%q) = %q, cut %v; want %q, cut %v", c.message, got, cut, c.want, c.wantCut)
			}
		})
	}
}

// The reason for the cap: agents write multi-section reports with tables, and
// pasting one into the lead's chat would cost more context than the read_agent
// call the notice is meant to save.
func TestSummaryOfCutsALongOne(t *testing.T) {
	t.Parallel()
	got, cut := summaryOf(longReport())
	if !cut {
		t.Fatal("a 500-line report went through uncut")
	}
	if n := utf8.RuneCountInString(got); n > maxSummary {
		t.Errorf("the cut summary is %d runes, over the %d cap", n, maxSummary)
	}
	if !strings.HasPrefix(got, "## What I did") {
		t.Errorf("the beginning was not kept:\n%s", got)
	}
	if strings.Contains(got, "|") {
		t.Errorf("a table survived the cut, so the lead reads part of one as all of it:\n%s", got)
	}
}

// Cutting badly is worse than cutting: half a table or an unclosed code block
// reads as complete, so the cut backs off to where the text still means what
// it says.
func TestShortenStopsBeforeWhatWouldMislead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		max  int
		want string
	}{
		{
			"back off out of a table, not into one",
			"Here is what changed.\n\n| File | Why |\n|---|---|\n| a.go | added |\n| b.go | added |\n| c.go | added |",
			60,
			"Here is what changed.",
		},
		{
			"a table that fits whole is kept whole",
			"Here is what changed.\n\n| File | Why |\n|---|---|\n| a.go | added |\n\nAnd then some more prose that does not fit in the cap at all, not nearly.",
			70,
			"Here is what changed.\n\n| File | Why |\n|---|---|\n| a.go | added |",
		},
		{
			"never inside a code block",
			"I changed the command.\n\n```\ngo test ./internal/daemon -run Notice\ngo build ./...\n```\n\nIt passes.",
			55,
			"I changed the command.",
		},
		{
			"not on a heading, which promises a section that was cut",
			"I changed the notice.\n\n## Testing\n\ngo test ./... passes, and the demo script writes a log.",
			40,
			"I changed the notice.",
		},
		{
			"one long line is cut at a sentence",
			"I moved the parser to its own package. It took a while to get right.",
			60,
			"I moved the parser to its own package.",
		},
		{
			"unless the sentence ends so early that most of what fits is lost",
			"I moved it. It now lives in its own package, which took a while to get right.",
			60,
			"I moved it. It now lives in its own package, which took a",
		},
		{
			"and at a word when there is no sentence to cut at",
			"a very long single sentence about the parser that never ends anywhere useful",
			30,
			"a very long single sentence",
		},
		{
			"what fits is left alone",
			"I changed the notice.",
			40,
			"I changed the notice.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, cut := shorten(c.text, c.max)
			if got != c.want {
				t.Errorf("shorten(..., %d) =\n%q\nwant\n%q", c.max, got, c.want)
			}
			if want := got != c.text; cut != want {
				t.Errorf("shorten(..., %d) reported cut %v, want %v", c.max, cut, want)
			}
			if n := utf8.RuneCountInString(got); n > c.max {
				t.Errorf("shorten(..., %d) returned %d runes", c.max, n)
			}
		})
	}
}

// Whatever the agent wrote, the notice has to stay inside the cap.
func TestShortenNeverExceedsTheCap(t *testing.T) {
	t.Parallel()
	texts := []string{
		longReport(),
		strings.Repeat("x", 5000),
		strings.Repeat("| a | b |\n", 400),
		"```\n" + strings.Repeat("go build ./...\n", 300) + "```",
		strings.Repeat("word ", 1000),
		strings.Repeat("\n", 900) + "tail",
	}
	for _, text := range texts {
		got, _ := shorten(text, maxSummary)
		if n := utf8.RuneCountInString(got); n > maxSummary {
			t.Errorf("shorten of %.20q… returned %d runes, over the %d cap", text, n, maxSummary)
		}
	}
}

// The agent's words are quoted so a heading inside them can't be read as part
// of the notice around them.
func TestQuote(t *testing.T) {
	t.Parallel()
	got := quote("I changed the notice.\n\n## Testing\ngo test ./... passes.")
	want := "> I changed the notice.\n>\n> ## Testing\n> go test ./... passes."
	if got != want {
		t.Errorf("quote() =\n%q\nwant\n%q", got, want)
	}
}

// longReport is the kind of summary agents on this project actually write:
// sections, a table of every file, and hundreds of lines of it.
func longReport() string {
	var b strings.Builder
	b.WriteString("## What I did\n\nI reworked the finish notice so the lead is told what the agent ")
	b.WriteString("did, not only how much it changed. The summary is the agent's own last message.\n\n")
	b.WriteString("## Files changed\n\n| File | Change |\n|---|---|\n")
	for i := range 200 {
		fmt.Fprintf(&b, "| internal/daemon/file%02d.go | rewrote the notice |\n", i)
	}
	b.WriteString("\n## Testing\n\n")
	for range 200 {
		b.WriteString("- `go test ./...` passes, and so does the demo script.\n")
	}
	return b.String()
}
