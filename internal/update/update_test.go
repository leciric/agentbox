package update

import (
	"cmp"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"
	"time"

	"agentbox/internal/hostos"
)

// TestCheckSendsExactlyTheFourFields pins what leaves the machine: the README
// promises these four query parameters and nothing more.
func TestCheckSendsExactlyTheFourFields(t *testing.T) {
	t.Setenv("AGENTBOX_HOST_OS", "")
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/latest" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("request carried credentials: %v", r.Header)
		}
		got = r.URL.Query()
		_, _ = w.Write([]byte(`{"version":"0.17.0","url":"https://github.com/leciric/agentbox/releases/tag/v0.17.0"}`))
	}))
	defer srv.Close()

	latest, err := Check(context.Background(), srv.URL+"/api/v1/latest", NewRequest("5b0c7d4e-1111-4222-8333-944455556666", "0.16.0"))
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != "0.17.0" || latest.URL != "https://github.com/leciric/agentbox/releases/tag/v0.17.0" {
		t.Errorf("Check() = %+v", latest)
	}
	want := url.Values{
		"install": {"5b0c7d4e-1111-4222-8333-944455556666"},
		"version": {"0.16.0"},
		"os":      {cmp.Or(hostos.OS(), runtime.GOOS)}, // windows under WSL2
		"arch":    {runtime.GOARCH},
	}
	if got.Encode() != want.Encode() {
		t.Errorf("query = %s, want %s", got.Encode(), want.Encode())
	}
}

// TestNewRequestSendsTheHostsOS: on a Mac or on Windows the daemon runs in a
// Linux VM, and the OS sent is the machine's, not the VM's.
func TestNewRequestSendsTheHostsOS(t *testing.T) {
	t.Setenv("AGENTBOX_HOST_OS", "darwin")
	if got := NewRequest("id", "0.16.0").OS; got != "darwin" {
		t.Errorf("OS in a Mac's VM = %q, want darwin", got)
	}
}

func TestCheckFailsOnAnythingButAnAnswer(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"an error status": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", http.StatusBadGateway) },
		"not JSON":        func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			if _, err := Check(context.Background(), srv.URL, NewRequest("id", "0.16.0")); err == nil {
				t.Error("Check() succeeded")
			}
		})
	}
}

func TestCheckGivesUpAfterTheTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	start := time.Now()
	if _, err := Check(context.Background(), srv.URL, NewRequest("id", "0.16.0")); err == nil {
		t.Fatal("Check() succeeded against a server that never answers")
	}
	if took := time.Since(start); took < Timeout || took > Timeout+2*time.Second {
		t.Errorf("Check() gave up after %s, want %s", took, Timeout)
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"0.17.0", "0.16.0", true},
		{"v0.17.0", "0.16.0", true},
		{"0.16.1", "0.16.0", true},
		{"0.16.0", "0.16.0", false},
		{"0.15.9", "0.16.0", false},
		{"0.10.0", "0.9.0", true},
		{"0.17.0", "dev", false},
		{"", "0.16.0", false},
		{"latest", "0.16.0", false},
	} {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestBlocked(t *testing.T) {
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	if got := Blocked("0.16.0"); got != "" {
		t.Errorf("Blocked() = %q with nothing in the way", got)
	}
	if Blocked("dev") == "" {
		t.Error("a development build isn't blocked")
	}
	t.Setenv("DO_NOT_TRACK", "1")
	if Blocked("0.16.0") == "" {
		t.Error("DO_NOT_TRACK=1 doesn't block the check")
	}
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "1")
	if Blocked("0.16.0") == "" {
		t.Error("AGENTBOX_NO_UPDATE_CHECK=1 doesn't block the check")
	}
}
