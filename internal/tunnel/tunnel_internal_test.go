package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEveryPinnedReleaseHasItsChecksums(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		if len(sums[Version+"/cloudflared-linux-"+arch]) != 64 {
			t.Errorf("tools.txt pins cloudflared %s, but install.go has no sha256 for its linux-%s binary", Version, arch)
		}
	}
}

func TestInstallChecksTheDownload(t *testing.T) {
	good := []byte("#!/bin/sh\necho cloudflared version " + Version + "\n")
	sum := sha256.Sum256(good)
	key := Version + "/cloudflared-linux-amd64"
	was := sums[key]
	t.Cleanup(func() { sums[key] = was })

	body := good
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+Version+"/cloudflared-linux-amd64" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	inst := Installer{Dir: t.TempDir(), Releases: srv.URL, Arch: "amd64"}

	sums[key] = hex.EncodeToString(sum[:])
	body = []byte("something else")
	if _, err := inst.Ensure(context.Background(), func(string) {}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a download that doesn't match: %v", err)
	}
	if _, err := os.Stat(inst.Path()); err == nil {
		t.Fatal("a download that didn't match was installed")
	}

	body = good
	var said []string
	path, err := inst.Ensure(context.Background(), func(s string) { said = append(said, s) })
	if err != nil {
		t.Fatal(err)
	}
	if path != inst.Path() || len(said) == 0 {
		t.Fatalf("path %s, status %q", path, said)
	}
	if st, err := os.Stat(path); err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("installed %v, %v", st, err)
	}
	// Installed, it isn't downloaded again.
	srv.Close()
	if _, err := inst.Ensure(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
}

// fake writes a fake cloudflared: it records its arguments and token, prints
// what the real one does, and exits for the first `fails` runs.
func fake(t *testing.T, fails int) (bin, dir string) {
	dir = t.TempDir()
	bin = filepath.Join(dir, "cloudflared")
	script := `#!/bin/sh
d=$(dirname "$0")
echo "$@" >"$d/args"
printf '%s' "$TUNNEL_TOKEN" >"$d/token"
echo "$HOME" >"$d/home"
n=$(cat "$d/runs" 2>/dev/null || echo 0); n=$((n+1)); echo $n >"$d/runs"
if [ $n -le ` + itoa(fails) + ` ]; then
  echo "2026-09-29T10:00:00Z ERR Couldn't start tunnel error=\"failed to dial to edge with quic: timeout, token $TUNNEL_TOKEN\"" >&2
  exit 1
fi
echo "2026-09-29T10:00:00Z INF |  https://quiet-river-1234.trycloudflare.com                                                |" >&2
echo "2026-09-29T10:00:01Z INF Registered tunnel connection connIndex=0" >&2
trap 'echo stopped >"$d/stopped"; exit 0' TERM
while :; do sleep 0.05; done
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func itoa(n int) string { return string(rune('0' + n)) }

func waitFor(t *testing.T, tun *Tunnel, ok func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := tun.Status(); ok(st) {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the tunnel never got there: %+v", tun.Status())
	return Status{}
}

func binary(path string) func(context.Context, func(string)) (string, error) {
	return func(context.Context, func(string)) (string, error) { return path, nil }
}

func TestQuickTunnel(t *testing.T) {
	bin, dir := fake(t, 0)
	home := t.TempDir()
	tun := Start(context.Background(), Config{Binary: binary(bin), Origin: "http://127.0.0.1:4321", Home: home}, nil, t.Logf)
	st := waitFor(t, tun, func(s Status) bool { return s.State == StateRunning })
	if st.URL != "https://quiet-river-1234.trycloudflare.com/" {
		t.Fatalf("URL %q", st.URL)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if got := strings.TrimSpace(string(args)); got != "tunnel --no-autoupdate --url http://127.0.0.1:4321" {
		t.Fatalf("args %q", got)
	}
	if h, _ := os.ReadFile(filepath.Join(dir, "home")); strings.TrimSpace(string(h)) != home {
		t.Fatalf("HOME %q", h)
	}
	tun.Stop()
	if _, err := os.Stat(filepath.Join(dir, "stopped")); err != nil {
		t.Fatal("Stop didn't end cloudflared with SIGTERM")
	}
}

func TestNamedTunnelKeepsItsTokenOffTheCommandLine(t *testing.T) {
	defer func(a, b time.Duration) { minBackoff, maxBackoff = a, b }(minBackoff, maxBackoff)
	minBackoff, maxBackoff = 10*time.Millisecond, 20*time.Millisecond
	bin, dir := fake(t, 1)
	const token = "eyJhIjoic2VjcmV0LXRva2VuIn0"
	var changes []Status
	var logged []string
	tun := Start(context.Background(), Config{Binary: binary(bin), Origin: "http://127.0.0.1:4321", Token: token, Hostname: "chat.example.com"},
		func(s Status) { changes = append(changes, s) },
		func(f string, a ...any) { logged = append(logged, f) })
	st := waitFor(t, tun, func(s Status) bool { return s.State == StateRunning })
	tun.Stop()
	if st.URL != "https://chat.example.com/" {
		t.Fatalf("URL %q", st.URL)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if got := strings.TrimSpace(string(args)); got != "tunnel --no-autoupdate run" {
		t.Fatalf("args %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "token")); string(got) != token {
		t.Fatalf("token %q", got)
	}
	// The first run failed: that was said, without the token it printed.
	var failed *Status
	for i := range changes {
		if changes[i].State == StateFailed {
			failed = &changes[i]
		}
		if strings.Contains(changes[i].Error, token) {
			t.Fatalf("the token got into the status: %q", changes[i].Error)
		}
	}
	if failed == nil || !strings.Contains(failed.Error, "failed to dial to edge") || !strings.Contains(failed.Error, "[token]") {
		t.Fatalf("changes %+v", changes)
	}
	if runs, _ := os.ReadFile(filepath.Join(dir, "runs")); strings.TrimSpace(string(runs)) != "2" {
		t.Fatalf("runs %q", runs)
	}
}

func TestADownloadThatFailsIsTriedAgain(t *testing.T) {
	defer func(a time.Duration) { minBackoff = a }(minBackoff)
	minBackoff = 10 * time.Millisecond
	bin, _ := fake(t, 0)
	tries := 0
	tun := Start(context.Background(), Config{Origin: "http://127.0.0.1:1", Binary: func(_ context.Context, status func(string)) (string, error) {
		tries++
		status("Downloading cloudflared, once for this machine")
		if tries == 1 {
			return "", os.ErrDeadlineExceeded
		}
		return bin, nil
	}}, nil, t.Logf)
	defer tun.Stop()
	waitFor(t, tun, func(s Status) bool { return s.State == StateRunning })
}
