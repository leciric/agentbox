// Package hubtest is a stand-in hub, for testing the half of AgentBox that
// talks to one.
//
// A real hub is a separate program, and this repository doesn't have it. What
// it does have is everything that connects to a hub — the daemon's connector,
// the API client, the CLI — and that side is only worth anything if it is
// exercised against something that answers. So this serves the endpoints of
// agentbox/hubapi, over a real HTTP server, with real WebSocket tunnels.
//
// It is a test double and nothing else. Accounts, passwords, ownership and
// persistence are the hub's business, and none of them are here: it takes any
// password, keeps everything in memory, and lets any session see any
// environment. Don't grow it into a hub — if a test needs a hub's judgement
// about who may see what, that test belongs with the hub.
package hubtest

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"agentbox/hubapi"
)

// Hub is a stand-in hub listening on URL. Close it with the test's cleanup.
type Hub struct {
	URL string

	t    *testing.T
	http *httptest.Server

	mu      sync.Mutex
	envs    map[string]*environment // by ID
	byToken map[string]*environment
	nextID  int
}

type environment struct {
	info    hubapi.HubEnvironment
	token   string
	session *yamux.Session
	proxy   *httputil.ReverseProxy
}

// New starts a stand-in hub, and stops it when the test ends.
func New(t *testing.T) *Hub {
	t.Helper()
	h := &Hub{t: t, envs: map[string]*environment{}, byToken: map[string]*environment{}}
	h.http = httptest.NewServer(h.handler())
	h.URL = h.http.URL
	t.Cleanup(func() {
		h.mu.Lock()
		for _, e := range h.envs {
			if e.session != nil {
				e.session.Close()
			}
		}
		h.mu.Unlock()
		h.http.Close()
	})
	return h
}

// Token is a session token this hub accepts, as signing in would return.
func (h *Hub) Token() string { return hubapi.SessionTokenPrefix + "test" }

func (h *Hub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(http.MethodPost+" "+hubapi.PathLogin, h.login)
	mux.HandleFunc(http.MethodPost+" "+hubapi.PathLogout, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc(http.MethodGet+" "+hubapi.PathMe, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, hubapi.HubUser{ID: "usr_test", Email: "owner@example.com", Name: "Owner"})
	})
	mux.HandleFunc(http.MethodGet+" "+hubapi.PathEnvironments, h.listEnvironments)
	mux.HandleFunc(http.MethodPost+" "+hubapi.PathEnvironments, h.createEnvironment)
	mux.HandleFunc(http.MethodDelete+" "+hubapi.PathEnvironments+"/{id}", h.deleteEnvironment)
	mux.HandleFunc(http.MethodGet+" "+hubapi.PathConnect, h.connect)
	mux.HandleFunc(hubapi.EnvironmentAPI("{id}")+"/{path...}", h.proxy)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, hubapi.Error{Error: msg})
}

// login takes any email and password: who may sign in is a hub's judgement.
func (h *Hub) login(w http.ResponseWriter, r *http.Request) {
	var req hubapi.HubLoginRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, hubapi.HubSession{
		Token:     hubapi.SessionTokenPrefix + "test",
		User:      hubapi.HubUser{ID: "usr_test", Email: req.Email, Name: "Owner"},
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
}

// signedIn checks only that a session token was sent, in the shape the
// protocol gives them. Whether it is a real session is a hub's business.
func (h *Hub) signedIn(w http.ResponseWriter, r *http.Request) bool {
	if token := bearer(r); len(token) > len(hubapi.SessionTokenPrefix) && token[:len(hubapi.SessionTokenPrefix)] == hubapi.SessionTokenPrefix {
		return true
	}
	fail(w, http.StatusUnauthorized, "sign in to the hub first")
	return false
}

func bearer(r *http.Request) string {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) > len(prefix) && auth[:len(prefix)] == prefix {
		return auth[len(prefix):]
	}
	return ""
}

func (h *Hub) createEnvironment(w http.ResponseWriter, r *http.Request) {
	if !h.signedIn(w, r) {
		return
	}
	var req hubapi.HubCreateEnvironmentRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, h.AddEnvironment(req.Name))
}

// AddEnvironment adds an environment and returns it with its token, as the
// hub's own answer would. Tests that don't need the HTTP call use this.
func (h *Hub) AddEnvironment(name string) hubapi.HubEnvironmentToken {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	e := &environment{
		info:  hubapi.HubEnvironment{ID: "env_" + strconv.Itoa(h.nextID), Name: name, CreatedAt: time.Now()},
		token: hubapi.EnvironmentTokenPrefix + "test" + strconv.Itoa(h.nextID),
	}
	h.envs[e.info.ID] = e
	h.byToken[e.token] = e
	return hubapi.HubEnvironmentToken{Environment: e.info, Token: e.token}
}

func (h *Hub) listEnvironments(w http.ResponseWriter, r *http.Request) {
	if !h.signedIn(w, r) {
		return
	}
	h.mu.Lock()
	out := make([]hubapi.HubEnvironment, 0, len(h.envs))
	for _, e := range h.envs {
		out = append(out, e.info)
	}
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (h *Hub) deleteEnvironment(w http.ResponseWriter, r *http.Request) {
	if !h.signedIn(w, r) {
		return
	}
	h.mu.Lock()
	e := h.envs[r.PathValue("id")]
	if e != nil {
		delete(h.envs, e.info.ID)
		delete(h.byToken, e.token)
	}
	h.mu.Unlock()
	if e == nil {
		fail(w, http.StatusNotFound, "no such environment")
		return
	}
	if e.session != nil {
		e.session.Close()
	}
	w.WriteHeader(http.StatusNoContent)
}

// connect accepts an environment's tunnel: the hub is the yamux client on the
// connection, and opens a stream for every request it passes through.
func (h *Hub) connect(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	e := h.byToken[bearer(r)]
	h.mu.Unlock()
	if e == nil {
		fail(w, http.StatusUnauthorized, "unknown environment token")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(-1)
	session, err := yamux.Client(websocket.NetConn(r.Context(), conn, websocket.MessageBinary), hubapi.TunnelConfig())
	if err != nil {
		conn.CloseNow()
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "http", "environment", "environment"
			pr.Out.URL.Path = "/" + pr.In.PathValue("path")
			pr.Out.URL.RawPath = ""
			// The environment trusts the hub; the hub's own credentials stay here.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Origin")
		},
		Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) { return session.Open() },
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			fail(w, http.StatusBadGateway, e.info.Name+" didn't answer: "+err.Error())
		},
		ErrorLog: log.New(io.Discard, "", 0),
	}
	now := time.Now()
	h.mu.Lock()
	e.session, e.proxy = session, proxy
	e.info.Online, e.info.ConnectedAt = true, now
	e.info.Version, e.info.Hostname = r.Header.Get(hubapi.HeaderVersion), r.Header.Get(hubapi.HeaderHostname)
	h.mu.Unlock()

	<-session.CloseChan()
	h.mu.Lock()
	if e.session == session {
		e.session, e.proxy = nil, nil
		e.info.Online, e.info.LastSeenAt = false, time.Now()
	}
	h.mu.Unlock()
}

func (h *Hub) proxy(w http.ResponseWriter, r *http.Request) {
	if !h.signedIn(w, r) {
		return
	}
	h.mu.Lock()
	e := h.envs[r.PathValue("id")]
	var proxy *httputil.ReverseProxy
	if e != nil {
		proxy = e.proxy
	}
	h.mu.Unlock()
	if e == nil {
		fail(w, http.StatusNotFound, "no such environment")
		return
	}
	if proxy == nil {
		fail(w, http.StatusServiceUnavailable, e.info.Name+" is offline")
		return
	}
	proxy.ServeHTTP(w, r)
}
