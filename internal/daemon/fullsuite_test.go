package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"agentbox/internal/api"
)

// An agent's whole test suites are refused only while the installation
// blocks them and its project doesn't allow them; the agent's API says which,
// and the commit its branch started from.
func TestTestPolicy(t *testing.T) {
	t.Parallel()
	d, _, _ := skillsDaemon(t)
	ctx := context.Background()
	if err := d.srv.store.SetAgentBaseCommit(ctx, "hello-stack", "agent-01", "abc123"); err != nil {
		t.Fatal(err)
	}
	h := d.srv.inAgentRoutes("ab-hello-stack-agent-01")
	policy := func() api.TestPolicy {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/self/tests", nil))
		var p api.TestPolicy
		if err := json.Unmarshal(rec.Body.Bytes(), &p); rec.Code != 200 || err != nil {
			t.Fatalf("GET /v1/self/tests: %d %s", rec.Code, rec.Body)
		}
		return p
	}
	if p := policy(); p.BlockFullSuite || p.Base != "abc123" {
		t.Errorf("by default: %+v, want not blocked and base abc123", p)
	}
	s, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{BlockFullSuite: new(true)})
	if err != nil || !s.BlockFullSuite {
		t.Fatalf("turning it on: %v, %v", s.BlockFullSuite, err)
	}
	if !policy().BlockFullSuite {
		t.Error("on: not blocked")
	}
	p, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{FullTestSuite: new(true)})
	if err != nil || !p.FullTestSuite {
		t.Fatalf("allowing the project: %v, %v", p.FullTestSuite, err)
	}
	if policy().BlockFullSuite {
		t.Error("on, and the project allows them: blocked")
	}
}
