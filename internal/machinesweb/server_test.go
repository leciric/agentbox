package machinesweb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/machinesmedia"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// fakeDaemon is the daemon's side: its items, and the ids opened and deleted.
type fakeDaemon struct {
	mu      sync.Mutex
	items   []Item
	files   map[string]string
	deleted []string
	err     error
	changed chan func()
}

func (d *fakeDaemon) Media(context.Context) ([]Item, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	return append([]Item(nil), d.items...), nil
}

func (d *fakeDaemon) Open(_ context.Context, id string) (io.ReadCloser, error) {
	if f, ok := d.files[id]; ok {
		return io.NopCloser(strings.NewReader(f)), nil
	}
	return nil, os.ErrNotExist
}

func (d *fakeDaemon) Delete(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deleted = append(d.deleted, id)
	return nil
}

func (d *fakeDaemon) Watch(ctx context.Context, changed func()) error {
	if d.changed != nil {
		d.changed <- changed
	}
	<-ctx.Done()
	return ctx.Err()
}

type fixture struct {
	t     *testing.T
	store machinesmedia.Store
	srv   *Server
	http  *httptest.Server
}

func newFixture(t *testing.T, d Daemon) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{t: t, store: machinesmedia.Store{Dir: filepath.Join(dir, "media")}}
	f.srv = New(f.store, filepath.Join(dir, "thumbs"), d)
	f.srv.Poll = 10 * time.Millisecond
	f.srv.Logf = t.Logf
	f.http = httptest.NewServer(f.srv.Handler())
	t.Cleanup(f.http.Close)
	return f
}

func (f *fixture) add(item machinesmedia.Item, ext string, data []byte) machinesmedia.Item {
	f.t.Helper()
	it, err := f.store.Write(item, ext, bytes.NewReader(data))
	if err != nil {
		f.t.Fatal(err)
	}
	return it
}

func (f *fixture) do(method, path string, hdr ...string) *http.Response {
	f.t.Helper()
	req, _ := http.NewRequest(method, f.http.URL+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (f *fixture) json(path string, v any) {
	f.t.Helper()
	resp := f.do("GET", path)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		f.t.Fatalf("GET %s: %d %s", path, resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		f.t.Fatal(err)
	}
}

func TestItemsFilterGroupAndPage(t *testing.T) {
	f := newFixture(t, nil)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	shot := pngBytes(t, 4, 4)
	f.add(machinesmedia.Item{Kind: "screenshot", Created: t0, Repo: "a/one", Branch: "main", Session: "s1", Tool: "claude", Caption: "Login page"}, ".png", shot)
	f.add(machinesmedia.Item{Kind: "recording", Created: t0.Add(time.Hour), Repo: "a/one", Branch: "feat", Session: "s2", Caption: "Checkout flow"}, ".mp4", []byte("v"))
	f.add(machinesmedia.Item{Kind: "screenshot", Created: t0.AddDate(0, 0, 2), Repo: "b/two", Branch: "main", Caption: "Empty state"}, ".png", shot)
	f.add(machinesmedia.Item{Kind: "screenshot", Created: t0.Add(2 * time.Hour)}, ".png", shot)
	if err := f.srv.Refresh(); err != nil {
		t.Fatal(err)
	}

	var page Page
	f.json("/api/items", &page)
	if page.Total != 4 || page.Items[0].Caption != "Empty state" || page.Items[0].Source != SourceMachines {
		t.Fatalf("all: %+v", page)
	}
	if !strings.HasPrefix(page.Items[0].ID, localPrefix) || page.Items[0].Mime != "image/png" || page.Items[0].Path == "" {
		t.Fatalf("item: %+v", page.Items[0])
	}
	if last := page.Items[3]; last.Repo != "a/one" || page.Items[1].Repo != "(no repository)" || page.Items[1].Branch != "(no branch)" {
		t.Fatalf("names: %+v", page.Items)
	}

	for _, tc := range []struct {
		query string
		want  []string // captions, in order
		total int
	}{
		{"q=login", []string{"Login page"}, 1},
		{"q=FLOW+checkout", []string{"Checkout flow"}, 1},
		{"q=feat", []string{"Checkout flow"}, 1}, // the branch is searched too
		{"kind=recording", []string{"Checkout flow"}, 1},
		{"repo=a/one", []string{"Checkout flow", "Login page"}, 2},
		{"repo=a/one&branch=main", []string{"Login page"}, 1},
		{"repo=a/one&branch=feat&session=s2", []string{"Checkout flow"}, 1},
		{"repo=(no+repository)", []string{""}, 1},
		{"from=2026-10-03", []string{"Empty state"}, 1},
		{"to=2026-10-01", []string{"", "Checkout flow", "Login page"}, 3},
		{"limit=2&offset=1", []string{"", "Checkout flow"}, 4},
		{"offset=99", nil, 4},
		{"q=nothing-matches", nil, 0},
		// By repository: groups ordered by their newest item, then newest first.
		{"group=repo", []string{"Empty state", "", "Checkout flow", "Login page"}, 4},
	} {
		var p Page
		f.json("/api/items?"+tc.query, &p)
		var got []string
		for _, it := range p.Items {
			got = append(got, it.Caption)
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") || p.Total != tc.total {
			t.Errorf("%s: got %q (total %d), want %q (total %d)", tc.query, got, p.Total, tc.want, tc.total)
		}
	}
}

func TestFacets(t *testing.T) {
	f := newFixture(t, nil)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	f.add(machinesmedia.Item{Kind: "screenshot", Created: t0, Repo: "a/one", Branch: "main", Session: "old", Tool: "codex"}, ".png", nil)
	f.add(machinesmedia.Item{Kind: "screenshot", Created: t0.Add(time.Hour), Repo: "a/one", Branch: "main", Session: "new", Tool: "claude"}, ".png", nil)
	f.add(machinesmedia.Item{Kind: "recording", Created: t0, Repo: "a/one", Branch: "dev", Worktree: "/w/dev"}, ".mp4", nil)
	f.add(machinesmedia.Item{Kind: "screenshot", Created: t0, Repo: "b/two", Branch: "main"}, ".png", nil)
	_ = f.srv.Refresh()

	var fc Facets
	// The tree's own filters don't narrow it; the others do.
	f.json("/api/facets?repo=b/two", &fc)
	if fc.Total != 4 || fc.Kinds["screenshot"] != 3 || fc.Kinds["recording"] != 1 || fc.Sources[SourceMachines] != 4 {
		t.Fatalf("facets: %+v", fc)
	}
	if len(fc.Repos) != 2 || fc.Repos[0].Repo != "a/one" || fc.Repos[0].Count != 3 {
		t.Fatalf("repos: %+v", fc.Repos)
	}
	br := fc.Repos[0].Branches
	if len(br) != 2 || br[0].Branch != "dev" || br[0].Worktree != "/w/dev" || br[1].Branch != "main" || br[1].Count != 2 {
		t.Fatalf("branches: %+v", br)
	}
	if s := br[1].Sessions; len(s) != 2 || s[0].Session != "new" || s[0].Label != "claude · new" || s[1].Session != "old" {
		t.Fatalf("sessions, newest first: %+v", s)
	}
	raw, _ := io.ReadAll(f.do("GET", "/api/items?q=nothing").Body)
	if !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("no match is an empty list, not null: %s", raw)
	}
	raw, _ = io.ReadAll(f.do("GET", "/api/facets?q=nothing").Body)
	if !strings.Contains(string(raw), `"repos":[]`) {
		t.Fatalf("no match is an empty tree, not null: %s", raw)
	}
	f.json("/api/facets?kind=recording", &fc)
	if fc.Total != 1 || len(fc.Repos) != 1 {
		t.Fatalf("kind narrows: %+v", fc)
	}
}

func TestFileRangeAndDownload(t *testing.T) {
	f := newFixture(t, nil)
	it := f.add(machinesmedia.Item{Kind: "recording", Repo: "a/one", Branch: "feat/x", Created: time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)}, ".mp4", []byte("0123456789"))
	_ = f.srv.Refresh()
	id := localPrefix + it.ID

	resp := f.do("GET", "/api/items/"+id+"/file", "Range", "bytes=2-5")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusPartialContent || string(b) != "2345" || resp.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("range: %d %q %v", resp.StatusCode, b, resp.Header)
	}
	resp = f.do("GET", "/api/items/"+id+"/file?download=1")
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename=a-one_feat-x_20261001-120000.mp4` {
		t.Fatalf("disposition: %q", cd)
	}
	if resp := f.do("GET", "/api/items/m-nope/file"); resp.StatusCode != 404 {
		t.Fatalf("unknown: %d", resp.StatusCode)
	}
	var got Item
	f.json("/api/items/"+id, &got)
	if got.ID != id || got.Bytes != 10 {
		t.Fatalf("item: %+v", got)
	}
}

func TestThumbnailMadeOnceAndCached(t *testing.T) {
	f := newFixture(t, nil)
	it := f.add(machinesmedia.Item{Kind: "screenshot"}, ".png", pngBytes(t, 1600, 1000))
	small := f.add(machinesmedia.Item{Kind: "screenshot"}, ".png", pngBytes(t, 40, 20))
	broken := f.add(machinesmedia.Item{Kind: "screenshot"}, ".png", []byte("not a png"))
	_ = f.srv.Refresh()

	resp := f.do("GET", "/api/items/"+localPrefix+it.ID+"/thumb")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("thumb: %d %v", resp.StatusCode, resp.Header)
	}
	img, err := jpeg.Decode(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != thumbWidth || b.Dy() != 400 {
		t.Fatalf("thumb is %v", b)
	}
	path := f.srv.thumbPath(localPrefix + it.ID)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Asked again, it's the cached file, not made anew.
	old := fi.ModTime().Add(-time.Hour)
	_ = os.Chtimes(path, old, old)
	f.do("GET", "/api/items/"+localPrefix+it.ID+"/thumb")
	if fi, _ := os.Stat(path); !fi.ModTime().Equal(old) {
		t.Fatal("thumbnail was made again")
	}

	resp = f.do("GET", "/api/items/"+localPrefix+small.ID+"/thumb")
	if img, err := jpeg.Decode(resp.Body); err != nil || img.Bounds().Dx() != 40 {
		t.Fatalf("a small image isn't scaled up: %v", err)
	}
	if resp := f.do("GET", "/api/items/"+localPrefix+broken.ID+"/thumb"); resp.StatusCode != 404 {
		t.Fatalf("undecodable: %d", resp.StatusCode)
	}
	if _, err := os.Stat(f.srv.thumbPath(localPrefix+broken.ID) + ".none"); err != nil {
		t.Fatal("a thumbnail that can't be made isn't remembered")
	}
}

func TestDeleteNeedsHeaderAndRemoves(t *testing.T) {
	f := newFixture(t, nil)
	it := f.add(machinesmedia.Item{Kind: "screenshot"}, ".png", pngBytes(t, 8, 8))
	_ = f.srv.Refresh()
	id := localPrefix + it.ID
	f.do("GET", "/api/items/"+id+"/thumb")

	if resp := f.do("DELETE", "/api/items/"+id); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("delete without X-Machines: %d", resp.StatusCode)
	}
	if resp := f.do("DELETE", "/api/items/"+id, "X-Machines", "1"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := f.store.Get(it.ID); err == nil {
		t.Fatal("still in the store")
	}
	if _, err := os.Stat(f.srv.thumbPath(id)); err == nil {
		t.Fatal("thumbnail left behind")
	}
	var page Page
	f.json("/api/items", &page)
	if page.Total != 0 {
		t.Fatalf("still listed: %+v", page)
	}
}

func TestGuardRefusesForeignHost(t *testing.T) {
	f := newFixture(t, nil)
	for host, want := range map[string]int{
		"evil.example:7790":         403,
		"127.0.0.1:7790":            200,
		"localhost:7790":            200,
		"[::1]:7790":                200,
		"7790.x.agentbox.localhost": 200,
	} {
		req, _ := http.NewRequest("GET", f.http.URL+"/api/items", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Host %s: %d, want %d", host, resp.StatusCode, want)
		}
	}
}

func TestPageIsEmbedded(t *testing.T) {
	f := newFixture(t, nil)
	for path, want := range map[string]string{"/": "Machines media", "/app.js": "EventSource", "/style.css": "prefers-color-scheme"} {
		b, _ := io.ReadAll(f.do("GET", path).Body)
		if !strings.Contains(string(b), want) {
			t.Errorf("%s doesn't contain %q", path, want)
		}
	}
}

func TestDaemonMediaMergedAndFollowed(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	d := &fakeDaemon{
		items:   []Item{{ID: agentPrefix + "x1", Source: SourceAgentBox, Kind: "screenshot", Created: t0.Add(time.Hour), Repo: "pawly", Branch: "agentbox/feat", Session: "agent-01", Caption: "agent shot", Mime: "image/png", Bytes: 5}},
		files:   map[string]string{"x1": "hello"},
		changed: make(chan func(), 1),
	}
	f := newFixture(t, d)
	f.add(machinesmedia.Item{Kind: "screenshot", Created: t0, Caption: "mine"}, ".png", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.srv.Run(ctx)
	changed := <-d.changed // following the daemon now

	var page Page
	waitFor(t, func() bool { f.json("/api/items", &page); return page.Total == 2 })
	if page.Items[0].Caption != "agent shot" || page.Items[1].Caption != "mine" {
		t.Fatalf("merged: %+v", page.Items)
	}
	// Not on this machine: streamed through the daemon.
	b, _ := io.ReadAll(f.do("GET", "/api/items/a-x1/file").Body)
	if string(b) != "hello" {
		t.Fatalf("file through the daemon: %q", b)
	}

	// The page hears about a new agent item as it happens.
	events := f.do("GET", "/api/events")
	r := bufio.NewReader(events.Body)
	if line, _ := r.ReadString('\n'); line != "event: hello\n" {
		t.Fatalf("first event: %q", line)
	}
	d.mu.Lock()
	d.items = append(d.items, Item{ID: agentPrefix + "x2", Source: SourceAgentBox, Kind: "recording", Created: t0.Add(2 * time.Hour)})
	d.mu.Unlock()
	changed()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "event: change\n" {
			break
		}
	}
	f.json("/api/items?source=agentbox", &page)
	if page.Total != 2 {
		t.Fatalf("after the event: %+v", page)
	}

	if resp := f.do("DELETE", "/api/items/a-x1", "X-Machines", "1"); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if len(d.deleted) != 1 || d.deleted[0] != "x1" {
		t.Fatalf("deleted through the daemon: %v", d.deleted)
	}
	f.json("/api/items?source=agentbox", &page)
	if page.Total != 1 {
		t.Fatalf("after delete: %+v", page)
	}
}

func TestDaemonDownIsQuiet(t *testing.T) {
	d := &fakeDaemon{err: errors.New("connection refused")}
	f := newFixture(t, d)
	f.add(machinesmedia.Item{Kind: "screenshot"}, ".png", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.srv.Run(ctx)
	var page Page
	waitFor(t, func() bool { f.json("/api/items", &page); return page.Total == 1 })
}

func TestStoreWatchedLive(t *testing.T) {
	f := newFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.srv.Run(ctx)
	var page Page
	f.json("/api/items", &page)
	if page.Total != 0 {
		t.Fatalf("start: %+v", page)
	}
	// The directory doesn't exist yet: the first item makes it.
	it := f.add(machinesmedia.Item{Kind: "screenshot", Caption: "new"}, ".png", nil)
	waitFor(t, func() bool { f.json("/api/items", &page); return page.Total == 1 })
	_ = f.store.Delete(it.ID)
	waitFor(t, func() bool { f.json("/api/items", &page); return page.Total == 0 })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}
