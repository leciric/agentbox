package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The states a tunnel is in.
const (
	StateStarting = "starting" // cloudflared is starting, or being downloaded
	StateRunning  = "running"  // Cloudflare's edge has a connection: URL works
	StateFailed   = "failed"   // cloudflared stopped; it's started again after a while
)

// Status is where a tunnel has got to.
type Status struct {
	State string
	URL   string // the https address phones open, once known
	Error string // why it isn't running, when it isn't
}

// Config is what a tunnel runs.
type Config struct {
	// Binary finds cloudflared, downloading it if need be (Installer.Ensure);
	// status says what it is doing meanwhile.
	Binary func(ctx context.Context, status func(string)) (string, error)
	// Origin is the local address the tunnel brings requests to, like
	// http://127.0.0.1:7781.
	Origin string
	// Token is a named tunnel's, from Cloudflare's dashboard; "" is a quick
	// tunnel. It goes to cloudflared in its environment, never on its
	// command line, where any process could read it, and never in a log.
	Token string
	// Hostname is a named tunnel's public hostname, which its token doesn't
	// say: the address phones open is https://<Hostname>/.
	Hostname string
	// Home is cloudflared's HOME, so a ~/.cloudflared of the user's doesn't
	// change what it does: a config file there turns a quick tunnel down.
	Home string
	// Resolve looks a quick tunnel's name up in DNS; nil asks its zone's
	// nameservers (authoritativeResolve).
	Resolve func(ctx context.Context, host string) error
}

// Restarting: after minBackoff, doubling each time up to maxBackoff, back to
// minBackoff once a run has lasted stableAfter. Variables for tests.
var (
	minBackoff  = time.Second
	maxBackoff  = time.Minute
	stableAfter = 2 * time.Minute
)

// A quick tunnel's name reaches DNS some seconds after cloudflared connects,
// and a phone that opens it before then caches "no such host": it's shown
// once it resolves, asked every resolveEvery, or after resolveFor anyway, for
// a network that blocks outside DNS. Variables for tests.
var (
	resolveEvery = time.Second
	resolveFor   = time.Minute
)

// cloudflared that loses its connection to Cloudflare's edge keeps trying
// again without exiting, and a quick tunnel it can't register again is gone,
// its name out of DNS. Its /ready, asked every healthEvery, says so: the
// address is hidden meanwhile, and cloudflared started again after lostAfter.
// Variables for tests, like metricsAddr, where cloudflared serves /ready.
var (
	healthEvery = 5 * time.Second
	lostAfter   = time.Minute
	metricsAddr = freeLocalAddr
)

// errLost is why cloudflared was stopped for lacking a connection.
var errLost = errors.New("cloudflared lost its connection to Cloudflare")

// freeLocalAddr is a loopback address with a port free right now.
func freeLocalAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String(), nil
}

// authoritativeResolve asks the nameservers of host's zone, which have a new
// quick tunnel's name first: a caching resolver, 1.1.1.1 included, keeps
// answering "no such host" a few seconds longer once it has asked.
func authoritativeResolve(ctx context.Context, host string) error {
	_, zone, _ := strings.Cut(host, ".")
	ns, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil {
		return err
	}
	if len(ns) == 0 {
		return fmt.Errorf("%s has no nameservers", zone)
	}
	for _, server := range ns {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(server.Host, "53"))
		}}
		// A share of ctx each, so one that doesn't answer leaves time for the next.
		ask, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err = r.LookupHost(ask, host)
		cancel()
		if err == nil {
			return nil
		}
	}
	return err
}

// Tunnel is a supervised cloudflared.
type Tunnel struct {
	cfg      Config
	onChange func(Status)
	logf     func(format string, args ...any)
	cancel   context.CancelFunc
	done     chan struct{}

	mu sync.Mutex
	st Status
}

// Start runs cloudflared until Stop, or ctx ends, starting it again whenever
// it stops. onChange is called, from another goroutine, each time the status
// changes.
func Start(ctx context.Context, cfg Config, onChange func(Status), logf func(format string, args ...any)) *Tunnel {
	ctx, cancel := context.WithCancel(ctx)
	t := &Tunnel{cfg: cfg, onChange: onChange, logf: logf, cancel: cancel, done: make(chan struct{}), st: Status{State: StateStarting}}
	go t.supervise(ctx)
	return t
}

// Status is where the tunnel has got to.
func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st
}

// Stop ends cloudflared and waits for it to be gone.
func (t *Tunnel) Stop() {
	t.cancel()
	<-t.done
}

func (t *Tunnel) set(update func(*Status)) {
	t.mu.Lock()
	before := t.st
	update(&t.st)
	after := t.st
	t.mu.Unlock()
	if after != before && t.onChange != nil {
		t.onChange(after)
	}
}

func (t *Tunnel) supervise(ctx context.Context) {
	defer close(t.done)
	backoff := minBackoff
	for {
		started := time.Now()
		err := t.run(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= stableAfter {
			backoff = minBackoff
		}
		msg := "cloudflared stopped"
		if err != nil {
			msg = err.Error()
		}
		t.logf("phones: the tunnel stopped (%s); starting it again in %s", msg, backoff)
		t.set(func(s *Status) {
			*s = Status{State: StateFailed, Error: fmt.Sprintf("%s: trying again in %s", msg, backoff.Round(time.Second))}
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, maxBackoff)
	}
}

// quickURL is the address cloudflared prints for a quick tunnel.
var quickURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// run runs cloudflared once, until it exits or ctx ends.
func (t *Tunnel) run(ctx context.Context) error {
	bin, err := t.cfg.Binary(ctx, func(detail string) {
		t.set(func(s *Status) { *s = Status{State: StateStarting, Error: detail} })
	})
	if err != nil {
		return err
	}
	t.set(func(s *Status) { *s = Status{State: StateStarting} })

	metrics, err := metricsAddr()
	if err != nil {
		return err
	}
	args := []string{"tunnel", "--no-autoupdate", "--metrics", metrics}
	named := t.cfg.Token != ""
	if named {
		args = append(args, "run")
	} else {
		args = append(args, "--url", t.cfg.Origin)
	}
	runCtx, stopRun := context.WithCancelCause(ctx)
	defer stopRun(nil)
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Env = t.env()
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	setDeathSignal(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	lastErr := make(chan string, 1)
	go func() {
		lastErr <- t.watch(watchCtx, pr, named)
	}()
	var health sync.WaitGroup
	health.Go(func() { t.watchHealth(watchCtx, metrics, named, stopRun) })
	werr := cmd.Wait()
	stopWatch()
	health.Wait()
	_ = pw.Close()
	last := <-lastErr
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case errors.Is(context.Cause(runCtx), errLost):
		return errLost
	case last != "":
		return errors.New(last)
	case werr != nil:
		return fmt.Errorf("cloudflared stopped: %v", werr)
	}
	return errors.New("cloudflared stopped")
}

// watch reads cloudflared's log for the tunnel's address and for when it's
// connected, and returns the last error it logged.
func (t *Tunnel) watch(ctx context.Context, r io.Reader, named bool) string {
	var url, last string
	var resolving sync.WaitGroup
	shown := false
	defer resolving.Wait()
	if named {
		url = "https://" + t.cfg.Hostname + "/"
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := t.scrub(sc.Text())
		if !named && url == "" {
			if u := quickURL.FindString(line); u != "" {
				url = u + "/"
			}
		}
		if strings.Contains(line, "Registered tunnel connection") && url != "" {
			// Once: after that, watchHealth says whether it's connected.
			switch {
			case shown:
			case named:
				shown = true
				t.set(func(s *Status) { *s = Status{State: StateRunning, URL: url} })
			default:
				shown = true
				resolving.Go(func() { t.runningOnceResolved(ctx, url) })
			}
		}
		if msg, ok := logError(line); ok {
			last = msg
		}
	}
	_, _ = io.Copy(io.Discard, r)
	return last
}

// watchHealth hides the address while cloudflared's /ready at addr says it
// has no connection, or stops answering after it has, shows it again once it
// has one and a quick tunnel's name resolves, and stops cloudflared with
// errLost once it has gone lostAfter without, starting up included.
func (t *Tunnel) watchHealth(ctx context.Context, addr string, named bool, stop context.CancelCauseFunc) {
	var lostSince time.Time
	var hidden string // the address, while it's hidden
	answeredOnce := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(healthEvery):
		}
		ready, answered := askReady(ctx, addr)
		answeredOnce = answeredOnce || answered
		if !answeredOnce {
			continue // not serving yet, or a cloudflared that doesn't
		}
		if ready && hidden != "" && !named && t.resolve(ctx, hostOf(hidden)) != nil {
			ready = false // reconnected, but its name isn't back in DNS
		}
		if ready {
			lostSince = time.Time{}
			if hidden != "" {
				url := hidden
				hidden = ""
				t.logf("phones: the tunnel is connected to Cloudflare again")
				t.set(func(s *Status) {
					if ctx.Err() == nil {
						*s = Status{State: StateRunning, URL: url}
					}
				})
			}
			continue
		}
		if lostSince.IsZero() {
			lostSince = time.Now()
		}
		if st := t.Status(); st.State == StateRunning {
			hidden = st.URL
			t.logf("phones: the tunnel lost its connection to Cloudflare; %s is hidden until it's back", st.URL)
			t.set(func(s *Status) {
				*s = Status{State: StateStarting, Error: "lost its connection to Cloudflare: reconnecting"}
			})
		}
		if time.Since(lostSince) >= lostAfter {
			stop(errLost)
			return
		}
	}
}

// askReady asks cloudflared's /ready whether it has a connection to
// Cloudflare's edge; answered is false when nothing answered.
func askReady(ctx context.Context, addr string) (ready, answered bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ready", nil)
	if err != nil {
		return false, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK, true
}

// runningOnceResolved says the quick tunnel at url is running once its name
// is in DNS, unless ctx, this run of cloudflared, ends first.
func (t *Tunnel) runningOnceResolved(ctx context.Context, url string) {
	host := hostOf(url)
	giveUp := time.After(resolveFor)
wait:
	for {
		err := t.resolve(ctx, host)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-giveUp:
			t.logf("phones: %s isn't in DNS after %s (%v); showing it anyway", host, resolveFor, err)
			break wait
		case <-time.After(resolveEvery):
		}
	}
	t.set(func(s *Status) {
		if ctx.Err() == nil {
			*s = Status{State: StateRunning, URL: url}
		}
	})
}

// resolve looks host up with the tunnel's Resolve, for at most 5 seconds.
func (t *Tunnel) resolve(ctx context.Context, host string) error {
	resolve := t.cfg.Resolve
	if resolve == nil {
		resolve = authoritativeResolve
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return resolve(ctx, host)
}

// hostOf is the host of a tunnel's https://host/ address.
func hostOf(url string) string {
	return strings.TrimSuffix(strings.TrimPrefix(url, "https://"), "/")
}

// logError picks out an error line of cloudflared's log, like
// `2026-09-29T10:00:00Z ERR Couldn't start tunnel error="..."`, without its
// timestamp.
func logError(line string) (string, bool) {
	for _, level := range []string{" ERR ", " FTL "} {
		if i := strings.Index(line, level); i >= 0 {
			return strings.TrimSpace(line[i+len(level):]), true
		}
	}
	return "", false
}

// scrub takes the token out of a line of cloudflared's log, should it ever
// print it: its log reaches AgentBox's.
func (t *Tunnel) scrub(line string) string {
	if t.cfg.Token != "" {
		line = strings.ReplaceAll(line, t.cfg.Token, "[token]")
	}
	return line
}

// env is cloudflared's environment: the daemon's, less whatever of
// cloudflared's own settings it may have, with the token and HOME.
func (t *Tunnel) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TUNNEL_") || strings.HasPrefix(kv, "HOME=") {
			continue
		}
		env = append(env, kv)
	}
	if t.cfg.Home != "" {
		env = append(env, "HOME="+t.cfg.Home)
	}
	if t.cfg.Token != "" {
		env = append(env, "TUNNEL_TOKEN="+t.cfg.Token)
	}
	return env
}
