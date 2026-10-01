package chv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"agentbox/internal/api"
)

// handler is what the supervisor serves on paths.VMSocket: the VM's state,
// and its controls (api.VMStatus).
func (s *supervisor) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/vm", func(w http.ResponseWriter, r *http.Request) {
		writeVMJSON(w, http.StatusOK, s.status(r.Context()))
	})
	mux.HandleFunc("POST /v1/vm/pause", func(w http.ResponseWriter, r *http.Request) {
		s.act(w, r, s.pause)
	})
	mux.HandleFunc("POST /v1/vm/resume", func(w http.ResponseWriter, r *http.Request) {
		s.act(w, r, s.resume)
	})
	mux.HandleFunc("POST /v1/vm/resize", func(w http.ResponseWriter, r *http.Request) {
		var req api.VMResizeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeVMError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		s.act(w, r, func(ctx context.Context) error { return s.resize(ctx, req) })
	})
	mux.HandleFunc("POST /v1/vm/stop", func(w http.ResponseWriter, r *http.Request) {
		var req api.VMStopRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeVMError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		select {
		case s.stopc <- req:
		default: // stopping already
		}
		s.setState(api.VMStopping)
		writeVMJSON(w, http.StatusAccepted, s.status(r.Context()))
	})
	return mux
}

// errConflict is an action the VM's state doesn't allow.
type errConflict string

func (e errConflict) Error() string { return string(e) }

func (s *supervisor) act(w http.ResponseWriter, r *http.Request, action func(context.Context) error) {
	if err := action(r.Context()); err != nil {
		status := http.StatusInternalServerError
		var conflict errConflict
		if errors.As(err, &conflict) {
			status = http.StatusConflict
		}
		writeVMError(w, status, err.Error())
		return
	}
	writeVMJSON(w, http.StatusOK, s.status(r.Context()))
}

func (s *supervisor) pause(ctx context.Context) error {
	switch s.getState() {
	case api.VMPaused:
		return nil
	case api.VMStopping:
		return errConflict("the VM is stopping")
	}
	if err := s.m.pause(ctx); err != nil {
		return err
	}
	s.setState(api.VMPaused)
	return nil
}

func (s *supervisor) resume(ctx context.Context) error {
	switch s.getState() {
	case api.VMPaused:
	case api.VMStopping:
		return errConflict("the VM is stopping")
	default:
		return nil
	}
	if err := s.m.resume(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	state := api.VMStarting
	if s.daemonReady {
		state = api.VMRunning
	}
	s.mu.Unlock()
	s.setState(state)
	return nil
}

// resize gives the running VM req's CPUs and memory cap, within its Room: a
// vCPU is hotplugged or ejected, and the memory policy keeps to the new cap,
// giving back at once whatever the VM holds above it. Anything outside the
// Room needs a restart, which is the caller's to do (errConflict).
func (s *supervisor) resize(ctx context.Context, req api.VMResizeRequest) error {
	switch s.getState() {
	case api.VMPaused:
		return errConflict("the VM is paused: resume it first")
	case api.VMStopping:
		return errConflict("the VM is stopping")
	}
	s.mu.Lock()
	c := s.c
	s.mu.Unlock()
	live := s.m.live(c)
	if req.CPUs != 0 && (req.CPUs < live.MinCPUs || req.CPUs > live.MaxCPUs) {
		return errConflict(fmt.Sprintf("the VM booted with room for %d CPUs: %d needs a restart", live.MaxCPUs, req.CPUs))
	}
	if req.MemoryCap != 0 && (req.MemoryCap < live.MinMemory || req.MemoryCap > live.MaxMemory) {
		return errConflict(fmt.Sprintf("the VM booted with room for %s of memory: a cap of %s needs a restart", gib(live.MaxMemory), gib(req.MemoryCap)))
	}
	if req.Disk != 0 && req.Disk != live.MinDisk {
		switch {
		case live.MaxDisk == 0:
			return errConflict("the VM's disk can't grow while it runs: it needs a restart")
		case req.Disk < live.MinDisk:
			return errConflict(fmt.Sprintf("the VM's disk is %s, and a disk only grows", gib(live.MinDisk)))
		case req.Disk > live.MaxDisk:
			return errConflict(fmt.Sprintf("a disk of %s: the most is %s", gib(req.Disk), gib(live.MaxDisk)))
		}
	}
	if req.CPUs != 0 && req.CPUs != c.CPUs {
		if err := s.m.setCPUs(ctx, req.CPUs); err != nil {
			return err
		}
		s.logf("CPUs: %d → %d", c.CPUs, req.CPUs)
		grew := req.CPUs > c.CPUs
		c.CPUs = req.CPUs
		if grew {
			s.onlineCPUs(ctx)
		}
	}
	if req.MemoryCap != 0 && req.MemoryCap != c.MemoryCap {
		c.MemoryCap = req.MemoryCap
		s.logf("memory cap: %s", gib(c.MemoryCap))
	}
	if req.Disk != 0 && req.Disk > live.MinDisk {
		if err := s.m.setDisk(ctx, req.Disk); err != nil {
			return err
		}
		s.logf("pool disk: %s → %s", gib(live.MinDisk), gib(req.Disk))
		c.Disk = req.Disk
		if _, err := s.ssh(ctx, GrowPoolScript(req.Disk)); err != nil {
			return fmt.Errorf("the VM's disk is %s now, but its pool didn't grow to it: %w", gib(req.Disk), err)
		}
	}
	s.mu.Lock()
	s.c = c
	s.policy.Cap = c.MemoryMin + hotplugSize(c)
	over := s.mem.Requested > s.policy.Cap
	if over {
		s.mem.Requested = s.policy.Cap
	}
	target := s.policy.Cap
	s.mu.Unlock()
	if over {
		// Down to the new cap now, rather than when the policy next shrinks.
		if err := s.m.setMemory(ctx, target); err != nil {
			s.logf("resizing the VM's memory to its new cap %s: %v", gib(target), err)
		}
	}
	return nil
}

// onlineCPUs onlines the vCPUs the guest was given and left offline: a
// kernel with no udev rule for it waits to be told. Every step is
// best-effort, and a vCPU that's online already is left alone. The guest's
// ACPI hotplug takes a moment to add the new ones, so it looks twice.
func (s *supervisor) onlineCPUs(ctx context.Context) {
	for range 2 {
		time.Sleep(500 * time.Millisecond)
		if _, err := s.ssh(ctx, onlineCPUsScript); err != nil {
			s.logf("onlining the VM's new CPUs: %v", err)
		}
	}
}

const onlineCPUsScript = `for f in /sys/devices/system/cpu/cpu[0-9]*/online; do [ "$(cat "$f")" = 1 ] || echo 1 | sudo -n tee "$f" >/dev/null; done`

// status is the VM as the supervisor sees it.
func (s *supervisor) status(ctx context.Context) api.VMStatus {
	s.mu.Lock()
	c := s.c
	s.mu.Unlock()
	st := offStatus(c, s.l)
	running := s.m.running()
	if running {
		live := s.m.live(c)
		st.Live = &live
	}
	s.mu.Lock()
	st.State, st.Since = s.state, s.since
	st.PausedForDisk = s.pausedForDisk && s.state == api.VMPaused
	if s.sample != nil {
		st.Memory.Used = s.sample.Used()
	}
	requested := s.mem.Requested
	s.mu.Unlock()
	// What the machine says the guest has, which lags what was asked for
	// while it's given or gives back memory.
	st.Memory.Granted = requested
	if running {
		if granted := s.m.granted(ctx); granted > 0 {
			st.Memory.Granted = granted
		}
	}
	_, st.Memory.Resident = s.m.resident()
	return st
}

// offStatus is what there is to say about the VM without it running: its
// size, from its config and disks.
func offStatus(c Config, l Layout) api.VMStatus {
	st := api.VMStatus{
		Mode:   api.ModeVM,
		Driver: c.DriverName(),
		Name:   c.Name,
		State:  api.VMOff,
		CPUs:   c.CPUs,
		Memory: api.VMMemory{Min: c.MemoryMin, Cap: c.MemoryMin + hotplugSize(c)},
		Limits: &api.VMLimits{MinCPUs: 1, MaxCPUs: max(hostCPUs(), c.CPUs), MinMemory: c.MemoryMin, MaxMemory: max(hostMemory(), c.MemoryCap)},
	}
	st.Disk.Pool, st.Disk.Root = DiskImage(l.PoolDisk()), DiskImage(l.RootDisk())
	st.Disk.Add(st.Disk.Pool)
	st.Disk.Add(st.Disk.Root)
	// A disk only grows, and a VM that's off has the size it was given
	// (Config.Disk) when it next starts.
	st.Limits.MinDisk = max(st.Disk.Pool.Size, c.Disk)
	st.Limits.MaxDisk = max(api.VMMaxDisk, st.Limits.MinDisk)
	st.Disk.HostFree = HostFree(l.Dir())
	return st
}

func writeVMJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeVMError answers as the daemon does: api.Error.
func writeVMError(w http.ResponseWriter, status int, msg string) {
	writeVMJSON(w, status, api.Error{Error: msg})
}
