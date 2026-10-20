package daemon

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// browserSocketPath is where an agent's browser service ("vnc" or "cdp") is
// reachable on the host, through an Incus proxy device: next to the agent's API
// socket, and no longer than it.
func (s *Server) browserSocketPath(instance, service string) string {
	return strings.TrimSuffix(s.agentSocketPath(instance), ".sock") + "." + service
}

func (s *Server) removeBrowserSockets(instance string) {
	for _, service := range agent.HostServices {
		_ = os.Remove(s.browserSocketPath(instance, service))
	}
}

func toAPIBrowser(st agent.BrowserStatus) api.BrowserStatus {
	out := api.BrowserStatus{Display: st.Display, Running: st.Running, Version: st.Version, Pages: make([]api.BrowserPage, 0, len(st.Pages))}
	for _, p := range st.Pages {
		out.Pages = append(out.Pages, api.BrowserPage{ID: p.ID, URL: p.URL, Title: p.Title})
	}
	return out
}

// browser serves the browser endpoints for the agent agentOf returns: the one
// named in the path on the host API, or the calling agent on its own socket.
func (s *Server) browser(action string, agentOf func(*http.Request) (state.Agent, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := agentOf(r)
		if err != nil {
			return err
		}
		ctx, m := r.Context(), s.manager(s.cfg.Log)
		var status agent.BrowserStatus
		switch action {
		case "status":
			status, err = m.BrowserStatus(ctx, a)
		case "start":
			status, err = m.StartBrowser(ctx, a)
		case "stop":
			if err = m.StopBrowser(ctx, a); err == nil {
				status, err = m.BrowserStatus(ctx, a)
			}
		case "open":
			var req api.BrowserOpenRequest
			if err = readJSON(r, &req); err == nil {
				status, err = m.OpenInBrowser(ctx, a, req.URL)
			}
		}
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, toAPIBrowser(status))
	}
}

// browserGPU reports what an agent's desktop renders on (agent.GPUReport).
func (s *Server) browserGPU(agentOf func(*http.Request) (state.Agent, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := agentOf(r)
		if err != nil {
			return err
		}
		report, err := s.manager(s.cfg.Log).GPUReport(r.Context(), a)
		if err != nil {
			return err
		}
		out := api.GPUReport{Device: report.Device, Display: report.Display, Renderer: report.Renderer,
			Accelerated: report.Accelerated, Vulkan: []api.VulkanDevice{}, Error: report.Error}
		for _, d := range report.Vulkan {
			out.Vulkan = append(out.Vulkan, api.VulkanDevice{Name: d.Name, Type: d.Type})
		}
		return writeJSON(w, http.StatusOK, out)
	}
}

// elementPicks cancels each agent's element picker: one at a time per agent,
// so a second pick ends the first.
type elementPicks struct {
	mu     sync.Mutex
	cancel map[string]*context.CancelFunc
}

func (p *elementPicks) start(ref string, cancel context.CancelFunc) (done func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel == nil {
		p.cancel = map[string]*context.CancelFunc{}
	}
	if previous := p.cancel[ref]; previous != nil {
		(*previous)()
	}
	mine := &cancel
	p.cancel[ref] = mine
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.cancel[ref] == mine {
			delete(p.cancel, ref)
		}
	}
}

func (p *elementPicks) stop(ref string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cancel := p.cancel[ref]; cancel != nil {
		(*cancel)()
	}
}

// pickElement shows the element picker on the page the agent's browser shows
// and answers once an element is clicked, or with none once it is cancelled.
func (s *Server) pickElement(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.ElementPickRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.picks.start(a.Ref(), cancel)()
	el, err := s.manager(nil).PickElement(ctx, a, req.Hint)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.ElementPickResult{Element: el})
}

func (s *Server) cancelPick(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	s.picks.stop(a.Ref())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) browserView(w http.ResponseWriter, r *http.Request) error {
	return s.vncView(w, r, s.manager(nil).DialVNC)
}

// vncView bridges a WebSocket to a VNC server of the agent, for noVNC in the
// desktop app. Binary frames carry the VNC protocol's bytes in both directions.
func (s *Server) vncView(w http.ResponseWriter, r *http.Request, dial func(context.Context, state.Agent) (net.Conn, error)) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	vnc, err := dial(r.Context(), a)
	if err != nil {
		return err
	}
	defer func() { _ = vnc.Close() }()

	// The socket is only reachable by this user, so any origin (the desktop app's) is fine.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil // Accept has already written the response
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stream := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	go func() {
		_, _ = io.Copy(stream, vnc)
		cancel()
	}()
	_, _ = io.Copy(vnc, stream)
	return nil
}

// projectDesktops answers when each of a project's running agents last used
// its desktop, for those that have since the daemon started: cheap enough
// for the lead's chat to ask every few seconds while it is open.
func (s *Server) projectDesktops(w http.ResponseWriter, r *http.Request) error {
	agents, err := s.store.Agents(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	out := []api.DesktopUse{}
	for _, a := range agents {
		if at := s.chat.DesktopAt(a.Ref()); !at.IsZero() {
			out = append(out, api.DesktopUse{Ref: a.Ref(), UsedAt: at})
		}
	}
	return writeJSON(w, http.StatusOK, out)
}
