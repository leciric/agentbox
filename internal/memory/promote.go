package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Promotion: a memory the context builder keeps putting in agents' briefs is
// offered to the lead as a project note.
//
// A note and a memory are two prices for the same fact. A note is pasted into
// every agent's brief whether or not it is relevant, so every one costs every
// agent context, and a model follows a long brief worse than a short one; a
// memory costs nothing until a build or a search picks it out. When a build
// picks the same memory out for agent after agent, the project is paying for
// it in every brief anyway, through the slower route, and a sentence in the
// notes might say it better. Whether it should is a judgement: some memories
// are served everywhere because they are important for a while, not because
// they are a standing rule. So nothing is promoted on its own. The lead is
// offered a few at a time in its brief and accepts one (it becomes a note and
// stops being served, so the fact isn't paid for twice) or dismisses it for
// good (it stays an ordinary memory and is never offered again).
//
// What counts is how many different agents a memory reached, not how many
// briefs: an agent's brief is rewritten every time a session starts or the
// notes change, and one agent reading a fact ten times says nothing about
// whether the next agent needs it.

// Where a memory stands as a candidate note. "" is a memory nobody has offered.
const (
	// PromotionOffered is a memory the lead's brief has put to it, and the
	// lead hasn't answered. It stays in the brief until it does, or until
	// OfferLasts has passed.
	PromotionOffered = "offered"
	// PromotionPromoted is a memory that is a project note now. No context
	// serves it again; search_memory still finds it.
	PromotionPromoted = "promoted"
	// PromotionDismissed is a memory the lead said should stay a memory. It
	// is served as before, and never offered again.
	PromotionDismissed = "dismissed"
)

// PromoteAfterAgents is how many different agents a memory has to have been
// served to before it is offered. Below it a memory is doing its job: the
// agents whose tasks it touched found it in their brief.
const PromoteAfterAgents = 5

// OfferLasts is how long an offer the lead hasn't answered stays in its brief.
// Past it the offer lapses and isn't made again, the same as a dismissal: a
// lead that let one sit through two weeks of sessions has answered it.
const OfferLasts = 14 * 24 * time.Hour

// NoteSuggestionsShown is how many offers a brief carries at once. The
// section is in the lead's brief, which is resent with every call it makes.
const NoteSuggestionsShown = 3

// promotableKinds are the kinds that can say something standing. An issue is
// waiting to be fixed, and an episodic memory is something that happened:
// neither is a rule every agent should start with, however often it is served.
var promotableKinds = []string{KindProject, KindDecision, KindDiscovery}

// NoteSuggestion is one memory offered as a note.
type NoteSuggestion struct {
	Memory
	// Agents is how many different agents it has been served to.
	Agents int `json:"agents"`
}

// recordServes notes that one agent's brief carried these memories. A pair
// already recorded is left alone: the count is of agents, not of briefs.
func (s *Store) recordServes(ctx context.Context, project, agent string, ids []string) error {
	ids = nonEmpty(ids)
	if agent == "" || len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UnixMilli()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO memory_serves (project, memory_id, agent, served_at) VALUES (?, ?, ?, ?)`,
			project, id, agent, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NoteSuggestions are the memories worth offering the lead as notes, at most
// limit of them: live, of a kind that can be a standing rule, served to at
// least PromoteAfterAgents agents, and neither answered nor lapsed. Offers
// already made come first, so the list a lead was shown doesn't reshuffle
// under it, then the most widely served.
func (s *Store) NoteSuggestions(ctx context.Context, project string, limit int) ([]NoteSuggestion, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = NoteSuggestionsShown
	}
	args := []any{project}
	for _, k := range promotableKinds {
		args = append(args, k)
	}
	args = append(args, PromotionOffered, time.Now().Add(-OfferLasts).UnixMilli(), PromoteAfterAgents, PromotionOffered, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+memoryColumns+`, COUNT(*) FROM memories m
		JOIN memory_serves sv ON sv.memory_id = m.id AND sv.project = m.project
		WHERE m.project = ? AND `+live+` AND m.kind IN (`+placeholders(len(promotableKinds))+`)
		  AND (m.promotion = '' OR (m.promotion = ? AND m.promotion_at > ?))
		GROUP BY m.id HAVING COUNT(*) >= ?
		ORDER BY m.promotion = ? DESC, COUNT(*) DESC, m.importance DESC, m.created_at DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []NoteSuggestion
	for rows.Next() {
		var n NoteSuggestion
		m, err := scanMemory(withExtra{rows, []any{&n.Agents}})
		if err != nil {
			return nil, err
		}
		n.Memory = m
		out = append(out, n)
	}
	return out, rows.Err()
}

// OfferNoteSuggestions is NoteSuggestions for the lead's brief: what it
// returns is marked offered, which is when an offer's OfferLasts starts.
func (s *Store) OfferNoteSuggestions(ctx context.Context, project string) ([]NoteSuggestion, error) {
	found, err := s.NoteSuggestions(ctx, project, NoteSuggestionsShown)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for i, n := range found {
		if n.Promotion != "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE memories SET promotion = ?, promotion_at = ? WHERE project = ? AND id = ? AND promotion = ''`,
			PromotionOffered, now.UnixMilli(), project, n.ID); err != nil {
			return nil, err
		}
		found[i].Promotion, found[i].PromotionAt = PromotionOffered, stamp(now)
	}
	return found, nil
}

// Promotable says whether a memory can still become a note: it is live, of a
// kind that can be a standing rule, and nobody has decided about it yet. It is
// asked before the note is written, so a refusal writes nothing.
func Promotable(m Memory) error {
	switch {
	case m.Superseded || !m.ResolvedAt.IsZero():
		return fmt.Errorf("%s is no longer live: a later memory replaced it or it was closed", m.ID)
	case m.Promotion == PromotionPromoted:
		return fmt.Errorf("%s is a note already", m.ID)
	case m.Promotion == PromotionDismissed:
		return fmt.Errorf("%s was dismissed as a note: write the note with append_note if that has changed", m.ID)
	}
	for _, k := range promotableKinds {
		if m.Kind == k {
			return nil
		}
	}
	return fmt.Errorf("%s is %s %s memory, which isn't a standing rule: only %s memories become notes",
		m.ID, article(m.Kind), m.Kind, strings.Join(promotableKinds, ", "))
}

// PromoteMemory marks a memory as a project note, once the note is written:
// no context serves it again, because the notes already say it to every agent.
func (s *Store) PromoteMemory(ctx context.Context, project, id string) (Memory, error) {
	return s.decidePromotion(ctx, project, id, PromotionPromoted)
}

// DismissPromotion keeps a memory a memory, and stops it being offered again.
func (s *Store) DismissPromotion(ctx context.Context, project, id string) (Memory, error) {
	return s.decidePromotion(ctx, project, id, PromotionDismissed)
}

func (s *Store) decidePromotion(ctx context.Context, project, id, to string) (Memory, error) {
	m, err := s.Memory(ctx, project, id)
	if err != nil {
		return Memory{}, err
	}
	if err := Promotable(m); err != nil {
		return Memory{}, err
	}
	now := stamp(time.Now())
	if _, err := s.db.ExecContext(ctx, `UPDATE memories SET promotion = ?, promotion_at = ? WHERE project = ? AND id = ?`,
		to, now.UnixMilli(), project, id); err != nil {
		return Memory{}, err
	}
	m.Promotion, m.PromotionAt = to, now
	return m, nil
}

// NoteText is what a promoted memory says as a note when the lead doesn't
// word it itself: its title, and the start of its content when it has some, on
// one line. A note is in every brief, so a long memory is cut rather than
// pasted whole; the lead is asked to word one itself.
func NoteText(m Memory) string {
	text := strings.TrimSpace(m.Title)
	if content := excerpt(m.Content, maxNoteContent); content != "" {
		text = strings.TrimRight(text, ".:") + ": " + content
	}
	return text
}

// Excerpt is the first max bytes of a memory's text on one line, cut on a word
// boundary, the way a context renders it.
func Excerpt(s string, max int) string { return excerpt(s, max) }

// maxNoteContent is how much of a memory's content a note it becomes keeps.
const maxNoteContent = 240

func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

// withExtra scans a row of memoryColumns followed by more columns of a query's
// own, so scanMemory stays the one place a memory row is read.
type withExtra struct {
	rows  scanner
	extra []any
}

func (w withExtra) Scan(dest ...any) error { return w.rows.Scan(append(dest, w.extra...)...) }
