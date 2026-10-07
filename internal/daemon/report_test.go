package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/report"
)

// fakeReports stands in for agentbox.linting.dev's report endpoint, and keeps
// what it's sent.
type fakeReports struct {
	mu   sync.Mutex
	sent []report.Payload
}

func (f *fakeReports) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/reports" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var p report.Payload
		if err := json.Unmarshal(body, &p); err != nil {
			t.Error(err)
		}
		f.mu.Lock()
		f.sent = append(f.sent, p)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"rep_test"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/api/v1/latest"
}

func callReport(t *testing.T, fn func(http.ResponseWriter, *http.Request) error, path string, body any, out any) error {
	t.Helper()
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	if err := fn(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(b)))); err != nil {
		return err
	}
	return json.Unmarshal(w.Body.Bytes(), out)
}

func TestReportDraftAndSend(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	home, _ := os.UserHomeDir()
	vmLog := filepath.Join(t.TempDir(), "vm.log")
	if err := os.WriteFile(vmLog, []byte("supervisor: booted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(report.VMLogEnv, vmLog)
	var fake fakeReports
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t), releasesURL: fakeStable(t, "0.16.0")})
	if err := os.WriteFile(d.paths.DaemonLog(), []byte("started\nGITHUB_TOKEN=ghp_0123456789abcdefABCDEF0123456789abcd in "+home+"/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var draft api.ReportDraft
	err := callReport(t, d.srv.reportDraft, "/v1/reports/draft", api.ReportDraftRequest{Sections: []api.ReportSection{
		{ID: api.ReportSectionAppLog, Title: "The app's log", Content: "renderer failed for someone@example.com"},
	}}, &draft)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, s := range draft.Sections {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "system,daemon-log,vm-log,app-log" {
		t.Fatalf("sections %v", ids)
	}
	if draft.Install == "" || draft.Version != Version || !strings.HasSuffix(draft.Endpoint, "/api/v1/reports") {
		t.Errorf("draft %+v", draft)
	}
	if s := draft.Sections[0].Content; !strings.Contains(s, "AgentBox:") || !strings.Contains(s, "Mode:") || !strings.Contains(s, "Setup:") {
		t.Errorf("the system section:\n%s", s)
	}
	if s := draft.Sections[1].Content; strings.Contains(s, "ghp_") || !strings.Contains(s, "GITHUB_TOKEN=[redacted] in ~/x") {
		t.Errorf("the daemon's log wasn't redacted:\n%s", s)
	}
	if s := draft.Sections[3].Content; s != "renderer failed for [email]" {
		t.Errorf("the app's section wasn't redacted: %q", s)
	}

	var sent api.ReportSent
	err = callReport(t, d.srv.sendReport, "/v1/reports", api.ReportRequest{Kind: api.ReportKindProblem, Message: "it hangs, token=abc",
		Sections: []api.ReportSection{{ID: "app-log", Title: "The app's log", Content: "sk-ant-oat01-ZYXWVUTSRQ_0123456789"}}}, &sent)
	if err != nil || sent.ID != "rep_test" {
		t.Fatalf("send: %+v, %v", sent, err)
	}
	p := fake.sent[0]
	if p.Install != draft.Install || p.Kind != api.ReportKindProblem || p.Message != "it hangs, token=[redacted]" ||
		len(p.Sections) != 1 || p.Sections[0].Content != "[redacted]" {
		t.Errorf("sent %+v", p)
	}

	if err := callReport(t, d.srv.sendReport, "/v1/reports", api.ReportRequest{Kind: api.ReportKindProblem, Message: " "}, &sent); err == nil {
		t.Error("a report without a message was sent")
	}
}

func TestErrorReportsWaitForTheUser(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	var fake fakeReports
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t), releasesURL: fakeStable(t, "0.16.0")})
	errReport := api.ReportRequest{Kind: api.ReportKindError, Message: "TypeError: x is undefined", Sections: []api.ReportSection{{ID: "error", Title: "The error", Content: "at App.tsx:1"}}}
	var sent api.ReportSent

	settings, err := patchSettings(t, d, `{}`)
	if err != nil || settings.ErrorReports || settings.ErrorReportsAsked {
		t.Fatalf("error reports start off and unasked: %+v, %v", settings, err)
	}
	if err := callReport(t, d.srv.sendReport, "/v1/reports", errReport, &sent); err == nil {
		t.Fatal("an error report was sent before the user turned them on")
	}
	if settings, _ = patchSettings(t, d, `{"errorReports":false}`); settings.ErrorReports || !settings.ErrorReportsAsked {
		t.Errorf("turned off: %+v", settings)
	}
	if settings, _ = patchSettings(t, d, `{"errorReports":true}`); !settings.ErrorReports || !settings.ErrorReportsAsked {
		t.Errorf("turned on: %+v", settings)
	}
	if err := callReport(t, d.srv.sendReport, "/v1/reports", errReport, &sent); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DO_NOT_TRACK", "1")
	if err := callReport(t, d.srv.sendReport, "/v1/reports", errReport, &sent); err == nil {
		t.Error("an error report was sent with DO_NOT_TRACK=1")
	}
	if len(fake.sent) != 1 || fake.sent[0].Kind != api.ReportKindError {
		t.Errorf("sent %+v", fake.sent)
	}
}
