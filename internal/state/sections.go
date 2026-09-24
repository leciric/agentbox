package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// How the user organised the projects list
// ([D79](../../docs/implementation/decisions.md#d79)).
//
// A section is a row of its own, not a name written on each project: renaming
// one touches one row rather than every project in it, and an empty section
// can exist while the user fills it. A project points at a section, or at
// none — a project in no section is shown all the same, so an installation
// that never makes a section reads exactly as it did before sections existed.
//
// Order is an integer position, the way chat_items holds one. Positions are
// rewritten from 1 by whatever changed them, in one transaction, so a list is
// always 1..n with no gap and no duplicate. Position 0 is the exception and
// means "never placed by hand": it sorts last, by name, which is what makes an
// unorganised installation alphabetical.

// MaxSectionNameLen bounds a section's name: it is a label in a 272-pixel
// sidebar, not a sentence.
const MaxSectionNameLen = 40

// Section is one group of projects in the sidebar.
type Section struct {
	ID   string
	Name string
	// Position is where the section sits among the sections, from 1.
	Position int
	// Collapsed is whether the user folded it away. It is stored here rather
	// than in the app so a machine talking to the same daemon — or to the
	// same hub — shows the sidebar the same way.
	Collapsed bool
	CreatedAt time.Time
}

// Layout is the whole of the sidebar's order: every section, in order, with
// the projects in each, and the projects that are in no section. A caller
// sends the whole thing rather than one move, so applying it can't interleave
// with another (D79).
//
// It need not be complete. A section or a project the layout doesn't name
// stays where it is, at the end of the list it is already in; a project it
// names that no longer exists is skipped. That is what makes a layout sent
// from a sidebar that was drawn a moment ago safe to apply.
type Layout struct {
	Sections []SectionProjects
	// Loose are the projects in no section, in order.
	Loose []string
}

// SectionProjects is one section of a Layout: which section, and what is in it.
type SectionProjects struct {
	ID       string
	Projects []string
}

const sectionColumns = `id, name, position, collapsed, created_at`

// Sections lists the sections in the order the sidebar draws them.
func (s *Store) Sections(ctx context.Context) ([]Section, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sectionColumns+` FROM project_sections `+sectionOrder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSections(rows)
}

const sectionOrder = `ORDER BY position, created_at, id`

func scanSections(rows *sql.Rows) ([]Section, error) {
	var sections []Section
	for rows.Next() {
		var sec Section
		var created int64
		if err := rows.Scan(&sec.ID, &sec.Name, &sec.Position, &sec.Collapsed, &created); err != nil {
			return nil, err
		}
		sec.CreatedAt = time.Unix(created, 0)
		sections = append(sections, sec)
	}
	return sections, rows.Err()
}

// Section is one section by id.
func (s *Store) Section(ctx context.Context, id string) (Section, error) {
	var sec Section
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT `+sectionColumns+` FROM project_sections WHERE id = ?`, id).
		Scan(&sec.ID, &sec.Name, &sec.Position, &sec.Collapsed, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Section{}, fmt.Errorf("section %q: %w", id, ErrNotFound)
	}
	sec.CreatedAt = time.Unix(created, 0)
	return sec, err
}

// AddSection makes an empty section at the end of the list. Names aren't
// unique: a section is identified by its id, and refusing a rename because
// another section is called the same would help nobody.
func (s *Store) AddSection(ctx context.Context, name string, now time.Time) (Section, error) {
	name, err := sectionName(name)
	if err != nil {
		return Section{}, err
	}
	sec := Section{ID: newSectionID(), Name: name, CreatedAt: now.Truncate(time.Second)}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Section{}, err
	}
	defer tx.Rollback()
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(position), 0) FROM project_sections`).Scan(&last); err != nil {
		return Section{}, err
	}
	sec.Position = last + 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_sections (`+sectionColumns+`) VALUES (?, ?, ?, ?, ?)`,
		sec.ID, sec.Name, sec.Position, sec.Collapsed, sec.CreatedAt.Unix()); err != nil {
		return Section{}, err
	}
	if err := tx.Commit(); err != nil {
		return Section{}, err
	}
	return sec, nil
}

// UpdateSection renames a section, folds it away or unfolds it. A nil field
// stays as it is.
func (s *Store) UpdateSection(ctx context.Context, id string, name *string, collapsed *bool) (Section, error) {
	sec, err := s.Section(ctx, id)
	if err != nil {
		return Section{}, err
	}
	if name != nil {
		if sec.Name, err = sectionName(*name); err != nil {
			return Section{}, err
		}
	}
	if collapsed != nil {
		sec.Collapsed = *collapsed
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE project_sections SET name = ?, collapsed = ? WHERE id = ?`,
		sec.Name, sec.Collapsed, sec.ID); err != nil {
		return Section{}, err
	}
	return sec, nil
}

// RemoveSection deletes a section and keeps every project that was in it: they
// go back to being in no section, at the end of that list. Nothing about a
// project is deleted here.
func (s *Store) RemoveSection(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM project_sections WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("section %q: %w", id, ErrNotFound)
	}
	// renumber is what frees the projects: a project whose section is gone is
	// loose, and it says so in one place rather than in every caller.
	if err := renumber(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// SetProjectLayout applies a whole new order in one transaction: the sections,
// and the projects within each and outside them all. Positions are written
// from 1, so nothing it leaves behind has a gap or a duplicate.
func (s *Store) SetProjectLayout(ctx context.Context, l Layout) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	sections, err := sectionsTx(ctx, tx)
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, sec := range sections {
		exists[sec.ID] = true
	}

	// The sections the layout names, in its order, then the ones it didn't —
	// a section made in another window while this one was being dragged —
	// after them, in the order they are in now.
	listed := map[string]bool{}
	var order []string
	for _, sp := range l.Sections {
		if !exists[sp.ID] {
			return fmt.Errorf("section %q: %w", sp.ID, ErrNotFound)
		}
		if listed[sp.ID] {
			return fmt.Errorf("section %q is in the layout twice", sp.ID)
		}
		listed[sp.ID] = true
		order = append(order, sp.ID)
	}
	for _, sec := range sections {
		if !listed[sec.ID] {
			order = append(order, sec.ID)
		}
	}
	for i, id := range order {
		if _, err := tx.ExecContext(ctx, `UPDATE project_sections SET position = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}

	current, err := projectPlacesTx(ctx, tx)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, p := range current {
		known[p.name] = true
	}
	placed := map[string]bool{}
	lists := map[string][]string{}
	take := func(section string, names []string) error {
		for _, name := range names {
			if !known[name] {
				continue // removed while the user was dragging
			}
			if placed[name] {
				return fmt.Errorf("project %q is in the layout twice", name)
			}
			placed[name] = true
			lists[section] = append(lists[section], name)
		}
		return nil
	}
	if err := take("", l.Loose); err != nil {
		return err
	}
	for _, sp := range l.Sections {
		if err := take(sp.ID, sp.Projects); err != nil {
			return err
		}
	}
	// A project the layout doesn't mention — added while the user was
	// dragging — stays in the list it is in, at the end of it.
	for _, p := range current {
		if placed[p.name] {
			continue
		}
		section := p.section
		if !exists[section] {
			section = ""
		}
		lists[section] = append(lists[section], p.name)
	}
	for section, names := range lists {
		for i, name := range names {
			if _, err := tx.ExecContext(ctx, `UPDATE projects SET section = ?, position = ? WHERE name = ?`,
				section, i+1, name); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// projectPlace is where one project sits now: enough to put back what a
// layout didn't mention.
type projectPlace struct {
	name    string
	section string
}

func projectPlacesTx(ctx context.Context, tx *sql.Tx) ([]projectPlace, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name, section FROM projects `+projectPlaceOrder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var places []projectPlace
	for rows.Next() {
		var p projectPlace
		if err := rows.Scan(&p.name, &p.section); err != nil {
			return nil, err
		}
		places = append(places, p)
	}
	return places, rows.Err()
}

// projectPlaceOrder is the order within one list, which is also the order the
// whole table is read in when something is being renumbered: placed projects
// first, in their order, then the ones nobody has placed, by name.
const projectPlaceOrder = `ORDER BY section, CASE WHEN position = 0 THEN 1 ELSE 0 END, position, name`

func sectionsTx(ctx context.Context, tx *sql.Tx) ([]Section, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+sectionColumns+` FROM project_sections `+sectionOrder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSections(rows)
}

// renumber rewrites every position from the order the rows are already in, and
// frees any project whose section no longer exists. It is what a change that
// isn't a layout — a section deleted, a project removed — ends with, so the
// invariant holds after every write: each list is 1..n, and a project in no
// section is in no section by saying so.
func renumber(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET section = '', position = 0
		 WHERE section <> '' AND section NOT IN (SELECT id FROM project_sections)`); err != nil {
		return err
	}
	sections, err := sectionsTx(ctx, tx)
	if err != nil {
		return err
	}
	for i, sec := range sections {
		if _, err := tx.ExecContext(ctx, `UPDATE project_sections SET position = ? WHERE id = ?`, i+1, sec.ID); err != nil {
			return err
		}
	}
	places, err := projectPlacesTx(ctx, tx)
	if err != nil {
		return err
	}
	next := map[string]int{}
	for _, p := range places {
		next[p.section]++
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET position = ? WHERE name = ?`, next[p.section], p.name); err != nil {
			return err
		}
	}
	return nil
}

// renumberProjects is renumber in a transaction of its own, for the callers
// that only removed something.
func (s *Store) renumberProjects(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := renumber(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func sectionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("a section needs a name")
	case len([]rune(name)) > MaxSectionNameLen:
		return "", fmt.Errorf("the section name %q is longer than %d characters", name, MaxSectionNameLen)
	case strings.ContainsAny(name, "\n\r\t"):
		return "", fmt.Errorf("the section name %q has a line break in it", name)
	}
	return name, nil
}

// newSectionID is a short id with a prefix that says what it names, the way
// package memory makes one.
func newSectionID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("reading random bytes: %v", err))
	}
	return "sec_" + hex.EncodeToString(b)
}
