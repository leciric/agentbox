package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Consolidation is what keeps a project's memory from being only bigger than
// it was yesterday ([D76](../../docs/implementation/decisions.md#d76)).
//
// Events are history and stay. Memories are the distilled judgement, and left
// alone they only grow: every compaction writes more of them, nothing merges
// two that say the same thing, and a problem fixed in March is still an open
// issue in September. Two passes answer that, and only one of them needs a
// model.
//
//   - The mechanical pass is this file: near-duplicate candidates, importance
//     decay, and the one supersession safe enough to make without judgement.
//     It reads and writes SQLite and nothing else, so it can run on a ticker
//     and costs nothing.
//   - The distillation pass is the daemon's (internal/daemon/distill.go): a
//     window of raw events and the current live memories, handed to the
//     project chat's own session, which answers with what to remember, what
//     to replace and what to close.
//
// Both record what they did in consolidation_passes, because the point of all
// this is spending fewer tokens and a saving nobody can see is a saving
// nobody believes.

// Pass kinds.
const (
	// PassMechanical is the no-model pass: duplicates, decay, exact merges.
	PassMechanical = "mechanical"
	// PassDistill is the one that asks a model to turn events into memories.
	PassDistill = "distill"
)

// DefaultDuplicateThreshold is how alike two titles have to be before the
// mechanical pass calls them near-duplicates, as a Jaccard index over their
// normalised word sets: the words they share, over the words they use
// between them.
//
// 0.7 is where it sits because Jaccard punishes one title for saying more
// than the other, which is exactly the case that must not be merged. Two
// worked examples, both after the normalising titleTokens does:
//
//   - "Agent briefs are capped at 3000 words" against "The agent brief is
//     capped at 3000 words" scores 1.0. Two people wrote down one fact; the
//     pair is worth somebody's attention.
//   - "The API listens on 7777" against "The API listens on 7777 in
//     development and on 8080 in CI" scores 0.5. Related, and the second is
//     not a duplicate of the first — it is a correction somebody should read.
//
// Between them there is a wide gap, and 0.7 sits in it. Nothing is deleted at
// any threshold: a candidate is a pair a person or a distillation might
// merge, and until one does, both memories stay live.
const DefaultDuplicateThreshold = 0.7

// DefaultDecayAfter is how long a memory of the kinds that age goes
// unreferenced before it loses a point of importance. A month is roughly the
// life of a piece of work: an episodic memory nothing has looked at in that
// time is background, and an issue nobody has hit in that time is probably
// not the thing that will bite the next agent.
const DefaultDecayAfter = 30 * 24 * time.Hour

// DecayingKinds are the kinds whose importance ages. Project facts and
// decisions never decay: a convention doesn't become less true because nobody
// searched for it, and a decision nobody re-reads is exactly the decision the
// next agent is about to undo. Those two are corrected by superseding them,
// which is a judgement, not a timer.
var DecayingKinds = []string{KindEpisodic, KindIssue}

// MaxDistillEvents bounds the window a distillation reads in one pass. It is
// the cap that makes the watermark matter: a project that gathered ten
// thousand events while the daemon was off gets them in windows of this,
// oldest first, rather than in one prompt nothing could fit.
const MaxDistillEvents = 300

// maxLiveMemories bounds what the mechanical pass loads. Past this a project
// has a different problem than duplicate titles, and the pass is quadratic in
// what it holds.
const maxLiveMemories = 2000

// maxDuplicatePairs bounds the candidate list. A project whose memories all
// read alike would otherwise produce a quadratic number of pairs, and a list
// nobody could act on is not a longer list — it is a different problem.
const maxDuplicatePairs = 200

// Pass is what one consolidation did: what it read, what it wrote, what it
// cost. A few integer columns, not a metrics system — enough for the app to
// say that a thousand events became twenty memories.
type Pass struct {
	ID      string    `json:"id"`
	Project string    `json:"project"`
	Kind    string    `json:"kind"` // PassMechanical or PassDistill
	At      time.Time `json:"at"`
	// Duration is how long the pass took. For a distillation most of it is
	// the model thinking.
	Duration time.Duration `json:"duration"`

	EventsRead         int `json:"eventsRead"`
	MemoriesWritten    int `json:"memoriesWritten"`
	MemoriesSuperseded int `json:"memoriesSuperseded"`
	MemoriesResolved   int `json:"memoriesResolved"`
	MemoriesDecayed    int `json:"memoriesDecayed"`
	DuplicatesFound    int `json:"duplicatesFound"`

	// InputBytes and OutputBytes are what a distillation sent and what came
	// back. AgentBox never sees a bill — a lead's tokens are the user's own
	// subscription, through whichever tool the project runs — so this is the
	// honest measure of a pass's cost: the size of the prompt and the answer.
	// Both are 0 for a mechanical pass, which is the point of having one.
	InputBytes  int `json:"inputBytes"`
	OutputBytes int `json:"outputBytes"`

	// Model is what the pass actually ran on, which is not always what the
	// project asked for: a cheap model its account won't run, or a session
	// that wouldn't start, falls back to the project chat's own model rather
	// than losing the consolidation (D78). Empty means the chat's own
	// session, or a mechanical pass, which runs on nothing at all.
	Model string `json:"model,omitempty"`

	// ThroughEventID and ThroughAt are how far a distillation got. The newest
	// successful distillation is the project's watermark: there is no
	// separate table for it, because a log of passes already records it and
	// two records of one fact are one too many.
	ThroughEventID string    `json:"throughEventId,omitempty"`
	ThroughAt      time.Time `json:"throughAt,omitzero,omitempty"`

	// Error is why the pass gave up, when it did. A failed pass is recorded
	// rather than hidden, and never advances the watermark.
	Error string `json:"error,omitempty"`
}

// Stats are a project's consolidation, totalled: how much raw history there
// is, how little of it the memories are, and how much the passes have cost.
// The compression ratio the app shows is Events over Memories; this returns
// both rather than the division, because a project with no memories yet
// should read as "nothing distilled", not as an infinity.
type Stats struct {
	Project string `json:"project"`
	// Setting is the project's consolidation window, copied in by the daemon
	// so one response says both what happened and what is configured.
	Setting int `json:"setting"`

	Events     int `json:"events"`     // every event the project has recorded
	Memories   int `json:"memories"`   // live memories now
	Superseded int `json:"superseded"` // memories a later one replaced
	Resolved   int `json:"resolved"`   // memories closed with no replacement
	Duplicates int `json:"duplicates"` // near-duplicate pairs waiting on a decision
	Pending    int `json:"pending"`    // events since the watermark, not yet distilled

	Passes             int `json:"passes"`
	EventsRead         int `json:"eventsRead"`
	MemoriesWritten    int `json:"memoriesWritten"`
	MemoriesSuperseded int `json:"memoriesSuperseded"`
	MemoriesResolved   int `json:"memoriesResolved"`
	MemoriesDecayed    int `json:"memoriesDecayed"`
	InputBytes         int `json:"inputBytes"`
	OutputBytes        int `json:"outputBytes"`

	LastPass  time.Time `json:"lastPass,omitzero,omitempty"`
	Watermark time.Time `json:"watermark,omitzero,omitempty"`
}

// Duplicate is one pair of live memories of a kind whose titles say close to
// the same thing. Memory is the newer of the two.
type Duplicate struct {
	Memory     Memory    `json:"memory"`
	Of         Memory    `json:"of"`
	Similarity float64   `json:"similarity"`
	FoundAt    time.Time `json:"foundAt"`
}

// ConsolidateOptions tune the mechanical pass. A zero value is the defaults,
// which is what the daemon passes.
type ConsolidateOptions struct {
	// DuplicateThreshold is the Jaccard index two titles must reach to be
	// called near-duplicates; DefaultDuplicateThreshold when 0.
	DuplicateThreshold float64
	// DecayAfter is how long a decaying kind goes unreferenced before it
	// loses a point; DefaultDecayAfter when 0. A negative value switches
	// decay off, which is what the tests for the other half use.
	DecayAfter time.Duration
	// Now is the moment the pass runs at, for tests that would otherwise have
	// to wait a month. Zero is time.Now().
	Now time.Time
}

func (o ConsolidateOptions) threshold() float64 {
	if o.DuplicateThreshold <= 0 {
		return DefaultDuplicateThreshold
	}
	return o.DuplicateThreshold
}

func (o ConsolidateOptions) now() time.Time {
	if o.Now.IsZero() {
		return time.Now()
	}
	return o.Now
}

// Consolidate runs the mechanical pass over a project's live memories and
// records what it did. It calls no model, so it can run as often as anything
// likes; what it changes is deliberately the small set of things that need no
// judgement.
//
//  1. Near-duplicate candidates are found and written down. Nothing is merged
//     on a score alone: a title is 200 bytes of somebody's summary, and two
//     summaries that rhyme are not the same fact.
//  2. Where two memories of one kind have the same title and the newer one's
//     content contains the older's word for word, the newer supersedes the
//     older. That is the one merge with nothing to lose: every word of the
//     old memory is still being said.
//  3. Importance decays, a point at a time and floored at 1, for episodic and
//     issue memories that nothing has referenced within DecayAfter. The decay
//     is stamped, so an hourly pass doesn't take a point every hour.
func (s *Store) Consolidate(ctx context.Context, project string, opts ConsolidateOptions) (Pass, error) {
	if err := requireProject(project); err != nil {
		return Pass{}, err
	}
	started := time.Now()
	now := opts.now()
	pass := Pass{Project: project, Kind: PassMechanical, At: now}

	live, err := s.liveMemories(ctx, project)
	if err != nil {
		return Pass{}, err
	}
	merged, pairs := compare(live, opts.threshold())
	for _, m := range merged {
		if err := s.SupersedeMemory(ctx, project, m.older, m.newer); err != nil {
			// A pair that can't be merged — the newer already replaces
			// something else — is left as a candidate rather than forced.
			pairs = append(pairs, duplicatePair{newer: m.newer, older: m.older, score: 1})
			continue
		}
		pass.MemoriesSuperseded++
	}
	if err := s.recordDuplicates(ctx, project, pairs, now); err != nil {
		return Pass{}, err
	}
	pass.DuplicatesFound = len(pairs)

	if opts.DecayAfter >= 0 {
		after := opts.DecayAfter
		if after == 0 {
			after = DefaultDecayAfter
		}
		decayed, err := s.decay(ctx, project, now, after)
		if err != nil {
			return Pass{}, err
		}
		pass.MemoriesDecayed = decayed
	}

	pass.Duration = time.Since(started)
	return s.RecordPass(ctx, pass)
}

// liveMemories are every memory of the project that still counts, oldest
// first. Memories caps at MaxLimit because it answers a model; this answers a
// pass, which has to see all of them to compare them.
func (s *Store) liveMemories(ctx context.Context, project string) ([]Memory, error) {
	return s.queryMemories(ctx, `WHERE m.project = ? AND `+live+
		` ORDER BY m.created_at ASC, m.rowid ASC LIMIT ?`, project, maxLiveMemories)
}

// duplicatePair is two live memories of one kind whose titles are close.
type duplicatePair struct {
	newer, older string
	score        float64
}

// compare is the whole of near-duplicate detection, and it is deliberately
// only string work: no model, no embedding, no index to keep warm.
//
// A title is normalised to a set of words — lower-cased, split on anything
// that isn't a letter or a digit, single characters dropped — and two titles
// of the same kind are scored by Jaccard: shared words over all their words.
// Comparing only within a kind is what keeps "the API listens on 7777" (a
// fact) apart from "the API stopped listening on 7777" (an issue), which are
// close in words and opposite in meaning.
//
// It returns the pairs safe to merge outright and the pairs worth flagging.
// memories must be oldest first, which is how liveMemories reads them.
func compare(memories []Memory, threshold float64) (merge []duplicatePair, candidates []duplicatePair) {
	tokens := make([]map[string]bool, len(memories))
	for i, m := range memories {
		tokens[i] = titleTokens(m.Title)
	}
	// gone are the memories this pass has already merged away: one of them
	// must not go on to be flagged as a duplicate of a third.
	gone := map[int]bool{}
	for i := range memories {
		for j := i + 1; j < len(memories); j++ {
			if gone[i] || gone[j] || memories[i].Kind != memories[j].Kind {
				continue
			}
			// j is the newer of the two: liveMemories is oldest first.
			if sameTitle(memories[i].Title, memories[j].Title) && contains(memories[j].Content, memories[i].Content) {
				merge = append(merge, duplicatePair{newer: memories[j].ID, older: memories[i].ID, score: 1})
				gone[i] = true
				continue
			}
			if score := jaccard(tokens[i], tokens[j]); score >= threshold && len(candidates) < maxDuplicatePairs {
				candidates = append(candidates, duplicatePair{newer: memories[j].ID, older: memories[i].ID, score: score})
			}
		}
	}
	return merge, candidates
}

// sameTitle is the exact match the one automatic merge needs: the same words
// in the same order, ignoring case and however the whitespace fell. Anything
// looser is a candidate, not a merge.
func sameTitle(a, b string) bool {
	return strings.EqualFold(collapse(a), collapse(b))
}

// contains reports whether the newer memory says every word of the older one,
// in order. That is the test that makes the merge lossless: what is about to
// stop coming back from a search is still being said by the memory that
// replaces it. An older memory with no content at all passes, because a
// memory that said nothing loses nothing.
func contains(newer, older string) bool {
	return strings.Contains(strings.ToLower(collapse(newer)), strings.ToLower(collapse(older)))
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// titleWords are the words that say nothing about whether two titles mean the
// same thing. Without them "Agent briefs are capped at 3000 words" and "The
// agent brief is capped at 3000 words" score 0.5 on their grammar rather than
// 1.0 on their meaning, and a threshold low enough to catch that pair is low
// enough to catch every pair that shares a subject.
var titleStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "in": true, "on": true, "at": true, "of": true, "to": true,
	"and": true, "or": true, "it": true, "its": true, "for": true, "with": true, "from": true,
	"that": true, "this": true, "has": true, "have": true, "by": true, "as": true, "into": true,
}

// titleTokens is a title as the set of words worth comparing: lower case,
// split on anything that isn't a letter or a digit, stop words dropped, and
// a trailing plural "s" taken off so "briefs" and "brief" are one word.
//
// The stemming is deliberately the crudest thing that works. Anything more —
// a real stemmer, a synonym list, an embedding — is a dependency and a tuning
// knob, for a comparison whose whole output is "somebody should look at these
// two". The cost of being wrong here is a pair in a list, not a lost memory.
func titleTokens(title string) map[string]bool {
	out := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(word)) < 2 || titleStopWords[word] {
			continue
		}
		out[singular(word)] = true
	}
	return out
}

// singular takes a trailing plural "s" off a word long enough for it to be
// one. "ss" is left alone: "class" is not a plural of "clas".
func singular(word string) string {
	if len(word) > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
		return string(word[:len(word)-1])
	}
	return string(word)
}

// jaccard is the shared words over all the words. Two titles with no words
// between them score 0 rather than dividing by nothing.
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for word := range a {
		if b[word] {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

// recordDuplicates replaces the project's candidate list with this pass's.
// Rewriting it whole is what keeps it honest: a pair somebody has since
// merged, superseded or resolved stops being listed without anything having
// to notice that it did.
func (s *Store) recordDuplicates(ctx context.Context, project string, pairs []duplicatePair, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_duplicates WHERE project = ?`, project); err != nil {
		return err
	}
	for _, p := range pairs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO memory_duplicates (project, memory_id, duplicate_of, similarity, found_at)
			 VALUES (?, ?, ?, ?, ?)`,
			project, p.newer, p.older, int(p.score*100+0.5), now.UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Duplicates are the near-duplicate pairs the last mechanical pass found,
// closest first, with both memories read back. A pair whose memories have
// stopped being live is left out rather than deleted: the next pass rewrites
// the list anyway.
func (s *Store) Duplicates(ctx context.Context, project string) ([]Duplicate, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT memory_id, duplicate_of, similarity, found_at FROM memory_duplicates
		 WHERE project = ? ORDER BY similarity DESC, found_at DESC LIMIT ?`, project, MaxLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type ref struct {
		newer, older string
		score        int
		at           int64
	}
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.newer, &r.older, &r.score, &r.at); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Duplicate, 0, len(refs))
	for _, r := range refs {
		newer, err := s.Memory(ctx, project, r.newer)
		if err != nil {
			continue
		}
		older, err := s.Memory(ctx, project, r.older)
		if err != nil {
			continue
		}
		if !newer.Live() || !older.Live() {
			continue
		}
		out = append(out, Duplicate{Memory: newer, Of: older, Similarity: float64(r.score) / 100, FoundAt: attime(r.at)})
	}
	return out, nil
}

// decay takes a point of importance off the memories that age and that
// nothing has looked at. "Nothing has looked at" is referenced_at, which the
// things that put memories in front of a model set (MarkReferenced): a
// memory the recap keeps handing to the chat is a memory the project is
// using, whatever its date says.
//
// The floor is MinImportance, so decay hides nothing and deletes nothing — it
// only changes what a context builder drops first when it can't keep
// everything. decayed_at is stamped so the next pass, an hour later, doesn't
// take another point.
func (s *Store) decay(ctx context.Context, project string, now time.Time, after time.Duration) (int, error) {
	cutoff := now.Add(-after).UnixMilli()
	res, err := s.db.ExecContext(ctx,
		`UPDATE memories SET importance = importance - 1, decayed_at = ?, updated_at = ?
		 WHERE project = ? AND kind IN (`+placeholders(len(DecayingKinds))+`)
		   AND importance > ?
		   AND resolved_at = 0
		   AND NOT EXISTS (SELECT 1 FROM memories r WHERE r.supersedes_id = memories.id)
		   AND max(created_at, referenced_at, decayed_at) <= ?`,
		append(append([]any{now.UnixMilli(), now.UnixMilli(), project}, kindArgs()...), MinImportance, cutoff)...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func kindArgs() []any {
	args := make([]any, 0, len(DecayingKinds))
	for _, k := range DecayingKinds {
		args = append(args, k)
	}
	return args
}

// MarkReferenced says that something put these memories in front of a model.
// It is what stops decay from quietly demoting the memories a project reads
// every day, and it is called by the readers that actually spend context on
// them — the lead's recap, and a distillation's window — rather than by
// Search, which is also how a person browses.
//
// It never fails the read that called it: an id that isn't this project's is
// skipped by the WHERE clause, and an empty list does nothing.
func (s *Store) MarkReferenced(ctx context.Context, project string, ids ...string) error {
	if err := requireProject(project); err != nil {
		return err
	}
	ids = nonEmpty(ids)
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, time.Now().UnixMilli(), project)
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE memories SET referenced_at = ? WHERE project = ? AND id IN (`+placeholders(len(ids))+`)`, args...)
	return err
}

func nonEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}

// ResolveMemory closes a memory without inventing a replacement for it. It is
// for the issue that stopped being true: the bug was fixed, the flaky test
// was deleted, the thing nobody had got to turned out not to matter.
//
// Superseding would be the wrong shape for that. A superseded memory points
// at what is true instead, and an issue that simply went away has no instead;
// making one up would put a memory of something that never happened in front
// of every future agent, and a memory superseded by itself is refused by
// AddMemory for the same reason. So resolution is its own fact, stored on the
// memory as resolved_at — the only record of it, with no derived truth beside
// it to contradict.
//
// A resolved memory stops coming back from Memories and Search, exactly as a
// superseded one does, and stays readable by id so the project's history of
// being wrong is still followable.
func (s *Store) ResolveMemory(ctx context.Context, project, id, why string) (Memory, error) {
	if err := requireProject(project); err != nil {
		return Memory{}, err
	}
	m, err := s.Memory(ctx, project, id)
	if err != nil {
		return Memory{}, err
	}
	if m.Resolved() {
		return m, nil // already closed: saying so again changes nothing
	}
	if m.Superseded {
		return Memory{}, fmt.Errorf("memory %s was already replaced by a later one: there is nothing left to close", id)
	}
	reason, err := text("the reason a memory was resolved", why, MaxTitleLen)
	if err != nil {
		return Memory{}, err
	}
	now := time.Now()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memories SET resolved_at = ?, resolved_by = ?, updated_at = ? WHERE project = ? AND id = ?`,
		now.UnixMilli(), reason, now.UnixMilli(), project, id); err != nil {
		return Memory{}, err
	}
	return s.Memory(ctx, project, id)
}

// RecordPass writes down what a consolidation did. The daemon calls it for a
// distillation; Consolidate calls it for itself.
func (s *Store) RecordPass(ctx context.Context, p Pass) (Pass, error) {
	if err := requireProject(p.Project); err != nil {
		return Pass{}, err
	}
	if p.Kind != PassMechanical && p.Kind != PassDistill {
		return Pass{}, fmt.Errorf("a consolidation pass is %s or %s, not %q", PassMechanical, PassDistill, p.Kind)
	}
	if p.ID == "" {
		p.ID = newID("pass")
	}
	if p.At.IsZero() {
		p.At = time.Now()
	}
	p.At = stamp(p.At)
	p.ThroughAt = stamp(p.ThroughAt)
	p.Error = truncateTo(p.Error, MaxTitleLen)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO consolidation_passes (id, project, kind, at, duration_ms, events_read, memories_written,
			memories_superseded, memories_resolved, memories_decayed, duplicates_found, input_bytes, output_bytes,
			through_event, through_at, error, model)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Project, p.Kind, p.At.UnixMilli(), p.Duration.Milliseconds(), p.EventsRead, p.MemoriesWritten,
		p.MemoriesSuperseded, p.MemoriesResolved, p.MemoriesDecayed, p.DuplicatesFound, p.InputBytes, p.OutputBytes,
		p.ThroughEventID, millis(p.ThroughAt), p.Error, p.Model)
	if err != nil {
		return Pass{}, err
	}
	return p, nil
}

// Passes are a project's consolidations, newest first.
func (s *Store) Passes(ctx context.Context, project string, limit int) ([]Pass, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project, kind, at, duration_ms, events_read, memories_written, memories_superseded,
			memories_resolved, memories_decayed, duplicates_found, input_bytes, output_bytes,
			through_event, through_at, error, model
		 FROM consolidation_passes WHERE project = ? ORDER BY at DESC, rowid DESC LIMIT ?`, project, limitOf(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pass
	for rows.Next() {
		var p Pass
		var at, duration, through int64
		if err := rows.Scan(&p.ID, &p.Project, &p.Kind, &at, &duration, &p.EventsRead, &p.MemoriesWritten,
			&p.MemoriesSuperseded, &p.MemoriesResolved, &p.MemoriesDecayed, &p.DuplicatesFound,
			&p.InputBytes, &p.OutputBytes, &p.ThroughEventID, &through, &p.Error, &p.Model); err != nil {
			return nil, err
		}
		p.At, p.ThroughAt, p.Duration = attime(at), attime(through), time.Duration(duration)*time.Millisecond
		out = append(out, p)
	}
	return out, rows.Err()
}

// Watermark is how far distillation has read: the moment of the newest event
// the newest successful distillation folded in. A project that has never been
// distilled has a zero watermark, so its first pass reads its oldest events.
//
// There is no watermark table. The pass log is the record, which is the same
// discipline supersedes_id follows: one fact, one place, nothing to disagree
// with. A pass that failed wrote no through_at, so it doesn't move it.
func (s *Store) Watermark(ctx context.Context, project string) (time.Time, error) {
	if err := requireProject(project); err != nil {
		return time.Time{}, err
	}
	// max() over no rows is one row of NULL, which is a project that has
	// never been distilled rather than anything having gone wrong.
	var at *int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT max(through_at) FROM consolidation_passes WHERE project = ? AND kind = ? AND error = ''`,
		project, PassDistill).Scan(&at); err != nil {
		return time.Time{}, err
	}
	if at == nil {
		return time.Time{}, nil
	}
	return attime(*at), nil
}

// distillTypes are the event types a distillation never reads: its own. See
// EventMemoryConsolidated.
var distillTypes = []string{EventMemoryConsolidated}

// PendingEvents is how many events the project has gathered since the
// watermark. It is the cheap question a lead chokepoint asks every turn, so
// it counts rather than reads.
func (s *Store) PendingEvents(ctx context.Context, project string, since time.Time) (int, error) {
	if err := requireProject(project); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM events WHERE project = ? AND at > ? AND type NOT IN (`+placeholders(len(distillTypes))+`)`,
		append([]any{project, millis(since)}, distillArgs()...)...).Scan(&n)
	return n, err
}

// EventsSince is the window a distillation reads: the project's events after
// the watermark, oldest first, capped. Oldest first because the model is
// being asked to follow a stretch of history, and history read backwards is a
// different story.
func (s *Store) EventsSince(ctx context.Context, project string, since time.Time, limit int) ([]Event, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxDistillEvents {
		limit = MaxDistillEvents
	}
	args := append([]any{project, millis(since)}, distillArgs()...)
	return s.queryEvents(ctx,
		`WHERE events.project = ? AND events.at > ? AND events.type NOT IN (`+placeholders(len(distillTypes))+`)
		 ORDER BY events.at ASC, events.rowid ASC LIMIT ?`, append(args, limit)...)
}

func distillArgs() []any {
	args := make([]any, 0, len(distillTypes))
	for _, t := range distillTypes {
		args = append(args, t)
	}
	return args
}

// ConsolidationStats totals a project's consolidation: how much raw history
// it has, how few memories that became, and what the passes cost.
func (s *Store) ConsolidationStats(ctx context.Context, project string) (Stats, error) {
	if err := requireProject(project); err != nil {
		return Stats{}, err
	}
	out := Stats{Project: project}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE project = ?`, project).Scan(&out.Events); err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT
			sum(CASE WHEN `+live+` THEN 1 ELSE 0 END),
			sum(CASE WHEN `+notSuperseded+` THEN 0 ELSE 1 END),
			sum(CASE WHEN m.resolved_at = 0 THEN 0 ELSE 1 END)
		 FROM memories m WHERE m.project = ?`, project).
		Scan(nullInt(&out.Memories), nullInt(&out.Superseded), nullInt(&out.Resolved)); err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM memory_duplicates WHERE project = ?`, project).Scan(&out.Duplicates); err != nil {
		return Stats{}, err
	}
	var last *int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*), sum(events_read), sum(memories_written), sum(memories_superseded), sum(memories_resolved),
			sum(memories_decayed), sum(input_bytes), sum(output_bytes), max(at)
		 FROM consolidation_passes WHERE project = ?`, project).
		Scan(&out.Passes, nullInt(&out.EventsRead), nullInt(&out.MemoriesWritten), nullInt(&out.MemoriesSuperseded),
			nullInt(&out.MemoriesResolved), nullInt(&out.MemoriesDecayed), nullInt(&out.InputBytes),
			nullInt(&out.OutputBytes), &last); err != nil {
		return Stats{}, err
	}
	if last != nil {
		out.LastPass = attime(*last)
	}
	watermark, err := s.Watermark(ctx, project)
	if err != nil {
		return Stats{}, err
	}
	out.Watermark = watermark
	if out.Pending, err = s.PendingEvents(ctx, project, watermark); err != nil {
		return Stats{}, err
	}
	return out, nil
}

// nullInt scans a SUM over no rows, which SQLite answers as NULL, into 0.
func nullInt(into *int) any { return (*zeroInt)(into) }

type zeroInt int

func (z *zeroInt) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*z = 0
	case int64:
		*z = zeroInt(v)
	case float64:
		*z = zeroInt(v)
	default:
		return fmt.Errorf("a count came back as %T", value)
	}
	return nil
}

// truncateTo cuts a string that has to fit a column, marking that it was cut.
func truncateTo(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max-1]) + "…"
}

// MemoryIDs is the ids of several lists of memories at once, for
// MarkReferenced: a recap builds its section out of three or four lists and
// wants to say that it read all of them.
func MemoryIDs(memories ...[]Memory) []string {
	var out []string
	for _, list := range memories {
		for _, m := range list {
			if m.ID != "" {
				out = append(out, m.ID)
			}
		}
	}
	return out
}
