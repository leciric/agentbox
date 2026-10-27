package hub

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"agentbox/hubapi"
)

type testHub struct {
	t      *testing.T
	server *Server
	http   *httptest.Server
	store  Store
}

func newTestHub(t *testing.T) *testHub {
	t.Helper()
	store, err := OpenStore(context.Background(), "sqlite:"+filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: store}
	h := &testHub{t: t, server: s, http: httptest.NewServer(s.Handler()), store: store}
	t.Cleanup(func() {
		s.Close()
		h.http.Close()
		_ = store.Close()
	})
	return h
}

// do sends a request to the hub, with a bearer token when token isn't empty.
func (h *testHub) do(method, path, token string, body any, header ...string) *http.Response {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.http.URL+path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

func decode[T any](t *testing.T, resp *http.Response, status int) T {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var v T
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("%s %s: status %d, want %d: %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, status, data)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &v); err != nil {
			t.Fatalf("decoding %s: %v", data, err)
		}
	}
	return v
}

// signIn makes an account the way hubs did before pairing, with an email and a
// password, and signs in with them.
func (h *testHub) signIn(email string) string {
	h.t.Helper()
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.CreateUser(context.Background(), User{ID: newID("usr_"), Email: email, Name: "Someone", PasswordHash: hash, CreatedAt: time.Now()}); err != nil {
		h.t.Fatal(err)
	}
	return decode[hubapi.HubSession](h.t, h.do("POST", "/v1/auth/login", "", hubapi.HubLoginRequest{Email: email, Password: "correct horse battery", Label: "test"}), 200).Token
}

// fakeEnvironment stands in for a daemon's API.
func fakeEnvironment() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"version":"fake","path":%q,"query":%q,"authorization":%q,"cookie":%q}`, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Cookie"))
	})
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: usage\ndata: {\"cpu\":1}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("GET /v1/echo", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			typ, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			_ = conn.Write(r.Context(), typ, append([]byte("echo: "), data...))
		}
	})
	mux.HandleFunc("GET /v1/big", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("0123456789abcdef"), 5<<20/16))
	})
	return mux
}

func (h *testHub) connectEnvironment(ctx context.Context, token string) *testConnector {
	h.t.Helper()
	c := &testConnector{Hub: h.http.URL, Token: token, Handler: fakeEnvironment(), Version: "0.2.0", Hostname: "vps-1"}
	go c.Run(ctx)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if c.Status().Connected {
			return c
		}
	}
	h.t.Fatalf("the environment didn't connect: %+v", c.Status())
	return nil
}

func TestEnvironmentThroughTheHub(t *testing.T) {
	h := newTestHub(t)
	owner := h.signIn("owner@example.com")
	created := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: "vps"}), 201)
	if !strings.HasPrefix(created.Token, "abx_e_") || created.Environment.Online {
		t.Fatalf("created = %+v", created)
	}
	id := created.Environment.ID
	api_ := "/v1/environments/" + id + "/api"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.connectEnvironment(ctx, created.Token)

	envs := decode[[]hubapi.HubEnvironment](t, h.do("GET", "/v1/environments", owner, nil), 200)
	if len(envs) != 1 || !envs[0].Online || envs[0].Version != "0.2.0" || envs[0].Hostname != "vps-1" {
		t.Errorf("environments = %+v", envs)
	}

	t.Run("requests reach the environment, without the hub's credentials", func(t *testing.T) {
		got := decode[map[string]string](t, h.do("GET", api_+"/v1/version?interval=1s", owner, nil, "Cookie", "abx_session=abc"), 200)
		if got["version"] != "fake" || got["path"] != "/v1/version" || got["query"] != "interval=1s" || got["authorization"] != "" || got["cookie"] != "" {
			t.Errorf("the environment saw %+v", got)
		}
	})

	t.Run("the event stream flows", func(t *testing.T) {
		resp := h.do("GET", api_+"/v1/events", owner, nil)
		defer func() { _ = resp.Body.Close() }()
		lines := make(chan string, 4)
		go func() {
			s := bufio.NewScanner(resp.Body)
			for s.Scan() {
				lines <- s.Text()
			}
		}()
		select {
		case line := <-lines:
			if line != "event: usage" {
				t.Errorf("first line %q", line)
			}
		case <-time.After(3 * time.Second):
			t.Error("no event arrived: the proxy buffers the stream")
		}
	})

	t.Run("WebSockets work", func(t *testing.T) {
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.http.URL, "http")+api_+"/v1/echo", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + owner}}})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.CloseNow() }()
		_ = conn.Write(ctx, websocket.MessageBinary, []byte("ls\n"))
		_, data, err := conn.Read(ctx)
		if err != nil || string(data) != "echo: ls\n" {
			t.Errorf("read %q, %v", data, err)
		}
	})

	t.Run("large responses arrive whole", func(t *testing.T) {
		resp := h.do("GET", api_+"/v1/big", owner, nil)
		defer func() { _ = resp.Body.Close() }()
		got := sha256.New()
		n, _ := io.Copy(got, resp.Body)
		want := sha256.Sum256(bytes.Repeat([]byte("0123456789abcdef"), 5<<20/16))
		if n != 5<<20 || !bytes.Equal(got.Sum(nil), want[:]) {
			t.Errorf("got %d bytes, a different body", n)
		}
	})

	t.Run("only its owner reaches it", func(t *testing.T) {
		if resp := h.do("GET", api_+"/v1/version", "", nil); resp.StatusCode != 401 {
			t.Errorf("without signing in: %d", resp.StatusCode)
		}
		other := h.signIn("other@example.com")
		if resp := h.do("GET", api_+"/v1/version", other, nil); resp.StatusCode != 404 {
			t.Errorf("another account: %d", resp.StatusCode)
		}
	})

	t.Run("the web app's cookie works from the hub's pages only", func(t *testing.T) {
		cookie := "abx_session=" + owner
		if resp := h.do("GET", api_+"/v1/version", "", nil, "Cookie", cookie, "Origin", h.http.URL); resp.StatusCode != 200 {
			t.Errorf("same origin: %d", resp.StatusCode)
		}
		if resp := h.do("GET", api_+"/v1/version", "", nil, "Cookie", cookie, "Origin", "https://evil.example"); resp.StatusCode != 403 {
			t.Errorf("another site: %d", resp.StatusCode)
		}
	})

	cancel()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if envs := decode[[]hubapi.HubEnvironment](t, h.do("GET", "/v1/environments", owner, nil), 200); !envs[0].Online {
			if envs[0].LastSeenAt.IsZero() {
				t.Error("no last-seen time after disconnecting")
			}
			resp := h.do("GET", api_+"/v1/version", owner, nil)
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 503 || !strings.Contains(string(body), "vps is offline") {
				t.Errorf("an offline environment: %d %s", resp.StatusCode, body)
			}
			return
		}
	}
	t.Error("the environment still shows online after it disconnected")
}

func TestSigningIn(t *testing.T) {
	h := newTestHub(t)
	session := h.signIn("owner@example.com")
	if resp := h.do("POST", "/v1/auth/signup", "", map[string]string{"email": "new@example.com", "password": "correct horse battery"}); resp.StatusCode != 404 && resp.StatusCode != 405 {
		t.Errorf("sign-up, which is gone: %d", resp.StatusCode)
	}
	if resp := h.do("POST", "/v1/auth/login", "", hubapi.HubLoginRequest{Email: "nobody@example.com", Password: "correct horse battery"}); resp.StatusCode != 401 {
		t.Errorf("an unknown email: %d", resp.StatusCode)
	}
	if resp := h.do("POST", "/v1/auth/login", "", hubapi.HubLoginRequest{Email: "owner@example.com", Password: "x"}, "Origin", "https://evil.example"); resp.StatusCode != 403 {
		t.Errorf("signing in from another site: %d", resp.StatusCode)
	}
	// The unknown email above was the first failure from this address.
	status := 0
	for i := 0; i < 9; i++ {
		status = h.do("POST", "/v1/auth/login", "", hubapi.HubLoginRequest{Email: "owner@example.com", Password: "wrong password!"}).StatusCode
	}
	if resp := h.do("POST", "/v1/auth/login", "", hubapi.HubLoginRequest{Email: "owner@example.com", Password: "correct horse battery"}); status != 401 || resp.StatusCode != 429 {
		t.Errorf("after 10 failures: %d, then %d; want 401, then 429", status, resp.StatusCode)
	}

	me := decode[hubapi.HubUser](t, h.do("GET", "/v1/me", session, nil), 200)
	if me.Email != "owner@example.com" {
		t.Errorf("me = %+v", me)
	}
	if resp := h.do("POST", "/v1/auth/logout", session, nil); resp.StatusCode != 204 {
		t.Errorf("logout: %d", resp.StatusCode)
	}
	if resp := h.do("GET", "/v1/me", session, nil); resp.StatusCode != 401 {
		t.Errorf("after logging out: %d", resp.StatusCode)
	}
}

func TestEnvironmentTokens(t *testing.T) {
	h := newTestHub(t)
	owner := h.signIn("owner@example.com")
	for _, name := range []string{"Bad Name", "", "-vps"} {
		if resp := h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: name}); resp.StatusCode != 400 {
			t.Errorf("name %q: %d", name, resp.StatusCode)
		}
	}
	created := decode[hubapi.HubEnvironmentToken](t, h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: "home-pc"}), 201)
	if resp := h.do("POST", "/v1/environments", owner, hubapi.HubCreateEnvironmentRequest{Name: "home-pc"}); resp.StatusCode != 409 {
		t.Errorf("the same name twice: %d", resp.StatusCode)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := h.connectEnvironment(ctx, created.Token)
	if resp := h.do("DELETE", "/v1/environments/"+created.Environment.ID, owner, nil); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	// Deleting it closes its tunnel, and the token no longer connects: the
	// hub answers 401, which is what an environment sees.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if st := c.Status(); !st.Connected && strings.Contains(st.LastError, "status 401") {
			return
		}
	}
	t.Errorf("after deleting the environment: %+v", c.Status())
}
