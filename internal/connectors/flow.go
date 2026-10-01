package connectors

import (
	"cmp"
	"context"
	"crypto/subtle"
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
// AgentBox with it if it hasn't already, and answers with the page to open.
// The server sends the browser back to a loopback address with a code, which
// is traded for tokens in the daemon: the sign-in ends on this machine, and
// nothing about it passes through the app or an agent.
//
// That loopback address has to be one the user's browser reaches, and the
// browser runs on the host while the daemon may run in a VM: the Cloud
// Hypervisor VM on Linux, or the one on a Mac. A port the daemon listens on in
// the VM isn't forwarded there, so the browser comes back to the daemon's
// preview proxy instead, at Service.Callback: every way AgentBox runs brings
// the host's 127.0.0.1:7777 to it — the VM's front end forwards it
// (internal/hostvm/chv), Lima forwards the VM's loopback ports, and a host
// install's daemon listens there itself. The daemon hands the request to
// ServeCallback. Only without a preview proxy (turned off, or its port taken)
// does each sign-in listen on a port of its own, which works when the daemon
// and the browser share a loopback.

// SignInTimeout is how long a sign-in waits for the browser.
const SignInTimeout = 10 * time.Minute

// CallbackPath is where the browser comes back to on the daemon's preview
// proxy.
const CallbackPath = "/connectors/callback"

// signIn is one sign-in waiting for the browser.
type signIn struct {
	key      key
	state    string
	verifier string
	redirect string
	disc     Discovery
	client   Client
	srv      *http.Server // nil when the browser comes back to Callback
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
	// Discovery and registration go over the network, so they run without
	// the connector's lock, which every relayed request takes: what they
	// find is kept below, under it, if the connector is still the same one.
	disc, err := s.OAuth.Discover(ctx, c.URL)
	if err != nil {
		return api.ConnectResult{}, state.Connector{}, fmt.Errorf("finding out how %s signs in: %w", c.URL, err)
	}
	ln, redirect, client, err := s.listen(ctx, c, disc)
	if err != nil {
		return api.ConnectResult{}, state.Connector{}, err
	}
	closeLn := func() {
		if ln != nil {
			_ = ln.Close()
		}
	}
	var sealed []byte
	if client.Secret != "" {
		if sealed, err = s.Secrets.Seal(client.Secret); err != nil {
			closeLn()
			return api.ConnectResult{}, state.Connector{}, err
		}
	}

	l := s.lock(k)
	l.Lock()
	now, err := s.State.Connector(ctx, project, agent, name)
	switch {
	case err != nil:
		l.Unlock()
		closeLn()
		return api.ConnectResult{}, state.Connector{}, err
	case now.URL != c.URL || now.Auth != c.Auth:
		l.Unlock()
		closeLn()
		return api.ConnectResult{}, state.Connector{}, fmt.Errorf("%s was changed while connecting: connect it again", c.Name)
	}
	c = now
	if c.Issuer != disc.Server.Issuer || c.ClientID != client.ID {
		// Another server, or another registration with it: the tokens were
		// for the old one, and must never be sent to the new one.
		c = forget(c, false)
	}
	if c.Issuer != disc.Server.Issuer || c.ClientID != client.ID || c.RedirectURI != redirect {
		// A new registration is kept straight away: connecting again reuses
		// it rather than registering once more.
		c.Issuer, c.ClientID, c.TokenAuth, c.RedirectURI, c.ClientSecret = disc.Server.Issuer, client.ID, client.AuthMethod, redirect, sealed
	}
	c.TokenEndpoint, c.Resource, c.Error, c.UpdatedAt = disc.Server.TokenEndpoint, disc.Resource, "", time.Now()
	if err := s.State.UpdateConnector(ctx, c); err != nil {
		l.Unlock()
		closeLn()
		return api.ConnectResult{}, state.Connector{}, err
	}
	l.Unlock()

	verifier, challenge := PKCE()
	in := &signIn{key: k, state: randomString(24), verifier: verifier, redirect: redirect, disc: disc, client: client}
	if ln != nil {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) { s.callback(w, r, in) })
		in.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	}
	in.timer = time.AfterFunc(SignInTimeout, func() {
		if s.endSignIn(in) {
			s.finish(in, fmt.Errorf("nobody finished signing in within %s", SignInTimeout))
		}
	})
	s.mu.Lock()
	if s.pending == nil {
		s.pending = map[key]*signIn{}
	}
	s.pending[k] = in
	s.mu.Unlock()
	if ln != nil {
		go func() { _ = in.srv.Serve(ln) }()
	}
	s.changed(c, false)
	return api.ConnectResult{
		AuthorizationURL: AuthorizationURL(disc, client, redirect, in.state, challenge),
		RedirectURI:      redirect,
		ExpiresAt:        time.Now().Add(SignInTimeout),
	}, c, nil
}

// listen finds where the browser comes back to, and the client registered
// for it: the one already registered with this server for that address, or a
// new one. It is the daemon's Callback when there is one, and otherwise a
// listener of the sign-in's own, ln, reusing the registered port if it is
// free.
func (s *Service) listen(ctx context.Context, c state.Connector, disc Discovery) (ln net.Listener, redirect string, client Client, err error) {
	registered := c.ClientID != "" && c.Issuer == disc.Server.Issuer
	if s.Callback != nil {
		if redirect = s.Callback(); redirect != "" {
			if registered && c.RedirectURI == redirect {
				if client, err = s.client(c); err == nil {
					return nil, redirect, client, nil
				}
			}
			client, err = s.register(ctx, c, disc, redirect)
			return nil, redirect, client, err
		}
	}
	host := cmp.Or(s.CallbackHost, "127.0.0.1")
	if registered {
		if u, err := url.Parse(c.RedirectURI); err == nil && u.Hostname() == host {
			if ln, err := net.Listen("tcp", u.Host); err == nil {
				client, err := s.client(c)
				if err == nil {
					return ln, c.RedirectURI, client, nil
				}
				_ = ln.Close()
			}
		}
	}
	if ln, err = net.Listen("tcp", net.JoinHostPort(host, "0")); err != nil {
		return nil, "", Client{}, fmt.Errorf("couldn't listen for the sign-in: %w", err)
	}
	redirect = "http://" + ln.Addr().String() + "/callback"
	if client, err = s.register(ctx, c, disc, redirect); err != nil {
		_ = ln.Close()
		return nil, "", Client{}, err
	}
	return ln, redirect, client, nil
}

// register registers AgentBox with the connector's authorization server, to
// come back to redirect.
func (s *Service) register(ctx context.Context, c state.Connector, disc Discovery, redirect string) (Client, error) {
	client, err := s.OAuth.Register(ctx, disc.Server, redirect, disc.Scope)
	if errors.Is(err, ErrRegistration) {
		return Client{}, fmt.Errorf("%w. Use a token instead: store it with agentbox secrets set, and change the connector to send it: agentbox connector add %s %s --url %s --secret <NAME>",
			err, target(c), c.Name, c.URL)
	}
	return client, err
}

// ServeCallback is the daemon's Callback: the browser coming back from any
// connector's sign-in, told apart by its state.
func (s *Service) ServeCallback(w http.ResponseWriter, r *http.Request) {
	st := r.URL.Query().Get("state")
	var in *signIn
	s.mu.Lock()
	for _, p := range s.pending {
		if p.srv == nil && st != "" && subtle.ConstantTimeCompare([]byte(p.state), []byte(st)) == 1 {
			in = p
		}
	}
	s.mu.Unlock()
	if in == nil {
		page(w, http.StatusBadRequest, "This isn't a sign-in AgentBox is waiting for", "It may have timed out, or been started again: start connecting again from AgentBox.")
		return
	}
	s.callback(w, r, in)
}

// callback is where the browser comes back to.
func (s *Service) callback(w http.ResponseWriter, r *http.Request, in *signIn) {
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(in.state)) != 1 {
		// Not this sign-in's: a stale tab, or somebody else's request. The
		// sign-in goes on waiting for its own.
		page(w, http.StatusBadRequest, "This isn't the sign-in AgentBox is waiting for", "Start connecting again from AgentBox.")
		return
	}
	if !s.endSignIn(in) {
		page(w, http.StatusGone, "This sign-in is over", "Start connecting again from AgentBox.")
		return
	}
	if in.srv != nil {
		// Shutdown, not Close, which would cut off this very page.
		defer func() {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = in.srv.Shutdown(ctx)
			}()
		}()
	}
	if e := q.Get("error"); e != "" {
		err := fmt.Errorf("the sign-in was refused: %s", cmp.Or(q.Get("error_description"), e))
		s.finish(in, err)
		page(w, http.StatusOK, "Not connected", err.Error())
		return
	}
	code := q.Get("code")
	if code == "" {
		s.finish(in, errors.New("the server sent the browser back without a code"))
		page(w, http.StatusBadRequest, "Not connected", "The server sent the browser back without a code.")
		return
	}
	// Issuer check (RFC 9207): a server that says who it is must be the one
	// this sign-in went to.
	if iss := q.Get("iss"); iss != "" && iss != in.disc.Server.Issuer {
		s.finish(in, fmt.Errorf("the sign-in came back from %s, not %s", iss, in.disc.Server.Issuer))
		page(w, http.StatusBadRequest, "Not connected", "The sign-in came back from a different server.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	t, err := s.OAuth.Exchange(ctx, in.disc.Server.TokenEndpoint, in.client, code, in.verifier, in.redirect, in.disc.Resource)
	if err != nil {
		s.finish(in, err)
		page(w, http.StatusOK, "Not connected", err.Error())
		return
	}
	name := s.finish(in, nil, t)
	page(w, http.StatusOK, "Connected", fmt.Sprintf("AgentBox is signed in to %s. You can close this tab.", cmp.Or(name, "the server")))
}

// finish records how a sign-in ended: tokens, or an error on the connector.
// It answers with the connector's name. A connector removed, or changed to
// another server or registration, while the browser was out keeps nothing.
func (s *Service) finish(in *signIn, failure error, tokens ...Token) string {
	ctx := context.Background()
	k := in.key
	l := s.lock(k)
	l.Lock()
	defer l.Unlock()
	c, err := s.State.Connector(ctx, k.project, k.agent, k.name)
	if err != nil || c.Issuer != in.disc.Server.Issuer || c.ClientID != in.client.ID {
		return ""
	}
	if failure != nil {
		c.Error, c.UpdatedAt = "signing in failed: "+failure.Error(), time.Now()
		if len(c.AccessToken) > 0 {
			// A connector that was signed in stays signed in: a sign-in
			// abandoned halfway is not a reason to lose the one before,
			// which was with this same server and registration.
			c.Error = ""
		}
		if err := s.State.UpdateConnector(ctx, c); err == nil {
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
func (s *Service) endSignIn(in *signIn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[in.key] != in {
		return false
	}
	delete(s.pending, in.key)
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
		if in.srv != nil {
			_ = in.srv.Close()
		}
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
