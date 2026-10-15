package daemon

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A quiet event stream says it's still there: as a comment, which a browser's
// EventSource never shows the page, or with ?ping=1 as a "ping" event, which
// the phone's web app watches to tell a stream that died from a quiet one.
func TestEventStreamKeepalive(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	d.srv.keepalive = 20 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = d.srv.eventStream(w, r) }))
	defer srv.Close()

	beat := func(query string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/v1/events"+query, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		lines := bufio.NewScanner(res.Body)
		for lines.Scan() {
			line := lines.Text()
			if line == ": keepalive" || strings.HasPrefix(line, "event: ping") {
				return line
			}
		}
		t.Fatalf("the stream%s ended with no keepalive: %v", query, lines.Err())
		return ""
	}
	if got := beat(""); got != ": keepalive" {
		t.Errorf("keepalive = %q, want a comment", got)
	}
	if got := beat("?ping=1"); got != "event: ping" {
		t.Errorf("keepalive with ?ping=1 = %q, want a ping event", got)
	}
}
