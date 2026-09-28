package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// An earlier AgentBox gave the shared budget disk controls, and a memory.high
// of 90% of its memory. The disk controls froze the host rather than
// protecting it: on the user's host (LUKS over btrfs on a Kingston NV3), the
// write ceiling on io.max made btrfs transaction commits wait on the agents'
// throttled writeback — commits of up to 11.5 s, user.slice stalled on IO
// 45–50% of the time — and io.cost's latency QoS, 5 ms for reads and a 5%
// floor, slowed the whole disk for everyone. The budget no longer writes any
// of them (budget.go), but a host that ran that version still has them set
// until it reboots, or longer where cgroupfs isn't reset. ResetLegacyBudget
// puts them back.

// ResetLegacyBudget turns off io.cost on every disk whose QoS is the one the
// earlier budget wrote, lifts the budget's io.max, and puts its io.weight and
// memory.high back to the kernel's defaults. It writes only what differs, so
// on a host that never had them it touches nothing. It reports what it reset.
func ResetLegacyBudget() (reset []string, err error) {
	var errs []error
	put := func(path, line, what string) {
		if werr := os.WriteFile(path, []byte(line), 0); werr != nil {
			errs = append(errs, fmt.Errorf("resetting %s: %w", what, werr))
			return
		}
		reset = append(reset, what)
	}
	qos := filepath.Join(filepath.Dir(BudgetDir), "io.cost.qos")
	for dev, kv := range nestedKeyed(readString(qos)) {
		if legacyQoS(kv) {
			put(qos, dev+" enable=0", "io.cost on "+dev)
		}
	}
	ioMax := filepath.Join(BudgetDir, "io.max")
	for dev, kv := range nestedKeyed(readString(ioMax)) {
		for _, v := range kv {
			if v != "max" {
				put(ioMax, dev+" rbps=max wbps=max riops=max wiops=max", "the budget's io.max on "+dev)
				break
			}
		}
	}
	weight := filepath.Join(BudgetDir, "io.weight")
	if w := strings.TrimSpace(readString(weight)); w != "" && w != "default 100" {
		put(weight, "default 100", "the budget's io.weight")
	}
	high := filepath.Join(BudgetDir, "memory.high")
	if h := strings.TrimSpace(readString(high)); h != "" && h != "max" {
		put(high, "max", "the budget's memory.high")
	}
	return reset, errors.Join(errs...)
}

// legacyQoS says whether a disk's io.cost.qos is on with what the earlier
// budget wrote — its SSD or its hard disk targets — rather than anything the
// host set itself.
func legacyQoS(kv map[string]string) bool {
	num := func(k string) float64 {
		f, _ := strconv.ParseFloat(kv[k], 64)
		return f
	}
	if kv["enable"] != "1" || kv["ctrl"] != "user" || num("rpct") != 95 || num("wpct") != 95 || num("min") != 5 || num("max") != 150 {
		return false
	}
	rlat, wlat := num("rlat"), num("wlat")
	return (rlat == 5000 && wlat == 10000) || (rlat == 75000 && wlat == 150000)
}

// nestedKeyed reads a nested-keyed cgroup file, one device a line:
//
//	253:0 rbps=max wbps=67108864 riops=max wiops=max
func nestedKeyed(file string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, line := range strings.Split(file, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(fields[0], ":") {
			continue
		}
		kv := map[string]string{}
		for _, f := range fields[1:] {
			if k, v, ok := strings.Cut(f, "="); ok {
				kv[k] = v
			}
		}
		out[fields[0]] = kv
	}
	return out
}

func readString(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}
