package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/github"
	"agentbox/internal/state"
)

// Questions are the chain the user asked for: an agent that can't decide
// something asks its project's chat; the lead answers if it can, and passes it
// to the user when it can't. The agent waits, so every question ends with an
// answer or a cancellation.

var errNotAnAgent = errors.New("that is the project's chat, not one of its agents")

// askTimeout bounds how long an agent waits. Long, because a question may sit
// until somebody reads it, but not forever: an unanswered question would hold
// the agent's turn open indefinitely.
const askTimeout = 2 * time.Hour

// waiters wakes the agents whose questions were answered.
type waiters struct {
	mu sync.Mutex
	by map[string]chan state.Question
}

func newWaiters() *waiters { return &waiters{by: map[string]chan state.Question{}} }

func (w *waiters) add(id string) chan state.Question {
	w.mu.Lock()
	defer w.mu.Unlock()
	ch := make(chan state.Question, 1)
	w.by[id] = ch
	return ch
}

func (w *waiters) remove(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.by, id)
}

func (w *waiters) resolve(q state.Question) {
	w.mu.Lock()
	ch := w.by[q.ID]
	delete(w.by, q.ID)
	w.mu.Unlock()
	if ch != nil {
		ch <- q
	}
}

// ask is the in-agent route: an agent asks its project's chat something and
// waits for the answer.
func (s *Server) ask(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		var req api.AskRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		if req.Question == "" {
			return errors.New("no question: say what you need decided")
		}
		q := state.Question{
			ID: newID(), Project: a.Project, Agent: a.Name,
			Text: req.Question, Context: req.Context,
			Status: state.QuestionPending, CreatedAt: time.Now(),
		}
		// Waiting before the question is stored, where it can be answered: an
		// answer that came in between would find nobody to give it to, and the
		// agent would wait out askTimeout for one it had already been sent.
		ch := s.waiting.add(q.ID)
		defer s.waiting.remove(q.ID)
		if err := s.store.AddQuestion(r.Context(), q); err != nil {
			return err
		}
		s.captureEvent(r.Context(), a.Project, a.Name, "question_asked", map[string]any{
			"question": q.Text, "context": q.Context,
		}, "")
		s.events.publish(api.EventQuestion, toAPIQuestion(q))
		s.record(r.Context(), questionEvent(q, a.Title, api.AgentAsked, q.CreatedAt))
		s.logf("%s asks its project's chat: %s", a.Ref(), req.Question)
		// Put it in front of the lead, and wake it: an agent waits on an answer,
		// so a question always starts a turn whatever finishNotices says.
		s.tellLead(r.Context(), a.Project, questionNotice(q), true)

		select {
		case answered := <-ch:
			return writeJSON(w, http.StatusOK, toAPIQuestion(answered))
		case <-r.Context().Done():
			return r.Context().Err()
		case <-time.After(askTimeout):
			q.Status = state.QuestionCancelled
			s.store.CancelQuestions(context.WithoutCancel(r.Context()), a.Project, a.Name)
			return fmt.Errorf("nobody answered within %s: decide it yourself, and say what you chose", askTimeout)
		}
	}
}

// askForTest runs the same path as the in-agent route, without HTTP.
func (s *Server) askForTest(ctx context.Context, a state.Agent, question, about string) (api.Question, error) {
	q := state.Question{
		ID: newID(), Project: a.Project, Agent: a.Name, Text: question, Context: about,
		Status: state.QuestionPending, CreatedAt: time.Now(),
	}
	ch := s.waiting.add(q.ID)
	defer s.waiting.remove(q.ID)
	if err := s.store.AddQuestion(ctx, q); err != nil {
		return api.Question{}, err
	}
	s.captureEvent(ctx, a.Project, a.Name, "question_asked", map[string]any{
		"question": q.Text, "context": q.Context,
	}, "")
	s.record(ctx, questionEvent(q, a.Title, api.AgentAsked, q.CreatedAt))
	s.tellLead(ctx, a.Project, questionNotice(q), true)
	select {
	case answered := <-ch:
		return toAPIQuestion(answered), nil
	case <-ctx.Done():
		return api.Question{}, ctx.Err()
	case <-time.After(askTimeout):
		return api.Question{}, errors.New("nobody answered")
	}
}

func questionNotice(q state.Question) string {
	notice := fmt.Sprintf("%s asks: %s", q.Agent, q.Text)
	if q.Context != "" {
		notice += "\n\nWhat it was doing: " + q.Context
	}
	return notice + fmt.Sprintf("\n\nanswer_question (id %s), or escalate_question if it is genuinely the user's call.", q.ID)
}

// answerQuestion records an answer and hands it to the waiting agent.
func (s *Server) answerQuestion(ctx context.Context, id, answer, by string) (state.Question, error) {
	q, err := s.store.AnswerQuestion(ctx, id, answer, by)
	if err != nil {
		return q, err
	}
	s.captureEvent(ctx, q.Project, q.Agent, "question_answered", map[string]any{
		"question": q.Text, "answer": q.Answer, "answeredBy": q.AnsweredBy,
	}, "")
	s.waiting.resolve(q)
	s.events.publish(api.EventQuestion, toAPIQuestion(q))
	s.record(ctx, questionEvent(q, s.titleOf(ctx, q.Project, q.Agent), api.AgentAnswered, q.AnsweredAt))
	return q, nil
}

// titleOf is what an agent is called now, for an event about it that doesn't
// have the agent in hand. An agent that has gone away has no title left.
func (s *Server) titleOf(ctx context.Context, project, agent string) string {
	a, err := s.store.Agent(ctx, project, agent)
	if err != nil {
		return ""
	}
	return a.Title
}

// leadQuestions lists what the lead still has to deal with.
func (s *Server) leadQuestions(w http.ResponseWriter, r *http.Request) error {
	questions, err := s.store.Questions(r.Context(), r.PathValue("project"), r.URL.Query().Get("all") == "")
	if err != nil {
		return err
	}
	out := make([]api.Question, 0, len(questions))
	for _, q := range questions {
		out = append(out, toAPIQuestion(q))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) leadAnswerQuestion(w http.ResponseWriter, r *http.Request) error {
	var req api.AnswerQuestionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Answer == "" {
		return errors.New("no answer: say what the agent should do")
	}
	q, err := s.store.Question(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if q.Project != r.PathValue("project") {
		return fmt.Errorf("question %s: %w", q.ID, state.ErrNotFound)
	}
	if q.Credential() {
		return errOnlyTheUser(q)
	}
	answered, err := s.answerQuestion(r.Context(), q.ID, req.Answer, "lead")
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, toAPIQuestion(answered))
}

// leadEscalateQuestion passes a question the lead can't answer to the user.
func (s *Server) leadEscalateQuestion(w http.ResponseWriter, r *http.Request) error {
	var req api.EscalateQuestionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	q, err := s.store.Question(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if q.Project != r.PathValue("project") {
		return fmt.Errorf("question %s: %w", q.ID, state.ErrNotFound)
	}
	if q.Credential() {
		return errOnlyTheUser(q)
	}
	escalated, err := s.store.EscalateQuestion(r.Context(), q.ID, req.Why)
	if err != nil {
		return err
	}
	s.captureEvent(r.Context(), escalated.Project, escalated.Agent, "question_escalated", map[string]any{
		"question": escalated.Text, "why": escalated.Escalation,
	}, "")
	s.events.publish(api.EventQuestion, toAPIQuestion(escalated))
	s.logf("%s/%s's question went to the user: %s", q.Project, q.Agent, req.Why)
	return writeJSON(w, http.StatusOK, toAPIQuestion(escalated))
}

// projectQuestions is the user's view: what is waiting for them.
func (s *Server) projectQuestions(w http.ResponseWriter, r *http.Request) error {
	return s.leadQuestions(w, r)
}

// answerAsUser answers a question the lead escalated.
func (s *Server) answerAsUser(w http.ResponseWriter, r *http.Request) error {
	var req api.AnswerQuestionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Answer == "" {
		return errors.New("no answer: say what the agent should do")
	}
	q, err := s.store.Question(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if q.Project != r.PathValue("project") {
		return fmt.Errorf("question %s: %w", q.ID, state.ErrNotFound)
	}
	// A credential request is answered with the credential, which a written
	// answer can't carry: it would be told to the agent as text.
	if q.Credential() {
		return fmt.Errorf("%s asks for %s: answer it from its card in the app", q.Ref(), credentialWanted(q))
	}
	answered, err := s.answerQuestion(r.Context(), q.ID, req.Answer, "user")
	if err != nil {
		return err
	}
	s.countFeature(api.FeatureQuestionAnswer)
	return writeJSON(w, http.StatusOK, toAPIQuestion(answered))
}

// errOnlyTheUser refuses the lead a credential request: the user answers it,
// in the app, so that the value never passes through a chat (D95).
func errOnlyTheUser(q state.Question) error {
	return fmt.Errorf("%s asked the user for %s, which only the user can answer, in the app: don't ask for the value in chat", q.Ref(), credentialWanted(q))
}

func toAPIQuestion(q state.Question) api.Question {
	out := api.Question{
		ID: q.ID, Project: q.Project, Agent: q.Agent, Ref: q.Ref(),
		Kind: q.Kind, SecretName: q.SecretName, Question: q.Text, Context: q.Context, Status: q.Status,
		Answer: q.Answer, AnsweredBy: q.AnsweredBy, Escalation: q.Escalation,
		CreatedAt: q.CreatedAt,
	}
	if !q.AnsweredAt.IsZero() {
		out.AnsweredAt = &q.AnsweredAt
	}
	return out
}

// tellLead puts something in front of a project's chat. With act it starts a
// turn too, so the lead reacts on its own; without, the notice waits in the
// conversation and the lead reads it the next time the user writes — the
// history still shows what happened, and nothing is spent on it.
//
// Autonomy governs what the lead then *does* about a notice it acts on — act,
// or propose and wait — not whether it notices at all: the lead's own brief
// tells it to read and decide either way.
// The prose is hidden from the app throughout: it is written for the lead's
// benefit, and the user reads the same thing as the agent's thread in the
// project chat's rail (see agentevents.go).
func (s *Server) tellLead(ctx context.Context, project, notice string, act bool) {
	if _, err := s.store.Project(ctx, project); err != nil {
		return
	}
	lead, err := s.manager(nil).Lead(ctx, project)
	if err != nil {
		// No chat yet: the notice would have nowhere to go. The question still
		// waits, and the user sees it in the project's Questions.
		return
	}
	if act {
		// The notice is about to start a turn, so this is a moment a full
		// session can be replaced (D73). The notice then waits for that, and
		// lands in the fresh session.
		s.rolloverIfNeeded(ctx, lead)
	}
	if err := s.chat.Notice(lead, notice, chat.NoticeOptions{Act: act, Hidden: true}); err != nil {
		s.logf("telling %s's chat: %v", project, err)
	}
}

// finishStartsTurn reports whether this agent's finish notice is worth a turn
// of the lead's, which is the setting's whole point: every finish costs a full
// turn even when there is nothing to decide, so a project can ask for the
// notice to be recorded and nothing more. A project that has gone missing
// keeps the default, which is to tell the chat.
func (s *Server) finishStartsTurn(ctx context.Context, a state.Agent) bool {
	p, err := s.store.Project(ctx, a.Project)
	if err != nil {
		return true
	}
	return resolveFinishStartsTurn(p.FinishNotices, a.FinishNotice)
}

// resolveFinishStartsTurn is finishStartsTurn's table. FinishNoticesOff never
// starts a turn and FinishNoticesChat always does, whatever the agent that
// finished chose or didn't: those two decide it for every agent of the
// project. FinishNoticesLead leaves it to that agent's own FinishNotice,
// defaulting to true — the same as FinishNoticesChat — so an agent that chose
// nothing, or was made before this existed, still wakes the lead. Anything
// unrecognised is treated like FinishNoticesChat, the setting's own default.
func resolveFinishStartsTurn(projectFinishNotices, agentFinishNotice string) bool {
	switch projectFinishNotices {
	case state.FinishNoticesOff:
		return false
	case state.FinishNoticesLead:
		return agentFinishNotice != state.FinishNoticesOff
	default:
		return true
	}
}

// finishedTask reports whether a turn's end means the agent is genuinely done
// with what it was asked, rather than merely pausing for breath: cancelled,
// failed, and a turn cut short by a limit or a refusal (see stopNotes in
// package chat) all need a nudge to continue, not a reaction from the lead.
func finishedTask(result api.ChatTurnResult) bool {
	return result.State == "completed" && (result.StopReason == "" || result.StopReason == "end_turn")
}

// noticeAgentFinished tells a project's chat that one of its agents has
// genuinely finished the work it was given. A turn that ended any other way
// is left alone, so the lead isn't woken every time an agent stops to breathe.
// What the agent said last is quoted, which its brief asks it to make a
// summary of the work: read_agent is still there, but for the detail the lead
// may not need at all, rather than as the only way to learn what happened.
func (s *Server) noticeAgentFinished(ctx context.Context, a state.Agent, result api.ChatTurnResult) {
	if a.IsLead() || !finishedTask(result) {
		return
	}
	changes, pr := changesOf(a), s.prFor(ctx, a)
	ev := s.record(ctx, finishedEvent(a, changes, pr, s.chat.LastMessage(a), time.Now()))
	s.captureAgentFinished(ctx, a, changes, pr, ev.Summary)
	// Whether the lead reacts to this now is the project's to say, or the
	// agent's when the project leaves it to whichever agent finished — and
	// only ever for work the lead asked for (D87). A turn somebody drove from
	// the agent's own chat is theirs: it is recorded all the same, and waking
	// the lead to read it would cost a turn of its whole context for nothing.
	waiting := s.leadWasWaiting(a)
	s.tellLead(ctx, a.Project, finishNotice(ev), waiting && s.finishStartsTurn(ctx, a))
}

// leadAsked records that a project's chat asked an agent for something — its
// first task, or a message since — so the agent's next finish is news the
// chat is waiting for (D87). It lives in memory only: a daemon that stops
// ends every running turn, and the chat asks again.
func (s *Server) leadAsked(a state.Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leadWaits[a.Ref()] = true
}

// leadWasWaiting reports whether the chat was waiting on the agent, and stops
// it waiting: one finish answers one request.
func (s *Server) leadWasWaiting(a state.Agent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiting := s.leadWaits[a.Ref()]
	delete(s.leadWaits, a.Ref())
	return waiting
}

// prFor looks up the agent's pull request, if it has one. It answers
// quietly with nothing when GitHub isn't configured or the lookup fails: a
// finish notice shouldn't fail over it.
func (s *Server) prFor(ctx context.Context, a state.Agent) *api.PullRequest {
	if a.Branch == "" {
		return nil
	}
	p, err := s.store.Project(ctx, a.Project)
	if err != nil {
		return nil
	}
	m := s.manager(nil)
	account, err := m.GitHubAccountFor(p, a.GitHubAccount)
	if err != nil {
		return nil
	}
	token, err := m.Creds.GitHubToken(account)
	if err != nil || token == "" {
		return nil
	}
	repo, err := github.RepoOf(p.Root)
	if err != nil {
		return nil
	}
	// Read fresh rather than from the pull request cache: an agent that has
	// just finished may have opened its pull request seconds ago, and the
	// cache remembers "this agent has none" for longer than that. It is
	// found by the agent's commits, whatever branch they were pushed to.
	h := agentHeadOf(p.Root, a)
	if h.tip == "" {
		return nil
	}
	l := lookUp(ctx, s.gitHub(token), repo, h)
	for _, pr := range l.prs {
		if h.accepts(pr, l.via) {
			return &api.PullRequest{Number: pr.Number, Title: pr.Title, State: pr.State, URL: pr.URL, Draft: pr.Draft}
		}
	}
	return nil
}

// agentFinished is what package chat calls when an agent's turn ends.
func (s *Server) agentFinished(a state.Agent, result api.ChatTurnResult) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.runCtx), 30*time.Second)
	defer cancel()
	s.noticeAgentFinished(ctx, a, result)
}
