package memory

import (
	"context"
	"errors"
	"strings"
	"time"
)

// AddArtifact records a reference to something an agent produced. The contents
// never come here: a file stays in its worktree, a recording stays in media, a
// pull request stays on GitHub. What is kept is enough to find it again and to
// say who made it and when.
func (s *Store) AddArtifact(ctx context.Context, a Artifact) (Artifact, error) {
	if err := requireProject(a.Project); err != nil {
		return Artifact{}, err
	}
	a.Type = strings.TrimSpace(a.Type)
	if a.Type == "" {
		return Artifact{}, errors.New("an artifact needs a type, like file, branch, pull_request, media or url")
	}
	path, err := text("an artifact's path", a.Path, MaxTitleLen*4)
	if err != nil {
		return Artifact{}, err
	}
	if path == "" {
		return Artifact{}, errors.New("an artifact needs a path: where the thing it refers to is")
	}
	a.Path = path
	metadata, err := jsonDocument("an artifact's metadata", a.Metadata)
	if err != nil {
		return Artifact{}, err
	}
	a.Metadata = []byte(metadata)
	if a.ID == "" {
		a.ID = newID("art")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	a.CreatedAt = stamp(a.CreatedAt)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO artifacts (id, project, agent, type, path, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Project, a.Agent, a.Type, a.Path, metadata, a.CreatedAt.UnixMilli())
	if err != nil {
		return Artifact{}, err
	}
	return a, nil
}

// Artifacts are a project's, newest first.
func (s *Store) Artifacts(ctx context.Context, project string) ([]Artifact, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	return s.queryArtifacts(ctx, `WHERE project = ? ORDER BY created_at DESC, rowid DESC LIMIT ?`, project, MaxLimit)
}

// Artifact is one by id.
func (s *Store) Artifact(ctx context.Context, project, id string) (Artifact, error) {
	rows, err := s.queryArtifacts(ctx, `WHERE project = ? AND id = ?`, project, id)
	if err != nil {
		return Artifact{}, err
	}
	if len(rows) == 0 {
		return Artifact{}, notFound("artifact", id)
	}
	return rows[0], nil
}

func (s *Store) queryArtifacts(ctx context.Context, clause string, args ...any) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project, agent, type, path, metadata, created_at FROM artifacts `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		var created int64
		var metadata string
		if err := rows.Scan(&a.ID, &a.Project, &a.Agent, &a.Type, &a.Path, &metadata, &created); err != nil {
			return nil, err
		}
		a.Metadata = []byte(metadata)
		a.CreatedAt = attime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}
