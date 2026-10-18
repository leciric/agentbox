package daemon

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/pressure"
	"agentbox/internal/trace"
)

// Diagnostics: local performance tracing (package trace), modelled on T3
// Code's. The daemon writes a span for every API request, job, Incus call,
// git command, ACP turn and SQLite transaction to spans.ndjson next to
// state.db, and the stalls among them — a request answered after 2s, a
// transaction waiting past 1s for SQLite's lock, the VM's memory or CPU
// stalling — to stalls.ndjson as well, which lasts for days. The usage loop's
// samples are kept for an hour in memory. Nothing leaves the machine.

// traceAPI wraps one of the daemon's APIs in a span per request, named after
// its route ("GET /v1/agents/{project}/{agent}"), prefixed by which API it is
// unless it's the main one. On the main API, the app's, a request answered
// after trace.SlowRequest is a stall, and so is one still unanswered after
// unansweredAfter, when it is noted while it hangs; the agents' and leads'
// APIs have requests that wait by design (a heavy command's turn, a
// question), and only get spans.
func traceAPI(name string, mux *http.ServeMux, stalls bool) http.Handler {
	return traceAPIAfter(name, mux, stalls, trace.SlowRequest, unansweredAfter)
}

// traceAPIAfter is traceAPI with its thresholds given, for tests.
func traceAPIAfter(name string, mux *http.ServeMux, stalls bool, slow, unanswered time.Duration) http.Handler {
	prefix := ""
	if name != "" {
		prefix = name + ": "
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if trace.Default() == nil {
			mux.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		if pattern == "" {
			pattern = r.Method + " (no route)"
		}
		ctx, span := trace.Start(r.Context(), prefix+pattern)
		tw := &tracedWriter{ResponseWriter: w, start: time.Now()}
		watch := stalls && !waitsByDesign[pattern]
		var hung *time.Timer
		if watch {
			hung = time.AfterFunc(unanswered, func() {
				if !tw.answered() {
					trace.Record(trace.Span{
						Name: prefix + pattern, Start: tw.start, DurationMS: trace.MS(time.Since(tw.start)),
						Level: api.SpanLevelWarn, Attrs: map[string]any{"stall": "unanswered", "path": r.URL.Path},
					})
				}
			})
		}
		defer func() {
			if hung != nil {
				hung.Stop()
			}
			status := tw.status
			if p := recover(); p != nil {
				span.Set("status", http.StatusInternalServerError)
				span.End(fmt.Errorf("panic: %v", p))
				panic(p)
			}
			if status == 0 {
				status = http.StatusOK
			}
			span.Set("status", status)
			wait := tw.firstByte()
			if watch && wait > slow {
				span.Warn("slow request")
				span.Set("waitMs", trace.MS(wait), "path", r.URL.Path)
			}
			var err error
			if status >= 500 {
				err = errors.New(http.StatusText(status))
			}
			span.End(err)
		}()
		mux.ServeHTTP(tw, r.WithContext(ctx))
	})
}

// unansweredAfter is how long a request on the main API may go unanswered
// before a stall says so while it still hangs.
const unansweredAfter = 30 * time.Second

// waitsByDesign are the main API's routes that wait for a person before
// answering, which no stall should be made of.
var waitsByDesign = map[string]bool{
	"POST /v1/agents/{project}/{agent}/browser/pick": true,
}

// tracedWriter notes a response's status and when its first byte was
// written: the time a stream (events, a job's log) took to answer, rather
// than how long it stayed open.
type tracedWriter struct {
	http.ResponseWriter
	start  time.Time
	mu     sync.Mutex
	status int
	first  time.Duration
}

func (w *tracedWriter) answer(status int) {
	w.mu.Lock()
	if w.first == 0 {
		w.first = max(time.Since(w.start), 1)
		w.status = status
	}
	w.mu.Unlock()
}

func (w *tracedWriter) answered() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.first != 0
}

// firstByte is how long the first byte took, or the whole request when none
// was written.
func (w *tracedWriter) firstByte() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.first == 0 {
		return time.Since(w.start)
	}
	return w.first
}

func (w *tracedWriter) WriteHeader(status int) {
	w.answer(status)
	w.ResponseWriter.WriteHeader(status)
}

func (w *tracedWriter) Write(b []byte) (int, error) {
	w.answer(http.StatusOK)
	return w.ResponseWriter.Write(b)
}

func (w *tracedWriter) Flush() {
	w.answer(http.StatusOK)
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack is a WebSocket's (a terminal, a browser view): it has answered.
func (w *tracedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.answer(http.StatusSwitchingProtocols)
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking isn't supported on this connection")
	}
	return h.Hijack()
}

func (w *tracedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// startTracing opens the span files and installs the recorder, unless the
// daemon was configured without them (tests). It returns what closes it.
func (s *Server) startTracing() func() {
	if s.cfg.TraceDir == "" {
		return func() {}
	}
	rec, err := trace.Open(s.cfg.TraceDir)
	if err != nil {
		s.logf("tracing: %v; spans aren't kept", err)
		return func() {}
	}
	trace.SetDefault(rec)
	return func() {
		trace.SetDefault(nil)
		_ = rec.Close()
	}
}

// diagnosticsWindow reads a query's duration ("30m", "24h", "7d"), def when
// it isn't given, and at most most.
func diagnosticsWindow(r *http.Request, key string, def, most time.Duration) (time.Duration, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	var d time.Duration
	var err error
	if days, ok := strings.CutSuffix(v, "d"); ok {
		var n int
		if _, err = fmt.Sscanf(days, "%d", &n); err == nil {
			d = time.Duration(n) * 24 * time.Hour
		}
	} else {
		d, err = time.ParseDuration(v)
	}
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid %s %q: use a duration such as 30m, 24h or 7d", key, v)
	}
	return min(d, most), nil
}

// spanLimit is how many of the slowest spans and of the latest failures the
// span summary lists.
const spanLimit = 50

func (s *Server) traceDir() string {
	if s.cfg.TraceDir != "" {
		return s.cfg.TraceDir
	}
	return filepath.Dir(s.cfg.Paths.StateDB())
}

func (s *Server) diagnosticsSpans(w http.ResponseWriter, r *http.Request) error {
	d, err := diagnosticsWindow(r, "since", 30*time.Minute, 7*24*time.Hour)
	if err != nil {
		return err
	}
	since := time.Now().Add(-d)
	spans, err := trace.Read(s.traceDir(), trace.SpansFile, since)
	if err != nil {
		return err
	}
	sum := trace.Summarize(spans, spanLimit)
	out := api.DiagnosticsSpans{Since: since, Stats: sum.Stats, Slowest: sum.Slowest, Failures: sum.Failures}
	if rec := trace.Default(); rec != nil {
		out.Dropped = rec.Dropped()
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) diagnosticsStalls(w http.ResponseWriter, r *http.Request) error {
	d, err := diagnosticsWindow(r, "since", 24*time.Hour, 90*24*time.Hour)
	if err != nil {
		return err
	}
	since := time.Now().Add(-d)
	stalls, err := trace.Read(s.traceDir(), trace.StallsFile, since)
	if err != nil {
		return err
	}
	slices.Reverse(stalls)
	if stalls == nil {
		stalls = []trace.Span{}
	}
	return writeJSON(w, http.StatusOK, api.DiagnosticsStalls{Since: since, Stalls: stalls})
}

func (s *Server) diagnosticsResources(w http.ResponseWriter, r *http.Request) error {
	d, err := diagnosticsWindow(r, "window", resourceWindow, resourceWindow)
	if err != nil {
		return err
	}
	out := s.resources.since(time.Now().Add(-d))
	out.IntervalSeconds = int(usageInterval / time.Second)
	return writeJSON(w, http.StatusOK, out)
}

// maxClientSpanName is the longest name a client's span may have.
const maxClientSpanName = 200

func (s *Server) clientSpans(w http.ResponseWriter, r *http.Request) error {
	var req api.ClientSpansRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if len(req.Spans) > api.MaxClientSpans {
		return fmt.Errorf("%d spans in one batch: send at most %d", len(req.Spans), api.MaxClientSpans)
	}
	for _, sp := range req.Spans {
		if sp.Name == "" || len(sp.Name) > maxClientSpanName {
			return fmt.Errorf("a span needs a name of at most %d bytes", maxClientSpanName)
		}
		if sp.DurationMS < 0 || sp.Start.IsZero() {
			return fmt.Errorf("span %q needs a start and a duration of 0 or more", sp.Name)
		}
		if sp.Level != "" && sp.Level != api.SpanLevelWarn {
			return fmt.Errorf("span %q has level %q: only %q or none", sp.Name, sp.Level, api.SpanLevelWarn)
		}
	}
	for _, sp := range req.Spans {
		sp.Source = api.SpanSourceApp
		trace.Record(sp)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// The resource history: every sample the usage loop takes, for an hour.

// resourceWindow is how long samples are kept.
const resourceWindow = time.Hour

type resourceSample struct {
	vm     api.VMResourceSample
	agents []api.AgentUsage
}

// resourceHistory is the last resourceWindow of samples, oldest first: a
// ring of about 120 at the usage loop's 30 seconds.
type resourceHistory struct {
	mu      sync.Mutex
	samples []resourceSample
}

func (h *resourceHistory) add(at time.Time, host agent.HostUsage, agents []agent.AgentUsage) {
	u := toAPIUsage(host, agents)
	sample := resourceSample{
		vm: api.VMResourceSample{
			At: at, CPU: u.Host.CPU, Cores: u.Host.Cores, MemUsed: u.Host.MemUsed, MemTotal: u.Host.MemTotal,
			DiskRead: u.Host.DiskRead, DiskWrite: u.Host.DiskWrite, Pressure: u.Host.Pressure,
		},
		agents: u.Agents,
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	cut := at.Add(-resourceWindow)
	old := 0
	for old < len(h.samples) && !h.samples[old].vm.At.After(cut) {
		old++
	}
	h.samples = append(slices.Delete(h.samples, 0, old), sample)
}

// since is the samples taken after from: the VM's, and each agent's, in the
// order agents first appear.
func (h *resourceHistory) since(from time.Time) api.DiagnosticsResources {
	out := api.DiagnosticsResources{Since: from, VM: []api.VMResourceSample{}, Agents: []api.AgentResources{}}
	index := map[string]int{}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sm := range h.samples {
		if sm.vm.At.Before(from) {
			continue
		}
		out.VM = append(out.VM, sm.vm)
		for _, a := range sm.agents {
			i, ok := index[a.Ref]
			if !ok {
				i = len(out.Agents)
				index[a.Ref] = i
				out.Agents = append(out.Agents, api.AgentResources{Ref: a.Ref})
			}
			out.Agents[i].Samples = append(out.Agents[i].Samples, api.AgentResourceSample{
				At: sm.vm.At, State: a.State, CPU: a.CPU, Memory: a.Memory, Processes: a.Processes,
				DiskRead: a.DiskRead, DiskWrite: a.DiskWrite,
			})
		}
	}
	return out
}

// PSI stalls: the VM's memory or CPU stalling, from the pressure loop's
// samples every two seconds. An episode is recorded once it ends, as one
// span covering it with the worst it got; one that goes on is recorded every
// psiEpisodeMax, so a VM that stays stalled still shows.

// Past these, the VM is stalling. Memory is the share of the last ten
// seconds in which some task, or every task, waited for memory; CPU in which
// some runnable task waited for a CPU (CPU's "full" is meaningless for the
// whole machine).
const (
	psiMemorySome = 20.0
	psiMemoryFull = 5.0
	psiCPUSome    = 60.0
	psiEpisodeMax = 5 * time.Minute
)

// cpuPressureFile is the kernel's CPU pressure.
const cpuPressureFile = "/proc/pressure/cpu"

type psiEpisode struct {
	start          time.Time
	peakSome, peak float64 // peak is full's, for memory
}

type psiWatch struct {
	mu       sync.Mutex
	mem, cpu *psiEpisode
}

// step takes one sample, memory's and CPU's PSI, and returns the stall spans
// of the episodes it ended.
func (p *psiWatch) step(now time.Time, mem, cpu pressure.PSI) []trace.Span {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []trace.Span
	out = stepEpisode(&p.mem, now, "psi memory", mem.Some.Avg10 >= psiMemorySome || mem.Full.Avg10 >= psiMemoryFull, mem.Some.Avg10, mem.Full.Avg10, out)
	out = stepEpisode(&p.cpu, now, "psi cpu", cpu.Some.Avg10 >= psiCPUSome, cpu.Some.Avg10, 0, out)
	return out
}

func stepEpisode(ep **psiEpisode, now time.Time, name string, stalling bool, some, full float64, out []trace.Span) []trace.Span {
	e := *ep
	if e != nil && (!stalling || now.Sub(e.start) >= psiEpisodeMax) {
		attrs := map[string]any{"stall": strings.TrimPrefix(name, "psi ") + " pressure", "peakSomeAvg10": e.peakSome}
		if name == "psi memory" {
			attrs["peakFullAvg10"] = e.peak
		}
		out = append(out, trace.Span{
			Name: name, Start: e.start, DurationMS: trace.MS(now.Sub(e.start)),
			Level: api.SpanLevelWarn, Attrs: attrs,
		})
		*ep, e = nil, nil
	}
	if !stalling {
		return out
	}
	if e == nil {
		e = &psiEpisode{start: now}
		*ep = e
	}
	e.peakSome = max(e.peakSome, some)
	e.peak = max(e.peak, full)
	return out
}

// watchPSI is the pressure loop's turn for stalls.
func (s *Server) watchPSI(now time.Time, mem pressure.PSI) {
	if trace.Default() == nil {
		return
	}
	cpu, _ := s.readCPUPressure()
	for _, sp := range s.psi.step(now, mem, cpu) {
		trace.Record(sp)
	}
}
