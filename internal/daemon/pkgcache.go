package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/pkgcache"
	"agentbox/internal/state"
)

// The shared package caches: one directory in AgentBox's VM that every
// agent's pnpm, npm, Go, pip, uv, Corepack and Playwright download into
// (internal/pkgcache, agent/pkgcache.go). Agents write in it themselves, so
// the daemon measures it every packageCacheEvery, and sooner when the disk
// guard finds a disk near its floor, and evicts what was used longest ago
// beyond SettingPackageCacheMax (10 GiB unless chosen) or below the floor, and
// not while a heavy run is under way, unless the caches are far over the cap.

// packageCacheEvery is how often the caches are measured and trimmed.
const packageCacheEvery = 10 * time.Minute

func (s *Server) newPackageCache() *pkgcache.Cache {
	c := &pkgcache.Cache{
		Dir: s.cfg.Paths.PackageCache(),
		Max: func() int64 {
			ctx := context.Background()
			on, chosen, err := s.store.PackageCache(ctx)
			if err != nil || !on {
				return 0
			}
			maxBytes, _ := s.cacheMax(ctx, s.cfg.Paths.PackageCache(), chosen, state.DefaultPackageCacheMax)
			return maxBytes
		},
		Room: func() int64 {
			free, total, _, ok := diskSpace(existingParent(s.cfg.Paths.PackageCache()))
			if !ok {
				return 0
			}
			return free - s.diskFloor(context.Background()).For(total)
		},
		// A heavy run (an install, a build, tests) may be reading and
		// writing the caches: not the time to evict from them.
		Busy: s.heavyRunActive,
		Logf: s.logf,
	}
	// Agents write in it as their user, which a daemon running as root
	// isn't.
	if os.Geteuid() == 0 && s.cfg.User.UID != 0 {
		c.Owner = &[2]int{s.cfg.User.UID, s.cfg.User.GID}
	}
	return c
}

// heavyRunActive says an agent has a heavy command running or waiting to
// run (pressure.go).
func (s *Server) heavyRunActive() bool {
	s.heavy.mu.Lock()
	defer s.heavy.mu.Unlock()
	return len(s.heavy.g.Running())+len(s.heavy.g.Waiting()) > 0
}

// diagnosticsPackageCache is what the shared package caches hold, by tool.
func (s *Server) diagnosticsPackageCache(w http.ResponseWriter, r *http.Request) error {
	on, chosen, err := s.store.PackageCache(r.Context())
	if err != nil {
		return err
	}
	max, _ := s.cacheMax(r.Context(), s.cfg.Paths.PackageCache(), chosen, state.DefaultPackageCacheMax)
	out := api.DiagnosticsPackageCache{On: on, Bytes: s.packageCache.Size(), MaxBytes: max, Parts: []api.PackageCachePart{}}
	for _, p := range s.packageCache.Breakdown() {
		name := p.Dir
		if name == "" {
			name = "other"
		}
		out.Parts = append(out.Parts, api.PackageCachePart{Name: name, Bytes: p.Bytes})
	}
	return writeJSON(w, http.StatusOK, out)
}

// packageCacheDir is the directory agents mount, or "" while the caches are
// off or it can't be made.
func (s *Server) packageCacheDir(ctx context.Context) string {
	on, _, err := s.store.PackageCache(ctx)
	if err != nil || !on {
		return ""
	}
	if err := s.packageCache.Prepare(); err != nil {
		s.logf("package cache: %v", err)
		return ""
	}
	// Where it really is: in a VM, a symlink to the agents' disk
	// (moveDataToPool). Agents mounted the old directory get the new one.
	if real, err := filepath.EvalSymlinks(s.packageCache.Dir); err == nil {
		return real
	}
	return s.packageCache.Dir
}

// watchPackageCache trims the caches every packageCacheEvery, and whenever
// kickPackageCache asks, until ctx ends.
func (s *Server) watchPackageCache(ctx context.Context) {
	for {
		s.packageCache.Trim()
		s.trimNodeModules(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(packageCacheEvery):
		case <-s.packageCacheKick:
		}
	}
}

// kickPackageCache has the caches trimmed now, without waiting for it: when
// a disk nears its floor, or the cap was lowered.
func (s *Server) kickPackageCache() {
	select {
	case s.packageCacheKick <- struct{}{}:
	default:
	}
}

// applyPackageCache mounts the caches in every running agent, or takes them
// out, after the setting changed. A stopped agent is set as it starts.
func (s *Server) applyPackageCache(ctx context.Context) {
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		s.logf("package cache: %v", err)
		return
	}
	instances, err := s.cfg.Incus.Instances(ctx)
	if err != nil {
		s.logf("package cache: %v", err)
		return
	}
	running := map[string]bool{}
	for _, inst := range instances {
		running[inst.Name] = inst.Status == "Running"
	}
	m := s.manager(s.cfg.Log)
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady || !running[a.Instance] {
			continue
		}
		m.EnsurePackageCache(ctx, a)
	}
}

// setPackageCache stores what a settings request changes about the caches.
func (s *Server) setPackageCache(ctx context.Context, on *bool, maxBytes *int64, clear bool) error {
	if maxBytes != nil {
		value := ""
		if *maxBytes != 0 {
			if *maxBytes < 1<<30 {
				return fmt.Errorf("the package caches hold at least 1 GiB; %d bytes is less", *maxBytes)
			}
			value = fmt.Sprint(*maxBytes)
		}
		if err := s.store.SetSetting(ctx, state.SettingPackageCacheMax, value); err != nil {
			return err
		}
		s.kickPackageCache()
	}
	if on != nil {
		if err := s.store.SetFlag(ctx, state.SettingPackageCache, *on); err != nil {
			return err
		}
		go s.applyPackageCache(s.background())
	}
	if clear {
		if err := s.packageCache.Clear(); err != nil {
			return err
		}
	}
	return nil
}
