package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// TestSetAgentStatusRetriesBusyWriter holds the write lock from another
// connection for longer than the store's busy_timeout and checks that the
// final write of a create is retried instead of returned as SQLITE_BUSY.
func TestSetAgentStatusRetriesBusyWriter(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := open(path, 15*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	conn, err := raw.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("taking the write lock: %v", err)
	}

	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(200 * time.Millisecond)
		if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
			t.Errorf("releasing the write lock: %v", err)
		}
		_ = conn.Close()
	}()

	start := time.Now()
	if err := st.SetAgentStatus(ctx, "pawly", "agent-01", AgentReady); err != nil {
		t.Fatalf("SetAgentStatus returned the busy writer instead of retrying: %v", err)
	}
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Fatalf("SetAgentStatus returned after %s, before the other writer let go", waited)
	}
	<-released
}
