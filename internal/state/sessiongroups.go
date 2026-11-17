package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// SessionGroup is one of a chat's tab groups: a button in the row above the
// chat with its sessions in a menu (the app's SessionTabs), named and
// coloured by the user. Sessions join it by LeadSession.Group; a pinned one
// can't. A group goes as soon as no session, open or closed, is in it: one
// whose sessions are all closed stays, hidden, for when one is reopened.
type SessionGroup struct {
	Project string
	ID      string
	// Name is what its button says; "" until the user names it, when the
	// button shows only its colour and count.
	Name string
	// Color is one of SessionColors.
	Color string
	// Collapsed folds its sessions into its button's menu; false spreads them
	// as tabs after it.
	Collapsed bool
	// Position is where it sits among the groups, from 1.
	Position int
}

// maxGroupName is the most a group's name may be: a button's label.
const maxGroupName = 60

// SessionGroupPatch changes a group; what it leaves nil stays.
type SessionGroupPatch struct {
	Name      *string
	Color     *string
	Collapsed *bool
}

func cleanGroupName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if len(name) > maxGroupName {
		return "", fmt.Errorf("a group's name is a button's label: keep it under %d characters", maxGroupName)
	}
	return name, nil
}

func checkGroupColor(color string) error {
	if !slices.Contains(SessionColors, color) {
		return fmt.Errorf("a group's colour is one of %s, not %q", strings.Join(SessionColors, ", "), color)
	}
	return nil
}

// SessionGroups lists a chat's tab groups in their order, the hidden ones
// (every session in them closed) too.
func (s *Store) SessionGroups(ctx context.Context, project string) ([]SessionGroup, error) {
	return sessionGroups(ctx, s.db, project)
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func sessionGroups(ctx context.Context, q querier, project string) ([]SessionGroup, error) {
	rows, err := q.QueryContext(ctx, `SELECT project, id, name, color, collapsed, position FROM lead_session_groups
		WHERE project = ? ORDER BY position, id`, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SessionGroup
	for rows.Next() {
		var g SessionGroup
		if err := rows.Scan(&g.Project, &g.ID, &g.Name, &g.Color, &g.Collapsed, &g.Position); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SessionGroup is one of a chat's tab groups.
func (s *Store) SessionGroup(ctx context.Context, project, id string) (SessionGroup, error) {
	all, err := s.SessionGroups(ctx, project)
	if err != nil {
		return SessionGroup{}, err
	}
	for _, g := range all {
		if g.ID == id {
			return g, nil
		}
	}
	return SessionGroup{}, fmt.Errorf("tab group %s of %s: %w", id, project, ErrNotFound)
}

// AddSessionGroup makes a tab group of sessions, the first of them at least,
// after the chat's other groups: named name ("" for unnamed) and coloured
// color, or with "" a colour no other group of the chat has while one is
// free. The sessions leave the groups they were in, which go if that left
// them empty; a pinned or closed one is an error.
func (s *Store) AddSessionGroup(ctx context.Context, project, name, color string, sessions []string) (SessionGroup, error) {
	name, err := cleanGroupName(name)
	if err != nil {
		return SessionGroup{}, err
	}
	if color != "" {
		if err := checkGroupColor(color); err != nil {
			return SessionGroup{}, err
		}
	}
	if len(sessions) == 0 {
		return SessionGroup{}, errors.New("a tab group needs a session to start with")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionGroup{}, err
	}
	defer func() { _ = tx.Rollback() }()
	groups, err := sessionGroups(ctx, tx, project)
	if err != nil {
		return SessionGroup{}, err
	}
	if color == "" {
		taken := make([]string, 0, len(groups))
		for _, g := range groups {
			taken = append(taken, g.Color)
		}
		color = pickSessionColor(taken)
	}
	position := 1
	if len(groups) > 0 {
		position = slices.MaxFunc(groups, func(a, b SessionGroup) int { return a.Position - b.Position }).Position + 1
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	g := SessionGroup{Project: project, ID: "g" + hex.EncodeToString(b), Name: name, Color: color, Collapsed: true, Position: position}
	if _, err := tx.ExecContext(ctx, `INSERT INTO lead_session_groups (project, id, name, color, collapsed, position) VALUES (?, ?, ?, ?, 1, ?)`,
		project, g.ID, g.Name, g.Color, g.Position); err != nil {
		return SessionGroup{}, err
	}
	for _, id := range sessions {
		if err := joinSessionGroup(ctx, tx, project, id, g.ID); err != nil {
			return SessionGroup{}, err
		}
	}
	if err := dropEmptySessionGroups(ctx, tx, project); err != nil {
		return SessionGroup{}, err
	}
	return g, tx.Commit()
}

// UpdateSessionGroup renames, recolours, folds or spreads a tab group.
func (s *Store) UpdateSessionGroup(ctx context.Context, project, id string, patch SessionGroupPatch) (SessionGroup, error) {
	var set []string
	var args []any
	if patch.Name != nil {
		name, err := cleanGroupName(*patch.Name)
		if err != nil {
			return SessionGroup{}, err
		}
		set, args = append(set, `name = ?`), append(args, name)
	}
	if patch.Color != nil {
		if err := checkGroupColor(*patch.Color); err != nil {
			return SessionGroup{}, err
		}
		set, args = append(set, `color = ?`), append(args, *patch.Color)
	}
	if patch.Collapsed != nil {
		set, args = append(set, `collapsed = ?`), append(args, *patch.Collapsed)
	}
	if len(set) > 0 {
		res, err := s.db.ExecContext(ctx, `UPDATE lead_session_groups SET `+strings.Join(set, ", ")+` WHERE project = ? AND id = ?`, append(args, project, id)...)
		if err != nil {
			return SessionGroup{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return SessionGroup{}, fmt.Errorf("tab group %s of %s: %w", id, project, ErrNotFound)
		}
	}
	return s.SessionGroup(ctx, project, id)
}

// DeleteSessionGroup ungroups a tab group: its sessions, open and closed,
// become loose tabs where they were, and the group goes.
func (s *Store) DeleteSessionGroup(ctx context.Context, project, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM lead_session_groups WHERE project = ? AND id = ?`, project, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("tab group %s of %s: %w", id, project, ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE lead_sessions SET tab_group = '' WHERE project = ? AND tab_group = ?`, project, id); err != nil {
		return err
	}
	return tx.Commit()
}

// OrderSessionGroups keeps the order a chat's groups were dragged into: ids,
// its groups in their new order. One left out goes after them, in the order
// it had; an id that isn't a group of the chat is an error.
func (s *Store) OrderSessionGroups(ctx context.Context, project string, ids []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	groups, err := sessionGroups(ctx, tx, project)
	if err != nil {
		return err
	}
	order := make([]string, 0, len(groups))
	for _, id := range ids {
		if !slices.ContainsFunc(groups, func(g SessionGroup) bool { return g.ID == id }) {
			return fmt.Errorf("tab group %s of %s: %w", id, project, ErrNotFound)
		}
		if !slices.Contains(order, id) {
			order = append(order, id)
		}
	}
	for _, g := range groups {
		if !slices.Contains(order, g.ID) {
			order = append(order, g.ID)
		}
	}
	for i, id := range order {
		if _, err := tx.ExecContext(ctx, `UPDATE lead_session_groups SET position = ? WHERE project = ? AND id = ?`, i+1, project, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetLeadSessionGroup moves a session into a tab group, last among its tabs,
// or with "" out of the one it is in. A pinned session can't join one: unpin
// it first. A group it leaves empty goes.
func (s *Store) SetLeadSessionGroup(ctx context.Context, project, id, group string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if group == "" {
		res, err := tx.ExecContext(ctx, `UPDATE lead_sessions SET tab_group = '' WHERE project = ? AND id = ?`, project, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("session %s of %s: %w", id, project, ErrNotFound)
		}
	} else {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM lead_session_groups WHERE project = ? AND id = ?`, project, group).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("tab group %s of %s: %w", group, project, ErrNotFound)
			}
			return err
		}
		// Moved in by hand, it goes last of the group's tabs: while the tabs
		// were never dragged, all at 0 and in the order they opened, that
		// order is written down first, or one opened earlier would sort
		// before the group's own.
		if err := numberTabs(ctx, tx, project); err != nil {
			return err
		}
		if err := joinSessionGroup(ctx, tx, project, id, group); err != nil {
			return err
		}
	}
	if err := dropEmptySessionGroups(ctx, tx, project); err != nil {
		return err
	}
	return tx.Commit()
}

// numberTabs gives a chat's open sessions the tab order they have, by when
// they opened, while none has one of its own (lastTab).
func numberTabs(ctx context.Context, tx *sql.Tx, project string) error {
	var ordered bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM lead_sessions WHERE project = ? AND closed_at = 0 AND tab_order > 0)`, project).Scan(&ordered); err != nil {
		return err
	}
	if ordered {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE lead_sessions SET tab_order = (
		SELECT count(*) FROM lead_sessions o WHERE o.project = lead_sessions.project AND o.closed_at = 0
			AND (o.created_at, o.id) <= (lead_sessions.created_at, lead_sessions.id))
		WHERE project = ? AND closed_at = 0`, project)
	return err
}

// joinSessionGroup puts an open, unpinned session in group, after the tabs
// it has.
func joinSessionGroup(ctx context.Context, tx *sql.Tx, project, id, group string) error {
	var pinned bool
	var closed int64
	var had string
	err := tx.QueryRowContext(ctx, `SELECT pinned, closed_at, tab_group FROM lead_sessions WHERE project = ? AND id = ?`, project, id).Scan(&pinned, &closed, &had)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("session %s of %s: %w", id, project, ErrNotFound)
	}
	if err != nil {
		return err
	}
	switch {
	case pinned:
		return fmt.Errorf("session %s of %s is pinned: a pinned session can't join a tab group, unpin it first", id, project)
	case closed != 0:
		return fmt.Errorf("session %s of %s is closed: reopen it first", id, project)
	case had == group:
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE lead_sessions SET tab_group = ?, tab_order = `+lastTab+` WHERE project = ? AND id = ?`,
		group, project, id, project, id, project, id)
	return err
}

// dropEmptySessionGroups deletes a chat's groups no session is in any more.
func dropEmptySessionGroups(ctx context.Context, tx *sql.Tx, project string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM lead_session_groups WHERE project = ?
		AND id NOT IN (SELECT tab_group FROM lead_sessions WHERE project = ?)`, project, project)
	return err
}
