package agent_test

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/state"
)

func packageCacheFixture(t *testing.T, devices string) (fixture, func() []string) {
	t.Helper()
	inc, calls := loggingIncus(t, `case "$*" in
  query*) echo '{"devices": `+devices+`}' ;;
esac`)
	return setup(t, inc), calls
}

func TestEnsurePackageCacheMountsTheCaches(t *testing.T) {
	f, calls := packageCacheFixture(t, `{}`)
	f.m.PackageCacheDir = func(context.Context) string { return "/vm/package-cache" }
	f.m.EnsurePackageCache(context.Background(), state.Agent{Instance: "ab-agent-01"})
	got := strings.Join(calls(), "\n")
	for _, want := range []string{
		"config device add ab-agent-01 pkgcache disk source=/vm/package-cache path=/var/cache/agentbox/packages",
		"store-dir=/var/cache/agentbox/packages/pnpm/store",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}

func TestEnsurePackageCacheLeavesAMountedAgentsDevice(t *testing.T) {
	f, calls := packageCacheFixture(t, `{"pkgcache": {"type": "disk", "source": "/vm/package-cache"}}`)
	f.m.PackageCacheDir = func(context.Context) string { return "/vm/package-cache" }
	f.m.EnsurePackageCache(context.Background(), state.Agent{Instance: "ab-agent-01"})
	if got := strings.Join(calls(), "\n"); strings.Contains(got, "config device") {
		t.Errorf("the device was changed:\n%s", got)
	}
}

// Off, the device goes, and the directory it leaves is the user's, for the
// shells that still point at it.
func TestEnsurePackageCacheOffTakesTheCachesOut(t *testing.T) {
	f, calls := packageCacheFixture(t, `{"pkgcache": {"type": "disk", "source": "/vm/package-cache"}}`)
	f.m.EnsurePackageCache(context.Background(), state.Agent{Instance: "ab-agent-01"})
	got := calls()
	remove, setup := -1, -1
	for i, c := range got {
		if strings.HasPrefix(c, "config device remove ab-agent-01 pkgcache") {
			remove = i
		}
		if strings.Contains(c, "install -d -o 1000 -g 1000 '/var/cache/agentbox/packages'") {
			setup = i
		}
	}
	if remove < 0 || setup < remove {
		t.Errorf("want the device removed, then the directory given to the user:\n%s", strings.Join(got, "\n"))
	}
}

func TestEnsurePackageCacheOffLeavesAnAgentWithoutThemAlone(t *testing.T) {
	f, calls := packageCacheFixture(t, `{}`)
	f.m.EnsurePackageCache(context.Background(), state.Agent{Instance: "ab-agent-01"})
	for _, c := range calls() {
		if !strings.HasPrefix(c, "query") {
			t.Errorf("unexpected call %q", c)
		}
	}
}

// A data directory that moved (~/.agentbox) moves the device's source.
func TestEnsurePackageCacheFollowsTheDirectory(t *testing.T) {
	f, calls := packageCacheFixture(t, `{"pkgcache": {"type": "disk", "source": "/old/package-cache"}}`)
	f.m.PackageCacheDir = func(context.Context) string { return "/new/package-cache" }
	f.m.EnsurePackageCache(context.Background(), state.Agent{Instance: "ab-agent-01"})
	got := strings.Join(calls(), "\n")
	if !strings.Contains(got, "config device remove ab-agent-01 pkgcache") || !strings.Contains(got, "source=/new/package-cache") {
		t.Errorf("the device wasn't moved:\n%s", got)
	}
}
