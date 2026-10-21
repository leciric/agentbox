package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
)

// fakeInAgentAPI serves the in-agent API's /v1/self and DELETE
// /v1/self/media/{id} on a socket that AGENTBOX_IN_AGENT_SOCKET points at, the
// rest answered the way the real one answers what isn't an agent's to reach.
func fakeInAgentAPI(t *testing.T, deleteStatus int, deleteError string) *[]string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "s")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", sock)
	var mu sync.Mutex
	var calls []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/self", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.Self{Ref: "pawly/agent-01", Project: "pawly", Agent: "agent-01"})
	})
	mux.HandleFunc("DELETE /v1/self/media/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if deleteStatus != 0 {
			w.WriteHeader(deleteStatus)
			_ = json.NewEncoder(w).Encode(api.Error{Error: deleteError})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(api.Error{Error: "not available inside an agent"})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &calls
}

func runMediaRm(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newMediaRmCmd(&app{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// An agent naming itself before the id, the host form, went to the host's
// API, which isn't in an agent: the CLI tried to start a daemon there and
// failed on "permission denied" (sai-app-4's agents, 20 Oct). It deletes on the
// agent's own socket, whichever way it names itself.
func TestMediaRmInsideAnAgentNamingItself(t *testing.T) {
	for _, ref := range []string{"pawly/agent-01", "agent-01"} {
		t.Run(ref, func(t *testing.T) {
			calls := fakeInAgentAPI(t, 0, "")
			out, err := runMediaRm(t, ref, "20261020T140000-abcdef")
			if err != nil {
				t.Fatalf("media rm %s <id> = %v (%s)", ref, err, out)
			}
			if want := []string{"DELETE /v1/self/media/20261020T140000-abcdef"}; strings.Join(*calls, ",") != strings.Join(want, ",") {
				t.Errorf("calls = %q, want %q", *calls, want)
			}
			if !strings.Contains(out, "Deleted 20261020T140000-abcdef") {
				t.Errorf("output = %q", out)
			}
		})
	}
}

// What the daemon said is what the agent reads, with what was being done.
func TestMediaRmSaysTheServersError(t *testing.T) {
	fakeInAgentAPI(t, http.StatusBadRequest, "files left behind: unlinkat /media/x: permission denied")
	_, err := runMediaRm(t, "20261020T140000-abcdef")
	if err == nil || err.Error() != "deleting 20261020T140000-abcdef: files left behind: unlinkat /media/x: permission denied" {
		t.Errorf("media rm <id> = %v", err)
	}
}
