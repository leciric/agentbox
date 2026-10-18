package trace

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Tx is a SQLite transaction with a span from its start to its commit or
// rollback, named after the function that began it ("sqlite
// state.(*Store).AddJob"). state.db's transactions begin with BEGIN
// IMMEDIATE (_txlock=immediate in state.Open), which takes the write lock
// there or waits for it, so how long beginning took is how long it waited for
// the lock: the span's lockWaitMs, and a stall past SlowLock.
//
// Statements outside a transaction wait on the same lock (busy_timeout), but
// database/sql gives no place to time that wait apart from the statement.
type Tx struct {
	*sql.Tx
	span *Live
}

// BeginTx begins a transaction on db, measured.
func BeginTx(ctx context.Context, db *sql.DB) (*Tx, error) {
	if current.Load() == nil {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		return &Tx{Tx: tx}, nil
	}
	_, span := Start(ctx, "sqlite "+caller())
	start := time.Now()
	tx, err := db.BeginTx(ctx, nil)
	wait := time.Since(start)
	span.Set("lockWaitMs", ms(wait))
	if wait > SlowLock {
		span.Warn("sqlite lock wait")
	}
	if err != nil {
		span.End(err)
		return nil, err
	}
	return &Tx{Tx: tx, span: span}, nil
}

// errRolledBack is a rolled back transaction's span's error: a transaction
// here is rolled back when what it was doing failed.
var errRolledBack = errors.New("rolled back")

// Commit commits the transaction and ends its span.
func (t *Tx) Commit() error {
	err := t.Tx.Commit()
	t.span.End(err)
	return err
}

// Rollback rolls the transaction back and ends its span, unless it was
// committed already: the deferred Rollback after a Commit.
func (t *Tx) Rollback() error {
	err := t.Tx.Rollback()
	if !errors.Is(err, sql.ErrTxDone) {
		t.span.End(errRolledBack)
	}
	return err
}

// callers maps a call site to the name of its function, worked out once.
var callers sync.Map

// caller is the name of the function that called BeginTx, without the
// module's path: "state.(*Store).AddJob".
func caller() string {
	pc, _, _, ok := runtime.Caller(2)
	if !ok {
		return "?"
	}
	if name, ok := callers.Load(pc); ok {
		return name.(string)
	}
	name := "?"
	if f := runtime.FuncForPC(pc); f != nil {
		name = f.Name()
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
	}
	callers.Store(pc, name)
	return name
}
