package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"agentbox/internal/memory"
)

// The distillation's input filter: what of a window is worth the cheap
// model's tokens.
//
// A window is three hundred events, and most of them say little: the same
// stall noticed four times, a pull request broken again by the next push, a
// "thanks" and a "you're welcome", ids nobody can look up. Every one of them
// was billed as input on every pass. This decides, with no model and no state
// beyond the window, which lines the prompt shows; it is the same bargain as
// ai-memory's entropy filter (akitaonrails/ai-memory, entropy_filter.rs).
//
// It only shapes the prompt. Every event stays stored and searchable, and the
// ones it drops still count as read: the watermark moves past them like any
// other, because a pass that skipped them has already judged them.
//
// What it does, in the order it does it:
//
//   - Payloads lose what carries nothing: empty fields, and the ids a model
//     can neither name in an anchor nor look up (an artifact's, a report's, a
//     head commit, a pull request's URL beside its number).
//   - A lead turn that says nothing on either side is dropped: a "thanks"
//     answered with "You're welcome", a background job ending to a reply of
//     "Still waiting on CI.". Judged by entropy_filter's three tests and a
//     short list of the words status lines are made of, and never when either
//     side names a pull request.
//   - Repeats fold into their first: the same type, agent and payload, with
//     counters and numbered names ignored, is one line that says how many
//     times and when the last was. Pull request numbers, question ids and
//     branches are never ignored, so two pull requests never fold together.
//   - Long text already shown in the window, a task quoted again by the
//     agent's finish or a question by its answer, reads "(as above)".

// distillLine is one line of the prompt's history: an event, and any repeats
// of it later in the window.
type distillLine struct {
	event   memory.Event
	payload string // the rendered payload, "" for none
	members []int  // the window's indexes this line stands for, the first being event
}

// The thresholds are entropy_filter's defaults, which are deliberately low:
// a terse note that says something has to survive them.
const (
	minInformativeChars  = 16  // non-space characters
	minEntropyBitsPerRun = 2.0 // Shannon entropy, bits per character
	maxRepetitionRatio   = 0.7 // 1 - distinct/total tokens
	repetitionMinTokens  = 6

	// sameTextMin is how long a string must be before a repeat of it reads
	// "(as above)": shorter ones cost less than the marker.
	sameTextMin = 32
	// countedTextMax is how long a string may be and still have its digits
	// ignored when looking for repeats: "screenshot-3" is a counter, a
	// paragraph's numbers are content.
	countedTextMax = 80
)

// distillLines is the window as the prompt shows it.
func distillLines(events []memory.Event) []distillLine {
	var lines []distillLine
	bySignature := map[string]int{}
	shown := map[string]bool{}
	for i, e := range events {
		fields, raw := prunedPayload(e.Payload)
		if e.Type == "lead_turn" && emptyTurn(fields) {
			continue
		}
		sig := e.Type + "\x00" + e.Agent + "\x00" + signature(fields, raw)
		if j, ok := bySignature[sig]; ok {
			lines[j].members = append(lines[j].members, i)
			continue
		}
		bySignature[sig] = len(lines)
		lines = append(lines, distillLine{event: e, payload: renderPayload(fields, raw, shown), members: []int{i}})
	}
	return lines
}

// render is the line as the prompt shows it, counting only the repeats up to
// through, the window's index past which nothing is shown this pass.
func (l distillLine) render(window []memory.Event, through int) string {
	e := l.event
	line := fmt.Sprintf("- %s %s", e.At.Format("2006-01-02 15:04"), e.Type)
	if e.Agent != "" {
		line += " (" + e.Agent + ")"
	}
	n, last := 0, e.At
	for _, i := range l.members {
		if i < through {
			n, last = n+1, window[i].At
		}
	}
	if n > 1 {
		format := "15:04"
		if last.YearDay() != e.At.YearDay() || last.Year() != e.At.Year() {
			format = "2006-01-02 15:04"
		}
		line += fmt.Sprintf(" ×%d, last %s", n, last.Format(format))
	}
	if l.payload != "" {
		line += " " + truncate(l.payload, 600)
	}
	return line
}

// boilerplateKeys are ids a distilling model can do nothing with: it can't
// look an artifact or a report up, and a head commit changes with every push.
var boilerplateKeys = map[string]bool{"artifactId": true, "reportId": true, "head": true}

// anchorKeys are what an anchor is made of (memory.ParseAnchor): never folded
// away as a counter, never shortened to "(as above)".
var anchorKeys = map[string]bool{"number": true, "questionId": true, "branch": true}

// prunedPayload reads a payload as a JSON object without what carries
// nothing. A payload that isn't an object comes back as raw, collapsed onto
// one line, exactly as the prompt showed every payload before.
func prunedPayload(payload json.RawMessage) (map[string]any, string) {
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		raw := collapseLines(string(payload))
		if raw == "{}" || raw == "null" {
			raw = ""
		}
		return nil, raw
	}
	prune(fields)
	return fields, ""
}

// prune drops a map's empty and boilerplate fields, in place and all the way
// down.
func prune(m map[string]any) {
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			prune(sub)
		}
		if boilerplateKeys[k] || empty(v) {
			delete(m, k)
		}
	}
	if _, ok := m["number"]; ok {
		delete(m, "url") // a pull request's number is its anchor; the URL repeats it
	}
}

func empty(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case bool:
		return !v
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

// emptyTurn says whether a lead turn says nothing on either side. The user's
// side counts as nothing when it is low-information or a background job
// waking the session; the lead's when it is low-information. A turn that
// names a pull request on either side always says something.
func emptyTurn(fields map[string]any) bool {
	msg, _ := fields["userMessage"].(string)
	reply, _ := fields["assistantReply"].(string)
	if mentionsPR(msg) || mentionsPR(reply) {
		return false
	}
	woken := strings.HasPrefix(msg, "(no message: background work")
	return (woken || lowInformation(msg) || statusLine(msg)) && (lowInformation(reply) || statusLine(reply))
}

// statusWords are what a status line is made of, and nothing else: "Still
// waiting on CI.", "The tests are still running." One word outside them —
// "failed", a file, a name — and the line is saying something.
var statusWords = map[string]bool{
	"still": true, "waiting": true, "wait": true, "on": true, "ci": true, "is": true, "are": true,
	"running": true, "the": true, "tests": true, "checks": true, "build": true, "for": true,
	"it": true, "it's": true, "its": true, "ok": true, "okay": true, "nothing": true, "new": true,
	"yet": true, "pending": true, "in": true, "progress": true, "working": true,
	"i'm": true, "i'll": true, "will": true, "let": true, "you": true, "know": true, "when": true,
}

// statusLine says whether a short text is only a status: "Still running."
func statusLine(s string) bool {
	tokens := strings.Fields(strings.ToLower(s))
	if len(tokens) == 0 || len(tokens) > 12 {
		return false
	}
	for _, t := range tokens {
		if !statusWords[strings.Trim(t, ".,;:!?…—-()")] {
			return false
		}
	}
	return true
}

var prMention = regexp.MustCompile(`#\d+`)

func mentionsPR(s string) bool { return prMention.MatchString(s) }

// lowInformation is entropy_filter's classify: text too short to say
// anything, made of too few symbols to, or the same few words over and over.
func lowInformation(s string) bool {
	nonSpace := 0
	counts := map[rune]int{}
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		nonSpace++
		counts[r]++
	}
	if nonSpace < minInformativeChars {
		return true
	}
	entropy := 0.0
	for _, c := range counts {
		p := float64(c) / float64(nonSpace)
		entropy -= p * math.Log2(p)
	}
	if entropy < minEntropyBitsPerRun {
		return true
	}
	tokens := strings.Fields(strings.ToLower(s))
	if len(tokens) < repetitionMinTokens {
		return false
	}
	distinct := map[string]bool{}
	for _, t := range tokens {
		distinct[t] = true
	}
	return 1-float64(len(distinct))/float64(len(tokens)) > maxRepetitionRatio
}

// signature is what two repeats share: the payload with its counters taken
// out — numbers, and the digits in a short string — except in what an anchor
// is made of.
func signature(fields map[string]any, raw string) string {
	if fields == nil {
		return raw
	}
	out, _ := json.Marshal(uncounted(fields))
	return string(out)
}

var digits = regexp.MustCompile(`#?\d+`)

func uncounted(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			if anchorKeys[k] {
				out[k] = x
				continue
			}
			out[k] = uncounted(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = uncounted(x)
		}
		return out
	case float64:
		return "#"
	case string:
		if utf8.RuneCountInString(v) > countedTextMax {
			return v
		}
		return digits.ReplaceAllStringFunc(v, func(d string) string {
			if strings.HasPrefix(d, "#") {
				return d // "#249" is a pull request, not a counter
			}
			return "#"
		})
	}
	return v
}

// renderPayload writes the pruned payload as one line of JSON, with any long
// string the window has already shown replaced by "(as above)". shown is
// shared by the whole window, and filled in as it goes.
func renderPayload(fields map[string]any, raw string, shown map[string]bool) string {
	if fields == nil {
		return raw
	}
	if len(fields) == 0 {
		return ""
	}
	dedupe(fields, shown)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fields); err != nil {
		return ""
	}
	return collapseLines(b.String())
}

func dedupe(m map[string]any, shown map[string]bool) {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		switch v := m[k].(type) {
		case map[string]any:
			dedupe(v, shown)
		case string:
			if anchorKeys[k] || utf8.RuneCountInString(v) < sameTextMin {
				continue
			}
			key := collapseLines(v)
			if shown[key] {
				m[k] = "(as above)"
				continue
			}
			shown[key] = true
		}
	}
}

// lastShown is the index past the last event a prompt that holds the first n
// lines covers: everything before the first line it left out, dropped events
// included. All of them is the whole window.
func lastShown(lines []distillLine, n, window int) int {
	if n >= len(lines) {
		return window
	}
	return lines[n].members[0]
}
