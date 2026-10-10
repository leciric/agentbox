package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Feedback: what the readers of a memory say about it.
//
// A memory is written once and read for weeks, by agents and the lead who are
// often the first to find out it no longer holds. Feedback is how they say so
// on the spot, without having to write a correction: a memory marked wrong or
// stale drops to MinImportance at once and loses its confirmations, so it
// falls to the bottom of every ranking and out of every context build; one
// marked helpful gains a point. It follows ai-memory's memory_feedback
// (salience_after_feedback in its decay.rs): wrong and stale go straight to
// the floor rather than stepping, because they say the memory can't be
// trusted, and helpful steps, and stops short of the top, so one reader's
// judgement can tilt a memory but not pin it there. MaxImportance stays the
// user's and the lead's to give, through remember.
//
// Every feedback is also an event (EventMemoryFeedback), naming who gave it
// and why, so a memory that dropped can be traced to whoever dropped it.

// Feedback verdicts.
const (
	// FeedbackWrong is a memory that says something untrue.
	FeedbackWrong = "wrong"
	// FeedbackStale is a memory that was true and no longer is. An open
	// item marked stale is resolved too: a problem that is over is closed,
	// which is what resolve_memory would have done.
	FeedbackStale = "stale"
	// FeedbackHelpful is a memory that saved its reader the work.
	FeedbackHelpful = "helpful"
)

// FeedbackVerdicts are the verdicts Feedback takes.
var FeedbackVerdicts = []string{FeedbackWrong, FeedbackStale, FeedbackHelpful}

// HelpfulCeiling is as high as helpful feedback raises a memory's importance.
// It is HighImportance, so a memory readers keep finding helpful reaches every
// context build, and never MaxImportance, which says "nobody should work on
// this project without it" and is somebody's decision rather than a tally.
const HelpfulCeiling = HighImportance

// EventMemoryFeedback is the event type Feedback appends: its payload names
// the memory, the verdict, who gave it and why.
const EventMemoryFeedback = "memory_feedback"

// FeedbackRequest is one reader's verdict on one memory.
type FeedbackRequest struct {
	Project string
	// Memory is the memory's id, or its title exactly as a brief or a search
	// showed it, since a brief lists memories by title alone.
	Memory  string
	Verdict string
	// Why is what the reader found, in a line.
	Why string
	// Agent is the agent that gave it, empty when it wasn't one.
	Agent string
	// By is who gave it, for the record: an agent's name, "lead" or "user".
	By string
}

// FeedbackResult is what the feedback did.
type FeedbackResult struct {
	Memory Memory `json:"memory"` // as it is now
	// Was is its importance before.
	Was int `json:"was"`
	// Resolved is true when the feedback closed it (stale, on an open item).
	Resolved bool `json:"resolved,omitempty"`
}

// importanceAfterFeedback is what one verdict does to an importance, as pure
// arithmetic so its bounds are testable without a database.
func importanceAfterFeedback(current int, verdict string) int {
	switch verdict {
	case FeedbackWrong, FeedbackStale:
		return MinImportance
	case FeedbackHelpful:
		if current >= HelpfulCeiling {
			return current
		}
		return current + 1
	}
	return current
}

// Feedback applies one reader's verdict to a live memory and records it.
// Wrong and stale drop it to MinImportance and clear its confirmations, and
// stale resolves it when it is an open item; helpful raises it a point, up
// to HelpfulCeiling, and counts as the memory having been read, which keeps
// decay off it.
func (s *Store) Feedback(ctx context.Context, req FeedbackRequest) (FeedbackResult, error) {
	if err := requireProject(req.Project); err != nil {
		return FeedbackResult{}, err
	}
	if err := oneOf("feedback", req.Verdict, FeedbackVerdicts); err != nil {
		return FeedbackResult{}, err
	}
	why, err := text("the reason for feedback", req.Why, MaxTitleLen)
	if err != nil {
		return FeedbackResult{}, err
	}
	if why == "" && req.Verdict != FeedbackHelpful {
		return FeedbackResult{}, fmt.Errorf("say why the memory is %s, in a line: the next reader wants to know what you found", req.Verdict)
	}
	m, err := s.findMemory(ctx, req.Project, req.Memory)
	if err != nil {
		return FeedbackResult{}, err
	}
	if !m.Live() {
		return FeedbackResult{}, fmt.Errorf("memory %s was already replaced or closed: it no longer comes back from a search", m.ID)
	}
	out := FeedbackResult{Was: m.Importance}
	now := time.Now().UnixMilli()
	importance := importanceAfterFeedback(m.Importance, req.Verdict)
	if req.Verdict == FeedbackHelpful {
		_, err = s.db.ExecContext(ctx,
			`UPDATE memories SET importance = ?, referenced_at = ?, updated_at = ? WHERE project = ? AND id = ?`,
			importance, now, now, req.Project, m.ID)
	} else {
		_, err = s.db.ExecContext(ctx,
			`UPDATE memories SET importance = ?, confirmations = 0, updated_at = ? WHERE project = ? AND id = ?`,
			importance, now, req.Project, m.ID)
	}
	if err != nil {
		return FeedbackResult{}, err
	}
	by := strings.TrimSpace(req.By)
	if by == "" {
		by = req.Agent
	}
	if req.Verdict == FeedbackStale && OpenItem(m) {
		reason := "stale"
		if by != "" {
			reason += ", says " + by
		}
		if _, err := s.ResolveMemory(ctx, req.Project, m.ID, truncateTo(reason+": "+why, MaxTitleLen)); err != nil {
			return FeedbackResult{}, err
		}
		out.Resolved = true
	}
	payload, err := json.Marshal(map[string]any{
		"memory": m.ID, "title": m.Title, "verdict": req.Verdict, "why": why, "by": by,
		"importance": importance, "was": m.Importance,
	})
	if err != nil {
		return FeedbackResult{}, err
	}
	if _, err := s.AppendEvent(ctx, Event{Project: req.Project, Agent: req.Agent, Type: EventMemoryFeedback, Payload: payload}); err != nil {
		return FeedbackResult{}, err
	}
	if out.Memory, err = s.Memory(ctx, req.Project, m.ID); err != nil {
		return FeedbackResult{}, err
	}
	return out, nil
}

// findMemory is a memory by id, or else the one live memory with that title.
// Titles are how a brief shows memories, so a reader can name one it was
// handed without searching for its id first; one title on several memories
// is refused with their ids rather than guessed between.
func (s *Store) findMemory(ctx context.Context, project, ref string) (Memory, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Memory{}, errors.New("say which memory: its id, or its title as you were shown it")
	}
	m, err := s.Memory(ctx, project, ref)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return m, err
	}
	// A brief renders a title in bold; a reader copying it may keep that.
	title := strings.TrimSpace(strings.Trim(ref, "*"))
	found, err := s.queryMemories(ctx, `WHERE m.project = ? AND `+live+` AND lower(m.title) = lower(?)
		ORDER BY m.created_at DESC LIMIT 6`, project, collapse(title))
	if err != nil {
		return Memory{}, err
	}
	switch len(found) {
	case 0:
		return Memory{}, notFound("memory", ref)
	case 1:
		return found[0], nil
	}
	ids := make([]string, len(found))
	for i, f := range found {
		ids[i] = f.ID
	}
	return Memory{}, fmt.Errorf("%d memories are titled %q (%s): say which by its id", len(found), title, strings.Join(ids, ", "))
}
