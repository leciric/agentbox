package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The app's customization is kept in the user's configuration, served to the
// app, changed through the API with every change published to the event
// stream (the app restyles from that), and refused with why when the schema
// doesn't allow it.
func TestCustomizationIsServedChangedAndPublished(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)

	status, err := d.client.Customization(ctx)
	if err != nil || status.Problem != "" || status.Customization.Version != api.CustomizationVersion || status.Path != d.paths.Customization() {
		t.Fatalf("Customization() = %+v, %v", status, err)
	}

	events, unsubscribe := d.srv.events.subscribe()
	defer unsubscribe()
	status, err = d.client.PatchCustomization(ctx, json.RawMessage(`{"theme":{"accent":"#22c55e"},"density":"compact"}`))
	if err != nil || status.Customization.Theme.Accent != "#22c55e" || status.Customization.Density != api.DensityCompact {
		t.Fatalf("PatchCustomization() = %+v, %v", status, err)
	}
	if _, err := os.Stat(d.paths.Customization()); err != nil {
		t.Errorf("the change wasn't written to the user's file: %v", err)
	}
	published := func() api.CustomizationStatus {
		t.Helper()
		select {
		case ev := <-events:
			if ev.Type != api.EventCustomization {
				t.Fatalf("event %q, want %q", ev.Type, api.EventCustomization)
			}
			var out api.CustomizationStatus
			if err := json.Unmarshal(ev.Data, &out); err != nil {
				t.Fatal(err)
			}
			return out
		default:
			t.Fatal("no customization event was published")
		}
		return api.CustomizationStatus{}
	}
	if got := published(); got.Customization.Theme.Accent != "#22c55e" {
		t.Errorf("the event carried %+v", got)
	}

	_, err = d.client.PatchCustomization(ctx, json.RawMessage(`{"density":"tight"}`))
	var se *api.StatusError
	if err == nil || !strings.Contains(err.Error(), `density is "tight": it is compact, comfortable`) || !errors.As(err, &se) || se.Code != http.StatusBadRequest {
		t.Errorf("a bad density: %v", err)
	}
	select {
	case ev := <-events:
		t.Errorf("a refused change was published: %s", ev.Type)
	default:
	}

	status, err = d.client.ResetCustomization(ctx)
	if err != nil || status.Customization.Density != "" || status.Customization.Theme.Accent != "" {
		t.Fatalf("ResetCustomization() = %+v, %v", status, err)
	}
	if got := published(); got.Customization.Density != "" {
		t.Errorf("the reset published %+v", got)
	}
}

// A project's chat and the Home chat reach the customization and the
// appearance setting on their own sockets, at the user's paths, for their
// appearance tool.
func TestChatsReachTheCustomization(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	for name, routes := range map[string]http.Handler{
		"lead": d.srv.leadRoutes("hello-stack", state.MigratedSession),
		"home": d.srv.homeRoutes(state.MigratedSession),
	} {
		call := func(method, path, body string) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
			return rec
		}
		if rec := call(http.MethodPatch, "/v1/customization", `{"sidebar":{"hidden":["jobs"]}}`); rec.Code != http.StatusOK {
			t.Errorf("%s: PATCH /v1/customization = %d %s", name, rec.Code, rec.Body)
		}
		if rec := call(http.MethodGet, "/v1/customization", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hidden":["jobs"]`) {
			t.Errorf("%s: GET /v1/customization = %d %s", name, rec.Code, rec.Body)
		}
		if rec := call(http.MethodPatch, "/v1/customization", `{"sidebar":{"hidden":["settings"]}}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "can't be hidden") {
			t.Errorf("%s: hiding settings = %d %s", name, rec.Code, rec.Body)
		}
		if rec := call(http.MethodPatch, "/v1/theme", `{"appearance":"light"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"appearance":"light"`) {
			t.Errorf("%s: PATCH /v1/theme = %d %s", name, rec.Code, rec.Body)
		}
		if rec := call(http.MethodDelete, "/v1/customization", ""); rec.Code != http.StatusOK {
			t.Errorf("%s: DELETE /v1/customization = %d %s", name, rec.Code, rec.Body)
		}
		// Nothing else of the daemon's settings comes with it.
		if rec := call(http.MethodPatch, "/v1/settings", `{}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s: PATCH /v1/settings = %d, want it refused", name, rec.Code)
		}
	}
}
