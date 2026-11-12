package iossim

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"agentbox/internal/api"
	"agentbox/internal/devicestream"
)

// Health is the helper's GET /v1/health.
type Health struct {
	Version string `json:"version"`
	Xcode   string `json:"xcode,omitempty"`
	Problem string `json:"problem,omitempty"` // why simulators can't run: no Xcode
}

// Server is the helper's HTTP API.
type Server struct {
	Token   string
	Version string
	Simctl  Simctl
	// Roots are the directories apps may be installed from: the agents'
	// worktrees, which the VM shares with the Mac at the same paths.
	Roots    []string
	hosts    *hosts
	forwards *forwards

	// The Xcode check, which runs two commands, for a moment: the app asks
	// for the status every few seconds while its iOS view is open.
	xcodeMu  sync.Mutex
	xcodeAt  time.Time
	xcode    string
	xcodeErr error
}

// NewServer makes the helper's API, starting simhosts with launch.
func NewServer(token, version string, simctl Simctl, roots []string, launch Launcher) *Server {
	return &Server{Token: token, Version: version, Simctl: simctl, Roots: roots, hosts: newHosts(launch, 2*time.Minute), forwards: newForwards("")}
}

// Run stops simhosts nothing uses, until ctx ends, and then closes the
// forwards.
func (s *Server) Run(ctx context.Context) {
	go s.forwards.closeAll(ctx)
	s.hosts.reap(ctx)
}

type handler func(w http.ResponseWriter, r *http.Request) error

// httpError is an error with its status.
type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &httpError{code: http.StatusBadRequest, msg: fmt.Sprintf(format, args...)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, f handler) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := f(w, r); err != nil {
				code := http.StatusInternalServerError
				var he *httpError
				if errors.As(err, &he) {
					code = he.code
				}
				writeJSON(w, code, api.Error{Error: err.Error()})
			}
		})
	}
	h("GET /v1/version", func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, http.StatusOK, Health{Version: s.Version})
		return nil
	})
	h("GET /v1/health", s.health)
	h("GET /v1/devices", s.devices)
	h("POST /v1/devices/{udid}/boot", s.device(func(r *http.Request, udid string) (any, error) {
		return nil, s.Simctl.Boot(r.Context(), udid)
	}))
	h("POST /v1/devices/{udid}/shutdown", s.device(func(r *http.Request, udid string) (any, error) {
		s.hosts.stop(udid)
		return nil, s.Simctl.Shutdown(r.Context(), udid)
	}))
	h("POST /v1/devices/{udid}/install", s.device(s.install))
	h("POST /v1/devices/{udid}/launch", s.device(s.launch))
	h("POST /v1/devices/{udid}/openurl", s.device(s.openURL))
	h("POST /v1/devices/{udid}/input", s.device(s.input))
	h("POST /v1/devices/{udid}/ui", s.device(s.ui))
	h("GET /v1/devices/{udid}/apps", s.device(func(r *http.Request, udid string) (any, error) {
		return s.Simctl.Apps(r.Context(), udid)
	}))
	h("GET /v1/devices/{udid}/screenshot", s.screenshot)
	h("GET /v1/devices/{udid}/view", s.view)
	h("GET /v1/forwards", func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, http.StatusOK, s.forwards.list())
		return nil
	})
	h("POST /v1/forwards", s.forwardRoute(false))
	h("POST /v1/forwards/stop", s.forwardRoute(true))
	return s.auth(mux)
}

// auth lets through only requests with the helper's token. It's on 127.0.0.1
// alone, where anything on the Mac could reach it, and the VM's daemon.
func (s *Server) auth(next http.Handler) http.Handler {
	want := []byte("Bearer " + s.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			writeJSON(w, http.StatusUnauthorized, api.Error{Error: "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return badRequest("invalid request: %v", err)
	}
	return nil
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) error {
	h := Health{Version: s.Version}
	s.xcodeMu.Lock()
	if time.Since(s.xcodeAt) > 30*time.Second {
		s.xcode, s.xcodeErr = s.Simctl.Xcode(r.Context())
		s.xcodeAt = time.Now()
	}
	xcode, err := s.xcode, s.xcodeErr
	s.xcodeMu.Unlock()
	if err != nil {
		h.Problem = Problem(err)
	}
	h.Xcode = xcode
	writeJSON(w, http.StatusOK, h)
	return nil
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) error {
	devices, err := s.Simctl.Devices(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, devices)
	return nil
}

func udidOf(r *http.Request) (string, error) {
	udid := r.PathValue("udid")
	if !ValidUDID(udid) {
		return "", badRequest("%q isn't a simulator's UDID", udid)
	}
	return udid, nil
}

// device is a route on one simulator that answers with JSON (null for nil).
func (s *Server) device(f func(r *http.Request, udid string) (any, error)) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		udid, err := udidOf(r)
		if err != nil {
			return err
		}
		out, err := f(r, udid)
		if err != nil {
			return err
		}
		if out == nil {
			out = struct{}{}
		}
		writeJSON(w, http.StatusOK, out)
		return nil
	}
}

// appPath checks that an app to install is a .app directory under one of
// the roots, symlinks resolved, so the helper installs only what agents
// built in their worktrees.
func (s *Server) appPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", badRequest("%q isn't an absolute path", path)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", badRequest("%s: %v", path, err)
	}
	if !strings.HasSuffix(real, ".app") {
		return "", badRequest("%s isn't a .app: build the app for the simulator (an iphonesimulator build) and install the .app directory", path)
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", badRequest("%s isn't a .app directory", path)
	}
	for _, root := range s.Roots {
		root, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(root, real); err == nil && filepath.IsLocal(rel) {
			return real, nil
		}
	}
	return "", &httpError{code: http.StatusForbidden, msg: fmt.Sprintf("%s isn't in an agent's worktree", path)}
}

func (s *Server) install(r *http.Request, udid string) (any, error) {
	var req api.IOSInstallRequest
	if err := readJSON(r, &req); err != nil {
		return nil, err
	}
	app, err := s.appPath(req.Path)
	if err != nil {
		return nil, err
	}
	return nil, s.Simctl.Install(r.Context(), udid, app)
}

func (s *Server) launch(r *http.Request, udid string) (any, error) {
	var req api.IOSLaunchRequest
	if err := readJSON(r, &req); err != nil {
		return nil, err
	}
	if !bundleIDPattern.MatchString(req.BundleID) {
		return nil, badRequest("%q isn't a bundle identifier", req.BundleID)
	}
	if req.Terminate {
		return nil, s.Simctl.Terminate(r.Context(), udid, req.BundleID)
	}
	out, err := s.Simctl.Launch(r.Context(), udid, req.BundleID, req.Args)
	return api.IOSResult{Output: out}, err
}

func (s *Server) openURL(r *http.Request, udid string) (any, error) {
	var req api.IOSOpenURLRequest
	if err := readJSON(r, &req); err != nil {
		return nil, err
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Scheme == "" || strings.HasPrefix(req.URL, "-") {
		return nil, badRequest("%q isn't a URL", req.URL)
	}
	return nil, s.Simctl.OpenURL(r.Context(), udid, req.URL)
}

func (s *Server) input(r *http.Request, udid string) (any, error) {
	var in api.IOSInput
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	h, err := s.hosts.get(r.Context(), udid)
	if err != nil {
		return nil, err
	}
	h.acting.Lock()
	defer h.acting.Unlock()
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	return nil, s.apply(r.Context(), h, in)
}

// apply plays an input on a simulator: text a US keyboard can't type goes
// on its pasteboard and is pasted.
func (s *Server) apply(ctx context.Context, h *host, in api.IOSInput) error {
	steps, paste, err := plan(in, func() (int, int, error) { return h.size(ctx) })
	if err != nil {
		return badRequest("%v", err)
	}
	if paste != "" {
		if err := s.Simctl.Pasteboard(ctx, h.udid, paste); err != nil {
			return err
		}
		steps = pasteSteps()
	}
	return h.run(ctx, steps)
}

func (s *Server) screenshot(w http.ResponseWriter, r *http.Request) error {
	udid, err := udidOf(r)
	if err != nil {
		return err
	}
	png, err := s.Simctl.Screenshot(r.Context(), udid)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
	return nil
}

// view is a simulator's screen as a WebSocket in internal/devicestream's wire
// format, the app's DeviceStreamView's: config and frame messages from the
// helper, a status message before it closes on a simhost that stopped, and
// the viewer's touches, keys, text, wheel and buttons to it (fromViewer).
func (s *Server) view(w http.ResponseWriter, r *http.Request) error {
	udid, err := udidOf(r)
	if err != nil {
		return err
	}
	h, err := s.hosts.get(r.Context(), udid)
	if err != nil {
		return err
	}
	// Only the daemon gets this far, with the token; what it relays is the
	// app's, from any origin.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stopped := func(err error) {
		write, done := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer done()
		_ = conn.Write(write, websocket.MessageBinary, devicestream.EncodeStatus(err))
		_ = conn.Close(websocket.StatusInternalError, "")
	}
	v, leave, err := h.watch()
	defer leave()
	if err != nil {
		stopped(err)
		return nil
	}
	go func() {
		defer cancel()
		// The user's finger holds the simulator from down to up, so an
		// agent's tap doesn't land in the middle of their drag.
		holding, fingerDown := false, false
		var lastX, lastY float64
		defer func() {
			if fingerDown { // the viewer left mid-drag: lift the finger
				_ = h.send(op{Op: "touch", Phase: "up", X: lastX, Y: lastY})
			}
			if holding {
				h.acting.Unlock()
			}
		}()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			in, err := devicestream.DecodeInput(data)
			if err != nil {
				continue
			}
			if in.Type == devicestream.InputKeyframe {
				_ = h.send(op{Op: "keyframe"})
				continue
			}
			planned, steps, err := fromViewer(in)
			touch := planned != nil && planned.Type == "touch"
			if !holding {
				h.acting.Lock()
				holding = true
			}
			if err == nil && planned != nil {
				err = s.apply(ctx, h, *planned)
			} else if err == nil {
				err = h.run(ctx, steps)
			}
			if touch {
				fingerDown, lastX, lastY = planned.Phase != "up", planned.X, planned.Y
			}
			if !fingerDown { // a key mid-drag keeps the drag's hold
				h.acting.Unlock()
				holding = false
			}
			// A key the simulator doesn't know is no reason to stop watching.
			if err != nil && ctx.Err() == nil {
				log.Printf("ios-helper: %s: %s: %v", udid, in.Type, err)
			}
		}
	}()
	for {
		select {
		case m := <-v.out:
			if err := conn.Write(ctx, websocket.MessageBinary, m); err != nil {
				return nil
			}
		case <-h.proc.Done:
			stopped(fmt.Errorf("the simulator's helper stopped: %s", h.why()))
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

// ui reads a simulator's screen, or acts on it and says what changed. An
// action holds the simulator from its first input until the screen settles,
// so another agent's or the user's input doesn't land in between.
func (s *Server) ui(r *http.Request, udid string) (any, error) {
	var req api.IOSUIRequest
	if err := readJSON(r, &req); err != nil {
		return nil, err
	}
	ctx := r.Context()
	h, err := s.hosts.get(ctx, udid)
	if err != nil {
		return nil, err
	}
	read := func(ctx context.Context) (*snapshot, error) {
		var t axTree
		if err := h.request(ctx, map[string]any{"op": "tree"}, 10*time.Second, &t); err != nil {
			return nil, fmt.Errorf("reading the screen: %w", err)
		}
		return newSnapshot(t), nil
	}
	hit := func(ctx context.Context, points [][2]float64) ([]*axElement, error) {
		var out struct {
			Hits []*axElement `json:"hits"`
		}
		err := h.request(ctx, map[string]any{"op": "hit", "points": points}, 5*time.Second+time.Duration(len(points))*50*time.Millisecond, &out)
		return out.Hits, err
	}
	checked := func(ctx context.Context) (*snapshot, error) {
		snap, err := read(ctx)
		if err == nil {
			err = snap.check(ctx, hit)
		}
		return snap, err
	}
	result := func(snap *snapshot, text string) (any, error) {
		return api.IOSUIResult{Screen: snap.title, Text: text}, nil
	}
	switch req.Action {
	case "":
		snap, err := read(ctx)
		if err != nil {
			return nil, err
		}
		if err := snap.check(ctx, hit); err != nil {
			return result(snap, snap.render(req.All)+"(couldn't check what a tap reaches: "+err.Error()+")\n")
		}
		return result(snap, snap.render(req.All))
	case "wait":
		return s.uiWait(ctx, req, read, hit)
	case "tap", "type", "scroll-to":
	default:
		return nil, badRequest("unknown action %q: use tap, type, scroll-to or wait", req.Action)
	}
	h.acting.Lock()
	defer h.acting.Unlock()
	before, err := checked(ctx)
	if err != nil {
		return nil, err
	}
	var target *uiElement
	if req.Target != "" {
		if target, err = before.findTarget(req.Target, req.Near, req.Action == "type"); err != nil {
			return nil, badRequest("%v", err)
		}
	}
	tap := func(e *uiElement) error {
		if err := e.reachable(req.Target, req.Force); err != nil {
			return badRequest("%v", err)
		}
		x, y := e.tapPoint()
		return s.apply(ctx, h, api.IOSInput{Type: "tap", X: x / before.screen.w, Y: y / before.screen.h})
	}
	did := ""
	switch req.Action {
	case "tap":
		if target == nil {
			return nil, badRequest("say what to tap: id:<id> or its label")
		}
		did = "Tapped " + before.line(target, false) + "\n"
		err = tap(target)
	case "type":
		if target != nil {
			if err = tap(target); err == nil {
				err = s.uiSync(ctx, h)
			}
			if err == nil {
				err = focused(ctx, read, before, target)
			}
			if err == nil { // a tap into a field may select a word, which the text would replace
				err = s.apply(ctx, h, api.IOSInput{Type: "key", Key: "down", Modifiers: []string{"command"}})
			}
		}
		if err == nil {
			err = s.apply(ctx, h, api.IOSInput{Type: "text", Text: req.Text})
		}
	case "scroll-to":
		if target == nil {
			return nil, badRequest("say what to scroll to: id:<id> or its label")
		}
		if target.where == onScreen && target.tappable {
			return result(before, fmt.Sprintf("%s is already on the screen\n", before.line(target, false)))
		}
		err = h.request(ctx, map[string]any{"op": "scroll", "index": target.index, "identifier": target.Identifier, "label": target.Label}, 5*time.Second, nil)
	}
	if err != nil {
		return nil, err
	}
	if err := s.uiSync(ctx, h); err != nil {
		return nil, err
	}
	after, changed, moving, err := settle(ctx, read, before)
	if err != nil {
		return nil, err
	}
	full := func() string {
		if err := after.check(ctx, hit); err != nil {
			return after.render(false) + "(couldn't check what's covered: " + err.Error() + ")\n"
		}
		return after.render(false)
	}
	text := did + change(before, after, changed, moving, full)
	unchanged := !changed
	if req.Action == "scroll-to" {
		if err := after.check(ctx, hit); err == nil {
			if e, err := after.findTarget(req.Target, req.Near, false); err == nil {
				if e.where == onScreen && e.tappable {
					text = after.line(e, false) + " is on the screen now\n"
				} else {
					text = after.line(e, false) + ": scrolling didn't bring it fully into view; take a screenshot\n"
				}
				unchanged = false
			}
		}
	}
	return api.IOSUIResult{Screen: after.title, Text: text, Unchanged: unchanged}, nil
}

// uiSync waits until the input sent so far has reached the simulator.
func (s *Server) uiSync(ctx context.Context, h *host) error {
	return h.request(ctx, map[string]any{"op": "sync"}, 5*time.Second, nil)
}

// uiWait reads the screen until the target is on it, or with Gone isn't.
// focused waits for a field the agent tapped to type into to take the
// keyboard's focus: text sent before it does loses its first characters.
func focused(ctx context.Context, read func(context.Context) (*snapshot, error), before *snapshot, target *uiElement) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap, err := read(ctx)
		if err != nil {
			return err
		}
		for _, e := range snap.elements {
			if e.Focused && (target.Identifier != "" && e.Identifier == target.Identifier || target.Identifier == "" && e.index == target.index && e.Role == target.Role) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return badRequest("tapped %s but it didn't take the keyboard focus, so nothing was typed: agentbox ios tap it, then agentbox ios type without --into", before.line(target, false))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (s *Server) uiWait(ctx context.Context, req api.IOSUIRequest, read func(context.Context) (*snapshot, error), hit hitter) (any, error) {
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	timeout = min(timeout, time.Minute)
	deadline := time.Now().Add(timeout)
	for {
		snap, err := read(ctx)
		if err != nil {
			return nil, err
		}
		found, _ := snap.matches(strings.TrimSpace(req.Target), false, true)
		var e *uiElement
		for _, f := range found {
			if f.where == onScreen {
				e = f
				break
			}
		}
		present := e != nil
		if present != req.Gone {
			text := fmt.Sprintf("%q is gone\n", req.Target)
			if present {
				text = snap.line(e, false) + " is on the screen\n"
			}
			return api.IOSUIResult{Screen: snap.title, Text: text}, nil
		}
		if time.Now().After(deadline) {
			what := "appear"
			if req.Gone {
				what = "go away"
			}
			_ = snap.check(ctx, hit) // best effort: without it the screen still reads
			return nil, &httpError{code: http.StatusRequestTimeout, msg: fmt.Sprintf("%q didn't %s in %s. The screen now:\n%s", req.Target, what, timeout, strings.TrimRight(snap.render(false), "\n"))}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
}
