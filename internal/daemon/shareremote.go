package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The other side of sharing (sharepeers.go): this AgentBox paired with an
// owner, and shows the projects the owner shares as its own, so the app opens
// their chats, sends prompts and watches their agents as it does a local
// project's.
//
// A remote project's name here is "<remote>~<project>": no project's slug has
// a "~" (naming.ProjectSlug). The socket's API (shareProxy) sends a request
// for such a project to its owner's share gateway, with the name its owner
// knows and the token the pairing gave, and names the project as it is known
// here in what comes back. GET /v1/projects and GET /v1/agents have the
// remote projects' added, and each remote's event stream is published here,
// renamed the same way (remoteEvents).

// remoteSep is between a remote's id and its project's name.
const remoteSep = "~"

type shareRemotes struct {
	mu    sync.Mutex
	conns map[string]*remoteConn // by the remote's id
	// client is what reaches the owners; a field for tests.
	client *http.Client
}

func newShareRemotes() *shareRemotes {
	return &shareRemotes{conns: map[string]*remoteConn{}, client: &http.Client{}}
}

// remoteConn is a remote as this daemon runs it.
type remoteConn struct {
	r      state.ShareRemote
	token  string
	cancel context.CancelFunc

	mu       sync.Mutex
	projects []json.RawMessage // its shared projects, renamed, the last time it answered
	agents   []json.RawMessage
	listedAt time.Time
	err      string
}

func (c *remoteConn) base() string { return strings.TrimRight(c.r.URL, "/") + "/share/api" }

func (c *remoteConn) request(ctx context.Context, client *http.Client, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return client.Do(req)
}

// remoteOf splits a project's name here into its remote and its name there.
func remoteOf(name string) (id, project string, ok bool) {
	id, project, ok = strings.Cut(name, remoteSep)
	return id, project, ok && id != "" && project != ""
}

func (s *Server) remoteConn(id string) *remoteConn {
	rs := s.peering.remotes
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.conns[id]
}

// startShareRemotes runs every remote this AgentBox paired with.
func (s *Server) startShareRemotes(ctx context.Context) {
	remotes, err := s.store.ShareRemotes(ctx)
	if err != nil {
		s.logf("share: %v", err)
		return
	}
	for _, r := range remotes {
		s.runRemote(ctx, r)
	}
}

func (s *Server) runRemote(ctx context.Context, r state.ShareRemote) {
	token, err := s.secrets().Open(r.Token)
	if err != nil {
		s.logf("share: %s's token can't be read: %v", r.Name, err)
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &remoteConn{r: r, token: token, cancel: cancel}
	rs := s.peering.remotes
	rs.mu.Lock()
	if old := rs.conns[r.ID]; old != nil {
		old.cancel()
	}
	rs.conns[r.ID] = c
	rs.mu.Unlock()
	go s.remoteEvents(ctx, c)
}

func (s *Server) shareRemoteInfo(r state.ShareRemote) api.ShareRemote {
	out := api.ShareRemote{ID: r.ID, Name: r.Name, URL: r.URL, Paired: r.Created}
	if c := s.remoteConn(r.ID); c != nil {
		c.mu.Lock()
		out.Projects, out.Error = len(c.projects), c.err
		c.mu.Unlock()
	}
	return out
}

// shareLink is what a pairing link holds: the owner's address and the
// secret.
func shareLink(link string) (base, secret string, err error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", errors.New("that isn't a pairing link: copy the whole link from Settings → Share on the other AgentBox")
	}
	frag, _ := url.ParseQuery(u.Fragment)
	if secret = frag.Get("share"); secret == "" {
		return "", "", errors.New("that link has no pairing code in it: make a new one in Settings → Share on the other AgentBox, under Paired AgentBoxes")
	}
	u.Fragment, u.RawQuery = "", ""
	return strings.TrimRight(u.String(), "/"), secret, nil
}

func (s *Server) addShareRemote(w http.ResponseWriter, r *http.Request) error {
	var req api.AddShareRemoteRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	base, secret, err := shareLink(req.Link)
	if err != nil {
		return err
	}
	ctx := r.Context()
	body, _ := json.Marshal(api.SharePairRequest{Secret: secret, Name: s.shareName(ctx)})
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	preq, err := http.NewRequestWithContext(pctx, http.MethodPost, base+"/share/pair", bytes.NewReader(body))
	if err != nil {
		return err
	}
	preq.Header.Set("Content-Type", "application/json")
	resp, err := s.peering.remotes.client.Do(preq)
	if err != nil {
		return fmt.Errorf("the other AgentBox can't be reached at %s: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		var e api.Error
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("the other AgentBox answered %s", resp.Status)
	}
	var out api.SharePairResponse
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		return errors.New("the other AgentBox answered with no token: is it AgentBox?")
	}
	sealed, err := s.secrets().Seal(out.Token)
	if err != nil {
		return err
	}
	id := make([]byte, 3)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	rem := state.ShareRemote{ID: "r" + hex.EncodeToString(id), Name: strings.TrimSpace(out.Owner), URL: base, Token: sealed, Created: time.Now()}
	if rem.Name == "" {
		rem.Name = "AgentBox"
	}
	if err := s.store.AddShareRemote(ctx, rem); err != nil {
		return err
	}
	s.logf("share: paired with %s at %s", rem.Name, base)
	s.runRemote(s.runCtx, rem)
	s.publishShare(ctx)
	s.events.publish(api.EventProject, struct{}{})
	return writeJSON(w, http.StatusOK, s.shareRemoteInfo(rem))
}

func (s *Server) removeShareRemote(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if err := s.store.RemoveShareRemote(r.Context(), id); err != nil {
		return err
	}
	rs := s.peering.remotes
	rs.mu.Lock()
	if c := rs.conns[id]; c != nil {
		c.cancel()
		delete(rs.conns, id)
	}
	rs.mu.Unlock()
	s.publishShare(r.Context())
	s.events.publish(api.EventProject, struct{}{})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// remoteEvents publishes a remote's event stream here, with its projects
// renamed, connecting again after a pause whenever it ends.
func (s *Server) remoteEvents(ctx context.Context, c *remoteConn) {
	wait := time.Second
	for ctx.Err() == nil {
		began := time.Now()
		err := s.remoteEventsOnce(ctx, c)
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		changed := c.err != errText(err)
		c.err = errText(err)
		c.mu.Unlock()
		if changed {
			s.publishShare(ctx)
			s.events.publish(api.EventProject, struct{}{})
		}
		if time.Since(began) > time.Minute {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 30*time.Second)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) remoteEventsOnce(ctx context.Context, c *remoteConn) error {
	resp, err := c.request(ctx, s.peering.remotes.client, http.MethodGet, "/v1/events", nil)
	if err != nil {
		return fmt.Errorf("%s can't be reached: %w", c.r.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return remoteStatusError(c, resp)
	}
	c.mu.Lock()
	hadErr := c.err != ""
	c.err = ""
	c.listedAt = time.Time{} // list again: what it shares may have changed while it was away
	c.mu.Unlock()
	if hadErr {
		s.publishShare(ctx)
	}
	s.events.publish(api.EventProject, struct{}{})
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	for sc.Scan() {
		line := sc.Bytes()
		data, ok := bytes.CutPrefix(line, []byte("data: "))
		if !ok {
			continue
		}
		var ev api.Event
		if json.Unmarshal(data, &ev) != nil || ev.Type == "" {
			continue
		}
		if ev.Type == api.EventProject {
			c.mu.Lock()
			c.listedAt = time.Time{}
			c.mu.Unlock()
		}
		s.events.publish(ev.Type, json.RawMessage(renameJSON(ev.Data, c.r.ID)))
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s's events stopped: %w", c.r.Name, err)
	}
	return errors.New(c.r.Name + "'s events stopped")
}

func remoteStatusError(c *remoteConn, resp *http.Response) error {
	var e api.Error
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%s unpaired this AgentBox: pair again with a new link from it", c.r.Name)
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return fmt.Errorf("%s: %s", c.r.Name, e.Error)
	}
	return fmt.Errorf("%s answered %s", c.r.Name, resp.Status)
}

// remoteLists are a remote's shared projects and their agents, renamed, read
// again when older than a few seconds or when its events said a project
// changed.
func (s *Server) remoteLists(ctx context.Context, c *remoteConn) (projects, agents []json.RawMessage) {
	c.mu.Lock()
	if time.Since(c.listedAt) < 5*time.Second {
		defer c.mu.Unlock()
		return c.projects, c.agents
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	get := func(path string) ([]json.RawMessage, error) {
		resp, err := c.request(ctx, s.peering.remotes.client, http.MethodGet, path, nil)
		if err != nil {
			return nil, fmt.Errorf("%s can't be reached: %w", c.r.Name, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, remoteStatusError(c, resp)
		}
		var items []json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
			return nil, err
		}
		for i, it := range items {
			items[i] = renameJSON(it, c.r.ID)
		}
		return items, nil
	}
	projects, err := get("/v1/projects")
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.err = err.Error()
		return c.projects, c.agents // what it last shared, until it answers again
	}
	// Its agents can fail on their own (its machines unreadable a moment):
	// the projects still show, with the agents it last listed.
	c.mu.Unlock()
	agents, aerr := get("/v1/agents")
	c.mu.Lock()
	if aerr != nil {
		agents = c.agents
	}
	for i, p := range projects {
		projects[i] = withRemote(p, api.ProjectRemote{ID: c.r.ID, Owner: c.r.Name})
	}
	c.projects, c.agents, c.listedAt, c.err = projects, agents, time.Now(), ""
	return projects, agents
}

func withRemote(project json.RawMessage, r api.ProjectRemote) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(project, &m) != nil {
		return project
	}
	m["remote"], _ = json.Marshal(r)
	out, err := json.Marshal(m)
	if err != nil {
		return project
	}
	return out
}

func (s *Server) remoteConns() []*remoteConn {
	rs := s.peering.remotes
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]*remoteConn, 0, len(rs.conns))
	for _, c := range rs.conns {
		out = append(out, c)
	}
	return out
}

// renameJSON names the projects in data as they are known here: a
// "project", the project of a "ref", a job's "target" or a chat event's
// "agent" ("project/agent"),
// and the "name" of a project itself. Numbers are kept as they were.
func renameJSON(data json.RawMessage, remote string) json.RawMessage {
	if len(data) == 0 || (data[0] != '{' && data[0] != '[') {
		return data
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return data
	}
	out, err := json.Marshal(renameValue(v, remote))
	if err != nil {
		return data
	}
	return out
}

func renameValue(v any, remote string) any {
	switch x := v.(type) {
	case []any:
		for i := range x {
			x[i] = renameValue(x[i], remote)
		}
	case map[string]any:
		_, isProject := x["displayName"]
		_, hasIcon := x["iconVersion"]
		for k, val := range x {
			str, isStr := val.(string)
			switch {
			case isStr && str != "" && k == "project" && str != state.HomeProject:
				x[k] = remote + remoteSep + str
			case isStr && (k == "ref" || k == "target" || k == "agent") && strings.Contains(str, "/"):
				x[k] = remote + remoteSep + str
			case isStr && str != "" && k == "name" && isProject && hasIcon:
				x[k] = remote + remoteSep + str
			default:
				x[k] = renameValue(val, remote)
			}
		}
	}
	return v
}

// unrename names a remote's projects as their owner knows them, in a
// request's body or query.
func unrename(b []byte, remote string) []byte {
	return bytes.ReplaceAll(b, []byte(`"`+remote+remoteSep), []byte(`"`))
}

// shareLocalChat is a message written in a local project's chat.
var shareLocalChat = regexp.MustCompile(`^/v1/projects/([^/]+)(/sessions/([^/]+))?/chat/messages$`)

// shareProxy is the socket's API with the remote projects in it: what is
// asked of one goes to its owner (proxyRemote), and the lists of projects and
// agents have theirs added.
func (s *Server) shareProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if m := shareProjectRoute.FindStringSubmatch(path); m != nil {
			if id, project, ok := remoteOf(m[2]); ok {
				s.proxyRemote(w, r, id, "/v1/"+m[1]+"/"+project+m[3], nil)
				return
			}
			if r.Method == http.MethodPost {
				if c := shareLocalChat.FindStringSubmatch(path); c != nil {
					s.heardLocally(c[1], c[3])
				}
			}
		}
		switch {
		case r.Method == http.MethodPost && path == "/v1/agents":
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeError(w, err)
				return
			}
			var req struct {
				Project string `json:"project"`
			}
			_ = json.Unmarshal(body, &req)
			if id, _, ok := remoteOf(req.Project); ok {
				s.proxyRemote(w, r, id, path, body)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		case r.Method == http.MethodGet && path == "/v1/agents" && strings.Contains(r.URL.Query().Get("project"), remoteSep):
			if id, _, ok := remoteOf(r.URL.Query().Get("project")); ok {
				s.proxyRemote(w, r, id, path, nil)
				return
			}
		case r.Method == http.MethodGet && (path == "/v1/projects" || path == "/v1/agents") && len(s.remoteConns()) > 0:
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)
			var items []json.RawMessage
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &items) != nil {
				copyRecorded(w, rec)
				return
			}
			for _, c := range s.remoteConns() {
				projects, agents := s.remoteLists(r.Context(), c)
				if path == "/v1/projects" {
					items = append(items, projects...)
				} else {
					items = append(items, agents...)
				}
			}
			_ = writeJSON(w, http.StatusOK, items)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func copyRecorded(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

// proxyRemote sends a request about one of a remote's projects to its owner,
// as path there, and names its projects as they are known here in the
// answer.
func (s *Server) proxyRemote(w http.ResponseWriter, r *http.Request, id, path string, body []byte) {
	c := s.remoteConn(id)
	if c == nil {
		writeError(w, fmt.Errorf("project %s: this AgentBox isn't paired with the one that shared it any more: %w", r.PathValue("project"), state.ErrNotFound))
		return
	}
	target, err := url.Parse(c.base() + path)
	if err != nil {
		writeError(w, err)
		return
	}
	target.RawQuery = strings.ReplaceAll(r.URL.RawQuery, id+remoteSep, "")
	if body == nil && r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		if body, err = io.ReadAll(io.LimitReader(r.Body, 96<<20)); err != nil {
			writeError(w, err)
			return
		}
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = target
			pr.Out.Host = target.Host
			for _, h := range []string{"Cookie", "Origin", "Referer", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest"} {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Set("Authorization", "Bearer "+c.token)
			if body != nil {
				b := unrename(body, id)
				pr.Out.Body = io.NopCloser(bytes.NewReader(b))
				pr.Out.ContentLength = int64(len(b))
				pr.Out.Header.Set("Content-Length", strconv.Itoa(len(b)))
			}
		},
		Transport:     s.peering.remotes.client.Transport,
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
				return nil
			}
			raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
			_ = resp.Body.Close()
			if err != nil {
				return err
			}
			out := renameJSON(raw, id)
			resp.Body = io.NopCloser(bytes.NewReader(out))
			resp.ContentLength = int64(len(out))
			resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			_ = writeJSON(w, http.StatusBadGateway, api.Error{Error: fmt.Sprintf("%s can't be reached: %v", c.r.Name, err)})
		},
	}
	rp.ServeHTTP(w, r)
}
