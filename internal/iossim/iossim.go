// Package iossim is the iOS Simulator for agents, on the Mac that AgentBox's
// VM runs on: the simulator can't run in the VM, so a helper runs beside it on
// the Mac, and the daemon in the VM reaches it.
//
//   - The helper (Serve) is `agentbox ios-helper`, which the Mac's front end
//     starts (Ensure, from internal/hostvm) and leaves running. It serves HTTP
//     on the Mac's 127.0.0.1, every request with the bearer token it writes,
//     with its port, to a state file in the Mac's AgentBox config (StateFile).
//     Its routes are simctl's list, boot, shutdown, install, launch,
//     terminate, openurl and screenshot, input, and the screen as a WebSocket
//     in internal/devicestream's wire format, which the daemon relays to the
//     app's DeviceStreamView as it does an Android emulator's. None runs
//     anything it is handed: only xcrun simctl with checked arguments, and
//     simhost.
//   - The daemon (Client) reads that state file, which the VM sees at the same
//     path in the shared home (the front end says where, in StateFileEnv), and
//     dials host.lima.internal, where Lima puts the Mac's 127.0.0.1. Agents
//     can't: the VM drops what they send to that address (agentbox.yaml), so
//     they go through the daemon's /v1/self/ios.
//   - simhost (simhost.m) is the part that needs Apple's private frameworks:
//     one process per simulator in use, compiled on the Mac with xcrun clang
//     the first time it's needed (Build). It captures the simulator's screen
//     from CoreSimulator's framebuffer, encodes it as H.264 on the Mac's
//     hardware encoder with no B-frames, and injects touches, keys and
//     buttons: over dtuhidd (Xcode 27's CoreSimulator 1155.4 and later) or
//     SimulatorKit's Indigo messages before it. Written for AgentBox from
//     facebook/idb's (MIT) notes on those interfaces, not from serve-sim.
package iossim

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// StateFileEnv tells the daemon in the VM where the helper's state file is
// (hostvm's vmEnv), at the path the VM sees the Mac's home at.
const StateFileEnv = "AGENTBOX_IOS_HELPER"

// DefaultHost is where the VM reaches the Mac's 127.0.0.1: Lima's name for
// it, 192.168.5.2 with the networking it sets up by default.
const DefaultHost = "host.lima.internal"

// StateFile is where the helper says how to reach it, in the Mac's AgentBox
// config directory.
func StateFile(configDir string) string { return filepath.Join(configDir, "ios-helper.json") }

// State is the helper's state file.
type State struct {
	Port    int    `json:"port"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
	// Host overrides DefaultHost, for tests.
	Host string `json:"host,omitempty"`
}

// ReadState reads the helper's state file.
func ReadState(file string) (State, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("%s: %w", file, err)
	}
	if st.Port <= 0 || st.Token == "" {
		return State{}, fmt.Errorf("%s has no port or token", file)
	}
	return st, nil
}

// writeState writes the state file only its owner can read, in one rename,
// so a reader never sees half of one.
func writeState(file string, st State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var udidPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// ValidUDID reports whether s is a simulator's UDID, which is all a route
// passes to simctl as a device.
func ValidUDID(s string) bool { return udidPattern.MatchString(s) }

var bundleIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,254}$`)

// ErrUnavailable is the helper not being reachable, or there being no helper:
// a Linux host, or a Mac the front end hasn't started one on.
var ErrUnavailable = errors.New("the iOS Simulator is unavailable")

// Problem says why the iOS Simulator can't be used, for a person, from what
// went wrong reaching it.
func Problem(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, os.ErrNotExist):
		return "AgentBox's simulator helper isn't running on your Mac. Run any agentbox command on the Mac (agentbox vm start) to start it."
	default:
		return strings.TrimPrefix(err.Error(), ErrUnavailable.Error()+": ")
	}
}
