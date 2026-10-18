// Package trace records how long the daemon's work takes, as spans written to
// NDJSON files next to state.db, and the stalls among them to a smaller file
// of their own that lasts for days. It is modelled on T3 Code's local trace
// file: everything stays on the machine, nothing is exported, and the files
// are read directly (`agentbox trace summary`) so they answer while the daemon
// is hung or stopped.
//
// Spans go at the boundaries — an API request, a job, an Incus call, a git
// command, an ACP turn, a SQLite transaction — never around small helpers.
// With no recorder installed (tests, the CLI) Start costs a load and a
// compare; with one, a span is two clock reads, a context value and a
// channel send, and the JSON is encoded and written by one goroutine of its
// own, in batches. A span that finds the channel full is dropped and counted
// rather than waited for.
package trace

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"agentbox/internal/api"
)

// Span is a finished span, as written: one line of a span file.
type Span = api.TraceSpan

// The files, in the same directory as state.db.
const (
	SpansFile  = "spans.ndjson"
	StallsFile = "stalls.ndjson"
)

// The stall thresholds: past these, a span is a warning, kept in the stall
// file too.
const (
	// SlowRequest is how long the daemon may take to answer a request.
	SlowRequest = 2 * time.Second
	// SlowLock is how long a SQLite transaction may wait for the write lock.
	SlowLock = time.Second
)

// Limits is how much of one kind of file is kept: the file itself up to
// MaxBytes, then that many again in each of MaxFiles backups (.1 the newest).
type Limits struct {
	MaxBytes int64
	MaxFiles int
}

// Spans are rotated at 8 MiB, keeping three backups: 32 MiB, which on a busy
// daemon is a few hours. Stalls are rare, and their 512 KiB and one backup
// is thousands of them: days or weeks.
var (
	SpanLimits  = Limits{MaxBytes: 8 << 20, MaxFiles: 3}
	StallLimits = Limits{MaxBytes: 512 << 10, MaxFiles: 1}
)

// queueSize is how many spans may wait to be written before more are dropped.
const queueSize = 4096

// maxError is the longest error kept on a span, in bytes.
const maxError = 500

// Recorder writes spans to a directory's span file, and stalls to its stall
// file as well.
type Recorder struct {
	spans, stalls *rotating
	queue         chan Span
	done          chan struct{}
	dropped       atomic.Int64
	closeOnce     sync.Once
}

// Open starts a recorder writing to dir, which it creates.
func Open(dir string) (*Recorder, error) {
	return OpenLimits(dir, SpanLimits, StallLimits)
}

// OpenLimits is Open with other limits, for tests.
func OpenLimits(dir string, spans, stalls Limits) (*Recorder, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	r := &Recorder{
		spans:  &rotating{path: filepath.Join(dir, SpansFile), Limits: spans},
		stalls: &rotating{path: filepath.Join(dir, StallsFile), Limits: stalls},
		queue:  make(chan Span, queueSize),
		done:   make(chan struct{}),
	}
	go r.write()
	return r, nil
}

// Close writes what is queued and closes the files. Spans recorded after it
// are dropped.
func (r *Recorder) Close() error {
	r.closeOnce.Do(func() { close(r.queue) })
	<-r.done
	return errors.Join(r.spans.close(), r.stalls.close())
}

// Dropped is how many spans were dropped because the queue was full.
func (r *Recorder) Dropped() int64 { return r.dropped.Load() }

// Record queues a finished span for writing, filling in its ID when empty.
func (r *Recorder) Record(s Span) {
	if s.ID == "" {
		s.ID = newID()
	}
	if len(s.Error) > maxError {
		s.Error = s.Error[:maxError]
	}
	defer func() {
		// Closed: the daemon is stopping, and the span goes with it.
		if recover() != nil {
			r.dropped.Add(1)
		}
	}()
	select {
	case r.queue <- s:
	default:
		r.dropped.Add(1)
	}
}

// write is the recorder's goroutine: it takes what is queued, as much as is
// there, and writes it in one go.
func (r *Recorder) write() {
	defer close(r.done)
	var spans, stalls []byte
	for s := range r.queue {
		spans, stalls = spans[:0], stalls[:0]
		spans, stalls = appendSpan(spans, stalls, s)
	more:
		for len(spans) < 1<<20 {
			select {
			case s, ok := <-r.queue:
				if !ok {
					break more
				}
				spans, stalls = appendSpan(spans, stalls, s)
			default:
				break more
			}
		}
		r.spans.write(spans)
		if len(stalls) > 0 {
			r.stalls.write(stalls)
		}
	}
}

func appendSpan(spans, stalls []byte, s Span) ([]byte, []byte) {
	line, err := marshal(s)
	if err != nil {
		return spans, stalls
	}
	spans = append(spans, line...)
	if s.Level == api.SpanLevelWarn {
		stalls = append(stalls, line...)
	}
	return spans, stalls
}

// The recorder Start and Record use: none until the daemon installs one.
var current atomic.Pointer[Recorder]

// SetDefault installs r as the recorder spans go to; nil turns tracing off.
func SetDefault(r *Recorder) { current.Store(r) }

// Default is the installed recorder, or nil.
func Default() *Recorder { return current.Load() }

// Record queues a finished span on the installed recorder, if any.
func Record(s Span) {
	if r := current.Load(); r != nil {
		r.Record(s)
	}
}

// Live is a span being measured. A nil *Live is valid, and does nothing: it
// is what Start returns when nothing is recording.
type Live struct {
	rec   *Recorder
	start time.Time
	span  Span
	ended atomic.Bool
}

type ctxKey struct{}

// Start starts a span named name under the span in ctx, if any, and returns
// a context carrying it. attrs are key, value pairs.
func Start(ctx context.Context, name string, attrs ...any) (context.Context, *Live) {
	rec := current.Load()
	if rec == nil {
		return ctx, nil
	}
	l := &Live{rec: rec, start: time.Now(), span: Span{ID: newID(), Name: name}}
	if parent, ok := ctx.Value(ctxKey{}).(*Live); ok && parent != nil {
		l.span.Parent = parent.span.ID
	}
	l.Set(attrs...)
	return context.WithValue(ctx, ctxKey{}, l), l
}

// Begin starts a span with no context to carry it: a root, unless a parent
// is given.
func Begin(name string, attrs ...any) *Live {
	_, l := Start(context.Background(), name, attrs...)
	return l
}

// Set adds key, value pairs to the span's attributes. Not safe for use by
// several goroutines at once.
func (l *Live) Set(attrs ...any) {
	if l == nil {
		return
	}
	for i := 0; i+1 < len(attrs); i += 2 {
		k, ok := attrs[i].(string)
		if !ok {
			continue
		}
		if l.span.Attrs == nil {
			l.span.Attrs = make(map[string]any, len(attrs)/2)
		}
		l.span.Attrs[k] = attrs[i+1]
	}
}

// Warn marks the span a stall, for the reason given, which also keeps it in
// the stall file.
func (l *Live) Warn(reason string) {
	if l == nil {
		return
	}
	l.span.Level = api.SpanLevelWarn
	l.Set("stall", reason)
}

// Elapsed is how long the span has run.
func (l *Live) Elapsed() time.Duration {
	if l == nil {
		return 0
	}
	return time.Since(l.start)
}

// End finishes the span, failed if err isn't nil, and queues it. Only the
// first End counts.
func (l *Live) End(err error) {
	if l == nil || l.ended.Swap(true) {
		return
	}
	l.span.Start = l.start
	l.span.DurationMS = ms(time.Since(l.start))
	if err != nil {
		l.span.Error = err.Error()
	}
	l.rec.Record(l.span)
}

// ms is d in milliseconds, to the microsecond.
func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// MS is d in milliseconds, to the microsecond, as spans keep durations.
func MS(d time.Duration) float64 { return ms(d) }

// IDs are a random number for this process plus a counter: unique enough
// across restarts, and cheap.
var (
	idBase = rand.Uint64()
	idSeq  atomic.Uint64
)

func newID() string { return strconv.FormatUint(idBase+idSeq.Add(1), 16) }

// End is when a span ended.
func End(s Span) time.Time {
	return s.Start.Add(time.Duration(s.DurationMS * float64(time.Millisecond)))
}
