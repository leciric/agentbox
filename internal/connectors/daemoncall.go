package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"agentbox/internal/state"
)

// The daemon calling a connector's tools itself, for the app — Hatch's
// get_page, to show an artifact — the way an agent's relay would: the same
// Relay, its requests handed straight to Proxy instead of over a socket, so
// they get the same credentials, refreshing and refusals.

// CallTool calls one of c's tools with arguments (marshalled to a JSON
// object), with the credentials forAgent's relay gets: "" for the project's.
func (s *Service) CallTool(ctx context.Context, c state.Connector, forAgent, tool string, arguments any) (ToolResult, error) {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return ToolResult{}, err
	}
	relay := &Relay{
		HTTP: &http.Client{Transport: proxyTransport{s: s, c: c, agent: forAgent}},
		URL:  "http://agentbox/connectors/" + c.Name + "/mcp",
		Log:  io.Discard,
	}
	return relay.CallTool(ctx, tool, raw)
}

// proxyTransport answers a relay's requests with Proxy, streaming its answer
// back as it is written.
type proxyTransport struct {
	s     *Service
	c     state.Connector
	agent string
}

func (t proxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		// A server's request always has one; a client's GET doesn't.
		req = req.Clone(req.Context())
		req.Body = http.NoBody
	}
	pr, pw := io.Pipe()
	w := &pipeResponse{header: http.Header{}, body: pw, ready: make(chan struct{})}
	go func() {
		t.s.Proxy(w, req, t.c, t.agent)
		w.WriteHeader(http.StatusOK) // nothing written: an empty answer
		_ = pw.Close()
	}()
	select {
	case <-w.ready:
	case <-req.Context().Done():
		_ = pr.CloseWithError(req.Context().Err())
		return nil, req.Context().Err()
	}
	return &http.Response{
		Status: http.StatusText(w.code), StatusCode: w.code, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: w.header, Body: pr, ContentLength: -1, Request: req,
	}, nil
}

// pipeResponse is the http.ResponseWriter Proxy writes to, read as the
// relay's response: its headers once WriteHeader is called, then its body.
type pipeResponse struct {
	header http.Header
	body   *io.PipeWriter
	code   int
	ready  chan struct{}
}

func (w *pipeResponse) Header() http.Header { return w.header }

func (w *pipeResponse) WriteHeader(code int) {
	if w.code != 0 {
		return
	}
	w.code = code
	close(w.ready)
}

func (w *pipeResponse) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(p)
}

func (w *pipeResponse) Flush() {}

// ErrTooLarge is a fetched page bigger than Fetch takes.
var ErrTooLarge = errors.New("the page is too large")

// Fetch reads a page a connector's server linked to, like Hatch's
// content_url, which carries its own short-lived credential: none of the
// connector's goes with it. It refuses what CheckURL does, doesn't follow
// redirects, and reads at most limit bytes.
func (s *Service) Fetch(ctx context.Context, raw string, limit int64) (body []byte, header http.Header, err error) {
	if err := s.OAuth.CheckURL(raw); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.upstream().Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't reach %s: %w", Redact(raw), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("%s answered %s", Redact(raw), resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(body)) > limit {
		return nil, nil, ErrTooLarge
	}
	return body, resp.Header, nil
}
