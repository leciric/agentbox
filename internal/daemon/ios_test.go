package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/iossim"
)

// fakeMacSimulators is the Mac's simulator helper with xcrun faked: two
// simulators, none booted until one is.
type fakeMacSimulators struct {
	mu     sync.Mutex
	booted string
	ran    []string
}

func (f *fakeMacSimulators) run(_ context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.Join(append([]string{name}, args...), " ")
	f.ran = append(f.ran, line)
	switch {
	case line == "xcrun xcodebuild -version":
		return []byte("Xcode 26.1\n"), nil
	case strings.HasPrefix(line, "xcrun simctl list"):
		state := func(udid string) string {
			if udid == f.booted {
				return "Booted"
			}
			return "Shutdown"
		}
		return fmt.Appendf(nil, `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-0":[
			{"udid":"0A1B2C3D-0000-4000-8000-00000000000A","name":"iPad Air","state":%q,"isAvailable":true},
			{"udid":"0A1B2C3D-0000-4000-8000-00000000000B","name":"iPhone 17 Pro","state":%q,"isAvailable":true}]}}`,
			state("0A1B2C3D-0000-4000-8000-00000000000A"), state("0A1B2C3D-0000-4000-8000-00000000000B")), nil
	case strings.HasPrefix(line, "xcrun simctl boot "):
		f.booted = args[2]
	}
	return nil, nil
}

func (f *fakeMacSimulators) ranLike(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.ran {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// withMacSimulators points the daemon at a helper serving the fake, as the
// front end's state file would on a Mac.
func withMacSimulators(t *testing.T, d testDaemon, roots ...string) *fakeMacSimulators {
	t.Helper()
	f := &fakeMacSimulators{}
	noSimhost := func(context.Context, string) (*iossim.Proc, error) { return nil, errors.New("no simhost in tests") }
	ts := httptest.NewServer(iossim.NewServer("token", "test", iossim.Simctl{Run: f.run}, roots, noSimhost).Handler())
	t.Cleanup(ts.Close)
	u, _ := url.Parse(ts.URL)
	port, _ := strconv.Atoi(u.Port())
	file := filepath.Join(t.TempDir(), "ios-helper.json")
	data := fmt.Sprintf(`{"port":%d,"token":"token","pid":1,"version":"test","host":%q}`, port, u.Hostname())
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	d.srv.iosSim.client = iossim.NewClient(file)
	return f
}

func TestIOSIsUnavailableOffAMac(t *testing.T) {
	t.Setenv(hostos.Env, "linux")
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(context.Background(), api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	addAgent(t, d, repo, "hello-stack", "agent-01", "")
	st, err := d.client.IOS(context.Background(), "hello-stack/agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if st.Available || !strings.Contains(st.Problem, "runs on a Mac") {
		t.Errorf("status off a Mac = %+v", st)
	}
}

func TestIOSOnAMac(t *testing.T) {
	t.Setenv(hostos.Env, "darwin")
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "")
	ref := a.Ref()

	// No helper yet: the status says how to start one.
	d.srv.iosSim.client = iossim.NewClient(filepath.Join(t.TempDir(), "ios-helper.json"))
	st, err := d.client.IOS(ctx, ref)
	if err != nil || st.Available || !strings.Contains(st.Problem, "isn't running on your Mac") {
		t.Fatalf("status with no helper = %+v, %v", st, err)
	}

	sims := withMacSimulators(t, d, d.paths.Worktrees())
	st, err = d.client.IOS(ctx, ref)
	if err != nil || !st.Available || st.Xcode != "Xcode 26.1" || len(st.Devices) != 2 || st.Device != "" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	// Nothing booted, nothing named: input has no simulator to go to.
	if err := d.client.IOSInput(ctx, ref, api.IOSInput{Type: "tap", X: 0.5, Y: 0.5}); err == nil || !strings.Contains(err.Error(), "agentbox ios open") {
		t.Errorf("input with nothing booted: %v", err)
	}
	// Opening one with nothing said picks an iPhone, and makes it the agent's.
	st, err = d.client.OpenIOS(ctx, ref, api.IOSDeviceRequest{})
	if err != nil || st.Device != "0A1B2C3D-0000-4000-8000-00000000000B" {
		t.Fatalf("open = %+v, %v", st, err)
	}
	if !sims.ranLike("xcrun simctl boot 0A1B2C3D-0000-4000-8000-00000000000B") {
		t.Error("open didn't boot the iPhone")
	}
	// By name too.
	if _, err := d.client.OpenIOS(ctx, ref, api.IOSDeviceRequest{Device: "ipad air"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.OpenIOS(ctx, ref, api.IOSDeviceRequest{Device: "Galaxy S25"}); err == nil {
		t.Error("opened a simulator that doesn't exist")
	}
	// The agent's own simulator is the one its commands use.
	if _, err := d.client.LaunchIOSApp(ctx, ref, api.IOSLaunchRequest{BundleID: "com.example.pawly"}); err != nil {
		t.Fatal(err)
	}
	if !sims.ranLike("xcrun simctl launch --terminate-running-process 0A1B2C3D-0000-4000-8000-00000000000A com.example.pawly") {
		t.Error("launch didn't go to the agent's simulator")
	}

	// Apps come from the agent's worktree, relative paths included.
	app := filepath.Join(a.Worktree, "build", "Pawly.app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.InstallIOSApp(ctx, ref, api.IOSInstallRequest{Path: "build/Pawly.app"}); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(app)
	if !sims.ranLike("xcrun simctl install 0A1B2C3D-0000-4000-8000-00000000000A " + real) {
		t.Error("the app wasn't installed")
	}
	for _, path := range []string{"../agent-02/build/Pawly.app", "/etc/Pawly.app"} {
		if _, err := d.client.InstallIOSApp(ctx, ref, api.IOSInstallRequest{Path: path}); err == nil || !strings.Contains(err.Error(), "worktree") {
			t.Errorf("installed %s from outside the worktree: %v", path, err)
		}
	}

	// Input reaches the helper, which says what's wrong.
	if err := d.client.IOSInput(ctx, ref, api.IOSInput{Type: "tap", X: 0.5, Y: 0.5}); err == nil || !strings.Contains(err.Error(), "no simhost in tests") {
		t.Errorf("input: %v", err)
	}

	if _, err := d.client.CloseIOS(ctx, ref, api.IOSDeviceRequest{}); err != nil || !sims.ranLike("xcrun simctl shutdown 0A1B2C3D-0000-4000-8000-00000000000A") {
		t.Errorf("close: %v", err)
	}
}
