package iossim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"agentbox/internal/api"
)

// Runner runs a command, returning its standard output, and an error with
// its standard error when it fails. Tests give it a fake.
type Runner func(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error)

// Exec is Runner on the real machine.
func Exec(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if msg != "" {
			return out, fmt.Errorf("%s %s: %s", name, args[0], msg)
		}
		return out, fmt.Errorf("%s %s: %w", name, args[0], err)
	}
	return out, nil
}

// Simctl is xcrun simctl: every simulator command the helper runs.
type Simctl struct {
	Run Runner
}

func (s Simctl) simctl(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	return s.Run(ctx, stdin, "xcrun", append([]string{"simctl"}, args...)...)
}

// Xcode says which Xcode is selected, like "Xcode 26.1", or why simulators
// can't run: no Xcode, or only its Command Line Tools.
func (s Simctl) Xcode(ctx context.Context) (string, error) {
	if _, err := s.Run(ctx, nil, "xcrun", "--find", "simctl"); err != nil {
		return "", fmt.Errorf("%w: Xcode isn't installed, or the Command Line Tools are selected instead: install Xcode from the App Store, open it once, then run sudo xcode-select -s /Applications/Xcode.app", ErrUnavailable)
	}
	out, err := s.Run(ctx, nil, "xcrun", "xcodebuild", "-version")
	if err != nil {
		return "Xcode", nil
	}
	first, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(first), nil
}

// DeveloperDir is the selected Xcode's Developer directory, where simhost
// finds SimulatorKit.
func (s Simctl) DeveloperDir(ctx context.Context) (string, error) {
	out, err := s.Run(ctx, nil, "xcode-select", "-p")
	return strings.TrimSpace(string(out)), err
}

type simctlList struct {
	Devices map[string][]struct {
		UDID        string `json:"udid"`
		Name        string `json:"name"`
		State       string `json:"state"`
		IsAvailable bool   `json:"isAvailable"`
	} `json:"devices"`
}

// runtimeName is "iOS 26.0" from "com.apple.CoreSimulator.SimRuntime.iOS-26-0".
func runtimeName(id string) (name, platform string) {
	short := id[strings.LastIndex(id, ".")+1:]
	platform, version, _ := strings.Cut(short, "-")
	return platform + " " + strings.ReplaceAll(version, "-", "."), platform
}

// Devices lists the available iOS simulators: booted ones first, then the
// newest runtime's.
func (s Simctl) Devices(ctx context.Context) ([]api.IOSDevice, error) {
	out, err := s.simctl(ctx, nil, "list", "devices", "available", "--json")
	if err != nil {
		return nil, err
	}
	var list simctlList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("simctl list: %w", err)
	}
	devices := []api.IOSDevice{}
	for id, ds := range list.Devices {
		name, platform := runtimeName(id)
		if platform != "iOS" {
			continue
		}
		for _, d := range ds {
			if d.IsAvailable && ValidUDID(d.UDID) {
				devices = append(devices, api.IOSDevice{UDID: d.UDID, Name: d.Name, Runtime: name, State: d.State})
			}
		}
	}
	sort.SliceStable(devices, func(i, j int) bool {
		a, b := devices[i], devices[j]
		if (a.State == "Booted") != (b.State == "Booted") {
			return a.State == "Booted"
		}
		if a.Runtime != b.Runtime {
			return newer(a.Runtime, b.Runtime)
		}
		return a.Name < b.Name
	})
	return devices, nil
}

// newer compares "iOS 26.0" and "iOS 18.4" by their numbers.
func newer(a, b string) bool {
	var am, an, bm, bn int
	_, _ = fmt.Sscanf(strings.TrimPrefix(a, "iOS "), "%d.%d", &am, &an)
	_, _ = fmt.Sscanf(strings.TrimPrefix(b, "iOS "), "%d.%d", &bm, &bn)
	if am != bm {
		return am > bm
	}
	return an > bn
}

// Boot boots a simulator, headless (Simulator.app isn't needed, the app's
// view is the screen), and waits until it has finished booting.
func (s Simctl) Boot(ctx context.Context, udid string) error {
	if _, err := s.simctl(ctx, nil, "boot", udid); err != nil && !strings.Contains(err.Error(), "current state: Booted") {
		return err
	}
	_, err := s.simctl(ctx, nil, "bootstatus", udid, "-b")
	return err
}

func (s Simctl) Shutdown(ctx context.Context, udid string) error {
	if _, err := s.simctl(ctx, nil, "shutdown", udid); err != nil && !strings.Contains(err.Error(), "current state: Shutdown") {
		return err
	}
	return nil
}

func (s Simctl) Install(ctx context.Context, udid, app string) error {
	_, err := s.simctl(ctx, nil, "install", udid, app)
	return err
}

// Launch starts an app, stopping it first if it runs, and says its pid.
func (s Simctl) Launch(ctx context.Context, udid, bundleID string, args []string) (string, error) {
	out, err := s.simctl(ctx, nil, append([]string{"launch", "--terminate-running-process", udid, bundleID}, args...)...)
	return strings.TrimSpace(string(out)), err
}

func (s Simctl) Terminate(ctx context.Context, udid, bundleID string) error {
	_, err := s.simctl(ctx, nil, "terminate", udid, bundleID)
	return err
}

func (s Simctl) OpenURL(ctx context.Context, udid, url string) error {
	_, err := s.simctl(ctx, nil, "openurl", udid, url)
	return err
}

// Pasteboard puts text on a simulator's pasteboard.
func (s Simctl) Pasteboard(ctx context.Context, udid, text string) error {
	_, err := s.simctl(ctx, strings.NewReader(text), "pbcopy", udid)
	return err
}

// Screenshot is a PNG of a simulator's screen.
func (s Simctl) Screenshot(ctx context.Context, udid string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "agentbox-ios-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, "screen.png")
	if _, err := s.simctl(ctx, nil, "io", udid, "screenshot", "--type=png", file); err != nil {
		return nil, err
	}
	return os.ReadFile(file)
}
