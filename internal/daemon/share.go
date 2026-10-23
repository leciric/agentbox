package daemon

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
)

// The shared home's file descriptors (internal/hostvm/chv/share.go): on a
// Linux host, virtiofsd holds one for every inode of the home the VM has
// cached, and the VM's reads there fail with ENFILE once it runs out. The VM's
// supervisor drops the VM's inode cache when the count is high, and reports
// it here; meanwhile the daemon holds back what walks many files at once:
// creating and removing worktrees, fetching bases, sweeping finished agents.

const (
	// shareReportTTL is how old the supervisor's last report may be before
	// it is taken as gone: it reports every two seconds.
	shareReportTTL = 10 * time.Second
	// shareBusyAt is the share of virtiofsd's limit past which git work
	// waits: the supervisor drops the VM's cache well before it.
	shareBusyAt = 0.7
	// shareWaitMax is the longest a worktree is held back: past it the work
	// goes ahead, since what keeps the count high is then pinned.
	shareWaitMax  = 2 * time.Minute
	shareWaitPoll = 2 * time.Second
)

type shareState struct {
	mu     sync.Mutex
	report *api.ShareReport
	at     time.Time
	warned bool // the last check found it busy and said so
}

// shareReport is the VM's supervisor saying how many files virtiofsd has
// open: PUT /v1/vm/share.
func (s *Server) shareReport(w http.ResponseWriter, r *http.Request) error {
	var req api.ShareReport
	if err := readJSON(r, &req); err != nil {
		return err
	}
	s.share.mu.Lock()
	s.share.report, s.share.at = &req, time.Now()
	s.share.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// shareLast is the supervisor's last report, nil when there's none lately.
func (s *Server) shareLast() *api.ShareReport {
	s.share.mu.Lock()
	defer s.share.mu.Unlock()
	if s.share.report == nil || time.Since(s.share.at) > shareReportTTL {
		return nil
	}
	r := *s.share.report
	return &r
}

// shareBusy says whether the shared home is close enough to virtiofsd's
// limit that what walks it should wait, and logs when that starts and ends.
func (s *Server) shareBusy() bool {
	busy := shareFull(s.shareLast())
	s.share.mu.Lock()
	changed := busy != s.share.warned
	s.share.warned = busy
	s.share.mu.Unlock()
	if changed {
		if r := s.shareLast(); busy && r != nil {
			s.logf("shared home: virtiofsd has %d of %d files open: holding git work back", r.Open, r.Limit)
		} else if !busy {
			s.logf("shared home: virtiofsd has room again: git work goes on")
		}
	}
	return busy
}

func shareFull(r *api.ShareReport) bool {
	return r != nil && r.Limit > 0 && float64(r.Open) >= shareBusyAt*float64(r.Limit)
}

// shareRoom waits, up to shareWaitMax, for the shared home to have room for
// what is about to walk it. It's agent.Manager.ShareRoom.
func (s *Server) shareRoom(ctx context.Context, what string) {
	deadline := time.Now().Add(shareWaitMax)
	for s.shareBusy() {
		if time.Now().After(deadline) {
			s.logf("shared home: %s waited %s for virtiofsd to have room, and goes ahead", what, shareWaitMax)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(shareWaitPoll):
		}
	}
}

// diagnosticsFiles is GET /v1/diagnostics/files.
func (s *Server) diagnosticsFiles(w http.ResponseWriter, r *http.Request) error {
	out := api.DiagnosticsFiles{Daemon: ownFiles(), System: systemFiles(), Share: s.shareLast()}
	out.Waiting = shareFull(out.Share)
	return writeJSON(w, http.StatusOK, out)
}

// systemFiles is the kernel's file-nr: files open, against fs.file-max. A
// file-max of LONG_MAX, systemd's default, is no limit.
func systemFiles() api.FileCount {
	b, err := os.ReadFile("/proc/sys/fs/file-nr")
	if err != nil {
		return api.FileCount{}
	}
	return parseFileNr(string(b))
}

func parseFileNr(s string) api.FileCount {
	f := strings.Fields(s)
	if len(f) != 3 {
		return api.FileCount{}
	}
	open, _ := strconv.ParseInt(f[0], 10, 64)
	max, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || max >= 1<<62 {
		max = 0
	}
	return api.FileCount{Open: open, Limit: max}
}

// countOwnFDs is how many file descriptors the daemon has open.
func countOwnFDs() int64 {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return int64(len(entries))
}
