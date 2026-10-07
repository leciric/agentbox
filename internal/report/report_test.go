package report

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/update"
)

func TestCleanRedactsCutsAndDrops(t *testing.T) {
	r := NewRedactor("/home/lint")
	big := strings.Repeat("old line\n", MaxSection/9+100) + "the last line\n"
	got, err := Clean(r, []api.ReportSection{
		{ID: "system", Title: " AgentBox ", Content: "home /home/lint\n"},
		{ID: "empty", Title: "Nothing", Content: "  \n"},
		{ID: "daemon-log", Title: "Log", Content: big},
		{ID: "system", Title: "Again", Content: "a second system section"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "system" || got[1].ID != "daemon-log" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Title != "AgentBox" || got[0].Content != "home ~" {
		t.Errorf("system: %+v", got[0])
	}
	log := got[1].Content
	if len(log) > MaxSection || !strings.HasPrefix(log, "[… earlier lines left out]\nold line\n") || !strings.HasSuffix(log, "the last line") {
		t.Errorf("the log wasn't cut to its end on a whole line: %d bytes, %q…%q", len(log), log[:40], log[len(log)-20:])
	}

	for _, bad := range []api.ReportSection{{ID: "Bad ID", Title: "x", Content: "x"}, {ID: "ok", Title: "", Content: "x"}, {ID: "ok", Title: strings.Repeat("t", MaxTitle+1), Content: "x"}} {
		if _, err := Clean(r, []api.ReportSection{bad}); err == nil {
			t.Errorf("%+v was taken", bad)
		}
	}
}

func TestCleanKeepsWithinTheTotal(t *testing.T) {
	var sections []api.ReportSection
	for i := range MaxSections + 3 {
		sections = append(sections, api.ReportSection{ID: "s" + string(rune('a'+i)), Title: "t", Content: strings.Repeat("x", MaxSection)})
	}
	got, err := Clean(NewRedactor(), sections)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, s := range got {
		total += len(s.Content)
	}
	if total > MaxTotal || len(got) > MaxSections || len(got) == 0 {
		t.Errorf("%d sections, %d bytes", len(got), total)
	}
}

func TestCleanMessage(t *testing.T) {
	r := NewRedactor("/home/lint")
	if got, err := CleanMessage(r, api.ReportKindProblem, "  it broke in /home/lint/app, mail me at a@b.io \n"); err != nil || got != "it broke in ~/app, mail me at [email]" {
		t.Errorf("got %q, %v", got, err)
	}
	for _, c := range []struct{ kind, message string }{
		{api.ReportKindProblem, "   "},
		{"crash", "x"},
		{api.ReportKindError, strings.Repeat("é", MaxMessage+1)},
	} {
		if _, err := CleanMessage(r, c.kind, c.message); err == nil {
			t.Errorf("%s %.20q was taken", c.kind, c.message)
		}
	}
}

func TestTailFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	if got := TailFile(path, 100); !strings.Contains(got, "doesn't exist") {
		t.Errorf("missing file: %q", got)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("0123456789\n", 1000)+"end\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := TailFile(path, 200)
	if len(got) > 200 || !strings.HasSuffix(got, "0123456789\nend\n") || !strings.HasPrefix(got, "[… earlier lines left out]\n0123456789\n") {
		t.Errorf("got %d bytes: %q", len(got), got)
	}
}

func TestURL(t *testing.T) {
	for base, want := range map[string]string{
		update.DefaultURL:                     "https://agentbox.linting.dev/api/v1/reports",
		"http://127.0.0.1:8788/api/v1/latest": "http://127.0.0.1:8788/api/v1/reports",
	} {
		if got, err := URL(base); err != nil || got != want {
			t.Errorf("URL(%q) = %q, %v", base, got, err)
		}
	}
}

// TestSendPayload pins what goes over the wire: the server
// (agentbox-landing's functions/api/v1/reports.ts) takes exactly these
// fields and no others.
func TestSendPayload(t *testing.T) {
	var got map[string]any
	status := http.StatusCreated
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/reports" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		if status == http.StatusCreated {
			_, _ = w.Write([]byte(`{"id":"rep_0123"}`))
		} else {
			_, _ = w.Write([]byte(`{"error":"slow down"}`))
		}
	}))
	defer srv.Close()
	p := Payload{Install: "3f1c0a52-8d8e-4c1b-9a51-6f0c8e0d2b11", Version: "0.10.0", OS: "linux", Arch: "amd64", Kind: api.ReportKindProblem,
		Message: "it broke", Sections: []api.ReportSection{{ID: "system", Title: "AgentBox", Content: "AgentBox: 0.10.0"}}}
	id, err := Send(context.Background(), srv.URL+"/api/v1/latest", p)
	if err != nil || id != "rep_0123" {
		t.Fatalf("Send = %q, %v", id, err)
	}
	want := `{"arch":"amd64","install":"3f1c0a52-8d8e-4c1b-9a51-6f0c8e0d2b11","kind":"problem","message":"it broke","os":"linux",` +
		`"sections":[{"content":"AgentBox: 0.10.0","id":"system","title":"AgentBox"}],"version":"0.10.0"}`
	if b, _ := json.Marshal(got); string(b) != want {
		t.Errorf("sent %s\nwant %s", b, want)
	}

	status = http.StatusTooManyRequests
	if _, err := Send(context.Background(), srv.URL+"/api/v1/latest", p); err == nil || !strings.Contains(err.Error(), "too many reports") {
		t.Errorf("a 429: %v", err)
	}
	status = http.StatusBadRequest
	if _, err := Send(context.Background(), srv.URL+"/api/v1/latest", p); err == nil || !strings.Contains(err.Error(), "slow down") {
		t.Errorf("a 400 should say why: %v", err)
	}
}
