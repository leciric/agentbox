package credentials_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/credentials"
)

// fakeAnthropic stands in for /api/oauth/profile: it answers for the tokens it
// was given and refuses every other one, in Anthropic's own wording.
type fakeAnthropic struct {
	calls atomic.Int64
	url   string
}

func anthropic(t *testing.T, accepts ...string) *fakeAnthropic {
	t.Helper()
	fake := &fakeAnthropic{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.calls.Add(1)
		if r.URL.Path != "/api/oauth/profile" {
			t.Errorf("the check asked for %s, want /api/oauth/profile", r.URL.Path)
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			t.Errorf("the check sent Authorization %q", r.Header.Get("Authorization"))
		}
		for _, good := range accepts {
			if token == good {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"account":{"email_address":"someone@example.com"}}`))
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"OAuth access token is invalid."}}`))
	}))
	t.Cleanup(srv.Close)
	fake.url = srv.URL
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	return fake
}

// sidecar is the raw <account>.json beside a token.
func sidecar(t *testing.T, s credentials.Store, account string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.ClaudeDir(), account+".json"))
	if err != nil {
		t.Fatalf("reading %s's sidecar: %v", account, err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s's sidecar isn't JSON: %v", account, err)
	}
	return out
}

func TestSavingATokenRecordsTheDate(t *testing.T) {
	s := store(t)
	before := time.Now()
	if err := s.SaveClaudeToken("work", "sk-ant-oat01-work"); err != nil {
		t.Fatal(err)
	}

	// The sidecar sits beside the token, is as private as it, and doesn't
	// disturb the file layout the accounts are listed from.
	info, err := os.Stat(filepath.Join(s.ClaudeDir(), "work.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the sidecar's mode = %o, want 600", perm)
	}
	if issued, ok := sidecar(t, s, "work")["issued"].(string); !ok || issued == "" {
		t.Errorf("the sidecar has no issued date: %v", sidecar(t, s, "work"))
	}

	accounts, err := s.ClaudeAccounts()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("ClaudeAccounts() = %+v, %v", accounts, err)
	}
	if !accounts[0].SavedAtKnown || accounts[0].SavedAt.Before(before) || accounts[0].SavedAt.After(time.Now()) {
		t.Errorf("SavedAt = %v (known %v), want between %v and now", accounts[0].SavedAt, accounts[0].SavedAtKnown, before)
	}
}

func TestATokenStoredBeforeTheSidecarHasNoDate(t *testing.T) {
	s := store(t)
	if err := os.MkdirAll(s.ClaudeDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.ClaudeTokenPath("default"), []byte("sk-ant-oat01-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	accounts, err := s.ClaudeAccounts()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("ClaudeAccounts() = %+v, %v", accounts, err)
	}
	if accounts[0].SavedAtKnown || !accounts[0].SavedAt.IsZero() {
		t.Errorf("an older token reports SavedAt = %v (known %v), want unknown", accounts[0].SavedAt, accounts[0].SavedAtKnown)
	}
	// Reading the accounts gave it a sidecar that says so outright, rather
	// than inventing a date from the token file's own timestamp.
	if issued, ok := sidecar(t, s, "default")["issued"]; !ok || issued != nil {
		t.Errorf(`the migrated sidecar has issued = %v, want null`, issued)
	}
	// The token itself is untouched.
	if token, _ := s.ClaudeToken(""); token != "sk-ant-oat01-old" {
		t.Errorf("ClaudeToken() = %q", token)
	}
}

// The single-account layout had no date either, so a token that comes through
// that migration is undated too, and the migration still works.
func TestTheMigratedSingleTokenIsUndated(t *testing.T) {
	s := store(t)
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "claude-oauth-token"), []byte("sk-ant-oat01-legacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	accounts, err := s.ClaudeAccounts()
	if err != nil || len(accounts) != 1 || accounts[0].Name != "default" || !accounts[0].Default {
		t.Fatalf("ClaudeAccounts() after the migration = %+v, %v", accounts, err)
	}
	if accounts[0].SavedAtKnown {
		t.Errorf("the migrated token reports a date (%v); it can't be known", accounts[0].SavedAt)
	}
	if token, _ := s.ClaudeToken(""); token != "sk-ant-oat01-legacy" {
		t.Errorf("ClaudeToken() after the migration = %q", token)
	}
	if issued, ok := sidecar(t, s, "default")["issued"]; !ok || issued != nil {
		t.Errorf("the migrated sidecar has issued = %v, want null", issued)
	}
}

func TestCheckingATokenAnthropicTakes(t *testing.T) {
	api := anthropic(t, "sk-ant-oat01-good")
	s := store(t)
	if err := s.SaveClaudeToken("", "sk-ant-oat01-good"); err != nil {
		t.Fatal(err)
	}

	v, err := s.CheckClaudeAccount(context.Background(), "")
	if err != nil || v.State != credentials.TokenValid {
		t.Fatalf("CheckClaudeAccount() = %+v, %v", v, err)
	}
	// The answer is written down, so listing the accounts says so without
	// asking again, and so does another process reading the same directory.
	accounts, _ := s.ClaudeAccounts()
	if len(accounts) != 1 || accounts[0].Valid != credentials.TokenValid {
		t.Errorf("ClaudeAccounts() = %+v", accounts)
	}
	if v := s.ClaudeValidity(""); v.State != credentials.TokenValid || v.Stale() {
		t.Errorf("ClaudeValidity() = %+v", v)
	}

	// An answer stands for an hour: asking again costs no call at all.
	if _, err := s.CheckClaudeAccount(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	if calls := api.calls.Load(); calls != 1 {
		t.Errorf("Anthropic was asked %d times, want 1", calls)
	}
}

func TestCheckingATokenAnthropicRefuses(t *testing.T) {
	anthropic(t, "sk-ant-oat01-good")
	s := store(t)
	if err := s.SaveClaudeToken("work", "sk-ant-oat01-revoked"); err != nil {
		t.Fatal(err)
	}

	v, err := s.CheckClaudeAccount(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != credentials.TokenRejected {
		t.Fatalf("CheckClaudeAccount() = %+v", v)
	}
	// Anthropic's own wording, not AgentBox's guess at it.
	if !strings.Contains(v.Detail, "OAuth access token is invalid") {
		t.Errorf("the refusal says %q", v.Detail)
	}

	// Storing a new token forgets the refusal: this is a different token, and
	// nothing has asked about it yet.
	if err := s.SaveClaudeToken("work", "sk-ant-oat01-good"); err != nil {
		t.Fatal(err)
	}
	if v := s.ClaudeValidity("work"); v.State != credentials.TokenUnknown || !v.Stale() {
		t.Errorf("after storing a new token, ClaudeValidity() = %+v", v)
	}
	if v, err := s.CheckClaudeAccount(context.Background(), "work"); err != nil || v.State != credentials.TokenValid {
		t.Errorf("the new token checks out as %+v, %v", v, err)
	}
}

// An outage says nothing about the token: an offline machine must not read as
// a revoked login, and the answer must not stand for a whole hour either.
func TestATokenNobodyCouldCheckIsNotRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"error","error":{"message":"Overloaded"}}`, http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)

	s := store(t)
	if err := s.SaveClaudeToken("", "sk-ant-oat01-fine"); err != nil {
		t.Fatal(err)
	}
	v, err := s.CheckClaudeAccount(context.Background(), "")
	if err != nil || v.State != credentials.TokenUnknown {
		t.Fatalf("CheckClaudeAccount() during an outage = %+v, %v", v, err)
	}
	if !strings.Contains(v.Detail, "503") {
		t.Errorf("the answer says %q, which doesn't name what happened", v.Detail)
	}
	// The date the token was stored survives a check that learned nothing.
	if accounts, _ := s.ClaudeAccounts(); len(accounts) != 1 || !accounts[0].SavedAtKnown {
		t.Errorf("ClaudeAccounts() after the check = %+v", accounts)
	}
}

// An agent whose turn was refused reports it, and that counts as an answer:
// waiting for the next check would leave the app saying the token is fine.
func TestRejectingAnAccountOutright(t *testing.T) {
	s := store(t)
	for _, account := range []string{"personal", "work"} {
		if err := s.SaveClaudeToken(account, "sk-ant-oat01-"+account); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RejectClaudeAccount("", "Invalid API key · Please run /login"); err != nil {
		t.Fatal(err)
	}

	// "" is the default account, which is the first one stored.
	if v := s.ClaudeValidity("personal"); v.State != credentials.TokenRejected || v.Stale() {
		t.Errorf("the default account is %+v", v)
	}
	if v := s.ClaudeValidity("work"); v.State != credentials.TokenUnknown {
		t.Errorf("the other account was marked too: %+v", v)
	}
	accounts, _ := s.ClaudeAccounts()
	if len(accounts) != 2 || accounts[0].Valid != credentials.TokenRejected || !accounts[0].SavedAtKnown {
		t.Errorf("ClaudeAccounts() = %+v", accounts)
	}
}

func TestRemovingAnAccountRemovesItsSidecar(t *testing.T) {
	s := store(t)
	if err := s.SaveClaudeToken("work", "sk-ant-oat01-work"); err != nil {
		t.Fatal(err)
	}
	if err := s.RejectClaudeAccount("work", "revoked"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveClaudeAccount("work"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.ClaudeDir(), "work.json")); !os.IsNotExist(err) {
		t.Errorf("the sidecar outlived the account: %v", err)
	}

	// An account stored again under the same name starts clean.
	if err := s.SaveClaudeToken("work", "sk-ant-oat01-new"); err != nil {
		t.Fatal(err)
	}
	if v := s.ClaudeValidity("work"); v.State != credentials.TokenUnknown {
		t.Errorf("the new account inherited %+v", v)
	}
}

func TestCheckingAnAccountThatIsntStored(t *testing.T) {
	s := store(t)
	if v, err := s.CheckClaudeAccount(context.Background(), ""); err != nil || v.State != credentials.TokenUnknown {
		t.Errorf("CheckClaudeAccount() with nothing stored = %+v, %v", v, err)
	}
	if v := s.ClaudeValidity(""); v.State != credentials.TokenUnknown {
		t.Errorf("ClaudeValidity() with nothing stored = %+v", v)
	}
}

// TestASetupTokenIsValidThoughItCantReadTheProfile: a token from `claude
// setup-token` may run inference and nothing else, so the profile answers it
// with a scope refusal. That refusal means Anthropic recognised the token; a
// login marked rejected for it was every setup-token login, every hour. The
// bodies are the ones Anthropic sent when probed live.
func TestASetupTokenIsValidThoughItCantReadTheProfile(t *testing.T) {
	for _, c := range []struct {
		what   string
		status int
		body   string
		want   credentials.TokenState
	}{
		{"a setup-token login, which lacks the profile scope", http.StatusForbidden,
			`{"type":"error","error":{"type":"permission_error","message":"OAuth token does not meet scope requirement any_of(user:profile, user:office)","details":{"required_scopes":["user:profile","user:office"],"match":"any","error_visibility":"user_facing","error_code":"oauth_scope_insufficient"}}}`,
			credentials.TokenValid},
		{"a token Anthropic doesn't know", http.StatusUnauthorized,
			`{"type":"error","error":{"type":"authentication_error","message":"OAuth access token is invalid."},"request_id":null}`,
			credentials.TokenRejected},
		{"any other refusal", http.StatusForbidden,
			`{"type":"error","error":{"type":"permission_error","message":"This organization has been disabled."}}`,
			credentials.TokenRejected},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		}))
		t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
		if got := credentials.CheckClaudeToken(context.Background(), "sk-ant-oat01-x"); got.State != c.want {
			t.Errorf("%s: state %q (%s), want %q", c.what, got.State, got.Detail, c.want)
		}
		srv.Close()
	}
}
