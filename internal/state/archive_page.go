package state

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The Work page's reading of the archive (internal/daemon/work.go): one page
// of past agents and how many each pull request state keeps, without reading
// every past agent an install has ever had, and what a page's rows show of
// their reviews and media in one query each rather than one per row.

// ArchivedCursor is a place in the archive's order, most recently archived
// first and then by project and name: ArchivedPage answers what comes after
// it.
type ArchivedCursor struct {
	At time.Time
	// Through also keeps every agent archived in At's second, whatever its
	// name: for a cursor that isn't a past agent, which sorts before them all.
	Through       bool
	Project, Name string
}

// ArchivedPageQuery is which past agents ArchivedPage reads.
type ArchivedPageQuery struct {
	Project string // "" for every project
	// Query keeps the agents whose name, title, task, branch or pull request
	// title holds it, ignoring case, or whose pull request number it is.
	Query string
	// PRState keeps those whose pull request is in it, or with none (PRNone).
	PRState string
	After   *ArchivedCursor
	Limit   int // 0 for the counts alone
}

// ArchivedCounts is how many past agents a search keeps, by pull request.
type ArchivedCounts struct{ All, Open, Merged, Closed, None int }

// Of is how many are in a pull request state, "" being all.
func (c ArchivedCounts) Of(prState string) int {
	switch prState {
	case "":
		return c.All
	case PROpen:
		return c.Open
	case PRMerged:
		return c.Merged
	case PRClosed:
		return c.Closed
	case PRNone:
		return c.None
	}
	return 0
}

func (c *ArchivedCounts) add(pr ArchivedPR) {
	c.All++
	switch {
	case pr.Number == 0:
		c.None++
	case pr.State == PROpen:
		c.Open++
	case pr.State == PRMerged:
		c.Merged++
	case pr.State == PRClosed:
		c.Closed++
	}
}

// ArchivedPage is up to q.Limit past agents after q.After, the most recently
// archived first and then by project and name, and how many q.Query keeps
// under each pull request state (whatever q.PRState and q.After are).
//
// Without a search both come from indexes: the counts from one aggregate and
// the page from a walk of (project, archived_at) that stops at the limit. A
// search reads the archive once and matches here, since SQLite's LIKE only
// ignores the case of ASCII letters and the page's search ignores it of any.
func (s *Store) ArchivedPage(ctx context.Context, q ArchivedPageQuery) ([]ArchivedAgent, ArchivedCounts, error) {
	if text := strings.ToLower(strings.TrimSpace(q.Query)); text != "" {
		return s.searchArchived(ctx, q, text)
	}
	var counts ArchivedCounts
	where, args := []string{"1"}, []any{}
	if q.Project != "" {
		where = append(where, "project = ?")
		args = append(args, q.Project)
	}
	err := s.db.QueryRowContext(ctx, `SELECT count(*),
			coalesce(sum(pr_number > 0 AND pr_state = '`+PROpen+`'), 0),
			coalesce(sum(pr_number > 0 AND pr_state = '`+PRMerged+`'), 0),
			coalesce(sum(pr_number > 0 AND pr_state = '`+PRClosed+`'), 0),
			coalesce(sum(pr_number = 0), 0)
		FROM archived_agents WHERE `+strings.Join(where, " AND "), args...).
		Scan(&counts.All, &counts.Open, &counts.Merged, &counts.Closed, &counts.None)
	if err != nil || q.Limit <= 0 {
		return nil, counts, err
	}
	switch q.PRState {
	case "":
	case PRNone:
		where = append(where, "pr_number = 0")
	case PROpen, PRMerged, PRClosed:
		where = append(where, "pr_number > 0 AND pr_state = '"+q.PRState+"'")
	default:
		return nil, counts, fmt.Errorf("a pull request's state is %s, %s, %s or %s, not %q", PROpen, PRMerged, PRClosed, PRNone, q.PRState)
	}
	if c := q.After; c != nil {
		if c.Through {
			where = append(where, "archived_at <= ?")
			args = append(args, c.At.Unix())
		} else {
			// The first term is the one the index is walked from.
			where = append(where, "archived_at <= ? AND (archived_at < ? OR (project, name) > (?, ?))")
			args = append(args, c.At.Unix(), c.At.Unix(), c.Project, c.Name)
		}
	}
	args = append(args, q.Limit)
	page, err := s.queryArchived(ctx, `SELECT `+archivedColumns+` FROM archived_agents WHERE `+strings.Join(where, " AND ")+
		` ORDER BY archived_at DESC, project, name LIMIT ?`, args...)
	return page, counts, err
}

// searchArchived is ArchivedPage under a search, text in lower case.
func (s *Store) searchArchived(ctx context.Context, q ArchivedPageQuery, text string) ([]ArchivedAgent, ArchivedCounts, error) {
	query, args := `SELECT `+archivedColumns+` FROM archived_agents`, []any{}
	if q.Project != "" {
		query += ` WHERE project = ?`
		args = append(args, q.Project)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY archived_at DESC, project, name`, args...)
	if err != nil {
		return nil, ArchivedCounts{}, err
	}
	defer func() { _ = rows.Close() }()
	number, _ := PRNumberQuery(text)
	var counts ArchivedCounts
	var page []ArchivedAgent
	for rows.Next() {
		var a ArchivedAgent
		var created, archived int64
		if err := rows.Scan(&a.Project, &a.Name, &a.ID, &a.Title, &a.Task, &a.AI, &a.Interface, &a.Branch, &a.BaseRef, &a.BaseCommit,
			&a.GitHubAccount, &created, &archived, &a.PR.Number, &a.PR.Title, &a.PR.URL, &a.PR.State); err != nil {
			return nil, ArchivedCounts{}, err
		}
		isNumber := number > 0 && a.PR.Number == number
		if !isNumber && !containsFold(text, a.Title, a.Name, a.Branch, a.Task, a.PR.Title) {
			continue
		}
		counts.add(a.PR)
		if len(page) >= q.Limit {
			continue
		}
		switch q.PRState {
		case "":
		case PRNone:
			if a.PR.Number != 0 {
				continue
			}
		default:
			if a.PR.Number == 0 || a.PR.State != q.PRState {
				continue
			}
		}
		if c := q.After; c != nil {
			at := c.At.Unix()
			after := archived < at || (archived == at && (c.Through || a.Project > c.Project || (a.Project == c.Project && a.Name > c.Name)))
			if !after {
				continue
			}
		}
		a.CreatedAt, a.ArchivedAt = time.Unix(created, 0), time.Unix(archived, 0)
		page = append(page, a)
	}
	return page, counts, rows.Err()
}

// containsFold is whether any field holds text, which is in lower case.
func containsFold(text string, fields ...string) bool {
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), text) {
			return true
		}
	}
	return false
}

// AgentKey names an agent, live or past, for what is read of several at once.
type AgentKey struct{ Project, Agent string }

// agentsJoin starts a query over a table's rows of keys' agents: the agents
// as a table k, joined to the table's (project, agent) index one agent at a
// time. CROSS JOIN keeps that order: left to choose, SQLite reads the whole
// table and looks each row up among the agents.
func agentsJoin(table string, keys []AgentKey) (string, []any) {
	args := make([]any, 0, 2*len(keys))
	for _, k := range keys {
		args = append(args, k.Project, k.Agent)
	}
	return ` FROM (VALUES ` + strings.TrimSuffix(strings.Repeat("(?, ?),", len(keys)), ",") + `) AS k CROSS JOIN ` + table +
		` AS t ON t.project = k.column1 AND t.agent = k.column2 `, args
}

// sqliteVariables is how many agents one query may name: two variables each,
// well under SQLite's limit.
const sqliteVariables = 400

// AgentReviewsOf is the verdicts on these agents, in one query.
func (s *Store) AgentReviewsOf(ctx context.Context, keys []AgentKey) (map[AgentKey]AgentReview, error) {
	out := map[AgentKey]AgentReview{}
	for ; len(keys) > 0; keys = keys[min(len(keys), sqliteVariables):] {
		from, args := agentsJoin("agent_reviews", keys[:min(len(keys), sqliteVariables)])
		rows, err := s.db.QueryContext(ctx, `SELECT t.project, t.agent, t.verdict, t.fingerprint, t.note, t.at`+from, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r AgentReview
			var at int64
			if err := rows.Scan(&r.Project, &r.Agent, &r.Verdict, &r.Fingerprint, &r.Note, &at); err != nil {
				_ = rows.Close()
				return nil, err
			}
			r.At = time.UnixMilli(at)
			out[AgentKey{r.Project, r.Agent}] = r
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MediaKindCounts is how many media items of each kind these agents kept, in
// one query: what MediaTallies counts for one agent, without the sizes and
// the unseen ones a list's rows don't show.
func (s *Store) MediaKindCounts(ctx context.Context, keys []AgentKey) (map[AgentKey]map[string]int, error) {
	out := map[AgentKey]map[string]int{}
	for ; len(keys) > 0; keys = keys[min(len(keys), sqliteVariables):] {
		from, args := agentsJoin("media", keys[:min(len(keys), sqliteVariables)])
		rows, err := s.db.QueryContext(ctx, `SELECT t.project, t.agent, t.kind, count(*)`+from+`GROUP BY t.project, t.agent, t.kind`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k AgentKey
			var kind string
			var n int
			if err := rows.Scan(&k.Project, &k.Agent, &kind, &n); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if out[k] == nil {
				out[k] = map[string]int{}
			}
			out[k][kind] = n
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
