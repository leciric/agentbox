package state

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrDestroyed is a write about an agent whose row already asks for it to be
// destroyed. A destroy is final: a restore, a snapshot or a move that finishes
// after it must not put the machine back, so the writes they make refuse it
// instead of overwriting desired_state.
var ErrDestroyed = errors.New("is being destroyed")

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
		pod = '', pod_uid = '', status_detail = '', destroyed_at = 0, claim = '', image = ''
		WHERE project = ? AND name = ?`, cluster, runtime, workspace, desired, project, name)
}

// MovePlacement is where an agent's machine is after a move, or where a move
// that failed put it back: the cluster, the workspace inside its pod, the claim
// its worktree lives on there, the image it runs ("" for the cluster's own), and
// what the user asks it to do ("" for running).
type MovePlacement struct {
	Cluster   string
	Workspace string
	Claim     string
	Image     string
	Desired   string
}

// MoveAgent records that an agent's machine is now a pod in another cluster, on
// the claim its workspace was restored into. It is one statement because the
// parts are one fact: a row that named the target cluster and the source claim,
// or the target claim and the source cluster, is a machine that doesn't exist.
// It is also what puts the row back when a move fails after this write, so the
// caller passes the source's own image and cluster for that.
//
// It forgets the pod the row named before, because that pod is on the cluster
// the agent has left. An empty Cluster puts it back on this host, which a move
// never asks for but which keeps this the inverse of PlaceAgent.
func (s *Store) MoveAgent(ctx context.Context, project, name string, to MovePlacement) error {
	runtime := ""
	if to.Cluster != "" {
		runtime = RuntimeKubernetes
	}
	desired := to.Desired
	if desired == "" {
		desired = DesiredRunning
	}
	return s.execLiveAgent(ctx, project, name, `UPDATE agents SET cluster = ?, runtime = ?, workspace = ?, claim = ?, image = ?,
		desired_state = ?, pod = '', pod_uid = '', status_detail = '', destroyed_at = 0
		WHERE project = ? AND name = ? AND desired_state != 'destroyed'`,
		to.Cluster, runtime, to.Workspace, to.Claim, to.Image, desired, project, name)
}

// SetAgentPod records the pod an agent's machine is, as the cluster has it, and
// the cluster's own reason it isn't doing what was asked. It is the reconciler's
// write: the pod the agent had before may be a different one.
func (s *Store) SetAgentPod(ctx context.Context, project, name, pod, uid, detail string) error {
	return s.execAgent(ctx, `UPDATE agents SET pod = ?, pod_uid = ?, status_detail = ? WHERE project = ? AND name = ?`,
		pod, uid, detail, project, name)
}

// SetAgentDesiredState records what the user asked the agent's machine to do. A
// destroyed row stays destroyed (ErrDestroyed): MarkAgentDestroyed is the only
// write that asks for that, and nothing undoes it.
func (s *Store) SetAgentDesiredState(ctx context.Context, project, name, desired string) error {
	return s.execLiveAgent(ctx, project, name, `UPDATE agents SET desired_state = ?
		WHERE project = ? AND name = ? AND desired_state != 'destroyed'`, desired, project, name)
}

// SetAgentClaim moves an agent onto the claim a restore prepared from a
// snapshot, and pins the image that snapshot was taken with. Both are written
// in one statement: a row that named the new claim but still ran the cluster's
// current image would come back as half the machine the snapshot saved.
//
// The names are the new ones, not a diff: a caller that only means to pin the
// image passes the claim the row already names.
func (s *Store) SetAgentClaim(ctx context.Context, project, name, claim, image string) error {
	return s.execLiveAgent(ctx, project, name, `UPDATE agents SET claim = ?, image = ?
		WHERE project = ? AND name = ? AND desired_state != 'destroyed'`, claim, image, project, name)
}

// SetAgentStatusDetail records the cluster's own reason an agent isn't doing what
// was asked, and clears it when it is.
func (s *Store) SetAgentStatusDetail(ctx context.Context, project, name, detail string) error {
	return s.execAgent(ctx, `UPDATE agents SET status_detail = ? WHERE project = ? AND name = ?`, detail, project, name)
}

// MarkAgentDestroyed marks a destroyed agent's pod: the reconciler deletes the
// pod, its token Secret and its claim. The timestamp records when, and no
// retention window reads it yet.
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

// execLiveAgent is execAgent for a statement that skips a destroyed row, and
// says which of the two it was when it touched nothing: no such agent, or one
// that is being destroyed.
func (s *Store) execLiveAgent(ctx context.Context, project, name, query string, args ...any) error {
	err := s.execAgent(ctx, query, args...)
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	var desired string
	if s.db.QueryRowContext(ctx, `SELECT desired_state FROM agents WHERE project = ? AND name = ?`, project, name).Scan(&desired) == nil && desired == DesiredDestroyed {
		return fmt.Errorf("%s/%s %w", project, name, ErrDestroyed)
	}
	return err
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
