package tunnel

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Tailscale instead of cloudflared, when the hub this machine connects to
// gives out tailnet addresses: the daemon joins the hub's tailnet as a node
// of its own and serves the phone listener on it with tailscale serve, so
// only the tailnet's devices reach it, and nothing is exposed to the internet.
//
// tailscaled runs in userspace mode (--tun=userspace-networking), as the
// daemon's own child, with its state and socket under AgentBox's data. That
// needs no root and no /dev/net/tun, and it leaves the machine's own network
// alone: no tailscale0, no routes, no DNS, nothing that could clash with the
// agents' bridges or with a Tailscale the user runs themselves. A userspace
// node can't dial out into the tailnet without a proxy, but it needn't:
// serve takes connections in, which is all phones do.

// TailscaleConfig is what a tailnet node runs.
type TailscaleConfig struct {
	// Binaries finds the directory of tailscale and tailscaled, downloading
	// them if need be (TailscaleInstaller.Ensure).
	Binaries func(ctx context.Context, status func(string)) (string, error)
	// Origin is the local address served on the node, like
	// http://127.0.0.1:7781.
	Origin string
	// AuthKey joins the node to the tailnet the first time; after that its
	// state does. It reaches tailscale in a file only it can read, never on
	// a command line or in a log.
	AuthKey string
	// Hostname is the node's name in the tailnet, which its MagicDNS name
	// starts with.
	Hostname string
	// StateDir keeps the node's state and tailscaled's socket. Owner says
	// whose node it is (the hub and environment it joined for): a state of
	// another owner's is forgotten, so the node joins with AuthKey rather
	// than staying in another tailnet. One tailscaled runs in it at a time.
	StateDir, Owner string
	// LoginServer is a control server other than Tailscale's, such as
	// Headscale; "" is Tailscale's.
	LoginServer string
	// OnUp is called, from another goroutine, each time the node serves.
	OnUp func(TailscaleNode)
}

// TailscaleNode is where the node serves, once it does.
type TailscaleNode struct {
	URL     string // https://<name>.<tailnet>.ts.net/
	DNSName string // <name>.<tailnet>.ts.net
	NodeID  string // its stable ID, like nXXXXCNTRL
	IPs     []string
	Tailnet string // the tailnet's name, as Tailscale shows it
}

// Tailscale is a supervised tailscaled serving one origin on its node. It
// has the same Status and Stop as a Tunnel.
type Tailscale struct {
	cfg      TailscaleConfig
	onChange func(Status)
	logf     func(format string, args ...any)
	cancel   context.CancelFunc
	done     chan struct{}

	mu sync.Mutex
	st Status
}

// Times a run waits on, variables for tests.
var (
	tailscaledStartup = 30 * time.Second
	tailscaleUp       = 2 * time.Minute
	tailscaleServe    = time.Minute
	tailscaleCheck    = 30 * time.Second
)

// StartTailscale runs tailscaled until Stop, or ctx ends, starting it again
// whenever it stops.
func StartTailscale(ctx context.Context, cfg TailscaleConfig, onChange func(Status), logf func(format string, args ...any)) *Tailscale {
	ctx, cancel := context.WithCancel(ctx)
	t := &Tailscale{cfg: cfg, onChange: onChange, logf: logf, cancel: cancel, done: make(chan struct{}), st: Status{State: StateStarting}}
	go t.supervise(ctx)
	return t
}

// Status is where the node has got to.
func (t *Tailscale) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st
}

// Stop ends tailscaled and waits for it to be gone. The node stays in the
// tailnet, offline, and comes back with its state.
func (t *Tailscale) Stop() {
	t.cancel()
	<-t.done
}

func (t *Tailscale) set(update func(*Status)) {
	t.mu.Lock()
	before := t.st
	update(&t.st)
	after := t.st
	t.mu.Unlock()
	if after != before && t.onChange != nil {
		t.onChange(after)
	}
}

func (t *Tailscale) supervise(ctx context.Context) {
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
		msg := "tailscaled stopped"
		if err != nil {
			msg = t.scrub(err.Error())
		}
		t.logf("phones: Tailscale stopped (%s); starting it again in %s", msg, backoff)
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

func (t *Tailscale) socket() string { return filepath.Join(t.cfg.StateDir, "tailscaled.sock") }

// run runs tailscaled once, brings the node up and serves on it, and watches
// it until tailscaled exits, the node stops running, or ctx ends.
func (t *Tailscale) run(ctx context.Context) error {
	dir, err := t.cfg.Binaries(ctx, func(detail string) {
		t.set(func(s *Status) { *s = Status{State: StateStarting, Error: detail} })
	})
	if err != nil {
		return err
	}
	// A node started before this one in the same directory, whose settings
	// changed, may still be stopping.
	release, err := lockState(ctx, t.cfg.StateDir)
	if err != nil {
		return err
	}
	defer release()
	t.set(func(s *Status) { *s = Status{State: StateStarting, Error: "joining the tailnet"} })
	if err := ownState(t.cfg.StateDir, t.cfg.Owner); err != nil {
		return fmt.Errorf("setting up the node's state: %w", err)
	}
	sock := t.socket()
	_ = os.Remove(sock)

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(runCtx, filepath.Join(dir, "tailscaled"),
		"--tun=userspace-networking", "--statedir="+t.cfg.StateDir, "--socket="+sock, "--port=0", "--no-logs-no-support")
	cmd.Env = tailscaleEnv()
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	setDeathSignal(cmd)
	var log tailBuffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	fail := func(err error) error {
		stop()
		<-exited
		return err
	}

	// Its socket appears once it can be talked to.
	deadline := time.Now().Add(tailscaledStartup)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		select {
		case err := <-exited:
			return tailscaledExit(err, log.last())
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fail(fmt.Errorf("tailscaled didn't start in %s", tailscaledStartup))
		}
	}

	if err := t.up(runCtx, dir); err != nil {
		return fail(err)
	}
	if err := t.serve(runCtx, dir); err != nil {
		return fail(err)
	}
	node, err := t.node(runCtx, dir)
	if err != nil {
		return fail(err)
	}
	t.set(func(s *Status) { *s = Status{State: StateRunning, URL: node.URL} })
	if t.cfg.OnUp != nil {
		go t.cfg.OnUp(node)
	}

	for {
		select {
		case err := <-exited:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return tailscaledExit(err, log.last())
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-time.After(tailscaleCheck):
		}
		if _, err := t.node(runCtx, dir); err != nil && ctx.Err() == nil {
			return fail(err)
		}
	}
}

// TailscaleOwnerFile is the file in a node's StateDir that says its Owner.
const TailscaleOwnerFile = "agentbox-owner"

// ownState makes dir the state of owner's node: emptied, unless it already
// is.
func ownState(dir, owner string) error {
	marker := filepath.Join(dir, TailscaleOwnerFile)
	if got, err := os.ReadFile(marker); err == nil && string(got) == owner {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(owner), 0o600)
}

var stateLocks sync.Map // StateDir → chan struct{}, holding one

func lockState(ctx context.Context, dir string) (func(), error) {
	v, _ := stateLocks.LoadOrStore(dir, make(chan struct{}, 1))
	ch := v.(chan struct{})
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func tailscaledExit(err error, last string) error {
	switch {
	case last != "":
		return fmt.Errorf("tailscaled stopped: %s", last)
	case err != nil:
		return fmt.Errorf("tailscaled stopped: %v", err)
	}
	return errors.New("tailscaled stopped")
}

// up joins the tailnet with the auth key, or with the node's state when it
// already has, as the node named Hostname. --reset makes every run's settings
// the same whatever an earlier one set.
func (t *Tailscale) up(ctx context.Context, dir string) error {
	args := []string{"up", "--reset", "--hostname=" + t.cfg.Hostname, "--accept-dns=false", "--timeout=" + tailscaleUp.String()}
	if t.cfg.LoginServer != "" {
		args = append(args, "--login-server="+t.cfg.LoginServer)
	}
	if t.cfg.AuthKey != "" {
		keyFile := filepath.Join(t.cfg.StateDir, "authkey")
		if err := os.WriteFile(keyFile, []byte(t.cfg.AuthKey), 0o600); err != nil {
			return err
		}
		defer func() { _ = os.Remove(keyFile) }()
		args = append(args, "--auth-key=file:"+keyFile)
	}
	ctx, cancel := context.WithTimeout(ctx, tailscaleUp+10*time.Second)
	defer cancel()
	if out, err := t.cli(ctx, dir, args...); err != nil {
		return fmt.Errorf("joining the tailnet: %s", explainTailscale(out, err))
	}
	return nil
}

// serve serves Origin on the node's https port, which needs HTTPS turned on
// for the tailnet (its MagicDNS name's certificate). --bg keeps it in the
// node's state, and --yes takes no questions.
func (t *Tailscale) serve(ctx context.Context, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, tailscaleServe)
	defer cancel()
	if out, err := t.cli(ctx, dir, "serve", "--bg", "--yes", t.cfg.Origin); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("it didn't answer in %s", tailscaleServe)
		}
		return fmt.Errorf("serving on the tailnet: %s", explainTailscale(out, err))
	}
	return nil
}

// tailscaleStatus is what node reads of tailscale status --json.
type tailscaleStatus struct {
	BackendState string
	Self         *struct {
		ID           string
		DNSName      string
		TailscaleIPs []string
	}
	CurrentTailnet *struct {
		Name string
	}
	AuthURL string
}

// node reads where the node serves, and fails unless it's running.
func (t *Tailscale) node(ctx context.Context, dir string) (TailscaleNode, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := t.cli(ctx, dir, "status", "--json")
	if err != nil {
		return TailscaleNode{}, fmt.Errorf("reading the node's status: %s", explainTailscale(out, err))
	}
	return parseTailscaleStatus(out)
}

func parseTailscaleStatus(out []byte) (TailscaleNode, error) {
	var st tailscaleStatus
	if err := json.Unmarshal(out, &st); err != nil {
		return TailscaleNode{}, fmt.Errorf("tailscale's status isn't JSON: %w", err)
	}
	switch st.BackendState {
	case "Running":
	case "NeedsLogin":
		return TailscaleNode{}, errors.New("this machine is signed out of the tailnet (its node was removed, or its key expired): connect it to the hub again")
	case "NeedsMachineAuth":
		return TailscaleNode{}, errors.New("this machine waits to be approved in the tailnet's admin console (Machines)")
	default:
		return TailscaleNode{}, fmt.Errorf("the node is %s, not running", cmp.Or(st.BackendState, "in no state"))
	}
	if st.Self == nil {
		return TailscaleNode{}, errors.New("tailscale's status has no node")
	}
	name := strings.TrimSuffix(st.Self.DNSName, ".")
	if !hostnameRe.MatchString(name) {
		return TailscaleNode{}, fmt.Errorf("the node has no MagicDNS name (%q): turn MagicDNS on for the tailnet", st.Self.DNSName)
	}
	n := TailscaleNode{URL: "https://" + name + "/", DNSName: name, NodeID: st.Self.ID, IPs: st.Self.TailscaleIPs}
	if st.CurrentTailnet != nil {
		n.Tailnet = st.CurrentTailnet.Name
	}
	return n, nil
}

var hostnameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// cli runs tailscale against this node's tailscaled, and returns what it
// printed.
func (t *Tailscale) cli(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "tailscale"), append([]string{"--socket=" + t.socket()}, args...)...)
	cmd.Env = tailscaleEnv()
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return []byte(t.scrub(out.String())), err
}

var enableURL = regexp.MustCompile(`https://login\.tailscale\.com/f/[^\s"]+`)

// explainTailscale is why a tailscale command failed, in its own words when
// it said any, and what to do about the ones a user can fix.
func explainTailscale(out []byte, err error) string {
	text := strings.TrimSpace(string(out))
	if u := enableURL.FindString(text); u != "" {
		return "turn on HTTPS certificates for the tailnet (MagicDNS too), then try again: " + u
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "enabling https feature") || strings.Contains(lower, "https is not enabled") || strings.Contains(lower, "serve is not enabled"):
		return "turn on HTTPS certificates for the tailnet (MagicDNS too), then try again (" + lastLine(text) + ")"
	case strings.Contains(lower, "invalid key") || strings.Contains(lower, "key expired") || strings.Contains(lower, "not valid"):
		return "the tailnet refused the hub's auth key (" + lastLine(text) + "): check the key or OAuth client the hub runs with"
	case strings.Contains(lower, "requested tags") && strings.Contains(lower, "invalid or not permitted"):
		return "the tailnet won't tag this machine (" + lastLine(text) + "): the tag needs an owner in the tailnet's policy (tagOwners)"
	}
	if text != "" {
		return lastLine(text)
	}
	return err.Error()
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// scrub takes the auth key out of anything tailscale printed.
func (t *Tailscale) scrub(text string) string {
	if t.cfg.AuthKey != "" {
		text = strings.ReplaceAll(text, t.cfg.AuthKey, "[auth key]")
	}
	return text
}

// tailscaleEnv is the daemon's environment less Tailscale's own settings, so
// a TS_AUTHKEY or TS_SOCKET of the user's doesn't change what it does.
func tailscaleEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TS_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// tailBuffer keeps the last line tailscaled wrote, for why it stopped.
type tailBuffer struct {
	mu   sync.Mutex
	line string
	part []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.part = append(b.part, p...)
	for {
		i := bytes.IndexByte(b.part, '\n')
		if i < 0 {
			break
		}
		if l := strings.TrimSpace(string(b.part[:i])); l != "" {
			b.line = l
		}
		b.part = b.part[i+1:]
	}
	if len(b.part) > 64<<10 {
		b.part = b.part[len(b.part)-(64<<10):]
	}
	return len(p), nil
}

func (b *tailBuffer) last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.line
}
