package cli

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/customize"
	"agentbox/internal/mcp"
)

// The appearance tool, on the lead's and the Home chat's sockets, against a
// daemon holding the real schema (customize.Store): show reads what is set
// with the guide to every field, set changes it, a bad value comes back as
// the error the schema gives, and reset puts AgentBox's own back.
func TestAppearanceTool(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "lead.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	store := &customize.Store{Path: filepath.Join(t.TempDir(), customize.FileName)}
	var mu sync.Mutex
	var calls []string
	appearance := api.AppearanceFollow
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, status api.CustomizationStatus, err error) {
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(api.Error{Error: err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(status)
	}
	record := func(r *http.Request, body string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+body))
	}
	mux.HandleFunc("GET /v1/customization", func(w http.ResponseWriter, r *http.Request) {
		record(r, "")
		respond(w, store.Status(), nil)
	})
	mux.HandleFunc("PATCH /v1/customization", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		record(r, string(body))
		status, err := store.Patch(body)
		respond(w, status, err)
	})
	mux.HandleFunc("DELETE /v1/customization", func(w http.ResponseWriter, r *http.Request) {
		record(r, "")
		status, err := store.Reset()
		respond(w, status, err)
	})
	mux.HandleFunc("GET /v1/theme", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.Theme{Appearance: appearance})
	})
	mux.HandleFunc("PATCH /v1/theme", func(w http.ResponseWriter, r *http.Request) {
		var req api.UpdateThemeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		appearance = *req.Appearance
		_ = json.NewEncoder(w).Encode(api.Theme{Appearance: appearance})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	c := api.NewClient(socket)
	for _, tools := range [][]string{namesOf(projectTools(context.Background(), c)), namesOf(homeTools(context.Background(), c))} {
		if !strings.Contains(strings.Join(tools, " "), "appearance") {
			t.Fatalf("no appearance tool in %v", tools)
		}
	}
	tool := appearanceTool(context.Background(), c)
	run := func(args string) (string, error) {
		t.Helper()
		return tool.Run(json.RawMessage(args))
	}
	must := func(args string) string {
		t.Helper()
		out, err := run(args)
		if err != nil {
			t.Fatalf("appearance(%s) = %v", args, err)
		}
		return out
	}

	out := must(`{"action":"show"}`)
	for _, want := range []string{"Window: follow (no desktop theme found", "Nothing is customized", "theme.accent", "sidebar.hidden", "density"} {
		if !strings.Contains(out, want) {
			t.Errorf("show didn't say %q:\n%s", want, out)
		}
	}

	out = must(`{"action":"set","changes":{"theme":{"accent":"#22C55E"},"density":"compact","sidebar":{"hidden":["jobs"]}}}`)
	if !strings.Contains(out, "The app shows it now") || !strings.Contains(out, `"accent": "#22c55e"`) || !strings.Contains(out, `"density": "compact"`) || strings.Contains(out, `"light"`) {
		t.Errorf("set said %q", out)
	}
	if got := store.Status().Customization; got.Theme.Accent != "#22c55e" || got.Density != api.DensityCompact || len(got.Sidebar.Hidden) != 1 {
		t.Errorf("set stored %+v", got)
	}

	// A model that sends the object as a string of JSON is understood, and
	// appearance sets the window's mode beside it.
	out = must(`{"action":"set","changes":"{\"fonts\":{\"size\":16}}","appearance":"light"}`)
	if store.Status().Customization.Fonts.Size != 16 || appearance != api.AppearanceLight || !strings.Contains(out, "Window: light.") {
		t.Errorf("set with a string and appearance said %q, stored %+v, appearance %s", out, store.Status().Customization, appearance)
	}

	// A bad value is refused with why, and nothing changes.
	_, err = run(`{"action":"set","changes":{"theme":{"accent":"green"},"density":"spacious"}}`)
	if err == nil || !strings.Contains(err.Error(), `nothing was changed: theme.accent is "green": a colour is #rrggbb`) {
		t.Errorf("a bad colour: %v", err)
	}
	_, err = run(`{"action":"set","changes":{"sidebar":{"hidden":["settings"]}}}`)
	if err == nil || !strings.Contains(err.Error(), "can't be hidden") {
		t.Errorf("hiding settings: %v", err)
	}
	if got := store.Status().Customization.Density; got != api.DensityCompact {
		t.Errorf("a refused change changed density to %q", got)
	}
	if _, err := run(`{"action":"set"}`); err == nil || !strings.Contains(err.Error(), "set needs changes") {
		t.Errorf("set with nothing: %v", err)
	}
	if _, err := run(`{"action":"set","changes":"compact"}`); err == nil || !strings.Contains(err.Error(), "changes is an object") {
		t.Errorf("set with a bare string: %v", err)
	}

	// reset puts one part back, or everything.
	out = must(`{"action":"reset","part":"sidebar"}`)
	if got := store.Status().Customization; len(got.Sidebar.Hidden) != 0 || got.Density != api.DensityCompact || !strings.Contains(out, "The sidebar is back") {
		t.Errorf("reset sidebar said %q and left %+v", out, got)
	}
	out = must(`{"action":"reset"}`)
	if !strings.Contains(out, "Everything is back to AgentBox's own. Nothing is customized") {
		t.Errorf("reset said %q", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if last := calls[len(calls)-1]; last != "DELETE /v1/customization" {
		t.Errorf("reset called %s", last)
	}
}

func namesOf(tools []mcp.Tool) []string {
	var out []string
	for _, tool := range tools {
		out = append(out, tool.Name)
	}
	return out
}
