package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/hostos"
)

// AgentBox's data on the VM's pool disk. A Cloud Hypervisor VM has two disks:
// its system disk (root.raw, 20 GiB, which nothing grows) and the agents' disk
// (pool.raw, Incus's btrfs pool, which `agentbox vm resize --disk` and
// Settings grow). What AgentBox keeps that grows, its caches, the leads' AI
// tools and the projects' lead homes, goes on the agents' disk: a btrfs
// subvolume of the pool, agentbox-data, mounted at poolDataMount by the VM's
// fstab, with each directory in the data directory a symlink to its place in
// it. state.db, the log and run/ stay where they are, on the system disk.
//
// A symlink rather than paths of their own, so every path AgentBox has
// written down (a lead's Claude Code session, a chat image's) stays good,
// and the data directory can still be moved as a whole.

const (
	// poolDevice is the agents' disk in a Cloud Hypervisor VM, by the serial
	// the VM gives it (chv's PoolDevice). Lima, WSL and host installs have
	// none, and keep everything where it was.
	poolDevice = "/dev/disk/by-id/virtio-agentbox-pool"
	// poolDataMount is where the agentbox-data subvolume is mounted.
	poolDataMount = "/var/lib/agentbox"
	// poolDataSubvolume is the subvolume's name at the top of the pool's
	// file system, beside Incus's own directories, which Incus leaves alone.
	poolDataSubvolume = "agentbox-data"
)

// poolItem is one directory of the data directory that goes on the agents'
// disk. A cache is started afresh there rather than copied: copying a cache
// of many GiB of small files would hold the daemon's start up for minutes,
// for what is downloaded again on demand.
type poolItem struct {
	name string
	copy bool
}

var poolItems = []poolItem{
	{"package-cache", false},
	{"image-cache", false},
	{"tools", true},
	{"projects", true},
	{"machines", true},
}

// poolData moves poolItems from data to mount.
type poolData struct {
	data, mount string
	// copyDir copies the directory src to dst, which doesn't exist yet.
	copyDir func(ctx context.Context, src, dst string) error
	logf    func(format string, args ...any)
}

// move puts every item on the agents' disk, and returns what's left of the
// old copies, for removing in the background: an item that can't move stays
// where it is, and is tried again at the next start.
func (p poolData) move(ctx context.Context) (aside []string, err error) {
	var errs []error
	for _, it := range poolItems {
		old, err := p.moveOne(ctx, it)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", it.name, err))
			continue
		}
		if old != "" {
			aside = append(aside, old)
		}
	}
	return aside, errors.Join(errs...)
}

// moveOne moves one item. Each step leaves something the next start can carry
// on from: the new copy is made whole under another name first, and the old
// directory is renamed aside before the symlink takes its place.
func (p poolData) moveOne(ctx context.Context, it poolItem) (aside string, err error) {
	src := filepath.Join(p.data, it.name)
	dst := filepath.Join(p.mount, it.name)
	aside = filepath.Join(p.data, "."+it.name+".moved")
	info, err := os.Lstat(src)
	switch {
	case err == nil && info.Mode()&fs.ModeSymlink != 0:
		// Moved at an earlier start; one that stopped short left the old copy.
		if _, err := os.Lstat(aside); err == nil {
			return aside, nil
		}
		return "", nil
	case errors.Is(err, fs.ErrNotExist):
		// Never made, or renamed aside by a start that stopped before the
		// symlink: dst is whole by then.
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return "", err
		}
		if err := os.Symlink(dst, src); err != nil {
			return "", err
		}
		if _, err := os.Lstat(aside); err == nil {
			return aside, nil
		}
		return "", nil
	case err != nil:
		return "", err
	case !info.IsDir():
		return "", fmt.Errorf("%s isn't a directory", src)
	}
	if it.copy {
		tmp := dst + ".moving"
		if err := os.RemoveAll(tmp); err != nil {
			return "", err
		}
		p.logf("moving %s to the agents' disk (%s)", src, dst)
		start := time.Now()
		if err := p.copyDir(ctx, src, tmp); err != nil {
			_ = os.RemoveAll(tmp)
			return "", err
		}
		if err := os.RemoveAll(dst); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return "", err
		}
		p.logf("moved %s in %s", src, time.Since(start).Round(time.Second))
	} else {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return "", err
		}
		p.logf("%s starts afresh on the agents' disk (%s); the old one is removed", src, dst)
	}
	if err := os.RemoveAll(aside); err != nil {
		return "", err
	}
	if err := os.Rename(src, aside); err != nil {
		return "", err
	}
	if err := os.Symlink(dst, src); err != nil {
		if back := os.Rename(aside, src); back != nil {
			return "", fmt.Errorf("%w (and putting it back: %v)", err, back)
		}
		return "", err
	}
	return aside, nil
}

// cpDir copies with cp -a, which keeps what Go's copy wouldn't: symlinks,
// modes and times, as the leads' tools' node_modules need.
func cpDir(ctx context.Context, src, dst string) error {
	out, err := exec.CommandContext(ctx, "cp", "-a", "--", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copying: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// mountPoolDataScript makes the agentbox-data subvolume and mounts it, as
// root, idempotently: run at every start that finds it unmounted, it only
// mounts it, from the fstab line the first one wrote. It never formats: a
// pool disk that isn't btrfs yet (Incus makes it on the VM's first boot)
// exits 3, and is left for the next start. $1 and $2 are the owner's uid
// and gid.
const mountPoolDataScript = `set -eu
dev=` + poolDevice + `
mnt=` + poolDataMount + `
[ -b "$dev" ] || exit 3
[ "$(blkid -s TYPE -o value "$dev")" = btrfs ] || exit 3
if ! mountpoint -q "$mnt"; then
  if ! grep -qs "^[^#]*[[:space:]]$mnt[[:space:]]" /etc/fstab; then
    top=$(mktemp -d)
    mount -o subvolid=5 "$dev" "$top"
    [ -d "$top/` + poolDataSubvolume + `" ] || btrfs -q subvolume create "$top/` + poolDataSubvolume + `"
    umount "$top"
    rmdir "$top"
    echo "UUID=$(blkid -s UUID -o value "$dev") $mnt btrfs subvol=` + poolDataSubvolume + `,user_subvol_rm_allowed,nofail 0 0" >>/etc/fstab
    systemctl daemon-reload || true
  fi
  mkdir -p "$mnt"
  mount "$mnt"
fi
chown "$1:$2" "$mnt"
chmod 0700 "$mnt"
`

// startingFile is written while the daemon moves data before it listens, so
// `agentbox daemon start` waits for it past its usual ten seconds
// (StartingSince).
func startingFile(data string) string { return filepath.Join(data, "run", "starting") }

// StartingSince is what a daemon still busy starting says it's doing, and
// when it last said so; "" when none is.
func StartingSince(data string) (what string, at time.Time) {
	f := startingFile(data)
	info, err := os.Stat(f)
	if err != nil {
		return "", time.Time{}
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return "", time.Time{}
	}
	return strings.TrimSpace(string(b)), info.ModTime()
}

// moveDataToPool puts AgentBox's data on the agents' disk in a Cloud
// Hypervisor VM, before the daemon listens, while nothing uses it. It returns
// the old copies, for dropMovedData.
func (s *Server) moveDataToPool(ctx context.Context) (aside []string) {
	if !hostos.InVM() {
		return nil
	}
	if _, err := os.Stat(poolDevice); err != nil {
		return nil
	}
	if !isMountpoint(poolDataMount) {
		uid, gid := s.cfg.User.UID, s.cfg.User.GID
		if uid == 0 {
			uid, gid = os.Getuid(), os.Getgid()
		}
		mctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		out, err := exec.CommandContext(mctx, "sudo", "-n", "sh", "-c", mountPoolDataScript, "sh", strconv.Itoa(uid), strconv.Itoa(gid)).CombinedOutput()
		cancel()
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 3 {
			return nil // the pool isn't made yet
		}
		if err != nil {
			s.logf("AgentBox's data stays on the VM's system disk: mounting the agents' disk: %v: %s", err, strings.TrimSpace(string(out)))
			return nil
		}
	}
	marker := startingFile(s.cfg.Paths.Data)
	say := func() { _ = os.WriteFile(marker, []byte("Moving AgentBox's data to the agents' disk"), 0o600) }
	say()
	defer func() { _ = os.Remove(marker) }()
	// Kept fresh while it copies, so a start waiting on it can tell a long
	// copy from a daemon that died.
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-time.After(2 * time.Second):
				say()
			}
		}
	}()
	p := poolData{data: s.cfg.Paths.Data, mount: poolDataMount, copyDir: cpDir, logf: s.logf}
	aside, err := p.move(ctx)
	if err != nil {
		s.logf("moving AgentBox's data to the agents' disk: %v", err)
	}
	return aside
}

// dropMovedData removes the old copies moveDataToPool left. Running agents
// have the old package cache mounted: they're moved onto the new one first,
// so none of them loses it under a running install.
func (s *Server) dropMovedData(ctx context.Context, aside []string) {
	if len(aside) == 0 {
		return
	}
	s.applyPackageCache(ctx)
	for _, dir := range aside {
		if err := os.RemoveAll(dir); err != nil {
			s.logf("removing %s: %v", dir, err)
		}
	}
}
