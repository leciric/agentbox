package memory

import (
	"context"
	"errors"
	"fmt"
)

// AgentBox-wide memory: what holds in every project rather than one — the
// user's preferences ("Agent preference: one agent at a time"), conventions
// they want everywhere. It is the same table and the same rows as a project's
// memory, kept under Global, which no project can be called: a project's name
// starts with a letter (naming.ProjectSlug), and the Home chat's key starts
// with an underscore.
//
// It is only ever written on purpose. A project's lead writes here when the
// user asks for something to apply to all projects, the Home chat writes here
// because it has no project of its own, and nothing harvests it: no
// distillation, decay or tidy touches it, because each of those runs over one
// project's rows (ai-memory's cross-project profile was the background, its
// automatic promotion is deliberately not).
//
// Every project reads it: Search returns it beside the project's own
// memories, and a context has a section of it (context.go), both within what
// they already spend. Memory, by id, stays scoped; scopeOf is how resolving
// one from a project's chat finds it.

// Global is the project an AgentBox-wide memory is kept under.
const Global = "*"

// scopeOf is where a memory a project's chat names lives: the project's own,
// or AgentBox-wide. Anything else is ErrNotFound, as Memory has it.
func (s *Store) scopeOf(ctx context.Context, project, id string) (string, error) {
	if _, err := s.Memory(ctx, project, id); err == nil || !errors.Is(err, ErrNotFound) || project == Global {
		return project, err
	}
	if _, err := s.Memory(ctx, Global, id); err != nil {
		return "", notFound("memory", id)
	}
	return Global, nil
}

// crossScope explains a superseded memory that isn't in the scope being
// written to, when it is in the other one: superseding across the two would
// hide a memory where it was never written.
func crossScope(ctx context.Context, s *Store, project, id string, err error) error {
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	var owner string
	if s.db.QueryRowContext(ctx, `SELECT project FROM memories WHERE id = ?`, id).Scan(&owner) != nil {
		return err
	}
	switch {
	case owner == Global:
		return fmt.Errorf("%s is remembered for all projects: replace it with another memory for all projects, or resolve it", id)
	case project == Global:
		return fmt.Errorf("%s is one project's memory, and a memory for all projects can't replace it: resolve it in its project", id)
	}
	return err
}

// DeleteMemory removes a memory for good, with every memory it superseded:
// those would otherwise come back to life the moment nothing names them. It
// is the user's, from the app, for a memory they never want read again; a
// model closes or replaces one instead, which keeps the history.
func (s *Store) DeleteMemory(ctx context.Context, project, id string) error {
	if err := requireProject(project); err != nil {
		return err
	}
	if _, err := s.Memory(ctx, project, id); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	const chain = `WITH RECURSIVE chain(id) AS (
		SELECT ?
		UNION SELECT m.supersedes_id FROM memories m JOIN chain ON m.id = chain.id
		 WHERE m.project = ? AND m.supersedes_id IS NOT NULL)`
	for _, q := range []string{
		chain + ` DELETE FROM memory_anchors WHERE project = ? AND memory_id IN chain`,
		chain + ` DELETE FROM memories WHERE project = ? AND id IN chain`,
	} {
		if _, err := tx.ExecContext(ctx, q, id, project, project); err != nil {
			return err
		}
	}
	return tx.Commit()
}
