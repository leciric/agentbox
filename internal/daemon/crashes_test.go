package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/report"
)

func (f *fakeReports) payloads() []report.Payload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]report.Payload(nil), f.sent...)
}

func TestAPanicInAHandlerIsAnsweredRecordedAndReported(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	home, _ := os.UserHomeDir()
	var fake fakeReports
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t), releasesURL: fakeStable(t, "0.16.0")})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/boom", nil)
	r.Pattern = "GET /v1/boom"
	func() {
		defer d.srv.recoverPanic(w, r)
		panic("nil map in " + home + "/project")
	}()
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "the daemon hit a bug") {
		t.Errorf("answered %d %s", w.Code, w.Body)
	}
	waitFor(t, "the crash report", func() bool { return len(fake.payloads()) == 1 })
	p := fake.payloads()[0]
	if p.Kind != api.ReportKindError || p.Message != "The daemon panicked in GET /v1/boom: nil map in ~/project" {
		t.Errorf("sent %q %q", p.Kind, p.Message)
	}
	if len(p.Sections) != 2 || p.Sections[0].ID != api.ReportSectionError || !strings.Contains(p.Sections[0].Content, "recoverPanic") ||
		strings.Contains(p.Sections[0].Content, home+"/") {
		t.Errorf("sections %+v", p.Sections)
	}
	waitFor(t, "the record to be removed", func() bool {
		pending, _ := d.srv.crashes().Pending()
		return len(pending) == 0
	})
}

func TestTheLastDaemonsCrashIsReportedAtStart(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	root := t.TempDir()
	crashes := report.Crashes{Dir: filepath.Join(root, "data", "crashes")}
	at := time.Now().Add(-time.Minute)
	if err := crashes.Record(report.NewRedactor(), report.Crash{Kind: report.CrashFatal, At: at, Version: "0.15.0",
		Value: "fatal error: concurrent map writes", Stack: "fatal error: concurrent map writes\n\ngoroutine 7 [running]:\nagentbox/internal/daemon.(*Server).watch(0x1)"}); err != nil {
		t.Fatal(err)
	}
	var fake fakeReports
	startTestDaemon(t, root, fakeIncus, testConfig{updateURL: fake.start(t), releasesURL: fakeStable(t, "0.16.0")})
	waitFor(t, "the crash report", func() bool { return len(fake.payloads()) == 1 })
	p := fake.payloads()[0]
	if p.Message != "The daemon crashed: fatal error: concurrent map writes" || !strings.Contains(p.Sections[1].Content, "Daemon:   0.15.0") {
		t.Errorf("sent %q %+v", p.Message, p.Sections)
	}
	waitFor(t, "the record to be removed", func() bool {
		pending, _ := crashes.Pending()
		return len(pending) == 0
	})
}

func TestCrashRecordsAreDroppedUnsentWhileErrorReportsAreOff(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	var fake fakeReports
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t), releasesURL: fakeStable(t, "0.16.0")})
	if _, err := patchSettings(t, d, `{"errorReports":false}`); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.crashes().Record(d.srv.redactor(), report.Crash{Kind: report.CrashDied, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	d.srv.sendCrashes(t.Context())
	if pending, _ := d.srv.crashes().Pending(); len(pending) != 0 || len(fake.payloads()) != 0 {
		t.Errorf("%d record(s) left, %d sent", len(pending), len(fake.payloads()))
	}
}

func TestADaemonSendsAtMostItsCapOfCrashReports(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	var fake fakeReports
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t), releasesURL: fakeStable(t, "0.16.0")})
	d.srv.crashesSent = maxCrashReports - 1
	for i := range 3 {
		if err := d.srv.crashes().Record(d.srv.redactor(), report.Crash{Kind: report.CrashPanic, At: time.Now().Add(time.Duration(i)), Value: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	d.srv.sendCrashes(t.Context())
	if pending, _ := d.srv.crashes().Pending(); len(pending) != 2 || len(fake.payloads()) != 1 {
		t.Errorf("%d record(s) left, %d sent", len(pending), len(fake.payloads()))
	}
}
