package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/omarchy"
	"agentbox/internal/state"
)

// AgentBox wears the theme of the desktop it runs on. The daemon is the only
// part that can read it — it is the half that runs on the host, beside the
// user's ~/.config, while the app's window and every agent's desktop are
// elsewhere — so it watches the theme, reports it on /v1/theme, and publishes
// api.EventTheme when it moves. The app restyles itself from that; each agent
// gets the colours written into it the way the wallpaper is (agent/theme.go).
//
// D67's line holds: what goes into an agent is dock, window-decoration and
// backdrop colour. Nothing here touches GTK's dark preference, which Chromium
// maps onto prefers-color-scheme and would use to dark-mode every page the
// agent browses.
//
// A machine with no Omarchy on it is the ordinary case, not an error: the
// watcher finds nothing, /v1/theme says so, and AgentBox looks like AgentBox.

// watchTheme starts following the host's theme, for as long as the daemon
// runs. It is started whether or not anyone is subscribed to the event stream,
// unlike watch(): the theme is also what agents are painted in, and an agent's
// desktop subscribes to nothing.
//
// The watcher is built here, before the API is served, so that every later
// reader finds either a watcher or the nil this leaves on a machine with no
// home directory to read — and never a field being assigned underneath it.
func (s *Server) watchTheme(ctx context.Context) {
	home, err := os.UserHomeDir()
	if err != nil {
		// No home to read a theme out of. Nothing else depends on this, so it
		// stops here rather than taking anything with it.
		s.logf("the host's desktop theme isn't being followed: %v", err)
		return
	}
	s.themes = omarchy.NewWatcher(home)
	if p, ok := s.themes.Palette(); ok {
		s.logf("this machine's desktop theme is %s, and AgentBox follows it; turn that off in Settings", p.Name)
	}
	go s.themes.Run(ctx, func() { s.themeChanged(ctx) })
}

// themeChanged tells the app, and repaints the desktop of every agent that has
// one up. Agents are done in the background: a theme change is a cosmetic
// event and must not hold the watcher, and an agent that is busy, wedged or
// mid-snapshot is not a reason for any of it to fail.
func (s *Server) themeChanged(ctx context.Context) {
	theme := s.theme(ctx)
	s.events.publish(api.EventTheme, theme)
	go s.repaintAgents(ctx, theme)
}

func (s *Server) repaintAgents(ctx context.Context, theme api.Theme) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	m := s.manager(nil)
	agents, err := m.List(ctx, "")
	if err != nil {
		return
	}
	for _, st := range agents {
		if st.State != "running" {
			continue
		}
		a, err := m.Get(ctx, st.Ref())
		if err != nil {
			continue
		}
		if err := m.ApplyTheme(ctx, a); err != nil {
			s.logf("%s: the desktop wasn't repainted in %s: %v", st.Ref(), theme.Describe(), err)
		}
	}
}

// theme is what AgentBox should wear: the theme this machine is running, and
// the appearance setting, which says whether to follow it. Any failure reading
// the setting is reported as "following", because that is the default and a
// setting that can't be read is not a reason to change how anything looks.
func (s *Server) theme(ctx context.Context) api.Theme {
	appearance, err := s.store.Appearance(ctx)
	if err != nil {
		s.logf("the appearance setting couldn't be read: %v", err)
		appearance = api.AppearanceFollow
	}
	out := api.Theme{Appearance: appearance}
	if s.themes == nil {
		return out
	}
	p, ok := s.themes.Palette()
	if !ok {
		return out
	}
	out.Available = true
	out.Name, out.Mode = p.Name, p.Mode
	out.Background, out.Surface = p.Background, p.Surface
	out.Foreground, out.Muted, out.Accent = p.Foreground, p.Muted, p.Accent
	return out
}

// desktopTheme is the palette agents' desktops are painted in: the host's when
// it is being followed, and AgentBox's own otherwise — including on every
// machine that isn't running a desktop AgentBox can read.
func (s *Server) desktopTheme() agent.DesktopTheme {
	t := s.theme(context.Background())
	if !t.Applied() {
		return agent.BrandDesktopTheme
	}
	return agent.DesktopTheme{
		Name: t.Name, Mode: t.Mode,
		Background: t.Background, Surface: t.Surface,
		Foreground: t.Foreground, Muted: t.Muted, Accent: t.Accent,
	}
}

func (s *Server) themeStatus(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, s.theme(r.Context()))
}

// updateTheme sets the appearance: follow the desktop's theme, or keep
// AgentBox's own colours light or dark. Not following is how someone who wants
// AgentBox's own look keeps it on a machine that does have a theme to follow,
// and light or dark is how they say which way round that look should be.
func (s *Server) updateTheme(w http.ResponseWriter, r *http.Request) error {
	var req api.UpdateThemeRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Appearance != nil {
		switch *req.Appearance {
		case api.AppearanceFollow, api.AppearanceLight, api.AppearanceDark:
		default:
			return fmt.Errorf("appearance is %q: it must be follow, light or dark", *req.Appearance)
		}
		if err := s.store.SetSetting(r.Context(), state.SettingAppearance, *req.Appearance); err != nil {
			return err
		}
		// The same path a theme change takes: the app restyles, and agents
		// that are up go back to AgentBox's colours, or take on the host's.
		s.themeChanged(s.themeContext())
	}
	return writeJSON(w, http.StatusOK, s.theme(r.Context()))
}

// themeContext is a context that outlives the request that started the
// repaint: the agents are gone round in the background, and the request's own
// context is cancelled as soon as its answer is written.
func (s *Server) themeContext() context.Context {
	if s.runCtx != nil {
		return s.runCtx
	}
	return context.Background()
}
