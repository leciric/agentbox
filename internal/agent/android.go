package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agentbox/internal/android"
	"agentbox/internal/hostos"
	"agentbox/internal/state"
)

// An agent can run an Android emulator (android.sh). It gets /dev/kvm and the
// host's Android SDK, read-only. The emulator has no window of its own: scrcpy
// shows it on a separate display, whose VNC server AgentBox reaches through an
// Incus proxy device, like the browser's.

//go:embed android.sh
var androidScript []byte

const (
	androidScriptPath  = "/usr/local/bin/agentbox-android"
	androidProfilePath = "/etc/profile.d/agentbox-android.sh"
	kvmDevice          = "agentbox-kvm"
	androidSDKDevice   = "agentbox-android-sdk"
	androidViewDevice  = "agentbox-android-vnc"
	sharedSDKPath      = "/opt/android-sdk"
	androidViewPort    = 5901
	// AndroidService names the host socket of the emulator's display, next to the browser's.
	AndroidService = "avnc"

	defaultAndroidMemory = 2048
	defaultAndroidCores  = 4
)

// HostServices are the agent services AgentBox reaches through host sockets.
var HostServices = []string{"vnc", "cdp", AndroidService}

const androidProfile = `# Written by AgentBox: the Android SDK, shared read-only and linked into a home builds can add to.
export ANDROID_HOME="$HOME/.local/share/android-sdk"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
export PATH="$PATH:$ANDROID_HOME/platform-tools:$ANDROID_HOME/emulator:$ANDROID_HOME/cmdline-tools/latest/bin"
`

type AndroidStatus struct {
	Available bool   // this machine can run emulators
	Problem   string // why it can't
	SDK       string
	Images    []string
	Running   bool // the emulator runs
	Booted    bool // and Android has finished starting
	View      bool // scrcpy shows it on its display
	Image     string
	Device    string
}

type AndroidOptions struct {
	Image    string // default: the SDK's best system image
	MemoryMB int
	Cores    int
}

// AndroidHost checks that this machine can run emulators and finds its SDK.
func (m *Manager) AndroidHost() (android.SDK, error) {
	if m.AndroidSDK == nil {
		return android.SDK{}, errors.New("only the AgentBox daemon manages Android emulators")
	}
	if err := AndroidUnsupported(); err != nil {
		return android.SDK{}, err
	}
	if err := CheckKVM(); err != nil {
		return android.SDK{}, err
	}
	return m.AndroidSDK()
}

// AndroidUnsupported says why this machine can't run emulators whatever is
// installed on it, or nil. They are x86_64 throughout (android.sh runs
// qemu-system-x86_64 on x86_64 system images), and AgentBox's VM on a Mac has
// no KVM to run them with: Android is off there.
func AndroidUnsupported() error {
	if hostos.InVM() {
		return errors.New("this AgentBox runs in a VM on a Mac, which has no Android emulators")
	}
	if runtime.GOARCH != "amd64" {
		return fmt.Errorf("this machine is %s, and Android emulators need x86_64", runtime.GOARCH)
	}
	return nil
}

// CheckKVM reports whether this user can use hardware virtualization, which emulators need.
func CheckKVM() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) && hostos.WSL() {
		// WSL's kernel has KVM; what's missing is Windows passing
		// virtualization through to WSL's VM (D94).
		return errors.New("WSL has no /dev/kvm: Android emulators need nested virtualization, which WSL has on Windows 11 with virtualization on in the firmware. Set nestedVirtualization=true under [wsl2] in %UserProfile%\\.wslconfig, then run wsl --shutdown")
	}
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("this machine has no /dev/kvm: turn on virtualization (VT-x or AMD-V) in its firmware settings")
	}
	if err != nil {
		return fmt.Errorf("your user can't use /dev/kvm: sudo agentbox host setup adds you to the kvm group, then log out and back in")
	}
	return f.Close()
}

// StartAndroid starts the agent's emulator, unless it runs, and waits until
// Android has booted.
func (m *Manager) StartAndroid(ctx context.Context, a state.Agent, opts AndroidOptions) (AndroidStatus, error) {
	sdk, err := m.AndroidHost()
	if err != nil {
		return AndroidStatus{}, err
	}
	image, err := sdk.Find(opts.Image)
	if err != nil {
		return AndroidStatus{}, err
	}
	memory, cores := opts.MemoryMB, opts.Cores
	if memory <= 0 {
		memory = defaultAndroidMemory
	}
	if cores <= 0 {
		cores = defaultAndroidCores
	}
	if err := m.prepareAndroid(ctx, a, sdk); err != nil {
		return AndroidStatus{}, err
	}
	m.logf("Starting the Android emulator (%s)", image.ID)
	if _, err := m.androidRun(ctx, a, fmt.Sprintf("start %s %d %d", shellQuote(image.ID), memory, cores)); err != nil {
		return AndroidStatus{}, err
	}
	wait, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	if _, err := m.androidRun(wait, a, "wait"); err != nil {
		return AndroidStatus{}, err
	}
	return m.AndroidStatus(ctx, a)
}

func (m *Manager) StopAndroid(ctx context.Context, a state.Agent) error {
	if err := m.requireRunning(ctx, a); err != nil {
		return err
	}
	if started, err := m.androidStarted(ctx, a); err != nil || !started {
		return err
	}
	if err := m.Incus.WriteFile(ctx, a.Instance, androidScriptPath, androidScript, 0, 0, 0o755); err != nil {
		return err
	}
	_, err := m.androidRun(ctx, a, "stop")
	return err
}

// prepareAndroid gives a running agent /dev/kvm, the SDK and the view's proxy
// device, and installs the current android.sh.
func (m *Manager) prepareAndroid(ctx context.Context, a state.Agent, sdk android.SDK) error {
	if m.BrowserSocket == nil {
		return errors.New("only the AgentBox daemon manages Android emulators")
	}
	if err := m.requireRunning(ctx, a); err != nil {
		return err
	}
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return err
	}
	if _, ok := devices[kvmDevice]; !ok {
		// 0666, because a device added while the agent runs doesn't get the kvm group.
		if _, err := m.Incus.Run(ctx, "config", "device", "add", a.Instance, kvmDevice, "unix-char",
			"source=/dev/kvm", "path=/dev/kvm", "mode=0666"); err != nil {
			return err
		}
	}
	if device, ok := devices[androidSDKDevice]; !ok || device["source"] != sdk.Path {
		if ok {
			if _, err := m.Incus.Run(ctx, "config", "device", "remove", a.Instance, androidSDKDevice); err != nil {
				return err
			}
		}
		if _, err := m.Incus.Run(ctx, "config", "device", "add", a.Instance, androidSDKDevice, "disk",
			"source="+sdk.Path, "path="+sharedSDKPath, "readonly=true"); err != nil {
			return err
		}
	}
	if _, ok := devices[androidViewDevice]; !ok {
		if err := m.addSocketProxy(ctx, a, androidViewDevice, AndroidService, androidViewPort); err != nil {
			return err
		}
	}
	if err := m.Incus.WriteFile(ctx, a.Instance, androidProfilePath, []byte(androidProfile), 0, 0, 0o644); err != nil {
		return err
	}
	return m.Incus.WriteFile(ctx, a.Instance, androidScriptPath, androidScript, 0, 0, 0o755)
}

// androidStarted reports whether the agent's emulator was ever started, which
// gives it the view's proxy device.
func (m *Manager) androidStarted(ctx context.Context, a state.Agent) (bool, error) {
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return false, err
	}
	_, ok := devices[androidViewDevice]
	return ok, nil
}

// androidRun runs android.sh with args as the agent's user.
func (m *Manager) androidRun(ctx context.Context, a state.Agent, args string) (string, error) {
	out, err := m.agentShell(ctx, a, androidScriptPath+" "+args)
	if err != nil && strings.Contains(err.Error(), androidScriptPath) && strings.Contains(err.Error(), "not found") {
		return out, errors.New("the emulator isn't running: start it with agentbox android start")
	}
	return out, err
}

// AndroidStatus describes the agent's emulator, and whether this machine can run one.
func (m *Manager) AndroidStatus(ctx context.Context, a state.Agent) (AndroidStatus, error) {
	status := AndroidStatus{Images: []string{}}
	if sdk, err := m.AndroidHost(); err != nil {
		status.Problem = err.Error()
	} else {
		status.Available, status.SDK = true, sdk.Path
		for _, image := range sdk.Images {
			status.Images = append(status.Images, image.ID)
		}
	}
	if m.BrowserSocket == nil {
		return status, nil
	}
	if inst, err := m.Incus.Instance(ctx, a.Instance); err != nil || inst.Status != "Running" {
		return status, err
	}
	if started, err := m.androidStarted(ctx, a); err != nil || !started {
		return status, err
	}
	out, err := m.androidRun(ctx, a, "status")
	if err != nil {
		return status, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			values[key] = value
		}
	}
	status.Running, status.Booted, status.View = values["running"] == "1", values["booted"] == "1", values["view"] == "1"
	if status.Running {
		status.Image = values["image"]
		status.Device = describeDevice(values["image"], values["release"])
	}
	return status, nil
}

// describeDevice is like "Pixel 7, Android 15 (API 35)".
func describeDevice(image, release string) string {
	parts := strings.Split(image, ";")
	if len(parts) < 2 {
		return "Pixel 7"
	}
	api, _, _ := strings.Cut(strings.TrimPrefix(parts[1], "android-"), ".")
	if release == "" {
		return "Pixel 7, API " + api
	}
	return fmt.Sprintf("Pixel 7, Android %s (API %s)", release, api)
}

// DialAndroidView connects to the VNC server on the emulator's display,
// starting what shows the device there if it stopped.
func (m *Manager) DialAndroidView(ctx context.Context, a state.Agent) (net.Conn, error) {
	status, err := m.AndroidStatus(ctx, a)
	if err != nil {
		return nil, err
	}
	if !status.Booted {
		return nil, fmt.Errorf("%s's emulator isn't running: start it first", a.Ref())
	}
	if _, err := m.androidRun(ctx, a, "view"); err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", m.BrowserSocket(a.Instance, AndroidService))
}

// InstallAPK installs an APK from the agent's machine on its emulator.
func (m *Manager) InstallAPK(ctx context.Context, a state.Agent, path string) (string, error) {
	if err := m.requireRunning(ctx, a); err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("no APK to install")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.Worktree, path)
	}
	out, err := m.androidRun(ctx, a, "install "+shellQuote(path))
	if err != nil {
		return "", fmt.Errorf("installing %s: %w", filepath.Base(path), err)
	}
	return strings.TrimSpace(out), nil
}

func (m *Manager) androidScreenshot(ctx context.Context, a state.Agent, file, id string) error {
	tmp := "/tmp/agentbox-screenshot-" + id + ".png"
	if _, err := m.androidRun(ctx, a, "screenshot "+tmp); err != nil {
		return fmt.Errorf("taking the screenshot: %w", err)
	}
	defer func() { _, _ = m.Incus.Run(context.WithoutCancel(ctx), "exec", a.Instance, "--", "rm", "-f", tmp) }()
	_, err := m.Incus.Run(ctx, "file", "pull", a.Instance+tmp, file)
	return err
}
