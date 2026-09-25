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
	return cmd
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
		return out, fmt.Errorf("wsl %s: %s", strings.Join(args, " "), lastLine(msg))
	}
	return out, nil
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
	out, err := d.run(ctx, nil, "--version")
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
	out, err := d.run(ctx, nil, "--list", "--verbose")
	if err != nil {
		// With no distros at all, wsl --list fails and says so.
		if strings.Contains(strings.ToLower(err.Error()), "no installed distributions") {
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
	st, err := d.State(ctx)
	if err != nil {
		return err
	}
	if !st.Exists {
		return ErrNotCreated
	}
	if st.Version == 1 {
		return fmt.Errorf("%s is a WSL 1 distro, and Incus needs WSL 2: wsl --set-version %s 2", d.Name, d.Name)
	}
	return nil
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
	out, _ := d.exec(ctx, true, nil, "sha256sum", distroBinary)
	if strings.HasPrefix(strings.TrimSpace(out), want) {
		return false, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	// Through stdin rather than /mnt/c: automount may be off, and the
	// front end's folder may be somewhere WSL can't see.
	if err := d.writeFile(ctx, distroBinary, "0755", f); err != nil {
		return false, fmt.Errorf("installing agentbox in %s: %w", d.Name, err)
	}
	return true, nil
}

// writeFile writes what r holds to path in the distro, as root, replacing it
// in one rename so a running agentbox isn't cut short.
func (d *Distro) writeFile(ctx context.Context, path, mode string, r io.Reader) error {
	_, err := d.exec(ctx, true, r, "sh", "-c", `set -e; t="$1.new.$$"; cat >"$t"; chmod "$2" "$t"; mv -f "$t" "$1"`, "sh", path, mode)
	return err
}

// Forward runs an agentbox command in the distro, and returns its exit status.
// The distro's agentbox is brought up to date first, and the daemon started
// through a wsl.exe that keeps the distro running while it does.
func (d *Distro) Forward(ctx context.Context, args []string) (int, error) {
	if err := d.ready(ctx); err != nil {
		return 1, err
	}
	if installed, err := d.EnsureBinary(ctx); err != nil {
		return 1, err
	} else if installed {
		_, _ = fmt.Fprintf(d.Log, "Installed agentbox %s in %s\n", d.Binary, d.Name)
	}
	if needsDaemon(args) {
		if err := d.StartDaemon(ctx); err != nil {
			return 1, err
		}
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
	err := runForeground(cmd)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
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

var (
	drivePath = regexp.MustCompile(`^([A-Za-z]):(?:[\\/](.*))?$`)
	// \\wsl.localhost\<distro>\... and the older \\wsl$\<distro>\...
	wslPath = regexp.MustCompile(`(?i)^[\\/]{2}(?:wsl\.localhost|wsl\$)[\\/]([^\\/]+)(?:[\\/](.*))?$`)
)

// LinuxPath is where a Windows path is in the distro: C:\Users\ana is
// /mnt/c/Users/ana, and \\wsl.localhost\AgentBox\home\ana is /home/ana. A Linux
// path is itself. ok is false for a path WSL can't see: another distro's
// files, or a network share.
func LinuxPath(p, distro string) (string, bool) {
	if strings.HasPrefix(p, "/") {
		return p, true
	}
	if m := drivePath.FindStringSubmatch(p); m != nil {
		rest := strings.TrimRight(strings.ReplaceAll(m[2], `\`, "/"), "/")
		out := "/mnt/" + strings.ToLower(m[1])
		if rest != "" {
			out += "/" + rest
		}
		return out, true
	}
	if m := wslPath.FindStringSubmatch(p); m != nil {
		if !strings.EqualFold(m[1], distro) {
			return "", false
		}
		return "/" + strings.TrimRight(strings.ReplaceAll(m[2], `\`, "/"), "/"), true
	}
	return "", false
}

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
		if drivePath.MatchString(a) || wslPath.MatchString(a) {
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
	cmd := d.command(context.Background(), d.execArgs(false, "~", "sh", "-c",
		`mkdir -p "$(dirname "`+daemonLog+`")" && exec agentbox daemon >>"`+daemonLog+`" 2>&1`)...)
	cmd, err := startDetached(cmd)
	if err != nil {
		return fmt.Errorf("starting the daemon in %s: %w", d.Name, err)
	}
	// Reap it if this process outlives it; nothing waits on it otherwise.
	go func() { _ = cmd.Wait() }()
	if !d.daemonAnswers(ctx, 30*time.Second) {
		return fmt.Errorf("the AgentBox daemon didn't start in %s: see %s there (agentbox wsl shell)", d.Name, "~/.local/share/agentbox/daemon.log")
	}
	_, _ = fmt.Fprintf(d.Log, "Started the AgentBox daemon in %s\n", d.Name)
	return nil
}

// daemonAnswers asks the distro's agentbox whether the daemon answers, waiting
// up to wait for it to.
func (d *Distro) daemonAnswers(ctx context.Context, wait time.Duration) bool {
	_, err := d.exec(ctx, false, nil, "agentbox", "wsl-bridge", "--wait", wait.String())
	return err == nil
}

// Shutdown stops the distro. The daemon, and with it every agent, stops too.
func (d *Distro) Shutdown(ctx context.Context) error {
	_, err := d.run(ctx, nil, "--terminate", d.Name)
	return err
}
