package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// podSelf makes a request the way a pod does: with its token and with no
// Tailscale login header, which a pod has none of.
func podSelf(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// A pod's token names the agent, and the in-agent API is served on the
// gateway's listener, which is where a pod reaches the daemon: GET /v1/self
// answers that agent, with no Tailscale header to say so.
func TestAPodsTokenReachesTheSelfAPI(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	addTestAgent(t, d)
	if err := d.srv.store.SetAgentToken(ctx, testGatewayRef.Project, testGatewayRef.Agent, testGatewayToken, time.Time{}); err != nil {
		t.Fatal(err)
	}

	w := podSelf(d.srv.gatewayListenerHandler(true), http.MethodGet, "/v1/self", testGatewayToken)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/self with the pod's token = %d %s, want 200", w.Code, strings.TrimSpace(w.Body.String()))
	}
	var self api.Self
	if err := json.Unmarshal(w.Body.Bytes(), &self); err != nil {
		t.Fatal(err)
	}
	if want := testGatewayRef.Project + "/" + testGatewayRef.Agent; self.Ref != want {
		t.Errorf("Self.Ref = %q, want %q: the token names the agent", self.Ref, want)
	}
}

// A request with no token, or with one this daemon never issued, is refused
// with 401 by the in-agent API itself: the gateway's listener has no Tailscale
// login check to fall through to.
func TestTheSelfAPIRefusesWhatItCannotAuthenticate(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	addTestAgent(t, d)
	h := d.srv.gatewayListenerHandler(true)

	for _, c := range []struct {
		what, token, want string
	}{
		{"no token", "", "this request needs the agent's token"},
		{"a token the store doesn't know", testGatewayToken, "the agent's token isn't one this daemon issued"},
	} {
		w := podSelf(h, http.MethodGet, "/v1/self", c.token)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET /v1/self with %s = %d, want 401", c.what, w.Code)
			continue
		}
		if body := w.Body.String(); !strings.Contains(body, c.want) {
			t.Errorf("GET /v1/self with %s = %q, want it to say %q", c.what, body, c.want)
		}
	}
}

// On the tailnet listener a pod's token reaches the in-agent API, and exactly
// it: a pod has no login header for the check that guards everything else, so
// anything but /v1/self still has to come through Tailscale.
func TestTheTailnetServesOnlyTheSelfAPIToAPodsToken(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	addTestAgent(t, d)
	if err := d.srv.store.SetAgentToken(ctx, testGatewayRef.Project, testGatewayRef.Agent, testGatewayToken, time.Time{}); err != nil {
		t.Fatal(err)
	}
	h := d.srv.tailnetHandler()

	if w := podSelf(h, http.MethodGet, "/v1/self", testGatewayToken); w.Code != http.StatusOK {
		t.Errorf("GET /v1/self with the pod's token and no login = %d %s, want 200", w.Code, strings.TrimSpace(w.Body.String()))
	}
	// Refused by the token's check, not by the login one: the bypass is the
	// self API's, and a pod without a token is still nobody.
	w := podSelf(h, http.MethodGet, "/v1/self", "")
	if body := w.Body.String(); w.Code != http.StatusUnauthorized || !strings.Contains(body, "this request needs the agent's token") {
		t.Errorf("GET /v1/self with no token = %d %q, want the agent API's 401", w.Code, body)
	}
	if w := podSelf(h, http.MethodGet, "/v1/projects", testGatewayToken); w.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/projects with the pod's token and no login = %d, want 401", w.Code)
	}
}

// The agent the token names is the one a route acts on: its own base commit,
// and its own connectors, which are none.
func TestTheSelfAPIActsOnTheAgentItsTokenNames(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	addTestAgent(t, d)
	// A second agent of the project, and the token is its: the routes must
	// answer it, not whichever agent the test made first.
	other := state.Agent{
		Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02", AI: "none",
		Branch: "agentbox/agent-02", Worktree: "/worktrees/agent-02", Status: state.AgentReady, CreatedAt: time.Now(),
	}
	if err := d.srv.store.AddAgent(ctx, other); err != nil {
		t.Fatal(err)
	}
	const base = "0123456789abcdef0123456789abcdef01234567"
	if err := d.srv.store.SetAgentBaseCommit(ctx, other.Project, other.Name, base); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.SetAgentToken(ctx, other.Project, other.Name, testGatewayToken, time.Time{}); err != nil {
		t.Fatal(err)
	}
	h := d.srv.gatewayListenerHandler(true)

	w := podSelf(h, http.MethodGet, "/v1/self/tests", testGatewayToken)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/self/tests = %d %s, want 200", w.Code, strings.TrimSpace(w.Body.String()))
	}
	var policy api.TestPolicy
	if err := json.Unmarshal(w.Body.Bytes(), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Base != base {
		t.Errorf("TestPolicy.Base = %q, want %q: the token's agent is the one asked", policy.Base, base)
	}

	w = podSelf(h, http.MethodGet, "/v1/self/connectors", testGatewayToken)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("GET /v1/self/connectors = %d %s, want 200 []", w.Code, strings.TrimSpace(w.Body.String()))
	}
}

// The gateway's listener holds a pod's two ways in and nothing else: the
// gateway's route, the in-agent API, and a 404 for the rest of the daemon.
func TestTheGatewayListenerServesPodsAndNothingElse(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	addTestAgent(t, d)
	h := d.srv.gatewayListenerHandler(true)

	if w := podSelf(h, http.MethodGet, "/v1/version", ""); w.Code != http.StatusNotFound {
		t.Errorf("GET /v1/version = %d, want 404: the rest of the daemon isn't a pod's", w.Code)
	}
	if w := podSelf(h, http.MethodGet, "/v1/agents/hello-stack/agent-01", ""); w.Code != http.StatusNotFound {
		t.Errorf("GET /v1/agents/hello-stack/agent-01 = %d, want 404", w.Code)
	}
}
