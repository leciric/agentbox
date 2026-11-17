package daemon

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime/debug"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/report"
)

// The daemon's own crashes (report.Crashes), sent as error reports like the
// app's uncaught errors: while the user has them on, redacted, and no more
// than maxCrashReports in one run. Each is kept in the crashes folder until
// it's sent, which the next daemon does as it starts for a crash that killed
// this one, and this one does at once for a panic it recovered from.

// maxCrashReports is the most crash reports one daemon sends: the app's own
// per-run cap (maxPerRun in the app's main/reportqueue.ts). The rest wait
// for the next start.
const maxCrashReports = 5

func (s *Server) crashes() report.Crashes {
	return report.Crashes{Dir: filepath.Join(s.cfg.Paths.Data, "crashes")}
}

// startCrashes records how the last daemon ended and marks this one running;
// the func is this one stopping as it should. A crashes folder that can't be
// written only loses crash reports.
func (s *Server) startCrashes() func() {
	stop, err := s.crashes().Start(s.redactor(), Version, report.BootID(), s.cfg.CrashOutput)
	if err != nil {
		s.logf("crash records: %v", err)
		return func() {}
	}
	return stop
}

// recoverPanic is deferred by every API handler: net/http would recover a
// handler's panic itself, but only log it, and the daemon carries on with
// nobody told. This records it, and answers the request with an error.
func (s *Server) recoverPanic(w http.ResponseWriter, r *http.Request) {
	v := recover()
	if v == nil {
		return
	}
	if v == http.ErrAbortHandler { //nolint:errorlint // what net/http itself compares
		panic(v)
	}
	stack := string(debug.Stack())
	where := cmp.Or(r.Pattern, r.Method+" "+r.URL.Path)
	s.logf("panic in %s: %v\n%s", where, v, stack)
	s.recordCrash(report.Crash{Kind: report.CrashPanic, At: time.Now(), Version: Version, Value: fmt.Sprint(v), Where: where, Stack: stack})
	writeError(w, fmt.Errorf("the daemon hit a bug answering this (%v); it was recorded", v))
}

// recordCrash keeps c and sends it, in the background.
func (s *Server) recordCrash(c report.Crash) {
	if err := s.crashes().Record(s.redactor(), c); err != nil {
		s.logf("recording a crash: %v", err)
		return
	}
	ctx := s.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	go s.sendCrashes(ctx)
}

// sendCrashes sends the crash records waiting, oldest first, and removes
// each one sent. With error reports off they are removed unsent; one that
// fails to go stays, with the rest, for the next start.
func (s *Server) sendCrashes(ctx context.Context) {
	s.crashSend.Lock()
	defer s.crashSend.Unlock()
	pending, err := s.crashes().Pending()
	if err != nil {
		s.logf("crash records: %v", err)
	}
	if len(pending) == 0 {
		return
	}
	if err := s.errorReportsAllowed(ctx); err != nil {
		for _, p := range pending {
			_ = p.Remove()
		}
		s.logf("dropped %d crash record(s) unsent: %v", len(pending), err)
		return
	}
	for _, p := range pending {
		if s.crashesSent >= maxCrashReports {
			s.logf("%d crash record(s) wait for the next start: this daemon sent %d", len(pending), maxCrashReports)
			return
		}
		s.crashesSent++
		id, err := s.sendReportOf(ctx, api.ReportKindError, p.Message(), p.Sections())
		if err != nil {
			s.logf("sending a crash report: %v", err)
			return
		}
		pending = pending[1:]
		s.logf("sent a crash report (%s): %s", p.Kind, id)
		if err := p.Remove(); err != nil {
			s.logf("crash records: %v", err)
		}
	}
}
