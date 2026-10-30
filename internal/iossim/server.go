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
	Roots []string
	hosts *hosts

	// The Xcode check, which runs two commands, for a moment: the app asks
	// for the status every few seconds while its iOS view is open.
	xcodeMu  sync.Mutex
	xcodeAt  time.Time
	xcode    string
	xcodeErr error
}

// NewServer makes the helper's API, starting simhosts with launch.
func NewServer(token, version string, simctl Simctl, roots []string, launch Launcher) *Server {
	return &Server{Token: token, Version: version, Simctl: simctl, Roots: roots, hosts: newHosts(launch, 2*time.Minute)}
}

// Run stops simhosts nothing uses, until ctx ends.
func (s *Server) Run(ctx context.Context) { s.hosts.reap(ctx) }

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
	h("GET /v1/devices/{udid}/screenshot", s.screenshot)
	h("GET /v1/devices/{udid}/view", s.view)
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
			if err == nil && planned != nil {
				err = s.apply(ctx, h, *planned)
			} else if err == nil {
				err = h.run(ctx, steps)
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
