package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A project's chat is driven by its lead, which is an agent row with no
// machine. The chat handlers are shared with the agents' own chats: only how
// the agent is found differs, so the app, the CLI and the API see one chat.

// agentFrom finds the agent a request is about.
type agentFrom func(*http.Request) (state.Agent, error)

// leadFromPath returns a project's lead without creating it: the lead of the
// session the request names, or of the one most recently active when it
// names none. A session never written to has no lead yet, and gets one that
// exists only for this request, so reading its empty conversation makes
// nothing on disk; so does a project with no session at all.
func (s *Server) leadFromPath(r *http.Request) (state.Agent, error) {
	project := r.PathValue("project")
	if project == state.HomeProject {
		return s.manager(nil).Home(), nil
	}
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return state.Agent{}, err
	}
	var ls state.LeadSession
	var err error
	if session := sessionFrom(r); session != "" {
		ls, err = s.store.LeadSession(r.Context(), project, session)
	} else {
		ls, err = s.store.LastActiveLeadSession(r.Context(), project)
		if errors.Is(err, state.ErrNotFound) {
			return state.Agent{
				Project: project, Name: state.LeadName, AI: agent.ChatTool,
				Interface: state.InterfaceChat, Role: state.RoleLead,
			}, nil
		}
	}
	if err != nil {
		return state.Agent{}, err
	}
	a, err := s.manager(nil).LeadSession(r.Context(), project, ls.ID)
	if err == nil {
		return a, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.Agent{}, err
	}
	return state.Agent{
		Project: project, Name: state.LeadSessionName(ls.ID), Title: ls.Topic, AI: agent.ChatTool,
		Interface: state.InterfaceChat, Role: state.RoleLead,
	}, nil
}

// ensureLeadFromPath creates the project's lead if this is the first message.
// Its worktree is then moved to the tip of the branch it stands on, so the turn
// reads what is on that branch now.
func (s *Server) ensureLeadFromPath(r *http.Request) (state.Agent, error) {
	m := s.manager(s.cfg.Log)
	if r.PathValue("project") == state.HomeProject {
		// The Home chat has no project to roll its session into, nor a
		// worktree to move: it is readied, and nothing more.
		return m.EnsureHome(r.Context())
	}
	// A message that names no session, from a client that knows none, goes
	// to the one most recently active, or opens one.
	a, err := s.leadOf(r.Context(), r.PathValue("project"), sessionFrom(r), "Chat", true)
	if err != nil {
		return state.Agent{}, err
	}
	// A turn is about to start, which is a moment the session may be replaced
	// (D73): past the project's threshold the conversation is consolidated
	// into its memory, so what starts next starts in a session with room in
	// it. This only starts that, before the message lands: the chat then holds
	// the message until the fresh session is in place, and the request
	// doesn't wait for it.
	s.rolloverIfNeeded(r.Context(), a)
	return m.SyncLead(r.Context(), a)
}

// leadForMessage is ensureLeadFromPath for a message to the chat. One that
// joins a running turn doesn't wait for the lead to be readied again: its
// brief and tools are the running session's already, and its worktree stays
// on the commit the turn is reading rather than moving under it.
func (s *Server) leadForMessage(r *http.Request) (state.Agent, error) {
	if project := r.PathValue("project"); project != state.HomeProject {
		if a, err := s.leadFromPath(r); err == nil && a.Status != "" && s.chat.Busy(a) {
			return a, nil
		}
	}
	return s.ensureLeadFromPath(r)
}

// projectChat describes a project's chat for the app: whether it has been used,
// and what its session is doing.
func (s *Server) projectChat(w http.ResponseWriter, r *http.Request) error {
	a, err := s.leadFromPath(r)
	if err != nil {
		return err
	}
	// A project always has a chat, even before its first message, so its state
	// is "off" rather than the empty string an agent without one reports.
	session := s.chat.State(a.Ref())
	if session == "" {
		session = api.ChatOff
	}
	// Asked about no session, it is the whole chat's: busy when any of its
	// sessions is, for what shows a project at work (the sidebar, keeping the
	// computer awake).
	if sessionFrom(r) == "" && a.Project != state.HomeProject {
		if sessions, err := s.store.LeadSessions(r.Context(), a.Project); err == nil {
			for _, ls := range sessions {
				if st := s.chat.State(agent.SessionRef(a.Project, ls.ID)); chatRank[st] > chatRank[session] {
					session = st
				}
			}
		}
	}
	info := api.ProjectChat{
		Project:  a.Project,
		Session:  sessionIfAny(a),
		Ref:      a.Ref(),
		Started:  a.Status != "",
		Worktree: a.Worktree,
		BaseRef:  a.BaseRef,
		Chat:     session,
		Last:     s.lastChatLine(r.Context(), a),
	}
	return writeJSON(w, http.StatusOK, info)
}

// lastChatLineMax is how much of the last message ProjectChat.Last keeps.
const lastChatLineMax = 280

// lastChatLine is a chat's last message someone wrote, cut short.
func (s *Server) lastChatLine(ctx context.Context, a state.Agent) *api.ChatLine {
	row, ok, err := s.store.LastChatLine(ctx, a.Project, a.Name)
	if err != nil || !ok {
		return nil
	}
	var item api.ChatItem
	if json.Unmarshal(row.Data, &item) != nil {
		return nil
	}
	text := strings.TrimSpace(item.Text)
	if len(text) > lastChatLineMax {
		cut := lastChatLineMax
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	at := item.UpdatedAt
	if at.IsZero() {
		at = item.CreatedAt
	}
	return &api.ChatLine{Kind: item.Kind, Text: text, At: at}
}

// resetProjectChat throws the project's chat away: its conversation, its
// worktree and its private HOME. The project's agents are untouched.
func (s *Server) resetProjectChat(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	// The whole chat, every session of it.
	refs := []string{agent.LeadRef(project)}
	if project != state.HomeProject {
		sessions, err := s.store.LeadSessions(r.Context(), project)
		if err != nil {
			return err
		}
		for _, ls := range sessions {
			refs = append(refs, agent.SessionRef(project, ls.ID))
			s.stopSessionAPI(project, ls.ID)
		}
		defer s.sessionsChanged(context.WithoutCancel(r.Context()), project)
	}
	for _, ref := range refs {
		s.chat.Stop(ref, "the project chat was reset")
		s.chat.Forget(ref)
		s.settleLeadCache(ref, true)
	}
	if project == state.HomeProject {
		if err := s.manager(s.cfg.Log).DestroyHome(r.Context()); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	if err := s.manager(s.cfg.Log).DestroyLead(r.Context(), project); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// sessionIfAny is a lead's session, "" for one standing in for a project
// that has none.
func sessionIfAny(a state.Agent) string {
	if a.Status == "" && a.Title == "" && a.Name == state.LeadName {
		return ""
	}
	return a.Session()
}

// chatRank orders a chat's states by how much they say it is doing, for a
// project's chat across its sessions.
var chatRank = map[string]int{
	api.ChatOff: 0, api.ChatError: 1, api.ChatReady: 2, api.ChatStarting: 3, api.ChatWaiting: 4, api.ChatRunning: 5,
}
