package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Earlier releases could put every agent under one shared budget, a parent
// cgroup (oldBudgetCgroup) whose memory.max, memory.swap.max and cpu.max held
// the agents together, with memory.low on the host's user.slice and
// system.slice reserving the rest of the memory for the host's own apps. An
// earlier one still gave that budget disk controls and a memory.high, which
// froze the host rather than protecting it: on the user's host (LUKS over
// btrfs on a Kingston NV3), the write ceiling on io.max made btrfs transaction
// commits wait on the agents' throttled writeback, and io.cost's latency QoS
// slowed the whole disk for everyone. Nothing writes any of it now, but cgroup
// files keep what was written until the host reboots, or longer where cgroupfs
// isn't reset, and agents still running inside the budget would stay held by
// it. ResetLegacyBudget puts them back.

// ResetLegacyBudget turns off io.cost on every disk whose QoS is the one the
// earlier budget wrote, and, where the budget's cgroup is there, lifts every
// limit it had and the reserve it kept on the host's slices. It writes only
// what differs, so on a host that never had them it touches nothing. It
// reports what it reset.
func ResetLegacyBudget() (reset []string, err error) {
	var errs []error
	put := func(path, line, what string) {
		if werr := os.WriteFile(path, []byte(line), 0); werr != nil {
			errs = append(errs, fmt.Errorf("resetting %s: %w", what, werr))
			return
		}
		reset = append(reset, what)
	}
	root := filepath.Dir(OldBudgetDir)
	qos := filepath.Join(root, "io.cost.qos")
	for dev, kv := range nestedKeyed(readString(qos)) {
		if legacyQoS(kv) {
			put(qos, dev+" enable=0", "io.cost on "+dev)
		}
	}
	if _, err := os.Stat(OldBudgetDir); err != nil {
		return reset, errors.Join(errs...)
	}
	ioMax := filepath.Join(OldBudgetDir, "io.max")
	for dev, kv := range nestedKeyed(readString(ioMax)) {
		for _, v := range kv {
			if v != "max" {
				put(ioMax, dev+" rbps=max wbps=max riops=max wiops=max", "the budget's io.max on "+dev)
				break
			}
		}
	}
	weight := filepath.Join(OldBudgetDir, "io.weight")
	if w := strings.TrimSpace(readString(weight)); w != "" && w != "default 100" {
		put(weight, "default 100", "the budget's io.weight")
	}
	for _, name := range []string{"memory.high", "memory.max", "memory.swap.max"} {
		path := filepath.Join(OldBudgetDir, name)
		if v := strings.TrimSpace(readString(path)); v != "" && v != "max" {
			put(path, "max", "the budget's "+name)
		}
	}
	cpuMax := filepath.Join(OldBudgetDir, "cpu.max")
	if quota, _, _ := strings.Cut(strings.TrimSpace(readString(cpuMax)), " "); quota != "" && quota != "max" {
		put(cpuMax, "max 100000", "the budget's cpu.max")
	}
	for _, slice := range []string{"user.slice", "system.slice"} {
		path := filepath.Join(root, slice, "memory.low")
		if v := strings.TrimSpace(readString(path)); v != "" && v != "0" {
			put(path, "0", "the reserve on "+slice)
		}
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
