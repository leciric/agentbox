package daemon

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
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
		defer func() { _ = resp.Body.Close() }()
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

// A file sent to the project's chat is put in the lead's worktree, where git
// ignores it, and the app reads it back from the chat's files route as bytes
// to save, never a page to render.
func TestChatFilesAreKeptAndServed(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	d.srv.chat.Launch = func(context.Context, state.Agent, func(string)) (*chat.Process, error) {
		return nil, errors.New("this test starts no AI tool")
	}
	page := []byte("<script>alert(1)</script>")
	item, err := d.client.SendChatMessage(ctx, "hello-stack", api.ChatMessageRequest{Text: "look", Files: []api.ChatFileUpload{{Name: "page.html", MimeType: "text/html", Data: base64.StdEncoding.EncodeToString(page)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Files) != 1 || item.Files[0].Name != "page.html" {
		t.Fatalf("the message's files = %+v", item.Files)
	}
	f := item.Files[0]
	if want := d.paths.Worktree("hello-stack", state.LeadName); !strings.HasPrefix(f.Path, want+"/.agentbox/attachments/") {
		t.Errorf("the file is at %s, not in the lead's worktree %s", f.Path, want)
	}
	if b, err := os.ReadFile(f.Path); err != nil || string(b) != string(page) {
		t.Errorf("the lead's copy = %q, %v", b, err)
	}
	status, err := exec.Command("git", "-C", d.paths.Worktree("hello-stack", state.LeadName), "status", "--porcelain").Output()
	if err != nil || len(status) != 0 {
		t.Errorf("git status in the lead's worktree = %q, %v, want nothing to commit", status, err)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://agentbox/v1/projects/hello-stack/chat/files/"+f.ID, nil)
	resp, err := d.client.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/octet-stream" || resp.Header.Get("Content-Disposition") != "attachment" || string(body) != string(page) {
		t.Errorf("GET the file = %d %s %q, %q", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"), body)
	}
}
