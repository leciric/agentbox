package daemon

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"agentbox/internal/api"
)

// A request with api.PreviewHeader goes to the agent and port it names, with
// its own Host kept, as the iOS Simulator's forwards send them: the agent's
// server builds its URLs from localhost:<port>, which the app reaches.
func TestPreviewRoutesByHeader(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus, testConfig{previewAddr: "127.0.0.1:0"})
	a := addTestAgent(t, d)
	var gotHost, gotRoute string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotRoute = r.Host, r.Header.Get(api.PreviewHeader)
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	d.srv.mu.Lock()
	d.srv.previewIPs[a.Ref()] = previewTarget{ip: "127.0.0.1", at: time.Now().Add(time.Hour)}
	addr := d.srv.previewAddr
	d.srv.mu.Unlock()

	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/index.bundle", nil)
	req.Host = "localhost:8081"
	req.Header.Set(api.PreviewHeader, u.Port()+"."+a.Name+"."+a.Project)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the preview answered %s", resp.Status)
	}
	if gotHost != "localhost:8081" || gotRoute != "" {
		t.Errorf("the agent's server got Host %q and the route header %q, want localhost:8081 and none", gotHost, gotRoute)
	}

	// A page that rebinds its own name to 127.0.0.1 can't route with it.
	gotHost = ""
	req.Host = "evil.example:7777"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || gotHost != "" {
		t.Errorf("with Host evil.example the preview answered %s and the agent got %q", resp.Status, gotHost)
	}
}
