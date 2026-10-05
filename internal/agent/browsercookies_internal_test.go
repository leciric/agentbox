package agent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/coder/websocket"

	"agentbox/internal/cookieimport"
	"agentbox/internal/state"
)

// The imported cookies reach the agent's Chromium as one Storage.setCookies
// on its browser-wide DevTools endpoint: domain cookies by domain, host-only
// ones by URL, session cookies without an expiry.
func TestSetBrowserCookiesOverDevTools(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "cdp")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan map[string]any, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Browser":"Chrome/141","webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/abc"}`))
	})
	mux.HandleFunc("/devtools/browser/abc", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(data, &msg)
		got <- msg
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"id":1,"result":{}}`))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	m := &Manager{BrowserSocket: func(string, string) string { return socket }}
	cookies := []cookieimport.Cookie{
		{Domain: ".github.com", Name: "_octo", Value: "GH1", Path: "/", Expires: 4102444800, Secure: true, SameSite: "Lax"},
		{Domain: "github.com", Name: "user_session", Value: "sess", Path: "/", Secure: true, HTTPOnly: true},
		{Domain: "localhost", Name: "dev", Value: "1", Path: "/app"},
	}
	if err := m.setBrowserCookies(context.Background(), state.Agent{Instance: "ab-p-agent-01"}, cookies); err != nil {
		t.Fatal(err)
	}
	msg := <-got
	if msg["method"] != "Storage.setCookies" {
		t.Fatalf("method = %v", msg["method"])
	}
	params, _ := json.Marshal(msg["params"])
	var p struct{ Cookies []map[string]any }
	_ = json.Unmarshal(params, &p)
	want := []map[string]any{
		{"name": "_octo", "value": "GH1", "path": "/", "domain": ".github.com", "expires": 4102444800.0, "secure": true, "httpOnly": false, "sameSite": "Lax"},
		{"name": "user_session", "value": "sess", "path": "/", "url": "https://github.com/", "secure": true, "httpOnly": true},
		{"name": "dev", "value": "1", "path": "/app", "url": "http://localhost/app", "secure": false, "httpOnly": false},
	}
	if !reflect.DeepEqual(p.Cookies, want) {
		t.Errorf("cookies =\n%v\nwant\n%v", p.Cookies, want)
	}
}
