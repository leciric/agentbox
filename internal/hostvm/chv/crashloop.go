package chv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
)

// A VM that dies without a clean stop (the host's kernel killing Cloud
// Hypervisor for memory, the host itself rebooting, the supervisor killed)
// and is started again soon has every agent resume its turn at once, which is
// what ran the host out of memory the first time: on 18 Nov 2026 a 32 GiB PC
// lost its VM six times in 35 minutes. So the supervisor keeps a record of the
// VM's runs (Layout.RunsFile): when each started, when it was last seen
// running, and whether it stopped cleanly. When crashLoopCount runs that
// didn't were last seen within crashLoopWindow, the next start is a safe one:
// the VM boots at its memoryMin and grows slowly for safeFor
// (memPolicy.SafeUntil), and api.VMBoot, written before it boots, tells the
// daemon to resume its agents slowly too.

const (
	crashLoopWindow = 15 * time.Minute
	crashLoopCount  = 2
	safeFor         = 15 * time.Minute
	// aliveEvery is how often a run's Alive is brought up to date.
	aliveEvery = 30 * time.Second
	// runsKept is how many runs the record keeps.
	runsKept = 20
)

// RunsFile is the record of the VM's runs (crashloop.go).
func (l Layout) RunsFile() string { return filepath.Join(l.Dir(), "runs.json") }

// BootFile is where the supervisor tells the daemon how it started the VM
// (api.VMBoot).
func (l Layout) BootFile() string { return filepath.Join(l.Dir(), api.VMBootFile) }

// vmRun is one run of the VM.
type vmRun struct {
	Started time.Time `json:"started"`
	// Alive is when it was last seen running.
	Alive time.Time `json:"alive"`
	// Ended is when it stopped, and Clean whether that was a stop the
	// supervisor saw through; zero for a run that died with its supervisor
	// or the host.
	Ended time.Time `json:"ended,omitzero"`
	Clean bool      `json:"clean,omitempty"`
	How   string    `json:"how,omitempty"`
}

// lastSeen is when r was last known to run.
func (r vmRun) lastSeen() time.Time {
	if !r.Ended.IsZero() {
		return r.Ended
	}
	if !r.Alive.IsZero() {
		return r.Alive
	}
	return r.Started
}

// runLog is the record of the VM's runs, the last of them this one.
type runLog struct {
	path string
	mu   sync.Mutex
	runs []vmRun
}

// readRuns reads the record at path; an unreadable one is an empty record.
func readRuns(path string) *runLog {
	rl := &runLog{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &rl.runs)
	}
	return rl
}

// crashes are when the runs that didn't stop cleanly were last seen, of
// those last seen within crashLoopWindow before now, oldest first.
func crashes(runs []vmRun, now time.Time) []time.Time {
	var out []time.Time
	for _, r := range runs {
		if r.Clean {
			continue
		}
		if seen := r.lastSeen(); now.Sub(seen) <= crashLoopWindow {
			out = append(out, seen)
		}
	}
	return out
}

// bootFor is how a VM starting at now, after runs, starts: in safe mode
// after crashLoopCount crashes within crashLoopWindow.
func bootFor(runs []vmRun, now time.Time) api.VMBoot {
	b := api.VMBoot{Started: now.UTC()}
	c := crashes(runs, now)
	if len(c) < crashLoopCount {
		return b
	}
	when := make([]string, len(c))
	for i, t := range c {
		b.Crashes = append(b.Crashes, t.UTC())
		when[i] = t.Local().Format("15:04")
	}
	b.Safe, b.SafeUntil = true, now.Add(safeFor).UTC()
	b.Why = fmt.Sprintf("the VM died without a clean stop %d times in %s (last seen running at %s)", len(c), crashLoopWindow, strings.Join(when, ", "))
	return b
}

// start begins a run at now and says how it boots.
func (rl *runLog) start(now time.Time) api.VMBoot {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b := bootFor(rl.runs, now)
	rl.runs = append(rl.runs, vmRun{Started: now.UTC(), Alive: now.UTC()})
	if len(rl.runs) > runsKept {
		rl.runs = rl.runs[len(rl.runs)-runsKept:]
	}
	rl.save()
	return b
}

// alive notes the run is still going at now.
func (rl *runLog) alive(now time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if n := len(rl.runs); n > 0 && rl.runs[n-1].Ended.IsZero() {
		rl.runs[n-1].Alive = now.UTC()
		rl.save()
	}
}

// end says how the run ended at now: clean for a stop the supervisor saw
// through, not for a VM that died under it.
func (rl *runLog) end(now time.Time, clean bool, how string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if n := len(rl.runs); n > 0 && rl.runs[n-1].Ended.IsZero() {
		r := &rl.runs[n-1]
		r.Alive, r.Ended, r.Clean, r.How = now.UTC(), now.UTC(), clean, how
		rl.save()
	}
}

// save writes the record, through a temporary file so a host dying mid-write
// leaves the last one whole.
func (rl *runLog) save() {
	b, err := json.MarshalIndent(rl.runs, "", "  ")
	if err != nil {
		return
	}
	_ = writeFileAtomic(rl.path, append(b, '\n'), 0o644)
}

// writeBoot writes b where the daemon reads it as it starts.
func writeBoot(path string, b api.VMBoot) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o644)
}
