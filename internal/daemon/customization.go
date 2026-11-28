package daemon

import (
	"fmt"
	"io"
	"net/http"

	"agentbox/internal/api"
	"agentbox/internal/customize"
)

// The user's customization of the app's look and layout (internal/customize):
// served to the app, which wears it, and to the lead's and the Home chat's
// sockets at the same paths, whose appearance tool changes it when the user
// asks a chat to. Every change is published as api.EventCustomization, which
// the app restyles itself from as it arrives: asking a chat for a green
// accent turns the window green while the chat is answering.

// customizationRoutes are what a chat's appearance tool reaches: the
// customization, and the appearance setting (theme.go), the window's mode,
// which a chat asked for a light window sets.
func (s *Server) customizationRoutes(handle func(pattern string, fn func(http.ResponseWriter, *http.Request) error)) {
	handle("GET /v1/customization", s.getCustomization)
	handle("PATCH /v1/customization", s.patchCustomization)
	handle("DELETE /v1/customization", s.resetCustomization)
	handle("GET /v1/theme", s.themeStatus)
	handle("PATCH /v1/theme", s.updateTheme)
}

func (s *Server) getCustomization(w http.ResponseWriter, _ *http.Request) error {
	status := s.customization.Status()
	if status.Problem != "" {
		s.logf("customization: %s", status.Problem)
	}
	return writeJSON(w, http.StatusOK, status)
}

// maxCustomizationPatch bounds a change: the largest custom stylesheet, with
// room for the rest and JSON's escaping of it.
const maxCustomizationPatch = 4*customize.MaxCSS + 16<<10

// patchCustomization changes the customization by a JSON merge patch. What
// the schema refuses is a 400 that says why, and changes nothing.
func (s *Server) patchCustomization(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCustomizationPatch+1))
	if err != nil {
		return err
	}
	if len(body) > maxCustomizationPatch {
		return fmt.Errorf("the change is over %d KiB", maxCustomizationPatch>>10)
	}
	status, err := s.customization.Patch(body)
	if err != nil {
		return err
	}
	s.customizationChanged(status)
	return writeJSON(w, http.StatusOK, status)
}

// resetCustomization puts AgentBox's own look back: Settings → Appearance's
// Reset to default, and the appearance tool's reset.
func (s *Server) resetCustomization(w http.ResponseWriter, _ *http.Request) error {
	status, err := s.customization.Reset()
	if err != nil {
		return err
	}
	s.customizationChanged(status)
	return writeJSON(w, http.StatusOK, status)
}

func (s *Server) customizationChanged(status api.CustomizationStatus) {
	s.events.publish(api.EventCustomization, status)
}
