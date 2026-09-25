package hostwsl

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed configure.sh
var configureScript string

// RootfsURL is the Ubuntu 24.04 image for WSL that `agentbox wsl init`
// imports: Canonical's own, the one the Store's Ubuntu 24.04 installs. Its
// SHA256SUMS is beside it. AGENTBOX_WSL_ROOTFS names another, a URL or a file,
// which is then used without a checksum.
const RootfsURL = "https://cloud-images.ubuntu.com/wsl/releases/24.04/current/ubuntu-noble-wsl-amd64-wsl.rootfs.tar.gz"

// Init makes the distro when there isn't one, and sets AgentBox up in it:
// the user, systemd, the Linux agentbox, `agentbox host setup` (Incus, its
// storage and network), the user's git identity and the daemon. Safe to run
// again, which is how a half-done setup is finished.
func (d *Distro) Init(ctx context.Context) error {
	if d.WSL == "" {
		return errNoWSL
	}
	v, err := d.Version(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Log, "==> WSL %s\n", v)
	st, err := d.State(ctx)
	if err != nil {
		return err
	}
	if !st.Exists {
		if err := d.Import(ctx); err != nil {
			return err
		}
	} else if st.Version == 1 {
		return d.ready(ctx)
	}

	fmt.Fprintf(d.Log, "==> Making %s's user, %s, and turning systemd on\n", d.Name, d.User)
	if _, err := d.exec(ctx, true, strings.NewReader(configureScript), "sh", "-s", d.User); err != nil {
		return fmt.Errorf("configuring %s: %w", d.Name, err)
	}
	// wsl.conf is read when the distro boots: restart it unless it booted
	// with this wsl.conf already, with systemd and with the user as its
	// default. Ubuntu's image has systemd on, so systemd alone doesn't say so,
	// and a distro still defaulting to root would start the daemon as root.
	init, _ := d.exec(ctx, true, nil, "cat", "/proc/1/comm")
	who, _ := d.exec(ctx, false, nil, "id", "-un")
	if strings.TrimSpace(init) != "systemd" || strings.TrimSpace(who) != d.User {
		fmt.Fprintf(d.Log, "==> Restarting %s with systemd, and %s as its user\n", d.Name, d.User)
		if err := d.Shutdown(ctx); err != nil {
			return err
		}
	}
	if err := d.waitSystemd(ctx); err != nil {
		return err
	}

	if _, err := d.EnsureBinary(ctx); err != nil {
		return err
	}
	fmt.Fprintln(d.Log, "==> Host setup in the distro: Incus, its storage and network, and the user mapping")
	if err := d.execLog(ctx, true, "agentbox", "host", "setup", "--user", d.User); err != nil {
		return fmt.Errorf("host setup in %s: %w", d.Name, err)
	}
	d.copyGitIdentity(ctx)
	fmt.Fprintln(d.Log, "==> Starting the daemon")
	return d.StartDaemon(ctx)
}

// waitSystemd waits for systemd to finish booting. "degraded" (a unit
// failed) is booted too: WSL's images always have a unit or two that can't
// start in WSL.
func (d *Distro) waitSystemd(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, _ := d.exec(ctx, true, nil, "systemctl", "is-system-running", "--wait")
	switch state := strings.TrimSpace(out); state {
	case "running", "degraded":
		return nil
	default:
		if ctx.Err() != nil {
			return fmt.Errorf("systemd didn't finish starting in %s within 3 minutes", d.Name)
		}
		return fmt.Errorf("systemd isn't running in %s (%q): check /etc/wsl.conf has [boot] systemd=true, and that WSL is 0.67.6 or later", d.Name, state)
	}
}

// Import downloads Ubuntu's WSL image, checks it, and makes the distro from
// it, with its disk in d.Dir.
func (d *Distro) Import(ctx context.Context) error {
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return err
	}
	tarball, err := d.rootfs(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Log, "==> Making the %s distro in %s\n", d.Name, d.Dir)
	if _, err := d.run(ctx, nil, "--import", d.Name, d.Dir, tarball, "--version", "2"); err != nil {
		return err
	}
	if tarball == filepath.Join(d.Dir, "rootfs.tar.gz") {
		os.Remove(tarball)
	}
	return nil
}

// rootfs is the image to import, downloaded and checked into d.Dir.
func (d *Distro) rootfs(ctx context.Context) (string, error) {
	src, check := os.Getenv("AGENTBOX_WSL_ROOTFS"), true
	if src == "" {
		src = RootfsURL
	} else {
		check = false
		if !strings.Contains(src, "://") {
			return src, nil
		}
	}
	var want string
	if check {
		sums, err := fetch(ctx, src[:strings.LastIndex(src, "/")+1]+"SHA256SUMS")
		if err != nil {
			return "", fmt.Errorf("fetching the Ubuntu image's checksums: %w", err)
		}
		want = findSum(string(sums), src[strings.LastIndex(src, "/")+1:])
		if want == "" {
			return "", fmt.Errorf("no checksum for %s in its SHA256SUMS", src)
		}
	}
	dst := filepath.Join(d.Dir, "rootfs.tar.gz")
	fmt.Fprintf(d.Log, "==> Downloading %s\n", src)
	if err := download(ctx, src, dst); err != nil {
		return "", err
	}
	if check {
		f, err := os.Open(dst)
		if err != nil {
			return "", err
		}
		got, err := digest(f)
		f.Close()
		if err != nil {
			return "", err
		}
		if got != want {
			os.Remove(dst)
			return "", fmt.Errorf("the Ubuntu image's checksum is %s, not the %s its SHA256SUMS says", got, want)
		}
	}
	return dst, nil
}

func findSum(sums, name string) string {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return f[0]
		}
	}
	return ""
}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func download(ctx context.Context, url, dst string) error {
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(dst + ".part")
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst + ".part")
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	return os.Rename(dst+".part", dst)
}

// execLog runs a command in the distro with its output going to d.Log, for
// the long steps whose progress is worth seeing.
func (d *Distro) execLog(ctx context.Context, root bool, command ...string) error {
	cmd := d.command(ctx, d.execArgs(root, "", command...)...)
	cmd.Stdout, cmd.Stderr = d.Log, d.Log
	return cmd.Run()
}

// copyGitIdentity gives the distro's user the Windows user's git name and
// email, which the daemon commits snapshots with. Best effort: Windows may
// have no git, and the user can set them in the distro.
func (d *Distro) copyGitIdentity(ctx context.Context) {
	git, err := exec.LookPath("git")
	if err != nil {
		return
	}
	for _, key := range []string{"user.name", "user.email"} {
		cmd := exec.CommandContext(ctx, git, "config", "--global", key)
		hideWindow(cmd)
		out, err := cmd.Output()
		value := strings.TrimSpace(string(out))
		if err != nil || value == "" {
			continue
		}
		if have, _ := d.exec(ctx, false, nil, "git", "config", "--global", key); strings.TrimSpace(have) != "" {
			continue
		}
		d.exec(ctx, false, nil, "git", "config", "--global", key, value)
	}
}

// Upgrade brings the distro's agentbox up to date and restarts the daemon on
// it, which is what a new version of the app needs.
func (d *Distro) Upgrade(ctx context.Context) error {
	if err := d.ready(ctx); err != nil {
		return err
	}
	installed, err := d.EnsureBinary(ctx)
	if err != nil {
		return err
	}
	if !installed {
		fmt.Fprintf(d.Log, "%s's agentbox is up to date\n", d.Name)
	} else if d.daemonAnswers(ctx, 0) {
		fmt.Fprintln(d.Log, "==> Restarting the daemon on the new agentbox")
		if _, err := d.exec(ctx, false, nil, "agentbox", "daemon", "stop"); err != nil {
			return err
		}
	}
	return d.StartDaemon(ctx)
}

// Unregister deletes the distro and its disk: every agent, project and
// setting AgentBox has on this computer.
func (d *Distro) Unregister(ctx context.Context) error {
	st, err := d.State(ctx)
	if err != nil {
		return err
	}
	if !st.Exists {
		return errors.New(d.Name + " doesn't exist")
	}
	_, err = d.run(ctx, nil, "--unregister", d.Name)
	return err
}

// Shell opens a login shell in the distro, in this console.
func (d *Distro) Shell(ctx context.Context) (int, error) {
	if err := d.ready(ctx); err != nil {
		return 1, err
	}
	cmd := d.command(ctx, "--distribution", d.Name, "--cd", "~")
	showWindow(cmd)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := runForeground(cmd)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return 0, err
}
