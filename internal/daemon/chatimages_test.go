package daemon

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// A picture sent to the project's chat is kept beside it, not in state.db, and
// the app reads it back from the chat's images route. It goes with the chat.
func TestChatImagesAreKeptAndServed(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	// No AI tool starts in a test: the turn fails, the message stands.
	d.srv.chat.Launch = func(context.Context, state.Agent, func(string)) (*chat.Process, error) {
		return nil, errors.New("this test starts no AI tool")
	}

	// The smallest GIF there is.
	gif, _ := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==")
	item, err := d.client.SendChat(ctx, "hello-stack", "", api.ChatImageUpload{MimeType: "image/gif", Name: "dot.gif", Data: base64.StdEncoding.EncodeToString(gif)})
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Images) != 1 {
		t.Fatalf("the message's images = %+v", item.Images)
	}

	get := func(path string) (int, string, []byte) {
		req, _ := http.NewRequest(http.MethodGet, "http://agentbox"+path, nil)
		resp, err := d.client.HTTPClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), body
	}
	code, typ, body := get("/v1/projects/hello-stack/chat/images/" + item.Images[0].ID)
	if code != http.StatusOK || typ != "image/gif" || string(body) != string(gif) {
		t.Errorf("GET the image = %d %s, %d bytes", code, typ, len(body))
	}
	if code, _, _ := get("/v1/projects/hello-stack/chat/images/0123456789abcdef"); code != http.StatusNotFound {
		t.Errorf("GET an image that was never sent = %d, want 404", code)
	}
	if code, _, _ := get("/v1/projects/hello-stack/chat/images/..%2Fstate.db"); code != http.StatusNotFound {
		t.Errorf("GET a path for an image = %d, want 404", code)
	}

	dir := d.paths.ChatImages("hello-stack", state.LeadName)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the image isn't kept under %s: %v", dir, err)
	}
	if err := d.client.ClearChat(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("clearing the chat left its images (stat: %v)", err)
	}
}
