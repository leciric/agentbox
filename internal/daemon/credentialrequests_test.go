package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// requestInBackground asks for a credential as the agent would, and hands
// back what the agent is eventually told.
func requestInBackground(t *testing.T, d testDaemon, req api.CredentialRequest) (<-chan state.Question, string) {
	t.Helper()
	ctx := context.Background()
	a, err := d.srv.store.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	told := make(chan state.Question, 1)
	go func() {
		q, err := d.srv.requestCredentialFor(ctx, a, req)
		if err != nil {
			t.Errorf("requestCredentialFor: %v", err)
		}
		told <- q
	}()
	return told, waitForQuestion(t, d, "hello-stack")
}

func toldAgent(t *testing.T, told <-chan state.Question) state.Question {
	t.Helper()
	select {
	case q := <-told:
		return q
	case <-time.After(5 * time.Second):
		t.Fatal("the agent was never told the outcome")
		return state.Question{}
	}
}

// A secret goes from the user into the agent's environment without passing
// through anything a model reads: the agent is told the variable, and neither
// the answer nor the question's record carries the value.
func TestCredentialRequestForASecret(t *testing.T) {
	d, files := secretsDaemon(t)
	ctx := context.Background()
	const value = "sk_live_never_in_a_model"

	told, id := requestInBackground(t, d, api.CredentialRequest{
		Kind: "secret", Name: "STRIPE_SECRET_KEY", Reason: "the checkout tests need a Stripe key",
	})

	// It goes straight to the user, as a credential request.
	waiting, err := d.client.Questions(ctx, "hello-stack", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].Kind != "secret" || waiting[0].SecretName != "STRIPE_SECRET_KEY" ||
		waiting[0].Status != state.QuestionEscalated || waiting[0].Question != "the checkout tests need a Stripe key" {
		t.Fatalf("Questions() = %+v, want one secret request waiting for the user", waiting)
	}

	// A written answer can't carry a credential, and neither can the lead's.
	if _, err := d.client.AnswerQuestion(ctx, "hello-stack", id, value); err == nil {
		t.Error("a credential request was answered with text")
	}
	if code, body := leadAnswers(t, d, id); code == http.StatusOK || !strings.Contains(body, "only the user") {
		t.Errorf("the lead answering a credential request = %d %s, want it refused", code, body)
	}
	// One answer, not two.
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Value: value, Refuse: true}); err == nil {
		t.Error("an answer that both gave and refused was accepted")
	}

	answered, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Value: value})
	if err != nil {
		t.Fatal(err)
	}
	q := toldAgent(t, told)
	if !strings.Contains(q.Answer, "$STRIPE_SECRET_KEY") || q.AnsweredBy != "user" {
		t.Errorf("the agent was told %q by %q, want where the secret is", q.Answer, q.AnsweredBy)
	}
	for what, text := range map[string]string{"the agent's answer": q.Answer, "the app's answer": answered.Answer} {
		if strings.Contains(text, value) {
			t.Errorf("%s carries the value: %q", what, text)
		}
	}
	stored, err := d.srv.store.Question(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.Answer+stored.Text+stored.Context, value) {
		t.Errorf("the question's record carries the value: %+v", stored)
	}

	// Stored as the Secrets tab stores it — the project's — and written into
	// the running agent.
	secrets, err := d.client.Secrets(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 1 || secrets[0].Name != "STRIPE_SECRET_KEY" || secrets[0].Scope != "project" {
		t.Errorf("the project's secrets = %+v, want STRIPE_SECRET_KEY", secrets)
	}
	if file := agentFile(t, files, "/home/dev/.config/agentbox/secrets.env"); !strings.Contains(file, value) {
		t.Errorf("the agent's secrets file = %q, want the value in it", file)
	}

	// It is over: answering again is refused.
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Value: value}); err == nil {
		t.Error("an answered request was answered again")
	}
}

// A GitHub account the user picks becomes the project's, and the asking
// agent's token is replaced with its own.
func TestCredentialRequestForAGitHubAccount(t *testing.T) {
	d, files := secretsDaemon(t)
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveGitHubToken("", "ghp_default_token"); err != nil {
		t.Fatal(err)
	}
	if err := creds.SaveGitHubToken("personal", "ghp_personal_token"); err != nil {
		t.Fatal(err)
	}
	if err := creds.SaveGitHubLogin("personal", "octocat"); err != nil {
		t.Fatal(err)
	}

	told, id := requestInBackground(t, d, api.CredentialRequest{
		Kind: "github", Reason: "git push: Repository not found",
	})
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{GitHubAccount: "nobody"}); err == nil {
		t.Error("an account that isn't stored was accepted")
	}
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{GitHubAccount: "personal"}); err != nil {
		t.Fatal(err)
	}
	q := toldAgent(t, told)
	if !strings.Contains(q.Answer, `"personal"`) || !strings.Contains(q.Answer, "octocat") {
		t.Errorf("the agent was told %q, want the account and who it is", q.Answer)
	}
	if strings.Contains(q.Answer, "ghp_") {
		t.Errorf("the agent's answer carries the token: %q", q.Answer)
	}

	p, err := d.srv.store.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if p.GitHubAccount != "personal" {
		t.Errorf("the project's GitHub account = %q, want personal", p.GitHubAccount)
	}
	a, err := d.srv.store.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if a.GitHubAccount != "personal" {
		t.Errorf("the agent's GitHub account = %q, want personal", a.GitHubAccount)
	}
	if env := agentFile(t, files, "/home/dev/.config/agentbox/env"); !strings.Contains(env, "GH_TOKEN='ghp_personal_token'") {
		t.Errorf("the agent's env file = %q, want the personal token", env)
	}
}

// The user can say no, with a reason the agent is told.
func TestCredentialRequestRefused(t *testing.T) {
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	told, id := requestInBackground(t, d, api.CredentialRequest{Kind: "github", Reason: "gh pr create: 403"})
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{
		Refuse: true, Reason: "I'll push this one myself",
	}); err != nil {
		t.Fatal(err)
	}
	if q := toldAgent(t, told); !strings.HasPrefix(q.Answer, "refused: I'll push this one myself") {
		t.Errorf("the agent was told %q, want the refusal and why", q.Answer)
	}
	if p, _ := d.srv.store.Project(ctx, "hello-stack"); p.GitHubAccount != "" {
		t.Errorf("a refusal changed the project's account to %q", p.GitHubAccount)
	}
}

// What an agent may ask for is checked before anybody is bothered with it.
func TestCredentialRequestIsChecked(t *testing.T) {
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	a, err := d.srv.store.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]api.CredentialRequest{
		"an unknown kind":       {Kind: "password", Reason: "login"},
		"a secret with no name": {Kind: "secret", Reason: "needs a key"},
		"a secret's bad name":   {Kind: "secret", Name: "not a name", Reason: "needs a key"},
		"no reason":             {Kind: "github"},
	} {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if _, err := d.srv.requestCredentialFor(ctx, a, req); err == nil || err == context.DeadlineExceeded {
			t.Errorf("%s: requestCredentialFor() = %v, want it refused at once", name, err)
		}
		cancel()
	}
	if waiting, _ := d.srv.store.Questions(ctx, "hello-stack", true); len(waiting) != 0 {
		t.Errorf("refused requests were recorded: %+v", waiting)
	}
	// And a decision isn't answered as if it were a credential.
	q := state.Question{ID: "q-decision", Project: "hello-stack", Agent: "agent-01", Text: "paginate?",
		Status: state.QuestionPending, CreatedAt: time.Now()}
	if err := d.srv.store.AddQuestion(ctx, q); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", q.ID, api.AnswerCredentialRequest{Refuse: true}); err == nil {
		t.Error("a decision was answered as a credential request")
	}
}

// leadAnswers tries answering a question as the project's chat would.
func leadAnswers(t *testing.T, d testDaemon, id string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/project/questions/"+id+"/answer", strings.NewReader(`{"answer":"here it is"}`))
	r.SetPathValue("project", "hello-stack")
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	if err := d.srv.leadAnswerQuestion(w, r); err != nil {
		writeError(w, err)
	}
	return w.Code, w.Body.String()
}

// A request waits only while the call behind it does: a call given up on —
// interrupted, or its session gone — cancels it, so its card doesn't wait on
// the user for nothing.
func TestCredentialRequestCancelledWhenTheCallIsGone(t *testing.T) {
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	a, err := d.srv.store.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	call, hangUp := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := d.srv.requestCredentialFor(call, a, api.CredentialRequest{Kind: "github", Reason: "git push: 403"})
		done <- err
	}()
	id := waitForQuestion(t, d, "hello-stack")
	hangUp()
	if err := <-done; err == nil {
		t.Fatal("a call that was given up on was answered")
	}
	q, err := d.srv.store.Question(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if q.Status != state.QuestionCancelled || q.Answer == "" {
		t.Errorf("the request is %s (%q), want it cancelled, with why", q.Status, q.Answer)
	}
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Refuse: true}); err == nil {
		t.Error("a cancelled request was answered")
	}
}

// An agent asking again replaces what it asked before: it waits on one call
// at a time, so the earlier card would be answering nobody.
func TestCredentialRequestReplacedWhenTheAgentAsksAgain(t *testing.T) {
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	a, err := d.srv.store.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() {
		_, err := d.srv.requestCredentialFor(ctx, a, api.CredentialRequest{Kind: "github", Reason: "git push: 403"})
		first <- err
	}()
	firstID := waitForQuestion(t, d, "hello-stack")

	told := make(chan state.Question, 1)
	go func() {
		q, err := d.srv.requestCredentialFor(ctx, a, api.CredentialRequest{Kind: "github", Reason: "git push: 403, again"})
		if err != nil {
			t.Errorf("the second request: %v", err)
		}
		told <- q
	}()
	select {
	case err := <-first:
		if err == nil || !strings.Contains(err.Error(), "asked again") {
			t.Errorf("the first call was told %v, want that it was replaced", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first call still waits")
	}
	secondID := waitForQuestion(t, d, "hello-stack")
	if secondID == firstID {
		t.Fatal("the first request is still the one waiting")
	}
	if q, _ := d.srv.store.Question(ctx, firstID); q.Status != state.QuestionCancelled {
		t.Errorf("the first request is %s, want cancelled", q.Status)
	}
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", secondID, api.AnswerCredentialRequest{Refuse: true}); err != nil {
		t.Fatal(err)
	}
	if q := toldAgent(t, told); q.ID != secondID || !strings.HasPrefix(q.Answer, "refused") {
		t.Errorf("the second call was told %+v, want its refusal", q)
	}
}

// No call outlives the daemon it was made to, so a daemon starting cancels
// every request its predecessor left waiting — and leaves decisions alone.
func TestCredentialRequestsCancelledOnRestart(t *testing.T) {
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	now := time.Now()
	for _, q := range []state.Question{
		{ID: "q-left", Project: "hello-stack", Agent: "agent-01", Kind: state.QuestionGitHub, Text: "push: 403", Status: state.QuestionEscalated, CreatedAt: now},
		{ID: "q-decision", Project: "hello-stack", Agent: "agent-01", Text: "paginate?", Status: state.QuestionPending, CreatedAt: now},
	} {
		if err := d.srv.store.AddQuestion(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	d.srv.reconcile(ctx)
	if q, _ := d.srv.store.Question(ctx, "q-left"); q.Status != state.QuestionCancelled || !strings.Contains(q.Answer, "restarted") {
		t.Errorf("the request left waiting is %s (%q), want it cancelled by the restart", q.Status, q.Answer)
	}
	if q, _ := d.srv.store.Question(ctx, "q-decision"); q.Status != state.QuestionPending {
		t.Errorf("a decision is %s after the restart, want it left pending", q.Status)
	}
}
