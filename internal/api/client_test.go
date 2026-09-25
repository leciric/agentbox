package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// newTestServer stands up an httptest server and a Client that talks to it
// over ordinary TCP, the way NewRemoteClient does through a hub.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewRemoteClient(srv.URL, ""), srv
}

// TestDoDecodesASuccessfulResponse checks the three shapes `do` can decode
// into: a typed value, a bare string, and nothing at all.
func TestDoDecodesASuccessfulResponse(t *testing.T) {
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Write([]byte(`{"user":"pawly"}`))
		case "/string":
			w.Write([]byte("plain text"))
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		}
	})

	var out struct {
		User string `json:"user"`
	}
	if err := c.do(context.Background(), http.MethodGet, "/json", nil, &out); err != nil {
		t.Fatalf("do(/json) = %v", err)
	}
	if out.User != "pawly" {
		t.Errorf("decoded User = %q, want pawly", out.User)
	}

	var s string
	if err := c.do(context.Background(), http.MethodGet, "/string", nil, &s); err != nil {
		t.Fatalf("do(/string) = %v", err)
	}
	if s != "plain text" {
		t.Errorf("decoded string = %q, want %q", s, "plain text")
	}

	if err := c.do(context.Background(), http.MethodGet, "/empty", nil, nil); err != nil {
		t.Errorf("do(/empty, nil out) = %v, want nil: a nil out only drains the body", err)
	}
}

// TestDoSendsTheBodyItIsGiven checks a request body is marshalled as JSON,
// with the content type set, and that a body-less request sends neither.
func TestDoSendsTheBodyItIsGiven(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte("{}"))
	})

	type req struct {
		Name string `json:"name"`
	}
	var out struct{}
	if err := c.do(context.Background(), http.MethodPost, "/things", req{Name: "pawly"}, &out); err != nil {
		t.Fatalf("do() = %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if string(gotBody) != `{"name":"pawly"}` {
		t.Errorf("body = %s, want %s", gotBody, `{"name":"pawly"}`)
	}

	gotContentType = "unset"
	if err := c.do(context.Background(), http.MethodGet, "/things", nil, &out); err != nil {
		t.Fatalf("do() = %v", err)
	}
	if gotContentType != "" {
		t.Errorf("Content-Type on a body-less request = %q, want none set", gotContentType)
	}
}

// TestDoTurnsAnErrorStatusIntoAStatusError checks the daemon's {"error": "..."}
// body becomes a StatusError callers can inspect, and that a body which isn't
// that shape is still surfaced, trimmed, rather than dropped.
func TestDoTurnsAnErrorStatusIntoAStatusError(t *testing.T) {
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json-error":
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":"agent-01 is busy"}`))
		case "/plain-error":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("  panic: boom  \n"))
		case "/not-found":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"no saved base"}`))
		}
	})

	err := c.do(context.Background(), http.MethodGet, "/json-error", nil, nil)
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StatusError", err)
	}
	if se.Code != http.StatusConflict || se.Message != "agent-01 is busy" {
		t.Errorf("StatusError = %+v, want {409 agent-01 is busy}", se)
	}
	if se.Error() != "agent-01 is busy" {
		t.Errorf("Error() = %q, want the message", se.Error())
	}

	err = c.do(context.Background(), http.MethodGet, "/plain-error", nil, nil)
	if !errors.As(err, &se) || se.Message != "panic: boom" {
		t.Errorf("err = %v, want the trimmed body as the message", err)
	}

	err = c.do(context.Background(), http.MethodGet, "/not-found", nil, nil)
	if !IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false, want true", err)
	}
	if IsNotFound(errors.New("boom")) {
		t.Error("IsNotFound(a plain error) = true, want false: only a *StatusError carries a code")
	}
}

// TestDoFailsOnAnUnmarshallableBody checks a caller who asks for JSON but is
// carrying a body that isn't valid JSON gets that error back rather than a
// zeroed, silently wrong result.
func TestDoFailsOnAnUnmarshallableBody(t *testing.T) {
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	var out struct {
		Name string `json:"name"`
	}
	if err := c.do(context.Background(), http.MethodGet, "/x", nil, &out); err == nil {
		t.Error("do() with a malformed body succeeded, want a decode error")
	}
}

// unmarshallable can't be marshalled to JSON, so request has to fail before
// it ever reaches the network.
type unmarshallable struct {
	Ch chan int
}

func TestRequestFailsBeforeSendingABodyThatWontMarshal(t *testing.T) {
	called := false
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	if err := c.do(context.Background(), http.MethodPost, "/x", unmarshallable{}, nil); err == nil {
		t.Error("do() with an unmarshallable body succeeded, want an error")
	}
	if called {
		t.Error("the server was reached even though marshalling the body failed")
	}
}

// TestRequestFailsOnACancelledContext checks a context cancelled before the
// call is made stops it from reaching the network at all.
func TestRequestFailsOnACancelledContext(t *testing.T) {
	called := false
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.do(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Error("do() with a cancelled context succeeded, want an error")
	}
	if called {
		t.Error("the server was reached even though the context was already cancelled")
	}
}

// TestRequestFailsWhenTheServerIsGone checks a closed connection — the daemon
// isn't there, or died mid-call — comes back as an error rather than a hang
// or a zeroed result.
func TestRequestFailsWhenTheServerIsGone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	c := NewRemoteClient(srv.URL, "")
	srv.Close()
	if err := c.do(context.Background(), http.MethodGet, "/x", nil, nil); err == nil {
		t.Error("do() against a closed server succeeded, want a connection error")
	}
}

// TestNewRemoteClientSendsABearerToken checks a hub session is sent as an
// Authorization header, and that a trailing slash on the hub's base doesn't
// end up doubled in the request path.
func TestNewRemoteClientSendsABearerToken(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)

	c := NewRemoteClient(srv.URL+"/", "hub-session-token")
	if err := c.do(context.Background(), http.MethodGet, "/v1/version", nil, nil); err != nil {
		t.Fatalf("do() = %v", err)
	}
	if gotAuth != "Bearer hub-session-token" {
		t.Errorf("Authorization = %q, want a bearer token", gotAuth)
	}
	if gotPath != "/v1/version" {
		t.Errorf("path = %q, want /v1/version with no doubled slash", gotPath)
	}
	if !c.Remote() {
		t.Error("Remote() = false for a client made with NewRemoteClient, want true")
	}
	if c.Socket() != c.base {
		t.Errorf("Socket() = %q, want the base URL for a remote client", c.Socket())
	}
}

// TestNewClientDialsTheGivenSocket checks a Client made with NewClient talks
// unix, not TCP: it can reach a daemon listening on the socket path it names,
// and nothing else answers on http://agentbox otherwise.
func TestNewClientDialsTheGivenSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "agentbox.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	t.Cleanup(func() { l.Close() })
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"dev"}`))
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	c := NewClient(sock)
	if c.Remote() {
		t.Error("Remote() = true for a client made with NewClient, want false: it dials a socket")
	}
	if c.Socket() != sock {
		t.Errorf("Socket() = %q, want %q", c.Socket(), sock)
	}
	info, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version() = %v", err)
	}
	if info.Version != "dev" {
		t.Errorf("Version = %q, want dev", info.Version)
	}
}

// TestAgentPathValidatesTheRef checks AgentPath refuses anything that isn't
// cleanly "<project>/<agent>", and escapes what it accepts.
func TestAgentPathValidatesTheRef(t *testing.T) {
	tests := []struct {
		ref     string
		want    string
		wantErr bool
	}{
		{"pawly/agent 01", "/v1/agents/pawly/agent%2001", false},
		{"my project/agent-01", "/v1/agents/my%20project/agent-01", false},
		{"pawly", "", true},     // no agent named
		{"", "", true},          // empty
		{"pawly/", "", true},    // empty agent
		{"/agent-01", "", true}, // empty project
		{"pawly/a/b", "", true}, // agent has a slash in it
	}
	for _, tt := range tests {
		got, err := AgentPath(tt.ref)
		if (err != nil) != tt.wantErr {
			t.Errorf("AgentPath(%q) err = %v, wantErr %v", tt.ref, err, tt.wantErr)
			continue
		}
		if err == nil && got != tt.want {
			t.Errorf("AgentPath(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

// TestMediaBrowserAndAndroidPathsTreatAnEmptyRefAsSelf checks these three
// paths follow the same rule: no ref means the caller's own agent, on the
// in-agent API, and any other ref goes through AgentPath's validation.
func TestMediaBrowserAndAndroidPathsTreatAnEmptyRefAsSelf(t *testing.T) {
	for _, fn := range []struct {
		name string
		path func(string) (string, error)
		self string
	}{
		{"MediaPath", MediaPath, "/v1/self/media"},
		{"BrowserPath", BrowserPath, "/v1/self/browser"},
		{"AndroidPath", AndroidPath, "/v1/self/android"},
	} {
		got, err := fn.path("")
		if err != nil || got != fn.self {
			t.Errorf("%s(\"\") = %q, %v; want %q, nil", fn.name, got, err, fn.self)
		}
		if _, err := fn.path("pawly"); err == nil {
			t.Errorf("%s(%q) succeeded, want an error: no agent named", fn.name, "pawly")
		}
		got, err = fn.path("pawly/agent-01")
		want := "/v1/agents/pawly/agent-01"
		switch fn.name {
		case "MediaPath":
			want += "/media"
		case "BrowserPath":
			want += "/browser"
		case "AndroidPath":
			want += "/android"
		}
		if err != nil || got != want {
			t.Errorf("%s(%q) = %q, %v; want %q, nil", fn.name, "pawly/agent-01", got, err, want)
		}
	}
}

// TestFilesPathFollowsTheSameRefRuleAsChatPath checks "pawly" and
// "pawly/lead" both name the project lead's file listing.
func TestFilesPathFollowsTheSameRefRuleAsChatPath(t *testing.T) {
	for ref, want := range map[string]string{
		"pawly":          "/v1/projects/pawly/files",
		"pawly/lead":     "/v1/projects/pawly/files",
		"pawly/agent-01": "/v1/agents/pawly/agent-01/files",
	} {
		got, err := filesPath(ref)
		if err != nil || got != want {
			t.Errorf("filesPath(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	if _, err := filesPath(""); err == nil {
		t.Error("filesPath(\"\") succeeded, want an error")
	}
}

// TestBaseTellsANoSavedBaseFromAnyOtherNotFound checks Base() turns the
// daemon's specific "no saved base" 404 into (Base{}, false, nil) — not an
// error a caller has to filter out themselves — while any other 404, or any
// other error, is still reported.
func TestBaseTellsANoSavedBaseFromAnyOtherNotFound(t *testing.T) {
	status := http.StatusNotFound
	message := `{"error":"no saved base"}`
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(message))
	})

	base, ok, err := c.Base(context.Background(), "pawly")
	if err != nil || ok || base != (Base{}) {
		t.Errorf("Base() = %+v, %v, %v; want zero value, false, nil", base, ok, err)
	}

	// A 404 that isn't about a missing base is still an error.
	message = `{"error":"no such project"}`
	_, ok, err = c.Base(context.Background(), "pawly")
	if ok || err == nil {
		t.Errorf("Base() with an unrelated 404 = ok %v, err %v; want ok false, an error", ok, err)
	}

	status = http.StatusInternalServerError
	message = `{"error":"boom"}`
	_, ok, err = c.Base(context.Background(), "pawly")
	if ok || err == nil {
		t.Errorf("Base() on a 500 = ok %v, err %v; want ok false, an error", ok, err)
	}
}

// TestEventsCallsFnForEachEventAndSkipsMalformedLines checks the SSE stream
// is parsed line by line: only "data: " lines are looked at, a line that
// isn't valid JSON is skipped rather than aborting the stream, and fn's
// return value both stops the loop and comes back as Events' own error.
func TestEventsCallsFnForEachEventAndSkipsMalformedLines(t *testing.T) {
	body := "data: {\"type\":\"job\",\"data\":{}}\n" +
		"data: not json at all\n" +
		": a comment, not a data line\n" +
		"data: {\"type\":\"agent\",\"data\":{}}\n"
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	})

	var types []string
	err := c.Events(context.Background(), func(ev Event) error {
		types = append(types, ev.Type)
		return nil
	})
	if err != nil {
		t.Fatalf("Events() = %v", err)
	}
	if want := []string{"job", "agent"}; !equalStrings(types, want) {
		t.Errorf("saw events %v, want %v: the malformed and comment lines must be skipped", types, want)
	}

	// fn's error stops the stream and is returned.
	boom := errors.New("boom")
	err = c.Events(context.Background(), func(ev Event) error { return boom })
	if !errors.Is(err, boom) {
		t.Errorf("Events() = %v, want fn's own error", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFollowJobLogStopsWhenTheContextEnds checks a cancelled context is
// reported even though the server keeps the connection (and so io.Copy)
// open: a caller waiting on FollowJobLog must not hang forever.
func TestFollowJobLogStopsWhenTheContextEnds(t *testing.T) {
	started := make(chan struct{})
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done() // hangs until the client goes away
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.FollowJobLog(ctx, "job-1", discard{})
	}()
	<-started
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("FollowJobLog() = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FollowJobLog() didn't return after its context was cancelled")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// TestSaveGitHubTokenReturnsWhoItBelongsTo checks both halves of the call: the
// account and token are sent as the request body, and the user in the
// response comes back out.
func TestSaveGitHubTokenReturnsWhoItBelongsTo(t *testing.T) {
	var gotBody []byte
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"user":"octocat"}`))
	})
	user, err := c.SaveGitHubToken(context.Background(), "work", "ghp_abc")
	if err != nil {
		t.Fatalf("SaveGitHubToken() = %v", err)
	}
	if user != "octocat" {
		t.Errorf("user = %q, want octocat", user)
	}
	var sent GitHubTokenRequest
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("request body isn't valid JSON: %v", err)
	}
	if sent.Token != "ghp_abc" || sent.Account != "work" {
		t.Errorf("sent %+v, want {ghp_abc work}", sent)
	}
}
