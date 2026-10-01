package hostvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/hostvm/chv"
	"agentbox/internal/paths"
	"agentbox/internal/report"
)

// clearEnv leaves the tests' AGENTBOX_ settings (and where an Android SDK is)
// to the tests, and puts the XDG directories in a temporary one, so
// paths.Default is the test's.
func clearEnv(t *testing.T) paths.Paths {
	t.Helper()
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "AGENTBOX_") || name == "ANDROID_HOME" || name == "ANDROID_SDK_ROOT" {
			t.Setenv(name, "")
			_ = os.Unsetenv(name)
		}
	}
	dir := shortTempDir(t)
	// The tests may run in an agent, whose agentbox is never a front end.
	old := inAgentSocket
	inAgentSocket = filepath.Join(dir, "no-agent.sock")
	t.Cleanup(func() { inAgentSocket = old })
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	p, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// shortTempDir is a temporary directory the daemon's socket fits under. A
// unix socket's path can't be longer than 104 bytes on macOS (108 on Linux),
// and macOS's TMPDIR (/var/folders/…) with a test's name, and the socket's own
// path under XDG_DATA_HOME, is longer than that; a real home's
// ~/.local/share/agentbox/run/agentbox.sock isn't. A short directory in /tmp
// stands in for the home when t.TempDir is too long.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const socketSuffix = len("/data/agentbox/run/agentbox.sock")
	if len(dir)+socketSuffix < 100 || runtime.GOOS == "windows" {
		return dir
	}
	dir, err := os.MkdirTemp("/tmp", "ab")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestFrontAndHandles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Cloud Hypervisor's VM is Linux's")
	}
	p := clearEnv(t)
	// A Linux machine runs AgentBox in the VM, made or not: every command is
	// the front end's, which says to run vm init until it's made.
	if !Front() || !Handles([]string{"ls"}) || !Handles([]string{"vm", "init"}) {
		t.Error("a Linux machine with no VM yet isn't the VM's front end")
	}
	if useLima() {
		t.Error("a Linux machine's VM is Lima's")
	}

	// A host-mode installation from before keeps running AgentBox itself
	// until it moves: only agentbox vm, which moves it, is the front end's.
	if err := os.MkdirAll(p.Data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.StateDB(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !HostInstall(p) || Front() {
		t.Error("a host-mode installation is a front end")
	}
	if !Handles([]string{"vm", "migrate"}) || !Handles([]string{"vm", "status", "--json"}) {
		t.Error("agentbox vm isn't the front end's on a host-mode installation")
	}
	if Handles([]string{"ls"}) || Handles(nil) {
		t.Error("a host-mode installation forwards its commands")
	}

	c := DefaultConfig(chv.DefaultName, "alice", 1000, 1000, "/home/alice", 8, 32*chv.GiB)
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	if !Front() || !Handles([]string{"ls"}) {
		t.Error("a machine vm init made a VM on isn't its front end")
	}
	if useLima() {
		t.Error("a Linux machine's VM is Lima's")
	}

	// The agentbox in the VM is never a front end, whatever its files say:
	// with HOME the host's, shared into it, it sees the host's VM. agentbox vm
	// there only says so, and does nothing else: it would start the VM again,
	// removing the running one's sockets on the share.
	t.Setenv(hostos.Env, hostos.Linux)
	if Front() || Handles([]string{"ls"}) {
		t.Error("the VM's own agentbox thinks it's a front end")
	}
	if !inVM() {
		t.Error("the VM's own agentbox doesn't know it's in the VM")
	}
	for _, args := range [][]string{{"vm", "start"}, {"vm", "run"}, {"status"}} {
		if code := Main(args, "test"); code != 1 {
			t.Errorf("Main(%q) in the VM = %d, want it refused", args, code)
		}
	}
	if _, err := os.Stat(p.VMSocket()); err == nil {
		t.Error("agentbox vm in the VM touched the host's VM")
	}
	t.Setenv(hostos.Env, "")
	// Nor is the Windows front end being tested on Linux.
	t.Setenv("AGENTBOX_FRONT_END", "wsl")
	if Front() || Handles([]string{"vm", "status"}) {
		t.Error("AGENTBOX_FRONT_END=wsl is taken for this front end")
	}
	// AGENTBOX_FRONT_END=vm is Lima's, as it always was.
	t.Setenv("AGENTBOX_FRONT_END", "vm")
	if !Front() || !useLima() {
		t.Error("AGENTBOX_FRONT_END=vm isn't Lima's front end any more")
	}
	// AGENTBOX_FRONT_END=host runs AgentBox itself, as CI does.
	t.Setenv("AGENTBOX_FRONT_END", "host")
	if Front() || Handles([]string{"ls"}) {
		t.Error("AGENTBOX_FRONT_END=host is a front end")
	}
	// Another VM's name has no Config: the host-mode installation is its.
	t.Setenv("AGENTBOX_FRONT_END", "")
	t.Setenv("AGENTBOX_VM", "other")
	if Front() {
		t.Error("a VM that wasn't made makes a host-mode installation a front end")
	}
	// Nor is an agent's machine, where agentbox talks to its daemon.
	_ = os.Remove(p.StateDB())
	if !Front() {
		t.Fatal("a machine with neither is not a front end")
	}
	if err := os.WriteFile(inAgentSocket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if Front() {
		t.Error("an agent's machine is a front end")
	}
}

func TestDefaultConfig(t *testing.T) {
	for _, tc := range []struct {
		cpus         int
		memory, want int64
		wantCPUs     int
	}{
		{16, 32 * chv.GiB, 24 * chv.GiB, 8},       // three quarters
		{8, 64 * chv.GiB, 48 * chv.GiB, 4},        // three quarters
		{4, 12 * chv.GiB, 8 * chv.GiB, 2},         // all but 4GiB
		{2, 6 * chv.GiB, chv.DefaultMemoryMin, 2}, // never below what it boots with
		{1, 0, chv.DefaultMemoryMin, 2},
		{16, 31*chv.GiB + 300<<20, 23 * chv.GiB, 8}, // in whole GiB
	} {
		c := DefaultConfig("agentbox", "alice", 1001, 1002, "/home/alice", tc.cpus, tc.memory)
		if c.MemoryCap != tc.want || c.CPUs != tc.wantCPUs {
			t.Errorf("%d cores, %s: got %d CPUs and a cap of %s, want %d and %s", tc.cpus, sizeWords(tc.memory), c.CPUs, sizeWords(c.MemoryCap), tc.wantCPUs, sizeWords(tc.want))
		}
		if c.MemoryMin != chv.DefaultMemoryMin || c.Disk != chv.DefaultDisk {
			t.Errorf("got %+v", c)
		}
		if c.User != "alice" || c.UID != 1001 || c.GID != 1002 || c.Home != "/home/alice" || c.GuestHome != "/home/alice.linux" {
			t.Errorf("the VM's user: got %+v", c)
		}
	}
}

func TestCheckConfig(t *testing.T) {
	good := DefaultConfig("agentbox", "alice", 1000, 1000, "/home/alice", 8, 32*chv.GiB)
	if err := checkConfig(good, 8, 32*chv.GiB); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(c *chv.Config){
		"no CPUs":              func(c *chv.Config) { c.CPUs = 0 },
		"more CPUs than cores": func(c *chv.Config) { c.CPUs = 9 },
		"a cap below the min":  func(c *chv.Config) { c.MemoryCap = 2 * chv.GiB },
		"a cap over the host":  func(c *chv.Config) { c.MemoryCap = 40 * chv.GiB },
		"a tiny min":           func(c *chv.Config) { c.MemoryMin = 512 << 20 },
		"a tiny disk":          func(c *chv.Config) { c.Disk = 10 * chv.GiB },
	} {
		c := good
		change(&c)
		if err := checkConfig(c, 8, 32*chv.GiB); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// A machine that runs AgentBox itself isn't switched to a VM while its daemon
// runs or it has agents: vm init says what to do instead of doing it.
func TestHostModeInUse(t *testing.T) {
	p := clearEnv(t)
	ctx := context.Background()
	if err := hostModeInUse(ctx, p); err != nil {
		t.Fatalf("a machine with nothing on it: %v", err)
	}
	// A lead's worktree doesn't count: it is reset whenever the lead starts.
	_ = os.MkdirAll(p.Worktree("app", "lead"), 0o755)
	if err := hostModeInUse(ctx, p); err != nil {
		t.Fatalf("only a lead's worktree: %v", err)
	}
	_ = os.MkdirAll(p.Worktree("app", "agent-1"), 0o755)
	err := hostModeInUse(ctx, p)
	if err == nil || !strings.Contains(err.Error(), "app/agent-1") || !strings.Contains(err.Error(), "agentbox vm migrate") {
		t.Fatalf("an agent on the machine: %v", err)
	}
	_ = os.RemoveAll(p.Worktrees())

	// A daemon answering on the socket.
	_ = os.MkdirAll(filepath.Dir(p.Socket()), 0o700)
	l, err := net.Listen("unix", p.Socket())
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	err = hostModeInUse(ctx, p)
	if err == nil || !strings.Contains(err.Error(), "agentbox daemon stop") {
		t.Fatalf("a daemon running: %v", err)
	}
}

// makeHostInstall makes p a host-mode installation from before AgentBox ran in a
// VM on Linux: one with a state.db of its own.
func makeHostInstall(t *testing.T, p paths.Paths) {
	t.Helper()
	if err := os.MkdirAll(p.Data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.StateDB(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// `agentbox vm status --json` on a host-mode installation says it runs
// AgentBox itself, as an api.VMStatus.
func TestStatusJSONInHostMode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Cloud Hypervisor's VM is Linux's")
	}
	makeHostInstall(t, clearEnv(t))
	var out bytes.Buffer
	cmd := newVMCmd("dev")
	cmd.SetArgs([]string{"status", "--json"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var st api.VMStatus
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if st.Mode != api.ModeHost || st.State != api.VMOff || st.Driver != "" {
		t.Errorf("got %+v", st)
	}
	var raw map[string]any
	_ = json.Unmarshal(out.Bytes(), &raw)
	if raw["mode"] != "host" || raw["state"] != "off" {
		t.Errorf("got %s", out.String())
	}
}

// On any other Linux machine, before vm init, it's VM mode with the VM yet to
// make, which is what the app's Setup offers to make; and vm power says it's
// off.
func TestStatusJSONBeforeVMInit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Cloud Hypervisor's VM is Linux's")
	}
	clearEnv(t)
	var out bytes.Buffer
	cmd := newVMCmd("dev")
	cmd.SetArgs([]string{"status", "--json"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var st api.VMStatus
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if st.Mode != api.ModeVM || st.State != api.VMMissing || st.Driver != api.VMDriverCloudHypervisor || !strings.Contains(st.Problem, "agentbox vm init") {
		t.Errorf("got %+v", st)
	}

	out.Reset()
	cmd = newVMCmd("dev")
	cmd.SetArgs([]string{"power", "--json"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var p Power
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if p.State != api.VMOff || !strings.Contains(p.Error, "agentbox vm init") {
		t.Errorf("got %s", out.String())
	}
}

// The Mac's app parses Lima's Status as it always was: AGENTBOX_FRONT_END=vm is
// that front end on Linux.
func TestStatusJSONOnLima(t *testing.T) {
	vm, dir := newFake(t)
	t.Setenv("AGENTBOX_FRONT_END", "vm")
	t.Setenv("AGENTBOX_LIMACTL", vm.Limactl)
	t.Setenv("AGENTBOX_LINUX_BINARY", vm.Binary)
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Running","cpus":4}`+"\n"), 0o644)
	var out bytes.Buffer
	cmd := newVMCmd("dev")
	cmd.SetArgs([]string{"status", "--json"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var st Status
	if err := json.Unmarshal(out.Bytes(), &st); err != nil || !st.Exists || st.Status != "Running" || st.Lima != vm.Limactl {
		t.Fatalf("got %+v, %v: %s", st, err, out.String())
	}
}

// fakeSSH stands in for chv.SSHArgs's ssh: it records the command it runs,
// one a line, and answers the digest check and keeps what's installed.
const fakeSSH = `#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_DIR/calls"
case "$*" in
  *sha256sum*) cat "$FAKE_DIR/digest" 2>/dev/null ;;
  *"sudo sh -c"*agentbox.new*) cat >"$FAKE_DIR/installed" ;;
  *"sudo sh -c"*profile.d*) cat >"$FAKE_DIR/profile" ;;
esac
exit 0
`

// fakeCHV is a Cloud Hypervisor VM whose ssh is fakeSSH and whose state is
// *state; each chv step it runs is recorded in steps.
func fakeCHV(t *testing.T, state *string, steps *[]string) (*VM, string) {
	t.Helper()
	p := clearEnv(t)
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	ssh := filepath.Join(dir, "ssh")
	if err := os.WriteFile(ssh, []byte(fakeSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	// A Mac's VM is given the Linux build beside the front end.
	linux := filepath.Join(dir, LinuxBinary)
	if err := os.WriteFile(linux, []byte("the linux agentbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTBOX_LINUX_BINARY", linux)
	home := filepath.Join(dir, "home")
	_ = os.MkdirAll(home, 0o755)
	c := DefaultConfig(chv.DefaultName, "alice", 1000, 1000, home, 8, 32*chv.GiB)
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	vm, err := NewCHV(p, c)
	if err != nil {
		t.Fatal(err)
	}
	vm.Log = &bytes.Buffer{}

	record := func(step string) { *steps = append(*steps, step) }
	stub(t, &chvSSHArgs, func(c chv.Config, _ chv.Layout, self, workdir string, tty bool, argv []string) []string {
		if self != vm.CHV.Self || workdir == "" {
			t.Errorf("ssh as %q in %q", self, workdir)
		}
		return append([]string{ssh}, argv...)
	})
	stub(t, &chvEnsureTools, func(context.Context, chv.Layout, io.Writer) error { record("tools"); return nil })
	stub(t, &chvMakeDisks, func(context.Context, chv.Config, chv.Layout, io.Writer) error { record("disks"); return nil })
	stub(t, &chvWaitProvisioned, func(context.Context, chv.Config, chv.Layout, io.Writer) error {
		record("provisioned")
		return nil
	})
	stub(t, &chvStart, func(context.Context, chv.Config, chv.Layout, paths.Paths, io.Writer) error {
		record("start")
		*state = api.VMRunning
		return nil
	})
	stub(t, &chvStop, func(_ context.Context, _ chv.Config, _ chv.Layout, _ paths.Paths, agents bool, _ io.Writer) error {
		if agents {
			record("stop --agents")
		} else {
			record("stop")
		}
		*state = api.VMOff
		return nil
	})
	stub(t, &chvStatus, func(_ context.Context, c chv.Config, _ chv.Layout, _ paths.Paths) api.VMStatus {
		return api.VMStatus{Mode: api.ModeVM, Driver: api.VMDriverCloudHypervisor, Name: c.Name, State: *state, CPUs: c.CPUs}
	})
	return vm, dir
}

func stub[T any](t *testing.T, v *T, fake T) {
	old := *v
	*v = fake
	t.Cleanup(func() { *v = old })
}

func TestCHVSetUp(t *testing.T) {
	state, steps := api.VMOff, []string{}
	vm, dir := fakeCHV(t, &state, &steps)
	_ = os.WriteFile(filepath.Join(vm.Home, ".gitconfig"), []byte("[user]\n"), 0o644)
	if err := vm.CHV.setUp(context.Background(), vm, true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"tools", "disks", "start", "provisioned"}; !slices.Equal(steps, want) {
		t.Errorf("steps %q, want %q", steps, want)
	}
	got, err := os.ReadFile(filepath.Join(dir, "installed"))
	if err != nil {
		t.Fatal(err)
	}
	if self, _ := os.ReadFile(vm.Binary); !bytes.Equal(got, self) {
		t.Error("the VM wasn't given this agentbox")
	}
	log := strings.Join(calls(t, dir), "\n")
	for _, want := range []string{
		// The VM's user is the Config's, and the bridge chv's.
		"sudo /usr/local/bin/agentbox host setup --user alice --bridge-subnet " + chv.BridgeSubnet,
		"ln -sfn '" + filepath.Join(vm.Home, ".gitconfig") + "' ~/.gitconfig",
		"env " + hostos.Env + "=" + hostos.Linux + " " + hostos.HomeEnv + "=" + vm.Home,
		vmMemoryCapEnv + "=25769803776",
		"/usr/local/bin/agentbox daemon start",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("setup didn't run %q; it ran:\n%s", want, log)
		}
	}
	if strings.Contains(log, "id -un") {
		t.Error("setup asked the VM who its user is: the Config says")
	}
	profile, _ := os.ReadFile(filepath.Join(dir, "profile"))
	if !strings.Contains(string(profile), "export "+vmMemoryCapEnv+"='25769803776'\n") || !strings.Contains(string(profile), "export "+hostos.Env+"='linux'\n") {
		t.Errorf("profile:\n%s", profile)
	}
	if !strings.Contains(vm.Log.(*bytes.Buffer).String(), "(took ") {
		t.Error("setting up doesn't say how long its steps took")
	}
}

// A forwarded command starts a stopped VM, and runs as agentbox in it with the
// host's settings, the VM's memory cap among them, over ssh.
func TestCHVForward(t *testing.T) {
	state, steps := api.VMOff, []string{}
	vm, dir := fakeCHV(t, &state, &steps)
	t.Setenv("AGENTBOX_PREVIEW_ADDR", "127.0.0.1:17777")
	t.Setenv(vmMemoryCapEnv, "1") // the host's own doesn't go
	want, _ := vm.Digest()
	_ = os.WriteFile(filepath.Join(dir, "digest"), []byte(want+"\n"), 0o644)
	if err := vm.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Starting brings the programs and the seed up to date first.
	if !slices.Equal(steps, []string{"tools", "disks", "start"}) {
		t.Errorf("a stopped VM: steps %q", steps)
	}
	if _, err := os.Stat(filepath.Join(dir, "installed")); err == nil {
		t.Error("an up-to-date agentbox was installed again")
	}
	if err := vm.Ready(context.Background()); err != nil || len(steps) != 3 {
		t.Errorf("a running VM was started again: %q, %v", steps, err)
	}

	got := vm.ForwardArgs(vm.Home, []string{"create", "app"})
	wantArgs := []string{filepath.Join(dir, "ssh"), "env",
		hostos.Env + "=linux", hostos.HomeEnv + "=" + vm.Home, "AGENTBOX_WORKTREES=" + vm.Paths.Worktrees(), "AGENTBOX_MEDIA=" + vm.Paths.Media(),
		vmMemoryCapEnv + "=25769803776", report.VMLogEnv + "=" + vm.CHV.Layout.Log(), hostos.VMDisksEnv + "=" + vm.CHV.Layout.Dir(), "AGENTBOX_PREVIEW_ADDR=127.0.0.1:17777",
		"/usr/local/bin/agentbox", "create", "app"}
	if !slices.Equal(got, wantArgs) {
		t.Errorf("got  %q\nwant %q", got, wantArgs)
	}
}

// The host's Android SDK is found with the host's settings and told to the
// VM's agentbox, which sees it at the same path in the shared home; the host's
// own AGENTBOX_ANDROID_SDK doesn't go as well, since what was found wins.
func TestCHVAndroidSDK(t *testing.T) {
	state, steps := api.VMOff, []string{}
	vm, _ := fakeCHV(t, &state, &steps)
	if env := vm.androidSDKEnv(); env != nil {
		t.Errorf("no SDK: %q", env)
	}
	sdk := filepath.Join(vm.Home, "sdks", "android")
	for _, f := range []string{"emulator/emulator", "platform-tools/adb", "system-images/android-34/google_apis/x86_64/system.img", "system-images/android-34/google_apis/x86_64/kernel-ranchu"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(sdk, f)), 0o755)
		_ = os.WriteFile(filepath.Join(sdk, f), nil, 0o755)
	}
	// Found through a link, which the VM could only follow when it stays in
	// the share: it's told where it leads.
	_ = os.MkdirAll(filepath.Join(vm.Home, "Android"), 0o755)
	if err := os.Symlink(sdk, filepath.Join(vm.Home, "Android", "Sdk")); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(sdk)
	want := androidSDKEnvName + "=" + real
	if env := vm.androidSDKEnv(); !slices.Equal(env, []string{want}) {
		t.Errorf("an SDK in the home: %q, want %s", env, want)
	}
	t.Setenv("ANDROID_HOME", filepath.Join(vm.Home, "incomplete"))
	t.Setenv(androidSDKEnvName, "/not/an/sdk")
	var found []string
	for _, kv := range vm.forwardEnv() {
		if strings.HasPrefix(kv, androidSDKEnvName+"=") {
			found = append(found, kv)
		}
	}
	if !slices.Equal(found, []string{want}) {
		t.Errorf("forwarded %q, want %s", found, want)
	}
	if !strings.Contains(vm.profile(), "export "+androidSDKEnvName+"="+shellQuote(real)+"\n") {
		t.Errorf("the profile has no SDK:\n%s", vm.profile())
	}
}

func TestCHVResize(t *testing.T) {
	state, steps := api.VMRunning, []string{}
	vm, _ := fakeCHV(t, &state, &steps)
	if err := vm.Resize(context.Background(), 6, 20*chv.GiB); err != nil {
		t.Fatal(err)
	}
	c, err := chv.Load(vm.Paths, vm.Name)
	if err != nil || c.CPUs != 6 || c.MemoryCap != 20*chv.GiB {
		t.Fatalf("saved %+v, %v", c, err)
	}
	if len(steps) != 0 {
		t.Errorf("a resize stopped or started the VM: %q", steps)
	}
	if !strings.Contains(vm.Log.(*bytes.Buffer).String(), "next starts") {
		t.Errorf("didn't say when it applies: %q", vm.Log)
	}
	if err := vm.Resize(context.Background(), 0, 2*chv.GiB); err == nil {
		t.Error("a cap below the memory it boots with")
	}
}

// A bigger disk is saved for the VM's next start, when its file grows and
// the pool in it with it; a smaller one is refused, since a disk only grows.
func TestCHVResizeDisk(t *testing.T) {
	state, steps := api.VMOff, []string{}
	vm, dir := fakeCHV(t, &state, &steps)
	l := vm.CHV.Layout
	_ = os.MkdirAll(l.Dir(), 0o755)
	if err := os.WriteFile(l.PoolDisk(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Truncate(l.PoolDisk(), 100*chv.GiB)
	cpus := vm.CHV.Config.CPUs
	if err := vm.CHV.resize(context.Background(), vm, 0, 0, 200*chv.GiB, false); err != nil {
		t.Fatal(err)
	}
	c, err := chv.Load(vm.Paths, vm.Name)
	if err != nil || c.Disk != 200*chv.GiB || c.CPUs != cpus {
		t.Fatalf("saved %+v, %v", c, err)
	}
	if !strings.Contains(vm.Log.(*bytes.Buffer).String(), "a 200GiB disk when it next starts") {
		t.Errorf("didn't say when it applies: %q", vm.Log)
	}
	for _, disk := range []int64{150 * chv.GiB, 50 * chv.GiB} {
		if err := vm.CHV.resize(context.Background(), vm, 0, 0, disk, false); err == nil || !strings.Contains(err.Error(), "only grows") {
			t.Errorf("a %s disk: %v", sizeWords(disk), err)
		}
	}
	// Starting grows the pool in the VM to the disk.
	if err := vm.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if log := strings.Join(calls(t, dir), "\n"); !strings.Contains(log, "btrfs filesystem resize max") || !strings.Contains(log, "-ge 214748364800") {
		t.Errorf("starting didn't grow the pool:\n%s", log)
	}
}

func TestDiskArg(t *testing.T) {
	for in, want := range map[string]int64{"200GiB": 200 * chv.GiB, "1T": 1 << 40, "20.0001G": 20*chv.GiB + 1<<20} {
		if got, err := diskArg(in); err != nil || got != want {
			t.Errorf("%s: %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"10GiB", "17TiB", "big"} {
		if _, err := diskArg(in); err == nil {
			t.Errorf("%s: no error", in)
		}
	}
}

// vm delete stops the VM and removes it and its Config: the machine is back to
// running AgentBox itself. What runs VMs stays.
func TestCHVDelete(t *testing.T) {
	state, steps := api.VMRunning, []string{}
	vm, _ := fakeCHV(t, &state, &steps)
	l := vm.CHV.Layout
	_ = os.MkdirAll(l.Dir(), 0o755)
	_ = os.WriteFile(l.RootDisk(), []byte("disk"), 0o644)
	_ = os.MkdirAll(filepath.Dir(l.Bin("cloud-hypervisor")), 0o755)
	_ = os.WriteFile(l.Bin("cloud-hypervisor"), []byte("ch"), 0o755)
	if err := vm.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(steps, []string{"stop"}) {
		t.Errorf("steps %q", steps)
	}
	if _, err := os.Stat(l.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Error("the VM's directory is still there")
	}
	if chv.Exists(vm.Paths, vm.Name) {
		t.Error("the machine is still a front end")
	}
	if _, err := os.Stat(l.Bin("cloud-hypervisor")); err != nil {
		t.Error("the programs that run VMs went with it")
	}
}

func TestCHVStop(t *testing.T) {
	state, steps := api.VMRunning, []string{}
	vm, _ := fakeCHV(t, &state, &steps)
	if err := vm.Stop(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(steps, []string{"stop --agents"}) {
		t.Errorf("steps %q", steps)
	}
}

// `agentbox vm power --json` is the top bar's: {"mode":"host"} with no VM.
func TestPowerInHostMode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Cloud Hypervisor's VM is Linux's")
	}
	makeHostInstall(t, clearEnv(t))
	var out bytes.Buffer
	cmd := newVMCmd("dev")
	cmd.SetArgs([]string{"power", "--json"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != `{"mode":"host"}` {
		t.Errorf("got %s", got)
	}
}

func TestPowerFrom(t *testing.T) {
	st := api.VMStatus{Mode: api.ModeVM, State: api.VMRunning, CPUs: 4, Memory: api.VMMemory{Min: 4 << 30, Cap: 24 << 30, Granted: 8 << 30, Used: 5 << 30, Resident: 7 << 30}}
	b, _ := json.Marshal(powerFrom(st))
	if want := `{"state":"running","memoryUsed":5368709120,"memoryGranted":8589934592,"memoryCap":25769803776,"cpus":4}`; string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	p := powerFrom(api.VMStatus{State: api.VMMissing})
	if p.State != api.VMOff || p.Error == "" {
		t.Errorf("a VM not made yet: %+v", p)
	}
	if p := powerFrom(api.VMStatus{State: api.VMMissing, Problem: "no disks: run agentbox vm init"}); p.Error != "no disks: run agentbox vm init" {
		t.Errorf("the problem is lost: %+v", p)
	}
}

// The top bar's disk meter reads what the VM costs on the host's disk
// against the size it was given, from power's disk. Measured on the user's
// VM on 2026-10-01: pool.raw 100 GiB with 18.4 GiB allocated, root.raw 20 GiB
// with 5.8 GiB, which is 24.2 GiB of 120 GiB; the 15.5 GiB the pool says it
// uses inside isn't any of these.
func TestPowerFromDisk(t *testing.T) {
	gib := func(f float64) int64 { return int64(f * (1 << 30)) }
	st := api.VMStatus{State: api.VMRunning, Disk: api.VMDisk{
		Pool: api.VMDiskImage{Size: gib(100), Allocated: gib(18.4)},
		Root: api.VMDiskImage{Size: gib(20), Allocated: gib(5.8)},
		HostFree: gib(310),
	}}
	st.Disk.Add(st.Disk.Pool)
	st.Disk.Add(st.Disk.Root)
	p := powerFrom(st)
	if p.Disk == nil || p.Disk.Size != gib(120) || p.Disk.Allocated != gib(18.4)+gib(5.8) || p.HostFree != gib(310) {
		t.Fatalf("disk = %+v, host free %d", p.Disk, p.HostFree)
	}
	b, _ := json.Marshal(p.Disk)
	if want := `{"size":128849018880,"allocated":25984552140,"pool":{"size":107374182400,"allocated":19756849561},"root":{"size":21474836480,"allocated":6227702579},"hostFree":332859965440}`; string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	if p := powerFrom(api.VMStatus{State: api.VMMissing}); p.Disk != nil {
		t.Errorf("a VM with no disks has a disk: %+v", p.Disk)
	}
}

// A Lima VM's one disk is its Root: Lima's size, and its images' allocated
// bytes in the instance's directory.
func TestLimaDisk(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "diffdisk"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	p := limaPower(State{Exists: true, Status: "Running", Dir: dir, Disk: 100 << 30, Memory: 8 << 30}, nil)
	if p.Disk == nil || p.Disk.Size != 100<<30 || p.Disk.Root.Size != 100<<30 || p.Disk.Pool != (api.VMDiskImage{}) {
		t.Fatalf("disk = %+v", p.Disk)
	}
	if p.Disk.Allocated < 1<<20 || p.Disk.Allocated >= 1<<30 || p.Disk.Allocated != p.Disk.Root.Allocated {
		t.Errorf("allocated = %d, want what diffdisk takes, not its size", p.Disk.Allocated)
	}
	if p.HostFree <= 0 || p.HostFree != p.Disk.HostFree {
		t.Errorf("host free = %d, disk's %d", p.HostFree, p.Disk.HostFree)
	}
	if p := limaPower(State{Exists: true, Status: "Stopped"}, nil); p.Disk != nil {
		t.Errorf("no directory, yet a disk: %+v", p.Disk)
	}
}

// vm disk measures the worktrees and media in the host's home, not the VM's
// disk images beside them.
func TestHomeDisk(t *testing.T) {
	p := paths.Paths{Data: t.TempDir()}
	t.Setenv("AGENTBOX_WORKTREES", "")
	for name, size := range map[string]int{"worktrees/p/agent-1/a": 3 << 20, "media/p/agent-1/rec.webm": 1 << 20, "vm/agentbox/pool.raw": 5 << 20} {
		path := filepath.Join(p.Data, name)
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A hard link takes no more room.
	if err := os.Link(filepath.Join(p.Data, "worktrees/p/agent-1/a"), filepath.Join(p.Data, "worktrees/p/agent-1/b")); err != nil {
		t.Fatal(err)
	}
	home := HomeDisk(p)
	if home.Worktrees < 3<<20 || home.Worktrees >= 4<<20 || home.Media < 1<<20 || home.Media >= 2<<20 {
		t.Errorf("home = %+v, want 3 MiB of worktrees and 1 MiB of media", home)
	}
}

func TestLimaPower(t *testing.T) {
	for _, tc := range []struct {
		st      State
		err     error
		state   string
		granted int64
		failed  bool
	}{
		{State{Exists: true, Status: "Running", CPUs: 4, Memory: 8 << 30}, nil, api.VMRunning, 8 << 30, false},
		{State{Exists: true, Status: "Stopped", CPUs: 4, Memory: 8 << 30}, nil, api.VMOff, 0, false},
		{State{Exists: true, Status: "Broken"}, nil, api.VMOff, 0, true},
		{State{}, nil, api.VMOff, 0, true},
		{State{}, errors.New("no limactl"), api.VMOff, 0, true},
	} {
		p := limaPower(tc.st, tc.err)
		if p.State != tc.state || p.MemoryGranted != tc.granted || (p.Error != "") != tc.failed {
			t.Errorf("%+v, %v: got %+v", tc.st, tc.err, p)
		}
	}
}

// vm start and vm resume return once the VM's daemon answers on this
// machine's socket, starting it when it doesn't.
func TestDaemonUp(t *testing.T) {
	state, steps := api.VMRunning, []string{}
	vm, dir := fakeCHV(t, &state, &steps)
	stub(t, &daemonWait, 3*time.Second)
	// "daemon start" in the VM brings up a daemon answering on the socket.
	_ = os.MkdirAll(filepath.Dir(vm.Paths.Socket()), 0o700)
	started := make(chan struct{})
	go func() {
		for {
			if b, _ := os.ReadFile(filepath.Join(dir, "calls")); strings.Contains(string(b), "daemon start") {
				close(started)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	go func() {
		<-started
		l, err := net.Listen("unix", vm.Paths.Socket())
		if err != nil {
			return
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })}
		t.Cleanup(func() { _ = srv.Close() })
		_ = srv.Serve(l)
	}()
	if err := vm.daemonUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Once it answers, there's nothing to start.
	_ = os.Remove(filepath.Join(dir, "calls"))
	if err := vm.daemonUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "calls")); err == nil {
		t.Errorf("a daemon that answers was started again: %q", calls(t, dir))
	}
}

// Each driver has the commands it can do: pausing and the supervisor are
// Cloud Hypervisor's.
func TestCommandsPerDriver(t *testing.T) {
	names := func() []string {
		var out []string
		for _, c := range newVMCmd("dev").Commands() {
			out = append(out, c.Name())
		}
		return out
	}
	clearEnv(t)
	if runtime.GOOS == "linux" {
		got := names()
		for _, want := range []string{"init", "start", "stop", "pause", "resume", "status", "power", "shell", "resize", "upgrade", "delete", "run", "proxy"} {
			if !slices.Contains(got, want) {
				t.Errorf("no vm %s on Linux: %q", want, got)
			}
		}
	}
	t.Setenv("AGENTBOX_FRONT_END", "vm")
	got := names()
	if slices.Contains(got, "pause") || slices.Contains(got, "run") || !slices.Contains(got, "power") {
		t.Errorf("Lima's commands: %q", got)
	}
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"help", "create"}, {"--help"}, {"-h"}, {"create", "--help"}, {"exec", "agent-1", "-h", "--", "x"}} {
		if !Help(args) {
			t.Errorf("Help(%q) = false, want the front end to answer it", args)
		}
	}
	for _, args := range [][]string{{"status"}, {"vm", "--help"}, {"vm", "start", "-h"}, {"exec", "agent-1", "--", "ls", "--help"}, {"ask", "what's --help for?"}} {
		if Help(args) {
			t.Errorf("Help(%q) = true, want it forwarded (or the front end's own vm)", args)
		}
	}
}
