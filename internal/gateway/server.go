package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// Ref is the agent a session belongs to.
type Ref struct {
	Project string
	Agent   string
}

func (r Ref) String() string { return r.Project + "/" + r.Agent }

// Server is the master's side: it accepts one WebSocket per pod, keeps the
// session that pod's agent is reachable through, and opens streams on it.
type Server struct {
	// Auth turns the bearer token a pod connected with into the agent it
	// belongs to. The token is the one the daemon wrote into the pod's Secret;
	// only its hash is in the database.
	Auth func(ctx context.Context, token string) (Ref, error)
	// Log is where a session opening and closing is recorded; nil discards it.
	Log func(format string, args ...any)

	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}

type session struct {
	ref  Ref
	mux  *yamux.Session
	when time.Time
}

// NewServer is a server that authenticates with auth.
func NewServer(auth func(ctx context.Context, token string) (Ref, error)) *Server {
	return &Server{Auth: auth, sessions: map[string]*session{}}
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// ServeHTTP upgrades a pod's connection, authenticates it, and keeps it until
// it dies.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	if token == "" {
		http.Error(w, "the gateway needs the agent's token", http.StatusUnauthorized)
		return
	}
	ref, err := s.Auth(r.Context(), token)
	if err != nil {
		http.Error(w, "the agent's token isn't one this daemon issued", http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	// The connection outlives this request, which ends as soon as the upgrade
	// does: its context is the daemon's, not the request's.
	conn := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
	mux, err := yamux.Server(conn, config())
	if err != nil {
		s.logf("gateway: %s: %v", ref, err)
		return
	}
	sess := &session{ref: ref, mux: mux, when: time.Now()}
	s.replace(sess)
	defer func() {
		s.forget(sess)
		_ = mux.Close()
	}()
	s.logf("gateway: %s connected", ref)
	<-mux.CloseChan()
	s.logf("gateway: %s disconnected", ref)
}

// replace makes sess the agent's session, closing the one it had: a pod that
// reconnects is the one to talk to, and a half-dead earlier session is not.
func (s *Server) replace(sess *session) {
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]*session{}
	}
	old := s.sessions[sess.ref.String()]
	s.sessions[sess.ref.String()] = sess
	s.mu.Unlock()
	if old != nil && old != sess {
		_ = old.mux.Close()
		s.logf("gateway: %s replaced by a newer session", sess.ref)
	}
}

func (s *Server) forget(sess *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[sess.ref.String()] == sess {
		delete(s.sessions, sess.ref.String())
	}
}

// Connected reports whether the agent's pod has a session now.
func (s *Server) Connected(ref Ref) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[ref.String()]
	return ok
}

// WaitFor waits for the agent's pod to connect, at most timeout. A pod that is
// being created takes a few seconds to pull its image and start.
func (s *Server) WaitFor(ctx context.Context, ref Ref, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	if timeout <= 0 {
		deadline = time.Now()
	}
	for {
		if s.Connected(ref) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: %w", ref, ErrNotConnected)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Dial opens a stream to the agent's pod and writes h on it. The caller closes
// the stream when it is done.
func (s *Server) Dial(ctx context.Context, ref Ref, h Header) (*Stream, error) {
	sess, err := s.session(ref)
	if err != nil {
		return nil, err
	}
	conn, err := sess.mux.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("%s: opening a stream: %w", ref, err)
	}
	stream := NewStream(conn)
	if err := stream.WriteHeader(h); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	return stream, nil
}

func (s *Server) session(ref Ref) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("the gateway is closing")
	}
	sess := s.sessions[ref.String()]
	if sess == nil {
		return nil, fmt.Errorf("%s: %w", ref, ErrNotConnected)
	}
	return sess, nil
}

// Close ends every session, which is what the daemon's shutdown does.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	sessions := make([]*session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.sessions = map[string]*session{}
	s.mu.Unlock()
	for _, sess := range sessions {
		_ = sess.mux.Close()
	}
	return nil
}

// SessionRefs is the agents connected now, for the daemon's health page.
func (s *Server) SessionRefs() []Ref {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs := make([]Ref, 0, len(s.sessions))
	for _, sess := range s.sessions {
		refs = append(refs, sess.ref)
	}
	return refs
}

// bearer is the token a request carries.
func bearer(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 7 || !strings.EqualFold(value[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(value[7:])
}

// config is the multiplexer both ends run: keepalive detects a dead peer within
// about 90 seconds, which is what makes a stopped pod's session go away without
// a heartbeat of our own.
func config() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second
	cfg.ConnectionWriteTimeout = 60 * time.Second
	cfg.MaxStreamWindowSize = 1 << 20
	cfg.AcceptBacklog = MaxStreams
	cfg.LogOutput = io.Discard
	return cfg
}
