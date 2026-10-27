package daemon

import (
	"net/http"
	"os"

	"github.com/coder/websocket"

	"agentbox/internal/agent"
	"agentbox/internal/android"
	"agentbox/internal/api"
	"agentbox/internal/devicestream"
	"agentbox/internal/hostos"
	"agentbox/internal/state"
)

// findAndroidSDK looks for the Android SDK every time, so installing one needs
// no restart. In AgentBox's Cloud Hypervisor VM, the SDK is the host's: the
// front end finds it with the host's settings and says where in
// AGENTBOX_ANDROID_SDK (hostvm's vmEnv), and the usual place is in the host's
// home, which the VM shares at the same path, not in the VM user's own.
func findAndroidSDK() (android.SDK, error) {
	if share := hostos.Home(); hostos.OS() == hostos.Linux && share != "" {
		return android.FindSharedSDK(android.Candidates(os.Getenv, share), share)
	}
	home, _ := os.UserHomeDir()
	return android.FindSDK(android.Candidates(os.Getenv, home))
}

func toAPIAndroid(st agent.AndroidStatus) api.AndroidStatus {
	return api.AndroidStatus{
		Available: st.Available, Problem: st.Problem, SDK: st.SDK, Images: st.Images,
		Running: st.Running, Booted: st.Booted, Image: st.Image, Device: st.Device,
		GPU: st.GPU, GPUProblem: st.GPUProblem,
	}
}

// android serves an agent's emulator endpoints. source is "agent" on the
// agent's own socket, which doesn't see host paths.
func (s *Server) android(action string, agentOf func(*http.Request) (state.Agent, error), source string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := agentOf(r)
		if err != nil {
			return err
		}
		ctx, m := r.Context(), s.manager(s.cfg.Log)
		switch action {
		case "start":
			var req api.AndroidStartRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			if _, err := m.StartAndroid(ctx, a, agent.AndroidOptions{Image: req.Image, MemoryMB: req.MemoryMB, Cores: req.Cores}); err != nil {
				return err
			}
		case "stop":
			if err := m.StopAndroid(ctx, a); err != nil {
				return err
			}
		case "install":
			var req api.AndroidInstallRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			out, err := m.InstallAPK(ctx, a, req.Path)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, api.AndroidInstallResult{Output: out})
		}
		status, err := m.AndroidStatus(ctx, a)
		if err != nil {
			return err
		}
		out := toAPIAndroid(status)
		if source == "agent" {
			out.SDK = ""
		}
		return writeJSON(w, http.StatusOK, out)
	}
}

// androidStream relays an agent's emulator's stream (devicestream: H.264
// video out, input in) between the app and the stream server in the agent,
// message for message. Like the VNC views, it is reached only through the
// daemon's socket, and the agent's server only through its proxy device.
func (s *Server) androidStream(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	defer s.viewIdle(a.Ref())()
	conn, err := s.manager(nil).DialAndroidStream(r.Context(), a)
	if err != nil {
		return err
	}
	device, err := devicestream.Dial(r.Context(), conn)
	if err != nil {
		return err
	}
	defer func() { _ = device.CloseNow() }()

	// The socket is only reachable by this user, so any origin (the desktop app's) is fine.
	viewer, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil // Accept has already written the response
	}
	defer func() { _ = viewer.CloseNow() }()
	viewer.SetReadLimit(1 << 20)
	if err := devicestream.Relay(r.Context(), viewer, device); err != nil {
		s.logf("%s's Android stream: %v", a.Ref(), err)
	}
	_ = viewer.Close(websocket.StatusNormalClosure, "")
	return nil
}
