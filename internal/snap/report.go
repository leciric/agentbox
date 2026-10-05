package snap

import (
	"fmt"
	"strings"
	"time"
)

// Report is the message a SnapShot is sent as: the user's note first, then
// what was captured, then the accessibility summary in a fenced block, for
// the model to read beside the picture.
func Report(note, app, title, desktop string, window bool, accessibility string, taken time.Time) string {
	var b strings.Builder
	what := "the screen"
	if window {
		what = "a window"
	}
	b.WriteString("Bug report from a SnapShot of " + what)
	switch {
	case app != "" && title != "":
		fmt.Fprintf(&b, ": %s, %q", app, title)
	case app != "" || title != "":
		b.WriteString(": " + app + title)
	}
	b.WriteString(".\n")
	if note = strings.TrimSpace(note); note != "" {
		b.WriteString("\n" + note + "\n")
	}
	fmt.Fprintf(&b, "\nCaptured %s", taken.Format("2006-01-02 15:04 MST"))
	if desktop != "" && desktop != "screen" {
		b.WriteString(" on " + desktop)
	}
	b.WriteString("; the picture is attached.")
	if accessibility = strings.TrimSpace(accessibility); accessibility != "" {
		b.WriteString(" The window's accessibility tree, summarized (role \"name\": text):\n\n```\n" +
			strings.ReplaceAll(accessibility, "```", "'''") + "\n```")
	}
	return b.String()
}
