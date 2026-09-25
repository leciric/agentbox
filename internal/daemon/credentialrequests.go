package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/secrets"
	"agentbox/internal/state"
)

// A credential request is an agent asking the user for a credential it lacks —
// a GitHub account that can push, or a secret — with the value never passing
// through a model (D95). It is a question underneath, so it waits, is listed
// and shows in the agent's thread the way questions do, but it goes straight
// to the user: the lead can't answer it, and nobody types the value into a
// chat. The user answers from the app, the daemon puts the credential where
// the agent reads it, and what the agent is told is only the outcome.

// envFile is where an agent's shell picks its credentials up, as the agent
// knows it: ~/.config/agentbox/env, which sources the secrets file after it.
const envFile = "~/.config/agentbox/env"

// requestCredential is the in-agent route: the agent asks, and waits.
func (s *Server) requestCredential(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		var req api.CredentialRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		q, err := s.requestCredentialFor(r.Context(), a, req)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, toAPIQuestion(q))
	}
}

// requestCredentialFor records an agent's request, puts it in front of the
// user and waits for the answer.
func (s *Server) requestCredentialFor(ctx context.Context, a state.Agent, req api.CredentialRequest) (state.Question, error) {
	if a.IsLead() {
		return state.Question{}, errNotAnAgent
	}
	q := state.Question{
		ID: newID(), Project: a.Project, Agent: a.Name, Kind: strings.TrimSpace(req.Kind),
		Text:   strings.TrimSpace(req.Reason),
		Status: state.QuestionEscalated, CreatedAt: time.Now(),
	}
	switch q.Kind {
	case state.QuestionGitHub:
	case state.QuestionSecret:
		q.SecretName = strings.TrimSpace(req.Name)
		if q.SecretName == "" {
			return state.Question{}, errors.New("a secret needs a name: the environment variable it should be in, like STRIPE_SECRET_KEY")
		}
		if err := secrets.ValidateName(q.SecretName); err != nil {
			return state.Question{}, err
		}
	default:
		return state.Question{}, fmt.Errorf("kind is %q: it is github (an account that can push and use gh) or secret (a value in an environment variable)", req.Kind)
	}
	if q.Text == "" {
		return state.Question{}, errors.New("no reason: say what failed and what you need the credential for, so the user can decide")
	}
	if err := s.store.AddQuestion(ctx, q); err != nil {
		return state.Question{}, err
	}
	s.captureEvent(ctx, a.Project, a.Name, "credential_requested", map[string]any{
		"kind": q.Kind, "name": q.SecretName, "reason": q.Text,
	}, "")
	ch := s.waiting.add(q.ID)
	defer s.waiting.remove(q.ID)
	s.events.publish(api.EventQuestion, toAPIQuestion(q))
	s.record(ctx, questionEvent(q, a.Title, api.AgentAsked, q.CreatedAt))
	s.logf("%s asks the user for %s: %s", a.Ref(), credentialWanted(q), q.Text)
	// The lead is told, so it knows why the agent is waiting, but not woken:
	// there is nothing it can do about it.
	s.tellLead(ctx, a.Project, credentialNotice(q), false)

	select {
	case answered := <-ch:
		return answered, nil
	case <-ctx.Done():
		return state.Question{}, ctx.Err()
	case <-time.After(askTimeout):
		s.store.CancelQuestions(context.WithoutCancel(ctx), a.Project, a.Name)
		return state.Question{}, fmt.Errorf("nobody answered within %s: carry on without it, and say in your final message what it was needed for", askTimeout)
	}
}

func credentialWanted(q state.Question) string {
	if q.Kind == state.QuestionSecret {
		return "the secret $" + q.SecretName
	}
	return "a GitHub account"
}

func credentialNotice(q state.Question) string {
	return fmt.Sprintf("%s asked the user for %s: %s\n\nOnly the user can answer this, from the card in the app, "+
		"and it waits until they do. You can't answer it, and don't ask anybody for the value in this chat.",
		q.Agent, credentialWanted(q), q.Text)
}

// answerCredential is the user's answer, from the app.
func (s *Server) answerCredential(w http.ResponseWriter, r *http.Request) error {
	var req api.AnswerCredentialRequest
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
	answered, err := s.answerCredentialFor(r.Context(), q, req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, toAPIQuestion(answered))
}

// answerCredentialFor puts the credential where the agent reads it and hands
// the waiting agent what happened. A credential that couldn't be put in place
// leaves the request waiting, so the user can try again.
func (s *Server) answerCredentialFor(ctx context.Context, q state.Question, req api.AnswerCredentialRequest) (state.Question, error) {
	if !q.Credential() {
		return q, fmt.Errorf("question %s asks for a decision, not a credential: answer it instead", q.ID)
	}
	if !q.Waiting() {
		return q, fmt.Errorf("question %s was already %s", q.ID, q.Status)
	}
	given := 0
	for _, set := range []bool{req.GitHubAccount != "", req.Value != "", req.Refuse} {
		if set {
			given++
		}
	}
	if given != 1 {
		return q, errors.New("answer with one of a GitHub account, a value, or a refusal")
	}
	var outcome string
	switch {
	case req.Refuse:
		outcome = "refused"
		if why := strings.TrimSpace(req.Reason); why != "" {
			outcome += ": " + why
		}
		outcome += ". Carry on without it, and don't ask for it again in chat; say in your final message what it was needed for."
	case q.Kind == state.QuestionGitHub:
		if req.GitHubAccount == "" {
			return q, errors.New("this request is for a GitHub account: pick one")
		}
		var err error
		if outcome, err = s.giveGitHubAccount(ctx, q, strings.TrimSpace(req.GitHubAccount)); err != nil {
			return q, err
		}
	default:
		if req.Value == "" {
			return q, fmt.Errorf("this request is for the secret $%s: give its value", q.SecretName)
		}
		if err := s.giveSecret(ctx, q, req.Value); err != nil {
			return q, err
		}
		outcome = fmt.Sprintf("It's in $%s now, for every agent of this project. A shell that started before this doesn't "+
			"have it: run `. %s` in the command that needs it. Use it by name, and never print its value.", q.SecretName, envFile)
	}
	return s.answerQuestion(ctx, q.ID, outcome, "user")
}

// giveGitHubAccount makes an account the project's, the way the project's
// page does, and writes it into the agent that asked, the way
// `agentbox github-account project/agent` does.
func (s *Server) giveGitHubAccount(ctx context.Context, q state.Question, account string) (string, error) {
	if err := s.checkGitHubAccount(account); err != nil {
		return "", err
	}
	a, err := s.store.Agent(ctx, q.Project, q.Agent)
	if err != nil {
		return "", err
	}
	if err := s.store.SetProjectGitHubAccount(ctx, q.Project, account); err != nil {
		return "", err
	}
	s.pulls.reset()
	s.events.publish(api.EventProject, api.ProjectChange{Name: q.Project})
	if _, err := s.manager(nil).SetGitHubAccount(ctx, a, account); err != nil {
		return "", fmt.Errorf("%q is the project's GitHub account now, but couldn't be written into %s: %w", account, a.Ref(), err)
	}
	s.logf("%s: the user gave it the GitHub account %s", a.Ref(), account)
	who := ""
	if login, err := s.manager(nil).Creds.GitHubLogin(account); err == nil && login != "" {
		who = ", which is the GitHub user " + login
	}
	return fmt.Sprintf("You have the GitHub account %q now%s, and so does this project. GH_TOKEN and GITHUB_TOKEN hold "+
		"its token; a shell that started before this has the old one, so run `. %s` in the command that needs it. "+
		"gh works with it as it is, and pushing works once git uses it too: run `gh auth setup-git` once, with the "+
		"remote on HTTPS.",
		account, who, envFile), nil
}

// giveSecret stores the value as a project secret, the way the Secrets tab
// does, and writes it into the project's running agents.
func (s *Server) giveSecret(ctx context.Context, q state.Question, value string) error {
	stored, err := s.secrets().Set(ctx, q.Project, "", q.SecretName, value)
	if err != nil {
		return err
	}
	// Nothing is logged about the value, and the event carries only the name.
	s.logf("secret %s set for %s, as %s/%s asked", stored.Name, secretScope(q.Project, ""), q.Project, q.Agent)
	return s.deliver(ctx, q.Project, "")
}
