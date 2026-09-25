package daemon

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The token ledger's routes (D83). The chat writes the ledger as turns end
// (internal/chat); these only read it, for the app's Tokens views and
// `agentbox tokens`. A bad query is an error like any other, which
// writeError answers with 400.

// maxTokenTurns bounds a listing of the ledger itself.
const maxTokenTurns = 1000

// tokenFilter reads which part of the ledger a request is about: ?project=,
// ?agent= (only with a project) and ?since=, which is a time (RFC 3339) or a
// stretch back from now ("5h", "30m", "7d").
func tokenFilter(r *http.Request, now time.Time) (state.TokenFilter, error) {
	q := r.URL.Query()
	f := state.TokenFilter{Project: q.Get("project"), Agent: q.Get("agent"), Until: now}
	if f.Agent != "" && f.Project == "" {
		return f, errors.New("an agent is named with its project: ?project=<project>&agent=<agent>")
	}
	if raw := strings.TrimSpace(q.Get("since")); raw != "" {
		since, err := parseSince(raw, now)
		if err != nil {
			return f, err
		}
		f.Since = since
	}
	return f, nil
}

// parseSince reads a time or a stretch back from now. A stretch may be given in
// days, which Go's durations don't have.
func parseSince(raw string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return now.AddDate(0, 0, -n), nil
		}
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("since %q is neither a time (RFC 3339) nor a stretch back from now like 5h or 7d", raw)
}

// bucketFor is how wide a report's buckets are, for a stretch this long: a
// few dozen bars whatever the stretch, so a five-hour window shows how the
// limit went and a month shows which days.
func bucketFor(span time.Duration) time.Duration {
	switch {
	case span <= 0:
		return 24 * time.Hour // since the ledger began
	case span <= 6*time.Hour:
		return 10 * time.Minute
	case span <= 48*time.Hour:
		return time.Hour
	case span <= 14*24*time.Hour:
		return 6 * time.Hour
	}
	return 24 * time.Hour
}

func counts(input, output, cacheRead, cacheWrite int64, cost float64) api.TokenCounts {
	return api.TokenCounts{
		Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite,
		Total: input + output + cacheRead + cacheWrite, CostUSD: cost,
	}
}

func addCounts(a, b api.TokenCounts) api.TokenCounts {
	return counts(a.Input+b.Input, a.Output+b.Output, a.CacheRead+b.CacheRead, a.CacheWrite+b.CacheWrite, a.CostUSD+b.CostUSD)
}

// byCost orders spend most expensive first, and by tokens where the cost is
// the same — as it is for a tool that reports none.
func byCost(a, b api.TokenCounts) int {
	return cmp.Or(cmp.Compare(b.CostUSD, a.CostUSD), cmp.Compare(b.Total, a.Total))
}

func (s *Server) tokenReport(w http.ResponseWriter, r *http.Request) error {
	now := time.Now()
	f, err := tokenFilter(r, now)
	if err != nil {
		return err
	}
	totals, err := s.store.TokenTotals(r.Context(), f)
	if err != nil {
		return err
	}
	var span time.Duration
	if !f.Since.IsZero() {
		span = now.Sub(f.Since)
	}
	width := bucketFor(span)
	buckets, err := s.store.TokenBuckets(r.Context(), f, width)
	if err != nil {
		return err
	}
	// Which of the agents the ledger names still exist, and what they are
	// called now. An agent that doesn't is still reported: that it spent is
	// the point, and it is gone either way.
	agents, err := s.store.Agents(r.Context(), f.Project)
	if err != nil {
		return err
	}
	live := map[string]state.Agent{}
	for _, a := range agents {
		live[a.Ref()] = a
	}

	report := api.TokenReport{Until: now, Agents: []api.AgentTokens{}, Buckets: []api.TokenBucket{}, BucketSeconds: int64(width / time.Second)}
	if !f.Since.IsZero() {
		since := f.Since
		report.Since = &since
	}
	index := map[string]int{}
	for _, t := range totals {
		ref := t.Project + "/" + t.Agent
		i, ok := index[ref]
		if !ok {
			a, exists := live[ref]
			report.Agents = append(report.Agents, api.AgentTokens{
				Project: t.Project, Agent: t.Agent, Ref: ref, AI: t.AI, Title: a.Title, Exists: exists,
				Models: []api.ModelTokens{},
			})
			i = len(report.Agents) - 1
			index[ref] = i
		}
		c := counts(t.Input, t.Output, t.CacheRead, t.CacheWrite, t.CostUSD)
		at := &report.Agents[i]
		at.TokenCounts = addCounts(at.TokenCounts, c)
		at.Turns += t.Turns
		at.MaxContext = max(at.MaxContext, t.MaxContext)
		if t.LastAt.After(at.LastAt) {
			at.LastAt = t.LastAt
		}
		at.Models = append(at.Models, api.ModelTokens{Model: t.Model, TokenCounts: c})
		report.TokenCounts = addCounts(report.TokenCounts, c)
	}
	for i := range report.Agents {
		slices.SortFunc(report.Agents[i].Models, func(a, b api.ModelTokens) int { return byCost(a.TokenCounts, b.TokenCounts) })
	}
	slices.SortFunc(report.Agents, func(a, b api.AgentTokens) int { return byCost(a.TokenCounts, b.TokenCounts) })
	for _, b := range buckets {
		report.Buckets = append(report.Buckets, api.TokenBucket{
			Start: b.Start, TokenCounts: counts(b.Input, b.Output, b.CacheRead, b.CacheWrite, b.CostUSD),
		})
	}
	return writeJSON(w, http.StatusOK, report)
}

func (s *Server) tokenTurns(w http.ResponseWriter, r *http.Request) error {
	f, err := tokenFilter(r, time.Now())
	if err != nil {
		return err
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return fmt.Errorf("limit %q isn't a positive number", raw)
		}
		limit = min(n, maxTokenTurns)
	}
	rows, err := s.store.TokenRows(r.Context(), f, limit)
	if err != nil {
		return err
	}
	out := make([]api.TokenTurn, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.TokenTurn{
			Project: row.Project, Agent: row.Agent, AI: row.AI, Session: row.Session, Turn: row.Turn,
			Kind: row.Kind, Model: row.Model, At: row.At, Context: row.Context,
			TokenCounts: counts(row.Input, row.Output, row.CacheRead, row.CacheWrite, row.CostUSD),
		})
	}
	return writeJSON(w, http.StatusOK, out)
}
