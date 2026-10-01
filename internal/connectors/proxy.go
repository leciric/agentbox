package connectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The daemon's side of the relay: a request from inside an agent, to one of
// its connectors, goes on to the server with the connector's credentials, and
// the server's answer comes back as it arrives — a streamed answer stays a
// stream. Only the headers MCP's transport needs cross, in either direction:
// the agent can't set another credential, and the server can't set a cookie
// on anything.

// forwarded are the request headers that go on to the server; returned are
// the response headers that come back.
var (
	forwarded = []string{"Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-Id"}
	returned  = []string{"Content-Type", "Mcp-Session-Id", "Cache-Control"}
)

// maxMessage bounds one JSON-RPC message from an agent: a tool call's
// arguments, not a file upload.
const maxMessage = 16 << 20

// Proxy relays one request from agent to its connector c.
func (s *Service) Proxy(w http.ResponseWriter, r *http.Request, c state.Connector, agent string) {
	if !c.Enabled {
		RelayError(w, http.StatusConflict, fmt.Sprintf("the connector %s is turned off", c.Name))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMessage+1))
	if err != nil {
		RelayError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body) > maxMessage {
		RelayError(w, http.StatusRequestEntityTooLarge, "the message is too large to relay")
		return
	}
	for attempt := 0; ; attempt++ {
		name, value, err := s.Header(r.Context(), c, agent, attempt > 0)
		if err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, ErrNotConnected) {
				code = http.StatusConflict
			}
			RelayError(w, code, err.Error())
			return
		}
		var reader io.Reader
		if len(body) > 0 {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, c.URL, reader)
		if err != nil {
			RelayError(w, http.StatusBadGateway, err.Error())
			return
		}
		for _, h := range forwarded {
			if v := r.Header.Get(h); v != "" {
				req.Header.Set(h, v)
			}
		}
		if name != "" {
			req.Header.Set(name, value)
		}
		resp, err := s.upstream().Do(req)
		if err != nil {
			RelayError(w, http.StatusBadGateway, fmt.Sprintf("couldn't reach %s: %v", c.URL, err))
			return
		}
		// A token the server refuses is refreshed and tried once more: it
		// may have been revoked early, or the clock may be off.
		if resp.StatusCode == http.StatusUnauthorized && c.Auth == api.ConnectorOAuth && attempt == 0 {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxMetadata))
			_ = resp.Body.Close()
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized && c.Auth == api.ConnectorOAuth {
			_ = resp.Body.Close()
			err := s.fail(r.Context(), c, "the server refused its sign-in even after renewing it: connect it again")
			RelayError(w, http.StatusConflict, err.Error())
			return
		}
		copyResponse(w, resp)
		return
	}
}

// copyResponse streams the server's answer back, flushing as it goes, so an
// event stream's messages arrive when they are sent rather than when it ends.
func copyResponse(w http.ResponseWriter, resp *http.Response) {
	defer func() { _ = resp.Body.Close() }()
	for _, h := range returned {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flush := http.NewResponseController(w).Flush
	_ = flush()
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = flush()
		}
		if err != nil {
			return
		}
	}
}

// upstream is the relay's client: a redirect is handed back rather than
// followed, since following one to plain http on the same host would carry the
// token along, and an MCP server has no reason to redirect its endpoint. It
// dials with OAuth.Dialer, so a name that resolves to this machine doesn't
// take an agent's requests to it.
func (s *Service) upstream() *http.Client {
	if s.Upstream != nil {
		return s.Upstream
	}
	s.upstreamOnce.Do(func() {
		s.relayClient = &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           s.OAuth.Dialer().DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: time.Second,
			},
		}
	})
	return s.relayClient
}

// RelayError is the daemon's own refusal, as the API's usual error: the relay
// inside the agent tells it from the server's answer by X-Agentbox-Relay, and
// turns it into a JSON-RPC error for the request.
func RelayError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(RelayHeader, "1")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(api.Error{Error: msg})
}

// RelayHeader marks an answer as the daemon's rather than the server's.
const RelayHeader = "X-Agentbox-Relay"
