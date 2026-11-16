package chv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
)

// virtiofsd holds a file descriptor for every inode of the shared home the
// VM's kernel has cached (--inode-file-handles=never, see virtiofsdArgs), and
// once it has used all its RLIMIT_NOFILE allows it answers the VM's lookups
// with ENFILE, "Too many open files in system": a git, npm or editor in any
// agent then fails. The VM's kernel keeps what it looked up until memory runs
// short, which a VM with room never does, and one walk of a few worktrees'
// node_modules (removing an agent, docker prune, git worktree add) looks up
// hundreds of thousands in under a minute. The VM's own timer
// (trim-share-inodes) drops its caches past a fixed count; shareLoop watches
// virtiofsd's actual count against its actual limit, every shareTick, and
// has the VM drop them as soon as the count is high or climbing fast enough
// to get there.
const (
	shareTick = 2 * time.Second
	// shareTrimAt is the share of the limit past which the VM drops its
	// inode cache.
	shareTrimAt = 0.4
	// shareFullAt and shareHorizon: the VM drops it, too, when the count
	// grows fast enough to pass shareFullAt within shareHorizon.
	shareFullAt  = 0.75
	shareHorizon = 15 * time.Second
	// shareTrimGap is the least time between two drops: a drop with a few
	// hundred thousand inodes takes a second or two, and costs the VM its
	// cache of its own disk too.
	shareTrimGap = 4 * time.Second
	// shareSlack is how far past what stayed open after the last drop the
	// count must grow before another drop is worth it: what stays is pinned,
	// by open files, working directories and inotify watches.
	shareSlack = 0.05
)

// trimShareScript drops the VM's dentry and inode caches, which makes its
// kernel forget the share's inodes it doesn't use, and virtiofsd close their
// descriptors (FUSE's forget).
const trimShareScript = "echo 2 | sudo -n tee /proc/sys/vm/drop_caches >/dev/null"

// shareTrimmer decides when the VM drops its inode cache, from virtiofsd's
// count of open files sampled every shareTick.
type shareTrimmer struct {
	prev     int64
	prevAt   time.Time
	lastTrim time.Time
	trims    int
	settling bool  // the sample after a drop is what it left open
	floor    int64 // what stayed open after the last drop
}

// sample takes in open, of limit, at now, and says whether to drop the cache
// now and why.
func (t *shareTrimmer) sample(open, limit int64, now time.Time) (bool, string) {
	defer func() { t.prev, t.prevAt = open, now }()
	if t.settling {
		t.floor, t.settling = open, false
	}
	t.floor = min(t.floor, open)
	if limit <= 0 || now.Sub(t.lastTrim) < shareTrimGap {
		return false, ""
	}
	above := float64(open - t.floor)
	if above < shareSlack*float64(limit) {
		return false, ""
	}
	if float64(open) > shareTrimAt*float64(limit) {
		return true, fmt.Sprintf("past %d%% of the limit", int(shareTrimAt*100))
	}
	if !t.prevAt.IsZero() && open > t.prev {
		perSec := float64(open-t.prev) / now.Sub(t.prevAt).Seconds()
		if float64(open)+perSec*shareHorizon.Seconds() > shareFullAt*float64(limit) {
			return true, fmt.Sprintf("growing by %d a second", int64(perSec))
		}
	}
	return false, ""
}

// trimmed records a drop made at now.
func (t *shareTrimmer) trimmed(now time.Time) {
	t.lastTrim, t.settling = now, true
	t.trims++
}

func (t *shareTrimmer) report(open, limit int64) api.ShareReport {
	r := api.ShareReport{Open: open, Limit: limit, Trims: t.trims, Pinned: t.floor}
	if !t.lastTrim.IsZero() {
		at := t.lastTrim
		r.LastTrim = &at
	}
	return r
}

// shareLoop keeps virtiofsd's open files in bounds while the VM runs, and
// tells the daemon how many there are (PUT /v1/vm/share), for Diagnostics and
// to hold its git work back when they near the limit.
func (s *supervisor) shareLoop(ctx context.Context, m *chMachine) {
	tick := time.NewTicker(shareTick)
	defer tick.Stop()
	client := s.daemonClient(shareTick)
	var t shareTrimmer
	pid := 0
	var limit int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		fs := m.fs
		if fs == nil || fs.exited() {
			continue
		}
		if fs.pid() != pid {
			// A new virtiofsd, with the VM started again.
			pid, limit, t = fs.pid(), openFileLimit(fs.pid()), shareTrimmer{}
		}
		open, err := countFDs(pid)
		if err != nil {
			continue
		}
		now := time.Now()
		if trim, why := t.sample(open, limit, now); trim && s.getState() != api.VMPaused {
			if _, err := s.sshFor(ctx, trimShareScript, 30*time.Second); err != nil {
				s.logf("shared home: virtiofsd has %d of %d files open (%s), and dropping the VM's inode cache failed: %v", open, limit, why, err)
			} else {
				t.trimmed(now)
				s.logf("shared home: virtiofsd had %d of %d files open (%s): dropped the VM's inode cache in %s", open, limit, why, time.Since(now).Round(time.Millisecond))
			}
		}
		s.mu.Lock()
		ready := s.daemonReady
		s.mu.Unlock()
		if ready {
			_ = putDaemon(ctx, client, "/v1/vm/share", t.report(open, limit))
		}
	}
}

// putDaemon PUTs body to the daemon in the VM as JSON.
func putDaemon(ctx context.Context, client *http.Client, path string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://agentbox"+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("PUT %s: %s", path, resp.Status)
	}
	return nil
}

// countFDs is how many file descriptors pid has open.
func countFDs(pid int) (int64, error) {
	d, err := os.Open("/proc/" + strconv.Itoa(pid) + "/fd")
	if err != nil {
		return 0, err
	}
	defer func() { _ = d.Close() }()
	var n int64
	for {
		names, err := d.Readdirnames(4096)
		n += int64(len(names))
		if err != nil || len(names) == 0 {
			return n, nil
		}
	}
}

// openFileLimit is pid's soft RLIMIT_NOFILE, from /proc/<pid>/limits; 0 if
// it won't say or there is none.
func openFileLimit(pid int) int64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/limits")
	if err != nil {
		return 0
	}
	return parseOpenFileLimit(string(b))
}

func parseOpenFileLimit(limits string) int64 {
	for line := range strings.Lines(limits) {
		rest, ok := strings.CutPrefix(line, "Max open files")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return 0
		}
		n, _ := strconv.ParseInt(f[0], 10, 64)
		return n
	}
	return 0
}

// virtiofsdFileLimit is what virtiofsd is asked to raise its RLIMIT_NOFILE
// to: as far as the hard limit and fs.nr_open allow. Unasked, it stops at a
// million, or at the hard limit when that is lower; 0 leaves it to it.
func virtiofsdFileLimit(hard uint64) uint64 {
	b, err := os.ReadFile("/proc/sys/fs/nr_open")
	if err != nil {
		return 0
	}
	nrOpen, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || hard == 0 {
		return 0
	}
	return min(hard, nrOpen)
}
