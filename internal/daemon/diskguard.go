package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/state"
)

// The disk guard (agent.DiskGuard) runs here, on its own loop, whether or not
// the app is open: the daemon measures the disks every few seconds, and does
// what the guard decides — warn, refuse, pause the agents writing the most,
// resume them.
//
// The disks it watches are every file system AgentBox writes to, each
// measured once however many of its directories it holds:
//
//   - the storage pool, as Incus reports it;
//   - in the VM, the host's disk that holds the VM's disk images
//     (hostos.VMDisks, seen through the shared home): they're sparse, so the
//     pool can say it has room the host doesn't, and this is what keeps the
//     pool from outgrowing what the host has free, less the floor;
//   - the worktrees, AgentBox's own data (state.db, media, logs), and on a
//     host of its own Incus's directory, where a pool made on a loop file
//     keeps its sparse image.
//
// Beneath it, in the VM, the supervisor on the host pauses the whole VM when
// the host's disk is down to its last api.VMDiskBackstop
// (hostvm/chv/diskbackstop.go), for when the daemon can't act.

// diskWatch is the guard's loop: its settings, the guard, and how it measures
// and acts, which a test replaces.
type diskWatch struct {
	every    time.Duration // between checks while every disk is fine; 0 for tests, which drive checkDisk
	lowEvery time.Duration // between checks while one is near or at its floor

	mu     sync.Mutex // held through a check, which may wait on Incus
	guard  *agent.DiskGuard
	loaded bool // the guard has been given the agents it paused before a restart

	// status is what the last check found, behind a lock of its own so a
	// refusal never waits on a check.
	statusMu sync.Mutex
	status   agent.DiskStatus

	measure func(ctx context.Context) []agent.DiskSpace
	writers func(ctx context.Context) ([]agent.DiskWriter, error)
	pause   func(ctx context.Context, ref string) error
	resume  func(ctx context.Context, ref string) error
}

func (s *Server) newDiskWatch() *diskWatch {
	return &diskWatch{
		every:    10 * time.Second,
		lowEvery: 2 * time.Second,
		guard:    agent.NewDiskGuard(nil),
		status:   agent.DiskStatus{Level: agent.DiskOK},
		measure:  s.measureDisks,
		writers:  func(ctx context.Context) ([]agent.DiskWriter, error) { return s.manager(nil).DiskWriters(ctx) },
		pause:    func(ctx context.Context, ref string) error { return s.diskAgentAction(ctx, ref, true) },
		resume:   func(ctx context.Context, ref string) error { return s.diskAgentAction(ctx, ref, false) },
	}
}

func (s *Server) diskAgentAction(ctx context.Context, ref string, pause bool) error {
	project, name, _ := strings.Cut(ref, "/")
	a, err := s.store.Agent(ctx, project, name)
	if err != nil {
		return err
	}
	if pause {
		return s.manager(nil).Pause(ctx, a)
	}
	return s.manager(nil).Resume(ctx, a)
}

// watchDisk checks the disks every diskWatch.every, and more often while one
// is near its floor, where a fast writer can cover the rest quickly.
func (s *Server) watchDisk(ctx context.Context) {
	w := s.disk
	if w.every <= 0 {
		return
	}
	for {
		s.checkDisk(ctx)
		wait := w.every
		if s.diskStatus().Level != agent.DiskOK {
			wait = w.lowEvery
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// checkDisk is one check: measure, decide, act, and say so when anything
// moved.
func (s *Server) checkDisk(ctx context.Context) {
	w := s.disk
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.loaded {
		w.guard = agent.NewDiskGuard(s.diskGuardPaused(ctx))
		w.loaded = true
	}
	spaces := w.measure(ctx)
	writers, err := w.writers(ctx)
	if err != nil {
		// Without the agents, the guard can't tell who it paused from who's
		// gone: it still warns and refuses, but pauses and resumes nobody.
		writers = nil
	} else if writers == nil {
		writers = []agent.DiskWriter{}
	}
	step := w.guard.Step(time.Now(), s.diskFloor(ctx), spaces, writers)
	if step.Pause != nil {
		ref := step.Pause.Ref
		if err := w.pause(ctx, ref); err != nil {
			s.logf("disk guard: pausing %s: %v", ref, err)
			w.guard.Unpause(ref)
		} else {
			s.logf("disk guard: paused %s, the agent writing the most: %s", ref, step.Status.Summary())
			project, _, _ := strings.Cut(ref, "/")
			s.diskTellLead(ctx, project, ref+" was paused by AgentBox's disk guard: "+step.Status.Summary()+". It is resumed by itself once there's room again; meanwhile new agents can't be created. Retiring agents that are done frees space.")
		}
	}
	for _, ref := range step.Resume {
		if err := w.resume(ctx, ref); err != nil {
			s.logf("disk guard: resuming %s: %v", ref, err)
			continue
		}
		s.logf("disk guard: resumed %s, there's room again", ref)
		project, _, _ := strings.Cut(ref, "/")
		s.diskTellLead(ctx, project, ref+" was resumed: the disk it was paused for has room again.")
	}
	status := w.guard.Status()
	if status.Level != agent.DiskOK {
		// The package caches give back what they hold beyond the floor.
		s.kickPackageCache()
	}
	changed := step.Changed || step.Pause != nil || len(step.Resume) > 0
	w.statusMu.Lock()
	prev := w.status
	w.status = status
	w.statusMu.Unlock()
	if !changed {
		return
	}
	if status.Level != prev.Level {
		switch status.Level {
		case agent.DiskOK:
			s.logf("disk guard: every disk has room again")
		case agent.DiskLow:
			s.logf("disk guard: nearing the floor: %s", status.Summary())
		case agent.DiskFull:
			s.logf("disk guard: at the floor, refusing new agents and image builds: %s", status.Summary())
		}
	}
	if !slices.Equal(status.Paused, prev.Paused) {
		s.saveDiskGuardPaused(ctx, status.Paused)
	}
	s.events.publish(api.EventDisk, toAPIDiskGuard(status))
	if step.Pause != nil || len(step.Resume) > 0 {
		s.refreshAgents(ctx)
	}
	if prev.Level == agent.DiskFull && status.Level != agent.DiskFull {
		// Queued agents waited for room.
		s.kickQueue()
	}
}

func (s *Server) diskTellLead(ctx context.Context, project, notice string) {
	if s.prLead != nil {
		// A notice in the chat, not a turn: the lead reads it with whatever it
		// does next, and nothing is spent on it now.
		s.prLead(ctx, project, notice, false)
	}
}

// diskStatus is what the guard last found.
func (s *Server) diskStatus() agent.DiskStatus {
	s.disk.statusMu.Lock()
	defer s.disk.statusMu.Unlock()
	return s.disk.status
}

// diskRefusal refuses what would write a lot while a disk is at its floor:
// nil when every disk has room.
func (s *Server) diskRefusal(what string) error {
	if err := s.diskStatus().Refusal(what); err != nil {
		return &diskFullError{err}
	}
	return nil
}

// diskPausedRefusal refuses to resume or start an agent the guard paused
// while the disk it paused it for is still at its floor: it would only go on
// filling it. The guard resumes it itself once there's room.
func (s *Server) diskPausedRefusal(ref string) error {
	status := s.diskStatus()
	if !slices.Contains(status.Paused, ref) {
		return nil
	}
	if err := status.Refusal("resuming " + ref); err != nil {
		return &diskFullError{fmt.Errorf("%w. AgentBox paused it for that, and resumes it by itself once there's room", err)}
	}
	return nil
}

// diskFullError is a refusal for want of disk space, which the API answers
// with 507 Insufficient Storage.
type diskFullError struct{ err error }

func (e *diskFullError) Error() string { return e.err.Error() }

func isDiskFull(err error) bool {
	var full *diskFullError
	return errors.As(err, &full)
}

// diskFloor is the floor the settings choose, agent.DefaultDiskFloor's where
// nobody chose.
func (s *Server) diskFloor(ctx context.Context) agent.DiskFloor {
	floor := agent.DefaultDiskFloor
	if v, err := s.store.Setting(ctx, state.SettingDiskFloorMin); err == nil && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			floor.Min = n
		}
	}
	if v, err := s.store.Setting(ctx, state.SettingDiskFloorPercent); err == nil && v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			floor.Percent = n
		}
	}
	return floor
}

// setDiskFloor stores the floor a settings request chooses: min 0 and a
// negative percent go back to the default's.
func (s *Server) setDiskFloor(ctx context.Context, minBytes *int64, percent *float64) error {
	floor := s.diskFloor(ctx)
	minValue, percentValue := "", ""
	if minBytes != nil {
		floor.Min = agent.DefaultDiskFloor.Min
		if *minBytes != 0 {
			floor.Min, minValue = *minBytes, strconv.FormatInt(*minBytes, 10)
		}
	} else if v, _ := s.store.Setting(ctx, state.SettingDiskFloorMin); v != "" {
		minValue = v
	}
	if percent != nil {
		floor.Percent = agent.DefaultDiskFloor.Percent
		if *percent >= 0 {
			floor.Percent, percentValue = *percent, strconv.FormatFloat(*percent, 'f', -1, 64)
		}
	} else if v, _ := s.store.Setting(ctx, state.SettingDiskFloorPercent); v != "" {
		percentValue = v
	}
	if err := floor.Validate(); err != nil {
		return err
	}
	if err := s.store.SetSetting(ctx, state.SettingDiskFloorMin, minValue); err != nil {
		return err
	}
	if err := s.store.SetSetting(ctx, state.SettingDiskFloorPercent, percentValue); err != nil {
		return err
	}
	// Taken at once, not at the next check: lowering the floor is how you get
	// going again.
	if s.disk.every > 0 {
		go s.checkDisk(s.background())
	}
	return nil
}

func (s *Server) diskGuardPaused(ctx context.Context) []string {
	v, err := s.store.Setting(ctx, state.SettingDiskGuardPaused)
	if err != nil || v == "" {
		return nil
	}
	var refs []string
	_ = json.Unmarshal([]byte(v), &refs)
	return refs
}

func (s *Server) saveDiskGuardPaused(ctx context.Context, refs []string) {
	b, _ := json.Marshal(refs)
	if err := s.store.SetSetting(ctx, state.SettingDiskGuardPaused, string(b)); err != nil {
		s.logf("disk guard: remembering the agents it paused: %v", err)
	}
}

// measureDisks measures every disk the guard watches, each file system once,
// its label naming everything of AgentBox's on it.
func (s *Server) measureDisks(ctx context.Context) []agent.DiskSpace {
	var spaces []agent.DiskSpace
	if used, total, err := s.manager(nil).Incus.PoolSpace(ctx, "default"); err == nil && total > 0 {
		spaces = append(spaces, agent.DiskSpace{Label: "Storage pool", Free: max(total-used, 0), Total: total})
	}
	type dir struct{ label, path string }
	var dirs []dir
	if disks := hostos.VMDisks(); disks != "" && onSharedFS(existingParent(disks)) {
		dirs = append(dirs, dir{"the VM's disk images", disks})
	} else if home := hostos.Home(); home != "" && onSharedFS(home) {
		// A front end from before VMDisksEnv: the disk images are in the
		// host's home, unless the host moved its data directory away.
		dirs = append(dirs, dir{"the VM's disk images", home})
	}
	dirs = append(dirs, dir{"worktrees", s.cfg.Paths.Worktrees()}, dir{"media", s.cfg.Paths.Media()}, dir{"AgentBox's data", s.cfg.Paths.Data})
	if _, err := os.Stat("/var/lib/incus"); err == nil && !hostos.InVM() {
		dirs = append(dirs, dir{"Incus", "/var/lib/incus"})
	}
	byID := map[string]int{}
	for _, d := range dirs {
		free, total, id, ok := diskSpace(existingParent(d.path))
		if !ok || total <= 0 {
			continue
		}
		if i, ok := byID[id]; ok {
			spaces[i].Label += ", " + d.label
			continue
		}
		byID[id] = len(spaces)
		spaces = append(spaces, agent.DiskSpace{Label: d.label, Path: filepath.Clean(d.path), Free: free, Total: total})
	}
	for i := range spaces {
		spaces[i].Label = capitalize(spaces[i].Label)
	}
	return spaces
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func toAPIDiskGuard(st agent.DiskStatus) api.DiskGuard {
	out := api.DiskGuard{Level: st.Level, Disks: []api.DiskGuardDisk{}, Paused: append([]string{}, st.Paused...), Since: st.Since}
	if out.Level == "" {
		out.Level = api.DiskOK
	}
	for _, d := range st.Disks {
		out.Disks = append(out.Disks, api.DiskGuardDisk{Label: d.Label, Path: d.Path, Free: d.Free, Total: d.Total, Floor: d.Floor, Level: d.Level})
	}
	out.Message = st.Summary()
	return out
}

// getDiskGuard is GET /v1/disk.
func (s *Server) getDiskGuard(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, toAPIDiskGuard(s.diskStatus()))
}
