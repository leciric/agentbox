package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Skill is one stored skill: its name, which is also its folder's name in
// every agent, what its SKILL.md says it is for, where it came from, and the
// AgentBox-wide switch. Overrides are the projects that say otherwise.
type Skill struct {
	Name        string
	Description string
	// Source is where it was imported from (a path, a git URL, "claude", …),
	// or empty for one written in AgentBox.
	Source    string
	Enabled   bool
	Overrides map[string]bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SkillFile is one file of a skill's folder, by its slash-separated path
// inside it. SKILL.md is always one of them.
type SkillFile struct {
	Path    string
	Mode    uint32
	Content []byte
}

// EnabledFor says whether the skill is on in a project: its override there,
// or the AgentBox-wide switch. project "" is AgentBox-wide alone (the Home
// chat, which spans every project).
func (s Skill) EnabledFor(project string) bool {
	if on, ok := s.Overrides[project]; ok && project != "" {
		return on
	}
	return s.Enabled
}

// SetSkill stores a skill and its files whole, replacing the files it had.
// Its overrides are kept: editing a skill doesn't change where it's on.
func (s *Store) SetSkill(ctx context.Context, sk Skill, files []SkillFile) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	created := sk.CreatedAt
	if created.IsZero() {
		created = sk.UpdatedAt
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skills (name, description, source, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET description = excluded.description, source = excluded.source, enabled = excluded.enabled, updated_at = excluded.updated_at`,
		sk.Name, sk.Description, sk.Source, sk.Enabled, created.Unix(), sk.UpdatedAt.Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_files WHERE skill = ?`, sk.Name); err != nil {
		return err
	}
	for _, f := range files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_files (skill, path, mode, content) VALUES (?, ?, ?, ?)`, sk.Name, f.Path, f.Mode, f.Content); err != nil {
			return err
		}
	}
	for project, on := range sk.Overrides {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO skill_projects (skill, project, enabled) VALUES (?, ?, ?)`, sk.Name, project, on); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetSkillEnabled flips a skill's AgentBox-wide switch.
func (s *Store) SetSkillEnabled(ctx context.Context, name string, on bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE skills SET enabled = ? WHERE name = ?`, on, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("skill %s: %w", name, ErrNotFound)
	}
	return nil
}

// SetSkillOverride sets a project's say on a skill: on, off, or (nil) none,
// when it follows the AgentBox-wide switch again.
func (s *Store) SetSkillOverride(ctx context.Context, name, project string, on *bool) error {
	if _, err := s.Skill(ctx, name); err != nil {
		return err
	}
	if on == nil {
		_, err := s.db.ExecContext(ctx, `DELETE FROM skill_projects WHERE skill = ? AND project = ?`, name, project)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO skill_projects (skill, project, enabled) VALUES (?, ?, ?)`, name, project, *on)
	return err
}

// RemoveSkill forgets a skill, its files and its overrides.
func (s *Store) RemoveSkill(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM skills WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("skill %s: %w", name, ErrNotFound)
	}
	return nil
}

// Skills lists every stored skill by name, with its overrides.
func (s *Store) Skills(ctx context.Context) ([]Skill, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, description, source, enabled, created_at, updated_at FROM skills ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Skill
	byName := map[string]int{}
	for rows.Next() {
		var sk Skill
		var created, updated int64
		if err := rows.Scan(&sk.Name, &sk.Description, &sk.Source, &sk.Enabled, &created, &updated); err != nil {
			return nil, err
		}
		sk.CreatedAt, sk.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
		sk.Overrides = map[string]bool{}
		byName[sk.Name] = len(out)
		out = append(out, sk)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	overrides, err := s.db.QueryContext(ctx, `SELECT skill, project, enabled FROM skill_projects`)
	if err != nil {
		return nil, err
	}
	defer overrides.Close()
	for overrides.Next() {
		var skill, project string
		var on bool
		if err := overrides.Scan(&skill, &project, &on); err != nil {
			return nil, err
		}
		if i, ok := byName[skill]; ok {
			out[i].Overrides[project] = on
		}
	}
	return out, overrides.Err()
}

// Skill is one stored skill, without its files.
func (s *Store) Skill(ctx context.Context, name string) (Skill, error) {
	all, err := s.Skills(ctx)
	if err != nil {
		return Skill{}, err
	}
	for _, sk := range all {
		if sk.Name == name {
			return sk, nil
		}
	}
	return Skill{}, fmt.Errorf("skill %s: %w", name, ErrNotFound)
}

// SkillFiles is a skill's folder, by path.
func (s *Store) SkillFiles(ctx context.Context, name string) ([]SkillFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, mode, content FROM skill_files WHERE skill = ? ORDER BY path`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SkillFile
	for rows.Next() {
		var f SkillFile
		if err := rows.Scan(&f.Path, &f.Mode, &f.Content); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SkillFile is one file of a skill.
func (s *Store) SkillFile(ctx context.Context, name, path string) (SkillFile, error) {
	f := SkillFile{Path: path}
	err := s.db.QueryRowContext(ctx, `SELECT mode, content FROM skill_files WHERE skill = ? AND path = ?`, name, path).Scan(&f.Mode, &f.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return f, fmt.Errorf("%s in skill %s: %w", path, name, ErrNotFound)
	}
	return f, err
}

// SkillSizes is how many files each skill has and how many bytes they hold.
func (s *Store) SkillSizes(ctx context.Context) (map[string][2]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT skill, COUNT(*), SUM(LENGTH(content)) FROM skill_files GROUP BY skill`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var name string
		var n, size int64
		if err := rows.Scan(&name, &n, &size); err != nil {
			return nil, err
		}
		out[name] = [2]int64{n, size}
	}
	return out, rows.Err()
}
