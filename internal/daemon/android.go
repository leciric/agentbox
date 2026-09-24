package daemon

import (
	"net/http"
	"os"

	"agentbox/internal/agent"
	"agentbox/internal/android"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// findAndroidSDK looks for the Android SDK every time, so installing one needs
// no restart.
func findAndroidSDK() (android.SDK, error) {
	home, _ := os.UserHomeDir()
	return android.FindSDK(android.Candidates(os.Getenv, home))
}

func toAPIAndroid(st agent.AndroidStatus) api.AndroidStatus {
	return api.AndroidStatus{
		Available: st.Available, Problem: st.Problem, SDK: st.SDK, Images: st.Images,
		Running: st.Running, Booted: st.Booted, Image: st.Image, Device: st.Device,
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

func (s *Server) androidView(w http.ResponseWriter, r *http.Request) error {
	return s.vncView(w, r, s.manager(nil).DialAndroidView)
}
