package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
)

// fakeSelfMedia serves an agent's own media API on a unix socket, recording
// what was asked of it.
func fakeSelfMedia(t *testing.T, items []api.MediaItem) (*api.Client, *[]string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "s")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		call := r.Method + " " + r.URL.Path
		if r.Method == http.MethodPost {
			var req api.AddMediaRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			call += " " + req.Kind + " " + req.Name + " " + filepath.Base(req.Path)
		}
		calls = append(calls, call)
		mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(items)
		case http.MethodPost:
			_ = json.NewEncoder(w).Encode(api.MediaItem{ID: "new"})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return api.NewClient(sock), &calls
}

func TestKeepScriptReplacesTheEarlierTakesScript(t *testing.T) {
	c, calls := fakeSelfMedia(t, []api.MediaItem{
		{ID: "old", Kind: "file", Name: "tour script"},
		{ID: "rec", Kind: "recording", Name: "tour script"},
		{ID: "other", Kind: "file", Name: "settings script"},
	})
	script := filepath.Join(t.TempDir(), "tour.json")
	if err := os.WriteFile(script, []byte(`[{"do":"key","combo":"Return"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := keepScript(context.Background(), c, script, "tour"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(*calls, "\n")
	for _, want := range []string{"DELETE /v1/self/media/old", "POST /v1/self/media/add file tour script tour.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	for _, not := range []string{"/rec", "/other"} {
		if strings.Contains(got, "DELETE /v1/self/media"+not) {
			t.Errorf("deleted %s, which isn't the earlier take's script:\n%s", not, got)
		}
	}
}

func TestRecordScriptRefusesBeforeRecording(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`[{"do":"click"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"media", "record", "--script", bad}, "name the recording"},
		{[]string{"media", "record", "--script", bad, "--name", "x"}, "step 1 (click): click needs x and y, or a label"},
		{[]string{"media", "record", "--script", filepath.Join(dir, "missing.json"), "--name", "x"}, "no such file"},
	} {
		cmd := NewRootCmd()
		cmd.SetArgs(tc.args)
		cmd.SetOut(&strings.Builder{})
		cmd.SetErr(&strings.Builder{})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}
