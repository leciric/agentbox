package chv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
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
	if err := s.ch.Pause(ctx); err != nil {
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
	if err := s.ch.Resume(ctx); err != nil {
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

// status is the VM as the supervisor sees it.
func (s *supervisor) status(ctx context.Context) api.VMStatus {
	st := offStatus(s.c, s.l)
	s.mu.Lock()
	st.State, st.Since = s.state, s.since
	if s.sample != nil {
		st.Memory.Used = s.sample.Used()
	}
	requested := s.mem.Requested
	s.mu.Unlock()
	// What's plugged, which lags what was asked for while the guest plugs
	// or unplugs it.
	st.Memory.Granted = requested
	if s.cloud != nil && !s.cloud.exited() {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		if info, err := s.ch.Info(ctx); err == nil && info.MemoryActualSize > 0 {
			st.Memory.Granted = info.MemoryActualSize
		}
		cancel()
	}
	for _, c := range []*child{s.cloud, s.passt, s.fs} {
		if c != nil && !c.exited() {
			st.Memory.Resident += resident(c.pid())
		}
	}
	return st
}

// offStatus is what there is to say about the VM without it running: its
// size, from its config and disks.
func offStatus(c Config, l Layout) api.VMStatus {
	st := api.VMStatus{
		Mode:   api.ModeVM,
		Driver: api.VMDriverCloudHypervisor,
		Name:   c.Name,
		State:  api.VMOff,
		CPUs:   c.CPUs,
		Memory: api.VMMemory{Min: c.MemoryMin, Cap: c.MemoryMin + hotplugSize(c)},
	}
	for _, disk := range []string{l.RootDisk(), l.PoolDisk()} {
		if fi, err := os.Stat(disk); err == nil {
			st.Disk.Size += fi.Size()
			st.Disk.Used += allocated(fi)
		}
	}
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
