// Package timeline reads what an agent did out of its chat: its items, in
// order, grouped into steps of one kind each (the task it was sent, reading,
// a plan, edits, tests, other commands, a check on the desktop, a Hatch page,
// its pull request), with the media it made placed in the step that was
// running when it was made. The app draws the steps as a timeline over the
// agent's recordings (desktop/src/renderer/components/timeline).
//
// It is pure: the chat and the media go in, and nothing is read or kept.
// What a step says in words is left to the app, which translates it: a step
// carries the agent's and the user's own words (a message, a command, a
// subagent's task, a page's title) and counts, never prose of ours.
package timeline

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"agentbox/internal/api"
)

// How much text a step keeps: the app shows a few lines, not the transcript.
const (
	maxLines     = 16
	maxLineRunes = 200
	maxPaths     = 40
	outputTail   = 8
	maxCommands  = 3
)

// Build is the timeline of a chat's items and the media its agent made.
func Build(items []api.ChatItem, media []api.MediaItem) api.Timeline {
	b := builder{subagents: map[string]int{}}
	for _, it := range items {
		b.add(it)
	}
	steps := absorbReads(b.steps)
	tl := api.Timeline{Steps: steps, Working: working(items)}
	if tl.Working {
		for i := range tl.Steps {
			if tl.Steps[i].Status == statusPending {
				tl.Steps[i].Status = "running"
			}
		}
	}
	for i := range tl.Steps {
		if tl.Steps[i].Status == statusPending {
			tl.Steps[i].Status = ""
		}
	}
	place(tl.Steps, media)
	if len(tl.Steps) > 0 {
		tl.Start = tl.Steps[0].Start
		for _, s := range tl.Steps {
			tl.End = later(tl.End, s.End)
			for _, m := range s.Media {
				tl.End = later(tl.End, mediaEnd(m))
			}
		}
	}
	if tl.Steps == nil {
		tl.Steps = []api.TimelineStep{}
	}
	return tl
}

// statusPending marks a call that hadn't finished when the chat was read:
// running while the turn still is, nothing once it ended without it.
const statusPending = "pending"

type builder struct {
	steps []api.TimelineStep
	// subagents maps a subagent's card to the step it opened, so the calls
	// it makes (items whose Parent is the card) count towards that step.
	subagents map[string]int
	// commands counts the commands each step has shown, so a long run of
	// them keeps its first few.
	commands map[int]int
}

func (b *builder) add(it api.ChatItem) {
	if it.Hidden {
		return
	}
	if it.Parent != "" {
		if i, ok := b.subagents[it.Parent]; ok && it.Kind == "tool" {
			b.steps[i].Calls++
			b.steps[i].End = later(b.steps[i].End, end(it))
		}
		return
	}
	switch it.Kind {
	case "user", "aside":
		if it.Woken || strings.TrimSpace(it.Text) == "" {
			return
		}
		b.steps = append(b.steps, api.TimelineStep{
			Kind:  api.TimelineKindTask,
			Title: firstLine(it.Text),
			Start: it.CreatedAt,
			End:   it.CreatedAt,
			Lines: clip(strings.Split(strings.TrimSpace(it.Text), "\n")),
			Item:  it.ID,
		})
	case "plan":
		lines := make([]string, 0, len(it.Plan))
		for _, e := range it.Plan {
			mark := "[ ] "
			switch e.Status {
			case "completed":
				mark = "[x] "
			case "in_progress":
				mark = "[~] "
			}
			lines = append(lines, mark+e.Content)
		}
		// A plan is set out once and ticked off as it goes: a later plan item
		// replaces the lines of the plan step before it, if nothing came between.
		if n := len(b.steps); n > 0 && b.steps[n-1].Kind == api.TimelineKindPlan {
			b.steps[n-1].Lines = clip(lines)
			b.steps[n-1].End = later(b.steps[n-1].End, end(it))
			return
		}
		b.steps = append(b.steps, api.TimelineStep{Kind: api.TimelineKindPlan, Start: it.CreatedAt, End: end(it), Lines: clip(lines), Item: it.ID})
	case "subagent":
		if it.Subagent == nil {
			return
		}
		kind := api.TimelineKindRead
		if isDesktopAgent(it.Subagent.Name) {
			kind = api.TimelineKindDesktop
		}
		s := api.TimelineStep{Kind: kind, Title: firstLine(it.Subagent.Task), Start: it.CreatedAt, End: end(it), Item: it.ID}
		switch it.Subagent.State {
		case "failed":
			s.Status = "failed"
		case "running":
			s.Status = statusPending
		}
		b.steps = append(b.steps, s)
		b.subagents[it.ID] = len(b.steps) - 1
	case "tool":
		if it.Tool != nil {
			b.tool(it)
		}
	}
}

func (b *builder) tool(it api.ChatItem) {
	t := it.Tool
	kind := Classify(t)
	n := len(b.steps)
	status := callStatus(kind, t)
	var s *api.TimelineStep
	// A call joins the step before when it does the same kind of thing, but
	// a test run that ended otherwise than the one before is a step of its
	// own: failing, then passing once fixed, is two.
	if n > 0 && b.steps[n-1].Kind == kind && kind != api.TimelineKindTask && (kind != api.TimelineKindTest || b.steps[n-1].Status == status || b.steps[n-1].Status == statusPending) {
		s = &b.steps[n-1]
	} else {
		b.steps = append(b.steps, api.TimelineStep{Kind: kind, Start: it.CreatedAt, End: it.CreatedAt, Item: it.ID})
		n++
		s = &b.steps[n-1]
	}
	s.Calls++
	s.End = later(s.End, end(it))
	for _, p := range t.Paths {
		if len(s.Paths) < maxPaths && !slices.Contains(s.Paths, p) {
			s.Paths = append(s.Paths, p)
		}
	}
	// The step's status is its latest call's: tests that failed and were
	// run again until they passed have passed.
	if status != "" || kind == api.TimelineKindTest {
		s.Status = status
	}
	switch kind {
	case api.TimelineKindEdit:
		for _, d := range t.Diffs {
			add, rem, lines := diffLines(d)
			s.Added += add
			s.Removed += rem
			if len(s.Lines) < maxLines {
				s.Lines = clip(append(append(s.Lines, "--- "+d.Path), lines...))
			}
			if len(s.Paths) < maxPaths && !slices.Contains(s.Paths, d.Path) {
				s.Paths = append(s.Paths, d.Path)
			}
		}
	case api.TimelineKindPage:
		if t.Page != nil {
			s.Pages = append(s.Pages, api.TimelinePage{ID: t.Page.ID, Title: t.Page.Title, Item: it.ID})
			if s.Title == "" {
				s.Title = t.Page.Title
			}
		}
	case api.TimelineKindRead:
		// Its paths say what it read.
	default:
		if b.commands == nil {
			b.commands = map[int]int{}
		}
		what := strings.TrimSpace(t.Command)
		if what == "" {
			what = t.Title
		}
		if s.Title == "" {
			s.Title = firstLine(what)
		}
		if b.commands[n-1] < maxCommands {
			b.commands[n-1]++
			lines := []string{"$ " + firstLine(what)}
			if out := strings.TrimRight(t.Output, "\n "); out != "" {
				all := strings.Split(out, "\n")
				lines = append(lines, all[max(0, len(all)-outputTail):]...)
			}
			// A step's lines are its latest command's, so a test run shows how
			// its last attempt ended.
			s.Lines = clip(lines)
		}
	}
}

var (
	testCommand = regexp.MustCompile(`(^|[\s;&|(/])(go test|gotestsum|(npm|pnpm|yarn|bun)\b[^|;&]*\btest\b|npx (vitest|jest|playwright test|mocha)|vitest|jest|pytest|py\.test|tox|cargo (test|nextest)|mix test|rspec|deno test|phpunit|ctest|(make|just) (test|check)\b|\S*gradlew? \S*test|mvn \S*test|check-coverage)`)
	prCommand   = regexp.MustCompile(`(^|[\s;&|(])gh pr (create|edit|ready)\b`)
	// agentbox's own media, browser and phone commands are how an agent
	// shows its work on its desktop.
	desktopCommand = regexp.MustCompile(`(^|[\s;&|(])agentbox (media|browser|android|desktop)\b`)
	// What a failed test run prints, in the runners above.
	testFailed = regexp.MustCompile(`(?m)^(FAIL\b|--- FAIL|not ok\b)|\bfail [1-9]|\b[1-9]\d* (failed|failing)\b|\bFAILED\b|test result: FAILED`)
)

// Classify is the kind of step a tool call is.
func Classify(t *api.ChatTool) string {
	name := strings.ToLower(t.Name)
	switch {
	case t.Page != nil:
		return api.TimelineKindPage
	case strings.Contains(name, "desktop") || strings.Contains(name, "playwright") || strings.Contains(name, "browser_") || strings.Contains(name, "computer"):
		return api.TimelineKindDesktop
	}
	switch t.Kind {
	case "edit", "delete", "move":
		return api.TimelineKindEdit
	case "read", "search", "fetch", "think":
		return api.TimelineKindRead
	}
	cmd := t.Command
	if cmd == "" && t.Kind == "execute" {
		cmd = t.Title
	}
	switch {
	case cmd == "":
		if t.Kind == "execute" {
			return api.TimelineKindRun
		}
		return api.TimelineKindRead
	case prCommand.MatchString(cmd):
		return api.TimelineKindPR
	case desktopCommand.MatchString(cmd):
		return api.TimelineKindDesktop
	case testCommand.MatchString(cmd):
		return api.TimelineKindTest
	}
	return api.TimelineKindRun
}

func callStatus(kind string, t *api.ChatTool) string {
	switch t.Status {
	case "pending", "in_progress":
		return statusPending
	case "failed":
		return "failed"
	}
	if kind == api.TimelineKindTest {
		if testFailed.MatchString(t.Output) {
			return "failed"
		}
		return "passed"
	}
	return ""
}

func isDesktopAgent(name string) bool {
	n := strings.ToLower(name)
	return n == "desktop" || strings.Contains(n, "browser") || strings.Contains(n, "playwright")
}

// absorbReads folds a short stretch of reading between two steps of the
// same kind into them: an agent reads a file between two edits, and that is
// one run of edits, not three steps.
func absorbReads(steps []api.TimelineStep) []api.TimelineStep {
	out := make([]api.TimelineStep, 0, len(steps))
	for i := 0; i < len(steps); i++ {
		s := steps[i]
		if n := len(out); n > 0 && s.Kind == api.TimelineKindRead && s.Title == "" && s.Calls <= 2 && i+1 < len(steps) {
			prev, next := &out[n-1], steps[i+1]
			if next.Kind == prev.Kind && mergeable(prev.Kind) {
				merge(prev, s)
				merge(prev, next)
				i++
				continue
			}
		}
		if n := len(out); n > 0 && s.Kind == out[n-1].Kind && mergeable(s.Kind) {
			merge(&out[n-1], s)
			continue
		}
		out = append(out, s)
	}
	return out
}

func mergeable(kind string) bool {
	return kind == api.TimelineKindEdit || kind == api.TimelineKindRead || kind == api.TimelineKindRun
}

func merge(into *api.TimelineStep, s api.TimelineStep) {
	into.End = later(into.End, s.End)
	into.Calls += s.Calls
	into.Added += s.Added
	into.Removed += s.Removed
	if s.Status != "" {
		into.Status = s.Status
	}
	for _, p := range s.Paths {
		if len(into.Paths) < maxPaths && !slices.Contains(into.Paths, p) {
			into.Paths = append(into.Paths, p)
		}
	}
	if into.Kind == api.TimelineKindEdit {
		into.Lines = clip(append(into.Lines, s.Lines...))
	} else if len(s.Lines) > 0 {
		into.Lines = s.Lines
	}
}

// place puts each screenshot, recording and report in the step that was
// running when it was made (a recording when it started), or the last one
// to start before it; media from before the first step goes in the first.
func place(steps []api.TimelineStep, media []api.MediaItem) {
	if len(steps) == 0 {
		return
	}
	kept := slices.Clone(media)
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].CreatedAt.Before(kept[j].CreatedAt) })
	for _, m := range kept {
		if m.Kind != "screenshot" && m.Kind != "recording" && m.Kind != "report" {
			continue
		}
		at := 0
		for i, s := range steps {
			if !s.Start.After(m.CreatedAt) {
				at = i
			}
		}
		// Media made while a desktop check ran is that check's, even when a
		// later step started meanwhile (the subagent's card runs alongside).
		for i, s := range steps {
			if s.Kind == api.TimelineKindDesktop && !m.CreatedAt.Before(s.Start) && !m.CreatedAt.After(s.End) {
				at = i
			}
		}
		steps[at].Media = append(steps[at].Media, m)
	}
}

func mediaEnd(m api.MediaItem) time.Time {
	return m.CreatedAt.Add(time.Duration(m.Meta.Duration * float64(time.Second)))
}

// working says whether the chat's last turn is still running: its user item
// has no result yet.
func working(items []api.ChatItem) bool {
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		if it.Kind == "user" && it.Parent == "" {
			return it.Result == nil
		}
	}
	return false
}

// diffLines counts the lines a diff added and removed, and gives the first
// few of them, marked + and −.
func diffLines(d api.ChatDiff) (added, removed int, lines []string) {
	old := map[string]int{}
	for _, l := range splitLines(d.OldText) {
		old[l]++
	}
	var plus []string
	for _, l := range splitLines(d.NewText) {
		if old[l] > 0 {
			old[l]--
			continue
		}
		added++
		plus = append(plus, "+"+l)
	}
	var minus []string
	for _, l := range splitLines(d.OldText) {
		if old[l] > 0 {
			old[l]--
			removed++
			minus = append(minus, "-"+l)
		}
	}
	lines = append(minus, plus...)
	if len(lines) > 8 {
		lines = lines[:8]
	}
	return added, removed, lines
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func end(it api.ChatItem) time.Time {
	return later(it.CreatedAt, it.UpdatedAt)
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return cut(s)
}

func cut(s string) string {
	if utf8.RuneCountInString(s) <= maxLineRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxLineRunes-1]) + "…"
}

func clip(lines []string) []string {
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = cut(l)
	}
	return out
}
