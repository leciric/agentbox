package state

import (
	"context"
	"fmt"
	"time"
)

// What is left of the agent queue, which an earlier release had: agents a
// project asked for that waited for one of its slots before they got a
// machine, each an agents row with status AgentQueued and a row in
// agent_queue with the create request it starts from. Nothing queues any
// more; the daemon starts whatever an earlier release left queued as it
// starts (package daemon, leftoverqueue.go), and this is what it reads.

// QueuedAgent is one agent an earlier release left waiting in its project's
// queue.
type QueuedAgent struct {
	Project  string
	Name     string
	Position int // 1 is next, within its project
	// Request is the create request it starts from, as the daemon wrote it:
	// JSON this package never reads.
	Request  []byte
	QueuedAt time.Time
}

// Ref is the agent's project/name.
func (q QueuedAgent) Ref() string { return q.Project + "/" + q.Name }

// LeftoverQueue lists the queued agents of one project, or of every project
// for "", in order: by project, then by place in line.
func (s *Store) LeftoverQueue(ctx context.Context, project string) ([]QueuedAgent, error) {
	query := `SELECT project, name, position, request, queued_at FROM agent_queue`
	var args []any
	if project != "" {
		query += ` WHERE project = ?`
		args = append(args, project)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY project, position, queued_at, name`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []QueuedAgent
	for rows.Next() {
		var q QueuedAgent
		var request string
		var at int64
		if err := rows.Scan(&q.Project, &q.Name, &q.Position, &request, &at); err != nil {
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

// StartQueued takes a queued agent out of the queue as it starts being made:
// its row becomes a with status AgentCreating, the machine's columns filled
// in. It fails with ErrNotFound when the agent isn't queued any more, so two
// starts of the same agent can't both go ahead.
func (s *Store) StartQueued(ctx context.Context, a Agent) error {
	tx, err := s.db.BeginTx(ctx, nil)
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
		worktree = ?, status = ?, source = ?, title = ?, claude_account = ?, interface = ?, github_account = ?, finish_notice = ?, connectors = ?
		WHERE project = ? AND name = ? AND status = ?`,
		a.Instance, a.AI, a.Autonomous, a.Branch, a.BaseRef, a.BaseCommit, a.Worktree, AgentCreating, a.Source, a.Title,
		a.ClaudeAccount, a.Interface, a.GitHubAccount, a.FinishNotice, connectors, a.Project, a.Name, AgentQueued)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s isn't queued: %w", a.Ref(), ErrNotFound)
	}
	return tx.Commit()
}
