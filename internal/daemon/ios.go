package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"agentbox/internal/api"
	"agentbox/internal/devicestream"
	"agentbox/internal/hostos"
	"agentbox/internal/iossim"
	"agentbox/internal/state"
)

// iosState is the iOS Simulator as the daemon sees it: the Mac's helper,
// found from the state file the front end names (iossim.StateFileEnv), and
// the simulator each agent opened last, which its commands and its panel's
// view use when they don't name one. The simulators are the Mac's, shared by
// every agent: two agents may well drive the same one.
type iosState struct {
	once    sync.Once
	client  *iossim.Client
	mu      sync.Mutex
	devices map[string]string // agent ref → UDID
}

func (s *Server) iosClient() *iossim.Client {
	s.iosSim.once.Do(func() {
		if s.iosSim.client == nil {
			s.iosSim.client = iossim.NewClient(os.Getenv(iossim.StateFileEnv))
		}
	})
	return s.iosSim.client
}

func (s *Server) iosCurrent(ref string) string {
	s.iosSim.mu.Lock()
	defer s.iosSim.mu.Unlock()
	return s.iosSim.devices[ref]
}

func (s *Server) setIOSCurrent(ref, udid string) {
	s.iosSim.mu.Lock()
	defer s.iosSim.mu.Unlock()
	if s.iosSim.devices == nil {
		s.iosSim.devices = map[string]string{}
	}
	s.iosSim.devices[ref] = udid
}

// iosStatus is what an agent sees of the simulators. Not on a Mac, or
// without the helper or Xcode, it says why instead.
func (s *Server) iosStatus(ctx context.Context, a state.Agent) api.IOSStatus {
	st := api.IOSStatus{Devices: []api.IOSDevice{}}
	if hostos.OS() != "darwin" {
		st.Problem = "The iOS Simulator runs on a Mac with Xcode, and AgentBox here doesn't run on a Mac."
		return st
	}
	c := s.iosClient()
	h, err := c.Health(ctx)
	if err != nil {
		st.Problem = iossim.Problem(err)
		return st
	}
	st.Xcode = h.Xcode
	if h.Problem != "" {
		st.Problem = h.Problem
		return st
	}
	devices, err := c.Devices(ctx)
	if err != nil {
		st.Problem = err.Error()
		return st
	}
	st.Available, st.Devices, st.Device = true, devices, s.iosCurrent(a.Ref())
	return st
}

// iosDevice is the simulator a request means: the one it names, by UDID or
// by name (the booted one of that name first); else the agent's own; else the
// only one booted. Opening one picks the newest iPhone when nothing else
// says which.
func (s *Server) iosDevice(ctx context.Context, a state.Agent, named string, opening bool) (string, error) {
	if named == "" {
		if udid := s.iosCurrent(a.Ref()); udid != "" {
			return udid, nil
		}
	}
	if iossim.ValidUDID(named) {
		return named, nil
	}
	devices, err := s.iosClient().Devices(ctx)
	if err != nil {
		return "", err
	}
	if named != "" {
		for _, d := range devices { // booted first
			if strings.EqualFold(d.Name, named) {
				return d.UDID, nil
			}
		}
		return "", fmt.Errorf("no simulator named %q: agentbox ios list shows them", named)
	}
	var booted []api.IOSDevice
	for _, d := range devices {
		if d.State == "Booted" {
			booted = append(booted, d)
		}
	}
	switch {
	case len(booted) == 1:
		return booted[0].UDID, nil
	case len(booted) > 1:
		return "", errors.New("several simulators are booted: say which with --device")
	case opening:
		for _, d := range devices {
			if strings.HasPrefix(d.Name, "iPhone") {
				return d.UDID, nil
			}
		}
		if len(devices) > 0 {
			return devices[0].UDID, nil
		}
		return "", errors.New("there are no iOS simulators: add one in Xcode's Devices and Simulators window")
	}
	return "", errors.New("no simulator is booted: open one first (agentbox ios open)")
}

// iosAppPath is an app to install as the Mac sees it: an agent's paths are
// its worktree's, which the Mac has at the same path, and a relative one is
// in its worktree.
func iosAppPath(a state.Agent, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.Worktree, path)
	}
	path = filepath.Clean(path)
	if rel, err := filepath.Rel(a.Worktree, path); err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%s isn't in %s's worktree, which is all the Mac can install from", path, a.Ref())
	}
	return path, nil
}

// iosActions are the POST routes under an agent's /ios.
var iosActions = []string{"open", "close", "input", "install", "launch", "openurl"}

// ios serves an agent's simulator endpoints, on the agent's own socket and
// under the agent for the app and the CLI alike.
func (s *Server) ios(action string, agentOf func(*http.Request) (state.Agent, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := agentOf(r)
		if err != nil {
			return err
		}
		ctx, c := r.Context(), s.iosClient()
		switch action {
		case "open", "close":
			var req api.IOSDeviceRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			udid, err := s.iosDevice(ctx, a, req.Device, action == "open")
			if err != nil {
				return err
			}
			if action == "open" {
				if err := c.Boot(ctx, udid); err != nil {
					return err
				}
				s.setIOSCurrent(a.Ref(), udid)
			} else if err := c.Shutdown(ctx, udid); err != nil {
				return err
			}
		case "input":
			var in api.IOSInput
			if err := readJSON(r, &in); err != nil {
				return err
			}
			udid, err := s.iosDevice(ctx, a, in.Device, false)
			if err != nil {
				return err
			}
			if err := c.Input(ctx, udid, in); err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, struct{}{})
		case "install":
			var req api.IOSInstallRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			path, err := iosAppPath(a, req.Path)
			if err != nil {
				return err
			}
			udid, err := s.iosDevice(ctx, a, req.Device, false)
			if err != nil {
				return err
			}
			if err := c.Install(ctx, udid, path); err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, api.IOSResult{Output: "Installed " + filepath.Base(path)})
		case "launch":
			var req api.IOSLaunchRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			udid, err := s.iosDevice(ctx, a, req.Device, false)
			if err != nil {
				return err
			}
			out, err := c.Launch(ctx, udid, req)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, out)
		case "openurl":
			var req api.IOSOpenURLRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			udid, err := s.iosDevice(ctx, a, req.Device, false)
			if err != nil {
				return err
			}
			if err := c.OpenURL(ctx, udid, req.URL); err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, struct{}{})
		}
		return writeJSON(w, http.StatusOK, s.iosStatus(ctx, a))
	}
}

// iosScreenshot is a PNG of the agent's simulator, for its media
// (agent.Manager's IOSScreenshot).
func (s *Server) iosScreenshot(ctx context.Context, a state.Agent) ([]byte, error) {
	udid, err := s.iosDevice(ctx, a, "", false)
	if err != nil {
		return nil, err
	}
	return s.iosClient().Screenshot(ctx, udid)
}

// iosView relays a simulator's screen between the app and the helper,
// message for message, as androidStream does an emulator's: the helper
// speaks internal/devicestream's wire format. ?device= names it, or else
// it's the agent's.
func (s *Server) iosView(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	udid, err := s.iosDevice(r.Context(), a, r.URL.Query().Get("device"), false)
	if err != nil {
		return err
	}
	device, err := s.iosClient().View(r.Context(), udid)
	if err != nil {
		return err
	}
	defer func() { _ = device.CloseNow() }()
	s.setIOSCurrent(a.Ref(), udid)
	// The socket is only reachable by this user, so any origin (the desktop app's) is fine.
	viewer, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil // Accept has already written the response
	}
	defer func() { _ = viewer.CloseNow() }()
	viewer.SetReadLimit(1 << 20)
	if err := devicestream.Relay(r.Context(), viewer, device); err != nil {
		s.logf("%s's iOS stream: %v", a.Ref(), err)
	}
	_ = viewer.Close(websocket.StatusNormalClosure, "")
	return nil
}
