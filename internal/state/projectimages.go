package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A project image is a project's own container image: the machine a cluster
// agent starts from, where an agent on this host is copied from an Incus base.
// It is built from the project's .agentbox/agent.Dockerfile on top of the agent
// image its cluster runs, pushed to a registry, and recorded here by the digest
// the registry reported.

// ProjectImage is one saved project image.
type ProjectImage struct {
	Project   string
	Image     string
	Digest    string
	SavedFrom string
	SavedAt   time.Time
}

// Ref is what a pod runs: the digest when the save recorded one, so a tag that
// moved cannot change what an agent's next pod runs, and the tag alone when it
// didn't, which is the case for an image a local runtime imported.
func (p ProjectImage) Ref() string {
	if p.Digest == "" {
		return p.Image
	}
	return p.Image + "@" + strings.TrimPrefix(p.Digest, "@")
}

// ProjectImage is the image a project's agents run; ok is false when the
// project has none.
func (s *Store) ProjectImage(ctx context.Context, project string) (ProjectImage, bool, error) {
	current, _, hasPrevious, err := s.projectImages(ctx, project)
	if err != nil {
		return ProjectImage{}, false, err
	}
	return current, current.Image != "" || hasPrevious, nil
}

// PreviousProjectImage is the image the last save replaced, which
// RevertProjectImage puts back; ok is false when there is none.
func (s *Store) PreviousProjectImage(ctx context.Context, project string) (ProjectImage, bool, error) {
	_, previous, hasPrevious, err := s.projectImages(ctx, project)
	if err != nil || !hasPrevious {
		return ProjectImage{}, false, err
	}
	return previous, true, nil
}

// projectImages reads a project's image and the one it replaced. A project with
// no row is no image rather than an error: no project has one until it is saved.
func (s *Store) projectImages(ctx context.Context, project string) (current, previous ProjectImage, hasPrevious bool, err error) {
	var currentSavedAt, previousSavedAt int64
	err = s.db.QueryRowContext(ctx, `SELECT project, image, digest, saved_from, saved_at,
		previous_image, previous_digest, previous_saved_from, previous_saved_at
		FROM project_images WHERE project = ?`, project).Scan(
		&current.Project, &current.Image, &current.Digest, &current.SavedFrom, &currentSavedAt,
		&previous.Image, &previous.Digest, &previous.SavedFrom, &previousSavedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectImage{}, ProjectImage{}, false, nil
	}
	if err != nil {
		return ProjectImage{}, ProjectImage{}, false, err
	}
	current.SavedAt = projectImageTime(currentSavedAt)
	previous.Project = project
	previous.SavedAt = projectImageTime(previousSavedAt)
	return current, previous, previous.Image != "", nil
}

func projectImageTime(unix int64) time.Time {
	if unix == 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}

// SetProjectImage records the image a project's agents start from, and keeps the
// one it replaces for one revert. The project's first save has nothing to keep.
func (s *Store) SetProjectImage(ctx context.Context, image ProjectImage) error {
	if image.Project == "" || image.Image == "" {
		return errors.New("a project image needs a project and an image")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO project_images
		(project, image, digest, saved_from, saved_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(project) DO UPDATE SET
			previous_image = project_images.image,
			previous_digest = project_images.digest,
			previous_saved_from = project_images.saved_from,
			previous_saved_at = project_images.saved_at,
			image = excluded.image,
			digest = excluded.digest,
			saved_from = excluded.saved_from,
			saved_at = excluded.saved_at`,
		image.Project, image.Image, image.Digest, image.SavedFrom, image.SavedAt.Unix())
	if err != nil {
		return fmt.Errorf("recording the project image of %s: %w", image.Project, err)
	}
	return nil
}

// RevertProjectImage puts back the image the last save replaced and drops the
// one that replaced it. There is only ever one step to go back.
func (s *Store) RevertProjectImage(ctx context.Context, project string) (ProjectImage, error) {
	previous, ok, err := s.PreviousProjectImage(ctx, project)
	if err != nil {
		return ProjectImage{}, err
	}
	if !ok {
		return ProjectImage{}, fmt.Errorf("project %s has no previous project image: %w", project, ErrNotFound)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE project_images SET
		image = previous_image, digest = previous_digest,
		saved_from = previous_saved_from, saved_at = previous_saved_at,
		previous_image = '', previous_digest = '',
		previous_saved_from = '', previous_saved_at = 0
		WHERE project = ?`, project); err != nil {
		return ProjectImage{}, fmt.Errorf("going back to %s's previous project image: %w", project, err)
	}
	return previous, nil
}

// RemoveProjectImage deletes the image a project's agents start from, and with
// it the one a save kept: new agents run the agent image again. previous removes
// only what the last save kept, which leaves the project on its image.
func (s *Store) RemoveProjectImage(ctx context.Context, project string, previous bool) error {
	if previous {
		current, _, hasPrevious, err := s.projectImages(ctx, project)
		if err != nil {
			return err
		}
		if current.Image == "" || !hasPrevious {
			return fmt.Errorf("project %s has no previous project image: %w", project, ErrNotFound)
		}
		_, err = s.db.ExecContext(ctx, `UPDATE project_images SET
			previous_image = '', previous_digest = '',
			previous_saved_from = '', previous_saved_at = 0
			WHERE project = ?`, project)
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM project_images WHERE project = ?`, project)
	if err != nil {
		return fmt.Errorf("removing %s's project image: %w", project, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %s has no project image: %w", project, ErrNotFound)
	}
	return nil
}
