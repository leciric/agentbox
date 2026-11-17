//go:build pressurelab

package hostload

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"agentbox/internal/pressure"
)

// TestHostLab is a small stand-in for "ten agents build at once", on one
// machine: LAB_BUILDS (10) processes at nice 10, as the VM's are
// (chv/priority_linux.go), each spinning a CPU and writing LAB_BUILD_MB with
// an fsync every 4 MiB, while a probe stands in for the desktop at nice 0:
// every 50 ms it sleeps, writes and fsyncs 4 KiB, and times both, as an app
// saving its state would. It runs twice:
//
//   - before: every build starts at once, as heavy commands did with the VM
//     calm, whatever the host;
//   - after: each build asks the governor first, which this machine's own
//     PSI, read and judged as a front end does (Read, Judge), holds back
//     while the host is busy (Sample.HostBusy).
//
// and logs the probe's latencies, the host's PSI and how long the builds
// took. Not a test of a threshold: the numbers go in the pull request.
//
//	go test -tags pressurelab -run TestHostLab -v -timeout 30m ./internal/hostload
//
// LAB_DIR is where the builds write (default ~/hostlab: not a tmpfs).
func TestHostLab(t *testing.T) {
	if os.Getenv("HOSTLAB_BUILD") != "" {
		build()
		return
	}
	dir := os.Getenv("LAB_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "hostlab")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	n := labInt("LAB_BUILDS", 10)
	for _, gated := range []bool{false, true} {
		name := map[bool]string{false: "before", true: "after"}[gated]
		settle(t)
		r := runLab(t, dir, n, gated)
		t.Logf("%s: %s", name, r)
	}
}

type labResult struct {
	sleep, fsync         []time.Duration
	cpu, io, mem         []float64
	took                 time.Duration
	maxRunning, busyTick int
	ticks                int
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := slices.Clone(d)
	slices.Sort(s)
	return s[min(len(s)-1, int(p*float64(len(s))))]
}

func avg(f []float64) float64 {
	var t float64
	for _, x := range f {
		t += x
	}
	return t / float64(max(1, len(f)))
}

func (r labResult) String() string {
	ms := func(d time.Duration) string { return fmt.Sprintf("%.0f", float64(d)/1e6) }
	return fmt.Sprintf("probe sleep(50ms) lateness p50/p99/max %s/%s/%s ms; 4 KiB write+fsync p50/p99/max %s/%s/%s ms; "+
		"host PSI some avg10 mean cpu %.0f%% io %.0f%% memory %.0f%%; busy %d of %d readings; at most %d builds at once; all builds done in %s",
		ms(pct(r.sleep, .5)), ms(pct(r.sleep, .99)), ms(pct(r.sleep, 1)),
		ms(pct(r.fsync, .5)), ms(pct(r.fsync, .99)), ms(pct(r.fsync, 1)),
		avg(r.cpu), avg(r.io), avg(r.mem), r.busyTick, r.ticks, r.maxRunning, r.took.Round(time.Second))
}

// settle waits for the host's PSI to come down from whatever ran before.
func settle(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		r, err := Read()
		if err != nil {
			t.Fatal(err)
		}
		if r.over() == "" && r.CPU < 10 && r.IO < 10 {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func runLab(t *testing.T, dir string, n int, gated bool) labResult {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu      sync.Mutex
		res     labResult
		running int
	)
	// The probe: the desktop.
	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		f, err := os.Create(filepath.Join(dir, "probe"))
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = f.Close() }()
		buf := make([]byte, 4096)
		for ctx.Err() == nil {
			start := time.Now()
			time.Sleep(50 * time.Millisecond)
			late := time.Since(start) - 50*time.Millisecond
			start = time.Now()
			_, _ = f.WriteAt(buf, 0)
			_ = f.Sync()
			took := time.Since(start)
			mu.Lock()
			res.sleep = append(res.sleep, late)
			res.fsync = append(res.fsync, took)
			mu.Unlock()
		}
	}()
	// The governor, fed as the daemon is: the host's report every Interval.
	g := pressure.NewGovernor(pressure.DefaultPolicy)
	started := map[string]chan struct{}{}
	var gmu sync.Mutex
	var j Judge
	go func() {
		tick := time.NewTicker(Interval)
		defer tick.Stop()
		for {
			reading, err := Read()
			if err == nil {
				load := j.Judge(reading, time.Now())
				mu.Lock()
				res.ticks++
				res.cpu, res.io, res.mem = append(res.cpu, reading.CPU), append(res.io, reading.IO), append(res.mem, reading.Memory)
				if load.Busy {
					res.busyTick++
				}
				mu.Unlock()
				gmu.Lock()
				acts := g.Step(time.Now(), pressure.Sample{HostBusy: gated && load.Busy})
				for _, r := range acts.Start {
					close(started[r.Key])
				}
				gmu.Unlock()
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	begin := time.Now()
	var wg sync.WaitGroup
	for i := range n {
		key := "b" + strconv.Itoa(i)
		ch := make(chan struct{})
		gmu.Lock()
		started[key] = ch
		if _, ok := g.Ask("lab/agent-"+key, key, "build", time.Hour, time.Now()); ok {
			close(ch)
		}
		gmu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if gated {
				<-ch
			}
			mu.Lock()
			running++
			res.maxRunning = max(res.maxRunning, running)
			mu.Unlock()
			cmd := exec.Command("nice", "-n", "10", os.Args[0], "-test.run=^TestHostLab$")
			cmd.Env = append(os.Environ(), "HOSTLAB_BUILD=1", "HOSTLAB_FILE="+filepath.Join(dir, key))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("build %s: %v: %s", key, err, out)
			}
			mu.Lock()
			running--
			mu.Unlock()
			gmu.Lock()
			g.End("lab/agent-"+key, key)
			gmu.Unlock()
		}()
	}
	wg.Wait()
	res.took = time.Since(begin)
	cancel()
	<-probeDone
	return res
}

// build is one build: LAB_BUILD_SECS of a CPU spinning while another thread
// writes LAB_BUILD_MB to HOSTLAB_FILE, fsyncing every 4 MiB.
func build() {
	secs := labInt("LAB_BUILD_SECS", 15)
	mb := labInt("LAB_BUILD_MB", 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f, err := os.Create(os.Getenv("HOSTLAB_FILE"))
		if err != nil {
			return
		}
		defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
		buf := make([]byte, 1<<20)
		for i := range mb {
			_, _ = f.Write(buf)
			if i%4 == 3 {
				_ = f.Sync()
			}
		}
	}()
	end := time.Now().Add(time.Duration(secs) * time.Second)
	x := 0
	for time.Now().Before(end) {
		for i := range 1 << 20 {
			x += i
		}
	}
	<-done
	_ = x
}

func labInt(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return def
}
