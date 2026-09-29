package chv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/lan"
)

// Phones on the local network (internal/daemon/lan.go) reach the daemon in
// the VM through the host: the network is the host's, and passt brings
// nothing into the VM that the VM didn't ask for. While the daemon has it
// turned on, the supervisor opens its port on the host, on every interface,
// and passes each request on to the daemon over its socket, under
// /v1/lan/net/, where the daemon serves phones and checks they're paired.
// Every request goes there: nothing of the rest of the daemon's API is
// reachable from the network. It says how that went, and at which of the
// host's addresses, every lanReportEvery (PUT /v1/lan/host).

// lanReportEvery is how often the supervisor looks at whether phones are on,
// and reports on its port.
const lanReportEvery = 3 * time.Second

type lanForward struct {
	// daemon reaches the daemon's API; proxy passes phones' requests on.
	daemon    *http.Client
	proxy     http.Handler
	listen    func(port int) (net.Listener, error)
	addresses func() []string
	open      func() bool // false while the VM is paused
	logf      func(format string, args ...any)

	srv  *http.Server
	port int
	err  string
}

func newLANForward(dial func(ctx context.Context) (net.Conn, error), open func() bool, logf func(string, ...any)) *lanForward {
	transport := &http.Transport{
		DialContext:     func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		IdleConnTimeout: 30 * time.Second,
	}
	target := &url.URL{Scheme: "http", Host: "agentbox", Path: "/v1/lan/net"}
	proxy := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1, // the event stream
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "AgentBox's daemon doesn't answer: "+err.Error(), http.StatusBadGateway)
		},
	}
	return &lanForward{
		daemon:    &http.Client{Transport: transport, Timeout: 5 * time.Second},
		proxy:     proxy,
		listen:    func(port int) (net.Listener, error) { return net.Listen("tcp", fmt.Sprintf(":%d", port)) },
		addresses: lan.Addresses,
		open:      open,
		logf:      logf,
	}
}

func (f *lanForward) run(ctx context.Context) {
	defer f.close()
	for {
		f.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(lanReportEvery):
		}
	}
}

// tick opens or closes the port to match what the daemon says, and reports.
func (f *lanForward) tick(ctx context.Context) {
	if !f.open() {
		return
	}
	var st api.LANStatus
	if err := f.call(ctx, http.MethodGet, "/v1/lan", nil, &st); err != nil {
		return // the daemon isn't up, or is restarting: leave things as they are
	}
	if !st.Enabled {
		if f.srv != nil {
			f.close()
			f.logf("phones: port %d closed", f.port)
			_ = f.call(ctx, http.MethodPut, "/v1/lan/host", api.LANHostReport{Port: f.port}, nil)
		}
		return
	}
	if f.srv != nil && f.port != st.Port {
		f.close()
	}
	if f.srv == nil {
		f.port, f.err = st.Port, ""
		ln, err := f.listen(st.Port)
		if err != nil {
			f.err = fmt.Sprintf("port %d can't be opened on this computer: %v", st.Port, err)
		} else {
			f.srv = &http.Server{Handler: f.handler(), ReadHeaderTimeout: 10 * time.Second}
			go func(srv *http.Server) { _ = srv.Serve(ln) }(f.srv)
			f.logf("phones: port %d open, passed on to the daemon", st.Port)
		}
	}
	report := api.LANHostReport{Port: f.port, Listening: f.srv != nil, Error: f.err, Addresses: f.addresses()}
	if report.Addresses == nil {
		report.Addresses = []string{}
	}
	_ = f.call(ctx, http.MethodPut, "/v1/lan/host", report, nil)
}

func (f *lanForward) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !f.open() {
			http.Error(w, "AgentBox's VM is paused.", http.StatusServiceUnavailable)
			return
		}
		f.proxy.ServeHTTP(w, r)
	})
}

func (f *lanForward) close() {
	if f.srv != nil {
		_ = f.srv.Close()
		f.srv = nil
	}
}

func (f *lanForward) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://agentbox"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.daemon.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return errors.New(resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
