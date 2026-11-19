package daemon

import (
	"context"
	"net/http"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A chat's tab groups (state/sessiongroups.go): each a button in the row
// above the chat, its sessions in a menu. Kept here, as pins and the tabs'
// order are, so every computer and the phone see the same row. A change to
// one is a change to the sessions' list (EventLeadSessions), which the app
// reads both again on; the briefs don't name groups, so none is rewritten.

func apiSessionGroup(g state.SessionGroup) api.LeadSessionGroup {
	return api.LeadSessionGroup{Project: g.Project, ID: g.ID, Name: g.Name, Color: g.Color, Collapsed: g.Collapsed, Position: g.Position}
}

func (s *Server) apiSessionGroups(ctx context.Context, project string) ([]api.LeadSessionGroup, error) {
	groups, err := s.store.SessionGroups(ctx, project)
	if err != nil {
		return nil, err
	}
	out := make([]api.LeadSessionGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, apiSessionGroup(g))
	}
	return out, nil
}

func (s *Server) groupsChanged(project string) {
	s.events.publish(api.EventLeadSessions, api.LeadSessionsChange{Project: project})
}

// listSessionGroups answers GET /v1/projects/{project}/session-groups.
func (s *Server) listSessionGroups(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if err := s.chatProject(r.Context(), project); err != nil {
		return err
	}
	out, err := s.apiSessionGroups(r.Context(), project)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// newSessionGroup answers POST /v1/projects/{project}/session-groups: a
// group made of sessions, after the others.
func (s *Server) newSessionGroup(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if err := s.chatProject(r.Context(), project); err != nil {
		return err
	}
	var req api.NewLeadSessionGroup
	if err := readJSON(r, &req); err != nil {
		return err
	}
	g, err := s.store.AddSessionGroup(r.Context(), project, req.Name, req.Color, req.Sessions)
	if err != nil {
		return err
	}
	s.groupsChanged(project)
	return writeJSON(w, http.StatusCreated, apiSessionGroup(g))
}

// updateSessionGroup answers PATCH
// /v1/projects/{project}/session-groups/{group}: a new name or colour, or
// folding or spreading its tabs.
func (s *Server) updateSessionGroup(w http.ResponseWriter, r *http.Request) error {
	project, id := r.PathValue("project"), r.PathValue("group")
	var req api.LeadSessionGroupPatch
	if err := readJSON(r, &req); err != nil {
		return err
	}
	g, err := s.store.UpdateSessionGroup(r.Context(), project, id, state.SessionGroupPatch{Name: req.Name, Color: req.Color, Collapsed: req.Collapsed})
	if err != nil {
		return err
	}
	s.groupsChanged(project)
	return writeJSON(w, http.StatusOK, apiSessionGroup(g))
}

// ungroupSessions answers DELETE
// /v1/projects/{project}/session-groups/{group}: the group goes, its
// sessions stay as loose tabs.
func (s *Server) ungroupSessions(w http.ResponseWriter, r *http.Request) error {
	project, id := r.PathValue("project"), r.PathValue("group")
	if err := s.store.DeleteSessionGroup(r.Context(), project, id); err != nil {
		return err
	}
	s.groupsChanged(project)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// orderSessionGroups answers PUT
// /v1/projects/{project}/session-groups/order: the order the groups were
// dragged into. It answers the groups in it.
func (s *Server) orderSessionGroups(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if err := s.chatProject(r.Context(), project); err != nil {
		return err
	}
	var req api.LeadSessionGroupOrder
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := s.store.OrderSessionGroups(r.Context(), project, req.IDs); err != nil {
		return err
	}
	s.groupsChanged(project)
	out, err := s.apiSessionGroups(r.Context(), project)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}
