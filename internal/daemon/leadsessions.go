package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
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
//     or whose session was closed or deleted — goes to the open session most
//     recently active
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
// never been written to, its socket served. A Home session's is readied
// (agent.EnsureHomeSession), which makes no row.
func (s *Server) sessionLead(ctx context.Context, project, session string) (state.Agent, error) {
	m := s.manager(s.cfg.Log)
	ensure := m.EnsureLeadSession
	if project == state.HomeProject {
		ensure = func(ctx context.Context, _, session string) (state.Agent, error) {
			return m.EnsureHomeSession(ctx, session)
		}
	}
	a, err := ensure(ctx, project, session)
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
			if err == nil && ls.Closed() {
				err = fmt.Errorf("%s's chat has no open session: %w", project, state.ErrNotFound)
			}
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
// is open, and otherwise eventLead's. A closed session's agents are heard by
// the open session most recently active until it is reopened.
func (s *Server) leadOf(ctx context.Context, project, session, topic string, open bool) (state.Agent, error) {
	if session != "" {
		ls, err := s.store.LeadSession(ctx, project, session)
		switch {
		case err == nil && !ls.Closed():
			return s.sessionLead(ctx, project, session)
		case err != nil && !errors.Is(err, state.ErrNotFound):
			return state.Agent{}, err
		}
	}
	return s.eventLead(ctx, project, topic, open)
}

// errSessionClosed refuses a turn in a session of the chat's history: it is
// reopened, or continued from in a new one, first.
var errSessionClosed = errors.New("the session is closed: reopen it, or start a new session from it, to write in it")

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

// apiSessions describes a project's open sessions for the app, or with
// closed those in its history.
func (s *Server) apiSessions(ctx context.Context, project string, closed bool) ([]api.LeadSession, error) {
	list, activityOf := s.store.LeadSessions, s.store.LeadSessionActivity
	if closed {
		list, activityOf = s.store.ClosedLeadSessions, s.store.ClosedLeadSessionActivity
	}
	sessions, err := list(ctx, project)
	if err != nil {
		return nil, err
	}
	activity, err := activityOf(ctx, project)
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
		row := api.LeadSession{
			Project: project, ID: ls.ID, Ref: ref, Topic: ls.Topic, Untitled: ls.Untitled, Color: ls.Color, Chat: chatState,
			Agents: owned[ls.ID], Last: s.lastChatLine(ctx, lead),
			CreatedAt: ls.CreatedAt, ActiveAt: activity[ls.ID],
		}
		if ls.Closed() {
			row.ClosedAt = &ls.ClosedAt
		}
		out = append(out, row)
	}
	return out, nil
}

// chatProject checks that project names a chat with sessions: a project's,
// or the Home chat's, state.HomeProject, which has no projects row.
func (s *Server) chatProject(ctx context.Context, project string) error {
	if project == state.HomeProject {
		return nil
	}
	_, err := s.store.Project(ctx, project)
	return err
}

// listSessions answers GET /v1/projects/{project}/sessions: the open ones, or
// with ?closed=true the chat's history.
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if err := s.chatProject(r.Context(), project); err != nil {
		return err
	}
	out, err := s.apiSessions(r.Context(), project, r.URL.Query().Get("closed") == "true")
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// newSession answers POST /v1/projects/{project}/sessions: a session on a
// topic, which makes nothing until it is first written to.
func (s *Server) newSession(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if err := s.chatProject(r.Context(), project); err != nil {
		return err
	}
	var req api.NewLeadSessionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	var continued state.LeadSession
	if req.ContinueFrom != "" {
		var err error
		if continued, err = s.store.LeadSession(r.Context(), project, req.ContinueFrom); err != nil {
			return err
		}
		if req.From == "" {
			req.From = continued.ID
		}
		if strings.TrimSpace(req.Topic) == "" && !continued.Untitled {
			req.Topic = continued.Topic
		}
	}
	// Asked before the new one exists, which would be the most recent.
	from := req.From
	if from == "" {
		if last, err := s.store.LastActiveLeadSession(r.Context(), project); err == nil {
			from = last.ID
		}
	}
	var ls state.LeadSession
	var err error
	if strings.TrimSpace(req.Topic) == "" {
		ls, err = s.store.AddUntitledLeadSession(r.Context(), project, time.Now())
	} else {
		ls, err = s.store.AddLeadSession(r.Context(), project, req.Topic, time.Now())
	}
	if err != nil {
		return err
	}
	if from != "" {
		if err := s.inheritSessionSettings(r.Context(), project, from, ls.ID); err != nil {
			s.logf("starting %s's %s session on %s's settings: %v", project, ls.ID, from, err)
		}
	}
	if continued.ID != "" {
		if err := s.seedSession(r.Context(), project, ls.ID, continued); err != nil {
			s.logf("starting %s's %s session from %s: %v", project, ls.ID, continued.ID, err)
		}
	}
	s.sessionsChanged(r.Context(), project)
	return writeJSON(w, http.StatusCreated, s.apiSession(r.Context(), project, ls.ID))
}

// seedLines is how many of the old session's last messages a session
// continued from it starts with, and seedLineMax how much of each.
const (
	seedLines   = 6
	seedLineMax = 600
)

// seedSession starts session to where session from left off: a notice in its
// chat, which the user sees and its lead reads with its first message, naming
// from and quoting its last exchanges. Nothing of from's is reused: to has an
// id, lead, conversation and memory of its own, and from's agents stay
// from's. What mattered in from, its lead wrote to the project's memory.
func (s *Server) seedSession(ctx context.Context, project, to string, from state.LeadSession) error {
	items, err := s.store.ChatItems(ctx, project, state.LeadSessionName(from.ID))
	if err != nil {
		return err
	}
	var lines []string
	for i := len(items) - 1; i >= 0 && len(lines) < seedLines; i-- {
		var item api.ChatItem
		if json.Unmarshal(items[i].Data, &item) != nil || item.Hidden {
			continue
		}
		who := map[string]string{"user": "The user", "assistant": "The lead"}[item.Kind]
		text := strings.TrimSpace(item.Text)
		if who == "" || text == "" {
			continue
		}
		lines = append(lines, who+": "+cutText(text, seedLineMax))
	}
	slices.Reverse(lines)
	var b strings.Builder
	fmt.Fprintf(&b, "This session continues the session %q (%s), which is closed and kept in this chat's history.", from.Topic, from.ID)
	if len(lines) > 0 {
		b.WriteString(" Where it left off:\n\n")
		b.WriteString(strings.Join(lines, "\n\n"))
	}
	b.WriteString("\n\nIts agents still belong to it; what it learned is in the project's memory.")
	// Its lead as leadFromPath reads a session never written to: the notice
	// waits in its conversation, and makes nothing, needs no login, until the
	// first message does.
	lead := state.Agent{
		Project: project, Name: state.LeadSessionName(to), AI: agent.ChatTool,
		Interface: state.InterfaceChat, Role: state.RoleLead,
	}
	if project == state.HomeProject {
		lead = s.manager(nil).HomeSession(to)
	}
	return s.chat.Notice(lead, b.String(), chat.NoticeOptions{})
}

// cutText is text cut to at most max bytes, at a rune, marked when cut.
func cutText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	for max > 0 && !utf8.RuneStart(text[max]) {
		max--
	}
	return text[:max] + "…"
}

// inheritSessionSettings starts session to on what session from runs on: its
// AI tool and accounts, and the settings chosen on them (model, permission
// mode, effort, context window), so a new session is the last ones chosen
// rather than the tool's defaults. They are stored as to's own chat settings,
// as if chosen in its composer, which its session applies when it starts.
// Planning isn't carried: it is a task's, and from's plan isn't to's.
func (s *Server) inheritSessionSettings(ctx context.Context, project, from, to string) error {
	if _, err := s.store.LeadSession(ctx, project, from); err != nil {
		return err
	}
	src, err := s.store.Chat(ctx, project, state.LeadSessionName(from))
	if err != nil {
		return err
	}
	if len(src.Options) == 0 && src.Tool == "" && src.Account == "" && len(src.ToolAccounts) == 0 {
		return nil // nothing chosen: to starts where from did, on the defaults
	}
	// The tool from runs, which is what its settings are for: its own choice,
	// or the project's when it made none (agent.LeadChoiceOf).
	choice, err := s.manager(nil).LeadChoiceOf(ctx, state.Agent{Project: project, Name: state.LeadSessionName(from)})
	if err != nil {
		return err
	}
	inherited := state.Chat{Tool: choice.Tool, Account: choice.Account, ToolAccounts: choice.ToolAccounts, Options: map[string]string{}}
	for k, v := range src.Options {
		// "plan", and "<tool>/plan" put aside for another tool (chat.applyTool).
		if k == api.ChatOptionPlan || strings.HasSuffix(k, "/"+api.ChatOptionPlan) {
			continue
		}
		inherited.Options[k] = v
	}
	return s.store.SaveChat(ctx, project, state.LeadSessionName(to), inherited)
}

// apiSession is one session as listSessions shows it, open or closed.
func (s *Server) apiSession(ctx context.Context, project, id string) api.LeadSession {
	for _, closed := range []bool{false, true} {
		all, _ := s.apiSessions(ctx, project, closed)
		for _, ls := range all {
			if ls.ID == id {
				return ls
			}
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

// closeSession answers POST /v1/projects/{project}/sessions/{session}/close:
// the tab's close. Its conversation stops and is kept, with its HOME and its
// agents, in the chat's history, from where it can be read, reopened or
// continued from; its agents' notices go to the open session most recently
// active meanwhile (leadOf). What it was working on is forgotten, as a task
// put down.
func (s *Server) closeSession(w http.ResponseWriter, r *http.Request) error {
	project, id := r.PathValue("project"), r.PathValue("session")
	// Closed first, so that a message arriving now is refused rather than
	// starting the turn the stop below would cut.
	if err := s.store.CloseLeadSession(r.Context(), project, id, time.Now()); err != nil {
		return err
	}
	ref := agent.SessionRef(project, id)
	s.chat.Stop(ref, "the session was closed")
	s.chat.Forget(ref)
	s.settleLeadCache(ref, true)
	s.stopSessionAPI(project, id)
	if err := s.memory().ForgetSession(r.Context(), project, id); err != nil {
		s.logf("forgetting what %s's %s session was working on: %v", project, id, err)
	}
	s.logf("Closed %s's %s session into its history", project, id)
	s.sessionsChanged(r.Context(), project)
	return writeJSON(w, http.StatusOK, s.apiSession(r.Context(), project, id))
}

// reopenSession answers POST /v1/projects/{project}/sessions/{session}/reopen:
// a session from the chat's history open again as it was, its agents' notices
// coming back to it.
func (s *Server) reopenSession(w http.ResponseWriter, r *http.Request) error {
	project, id := r.PathValue("project"), r.PathValue("session")
	if err := s.store.ReopenLeadSession(r.Context(), project, id); err != nil {
		return err
	}
	s.logf("Reopened %s's %s session", project, id)
	s.sessionsChanged(r.Context(), project)
	return writeJSON(w, http.StatusOK, s.apiSession(r.Context(), project, id))
}

// deleteSession answers DELETE /v1/projects/{project}/sessions/{session}:
// a session, open or in the history, goes for good. Its conversation stops and
// goes, with its HOME and socket, and its agents are left to nobody; its id
// is never used again. Nothing of it is summarised: what mattered, its lead
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
		ls, err := s.store.LeadSession(r.Context(), project, req.Session)
		if err != nil {
			return err
		}
		if ls.Closed() {
			return errSessionClosed
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
