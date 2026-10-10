package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// ChatItem is one stored entry of an agent's conversation. Package chat decides
// what Data holds.
type ChatItem struct {
	ID       string
	Position int64
	Data     []byte // JSON
}

// Chat is what AgentBox keeps about an agent's conversation besides its items.
type Chat struct {
	SessionID string            // the AI tool's session, resumed after the adapter restarts
	Options   map[string]string // settings you chose, like the model, applied to every new session
	// Handoff is what the next fresh session is told of the conversation it
	// continues, after a rollback or a fork; "" once a turn has carried it.
	Handoff string
}

// ChatItems returns an agent's conversation, in order.
func (s *Store) ChatItems(ctx context.Context, project, agent string) ([]ChatItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, position, data FROM chat_items WHERE project = ? AND agent = ? ORDER BY position`, project, agent)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var items []ChatItem
	for rows.Next() {
		var it ChatItem
		var data string
		if err := rows.Scan(&it.ID, &it.Position, &data); err != nil {
			return nil, err
		}
		it.Data = []byte(data)
		items = append(items, it)
	}
	return items, rows.Err()
}

// LastChatItem returns an agent's most recent conversation item, which is how
// long ago it last did anything. ok is false for an agent that has never
// been written to.
func (s *Store) LastChatItem(ctx context.Context, project, agent string) (ChatItem, bool, error) {
	var it ChatItem
	var data string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, position, data FROM chat_items WHERE project = ? AND agent = ? ORDER BY position DESC LIMIT 1`,
		project, agent).Scan(&it.ID, &it.Position, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatItem{}, false, nil
	}
	if err != nil {
		return ChatItem{}, false, err
	}
	it.Data = []byte(data)
	return it, true, nil
}

// ProjectChatItem is an item of one of a project's chats, with whose.
type ProjectChatItem struct {
	Agent string
	ChatItem
}

// ToolCallsNamed returns the tool calls of all of a project's chats, its
// lead's and its agents', whose name or title ends with one of suffixes,
// in each chat's order (chats one after another). Matching ignores case.
func (s *Store) ToolCallsNamed(ctx context.Context, project string, suffixes ...string) ([]ProjectChatItem, error) {
	if len(suffixes) == 0 {
		return nil, nil
	}
	var match []string
	args := []any{project}
	for _, suffix := range suffixes {
		match = append(match, `lower(coalesce(json_extract(data, '$.tool.name'), '')) LIKE ? ESCAPE '\'`,
			`lower(coalesce(json_extract(data, '$.tool.title'), '')) LIKE ? ESCAPE '\'`)
		like := "%" + likeEscaper.Replace(strings.ToLower(suffix))
		args = append(args, like, like)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT agent, id, position, data FROM chat_items
		WHERE project = ? AND json_valid(data) AND json_extract(data, '$.kind') = 'tool' AND (`+strings.Join(match, " OR ")+`)
		ORDER BY agent, position`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var items []ProjectChatItem
	for rows.Next() {
		var it ProjectChatItem
		var data string
		if err := rows.Scan(&it.Agent, &it.ID, &it.Position, &data); err != nil {
			return nil, err
		}
		it.Data = []byte(data)
		items = append(items, it)
	}
	return items, rows.Err()
}

// SaveChatItems adds or replaces items, all of them or none.
func (s *Store) SaveChatItems(ctx context.Context, project, agent string, items []ChatItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO chat_items (project, agent, id, position, data) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (project, agent, id) DO UPDATE SET position = excluded.position, data = excluded.data`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, it := range items {
		if _, err := stmt.ExecContext(ctx, project, agent, it.ID, it.Position, string(it.Data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Chat returns what's kept about an agent's conversation; an agent that never
// chatted has no session and no options.
func (s *Store) Chat(ctx context.Context, project, agent string) (Chat, error) {
	c := Chat{Options: map[string]string{}}
	var options string
	err := s.db.QueryRowContext(ctx, `SELECT session_id, options, handoff FROM chats WHERE project = ? AND agent = ?`, project, agent).Scan(&c.SessionID, &options, &c.Handoff)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if json.Unmarshal([]byte(options), &c.Options) != nil || c.Options == nil {
		c.Options = map[string]string{}
	}
	return c, nil
}

func (s *Store) SaveChat(ctx context.Context, project, agent string, c Chat) error {
	if c.Options == nil {
		c.Options = map[string]string{}
	}
	options, err := json.Marshal(c.Options)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO chats (project, agent, session_id, options, handoff) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (project, agent) DO UPDATE SET session_id = excluded.session_id, options = excluded.options, handoff = excluded.handoff`,
		project, agent, c.SessionID, string(options), c.Handoff)
	return err
}

// ClearChat removes a conversation's items and forgets its session. The options stay.
func (s *Store) ClearChat(ctx context.Context, project, agent string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_items WHERE project = ? AND agent = ?`, project, agent); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chats SET session_id = '', handoff = '' WHERE project = ? AND agent = ?`, project, agent); err != nil {
		return err
	}
	return tx.Commit()
}

// TruncateChatItems removes every item from position keep on: what a rollback
// takes out of a conversation.
func (s *Store) TruncateChatItems(ctx context.Context, project, agent string, keep int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_items WHERE project = ? AND agent = ? AND position >= ?`, project, agent, keep)
	return err
}

// removeChat deletes everything kept about an agent's conversation, with the agent.
func removeChat(ctx context.Context, db *sql.DB, project, agent string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM chat_items WHERE project = ? AND agent = ?`, project, agent); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM running_turns WHERE project = ? AND agent = ?`, project, agent); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `DELETE FROM chats WHERE project = ? AND agent = ?`, project, agent)
	return err
}
