package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A session opened with no topic (Ctrl+T in the app, `agentbox chat session
// new` with none) is untitled until its lead's first turn has ended, and is
// then named from what was said in it: the user's opening words and the
// lead's answer, read by the tool's cheap model in a session of its own
// (askAside), as the turn checks and distillation are. The lead isn't asked
// itself: naming would be a turn in its conversation, in its context and its
// prompt cache, for a few words the user may never look at.
//
// A name the user gives first, or while this one is being worked out, stays
// (state.NameLeadSession): only an untitled session is named.

// sessionNameAsk is what the cheap model is asked; only the conversation's
// opening goes, cut short.
const sessionNameAsk = `Below is the start of a conversation between a developer and the AI that runs their software project. Name it for a tab: a topic of two to five words, like "Prod: login 500s" or "Dark mode in settings". Answer with the name alone, no quotes and nothing else.

<conversation>
%s
</conversation>`

// sessionNameTimeout bounds the cheap model's look. Without an answer the
// session is named from the user's first line.
const sessionNameTimeout = 60 * time.Second

// sessionOpeningMax is how much of the conversation the cheap model reads.
const sessionOpeningMax = 3000

// nameSession names a's session, a lead's whose turn has just ended, when it
// is untitled and has been written to. One naming at a time per session.
func (s *Server) nameSession(ctx context.Context, a state.Agent) {
	if !a.IsLead() {
		return
	}
	id := a.Session()
	ls, err := s.store.LeadSession(ctx, a.Project, id)
	if err != nil || !ls.Untitled {
		return
	}
	ref := agent.SessionRef(a.Project, id)
	s.mu.Lock()
	if s.naming[ref] {
		s.mu.Unlock()
		return
	}
	if s.naming == nil {
		s.naming = map[string]bool{}
	}
	s.naming[ref] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.naming, ref)
		s.mu.Unlock()
	}()

	opening, first := s.sessionOpening(ctx, a)
	if first == "" {
		// Only notices so far: nothing the user said to name it after.
		return
	}
	topic := s.askSessionTopic(ctx, a, opening)
	if topic == "" {
		topic = eventTopic(first)
	}
	named, err := s.store.NameLeadSession(ctx, a.Project, id, topic)
	if err != nil || !named {
		if err != nil {
			s.logf("naming %s's %s session: %v", a.Project, id, err)
		}
		return
	}
	if lead, err := s.manager(nil).LeadSession(ctx, a.Project, id); err == nil {
		if err := s.store.SetAgentTitle(ctx, lead.Project, lead.Name, topic); err != nil {
			s.logf("naming %s's %s session: %v", a.Project, id, err)
		}
	}
	s.logf("Named %s's %s session %q", a.Project, id, topic)
	s.sessionsChanged(ctx, a.Project)
}

// sessionOpening is what a session's conversation said so far, as the cheap
// model reads it, and the first thing the user wrote in it: "" when they have
// written nothing. Notices and what the app hides are left out. It is read
// from the store, which the turn's end has written: a session nobody has
// opened isn't loaded for it.
func (s *Server) sessionOpening(ctx context.Context, a state.Agent) (opening, first string) {
	rows, err := s.store.ChatItems(ctx, a.Project, a.Name)
	if err != nil {
		return "", ""
	}
	var b strings.Builder
	for _, row := range rows {
		var it api.ChatItem
		if json.Unmarshal(row.Data, &it) != nil || it.Hidden || strings.TrimSpace(it.Text) == "" {
			continue
		}
		var who string
		switch {
		case it.Kind == "user" && !it.Woken:
			who = "Developer"
			if first == "" {
				first = it.Text
			}
		case it.Kind == "assistant":
			who = "AI"
		default:
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n\n", who, strings.TrimSpace(it.Text))
		if b.Len() >= sessionOpeningMax {
			break
		}
	}
	return headWords(b.String(), sessionOpeningMax), first
}

// askSessionTopic is the topic the cheap model reads in a session's opening,
// "" without a cheap model for the lead's tool or an answer.
func (s *Server) askSessionTopic(ctx context.Context, a state.Agent, opening string) string {
	model := state.CheapModelFor(a.AI)
	if model == "" || s.askAside == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, sessionNameTimeout)
	defer cancel()
	answer, _, err := s.askAside(ctx, a, model, fmt.Sprintf(sessionNameAsk, opening))
	if err != nil {
		s.logf("naming %s's %s session: %v", a.Project, a.Session(), err)
		return ""
	}
	return cleanSessionTopic(answer)
}

// cleanSessionTopic is a model's answer as a topic: its first line, without
// the quotes, emphasis or "Title:" a model dresses it in, cut to fit a tab.
func cleanSessionTopic(answer string) string {
	line := strings.TrimSpace(firstLineOf(answer))
	for _, prefix := range []string{"Title:", "Topic:", "Name:"} {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			line = rest
		}
	}
	line = strings.Trim(line, " \t*_`\"'“”‘’#.")
	if line == "" {
		return ""
	}
	return eventTopic(line)
}
