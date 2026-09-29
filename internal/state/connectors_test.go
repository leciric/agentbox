package state_test

import (
	"context"
	"errors"
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
