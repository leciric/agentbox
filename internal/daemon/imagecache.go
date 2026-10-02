package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"agentbox/internal/imagecache"
	"agentbox/internal/state"
)

// The shared image cache: one pull-through cache of Docker Hub's images in
// AgentBox's VM that every agent's Docker pulls through (internal/imagecache,
// agent/imagecache.go). The daemon serves it on a unix socket for as long as
// it runs, whether it's on or not; on or off is whether agents are pointed at
// it. Its blobs are on the disk the daemon's state is on — the VM's own disk —
// under a cap of SettingImageCacheMax, and never past the disk floor.

func (s *Server) newImageCache() *imagecache.Cache {
	return &imagecache.Cache{
		Dir: s.cfg.Paths.ImageCache(),
		Max: func() int64 {
			on, maxBytes, err := s.store.ImageCache(context.Background())
			if err != nil || !on {
				return 0
			}
			return maxBytes
		},
		Room: func() int64 {
			free, total, _, ok := diskSpace(s.cfg.Paths.Data)
			if !ok {
				return 0
			}
			return free - s.diskFloor(context.Background()).For(total)
		},
		Client: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: time.Minute,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   30 * time.Second,
			ForceAttemptHTTP2:     true,
		}},
		Logf: s.logf,
	}
}

// serveImageCache listens on the image cache's socket until ctx ends. A
// cache that can't listen leaves agents pulling directly, as they do with it
// off: imageCacheSocket says so.
func (s *Server) serveImageCache(ctx context.Context) {
	path := s.cfg.Paths.ImageCacheSocket()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.logf("image cache: %v", err)
		return
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		s.logf("image cache: %v", err)
		return
	}
	// Only root (Incus' proxy) and this user can connect.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		s.logf("image cache: %v", err)
		return
	}
	srv := &http.Server{Handler: s.imageCache, ReadHeaderTimeout: 30 * time.Second}
	s.imageCacheUp.Store(true)
	go func() {
		<-ctx.Done()
		s.imageCacheUp.Store(false)
		_ = srv.Close()
		_ = os.Remove(path)
	}()
	go func() { _ = srv.Serve(ln) }()
}

// imageCacheSocket is the socket agents' proxy devices connect to, or ""
// while the cache is off or isn't being served.
func (s *Server) imageCacheSocket(ctx context.Context) string {
	if !s.imageCacheUp.Load() {
		return ""
	}
	on, _, err := s.store.ImageCache(ctx)
	if err != nil || !on {
		return ""
	}
	return s.cfg.Paths.ImageCacheSocket()
}

// applyImageCache points every running agent's Docker at the cache, or away
// from it, after the setting changed. A stopped agent is set as it starts.
func (s *Server) applyImageCache(ctx context.Context) {
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		s.logf("image cache: %v", err)
		return
	}
	running := map[string]bool{}
	instances, err := s.cfg.Incus.Instances(ctx)
	if err != nil {
		s.logf("image cache: %v", err)
		return
	}
	for _, inst := range instances {
		running[inst.Name] = inst.Status == "Running"
	}
	m := s.manager(s.cfg.Log)
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady || !running[a.Instance] {
			continue
		}
		m.EnsureImageCache(ctx, a)
	}
}

// setImageCache stores what a settings request changes about the cache.
func (s *Server) setImageCache(ctx context.Context, on *bool, maxBytes *int64, clear bool) error {
	if maxBytes != nil {
		value := ""
		if *maxBytes != 0 {
			if *maxBytes < 1<<30 {
				return fmt.Errorf("the image cache holds at least 1 GiB; %d bytes is less", *maxBytes)
			}
			value = fmt.Sprint(*maxBytes)
		}
		if err := s.store.SetSetting(ctx, state.SettingImageCacheMax, value); err != nil {
			return err
		}
		s.imageCache.Trim()
	}
	if on != nil {
		if err := s.store.SetFlag(ctx, state.SettingImageCache, *on); err != nil {
			return err
		}
		go s.applyImageCache(s.background())
	}
	if clear {
		if err := s.imageCache.Clear(); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
