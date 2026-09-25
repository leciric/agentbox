package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// jobStore is what jobs needs from the store: small enough for a test to fake,
// so it can pin down the order jobs finish in without a real database.
type jobStore interface {
	AddJob(ctx context.Context, j state.Job) error
	FinishJob(ctx context.Context, j state.Job) error
	Job(ctx context.Context, id string) (state.Job, error)
	Jobs(ctx context.Context, limit int) ([]state.Job, error)
}

// jobs runs long operations detached from the request that started them, so a
// client can disconnect (or press Ctrl-C) while the job carries on.
type jobs struct {
	base   context.Context
	store  jobStore
	events *broker

	mu   sync.Mutex
	byID map[string]*job
	wg   sync.WaitGroup
}

// wait blocks until every job has finished, including rollbacks after a cancel.
func (js *jobs) wait() { js.wg.Wait() }

type job struct {
	events *broker

	mu      sync.Mutex
	info    api.Job
	lines   []string
	partial string
	changed chan struct{} // closed and replaced whenever lines or status change
	cancel  context.CancelFunc
}

func newJobs(base context.Context, store jobStore, events *broker) *jobs {
	return &jobs{base: base, store: store, events: events, byID: map[string]*job{}}
}

// start runs fn in the background as a job. Its log is fn's writer; its
// result is JSON-encoded when fn succeeds.
func (js *jobs) start(kind, target string, fn func(ctx context.Context, log io.Writer) (any, error)) (*job, error) {
	ctx, cancel := context.WithCancel(js.base)
	j := &job{
		events:  js.events,
		info:    api.Job{ID: newID(), Kind: kind, Target: target, Status: api.JobRunning, CreatedAt: time.Now()},
		changed: make(chan struct{}),
		cancel:  cancel,
	}
	if err := js.store.AddJob(ctx, state.Job{ID: j.info.ID, Kind: kind, Target: target, Status: api.JobRunning, CreatedAt: j.info.CreatedAt}); err != nil {
		cancel()
		return nil, err
	}
	js.mu.Lock()
	js.byID[j.info.ID] = j
	js.mu.Unlock()
	js.events.publish(api.EventJob, j.snapshot())

	js.wg.Add(1)
	go func() {
		defer js.wg.Done()
		defer cancel()
		result, err := fn(ctx, j)
		info, record := j.prepareFinish(result, err, ctx.Err())
		// Persist before this job's terminal status becomes visible to
		// anyone (a follower, a lookup, an event), so a client can never see
		// "failed" or "succeeded" for a job the store still has as running.
		_ = js.store.FinishJob(context.WithoutCancel(ctx), record)
		j.commitFinish(info)
		js.events.publish(api.EventJob, info)
	}()
	return j, nil
}

func (js *jobs) get(id string) (*job, bool) {
	js.mu.Lock()
	defer js.mu.Unlock()
	j, ok := js.byID[id]
	return j, ok
}

// list returns this daemon's jobs merged with older ones from the store, newest first.
func (js *jobs) list(ctx context.Context, limit int) ([]api.Job, error) {
	records, err := js.store.Jobs(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.Job, 0, len(records))
	for _, r := range records {
		if j, ok := js.get(r.ID); ok {
			out = append(out, j.snapshot())
		} else {
			out = append(out, fromRecord(r))
		}
	}
	return out, nil
}

// lookup returns a job's info and log, from memory or from the store.
func (js *jobs) lookup(ctx context.Context, id string) (api.Job, []string, error) {
	if j, ok := js.get(id); ok {
		return j.snapshot(), j.logLines(), nil
	}
	r, err := js.store.Job(ctx, id)
	if err != nil {
		return api.Job{}, nil, err
	}
	var lines []string
	if r.Log != "" {
		lines = strings.Split(r.Log, "\n")
	}
	return fromRecord(r), lines, nil
}

func fromRecord(r state.Job) api.Job {
	j := api.Job{ID: r.ID, Kind: r.Kind, Target: r.Target, Status: r.Status, Error: r.Error, CreatedAt: r.CreatedAt}
	if r.Result != "" {
		j.Result = json.RawMessage(r.Result)
	}
	if !r.FinishedAt.IsZero() {
		finished := r.FinishedAt
		j.FinishedAt = &finished
	}
	return j
}

// Write appends to the job's log, one event per complete line.
func (j *job) Write(p []byte) (int, error) {
	j.mu.Lock()
	text := j.partial + string(p)
	lines := strings.Split(text, "\n")
	j.partial = lines[len(lines)-1]
	complete := lines[:len(lines)-1]
	first := len(j.lines)
	j.lines = append(j.lines, complete...)
	if len(complete) > 0 {
		j.notifyLocked()
	}
	id := j.info.ID
	j.mu.Unlock()
	for i, line := range complete {
		j.events.publish(api.EventJobLog, api.JobLogLine{Job: id, N: first + i, Line: line})
	}
	return len(p), nil
}

// prepareFinish computes a job's terminal state without publishing it: the
// job isn't done until that state is durable, so nothing here may become
// visible through snapshot, follow or the store yet. Call commitFinish with
// the returned info once it has been written to the store.
func (j *job) prepareFinish(result any, err, ctxErr error) (api.Job, state.Job) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.partial != "" {
		j.lines = append(j.lines, j.partial)
		j.partial = ""
	}
	info := j.info
	now := time.Now()
	info.FinishedAt = &now
	switch {
	case err == nil:
		info.Status = api.JobSucceeded
		if raw, merr := json.Marshal(result); merr == nil {
			info.Result = raw
		}
	case errors.Is(ctxErr, context.Canceled):
		info.Status = api.JobCancelled
		info.Error = err.Error()
	default:
		info.Status = api.JobFailed
		info.Error = err.Error()
	}
	record := state.Job{
		ID:         info.ID,
		Status:     info.Status,
		Error:      info.Error,
		Result:     string(info.Result),
		Log:        strings.Join(j.lines, "\n"),
		FinishedAt: *info.FinishedAt,
	}
	return info, record
}

// commitFinish makes a job's terminal state (computed by prepareFinish, and
// by then durably stored) visible to followers, lookups and snapshots.
func (j *job) commitFinish(info api.Job) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.info = info
	j.notifyLocked()
}

func (j *job) notifyLocked() {
	close(j.changed)
	j.changed = make(chan struct{})
}

func (j *job) snapshot() api.Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.info
}

func (j *job) logLines() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.lines...)
}

// follow calls emit for every log line from `from` onwards until the job is
// done or ctx ends, and returns the job's final info.
func (j *job) follow(ctx context.Context, from int, emit func(string) error) (api.Job, error) {
	for {
		j.mu.Lock()
		lines := append([]string(nil), j.lines[min(from, len(j.lines)):]...)
		done := j.info.Status != api.JobRunning
		changed := j.changed
		info := j.info
		j.mu.Unlock()

		for _, line := range lines {
			if err := emit(line); err != nil {
				return info, err
			}
		}
		from += len(lines)
		if done {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return info, ctx.Err()
		case <-changed:
		}
	}
}

func newID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("reading random bytes: %v", err))
	}
	return hex.EncodeToString(b)
}
