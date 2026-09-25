package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/chat"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// Compacting a project's chat (D73).
//
// A project's chat is one conversation that never ends, and a model's context
// is finite. Before a turn of the lead's starts, this checks how full that
// context is; past the project's threshold, the conversation is consolidated
// into the project's memory and carried on in a fresh session
// (chat.Manager.Compact). The user sees one chat with a notice in it.
//
// It is provider-agnostic on purpose. Claude Code compacts conversations
// itself, and so may any other tool behind an ACP adapter; AgentBox neither
// fights that nor relies on it. What the tool does inside its own session is
// the tool's business — what AgentBox keeps is a memory of the project that
// outlives any session, and a recap it can start the next one with.

// compactTimeout bounds the hidden consolidation prompt. It is generous: the
// model is summarising a whole conversation, and the turn the user is waiting
// for hasn't started. Past it the session is rolled over anyway, with whatever
// the recap can be built from without it.
const compactTimeout = 3 * time.Minute

// rolloverIfNeeded compacts a project's chat when its context has filled past
// the project's threshold. It is called where a lead turn is about to start —
// the user writing, or an agent's notice waking the chat — because that is the
// only moment at which replacing the session costs nothing: no turn is
// running, and the turn that follows starts in the fresh session and reads the
// recap.
//
// It blocks, and it is meant to. Rolling over after the turn had started would
// put the user's message in the session that is being thrown away.
//
// Anything that goes wrong is logged and swallowed: a chat that couldn't be
// compacted still takes its turn, on the session it has.
func (s *Server) rolloverIfNeeded(ctx context.Context, a state.Agent) {
	if a.Role != state.RoleLead {
		return
	}
	p, err := s.store.Project(ctx, a.Project)
	if err != nil || p.RolloverThreshold == state.RolloverOff {
		return
	}
	// The size is already the room the session really has: the chat measures
	// a Claude lead against the compact window it started with, not the
	// model's (D83), and that is the lead's own context window now (D91).
	used, size, ready := s.chat.Context(agent.LeadRef(a.Project))
	if !ready || !needsRollover(used, size, p.RolloverThreshold) {
		return
	}
	s.logf("compacting the %s chat: %d of %d tokens used, over %d%%", a.Project, used, size, p.RolloverThreshold)
	if err := s.compactLead(ctx, a); err != nil && !errors.Is(err, chat.ErrBusy) {
		s.logf("compacting the %s chat: %v", a.Project, err)
	}
}

// needsRollover reports whether a context this full is worth compacting, for a
// project with this threshold.
//
// The unknown case is what this is careful about. A tool reports its usage
// when it feels like it, and reports nothing at all until the first turn of a
// session has run — including the fresh session a rollover has just started.
// Zero is therefore "it hasn't said", never "there is room": read as room to
// spare it would compact nothing, and read as a full context it would compact
// every new session immediately, forever. So it is skipped, and asked again
// next turn, by which time the tool has said.
func needsRollover(used, size int64, threshold int) bool {
	if threshold == state.RolloverOff || used <= 0 || size <= 0 {
		return false
	}
	return used*100 >= size*int64(threshold)
}

// compactLead consolidates the chat's session into the project's memory and
// rolls it over, whatever the threshold says. The route does this by hand, and
// rolloverIfNeeded does it when the context is full.
func (s *Server) compactLead(ctx context.Context, a state.Agent) error {
	// The compaction outlives the request that asked for it: a user who
	// navigates away mid-consolidation shouldn't leave the chat half rolled.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), compactTimeout)
	defer cancel()
	return s.chat.Compact(ctx, a, consolidationAsk, func(answer string, askErr error) error {
		return s.settleCompaction(ctx, a, answer, askErr)
	})
}

// settleCompaction writes what the session said into the project's memory and
// rewrites the chat's brief, so the session that starts next reads a recap of
// what this one knew.
//
// A consolidation that failed, or came back as something that isn't JSON,
// costs the project the memories of this stretch of conversation and nothing
// else: the brief is still rewritten, from working memory and whatever was
// already remembered, and the caller rolls over regardless. A chat that can't
// summarise itself must not be a chat that can't be compacted.
func (s *Server) settleCompaction(ctx context.Context, a state.Agent, answer string, askErr error) error {
	rewrite := func() error {
		if err := s.manager(nil).ReconfigureLead(ctx, a.Project); err != nil {
			return fmt.Errorf("rewriting the %s chat's brief: %w", a.Project, err)
		}
		return nil
	}
	if askErr != nil {
		if err := rewrite(); err != nil {
			return err
		}
		return fmt.Errorf("the %s chat didn't summarise itself: %w", a.Project, askErr)
	}
	summary, err := parseConsolidation(answer)
	if err != nil {
		if rewriteErr := rewrite(); rewriteErr != nil {
			return rewriteErr
		}
		return fmt.Errorf("reading the %s chat's summary: %w", a.Project, err)
	}
	if err := s.storeConsolidation(ctx, a, summary, answer); err != nil {
		if rewriteErr := rewrite(); rewriteErr != nil {
			return rewriteErr
		}
		return fmt.Errorf("storing the %s chat's summary: %w", a.Project, err)
	}
	return rewrite()
}

// storeConsolidation writes the consolidation as one event and the memories
// that came from it. The event is what ties them together: every memory
// written here names it, which is how the recap finds this narrative again
// (memory.Store.LatestFrom) without anything having to keep an id.
func (s *Server) storeConsolidation(ctx context.Context, a state.Agent, c consolidation, answer string) error {
	store := s.memory()
	// The narrative of the last compaction, which this one continues. Naming
	// it supersedes it: "the chat so far" is one memory that is rewritten,
	// not a pile of stale summaries for every later reader to date-sort. What
	// it said is still there, by id and through this one, and the raw answer
	// it came from is still its event.
	previous := ""
	if was, err := store.LatestFrom(ctx, a.Project, memory.KindEpisodic, memory.EventConversationCompacted); err == nil {
		previous = was.ID
	} else if !errors.Is(err, memory.ErrNotFound) {
		return err
	}
	event, err := store.AppendEvent(ctx, memory.Event{
		Project: a.Project,
		Agent:   a.Name,
		Type:    memory.EventConversationCompacted,
		Payload: consolidationPayload(answer),
	})
	if err != nil {
		return err
	}
	// The narrative first: it is the one thing the next session cannot do
	// without, so a failure part-way through this leaves the useful half.
	if text := strings.TrimSpace(c.Summary); text != "" {
		if _, err := store.AddMemory(ctx, memory.Memory{
			Project:       a.Project,
			Kind:          memory.KindEpisodic,
			Title:         "The chat up to " + time.Now().Format("2 January 2006"),
			Content:       text,
			Importance:    4,
			SupersedesID:  previous,
			SourceEventID: event.ID,
		}); err != nil {
			return err
		}
	}
	for _, group := range []struct {
		kind  string
		items []consolidationItem
	}{
		{memory.KindDecision, c.Decisions},
		{memory.KindDiscovery, c.Discoveries},
		{memory.KindProject, c.Facts},
		{memory.KindIssue, c.Issues},
	} {
		for _, item := range group.items {
			title, content := item.split()
			if title == "" {
				continue
			}
			if _, err := store.AddMemory(ctx, memory.Memory{
				Project:       a.Project,
				Kind:          group.kind,
				Title:         title,
				Content:       content,
				SourceEventID: event.ID,
			}); err != nil {
				s.logf("compacting the %s chat: keeping %q: %v", a.Project, title, err)
			}
		}
	}
	if !c.Working.Empty() {
		if _, err := s.memory().SetWorkingMemory(ctx, a.Project, c.Working); err != nil {
			s.logf("compacting the %s chat: what it is doing now: %v", a.Project, err)
		}
	}
	return nil
}

// consolidationPayload is the raw answer, kept with the event so a compaction
// can be read back as the model wrote it — including the parts that became no
// memory. An answer too long for the column is left out rather than truncated
// into something that looks whole.
func consolidationPayload(answer string) json.RawMessage {
	doc, err := json.Marshal(map[string]string{"answer": answer})
	if err != nil || len(doc) > memory.MaxPayloadLen {
		return nil
	}
	return doc
}

// consolidation is what the hidden prompt is asked for: the conversation, in
// the shape the project's memory keeps things in.
type consolidation struct {
	Summary     string                    `json:"summary"`
	Decisions   []consolidationItem       `json:"decisions"`
	Discoveries []consolidationItem       `json:"discoveries"`
	Facts       []consolidationItem       `json:"facts"`
	Issues      []consolidationItem       `json:"issues"`
	Working     memory.WorkingMemoryPatch `json:"working"`
}

// consolidationItem is one memory. A model asked for objects sometimes answers
// with strings, so both are read: what matters is that a title and a body come
// out of it, and a plain sentence is a title with no body.
type consolidationItem struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	text   string // an item that arrived as a bare string
}

func (i *consolidationItem) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '"' {
		return json.Unmarshal(raw, &i.text)
	}
	type plain consolidationItem
	var out plain
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	*i = consolidationItem(out)
	return nil
}

// split is the item as a memory's title and content. A long bare string
// becomes a first sentence and the rest, because a title is what a search
// ranks twice and a paragraph makes a poor one.
func (i consolidationItem) split() (title, content string) {
	title, content = strings.TrimSpace(i.Title), strings.TrimSpace(i.Detail)
	if title != "" {
		return truncate(title, memory.MaxTitleLen), truncate(content, memory.MaxContentLen)
	}
	text := strings.TrimSpace(i.text)
	if text == "" {
		return "", ""
	}
	if head, rest, ok := strings.Cut(text, ". "); ok && len(head) < memory.MaxTitleLen {
		return head, truncate(strings.TrimSpace(rest), memory.MaxContentLen)
	}
	if len(text) <= memory.MaxTitleLen {
		return text, ""
	}
	return truncate(text, memory.MaxTitleLen), truncate(text, memory.MaxContentLen)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max-1]) + "…"
}

// parseConsolidation reads the answer leniently. A model asked for JSON
// answers with JSON in a fenced block, or with a sentence before it, often
// enough that refusing those would mean throwing away a summary that is
// perfectly good: the object is taken from the first brace to the last.
func parseConsolidation(answer string) (consolidation, error) {
	body := strings.TrimSpace(answer)
	if start, end := strings.Index(body, "{"), strings.LastIndex(body, "}"); start >= 0 && end > start {
		body = body[start : end+1]
	}
	var out consolidation
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return consolidation{}, err
	}
	if strings.TrimSpace(out.Summary) == "" && len(out.Decisions)+len(out.Discoveries)+len(out.Facts)+len(out.Issues) == 0 && out.Working.Empty() {
		return consolidation{}, errors.New("it said nothing worth keeping")
	}
	return out, nil
}

// consolidationAsk is the hidden prompt. It runs on the session that is about
// to be replaced and never appears in the conversation, so it says plainly who
// is asking: a model that thought the user had asked this would answer them
// instead, and the answer would be lost with the session.
const consolidationAsk = `[AgentBox] Your session has nearly filled its context, so AgentBox is about to continue this conversation in a fresh one. The user sees no break and is not waiting on this message: it is AgentBox asking, not them.

Write down everything the next session needs to carry on as if it had been here. It will be given what you write, and nothing else of this conversation.

Answer with one JSON object and nothing else, in this shape:

{
  "summary": "What this conversation has been about, in a few paragraphs: what the user wants, what has been done, what was decided and why, and what was about to happen next. Write it for yourself, later.",
  "decisions": [{"title": "the choice, in a line", "detail": "why, and what it rules out"}],
  "discoveries": [{"title": "what was found out", "detail": "how it was found, and what it means"}],
  "facts": [{"title": "a long-lived fact about the project", "detail": "the detail"}],
  "issues": [{"title": "something still wrong or unfinished", "detail": "what is known about it"}],
  "working": {
    "goal": "what the project is trying to achieve at the moment",
    "currentTask": "what is being worked on right now",
    "activeAgents": ["agent-01"],
    "blockers": ["what is in the way"],
    "notes": "anything else that matters today"
  }
}

Every list may be empty, and "working" may be left out entirely — leaving a field out keeps what the project already has there, while an empty string or an empty list clears it. Only write down what this conversation established. Don't repeat the project's notes or its README back, and don't invent decisions nobody made.`

// rolloverChat compacts a project's chat on demand, whatever its threshold
// says: the same path the daemon takes by itself, for a user who wants to see
// it happen. A chat with no session yet has nothing to compact, and says so.
func (s *Server) rolloverChat(w http.ResponseWriter, r *http.Request) error {
	a, err := s.leadFromPath(r)
	if err != nil {
		return err
	}
	if err := s.compactLead(r.Context(), a); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
