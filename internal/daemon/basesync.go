package daemon

import (
	"context"
	"strings"
	"time"

	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// baseSyncInterval is how often the daemon fetches each project's remote and
// fast-forwards its base branch (state.Project.BaseSyncOff). A new agent
// doesn't wait for it: creating one syncs first too.
const baseSyncInterval = 5 * time.Minute

// syncBases keeps every project's base branch up to date with its remote,
// once at startup and then every baseSyncInterval.
func (s *Server) syncBases(ctx context.Context) {
	ticker := time.NewTicker(baseSyncInterval)
	defer ticker.Stop()
	for {
		projects, err := s.store.Projects(ctx)
		if err != nil && ctx.Err() == nil {
			s.logf("base sync: %v", err)
		}
		for _, p := range projects {
			if ctx.Err() != nil {
				return
			}
			if !p.BaseSyncOff {
				s.syncBase(ctx, p)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// syncBase does it for one project. A failure is logged when it first
// happens and when it changes, not every few minutes while a remote stays
// out of reach.
func (s *Server) syncBase(ctx context.Context, p state.Project) {
	repo, err := gitrepo.Open(p.Root)
	if err != nil {
		return
	}
	synced, err := repo.SyncBase(ctx)
	if ctx.Err() != nil {
		return
	}
	var why string
	if err != nil {
		why = err.Error()
	}
	s.mu.Lock()
	logIt := s.baseSyncErrs[p.Name] != why
	if why == "" {
		delete(s.baseSyncErrs, p.Name)
	} else {
		s.baseSyncErrs[p.Name] = why
	}
	s.mu.Unlock()
	up := strings.TrimPrefix(synced.Upstream.Ref, "refs/remotes/")
	switch {
	case err != nil && logIt:
		s.logf("%s: couldn't bring %s up to date with %s: %v", p.Name, synced.Branch, up, err)
	case err == nil && logIt:
		s.logf("%s: %s syncs with %s again", p.Name, synced.Branch, up)
	case synced.Moved:
		s.logf("%s: fast-forwarded %s to %s", p.Name, synced.Branch, up)
	}
}
