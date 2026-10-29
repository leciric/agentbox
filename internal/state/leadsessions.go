package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/naming"
)

// A project's chat in sessions: each a lead conversation of its own on one
// topic ("Prod: login 500s"), with its own context, compaction and prompt
// cache. A project has as many as the user opens, and none once they have all
// been deleted. A session's lead is an agents row like any lead's, named
// LeadSessionName(id), made the first time it is written to, so a session
// nobody has written in costs a row here and nothing else.
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
	Project   string
	ID        string
	Topic     string
	CreatedAt time.Time
}

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
	return id, ok && id != ""
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
// topic's slug, numbered when the project has a session by that id, or had
// one whose conversation is still kept.
func (s *Store) AddLeadSession(ctx context.Context, project, topic string, at time.Time) (LeadSession, error) {
	topic, err := cleanTopic(topic)
	if err != nil {
		return LeadSession{}, err
	}
	base := naming.Slug(topic)
	if len(base) > maxSessionID-4 {
		base = strings.TrimRight(base[:maxSessionID-4], "-")
	}
	if base == "" {
		base = "session"
	}
	for n := 1; n < 1000; n++ {
		id := base
		if n > 1 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		// A lead row left by a session deleted while its chat was being made
		// would hand its name on; so would its conversation.
		var used int
		if err := s.db.QueryRowContext(ctx,
			`SELECT (SELECT count(*) FROM agents WHERE project = ? AND name = ?) + (SELECT count(*) FROM chat_items WHERE project = ? AND agent = ?)`,
			project, LeadSessionName(id), project, LeadSessionName(id)).Scan(&used); err != nil {
			return LeadSession{}, err
		}
		if used > 0 {
			continue
		}
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO lead_sessions (project, id, topic, created_at) VALUES (?, ?, ?, ?)`,
			project, id, topic, at.UnixMilli())
		if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
			continue
		}
		if err != nil {
			return LeadSession{}, err
		}
		return LeadSession{Project: project, ID: id, Topic: topic, CreatedAt: time.UnixMilli(at.UnixMilli())}, nil
	}
	return LeadSession{}, fmt.Errorf("session %q: %w", base, ErrExists)
}

// AddLeadSessionID opens a session with an id of the caller's: the migrated
// session, for a project chatted with as it always was (agent.EnsureLead).
func (s *Store) AddLeadSessionID(ctx context.Context, project, id, topic string, at time.Time) error {
	topic, err := cleanTopic(topic)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO lead_sessions (project, id, topic, created_at) VALUES (?, ?, ?, ?)`, project, id, topic, at.UnixMilli())
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("session %s of %s: %w", id, project, ErrExists)
	}
	return err
}

// LeadSessions lists a project's sessions, oldest first.
func (s *Store) LeadSessions(ctx context.Context, project string) ([]LeadSession, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT project, id, topic, created_at FROM lead_sessions WHERE project = ? ORDER BY created_at, id`, project)
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

// LeadSession is one session of a project's chat.
func (s *Store) LeadSession(ctx context.Context, project, id string) (LeadSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT project, id, topic, created_at FROM lead_sessions WHERE project = ? AND id = ?`, project, id)
	ls, err := scanLeadSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LeadSession{}, fmt.Errorf("session %s of %s: %w", id, project, ErrNotFound)
	}
	return ls, err
}

func scanLeadSession(row interface{ Scan(...any) error }) (LeadSession, error) {
	var ls LeadSession
	var created int64
	if err := row.Scan(&ls.Project, &ls.ID, &ls.Topic, &created); err != nil {
		return LeadSession{}, err
	}
	ls.CreatedAt = time.UnixMilli(created)
	return ls, nil
}

// LeadSessionActivity is when each of a project's sessions was last active,
// by id: its conversation's newest message, or when it was opened when it
// has none.
func (s *Store) LeadSessionActivity(ctx context.Context, project string) (map[string]time.Time, error) {
	sessions, err := s.LeadSessions(ctx, project)
	if err != nil {
		return nil, err
	}
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

// LastActiveLeadSession is the project's session most recently active, the
// one a notice nobody owns goes to; ErrNotFound when it has none.
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

// RenameLeadSession changes a session's topic. Its id stays.
func (s *Store) RenameLeadSession(ctx context.Context, project, id, topic string) error {
	topic, err := cleanTopic(topic)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE lead_sessions SET topic = ? WHERE project = ? AND id = ?`, topic, project, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("session %s of %s: %w", id, project, ErrNotFound)
	}
	return nil
}

// DeleteLeadSession deletes a session: its row, its lead's, and its
// conversation, in one transaction. The agents it made are left to nobody,
// and it returns them.
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
// conversations: the project's chat was reset.
func (s *Store) RemoveLeadSessions(ctx context.Context, project string) error {
	sessions, err := s.LeadSessions(ctx, project)
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
