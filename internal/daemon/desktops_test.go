package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"agentbox/internal/api"
)

// A project none of whose agents has used its desktop answers an empty
// list, not null: the lead's chat reads it as no desktops in use.
func TestAProjectWithNoDesktopsInUseListsNone(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	if _, err := d.client.AddProject(context.Background(), api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://agentbox/v1/projects/hello-stack/desktops", nil)
	resp, err := d.client.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var got []api.DesktopUse
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &got) != nil || got == nil || len(got) != 0 {
		t.Fatalf("GET desktops = %d %s, want 200 []", resp.StatusCode, body)
	}
}
