package memory

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Anchors: what would close an open item.
//
// An issue memory is written while something is wrong and read for weeks
// after it stopped being. Most of them say, in their own words, what they are
// waiting on — "#234 then #237 (stacked)", "agentbox/fix-x needs a rebase",
// "agent-07 asked (id 1a2b3c4d)" — and every one of those is something the
// daemon can check without a model: a pull request is merged or closed, a
// question is answered. So when an open item is written, those references are
// pulled out of its text and kept beside it, and the daemon closes it once
// every one of them is over (ResolveAnchored).
//
// Paths and symbols are kept too, but never close anything: a file that still
// exists says nothing about whether the bug in it was fixed. They are what
// two issues about the same code have in common, which dedup weighs.

// Anchor kinds.
const (
	AnchorPR       = "pr"       // a pull request number, "234"
	AnchorBranch   = "branch"   // a branch name, "agentbox/fix-x"
	AnchorQuestion = "question" // a question id an agent asked, "1a2b3c4d"
	AnchorPath     = "path"     // a file path, "internal/agent/recap.go"
	AnchorSymbol   = "symbol"   // a Go-style symbol, "memory.BuildContext"
)

// Anchor is one thing an open item names that says when it is over.
type Anchor struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func (a Anchor) String() string {
	switch a.Kind {
	case AnchorPR:
		return "#" + a.Value
	case AnchorQuestion:
		return "question " + a.Value
	}
	return a.Value
}

// Closes reports whether this kind of anchor can close an item by itself
// being over. Paths and symbols can't.
func (a Anchor) Closes() bool {
	return a.Kind == AnchorPR || a.Kind == AnchorBranch || a.Kind == AnchorQuestion
}

// maxAnchors bounds what one memory keeps. A memory listing forty pull
// requests is a list, and the first two dozen say as much about when it is
// over as all of them.
const maxAnchors = 24

var (
	// #234, PR 234, PR #234, pull request #234, …/pull/234. A bare number
	// after "#" is a pull request in this project's language; a number GitHub
	// knows as an issue instead simply never reads as merged.
	prPattern = regexp.MustCompile(`(?i)(?:#|\bPRs?\s+#?|\bpull requests?\s+#?|/pull/)(\d{1,6})\b`)
	// A branch is AgentBox's own (agentbox/…), a conventional prefix
	// (feat/…, fix/…), or anything that follows the word "branch".
	branchPattern       = regexp.MustCompile(`\b((?:agentbox|feat|fix|chore|refactor|docs|test|perf|ci|release-please-+branches-+)[A-Za-z0-9._-]*/[A-Za-z0-9._/-]*[A-Za-z0-9])`)
	namedBranchPattern  = regexp.MustCompile("(?i)\\bbranch(?:es)?\\s+`?([A-Za-z0-9._-]+/[A-Za-z0-9._/-]*[A-Za-z0-9])`?")
	questionPattern     = regexp.MustCompile(`(?i)(?:\bquestions?\s+(?:id\s+)?|\(id\s+|\banswer_question\s*\(?\s*(?:id\s*)?)` + "`?" + `([0-9a-f]{8})\b`)
	pathPattern         = regexp.MustCompile(`(?:^|[\s(` + "`" + `'"])((?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_-]+\.(?:go|ts|tsx|js|mjs|json|md|sh|ya?ml|toml|tmpl|txt|css|sql))\b`)
	symbolPattern       = regexp.MustCompile(`\b([a-z][a-z0-9]*\.[A-Z][A-Za-z0-9_]*(?:\.[A-Z][A-Za-z0-9_]*)?)\b`)
	branchFileExtension = regexp.MustCompile(`\.(?:go|ts|tsx|js|json|md|sh|ya?ml|toml|tmpl|txt)$`)
)

// ExtractAnchors pulls out of a memory's text what would close it. It is
// deliberately literal — patterns, not judgement — because a missed anchor
// costs an issue that ages out instead of closing, and a wrong one costs an
// issue closed by a pull request it only mentioned.
func ExtractAnchors(text string) []Anchor {
	var out []Anchor
	seen := map[Anchor]bool{}
	add := func(kind, value string) {
		a := Anchor{Kind: kind, Value: value}
		if value == "" || seen[a] || len(out) >= maxAnchors {
			return
		}
		seen[a] = true
		out = append(out, a)
	}
	for _, m := range prPattern.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			add(AnchorPR, strconv.Itoa(n))
		}
	}
	for _, p := range []*regexp.Regexp{branchPattern, namedBranchPattern} {
		for _, m := range p.FindAllStringSubmatchIndex(text, -1) {
			// Inside a URL or a longer path it is a piece of that, not a branch.
			if m[2] > 0 && strings.ContainsRune("/.:", rune(text[m[2]-1])) {
				continue
			}
			if b := text[m[2]:m[3]]; branchLike(b) {
				add(AnchorBranch, strings.TrimRight(b, "."))
			}
		}
	}
	for _, m := range questionPattern.FindAllStringSubmatch(text, -1) {
		add(AnchorQuestion, strings.ToLower(m[1]))
	}
	for _, m := range pathPattern.FindAllStringSubmatch(text, -1) {
		add(AnchorPath, m[1])
	}
	for _, m := range symbolPattern.FindAllStringSubmatch(text, -1) {
		add(AnchorSymbol, m[1])
	}
	return out
}

// branchLike keeps a branch pattern's match from being this module's own
// import paths ("agentbox/internal/memory") or a file.
func branchLike(s string) bool {
	for _, dir := range []string{"agentbox/internal", "agentbox/cmd", "agentbox/hubapi", "agentbox/desktop"} {
		if s == dir || strings.HasPrefix(s, dir+"/") {
			return false
		}
	}
	return !branchFileExtension.MatchString(s) && !strings.Contains(s, "//")
}

// openItemPattern is the wording of an item that is waiting on something,
// whatever kind it was filed as: "PRs awaiting the user's merge" is as often
// written down as a fact about the project as an issue.
var openItemPattern = regexp.MustCompile(`(?i)\b(awaiting|waiting (?:on|for)|open PRs?|unmerged|not (?:yet )?merged|still open|unfixed|left (?:to do|undone)|needs? (?:a )?(?:fix|rebase|review|merge))\b`)

// OpenItem reports whether a memory is something that gets closed rather
// than superseded: an issue, or a memory of another kind whose title says it
// is waiting on something. Only these are anchored, aged out of the lead's
// recap, merged as duplicates or tidied.
func OpenItem(m Memory) bool {
	switch m.Kind {
	case KindIssue:
		return true
	case KindProject, KindEpisodic:
		return openItemPattern.MatchString(m.Title)
	}
	return false
}

// setAnchors records a memory's anchors, replacing any it had.
func (s *Store) setAnchors(ctx context.Context, project, id string, anchors []Anchor) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_anchors WHERE project = ? AND memory_id = ?`, project, id); err != nil {
		return err
	}
	for _, a := range anchors {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO memory_anchors (project, memory_id, kind, value) VALUES (?, ?, ?, ?)`,
			project, id, a.Kind, a.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Anchors are what a memory was anchored to when it was written.
func (s *Store) Anchors(ctx context.Context, project, id string) ([]Anchor, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	all, err := s.anchorsOf(ctx, project, []string{id})
	return all[id], err
}

func (s *Store) anchorsOf(ctx context.Context, project string, ids []string) (map[string][]Anchor, error) {
	out := map[string][]Anchor{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{project}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT memory_id, kind, value FROM memory_anchors WHERE project = ? AND memory_id IN (`+placeholders(len(ids))+`)
		 ORDER BY rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var a Anchor
		if err := rows.Scan(&id, &a.Kind, &a.Value); err != nil {
			return nil, err
		}
		out[id] = append(out[id], a)
	}
	return out, rows.Err()
}

// Anchored is a live open item with what would close it.
type Anchored struct {
	Memory  Memory
	Anchors []Anchor
}

// Closers are the anchors that can close it.
func (a Anchored) Closers() []Anchor {
	var out []Anchor
	for _, an := range a.Anchors {
		if an.Closes() {
			out = append(out, an)
		}
	}
	return out
}

// OpenAnchored are the project's live memories with at least one anchor that
// can close them: what the daemon has to look up.
func (s *Store) OpenAnchored(ctx context.Context, project string) ([]Anchored, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	memories, err := s.queryMemories(ctx, `WHERE m.project = ? AND `+live+` AND EXISTS (
		SELECT 1 FROM memory_anchors a WHERE a.project = m.project AND a.memory_id = m.id AND a.kind IN (?, ?, ?))
		ORDER BY m.created_at ASC, m.rowid ASC LIMIT ?`, project, AnchorPR, AnchorBranch, AnchorQuestion, maxLiveMemories)
	if err != nil {
		return nil, err
	}
	anchors, err := s.anchorsOf(ctx, project, MemoryIDs(memories))
	if err != nil {
		return nil, err
	}
	out := make([]Anchored, 0, len(memories))
	for _, m := range memories {
		out = append(out, Anchored{Memory: m, Anchors: anchors[m.ID]})
	}
	return out, nil
}

// Closed is what the daemon found out about one anchor: it is over, when, and
// in a word how ("merged", "closed", "answered").
type Closed struct {
	At  time.Time
	How string
}

// ClosedBefore is how long before a memory was written an anchor may have
// closed and still count as what the memory was waiting on. A memory is
// usually written after the fact — a distillation reads events hours old —
// so "PR #240 is awaiting merge", written at six from an event at two, may
// name a pull request merged at four. Past this margin a closed anchor is
// history the memory is citing ("a regression from #200"), not what it is
// waiting on, and it neither closes nor holds the memory open.
const ClosedBefore = 12 * time.Hour

// ResolveAnchored closes every live open item all of whose pull requests,
// branches and questions are over, and answers what it closed. closed holds
// only the anchors known to be over; an anchor missing from it is open, or
// couldn't be looked up, and either way keeps its memory open.
//
// All of them, not any: "#234 then #237 (stacked)" is waiting on both.
func (s *Store) ResolveAnchored(ctx context.Context, project string, closed map[Anchor]Closed) ([]Memory, error) {
	items, err := s.OpenAnchored(ctx, project)
	if err != nil {
		return nil, err
	}
	var out []Memory
	for _, it := range items {
		why, ok := closedBy(it, closed)
		if !ok {
			continue
		}
		m, err := s.ResolveMemory(ctx, project, it.Memory.ID, why)
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, nil
}

// closedBy decides one item: whether everything it waits on is over, and the
// line resolved_by says so in.
func closedBy(it Anchored, closed map[Anchor]Closed) (string, bool) {
	written := it.Memory.CreatedAt
	var reasons []string
	waiting := 0
	for _, a := range it.Closers() {
		c, ok := closed[a]
		if ok && !c.At.IsZero() && c.At.Before(written.Add(-ClosedBefore)) {
			continue // closed long before this was written: history it cites
		}
		waiting++
		if !ok {
			return "", false
		}
		reasons = append(reasons, a.String()+" "+c.How)
	}
	if waiting == 0 {
		return "", false
	}
	return truncateTo("auto: "+strings.Join(reasons, ", "), MaxTitleLen), true
}

// MentionAnchors says that something happened to these anchors — an agent's
// pull request broke, a question was answered — and marks the open items
// that name them as mentioned, which keeps them in the lead's "Still open".
// It never fails what called it.
func (s *Store) MentionAnchors(ctx context.Context, project string, at time.Time, anchors ...Anchor) error {
	var closers []Anchor
	for _, a := range anchors {
		if a.Closes() {
			closers = append(closers, a)
		}
	}
	if len(closers) == 0 {
		return nil
	}
	conds := make([]string, 0, len(closers))
	args := []any{at.UnixMilli(), project, project}
	for _, a := range closers {
		conds = append(conds, "(a.kind = ? AND a.value = ?)")
		args = append(args, a.Kind, a.Value)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE memories SET mentioned_at = max(mentioned_at, ?)
		WHERE project = ? AND resolved_at = 0 AND id IN (
			SELECT a.memory_id FROM memory_anchors a WHERE a.project = ? AND (`+strings.Join(conds, " OR ")+`))`, args...)
	return err
}

// anchorsFor is what a new memory is anchored to: the ones its writer named,
// and the ones its text names, for an open item; nothing for a fact.
func anchorsFor(m Memory) []Anchor {
	if !OpenItem(m) && len(m.Anchors) == 0 {
		return nil
	}
	var all []Anchor
	for _, a := range m.Anchors {
		if a, ok := normalAnchor(a); ok && !slices.Contains(all, a) {
			all = append(all, a)
		}
	}
	for _, a := range ExtractAnchors(m.Title + "\n" + m.Content) {
		if !slices.Contains(all, a) {
			all = append(all, a)
		}
	}
	return firstN(all, maxAnchors)
}

// normalAnchor tidies an anchor a writer named — a model may say "#234" for a
// pull request, or a branch with a stray space — and refuses one of no known
// kind.
func normalAnchor(a Anchor) (Anchor, bool) {
	a.Kind = strings.ToLower(strings.TrimSpace(a.Kind))
	a.Value = strings.TrimSpace(a.Value)
	switch a.Kind {
	case AnchorPR:
		n, err := strconv.Atoi(strings.TrimPrefix(a.Value, "#"))
		if err != nil || n <= 0 {
			return Anchor{}, false
		}
		a.Value = strconv.Itoa(n)
	case AnchorQuestion:
		a.Value = strings.ToLower(a.Value)
	case AnchorBranch, AnchorPath, AnchorSymbol:
	default:
		return Anchor{}, false
	}
	if a.Value == "" || len(a.Value) > MaxTitleLen || strings.ContainsAny(a.Value, " \t\n") {
		return Anchor{}, false
	}
	return a, true
}

// ParseAnchor reads one anchor as a writer would type it: "#234", "PR 234",
// "branch agentbox/fix-x", "question 1a2b3c4d", a path, or anything
// ExtractAnchors recognises. It answers false for what names nothing.
func ParseAnchor(s string) (Anchor, bool) {
	s = strings.TrimSpace(s)
	if kind, value, ok := strings.Cut(s, ":"); ok {
		if a, ok := normalAnchor(Anchor{Kind: kind, Value: value}); ok {
			return a, true
		}
	}
	found := ExtractAnchors(s)
	if len(found) == 0 {
		if strings.Contains(s, "/") && branchLike(s) && !strings.ContainsAny(s, " \t") {
			return Anchor{Kind: AnchorBranch, Value: s}, true
		}
		return Anchor{}, false
	}
	return found[0], true
}
