package daemon

import (
	"net/http"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Sections, and the order of the projects in them
// ([D79](../../docs/implementation/decisions.md#d79)).
//
// Every one of these ends by publishing a project event with no name on it,
// which is how a window that isn't the one that did it hears that the list
// changed rather than one project.

func sectionInfo(sec state.Section) api.Section {
	return api.Section{ID: sec.ID, Name: sec.Name, Position: sec.Position, Collapsed: sec.Collapsed, CreatedAt: sec.CreatedAt}
}

func (s *Server) listSections(w http.ResponseWriter, r *http.Request) error {
	sections, err := s.store.Sections(r.Context())
	if err != nil {
		return err
	}
	out := make([]api.Section, 0, len(sections))
	for _, sec := range sections {
		out = append(out, sectionInfo(sec))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) addSection(w http.ResponseWriter, r *http.Request) error {
	var req api.AddSectionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	sec, err := s.store.AddSection(r.Context(), req.Name, time.Now())
	if err != nil {
		return err
	}
	s.listChanged()
	return writeJSON(w, http.StatusCreated, sectionInfo(sec))
}

func (s *Server) updateSection(w http.ResponseWriter, r *http.Request) error {
	var req api.UpdateSectionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	sec, err := s.store.UpdateSection(r.Context(), r.PathValue("id"), req.Name, req.Collapsed)
	if err != nil {
		return err
	}
	s.listChanged()
	return writeJSON(w, http.StatusOK, sectionInfo(sec))
}

// removeSection deletes the section and nothing else: the projects that were
// in it go back to being in no section, and are still there.
func (s *Server) removeSection(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.RemoveSection(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	s.listChanged()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) setProjectLayout(w http.ResponseWriter, r *http.Request) error {
	var req api.ProjectLayout
	if err := readJSON(r, &req); err != nil {
		return err
	}
	layout := state.Layout{Loose: req.Loose}
	for _, sp := range req.Sections {
		layout.Sections = append(layout.Sections, state.SectionProjects{ID: sp.ID, Projects: sp.Projects})
	}
	if err := s.store.SetProjectLayout(r.Context(), layout); err != nil {
		return err
	}
	s.listChanged()
	projects, err := s.store.Projects(r.Context())
	if err != nil {
		return err
	}
	out := make([]api.Project, 0, len(projects))
	for _, p := range projects {
		out = append(out, projectInfo(p))
	}
	return writeJSON(w, http.StatusOK, out)
}

// listChanged says the projects list itself moved — a section, or an order —
// rather than any one project.
func (s *Server) listChanged() {
	s.events.publish(api.EventProject, api.ProjectChange{})
}
