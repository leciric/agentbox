package daemon

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/hostos"
)

// The user's computer comes first: its front end reports how loaded it is
// (internal/hostload, PUT /v1/host/load), and while it is busy the pressure
// loop starts no heavy command and resumes no paused one (Sample.HostBusy),
// and builds run fewer jobs (memoryjobs.go counts it as short). What runs
// goes on: an agent may stall, the desktop doesn't. Where the front end has
// no memory loop of its own (Lima, WSL), a report short of memory also has
// the daemon drop the VM's caches, at most every hostReclaimEvery, which free
// page reporting (or WSL's autoMemoryReclaim) then gives back to the host.

// hostReclaimEvery is the least time between two cache drops for the host.
const hostReclaimEvery = 2 * time.Minute

type hostLoad struct {
	mu          sync.Mutex
	last        api.HostLoad
	got         time.Time // when last came; zero before the first
	lastReclaim time.Time
}

// current is the last report, if it came within api.HostLoadTTL of now.
func (h *hostLoad) current(now time.Time) (api.HostLoad, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.got.IsZero() || now.Sub(h.got) > api.HostLoadTTL {
		return api.HostLoad{}, false
	}
	return h.last, true
}

// busy reports whether the host is busy now, by a fresh report.
func (h *hostLoad) busy(now time.Time) bool {
	l, ok := h.current(now)
	return ok && l.Busy
}

// putHostLoad is PUT /v1/host/load.
func (s *Server) putHostLoad(w http.ResponseWriter, r *http.Request) error {
	var load api.HostLoad
	if err := readJSON(r, &load); err != nil {
		return err
	}
	now := time.Now()
	h := &s.hostLoad
	h.mu.Lock()
	was := !h.got.IsZero() && now.Sub(h.got) <= api.HostLoadTTL && h.last.Busy
	h.last, h.got = load, now
	reclaim := load.Reclaim && now.Sub(h.lastReclaim) >= hostReclaimEvery
	if reclaim {
		h.lastReclaim = now
	}
	h.mu.Unlock()
	if load.Busy != was {
		if load.Busy {
			s.logf("host load: this computer is busy (%s): heavy commands wait to start", hostWhy(load))
		} else {
			s.logf("host load: this computer is calm again")
		}
	}
	if reclaim {
		// Aside: it can take seconds, and the front end waits on nothing.
		go func() {
			if err := dropVMCaches(); err != nil {
				s.logf("host load: dropping the VM's caches for the host: %v", err)
			} else {
				s.logf("host load: this computer is short of memory: dropped the VM's caches")
			}
		}()
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// hostWhy says what made a report busy, for the log.
func hostWhy(l api.HostLoad) string {
	switch l.Why {
	case "memory":
		if l.Level != "" {
			return "memory pressure " + l.Level
		}
		if l.Memory >= 0 {
			return "memory, " + percent(l.Memory) + " stalled"
		}
		return "memory, " + agent.HumanBytes(l.Available) + " available"
	case "cpu":
		if l.CPU >= 0 {
			return "CPU, " + percent(l.CPU) + " stalled"
		}
		return "CPU, " + percent(l.CPUShare) + " busy"
	case "io":
		return "disk, " + percent(l.IO) + " stalled"
	}
	return "calming down"
}

func percent(f float64) string { return fmt.Sprintf("%.0f%%", f) }

// dropVMCaches drops the VM's page cache and the kernel's reclaimable caches
// and compacts what's free into whole blocks, as chv's reclaimScript does.
// Only in the VM: on a machine of the user's own, its caches are theirs.
// Tests replace it.
var dropVMCaches = func() error {
	if !hostos.InVM() {
		return nil
	}
	if err := agent.Sudo("tee", "3", "/proc/sys/vm/drop_caches"); err != nil {
		return err
	}
	return agent.Sudo("tee", "1", "/proc/sys/vm/compact_memory")
}
