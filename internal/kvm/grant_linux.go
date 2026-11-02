package kvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// What Grant writes outside /dev/kvm, by name so a reader of /etc can tell
// whose they are.
const (
	modulesFile = "/etc/modules-load.d/agentbox-kvm.conf"
	// Before 73-seat-late.rules, which acts on the uaccess tag.
	udevRule = "/etc/udev/rules.d/70-agentbox-kvm.rules"
)

// Grant gives u read and write on /dev/kvm, now and after the next boot, as
// root (see the package's comment), saying what it does on log. It fails when
// the CPU can't virtualize, which only the firmware's settings change.
func Grant(ctx context.Context, u *user.User, log io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("giving " + u.Username + " /dev/kvm has to run as root: " + Command)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("%s's UID %q: %w", u.Username, u.Uid, err)
	}
	sys := System()
	if err := sys.Stat(); errors.Is(err, os.ErrNotExist) {
		module := Module(sys)
		if module == "" {
			return Error(NoVirt, nil)
		}
		_, _ = fmt.Fprintf(log, "==> Loading %s, now and at every boot (%s)\n", module, modulesFile)
		if out, err := exec.CommandContext(ctx, sbin("modprobe"), module).CombinedOutput(); err != nil {
			return fmt.Errorf("modprobe %s: %w: %s", module, err, out)
		}
		if err := os.WriteFile(modulesFile, []byte("# AgentBox's VM needs /dev/kvm (agentbox host kvm).\n"+module+"\n"), 0o644); err != nil {
			return err
		}
		// udev makes the node after the module is in.
		_ = exec.CommandContext(ctx, sbin("udevadm"), "settle", "--timeout=10").Run()
		if err := sys.Stat(); err != nil {
			return fmt.Errorf("%s is loaded, and still there's no /dev/kvm: %w", module, err)
		}
	}

	_, _ = fmt.Fprintf(log, "==> Giving %s /dev/kvm now, with no new login (an ACL)\n", u.Username)
	if err := addACL(Device, uint32(uid)); err != nil {
		return fmt.Errorf("the ACL on /dev/kvm: %w", err)
	}

	// The ACL goes with the device node at the next boot: the group, or the
	// seat, gives it from then on.
	if _, err := user.LookupGroup("kvm"); err == nil {
		_, _ = fmt.Fprintf(log, "==> Adding %s to the kvm group, for later logins\n", u.Username)
		if out, err := exec.CommandContext(ctx, sbin("usermod"), "-aG", "kvm", u.Username).CombinedOutput(); err != nil {
			_, _ = fmt.Fprintf(log, "warning: usermod -aG kvm %s: %v: %s", u.Username, err, out)
		}
	} else {
		_, _ = fmt.Fprintf(log, "==> No kvm group here: whoever is at this computer's screen gets /dev/kvm on every boot (%s)\n", udevRule)
		rule := "# AgentBox's VM needs /dev/kvm (agentbox host kvm): logind gives it to the seat's user.\n" +
			`KERNEL=="kvm", TAG+="uaccess"` + "\n"
		if err := os.WriteFile(udevRule, []byte(rule), 0o644); err != nil {
			return err
		}
		_ = exec.CommandContext(ctx, sbin("udevadm"), "control", "--reload").Run()
	}

	if err := openAs(ctx, u); err != nil {
		return fmt.Errorf("%s still can't open /dev/kvm: %w", u.Username, err)
	}
	_, _ = fmt.Fprintf(log, "%s can use /dev/kvm.\n", u.Username)
	return nil
}

// sbin finds a system tool on PATH, or in the sbin directories a sudo or a
// pkexec with a short PATH may leave out.
func sbin(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/bin"} {
		if p := filepath.Join(dir, name); fileExists(p) {
			return p
		}
	}
	return name
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// addACL gives uid read and write on path in its access ACL, keeping what's
// there.
func addACL(path string, uid uint32) error {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return err
	}
	cur := make([]byte, 1024)
	n, err := unix.Getxattr(path, "system.posix_acl_access", cur)
	switch {
	case errors.Is(err, unix.ENODATA):
		n = 0
	case err != nil:
		return err
	}
	acl, err := withUser(cur[:n], st.Mode, uid)
	if err != nil {
		return err
	}
	return unix.Setxattr(path, "system.posix_acl_access", acl, 0)
}

// openAs opens /dev/kvm as u would, with the groups u is in now: a shell run
// with u's credentials.
func openAs(ctx context.Context, u *user.User) error {
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return err
	}
	var groups []uint32
	if ids, err := u.GroupIds(); err == nil {
		for _, id := range ids {
			if g, err := strconv.ParseUint(id, 10, 32); err == nil {
				groups = append(groups, uint32(g))
			}
		}
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "exec 3<>"+Device)
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: groups}}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
