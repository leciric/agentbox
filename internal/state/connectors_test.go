package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentbox/internal/state"
)

// An agent is given its project's connectors and its own, its own replacing
// its project's of the same name; its own go when it does.
func TestAgentConnectors(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	for _, c := range []state.Connector{
		{Project: "pawly", Name: "notion", URL: "https://mcp.notion.com/mcp", Auth: "oauth", Enabled: true, AccessToken: []byte("sealed"), ExpiresAt: now.Add(time.Hour), UpdatedAt: now},
		{Project: "pawly", Name: "linear", URL: "https://mcp.linear.app/mcp", Auth: "oauth", UpdatedAt: now},
		{Project: "pawly", Agent: "agent-01", Name: "notion", URL: "https://mcp.notion.com/mcp", Auth: "secret", Secret: "NOTION_TOKEN", Enabled: true, UpdatedAt: now},
		{Project: "other", Name: "figma", URL: "https://mcp.figma.com/mcp", Auth: "secret", UpdatedAt: now},
	} {
		if err := st.SetConnector(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.AgentConnectors(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "linear" || got[1].Name != "notion" || got[1].Agent != "agent-01" || got[1].Scope() != state.ScopeAgent {
		t.Errorf("agent-01's connectors = %+v", got)
	}
	got, _ = st.AgentConnectors(ctx, "pawly", "agent-02")
	if len(got) != 2 || got[1].Agent != "" || string(got[1].AccessToken) != "sealed" || !got[1].ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("agent-02's connectors = %+v", got)
	}

	if err := st.RemoveAgent(ctx, "pawly", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Connector(ctx, "pawly", "agent-01", "notion"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("an agent's connector outlived it: %v", err)
	}
	if err := st.RemoveConnector(ctx, "pawly", "", "linear"); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveConnector(ctx, "pawly", "", "linear"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("removing it twice: %v", err)
	}
	if err := st.RemoveProjectConnectors(ctx, "pawly"); err != nil {
		t.Fatal(err)
	}
	all, _ := st.AllConnectors(ctx)
	if len(all) != 1 || all[0].Project != "other" {
		t.Errorf("left after removing pawly's = %+v", all)
	}
}

// An agent made with a limit is given only the project connectors it names,

// An AgentBox-wide connector reaches every project, as its switch or the
// project's override says, and a project's own of the same name replaces it.
func TestWideConnectors(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	for _, c := range []state.Connector{
		{Name: "notion", URL: "https://mcp.notion.com/mcp", Auth: "oauth", Enabled: true, UpdatedAt: now},
		{Name: "linear", URL: "https://mcp.linear.app/mcp", Auth: "oauth", Enabled: false, UpdatedAt: now},
		{Project: "pawly", Name: "linear", URL: "https://linear.example/mcp", Auth: "none", Enabled: true, UpdatedAt: now},
		{Project: "pawly", Agent: "agent-01", Name: "notion", URL: "https://notion.example/mcp", Auth: "none", Enabled: true, UpdatedAt: now},
	} {
		if err := st.SetConnector(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	names := func(found []state.Connector) string {
		var out []string
		for _, c := range found {
			on := "off"
			if c.Enabled {
				on = "on"
			}
			out = append(out, c.Name+":"+c.Scope()+":"+on)
		}
		return strings.Join(out, " ")
	}
	got, _ := st.ProjectConnectors(ctx, "pawly")
	if want := "linear:project:on notion:agentbox:on"; names(got) != want {
		t.Errorf("pawly gets %s, want %s", names(got), want)
	}
	got, _ = st.ProjectConnectors(ctx, "other")
	if want := "linear:agentbox:off notion:agentbox:on"; names(got) != want {
		t.Errorf("other gets %s, want %s", names(got), want)
	}
	got, _ = st.AgentConnectors(ctx, "pawly", "agent-01")
	if want := "linear:project:on notion:agent:on"; names(got) != want {
		t.Errorf("agent-01 gets %s, want %s", names(got), want)
	}

	on, off := true, false
	if err := st.SetConnectorOverride(ctx, "linear", "other", &on); err != nil {
		t.Fatal(err)
	}
	if err := st.SetConnectorOverride(ctx, "notion", "other", &off); err != nil {
		t.Fatal(err)
	}
	if err := st.SetConnectorOverride(ctx, "figma", "other", &on); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("overriding a connector that isn't AgentBox-wide: %v", err)
	}
	got, _ = st.AgentConnectors(ctx, "other", "agent-02")
	if want := "linear:agentbox:on notion:agentbox:off"; names(got) != want {
		t.Errorf("other/agent-02 gets %s, want %s", names(got), want)
	}
	c, err := st.ProjectConnector(ctx, "other", "notion")
	if err != nil || c.Enabled || !c.Wide() {
		t.Errorf("other's notion = %+v, %v", c, err)
	}
	wide, _ := st.Connector(ctx, "", "", "notion")
	if !wide.Enabled || len(wide.Overrides) != 1 || wide.Overrides["other"] {
		t.Errorf("notion itself = %+v", wide)
	}
	if err := st.SetConnectorOverride(ctx, "notion", "other", nil); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.ProjectConnector(ctx, "other", "notion"); !c.Enabled {
		t.Error("notion didn't follow AgentBox-wide again")
	}

	if err := st.RemoveProjectConnectors(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.Connector(ctx, "", "", "linear"); len(c.Overrides) != 0 {
		t.Errorf("a removed project's override stayed: %+v", c.Overrides)
	}
	if err := st.RemoveConnector(ctx, "", "", "notion"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ProjectConnectors(ctx, "pawly"); names(got) != "linear:project:on" {
		t.Errorf("after removing notion, pawly gets %s", names(got))
	}
}

// and always its own; nil is every one, and empty is none.
func TestAgentConnectorLimit(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []state.Connector{
		{Project: "pawly", Name: "notion", URL: "https://mcp.notion.com/mcp", Auth: "oauth", Enabled: true, UpdatedAt: now},
		{Project: "pawly", Name: "linear", URL: "https://mcp.linear.app/mcp", Auth: "oauth", Enabled: true, UpdatedAt: now},
		{Project: "pawly", Agent: "agent-02", Name: "sentry", URL: "https://mcp.sentry.dev/mcp", Auth: "none", Enabled: true, UpdatedAt: now},
	} {
		if err := st.SetConnector(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	names := func(agent string) string {
		t.Helper()
		got, err := st.AgentConnectors(ctx, "pawly", agent)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range got {
			out = append(out, c.Name)
		}
		return strings.Join(out, ",")
	}
	for _, a := range []state.Agent{
		{Project: "pawly", Name: "agent-01", Instance: "ab-pawly-agent-01", CreatedAt: now},
		{Project: "pawly", Name: "agent-02", Instance: "ab-pawly-agent-02", CreatedAt: now, Connectors: []string{"notion"}},
		{Project: "pawly", Name: "agent-03", Instance: "ab-pawly-agent-03", CreatedAt: now, Connectors: []string{}},
	} {
		if err := st.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if got := names("agent-01"); got != "linear,notion" {
		t.Errorf("no limit: %s", got)
	}
	if got := names("agent-02"); got != "notion,sentry" {
		t.Errorf("limited to notion, with its own sentry: %s", got)
	}
	if got := names("agent-03"); got != "" {
		t.Errorf("limited to none: %s", got)
	}
	a, err := st.Agent(ctx, "pawly", "agent-03")
	if err != nil || a.Connectors == nil || len(a.Connectors) != 0 {
		t.Errorf("agent-03's limit read back as %#v, %v: want empty, not nil", a.Connectors, err)
	}

	if err := st.SetAgentConnectors(ctx, "pawly", "agent-03", []string{"linear"}); err != nil {
		t.Fatal(err)
	}
	if got := names("agent-03"); got != "linear" {
		t.Errorf("after granting linear: %s", got)
	}
	if err := st.SetAgentConnectors(ctx, "pawly", "agent-03", nil); err != nil {
		t.Fatal(err)
	}
	if got := names("agent-03"); got != "linear,notion" {
		t.Errorf("after lifting the limit: %s", got)
	}
	if err := st.SetAgentConnectors(ctx, "pawly", "agent-09", nil); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("an agent that doesn't exist: %v", err)
	}
}

// A connector request keeps what it asks for.
func TestConnectorRequests(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	for _, q := range []state.Question{
		{ID: "q1", Project: "pawly", Agent: "agent-01", Kind: state.QuestionConnector, Connector: "notion",
			ConnectorURL: "https://mcp.notion.com/mcp", Text: "the spec is in Notion", Status: state.QuestionEscalated, CreatedAt: time.Now()},
		{ID: "q2", Project: "pawly", Agent: "agent-01", Kind: state.QuestionSecret, SecretName: "KEY", Text: "x", Status: state.QuestionEscalated, CreatedAt: time.Now()},
		{ID: "q3", Project: "pawly", Agent: "agent-02", Text: "a decision", Status: state.QuestionPending, CreatedAt: time.Now()},
	} {
		if err := st.AddQuestion(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	waiting, err := st.WaitingConnectorRequests(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].Connector != "notion" || waiting[0].ConnectorURL != "https://mcp.notion.com/mcp" || !waiting[0].Credential() {
		t.Errorf("WaitingConnectorRequests() = %+v", waiting)
	}
	if _, err := st.AnswerQuestion(ctx, "q1", "connected", "user"); err != nil {
		t.Fatal(err)
	}
	if waiting, _ := st.WaitingConnectorRequests(ctx, "pawly"); len(waiting) != 0 {
		t.Errorf("an answered request is still waiting: %+v", waiting)
	}
}
