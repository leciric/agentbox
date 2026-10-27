package state

import (
	"context"
	"fmt"
	"time"
)

// PlaceAgent records that an agent's machine is a pod in cluster, where its
// worktree lives inside it, and the state the user asked it to be in. It forgets
// the pod the row named before, because the one it is about to have is a new one.
//
// An empty cluster puts the agent back on this host, which is what an Incus
// instance is, and is why runtime is cleared with it.
func (s *Store) PlaceAgent(ctx context.Context, project, name, cluster, workspace, desired string) error {
	runtime := ""
	if cluster != "" {
		runtime = RuntimeKubernetes
	}
	if desired == "" {
		desired = DesiredRunning
	}
	return s.execAgent(ctx, `UPDATE agents SET cluster = ?, runtime = ?, workspace = ?, desired_state = ?,
		pod = '', pod_uid = '', status_detail = '', destroyed_at = 0
		WHERE project = ? AND name = ?`, cluster, runtime, workspace, desired, project, name)
}

// SetAgentPod records the pod an agent's machine is, as the cluster has it, and
// the cluster's own reason it isn't doing what was asked. It is the reconciler's
// write: the pod the agent had before may be a different one.
func (s *Store) SetAgentPod(ctx context.Context, project, name, pod, uid, detail string) error {
	return s.execAgent(ctx, `UPDATE agents SET pod = ?, pod_uid = ?, status_detail = ? WHERE project = ? AND name = ?`,
		pod, uid, detail, project, name)
}

// SetAgentDesiredState records what the user asked the agent's machine to do.
func (s *Store) SetAgentDesiredState(ctx context.Context, project, name, desired string) error {
	return s.execAgent(ctx, `UPDATE agents SET desired_state = ? WHERE project = ? AND name = ?`, desired, project, name)
}

// SetAgentStatusDetail records the cluster's own reason an agent isn't doing what
// was asked, and clears it when it is.
func (s *Store) SetAgentStatusDetail(ctx context.Context, project, name, detail string) error {
	return s.execAgent(ctx, `UPDATE agents SET status_detail = ? WHERE project = ? AND name = ?`, detail, project, name)
}

// MarkAgentDestroyed starts the retention clock on a destroyed agent's pod: the
// reconciler deletes it, and the row remembers when, so the claim is kept for the
// window the user configured and not forever.
func (s *Store) MarkAgentDestroyed(ctx context.Context, project, name string, at time.Time) error {
	return s.execAgent(ctx, `UPDATE agents SET desired_state = ?, destroyed_at = ? WHERE project = ? AND name = ?`,
		DesiredDestroyed, at.Unix(), project, name)
}

// AgentsOnCluster is every agent whose machine is a pod in cluster, which is what
// that cluster's reconciler works through.
func (s *Store) AgentsOnCluster(ctx context.Context, cluster string) ([]Agent, error) {
	return s.queryAgents(ctx, `WHERE cluster = ? ORDER BY project, name`, cluster)
}

// ClustersInUse is the clusters that have agents in them: the reconciler runs one
// loop per cluster here, and none for a cluster the user only added.
func (s *Store) ClustersInUse(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT cluster FROM agents WHERE cluster != '' ORDER BY cluster`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var clusters []string
	for rows.Next() {
		var cluster string
		if err := rows.Scan(&cluster); err != nil {
			return nil, err
		}
		clusters = append(clusters, cluster)
	}
	return clusters, rows.Err()
}

// execAgent runs a statement about one agent, and says when there is no such
// agent: the reconciler races deletions, and it has to tell "gone" from "failed".
func (s *Store) execAgent(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("agent: %w", ErrNotFound)
	}
	return nil
}
