package daemon

import (
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime/debug"
	"testing"

	"agentbox/internal/incus"
	"agentbox/internal/paths"
)

// The profiler is on the socket and nowhere else: routes() is also what
// phones on the LAN and the hub are served.
func TestPprofIsOnTheSocketOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}
	srv, err := New(Config{Paths: p, Incus: incus.Client{Bin: "false"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.store.Close() })
	get := func(h http.Handler, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	socket := srv.socketHandler()
	if w := get(socket, "/debug/pprof/heap?gc=1"); w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Fatalf("socket /debug/pprof/heap = %d, %d bytes", w.Code, w.Body.Len())
	}
	if w := get(socket, "/v1/version"); w.Code != http.StatusOK {
		t.Fatalf("socket /v1/version = %d: the API must still be served beside the profiler", w.Code)
	}
	for name, h := range map[string]http.Handler{"routes": srv.routes(), "hub": srv.remoteHandler()} {
		if w := get(h, "/debug/pprof/heap"); w.Code == http.StatusOK {
			t.Errorf("%s serves /debug/pprof/heap; only the socket may", name)
		}
	}
}

func TestSetMemoryLimit(t *testing.T) {
	// Not parallel: it sets the process's limit and GOMEMLIMIT.
	defer debug.SetMemoryLimit(debug.SetMemoryLimit(-1))
	t.Setenv("GOMEMLIMIT", "")
	if got := setMemoryLimit(); got != DefaultMemoryLimit || debug.SetMemoryLimit(-1) != DefaultMemoryLimit {
		t.Fatalf("with no GOMEMLIMIT, limit = %d; want %d", got, DefaultMemoryLimit)
	}
	debug.SetMemoryLimit(math.MaxInt64)
	t.Setenv("GOMEMLIMIT", "1GiB")
	if got := setMemoryLimit(); got != math.MaxInt64 {
		t.Fatalf("with GOMEMLIMIT set, setMemoryLimit changed the limit to %d; the runtime's own reading of it stands", got)
	}
}
