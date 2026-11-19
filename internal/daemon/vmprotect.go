package daemon

import (
	"fmt"
	"os"

	"agentbox/internal/agent"
	"agentbox/internal/hostos"
)

// protectVM keeps AgentBox itself responsive in its VM, however much memory
// the agents want (agent/vmprotect.go): it takes away the zram swap an
// earlier daemon gave the VM, has cgroup2 share memory protection down a
// slice, protects system.slice, Incus and the daemon's own scope from the
// kernel's reclaim and from swap, and moves the daemon into that scope. Each
// step is logged and none stops the others. Outside the VM (an earlier
// release's host install) the machine is its owner's, and nothing is done.
func (s *Server) protectVM() {
	if !hostos.InVM() {
		return
	}
	// The disks' scheduler first: it decides whether the agents' io.weight
	// (watchMemory) counts for anything.
	if did, err := agent.UseBFQ(); err != nil {
		s.logf("disk scheduler: %v; agents' disk use doesn't yield to the daemon's and Incus's", err)
	} else if did != "" {
		s.logf("disk scheduler: %s", did)
	}
	if did, err := agent.RecursiveProt(); err != nil {
		s.logf("memory protection: %v; only the units given their own are protected", err)
	} else if did != "" {
		s.logf("memory protection: %s", did)
	}
	if err := agent.ProtectServices(); err != nil {
		s.logf("memory protection: Incus and the VM's services: %v", err)
	}
	pid := os.Getpid()
	if err := agent.AdoptDaemon(pid); err != nil {
		s.logf("memory protection: moving the daemon into %s: %v; it runs unprotected where it was started", agent.DaemonScope(pid), err)
	} else {
		s.logf("memory protection: the daemon runs in system.slice/%s", agent.DaemonScope(pid))
	}
	// Last, and aside: turning off a full zram device reads all of it back
	// into memory, which takes a while.
	go func() {
		if did, err := agent.DropZram(); err != nil {
			s.logf("swap: %v", err)
		} else if did != "" {
			s.logf("swap: %s", did)
		}
	}()
}

// raiseOOMScore raises the OOM score of every process in an agent's machine
// (agent.OOMScorer), so that when the VM runs out, the kernel kills one of
// the agents' processes rather than Incus or a service the app goes through.
func (s *Server) raiseOOMScore(ref, instance string) {
	if s.oomScorer == nil || !hostos.InVM() {
		return
	}
	if _, err := s.oomScorer.Raise(instance); err != nil {
		s.logOnce("oom score "+instance, fmt.Sprintf("memory: raising %s's processes' OOM score: %v", ref, err))
	}
}
