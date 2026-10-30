package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/trace"
)

// ArchivedAgent is what is kept of an agent whose machine was removed: who it
// was, what it was asked, where its work went, and its pull request. Its
// conversation stays in chat_items under its name, its media and token rows
// under theirs, until the user deletes it (DeleteArchivedAgent).
type ArchivedAgent struct {
	Project, Name string
	ID            string // the agent's ID, which its token_usage rows carry
	Title         string
	Task          string // its first message, as sent
	AI, Interface string
	Branch        string
	BaseRef       string
	BaseCommit    string
	GitHubAccount string
	CreatedAt     time.Time
	ArchivedAt    time.Time
	PR            ArchivedPR
}

// ArchivedPR is an archived agent's pull request as last read; Number 0 for
// none found.
type ArchivedPR struct {
	Number int
	Title  string
	URL    string
	State  string // open, closed or merged
}

func (a ArchivedAgent) Ref() string { return a.Project + "/" + a.Name }

// AsAgent is the archived agent as an Agent record, for reading its chat: no
// machine, worktree or instance, and the status AgentArchived.
func (a ArchivedAgent) AsAgent() Agent {
	return Agent{
		Project: a.Project, Name: a.Name, ID: a.ID, Title: a.Title, AI: a.AI, Interface: a.Interface,
		Branch: a.Branch, BaseRef: a.BaseRef, BaseCommit: a.BaseCommit, GitHubAccount: a.GitHubAccount,
		Status: AgentArchived, CreatedAt: a.CreatedAt, Role: RoleWorker,
	}
}

// AgentArchived is the status of an Agent made from an ArchivedAgent: no
// agents row has it.
const AgentArchived = "archived"

// The pull request states ArchivedQuery.PRState filters on; PRNone is an
// agent with no pull request found.
const (
	PRNone   = "none"
	PROpen   = "open"
	PRClosed = "closed"
	PRMerged = "merged"
)

const archivedColumns = `project, name, id, title, task, ai, interface, branch, base_ref, base_commit, github_account, created_at, archived_at, pr_number, pr_title, pr_url, pr_state`

// maxTask is how much of an agent's first message the archive keeps.
const maxTask = 4000

// ArchiveAgent removes a live agent's record and keeps it as archived, in one
// transaction: what belonged to its machine goes (its secrets, connectors,
// checkpoints, rail events, running turn, unanswered questions and the AI
// tool's session id), and its conversation stays. task, when empty, is taken
// from the first message of its chat. A lead is never archived: it has no
// machine to lose, so this refuses one.
func (s *Store) ArchiveAgent(ctx context.Context, a Agent, task string, pr ArchivedPR, at time.Time) error {
	if a.IsLead() || a.Project == HomeProject {
		return fmt.Errorf("%s is a project's chat, which is never archived", a.Ref())
	}
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if task == "" {
		task, err = firstMessage(ctx, tx, a.Project, a.Name)
		if err != nil {
			return err
		}
	}
	if r := []rune(task); len(r) > maxTask {
		task = string(r[:maxTask]) + "…"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO archived_agents (`+archivedColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, name) DO NOTHING`,
		a.Project, a.Name, a.ID, a.Title, task, a.AI, a.Interface, a.Branch, a.BaseRef, a.BaseCommit, a.GitHubAccount,
		a.CreatedAt.Unix(), at.Unix(), pr.Number, pr.Title, pr.URL, pr.State); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM agents WHERE project = ? AND name = ?`,
		`DELETE FROM secrets WHERE project = ? AND agent = ?`,
		`DELETE FROM agent_queue WHERE project = ? AND name = ?`,
		`DELETE FROM connectors WHERE project = ? AND agent = ?`,
		`DELETE FROM agent_events WHERE project = ? AND agent = ?`,
		`DELETE FROM checkpoints WHERE project = ? AND agent = ?`,
		`DELETE FROM running_turns WHERE project = ? AND agent = ?`,
		// The session lived in its machine, which is gone: nothing resumes it.
		`UPDATE chats SET session_id = '', handoff = '' WHERE project = ? AND agent = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, a.Project, a.Name); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE questions SET status = ? WHERE project = ? AND agent = ? AND status IN (?, ?)`,
		QuestionCancelled, a.Project, a.Name, QuestionPending, QuestionEscalated); err != nil {
		return err
	}
	return tx.Commit()
}

// FirstMessage is the text of the first message somebody sent in an agent's
// chat, its task, as ArchiveAgent keeps it: "" for none.
func (s *Store) FirstMessage(ctx context.Context, project, agent string) (string, error) {
	task, err := firstMessage(ctx, s.db, project, agent)
	if r := []rune(task); len(r) > maxTask {
		task = string(r[:maxTask]) + "…"
	}
	return task, err
}

// firstMessage is the text of the first message somebody sent in a chat.
func firstMessage(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, project, agent string) (string, error) {
	var text sql.NullString
	err := q.QueryRowContext(ctx, `SELECT json_extract(data, '$.text') FROM chat_items
		WHERE project = ? AND agent = ? AND json_extract(data, '$.kind') = 'user' AND coalesce(json_extract(data, '$.woken'), 0) = 0
		ORDER BY position LIMIT 1`, project, agent).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return strings.TrimSpace(text.String), err
}

// PRNumberQuery reads text as a pull request number the way people type one:
// "404", "#404", "pr 404", "PR#404" or "pr #404".
func PRNumberQuery(text string) (int, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimSpace(strings.TrimPrefix(t, "pr"))
	t = strings.TrimSpace(strings.TrimPrefix(t, "#"))
	n, err := strconv.Atoi(t)
	return n, err == nil && n > 0 && t == strconv.Itoa(n)
}

// ArchivedQuery is which archived agents ArchivedAgents lists.
type ArchivedQuery struct {
	Project string // "" for every project
	// Query, when set, keeps the agents whose name, title, task, branch or
	// pull request title holds it, ignoring case, or whose pull request number
	// it is (PRNumberQuery).
	Query string
	// PRState keeps only those whose pull request is in it, or with none
	// (PRNone); "" keeps every one.
	PRState string
	// Since and Until, when set, keep those archived in [Since, Until).
	Since, Until time.Time
	Limit        int // 0 for every one
}

// ArchivedAgents lists archived agents, the most recently archived first.
func (s *Store) ArchivedAgents(ctx context.Context, q ArchivedQuery) ([]ArchivedAgent, error) {
	where := []string{"1"}
	var args []any
	if q.Project != "" {
		where = append(where, "project = ?")
		args = append(args, q.Project)
	}
	if text := strings.TrimSpace(q.Query); text != "" {
		like := "%" + likeEscaper.Replace(text) + "%"
		// A pull request number is 0 when the text isn't one, which no agent has.
		number, _ := PRNumberQuery(text)
		where = append(where, `(name LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\' OR task LIKE ? ESCAPE '\' OR branch LIKE ? ESCAPE '\' OR pr_title LIKE ? ESCAPE '\' OR (pr_number > 0 AND pr_number = ?))`)
		args = append(args, like, like, like, like, like, number)
	}
	switch q.PRState {
	case "":
	case PRNone:
		where = append(where, "pr_number = 0")
	case PROpen, PRClosed, PRMerged:
		where = append(where, "pr_number > 0 AND pr_state = ?")
		args = append(args, q.PRState)
	default:
		return nil, fmt.Errorf("a pull request's state is %s, %s, %s or %s, not %q", PROpen, PRMerged, PRClosed, PRNone, q.PRState)
	}
	if !q.Since.IsZero() {
		where = append(where, "archived_at >= ?")
		args = append(args, q.Since.Unix())
	}
	if !q.Until.IsZero() {
		where = append(where, "archived_at < ?")
		args = append(args, q.Until.Unix())
	}
	query := `SELECT ` + archivedColumns + ` FROM archived_agents WHERE ` + strings.Join(where, " AND ") + ` ORDER BY archived_at DESC, project, name`
	if q.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, q.Limit)
	}
	return s.queryArchived(ctx, query, args...)
}

// ArchivedAgentByName is one archived agent.
func (s *Store) ArchivedAgentByName(ctx context.Context, project, name string) (ArchivedAgent, error) {
	found, err := s.queryArchived(ctx, `SELECT `+archivedColumns+` FROM archived_agents WHERE project = ? AND name = ?`, project, name)
	if err != nil {
		return ArchivedAgent{}, err
	}
	if len(found) == 0 {
		return ArchivedAgent{}, fmt.Errorf("past agent %s/%s: %w", project, name, ErrNotFound)
	}
	return found[0], nil
}

// SetArchivedPR records what an archived agent's pull request is now.
func (s *Store) SetArchivedPR(ctx context.Context, project, name string, pr ArchivedPR) error {
	_, err := s.db.ExecContext(ctx, `UPDATE archived_agents SET pr_number = ?, pr_title = ?, pr_url = ?, pr_state = ? WHERE project = ? AND name = ?`,
		pr.Number, pr.Title, pr.URL, pr.State, project, name)
	return err
}

// DeleteArchivedAgent forgets an archived agent for good: its record and its
// conversation. Its name stays taken (agent_seq and PastAgentNames never go
// back), and its token rows stay, as for any agent, for accounting.
func (s *Store) DeleteArchivedAgent(ctx context.Context, project, name string) error {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM archived_agents WHERE project = ? AND name = ?`, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("past agent %s/%s: %w", project, name, ErrNotFound)
	}
	for _, q := range []string{
		`DELETE FROM chat_items WHERE project = ? AND agent = ?`,
		`DELETE FROM running_turns WHERE project = ? AND agent = ?`,
		`DELETE FROM chats WHERE project = ? AND agent = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, project, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ArchivedTokens is how many tokens each archived agent used, and what they
// cost, by agent ID.
func (s *Store) ArchivedTokens(ctx context.Context, ids []string) (map[string]TokenSum, error) {
	out := map[string]TokenSum{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT agent_id, sum(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), sum(cost_usd)
		FROM token_usage WHERE agent_id IN (?`+strings.Repeat(`, ?`, len(ids)-1)+`) GROUP BY agent_id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var sum TokenSum
		if err := rows.Scan(&id, &sum.Tokens, &sum.CostUSD); err != nil {
			return nil, err
		}
		out[id] = sum
	}
	return out, rows.Err()
}

// TokenSum is a total of token_usage rows.
type TokenSum struct {
	Tokens  int64
	CostUSD float64
}

func (s *Store) queryArchived(ctx context.Context, query string, args ...any) ([]ArchivedAgent, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ArchivedAgent
	for rows.Next() {
		var a ArchivedAgent
		var created, archived int64
		if err := rows.Scan(&a.Project, &a.Name, &a.ID, &a.Title, &a.Task, &a.AI, &a.Interface, &a.Branch, &a.BaseRef, &a.BaseCommit,
			&a.GitHubAccount, &created, &archived, &a.PR.Number, &a.PR.Title, &a.PR.URL, &a.PR.State); err != nil {
			return nil, err
		}
		a.CreatedAt, a.ArchivedAt = time.Unix(created, 0), time.Unix(archived, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

// LastMessage is the text of the last item of a kind (assistant, user) in a
// chat, and "" when it has none.
func (s *Store) LastMessage(ctx context.Context, project, agent, kind string) (string, error) {
	var text sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT json_extract(data, '$.text') FROM chat_items
		WHERE project = ? AND agent = ? AND json_extract(data, '$.kind') = ? AND coalesce(json_extract(data, '$.text'), '') != ''
		ORDER BY position DESC LIMIT 1`, project, agent, kind).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return strings.TrimSpace(text.String), err
}
