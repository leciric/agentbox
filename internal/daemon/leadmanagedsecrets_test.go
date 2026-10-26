package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
)

// leadNotices records what a project's lead is told of credential requests,
// and whether it was woken for them.
type leadNotices struct {
	mu   sync.Mutex
	told []toldLead
}

type toldLead struct {
	text string
	act  bool
}

func recordLeadNotices(d testDaemon) *leadNotices {
	n := &leadNotices{}
	d.srv.credentialTell = func(_ context.Context, _, notice string, act bool) {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.told = append(n.told, toldLead{notice, act})
	}
	return n
}

// last waits for the lead to be told something, and returns the latest.
func (n *leadNotices) last(t *testing.T) toldLead {
	t.Helper()
	var got toldLead
	waitFor(t, "the lead to be told of the request", func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		if len(n.told) == 0 {
			return false
		}
		got = n.told[len(n.told)-1]
		return true
	})
	return got
}

// leadFulfils calls fulfil_credential's route as the project's chat would.
func leadFulfils(t *testing.T, d testDaemon, id string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/project/questions/"+id+"/fulfil", nil)
	r.SetPathValue("project", "hello-stack")
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	if err := d.srv.leadFulfilCredential(w, r); err != nil {
		writeError(w, err)
	}
	return w.Code, w.Body.String()
}

func setLeadManaged(t *testing.T, d testDaemon, name, value string) {
	t.Helper()
	managed := true
	sec, err := d.client.SetSecretWith(context.Background(), "hello-stack", name, api.SetSecretRequest{Value: value, LeadManaged: &managed})
	if err != nil {
		t.Fatal(err)
	}
	if !sec.LeadManaged {
		t.Fatalf("SetSecretWith(lead-managed) = %+v, want it lead-managed", sec)
	}
}

// A request for a secret the lead manages wakes the lead, which refreshes the
// value and fulfils the request by name: the agent gets the value stored at
// that moment, and the value is in none of what the lead is shown or sends.
func TestLeadFulfilsALeadManagedSecret(t *testing.T) {
	t.Parallel()
	d, files := secretsDaemon(t)
	ctx := context.Background()
	const stale, fresh = "azure_token_expired_value", "azure_token_fresh_value"
	setLeadManaged(t, d, "AZURE_TOKEN", stale)
	notices := recordLeadNotices(d)

	told, id := requestInBackground(t, d, api.CredentialRequest{
		Kind: "secret", Name: "AZURE_TOKEN", Reason: "az account show: the token has expired",
	})
	notice := notices.last(t)
	if !notice.act || !strings.Contains(notice.text, "managed by you") || !strings.Contains(notice.text, "fulfil_credential (id "+id+")") ||
		!strings.Contains(notice.text, "--value-stdin") {
		t.Errorf("the lead was told %+v, want it woken and told it may fulfil the request", notice)
	}

	// Never with text, even for a secret it manages.
	if code, body := leadAnswers(t, d, id); code == http.StatusOK || !strings.Contains(body, "fulfil_credential") {
		t.Errorf("the lead answering with text = %d %s, want it pointed at fulfil_credential", code, body)
	}

	// The lead's script refreshes the value; setting it again keeps the flag.
	sec, err := d.client.SetSecret(ctx, "hello-stack", "AZURE_TOKEN", fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !sec.LeadManaged {
		t.Error("setting a lead-managed secret's value again stopped it being lead-managed")
	}

	code, body := leadFulfils(t, d, id)
	if code != http.StatusOK {
		t.Fatalf("fulfil_credential = %d %s", code, body)
	}
	q := toldAgent(t, told)
	if !strings.Contains(q.Answer, "$AZURE_TOKEN") || q.AnsweredBy != "lead" {
		t.Errorf("the agent was told %q by %q, want where the secret is, by the lead", q.Answer, q.AnsweredBy)
	}
	if file := agentFile(t, files, "/home/dev/.config/agentbox/secrets.env"); !strings.Contains(file, fresh) {
		t.Error("the agent's secrets file doesn't have the refreshed value")
	}
	stored, err := d.srv.store.Question(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for what, text := range map[string]string{
		"the lead's notice": notice.text, "fulfil_credential's answer": body, "the agent's answer": q.Answer,
		"the question's record": stored.Text + stored.Answer + stored.Context,
	} {
		if strings.Contains(text, stale) || strings.Contains(text, fresh) {
			t.Errorf("%s carries the value: %q", what, text)
		}
	}

	// Whoever answers first wins: the user's card is over, and so is the lead's.
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Value: "late"}); err == nil {
		t.Error("the user answered a request the lead had fulfilled")
	}
	if code, _ := leadFulfils(t, d, id); code == http.StatusOK {
		t.Error("a fulfilled request was fulfilled again")
	}
}

// The user can still answer a lead-managed request, and when they do first,
// the lead's fulfil is refused.
func TestUserAnswersALeadManagedRequestFirst(t *testing.T) {
	t.Parallel()
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	setLeadManaged(t, d, "AZURE_TOKEN", "old")
	recordLeadNotices(d)
	told, id := requestInBackground(t, d, api.CredentialRequest{Kind: "secret", Name: "AZURE_TOKEN", Reason: "expired"})
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Value: "from-the-user"}); err != nil {
		t.Fatal(err)
	}
	if q := toldAgent(t, told); q.AnsweredBy != "user" {
		t.Errorf("answered by %q, want the user", q.AnsweredBy)
	}
	if code, body := leadFulfils(t, d, id); code == http.StatusOK || !strings.Contains(body, "already") {
		t.Errorf("fulfil after the user answered = %d %s, want it refused", code, body)
	}
	if sec, _ := d.srv.store.Secret(ctx, "hello-stack", "", "AZURE_TOKEN"); !sec.LeadManaged {
		t.Error("the user's answer stopped the secret being lead-managed")
	}
}

// fulfil_credential is for lead-managed secrets and nothing else: every other
// credential request stays the user's, and leaves the lead asleep.
func TestFulfilCredentialRefusedUnlessLeadManaged(t *testing.T) {
	t.Parallel()
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	notices := recordLeadNotices(d)

	// A secret the project doesn't have, then one it has but the lead doesn't manage.
	told, id := requestInBackground(t, d, api.CredentialRequest{Kind: "secret", Name: "STRIPE_SECRET_KEY", Reason: "checkout tests"})
	if n := notices.last(t); n.act || !strings.Contains(n.text, "Only the user can answer this") {
		t.Errorf("the lead was told %+v, want it told and not woken", n)
	}
	if code, body := leadFulfils(t, d, id); code == http.StatusOK || !strings.Contains(body, "isn't managed by you") {
		t.Errorf("fulfil for a secret the project lacks = %d %s, want it refused", code, body)
	}
	if _, err := d.client.SetSecret(ctx, "hello-stack", "STRIPE_SECRET_KEY", "sk_test"); err != nil {
		t.Fatal(err)
	}
	if code, body := leadFulfils(t, d, id); code == http.StatusOK || !strings.Contains(body, "isn't managed by you") {
		t.Errorf("fulfil for a secret the lead doesn't manage = %d %s, want it refused", code, body)
	}

	// Managed, then not any more, from the Settings toggle.
	if sec, err := d.client.SetSecretLeadManaged(ctx, "hello-stack", "STRIPE_SECRET_KEY", true); err != nil || !sec.LeadManaged {
		t.Fatalf("SetSecretLeadManaged(true) = %+v, %v", sec, err)
	}
	if sec, err := d.client.SetSecretLeadManaged(ctx, "hello-stack", "STRIPE_SECRET_KEY", false); err != nil || sec.LeadManaged {
		t.Fatalf("SetSecretLeadManaged(false) = %+v, %v", sec, err)
	}
	if code, _ := leadFulfils(t, d, id); code == http.StatusOK {
		t.Error("fulfil went through for a secret no longer lead-managed")
	}

	// None of that answered it: it is still the user's, who refuses it.
	if q, _ := d.srv.store.Question(ctx, id); !q.Waiting() {
		t.Errorf("a refused fulfil left the request %s", q.Status)
	}
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Refuse: true}); err != nil {
		t.Fatal(err)
	}
	toldAgent(t, told)

	// A GitHub account is the user's alone.
	told, gh := requestInBackground(t, d, api.CredentialRequest{Kind: "github", Reason: "gh pr create: 403"})
	if code, body := leadFulfils(t, d, gh); code == http.StatusOK || !strings.Contains(body, "only the user") {
		t.Errorf("fulfil for a GitHub account = %d %s, want it refused", code, body)
	}
	if q, _ := d.srv.store.Question(ctx, gh); !q.Waiting() {
		t.Errorf("a refused fulfil left the request %s", q.Status)
	}
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", gh, api.AnswerCredentialRequest{Refuse: true}); err != nil {
		t.Fatal(err)
	}
	toldAgent(t, told)

	// Only a project's own secrets can be lead-managed.
	managed := true
	if _, err := d.client.SetSecretWith(ctx, "hello-stack/agent-01", "AGENT_KEY", api.SetSecretRequest{Value: "v", LeadManaged: &managed}); err == nil {
		t.Error("an agent's own secret was made lead-managed")
	}
	if _, err := d.client.SetSecretLeadManaged(ctx, "hello-stack", "NOT_THERE", true); err == nil {
		t.Error("a secret the project doesn't have was made lead-managed")
	}
}
