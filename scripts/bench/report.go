package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// hostInfo is what the JSON says about the machine: enough to tell one host's
// numbers from another's.
func hostInfo(probeDir string) map[string]any {
	h := map[string]any{"cpus": runtime.NumCPU()}
	var u syscall.Utsname
	if syscall.Uname(&u) == nil {
		h["kernel"] = cstr(u.Release[:])
	}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for line := range strings.SplitSeq(string(b), "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "model name" {
				h["cpu"] = strings.TrimSpace(v)
				break
			}
		}
	}
	if mi, err := readMeminfo(); err == nil {
		h["memGiB"] = gib(mi["MemTotal"])
		h["swapGiB"] = gib(mi["SwapTotal"])
	}
	fstype, source := mountOf(probeDir)
	h["probeFilesystem"] = fstype + " on " + source
	h["probeDir"] = probeDir
	var disks []string
	for _, d := range physicalDisks() {
		model, _ := os.ReadFile(filepath.Join("/sys/block", d, "device", "model"))
		disks = append(disks, strings.TrimSpace(d+" "+strings.TrimSpace(string(model))))
	}
	h["disks"] = disks
	crypt := false
	uuids, _ := filepath.Glob("/sys/block/dm-*/dm/uuid")
	for _, f := range uuids {
		if b, err := os.ReadFile(f); err == nil && strings.HasPrefix(string(b), "CRYPT-") {
			crypt = true
		}
	}
	h["dmCrypt"] = crypt
	return h
}

func cstr(b []int8) string {
	var s strings.Builder
	for _, c := range b {
		if c == 0 {
			break
		}
		s.WriteByte(byte(c))
	}
	return s.String()
}

// A row of the table: a label, and how to read a mode's cell.
type row struct {
	label string
	key   string // the metric, or the window for window rows
	cell  func(m *modeResult) (string, bool)
}

func secsRow(label, key string) row {
	return row{label, key, func(m *modeResult) (string, bool) {
		v, ok := m.Metrics[key]
		if !ok {
			return "", false
		}
		return fmtSecs(v), true
	}}
}

func numRow(label, key, format string) row {
	return row{label, key, func(m *modeResult) (string, bool) {
		v, ok := m.Metrics[key]
		if !ok {
			return "", false
		}
		return fmt.Sprintf(format, v), true
	}}
}

// memRow is the host's memory in use over the mode's baseline, and in
// brackets what the agents' cgroups or the VM's process hold.
func memRow(label, name string) row {
	return row{label, "mem." + name, func(m *modeResult) (string, bool) {
		d, ok := m.Metrics["mem."+name+".delta"]
		if !ok {
			return "", false
		}
		return fmt.Sprintf("%+.2f (%.2f)", d, m.Metrics["mem."+name+".held"]), true
	}}
}

func winRow(label, win string, cell func(w *window) string) row {
	return row{label, win, func(m *modeResult) (string, bool) {
		w := m.Windows[win]
		if w == nil {
			return "", false
		}
		return cell(w), true
	}}
}

func windowRows(win, what string) []row {
	return []row{
		winRow(what+": host fsync p50 / p95 / max, ms", win, func(w *window) string {
			if w.Fsync.Count == 0 {
				return "no probes"
			}
			return fmt.Sprintf("%.1f / %.1f / %.0f", w.Fsync.P50, w.Fsync.P95, w.Fsync.Max)
		}),
		winRow(what+": fsyncs over 100 ms / over 1 s", win, func(w *window) string {
			return fmt.Sprintf("%d / %d of %d", w.Fsync.Over100ms, w.Fsync.Over1s, w.Fsync.Count)
		}),
		winRow(what+": io pressure some, mean / worst second, %", win, func(w *window) string {
			return fmt.Sprintf("%.1f / %.0f", w.IOSomeAvg, w.IOSomeMax)
		}),
		winRow(what+": io pressure full, mean / worst second, %", win, func(w *window) string {
			return fmt.Sprintf("%.1f / %.0f", w.IOFullAvg, w.IOFullMax)
		}),
		winRow(what+": memory pressure some, mean / worst second, %", win, func(w *window) string {
			return fmt.Sprintf("%.1f / %.0f", w.MemSomeAvg, w.MemSomeMax)
		}),
		winRow(what+": memory pressure full, mean / worst second, %", win, func(w *window) string {
			return fmt.Sprintf("%.1f / %.0f", w.MemFullAvg, w.MemFullMax)
		}),
		winRow(what+": btrfs commits, mean / max ms", win, func(w *window) string {
			if w.Commits == 0 && w.CommitMaxMs == 0 {
				return "none"
			}
			return fmt.Sprintf("%d, %.0f / %.0f", w.Commits, w.CommitMeanMs, w.CommitMaxMs)
		}),
		winRow(what+": host disk written, GiB (worst MiB/s)", win, func(w *window) string {
			return fmt.Sprintf("%.1f (%.0f)", w.WriteMiB/1024, w.WriteMiBsMax)
		}),
		winRow(what+": host disk read, GiB", win, func(w *window) string {
			return fmt.Sprintf("%.1f", w.ReadMiB/1024)
		}),
		winRow(what+": page cache re-read after eviction (refaults), GiB: host / guest", win, func(w *window) string {
			if w.GuestRefaultGiB == nil {
				return fmt.Sprintf("%.1f / —", w.HostRefaultGiB)
			}
			return fmt.Sprintf("%.1f / %.1f", w.HostRefaultGiB, *w.GuestRefaultGiB)
		}),
		winRow(what+": guest memory pressure some / full, mean %", win, func(w *window) string {
			if w.GuestMemSome == nil {
				return "—"
			}
			return fmt.Sprintf("%.1f / %.1f", *w.GuestMemSome, *w.GuestMemFull)
		}),
	}
}

func tableRows(write float64, reclaim string) []row {
	rows := []row{
		numRow("Setup: steps from a fresh install", "setup.steps", "%.0f"),
		secsRow("Setup: time", "setup.total"),
		secsRow("VM boot, until agents can be made", "vm.boot"),
		secsRow("VM shut down", "vm.shutdown"),
		secsRow("VM pause", "vm.pause"),
		secsRow("VM resume, until it answers", "vm.resume"),
		secsRow("Agent create (median of 5)", "agent.create"),
		secsRow("Agent stop", "agent.stop"),
		secsRow("Agent start", "agent.start"),
		secsRow("Agent pause", "agent.pause"),
		secsRow("Agent resume", "agent.resume"),
		secsRow("Build, one agent: Go, cold", "build.go.cold"),
		secsRow("Build, one agent: Go, warm", "build.go.warm"),
		secsRow("Build, one agent: desktop, cold", "build.npm.cold"),
		secsRow("Build, one agent: desktop, warm", "build.npm.warm"),
		secsRow("Build, 3 agents at once, cold: wall time", "build3.wall"),
		memRow("Memory, GiB over baseline (held): idle", "idle"),
		memRow("Memory: 1 agent", "agents1"),
		memRow("Memory: 3 agents", "agents3"),
		memRow("Memory: 5 agents", "agents5"),
		numRow("Memory: held at most, 3 agents building", "mem.build3.heldMax", "(%.2f)"),
		memRow("Memory: every agent stopped", "stopped"),
		memRow("Memory: "+reclaim+" later", "stopped_later"),
		memRow("Memory: guest caches dropped", "caches_dropped"),
		memRow("Memory: VM shut down", "vm_off"),
	}
	rows = append(rows, windowRows("idle", "Idle host")...)
	rows = append(rows, secsRow(fmt.Sprintf("Sustained write, %.0f GiB in one agent: time", write), "write.seconds"),
		numRow("Sustained write: MiB/s", "write.mibs", "%.0f"))
	rows = append(rows, windowRows("write", "Sustained write, and its writeback")...)
	rows = append(rows, windowRows("build3", "3 agents building")...)
	return rows
}

// markdown is the results' one table: a row per measure, a column per mode,
// with what went wrong under it.
func markdown(res *results) string {
	var b strings.Builder
	write := 10.0
	if v, ok := res.Options["writeGiB"].(float64); ok {
		write = v
	}
	fmt.Fprintf(&b, "| Measure | %s |\n|---|%s\n", strings.Join(modeNames(res), " | "), strings.Repeat("---|", len(res.Modes)))
	var notes []string
	reclaim, _ := res.Options["reclaimWait"].(string)
	for _, r := range tableRows(write, reclaim) {
		cells := make([]string, len(res.Modes))
		any := false
		for i, m := range res.Modes {
			s, ok := r.cell(m)
			if ok {
				any = true
			} else {
				s = "—"
			}
			if n := m.Notes[r.key]; n != "" {
				notes = append(notes, fmt.Sprintf("%s, %s: %s", m.Mode, r.label, n))
				s += " ¹"
			}
			cells[i] = s
		}
		if any {
			fmt.Fprintf(&b, "| %s | %s |\n", r.label, strings.Join(cells, " | "))
		}
	}
	notes = dedupe(notes)
	for _, m := range res.Modes {
		for _, e := range m.Errors {
			notes = append(notes, m.Mode+": "+e)
		}
	}
	b.WriteString("\nTimes are medians where repeated. Memory is the host's in use (MemTotal − MemAvailable) over the mode's own baseline, taken before it set anything up; in brackets, what the agents' cgroups (containers) or the VM's process (VM modes) hold. Pressure is the share of each second some or all host tasks were stalled. The fsync probe writes 4 KiB and fsyncs it five times a second on the host's own filesystem.\n")
	if len(notes) > 0 {
		b.WriteString("\n¹ Notes and errors:\n\n")
		for _, n := range notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
	}
	return b.String()
}

func modeNames(res *results) []string {
	names := make([]string, len(res.Modes))
	for i, m := range res.Modes {
		names[i] = m.Mode
	}
	return names
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
