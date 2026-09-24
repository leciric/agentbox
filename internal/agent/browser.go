package agent

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"

	"agentbox/internal/state"
)

// Each agent can run a browser: Chromium on a small virtual desktop (openbox,
// tint2, a file manager and a terminal), with a VNC server on that display
// (browser.sh). The agent drives Chromium through the DevTools protocol on
// 127.0.0.1:9222. AgentBox reaches the DevTools endpoint and the VNC server
// through Incus proxy devices that listen on host unix sockets, so neither
// port is open on the agent's network.

//go:embed browser.sh
var browserScript []byte

const (
	browserScriptPath = "/usr/local/bin/agentbox-browser"
	vncDevice         = "agentbox-vnc"
	devtoolsDevice    = "agentbox-cdp"
	// BrowserDevTools is the DevTools endpoint inside the agent, for its AI tools.
	BrowserDevTools = "http://127.0.0.1:9222"
)

// BrowserServices are the browser services AgentBox reaches from the host: the
// VNC server and the Chrome DevTools Protocol endpoint.
var BrowserServices = []string{"vnc", "cdp"}

var browserPorts = map[string]struct {
	device string
	port   int
}{
	"vnc": {vncDevice, 5900},
	"cdp": {devtoolsDevice, 9222},
}

// copiedDevices point at one agent's host paths and sockets, so a copy of an
// agent (a fork or a project base) must not keep them.
var copiedDevices = []string{"worktree", "gitdir", agentAPIDevice, vncDevice, devtoolsDevice, androidViewDevice}

type BrowserStatus struct {
	// Display is the agent's desktop: the display and its VNC server. It runs
	// without Chromium, and the app's Desktop tab shows it either way.
	Display bool
	// Running is Chromium's own state, which the page controls depend on.
	Running bool
	Version string
	Pages   []BrowserPage // most recently used first
}

type BrowserPage struct {
	ID, URL, Title string
	webSocket      string // the page's DevTools protocol endpoint
}

// StartBrowser starts the agent's display, VNC server and browser, unless they
// are running already.
func (m *Manager) StartBrowser(ctx context.Context, a state.Agent) (BrowserStatus, error) {
	if err := m.prepareBrowser(ctx, a); err != nil {
		return BrowserStatus{}, err
	}
	if err := m.runBrowserScript(ctx, a, "start"); err != nil {
		return BrowserStatus{}, err
	}
	return m.BrowserStatus(ctx, a)
}

// EnsureBrowser starts the agent's browser along with the agent. The agent
// works without one, so a failure is logged rather than returned.
func (m *Manager) EnsureBrowser(ctx context.Context, a state.Agent) {
	if m.BrowserSocket == nil {
		return
	}
	m.logf("Starting the browser")
	if _, err := m.StartBrowser(ctx, a); err != nil {
		m.logf("The browser didn't start: %v (retry with agentbox browser start %s)", err, a.Ref())
	}
}

func (m *Manager) StopBrowser(ctx context.Context, a state.Agent) error {
	if err := m.prepareBrowser(ctx, a); err != nil {
		return err
	}
	return m.runBrowserScript(ctx, a, "stop")
}

// prepareBrowser checks that the agent runs, and installs the proxy devices and
// the current browser.sh, wallpaper and desktop colours.
func (m *Manager) prepareBrowser(ctx context.Context, a state.Agent) error {
	if m.BrowserSocket == nil {
		return errors.New("the browser is managed by the AgentBox daemon")
	}
	if err := m.requireRunning(ctx, a); err != nil {
		return err
	}
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return err
	}
	for _, service := range BrowserServices {
		p := browserPorts[service]
		if _, ok := devices[p.device]; ok {
			continue
		}
		if err := m.addSocketProxy(ctx, a, p.device, service, p.port); err != nil {
			return err
		}
	}
	m.installWallpaper(ctx, a)
	m.installTheme(ctx, a)
	return m.Incus.WriteFile(ctx, a.Instance, browserScriptPath, browserScript, 0, 0, 0o755)
}

// addSocketProxy makes a port that listens on the agent's 127.0.0.1 reachable
// at the host socket for service, and nowhere else.
func (m *Manager) addSocketProxy(ctx context.Context, a state.Agent, device, service string, port int) error {
	socket := m.BrowserSocket(a.Instance, service)
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	os.Remove(socket) // left by an earlier device
	_, err := m.Incus.Run(ctx, "config", "device", "add", a.Instance, device, "proxy",
		"listen=unix:"+socket,
		fmt.Sprintf("connect=tcp:127.0.0.1:%d", port),
		"bind=host",
		fmt.Sprintf("uid=%d", m.User.UID),
		fmt.Sprintf("gid=%d", m.User.GID),
		"mode=0600")
	return err
}

func (m *Manager) runBrowserScript(ctx context.Context, a state.Agent, action string) error {
	var out bytes.Buffer
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, browserScriptPath+" "+action, nil, &out, &out); err != nil {
		if msg := strings.TrimSpace(out.String()); msg != "" {
			return fmt.Errorf("browser %s: %s", action, msg)
		}
		return fmt.Errorf("browser %s: %w", action, err)
	}
	return nil
}

// BrowserStatus asks the agent's browser which pages it has open, and says
// whether the desktop under it is up. An agent that is stopped, or never
// started its desktop, has neither.
func (m *Manager) BrowserStatus(ctx context.Context, a state.Agent) (BrowserStatus, error) {
	status := BrowserStatus{Pages: []BrowserPage{}}
	if m.BrowserSocket == nil {
		return status, nil
	}
	inst, err := m.Incus.Instance(ctx, a.Instance)
	if err != nil || inst.Status != "Running" {
		return status, err
	}
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return status, err
	}
	if _, ok := devices[devtoolsDevice]; ok {
		if pages, version, ok := m.devtoolsPages(ctx, a); ok {
			// It answered from the display, so the display is up: no need to
			// ask the VNC server as well.
			status.Display, status.Running, status.Version, status.Pages = true, true, version, pages
			return status, nil
		}
	}
	// Chromium isn't answering. The desktop it was a window on usually still
	// is — an agent that closed the browser's window, or a browser that
	// crashed, leaves the display, the dock and everything else running.
	if _, ok := devices[vncDevice]; ok {
		status.Display = m.displayUp(ctx, a)
	}
	return status, nil
}

// devtoolsPages returns the browser's open pages and its version, or ok=false
// if nothing answers on the DevTools endpoint.
func (m *Manager) devtoolsPages(ctx context.Context, a state.Agent) ([]BrowserPage, string, bool) {
	var version struct{ Browser string }
	if err := m.devtoolsRequest(ctx, a, http.MethodGet, "/json/version", &version); err != nil {
		return nil, "", false
	}
	var targets []struct{ ID, Type, URL, Title, WebSocketDebuggerURL string }
	if err := m.devtoolsRequest(ctx, a, http.MethodGet, "/json/list", &targets); err != nil {
		return nil, "", false
	}
	pages := []BrowserPage{}
	for _, t := range targets {
		if t.Type == "page" {
			pages = append(pages, BrowserPage{ID: t.ID, URL: t.URL, Title: t.Title, webSocket: t.WebSocketDebuggerURL})
		}
	}
	return pages, version.Browser, true
}

// OpenInBrowser shows target in the agent's browser, starting the browser if
// needed, and waits briefly for the page to load.
func (m *Manager) OpenInBrowser(ctx context.Context, a state.Agent, target string) (BrowserStatus, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return BrowserStatus{}, errors.New("no URL to open")
	}
	if !strings.Contains(target, "://") && !strings.HasPrefix(target, "about:") {
		target = "http://" + target
	}
	status, err := m.StartBrowser(ctx, a)
	if err != nil {
		return status, err
	}
	if len(status.Pages) == 0 {
		if err := m.devtoolsRequest(ctx, a, http.MethodPut, "/json/new?about:blank", nil); err != nil {
			return status, err
		}
		if status, err = m.BrowserStatus(ctx, a); err != nil {
			return status, err
		}
		if len(status.Pages) == 0 {
			return status, errors.New("the browser has no tab to open the page in")
		}
	}
	page := status.Pages[0]
	if err := m.navigate(ctx, a, page, target); err != nil {
		return status, err
	}
	m.devtoolsRequest(ctx, a, http.MethodGet, "/json/activate/"+page.ID, nil)
	return m.BrowserStatus(ctx, a)
}

// displayUp says whether the agent's display and its VNC server are running.
// The VNC server greets whoever connects with its RFB version before it is
// asked anything, and the proxy device accepts the connection even when
// nothing listens inside the agent, so those few bytes are what tells a
// running display from a dead one.
func (m *Manager) displayUp(ctx context.Context, a state.Agent) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := m.dialVNC(ctx, a)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	greeting := make([]byte, 3)
	_, err = io.ReadFull(conn, greeting)
	return err == nil && string(greeting) == "RFB"
}

// DialVNC connects to the VNC server on the agent's display. Chromium doesn't
// have to be running: the desktop is watchable on its own.
func (m *Manager) DialVNC(ctx context.Context, a state.Agent) (net.Conn, error) {
	status, err := m.BrowserStatus(ctx, a)
	if err != nil {
		return nil, err
	}
	if !status.Display {
		return nil, fmt.Errorf("%s's desktop isn't running: start it first", a.Ref())
	}
	return m.dialVNC(ctx, a)
}

func (m *Manager) dialVNC(ctx context.Context, a state.Agent) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", m.BrowserSocket(a.Instance, "vnc"))
}

// devtools is an HTTP client for the agent's DevTools endpoint, through its
// proxy device. Chromium only answers requests addressed to 127.0.0.1 or
// localhost, so URLs keep that host.
func (m *Manager) devtools(a state.Agent) *http.Client {
	socket := m.BrowserSocket(a.Instance, "cdp")
	return &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
}

func (m *Manager) devtoolsRequest(ctx context.Context, a state.Agent, method, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, BrowserDevTools+path, nil)
	if err != nil {
		return err
	}
	resp, err := m.devtools(a).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("DevTools %s: %s", path, resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// navigate loads target in a tab over the DevTools protocol, then waits up to
// five seconds for the page to finish loading.
func (m *Manager) navigate(ctx context.Context, a state.Agent, page BrowserPage, target string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, _, err := websocketDial(ctx, page.webSocket, m.devtools(a))
	if err != nil {
		return fmt.Errorf("connecting to the browser: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(32 << 20)

	session := devtoolsSession{conn: conn}
	var nav struct{ ErrorText string }
	if err := session.call(ctx, "Page.navigate", map[string]any{"url": target}, &nav); err != nil {
		return err
	}
	if nav.ErrorText != "" {
		return fmt.Errorf("opening %s: %s", target, nav.ErrorText)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		var eval struct{ Result struct{ Value any } }
		err := session.call(ctx, "Runtime.evaluate", map[string]any{"expression": "document.readyState", "returnByValue": true}, &eval)
		if err == nil && eval.Result.Value == "complete" {
			break
		}
	}
	return nil
}

func websocketDial(ctx context.Context, url string, client *http.Client) (*websocket.Conn, *http.Response, error) {
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client})
}

// devtoolsSession sends DevTools protocol commands over one page connection.
type devtoolsSession struct {
	conn *websocket.Conn
	last int
}

func (s *devtoolsSession) call(ctx context.Context, method string, params, result any) error {
	s.last++
	id := s.last
	msg, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := s.conn.Write(ctx, websocket.MessageText, msg); err != nil {
		return err
	}
	for {
		_, data, err := s.conn.Read(ctx)
		if err != nil {
			return err
		}
		var reply struct {
			ID     int
			Result json.RawMessage
			Error  *struct{ Message string }
		}
		if json.Unmarshal(data, &reply) != nil || reply.ID != id {
			continue // an event, or the reply to another command
		}
		if reply.Error != nil {
			return fmt.Errorf("%s: %s", method, reply.Error.Message)
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(reply.Result, result)
	}
}
