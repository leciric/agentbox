package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

func TestMediaNotesFromTheAgentAndTheUser(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	inAgent := api.NewClient(d.srv.agentSocketPath(a.Instance))

	fromAgent, err := inAgent.AddNote(ctx, "", api.NoteRequest{Text: "Implemented the list.\nTested create and delete."})
	if err != nil {
		t.Fatal(err)
	}
	if fromAgent.Source != "agent" || fromAgent.Kind != "note" || fromAgent.Name != "Implemented the list." || fromAgent.Path != "" {
		t.Errorf("note from the agent = %+v", fromAgent)
	}
	fromUser, err := d.client.AddNote(ctx, a.Ref(), api.NoteRequest{Text: "Looks good", Name: "Review"})
	if err != nil {
		t.Fatal(err)
	}
	if fromUser.Source != "user" || fromUser.Name != "Review" {
		t.Errorf("note from the user = %+v", fromUser)
	}

	items, err := d.client.Media(ctx, a.Ref())
	if err != nil || len(items) != 2 || items[0].ID != fromUser.ID {
		t.Fatalf("Media() = %+v, %v; want both notes, newest first", items, err)
	}
	if own, err := inAgent.Media(ctx, ""); err != nil || len(own) != 2 {
		t.Errorf("the agent's own media = %d items, %v", len(own), err)
	}

	if err := inAgent.DeleteMedia(ctx, fromAgent.ID); err == nil || !strings.Contains(err.Error(), "not available inside an agent") {
		t.Errorf("an agent deleting media: got %v", err)
	}
	if _, err := inAgent.ExportMedia(ctx, "", api.ExportRequest{}); err == nil {
		t.Error("an agent exported its media")
	}
	if _, err := d.client.AddNote(ctx, a.Ref(), api.NoteRequest{Text: "   "}); err == nil {
		t.Error("an empty note was accepted")
	}

	if err := d.client.DeleteMedia(ctx, fromAgent.ID); err != nil {
		t.Fatal(err)
	}
	if items, _ := d.client.Media(ctx, a.Ref()); len(items) != 1 {
		t.Errorf("after deleting one note, %d items are left", len(items))
	}
}

func TestMediaFilesAreServedWithRanges(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)

	dir := filepath.Join(d.srv.manager(nil).MediaDir(a.Project, a.Name), "item")
	if err := os.MkdirAll(filepath.Join(dir, "report", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"clip.mp4":          "hello video",
		"report/index.html": "<h1>12 passed</h1>",
		"report/data/a.js":  "ok",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []state.Media{
		{ID: "clip", Kind: "recording", Name: "clip", File: "item/clip.mp4", Mime: "video/mp4", Meta: "{}"},
		{ID: "report", Kind: "report", Name: "report", File: "item/report", Meta: `{"entry":"index.html"}`},
	} {
		item.Project, item.Agent, item.Source, item.CreatedAt = a.Project, a.Name, "agent", time.Now()
		if err := d.srv.store.AddMedia(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	get := func(path, rangeHeader string) (int, string) {
		req, err := http.NewRequest(http.MethodGet, "http://agentbox"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		resp, err := d.client.HTTPClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if code, body := get("/v1/media/clip/file", "bytes=0-4"); code != http.StatusPartialContent || body != "hello" {
		t.Errorf("a range of the video = %d %q, want 206 \"hello\"", code, body)
	}
	if code, body := get("/v1/media/report/file", ""); code != http.StatusOK || !strings.Contains(body, "12 passed") {
		t.Errorf("the report's entry = %d %q", code, body)
	}
	if code, body := get("/v1/media/report/file?path=data/a.js", ""); code != http.StatusOK || body != "ok" {
		t.Errorf("a file inside the report = %d %q", code, body)
	}
	if code, _ := get("/v1/media/report/file?path=../clip.mp4", ""); code != http.StatusBadRequest {
		t.Errorf("a path outside the report answered %d, want 400", code)
	}
}

// Once an agent is destroyed with its media kept, the project's media view
// must still say which agent it came from and that the agent is gone, not
// render it blank or broken.
func TestProjectMediaShowsAnItemWhoseAgentIsGone(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.store.AddMedia(ctx, state.Media{
		ID: "item-1", Project: a.Project, Agent: a.Name, Kind: "note", Name: "note", Source: "agent", Meta: "{}", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	live, err := d.client.ProjectMedia(ctx, a.Project, "", "")
	if err != nil || len(live) != 1 || live[0].AgentGone || live[0].ExpiresAt != nil {
		t.Fatalf("ProjectMedia() for a live agent = %+v, %v; want not gone, no expiry", live, err)
	}

	// What Destroy(DeleteMedia: false) does: start the retention clock, then
	// remove the agent record. Exercised at the store level here so this test
	// only depends on the projectMedia handler's own behavior.
	if err := d.srv.store.OrphanAgentMedia(ctx, a.Project, a.Name, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.RemoveAgent(ctx, a.Project, a.Name); err != nil {
		t.Fatal(err)
	}

	gone, err := d.client.ProjectMedia(ctx, a.Project, "", "")
	if err != nil || len(gone) != 1 {
		t.Fatalf("ProjectMedia() once the agent is gone = %+v, %v", gone, err)
	}
	got := gone[0]
	if got.AgentName != a.Name {
		t.Errorf("AgentName = %q, want %q: still findable by name", got.AgentName, a.Name)
	}
	if !got.AgentGone {
		t.Error("AgentGone = false, want true: its agent was destroyed")
	}
	if got.ExpiresAt == nil {
		t.Error("ExpiresAt = nil, want the retention deadline")
	}
}

// The sweeper removes expired media, row and file together, and leaves media
// whose agent still exists or whose retention period hasn't passed yet — with
// the clock faked, rather than waiting on a real 30 days. Each item gets its
// own agent name so OrphanAgentMedia, which is scoped by agent, can put each
// on its own clock.
func TestSweepExpiredMediaRemovesRowsAndFiles(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	now := time.Now()

	addItem := func(id, agent string) {
		dir := filepath.Join(d.srv.manager(nil).MediaDir(a.Project, agent), id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("proof"), 0o600); err != nil {
			t.Fatal(err)
		}
		item := state.Media{ID: id, Project: a.Project, Agent: agent, Kind: "file", Name: id, File: id + "/note.txt", Source: "agent", Meta: "{}", CreatedAt: now}
		if err := d.srv.store.AddMedia(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	itemDir := func(id, agent string) string { return filepath.Join(d.srv.manager(nil).MediaDir(a.Project, agent), id) }

	addItem("expired", "agent-expired") // orphaned 2 days ago: past the 1-day default
	if err := d.srv.store.OrphanAgentMedia(ctx, a.Project, "agent-expired", now.Add(-2*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	addItem("live", "agent-live") // never orphaned: its agent still exists
	addItem("fresh", "agent-fresh")
	if err := d.srv.store.OrphanAgentMedia(ctx, a.Project, "agent-fresh", now); err != nil { // orphaned just now
		t.Fatal(err)
	}

	d.srv.sweepExpiredMedia(ctx, now)

	if _, err := d.srv.store.MediaItem(ctx, "expired"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("expired item's row = %v, want gone", err)
	}
	if _, err := os.Stat(itemDir("expired", "agent-expired")); !os.IsNotExist(err) {
		t.Errorf("expired item's file = %v, want gone", err)
	}
	for _, kept := range []struct{ id, agent string }{{"live", "agent-live"}, {"fresh", "agent-fresh"}} {
		if _, err := d.srv.store.MediaItem(ctx, kept.id); err != nil {
			t.Errorf("%s's row = %v, want it to survive the sweep", kept.id, err)
		}
		if _, err := os.Stat(itemDir(kept.id, kept.agent)); err != nil {
			t.Errorf("%s's file = %v, want it to survive the sweep", kept.id, err)
		}
	}
}

// addTestMedia writes an item's file and its row, so a delete has both to
// take away and a real size to report as freed.
func addTestMedia(t *testing.T, d testDaemon, project, agent, id, kind string, size int) state.Media {
	t.Helper()
	dir := filepath.Join(d.srv.manager(nil).MediaDir(project, agent), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proof.txt"), bytes.Repeat([]byte("x"), size), 0o600); err != nil {
		t.Fatal(err)
	}
	item := state.Media{ID: id, Project: project, Agent: agent, Kind: kind, Name: id, File: id + "/proof.txt",
		Size: int64(size), Source: "agent", Meta: "{}", CreatedAt: time.Now()}
	if err := d.srv.store.AddMedia(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	return item
}

// addSecondTestAgent puts another agent in the first one's project, so filters
// and scopes have something to tell apart.
func addSecondTestAgent(t *testing.T, d testDaemon, project string) state.Agent {
	t.Helper()
	a := state.Agent{
		Project: project, Name: "agent-02", Instance: "ab-" + project + "-agent-02", AI: "none",
		Branch: "agentbox/agent-02", Worktree: "/worktrees/agent-02", Status: state.AgentReady, CreatedAt: time.Now(),
	}
	if err := d.srv.store.AddAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

func mediaIsGone(t *testing.T, d testDaemon, item state.Media) {
	t.Helper()
	if _, err := d.srv.store.MediaItem(context.Background(), item.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("%s's row = %v, want gone", item.ID, err)
	}
	dir := filepath.Join(d.srv.manager(nil).MediaDir(item.Project, item.Agent), item.ID)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("%s's files = %v, want gone", item.ID, err)
	}
}

// Deleting by ID takes the rows and the files, says how many bytes that freed,
// and announces each item as removed so open galleries drop it. An ID that is
// already gone is not an error; one from another agent, on an agent's own
// route, is refused rather than quietly taken.
func TestBulkDeleteMediaByIDs(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	other := addSecondTestAgent(t, d, a.Project)
	one := addTestMedia(t, d, a.Project, a.Name, "shot-1", "screenshot", 100)
	two := addTestMedia(t, d, a.Project, a.Name, "log-1", "log", 20)
	keep := addTestMedia(t, d, a.Project, a.Name, "shot-2", "screenshot", 7)
	theirs := addTestMedia(t, d, other.Project, other.Name, "shot-3", "screenshot", 5)

	events := make(chan api.Event, 64)
	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	go func() {
		_ = d.client.Events(eventsCtx, func(ev api.Event) error {
			events <- ev
			return nil
		})
	}()
	waitFor(t, "an event subscriber", func() bool { return d.srv.events.subscribers() > 0 })

	got, err := d.client.DeleteAgentMedia(ctx, a.Ref(), api.DeleteMediaRequest{IDs: []string{one.ID, two.ID, "never-existed"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Deleted != 2 || got.Bytes != 120 {
		t.Errorf("DeleteAgentMedia() = %+v, want 2 items and 120 bytes freed", got)
	}
	mediaIsGone(t, d, one)
	mediaIsGone(t, d, two)
	if _, err := d.srv.store.MediaItem(ctx, keep.ID); err != nil {
		t.Errorf("%s = %v, want it kept: it wasn't named", keep.ID, err)
	}

	var removed []string
	for timeout := time.After(500 * time.Millisecond); ; {
		select {
		case ev := <-events:
			var item api.MediaItem
			if ev.Type == api.EventMedia && json.Unmarshal(ev.Data, &item) == nil && item.Removed {
				removed = append(removed, item.ID)
			}
			continue
		case <-timeout:
		}
		break
	}
	slices.Sort(removed)
	if !slices.Equal(removed, []string{"log-1", "shot-1"}) {
		t.Errorf("removal events = %v, want one for each deleted item", removed)
	}

	if _, err := d.client.DeleteAgentMedia(ctx, a.Ref(), api.DeleteMediaRequest{IDs: []string{theirs.ID}}); err == nil || !strings.Contains(err.Error(), "belongs to") {
		t.Errorf("deleting %s's item through %s: got %v", other.Ref(), a.Ref(), err)
	}
	if _, err := d.srv.store.MediaItem(ctx, theirs.ID); err != nil {
		t.Errorf("%s's item = %v, want it untouched", other.Ref(), err)
	}
	if _, err := d.client.DeleteProjectMedia(ctx, a.Project, api.DeleteMediaRequest{}); err == nil {
		t.Error("a delete naming neither items nor all was accepted")
	}
	if _, err := d.client.DeleteProjectMedia(ctx, a.Project, api.DeleteMediaRequest{All: true, IDs: []string{keep.ID}}); err == nil {
		t.Error("a delete naming both items and all was accepted")
	}

	// Agents add to their own media; they never delete, in bulk or otherwise.
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	inAgent := api.NewClient(d.srv.agentSocketPath(a.Instance))
	if _, err := inAgent.DeleteAgentMedia(ctx, "", api.DeleteMediaRequest{All: true}); err == nil || !strings.Contains(err.Error(), "not available inside an agent") {
		t.Errorf("an agent deleting its media in bulk: got %v", err)
	}
}

// Deleting all obeys the same agent and kind filters as the project's list, so
// what goes is exactly what the gallery was showing.
func TestBulkDeleteProjectMediaByFilter(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	other := addSecondTestAgent(t, d, a.Project)
	shotOne := addTestMedia(t, d, a.Project, a.Name, "shot-a1", "screenshot", 10)
	logOne := addTestMedia(t, d, a.Project, a.Name, "log-a1", "log", 20)
	shotTwo := addTestMedia(t, d, other.Project, other.Name, "shot-a2", "screenshot", 30)
	logTwo := addTestMedia(t, d, other.Project, other.Name, "log-a2", "log", 40)

	got, err := d.client.DeleteProjectMedia(ctx, a.Project, api.DeleteMediaRequest{All: true, Kind: "screenshot"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Deleted != 2 || got.Bytes != 40 {
		t.Errorf("deleting all screenshots = %+v, want both agents' 2 items and 40 bytes", got)
	}
	mediaIsGone(t, d, shotOne)
	mediaIsGone(t, d, shotTwo)
	left, err := d.client.ProjectMedia(ctx, a.Project, "", "")
	if err != nil || len(left) != 2 {
		t.Fatalf("what's left = %+v, %v; want both logs", left, err)
	}

	// Its agent is destroyed first, and its media kept: the project's route
	// never asks the agents table, so media outliving its agent — the media
	// most worth clearing out — can still be emptied by that agent's name.
	if err := d.srv.store.RemoveAgent(ctx, other.Project, other.Name); err != nil {
		t.Fatal(err)
	}
	got, err = d.client.DeleteProjectMedia(ctx, a.Project, api.DeleteMediaRequest{All: true, Agent: other.Name})
	if err != nil {
		t.Fatal(err)
	}
	if got.Deleted != 1 || got.Bytes != 40 {
		t.Errorf("deleting all of %s = %+v, want its 1 item and 40 bytes", other.Ref(), got)
	}
	mediaIsGone(t, d, logTwo)
	if _, err := d.srv.store.MediaItem(ctx, logOne.ID); err != nil {
		t.Errorf("%s = %v, want it kept: it belongs to another agent", logOne.ID, err)
	}

	if _, err := d.client.DeleteProjectMedia(ctx, "no-such-project", api.DeleteMediaRequest{All: true}); err == nil {
		t.Error("deleting all of a project that doesn't exist was accepted")
	}
}
