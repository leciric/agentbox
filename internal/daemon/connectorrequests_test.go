package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/connectors"
	"agentbox/internal/connectors/connectorstest"
	"agentbox/internal/state"
)

// connectorRequestInBackground asks for a connector as the agent would, and
// hands back what the agent is eventually told.
func connectorRequestInBackground(t *testing.T, d testDaemon, a state.Agent, req api.ConnectorRequest) (<-chan state.Question, string) {
	t.Helper()
	told := make(chan state.Question, 1)
	go func() {
		q, err := d.srv.requestConnectorFor(context.Background(), a, req)
		if err != nil {
			t.Errorf("requestConnectorFor: %v", err)
		}
		told <- q
	}()
	return told, waitForQuestion(t, d, a.Project)
}

// The whole of it, from inside the agent: it asks for Notion, which the
// project doesn't have; the user adds it and signs in to a server shaped like
// Notion's, the way the app's card does; that alone answers the agent, which
// uses it straight away from its shell — and asking again is answered at once.
func TestConnectorRequestEndToEnd(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	capture := filepath.Join(root, "capture")
	if err := os.MkdirAll(capture, 0o755); err != nil {
		t.Fatal(err)
	}
	d := startTestDaemon(t, root, connectorsIncus, testConfig{env: map[string]string{"CAPTURE": capture}})
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	fake := connectorstest.New()
	defer fake.Close()
	inAgent := api.NewClient(d.srv.agentSocketPath(a.Instance))

	type told struct {
		q   api.Question
		err error
	}
	answer := make(chan told, 1)
	go func() {
		q, err := inAgent.RequestConnector(ctx, api.ConnectorRequest{Name: "notion", URL: fake.MCP(), Reason: "the spec is in Notion"})
		answer <- told{q, err}
	}()
	id := waitForQuestion(t, d, "hello-stack")

	// It goes straight to the user, with what the card needs to add it.
	waiting, err := d.client.Questions(ctx, "hello-stack", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].Kind != api.QuestionConnector || waiting[0].Connector != "notion" ||
		waiting[0].ConnectorURL != fake.MCP() || waiting[0].Status != state.QuestionEscalated || waiting[0].Question != "the spec is in Notion" {
		t.Fatalf("Questions() = %+v, want one connector request waiting for the user", waiting)
	}
	raw, _ := json.Marshal(waiting[0])
	if !strings.Contains(string(raw), `"kind":"connector","connector":"notion","url":"`+fake.MCP()+`"`) {
		t.Errorf("the request as the app reads it: %s", raw)
	}

	// Nobody but the user answers it, and not with text or a secret.
	if code, body := leadAnswers(t, d, id); code == http.StatusOK || !strings.Contains(body, "only the user") {
		t.Errorf("the lead answering a connector request = %d %s, want it refused", code, body)
	}
	if _, err := d.client.AnswerQuestion(ctx, "hello-stack", id, "connected"); err == nil {
		t.Error("a connector request was answered with text")
	}
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Value: "at-123"}); err == nil {
		t.Error("a connector request was answered with a value")
	}
	// Answering with the connector before it is there leaves it waiting.
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Connector: "notion"}); err == nil ||
		!strings.Contains(err.Error(), "no connector notion yet") {
		t.Errorf("answering before the connector exists: %v", err)
	}

	// What the card does: add it, connect it, and the browser signs in.
	if _, err := d.client.SetConnector(ctx, "hello-stack", "notion", api.SetConnectorRequest{URL: fake.MCP()}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Connector: "notion"}); err == nil ||
		!strings.Contains(err.Error(), "can't be used yet") {
		t.Errorf("answering before signing in: %v", err)
	}
	res, err := d.client.ConnectConnector(ctx, "hello-stack", "notion")
	if err != nil {
		t.Fatal(err)
	}
	if code, page, err := connectorstest.SignIn(res.AuthorizationURL); err != nil || code != http.StatusOK {
		t.Fatalf("the browser: %d %v\n%s", code, err, page)
	}

	// The sign-in finishing is what answers the agent.
	var got told
	select {
	case got = <-answer:
	case <-time.After(10 * time.Second):
		t.Fatal("the agent was never told it has the connector")
	}
	if got.err != nil || got.q.Status != state.QuestionAnswered || got.q.AnsweredBy != "user" ||
		!strings.Contains(got.q.Answer, "The user connected notion") || !strings.Contains(got.q.Answer, "agentbox connector call notion") {
		t.Errorf("the agent was told %+v, %v", got.q, got.err)
	}
	// The card's own answer, right behind it, finds it done.
	if q, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Connector: "notion"}); err != nil || q.Status != state.QuestionAnswered {
		t.Errorf("the card answering after the sign-in = %+v, %v", q, err)
	}
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Refuse: true}); err == nil {
		t.Error("an answered request was refused after all")
	}

	// And the agent uses it at once, from its shell: its AI tool's session
	// started without it.
	relay := func() *connectors.Relay {
		return &connectors.Relay{HTTP: inAgent.HTTPClient(), URL: inAgent.SelfConnectorURL("notion")}
	}
	tools, err := relay().Tools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "notion-search" || !strings.Contains(string(tools[0].InputSchema), "query") {
		t.Errorf("Tools() = %+v, %v", tools, err)
	}
	result, err := relay().CallTool(ctx, "notion-search", json.RawMessage(`{"query":"onboarding spec"}`))
	if err != nil || result.IsError || result.Text() != "found: onboarding spec" {
		t.Errorf("CallTool() = %+v, %v", result, err)
	}

	// Asking again is answered straight away, with nothing put before the user.
	q, err := inAgent.RequestConnector(ctx, api.ConnectorRequest{Name: "notion", Reason: "the spec again"})
	if err != nil || q.Status != state.QuestionAnswered || !strings.Contains(q.Answer, "already connected") {
		t.Errorf("asking for a connector it has = %+v, %v", q, err)
	}
	if waiting, _ := d.client.Questions(ctx, "hello-stack", false); len(waiting) != 0 {
		t.Errorf("waiting after asking for one it has: %+v", waiting)
	}
}

// Declined, the agent is told why; and a request that doesn't make sense is
// refused before anybody sees it.
func TestConnectorRequestDeclinedAndChecked(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)

	for _, tc := range []struct {
		req  api.ConnectorRequest
		want string
	}{
		{api.ConnectorRequest{Name: "notion", URL: "https://mcp.notion.com/mcp"}, "no reason"},
		{api.ConnectorRequest{Name: "Notion!", Reason: "x"}, "invalid connector name"},
		{api.ConnectorRequest{Name: "memory", Reason: "x"}, "AgentBox gives every agent itself"},
		{api.ConnectorRequest{Name: "notion", Reason: "x"}, "give url"},
		{api.ConnectorRequest{Name: "acme", URL: "http://mcp.acme.io/mcp", Reason: "x"}, "isn't https"},
	} {
		if _, err := d.srv.requestConnectorFor(ctx, a, tc.req); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("requestConnectorFor(%+v) = %v, want %q", tc.req, err, tc.want)
		}
	}
	lead := state.Agent{Project: "hello-stack", Name: state.LeadName, Role: state.RoleLead}
	if _, err := d.srv.requestConnectorFor(ctx, lead, api.ConnectorRequest{Name: "notion", URL: "https://mcp.notion.com/mcp", Reason: "x"}); err == nil {
		t.Error("the lead asked for a connector")
	}
	if waiting, _ := d.client.Questions(ctx, "hello-stack", false); len(waiting) != 0 {
		t.Fatalf("a refused request is waiting: %+v", waiting)
	}

	told, id := connectorRequestInBackground(t, d, a, api.ConnectorRequest{Name: "linear", URL: "https://mcp.linear.app/mcp", Reason: "the bug is in Linear"})
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Connector: "notion"}); err == nil || !strings.Contains(err.Error(), "not notion") {
		t.Errorf("answering with another connector: %v", err)
	}
	if _, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Connector: "linear", Refuse: true}); err == nil {
		t.Error("an answer that both connected and refused was accepted")
	}
	q, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Refuse: true, Reason: "not this sprint"})
	if err != nil || q.Status != state.QuestionAnswered {
		t.Fatalf("declining = %+v, %v", q, err)
	}
	if got := toldAgent(t, told); !strings.Contains(got.Answer, "declined: not this sprint") {
		t.Errorf("the agent was told %q", got.Answer)
	}
	if _, err := d.srv.store.Connector(ctx, "hello-stack", "", "linear"); err == nil {
		t.Error("declining added the connector")
	}
}

// An agent made without a connector asks for it: answering turns it on and
// gives it to the agent past its limit, and nothing else of the project's.
func TestConnectorRequestGivesAConnectorPastTheLimit(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	off := false
	for _, name := range []string{"sentry", "linear"} {
		if _, err := d.client.SetConnector(ctx, "hello-stack", name, api.SetConnectorRequest{URL: "https://mcp." + name + ".dev/mcp", Auth: api.ConnectorNone, Enabled: &off}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.srv.store.SetAgentConnectors(ctx, a.Project, a.Name, []string{}); err != nil {
		t.Fatal(err)
	}
	a, _ = d.srv.store.Agent(ctx, a.Project, a.Name)
	if c, err := d.client.Connector(ctx, "hello-stack", "sentry"); err != nil || len(c.Agents) != 0 {
		t.Errorf("sentry reaches %v before, %v: want nobody, past agent-01's limit", c.Agents, err)
	}

	told, id := connectorRequestInBackground(t, d, a, api.ConnectorRequest{Name: "sentry", Reason: "the crash is in Sentry"})
	if q, _ := d.srv.store.Question(ctx, id); q.ConnectorURL != "https://mcp.sentry.dev/mcp" {
		t.Errorf("the request's URL = %q, want the project connector's", q.ConnectorURL)
	}
	q, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Connector: "sentry"})
	if err != nil || q.Status != state.QuestionAnswered {
		t.Fatalf("answering = %+v, %v", q, err)
	}
	if got := toldAgent(t, told); !strings.Contains(got.Answer, "The user connected sentry") {
		t.Errorf("the agent was told %q", got.Answer)
	}
	after, _ := d.srv.store.Agent(ctx, a.Project, a.Name)
	if strings.Join(after.Connectors, ",") != "sentry" {
		t.Errorf("agent-01's connectors = %#v, want sentry alone", after.Connectors)
	}
	if c, err := d.client.Connector(ctx, "hello-stack", "sentry"); err != nil || !c.Enabled || strings.Join(c.Agents, ",") != "hello-stack/agent-01" {
		t.Errorf("sentry = %+v, %v: want it on, and agent-01's", c, err)
	}
	if c, _ := d.client.Connector(ctx, "hello-stack", "linear"); c.Enabled || len(c.Agents) != 0 {
		t.Errorf("linear = %+v: it wasn't asked for", c)
	}
}

// An agent asks for an AgentBox-wide connector its project turned off:
// answering turns it on in that project alone, leaving the AgentBox-wide
// switch as it was.
func TestConnectorRequestForAWideConnector(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	if _, err := d.client.SetConnector(ctx, "", "sentry", api.SetConnectorRequest{URL: "https://mcp.sentry.dev/mcp", Auth: api.ConnectorNone}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetConnectorOverride(ctx, "hello-stack", "sentry", "off"); err != nil {
		t.Fatal(err)
	}
	told, id := connectorRequestInBackground(t, d, a, api.ConnectorRequest{Name: "sentry", Reason: "the crash is in Sentry"})
	if q, _ := d.srv.store.Question(ctx, id); q.ConnectorURL != "https://mcp.sentry.dev/mcp" {
		t.Errorf("the request's URL = %q, want the AgentBox-wide connector's", q.ConnectorURL)
	}
	if q, err := d.client.AnswerCredential(ctx, "hello-stack", id, api.AnswerCredentialRequest{Connector: "sentry"}); err != nil || q.Status != state.QuestionAnswered {
		t.Fatalf("answering = %+v, %v", q, err)
	}
	if got := toldAgent(t, told); !strings.Contains(got.Answer, "The user connected sentry") {
		t.Errorf("the agent was told %q", got.Answer)
	}
	if c, err := d.client.Connector(ctx, "hello-stack", "sentry"); err != nil || !c.Enabled || c.Override != "on" {
		t.Errorf("sentry in hello-stack = %+v, %v: want it on there", c, err)
	}
	if wide, _ := d.client.Connector(ctx, "", "sentry"); !wide.Enabled || len(wide.Overrides) != 1 {
		t.Errorf("sentry itself = %+v", wide)
	}
}

// A project's chat lists its project's connectors, and has them as its own
// tools through the same relay, on its own socket — only its project's, and
// no agent's own.
func TestLeadConnectors(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	addTestAgent(t, d)
	fake := connectorstest.New()
	defer fake.Close()
	if _, err := d.client.SetConnector(ctx, "hello-stack", "notion", api.SetConnectorRequest{URL: fake.MCP()}); err != nil {
		t.Fatal(err)
	}
	res, err := d.client.ConnectConnector(ctx, "hello-stack", "notion")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := connectorstest.SignIn(res.AuthorizationURL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "notion to be connected", func() bool {
		c, err := d.client.Connector(ctx, "hello-stack", "notion")
		return err == nil && c.Status == api.ConnectorConnected
	})
	if _, err := d.client.SetConnector(ctx, "hello-stack/agent-01", "linear", api.SetConnectorRequest{URL: fake.MCP()}); err != nil {
		t.Fatal(err)
	}

	if err := d.srv.serveLeadAPI("hello-stack"); err != nil {
		t.Fatal(err)
	}
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	list, err := lead.ProjectConnectors(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "notion" || list[0].Status != api.ConnectorConnected || strings.Join(list[0].Agents, ",") != "hello-stack/agent-01" {
		t.Errorf("ProjectConnectors() = %+v, %v", list, err)
	}
	mine, err := lead.SelfConnectors(ctx)
	if err != nil || len(mine) != 1 || mine[0].Name != "notion" {
		t.Errorf("the lead's SelfConnectors() = %+v, %v", mine, err)
	}
	relay := &connectors.Relay{HTTP: lead.HTTPClient(), URL: lead.SelfConnectorURL("notion")}
	if result, err := relay.CallTool(ctx, "notion-search", json.RawMessage(`{"query":"roadmap"}`)); err != nil || result.Text() != "found: roadmap" {
		t.Errorf("the lead calling notion = %+v, %v", result, err)
	}
	other := &connectors.Relay{HTTP: lead.HTTPClient(), URL: lead.SelfConnectorURL("linear")}
	if _, err := other.Tools(ctx); err == nil || !strings.Contains(err.Error(), `has no connector "linear"`) {
		t.Errorf("the lead reaching an agent's own connector: %v", err)
	}

	// create_agent's connectors must name the project's.
	_, err = lead.CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Spec", Task: "read the spec", AI: "none", Connectors: &[]string{"notion", "jira"}})
	if err == nil || !strings.Contains(err.Error(), `no connector "jira"`) || !strings.Contains(err.Error(), "notion") {
		t.Errorf("creating an agent with a connector the project lacks: %v", err)
	}
}
