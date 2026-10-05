// Package machinesweb is `agentbox machines serve`: a web page on 127.0.0.1
// to browse every screenshot and recording on this machine, from the central
// store of `agentbox machines mcp` (package machinesmedia) and from
// AgentBox's own agents, through the daemon when it is reachable.
//
// The page is plain HTML, CSS and JavaScript embedded in the binary (static/),
// so there's nothing to build or deploy. It asks the server for a page of
// items at a time (GET /api/items), filtered and grouped on the server, and
// for the sidebar's tree of repositories, branches and sessions (GET
// /api/facets); thumbnails are made once and kept in a cache directory, and
// an event stream (GET /api/events) tells the page when items change.
package machinesweb

import (
	"cmp"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"agentbox/internal/machinesmedia"
)

//go:embed static
var static embed.FS

// Server serves the page and its API.
type Server struct {
	store    machinesmedia.Store
	thumbDir string
	daemon   Daemon // nil when there is none to ask

	// Poll is how often the store's directory is checked for changes, and
	// DaemonRefresh how often the daemon's media is reread even with no
	// event saying it changed (an agent's branch or title may have).
	Poll          time.Duration
	DaemonRefresh time.Duration
	Logf          func(format string, args ...any)

	mu      sync.RWMutex
	entries map[string]machinesmedia.Entry // the store's, for ListCached
	local   []Item
	agents  []Item
	all     []Item // local and agents, newest first
	byID    map[string]Item
	version int64
	subs    map[chan int64]struct{}

	thumbs singleflight.Group
	sem    chan struct{} // bounds thumbnails made at once
}

// New makes a server over a store, keeping thumbnails in thumbDir. daemon
// may be nil.
func New(store machinesmedia.Store, thumbDir string, daemon Daemon) *Server {
	return &Server{
		store: store, thumbDir: thumbDir, daemon: daemon,
		Poll: 500 * time.Millisecond, DaemonRefresh: time.Minute, Logf: log.Printf,
		byID: map[string]Item{}, subs: map[chan int64]struct{}{},
		sem: make(chan struct{}, 4),
	}
}

// Run reads both sources, then keeps following them until ctx ends.
func (s *Server) Run(ctx context.Context) {
	_ = s.Refresh()
	go s.watchStore(ctx)
	if s.daemon != nil {
		go s.followDaemon(ctx)
	}
	<-ctx.Done()
}

// Refresh rereads the store.
func (s *Server) Refresh() error {
	s.mu.RLock()
	prev := s.entries
	s.mu.RUnlock()
	entries, err := s.store.ListCached(prev)
	if err != nil {
		return err
	}
	byID := make(map[string]machinesmedia.Entry, len(entries))
	items := make([]Item, len(entries))
	for i, e := range entries {
		byID[e.ID] = e
		items[i] = fromEntry(e).named()
	}
	s.mu.Lock()
	changed := !slices.Equal(items, s.local)
	s.entries, s.local = byID, items
	if changed {
		s.rebuildLocked()
	}
	s.mu.Unlock()
	return nil
}

func (s *Server) setAgents(items []Item) {
	for i := range items {
		items[i] = items[i].named()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Equal(items, s.agents) {
		return
	}
	s.agents = items
	s.rebuildLocked()
}

// rebuildLocked merges the two sources and tells the pages watching.
func (s *Server) rebuildLocked() {
	all := make([]Item, 0, len(s.local)+len(s.agents))
	all = append(append(all, s.local...), s.agents...)
	sortNewest(all)
	s.all = all
	s.byID = make(map[string]Item, len(all))
	for _, it := range all {
		s.byID[it.ID] = it
	}
	s.version++
	for ch := range s.subs {
		select {
		case ch <- s.version:
		default: // it has one pending already
		}
	}
}

func sortNewest(items []Item) {
	slices.SortStableFunc(items, func(a, b Item) int {
		return cmp.Or(b.Created.Compare(a.Created), strings.Compare(b.ID, a.ID))
	})
}

// watchStore follows the store's directory by its modification time, which
// changes whenever an item is added, renamed into place or removed: one stat
// per Poll, the same on Linux and macOS, with no watcher to set up. A
// directory whose times have a coarse granularity could hide a second change
// in the same tick, so a directory changed in the last two seconds is
// reread regardless.
func (s *Server) watchStore(ctx context.Context) {
	var last time.Time
	t := time.NewTicker(s.Poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fi, err := os.Stat(s.store.Dir)
		if err != nil {
			if !last.IsZero() {
				last = time.Time{}
				_ = s.Refresh()
			}
			continue
		}
		if m := fi.ModTime(); !m.Equal(last) || time.Since(m) < 2*time.Second {
			last = m
			if err := s.Refresh(); err != nil {
				s.Logf("reading %s: %v", s.store.Dir, err)
			}
		}
	}
}

// followDaemon keeps the agents' media current: reread on every media event,
// and every DaemonRefresh. A daemon that isn't running, or stops, only means
// no agents' media until it answers again; nothing is reported.
func (s *Server) followDaemon(ctx context.Context) {
	for ctx.Err() == nil {
		items, err := s.daemon.Media(ctx)
		s.setAgents(items) // none when it failed
		if err != nil {
			sleep(ctx, 15*time.Second)
			continue
		}
		wctx, cancel := context.WithCancel(ctx)
		changed := make(chan struct{}, 1)
		go func() {
			_ = s.daemon.Watch(wctx, func() {
				select {
				case changed <- struct{}{}:
				default:
				}
			})
			cancel()
		}()
		tick := time.NewTicker(s.DaemonRefresh)
	loop:
		for {
			select {
			case <-wctx.Done():
				break loop
			case <-changed:
				sleep(wctx, 300*time.Millisecond) // a burst of events, one reread
			case <-tick.C:
			}
			if items, err := s.daemon.Media(wctx); err == nil {
				s.setAgents(items)
			}
		}
		tick.Stop()
		cancel()
		sleep(ctx, 5*time.Second)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (s *Server) snapshot() ([]Item, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.all, s.version
}

func (s *Server) item(id string) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.byID[id]
	return it, ok
}

// Handler is the page and its API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	files := http.FileServerFS(sub)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /api/items", s.handleItems)
	mux.HandleFunc("GET /api/facets", s.handleFacets)
	mux.HandleFunc("GET /api/items/{id}", s.handleItem)
	mux.HandleFunc("GET /api/items/{id}/file", s.handleFile)
	mux.HandleFunc("GET /api/items/{id}/thumb", s.handleThumb)
	mux.HandleFunc("DELETE /api/items/{id}", s.handleDelete)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	return guard(mux)
}

// guard keeps the server to pages it served itself. It listens on loopback,
// but any web page the user visits can make their browser send requests
// there: a Host other than a loopback name is a DNS rebinding attempt, and a
// DELETE must carry a header no cross-origin page can set without a CORS
// preflight, which this server never grants.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host != "localhost" && host != "127.0.0.1" && host != "::1" && !strings.HasSuffix(host, ".localhost") {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Machines") != "1" {
			http.Error(w, "missing X-Machines header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ParseQuery reads a Query from the page's parameters. Dates are YYYY-MM-DD,
// local, and to is inclusive of its day.
func ParseQuery(r *http.Request) Query {
	v := r.URL.Query()
	q := Query{
		Text: v.Get("q"), Kind: v.Get("kind"), Source: v.Get("source"),
		Repo: v.Get("repo"), Branch: v.Get("branch"), Session: v.Get("session"),
		Group: v.Get("group"), Offset: atoi(v.Get("offset"), 0), Limit: min(atoi(v.Get("limit"), 100), 1000),
	}
	if d, err := time.ParseInLocation(time.DateOnly, v.Get("from"), time.Local); err == nil {
		q.From = d
	}
	if d, err := time.ParseInLocation(time.DateOnly, v.Get("to"), time.Local); err == nil {
		q.To = d.AddDate(0, 0, 1)
	}
	return q
}

func (s *Server) handleItems(w http.ResponseWriter, r *http.Request) {
	items, version := s.snapshot()
	page := ParseQuery(r).Run(items)
	w.Header().Set("X-Version", strconv.FormatInt(version, 10))
	writeJSON(w, page)
}

func (s *Server) handleFacets(w http.ResponseWriter, r *http.Request) {
	items, _ := s.snapshot()
	writeJSON(w, ParseQuery(r).Facets(items))
}

func (s *Server) handleItem(w http.ResponseWriter, r *http.Request) {
	it, ok := s.item(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, it)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// localFile opens an item's file when it is on this machine: always for the
// store's, and for an agent's when its media directory is shared with this
// machine (the VM shares the user's home at the same path).
func localFile(it Item) (*os.File, os.FileInfo, bool) {
	if it.Path == "" {
		return nil, nil, false
	}
	f, err := os.Open(it.Path)
	if err != nil {
		return nil, nil, false
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, false
	}
	return f, fi, true
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	it, ok := s.item(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if it.Mime != "" {
		w.Header().Set("Content-Type", it.Mime)
	}
	if r.URL.Query().Has("download") {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": downloadName(it)}))
	}
	if f, fi, ok := localFile(it); ok {
		defer func() { _ = f.Close() }()
		// ServeContent answers range requests, which is what lets the
		// video element seek.
		http.ServeContent(w, r, "", fi.ModTime(), f)
		return
	}
	if it.Source != SourceAgentBox || s.daemon == nil {
		http.NotFound(w, r)
		return
	}
	rc, err := s.daemon.Open(r.Context(), strings.TrimPrefix(it.ID, agentPrefix))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = rc.Close() }()
	if it.Bytes > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(it.Bytes, 10))
	}
	_, _ = io.Copy(w, rc)
}

// downloadName is a file name that says where an item came from.
func downloadName(it Item) string {
	ext := filepath.Ext(it.Path)
	if ext == "" {
		if exts, _ := mime.ExtensionsByType(it.Mime); len(exts) > 0 {
			ext = exts[0]
		}
	}
	parts := []string{}
	for _, p := range []string{it.Repo, it.Branch, it.Created.Local().Format("20060102-150405")} {
		if p = sanitize(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "_") + ext
}

func sanitize(s string) string {
	return strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' {
			return r
		}
		return '-'
	}, s), "-.")
}

// thumbPath is where an item's thumbnail is cached. Ids are stable, and an
// item's file never changes, so a thumbnail never goes stale; the name is a
// hash so any id is a safe file name.
func (s *Server) thumbPath(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.thumbDir, hex.EncodeToString(sum[:12])+".jpg")
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	it, ok := s.item(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	path := s.thumbPath(it.ID)
	_, err, _ := s.thumbs.Do(path, func() (any, error) {
		return nil, s.ensureThumb(r.Context(), it, path)
	})
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}

// ensureThumb makes an item's thumbnail unless it is cached. One that can't
// be made is remembered too (a .none file beside it), so a page full of
// recordings on a machine without ffmpeg doesn't retry each one on every
// visit.
func (s *Server) ensureThumb(ctx context.Context, it Item, path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if _, err := os.Stat(path + ".none"); err == nil {
		return errNoThumb
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	src := it.Path
	if f, _, ok := localFile(it); ok {
		_ = f.Close()
	} else if it.Source == SourceAgentBox && s.daemon != nil {
		rc, err := s.daemon.Open(ctx, strings.TrimPrefix(it.ID, agentPrefix))
		if err != nil {
			return err
		}
		tmp, err := copyToTemp(s.thumbDir, rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(tmp) }()
		src = tmp
	} else {
		return fs.ErrNotExist
	}
	// Made under its own context: a page scrolled past mid-way still leaves
	// the thumbnail for next time, and the singleflight's other waiters
	// aren't failed by the first one's cancel.
	err := makeThumb(context.WithoutCancel(ctx), src, path, it.Kind)
	if errors.Is(err, errNoThumb) {
		_ = os.WriteFile(path+".none", nil, 0o644)
	}
	return err
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	it, ok := s.item(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	var err error
	switch it.Source {
	case SourceMachines:
		err = s.store.Delete(strings.TrimPrefix(it.ID, localPrefix))
		if err == nil {
			err = s.Refresh()
		}
	case SourceAgentBox:
		if s.daemon == nil {
			err = errors.New("AgentBox isn't running")
			break
		}
		if err = s.daemon.Delete(r.Context(), strings.TrimPrefix(it.ID, agentPrefix)); err == nil {
			s.mu.RLock()
			agents := slices.DeleteFunc(slices.Clone(s.agents), func(x Item) bool { return x.ID == it.ID })
			s.mu.RUnlock()
			s.setAgents(agents)
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	thumb := s.thumbPath(it.ID)
	_ = os.Remove(thumb)
	_ = os.Remove(thumb + ".none")
	w.WriteHeader(http.StatusNoContent)
}

// handleEvents is a server-sent event stream: "change", with the items'
// version, whenever they change.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch := make(chan int64, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	version := s.version
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()
	_, _ = fmt.Fprintf(w, "event: hello\ndata: %d\n\n", version)
	flusher.Flush()
	keep := time.NewTicker(25 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case v := <-ch:
			_, _ = fmt.Fprintf(w, "event: change\ndata: %d\n\n", v)
		case <-keep.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
		}
		flusher.Flush()
	}
}
