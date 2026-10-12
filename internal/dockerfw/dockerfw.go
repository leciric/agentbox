// Package dockerfw keeps Docker's firewall from blocking the Incus bridge.
//
// Docker sets the iptables FORWARD policy to DROP and sends forwarded traffic
// through its DOCKER-USER chain first, so containers on incusbr0 get no route
// out unless that chain accepts them. host-setup.sh does it for a
// docker.service on this machine, through a drop-in. Where dockerd isn't a
// local service (Docker Desktop's WSL integration runs it in another distro,
// sharing this one's network namespace) the drop-in never fires, so the daemon
// checks the chain itself: on start, on a timer, and before an image build.
package dockerfw

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Bridge is the interface of AgentBox's Incus network. The rules name the
// interface rather than its subnet, which Incus chooses.
const Bridge = "incusbr0"

// Chain is the one Docker leaves for others' rules.
const Chain = "DOCKER-USER"

// Run runs a command and returns its combined output.
type Run func(ctx context.Context, name string, args ...string) ([]byte, error)

// System runs commands here, through sudo when this isn't root: the daemon runs
// as the user, and changing the firewall takes root.
func System(ctx context.Context, name string, args ...string) ([]byte, error) {
	if os.Geteuid() != 0 {
		args = append([]string{"-n", name}, args...)
		name = "sudo"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// ErrNoTool is returned when this machine has neither iptables nor nft.
var ErrNoTool = errors.New("neither iptables nor nft is installed")

// iptables rules, as `iptables -C/-I DOCKER-USER` takes them.
var iptablesRules = [][]string{
	{"-i", Bridge, "-j", "ACCEPT"},
	{"-o", Bridge, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
}

// nft rules, the same two, for a machine with no iptables binary. Docker's
// iptables-nft writes to table ip filter, which is what these edit.
var nftRules = []string{
	`iifname "` + Bridge + `" counter accept`,
	`oifname "` + Bridge + `" ct state related,established counter accept`,
}

// Ensure adds the rules to DOCKER-USER when the chain exists and lacks them,
// and reports how many it added. No chain means no Docker firewall to get past,
// and nothing is done. It is safe to call often: when nothing is missing it
// costs one command.
func Ensure(ctx context.Context, run Run) (int, error) {
	if _, err := lookPath("iptables"); err == nil {
		return ensureIptables(ctx, run)
	}
	if _, err := lookPath("nft"); err == nil {
		return ensureNft(ctx, run)
	}
	return 0, ErrNoTool
}

// lookPath is exec.LookPath, replaced in tests.
var lookPath = exec.LookPath

func ensureIptables(ctx context.Context, run Run) (int, error) {
	// -S lists the chain's rules, and fails when there is no chain.
	out, err := run(ctx, "iptables", "-S", Chain)
	if err != nil {
		return 0, nil
	}
	added := 0
	for i := len(iptablesRules) - 1; i >= 0; i-- {
		rule := iptablesRules[i]
		if strings.Contains(string(out), "-A "+Chain+" "+strings.Join(rule, " ")) {
			continue
		}
		if out, err := run(ctx, "iptables", append([]string{"-I", Chain}, rule...)...); err != nil {
			return added, errors.New("adding a " + Chain + " rule: " + strings.TrimSpace(string(out)))
		}
		added++
	}
	return added, nil
}

func ensureNft(ctx context.Context, run Run) (int, error) {
	out, err := run(ctx, "nft", "list", "chain", "ip", "filter", Chain)
	if err != nil {
		return 0, nil
	}
	have := string(out)
	added := 0
	for i := len(nftRules) - 1; i >= 0; i-- {
		rule := nftRules[i]
		if strings.Contains(strings.ReplaceAll(have, " counter", ""), strings.ReplaceAll(rule, " counter", "")) {
			continue
		}
		if out, err := run(ctx, "nft", "insert", "rule", "ip", "filter", Chain, rule); err != nil {
			return added, errors.New("adding a " + Chain + " rule: " + strings.TrimSpace(string(out)))
		}
		added++
	}
	return added, nil
}

// Hint is what to tell someone whose image build couldn't reach the network:
// non-empty when Docker's firewall is the likely cause, a DOCKER-USER chain
// that doesn't accept the bridge.
func Hint(ctx context.Context, run Run) string {
	chain := ""
	if _, err := lookPath("iptables"); err == nil {
		out, err := run(ctx, "iptables", "-S", Chain)
		if err != nil {
			return ""
		}
		chain = string(out)
		if strings.Contains(chain, "-A "+Chain+" "+strings.Join(iptablesRules[0], " ")) {
			return ""
		}
	} else if _, err := lookPath("nft"); err == nil {
		out, err := run(ctx, "nft", "list", "chain", "ip", "filter", Chain)
		if err != nil {
			return ""
		}
		if strings.Contains(string(out), `iifname "`+Bridge+`"`) {
			return ""
		}
	} else {
		return ""
	}
	return "Docker's firewall (the FORWARD policy DROP and its " + Chain + " chain) is blocking " + Bridge + ", which is the likely reason downloads time out even though the container has an address and DNS. AgentBox adds the accept rules for " + Bridge + " itself; if it couldn't, run: sudo iptables -I " + Chain + " -i " + Bridge + " -j ACCEPT && sudo iptables -I " + Chain + " -o " + Bridge + " -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT"
}

// Watch makes sure of the rules now and then every interval, until ctx ends:
// Docker Desktop starting or restarting after AgentBox rebuilds its chains and
// drops them. logf says what it did; it isn't called when nothing changed.
func Watch(ctx context.Context, interval time.Duration, run Run, logf func(string, ...any)) {
	var last string
	check := func() {
		n, err := Ensure(ctx, run)
		msg := ""
		switch {
		case err != nil:
			msg = err.Error()
		case n > 0:
			logf("added %d %s rule(s) so Docker's firewall lets %s through", n, Chain, Bridge)
		}
		// A failure that doesn't change is logged once, not every interval.
		if msg != "" && msg != last && !errors.Is(err, ErrNoTool) {
			logf("%s: %v", Chain, err)
		}
		last = msg
	}
	check()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}
