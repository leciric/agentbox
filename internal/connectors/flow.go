package connectors

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The browser sign-in. Connect finds out how the server signs in, registers
// AgentBox with it if it hasn't already, opens a listener on 127.0.0.1 and
// answers with the page to open. The server sends the browser back to that
// listener with a code, which is traded for tokens there: the sign-in ends on
// this machine, and nothing about it passes through the app or an agent.

// SignInTimeout is how long a sign-in waits for the browser.
const SignInTimeout = 10 * time.Minute

// signIn is one sign-in waiting for the browser.
type signIn struct {
	state    string
	verifier string
	redirect string
	disc     Discovery
	client   Client
	srv      *http.Server
	timer    *time.Timer
}

// Connect starts signing a connector in, and answers with the page the user
// signs in at. The sign-in finishes on its own, when the browser comes back;
// OnChange hears of it either way.
func (s *Service) Connect(ctx context.Context, project, agent, name string) (api.ConnectResult, state.Connector, error) {
	c, err := s.State.Connector(ctx, project, agent, name)
	if err != nil {
		return api.ConnectResult{}, state.Connector{}, err
	}
	if c.Auth != api.ConnectorOAuth {
		return api.ConnectResult{}, state.Connector{}, fmt.Errorf("%s sends %s, and has nothing to sign in to: only an oauth connector connects", c.Name, describeAuth(c))
	}
	k := keyOf(c)
	s.cancelSignIn(k)
	disc, err := s.OAuth.Discover(ctx, c.URL)
	if err != nil {
		return api.ConnectResult{}, state.Connector{}, fmt.Errorf("finding out how %s signs in: %w", c.URL, err)
	}
	ln, client, err := s.listen(ctx, c, disc)
	if err != nil {
		return api.ConnectResult{}, state.Connector{}, err
	}
	redirect := "http://" + ln.Addr().String() + "/callback"
	if c.Issuer != disc.Server.Issuer || c.ClientID != client.ID || c.RedirectURI != redirect {
		// A new registration is kept straight away: connecting again reuses
		// it, if its port is free, rather than registering once more.
		c.Issuer, c.TokenEndpoint, c.ClientID, c.TokenAuth, c.RedirectURI = disc.Server.Issuer, disc.Server.TokenEndpoint, client.ID, client.AuthMethod, redirect
		c.ClientSecret = nil
		if client.Secret != "" {
			if c.ClientSecret, err = s.Secrets.Seal(client.Secret); err != nil {
				_ = ln.Close()
				return api.ConnectResult{}, state.Connector{}, err
			}
		}
	}
	c.TokenEndpoint, c.Resource, c.Error, c.UpdatedAt = disc.Server.TokenEndpoint, disc.Resource, "", time.Now()
	if err := s.State.SetConnector(ctx, c); err != nil {
		_ = ln.Close()
		return api.ConnectResult{}, state.Connector{}, err
	}

	verifier, challenge := PKCE()
	in := &signIn{state: randomString(24), verifier: verifier, redirect: redirect, disc: disc, client: client}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) { s.callback(w, r, k, in) })
	in.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	in.timer = time.AfterFunc(SignInTimeout, func() {
		if s.endSignIn(k, in) {
			s.finish(k, fmt.Errorf("nobody finished signing in within %s", SignInTimeout))
		}
	})
	s.mu.Lock()
	if s.pending == nil {
		s.pending = map[key]*signIn{}
	}
	s.pending[k] = in
	s.mu.Unlock()
	go func() { _ = in.srv.Serve(ln) }()
	s.changed(c, false)
	return api.ConnectResult{
		AuthorizationURL: AuthorizationURL(disc, client, redirect, in.state, challenge),
		RedirectURI:      redirect,
		ExpiresAt:        time.Now().Add(SignInTimeout),
	}, c, nil
}

// listen opens the sign-in's listener and finds the client it goes with: the
// one already registered, if its redirect URI's port can be listened on again
// and it was registered with this server, or a new one for a new port.
func (s *Service) listen(ctx context.Context, c state.Connector, disc Discovery) (net.Listener, Client, error) {
	host := cmp.Or(s.CallbackHost, "127.0.0.1")
	if c.ClientID != "" && c.Issuer == disc.Server.Issuer {
		if u, err := url.Parse(c.RedirectURI); err == nil && u.Hostname() == host {
			if ln, err := net.Listen("tcp", u.Host); err == nil {
				client, err := s.client(c)
				if err == nil {
					return ln, client, nil
				}
				_ = ln.Close()
			}
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, Client{}, fmt.Errorf("couldn't listen for the sign-in: %w", err)
	}
	client, err := s.OAuth.Register(ctx, disc.Server, "http://"+ln.Addr().String()+"/callback", disc.Scope)
	if err != nil {
		_ = ln.Close()
		if errors.Is(err, ErrRegistration) {
			return nil, Client{}, fmt.Errorf("%w. Use a token instead: store it with agentbox secrets set, and change the connector to send it: agentbox connector add %s %s --url %s --secret <NAME>",
				err, target(c), c.Name, c.URL)
		}
		return nil, Client{}, err
	}
	return ln, client, nil
}

// callback is where the browser comes back to.
func (s *Service) callback(w http.ResponseWriter, r *http.Request, k key, in *signIn) {
	q := r.URL.Query()
	if q.Get("state") != in.state {
		// Not this sign-in's: a stale tab, or somebody else's request. The
		// sign-in goes on waiting for its own.
		page(w, http.StatusBadRequest, "This isn't the sign-in AgentBox is waiting for", "Start connecting again from AgentBox.")
		return
	}
	if !s.endSignIn(k, in) {
		page(w, http.StatusGone, "This sign-in is over", "Start connecting again from AgentBox.")
		return
	}
	defer func() { go func() { _ = in.srv.Close() }() }()
	if e := q.Get("error"); e != "" {
		err := fmt.Errorf("the sign-in was refused: %s", cmp.Or(q.Get("error_description"), e))
		s.finish(k, err)
		page(w, http.StatusOK, "Not connected", err.Error())
		return
	}
	code := q.Get("code")
	if code == "" {
		s.finish(k, errors.New("the server sent the browser back without a code"))
		page(w, http.StatusBadRequest, "Not connected", "The server sent the browser back without a code.")
		return
	}
	// Issuer check (RFC 9207): a server that says who it is must be the one
	// this sign-in went to.
	if iss := q.Get("iss"); iss != "" && iss != in.disc.Server.Issuer {
		s.finish(k, fmt.Errorf("the sign-in came back from %s, not %s", iss, in.disc.Server.Issuer))
		page(w, http.StatusBadRequest, "Not connected", "The sign-in came back from a different server.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	t, err := s.OAuth.Exchange(ctx, in.disc.Server.TokenEndpoint, in.client, code, in.verifier, in.redirect, in.disc.Resource)
	if err != nil {
		s.finish(k, err)
		page(w, http.StatusOK, "Not connected", err.Error())
		return
	}
	name := s.finish(k, nil, t)
	page(w, http.StatusOK, "Connected", fmt.Sprintf("AgentBox is signed in to %s. You can close this tab.", cmp.Or(name, "the server")))
}

// finish records how a sign-in ended: tokens, or an error on the connector.
// It answers with the connector's name.
func (s *Service) finish(k key, failure error, tokens ...Token) string {
	ctx := context.Background()
	c, err := s.State.Connector(ctx, k.project, k.agent, k.name)
	if err != nil {
		return "" // removed while the browser was out
	}
	if failure != nil {
		c.Error, c.UpdatedAt = "signing in failed: "+failure.Error(), time.Now()
		if len(c.AccessToken) > 0 {
			// A connector that was signed in stays signed in: a sign-in
			// abandoned halfway is not a reason to lose the one before.
			c.Error = ""
		}
		if err := s.State.SetConnector(ctx, c); err == nil {
			s.changed(c, false)
		}
		return c.Name
	}
	if c, err = s.store(ctx, c, tokens[0], true); err == nil {
		s.changed(c, false)
	}
	return c.Name
}

// endSignIn takes a sign-in off the waiting list, reporting whether it was
// still on it: the callback and the timeout race to end it, and only one may.
func (s *Service) endSignIn(k key, in *signIn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[k] != in {
		return false
	}
	delete(s.pending, k)
	in.timer.Stop()
	return true
}

// cancelSignIn ends a connector's waiting sign-in, if it has one, without a
// word: whatever cancelled it says what happened.
func (s *Service) cancelSignIn(k key) {
	s.mu.Lock()
	in := s.pending[k]
	delete(s.pending, k)
	s.mu.Unlock()
	if in != nil {
		in.timer.Stop()
		_ = in.srv.Close()
	}
}

func describeAuth(c state.Connector) string {
	if c.Auth == api.ConnectorSecret {
		return "the secret " + c.Secret
	}
	return "no credentials"
}

// page is what the browser shows at the end of a sign-in.
func page(w http.ResponseWriter, code int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%[1]s · AgentBox</title>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h1 style="font-size:1.4rem">%[1]s</h1><p>%[2]s</p></body>`, html.EscapeString(title), html.EscapeString(text))
}
