package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/incus"
	"agentbox/internal/paths"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
	"agentbox/internal/trace"
)

// recordTo installs a recorder writing to a directory of the test's own. Not
// for parallel tests: the recorder is the process's.
func recordTo(t *testing.T) (dir string, flush func()) {
	t.Helper()
	dir = t.TempDir()
	rec, err := trace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	trace.SetDefault(rec)
	closed := false
	flush = func() {
		if !closed {
			closed = true
			trace.SetDefault(nil)
			if err := rec.Close(); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(flush)
	return dir, flush
}

func TestRequestsAreTracedAndSlowOnesAreStalls(t *testing.T) {
	dir, flush := recordTo(t)
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /fast/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // answered at once, then open for a while
		w.(http.Flusher).Flush()
		time.Sleep(80 * time.Millisecond)
	})
	mux.HandleFunc("POST /v1/agents/{project}/{agent}/browser/pick", func(http.ResponseWriter, *http.Request) {
		time.Sleep(80 * time.Millisecond) // waits for a person
	})
	mux.HandleFunc("GET /hung", func(w http.ResponseWriter, _ *http.Request) {
		<-release
	})
	srv := httptest.NewServer(traceAPIAfter("", mux, true, 40*time.Millisecond, 100*time.Millisecond))
	defer srv.Close()
	get := func(method, path string) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	get("GET", "/fast/1")
	get("GET", "/fast/2")
	get("GET", "/nowhere")
	get("GET", "/slow")
	get("GET", "/stream")
	get("POST", "/v1/agents/p/a/browser/pick")
	done := make(chan struct{})
	go func() { get("GET", "/hung"); close(done) }()
	time.Sleep(250 * time.Millisecond)
	close(release)
	<-done
	flush()

	spans, err := trace.Read(dir, trace.SpansFile, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string][]trace.Span{}
	for _, s := range spans {
		byName[s.Name] = append(byName[s.Name], s)
	}
	if n := len(byName["GET /fast/{id}"]); n != 2 {
		t.Errorf("GET /fast/{id}: %d spans, want 2, named after the route: %v", n, byName)
	}
	if s := byName["GET /slow"]; len(s) != 1 || s[0].Error == "" || s[0].Attrs["status"] != float64(500) {
		t.Errorf("GET /slow = %+v, want a failure with its status", s)
	}
	if len(byName["GET (no route)"]) != 1 {
		t.Errorf("an unrouted request wasn't traced: %v", byName)
	}

	stalls, _ := trace.Read(dir, trace.StallsFile, time.Time{})
	reasons := map[string]string{}
	for _, s := range stalls {
		reason, _ := s.Attrs["stall"].(string)
		reasons[s.Name] += reason + ";"
	}
	want := map[string]string{
		"GET /slow": "slow request;",
		// Noted while it hung, and again once it answered.
		"GET /hung": "unanswered;slow request;",
	}
	if len(reasons) != len(want) {
		t.Errorf("stalls = %v, want %v: a stream and a pick aren't stalls", reasons, want)
	}
	for name, r := range want {
		if reasons[name] != r {
			t.Errorf("%s stalls = %q, want %q", name, reasons[name], r)
		}
	}
}

// An agent's API waits by design (a heavy command's turn, a question): its
// requests get spans and never stalls.
func TestAgentAPIRequestsAreNeverStalls(t *testing.T) {
	dir, flush := recordTo(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/self/heavy", func(http.ResponseWriter, *http.Request) { time.Sleep(60 * time.Millisecond) })
	srv := httptest.NewServer(traceAPIAfter("agent", mux, false, 10*time.Millisecond, 20*time.Millisecond))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/self/heavy", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	flush()
	spans, _ := trace.Read(dir, trace.SpansFile, time.Time{})
	if len(spans) != 1 || spans[0].Name != "agent: POST /v1/self/heavy" {
		t.Errorf("spans = %+v", spans)
	}
	if stalls, _ := trace.Read(dir, trace.StallsFile, time.Time{}); len(stalls) != 0 {
		t.Errorf("stalls = %+v, want none", stalls)
	}
}

func TestPSIStallEpisodes(t *testing.T) {
	var w psiWatch
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	calm := pressure.PSI{}
	memFull := pressure.PSI{Some: pressure.Stall{Avg10: 30}, Full: pressure.Stall{Avg10: 12}}
	memSome := pressure.PSI{Some: pressure.Stall{Avg10: 25}, Full: pressure.Stall{Avg10: 1}}
	cpuBusy := pressure.PSI{Some: pressure.Stall{Avg10: 75}}

	step := func(sec int, mem, cpu pressure.PSI) []trace.Span {
		return w.step(at.Add(time.Duration(sec)*time.Second), mem, cpu)
	}
	if got := step(0, calm, pressure.PSI{Some: pressure.Stall{Avg10: psiCPUSome - 1}}); len(got) != 0 {
		t.Fatalf("calm: %+v", got)
	}
	// Memory stalls from 2s to 8s, peaking at 12% full; nothing until it ends.
	for _, sec := range []int{2, 4, 6} {
		mem := memSome
		if sec == 4 {
			mem = memFull
		}
		if got := step(sec, mem, calm); len(got) != 0 {
			t.Fatalf("at %ds, mid-episode: %+v", sec, got)
		}
	}
	got := step(8, calm, cpuBusy)
	if len(got) != 1 {
		t.Fatalf("at 8s: %+v, want the memory episode", got)
	}
	m := got[0]
	if m.Name != "psi memory" || m.Level != api.SpanLevelWarn || !m.Start.Equal(at.Add(2*time.Second)) || m.DurationMS != 6000 ||
		m.Attrs["peakSomeAvg10"] != 30.0 || m.Attrs["peakFullAvg10"] != 12.0 {
		t.Errorf("memory episode = %+v", m)
	}
	// CPU goes on stalling: it is recorded every psiEpisodeMax regardless.
	got = step(8+int(psiEpisodeMax/time.Second), calm, cpuBusy)
	if len(got) != 1 || got[0].Name != "psi cpu" || got[0].DurationMS != float64(psiEpisodeMax/time.Millisecond) || got[0].Attrs["peakSomeAvg10"] != 75.0 {
		t.Fatalf("a long CPU episode: %+v", got)
	}
	if got := step(9+int(psiEpisodeMax/time.Second), calm, calm); len(got) != 1 || got[0].DurationMS != 1000 {
		t.Errorf("the rest of the CPU episode: %+v", got)
	}
}

func TestResourceHistoryKeepsAnHour(t *testing.T) {
	var h resourceHistory
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	a := func(name string, cpu float64) agent.AgentUsage {
		return agent.AgentUsage{Agent: state.Agent{Project: "p", Name: name}, State: "running", CPU: cpu}
	}
	for i := range 150 { // 75 minutes at 30s
		agents := []agent.AgentUsage{a("one", float64(i))}
		if i >= 140 {
			agents = append(agents, a("two", 1))
		}
		h.add(at.Add(time.Duration(i)*30*time.Second), agent.HostUsage{CPU: float64(i), MemTotal: 8 << 30}, agents)
	}
	last := at.Add(149 * 30 * time.Second)
	all := h.since(time.Time{})
	if len(all.VM) != 120 || !all.VM[119].At.Equal(last) || !all.VM[0].At.Equal(last.Add(-119*30*time.Second)) {
		t.Fatalf("kept %d samples, %v to %v; want the last hour's 120", len(all.VM), all.VM[0].At, all.VM[len(all.VM)-1].At)
	}
	recent := h.since(last.Add(-5 * time.Minute))
	if len(recent.VM) != 11 || len(recent.Agents) != 2 {
		t.Fatalf("the last 5 minutes: %d samples, %d agents", len(recent.VM), len(recent.Agents))
	}
	if one := recent.Agents[0]; one.Ref != "p/one" || len(one.Samples) != 11 || one.Samples[10].CPU != 149 {
		t.Errorf("p/one = %+v", one)
	}
	if two := recent.Agents[1]; two.Ref != "p/two" || len(two.Samples) != 10 {
		t.Errorf("p/two = %+v, want only the samples it was in", two)
	}
}

func TestDiagnosticsAPI(t *testing.T) {
	dir := t.TempDir()
	d := startTestDaemon(t, t.TempDir(), "exit 0\n", testConfig{setup: func(s *Server) { s.cfg.TraceDir = dir }})
	ctx := context.Background()
	now := time.Now()
	err := d.client.SendClientSpans(ctx, []api.TraceSpan{
		{Name: "app request GET /v1/agents", Start: now.Add(-time.Second), DurationMS: 120},
		{Name: "app long task", Start: now.Add(-time.Second), DurationMS: 2400, Level: api.SpanLevelWarn, Attrs: map[string]any{"stall": "long task"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.client.SendClientSpans(ctx, []api.TraceSpan{{Name: "", Start: now}}); err == nil {
		t.Error("a span with no name was taken")
	}
	if err := d.client.SendClientSpans(ctx, make([]api.TraceSpan, api.MaxClientSpans+1)); err == nil {
		t.Error("an oversized batch was taken")
	}

	var spans api.DiagnosticsSpans
	waitFor(t, "the app's spans to be written", func() bool {
		spans, err = d.client.DiagnosticsSpans(ctx, 30*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, s := range spans.Stats {
			names[s.Name] = true
		}
		return names["app long task"] && names["POST /v1/diagnostics/client-spans"]
	})
	if len(spans.Slowest) == 0 || spans.Slowest[0].Name != "app long task" || spans.Slowest[0].Source != api.SpanSourceApp {
		t.Errorf("slowest = %+v", spans.Slowest)
	}
	stalls, err := d.client.DiagnosticsStalls(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(stalls.Stalls) != 1 || stalls.Stalls[0].Name != "app long task" {
		t.Errorf("stalls = %+v", stalls.Stalls)
	}

	d.srv.resources.add(now, agent.HostUsage{CPU: 12, Cores: 4}, []agent.AgentUsage{{Agent: state.Agent{Project: "p", Name: "a"}, CPU: 50}})
	res, err := d.client.DiagnosticsResources(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.VM) != 1 || res.VM[0].CPU != 12 || len(res.Agents) != 1 || res.Agents[0].Ref != "p/a" || res.IntervalSeconds != 30 {
		t.Errorf("resources = %+v", res)
	}
}

func TestDiagnosticsWindow(t *testing.T) {
	for _, c := range []struct {
		query string
		want  time.Duration
		bad   bool
	}{
		{"", 30 * time.Minute, false},
		{"?since=2h", 2 * time.Hour, false},
		{"?since=3d", 72 * time.Hour, false},
		{"?since=30d", 7 * 24 * time.Hour, false}, // capped
		{"?since=yesterday", 0, true},
		{"?since=-5m", 0, true},
	} {
		got, err := diagnosticsWindow(httptest.NewRequest("GET", "/v1/diagnostics/spans"+c.query, nil), "since", 30*time.Minute, 7*24*time.Hour)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%q = %v, %v; want %v (bad: %v)", c.query, got, err, c.want, c.bad)
		}
	}
}

// BenchmarkTraceAPI is what the span around every request costs: the main
// API's real routes, answering GET /v1/version, with tracing off and on.
func BenchmarkTraceAPI(b *testing.B) {
	root := b.TempDir()
	p := paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}
	srv, err := New(Config{Paths: p, Incus: incus.Client{Bin: "false"}})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = srv.store.Close() }()
	h := srv.routes()
	serve := func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/version", nil))
		}
	}
	b.Run("off", serve)
	b.Run("on", func(b *testing.B) {
		rec, err := trace.Open(b.TempDir())
		if err != nil {
			b.Fatal(err)
		}
		trace.SetDefault(rec)
		defer func() { trace.SetDefault(nil); _ = rec.Close() }()
		serve(b)
	})
}
