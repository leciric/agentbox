package memory

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

// Results are one search's answer: the three things a project remembers, each
// ranked on its own and each capped at the limit. They are kept apart rather
// than merged into one ordered list because bm25 scores are only comparable
// within an index, and because what a caller wants from a memory and from an
// event is different.
type Results struct {
	Memories []Memory `json:"memories,omitempty"`
	Events   []Event  `json:"events,omitempty"`
	Reports  []Report `json:"reports,omitempty"`
}

// Empty reports whether the search found nothing at all.
func (r Results) Empty() bool {
	return len(r.Memories) == 0 && len(r.Events) == 0 && len(r.Reports) == 0
}

// Search looks through a project's memories, events and reports, found by
// FTS5's bm25 and then reranked (see rerank, below) against the top of what
// bm25 found. Superseded and resolved memories are left out before either
// step: a search that returns what the project has since decided is wrong,
// or has since closed, is worse than one that returns less.
//
// Every word of the query is matched as a phrase, so exact tokens survive:
// internal/daemon/server.go, 7777 and "connection refused" are found as
// themselves rather than as whatever a stemmer would make of them. Words are
// first required together; the indexes that match nothing that way are then
// filled from a second pass that takes any of them, so a long question still
// answers with its best match instead of nothing.
//
// The any-word pass is per index rather than for the search as a whole. A
// task written out in full — the sort of thing the context builder searches
// with (D75) — often matches the event that recorded it word for word while
// matching no memory at all, and the memories are the part that was worth
// asking for.
func (s *Store) Search(ctx context.Context, project, query string, limit int) (Results, error) {
	limit = limitOf(limit)
	out, err := s.searchBM25(ctx, project, query, rerankPoolFor(limit))
	if err != nil {
		return Results{}, err
	}
	return rerank(out, limit), nil
}

// searchBM25 is bm25's own answer, before anything reorders it: the
// strict-then-loose match against each index, ranked purely by bm25 and
// capped at limit. It is Search's first half, split out so
// rerank_eval_test.go can score it on its own against the reranked Search
// over the same fixtures — which is the only way to say whether reranking
// earned its keep rather than just asserting that it did.
func (s *Store) searchBM25(ctx context.Context, project, query string, limit int) (Results, error) {
	if err := requireProject(project); err != nil {
		return Results{}, err
	}
	all, some := ftsQuery(query)
	if all == "" {
		return Results{}, errors.New("a search needs something to look for")
	}
	out, err := s.search(ctx, project, all, limit)
	if err != nil {
		return Results{}, err
	}
	if some != all && (len(out.Memories) == 0 || len(out.Events) == 0 || len(out.Reports) == 0) {
		loose, err := s.search(ctx, project, some, limit)
		if err != nil {
			return Results{}, err
		}
		// Only where the strict pass found nothing: an index that answered
		// keeps its answer, because every one of those rows has all of the
		// words in it.
		if len(out.Memories) == 0 {
			out.Memories = loose.Memories
		}
		if len(out.Events) == 0 {
			out.Events = loose.Events
		}
		if len(out.Reports) == 0 {
			out.Reports = loose.Reports
		}
	}
	return out, nil
}

func (s *Store) search(ctx context.Context, project, match string, limit int) (Results, error) {
	var out Results
	// Each index is joined by its own name — MATCH and bm25 both want the
	// table, not an alias — and the row columns are all qualified, so the two
	// tables' same-named columns can't collide. A title is worth twice a body,
	// so a memory called after the thing you searched for comes before one
	// that merely mentions it.
	memories, err := s.queryMemories(ctx,
		`JOIN memories_fts ON memories_fts.rowid = m.rowid
		 WHERE memories_fts MATCH ? AND m.project = ? AND `+live+`
		 ORDER BY bm25(memories_fts, 2.0, 1.0) LIMIT ?`, match, project, limit)
	if err != nil {
		return Results{}, searchError(err)
	}
	out.Memories = memories

	events, err := s.queryEvents(ctx,
		`JOIN events_fts ON events_fts.rowid = events.rowid
		 WHERE events_fts MATCH ? AND events.project = ?
		 ORDER BY bm25(events_fts) LIMIT ?`, match, project, limit)
	if err != nil {
		return Results{}, searchError(err)
	}
	out.Events = events

	reports, err := s.queryReports(ctx,
		`JOIN reports_fts ON reports_fts.rowid = agent_reports.rowid
		 WHERE reports_fts MATCH ? AND agent_reports.project = ?
		 ORDER BY bm25(reports_fts) LIMIT ?`, match, project, limit)
	if err != nil {
		return Results{}, searchError(err)
	}
	out.Reports = reports
	return out, nil
}

// ftsQuery turns what a person or a model typed into two FTS5 queries: one
// that requires every word, and one that takes any of them. Each word becomes
// a quoted phrase, which does two things at once — it keeps a filename or an
// error string matching as itself, and it means nothing the caller typed is
// ever read as FTS5 syntax (a stray NOT, * or : is just a word).
func ftsQuery(query string) (all, some string) {
	fields := strings.Fields(query)
	phrases := make([]string, 0, len(fields))
	for _, f := range fields {
		// A quote inside a phrase is escaped by doubling it, which is the
		// only escaping FTS5 string literals have.
		f = strings.ReplaceAll(f, `"`, `""`)
		if strings.TrimSpace(f) == "" {
			continue
		}
		phrases = append(phrases, `"`+f+`"`)
	}
	if len(phrases) == 0 {
		return "", ""
	}
	return strings.Join(phrases, " AND "), strings.Join(phrases, " OR ")
}

// searchError turns SQLite's own words for a malformed MATCH into something a
// caller can act on. Everything the caller typed is quoted, so this should be
// unreachable; if it ever isn't, it shouldn't look like a database fault.
func searchError(err error) error {
	if strings.Contains(err.Error(), "fts5") || strings.Contains(err.Error(), "syntax error") {
		return errors.New("that search couldn't be understood: search for words, a filename or a quoted phrase")
	}
	return err
}

// Reranking. bm25 answers "is this row about the query"; it has never been
// told that a memory a person marked important outranks one nobody rated, or
// that this week beats six months ago. Both are columns the store already
// has, so a reranking pass over bm25's own shortlist can use them without a
// model, an embedding or a second index.
//
// Superseded and resolved memories need no weight here at all: `live`, in the
// query above, keeps them out of the candidate pool before reranking ever
// sees a row, the same way it always kept them out of a bm25-only result. A
// weight for "not superseded" would only ever multiply by one.
const (
	// rerankPool is how many of bm25's own top matches, per index, are
	// eligible to be reordered. A row bm25 ranks outside it is never
	// promoted here however important or recent it is: this reorders bm25's
	// shortlist, it does not compete with the index.
	rerankPool = 40

	// weightBM25 is bm25's own share of the combined score, expressed as the
	// row's position in the shortlist rather than its raw score — bm25's
	// magnitude isn't comparable across queries, but "was it bm25's best
	// match" is. It is the largest weight by a wide margin: the signals below
	// only reorder what bm25 already agreed was relevant.
	weightBM25 = 0.65
	// weightRecency rewards a row from this week over one from months ago.
	// Every kind carries a timestamp bm25 never looks at, so this applies to
	// memories, events and reports alike.
	weightRecency = 0.20
	// weightImportance is the 1 to 5 a person, or a distillation, put on a
	// memory on purpose (see Importance bounds, above) — a column Search has
	// never consulted until now. Memories only: events and reports carry no
	// such judgement.
	weightImportance = 0.10
	// weightKind nudges a `project` fact or a `decision` — true until the
	// project changes — ahead of an `episodic` memory that merely happened,
	// which is what the doc means by a decision from last week beating an
	// episodic memory from six months ago. Memories only.
	weightKind = 0.05

	// recencyHalfLife is how old a row has to be before recency's contribution
	// to its score halves. 14 days: short enough that this week outranks last
	// month, long enough that a decision from three weeks ago still beats one
	// bm25 barely matched.
	recencyHalfLife = 14 * 24 * time.Hour
)

// rerankPoolFor is how many rows bm25 is asked for so there is a shortlist to
// reorder. It is never smaller than what the caller asked for: a caller that
// wants more than rerankPool still gets bm25's full answer, just reordered.
func rerankPoolFor(limit int) int {
	if limit > rerankPool {
		return limit
	}
	return rerankPool
}

// rerank reorders every list in a bm25-ranked Results by the combined score
// and cuts each back to limit. bm25's own order survives as one of the
// signals being combined (see weightBM25), so a row bm25 was confident about
// is only displaced by one recent or important enough to be worth it.
func rerank(r Results, limit int) Results {
	r.Memories = rerankRows(r.Memories, limit, func(m Memory) (time.Time, float64) {
		return m.CreatedAt, weightRecency*recencyScore(m.CreatedAt) +
			weightImportance*importanceScore(m.Importance) +
			weightKind*kindScore(m.Kind)
	})
	r.Events = rerankRows(r.Events, limit, func(e Event) (time.Time, float64) {
		return e.At, 0
	})
	r.Reports = rerankRows(r.Reports, limit, func(rp Report) (time.Time, float64) {
		return rp.CreatedAt, 0
	})
	return r
}

// rerankRows scores rows already in bm25 order and returns the top limit by
// combined score. extra is whatever a row's own kind of signal contributes
// beyond bm25 and recency — importance and kind for a memory, nothing for an
// event or a report, which the store has no such column for.
func rerankRows[T any](rows []T, limit int, extra func(T) (time.Time, float64)) []T {
	if len(rows) == 0 {
		return rows
	}
	type scored struct {
		row   T
		score float64
	}
	n := len(rows)
	ranked := make([]scored, n)
	for i, row := range rows {
		at, extraScore := extra(row)
		ranked[i] = scored{row, weightBM25*bm25RankScore(i, n) + weightRecency*recencyScore(at) + extraScore}
	}
	// Stable so two rows bm25 (and everything else) scores identically keep
	// the order bm25 gave them, rather than swapping on every call.
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if limit > n {
		limit = n
	}
	out := make([]T, limit)
	for i := range out {
		out[i] = ranked[i].row
	}
	return out
}

// bm25RankScore turns a row's position in bm25's own order into a score from
// 1 (bm25's best match) down to 0 (the worst of the pool it was asked for),
// since bm25's raw magnitude means nothing outside the query that produced it
// but its ordering does.
func bm25RankScore(rank, poolSize int) float64 {
	if poolSize <= 1 {
		return 1
	}
	return 1 - float64(rank)/float64(poolSize-1)
}

// recencyScore is 1 for a row from right now, halving every recencyHalfLife.
// A zero time — nothing here has one, but a caller composing a Memory by hand
// might — scores 0 rather than panicking on a negative duration.
func recencyScore(at time.Time) float64 {
	if at.IsZero() {
		return 0
	}
	age := time.Since(at)
	if age < 0 {
		age = 0
	}
	return math.Pow(0.5, age.Hours()/recencyHalfLife.Hours())
}

// importanceScore stretches Importance's 1-to-5 column to 0..1.
func importanceScore(importance int) float64 {
	return float64(importance-MinImportance) / float64(MaxImportance-MinImportance)
}

// kindScore is how long a kind is expected to keep mattering (see the Kind
// constants' own doc comments): a `project` fact or a `decision` is true
// until the project changes, a `discovery` or an open `issue` matters until
// somebody closes it, and an `episodic` memory is merely a thing that
// happened — the least likely of the five to be the answer to a fresh query.
func kindScore(kind string) float64 {
	switch kind {
	case KindProject, KindDecision:
		return 1
	case KindDiscovery, KindIssue:
		return 0.6
	case KindEpisodic:
		return 0.2
	default:
		return 0.5
	}
}
