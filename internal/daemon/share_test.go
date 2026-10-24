package daemon

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/api"
)

func TestShareReportHoldsGitWorkBack(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), "exit 0\n")
	ctx := context.Background()

	files, err := d.client.DiagnosticsFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if files.Share != nil || files.Waiting || files.Daemon.Open == 0 {
		t.Fatalf("with no report: %+v", files)
	}
	if d.srv.shareBusy() {
		t.Fatal("busy with no report")
	}

	trim := time.Now()
	if err := d.client.ReportShare(ctx, api.ShareReport{Open: 400_000, Limit: 524_288, Trims: 3, LastTrim: &trim, Pinned: 380_000}); err != nil {
		t.Fatal(err)
	}
	files, err = d.client.DiagnosticsFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if files.Share == nil || files.Share.Open != 400_000 || files.Share.Pinned != 380_000 || !files.Waiting {
		t.Fatalf("with a report past %v of the limit: %+v", shareBusyAt, files)
	}
	if !d.srv.shareBusy() {
		t.Fatal("not busy past the limit's share")
	}
	// shareRoom waits while it's busy, and stops waiting with its context.
	waitCtx, cancel := context.WithTimeout(ctx, 3*shareWaitPoll/2)
	defer cancel()
	started := time.Now()
	d.srv.shareRoom(waitCtx, "a test")
	if time.Since(started) < shareWaitPoll {
		t.Fatal("shareRoom didn't wait")
	}

	if err := d.client.ReportShare(ctx, api.ShareReport{Open: 90_000, Limit: 524_288}); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	d.srv.shareRoom(ctx, "a test")
	if time.Since(started) > time.Second || d.srv.shareBusy() {
		t.Fatal("still held back with room")
	}

	// A report the supervisor stopped renewing is taken as gone.
	d.srv.share.mu.Lock()
	d.srv.share.at = time.Now().Add(-2 * shareReportTTL)
	d.srv.share.mu.Unlock()
	if d.srv.shareLast() != nil {
		t.Fatal("a stale report counts")
	}
}

func TestParseFileNr(t *testing.T) {
	if c := parseFileNr("20640\t0\t9223372036854775807\n"); c != (api.FileCount{Open: 20640}) {
		t.Errorf("unlimited: %+v", c)
	}
	if c := parseFileNr("1000\t0\t2000\n"); c != (api.FileCount{Open: 1000, Limit: 2000}) {
		t.Errorf("limited: %+v", c)
	}
	if c := parseFileNr("garbage"); c != (api.FileCount{}) {
		t.Errorf("garbage: %+v", c)
	}
}
