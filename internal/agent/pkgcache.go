package agent

import (
	"context"
	"fmt"
	"path"
	"strings"

	"agentbox/internal/pkgcache"
	"agentbox/internal/state"
)

// Every agent's package managers share their caches (internal/pkgcache): the
// daemon's package-cache directory, a disk device at PackageCachePath in
// every agent, which pnpm, npm, Yarn Berry, Go, pip, uv, Corepack and
// Playwright are pointed at. A dependency several agents need is downloaded
// once, and is still there for the agents that come after.
//
// The tools are pointed at it by the agent's env file (packageCacheEnv), the
// same in every agent and whether the cache is on or not: it only names the
// cache when the directory is there for the agent's user to write in. pnpm 10
// and older, which don't read pnpm_config_* variables, and Yarn Berry, whose
// global folder variable Yarn 1 would take for its own, read files in the
// agent's home instead (packageCacheSetup).
//
// Only caches go in it, never what's the agent's own: logins, tokens and .env
// files stay in the agent, and npm's logs, which can quote a command line, go
// to the agent's ~/.npm.

// packageCacheDevice is the disk device that mounts the caches in an agent.
const packageCacheDevice = "pkgcache"

// PackageCachePath is where the caches are in every agent: the same path for
// all of them, as Go's build cache keys what it compiled on where the module
// was, and Playwright finds its browsers by path.
const PackageCachePath = "/var/cache/agentbox/packages"

// packageCacheEnv is the env file's lines that point the tools at the caches.
func (m *Manager) packageCacheEnv() string {
	dir := func(sub string) string { return shellQuote(path.Join(PackageCachePath, sub)) }
	vars := [][2]string{
		{"npm_config_cache", dir(pkgcache.Npm)},
		{"npm_config_logs_dir", shellQuote("/home/" + m.User.Name + "/.npm/_logs")},
		{"pnpm_config_store_dir", dir(pkgcache.PnpmStore)},
		{"pnpm_config_cache_dir", dir(pkgcache.PnpmCache)},
		{"COREPACK_HOME", dir(pkgcache.Corepack)},
		{"GOMODCACHE", dir(pkgcache.GoMod)},
		{"GOCACHE", dir(pkgcache.GoBuild)},
		{"PIP_CACHE_DIR", dir(pkgcache.Pip)},
		{"UV_CACHE_DIR", dir(pkgcache.Uv)},
		// uv hard-links from its cache, which can't reach across to the
		// project's mount: it would copy anyway, after a warning every time.
		{"UV_LINK_MODE", "copy"},
		{"PLAYWRIGHT_BROWSERS_PATH", dir(pkgcache.Playwright)},
		// From one agent, the projects of the others look deleted, and
		// Playwright would delete the browsers they use: the daemon evicts
		// browsers instead.
		{"PLAYWRIGHT_SKIP_BROWSER_GC", "1"},
	}
	var b strings.Builder
	b.WriteString("# Package managers' caches every agent shares, while there's one to write in.\n")
	b.WriteString("if [ -d " + shellQuote(PackageCachePath) + " ] && [ -w " + shellQuote(PackageCachePath) + " ]; then\n")
	for _, v := range vars {
		b.WriteString("  export " + v[0] + "=" + v[1] + "\n")
	}
	b.WriteString("fi\n")
	return b.String()
}

// packageCacheSetup is the script, run as root in the agent, that gives the
// agent's user the cache's directory — the mount's, or once it's taken out
// the directory left behind, so that a shell started while it was in keeps
// working, with caches of the agent's own — and points pnpm 10 and Yarn
// Berry at it, leaving any line the user set alone.
func (m *Manager) packageCacheSetup() string {
	owner := fmt.Sprintf("%d", m.User.UID)
	group := fmt.Sprintf("%d", m.User.GID)
	home := "/home/" + m.User.Name
	dir := func(sub string) string { return path.Join(PackageCachePath, sub) }
	line := func(file, key, value string) string {
		f := shellQuote(home + "/" + file)
		return "[ -e " + f + " ] || install -D -m 644 -o " + owner + " -g " + group + " /dev/null " + f + "\n" +
			"grep -q " + shellQuote("^"+key) + " " + f + " || echo " + shellQuote(key+value) + " >> " + f + "\n"
	}
	return "set -e\n" +
		"install -d -o " + owner + " -g " + group + " " + shellQuote(PackageCachePath) + "\n" +
		line(".config/pnpm/rc", "store-dir=", dir(pkgcache.PnpmStore)) +
		line(".config/pnpm/rc", "cache-dir=", dir(pkgcache.PnpmCache)) +
		line(".yarnrc.yml", "globalFolder:", " "+dir(pkgcache.Yarn))
}

// EnsurePackageCache mounts the shared package caches in the agent when
// they're on, and takes them out when they're off; an agent that never had
// them is left as it is. Best-effort, like EnsureImageCache: the agent's
// package managers work either way, only slower.
func (m *Manager) EnsurePackageCache(ctx context.Context, a state.Agent) {
	if err := m.applyPackageCache(ctx, a); err != nil {
		m.logf("%s isn't using the shared package caches: %v", a.Ref(), err)
	}
}

func (m *Manager) applyPackageCache(ctx context.Context, a state.Agent) error {
	source := ""
	if m.PackageCacheDir != nil {
		source = m.PackageCacheDir(ctx)
	}
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return err
	}
	device, has := devices[packageCacheDevice]
	if !has && source == "" {
		// Off, and never mounted: the agent's tools keep their own caches.
		return nil
	}
	if has && device["source"] != source {
		if err := m.Incus.RemoveDevice(ctx, a.Instance, packageCacheDevice); err != nil {
			return err
		}
		has = false
	}
	if source != "" && !has {
		if err := m.Incus.AddDevice(ctx, a.Instance, packageCacheDevice, "disk",
			"source="+source, "path="+PackageCachePath); err != nil {
			return err
		}
	}
	_, err = m.Incus.Exec(ctx, a.Instance, "sh", "-c", m.packageCacheSetup())
	return err
}
