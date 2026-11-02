package tunnel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEveryPinnedTailscaleHasItsChecksums(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		asset, err := TailscaleInstaller{Arch: arch}.asset()
		if err != nil || len(tailscaleSums[TailscaleVersion+"/"+asset]) != 64 {
			t.Errorf("tools.txt pins tailscale %s, but tailscale_install.go has no sha256 for %s (%v)", TailscaleVersion, asset, err)
		}
	}
}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestTailscaleInstallChecksAndUnpacks(t *testing.T) {
	asset := "tailscale_" + TailscaleVersion + "_amd64.tgz"
	top := strings.TrimSuffix(asset, ".tgz") + "/"
	good := tarball(t, map[string]string{
		top + "tailscale":                  "#!/bin/sh\necho " + TailscaleVersion + "\n",
		top + "tailscaled":                 "#!/bin/sh\n",
		top + "systemd/tailscaled.service": "[Unit]\n",
	})
	sum := sha256.Sum256(good)
	key := TailscaleVersion + "/" + asset
	was := tailscaleSums[key]
	t.Cleanup(func() { tailscaleSums[key] = was })
	tailscaleSums[key] = hex.EncodeToString(sum[:])

	body := []byte("something else")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+asset {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	inst := TailscaleInstaller{Dir: t.TempDir(), Releases: srv.URL, Arch: "amd64"}
	if _, err := inst.Ensure(context.Background(), func(string) {}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a download that doesn't match: %v", err)
	}
	if _, err := os.Stat(inst.Path()); err == nil {
		t.Fatal("a download that didn't match was installed")
	}

	body = good
	dir, err := inst.Ensure(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tailscale", "tailscaled"} {
		if !executable(filepath.Join(dir, name)) {
			t.Errorf("%s isn't installed in %s", name, dir)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "systemd")); err == nil {
		t.Error("the systemd files were unpacked too")
	}
	srv.Close()
	if _, err := inst.Ensure(context.Background(), func(string) {}); err != nil {
		t.Fatalf("installed, it was downloaded again: %v", err)
	}
}

func TestParseTailscaleStatus(t *testing.T) {
	n, err := parseTailscaleStatus([]byte(`{"BackendState":"Running","Self":{"ID":"nAbC123CNTRL","DNSName":"agentbox-5f3a.tail1234.ts.net.","TailscaleIPs":["100.101.102.103","fd7a:115c:a1e0::1"]},"CurrentTailnet":{"Name":"example.com"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if n.URL != "https://agentbox-5f3a.tail1234.ts.net/" || n.DNSName != "agentbox-5f3a.tail1234.ts.net" || n.NodeID != "nAbC123CNTRL" || len(n.IPs) != 2 || n.Tailnet != "example.com" {
		t.Fatalf("node = %+v", n)
	}
	for status, want := range map[string]string{
		`{"BackendState":"NeedsLogin"}`:                             "signed out of the tailnet",
		`{"BackendState":"NeedsMachineAuth"}`:                       "approved",
		`{"BackendState":"Starting"}`:                               "Starting, not running",
		`{"BackendState":"Running","Self":{"DNSName":""}}`:          "MagicDNS",
		`{"BackendState":"Running","Self":{"DNSName":"100.1.2.3"}}`: "MagicDNS",
		`not json`: "isn't JSON",
	} {
		if _, err := parseTailscaleStatus([]byte(status)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", status, err, want)
		}
	}
}

func TestExplainTailscale(t *testing.T) {
	for out, want := range map[string]string{
		"Serve is not enabled on your tailnet.\nTo enable, visit:\n\n         https://login.tailscale.com/f/serve?node=nAbC\n": "turn on HTTPS certificates for the tailnet (MagicDNS too), then try again: https://login.tailscale.com/f/serve?node=nAbC",
		"error enabling https feature: error 404 Not Found: 404 page not found:":                                               "turn on HTTPS certificates",
		"backend error: invalid key: API key does not exist":                                                                   "refused the hub's auth key",
		"backend error: requested tags [tag:agentbox] are invalid or not permitted":                                            "tagOwners",
		"something\nwent wrong": "went wrong",
	} {
		if got := explainTailscale([]byte(out), errors.New("exit status 1")); !strings.Contains(got, want) {
			t.Errorf("%q: %q, want %q", out, got, want)
		}
	}
	if got := explainTailscale(nil, errors.New("exit status 1")); got != "exit status 1" {
		t.Errorf("with nothing printed: %q", got)
	}
}

// fakeTailscale writes a tailscaled that makes its socket and waits to be
// stopped, and a tailscale that records each command and the auth key it was
// given, and answers status as a running node.
func fakeTailscale(t *testing.T, serveOut string) (dir string) {
	dir = t.TempDir()
	daemon := `#!/bin/sh
d=$(dirname "$0")
echo "$@" >"$d/daemon-args"
for a in "$@"; do case "$a" in --socket=*) touch "${a#--socket=}";; --statedir=*) touch "${a#--statedir=}/tailscaled.state";; esac; done
trap 'echo stopped >"$d/stopped"; exit 0' TERM
while :; do sleep 0.05; done
`
	cli := `#!/bin/sh
d=$(dirname "$0")
echo "$@" >>"$d/calls"
for a in "$@"; do case "$a" in --auth-key=file:*) cat "${a#--auth-key=file:}" >"$d/authkey";; esac; done
case "$2" in
status) echo '{"BackendState":"Running","Self":{"ID":"nAbC123CNTRL","DNSName":"agentbox-5f3a.tail1234.ts.net.","TailscaleIPs":["100.101.102.103"]},"CurrentTailnet":{"Name":"example.com"}}';;
serve) if [ -n "$SERVE_OUT" ]; then printf '%s\n' "$SERVE_OUT"; exit 1; fi;;
esac
`
	cli = strings.Replace(cli, `"$SERVE_OUT"`, "'"+serveOut+"'", 2)
	for name, script := range map[string]string{"tailscaled": daemon, "tailscale": cli} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func startFake(t *testing.T, dir, stateDir, owner string) (*Tailscale, chan TailscaleNode) {
	t.Helper()
	up := make(chan TailscaleNode, 4)
	ts := StartTailscale(context.Background(), TailscaleConfig{
		Binaries: func(context.Context, func(string)) (string, error) { return dir, nil },
		Origin:   "http://127.0.0.1:7781", AuthKey: "tskey-auth-kSecret-CNTRL", Hostname: "agentbox-5f3a",
		StateDir: stateDir, Owner: owner,
		OnUp: func(n TailscaleNode) { up <- n },
	}, nil, t.Logf)
	return ts, up
}

func TestTailscaleJoinsAndServes(t *testing.T) {
	dir := fakeTailscale(t, "")
	state := filepath.Join(t.TempDir(), "tailscale")
	ts, up := startFake(t, dir, state, "hub-a")
	var n TailscaleNode
	select {
	case n = <-up:
	case <-time.After(10 * time.Second):
		t.Fatalf("the node didn't come up: %+v", ts.Status())
	}
	if st := ts.Status(); st.State != StateRunning || st.URL != "https://agentbox-5f3a.tail1234.ts.net/" || n.NodeID != "nAbC123CNTRL" {
		t.Fatalf("status %+v, node %+v", st, n)
	}
	daemonArgs, _ := os.ReadFile(filepath.Join(dir, "daemon-args"))
	if !strings.Contains(string(daemonArgs), "--tun=userspace-networking") || !strings.Contains(string(daemonArgs), "--statedir="+state) {
		t.Errorf("tailscaled ran with %q", daemonArgs)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if strings.Contains(string(calls), "kSecret") {
		t.Fatalf("the auth key was on a command line: %q", calls)
	}
	for _, want := range []string{"up --reset --hostname=agentbox-5f3a --accept-dns=false", "serve --bg --yes http://127.0.0.1:7781", "status --json"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("tailscale wasn't run with %q: %q", want, calls)
		}
	}
	if key, _ := os.ReadFile(filepath.Join(dir, "authkey")); string(key) != "tskey-auth-kSecret-CNTRL" {
		t.Errorf("tailscale up read the auth key %q", key)
	}
	if _, err := os.Stat(filepath.Join(state, "authkey")); err == nil {
		t.Error("the auth key's file was left behind")
	}
	ts.Stop()
	if _, err := os.Stat(filepath.Join(dir, "stopped")); err != nil {
		t.Error("tailscaled wasn't stopped")
	}

	// The same owner keeps its state; another forgets it.
	ts, up = startFake(t, dir, state, "hub-a")
	<-up
	ts.Stop()
	if _, err := os.Stat(filepath.Join(state, "tailscaled.state")); err != nil {
		t.Fatal("the node's state is gone")
	}
	if err := os.WriteFile(filepath.Join(state, "old-tailnet"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ts, up = startFake(t, dir, state, "hub-b")
	<-up
	ts.Stop()
	if _, err := os.Stat(filepath.Join(state, "old-tailnet")); err == nil {
		t.Error("another hub's node state was kept")
	}
	if owner, _ := os.ReadFile(filepath.Join(state, TailscaleOwnerFile)); string(owner) != "hub-b" {
		t.Errorf("the state's owner is %q", owner)
	}
}

func TestTailscaleSaysHowToTurnServeOn(t *testing.T) {
	was := minBackoff
	minBackoff = time.Hour
	t.Cleanup(func() { minBackoff = was })
	dir := fakeTailscale(t, "Serve is not enabled on your tailnet. To enable, visit: https://login.tailscale.com/f/serve?node=nAbC")
	ts, _ := startFake(t, dir, filepath.Join(t.TempDir(), "tailscale"), "hub-a")
	defer ts.Stop()
	deadline := time.Now().Add(10 * time.Second)
	for ts.Status().State != StateFailed && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := ts.Status(); st.State != StateFailed || !strings.Contains(st.Error, "turn on HTTPS certificates") || !strings.Contains(st.Error, "https://login.tailscale.com/f/serve?node=nAbC") {
		t.Fatalf("status %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "stopped")); err != nil {
		t.Error("tailscaled was left running after serve failed")
	}
}
