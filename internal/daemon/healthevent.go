package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/hostos"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// The health event: how this installation's VM is holding up, sent with the
// usage events (same opt-in, same endpoint) once a day and after a
// memory-short episode. Every field is a number, a yes or no, or a value from
// a fixed list, with nothing that names anything: no paths, projects, repos,
// branches, hosts. The daemon's log is read the same way: each line it
// writes is matched against fixed patterns (logClasses) and only the count
// per class leaves.
//
// Each field, its range and whether it may be left out are listed in
// healthevent_test.go, which fails on a field added here without being
// listed; the landing site's events.ts takes the same list. Until it does,
// a batch with a health event in it is refused whole, so health events are
// sent in batches of their own (eventBatch).

const eventHealth = "health"

// The triggers of a health event.
const (
	healthDaily   = "daily"
	healthEpisode = "episode"
)

// settingUsageHealth is the day the last daily health event was for.
const settingUsageHealth = "usage_health_day"

// healthEpisodeEvery is the least time between two health events sent for
// episodes: a VM going in and out of pressure all afternoon sends one an hour.
const healthEpisodeEvery = time.Hour

// healthEvent is a health event's fields. Pointers are what some front ends
// can't measure, left out when unknown. Errors is a count per logClasses
// class, sent flat as err_<class>.
type healthEvent struct {
	Trigger  string `json:"trigger"`
	Period   string `json:"period"`
	FrontEnd string `json:"front_end"`
	VMMemory string `json:"vm_memory"`
	VMUptime string `json:"vm_uptime"`
	VMBootS  *int64 `json:"vm_boot_s,omitempty"`

	MemAvailablePct int64  `json:"mem_available_pct"`
	MemAnonPct      int64  `json:"mem_anon_pct"`
	MemFilePct      int64  `json:"mem_file_pct"`
	MemKernelPct    int64  `json:"mem_kernel_pct"`
	SwapSizePct     int64  `json:"swap_size_pct"`
	SwapUsedPct     int64  `json:"swap_used_pct"`
	ZramSizePct     *int64 `json:"zram_size_pct,omitempty"`
	ZramRatioPct    *int64 `json:"zram_ratio_pct,omitempty"`

	Episodes       int64  `json:"episodes"`
	EpisodeS       int64  `json:"episode_s"`
	EpisodeMaxS    int64  `json:"episode_max_s"`
	PSIPeakPct     *int64 `json:"psi_peak_pct,omitempty"`
	PSIFullPct     *int64 `json:"psi_full_peak_pct,omitempty"`
	RunPauses      int64  `json:"run_pauses"`
	MachineFreezes int64  `json:"machine_freezes"`
	IdleFreezes    int64  `json:"idle_freezes"`
	IdleStops      int64  `json:"idle_stops"`
	OOMKills       int64  `json:"oom_kills"`

	DaemonRSSMiB  int64 `json:"daemon_rss_mib"`
	DaemonPeakMiB int64 `json:"daemon_peak_mib"`

	ShareFDPct     *int64 `json:"share_fd_pct,omitempty"`
	ShareFDPeakPct *int64 `json:"share_fd_peak_pct,omitempty"`

	AgentsBusy int64 `json:"agents_busy"`
	AgentsIdle int64 `json:"agents_idle"`

	Errors map[string]int64 `json:"-"`
}

// MarshalJSON is the fields, flat, with an err_<class> for every class.
func (e healthEvent) MarshalJSON() ([]byte, error) {
	type plain healthEvent
	raw, err := json.Marshal(plain(e))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	for _, c := range logClasses {
		out["err_"+c.class] = min(e.Errors[c.class], maxHealthCount)
	}
	out["err_"+classOther] = min(e.Errors[classOther], maxHealthCount)
	return json.Marshal(out)
}

// maxHealthCount is the most any count says, as the server takes it.
const maxHealthCount = 100_000

// maxHealthSeconds is the longest a duration says, a week.
const maxHealthSeconds = int64(7 * 24 * time.Hour / time.Second)

// healthStats is what the daemon counts between two health events, under mu.
type healthStats struct {
	mu    sync.Mutex
	since time.Time // the last event, or the daemon's start

	episodes, episodeS, episodeMaxS int64
	episodeAt                       time.Time // the episode going on, zero for none
	lastEpisodeEvent                time.Time

	psiRead          bool
	psiSome, psiFull float64
	runPauses        int64
	machineFreezes   int64
	idleFreezes      int64
	idleStops        int64
	oomKills         int64
	sharePeak        float64
	errors           map[string]int64
}

// healthStart is this process's start, as the kernel's uptime then: how long
// after the VM booted the daemon came up.
var healthStart = readUptime()

func (h *healthStats) lock() {
	h.mu.Lock()
	if h.since.IsZero() {
		h.since = time.Now()
	}
}

// count adds n to one of the counts.
func (h *healthStats) count(field *int64, n int64) {
	h.lock()
	*field += n
	h.mu.Unlock()
}

func (s *Server) countRunPause()      { s.health.count(&s.health.runPauses, 1) }
func (s *Server) countMachineFreeze() { s.health.count(&s.health.machineFreezes, 1) }
func (s *Server) countIdleFreeze()    { s.health.count(&s.health.idleFreezes, 1) }
func (s *Server) countIdleStop()      { s.health.count(&s.health.idleStops, 1) }
func (s *Server) countOOMKills(n int64) {
	s.health.count(&s.health.oomKills, n)
}

// notePressure is the pressure loop's reading: its peak, and whether memory
// is short, by why the governor holds runs back. An episode that ends has a
// health event recorded, aside, unless one was lately.
func (s *Server) notePressure(now time.Time, sample pressure.Sample, psiOK bool, why pressure.Reason) {
	short := why == pressure.Stalling || why == pressure.Falling || why == pressure.Churning || why == pressure.Low
	h := &s.health
	h.lock()
	if psiOK {
		h.psiRead = true
		h.psiSome = max(h.psiSome, sample.Some.Avg10)
		h.psiFull = max(h.psiFull, sample.Full.Avg10)
	}
	ended := false
	switch {
	case short && h.episodeAt.IsZero():
		h.episodeAt = now
		h.episodes++
	case !short && !h.episodeAt.IsZero():
		d := int64(now.Sub(h.episodeAt) / time.Second)
		h.episodeS += d
		h.episodeMaxS = max(h.episodeMaxS, d)
		h.episodeAt = time.Time{}
		ended = now.Sub(h.lastEpisodeEvent) >= healthEpisodeEvery
		if ended {
			h.lastEpisodeEvent = now
		}
	}
	h.mu.Unlock()
	if ended {
		go s.recordHealth(s.background(), healthEpisode)
	}
}

// noteShare keeps the shared home's busiest moment.
func (s *Server) noteShare(open, limit int64) {
	if limit <= 0 {
		return
	}
	h := &s.health
	h.lock()
	h.sharePeak = max(h.sharePeak, float64(open)/float64(limit))
	h.mu.Unlock()
}

// noteLog counts a line of the daemon's log under its class, when it is an
// error.
func (s *Server) noteLog(line string) {
	class := logClass(line)
	if class == "" {
		return
	}
	h := &s.health
	h.lock()
	if h.errors == nil {
		h.errors = map[string]int64{}
	}
	h.errors[class]++
	h.mu.Unlock()
}

// logClasses are the errors the daemon's log is counted by, in the order
// they are tried. A line that matches none, and reads as an error, is
// classOther; any other line isn't counted. The class names are what is
// sent, as err_<class>: changing one changes the event.
var logClasses = []struct {
	class string
	words []string
}{
	{"too_many_files", []string{"too many open files", "emfile", "enfile"}},
	{"no_space", []string{"no space left", "enospc", "disk quota exceeded", "disk is full"}},
	{"out_of_memory", []string{"out of memory", "oom-kill", "oom_kill", "killed for memory", "cannot allocate memory"}},
	{"git_credential", []string{"could not read username", "could not read password", "terminal prompts disabled", "authentication failed for", "invalid username or password", "credential helper", "git credential", "permission denied (publickey"}},
	{"git_lock", []string{"index.lock", ".lock': file exists", "unable to create '"}},
	{"database_locked", []string{"database is locked", "sqlite_busy"}},
	{"incus_down", []string{"incus isn't answering", "incus.socket", "unix.socket: connect"}},
	{"acp_closed", []string{"acp connection closed", acpClosedText}},
	{"auth", []string{"unauthorized", "unauthenticated", "bad credentials", "invalid api key", "not logged in", "token expired", "forbidden"}},
	{"network", []string{"dial tcp", "dial udp", "no such host", "connection refused", "connection reset", "network is unreachable", "no route to host", "tls handshake", "could not resolve host", "i/o timeout"}},
	{"timeout", []string{"deadline exceeded", "timed out", "timeout"}},
	{"killed", []string{"signal: killed", "exit status 137", "signal: terminated"}},
	{"permission", []string{"permission denied", "operation not permitted"}},
	{"not_found", []string{"not found", "no such file", "does not exist", "doesn't exist"}},
}

// logErrorWords make a line no class matched an error all the same.
var logErrorWords = []string{"error", "fail", "can't", "cannot", "couldn't", "unable", "panic"}

// logClass is the class a line of the log counts under, "" for none.
func logClass(line string) string {
	lower := strings.ToLower(line)
	for _, c := range logClasses {
		for _, w := range c.words {
			if strings.Contains(lower, w) {
				return c.class
			}
		}
	}
	for _, w := range logErrorWords {
		if strings.Contains(lower, w) {
			return classOther
		}
	}
	return ""
}

// recordHealth keeps a health event, and starts the counts over. The daily
// one is kept once a day.
func (s *Server) recordHealth(ctx context.Context, trigger string) {
	if !s.usageStatsOn(ctx) {
		return
	}
	if trigger == healthDaily {
		today := usageDay(time.Now())
		if last, err := s.store.Setting(ctx, settingUsageHealth); err != nil || last == today {
			return
		}
		if err := s.store.SetSetting(ctx, settingUsageHealth, today); err != nil {
			return
		}
	}
	s.recordEvent(eventHealth, s.healthNow(ctx, trigger, time.Now()))
}

// healthNow is the health event now, taking the counts since the last one.
func (s *Server) healthNow(ctx context.Context, trigger string, now time.Time) healthEvent {
	e := healthEvent{Trigger: trigger, FrontEnd: healthFrontEnd(hostos.OS(), agent.VenusVM())}

	mem := readMemInfo()
	e.VMMemory = usageMemory(mem["MemTotal"])
	if total := mem["MemTotal"]; total > 0 {
		anon := mem["AnonPages"] + mem["Shmem"]
		file := mem["Active(file)"] + mem["Inactive(file)"]
		kernel := max(total-mem["MemFree"]-anon-file, 0)
		e.MemAvailablePct = pct(mem["MemAvailable"], total, 100)
		e.MemAnonPct = pct(anon, total, 100)
		e.MemFilePct = pct(file, total, 100)
		e.MemKernelPct = pct(kernel, total, 100)
		e.SwapSizePct = pct(mem["SwapTotal"], total, 1000)
		if z, ok := agent.Zram(); ok {
			e.ZramSizePct = ptr(pct(z.DiskSize, total, 1000))
			if z.RealBytes > 0 {
				e.ZramRatioPct = ptr(pct(z.SwapBytes, z.RealBytes, 1000))
			}
		}
	}
	if swap := mem["SwapTotal"]; swap > 0 {
		e.SwapUsedPct = pct(swap-mem["SwapFree"], swap, 100)
	}

	if up := readUptime(); up > 0 {
		e.VMUptime = usageSpan(time.Duration(up) * time.Second)
	} else {
		e.VMUptime = usageSpan(0)
	}
	// Only a daemon that came up with the VM says how long that took; one
	// restarted later would say how long it ran before.
	if hostos.InVM() && healthStart > 0 && healthStart < 600 {
		e.VMBootS = ptr(healthStart)
	}

	rss, peak := daemonMemory()
	e.DaemonRSSMiB, e.DaemonPeakMiB = min(rss>>20, 1<<20), min(peak>>20, 1<<20)

	if r := s.shareLast(); r != nil && r.Limit > 0 {
		s.noteShare(r.Open, r.Limit)
		e.ShareFDPct = ptr(pct(r.Open, r.Limit, 100))
	}

	if agents, err := s.store.Agents(ctx, ""); err == nil {
		for _, a := range agents {
			if a.IsLead() || a.Status != state.AgentReady {
				continue
			}
			if s.chatBusy(a.Ref()) || s.heavyHeld(a.Ref()) {
				e.AgentsBusy++
			} else {
				e.AgentsIdle++
			}
		}
		e.AgentsBusy, e.AgentsIdle = min(e.AgentsBusy, maxHealthCount), min(e.AgentsIdle, maxHealthCount)
	}

	h := &s.health
	h.lock()
	e.Period = usageSpan(now.Sub(h.since))
	e.Episodes = min(h.episodes, maxHealthCount)
	e.EpisodeS, e.EpisodeMaxS = h.episodeS, h.episodeMaxS
	if !h.episodeAt.IsZero() {
		// One going on counts up to now, and goes on in the next.
		d := int64(now.Sub(h.episodeAt) / time.Second)
		e.EpisodeS += d
		e.EpisodeMaxS = max(e.EpisodeMaxS, d)
	}
	e.EpisodeS, e.EpisodeMaxS = min(e.EpisodeS, maxHealthSeconds), min(e.EpisodeMaxS, maxHealthSeconds)
	if h.psiRead {
		e.PSIPeakPct = ptr(min(int64(h.psiSome+0.5), 100))
		e.PSIFullPct = ptr(min(int64(h.psiFull+0.5), 100))
	}
	e.RunPauses = min(h.runPauses, maxHealthCount)
	e.MachineFreezes = min(h.machineFreezes, maxHealthCount)
	e.IdleFreezes = min(h.idleFreezes, maxHealthCount)
	e.IdleStops = min(h.idleStops, maxHealthCount)
	e.OOMKills = min(h.oomKills, maxHealthCount)
	if e.ShareFDPct != nil {
		e.ShareFDPeakPct = ptr(max(*e.ShareFDPct, min(int64(h.sharePeak*100+0.5), 100)))
	}
	e.Errors = h.errors
	h.reset(now)
	h.mu.Unlock()
	return e
}

// reset starts the counts over, at now. Under mu. An episode going on
// carries on into the next event's counts.
func (h *healthStats) reset(now time.Time) {
	h.since = now
	h.episodes, h.episodeS, h.episodeMaxS = 0, 0, 0
	if !h.episodeAt.IsZero() {
		h.episodeAt, h.episodes = now, 1
	}
	h.psiRead, h.psiSome, h.psiFull = false, 0, 0
	h.runPauses, h.machineFreezes, h.idleFreezes, h.idleStops, h.oomKills = 0, 0, 0, 0, 0
	h.sharePeak = 0
	h.errors = nil
}

// healthFrontEnd is usageFrontEnd, telling a Mac's krunkit VM from its vz
// one: only krunkit's has the GPU's script (agent.VenusVM).
func healthFrontEnd(os string, venus bool) string {
	fe := usageFrontEnd(os)
	if fe != "lima" {
		return fe
	}
	if venus {
		return "lima_krunkit"
	}
	return "lima_vz"
}

// pct is n's share of of, in percent, from 0 to most.
func pct(n, of, most int64) int64 {
	if of <= 0 || n <= 0 {
		return 0
	}
	return min(int64(float64(n)*100/float64(of)+0.5), most)
}

// readMemInfo is /proc/meminfo, in bytes, by key; empty when unreadable.
func readMemInfo() map[string]int64 {
	out := map[string]int64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
		if err == nil {
			out[key] = kb << 10
		}
	}
	return out
}

// readUptime is how long this machine has been up, in whole seconds; 0
// unknown.
func readUptime() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	first, _, _ := strings.Cut(string(b), " ")
	up, err := strconv.ParseFloat(first, 64)
	if err != nil {
		return 0
	}
	return int64(up)
}

// daemonMemory is the daemon's resident memory now and at its most, in
// bytes; 0 unknown.
func daemonMemory() (rss, peak int64) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok || (key != "VmRSS" && key != "VmHWM") {
			continue
		}
		kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
		if err != nil {
			continue
		}
		if key == "VmRSS" {
			rss = kb << 10
		} else {
			peak = kb << 10
		}
	}
	return rss, peak
}

// fleetWhyNow is why the fleet (agentfreeze.go) holds machines back now.
func (s *Server) fleetWhyNow() pressure.Reason {
	s.frozen.mu.Lock()
	defer s.frozen.mu.Unlock()
	return s.frozen.f.Why()
}
