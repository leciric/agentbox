package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"agentbox/internal/state"
)

// Every agent's Docker pulls Docker Hub's images through the daemon's shared
// image cache (internal/imagecache), so an image several agents use is
// downloaded and stored once.
//
// The cache is a unix socket of the daemon's. A proxy device, like the
// in-agent API's, makes it a port on the agent's own 127.0.0.1, and the
// agent's /etc/docker/daemon.json names that port in registry-mirrors: a
// loopback address, which Docker talks plain HTTP to without being told to,
// and which needs no route from the agent's network to the VM. dockerd reads
// registry-mirrors again on SIGHUP, so it is set on agents that already exist
// as they start, with no base image rebuild and no restart of their
// containers.
//
// Docker's mirror stands in for Docker Hub alone. Images from ghcr.io, quay.io
// and other registries are pulled from them directly, as before, by every
// agent: Docker has no mirror setting for them.
//
// When the cache can't answer — the daemon is down, or the cache can't reach
// Docker Hub — the port refuses the connection or the cache answers with an
// error, and Docker pulls from Docker Hub itself, as it does when a mirror
// fails.

// imageCacheDevice is the proxy device that brings the cache into an agent.
const imageCacheDevice = "imagecache"

// ImageCachePort is the port of the agent's 127.0.0.1 the cache is at: one
// nobody's local registry or dev server is likely to want.
const ImageCachePort = 47500

// ImageCacheMirror is the cache as Docker's registry-mirrors names it.
var ImageCacheMirror = fmt.Sprintf("http://127.0.0.1:%d", ImageCachePort)

// dockerDaemonJSON is the agent's Docker configuration.
const dockerDaemonJSON = "/etc/docker/daemon.json"

// EnsureImageCache points the agent's Docker at the shared image cache when
// it's on, and back at Docker Hub alone when it's off. Best-effort, like
// EnsureBrowser: an agent pulls its images either way.
func (m *Manager) EnsureImageCache(ctx context.Context, a state.Agent) {
	if err := m.applyImageCache(ctx, a); err != nil {
		m.logf("%s's Docker isn't using the shared image cache: %v", a.Ref(), err)
	}
}

func (m *Manager) applyImageCache(ctx context.Context, a state.Agent) error {
	socket := ""
	if m.ImageCacheSocket != nil {
		socket = m.ImageCacheSocket(ctx)
	}
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return err
	}
	device, has := devices[imageCacheDevice]
	if socket == "" {
		// An agent without the device never had the mirror set, or had it
		// taken out before the device went.
		if !has {
			return nil
		}
		if err := m.setDockerMirror(ctx, a, false); err != nil {
			return err
		}
		return m.Incus.RemoveDevice(ctx, a.Instance, imageCacheDevice)
	}
	if has && device["connect"] != "unix:"+socket {
		if err := m.Incus.RemoveDevice(ctx, a.Instance, imageCacheDevice); err != nil {
			return err
		}
		has = false
	}
	if !has {
		if err := m.Incus.AddDevice(ctx, a.Instance, imageCacheDevice,
			"proxy",
			"connect=unix:"+socket,
			fmt.Sprintf("listen=tcp:127.0.0.1:%d", ImageCachePort),
			"bind=instance"); err != nil {
			return err
		}
	}
	return m.setDockerMirror(ctx, a, true)
}

// setDockerMirror adds the cache to the agent's registry-mirrors, or takes it
// out, keeping whatever else daemon.json says, and has dockerd read it again
// if it is running. A dockerd that isn't yet reads it when it starts.
func (m *Manager) setDockerMirror(ctx context.Context, a state.Agent, on bool) error {
	current, err := m.Incus.Exec(ctx, a.Instance, "sh", "-c", "cat "+dockerDaemonJSON+" 2>/dev/null || true")
	if err != nil {
		return err
	}
	next, changed, err := withImageCacheMirror([]byte(current), on)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if err := m.Incus.WriteFile(ctx, a.Instance, dockerDaemonJSON, next, 0, 0, 0o644); err != nil {
		return err
	}
	_, err = m.Incus.Exec(ctx, a.Instance, "systemctl", "try-reload-or-restart", "docker.service")
	return err
}

// withImageCacheMirror is daemon.json with the cache first in
// registry-mirrors, or without it, and whether that changed anything. A file
// that isn't JSON is somebody's own and is left alone.
func withImageCacheMirror(raw []byte, on bool) ([]byte, bool, error) {
	config := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, false, fmt.Errorf("%s isn't JSON, so it's left as it is: %w", dockerDaemonJSON, err)
		}
	}
	var mirrors []string
	if list, ok := config["registry-mirrors"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok && strings.TrimRight(s, "/") != ImageCacheMirror {
				mirrors = append(mirrors, s)
			}
		}
	}
	if on {
		mirrors = append([]string{ImageCacheMirror}, mirrors...)
	}
	var before []string
	if list, ok := config["registry-mirrors"].([]any); ok {
		for _, v := range list {
			s, _ := v.(string)
			before = append(before, s)
		}
	}
	if slices.Equal(before, mirrors) {
		return raw, false, nil
	}
	if len(mirrors) == 0 {
		delete(config, "registry-mirrors")
	} else {
		config["registry-mirrors"] = mirrors
	}
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}
