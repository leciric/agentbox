package update

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUsageURLSitsBesideTheCheck(t *testing.T) {
	for base, want := range map[string]string{
		"":                                   "https://agentbox.linting.dev/api/v1/usage",
		"http://127.0.0.1:9/api/v1/latest":   "http://127.0.0.1:9/api/v1/usage",
		"http://127.0.0.1:9/api/v1/latest?x": "http://127.0.0.1:9/api/v1/usage",
		"http://127.0.0.1:9":                 "http://127.0.0.1:9/usage",
	} {
		if got, err := UsageURL(base); err != nil || got != want {
			t.Errorf("UsageURL(%q) = %q, %v; want %q", base, got, err, want)
		}
	}
}

// TestSendUsageSendsOnlyTheFieldsAndCounts pins the report's shape: the four
// fields of the update check, and days of feature counts.
func TestSendUsageSendsOnlyTheFieldsAndCounts(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/usage" || r.URL.RawQuery != "" ||
			r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	req := Request{Install: "5b0c7d4e-1111-4222-8333-944455556666", Version: "0.16.0", OS: "linux", Arch: "amd64"}
	days := []UsageDay{{Day: "2026-09-24", Features: map[string]int64{"agent.create.claude": 2, "pr.list": 1}}}
	if err := SendUsage(context.Background(), srv.URL+"/api/v1/latest", req, days); err != nil {
		t.Fatal(err)
	}
	want := `{"arch":"amd64","days":[{"day":"2026-09-24","features":{"agent.create.claude":2,"pr.list":1}}],"install":"5b0c7d4e-1111-4222-8333-944455556666","os":"linux","version":"0.16.0"}`
	if b, _ := json.Marshal(got); string(b) != want {
		t.Errorf("body = %s\nwant   %s", b, want)
	}
}

func TestSendUsageFailsOnAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusBadRequest)
	}))
	defer srv.Close()
	if err := SendUsage(context.Background(), srv.URL+"/api/v1/latest", Request{}, nil); err == nil {
		t.Error("SendUsage() = nil for a 400")
	}
}
