package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// A checkpoint is an agent's worktree at one point of its conversation: the
// end of a chat turn, or what it had just before a rollback. The files are a
// commit on Ref (package agent makes it); this row is what the commit is of.
type Checkpoint struct {
	Project string
	Agent   string
	ID      string // turn-<n>, or saved-<time>
	Kind    string // CheckpointTurn or CheckpointSaved
	// Turn is the chat item ID of the user message heading the turn, and
	// Number which turn of the conversation that is, from 1. A saved
	// checkpoint has the last turn there was when it was taken.
	Turn   string
	Number int
	Prompt string // the start of that turn's message, to tell turns apart
	Ref    string
	Commit string // the snapshot commit: the worktree's files, on top of Head
	Head   string // the commit the agent's branch was on
	// Context, on a saved checkpoint, is the conversation it had, as a fork
	// from it is told: the conversation itself is gone with the rollback.
	Context   string
	CreatedAt time.Time
}

const (
	CheckpointTurn  = "turn"
	CheckpointSaved = "saved"
)

const checkpointColumns = `project, agent, id, kind, turn, number, prompt, ref, commit_id, head, context, created_at`

// SaveCheckpoint adds a checkpoint, or replaces the one with its ID: turn 3 of
// a conversation that was rolled back and went on again is a new turn 3.
func (s *Store) SaveCheckpoint(ctx context.Context, c Checkpoint) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO checkpoints (`+checkpointColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, agent, id) DO UPDATE SET kind = excluded.kind, turn = excluded.turn, number = excluded.number,
			prompt = excluded.prompt, ref = excluded.ref, commit_id = excluded.commit_id, head = excluded.head,
			context = excluded.context, created_at = excluded.created_at`,
		c.Project, c.Agent, c.ID, c.Kind, c.Turn, c.Number, c.Prompt, c.Ref, c.Commit, c.Head, c.Context, c.CreatedAt.UnixMilli())
	return err
}

// Checkpoints lists an agent's checkpoints, oldest first.
func (s *Store) Checkpoints(ctx context.Context, project, agent string) ([]Checkpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+checkpointColumns+` FROM checkpoints WHERE project = ? AND agent = ?
		ORDER BY created_at, number, rowid`, project, agent)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Checkpoint
	for rows.Next() {
		c, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ErrNoCheckpoint says an agent has no checkpoint by that ID.
var ErrNoCheckpoint = errors.New("no such checkpoint")

func (s *Store) Checkpoint(ctx context.Context, project, agent, id string) (Checkpoint, error) {
	c, err := scanCheckpoint(s.db.QueryRowContext(ctx, `SELECT `+checkpointColumns+` FROM checkpoints WHERE project = ? AND agent = ? AND id = ?`, project, agent, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Checkpoint{}, fmt.Errorf("%s/%s has no checkpoint %q: %w", project, agent, id, ErrNoCheckpoint)
	}
	return c, err
}

func (s *Store) DeleteCheckpoint(ctx context.Context, project, agent, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM checkpoints WHERE project = ? AND agent = ? AND id = ?`, project, agent, id)
	return err
}

// removeCheckpoints drops an agent's checkpoints with the agent; its refs go
// with its worktree.
func (s *Store) removeCheckpoints(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM checkpoints WHERE project = ? AND agent = ?`, project, agent)
	return err
}

func scanCheckpoint(row interface{ Scan(...any) error }) (Checkpoint, error) {
	var c Checkpoint
	var created int64
	err := row.Scan(&c.Project, &c.Agent, &c.ID, &c.Kind, &c.Turn, &c.Number, &c.Prompt, &c.Ref, &c.Commit, &c.Head, &c.Context, &created)
	c.CreatedAt = time.UnixMilli(created)
	return c, err
}
