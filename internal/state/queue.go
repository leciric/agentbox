package state

import (
	"context"
	"fmt"
	"time"

	"agentbox/internal/trace"
)

// What this package does with the agent queue. An earlier release queued
// agents a project asked for that waited for one of its slots: each an agents
// row with status AgentQueued and a row in agent_queue with the create request
// it starts from. Stage 6 brought it back for a different reason — a cluster
// with no room — so the daemon queues an agent there and starts it when there
// is room (daemon/queue.go). The shape is the same, which is why an agent an
// earlier release left queued still starts.

// QueuedAgent is one agent waiting in its project's queue.
type QueuedAgent struct {
	Project  string
	Name     string
	Position int // 1 is next, within its project
	// Request is the create request it starts from, as the daemon wrote it:
	// JSON this package never reads.
	Request []byte
	// Reason is why it is waiting, as the clusters put it when it was queued.
	Reason   string
	QueuedAt time.Time
}

// Ref is the agent's project/name.
func (q QueuedAgent) Ref() string { return q.Project + "/" + q.Name }

// LeftoverQueue lists the queued agents of one project, or of every project
// for "", oldest first: scheduling is one line across projects, so a later-named
// project can't starve. Position is kept for display, numbered 1..n within a
// project in that same order.
func (s *Store) LeftoverQueue(ctx context.Context, project string) ([]QueuedAgent, error) {
	query := `SELECT project, name, position, request, reason, queued_at FROM agent_queue`
	var args []any
	if project != "" {
		query += ` WHERE project = ?`
		args = append(args, project)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY queued_at, project, position, name`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []QueuedAgent
	for rows.Next() {
		var q QueuedAgent
		var request string
		var at int64
		if err := rows.Scan(&q.Project, &q.Name, &q.Position, &request, &q.Reason, &at); err != nil {
			return nil, err
		}
		q.Request, q.QueuedAt = []byte(request), time.Unix(at, 0)
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Positions are kept 1..n by every write, but numbered here again from
	// the order alone, so a gap never shows as "Queued #3" with two ahead.
	for i := range out {
		if i == 0 || out[i-1].Project != out[i].Project {
			out[i].Position = 1
		} else {
			out[i].Position = out[i-1].Position + 1
		}
	}
	return out, nil
}

// QueueAgent puts a create in its project's queue: its agent row, with no
// machine and no branch yet, and the request the daemon starts it from once a
// cluster has room. reason is what the clusters said when it was queued, and
// is what the queue shows beside it.
//
// The row goes first, so a queued agent is part of its project from the moment
// it is accepted: the queue is what agentbox list and the app read, and a
// request with no row would be invisible. A failure to queue takes the row away
// again, so nothing is half queued.
func (s *Store) QueueAgent(ctx context.Context, a Agent, request []byte, reason string) error {
	if a.Status != AgentQueued {
		return fmt.Errorf("%s: a queued agent has status %q, not %q", a.Ref(), AgentQueued, a.Status)
	}
	if err := s.AddAgent(ctx, a); err != nil {
		return err
	}
	if err := s.enqueue(ctx, a.Project, a.Name, request, reason); err != nil {
		_ = s.RemoveAgent(context.WithoutCancel(ctx), a.Project, a.Name)
		return err
	}
	return nil
}

// enqueue appends an agent to the end of its project's queue.
func (s *Store) enqueue(ctx context.Context, project, name string, request []byte, reason string) error {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var position int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position), 0) + 1 FROM agent_queue WHERE project = ?`, project).Scan(&position); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agent_queue (project, name, position, request, reason, queued_at) VALUES (?, ?, ?, ?, ?, ?)`,
		project, name, position, string(request), reason, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// Dequeue takes an agent out of its project's line without touching its row,
// for a queue entry nothing can ever start: the agent it names is gone.
func (s *Store) Dequeue(ctx context.Context, project, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_queue WHERE project = ? AND name = ?`, project, name)
	return err
}

// StartQueued takes a queued agent out of the queue as it starts being made:
// its row becomes a with status AgentCreating, the machine's columns filled
// in. It fails with ErrNotFound when the agent isn't queued any more, so two
// starts of the same agent can't both go ahead.
func (s *Store) StartQueued(ctx context.Context, a Agent) error {
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM agent_queue WHERE project = ? AND name = ?`, a.Project, a.Name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s isn't queued: %w", a.Ref(), ErrNotFound)
	}
	connectors, err := connectorLimit(a.Connectors)
	if err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE agents SET instance = ?, ai = ?, autonomous = ?, branch = ?, base_ref = ?, base_commit = ?,
		worktree = ?, status = ?, source = ?, title = ?, claude_account = ?, interface = ?, github_account = ?, finish_notice = ?, connectors = ?,
		codex_account = ?, opencode_accounts = ?, cursor_account = ?, cluster = ?, runtime = ?, workspace = ?, desired_state = ?, claim = ?, image = ?
		WHERE project = ? AND name = ? AND status = ?`,
		a.Instance, a.AI, a.Autonomous, a.Branch, a.BaseRef, a.BaseCommit, a.Worktree, AgentCreating, a.Source, a.Title,
		a.ClaudeAccount, a.Interface, a.GitHubAccount, a.FinishNotice, connectors,
		a.CodexAccount, a.OpenCodeAccounts.String(), a.CursorAccount, a.Cluster, a.Runtime, a.Workspace, a.DesiredState, a.Claim, a.Image,
		a.Project, a.Name, AgentQueued)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s isn't queued: %w", a.Ref(), ErrNotFound)
	}
	return tx.Commit()
}
