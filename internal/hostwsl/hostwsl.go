// Package hostwsl runs AgentBox on Windows. Nothing of AgentBox is ported:
// the daemon, Incus and every agent run in a WSL2 distro of their own, exactly
// as they run on a Linux machine, and agentbox.exe on Windows is a front end
// for that distro (D94). It is the Windows counterpart of package hostvm, the
// Mac's front end for a Lima VM (PR #84), and shares package hostos with it.
//
//   - `agentbox wsl …` makes, starts, stops and removes the distro (cmd.go,
//     setup.go).
//   - Every other command runs in the distro, in the same working directory
//     where Windows has one there, with the console, the exit status and
//     Ctrl-C of a local one (Forward).
//   - The daemon's unix socket can't be reached from Windows: WSL2 doesn't
//     carry AF_UNIX between Windows and Linux. `agentbox relay` gives the app
//     a named pipe only its user can open, and carries each connection to the
//     socket over one wsl.exe, multiplexed with yamux (relay.go).
//   - The daemon is started by a wsl.exe of its own, which lives as long as it
//     does: WSL stops a distro nothing on Windows is attached to, and that
//     would stop every agent with it (StartDaemon).
//
// The distro's agentbox is the Linux build this front end ships beside it,
// kept identical: a command first compares the two and installs Windows' copy
// when they differ, so updating the app updates the distro.
package hostwsl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/term"

	"agentbox/internal/hostos"
)

const (
	// DefaultName is the WSL distro AgentBox makes; AGENTBOX_WSL_DISTRO
	// changes it.
	DefaultName = "AgentBox"
	// LinuxBinary is the Linux build of agentbox the front end installs in
	// the distro, kept next to the front end itself.
	LinuxBinary = "agentbox-linux"
	// distroBinary is where it goes in the distro.
	distroBinary = "/usr/local/bin/agentbox"
)

// ErrNotCreated is a distro `agentbox wsl init` hasn't made yet.
var ErrNotCreated = errors.New("AgentBox's WSL distro isn't set up: run agentbox wsl init")

// Front reports whether this process is the front end of a WSL distro rather
// than AgentBox itself: always on Windows, and on Linux when
// AGENTBOX_FRONT_END=wsl, which is how the front end is tested without Windows.
func Front() bool {
	return runtime.GOOS == "windows" || os.Getenv("AGENTBOX_FRONT_END") == "wsl"
}

// Distro is AgentBox's WSL distro, seen from Windows.
type Distro struct {
	WSL  string // wsl.exe
	Name string // the distro
	// User is the Linux user AgentBox runs as in the distro: the Windows
	// user's name, made into a Linux one.
	User string
	// Dir is where the distro's disk goes when it's made.
	Dir string
	// Binary is the Linux agentbox kept in the distro.
	Binary string
	// Log is where progress goes: the front end's stderr.
	Log io.Writer
	// Env is added to every wsl.exe the front end runs: tests point the fake
	// one at their files through it.
	Env []string
}

// New finds wsl.exe and the Linux binary, and describes the distro for this
// user. The Distro is returned with what was found even when something wasn't.
func New() (*Distro, error) {
	d := &Distro{
		Name: env("AGENTBOX_WSL_DISTRO", DefaultName),
		User: LinuxUser(windowsUser()),
		Dir:  env("AGENTBOX_WSL_DIR", defaultDir()),
		Log:  os.Stderr,
	}
	var err error
	d.WSL, err = FindWSL()
	if err != nil {
		return d, err
	}
	d.Binary, err = FindLinuxBinary()
	return d, err
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// defaultDir is %LOCALAPPDATA%\AgentBox\wsl: the distro's ext4.vhdx is
// per-user and large, so it belongs in local, not roaming, app data.
func defaultDir() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, "AgentBox", "wsl")
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "agentbox", "wsl")
	}
	return filepath.Join(os.TempDir(), "agentbox-wsl")
}

func windowsUser() string {
	if name := os.Getenv("USERNAME"); name != "" {
		return name
	}
	if u, err := user.Current(); err == nil {
		// DOMAIN\name on Windows.
		return u.Username[strings.LastIndex(u.Username, `\`)+1:]
	}
	return ""
}

var notLinuxName = regexp.MustCompile(`[^a-z0-9_-]+`)

// LinuxUser makes a Windows user name into a Linux one: lower case, letters,
// digits, _ and -, starting with a letter, at most 32 characters. "Ana María"
// is ana-mar-a; a name with nothing usable left is "agentbox".
func LinuxUser(name string) string {
	name = strings.Trim(notLinuxName.ReplaceAllString(strings.ToLower(name), "-"), "-_")
	name = strings.TrimLeft(name, "0123456789-_")
	if len(name) > 32 {
		name = strings.TrimRight(name[:32], "-_")
	}
	switch name {
	case "", "root", "daemon", "bin", "sys", "nobody", "ubuntu":
		// Reserved or taken in Ubuntu's image; "ubuntu" is what its own
		// first-run setup makes, and a user of that name could be another's.
		if name == "" {
			return "agentbox"
		}
		return name + "-agentbox"
	}
	return name
}

// FindWSL finds wsl.exe: AGENTBOX_WSL, System32 (an app started from the Start
// menu has Windows' PATH, but a 32-bit parent would be redirected away from
// the 64-bit one, which is in Sysnative for it), or PATH.
func FindWSL() (string, error) {
	if p := os.Getenv("AGENTBOX_WSL"); p != "" {
		return p, nil
	}
	if root := os.Getenv("SystemRoot"); root != "" {
		for _, dir := range []string{"System32", "Sysnative"} {
			p := filepath.Join(root, dir, "wsl.exe")
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	if p, err := exec.LookPath("wsl.exe"); err == nil {
		return p, nil
	}
	return "", errNoWSL
}

var errNoWSL = errors.New("WSL isn't installed. In PowerShell, as administrator: wsl --install --no-distribution, then restart Windows")

// FindLinuxBinary finds the Linux agentbox to put in the distro: next to this
// binary (the app and the release keep them together), or AGENTBOX_LINUX_BINARY.
func FindLinuxBinary() (string, error) {
	if p := os.Getenv("AGENTBOX_LINUX_BINARY"); p != "" {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	p := filepath.Join(filepath.Dir(exe), LinuxBinary)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("no Linux agentbox next to %s to run in WSL: the app ships one; from a checkout, GOOS=linux GOARCH=amd64 go build -o %s ./cmd/agentbox", exe, p)
	}
	return p, nil
}

// command is wsl.exe with args, its output in UTF-8, run without a console
// window of its own.
func (d *Distro) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, d.WSL, args...)
	// WSL_UTF8 makes wsl.exe's own messages UTF-8 rather than UTF-16.
	cmd.Env = append(append(os.Environ(), "WSL_UTF8=1"), d.Env...)
	hideWindow(cmd)
	// A wsl.exe killed for taking too long can leave a process in the
	// distro holding its output open: don't wait for that one too.
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// How long wsl.exe gets to answer the front end's own quick questions before
// it says WSL is stuck, rather than hang with nothing said, which is all the
// app could show (#433): listing the distros, which boots nothing, and
// running a short command in the distro, which may have to boot it first,
// systemd and all. Variables for tests.
var (
	listTimeout = 30 * time.Second
	execTimeout = 2 * time.Minute
)

// runWithin is run, given up after limit with an error that says so.
func (d *Distro) runWithin(ctx context.Context, limit time.Duration, stdin io.Reader, args ...string) (string, error) {
	tctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := d.run(tctx, stdin, args...)
	if err != nil && ctx.Err() == nil && errors.Is(tctx.Err(), context.DeadlineExceeded) {
		return out, &stuckError{args: args, limit: limit}
	}
	return out, err
}

// stuckError is a wsl.exe that didn't answer in time.
type stuckError struct {
	args  []string
	limit time.Duration
}

func (e *stuckError) Error() string {
	return fmt.Sprintf("wsl.exe didn't answer `wsl %s` in %s: WSL seems stuck. In PowerShell: wsl --shutdown, then start AgentBox again", strings.Join(e.args, " "), e.limit)
}

// execWithin is exec, given up after execTimeout.
func (d *Distro) execWithin(ctx context.Context, root bool, stdin io.Reader, command ...string) (string, error) {
	return d.runWithin(ctx, execTimeout, stdin, d.execArgs(root, "", command...)...)
}

// run runs wsl.exe and returns what it printed. An error carries what it said.
func (d *Distro) run(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	cmd := d.command(ctx, args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := decode(stdout.Bytes())
	if err != nil {
		msg := strings.TrimSpace(decode(stderr.Bytes()))
		if msg == "" {
			msg = strings.TrimSpace(out)
		}
		if msg == "" {
			return out, fmt.Errorf("wsl %s: %w", strings.Join(args, " "), err)
		}
		return out, &wslError{args: args, said: msg}
	}
	return out, nil
}

// wslError is a wsl.exe that failed, and all it said about it.
type wslError struct {
	args []string
	said string
}

// Error is the first line of what wsl.exe said, and its last when that's
// another, which is where WSL puts its error code: "Windows Subsystem for
// Linux has no installed distributions. (Error code: Wsl/…)".
func (e *wslError) Error() string {
	first, last := firstLine(e.said), lastLine(e.said)
	if first == last {
		return fmt.Sprintf("wsl %s: %s", strings.Join(e.args, " "), last)
	}
	return fmt.Sprintf("wsl %s: %s (%s)", strings.Join(e.args, " "), first, last)
}

func firstLine(s string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(strings.ReplaceAll(s, "\r", "")), "\n")
	return strings.TrimSpace(first)
}

// exec runs a command in the distro, as root when root is set.
func (d *Distro) exec(ctx context.Context, root bool, stdin io.Reader, command ...string) (string, error) {
	return d.run(ctx, stdin, d.execArgs(root, "", command...)...)
}

func (d *Distro) execArgs(root bool, dir string, command ...string) []string {
	args := []string{"--distribution", d.Name}
	if root {
		args = append(args, "--user", "root")
	}
	if dir != "" {
		args = append(args, "--cd", dir)
	}
	return append(append(args, "--exec"), command...)
}

// decode reads what wsl.exe printed: UTF-8 with WSL_UTF8 set, but UTF-16LE from
// a WSL too old to know it.
func decode(b []byte) string {
	if len(b) >= 2 && (b[1] == 0 || bytes.HasPrefix(b, []byte{0xff, 0xfe})) {
		b = bytes.TrimPrefix(b, []byte{0xff, 0xfe})
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		return string(utf16.Decode(u))
	}
	return string(b)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r", "")), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Version is WSL's own version, from `wsl --version`, which only the Store
// WSL has. The WSL built into older Windows doesn't, and has no systemd.
func (d *Distro) Version(ctx context.Context) (string, error) {
	out, err := d.runWithin(ctx, listTimeout, nil, "--version")
	if err == nil {
		for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
			// "WSL version: 2.4.13.0"; the label is translated, the number isn't.
			if m := regexp.MustCompile(`^[^:]*WSL[^:]*:\s*([0-9][0-9.]*)`).FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				return m[1], nil
			}
		}
	}
	return "", errors.New("WSL isn't installed, or it's the one built into older Windows, which has no systemd. In PowerShell, as administrator: wsl --install --no-distribution (or wsl --update), then restart Windows if it asks")
}

// State is what `wsl --list --verbose` says of the distro.
type State struct {
	Exists  bool   `json:"exists"`
	State   string `json:"state,omitempty"`   // Running, Stopped, Installing
	Version int    `json:"version,omitempty"` // 1 or 2
}

func (d *Distro) State(ctx context.Context) (State, error) {
	out, err := d.runWithin(ctx, listTimeout, nil, "--list", "--verbose")
	if err != nil {
		// With no distros at all, wsl --list fails and says so, in the
		// language of Windows, above an error code that's the same in every one.
		var w *wslError
		if errors.As(err, &w) && (strings.Contains(strings.ToLower(w.said), "no installed distributions") || strings.Contains(w.said, "WSL_E_DEFAULT_DISTRO_NOT_FOUND")) {
			return State{}, nil
		}
		return State{}, err
	}
	return parseList(out, d.Name), nil
}

// parseList reads `wsl --list --verbose`:
//
//	  NAME        STATE           VERSION
//	* Ubuntu      Running         2
//	  AgentBox    Stopped         2
func parseList(out, name string) State {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if len(fields) < 3 || !strings.EqualFold(fields[0], name) {
			continue
		}
		st := State{Exists: true, State: fields[1]}
		_, _ = fmt.Sscan(fields[len(fields)-1], &st.Version)
		return st
	}
	return State{}
}

// Status is `agentbox wsl status --json`, which the app's first screen reads
// on Windows.
type Status struct {
	WSL     string `json:"wsl"`               // WSL's version; "" when it isn't installed
	Problem string `json:"problem,omitempty"` // why there's no distro to use, and what to do
	Name    string `json:"name"`
	User    string `json:"user"`
	State
}

func (d *Distro) Status(ctx context.Context) Status {
	s := Status{Name: d.Name, User: d.User}
	if d.WSL == "" {
		s.Problem = errNoWSL.Error()
		return s
	}
	v, err := d.Version(ctx)
	if err != nil {
		s.Problem = err.Error()
		return s
	}
	s.WSL = v
	if s.State, err = d.State(ctx); err != nil {
		s.Problem = err.Error()
	} else if !s.Exists {
		s.Problem = ErrNotCreated.Error()
	} else if s.Version == 1 {
		s.Problem = fmt.Sprintf("%s is a WSL 1 distro, and Incus needs WSL 2's kernel: wsl --set-version %s 2", d.Name, d.Name)
	}
	return s
}

// ready fails when the distro can't run commands yet, and says what to do.
func (d *Distro) ready(ctx context.Context) error {
	_, err := d.readyState(ctx)
	return err
}

// readyState is ready, with the distro's state when it is.
func (d *Distro) readyState(ctx context.Context) (State, error) {
	st, err := d.State(ctx)
	if err != nil {
		return st, err
	}
	if !st.Exists {
		return st, ErrNotCreated
	}
	if st.Version == 1 {
		return st, fmt.Errorf("%s is a WSL 1 distro, and Incus needs WSL 2: wsl --set-version %s 2", d.Name, d.Name)
	}
	return st, nil
}

func digest(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// EnsureBinary installs the Linux agentbox shipped beside the front end as the
// distro's /usr/local/bin/agentbox, unless the distro has it already. It
// reports whether it installed it.
func (d *Distro) EnsureBinary(ctx context.Context) (bool, error) {
	f, err := os.Open(d.Binary)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	want, err := digest(f)
	if err != nil {
		return false, err
	}
	// One wsl.exe compares the two and, when they differ, copies Windows'
	// from where the distro sees it (/mnt/c/…): tens of megabytes through
	// wsl.exe's stdin take minutes, the first start after every update
	// (#433). The relay and `daemon start` both get here as the app starts:
	// the lock makes the second find the first's copy instead of making its
	// own.
	src, _ := LinuxPath(d.Binary, d.Name)
	start := time.Now()
	out, err := d.execWithin(ctx, true, nil, "sh", "-c", installScript, "sh", want, distroBinary, src)
	if err != nil {
		return false, fmt.Errorf("installing agentbox in %s: %w", d.Name, err)
	}
	switch strings.TrimSpace(out) {
	case "current":
		return false, nil
	case "installed":
		d.progress("Installed agentbox in %s (%s)", d.Name, since(start))
		return true, nil
	}
	// Through stdin when the distro can't see it: automount may be off, and
	// the front end's folder may be somewhere WSL can't see.
	d.progress("Copying agentbox into %s…", d.Name)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	if err := d.writeFile(ctx, distroBinary, "0755", f); err != nil {
		return false, fmt.Errorf("installing agentbox in %s: %w", d.Name, err)
	}
	d.progress("Installed agentbox in %s (%s)", d.Name, since(start))
	return true, nil
}

// installScript is EnsureBinary's, run as root with the agentbox wanted's
// SHA-256, where it goes and where the distro sees Windows' copy. It says
// "current", "installed", or "stdin" for a copy it couldn't read.
const installScript = `set -e
mkdir -p /run/lock; exec 9>/run/lock/agentbox-install; flock 9
sum() { sha256sum "$1" 2>/dev/null | cut -d' ' -f1; }
if [ "$(sum "$2")" = "$1" ]; then echo current; exit 0; fi
t="$2.new.$$"
if [ -n "$3" ] && cp "$3" "$t" 2>/dev/null && [ "$(sum "$t")" = "$1" ]; then
	chmod 0755 "$t"; mv -f "$t" "$2"; echo installed; exit 0
fi
rm -f "$t"; echo stdin`

// progress says what the front end is doing, on its log (stderr), which the
// app shows while it waits: a first start after an update takes minutes.
func (d *Distro) progress(format string, a ...any) {
	if d.Log != nil {
		_, _ = fmt.Fprintf(d.Log, format+"\n", a...)
	}
}

func since(t time.Time) time.Duration { return time.Since(t).Round(time.Second) }

// writeFile writes what r holds to path in the distro, as root, replacing it
// in one rename so a running agentbox isn't cut short.
func (d *Distro) writeFile(ctx context.Context, path, mode string, r io.Reader) error {
	_, err := d.execWithin(ctx, true, r, "sh", "-c", `set -e; t="$1.new.$$"; cat >"$t"; chmod "$2" "$t"; mv -f "$t" "$1"`, "sh", path, mode)
	return err
}

// Forward runs an agentbox command in the distro, and returns its exit status.
// The distro's agentbox is brought up to date first, and the daemon started
// through a wsl.exe that keeps the distro running while it does.
func (d *Distro) Forward(ctx context.Context, args []string) (int, error) {
	st, err := d.readyState(ctx)
	if err != nil {
		return 1, err
	}
	// The first command in a stopped distro boots it, systemd and all.
	if st.State != "Running" {
		d.progress("Starting %s, AgentBox's WSL distro…", d.Name)
	}
	if _, err := d.EnsureBinary(ctx); err != nil {
		return 1, err
	}
	if needsDaemon(args) {
		if err := d.StartDaemon(ctx); err != nil {
			return 1, err
		}
	}
	// `daemon start` is done: the front end started it, and the distro's own
	// would only be one more wsl.exe to fail in.
	if len(args) == 2 && args[0] == "daemon" && args[1] == "start" {
		_, _ = fmt.Fprintf(os.Stdout, "The AgentBox daemon is running in %s\n", d.Name)
		return 0, nil
	}
	dir, note := d.workdir()
	if note != "" {
		_, _ = fmt.Fprintln(d.Log, note)
	}
	command := append([]string{"agentbox"}, translateArgs(args)...)
	if env := forwardEnv(); len(env) > 0 {
		command = append(append([]string{"env"}, env...), command...)
	}
	if dir != "~" {
		// wsl.exe --cd fails outright on a folder the distro doesn't have, a
		// drive WSL didn't mount, say: start at home, and move from there.
		command = append([]string{"sh", "-c", inDir, "sh", dir}, command...)
	}
	cmd := d.command(ctx, d.execArgs(false, "~", command...)...)
	// The command's own console: a terminal, Ctrl-C and colours.
	showWindow(cmd)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Output that isn't a console's, the app's, is watched as it passes, so
	// that a failure nobody explained can be.
	var said, printed *tail
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		said = &tail{}
		cmd.Stderr = io.MultiWriter(os.Stderr, said)
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			printed = &tail{}
			cmd.Stdout = io.MultiWriter(os.Stdout, printed)
		}
	}
	err = runForeground(cmd)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), silentExit(args, d.Name, exit.ExitCode(), said, printed)
	}
	if err != nil {
		return 1, fmt.Errorf("running agentbox %s in %s: %w", strings.Join(args, " "), d.Name, err)
	}
	return 0, nil
}

// silentExit explains a forwarded command's failure when nothing else will:
// wsl.exe's own failures, whose status is no Linux one (WSL's error codes,
// -1), and whose message, if any, went to stdout; and a command that failed
// without a word on stderr, watched when it isn't a console (said). nil when
// the command said why itself.
func silentExit(args []string, distro string, code int, said, printed *tail) error {
	command := strings.TrimSpace("agentbox " + strings.Join(args, " "))
	if code < 0 || code > 255 {
		return fmt.Errorf("wsl.exe couldn't run %s in %s (error 0x%08x)%s", command, distro, uint32(code), printed.suffix())
	}
	if said != nil && said.suffix() == "" {
		return fmt.Errorf("%s exited with status %d in %s%s", command, code, distro, printed.suffix())
	}
	return nil
}

// hostOnly are the AGENTBOX_ settings that are the front end's own, or name
// Windows paths that mean nothing in the distro.
var hostOnly = map[string]bool{
	"AGENTBOX_FRONT_END": true, "AGENTBOX_WSL": true, "AGENTBOX_WSL_DISTRO": true,
	"AGENTBOX_WSL_DIR": true, "AGENTBOX_WSL_ROOTFS": true, "AGENTBOX_LINUX_BINARY": true,
	"AGENTBOX_BIN": true, "AGENTBOX_SOCKET": true,
}

// forwardEnv is the AGENTBOX_ settings of the front end's environment that
// mean the same in the distro (AGENTBOX_ENV, AGENTBOX_IMAGE_URL…), and
// DO_NOT_TRACK (hostos.Forwarded). wsl.exe
// passes none of Windows's environment through unless WSLENV names it, so
// they go as env's arguments.
func forwardEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if hostos.Forwarded(name) && !hostOnly[name] {
			env = append(env, kv)
		}
	}
	return env
}

// needsDaemon reports whether a command will talk to the daemon, which the
// front end starts itself first, so that the daemon isn't started inside the
// distro by the command, with nothing on Windows keeping the distro running.
func needsDaemon(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "help", "--help", "-h", "--version", "-v", "completion", "host", "wsl-bridge", "whoami", "__complete":
		return false
	case "daemon":
		// `daemon start` does, and it's done by the front end; stop,
		// install and the foreground daemon itself don't.
		return len(args) > 1 && args[1] == "start"
	}
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return false
		}
	}
	return true
}

// workdir is the directory a forwarded command runs in: the current one when
// WSL can see it, and otherwise the Linux user's home, with a note. A path on
// a Windows drive is fine to run in, since WSL mounts it; adding it as a
// project isn't, and the daemon says why.
const inDir = `cd "$1" 2>/dev/null || echo "note: $1 isn't in the distro, so this runs in your Linux home folder" >&2; shift; exec "$@"`

func (d *Distro) workdir() (string, string) {
	cwd, err := os.Getwd()
	if err != nil {
		return "~", ""
	}
	if p, ok := LinuxPath(cwd, d.Name); ok {
		return p, ""
	}
	return "~", fmt.Sprintf("note: %s isn't a folder WSL can see, so this runs in your Linux home folder", cwd)
}

// LinuxPath is where a Windows path is in the distro (hostos.LinuxPath).
func LinuxPath(p, distro string) (string, bool) { return hostos.LinuxPath(p, distro) }

// WindowsPath is where Windows sees a path of the distro:
// \\wsl.localhost\AgentBox\home\ana for /home/ana, and C:\x for /mnt/c/x.
func WindowsPath(p, distro string) string {
	if m := regexp.MustCompile(`^/mnt/([a-z])(/.*)?$`).FindStringSubmatch(p); m != nil {
		return strings.ToUpper(m[1]) + `:` + strings.ReplaceAll(orSlash(m[2]), "/", `\`)
	}
	return `\\wsl.localhost\` + distro + strings.ReplaceAll(p, "/", `\`)
}

func orSlash(s string) string {
	if s == "" {
		return "/"
	}
	return s
}

// translateArgs makes the arguments that are Windows paths, whole, into the
// distro's paths, so `agentbox add C:\src\app` names the folder in Linux
// terms. Anything else is left alone: a path can't be told from text in
// general, and only a whole argument that is a drive or WSL path is taken for
// one.
func translateArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = a
		if hostos.IsWindowsPath(a) {
			if p, ok := LinuxPath(a, env("AGENTBOX_WSL_DISTRO", DefaultName)); ok {
				out[i] = p
			}
		}
	}
	return out
}

// daemonLog is where the daemon the front end starts writes, in the distro:
// the same file `agentbox daemon start` logs to on Linux.
const daemonLog = `${XDG_DATA_HOME:-$HOME/.local/share}/agentbox/daemon.log`

// StartDaemon starts the daemon unless it answers. It runs it under a wsl.exe
// of its own, detached from this process and without a window: WSL stops a
// distro once nothing on Windows is attached to it, even with systemd
// services running, and the agents would stop with it. That wsl.exe lives
// exactly as long as the daemon does.
func (d *Distro) StartDaemon(ctx context.Context) error {
	if d.daemonAnswers(ctx, 0) {
		return nil
	}
	d.progress("Starting the AgentBox daemon in %s…", d.Name)
	start := time.Now()
	cmd := d.command(context.Background(), d.execArgs(false, "~", append([]string{"sh", "-c",
		`mkdir -p "$(dirname "` + daemonLog + `")" && exec env "$@" agentbox daemon >>"` + daemonLog + `" 2>&1`, "sh"}, d.daemonEnv()...)...)...)
	cmd, err := startDetached(cmd)
	if err != nil {
		return fmt.Errorf("starting the daemon in %s: %w", d.Name, err)
	}
	// Reap it if this process outlives it; nothing waits on it otherwise.
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	// A daemon migrates its database before it answers, which the first
	// start after an update can take minutes to: it's waited for as long as
	// it runs, up to daemonStartWait, and a daemon that exits is reported at
	// once, with the last line it logged.
	said := false
	for deadline := time.Now().Add(daemonStartWait); ; {
		if d.daemonAnswers(ctx, 5*time.Second) {
			break
		}
		select {
		case <-exited:
			if d.daemonAnswers(ctx, 0) { // another daemon won the socket
				return nil
			}
			last, _ := d.exec(ctx, false, nil, "sh", "-c", `tail -n 1 "`+daemonLog+`"`)
			if last = strings.TrimSpace(last); last != "" {
				last = ": " + last
			}
			return fmt.Errorf("the AgentBox daemon exited as it started in %s%s (log: ~/.local/share/agentbox/daemon.log there, agentbox wsl shell)", d.Name, last)
		default:
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the AgentBox daemon didn't answer in %s after %s: see ~/.local/share/agentbox/daemon.log there (agentbox wsl shell)", d.Name, daemonStartWait)
		}
		if !said && time.Since(start) > 15*time.Second {
			d.progress("Waiting for the AgentBox daemon: its first start after an update can take a few minutes…")
			said = true
		}
	}
	d.progress("Started the AgentBox daemon in %s (%s)", d.Name, since(start))
	return nil
}

// daemonStartWait is how long a starting daemon that hasn't exited is waited
// for. A variable for tests.
var daemonStartWait = 10 * time.Minute

// daemonEnv is what the daemon is told about Windows: where the install ID
// is kept (hostos.InstallIDFileEnv), beside the distro's directory rather than
// in it, so a new distro is still the same install; and the Windows user's
// home (hostos.WindowsHomeEnv), where their own AI tools keep their skills.
func (d *Distro) daemonEnv() []string {
	var env []string
	if d.Dir != "" {
		if file, ok := LinuxPath(filepath.Join(filepath.Dir(d.Dir), "install-id"), d.Name); ok {
			env = append(env, hostos.InstallIDFileEnv+"="+file)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if p, ok := LinuxPath(home, d.Name); ok {
			env = append(env, hostos.WindowsHomeEnv+"="+p)
		}
	}
	return env
}

// daemonAnswers asks the distro's agentbox whether the daemon answers, waiting
// up to wait for it to.
func (d *Distro) daemonAnswers(ctx context.Context, wait time.Duration) bool {
	_, err := d.runWithin(ctx, wait+execTimeout, nil, d.execArgs(false, "", "agentbox", "wsl-bridge", "--wait", wait.String())...)
	return err == nil
}

// Shutdown stops the distro. The daemon, and with it every agent, stops too.
func (d *Distro) Shutdown(ctx context.Context) error {
	_, err := d.run(ctx, nil, "--terminate", d.Name)
	return err
}
