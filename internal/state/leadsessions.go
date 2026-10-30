package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"agentbox/internal/naming"
)

// A project's chat in sessions: each a lead conversation of its own on one
// topic ("Prod: login 500s"), with its own context, compaction and prompt
// cache. A project has as many open as the user opens, and none once they
// have all been closed. A session's lead is an agents row like any lead's,
// named LeadSessionName(id), made the first time it is written to, so a
// session nobody has written in costs a row here and nothing else.
//
// Closing a session keeps it, conversation and all, in the chat's history
// (ClosedLeadSessions), to be read, reopened or continued from in a new
// session; only deleting it removes it. Either way its id is never handed out
// again (lead_session_ids): a new session that took it would take its lead's
// name, and with it the conversation, memory and agents kept under it.
//
// What a project knows is shared by every session: notes, memory, skills,
// schedules, the repository and the agents. What isn't is the conversation,
// what it is working on (memory.WorkingMemory.Sessions), and the agents it
// made (Agent.CreatedBySession), whose notices go back to it. Notices nobody
// owns go to the session most recently active (LastActiveLeadSession).

// MigratedSession is the id of the session every project chatted with before
// sessions got: that chat, whose lead is the row named LeadName. It is an
// ordinary session otherwise, and can be deleted like any.
const MigratedSession = "main"

// maxSessionID keeps a session's id, which is in its lead's name and paths,
// short.
const maxSessionID = 24

// maxTopic is the most a session's topic may be.
const maxTopic = 200

// LeadSession is one of a project's chat sessions.
type LeadSession struct {
	Project string
	ID      string
	Topic   string
	// Untitled says the session still has the topic it was opened with,
	// UntitledTopic, for want of one: its conversation names it (NameLeadSession)
	// unless the user does first (RenameLeadSession).
	Untitled bool
	// Color is the session's pastel, one of SessionColors, which the app tints
	// its tab and its agents with.
	Color     string
	CreatedAt time.Time
	// ClosedAt is when the session was closed, into the chat's history; zero
	// while it is open.
	ClosedAt time.Time
}

// Closed reports whether the session is in the chat's history.
func (ls LeadSession) Closed() bool { return !ls.ClosedAt.IsZero() }

// SessionColors are the colours a session can have, by name: the app holds
// what each looks like in its light and dark themes. Appending is safe;
// renaming or removing one leaves the sessions that have it uncoloured.
var SessionColors = []string{"rose", "peach", "lemon", "mint", "teal", "sky", "periwinkle", "lilac"}

// pickSessionColor is a colour none of the open sessions has, at random, or
// any at random once they all do.
func pickSessionColor(taken []string) string {
	free := slices.DeleteFunc(slices.Clone(SessionColors), func(c string) bool { return slices.Contains(taken, c) })
	if len(free) == 0 {
		free = SessionColors
	}
	return free[rand.IntN(len(free))]
}

// openSessionColors is the colours of a project's open sessions.
func openSessionColors(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, project string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT color FROM lead_sessions WHERE project = ? AND closed_at = 0`, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UntitledTopic is the topic of a session opened with none, until it is named.
const UntitledTopic = "New session"

// LeadSessionName is the agent name a session's lead runs under: LeadName for
// the migrated session, so a project's chat from before sessions is that
// one, and "lead.<id>" for any other, which no agent can be named
// (naming.Validate takes no dots).
func LeadSessionName(session string) string {
	if session == "" || session == MigratedSession {
		return LeadName
	}
	return LeadName + "." + session
}

// SessionOfLead is the session a lead's name says it runs, and whether the
// name is a lead's at all.
func SessionOfLead(name string) (string, bool) {
	if name == LeadName {
		return MigratedSession, true
	}
	id, ok := strings.CutPrefix(name, LeadName+".")
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// IsLeadName reports whether name is one a project's chat runs under.
func IsLeadName(name string) bool {
	_, ok := SessionOfLead(name)
	return ok
}

// Session is the chat session this agent is part of: for a lead, the one it
// runs; for any other agent, the one that made it and gets its notices, ""
// for none.
func (a Agent) Session() string {
	if a.IsLead() {
		if id, ok := SessionOfLead(a.Name); ok {
			return id
		}
		return MigratedSession
	}
	return a.CreatedBySession
}

// cleanTopic is a topic as it is kept, or why it can't be.
func cleanTopic(topic string) (string, error) {
	topic = strings.Join(strings.Fields(topic), " ")
	if topic == "" {
		return "", errors.New("a session needs a topic")
	}
	if len(topic) > maxTopic {
		return "", fmt.Errorf("a session's topic is a line, not a paragraph: keep it under %d characters", maxTopic)
	}
	return topic, nil
}

// AddLeadSession opens a new session on topic, with an id made from it: the
// topic's slug, numbered when any session the project ever had, open, closed
// or deleted, had that id.
func (s *Store) AddLeadSession(ctx context.Context, project, topic string, at time.Time) (LeadSession, error) {
	topic, err := cleanTopic(topic)
	if err != nil {
		return LeadSession{}, err
	}
	base := naming.Slug(topic)
	if len(base) > maxSessionID-4 {
		base = strings.TrimRight(base[:maxSessionID-4], "-")
	}
	return s.addLeadSession(ctx, project, topic, base, false, at)
}

// AddUntitledLeadSession opens a new session with no topic yet: it reads
// UntitledTopic until its first turn names it, and its id is "session",
// numbered, since a topic that comes later can't change it.
func (s *Store) AddUntitledLeadSession(ctx context.Context, project string, at time.Time) (LeadSession, error) {
	return s.addLeadSession(ctx, project, UntitledTopic, "", true, at)
}

func (s *Store) addLeadSession(ctx context.Context, project, topic, base string, untitled bool, at time.Time) (LeadSession, error) {
	if base == "" {
		base = "session"
	}
	for n := 1; n < 1000; n++ {
		id := base
		if n > 1 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		color, ok, err := s.claimLeadSessionID(ctx, project, id, topic, untitled, at)
		if err != nil {
			return LeadSession{}, err
		}
		if !ok {
			continue
		}
		return LeadSession{Project: project, ID: id, Topic: topic, Untitled: untitled, Color: color, CreatedAt: time.UnixMilli(at.UnixMilli())}, nil
	}
	return LeadSession{}, fmt.Errorf("session %q: %w", base, ErrExists)
}

// claimLeadSessionID opens a session with id, unless any session of the
// project ever had it, or a lead or conversation is still kept under its name
// (a lead row left by a session deleted while its chat was being made). It
// returns the colour the session got.
func (s *Store) claimLeadSessionID(ctx context.Context, project, id, topic string, untitled bool, at time.Time) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()
	var used int
	if err := tx.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM lead_session_ids WHERE project = ? AND id = ?)
			+ (SELECT count(*) FROM agents WHERE project = ? AND name = ?)
			+ (SELECT count(*) FROM chat_items WHERE project = ? AND agent = ?)`,
		project, id, project, LeadSessionName(id), project, LeadSessionName(id)).Scan(&used); err != nil {
		return "", false, err
	}
	if used > 0 {
		return "", false, nil
	}
	taken, err := openSessionColors(ctx, tx, project)
	if err != nil {
		return "", false, err
	}
	color := pickSessionColor(taken)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO lead_session_ids (project, id) VALUES (?, ?)`, []any{project, id}},
		{`INSERT INTO lead_sessions (project, id, topic, created_at, untitled, color) VALUES (?, ?, ?, ?, ?, ?)`, []any{project, id, topic, at.UnixMilli(), untitled, color}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return "", false, nil
			}
			return "", false, err
		}
	}
	return color, true, tx.Commit()
}

// AddLeadSessionID opens a session with an id of the caller's: the migrated
// session, for a project chatted with as it always was (agent.EnsureLead).
func (s *Store) AddLeadSessionID(ctx context.Context, project, id, topic string, at time.Time) error {
	topic, err := cleanTopic(topic)
	if err != nil {
		return err
	}
	taken, err := openSessionColors(ctx, s.db, project)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO lead_sessions (project, id, topic, created_at, color) VALUES (?, ?, ?, ?, ?)`, project, id, topic, at.UnixMilli(), pickSessionColor(taken))
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("session %s of %s: %w", id, project, ErrExists)
	}
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO lead_session_ids (project, id) VALUES (?, ?)`, project, id)
	return err
}

// leadSessionColumns is what scanLeadSession reads.
const leadSessionColumns = `project, id, topic, created_at, untitled, closed_at, color`

// LeadSessions lists a project's open sessions, oldest first.
func (s *Store) LeadSessions(ctx context.Context, project string) ([]LeadSession, error) {
	return s.leadSessions(ctx, `SELECT `+leadSessionColumns+` FROM lead_sessions WHERE project = ? AND closed_at = 0 ORDER BY created_at, id`, project)
}

// ClosedLeadSessions lists the sessions in a project's history, the most
// recently closed first.
func (s *Store) ClosedLeadSessions(ctx context.Context, project string) ([]LeadSession, error) {
	return s.leadSessions(ctx, `SELECT `+leadSessionColumns+` FROM lead_sessions WHERE project = ? AND closed_at != 0 ORDER BY closed_at DESC, id`, project)
}

func (s *Store) leadSessions(ctx context.Context, query, project string) ([]LeadSession, error) {
	rows, err := s.db.QueryContext(ctx, query, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LeadSession
	for rows.Next() {
		ls, err := scanLeadSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ls)
	}
	return out, rows.Err()
}

// LeadSession is one session of a project's chat, open or closed.
func (s *Store) LeadSession(ctx context.Context, project, id string) (LeadSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+leadSessionColumns+` FROM lead_sessions WHERE project = ? AND id = ?`, project, id)
	ls, err := scanLeadSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LeadSession{}, fmt.Errorf("session %s of %s: %w", id, project, ErrNotFound)
	}
	return ls, err
}

func scanLeadSession(row interface{ Scan(...any) error }) (LeadSession, error) {
	var ls LeadSession
	var created, closed int64
	if err := row.Scan(&ls.Project, &ls.ID, &ls.Topic, &created, &ls.Untitled, &closed, &ls.Color); err != nil {
		return LeadSession{}, err
	}
	ls.CreatedAt = time.UnixMilli(created)
	if closed != 0 {
		ls.ClosedAt = time.UnixMilli(closed)
	}
	return ls, nil
}

// LeadSessionActivity is when each of a project's open sessions was last
// active, by id: its conversation's newest message, or when it was opened
// when it has none.
func (s *Store) LeadSessionActivity(ctx context.Context, project string) (map[string]time.Time, error) {
	sessions, err := s.LeadSessions(ctx, project)
	if err != nil {
		return nil, err
	}
	return s.leadSessionActivity(ctx, project, sessions)
}

// ClosedLeadSessionActivity is LeadSessionActivity for the sessions in the
// project's history.
func (s *Store) ClosedLeadSessionActivity(ctx context.Context, project string) (map[string]time.Time, error) {
	sessions, err := s.ClosedLeadSessions(ctx, project)
	if err != nil {
		return nil, err
	}
	return s.leadSessionActivity(ctx, project, sessions)
}

func (s *Store) leadSessionActivity(ctx context.Context, project string, sessions []LeadSession) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(sessions))
	for _, ls := range sessions {
		at := ls.CreatedAt
		var data string
		err := s.db.QueryRowContext(ctx,
			`SELECT data FROM chat_items WHERE project = ? AND agent = ? ORDER BY position DESC LIMIT 1`,
			project, LeadSessionName(ls.ID)).Scan(&data)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		var item struct {
			CreatedAt time.Time `json:"createdAt"`
			UpdatedAt time.Time `json:"updatedAt"`
		}
		if data != "" && json.Unmarshal([]byte(data), &item) == nil {
			for _, t := range []time.Time{item.CreatedAt, item.UpdatedAt} {
				if t.After(at) {
					at = t
				}
			}
		}
		out[ls.ID] = at
	}
	return out, nil
}

// LastActiveLeadSession is the project's open session most recently active,
// the one a notice nobody owns goes to; ErrNotFound when it has none open.
func (s *Store) LastActiveLeadSession(ctx context.Context, project string) (LeadSession, error) {
	sessions, err := s.LeadSessions(ctx, project)
	if err != nil {
		return LeadSession{}, err
	}
	activity, err := s.LeadSessionActivity(ctx, project)
	if err != nil {
		return LeadSession{}, err
	}
	var best LeadSession
	for _, ls := range sessions {
		if best.ID == "" || !activity[ls.ID].Before(activity[best.ID]) {
			best = ls
		}
	}
	if best.ID == "" {
		return LeadSession{}, fmt.Errorf("%s's chat has no session: %w", project, ErrNotFound)
	}
	return best, nil
}

// RenameLeadSession changes a session's topic, the user's: nothing names it
// after that. Its id stays.
func (s *Store) RenameLeadSession(ctx context.Context, project, id, topic string) error {
	topic, err := cleanTopic(topic)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE lead_sessions SET topic = ?, untitled = 0 WHERE project = ? AND id = ?`, topic, project, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("session %s of %s: %w", id, project, ErrNotFound)
	}
	return nil
}

// NameLeadSession gives an untitled session the topic its conversation was
// found to be about. It reports false, and changes nothing, for a session
// already named, by the user or before: a name the user typed is never
// overwritten, even one typed while this topic was being worked out.
func (s *Store) NameLeadSession(ctx context.Context, project, id, topic string) (bool, error) {
	topic, err := cleanTopic(topic)
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE lead_sessions SET topic = ?, untitled = 0 WHERE project = ? AND id = ? AND untitled = 1`, topic, project, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CloseLeadSession moves an open session to the chat's history, its
// conversation, lead and agents kept as they are: the agents' notices go to
// the session most recently active while it is closed (daemon.leadOf), and
// back to it once it is reopened.
func (s *Store) CloseLeadSession(ctx context.Context, project, id string, at time.Time) error {
	return s.setLeadSessionClosed(ctx, project, id, at.UnixMilli(), `closed_at = 0`)
}

// ReopenLeadSession moves a closed session from the history back among the
// open ones.
func (s *Store) ReopenLeadSession(ctx context.Context, project, id string) error {
	return s.setLeadSessionClosed(ctx, project, id, 0, `closed_at != 0`)
}

func (s *Store) setLeadSessionClosed(ctx context.Context, project, id string, closedAt int64, was string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE lead_sessions SET closed_at = ? WHERE project = ? AND id = ? AND `+was, closedAt, project, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	ls, err := s.LeadSession(ctx, project, id)
	if err != nil {
		return err
	}
	if ls.Closed() {
		return fmt.Errorf("session %s of %s is already closed", id, project)
	}
	return fmt.Errorf("session %s of %s is open", id, project)
}

// DeleteLeadSession deletes a session, open or closed: its row, its lead's,
// and its conversation, in one transaction. Its id stays taken. The agents it
// made are left to nobody, and it returns them.
func (s *Store) DeleteLeadSession(ctx context.Context, project, id string) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM lead_sessions WHERE project = ? AND id = ?`, project, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("session %s of %s: %w", id, project, ErrNotFound)
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT name FROM agents WHERE project = ? AND created_by_session = ? AND role != ? ORDER BY name`, project, id, RoleLead)
	if err != nil {
		return nil, err
	}
	var freed []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		freed = append(freed, name)
	}
	_ = rows.Close()
	name := LeadSessionName(id)
	for _, q := range []string{
		`UPDATE agents SET created_by_session = '' WHERE project = ? AND created_by_session = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, project, id); err != nil {
			return nil, err
		}
	}
	for _, q := range []string{
		`DELETE FROM agents WHERE project = ? AND name = ? AND role = 'lead'`,
		`DELETE FROM chat_items WHERE project = ? AND agent = ?`,
		`DELETE FROM running_turns WHERE project = ? AND agent = ?`,
		`DELETE FROM chats WHERE project = ? AND agent = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, project, name); err != nil {
			return nil, err
		}
	}
	return freed, tx.Commit()
}

// SetAgentSession hands an agent to a session of its project's chat, whose
// notices it goes to from now on; "" to none.
func (s *Store) SetAgentSession(ctx context.Context, project, name, session string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agents SET created_by_session = ? WHERE project = ? AND name = ? AND role != ?`,
		session, project, name, RoleLead)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("agent %s/%s: %w", project, name, ErrNotFound)
	}
	return nil
}

// RemoveLeadSessions forgets every session of a project with their
// conversations, the closed ones too: the project's chat was reset. Their ids
// stay taken.
func (s *Store) RemoveLeadSessions(ctx context.Context, project string) error {
	sessions, err := s.AllLeadSessions(ctx, project)
	if err != nil {
		return err
	}
	for _, ls := range sessions {
		if _, err := s.DeleteLeadSession(ctx, project, ls.ID); err != nil {
			return err
		}
	}
	return nil
}

// AllLeadSessions is every session of a project, open or closed.
func (s *Store) AllLeadSessions(ctx context.Context, project string) ([]LeadSession, error) {
	return s.leadSessions(ctx, `SELECT `+leadSessionColumns+` FROM lead_sessions WHERE project = ? ORDER BY created_at, id`, project)
}
