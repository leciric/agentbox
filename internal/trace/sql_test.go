package trace

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// A transaction that waits past SlowLock for another's write lock is a stall,
// named after the function that began it, with how long it waited.
func TestALongLockWaitIsAStall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	// As state.Open opens it: BEGIN IMMEDIATE, and a busy timeout to wait.
	db, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	SetDefault(r)
	defer SetDefault(nil)

	holder, err := BeginTx(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() { done <- waitForTheLock(ctx, db) }()
	time.Sleep(SlowLock + 200*time.Millisecond)
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Rollback() // the deferred one after a commit: no second span
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	spans, _ := Read(dir, SpansFile, time.Time{})
	if len(spans) != 2 {
		t.Fatalf("spans = %+v, want the holder's and the waiter's", spans)
	}
	stalls, _ := Read(dir, StallsFile, time.Time{})
	if len(stalls) != 1 {
		t.Fatalf("stalls = %+v, want one", stalls)
	}
	s := stalls[0]
	if !strings.HasSuffix(s.Name, "waitForTheLock") || !strings.HasPrefix(s.Name, "sqlite trace.") {
		t.Errorf("name = %q, want it after the function that began it", s.Name)
	}
	if wait, _ := s.Attrs["lockWaitMs"].(float64); wait < float64(SlowLock/time.Millisecond) {
		t.Errorf("lockWaitMs = %v, want over %v", s.Attrs["lockWaitMs"], SlowLock)
	}
	if s.Error != "rolled back" || s.Attrs["stall"] != "sqlite lock wait" {
		t.Errorf("stall = %+v", s)
	}
}

func waitForTheLock(ctx context.Context, db *sql.DB) error {
	tx, err := BeginTx(ctx, db)
	if err != nil {
		return err
	}
	return tx.Rollback()
}
