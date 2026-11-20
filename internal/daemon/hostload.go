package daemon

import (
	"net/http"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/hostload"
	"agentbox/internal/hostos"
	"agentbox/internal/pressure"
)

// The user's computer comes first: its front end reports how loaded it is
// (internal/hostload, PUT /v1/host/load), and while it is busy builds run
// fewer jobs (Sample.HostBusy; memoryjobs.go counts it as short), and while
// it is below its hard floor no agent's machine boots (HoldBoots,
// admission.go). Nothing else waits for it: an agent may stall, the
// desktop doesn't. Where the front end has
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

// holdBoots reports whether a fresh report says to boot no machine: the
// host is below its hard floor (api.HostLoad.HoldBoots).
func (h *hostLoad) holdBoots(now time.Time) bool {
	l, ok := h.current(now)
	return ok && l.HoldBoots
}

// withCeiling is m with its cap lowered to what a fresh report says the VM
// may grow to now (api.HostLoad.VMCeiling): growth the host can't spare
// isn't room to boot or build in.
func (h *hostLoad) withCeiling(m pressure.Mem, now time.Time) pressure.Mem {
	if l, ok := h.current(now); ok && l.VMCeiling > 0 && m.Cap > l.VMCeiling {
		m.Cap = l.VMCeiling
	}
	return m
}

// reachableMemory is what the lead chats' shared ceiling (sharedmemory.go)
// is a share of, for a VM whose cap is vmCap: the cap, or less while the host
// can't spare that much (a fresh report's VMCeiling), but never less than
// what the VM has now, so a ceiling that dips while the user's desktop takes
// memory for a moment doesn't push memory.max under what the chats use. The
// agents' ceiling is a share of what the VM has now (agent.MemoryNow)
// instead: this is more than the VM has whenever it sits under its cap, and
// on 20 Nov 2026 it let the agents take all of a VM held at 13 of its 16 GiB.
func (s *Server) reachableMemory(vmCap int64, now time.Time) int64 {
	l, ok := s.hostLoad.current(now)
	if !ok {
		return vmCap
	}
	return reachable(vmCap, l.VMCeiling, s.lastMem().Total)
}

// reachable is reachableMemory's rule: vmCap, lowered to ceiling (0 for none)
// but never under plugged, what the VM has now.
func reachable(vmCap, ceiling, plugged int64) int64 {
	if ceiling <= 0 || vmCap <= 0 || ceiling >= vmCap {
		return vmCap
	}
	return min(vmCap, max(ceiling, plugged))
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
	fresh := !h.got.IsZero() && now.Sub(h.got) <= api.HostLoadTTL
	was, held := fresh && h.last.Busy, fresh && h.last.HoldBoots
	h.last, h.got = load, now
	reclaim := load.Reclaim && now.Sub(h.lastReclaim) >= hostReclaimEvery
	if reclaim {
		h.lastReclaim = now
	}
	h.mu.Unlock()
	if load.Busy != was {
		if load.Busy {
			s.logf("host load: this computer, outside the VM, is busy (%s): builds and tests run fewer jobs", hostload.Words(load))
		} else {
			s.logf("host load: this computer is calm again")
		}
	}
	if load.HoldBoots != held {
		if load.HoldBoots {
			s.logf("host load: this computer is below the memory it can't go under (%s available): agents' machines wait to boot, and the VM gives back what it can", agent.HumanBytes(load.Available))
		} else {
			s.logf("host load: this computer is over its hard floor again: agents' machines may boot")
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
