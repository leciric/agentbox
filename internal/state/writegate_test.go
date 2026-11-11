package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// contend runs the load of 6 Nov 2026 against db: another connection, as if
// another process, takes the write lock and keeps it for longer than
// busy_timeout; one transaction of this process's own holds it a while, as a
// commit stuck in a slow fsync would; and many writers save conversations at
// once. It answers how many of their writes failed.
func contend(t *testing.T, path string, write func(ctx context.Context, i int) error, slow func(ctx context.Context) error) []error {
	t.Helper()
	ctx := context.Background()
	other, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('contention', 'x') ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(4 * busyTimeout)
		_, _ = conn.ExecContext(ctx, `COMMIT`)
		close(released)
	}()

	const writers = 40
	errs := make([]error, writers+1)
	var wg sync.WaitGroup
	wg.Add(writers + 1)
	go func() {
		defer wg.Done()
		errs[writers] = slow(ctx)
	}()
	for i := range writers {
		go func() {
			defer wg.Done()
			errs[i] = write(ctx, i)
		}()
	}
	wg.Wait()
	<-released
	var failed []error
	for _, err := range errs {
		if err != nil {
			failed = append(failed, err)
		}
	}
	return failed
}

func shortBusyTimeout(t *testing.T) {
	old := busyTimeout
	busyTimeout = 150 * time.Millisecond
	t.Cleanup(func() { busyTimeout = old })
}

// Under that load, every write lands, and the transaction that held the lock
// too long is logged with who began it.
func TestNoWriteLostUnderContention(t *testing.T) {
	shortBusyTimeout(t)
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.AddProject(context.Background(), Project{Name: "p", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var logged []string
	s.SetLog(func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	})

	names := make([]string, 40)
	failed := contend(t, path, func(ctx context.Context, i int) error {
		for j := range 5 {
			item := ChatItem{ID: fmt.Sprintf("item-%d", j), Position: int64(j), Data: []byte(fmt.Sprintf(`{"kind":"assistant","text":"answer %d of chat %d"}`, j, i))}
			if err := s.SaveChatItems(ctx, "p", fmt.Sprintf("agent-x%d", i), []ChatItem{item}); err != nil {
				return err
			}
		}
		name, err := s.NextAgentName(ctx, "p", 1)
		names[i] = name
		return err
	}, func(ctx context.Context) error { return slowTransaction(ctx, s.db) })
	for _, err := range failed {
		t.Errorf("a write failed: %v", err)
	}

	var items int
	if err := s.db.QueryRow(`SELECT count(*) FROM chat_items WHERE project = 'p'`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 200 {
		t.Errorf("%d chat items stored, want all 200", items)
	}
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || seen[n] {
			t.Errorf("agent name %q handed out twice or not at all: %v", n, names)
		}
		seen[n] = true
	}
	if hits := searchCount(t, s, "answer 3 of chat 7"); hits != 1 {
		t.Errorf("search found %d items, want 1: the index lost a write", hits)
	}

	mu.Lock()
	defer mu.Unlock()
	var slowLine string
	for _, l := range logged {
		if strings.Contains(l, "slow write") && strings.Contains(l, "slowTransaction") {
			slowLine = l
		}
	}
	if slowLine == "" {
		t.Errorf("no slow-write line naming slowTransaction in the log: %q", logged)
	}
	t.Logf("the log: %s", slowLine)
}

func slowTransaction(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('slow', 'x')`); err != nil {
		return err
	}
	time.Sleep(SlowWrite + 300*time.Millisecond)
	return tx.Commit()
}

func searchCount(t *testing.T, s *Store, text string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM chat_items_fts WHERE chat_items_fts MATCH ?`, `"`+text+`"`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The same load against the database opened the way it was before the gate —
// SQLite's busy handler as the only queue — loses writes: what the test above
// would see without it.
func TestUngatedWritesFailUnderContention(t *testing.T) {
	shortBusyTimeout(t)
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddProject(context.Background(), Project{Name: "p", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	plain, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)", path, busyTimeout.Milliseconds()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close() }()
	bare := &Store{db: plain}

	failed := contend(t, path, func(ctx context.Context, i int) error {
		item := ChatItem{ID: "item", Data: []byte(`{"kind":"assistant","text":"hello"}`)}
		return bare.SaveChatItems(ctx, "p", fmt.Sprintf("agent-x%d", i), []ChatItem{item})
	}, func(ctx context.Context) error { return slowTransaction(ctx, plain) })
	if len(failed) == 0 {
		t.Fatal("no write failed without the gate: the load doesn't reproduce the contention")
	}
	if !isBusy(failed[0]) {
		t.Errorf("failed with %v, want SQLITE_BUSY", failed[0])
	}
	t.Logf("%d of 41 writers failed without the gate, the first with: %v", len(failed), failed[0])
}

// A rewrite that changes nothing leaves the chat's search index alone, and one
// that changes the text still updates it.
func TestChatSearchIndexFollowsRewrites(t *testing.T) {
	s := openSearchStore(t)
	ctx := context.Background()
	save := func(text string) {
		t.Helper()
		if err := s.SaveChatItems(ctx, "p", "a", []ChatItem{{ID: "1", Data: []byte(`{"kind":"assistant","text":"` + text + `"}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	save("first words")
	save("first words")
	if n := searchCount(t, s, "first words"); n != 1 {
		t.Fatalf("found %d, want 1", n)
	}
	save("second words")
	if n := searchCount(t, s, "first words"); n != 0 {
		t.Errorf("the old text is still found %d times", n)
	}
	if n := searchCount(t, s, "second words"); n != 1 {
		t.Errorf("the new text is found %d times, want 1", n)
	}
}
