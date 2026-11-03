package lan

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
)

// Phones on the local network (internal/daemon/lan.go) reach a daemon that
// runs in a VM through the computer the VM runs on: the network is that
// computer's, and the VM's own network isn't on it. While the daemon has
// phones on, a process on that computer opens their port there, on every
// interface, and passes each request on to the daemon over its socket, under
// /v1/lan/net/, where the daemon serves phones and checks they're paired.
// Every request goes there: nothing of the rest of the daemon's API is
// reachable from the network. It says how that went, and at which of the
// computer's addresses, every ReportEvery (PUT /v1/lan/host).
//
// Which process that is depends on the front end: the supervisor of a Cloud
// Hypervisor or vz VM (internal/hostvm/chv), `agentbox vm lan` beside a Lima
// VM on a Mac (internal/hostvm), and the app's relay on Windows
// (internal/hostwsl).

// ReportEvery is how often a Forward looks at whether phones are on, and
// reports on its port.
const ReportEvery = 3 * time.Second

// How often a Forward asks the firewall again while the port is open: every
// firewallEvery while it warns, as the user may be allowing it, and every
// firewallQuiet otherwise, as a rule may come to block it.
const (
	firewallEvery = time.Minute
	firewallQuiet = 10 * time.Minute
)

// Forward opens phones' port on this computer for a daemon in a VM.
type Forward struct {
	// Listen opens the port; Addresses are this computer's, as Addresses.
	Listen    func(port int) (net.Listener, error)
	Addresses func() []string
	// Open is false while the VM is paused, when nothing is asked of the
	// daemon and phones are told so.
	Open func() bool
	// Firewall says what of this computer's firewall would turn phones away
	// from port, and what to allow: "" when nothing does, or it can't tell.
	// nil asks nothing.
	Firewall func(ctx context.Context, port int) string
	Logf     func(format string, args ...any)

	// daemon reaches the daemon's API; proxy passes phones' requests on.
	daemon *http.Client
	proxy  http.Handler

	srv     *http.Server
	port    int
	err     string
	warning string    // what Firewall said
	asked   time.Time // when
}

// NewForward is a Forward to the daemon whose socket dial reaches.
func NewForward(dial func(ctx context.Context) (net.Conn, error), logf func(string, ...any)) *Forward {
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
	return &Forward{
		daemon:    &http.Client{Transport: transport, Timeout: 5 * time.Second},
		proxy:     proxy,
		Listen:    func(port int) (net.Listener, error) { return net.Listen("tcp", fmt.Sprintf(":%d", port)) },
		Addresses: Addresses,
		Open:      func() bool { return true },
		Logf:      logf,
	}
}

// Run keeps the port as the daemon wants it until ctx ends, and closes it.
func (f *Forward) Run(ctx context.Context) {
	defer f.close()
	for {
		f.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(ReportEvery):
		}
	}
}

// tick opens or closes the port to match what the daemon says, and reports.
func (f *Forward) tick(ctx context.Context) {
	if !f.Open() {
		return
	}
	var st api.LANStatus
	if err := f.call(ctx, http.MethodGet, "/v1/lan", nil, &st); err != nil {
		return // the daemon isn't up, or is restarting: leave things as they are
	}
	if !st.Enabled {
		if f.srv != nil {
			f.close()
			f.Logf("phones: port %d closed", f.port)
			_ = f.call(ctx, http.MethodPut, "/v1/lan/host", api.LANHostReport{Port: f.port}, nil)
		}
		return
	}
	if f.srv != nil && f.port != st.Port {
		f.close()
	}
	if f.srv == nil {
		f.port, f.err, f.warning, f.asked = st.Port, "", "", time.Time{}
		ln, err := f.Listen(st.Port)
		if err != nil {
			f.err = fmt.Sprintf("port %d can't be opened on this computer: %v", st.Port, err)
		} else {
			f.srv = &http.Server{Handler: f.handler(), ReadHeaderTimeout: 10 * time.Second}
			go func(srv *http.Server) { _ = srv.Serve(ln) }(f.srv)
			f.Logf("phones: port %d open, passed on to the daemon", st.Port)
		}
	}
	every := firewallQuiet
	if f.warning != "" {
		every = firewallEvery
	}
	if f.srv != nil && f.Firewall != nil && time.Since(f.asked) >= every {
		was := f.warning
		f.warning, f.asked = f.Firewall(ctx, f.port), time.Now()
		if f.warning != "" && f.warning != was {
			f.Logf("phones: %s", f.warning)
		}
	}
	report := api.LANHostReport{Port: f.port, Listening: f.srv != nil, Error: f.err, Warning: f.warning, Addresses: f.Addresses()}
	if report.Addresses == nil {
		report.Addresses = []string{}
	}
	_ = f.call(ctx, http.MethodPut, "/v1/lan/host", report, nil)
}

func (f *Forward) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !f.Open() {
			http.Error(w, "AgentBox's VM is paused.", http.StatusServiceUnavailable)
			return
		}
		f.proxy.ServeHTTP(w, r)
	})
}

func (f *Forward) close() {
	if f.srv != nil {
		_ = f.srv.Close()
		f.srv = nil
	}
}

func (f *Forward) call(ctx context.Context, method, path string, body, out any) error {
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
