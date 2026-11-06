package daemon

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
)

// Presence is what the clients say someone is looking at (api.PresenceRequest),
// held as leases that lapse unless renewed. The daemon spends its background
// work by it, borrowed from t3code's BackgroundPolicy:
//
//   - an agent's live resources are sampled only while a lease asks for them
//     (agentresources.go), and its history kept in memory, never stored;
//   - the housekeeping it polls for backs off to its idle interval for a
//     project nobody views, and for everything while the computer is on
//     battery or hot: the pull request watch (prwatch.go), the base syncs
//     (basesync.go) and the usage events the app's meters move on (watch.go).
//
// Backing off never stops anything: a pull request that conflicts, fails its
// checks or is asked for changes is still seen and told, only later. A project
// that comes into view is looked at again soon, so what it shows is fresh.

const (
	// presenceTTL is how long a lease lasts unrenewed: a client renews at a
	// third of it, so two renewals can go missing before it lapses.
	presenceTTL = 45 * time.Second
	// maxPresenceClient is the longest a client's name may be, and
	// maxPresenceViews how many views one lease may hold.
	maxPresenceClient = 200
	maxPresenceViews  = 64
)

type presenceLease struct {
	api.PresenceLease
}

// viewLeases is the live leases.
type viewLeases struct {
	now func() time.Time
	ttl time.Duration

	mu     sync.Mutex
	leases map[string]presenceLease
}

func newViewLeases() *viewLeases {
	return &viewLeases{now: time.Now, ttl: presenceTTL, leases: map[string]presenceLease{}}
}

// live is the leases that haven't lapsed, dropping those that have; p.mu held.
func (p *viewLeases) live() map[string]presenceLease {
	now := p.now()
	maps.DeleteFunc(p.leases, func(_ string, l presenceLease) bool { return !now.Before(l.Expires) })
	return p.leases
}

// renew takes or renews a lease, and says which projects came into view with
// it: none of the live leases viewed them before.
func (p *viewLeases) renew(req api.PresenceRequest) (cameIntoView []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	before := viewedProjects(p.live())
	p.leases[req.Client] = presenceLease{api.PresenceLease{
		Client: req.Client, Kind: req.Kind, Views: slices.Clone(req.Views), Power: req.Power,
		Expires: p.now().Add(p.ttl),
	}}
	for project := range viewedProjects(p.leases) {
		if !before[project] {
			cameIntoView = append(cameIntoView, project)
		}
	}
	slices.Sort(cameIntoView)
	return cameIntoView
}

func (p *viewLeases) end(client string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.leases, client)
}

func viewedProjects(leases map[string]presenceLease) map[string]bool {
	out := map[string]bool{}
	for _, l := range leases {
		for _, v := range l.Views {
			out[v.Project] = true
		}
	}
	return out
}

// power is the computer's power as the leases report it: saving if any says
// so. Only the desktop app reports it, for the computer the daemon runs on (or
// its VM's host).
func (p *viewLeases) power() api.HostPower {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.powerLocked()
}

func (p *viewLeases) powerLocked() api.HostPower {
	var out api.HostPower
	for _, l := range p.live() {
		if l.Power == nil {
			continue
		}
		out.OnBattery = out.OnBattery || l.Power.OnBattery
		if thermalRank(l.Power.Thermal) > thermalRank(out.Thermal) {
			out.Thermal = l.Power.Thermal
		}
	}
	return out
}

func thermalRank(s string) int {
	return slices.Index([]string{"nominal", "fair", "serious", "critical"}, s)
}

// idle is whether a project's housekeeping backs off: no live lease views it,
// or the computer is saving power.
func (p *viewLeases) idle(project string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.powerLocked().Saving() || !viewedProjects(p.live())[project]
}

// away is whether everything backs off: no live lease shows anything, or the
// computer is saving power.
func (p *viewLeases) away() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.awayLocked()
}

func (p *viewLeases) awayLocked() bool {
	return p.powerLocked().Saving() || len(viewedProjects(p.live())) == 0
}

// sampling is the agents whose resources a live lease asks for, by ref.
func (p *viewLeases) sampling() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.samplingLocked()
}

func (p *viewLeases) samplingLocked() []string {
	set := map[string]bool{}
	for _, l := range p.live() {
		for _, v := range l.Views {
			if v.Resources && v.Agent != "" {
				set[v.Agent] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

func (p *viewLeases) state() api.PresenceState {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := api.PresenceState{Leases: []api.PresenceLease{}, Idle: p.awayLocked(), Power: p.powerLocked(), Sampling: p.samplingLocked()}
	for _, l := range p.live() {
		out.Leases = append(out.Leases, l.PresenceLease)
	}
	slices.SortFunc(out.Leases, func(a, b api.PresenceLease) int { return strings.Compare(a.Client, b.Client) })
	return out
}

// renewPresence is PUT /v1/presence.
func (s *Server) renewPresence(w http.ResponseWriter, r *http.Request) error {
	var req api.PresenceRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	req.Client = strings.TrimSpace(req.Client)
	if req.Client == "" || len(req.Client) > maxPresenceClient {
		return fmt.Errorf("a lease needs a client name of 1 to %d characters", maxPresenceClient)
	}
	if len(req.Views) > maxPresenceViews {
		return fmt.Errorf("a lease holds at most %d views", maxPresenceViews)
	}
	for _, v := range req.Views {
		if v.Project == "" {
			return fmt.Errorf("every view names its project")
		}
	}
	for _, project := range s.presence.renew(req) {
		// Looked at again soon, so the view shows what is true now rather
		// than what was read before it backed off.
		s.prWatch.wake(project)
	}
	return writeJSON(w, http.StatusOK, api.PresenceResponse{TTLSeconds: int(s.presence.ttl / time.Second), Idle: s.presence.away()})
}

// endPresence is DELETE /v1/presence/{client}.
func (s *Server) endPresence(w http.ResponseWriter, r *http.Request) error {
	s.presence.end(r.PathValue("client"))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// getPresence is GET /v1/presence.
func (s *Server) getPresence(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, s.presence.state())
}
