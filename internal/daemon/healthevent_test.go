package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// healthField is one field of the health event as the landing site's
// events.ts takes it: an enum's values, or an int's range, and whether it
// may be left out.
type healthField struct {
	values   []string // an enum's; nil for an int
	min, max int64
	optional bool
	doc      string
}

func healthEnum(doc string, values ...string) healthField {
	return healthField{values: values, doc: doc}
}

func healthInt(doc string, lo, hi int64) healthField { return healthField{min: lo, max: hi, doc: doc} }

func healthOptional(f healthField) healthField { f.optional = true; return f }

var healthSpans = []string{"lt_1m", "1m_5m", "5m_30m", "30m_2h", "2h_1d", "1d_1w", "gt_1w"}

// healthFields is every field of the health event: what it says, its type
// and range, and whether a front end may leave it out. A field added to
// healthEvent, or a class to logClasses, fails TestHealthFieldsAreAllListed
// until it is listed here (and in events.ts).
var healthFields = func() map[string]healthField {
	count := func(doc string) healthField { return healthInt(doc, 0, maxHealthCount) }
	pctOf := func(doc string) healthField { return healthInt(doc, 0, 100) }
	f := map[string]healthField{
		"trigger":           healthEnum("what sent it: the day's, or a memory-short episode that ended", healthDaily, healthEpisode),
		"period":            healthEnum("how long the counts below cover: since the last health event, or the daemon's start", healthSpans...),
		"front_end":         healthEnum("what runs AgentBox's VM", "chv", "lima", "lima_krunkit", "lima_vz", "wsl", "host", "other"),
		"vm_memory":         healthEnum("the VM's memory, bucketed", "lt_8g", "8_16g", "16_32g", "32_64g", "64g_plus", "unknown"),
		"vm_uptime":         healthEnum("how long the VM has been up", healthSpans...),
		"vm_boot_s":         healthOptional(healthInt("seconds from the VM's boot to the daemon's start; only from a daemon that started with the VM", 0, 600)),
		"mem_available_pct": pctOf("MemAvailable, % of the VM's memory"),
		"mem_anon_pct":      pctOf("anonymous memory and shmem (what only swap can free), % of the VM's memory"),
		"mem_file_pct":      pctOf("page cache on the file LRU lists, % of the VM's memory"),
		"mem_kernel_pct":    pctOf("the rest in use: kernel, slab, page tables, % of the VM's memory"),
		"swap_size_pct":     healthInt("swap's size, % of the VM's memory", 0, 1000),
		"swap_used_pct":     pctOf("swap in use, % of swap; 0 with none"),
		"zram_size_pct":     healthOptional(healthInt("zram devices' size, % of the VM's memory; left out with no zram (WSL, a host install)", 0, 1000)),
		"zram_ratio_pct":    healthOptional(healthInt("zram's compression: what it holds over the RAM that costs, x100; left out with no zram or nothing in it", 0, 1000)),
		"episodes":          count("memory-short episodes (the governor stalling, falling, churning or low) begun in the period"),
		"episode_s":         healthInt("their total length, seconds", 0, maxHealthSeconds),
		"episode_max_s":     healthInt("the longest, seconds", 0, maxHealthSeconds),
		"psi_peak_pct":      healthOptional(healthInt("memory PSI 'some' avg10 at its peak; left out where PSI can't be read", 0, 100)),
		"psi_full_peak_pct": healthOptional(healthInt("memory PSI 'full' avg10 at its peak; left out where PSI can't be read", 0, 100)),
		"run_pauses":        count("heavy commands paused for memory"),
		"machine_freezes":   count("whole agents' machines frozen for memory (experimental)"),
		"idle_freezes":      count("idle agents frozen by idle reclaim"),
		"idle_stops":        count("finished idle agents' machines stopped by idle reclaim"),
		"oom_kills":         count("agents' processes killed for memory"),
		"daemon_rss_mib":    healthInt("the daemon's resident memory now, MiB", 0, 1<<20),
		"daemon_peak_mib":   healthInt("the daemon's resident memory at its most (VmHWM), MiB", 0, 1<<20),
		"share_fd_pct":      healthOptional(pctOf("files virtiofsd has open on the shared home, % of its limit; left out where there is no virtiofsd (Lima, WSL) or no report")),
		"share_fd_peak_pct": healthOptional(pctOf("the same at its peak in the period")),
		"agents_busy":       count("agents whose chat is working, or with heavy commands"),
		"agents_idle":       count("agents ready and doing nothing"),
		"err_" + classOther: count("lines of the daemon's log that read as an error and match no class below"),
	}
	docs := map[string]string{
		"too_many_files":  "out of file descriptors (EMFILE, ENFILE)",
		"no_space":        "a disk full",
		"out_of_memory":   "memory exhausted or processes killed for it",
		"git_credential":  "git asking for credentials it doesn't have",
		"git_lock":        "a git lock file in the way",
		"database_locked": "SQLite busy",
		"incus_down":      "Incus not answering",
		"acp_closed":      "an AI tool's adapter closing its connection",
		"auth":            "an API refusing the credentials",
		"network":         "a connection failing",
		"timeout":         "a deadline passing",
		"killed":          "a process killed by a signal",
		"permission":      "permission denied",
		"not_found":       "a file or thing not found",
	}
	for class, doc := range docs {
		f["err_"+class] = count("lines of the daemon's log for " + doc)
	}
	return f
}()

// check says what's wrong with v as f's value, "" if nothing.
func (f healthField) check(v any) string {
	if f.values != nil {
		s, ok := v.(string)
		if !ok || !slices.Contains(f.values, s) {
			return fmt.Sprintf("%v isn't one of %v", v, f.values)
		}
		return ""
	}
	n, ok := v.(float64)
	if !ok || n != float64(int64(n)) || int64(n) < f.min || int64(n) > f.max {
		return fmt.Sprintf("%v isn't a whole number from %d to %d", v, f.min, f.max)
	}
	return ""
}

func healthProps(t *testing.T, e healthEvent) map[string]any {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHealthFieldsAreAllListed(t *testing.T) {
	t.Parallel()
	n := int64(1)
	full := healthEvent{
		Trigger: healthDaily, Period: "lt_1m", FrontEnd: "chv", VMMemory: "unknown", VMUptime: "lt_1m",
		VMBootS: &n, ZramSizePct: &n, ZramRatioPct: &n, PSIPeakPct: &n, PSIFullPct: &n, ShareFDPct: &n, ShareFDPeakPct: &n,
	}
	props := healthProps(t, full)
	listed := slices.Sorted(maps.Keys(healthFields))
	if got := slices.Sorted(maps.Keys(props)); !slices.Equal(got, listed) {
		t.Fatalf("the health event's fields differ from those listed:\n sent   %v\n listed %v", got, listed)
	}
	var required []string
	for k, f := range healthFields {
		if !f.optional {
			required = append(required, k)
		}
		if f.doc == "" {
			t.Errorf("%s says nothing of itself", k)
		}
	}
	slices.Sort(required)
	full.VMBootS, full.ZramSizePct, full.ZramRatioPct, full.PSIPeakPct, full.PSIFullPct, full.ShareFDPct, full.ShareFDPeakPct = nil, nil, nil, nil, nil, nil, nil
	if got := slices.Sorted(maps.Keys(healthProps(t, full))); !slices.Equal(got, required) {
		t.Errorf("with nothing optional, sent %v, want %v", got, required)
	}
}

func TestHealthEventIsWithinItsRanges(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if err := d.srv.store.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.AddAgent(ctx, state.Agent{Project: "p", Name: "a", Status: state.AgentReady}); err != nil {
		t.Fatal(err)
	}
	d.srv.noteLog("git fetch: too many open files")
	d.srv.noteLog("syncing /home/someone/secret-repo failed: exit status 1")
	d.srv.noteLog("zram swap: on, 4.0G")
	for range maxHealthCount + 5 {
		d.srv.countRunPause()
	}
	d.srv.noteShare(9_000, 10_000)
	e := d.srv.healthNow(ctx, healthEpisode, time.Now())
	props := healthProps(t, e)
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "someone") {
		t.Errorf("the event holds a name: %s", raw)
	}
	for k, v := range props {
		f, ok := healthFields[k]
		if !ok {
			t.Errorf("%s isn't listed", k)
			continue
		}
		if problem := f.check(v); problem != "" {
			t.Errorf("%s: %s", k, problem)
		}
	}
	for k, want := range map[string]any{
		"trigger": healthEpisode, "err_too_many_files": float64(1), "run_pauses": float64(maxHealthCount),
		"agents_idle": float64(1), "agents_busy": float64(0),
	} {
		if props[k] != want {
			t.Errorf("%s = %v, want %v", k, props[k], want)
		}
	}
	if props["err_other"].(float64) < 1 {
		t.Errorf("err_other = %v", props["err_other"])
	}
	// The counts start over.
	if again := healthProps(t, d.srv.healthNow(ctx, healthDaily, time.Now())); again["run_pauses"] != float64(0) || again["err_too_many_files"] != float64(0) {
		t.Errorf("the next event = %v", again)
	}
}

func TestLogLinesCountUnderFixedClasses(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]string{
		"git fetch origin: fatal: could not read Username for 'https://github.com': terminal prompts disabled": "git_credential",
		"creating worktree: open /x: too many open files":                                                      "too_many_files",
		"memory: acme/agent-3: `node` (pid 4) was killed for memory":                                           "out_of_memory",
		"state: database is locked":                                                                            "database_locked",
		"fatal: Unable to create '/w/.git/index.lock': File exists":                                            "git_lock",
		"sampling usage: incus isn't answering":                                                                "incus_down",
		"base sync for acme: something odd failed":                                                             classOther,
		"zram swap: on": "",
		"memory pressure: resuming acme/a/k (go test ./...)":      "",
		"shared home: virtiofsd has room again: git work goes on": "",
	} {
		if got := logClass(line); got != want {
			t.Errorf("logClass(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestHealthCountsMemoryEpisodes(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	now := time.Now()
	s := pressure.Sample{}
	s.Some.Avg10, s.Full.Avg10 = 42.4, 7.6
	d.srv.health.lastEpisodeEvent = now // no event of its own, which wants the stats on
	d.srv.notePressure(now, s, true, pressure.Stalling)
	d.srv.notePressure(now.Add(time.Minute), pressure.Sample{}, true, pressure.Low)
	d.srv.notePressure(now.Add(90*time.Second), pressure.Sample{}, true, pressure.Easing)
	d.srv.notePressure(now.Add(2*time.Minute), pressure.Sample{}, true, pressure.Falling)
	e := d.srv.healthNow(context.Background(), healthDaily, now.Add(3*time.Minute))
	if e.Episodes != 2 || e.EpisodeS != 90+60 || e.EpisodeMaxS != 90 || *e.PSIPeakPct != 42 || *e.PSIFullPct != 8 {
		t.Errorf("episodes %d, %ds, longest %ds, psi %d/%d", e.Episodes, e.EpisodeS, e.EpisodeMaxS, *e.PSIPeakPct, *e.PSIFullPct)
	}
	// The one going on carries on into the next.
	if next := d.srv.healthNow(context.Background(), healthDaily, now.Add(4*time.Minute)); next.Episodes != 1 || next.EpisodeS != 60 || next.PSIPeakPct != nil {
		t.Errorf("next: episodes %d, %ds, psi %v", next.Episodes, next.EpisodeS, next.PSIPeakPct)
	}
}

func TestHealthEventsGoInBatchesOfTheirOwn(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	_ = d.srv.store.ForgetUsageEvents(ctx, 0, "")
	for _, name := range []string{eventError, eventTurn, eventHealth, eventHealth, eventError} {
		if err := d.srv.store.AddUsageEvent(ctx, state.UsageEvent{ID: name, Day: usageDay(time.Now()), Name: name, Props: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	var batches [][]string
	for range 5 {
		events, last, err := d.srv.pendingEvents(ctx)
		if err != nil || len(events) == 0 {
			break
		}
		var names []string
		for _, e := range events {
			names = append(names, e.Name)
		}
		batches = append(batches, names)
		_ = d.srv.store.ForgetUsageEvents(ctx, last, "")
	}
	want := [][]string{{eventError, eventTurn}, {eventHealth, eventHealth}, {eventError}}
	if fmt.Sprint(batches) != fmt.Sprint(want) {
		t.Errorf("batches = %v, want %v", batches, want)
	}
}
