package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// The sessions of a project's chat (state/leadsessions.go). Each has its own
// lead, socket and HOME; the daemon's part is which session hears what.
//
//   - A notice about an agent goes to the session that made it (tellOwnerOf):
//     its finish, stall and failure, its questions, its pull request.
//   - One about no agent, or about an agent nobody owns — a schedule, another
//     project's lead, the user's Home chat, CI on main, an agent the user made
//     or whose session was deleted — goes to the session most recently active
//     (tellSessionOf with no session). A project with no session at all gets
//     one, titled after the notice, when the notice starts a turn; one that
//     only waits to be read has nobody to read it, and is dropped, as it was
//     before a project's chat had been used.
//   - A lead may read every agent, and acts on its own and on nobody's freely.
//     Telling or retiring another session's agent waits for the user's yes on
//     a card in its chat (crossSession); handing an agent over is the user's,
//     from the app or `agentbox chat session hand`.

// leadSessionKey carries the session a lead socket's request came in on, for
// what is given a context rather than the request (approveInChat).
type leadSessionKey struct{}

func withLeadSession(ctx context.Context, session string) context.Context {
	return context.WithValue(ctx, leadSessionKey{}, session)
}

func leadSessionOf(ctx context.Context) string {
	session, _ := ctx.Value(leadSessionKey{}).(string)
	return session
}

// sessionFrom is the chat session a request is about: the lead socket's own,
// or ?session= on the app's routes. "" names none.
func sessionFrom(r *http.Request) string {
	if session := r.PathValue("session"); session != "" {
		return session
	}
	return r.URL.Query().Get("session")
}

// maxEventTopic is how much of a notice titles the session it opens.
const maxEventTopic = 60

// eventTopic is a session's topic made from what opened it: its first line,
// cut at a word.
func eventTopic(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.Join(strings.Fields(line), " ")
	if len(line) > maxEventTopic {
		cut := maxEventTopic
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		if i := strings.LastIndexByte(line[:cut], ' '); i > maxEventTopic/2 {
			cut = i
		}
		line = strings.TrimRight(line[:cut], " ,.;:") + "…"
	}
	if line == "" {
		return "Chat"
	}
	return line
}

// sessionLead is the lead of one of a project's sessions, made when it has
// never been written to, its socket served.
func (s *Server) sessionLead(ctx context.Context, project, session string) (state.Agent, error) {
	a, err := s.manager(s.cfg.Log).EnsureLeadSession(ctx, project, session)
	if err != nil {
		return state.Agent{}, err
	}
	if err := s.serveSessionAPI(a.Project, a.Session()); err != nil {
		return state.Agent{}, err
	}
	return a, nil
}

// eventLead is the lead that hears what belongs to no session: the session
// most recently active, or, with open a project with no session at all, a new
// one titled topic. Without open, a project with none is state.ErrNotFound.
func (s *Server) eventLead(ctx context.Context, project, topic string, open bool) (state.Agent, error) {
	ls, err := s.store.LastActiveLeadSession(ctx, project)
	if errors.Is(err, state.ErrNotFound) {
		// A lead from before sessions that the migration didn't see is the
		// session it would have made.
		if _, leadErr := s.manager(nil).Lead(ctx, project); leadErr == nil {
			if err := s.store.AddLeadSessionID(ctx, project, state.MigratedSession, "Chat", time.Now()); err != nil && !errors.Is(err, state.ErrExists) {
				return state.Agent{}, err
			}
			ls, err = s.store.LeadSession(ctx, project, state.MigratedSession)
		}
	}
	if errors.Is(err, state.ErrNotFound) && open {
		if ls, err = s.store.AddLeadSession(ctx, project, topic, time.Now()); err == nil {
			s.logf("%s's chat had no session: opened %q for %s", project, ls.Topic, firstWords(topic))
			s.sessionsChanged(ctx, project)
		}
	}
	if err != nil {
		return state.Agent{}, err
	}
	return s.sessionLead(ctx, project, ls.ID)
}

func firstWords(s string) string { return headWords(s, 12) }

// tellOwnerOf puts a notice about an agent in front of the session that made
// it, or of the one most recently active when nobody owns it.
func (s *Server) tellOwnerOf(ctx context.Context, a state.Agent, notice string, opts chat.NoticeOptions) {
	s.tellSessionOf(ctx, a.Project, a.Session(), notice, opts)
}

// tellOwner is tellOwnerOf with only act.
func (s *Server) tellOwner(ctx context.Context, a state.Agent, notice string, act bool) {
	s.tellOwnerOf(ctx, a, notice, chat.NoticeOptions{Act: act})
}

// tellSession is tellSessionOf with only act.
func (s *Server) tellSession(ctx context.Context, project, session, notice string, act bool) {
	s.tellSessionOf(ctx, project, session, notice, chat.NoticeOptions{Act: act})
}

// leadOf is the lead a notice for session goes to: that session's, while it
// exists, and otherwise eventLead's.
func (s *Server) leadOf(ctx context.Context, project, session, topic string, open bool) (state.Agent, error) {
	if session != "" {
		if _, err := s.store.LeadSession(ctx, project, session); err == nil {
			return s.sessionLead(ctx, project, session)
		} else if !errors.Is(err, state.ErrNotFound) {
			return state.Agent{}, err
		}
	}
	return s.eventLead(ctx, project, topic, open)
}

// groupSession is the session a group's notices go to: the one that made
// its agents.
func (s *Server) groupSession(ctx context.Context, g state.Group) string {
	for _, m := range g.Members {
		if m.Agent == "" {
			continue
		}
		if a, err := s.store.Agent(ctx, g.Project, m.Agent); err == nil {
			return a.Session()
		}
	}
	return ""
}

// sessionsChanged tells the app a project's sessions changed, and rewrites
// every session's brief, which names the others, in the background: that is
// a memory recap per session (leadRecap), which a project with dozens of
// sessions and a real memory took most of a second over, and a new session
// waited on it before it could open. Each running session reads its brief
// again at its next start, so nothing waits on it.
func (s *Server) sessionsChanged(ctx context.Context, project string) {
	s.events.publish(api.EventLeadSessions, api.LeadSessionsChange{Project: project})
	s.rewriteBriefs(project)
}

// rewriteBriefs rewrites a project's chat briefs aside, one rewrite at a time
// for each project: a change while one runs makes one more after it, however
// many arrive, since each rewrite reads the sessions as they are then.
func (s *Server) rewriteBriefs(project string) {
	s.mu.Lock()
	if _, running := s.briefsAgain[project]; running {
		s.briefsAgain[project] = true
		s.mu.Unlock()
		return
	}
	if s.briefsAgain == nil {
		s.briefsAgain = map[string]bool{}
	}
	s.briefsAgain[project] = false
	s.mu.Unlock()
	s.briefRewrites.Go(func() {
		for {
			if err := s.manager(nil).ReconfigureLead(context.Background(), project); err != nil {
				s.logf("rewriting %s's chat briefs: %v", project, err)
			}
			s.mu.Lock()
			again := s.briefsAgain[project]
			if !again {
				delete(s.briefsAgain, project)
			} else {
				s.briefsAgain[project] = false
			}
			s.mu.Unlock()
			if !again {
				return
			}
		}
	})
}

// apiSessions describes a project's sessions for the app.
func (s *Server) apiSessions(ctx context.Context, project string) ([]api.LeadSession, error) {
	sessions, err := s.store.LeadSessions(ctx, project)
	if err != nil {
		return nil, err
	}
	activity, err := s.store.LeadSessionActivity(ctx, project)
	if err != nil {
		return nil, err
	}
	agents, err := s.store.Agents(ctx, project)
	if err != nil {
		return nil, err
	}
	owned := map[string]int{}
	for _, a := range agents {
		if !a.IsLead() && a.Session() != "" {
			owned[a.Session()]++
		}
	}
	out := make([]api.LeadSession, 0, len(sessions))
	for _, ls := range sessions {
		ref := agent.SessionRef(project, ls.ID)
		lead := state.Agent{Project: project, Name: state.LeadSessionName(ls.ID)}
		chatState := s.chat.State(ref)
		if chatState == "" {
			chatState = api.ChatOff
		}
		out = append(out, api.LeadSession{
			Project: project, ID: ls.ID, Ref: ref, Topic: ls.Topic, Chat: chatState,
			Agents: owned[ls.ID], Last: s.lastChatLine(ctx, lead),
			CreatedAt: ls.CreatedAt, ActiveAt: activity[ls.ID],
		})
	}
	return out, nil
}

// listSessions answers GET /v1/projects/{project}/sessions.
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	out, err := s.apiSessions(r.Context(), project)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// newSession answers POST /v1/projects/{project}/sessions: a session on a
// topic, which makes nothing until it is first written to.
func (s *Server) newSession(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	var req api.NewLeadSessionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ls, err := s.store.AddLeadSession(r.Context(), project, req.Topic, time.Now())
	if err != nil {
		return err
	}
	s.sessionsChanged(r.Context(), project)
	return writeJSON(w, http.StatusCreated, s.apiSession(r.Context(), project, ls.ID))
}

// apiSession is one session as listSessions shows it.
func (s *Server) apiSession(ctx context.Context, project, id string) api.LeadSession {
	all, _ := s.apiSessions(ctx, project)
	for _, ls := range all {
		if ls.ID == id {
			return ls
		}
	}
	return api.LeadSession{Project: project, ID: id, Ref: agent.SessionRef(project, id)}
}

// renameSession answers PATCH /v1/projects/{project}/sessions/{session}.
func (s *Server) renameSession(w http.ResponseWriter, r *http.Request) error {
	project, id := r.PathValue("project"), r.PathValue("session")
	var req api.LeadSessionPatch
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := s.store.RenameLeadSession(r.Context(), project, id, req.Topic); err != nil {
		return err
	}
	if a, err := s.manager(nil).LeadSession(r.Context(), project, id); err == nil {
		if err := s.store.SetAgentTitle(r.Context(), a.Project, a.Name, strings.Join(strings.Fields(req.Topic), " ")); err != nil {
			return err
		}
	}
	s.sessionsChanged(r.Context(), project)
	return writeJSON(w, http.StatusOK, s.apiSession(r.Context(), project, id))
}

// deleteSession answers DELETE /v1/projects/{project}/sessions/{session}:
// its conversation stops and goes, with its HOME and socket, and its agents
// are left to nobody. Nothing of it is summarised: what mattered, its lead
// wrote to the project's memory as it went.
func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) error {
	project, id := r.PathValue("project"), r.PathValue("session")
	if _, err := s.store.LeadSession(r.Context(), project, id); err != nil {
		return err
	}
	ref := agent.SessionRef(project, id)
	s.chat.Stop(ref, "the session was deleted")
	s.chat.Forget(ref)
	s.settleLeadCache(ref, true)
	s.stopSessionAPI(project, id)
	freed, err := s.store.DeleteLeadSession(r.Context(), project, id)
	if err != nil {
		return err
	}
	s.manager(s.cfg.Log).DeleteLeadSession(r.Context(), project, id)
	if err := s.memory().ForgetSession(r.Context(), project, id); err != nil {
		s.logf("forgetting what %s's %s session was working on: %v", project, id, err)
	}
	s.logf("Deleted %s's %s session; its agents %v belong to no session now", project, id, freed)
	s.sessionsChanged(r.Context(), project)
	if len(freed) > 0 {
		s.refreshAgents(r.Context())
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handOver answers PUT /v1/agents/{project}/{agent}/session: the user moving
// an agent to another session, or to none, whose notices it goes to from now.
func (s *Server) handOver(w http.ResponseWriter, r *http.Request) error {
	project, name := r.PathValue("project"), r.PathValue("agent")
	a, err := s.store.Agent(r.Context(), project, name)
	if err != nil {
		return err
	}
	if a.IsLead() {
		return errNotAnAgent
	}
	var req api.AgentSessionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Session != "" {
		if _, err := s.store.LeadSession(r.Context(), project, req.Session); err != nil {
			return err
		}
	}
	if err := s.store.SetAgentSession(r.Context(), project, name, req.Session); err != nil {
		return err
	}
	s.sessionsChanged(r.Context(), project)
	s.refreshAgents(r.Context())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// crossSession waits for the user's yes before a lead acts on another
// session's agent: a card in the asking session's chat. Its own agents, and
// those nobody owns, it acts on freely.
func (s *Server) crossSession(ctx context.Context, from string, a state.Agent, what string) error {
	topic, other := s.otherSession(ctx, from, a)
	if !other {
		return nil
	}
	ok, err := s.approve(ctx, a.Project, api.ChatPermission{
		Approval: "session",
		Title:    fmt.Sprintf("%s %s, which the %q session made", what, a.Name, topic),
		Detail: fmt.Sprintf("%s belongs to another session of this chat, which follows its work. Approve to let this "+
			"session act on it once; to move it here for good, hand it over from its menu in the rail.", a.Name),
	})
	if err != nil {
		return fmt.Errorf("asking the user to approve it: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: %s is the %q session's agent, and was left alone. Leave it to that session", errRefused, a.Name, topic)
	}
	return nil
}

// sessionOfName is the session a lead's name runs.
func sessionOfName(name string) string {
	session, _ := state.SessionOfLead(name)
	return session
}
