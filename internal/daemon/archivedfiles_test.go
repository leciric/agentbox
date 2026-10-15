package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// A past agent's files are read from its branch in the project's repository,
// and once the branch is gone the routes say so with a 404.
func TestArchivedFileRoutes(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "HEAD"))
	head := strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	testutil.Git(t, repo, "checkout", "-q", "-b", "agentbox/agent-07")
	readme, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), append(readme, "past\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "added.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "-A")
	testutil.Git(t, repo, "commit", "-q", "-m", "past work")
	testutil.Git(t, repo, "checkout", "-q", head)

	a := state.Agent{
		Project: "hello-stack", Name: "agent-07", Instance: "ab-hello-stack-agent-07", AI: "claude",
		Branch: "agentbox/agent-07", BaseRef: head, BaseCommit: base, Worktree: "/gone/agent-07",
		Status: state.AgentReady, CreatedAt: time.Now(), Interface: state.InterfaceChat,
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if a, err = d.srv.store.Agent(ctx, a.Project, a.Name); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.ArchiveAgent(ctx, a, "", state.ArchivedPR{}, time.Now()); err != nil {
		t.Fatal(err)
	}

	get := func(rest string, out any) (int, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		d.srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/archived-agents/hello-stack/agent-07/files"+rest, nil))
		if rec.Code == http.StatusOK && out != nil {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
		return rec.Code, rec.Body.String()
	}

	var files api.WorktreeFiles
	if code, body := get("", &files); code != http.StatusOK || !slices.Contains(files.Files, "added.txt") {
		t.Errorf("files = %d %s", code, body)
	}
	var list api.FileList
	if code, body := get("/list?path=&ignored=1", &list); code != http.StatusOK || !slices.ContainsFunc(list.Entries, func(e api.FileEntry) bool { return e.Name == "added.txt" }) {
		t.Errorf("list = %d %s", code, body)
	}
	var st api.FileStatus
	if code, body := get("/status", &st); code != http.StatusOK || st.Base != head || st.Changes["README.md"] != api.FileModified || st.Changes["added.txt"] != api.FileAdded || len(st.Changes) != 2 {
		t.Errorf("status = %d %s", code, body)
	}
	var c api.FileContent
	if code, body := get("/read?path=README.md&base=1", &c); code != http.StatusOK || c.Status != api.FileModified || c.BaseText != string(readme) || !strings.HasSuffix(c.Text, "past\n") || c.Base != head {
		t.Errorf("read = %d %s", code, body)
	}
	if code, _ := get("/read?path=nope.txt", nil); code != http.StatusNotFound {
		t.Errorf("read(missing) = %d, want 404", code)
	}
	if code, _ := get("/read?path=../../etc/passwd", nil); code != http.StatusBadRequest {
		t.Errorf("read(outside) = %d, want 400", code)
	}

	testutil.Git(t, repo, "branch", "-q", "-D", "agentbox/agent-07")
	if code, body := get("/status", nil); code != http.StatusNotFound || !strings.Contains(body, "branch agentbox/agent-07 is gone") {
		t.Errorf("status after the branch went = %d %s, want a 404 saying so", code, body)
	}
}
