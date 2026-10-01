package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/datamove"
	"agentbox/internal/hostos"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// Incus can die in a way that leaves it looking alive: in AgentBox's VM, on
// 2026-09-30, incusd was killed a second after it started, while
// incus.service's ExecStartPost (`incusd waitready --timeout=600`) still ran.
// systemd waits for that before it fails the unit, so for ten minutes the
// unit was "activating", Restart=on-failure did nothing, and systemd went on
// holding Incus's socket: every connection was accepted and none answered.
// The daemon's first call got EOF, and every later one waited on it, the
// startup's among them, before the daemon served its socket.
//
// So the daemon watches Incus. It asks it for /1.0 every few seconds, with a
// deadline. While a call hangs, every Incus call fails at once, with why
// (incus.Health), so no request waits on it; in the VM, where the
// daemon may use sudo, it restarts incus.service once incusd is gone, or has
// hung for minutes; and each time Incus answers again, it plugs in the agents'
// in-agent API sockets, which a boot with Incus down would have left out.
// The VM's own drop-in for incus.service (chv's user-data) makes systemd
// restart a dead incusd by itself; this is for everything else.

// incusWatch is the watch's settings and what it last found.
type incusWatch struct {
	every        time.Duration // how often Incus is asked; 0 for tests, which drive checkIncus
	probeTimeout time.Duration // how long it has to answer
	// deadGrace is how long incusd may be gone before the daemon restarts
	// incus.service, which gives systemd's own restart the first go.
	deadGrace time.Duration
	// hungGrace is how long a running incusd may not answer before it is
	// restarted anyway: a start after an unclean shutdown can be slow.
	hungGrace    time.Duration
	restartEvery time.Duration // the least time between two restarts
	socketTries  int           // how many passes plugging in the agents' sockets may fail

	// usable reports whether Incus is there for this daemon to use at all:
	// one that isn't installed, or whose socket this user can't open, is host
	// setup's to fix, and Setup's to say.
	usable func() bool
	// restart restarts incus.service, or is nil where the daemon can't.
	restart func(context.Context) error
	unit    func(context.Context) (incusUnit, error)
	// plug plugs in the agents' sockets, and reports whether it did for every
	// running agent.
	plug func(context.Context) bool

	mu          sync.Mutex
	status      api.IncusStatus
	lastRestart time.Time // the watch's own, like restartErr
	restartErr  string
	socketsDue  bool // plug the sockets in once Incus answers
	socketFails int
	plugging    bool
}

func (s *Server) newIncusWatch() *incusWatch {
	w := &incusWatch{
		every: 5 * time.Second, probeTimeout: 5 * time.Second,
		deadGrace: 15 * time.Second, hungGrace: 3 * time.Minute, restartEvery: 2 * time.Minute,
		socketTries: 5,
		usable:      s.cfg.Incus.Reachable,
		unit:        readIncusUnit,
		plug:        s.plugAgentSockets,
		status:      api.IncusStatus{Answering: true},
		socketsDue:  true,
	}
	if hostos.InVM() {
		w.restart = restartIncusUnit
	}
	return w
}

// watchIncus asks Incus every w.every until ctx ends.
func (s *Server) watchIncus(ctx context.Context) {
	var plugs sync.WaitGroup
	defer plugs.Wait()
	if s.incus.every == 0 {
		return // tests drive it
	}
	ticker := time.NewTicker(s.incus.every)
	defer ticker.Stop()
	for {
		if s.checkIncus(ctx) {
			plugs.Go(func() { s.plugSockets(ctx) })
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// checkIncus asks Incus once and acts on the answer. It reports whether the
// agents' sockets are due to be plugged in now, which the caller does
// (plugSockets): that waits for agents to boot, and the watch mustn't.
func (s *Server) checkIncus(ctx context.Context) (plug bool) {
	w := s.incus
	probe, cancel := context.WithTimeout(ctx, w.probeTimeout)
	err := s.cfg.Incus.Probe(probe)
	hung := err != nil && probe.Err() != nil
	cancel()
	if ctx.Err() != nil {
		return false
	}
	now := time.Now()
	if err == nil {
		s.cfg.Incus.Health.SetNotAnswering("")
		w.mu.Lock()
		defer w.mu.Unlock()
		if !w.status.Answering {
			s.logf("Incus answers again, after %s", now.Sub(*w.status.Since).Round(time.Second))
			w.status = api.IncusStatus{Answering: true, Restarted: w.status.Restarted}
			s.events.publish(api.EventIncus, w.status)
		}
		if w.socketsDue && !w.plugging {
			w.plugging = true
			return true
		}
		return false
	}
	if errors.Is(err, exec.ErrNotFound) || !w.usable() {
		return false
	}

	w.mu.Lock()
	if w.status.Answering {
		w.status = api.IncusStatus{Since: &now, Restarted: w.status.Restarted}
		w.socketsDue, w.socketFails = true, 0
		s.logf("Incus isn't answering: %v", err)
	}
	since, restarted := *w.status.Since, w.status.Restarted
	w.mu.Unlock()

	// Only this goroutine restarts, so only status needs the lock, and asking
	// systemd doesn't hold it.
	down := now.Sub(since)
	detail := fmt.Sprintf("Incus hasn't answered AgentBox since %s: %s", since.Format(time.TimeOnly), firstLine(err))
	if w.restart != nil && now.Sub(w.lastRestart) >= w.restartEvery {
		u, uerr := w.unit(ctx)
		if gone := uerr == nil && u.MainPID == 0; (gone && down >= w.deadGrace) || down >= w.hungGrace {
			w.lastRestart, restarted = now, &now
			why := "incusd isn't running"
			if !gone {
				why = "incusd hasn't answered for " + down.Round(time.Second).String()
			}
			if uerr == nil {
				why += " (incus.service is " + u.State + ")"
			}
			s.logf("restarting incus.service: %s", why)
			if err := w.restart(ctx); err != nil {
				s.logf("restarting incus.service: %v", err)
				w.restartErr = firstLine(err)
			} else {
				w.restartErr = ""
			}
		}
	}
	switch {
	case restarted != nil && !restarted.Before(since) && w.restartErr != "":
		detail += "; restarting incus.service failed: " + w.restartErr
	case restarted != nil && !restarted.Before(since):
		detail += "; AgentBox restarted incus.service at " + restarted.Format(time.TimeOnly)
	case w.restart == nil:
		detail += ". Restart it with: sudo systemctl restart incus.service"
	}
	if hung {
		// Everything else would wait on it too: fail it at once instead.
		s.cfg.Incus.Health.SetNotAnswering(detail)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status.Restarted = restarted
	if w.status.Detail != detail {
		w.status.Detail = detail
		s.events.publish(api.EventIncus, w.status)
	}
	return false
}

// plugSockets runs w.plug, and counts a pass that failed: it is tried again
// at the next answer, a few times.
func (s *Server) plugSockets(ctx context.Context) {
	w := s.incus
	ok := w.plug(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.plugging = false
	if ok {
		w.socketsDue = false
		return
	}
	if w.socketFails++; w.socketFails >= w.socketTries {
		s.logf("giving up on the agents' in-agent API sockets after %d tries", w.socketFails)
		w.socketsDue = false
	}
}

// incusStatus is what the watch last found.
func (s *Server) incusStatus() api.IncusStatus {
	s.incus.mu.Lock()
	defer s.incus.mu.Unlock()
	return s.incus.status
}

// plugAgentSockets gives every ready agent its in-agent API, and a running
// one its socket back if its /run hides it (RestoreAgentAPISocket). It reports
// whether that worked for every running agent: one that isn't running gets
// its device, which is all it can have until it starts.
func (s *Server) plugAgentSockets(ctx context.Context) bool {
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		return false
	}
	m := s.manager(s.cfg.Log)
	// A machine that couldn't be moved this time is tried again with the
	// others' sockets, which don't wait for it.
	moved := s.finishDataMove(ctx, m, agents)
	var ready []state.Agent
	for _, a := range agents {
		if !a.IsLead() && a.Status == state.AgentReady {
			ready = append(ready, a)
		}
	}
	if len(ready) == 0 {
		return moved
	}
	// Which are running: without it, each still gets its device, but none is
	// asked for its socket, and the pass is to be tried again.
	ok := true
	running := map[string]bool{}
	if instances, err := s.cfg.Incus.Instances(ctx); err != nil {
		s.logf("agents' sockets: %v", err)
		ok = false
	} else {
		for _, inst := range instances {
			running[inst.Name] = inst.Status == "Running"
		}
	}
	for _, a := range ready {
		if err := m.EnsureAgentAPI(ctx, a); err != nil && !errors.Is(err, incus.ErrNotFound) {
			s.logf("in-agent API for %s: %v", a.Ref(), err)
			ok = ok && !running[a.Instance]
		}
	}
	// Each once it has booted, all at once, since they boot at once.
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, a := range ready {
		if !running[a.Instance] {
			continue
		}
		wg.Go(func() {
			if err := m.RestoreAgentAPISocket(ctx, a); err != nil && ctx.Err() == nil {
				s.logf("in-agent API socket for %s: %v", a.Ref(), err)
				mu.Lock()
				ok = false
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return ok && moved
}

// finishDataMove points every agent's machine at where AgentBox's data moved,
// when it has just moved (package datamove), before anything plugs their
// sockets in: their devices name the old paths. It reports whether it is
// done; the marker the move left goes once it is, so it happens once.
func (s *Server) finishDataMove(ctx context.Context, m *agent.Manager, agents []state.Agent) bool {
	k, ok := datamove.ReadMarker(s.cfg.Paths.Data)
	if !ok {
		return true
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	done := true
	for _, a := range agents {
		if a.IsLead() {
			continue
		}
		wg.Go(func() {
			if err := m.MoveDevices(ctx, a, k); err != nil {
				s.logf("moving %s to %s: %v", a.Ref(), k.To, err)
				mu.Lock()
				done = false
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if done {
		if err := datamove.ClearMarker(s.cfg.Paths.Data); err != nil {
			s.logf("data move: %v", err)
		}
		s.logf("moved the agents' machines to %s", k.To)
	}
	return done
}

// incusUnit is what systemd says of incus.service.
type incusUnit struct {
	State   string // ActiveState/SubState: activating/start-post
	MainPID int    // 0 when incusd isn't running
}

func readIncusUnit(ctx context.Context) (incusUnit, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", "-p", "ActiveState,SubState,MainPID", "incus.service").Output()
	if err != nil {
		return incusUnit{}, err
	}
	return parseIncusUnit(string(out))
}

func parseIncusUnit(out string) (incusUnit, error) {
	var u incusUnit
	props := map[string]string{}
	for line := range strings.Lines(out) {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	pid, err := strconv.Atoi(props["MainPID"])
	if err != nil || props["ActiveState"] == "" {
		return u, fmt.Errorf("systemctl show incus.service said %q", strings.TrimSpace(out))
	}
	u.MainPID, u.State = pid, props["ActiveState"]+"/"+props["SubState"]
	return u, nil
}

// restartIncusUnit restarts incus.service without waiting for it to start,
// which may take minutes: the watch sees it answer. A unit that failed too
// often in a row has hit systemd's start limit, which reset-failed clears.
func restartIncusUnit(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "sudo", "-n", "systemctl", "reset-failed", "incus.service").Run()
	out, err := exec.CommandContext(ctx, "sudo", "-n", "systemctl", "--no-block", "restart", "incus.service").CombinedOutput()
	if err != nil {
		return fmt.Errorf("sudo systemctl restart incus.service: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
