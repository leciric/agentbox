package daemon

import (
	"strings"
	"unicode/utf8"
)

// What an agent's last message is worth to the lead, in runes.
const (
	// maxSummary is how much of it a finish notice carries. Five or six
	// sentences of prose fit; the multi-section report with tables that agents
	// write instead doesn't, and is cut. A notice that costs the lead more
	// context than the read_agent call it saves would be worse than no notice.
	maxSummary = 800
	// minSummary is the line under which the message is an acknowledgement —
	// "Done.", "All set!" — rather than a summary. Quoting one of those would
	// promise the lead something the notice isn't giving it, so the notice says
	// there is no summary instead, and points at the conversation.
	minSummary = 24
)

// summaryOf turns an agent's last message into the summary its finish notice
// carries, and says whether anything was left out. It answers with "" when the
// message is no summary at all, so the notice can say so rather than quote a
// fragment of something else.
func summaryOf(message string) (summary string, cut bool) {
	text := strings.TrimSpace(message)
	if utf8.RuneCountInString(text) < minSummary {
		return "", false
	}
	return shorten(text, maxSummary)
}

// shorten cuts text down to at most max runes. It cuts between whole lines and
// never inside a table or a fenced code block: three rows of a nine-row table
// read like the whole table, which misleads the lead rather than merely telling
// it less. A first line already too long is cut at a sentence, or at a word.
func shorten(text string, max int) (string, bool) {
	if utf8.RuneCountInString(text) <= max {
		return text, false
	}
	lines := strings.Split(text, "\n")
	// open[i] reports whether lines[:i] leaves a code fence open.
	open := make([]bool, len(lines)+1)
	for i, line := range lines {
		open[i+1] = open[i] != isFence(line)
	}
	k, n := 0, 0
	for ; k < len(lines); k++ {
		if k > 0 {
			n++ // the newline joining it to the line before
		}
		n += utf8.RuneCountInString(lines[k])
		if n > max {
			break
		}
	}
	for ; k > 0 && !endsWell(lines, open, k); k-- {
	}
	if k == 0 {
		return cutLine(lines[0], max), true
	}
	return strings.TrimRight(strings.Join(lines[:k], "\n"), " \t\n"), true
}

// endsWell reports whether keeping the first k lines leaves something that
// reads as what it is: not half a table, not an unclosed code block, and not a
// heading promising a section that was cut off. Blank lines at the end are
// nothing in themselves, so it judges the last line with something on it.
func endsWell(lines []string, open []bool, k int) bool {
	if open[k] {
		return false
	}
	last := -1
	for i := k - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			last = i
			break
		}
	}
	if last < 0 {
		return false // only blank lines: there is no summary in that
	}
	line := strings.TrimSpace(lines[last])
	if strings.HasPrefix(line, "#") {
		return false
	}
	// A table is whole when what follows it isn't another row — a blank line
	// inside the cut, or the line the cut fell on.
	return !(isTableRow(line) && last+1 < len(lines) && isTableRow(strings.TrimSpace(lines[last+1])))
}

// cutLine cuts one long line to max runes, at the end of a sentence when that
// keeps most of what fits, and at a word otherwise.
func cutLine(line string, max int) string {
	runes := []rune(line)
	if len(runes) <= max {
		return line
	}
	head := string(runes[:max])
	if i := strings.LastIndexAny(head, ".!?"); i >= 0 && utf8.RuneCountInString(head[:i+1]) >= max/2 {
		return strings.TrimSpace(head[:i+1])
	}
	if i := strings.LastIndex(head, " "); i > 0 {
		return strings.TrimSpace(head[:i])
	}
	return strings.TrimSpace(head)
}

func isFence(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

func isTableRow(line string) bool {
	return strings.HasPrefix(line, "|")
}

// quote marks an agent's own words off from AgentBox's, so a heading or a list
// inside them can't be read as part of the notice around them.
func quote(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight("> "+line, " \t")
	}
	return strings.Join(lines, "\n")
}
