package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/state"
)

// imageCacheIncus is an Incus whose agent has the given devices and
// daemon.json, and which keeps what is written to daemon.json in written.
func imageCacheIncus(t *testing.T, devices, daemonJSON string) (f fixture, calls func() []string, written string) {
	t.Helper()
	written = filepath.Join(t.TempDir(), "daemon.json")
	inc, calls := loggingIncus(t, `case "$*" in
  query*) echo '{"devices": `+devices+`}' ;;
  *"cat /etc/docker/daemon.json"*) printf '%s' '`+daemonJSON+`' ;;
  *"mkdir -p"*) cat > `+written+` ;;
esac`)
	return setup(t, inc), calls, written
}

func TestEnsureImageCachePointsDockerAtTheCache(t *testing.T) {
	f, calls, written := imageCacheIncus(t, `{}`, `{"log-driver":"local"}`)
	f.m.ImageCacheSocket = func(context.Context) string { return "/t/run/image-cache.sock" }
	f.m.EnsureImageCache(context.Background(), state.Agent{Project: "p", Name: "a", Instance: "ab-agent-01"})
	got := strings.Join(calls(), "\n")
	for _, want := range []string{
		"config device add ab-agent-01 imagecache proxy connect=unix:/t/run/image-cache.sock listen=tcp:127.0.0.1:47500 bind=instance",
		"systemctl try-reload-or-restart docker.service",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	data, err := os.ReadFile(written)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(data); !strings.Contains(s, `"http://127.0.0.1:47500"`) || !strings.Contains(s, `"log-driver": "local"`) {
		t.Errorf("daemon.json = %s, want the mirror added and the rest kept", s)
	}
}

// An agent already set up is left as it is: no device added, no dockerd reload.
func TestEnsureImageCacheIsANoOpWhenAlreadySet(t *testing.T) {
	f, calls, _ := imageCacheIncus(t,
		`{"imagecache": {"type": "proxy", "connect": "unix:/t/run/image-cache.sock"}}`,
		`{"registry-mirrors": ["http://127.0.0.1:47500"]}`)
	f.m.ImageCacheSocket = func(context.Context) string { return "/t/run/image-cache.sock" }
	f.m.EnsureImageCache(context.Background(), state.Agent{Instance: "ab-agent-01"})
	got := strings.Join(calls(), "\n")
	if strings.Contains(got, "config device") || strings.Contains(got, "try-reload") || strings.Contains(got, "mkdir -p") {
		t.Errorf("an agent already set was changed:\n%s", got)
	}
}

// Turned off, the mirror comes out of daemon.json before the device goes, so
// Docker isn't left pointed at a port that no longer answers.
func TestEnsureImageCacheOffTakesTheMirrorOutThenTheDevice(t *testing.T) {
	f, calls, written := imageCacheIncus(t,
		`{"imagecache": {"type": "proxy", "connect": "unix:/t/run/image-cache.sock"}}`,
		`{"registry-mirrors": ["http://127.0.0.1:47500", "https://mirror.example"]}`)
	f.m.ImageCacheSocket = func(context.Context) string { return "" }
	f.m.EnsureImageCache(context.Background(), state.Agent{Instance: "ab-agent-01"})
	got := calls()
	write, remove := -1, -1
	for i, c := range got {
		if strings.Contains(c, "mkdir -p") {
			write = i
		}
		if strings.HasPrefix(c, "config device remove ab-agent-01 imagecache") {
			remove = i
		}
	}
	if write < 0 || remove < 0 || write > remove {
		t.Fatalf("want daemon.json written, then the device removed:\n%s", strings.Join(got, "\n"))
	}
	data, _ := os.ReadFile(written)
	if s := string(data); strings.Contains(s, "47500") || !strings.Contains(s, "https://mirror.example") {
		t.Errorf("daemon.json = %s, want the cache gone and the user's own mirror kept", s)
	}
}

// Off, an agent that never had the cache is never asked anything more.
func TestEnsureImageCacheOffLeavesAnAgentWithoutItAlone(t *testing.T) {
	f, calls, _ := imageCacheIncus(t, `{}`, ``)
	f.m.EnsureImageCache(context.Background(), state.Agent{Instance: "ab-agent-01"})
	for _, c := range calls() {
		if !strings.HasPrefix(c, "query") {
			t.Errorf("unexpected call %q", c)
		}
	}
}

// A device left pointing at another socket (a data directory that moved) is
// replaced.
func TestEnsureImageCacheReplacesADeviceForAnotherSocket(t *testing.T) {
	f, calls, _ := imageCacheIncus(t,
		`{"imagecache": {"type": "proxy", "connect": "unix:/old/image-cache.sock"}}`,
		`{"registry-mirrors": ["http://127.0.0.1:47500"]}`)
	f.m.ImageCacheSocket = func(context.Context) string { return "/t/run/image-cache.sock" }
	f.m.EnsureImageCache(context.Background(), state.Agent{Instance: "ab-agent-01"})
	got := strings.Join(calls(), "\n")
	if !strings.Contains(got, "config device remove ab-agent-01 imagecache") || !strings.Contains(got, "connect=unix:/t/run/image-cache.sock") {
		t.Errorf("the old device wasn't replaced:\n%s", got)
	}
}
