package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// What an agent of a shared project reports, for whoever asked for it.
//
// An agent's notices — it asks something, finishes (with its pull request)
// or fails — go to the lead of the session of the project's chat that made
// it, on the owner's AgentBox: for an agent there (tellOwnerOf), and for one
// on a peer's computer (sharemachines.go), whose session the peer was told
// when the lead made it (headerPeerSession) and names in each notice it sends
// back (api.ShareAgentEvent.Session). The owner reads them in that chat.
//
// When a paired AgentBox asked for the agent — it made it, or wrote last in
// that session before the lead made it (state.AddSharePeerAgent), or the
// agent runs on its computer — its own app hears them too: notifications in
// the project as it knows it ("<remote>~<project>"), which open that session.
//
//   - The owner's daemon publishes each as shareNoticeEvent, which its event
//     stream sends to that peer alone (shareEvents), and the peer's daemon
//     keeps as one of its own (remoteNoticeIn): its inbox, its toasts and its
//     paired phone have it. The owner's own notifications are its user's,
//     and go to no peer.
//   - The computer of a peer running the agent tells its user itself
//     (tellGuest).
//
// Its user answers a question from there (POST
// /v1/projects/<remote>~<project>/questions/{id}/answer), as far as its access
// lets it write in the project's chats (ShareAccessChat): for an agent on the
// owner's computer the owner answers it, and refuses one past its access; for
// one on its own computer the question is here (answerGuestQuestion), under
// the same rule. The lead that heard the question is told who answered it,
// so it doesn't answer it again (tellAnswered).

// shareNoticeEvent is, on an owner's event stream, a notification for one
// paired AgentBox (shareNotice). The owner's app has no use for it.
const shareNoticeEvent = "share.notice"

// shareNotice is shareNoticeEvent's data: a notification, and the peer it is
// for.
type shareNotice struct {
	Peer string `json:"peer"`
	api.Notification
}

// askerNotification is what ev, a notice about a, tells the person who asked
// for it: a question, a finish or a failure. What is only the lead's to know
// — that it is still working, waiting on work it left running — makes none.
func askerNotification(a state.Agent, ev peerEvent) (api.Notification, bool) {
	n := api.Notification{Project: a.Project, Agent: a.Name, Title: a.Title, Session: a.Session(), Text: cutNotice(ev.Summary)}
	switch ev.Status {
	case state.RemoteAsking:
		n.Kind, n.Question = api.NotifyQuestion, ev.Question
	case state.RemoteDone:
		n.Kind, n.PRURL = api.NotifyFinished, ev.PRURL
	case state.RemoteFailed:
		n.Kind = api.NotifyFailed
	default:
		return api.Notification{}, false
	}
	return n, true
}

// tellAsker tells the paired AgentBox that asked for one of this machine's
// agents what ev says, over its event stream: nothing when nobody did, or
// when it isn't connected (it reads the chat when it is back).
func (s *Server) tellAsker(ctx context.Context, a state.Agent, ev peerEvent) {
	if s.peering == nil {
		return
	}
	peer, err := s.store.SharePeerOfAgent(ctx, a.Project, a.Name)
	if err != nil {
		return
	}
	n, ok := askerNotification(a, ev)
	if !ok {
		return
	}
	n.ID, n.At, n.Ref = newID(), time.Now(), a.Ref()
	s.events.publish(shareNoticeEvent, shareNotice{Peer: peer, Notification: n})
}

// shareNoticeFor says whether an event of the owner's goes to peer: a
// notification only when it is one for that peer (shareNotice), or marks a
// media item unseen, as every event of its projects does. The owner's
// questions are for its own user.
func shareNoticeFor(typ string, data json.RawMessage, peer string) bool {
	switch typ {
	case shareNoticeEvent:
		var n shareNotice
		return json.Unmarshal(data, &n) == nil && n.Peer == peer
	case api.EventNotification:
		var n api.Notification
		return json.Unmarshal(data, &n) == nil && n.Kind == api.NotifyMedia
	}
	return true
}

// remoteNoticeIn keeps a notification the owner sent this AgentBox, already
// named as projects are here (fromRemote), as one of its own.
func (s *Server) remoteNoticeIn(ctx context.Context, data json.RawMessage) {
	var sn shareNotice
	if json.Unmarshal(data, &sn) != nil || sn.Kind == "" || !strings.Contains(sn.Project, remoteSep) {
		return
	}
	n := sn.Notification
	n.Seen, n.Media = false, nil
	s.notify(ctx, n)
}

// remoteQuestionMoved marks seen the notifications here about a question of
// the owner's that was answered or given up on there: nobody is asked it any
// more.
func (s *Server) remoteQuestionMoved(ctx context.Context, data json.RawMessage) {
	var q api.Question
	if json.Unmarshal(data, &q) != nil || q.ID == "" || q.Status == state.QuestionPending || q.Status == state.QuestionEscalated {
		return
	}
	ids, err := s.store.SeeQuestionNotices(ctx, q.ID)
	if err != nil {
		s.logf("share: marking %s's question seen: %v", q.Agent, err)
		return
	}
	if len(ids) > 0 {
		s.events.publish(api.EventNotificationsSeen, api.SeeNotificationsRequest{IDs: ids})
	}
}

// tellGuest tells this computer's user what ev says about a, an agent the
// lead of a project shared with this AgentBox made here: they asked for it
// (sharemachines.go). An agent a hub machine's lead made is that machine's
// user's, who reads its chat there.
func (s *Server) tellGuest(ctx context.Context, a state.Agent, origin state.PeerAgent, ev peerEvent) {
	remote, ok := strings.CutPrefix(origin.OriginID, shareOriginPrefix)
	if !ok {
		return
	}
	n, ok := askerNotification(a, ev)
	if !ok {
		return
	}
	n.Project, n.Session = remote+remoteSep+origin.OriginProject, origin.OriginSession
	s.notify(ctx, n)
}

// answerGuestQuestion answers here a question one of this computer's agents
// asked, which an owner's lead made for this AgentBox, and which its user
// answers in the project as they know it (POST
// /v1/projects/<remote>~<project>/questions/{id}/answer): the owner has no
// such question to answer. It reports whether the request was one.
func (s *Server) answerGuestQuestion(w http.ResponseWriter, r *http.Request, remoteID, project, rest string) bool {
	id, ok := strings.CutPrefix(rest, "/questions/")
	if r.Method != http.MethodPost || !ok {
		return false
	}
	if id, ok = strings.CutSuffix(id, "/answer"); !ok || strings.Contains(id, "/") {
		return false
	}
	id, err := url.PathUnescape(id)
	if err != nil {
		return false
	}
	ctx := r.Context()
	q, err := s.store.Question(ctx, id)
	if err != nil {
		return false
	}
	origin, err := s.store.PeerAgent(ctx, q.Project, q.Agent)
	if err != nil || origin.OriginID != shareOriginPrefix+remoteID || origin.OriginProject != project {
		return false
	}
	if err := s.answerGuest(w, r, q, origin); err != nil {
		writeError(w, err)
	}
	return true
}

func (s *Server) answerGuest(w http.ResponseWriter, r *http.Request, q state.Question, origin state.PeerAgent) error {
	ctx := r.Context()
	var req api.AnswerQuestionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Answer == "" {
		return errors.New("no answer: say what the agent should do")
	}
	if q.Credential() {
		return fmt.Errorf("%s asks for %s: answer it from its card in the app", q.Ref(), credentialWanted(q))
	}
	// The owner's rule for its own agents, for those it made here too: it
	// lets this AgentBox write in the project's chats, or only look.
	if c := s.remoteConn(strings.TrimPrefix(origin.OriginID, shareOriginPrefix)); c != nil {
		if access := s.remoteAccess(ctx, c, c.r.ID+remoteSep+origin.OriginProject); access != "" && state.ShareAccessRank(access) < state.ShareAccessRank(state.ShareAccessChat) {
			return shareOnlyLooks{owner: c.r.Name, project: origin.OriginProject, agent: q.Agent}
		}
	}
	answered, err := s.answerQuestion(ctx, q.ID, req.Answer, "user")
	if err != nil {
		return err
	}
	s.countFeature(api.FeatureQuestionAnswer)
	// The owner's lead heard the question, and would answer it again.
	if a, err := s.store.Agent(ctx, q.Project, q.Agent); err == nil {
		s.sendPeerEvent(origin.OriginID, origin.OriginName, peerEvent{
			Project: origin.OriginProject, Agent: a.Name, Title: a.Title, Branch: a.Branch, Status: state.RemoteWorking,
			Session: origin.OriginSession, Notice: answeredNotice(s.shareName(ctx), a.Name, q.Text, req.Answer),
		})
	}
	return writeJSON(w, http.StatusOK, toAPIQuestion(answered))
}

// shareOnlyLooks refuses this computer's user an answer to an agent of a
// project whose owner lets this AgentBox only look in it, as the owner
// refuses one to an agent of its own.
type shareOnlyLooks struct{ owner, project, agent string }

func (e shareOnlyLooks) Error() string {
	return fmt.Sprintf("%s lets this AgentBox only look in %s, so it can't answer %s's question; %s chooses what it may do, in Settings → Share",
		e.owner, e.project, e.agent, e.owner)
}

// remoteAccess is what a remote lets this AgentBox do in one of its projects,
// as it last listed them: "" when it doesn't say.
func (s *Server) remoteAccess(ctx context.Context, c *remoteConn, name string) string {
	projects, _ := s.remoteLists(ctx, c)
	for _, raw := range projects {
		var p struct {
			Name   string             `json:"name"`
			Remote *api.ProjectRemote `json:"remote"`
		}
		if json.Unmarshal(raw, &p) == nil && p.Name == name && p.Remote != nil {
			return p.Remote.Access
		}
	}
	return ""
}

// tellAnswered tells the session whose lead made a, and heard its question,
// that a paired AgentBox's user answered it from their side.
func (s *Server) tellAnswered(ctx context.Context, q state.Question, who *api.Person) {
	a, err := s.store.Agent(ctx, q.Project, q.Agent)
	if err != nil || s.isPeerAgent(ctx, a) {
		return
	}
	s.tellOwnerOf(ctx, a, answeredNotice(who.Name+", of a paired AgentBox,", a.Name, q.Text, q.Answer), chat.NoticeOptions{})
}

// answeredNotice is what a lead is told when someone answered one of its
// agents' questions without it.
func answeredNotice(who, agent, question, answer string) string {
	return fmt.Sprintf("[AgentBox: %s answered %s's question (%q) directly: %q. %s has the answer and goes on, so don't answer it again.]",
		who, agent, headWords(question, 120), headWords(answer, 400), agent)
}
