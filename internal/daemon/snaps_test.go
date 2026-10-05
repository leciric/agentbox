package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// A SnapShot is held for the app, announced as an event, served as a
// picture, and sent to the project's chat as a bug report with the picture
// attached; then it is gone.
func TestSnapIsHeldAnnouncedAndSentAsABugReport(t *testing.T) {
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

	events := make(chan api.Snap, 1)
	evCtx, stop := context.WithCancel(ctx)
	defer stop()
	ready := make(chan struct{})
	go func() {
		once := false
		_ = d.client.Events(evCtx, func(ev api.Event) error {
			if !once {
				once = true
				close(ready)
			}
			if ev.Type == api.EventSnap {
				var s api.Snap
				_ = json.Unmarshal(ev.Data, &s)
				events <- s
			}
			return nil
		})
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		// The stream sends nothing until something happens: go on, the
		// event is checked with its own timeout.
	}

	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	taken := time.Date(2026, 10, 5, 14, 3, 0, 0, time.UTC)
	held, err := d.client.TakeSnap(ctx, api.SnapRequest{
		Image: api.ChatImageUpload{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(png)},
		App:   "firefox", Title: "Settings", Desktop: "hyprland", Window: true,
		Accessibility: `push button "Save"`, TakenAt: taken,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.ID != held.ID || ev.App != "firefox" {
			t.Errorf("the event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Error("no snap event")
	}
	if list, _ := d.client.Snaps(ctx); len(list) != 1 || list[0].ID != held.ID {
		t.Errorf("Snaps = %+v", list)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://agentbox/v1/snaps/"+held.ID+"/image", nil)
	resp, err := d.client.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || string(body) != string(png) {
		t.Errorf("GET the picture = %d %s, %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	}

	item, err := d.client.SendSnap(ctx, held.ID, api.SnapSendRequest{Project: "hello-stack", Note: "Save does nothing.", Accessibility: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Images) != 1 || !strings.Contains(item.Text, "Save does nothing.") || !strings.Contains(item.Text, `push button "Save"`) || !strings.Contains(item.Text, `firefox, "Settings"`) {
		t.Errorf("the message = %q with %d images", item.Text, len(item.Images))
	}
	if _, err := d.client.SendSnap(ctx, held.ID, api.SnapSendRequest{Project: "hello-stack"}); err == nil {
		t.Error("a SnapShot was sent twice")
	}

	if _, err := d.client.TakeSnap(ctx, api.SnapRequest{Image: api.ChatImageUpload{MimeType: "image/gif", Data: "R0lG"}}); err == nil {
		t.Error("a GIF was taken as a SnapShot")
	}
	other, _ := d.client.TakeSnap(ctx, api.SnapRequest{Image: api.ChatImageUpload{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(png)}})
	if err := d.client.DropSnap(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.client.Snaps(ctx); len(list) != 0 {
		t.Errorf("dropped, still held: %+v", list)
	}
}

// The store keeps the newest few and forgets the stale.
func TestSnapStoreBounds(t *testing.T) {
	t.Parallel()
	var st snapStore
	for i := range api.MaxSnaps + 2 {
		st.add(heldSnap{Snap: api.Snap{ID: string(rune('a' + i))}})
	}
	list := st.list()
	if len(list) != api.MaxSnaps || list[0].ID != "c" {
		t.Errorf("held %d, oldest %q", len(list), list[0].ID)
	}
	st.snaps = append(st.snaps, heldSnap{Snap: api.Snap{ID: "old"}, received: time.Now().Add(-2 * api.SnapTTL)})
	if _, err := st.get("old"); err == nil {
		t.Error("a stale SnapShot is still held")
	}
}

// A cookie import is previewed by site, kept to the domains picked, and
// reported without a single cookie; removing it forgets it.
func TestBrowserCookiesImport(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	export := ".github.com\tTRUE\t/\tTRUE\t4102444800\t_octo\tvalue-one\n" +
		"#HttpOnly_github.com\tFALSE\t/\tTRUE\t4102444800\tuser_session\tvalue-two\n" +
		".google.com\tTRUE\t/\tTRUE\t4102444800\tNID\tvalue-three\n"

	if info, err := d.client.BrowserCookies(ctx, "hello-stack"); err != nil || info.Imported {
		t.Fatalf("before an import: %+v, %v", info, err)
	}
	preview, err := d.client.PreviewBrowserCookies(ctx, "hello-stack", export)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Format != "netscape" || preview.Cookies != 3 || len(preview.Domains) != 2 || preview.Domains[0] != (api.CookieDomain{Domain: "github.com", Cookies: 2}) {
		t.Errorf("preview = %+v", preview)
	}
	if _, err := d.client.ImportBrowserCookies(ctx, "hello-stack", export, nil); err == nil {
		t.Error("an import with no domains picked was stored")
	}
	info, err := d.client.ImportBrowserCookies(ctx, "hello-stack", export, []string{"https://github.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Imported || info.Cookies != 2 || len(info.Domains) != 1 || info.Domains[0] != "github.com" || info.ImportedAt == nil {
		t.Errorf("import = %+v", info)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "value-") {
		t.Errorf("the answer carries a cookie: %s", raw)
	}
	if secrets, _ := d.client.Secrets(ctx, "hello-stack"); len(secrets) != 0 {
		t.Errorf("the import shows as a secret: %+v", secrets)
	}
	if err := d.client.RemoveBrowserCookies(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if info, _ := d.client.BrowserCookies(ctx, "hello-stack"); info.Imported {
		t.Errorf("removed, still imported: %+v", info)
	}
}
