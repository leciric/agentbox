package daemon

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

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
		os.Remove(s.browserSocketPath(instance, service))
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
	defer vnc.Close()

	// The socket is only reachable by this user, so any origin (the desktop app's) is fine.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil // Accept has already written the response
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stream := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	go func() {
		io.Copy(stream, vnc)
		cancel()
	}()
	io.Copy(vnc, stream)
	return nil
}
