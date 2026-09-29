package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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
}

// Restarting: after minBackoff, doubling each time up to maxBackoff, back to
// minBackoff once a run has lasted stableAfter. Variables for tests.
var (
	minBackoff  = time.Second
	maxBackoff  = time.Minute
	stableAfter = 2 * time.Minute
)

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

	args := []string{"tunnel", "--no-autoupdate"}
	named := t.cfg.Token != ""
	if named {
		args = append(args, "run")
	} else {
		args = append(args, "--url", t.cfg.Origin)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = t.env()
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	setDeathSignal(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	lastErr := make(chan string, 1)
	go func() {
		lastErr <- t.watch(pr, named)
	}()
	werr := cmd.Wait()
	_ = pw.Close()
	last := <-lastErr
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case last != "":
		return errors.New(last)
	case werr != nil:
		return fmt.Errorf("cloudflared stopped: %v", werr)
	}
	return errors.New("cloudflared stopped")
}

// watch reads cloudflared's log for the tunnel's address and for when it's
// connected, and returns the last error it logged.
func (t *Tunnel) watch(r io.Reader, named bool) string {
	var url, last string
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
			t.set(func(s *Status) { *s = Status{State: StateRunning, URL: url} })
		}
		if msg, ok := logError(line); ok {
			last = msg
		}
	}
	_, _ = io.Copy(io.Discard, r)
	return last
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
