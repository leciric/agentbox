package main

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The sampler is the point of the harness: what the host's own programs feel
// while agents work. It runs in this process, on the host, never in an agent,
// and it runs for the whole of a mode, so any stretch of it can be summarized
// afterwards as a window.
//
// Two loops:
//
//   - the fsync prober writes 4 KiB to a file on the host's own filesystem and
//     fsyncs it, five times a second, and records how long that took: what a
//     browser or an editor saving a file waits for. A prober stuck behind a
//     20-second btrfs commit records 20 s, and the next probe starts late;
//   - once a second, host pressure stall information (/proc/pressure io and
//     memory, as the share of that second some or all tasks were stalled),
//     btrfs commit statistics, memory in use, disk throughput, and what the
//     mode's driver says it holds.

type fsyncSample struct {
	T  float64 `json:"t"` // seconds since the sampler started
	Ms float64 `json:"ms"`
}

type hostSample struct {
	T          float64      `json:"t"`
	IOSome     float64      `json:"ioSome"` // % of the last interval
	IOFull     float64      `json:"ioFull"`
	MemSome    float64      `json:"memSome"`
	MemFull    float64      `json:"memFull"`
	MemUsedGiB float64      `json:"memUsedGiB"` // MemTotal - MemAvailable
	HeldGiB    float64      `json:"heldGiB"`    // what the driver holds: agents' cgroups, or the VM process
	ReadMiBs   float64      `json:"readMiBs"`
	WriteMiBs  float64      `json:"writeMiBs"`
	Btrfs      *commitStats `json:"btrfs,omitempty"`
}

// commitStats is /sys/fs/btrfs/<uuid>/commit_stats: counters since mount,
// except cur (the running commit's age) and last (the last commit's length).
type commitStats struct {
	Commits int64 `json:"commits"`
	CurMs   int64 `json:"cur"`
	LastMs  int64 `json:"last"`
	MaxMs   int64 `json:"max"`
	TotalMs int64 `json:"total"`
}

type sampler struct {
	probeDir string
	interval time.Duration
	btrfs    string   // the commit_stats file of the probe's filesystem, "" if it isn't btrfs
	disks    []string // whole physical disks, as /proc/diskstats names them
	held     func() int64

	start time.Time
	mu    sync.Mutex
	fsync []fsyncSample
	host  []hostSample
	errs  []string
}

func newSampler(probeDir string, held func() int64) (*sampler, error) {
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		return nil, err
	}
	return &sampler{
		probeDir: probeDir,
		interval: 200 * time.Millisecond,
		btrfs:    btrfsStatsFor(probeDir),
		disks:    physicalDisks(),
		held:     held,
	}, nil
}

// run samples until ctx ends.
func (s *sampler) run(ctx context.Context) {
	s.start = time.Now()
	var wg sync.WaitGroup
	wg.Go(func() { s.probe(ctx) })
	wg.Go(func() { s.poll(ctx) })
	wg.Wait()
}

func (s *sampler) since() float64 { return time.Since(s.start).Seconds() }

func (s *sampler) probe(ctx context.Context) {
	path := filepath.Join(s.probeDir, "fsync-probe.dat")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		s.fail("fsync probe: %v", err)
		return
	}
	defer func() { _ = f.Close(); _ = os.Remove(path) }()
	buf := make([]byte, 4096)
	for i := 0; ctx.Err() == nil; i++ {
		t0 := time.Now()
		for j := range buf {
			buf[j] = byte(i + j)
		}
		if _, err := f.WriteAt(buf, int64(i%256)*4096); err != nil {
			s.fail("fsync probe: %v", err)
			return
		}
		if err := f.Sync(); err != nil {
			s.fail("fsync probe: %v", err)
			return
		}
		took := time.Since(t0)
		s.mu.Lock()
		s.fsync = append(s.fsync, fsyncSample{T: t0.Sub(s.start).Seconds(), Ms: float64(took.Microseconds()) / 1000})
		s.mu.Unlock()
		if wait := s.interval - took; wait > 0 {
			_ = sleepCtx(ctx, wait)
		}
	}
}

func (s *sampler) poll(ctx context.Context) {
	prevT := time.Now()
	prevIO, _ := readPressure("/proc/pressure/io")
	prevMem, _ := readPressure("/proc/pressure/memory")
	prevR, prevW := s.diskSectors()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := time.Now()
		us := float64(now.Sub(prevT).Microseconds())
		io, _ := readPressure("/proc/pressure/io")
		mem, _ := readPressure("/proc/pressure/memory")
		r, w := s.diskSectors()
		secs := now.Sub(prevT).Seconds()
		h := hostSample{
			T:         now.Sub(s.start).Seconds(),
			IOSome:    pct(io.some-prevIO.some, us),
			IOFull:    pct(io.full-prevIO.full, us),
			MemSome:   pct(mem.some-prevMem.some, us),
			MemFull:   pct(mem.full-prevMem.full, us),
			ReadMiBs:  float64(r-prevR) * 512 / (1 << 20) / secs,
			WriteMiBs: float64(w-prevW) * 512 / (1 << 20) / secs,
		}
		if mi, err := readMeminfo(); err == nil {
			h.MemUsedGiB = gib(mi["MemTotal"] - mi["MemAvailable"])
		}
		if s.held != nil {
			h.HeldGiB = gib(s.held())
		}
		if s.btrfs != "" {
			if c, err := readCommitStats(s.btrfs); err == nil {
				h.Btrfs = &c
			}
		}
		s.mu.Lock()
		s.host = append(s.host, h)
		s.mu.Unlock()
		prevT, prevIO, prevMem, prevR, prevW = now, io, mem, r, w
	}
}

func (s *sampler) fail(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, fmt.Sprintf(format, args...))
}

func pct(deltaUs int64, wallUs float64) float64 {
	if wallUs <= 0 {
		return 0
	}
	return math.Min(100, float64(deltaUs)/wallUs*100)
}

// window summarizes what the sampler saw between from and to, both in its
// seconds.
func (s *sampler) window(name string, from, to float64) *window {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := &window{Name: name, From: from, To: to, Seconds: to - from}
	var lat []float64
	for _, f := range s.fsync {
		if f.T >= from && f.T <= to {
			lat = append(lat, f.Ms)
		}
	}
	w.Fsync = summarizeLatency(lat)
	var hs []hostSample
	for _, h := range s.host {
		if h.T >= from && h.T <= to+1 {
			hs = append(hs, h)
		}
	}
	w.summarizeHost(hs)
	return w
}

type latency struct {
	Count     int     `json:"count"`
	P50       float64 `json:"p50Ms"`
	P95       float64 `json:"p95Ms"`
	P99       float64 `json:"p99Ms"`
	Max       float64 `json:"maxMs"`
	Over100ms int     `json:"over100ms"`
	Over1s    int     `json:"over1s"`
}

func summarizeLatency(ms []float64) latency {
	l := latency{Count: len(ms)}
	if len(ms) == 0 {
		return l
	}
	sorted := append([]float64(nil), ms...)
	sort.Float64s(sorted)
	l.P50, l.P95, l.P99 = percentile(sorted, 50), percentile(sorted, 95), percentile(sorted, 99)
	l.Max = sorted[len(sorted)-1]
	for _, v := range ms {
		if v > 100 {
			l.Over100ms++
		}
		if v > 1000 {
			l.Over1s++
		}
	}
	return l
}

// percentile is the nearest-rank percentile of sorted values.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	return sorted[min(max(rank, 1), len(sorted))-1]
}

type window struct {
	Name    string  `json:"name"`
	From    float64 `json:"from"`
	To      float64 `json:"to"`
	Seconds float64 `json:"seconds"`
	Fsync   latency `json:"fsync"`
	// Pressure: the mean and the worst one-second share of time stalled, in %.
	IOSomeAvg  float64 `json:"ioSomeAvg"`
	IOSomeMax  float64 `json:"ioSomeMax"`
	IOFullAvg  float64 `json:"ioFullAvg"`
	IOFullMax  float64 `json:"ioFullMax"`
	MemSomeAvg float64 `json:"memSomeAvg"`
	MemSomeMax float64 `json:"memSomeMax"`
	MemFullAvg float64 `json:"memFullAvg"`
	MemFullMax float64 `json:"memFullMax"`
	// btrfs transaction commits that ended in the window: how many, their mean
	// length, and the longest seen (a commit still running counts by its age).
	Commits       int64   `json:"btrfsCommits"`
	CommitMeanMs  float64 `json:"btrfsCommitMeanMs"`
	CommitMaxMs   float64 `json:"btrfsCommitMaxMs"`
	ReadMiB       float64 `json:"diskReadMiB"`
	WriteMiB      float64 `json:"diskWriteMiB"`
	WriteMiBsMax  float64 `json:"diskWriteMiBsMax"`
	MemUsedMaxGiB float64 `json:"memUsedMaxGiB"`
	HeldMaxGiB    float64 `json:"heldMaxGiB"`
}

func (w *window) summarizeHost(hs []hostSample) {
	if len(hs) == 0 {
		return
	}
	n := float64(len(hs))
	for _, h := range hs {
		w.IOSomeAvg += h.IOSome / n
		w.IOFullAvg += h.IOFull / n
		w.MemSomeAvg += h.MemSome / n
		w.MemFullAvg += h.MemFull / n
		w.IOSomeMax = math.Max(w.IOSomeMax, h.IOSome)
		w.IOFullMax = math.Max(w.IOFullMax, h.IOFull)
		w.MemSomeMax = math.Max(w.MemSomeMax, h.MemSome)
		w.MemFullMax = math.Max(w.MemFullMax, h.MemFull)
		w.ReadMiB += h.ReadMiBs
		w.WriteMiB += h.WriteMiBs
		w.WriteMiBsMax = math.Max(w.WriteMiBsMax, h.WriteMiBs)
		w.MemUsedMaxGiB = math.Max(w.MemUsedMaxGiB, h.MemUsedGiB)
		w.HeldMaxGiB = math.Max(w.HeldMaxGiB, h.HeldGiB)
	}
	var first, prev *commitStats
	for _, h := range hs {
		c := h.Btrfs
		if c == nil {
			continue
		}
		if first == nil {
			first = c
		}
		if prev != nil && c.Commits > prev.Commits {
			w.CommitMaxMs = math.Max(w.CommitMaxMs, float64(c.LastMs))
		}
		if prev != nil && c.MaxMs > prev.MaxMs {
			w.CommitMaxMs = math.Max(w.CommitMaxMs, float64(c.MaxMs))
		}
		w.CommitMaxMs = math.Max(w.CommitMaxMs, float64(c.CurMs))
		prev = c
	}
	if first != nil && prev != nil {
		w.Commits = prev.Commits - first.Commits
		if w.Commits > 0 {
			w.CommitMeanMs = float64(prev.TotalMs-first.TotalMs) / float64(w.Commits)
			// The counters are rounded to milliseconds apiece, so the mean can
			// come out a hair above the longest one seen.
			w.CommitMaxMs = math.Max(w.CommitMaxMs, w.CommitMeanMs)
		}
	}
}

// snapshot is everything sampled so far, for the raw JSON.
func (s *sampler) snapshot() (fs []fsyncSample, hs []hostSample, errs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]fsyncSample(nil), s.fsync...), append([]hostSample(nil), s.host...), append([]string(nil), s.errs...)
}

type pressure struct{ some, full int64 } // total stalled µs since boot

func readPressure(path string) (pressure, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return pressure{}, err
	}
	return parsePressure(string(b)), nil
}

func parsePressure(s string) pressure {
	var p pressure
	for line := range strings.SplitSeq(s, "\n") {
		kind, rest, _ := strings.Cut(line, " ")
		for field := range strings.FieldsSeq(rest) {
			if v, ok := strings.CutPrefix(field, "total="); ok {
				n, _ := strconv.ParseInt(v, 10, 64)
				switch kind {
				case "some":
					p.some = n
				case "full":
					p.full = n
				}
			}
		}
	}
	return p
}

// readMeminfo returns /proc/meminfo in bytes.
func readMeminfo() (map[string]int64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	return parseMeminfo(string(b)), nil
}

func parseMeminfo(s string) map[string]int64 {
	out := map[string]int64{}
	for line := range strings.SplitSeq(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(f[0], 10, 64)
		if len(f) > 1 && f[1] == "kB" {
			n *= 1024
		}
		out[k] = n
	}
	return out
}

func readCommitStats(path string) (commitStats, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return commitStats{}, err
	}
	return parseCommitStats(string(b)), nil
}

func parseCommitStats(s string) commitStats {
	var c commitStats
	for line := range strings.SplitSeq(s, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		n, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "commits":
			c.Commits = n
		case "cur_commit_ms":
			c.CurMs = n
		case "last_commit_ms":
			c.LastMs = n
		case "max_commit_ms":
			c.MaxMs = n
		case "total_commit_ms":
			c.TotalMs = n
		}
	}
	return c
}

// btrfsStatsFor finds the commit_stats of the btrfs filesystem dir is on: its
// mount's source device, through device-mapper names when it's /dev/mapper/…
// (LUKS), matched against each filesystem's devices. When that can't be
// worked out (in a container, /dev/mapper isn't there) and the host has one
// btrfs filesystem, it's that one.
func btrfsStatsFor(dir string) string {
	all, _ := filepath.Glob("/sys/fs/btrfs/*/commit_stats")
	fstype, source := mountOf(dir)
	if fstype != "btrfs" {
		return ""
	}
	dev := filepath.Base(source)
	if strings.HasPrefix(source, "/dev/mapper/") {
		dms, _ := filepath.Glob("/sys/block/dm-*/dm/name")
		for _, n := range dms {
			if b, err := os.ReadFile(n); err == nil && strings.TrimSpace(string(b)) == dev {
				dev = filepath.Base(filepath.Dir(filepath.Dir(n)))
			}
		}
	} else if real, err := filepath.EvalSymlinks(source); err == nil {
		dev = filepath.Base(real)
	}
	for _, c := range all {
		if _, err := os.Stat(filepath.Join(filepath.Dir(c), "devices", dev)); err == nil {
			return c
		}
	}
	if len(all) == 1 {
		return all[0]
	}
	return ""
}

// mountOf returns the filesystem type and source of the mount dir is on.
func mountOf(dir string) (fstype, source string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", ""
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()
	best := -1
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		pre, post, ok := strings.Cut(sc.Text(), " - ")
		if !ok {
			continue
		}
		pf, qf := strings.Fields(pre), strings.Fields(post)
		if len(pf) < 5 || len(qf) < 2 {
			continue
		}
		mp := unescapeMount(pf[4])
		if (abs == mp || strings.HasPrefix(abs, strings.TrimSuffix(mp, "/")+"/")) && len(mp) > best {
			best, fstype, source = len(mp), qf[0], qf[1]
		}
	}
	return fstype, source
}

func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

// physicalDisks are the whole disks under /sys/block with a device behind
// them: not loop, zram, device-mapper or md devices, which would count the
// same bytes twice.
func physicalDisks() []string {
	var out []string
	entries, _ := os.ReadDir("/sys/block")
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join("/sys/block", e.Name(), "device")); err == nil {
			out = append(out, e.Name())
		}
	}
	return out
}

func (s *sampler) diskSectors() (read, written int64) {
	b, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return 0, 0
	}
	want := map[string]bool{}
	for _, d := range s.disks {
		want[d] = true
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || !want[f[2]] {
			continue
		}
		r, _ := strconv.ParseInt(f[5], 10, 64)
		w, _ := strconv.ParseInt(f[9], 10, 64)
		read += r
		written += w
	}
	return read, written
}

func gib(b int64) float64 { return float64(b) / (1 << 30) }
