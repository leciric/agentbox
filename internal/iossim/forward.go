package iossim

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"agentbox/internal/api"
)

// DefaultPreview is where the Mac reaches the daemon's preview proxy, which
// the front end forwards from the VM.
const DefaultPreview = "127.0.0.1:7777"

// ForwardRequest is the helper's POST /v1/forwards and /v1/forwards/stop:
// one port of the Mac's 127.0.0.1 for an agent, or to stop, Port 0 for all
// of that agent's.
type ForwardRequest struct {
	Port    int    `json:"port"`
	Project string `json:"project"`
	Agent   string `json:"agent"`
}

var labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// forwards are the Mac's ports that go to agents' through the preview proxy:
// each serves HTTP on 127.0.0.1:<port> and passes every request on, upgrades
// included, with api.PreviewHeader saying which agent, and its Host left as
// the app sent it. One port is one agent's: the last to ask for it, which
// the daemon allows only once the agent holding it no longer runs.
type forwards struct {
	preview string
	mu      sync.Mutex
	open    map[int]*forward
}

type forward struct {
	project, agent string
	srvs           []*http.Server
	// conns are its connections, the hijacked ones (a WebSocket: Metro's
	// reloads) included, which closing the servers leaves open.
	mu    sync.Mutex
	conns map[net.Conn]bool
}

func (fw *forward) track(c net.Conn, st http.ConnState) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	switch st {
	case http.StateNew:
		fw.conns[c] = true
	case http.StateClosed:
		delete(fw.conns, c)
	}
}

func (fw *forward) close() {
	for _, srv := range fw.srvs {
		_ = srv.Close()
	}
	fw.mu.Lock()
	defer fw.mu.Unlock()
	for c := range fw.conns {
		_ = c.Close()
	}
	clear(fw.conns)
}

func newForwards(preview string) *forwards {
	return &forwards{preview: cmp.Or(preview, DefaultPreview), open: map[int]*forward{}}
}

// inUse is whether anything else answers on the port: a server on every
// address or on ::1 lets 127.0.0.1 be bound beside it on a Mac.
func inUse(port int) bool {
	for _, host := range []string{"127.0.0.1", "::1"} {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 300*time.Millisecond); err == nil {
			_ = c.Close()
			return true
		}
	}
	return false
}

func (f *forwards) start(req ForwardRequest) error {
	if req.Port < 1024 || req.Port > 65535 {
		return badRequest("%d isn't a port the Mac can forward: use 1024 to 65535", req.Port)
	}
	if !labelPattern.MatchString(req.Project) || !labelPattern.MatchString(req.Agent) {
		return badRequest("%q/%q isn't an agent", req.Project, req.Agent)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if cur, ok := f.open[req.Port]; ok {
		if cur.project == req.Project && cur.agent == req.Agent {
			return nil
		}
		cur.close()
		delete(f.open, req.Port)
	}
	taken := &httpError{code: http.StatusConflict, msg: fmt.Sprintf("port %d is taken on the Mac: something of yours listens on it", req.Port)}
	if inUse(req.Port) {
		return taken
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(req.Port)))
	if err != nil {
		return taken
	}
	lns := []net.Listener{ln}
	// localhost may be ::1 first; a Mac without IPv6 has only 127.0.0.1.
	if ln6, err := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(req.Port))); err == nil {
		lns = append(lns, ln6)
	}
	route := fmt.Sprintf("%d.%s.%s", req.Port, req.Agent, req.Project)
	target := &url.URL{Scheme: "http", Host: f.preview}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set(api.PreviewHeader, route)
		},
	}
	fw := &forward{project: req.Project, agent: req.Agent, conns: map[net.Conn]bool{}}
	for _, l := range lns {
		srv := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second, ConnState: fw.track}
		fw.srvs = append(fw.srvs, srv)
		go func() { _ = srv.Serve(l) }()
	}
	f.open[req.Port] = fw
	return nil
}

func (f *forwards) stop(req ForwardRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for port, cur := range f.open {
		if cur.project == req.Project && cur.agent == req.Agent && (req.Port == 0 || req.Port == port) {
			cur.close()
			delete(f.open, port)
		}
	}
}

func (f *forwards) list() []api.IOSForward {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []api.IOSForward{}
	for port, cur := range f.open {
		out = append(out, api.IOSForward{Port: port, Agent: cur.project + "/" + cur.agent})
	}
	slices.SortFunc(out, func(a, b api.IOSForward) int { return a.Port - b.Port })
	return out
}

func (f *forwards) closeAll(ctx context.Context) {
	<-ctx.Done()
	f.mu.Lock()
	defer f.mu.Unlock()
	for port, cur := range f.open {
		cur.close()
		delete(f.open, port)
	}
}

func (s *Server) forwardRoute(stop bool) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req ForwardRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		if stop {
			s.forwards.stop(req)
		} else if err := s.forwards.start(req); err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, s.forwards.list())
		return nil
	}
}
