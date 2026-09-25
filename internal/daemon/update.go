package daemon

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/update"
)

// updates is what the daily update check last found. It is kept in memory
// only: the daemon asks again as it starts, so there is nothing to carry over.
type updates struct {
	mu        sync.Mutex
	available *api.UpdateAvailable
	checkedAt *time.Time
	now       chan struct{} // pokes the loop to check at once, when the setting is turned on
}

// watchUpdates checks for a newer AgentBox as the daemon starts and every
// update.Interval after, for as long as the check is allowed. Every failure is
// dropped without a word: a machine offline, or a server that is down, is no
// reason to tell anyone anything.
func (s *Server) watchUpdates(ctx context.Context) {
	tick := time.NewTicker(update.Interval)
	defer tick.Stop()
	for {
		s.checkForUpdate(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.updates.now:
		}
	}
}

func (s *Server) checkForUpdate(ctx context.Context) {
	if on, err := s.updateCheckOn(ctx); err != nil || !on {
		return
	}
	install, err := s.installID(ctx)
	if err != nil {
		return
	}
	latest, err := update.Check(ctx, s.cfg.UpdateURL, update.NewRequest(install, Version))
	if err != nil {
		return
	}
	// Turned off while the request was out: what it found isn't shown.
	if on, err := s.updateCheckOn(ctx); err != nil || !on {
		return
	}
	now := time.Now()
	var available *api.UpdateAvailable
	if update.Newer(latest.Version, Version) {
		available = &api.UpdateAvailable{Version: latest.Version, URL: latest.URL}
	}
	s.updates.mu.Lock()
	changed := !sameUpdate(s.updates.available, available)
	s.updates.available, s.updates.checkedAt = available, &now
	s.updates.mu.Unlock()
	if changed {
		if available != nil {
			s.logf("AgentBox %s is out (this is %s): %s", available.Version, Version, available.URL)
		}
		s.publishUpdate(ctx)
	}
}

func sameUpdate(a, b *api.UpdateAvailable) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// updateCheckOn is whether a check may go out now: nothing in the way, and the
// setting on.
func (s *Server) updateCheckOn(ctx context.Context) (bool, error) {
	if update.Blocked(Version) != "" {
		return false, nil
	}
	return s.store.FlagOn(ctx, state.SettingUpdateCheck)
}

// installID is the random UUID the check sends, made the first time one is
// needed and kept from then on.
func (s *Server) installID(ctx context.Context) (string, error) {
	id, err := s.store.Setting(ctx, state.SettingInstallID)
	if err != nil || id != "" {
		return id, err
	}
	id = uuid.NewString()
	return id, s.store.SetSetting(ctx, state.SettingInstallID, id)
}

// setUpdateCheck is the setting changing. Turning it off forgets what the last
// check found, since nothing will keep it true; turning it on checks at once.
func (s *Server) setUpdateCheck(ctx context.Context, on bool) error {
	if err := s.store.SetFlag(ctx, state.SettingUpdateCheck, on); err != nil {
		return err
	}
	if on {
		select {
		case s.updates.now <- struct{}{}:
		default:
		}
	} else {
		s.updates.mu.Lock()
		s.updates.available, s.updates.checkedAt = nil, nil
		s.updates.mu.Unlock()
	}
	s.publishUpdate(ctx)
	return nil
}

func (s *Server) updateStatus(ctx context.Context) (api.UpdateStatus, error) {
	enabled, err := s.store.FlagOn(ctx, state.SettingUpdateCheck)
	if err != nil {
		return api.UpdateStatus{}, err
	}
	out := api.UpdateStatus{Current: Version, Enabled: enabled, Blocked: update.Blocked(Version)}
	s.updates.mu.Lock()
	out.Available, out.CheckedAt = s.updates.available, s.updates.checkedAt
	s.updates.mu.Unlock()
	return out, nil
}

func (s *Server) publishUpdate(ctx context.Context) {
	if status, err := s.updateStatus(ctx); err == nil {
		s.events.publish(api.EventUpdate, status)
	}
}

func (s *Server) getUpdate(w http.ResponseWriter, r *http.Request) error {
	status, err := s.updateStatus(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, status)
}
