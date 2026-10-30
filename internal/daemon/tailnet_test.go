package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// TestTailnetLetsInOnlyWhoTailscaleSays checks the listener's two gates: the
// header Tailscale Serve adds, and the logins the settings allow. A local
// process could forge the header, which is why the listener never leaves
// loopback.
func TestTailnetLetsInOnlyWhoTailscaleSays(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	allow := []string{"H4ad@github"}
	if _, err := d.client.UpdateTailnet(ctx, api.UpdateTailnetRequest{Allow: &allow}); err != nil {
		t.Fatal(err)
	}
	h := d.srv.tailnetHandler()
	do := func(login, method, path string) int {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if login != "" {
			r.Header.Set(tailnetLoginHeader, login)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, c := range []struct {
		login, method, path string
		want                int
	}{
		{"", "GET", "/v1/version", http.StatusUnauthorized},
		{"someone@example.com", "GET", "/v1/version", http.StatusForbidden},
		// Tailscale spells the login as the identity provider did; the
		// setting needn't match its case.
		{"h4ad@github", "GET", "/v1/version", http.StatusOK},
		// What only the machine itself may do stays out of reach, whoever
		// the tailnet says is asking.
		{"h4ad@github", "POST", "/v1/shutdown", http.StatusForbidden},
		{"h4ad@github", "PATCH", "/v1/tailnet", http.StatusForbidden},
	} {
		if got := do(c.login, c.method, c.path); got != c.want {
			t.Errorf("%s %s as %q = %d, want %d", c.method, c.path, c.login, got, c.want)
		}
	}
}

// TestTailnetNeedsSomeoneAllowedAndListens checks that turning the listener on
// says who may use it first, opens a port once it does, and closes it when
// turned off.
func TestTailnetNeedsSomeoneAllowedAndListens(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{setup: func(srv *Server) {
		// Nothing to do with the real port: a test's own keeps it parallel.
		srv.tailnet.addr = "127.0.0.1:0"
	}})
	ctx := context.Background()
	st, err := d.client.TailnetStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Enabled || st.Listening || len(st.Allow) != 0 {
		t.Fatalf("a fresh daemon's tailnet = %+v, want everything off", st)
	}

	enabled := true
	if _, err := d.client.UpdateTailnet(ctx, api.UpdateTailnetRequest{Enabled: &enabled}); err == nil || !strings.Contains(err.Error(), "who may use it") {
		t.Fatalf("enabling with nobody allowed = %v, want it refused", err)
	}

	allow := []string{" h4ad@github ", "", "you@example.com"}
	st, err = d.client.UpdateTailnet(ctx, api.UpdateTailnetRequest{Enabled: &enabled, Allow: &allow})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || !st.Listening || st.Error != "" {
		t.Fatalf("after enabling = %+v, want it listening", st)
	}
	if want := []string{"h4ad@github", "you@example.com"}; !slices.Equal(st.Allow, want) {
		t.Errorf("Allow = %q, want %q", st.Allow, want)
	}

	// A real listener, not just a flag: the API answers through it when
	// Tailscale says who is asking, and refuses a request that it doesn't.
	req, err := http.NewRequest(http.MethodGet, "http://"+st.Addr+"/v1/version", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(tailnetLoginHeader, "H4ad@github")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the listener didn't answer: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /v1/version with the header = %d, want 200", resp.StatusCode)
	}
	resp, err = http.Get("http://" + st.Addr + "/v1/version")
	if err != nil {
		t.Fatalf("the listener didn't answer: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /v1/version without the header = %d, want 401", resp.StatusCode)
	}

	off := false
	st, err = d.client.UpdateTailnet(ctx, api.UpdateTailnetRequest{Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if st.Enabled || st.Listening {
		t.Fatalf("after turning it off = %+v, want it closed", st)
	}
}
