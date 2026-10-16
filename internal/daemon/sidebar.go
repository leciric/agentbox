package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// UI v2's sidebar's hidden projects (api.SidebarLayout), one JSON setting
// rather than a table: the whole of it is read and written at once, and
// nothing joins on it.

func (s *Server) sidebarLayout(w http.ResponseWriter, r *http.Request) error {
	out, err := s.readSidebarLayout(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) setSidebarLayout(w http.ResponseWriter, r *http.Request) error {
	var req api.SidebarLayout
	if err := readJSON(r, &req); err != nil {
		return err
	}
	known, err := s.projectNames(r.Context())
	if err != nil {
		return err
	}
	out := tidySidebarLayout(req, known)
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := s.store.SetSetting(r.Context(), state.SettingSidebarLayout, string(raw)); err != nil {
		return err
	}
	s.listChanged()
	return writeJSON(w, http.StatusOK, out)
}

// readSidebarLayout is the stored layout without the projects removed since
// it was written. One that won't parse reads as none, rather than leaving
// the sidebar with no projects.
func (s *Server) readSidebarLayout(ctx context.Context) (api.SidebarLayout, error) {
	raw, err := s.store.Setting(ctx, state.SettingSidebarLayout)
	if err != nil {
		return api.SidebarLayout{}, err
	}
	var stored api.SidebarLayout
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &stored)
	}
	known, err := s.projectNames(ctx)
	if err != nil {
		return api.SidebarLayout{}, err
	}
	return tidySidebarLayout(stored, known), nil
}

func (s *Server) projectNames(ctx context.Context) (map[string]bool, error) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(projects))
	for _, p := range projects {
		known[p.Name] = true
	}
	return known, nil
}

// tidySidebarLayout keeps each hidden project of known once. The list is
// never nil, so the JSON has [] rather than null.
func tidySidebarLayout(layout api.SidebarLayout, known map[string]bool) api.SidebarLayout {
	out := api.SidebarLayout{Hidden: []string{}}
	for _, p := range layout.Hidden {
		if known[p] && !slices.Contains(out.Hidden, p) {
			out.Hidden = append(out.Hidden, p)
		}
	}
	return out
}
