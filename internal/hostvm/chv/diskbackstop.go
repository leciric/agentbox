package chv

import (
	"context"
	"time"

	"agentbox/internal/api"
)

// The VM's disks are sparse files on the host (image.go): the pool disk says
// 100 GiB to the VM whatever the host has free, and grows into the host's
// disk as the VM writes. The daemon's disk guard in the VM watches the host's
// disk through the shared home and keeps its floor there (daemon/diskguard.go),
// but it can only act while the daemon runs and Incus answers. This is the
// last line under it, in the supervisor, which runs for as long as the VM
// does, app or no app: when the host's disk that holds the VM's disks gets
// down to api.VMDiskBackstop free, the VM is paused, the way `agentbox vm
// pause` pauses it, so no write of the VM's fails half done and corrupts its
// file systems; it's resumed once twice that is free again. Pausing keeps
// everything in memory and loses nothing.

// diskTick is how often the supervisor reads what the host has free.
const diskTick = 5 * time.Second

// backstop says what the supervisor does about the host's disk: pause a
// running VM that's down to its last VMDiskBackstop, resume one it paused
// for that once there's room again. free is 0 when it couldn't be read,
// which does nothing either way. pausedForDisk says the supervisor paused it;
// a VM someone else paused is theirs to resume.
func backstop(free int64, state string, pausedForDisk bool) (pause, resume bool) {
	if free <= 0 {
		return false, false
	}
	switch {
	case free < api.VMDiskBackstop && (state == api.VMRunning || state == api.VMStarting):
		return true, false
	case pausedForDisk && state == api.VMPaused && free >= 2*api.VMDiskBackstop:
		return false, true
	}
	return false, false
}

// diskLoop runs backstop every diskTick while the VM runs.
func (s *supervisor) diskLoop(ctx context.Context) {
	tick := time.NewTicker(diskTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s.checkDisk(ctx, HostFree(s.l.Dir()))
	}
}

func (s *supervisor) checkDisk(ctx context.Context, free int64) {
	state := s.getState()
	s.mu.Lock()
	paused := s.pausedForDisk
	if paused && state != api.VMPaused {
		// Resumed by someone else since: if the disk is still that full, it's
		// paused again below, and is the supervisor's to resume then.
		s.pausedForDisk, paused = false, false
	}
	s.mu.Unlock()
	pause, resume := backstop(free, state, paused)
	switch {
	case pause:
		if err := s.pause(ctx); err != nil {
			s.logf("disk: the host has %s free where the VM's disks are, and pausing the VM failed: %v", gib(free), err)
			return
		}
		s.mu.Lock()
		s.pausedForDisk = true
		s.mu.Unlock()
		s.logf("disk: paused the VM, the host has only %s free where its disks are (%s); it resumes once %s is free", gib(free), s.l.Dir(), gib(2*api.VMDiskBackstop))
	case resume:
		if err := s.resume(ctx); err != nil {
			s.logf("disk: resuming the VM: %v", err)
			return
		}
		s.mu.Lock()
		s.pausedForDisk = false
		s.mu.Unlock()
		s.logf("disk: resumed the VM, the host has %s free again", gib(free))
	}
}
