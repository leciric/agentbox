package daemon

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// fakeJobStore is a jobStore whose FinishJob blocks until the test releases
// it, so a test can observe exactly what is (and isn't) visible while a
// job's terminal write is still in flight.
type fakeJobStore struct {
	releaseFinish  chan struct{}
	finishCalled   chan struct{}
	finishReturned chan struct{}

	mu   sync.Mutex
	jobs map[string]state.Job
}

func newFakeJobStore() *fakeJobStore {
	return &fakeJobStore{
		releaseFinish:  make(chan struct{}),
		finishCalled:   make(chan struct{}),
		finishReturned: make(chan struct{}),
		jobs:           map[string]state.Job{},
	}
}

func (s *fakeJobStore) AddJob(ctx context.Context, j state.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j
	return nil
}

func (s *fakeJobStore) FinishJob(ctx context.Context, j state.Job) error {
	close(s.finishCalled)
	<-s.releaseFinish
	s.mu.Lock()
	s.jobs[j.ID] = j
	s.mu.Unlock()
	close(s.finishReturned)
	return nil
}

func (s *fakeJobStore) Job(ctx context.Context, id string) (state.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return state.Job{}, errors.New("job not found")
	}
	return j, nil
}

func (s *fakeJobStore) Jobs(ctx context.Context, limit int) ([]state.Job, error) { return nil, nil }

// TestJobFinishIsDurableBeforeVisible pins the ordering that
// TestFailedCreateJobRollsBack caught broken: a job's terminal status must
// not become visible in memory (via snapshot, and so via the API and events)
// until it has actually been written to the store. Without that ordering, a
// daemon restart right after a job fails or succeeds could bring it back
// looking like it's still running, forever.
func TestJobFinishIsDurableBeforeVisible(t *testing.T) {
	t.Parallel()
	store := newFakeJobStore()
	js := newJobs(context.Background(), store, newBroker())

	fnReturned := make(chan struct{})
	j, err := js.start("create", "x", func(ctx context.Context, log io.Writer) (any, error) {
		defer close(fnReturned)
		return nil, errors.New("simulated failure")
	})
	if err != nil {
		t.Fatal(err)
	}

	<-fnReturned
	<-store.finishCalled

	// The store write is blocked: nothing should show this job as finished
	// yet. This isn't a timing check — commitFinish is sequenced strictly
	// after FinishJob returns in the same goroutine, so if FinishJob hasn't
	// returned, the in-memory status is guaranteed to still be "running".
	if info := j.snapshot(); info.Status != api.JobRunning {
		t.Fatalf("job visible as %q before its store write returned", info.Status)
	}
	select {
	case <-store.finishReturned:
		t.Fatal("store.FinishJob returned before the test released it")
	default:
	}

	close(store.releaseFinish)
	<-store.finishReturned

	waitFor(t, "the job to finish", func() bool { return j.snapshot().Status != api.JobRunning })
	info := j.snapshot()
	if info.Status != api.JobFailed || info.Error != "simulated failure" {
		t.Fatalf("job after finishing = %+v", info)
	}
	record, err := store.Job(context.Background(), info.ID)
	if err != nil || record.Status != api.JobFailed {
		t.Fatalf("stored job = %+v, %v", record, err)
	}
}

// A finished job lets its log go from memory, and its log is read from the
// store from then on.
func TestFinishedJobLogComesFromTheStoreOnceDropped(t *testing.T) {
	t.Parallel()
	store := newFakeJobStore()
	close(store.releaseFinish)
	js := newJobs(context.Background(), store, newBroker())
	j, err := js.start("create", "x", func(ctx context.Context, log io.Writer) (any, error) {
		_, _ = io.WriteString(log, "one\ntwo\n")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the job to finish", func() bool { return j.snapshot().Done() })
	j.dropLog()
	if lines, kept := j.keptLog(); kept || lines != nil {
		t.Fatalf("keptLog() after dropLog = %q, %v; want nothing", lines, kept)
	}
	info, lines, err := js.lookup(context.Background(), j.snapshot().ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != api.JobSucceeded || len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Fatalf("lookup() = %+v, %q; want the stored log", info, lines)
	}
}
