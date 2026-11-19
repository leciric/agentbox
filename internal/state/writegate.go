package state

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
)

// state.db has one writer at a time, and the daemon is nearly the only process
// that writes it: the CLI and the app reach it through the daemon's socket. So
// the daemon's writers queue for SQLite's write lock here, in Go, rather than
// in SQLite's busy handler.
//
// The busy handler is a poll, not a queue: every waiting connection sleeps and
// retries on its own, and nothing makes the one that has waited longest go
// next. With a dozen goroutines writing at once (four or five agents' chats
// streaming, their usage, PR watches, memory events) one of them could lose
// every race for longer than busy_timeout, and its write failed with "database
// is locked (5) (SQLITE_BUSY)" although nobody held the lock that long — 662
// times in 37 minutes on 6 Nov 2026, 202 of them a chat's conversation. A
// buffered channel's waiters are woken in the order they came, so here a
// writer waits only for the writers ahead of it.
//
// What still waits in SQLite's busy handler is a write from another process
// (a front end migrating, a test) and the rare write that misses the gate
// (gateWait); either retries SQLITE_BUSY with backoff (writeRetry) rather than
// fail the write that matters.
//
// The gate is also where a write that holds the lock too long is seen: every
// one past SlowWrite is logged with who began it.

// SlowWrite is how long a write may hold the write lock before it is logged.
const SlowWrite = 500 * time.Millisecond

// Overridable by tests.
var (
	// gateWait is how long a writer waits for the gate before going ahead
	// without it, to SQLite's own lock. Past it something is wrong — one
	// transaction holding the lock for half a minute, or a goroutine that
	// writes on a second connection while its own transaction holds the
	// gate, which would otherwise wait for itself for ever — and the log
	// says who held it.
	gateWait = 30 * time.Second
	// writeRetry is how long a write keeps retrying SQLITE_BUSY: two minutes
	// of somebody else's lock is a daemon that's stuck, not busy.
	writeRetry = 2 * time.Minute
	// busyTimeout is SQLite's own wait for the lock, in each attempt.
	busyTimeout = 5 * time.Second
)

// writeGate lets one write at a time at state.db's write lock.
type writeGate struct {
	slot chan struct{}
	// holder is who holds the slot now, for the log of a writer that gave up
	// waiting for it.
	holder atomic.Pointer[hold]
	logf   atomic.Pointer[func(format string, args ...any)]
}

func newWriteGate() *writeGate {
	return &writeGate{slot: make(chan struct{}, 1)}
}

func (g *writeGate) log(format string, args ...any) {
	if f := g.logf.Load(); f != nil {
		(*f)(format, args...)
	}
}

// hold is one write's turn at the lock.
type hold struct {
	g      *writeGate
	pcs    [32]uintptr
	n      int
	since  time.Time
	waited time.Duration
	gated  bool // false: it went ahead without the gate after gateWait
	once   sync.Once
}

// acquire waits for the gate, as long as ctx allows and up to gateWait.
func (g *writeGate) acquire(ctx context.Context) (*hold, error) {
	h := &hold{g: g}
	h.n = runtime.Callers(3, h.pcs[:])
	start := time.Now()
	select {
	case g.slot <- struct{}{}:
		h.gated = true
	default:
		timer := time.NewTimer(gateWait)
		defer timer.Stop()
		select {
		case g.slot <- struct{}{}:
			h.gated = true
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			who := "nobody"
			if cur := g.holder.Load(); cur != nil {
				who = fmt.Sprintf("%s, for %s", cur.caller(), time.Since(cur.since).Round(time.Millisecond))
			}
			g.log("sqlite: %s waited %s for the write lock (held by %s); going ahead without waiting more", h.caller(), gateWait, who)
		}
	}
	h.since = time.Now()
	h.waited = h.since.Sub(start)
	if h.gated {
		g.holder.Store(h)
	}
	return h, nil
}

// release gives the gate back, once, and logs a write that held it too long.
func (h *hold) release() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		held := time.Since(h.since)
		if h.gated {
			h.g.holder.CompareAndSwap(h, nil)
			<-h.g.slot
		}
		if held > SlowWrite {
			h.g.log("sqlite: slow write: %s held the write lock for %s (waited %s for it)",
				h.caller(), held.Round(time.Millisecond), h.waited.Round(time.Millisecond))
		}
	})
}

// caller names the code that began the write, "state.(*Store).SaveChatItems
// ← chat.(*conversation).save": the first two frames that aren't
// database/sql, the driver, this file or the trace package's wrapper.
func (h *hold) caller() string {
	frames := runtime.CallersFrames(h.pcs[:h.n])
	var names []string
	for len(names) < 2 {
		f, more := frames.Next()
		name := f.Function
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if name != "" && !plumbing(f.Function, name) {
			names = append(names, name)
		}
		if !more {
			break
		}
	}
	if len(names) == 0 {
		return "?"
	}
	return strings.Join(names, " ← ")
}

func plumbing(full, name string) bool {
	return strings.HasPrefix(full, "database/sql.") ||
		strings.HasPrefix(full, "modernc.org/") ||
		strings.HasPrefix(full, "runtime.") ||
		strings.HasPrefix(name, "trace.BeginTx") ||
		strings.HasPrefix(name, "state.(*gated") ||
		strings.HasPrefix(name, "state.(*writeGate)") ||
		strings.HasPrefix(name, "state.retryBusy")
}

// isBusy is SQLITE_BUSY, in any of its extended forms.
func isBusy(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code()&0xff == 5
}

// retryBusy runs fn until it doesn't fail with SQLITE_BUSY, backing off, for
// up to writeRetry. Only for what SQLite does all or nothing of: beginning a
// transaction, or one statement outside of any.
func retryBusy(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(writeRetry)
	backoff := 20 * time.Millisecond
	for {
		err := fn()
		if !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Second)
	}
}

// gatedConnector opens modernc connections wrapped in the gate.
type gatedConnector struct {
	dsn  string
	drv  *sqlite.Driver
	gate *writeGate
}

func (c *gatedConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.drv.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &gatedConn{conn: conn.(sqliteConn), gate: c.gate}, nil
}

func (c *gatedConnector) Driver() driver.Driver { return c.drv }

// sqliteConn is what modernc's connection implements, and so what database/sql
// would have used of it directly.
type sqliteConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

// gatedConn takes the gate for a transaction, from its beginning to its end,
// and for a statement executed outside of one. Queries go straight through:
// in WAL, readers don't wait for the writer.
type gatedConn struct {
	conn sqliteConn
	gate *writeGate
	tx   *hold // the open transaction's turn, nil outside one
}

func (c *gatedConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *gatedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	st, err := c.conn.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &gatedStmt{stmt: st.(sqliteStmt), conn: c}, nil
}

func (c *gatedConn) Close() error {
	c.tx.release()
	c.tx = nil
	return c.conn.Close()
}

func (c *gatedConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *gatedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	h, err := c.gate.acquire(ctx)
	if err != nil {
		return nil, err
	}
	var tx driver.Tx
	err = retryBusy(ctx, func() (err error) {
		tx, err = c.conn.BeginTx(ctx, opts)
		return err
	})
	if err != nil {
		h.release()
		return nil, err
	}
	c.tx = h
	return &gatedTx{tx: tx, conn: c}, nil
}

func (c *gatedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.tx != nil {
		return c.conn.ExecContext(ctx, query, args)
	}
	h, err := c.gate.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer h.release()
	var res driver.Result
	err = retryBusy(ctx, func() (err error) {
		res, err = c.conn.ExecContext(ctx, query, args)
		return err
	})
	return res, err
}

func (c *gatedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.conn.QueryContext(ctx, query, args)
}

func (c *gatedConn) Ping(ctx context.Context) error         { return c.conn.Ping(ctx) }
func (c *gatedConn) ResetSession(ctx context.Context) error { return c.conn.ResetSession(ctx) }
func (c *gatedConn) IsValid() bool                          { return c.conn.IsValid() }

type gatedTx struct {
	tx   driver.Tx
	conn *gatedConn
}

func (t *gatedTx) Commit() error {
	defer t.end()
	return t.tx.Commit()
}

func (t *gatedTx) Rollback() error {
	defer t.end()
	return t.tx.Rollback()
}

func (t *gatedTx) end() {
	t.conn.tx.release()
	t.conn.tx = nil
}

type sqliteStmt interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}

// gatedStmt is a prepared statement: inside a transaction it is the
// transaction's, and outside one its Exec takes the gate as the connection's
// does.
type gatedStmt struct {
	stmt sqliteStmt
	conn *gatedConn
}

func (s *gatedStmt) Close() error  { return s.stmt.Close() }
func (s *gatedStmt) NumInput() int { return s.stmt.NumInput() }

func (s *gatedStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), named(args))
}

func (s *gatedStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.stmt.QueryContext(context.Background(), named(args))
}

func (s *gatedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if s.conn.tx != nil {
		return s.stmt.ExecContext(ctx, args)
	}
	h, err := s.conn.gate.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer h.release()
	var res driver.Result
	err = retryBusy(ctx, func() (err error) {
		res, err = s.stmt.ExecContext(ctx, args)
		return err
	})
	return res, err
}

func (s *gatedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.stmt.QueryContext(ctx, args)
}

func named(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, v := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}
