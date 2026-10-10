package memory

import (
	"context"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Keeping open items from outliving what they were about.
//
// An issue memory is closed by an anchor (anchors.go) when it names one, by a
// distillation or a person when they notice, and otherwise by nothing: in one
// real project 93 of 119 issues were still open, most of them long fixed, and
// the lead's recap kept reading them back to the user. Three things here
// answer that without a model:
//
//   - Same-topic issues are merged, by what they say rather than by their
//     titles: "Agent names restart…", "Agent-name reuse bug unfixed" and
//     "Agent names are reused as soon as…" are one problem written down three
//     times. The newest stays; the others are resolved as its duplicates.
//   - An open item nobody has mentioned in StaleAfter drops out of the
//     lead's "Still open". It stays live and searchable: quiet is not fixed.
//   - Tidy, run by the user on a store that is already full of them,
//     resolves every open item older than a cutoff, after the merges.

// StaleAfter is how long an open item may go unmentioned before the lead's
// recap stops carrying it. Three weeks is longer than any piece of work here
// stays open without somebody touching it, and short enough that a fixed bug
// stops being repeated to the user within the month.
const StaleAfter = 21 * 24 * time.Hour

// DefaultTidyAge is how old an open item has to be before `agentbox memory
// tidy` resolves it.
const DefaultTidyAge = 7 * 24 * time.Hour

// ResolvedByTidy is what resolved_by says for an item Tidy closed for its age.
const ResolvedByTidy = "tidied"

// Topic similarity thresholds, over the idf-weighted cosine of two items'
// title and content (topicVector). They were set on fixtures shaped like the
// real project's duplicates (tidy_test.go): restatements of one problem
// scored 0.30 to 0.75, and two problems that share a subject — two bugs in
// agent retirement, agent names reused and agent names truncated — 0.17 at
// most.
const (
	// sameTopicScore is enough on its own.
	sameTopicScore = 0.3
	// sameTitleScore is enough when one title's words are all in the other's
	// ("PRs awaiting the user's merge" in "Open PRs awaiting the user's
	// merge"): the title already says they are about one thing, and the body
	// only has to not contradict it.
	sameTitleScore = 0.12
	// sharedAnchorScore is enough when both wait on the same pull request,
	// branch or question.
	sharedAnchorScore = 0.2
)

// minTitleWords is how many words a title must have for containment to
// count: "Flaky test" is in a great many titles that are about other tests.
const minTitleWords = 3

// Merge is one item folded into another as a duplicate.
type Merge struct {
	Memory Memory  `json:"memory"` // the one resolved
	Into   Memory  `json:"into"`   // the one that stays
	Score  float64 `json:"score"`
	Why    string  `json:"why"`
}

// sameTopic finds the open items that are one problem written down more than
// once, and answers the merges that fold each group into its newest member.
// items must be oldest first. Nothing is written.
func sameTopic(items []Memory, corpus []Memory, anchors map[string][]Anchor) []Merge {
	if len(items) < 2 {
		return nil
	}
	idf := inverseFrequency(corpus)
	vectors := make([]map[string]float64, len(items))
	titles := make([]map[string]bool, len(items))
	for i, m := range items {
		vectors[i] = topicVector(m, idf)
		titles[i] = titleTokens(m.Title)
	}
	// Union-find over the pairs that match, so three restatements of one
	// problem become one group whichever two of them happen to score best.
	parent := make([]int, len(items))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	best := make([]float64, len(items))
	reason := make([]string, len(items))
	for i := range items {
		for j := i + 1; j < len(items); j++ {
			score := cosine(vectors[i], vectors[j])
			why := ""
			switch {
			case score >= sameTopicScore:
				why = "says the same thing"
			case score >= sameTitleScore && titleWithin(titles[i], titles[j]):
				why = "same title"
			case score >= sharedAnchorScore && shareCloser(anchors[items[i].ID], anchors[items[j].ID]):
				why = "waits on the same thing"
			default:
				continue
			}
			ri, rj := find(i), find(j)
			if ri != rj {
				parent[ri] = rj
			}
			for _, k := range []int{i, j} {
				if score > best[k] {
					best[k], reason[k] = score, why
				}
			}
		}
	}
	// Each group keeps its newest member, which is the latest anybody said
	// about it; items are oldest first, so that is the highest index.
	newest := map[int]int{}
	for i := range items {
		root := find(i)
		if n, ok := newest[root]; !ok || i > n {
			newest[root] = i
		}
	}
	var out []Merge
	for i := range items {
		keep := newest[find(i)]
		if keep == i {
			continue
		}
		out = append(out, Merge{
			Memory: items[i], Into: items[keep], Score: math.Round(best[i]*100) / 100,
			Why: reason[i],
		})
	}
	return out
}

// titleWithin reports whether every word of the shorter title is in the
// longer one, and the shorter says enough to mean something.
func titleWithin(a, b map[string]bool) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(a) < minTitleWords {
		return false
	}
	for w := range a {
		if !b[w] {
			return false
		}
	}
	return true
}

func shareCloser(a, b []Anchor) bool {
	for _, x := range a {
		if x.Closes() && slices.Contains(b, x) {
			return true
		}
	}
	return false
}

// topicWords are the words of a memory's title and content, reduced to a
// crude stem so "reused", "reuse" and "reuses" are one word. Like titleTokens
// it is the simplest thing that works: the cost of a miss is a duplicate that
// stays, and the thresholds above are set against exactly this.
func topicWords(s string) []string {
	var out []string
	for _, word := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(word)) < 2 || titleStopWords[word] || topicStopWords[word] {
			continue
		}
		out = append(out, stem(word))
	}
	return out
}

// topicStopWords are the words of prose that say nothing about what it is
// about, beyond titleStopWords.
var topicStopWords = map[string]bool{
	"but": true, "not": true, "no": true, "so": true, "if": true, "then": true, "than": true, "when": true,
	"which": true, "who": true, "what": true, "can": true, "could": true, "would": true, "should": true,
	"will": true, "do": true, "does": true, "did": true, "there": true, "their": true, "they": true,
	"them": true, "he": true, "she": true, "we": true, "you": true, "i": true, "my": true, "our": true,
	"one": true, "all": true, "any": true, "also": true, "only": true, "still": true, "now": true,
	"after": true, "before": true, "up": true, "out": true, "just": true, "about": true, "more": true,
	"its": true, "it's": true, "had": true, "being": true, "same": true, "new": true, "old": true,
}

func stem(w string) string {
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(w) > len(suffix)+3 && strings.HasSuffix(w, suffix) && !strings.HasSuffix(w, "ss") {
			w = w[:len(w)-len(suffix)]
			break
		}
	}
	if len(w) > 4 && strings.HasSuffix(w, "e") {
		w = w[:len(w)-1]
	}
	return w
}

// inverseFrequency weighs each word by how few of the project's memories use
// it: in a project about agents, "agent" says nothing and "retire" a lot.
func inverseFrequency(corpus []Memory) map[string]float64 {
	df := map[string]int{}
	for _, m := range corpus {
		seen := map[string]bool{}
		for _, w := range topicWords(m.Title + " " + m.Content) {
			if !seen[w] {
				seen[w] = true
				df[w]++
			}
		}
	}
	n := float64(len(corpus))
	out := make(map[string]float64, len(df))
	for w, d := range df {
		out[w] = math.Log(1 + n/float64(d))
	}
	return out
}

// topicVector is a memory as weighted words: its title's count twice, since
// a title is what its writer thought it was about, and a word no other
// memory uses at the weight of the rarest.
func topicVector(m Memory, idf map[string]float64) map[string]float64 {
	rare := 0.0
	for _, v := range idf {
		rare = max(rare, v)
	}
	if rare == 0 {
		rare = 1
	}
	weight := func(w string) float64 {
		if v, ok := idf[w]; ok {
			return v
		}
		return rare
	}
	out := map[string]float64{}
	for _, w := range topicWords(m.Title) {
		out[w] += 2 * weight(w)
	}
	for _, w := range topicWords(m.Content) {
		out[w] += weight(w)
	}
	return out
}

func cosine(a, b map[string]float64) float64 {
	var dot, na, nb float64
	for w, x := range a {
		na += x * x
		if y, ok := b[w]; ok {
			dot += x * y
		}
	}
	for _, y := range b {
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// openItems are the project's live open items, oldest first, and every live
// memory as the corpus their words are weighed against.
func (s *Store) openItems(ctx context.Context, project string) (items, corpus []Memory, err error) {
	corpus, err = s.liveMemories(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range corpus {
		if OpenItem(m) {
			items = append(items, m)
		}
	}
	return items, corpus, nil
}

// MergeDuplicates folds same-topic open items into their newest, and
// answers what it merged. The mechanical pass runs it; Tidy plans it.
func (s *Store) MergeDuplicates(ctx context.Context, project string) ([]Merge, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	items, corpus, err := s.openItems(ctx, project)
	if err != nil {
		return nil, err
	}
	return s.mergeDuplicates(ctx, project, items, corpus, true)
}

func (s *Store) mergeDuplicates(ctx context.Context, project string, items, corpus []Memory, apply bool) ([]Merge, error) {
	if len(items) < 2 {
		return nil, nil
	}
	anchors, err := s.anchorsOf(ctx, project, MemoryIDs(items))
	if err != nil {
		return nil, err
	}
	merges := sameTopic(items, corpus, anchors)
	if !apply {
		return merges, nil
	}
	for _, m := range merges {
		if err := s.mergeInto(ctx, project, m, anchors); err != nil {
			return nil, err
		}
	}
	return merges, nil
}

// mergeInto resolves one duplicate and keeps what it had worth keeping on the
// item that stays: its importance, if higher, the latest mention, its
// confirmations plus this one (confirmed.go), and what would close it, if the
// one that stays names nothing.
func (s *Store) mergeInto(ctx context.Context, project string, m Merge, anchors map[string][]Anchor) error {
	if _, err := s.ResolveMemory(ctx, project, m.Memory.ID, "duplicate of "+m.Into.ID+" ("+m.Why+")"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memories SET importance = max(importance, ?), mentioned_at = max(mentioned_at, ?, ?),
			confirmations = confirmations + 1 + ? WHERE project = ? AND id = ?`,
		m.Memory.Importance, millis(m.Memory.LastMentioned()), millis(m.Into.CreatedAt), m.Memory.Confirmations,
		project, m.Into.ID); err != nil {
		return err
	}
	if len(Anchored{Anchors: anchors[m.Into.ID]}.Closers()) == 0 && len(anchors[m.Memory.ID]) > 0 {
		merged := append(slices.Clone(anchors[m.Into.ID]), anchors[m.Memory.ID]...)
		anchors[m.Into.ID] = firstN(merged, maxAnchors)
		return s.setAnchors(ctx, project, m.Into.ID, anchors[m.Into.ID])
	}
	return nil
}

// TidyOptions say what Tidy does. The zero value is a dry run with the
// default age.
type TidyOptions struct {
	// OlderThan is how long since an open item was last mentioned before
	// Tidy resolves it; DefaultTidyAge when 0.
	OlderThan time.Duration
	// Apply writes what the plan says. Without it Tidy only answers it.
	Apply bool
	// Now is when it runs, for tests.
	Now time.Time
}

// TidyPlan is what Tidy did, or would do.
type TidyPlan struct {
	Applied bool `json:"applied"`
	// Resolved are the open items old enough to close, as they were.
	Resolved []Memory `json:"resolved"`
	// Merged are the duplicates folded into another item.
	Merged []Merge `json:"merged"`
	// Kept is how many open items are left.
	Kept int `json:"kept"`
	// Scrubbed are the rows, of every kind, that still held a secret
	// (scrub.go).
	Scrubbed Scrubbed `json:"scrubbed"`
}

// Tidy cleans a store that is already full of stale open items, in one go and
// without a model: every live open item last mentioned before the cutoff is
// resolved as ResolvedByTidy, then what is left is merged by topic. Facts,
// decisions and discoveries are never touched, and neither is anything
// resolved or superseded already. First, every row of the project stored
// before secrets were removed on the way in has them removed now.
func (s *Store) Tidy(ctx context.Context, project string, opts TidyOptions) (TidyPlan, error) {
	if err := requireProject(project); err != nil {
		return TidyPlan{}, err
	}
	age := opts.OlderThan
	if age <= 0 {
		age = DefaultTidyAge
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-age)
	scrubbed, err := s.scrub(ctx, project, opts.Apply)
	if err != nil {
		return TidyPlan{}, err
	}
	items, corpus, err := s.openItems(ctx, project)
	if err != nil {
		return TidyPlan{}, err
	}
	plan := TidyPlan{Applied: opts.Apply, Resolved: []Memory{}, Merged: []Merge{}, Scrubbed: scrubbed}
	var recent []Memory
	for _, m := range items {
		if m.LastMentioned().Before(cutoff) {
			plan.Resolved = append(plan.Resolved, m)
			continue
		}
		recent = append(recent, m)
	}
	if opts.Apply {
		for _, m := range plan.Resolved {
			if _, err := s.ResolveMemory(ctx, project, m.ID, ResolvedByTidy); err != nil {
				return plan, err
			}
		}
	}
	merges, err := s.mergeDuplicates(ctx, project, recent, corpus, opts.Apply)
	if err != nil {
		return plan, err
	}
	if merges != nil {
		plan.Merged = merges
	}
	plan.Kept = len(recent) - len(plan.Merged)
	return plan, nil
}

// OpenIssues are the live issues something has mentioned since a moment,
// highest Standing first: what the lead's recap carries as "Still open".
func (s *Store) OpenIssues(ctx context.Context, project string, since time.Time, limit int) ([]Memory, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	return s.queryMemories(ctx, `WHERE m.project = ? AND m.kind = ? AND `+live+`
		AND max(m.created_at, m.mentioned_at) >= ?
		ORDER BY `+standingSQL+` DESC, m.importance DESC, m.created_at DESC, m.rowid DESC LIMIT ?`,
		project, KindIssue, millis(since), limitOf(limit))
}
