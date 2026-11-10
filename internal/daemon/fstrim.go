package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/hostos"
)

// The VM's disks are sparse files on the host: they grow as the VM writes,
// and only shrink when the VM tells its disk which blocks it no longer uses
// (a discard), which Cloud Hypervisor turns into a hole punched in the file.
// Btrfs, the agents' pool, sends some of those by itself where the kernel
// turns discard=async on, rate-limited and skipping small extents; the VM's
// ext4 system disk sends none. So space a destroyed agent, an evicted
// cache or an old base image gave back inside the VM stayed taken on the
// host's disk until the next trim.
//
// The daemon trims the VM's file systems (fstrim) itself: a while after it
// starts, every trimEvery after that, and trimSettle after an agent's machine
// or a saved base is removed, at the lowest best-effort I/O priority (never
// the idle class, which under BFQ starves a disk the agents keep busy) and
// not while a heavy run is under way. The VM's own fstrim.timer runs once a
// week, which on one user's VM left 18 GiB of pool.raw that the pool no
// longer used.

const (
	// trimDelay holds the first trim back after the daemon starts, past the
	// agents' own starts.
	trimDelay = 10 * time.Minute
	// trimEvery is how often the VM's file systems are trimmed anyway.
	trimEvery = 12 * time.Hour
	// trimSettle is how long a removal waits for the next, so that retiring
	// ten agents trims once.
	trimSettle = 3 * time.Minute
	// trimBusyWait is how long a trim waits for a heavy run to end before
	// looking again; it waits at most trimEvery in all.
	trimBusyWait = 10 * time.Minute
	// trimTimeout bounds one trim: on a large, fragmented pool the first one
	// can take minutes.
	trimTimeout = 30 * time.Minute
)

// trimmer trims the VM's file systems, and remembers the last trim for the
// disk breakdown.
type trimmer struct {
	// mounts are the file systems to trim, those that exist.
	mounts func() []string
	// run runs fstrim on one mount point and returns its output.
	run  func(ctx context.Context, mount string) (string, error)
	busy func() bool
	logf func(format string, args ...any)
	kick chan struct{}

	mu   sync.Mutex
	last TrimResult
	// unsupported are mounts whose disk takes no discards, not tried again.
	unsupported map[string]bool
}

// TrimResult is the last trim: when, and how many bytes the VM gave back
// to the host's disk. Bytes is what fstrim reports trimmed, which counts
// free space already given back once more on a disk that doesn't remember it.
type TrimResult struct {
	At    time.Time
	Bytes int64
	Err   string
}

func (s *Server) newTrimmer() *trimmer {
	return &trimmer{
		mounts: vmTrimMounts,
		run:    runFstrim,
		busy:   s.heavyRunActive,
		logf:   s.logf,
		kick:   make(chan struct{}, 1),
	}
}

// vmTrimMounts are the VM's file systems: its system disk, and the agents'
// pool where it has one of its own (Cloud Hypervisor). A Lima or WSL VM has
// one disk, at /.
func vmTrimMounts() []string {
	mounts := []string{"/"}
	if isMountpoint(poolDataMount) {
		mounts = append(mounts, poolDataMount)
	}
	return mounts
}

// runFstrim trims mount, as root, at the lowest best-effort I/O priority.
func runFstrim(ctx context.Context, mount string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, trimTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "-n", "ionice", "-c2", "-n7", "nice", "-n19", "fstrim", "-v", mount)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("fstrim %s: %w: %s", mount, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// trimmedBytes reads the bytes fstrim -v reports: "/: 1.2 GiB (1288490188
// bytes) trimmed".
var trimmedBytes = regexp.MustCompile(`\((\d+) bytes\) trimmed`)

// errNoDiscard is a disk that takes no discards: fstrim's "the discard
// operation is not supported".
var errNoDiscard = errors.New("discard not supported")

// trimOnce trims every mount, and records what came of it.
func (t *trimmer) trimOnce(ctx context.Context) TrimResult {
	res := TrimResult{At: time.Now()}
	var errs []string
	for _, m := range t.mounts() {
		t.mu.Lock()
		skip := t.unsupported[m]
		t.mu.Unlock()
		if skip {
			continue
		}
		out, err := t.run(ctx, m)
		if err != nil {
			if strings.Contains(strings.ToLower(out+err.Error()), "not supported") {
				t.mu.Lock()
				if t.unsupported == nil {
					t.unsupported = map[string]bool{}
				}
				t.unsupported[m] = true
				t.mu.Unlock()
				t.logf("trim: %s: %v, not tried again until the daemon restarts", m, errNoDiscard)
				continue
			}
			errs = append(errs, err.Error())
			continue
		}
		if sm := trimmedBytes.FindStringSubmatch(out); sm != nil {
			n, _ := strconv.ParseInt(sm[1], 10, 64)
			res.Bytes += n
		}
	}
	res.Err = strings.Join(errs, "; ")
	t.mu.Lock()
	t.last = res
	t.mu.Unlock()
	if res.Err != "" {
		t.logf("trim: %s", res.Err)
	} else {
		t.logf("trim: gave the host %d MiB back", res.Bytes>>20)
	}
	return res
}

// Last is the last trim, zero before the first.
func (t *trimmer) Last() TrimResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

// Kick asks for a trim once removals settle, without waiting for it.
func (t *trimmer) Kick() {
	if t == nil {
		return
	}
	select {
	case t.kick <- struct{}{}:
	default:
	}
}

// loop trims after delay, then every every and after each kick, until ctx
// ends. after makes the timers, so a test can step it.
func (t *trimmer) loop(ctx context.Context, delay, every, settle, busyWait time.Duration, after func(time.Duration) <-chan time.Time) {
	wait := delay
	for {
		select {
		case <-ctx.Done():
			return
		case <-after(wait):
		case <-t.kick:
			// More removals may follow: wait for them to settle.
			for settled := false; !settled; {
				select {
				case <-ctx.Done():
					return
				case <-t.kick:
				case <-after(settle):
					settled = true
				}
			}
		}
		for waited := time.Duration(0); t.busy() && waited < every; waited += busyWait {
			select {
			case <-ctx.Done():
				return
			case <-after(busyWait):
			}
		}
		t.trimOnce(ctx)
		wait = every
	}
}

// runTrim is the trim loop, in AgentBox's VM only: a host install's disks are
// its owner's.
func (s *Server) runTrim(ctx context.Context) {
	if !hostos.InVM() || s.trim == nil {
		return
	}
	if _, err := exec.LookPath("fstrim"); err != nil {
		if _, err := os.Stat("/usr/sbin/fstrim"); err != nil {
			s.logf("trim: no fstrim in the VM; the VM's disk images keep space it freed")
			return
		}
	}
	s.trim.loop(ctx, trimDelay, trimEvery, trimSettle, trimBusyWait, time.After)
}
