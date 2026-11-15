package hostwsl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"agentbox/internal/lan"
)

// Phones on the local network reach the daemon through the relay: WSL's
// network isn't the one Windows is on (NAT, the default), and in mirrored
// mode Hyper-V's firewall stands in the way. While the daemon has phones on,
// the relay opens their port on Windows, on every interface, and passes each
// request on over a stream to the daemon's socket, as it does the app's
// (lan.Forward). So phones reach AgentBox while the app runs, which is when
// the relay does.
//
// Windows Firewall asks the user, the first time agentbox.exe listens, whether
// to let it in on private networks, public ones or neither; dismissing that
// blocks it. What it decided is asked of PowerShell (firewallState), and the
// app says what to allow when it would turn phones away (firewallWarning).

// runLAN keeps phones' port open as the daemon wants it, until ctx ends.
func (r *Relay) runLAN(ctx context.Context) {
	f := lan.NewForward(r.dialDaemon, func(format string, a ...any) { r.Distro.progress(format, a...) })
	if exe, err := os.Executable(); err == nil && runtime.GOOS == "windows" {
		f.Firewall = func(ctx context.Context, port int) string {
			st, err := firewallState(ctx, exe, port)
			if err != nil {
				return ""
			}
			return firewallWarning(st, exe, port)
		}
	}
	f.Run(ctx)
}

// dialDaemon is a connection to the daemon's socket in the distro, over the
// relay's session.
func (r *Relay) dialDaemon(ctx context.Context) (net.Conn, error) {
	stream, err := r.open(ctx)
	if err != nil {
		return nil, err
	}
	status := make([]byte, 1)
	_ = stream.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.ReadFull(stream, status); err != nil {
		_ = stream.Close()
		return nil, err
	}
	_ = stream.SetReadDeadline(time.Time{})
	if status[0] != bridgeOK {
		_ = stream.Close()
		return nil, errors.New("the AgentBox daemon isn't running in " + r.Distro.Name)
	}
	return stream, nil
}

// firewall is what Windows Firewall says about phones' port.
type firewall struct {
	// Networks are the categories of the networks Windows is on: Public,
	// Private or DomainAuthenticated.
	Networks list[string] `json:"networks"`
	// On are the firewall's profiles that are turned on: Domain, Private,
	// Public.
	On list[string] `json:"on"`
	// Rules are the enabled inbound rules for agentbox.exe or the port.
	Rules list[firewallRule] `json:"rules"`
}

// list is a JSON array, or the one value PowerShell can make of an array of
// one, or null for none.
type list[T any] []T

func (l *list[T]) UnmarshalJSON(b []byte) error {
	if b = bytes.TrimSpace(b); len(b) > 0 && b[0] == '[' {
		return json.Unmarshal(b, (*[]T)(l))
	}
	if string(b) == "null" {
		*l = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*l = list[T]{v}
	return nil
}

type firewallRule struct {
	Action  string `json:"action"`  // Allow or Block
	Profile string `json:"profile"` // Any, or some of "Domain, Private, Public"
}

// firewallScript prints a firewall as JSON, after a line that sets $exe and
// $port.
const firewallScript = `$ErrorActionPreference = 'SilentlyContinue'
$inbound = { $_.Enabled -eq 'True' -and $_.Direction -eq 'Inbound' }
$rules = @(Get-NetFirewallApplicationFilter -Program $exe | Get-NetFirewallRule | Where-Object $inbound)
$rules += @(Get-NetFirewallPortFilter -Protocol TCP | Where-Object { $_.LocalPort -contains $port } | Get-NetFirewallRule | Where-Object $inbound)
[pscustomobject]@{
  networks = @(Get-NetConnectionProfile | ForEach-Object { [string]$_.NetworkCategory })
  on = @(Get-NetFirewallProfile | Where-Object { $_.Enabled -eq 'True' } | ForEach-Object { [string]$_.Name })
  rules = @($rules | ForEach-Object { [pscustomobject]@{ action = [string]$_.Action; profile = [string]$_.Profile } })
} | ConvertTo-Json -Compress -Depth 3`

// firewallState asks PowerShell. An error is that it couldn't say.
func firewallState(ctx context.Context, exe string, port int) (firewall, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// -Command takes one script: the path goes in it as a quoted string.
	script := fmt.Sprintf("$exe = '%s'; $port = '%d'\n%s", strings.ReplaceAll(exe, "'", "''"), port, firewallScript)
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return firewall{}, err
	}
	return parseFirewall(out)
}

func parseFirewall(out []byte) (firewall, error) {
	var fw firewall
	if err := json.Unmarshal(bytes.TrimSpace(out), &fw); err != nil {
		return firewall{}, err
	}
	return fw, nil
}

// firewallWarning is what to say when the firewall would turn phones away on
// a network Windows is on: when it's on for that network and a rule blocks
// agentbox.exe or the port there, or none lets them in. "" otherwise.
func firewallWarning(fw firewall, exe string, port int) string {
	var blocked, closed []string
	for _, n := range fw.Networks {
		profile := strings.TrimSuffix(n, "Authenticated") // DomainAuthenticated is the Domain profile
		if !slices.Contains(fw.On, profile) || slices.Contains(blocked, profile) || slices.Contains(closed, profile) {
			continue
		}
		allow, block := false, false
		for _, r := range fw.Rules {
			if r.Profile != "Any" && !slices.Contains(strings.Split(r.Profile, ", "), profile) {
				continue
			}
			switch r.Action {
			case "Allow":
				allow = true
			case "Block":
				block = true
			}
		}
		switch {
		case block:
			blocked = append(blocked, profile)
		case !allow:
			closed = append(closed, profile)
		}
	}
	rule := fmt.Sprintf(`netsh advfirewall firewall add rule name="AgentBox phones" dir=in action=allow protocol=TCP localport=%d program="%s"`, port, exe)
	switch {
	case len(blocked) > 0:
		return fmt.Sprintf("Windows Firewall blocks phones on this %s network: a rule blocks agentbox.exe. In Windows Security → Firewall & network protection → Allow an app through firewall, tick agentbox.exe for %s networks, or delete the rule blocking it in Windows Defender Firewall with Advanced Security",
			networks(blocked), networks(blocked))
	case len(closed) > 0:
		return fmt.Sprintf("Windows Firewall may turn phones away on this %s network: allow agentbox.exe when Windows asks, or in Windows Security → Firewall & network protection → Allow an app through firewall, or run in a terminal as administrator: %s",
			networks(closed), rule)
	}
	return ""
}

func networks(profiles []string) string {
	return strings.ToLower(strings.Join(profiles, " and "))
}
