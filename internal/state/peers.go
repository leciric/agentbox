package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PeerAgent is an agent of this machine that a lead on another machine made
// through the hub: its notices go back to that lead rather than to this
// project's own chat.
type PeerAgent struct {
	Project, Agent string
	// OriginID and OriginName are the other machine's environment on the
	// hub, and OriginProject the project there whose lead made it.
	OriginID, OriginName, OriginProject string
	CreatedAt                           time.Time
}

func (s *Store) AddPeerAgent(ctx context.Context, p PeerAgent) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO peer_agents (project, agent, origin_id, origin_name, origin_project, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, p.Project, p.Agent, p.OriginID, p.OriginName, p.OriginProject, p.CreatedAt.UnixMilli())
	return err
}

// PeerAgent is who made one of this machine's agents from elsewhere;
// ErrNotFound for an agent made here.
func (s *Store) PeerAgent(ctx context.Context, project, agent string) (PeerAgent, error) {
	p := PeerAgent{Project: project, Agent: agent}
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT origin_id, origin_name, origin_project, created_at FROM peer_agents WHERE project = ? AND agent = ?`,
		project, agent).Scan(&p.OriginID, &p.OriginName, &p.OriginProject, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return PeerAgent{}, ErrNotFound
	}
	p.CreatedAt = time.UnixMilli(created)
	return p, err
}

// The statuses of a RemoteAgent, as its notices last said.
const (
	RemoteCreating = "creating"
	RemoteWorking  = "working"
	RemoteAsking   = "asking"
	RemoteDone     = "done"
	RemoteFailed   = "failed"
	RemoteRetired  = "retired"
)

// RemoteAgent is an agent a lead of this machine runs on another machine.
type RemoteAgent struct {
	Project, MachineID, MachineName, Agent string
	Title, Branch, Status, Summary, PRURL  string
	UpdatedAt                              time.Time
}

// SaveRemoteAgent adds a remote agent or updates it; fields left empty keep
// what was known.
func (s *Store) SaveRemoteAgent(ctx context.Context, a RemoteAgent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO remote_agents (project, machine_id, machine_name, agent, title, branch, status, summary, pr_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, machine_id, agent) DO UPDATE SET
			machine_name = excluded.machine_name,
			title = CASE WHEN excluded.title = '' THEN title ELSE excluded.title END,
			branch = CASE WHEN excluded.branch = '' THEN branch ELSE excluded.branch END,
			status = CASE WHEN excluded.status = '' THEN status ELSE excluded.status END,
			summary = CASE WHEN excluded.summary = '' THEN summary ELSE excluded.summary END,
			pr_url = CASE WHEN excluded.pr_url = '' THEN pr_url ELSE excluded.pr_url END,
			updated_at = excluded.updated_at`,
		a.Project, a.MachineID, a.MachineName, a.Agent, a.Title, a.Branch, a.Status, a.Summary, a.PRURL, a.UpdatedAt.UnixMilli())
	return err
}

// RemoteAgents are a project's agents on other machines, by machine and
// then name.
func (s *Store) RemoteAgents(ctx context.Context, project string) ([]RemoteAgent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT machine_id, machine_name, agent, title, branch, status, summary, pr_url, updated_at
		FROM remote_agents WHERE project = ? ORDER BY machine_name, agent`, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RemoteAgent
	for rows.Next() {
		a := RemoteAgent{Project: project}
		var updated int64
		if err := rows.Scan(&a.MachineID, &a.MachineName, &a.Agent, &a.Title, &a.Branch, &a.Status, &a.Summary, &a.PRURL, &updated); err != nil {
			return nil, err
		}
		a.UpdatedAt = time.UnixMilli(updated)
		out = append(out, a)
	}
	return out, rows.Err()
}
