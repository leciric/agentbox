package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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
	// sending holds one usage report at a time (sendUsage): two checks at
	// once would both read the days not sent yet, and send them twice.
	sending sync.Mutex
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
	channel, err := s.updateChannel(ctx)
	if err != nil {
		return
	}
	stable, err := update.Check(ctx, s.cfg.UpdateURL, update.NewRequest(install, Version))
	// agentbox.linting.dev's link is to the release on GitHub, which the
	// private repository closes to users: the bucket's page for the same
	// version replaces it.
	if err == nil && stable.Version != "" {
		stable.URL = update.ReleasePage(s.cfg.ReleasesURL, "v"+strings.TrimPrefix(stable.Version, "v"))
	}
	// The usage stats go with the check, whatever it found (usagestats.go).
	s.sendUsage(ctx, install)
	// The nightly channel asks the release list for the nightlies too, since
	// agentbox.linting.dev only answers with stable releases. Either answer
	// alone is still worth offering.
	var nightly update.Latest
	var nightlyErr error
	if channel == update.ChannelNightly {
		nightly, nightlyErr = update.LatestRelease(ctx, s.cfg.ReleasesURL, update.ChannelNightly)
	}
	if err != nil && (channel != update.ChannelNightly || nightlyErr != nil) {
		return
	}
	// Turned off, or switched to another channel, while the requests were
	// out: what they found isn't shown.
	if on, err := s.updateCheckOn(ctx); err != nil || !on {
		return
	}
	if now, err := s.updateChannel(ctx); err != nil || now != channel {
		return
	}
	now := time.Now()
	var available *api.UpdateAvailable
	if offer, ok := update.Offer(channel, Version, stable, nightly); ok {
		available = &api.UpdateAvailable{Version: offer.Version, URL: offer.URL}
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
// check found, since nothing will keep it true, and the usage stats not sent
// yet, which went with it; turning it on checks at once.
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
		if err := s.store.ForgetFeatureUsage(ctx, ""); err != nil {
			return err
		}
		s.updates.mu.Lock()
		s.updates.available, s.updates.checkedAt = nil, nil
		s.updates.mu.Unlock()
	}
	s.publishUpdate(ctx)
	return nil
}

// updateChannel is the channel the check follows: the setting, or the one this
// build came from when nobody chose.
func (s *Server) updateChannel(ctx context.Context) (string, error) {
	c, err := s.store.Setting(ctx, state.SettingUpdateChannel)
	if err != nil {
		return "", err
	}
	if !update.ValidChannel(c) {
		c = update.DefaultChannel(Version)
	}
	return c, nil
}

// setUpdateChannel is the channel changing: what the last check found was for
// the other one, so it is forgotten, and a check goes out at once.
func (s *Server) setUpdateChannel(ctx context.Context, channel string) error {
	if !update.ValidChannel(channel) {
		return fmt.Errorf("updateChannel must be %q or %q", update.ChannelStable, update.ChannelNightly)
	}
	if err := s.store.SetSetting(ctx, state.SettingUpdateChannel, channel); err != nil {
		return err
	}
	s.updates.mu.Lock()
	s.updates.available, s.updates.checkedAt = nil, nil
	s.updates.mu.Unlock()
	select {
	case s.updates.now <- struct{}{}:
	default:
	}
	s.publishUpdate(ctx)
	return nil
}

func (s *Server) updateStatus(ctx context.Context) (api.UpdateStatus, error) {
	enabled, err := s.store.FlagOn(ctx, state.SettingUpdateCheck)
	if err != nil {
		return api.UpdateStatus{}, err
	}
	channel, err := s.updateChannel(ctx)
	if err != nil {
		return api.UpdateStatus{}, err
	}
	out := api.UpdateStatus{Current: Version, Enabled: enabled, Channel: channel, Nightly: update.IsNightly(Version), Blocked: update.Blocked(Version)}
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

// getLatestRelease is where "Update available" leads: the chosen channel's
// latest release as the release list has it at the moment it is clicked — on the
// stable channel the newest release that isn't a prerelease. The link used to
// be whatever the last daily check found, pinned to that version's page, and
// with releases coming out several a day an app that checked in the morning
// sent its user to a release two behind the latest all day. When the list can't
// be reached, the last check's find is still better than nothing. A newer
// release found here also becomes what the sidebar offers. The answer lists
// the release's files, from which the app updates itself in place
// (desktop/src/main/appupdate.ts); without them, it opens the page.
func (s *Server) getLatestRelease(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	channel, err := s.updateChannel(ctx)
	if err != nil {
		return err
	}
	latest, err := update.LatestRelease(ctx, s.cfg.ReleasesURL, channel)
	if err != nil {
		s.updates.mu.Lock()
		last := s.updates.available
		s.updates.mu.Unlock()
		if last == nil {
			return fmt.Errorf("finding the latest release: %w", err)
		}
		return writeJSON(w, http.StatusOK, api.UpdateRelease{Version: last.Version, URL: last.URL})
	}
	if on, err := s.updateCheckOn(ctx); err == nil && on {
		stable, nightly := latest, update.Latest{}
		if channel == update.ChannelNightly {
			nightly = latest
		}
		if offer, ok := update.Offer(channel, Version, stable, nightly); ok {
			available := &api.UpdateAvailable{Version: offer.Version, URL: offer.URL}
			s.updates.mu.Lock()
			changed := !sameUpdate(s.updates.available, available)
			s.updates.available = available
			s.updates.mu.Unlock()
			if changed {
				s.publishUpdate(ctx)
			}
		}
	}
	out := api.UpdateRelease{Version: latest.Version, URL: latest.URL}
	for _, a := range latest.Assets {
		out.Assets = append(out.Assets, api.ReleaseAsset(a))
	}
	return writeJSON(w, http.StatusOK, out)
}
