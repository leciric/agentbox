package daemon

import (
	"errors"
	"net/http"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A project's chat is driven by its lead, which is an agent row with no
// machine. The chat handlers are shared with the agents' own chats: only how
// the agent is found differs, so the app, the CLI and the API see one chat.

// agentFrom finds the agent a request is about.
type agentFrom func(*http.Request) (state.Agent, error)

// leadFromPath returns a project's lead without creating it. A project you have
// never written to has no lead yet, and gets one that exists only for this
// request, so reading its empty conversation makes nothing on disk.
func (s *Server) leadFromPath(r *http.Request) (state.Agent, error) {
	project := r.PathValue("project")
	a, err := s.manager(nil).Lead(r.Context(), project)
	if err == nil {
		return a, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.Agent{}, err
	}
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return state.Agent{}, err
	}
	return state.Agent{
		Project: project, Name: state.LeadName, AI: agent.ChatTool,
		Interface: state.InterfaceChat, Role: state.RoleLead,
	}, nil
}

// ensureLeadFromPath creates the project's lead if this is the first message.
// Its worktree is then moved to the tip of the branch it stands on, so the turn
// reads what is on that branch now.
func (s *Server) ensureLeadFromPath(r *http.Request) (state.Agent, error) {
	m := s.manager(s.cfg.Log)
	a, err := m.EnsureLead(r.Context(), r.PathValue("project"))
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
	info := api.ProjectChat{
		Project:  a.Project,
		Ref:      agent.LeadRef(a.Project),
		Started:  a.Status != "",
		Worktree: a.Worktree,
		BaseRef:  a.BaseRef,
		Chat:     session,
	}
	return writeJSON(w, http.StatusOK, info)
}

// resetProjectChat throws the project's chat away: its conversation, its
// worktree and its private HOME. The project's agents are untouched.
func (s *Server) resetProjectChat(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	s.chat.Stop(agent.LeadRef(project), "the project chat was reset")
	s.chat.Forget(agent.LeadRef(project))
	s.settleLeadCache(project, true)
	if err := s.manager(s.cfg.Log).DestroyLead(r.Context(), project); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
