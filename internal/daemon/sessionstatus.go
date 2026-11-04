package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// Where each open session of a project's chat stands (api.LeadSession's
// Status, NeedsYou, AgentsWorking, Unread), as a thread list shows it: what
// its tab, its sidebar row and the phone's bar draw, and what the app's
// notices are made from. It is worked out on every read of the list
// (apiSessions), and pushed as api.EventLeadSessionStatus whenever it changes:
// the broker shows this file every event (observe), each that can move a
// session's standing marks its project, and a moment later each marked
// project's list is read again and published if its standing moved.
//
// Needs you counts what waits on the user in the session and nobody else can
// answer: the lead's own permission requests (an approval AgentBox asks for
// is one), the agents' requests its chat shows as cards, and its agents'
// questions the lead passed on to the user (escalated; a credential or a
// connector request is one from the start). A question the lead is still
// deciding about is the lead's, not the user's.

// sessionStatusDelay gathers a burst of events, a turn's start and its first
// messages, into one read of the list.
const sessionStatusDelay = 250 * time.Millisecond

// sessionStatusWatch is the projects whose standing may have moved, and what
// was last published for each.
type sessionStatusWatch struct {
	mu         sync.Mutex
	dirty      map[string]bool
	last       map[string]string
	publishing sync.Mutex
}

// sessionStanding is where a session stands, from what its chat waits on and
// how many of its agents' questions wait on the user, with how many things
// need the user while it needs them.
func sessionStanding(att chat.Attention, questions int) (status string, needsYou int) {
	if n := att.Permissions + att.AgentPermissions + questions; n > 0 {
		return api.SessionNeedsYou, n
	}
	switch {
	case att.State == api.ChatError || att.Stalled:
		return api.SessionFailed, 0
	case att.State == api.ChatRunning || att.State == api.ChatStarting || att.State == api.ChatWaiting:
		return api.SessionWorking, 0
	}
	return api.SessionIdle, 0
}

// questionSessions counts the questions that wait on the user by the open
// session they wait in: their agent's, or, for an agent no open session owns,
// the open one most recently active, as the daemon routes its notices
// (leadOf). A question from an agent that is gone waits nowhere.
func questionSessions(open []state.LeadSession, activity map[string]time.Time, agents []state.Agent, questions []state.Question) map[string]int {
	out := map[string]int{}
	if len(open) == 0 {
		return out
	}
	isOpen := map[string]bool{}
	var latest string
	for _, ls := range open {
		isOpen[ls.ID] = true
		if latest == "" || !activity[ls.ID].Before(activity[latest]) {
			latest = ls.ID
		}
	}
	owner := map[string]string{}
	for _, a := range agents {
		owner[a.Name] = a.Session()
	}
	for _, q := range questions {
		if q.Status != state.QuestionEscalated {
			continue
		}
		session, ok := owner[q.Agent]
		if !ok {
			continue
		}
		if !isOpen[session] {
			session = latest
		}
		out[session]++
	}
	return out
}

// unread says a session has a message newer than when it was last seen, or,
// never seen, than when it was opened.
func unread(last *api.ChatLine, seen *time.Time, created time.Time) bool {
	if last == nil {
		return false
	}
	since := created
	if seen != nil {
		since = *seen
	}
	return last.At.After(since)
}

// standSessions fills in where each of out's open sessions stands.
func (s *Server) standSessions(ctx context.Context, project string, open []state.LeadSession, activity map[string]time.Time, agents []state.Agent, out []api.LeadSession) error {
	questions, err := s.store.Questions(ctx, project, true)
	if err != nil {
		return err
	}
	seen, err := s.store.LeadSessionsSeen(ctx, project)
	if err != nil {
		return err
	}
	asks := questionSessions(open, activity, agents, questions)
	working := map[string]int{}
	for _, a := range agents {
		if !a.IsLead() && a.Session() != "" && s.chatWorking(a.Ref()) {
			working[a.Session()]++
		}
	}
	for i := range out {
		row := &out[i]
		if row.ClosedAt != nil {
			continue
		}
		row.Status, row.NeedsYou = sessionStanding(s.chat.Attention(row.Ref), asks[row.ID])
		row.AgentsWorking = working[row.ID]
		if at, ok := seen[row.ID]; ok {
			row.SeenAt = &at
		}
		row.Unread = unread(row.Last, row.SeenAt, row.CreatedAt)
	}
	return nil
}

// observeSessionStatus is the broker's observer: it marks the project of an
// event that can move where one of its sessions stands.
func (s *Server) observeSessionStatus(typ string, data any) {
	var project string
	switch typ {
	case api.EventChat:
		ev, ok := data.(api.ChatEvent)
		if !ok || !movesStanding(ev) {
			return
		}
		project, _, _ = strings.Cut(ev.Agent, "/")
	case api.EventQuestion:
		if q, ok := data.(api.Question); ok {
			project = q.Project
		}
	case api.EventAgent:
		if c, ok := data.(api.AgentChange); ok {
			project, _, _ = strings.Cut(c.Ref, "/")
		}
	case api.EventLeadSessions:
		if c, ok := data.(api.LeadSessionsChange); ok {
			project = c.Project
		}
	}
	if project != "" {
		s.sessionStatusChanged(project)
	}
}

// movesStanding says a chat event can change a session's standing: the
// chat's state (a turn, a stall, an error), a permission request or its
// card, or a message, which may be unread.
func movesStanding(ev api.ChatEvent) bool {
	if ev.Session != nil || ev.Cleared {
		return true
	}
	it := ev.Item
	return it != nil && (it.Permission != nil || it.AgentPermission != nil || it.Kind == "user" || it.Kind == "assistant")
}

// sessionStatusChanged marks project, to be read again in a moment.
func (s *Server) sessionStatusChanged(project string) {
	w := &s.sessionStatus
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dirty == nil {
		w.dirty = map[string]bool{}
	}
	if len(w.dirty) == 0 {
		time.AfterFunc(sessionStatusDelay, s.publishSessionStatus)
	}
	w.dirty[project] = true
}

// publishSessionStatus reads each marked project's sessions again, and
// publishes those whose standing moved since it last did.
func (s *Server) publishSessionStatus() {
	w := &s.sessionStatus
	w.publishing.Lock()
	defer w.publishing.Unlock()
	w.mu.Lock()
	dirty := w.dirty
	w.dirty = nil
	w.mu.Unlock()
	if s.chat == nil || s.chat.Closing() {
		return
	}
	for project := range dirty {
		if s.chatProject(context.Background(), project) != nil {
			continue
		}
		list, err := s.apiSessions(context.Background(), project, false)
		if err != nil {
			continue // the store is closing, or the project went
		}
		mark := standingMark(list)
		w.mu.Lock()
		same := w.last[project] == mark
		if !same {
			if w.last == nil {
				w.last = map[string]string{}
			}
			w.last[project] = mark
		}
		w.mu.Unlock()
		if !same {
			s.events.publish(api.EventLeadSessionStatus, api.LeadSessionStatusChange{Project: project, Sessions: list})
		}
	}
}

// standingMark is what of a list's sessions a client must hear about when it
// changes.
func standingMark(list []api.LeadSession) string {
	var b strings.Builder
	for _, ls := range list {
		seen := int64(0)
		if ls.SeenAt != nil {
			seen = ls.SeenAt.UnixMilli()
		}
		fmt.Fprintf(&b, "%s|%s|%d|%d|%t|%d;", ls.ID, ls.Status, ls.NeedsYou, ls.AgentsWorking, ls.Unread, seen)
	}
	return b.String()
}

// seeSession answers POST /v1/projects/{project}/sessions/{session}/seen: the
// session was on screen, in a window or on the phone, which every other one
// then shows read.
func (s *Server) seeSession(w http.ResponseWriter, r *http.Request) error {
	project, id := r.PathValue("project"), r.PathValue("session")
	if err := s.chatProject(r.Context(), project); err != nil {
		return err
	}
	var req api.SeeLeadSessionRequest
	if r.ContentLength != 0 {
		if err := readJSON(r, &req); err != nil {
			return err
		}
	}
	at := time.Now()
	if req.At != nil && !req.At.IsZero() && req.At.Before(at) {
		at = *req.At
	}
	if _, err := s.store.SeeLeadSession(r.Context(), project, id, at); err != nil {
		return err
	}
	s.sessionStatusChanged(project)
	return writeJSON(w, http.StatusOK, s.apiSession(r.Context(), project, id))
}
