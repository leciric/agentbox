package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/image"
)

// buildingIncus has no base image until "$INCUS_LOG.built" exists, which the
// tests' stand-in for a build makes.
const buildingIncus = `echo "$*" >>"$INCUS_LOG"
case "$1" in
  list) echo '[]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) if [ -f "$INCUS_LOG.built" ]; then echo '["/1.0/instances/agentbox-base/snapshots/ready"]'; else echo 'Error: not found' >&2; exit 1; fi ;;
      */snapshots) echo '[]' ;;
      *) echo '{}' ;;
    esac ;;
  *) echo '[]' ;;
esac
`

// startFakeBuild stands in for an image build the user started: it holds
// the image until finish is called, and then makes the base or fails.
func startFakeBuild(t *testing.T, d testDaemon, root string) (id string, finish func(err error)) {
	t.Helper()
	if !d.srv.claimImage() {
		t.Fatal("the image was busy")
	}
	result := make(chan error, 1)
	j, err := d.srv.launchJob("image-build", "agentbox-base/ready", func(ctx context.Context, log io.Writer) (any, error) {
		defer d.srv.releaseImage()
		_, _ = io.WriteString(log, "==> Downloading the published image\n")
		err := <-result
		if err == nil {
			err = os.WriteFile(filepath.Join(root, "incus.log.built"), nil, 0o644)
		}
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	d.srv.setImagePhase(func(w *imageWork) { w.build = j.ID })
	return j.ID, func(err error) { result <- err }
}

// syncBuffer is a log two goroutines may touch.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func imageStatusOf(t *testing.T, d testDaemon) api.ImageStatus {
	t.Helper()
	out, err := d.srv.imageBuildStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// While the first image builds, Setup counts the step as done, the top bar
// can follow it, and what needs the image waits for it and then carries on.
func TestWaitingForTheImageBuild(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, buildingIncus)
	if st := imageStatusOf(t, d); st.Ready || st.Build != api.ImageBuildIdle {
		t.Fatalf("before a build: %+v", st)
	}
	id, finish := startFakeBuild(t, d, root)

	check := setupCheck(t, d, "image")
	if check.Status != api.SetupBuilding || check.Job != id {
		t.Errorf("Setup while it builds: %+v", check)
	}

	var log syncBuffer
	waited := make(chan error, 1)
	go func() { waited <- d.srv.awaitImage(context.Background(), &log) }()
	deadline := time.Now().Add(10 * time.Second)
	for imageStatusOf(t, d).Waiting != 1 {
		if time.Now().After(deadline) {
			t.Fatal("nothing waited for the build")
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := imageStatusOf(t, d)
	if st.Build != api.ImageBuilding || st.Job != id || st.Step != "Downloading the published image" {
		t.Errorf("status while it builds: %+v", st)
	}
	select {
	case err := <-waited:
		t.Fatalf("awaitImage returned before the build finished: %v", err)
	default:
	}

	finish(nil)
	if err := <-waited; err != nil {
		t.Fatalf("awaitImage = %v", err)
	}
	if !strings.Contains(log.String(), "Waiting for the base image") || !strings.Contains(log.String(), "The base image is ready") {
		t.Errorf("its log:\n%s", log.String())
	}
	d.srv.jobs.wait()
	if st := imageStatusOf(t, d); !st.Ready || st.Build != api.ImageBuildIdle || st.Waiting != 0 {
		t.Errorf("after the build: %+v", st)
	}
}

// A failed build fails what waited for it with the build's reason and the way
// to retry, and stays in the status until a build succeeds.
func TestWaitingForAFailedImageBuild(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, buildingIncus)
	id, finish := startFakeBuild(t, d, root)
	waited := make(chan error, 1)
	go func() { waited <- d.srv.awaitImage(context.Background(), io.Discard) }()
	for imageStatusOf(t, d).Waiting != 1 {
		time.Sleep(10 * time.Millisecond)
	}
	finish(errors.New("debootstrap: no network"))
	err := <-waited
	if err == nil || !strings.Contains(err.Error(), "debootstrap: no network") || !strings.Contains(err.Error(), "Retry") {
		t.Errorf("awaitImage = %v", err)
	}
	d.srv.jobs.wait()
	st := imageStatusOf(t, d)
	if st.Build != api.ImageBuildFailed || st.Job != id || !strings.Contains(st.Error, "no network") {
		t.Errorf("status after it failed: %+v", st)
	}
	if check := setupCheck(t, d, "image"); check.Status != api.SetupMissing || !strings.Contains(check.Detail, "the last build failed (debootstrap: no network)") {
		t.Errorf("Setup after it failed: %+v", check)
	}
}

// An agent asked for while the first image downloads is listed at once as
// waiting for it, with the download's progress, and is made by itself once
// the image is ready.
func TestAgentWaitsForTheImageAndStartsWhenReady(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, buildingIncus)
	ctx := context.Background()
	addProjects(t, d, "hello")

	if !d.srv.claimImage() {
		t.Fatal("the image was busy")
	}
	result := make(chan error, 1)
	j, err := d.srv.launchJob("image-build", "agentbox-base/ready", func(ctx context.Context, log io.Writer) (any, error) {
		defer d.srv.releaseImage()
		_, _ = io.WriteString(log, "==> Downloading the published image\n40% of 1800 MB\n")
		if err := <-result; err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(root, "incus.log.built"), nil, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	d.srv.setImagePhase(func(w *imageWork) { w.build = j.ID })

	created, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello", AI: "none", Title: "Waits"})
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitForAgent(t, d, func(a api.Agent) bool { return a.WaitingForImage })
	if waiting.State != "initializing" || waiting.Title != "Waits" {
		t.Errorf("the waiting agent: %+v", waiting)
	}
	if _, err := os.Stat(waiting.Worktree); err != nil {
		t.Errorf("its worktree isn't there while it waits: %v", err)
	}
	// The agent is listed as waiting a moment before its job counts as one.
	for deadline := time.Now().Add(10 * time.Second); imageStatusOf(t, d).Waiting != 1 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if st := imageStatusOf(t, d); st.Step != "Downloading the published image" || st.Progress != "40% of 1800 MB" || st.Waiting != 1 {
		t.Errorf("the image while the agent waits: %+v", st)
	}
	if log, _ := os.ReadFile(filepath.Join(root, "incus.log")); strings.Contains(string(log), "copy ") {
		t.Errorf("a machine was copied before the image was ready:\n%s", log)
	}

	result <- nil
	// The create goes on by itself: what comes after the wait, the machine
	// copied and booted, is the fake's to fail or not, and the runner's disk
	// space to refuse, so the job's own word that it went on is what counts.
	job, _ := d.srv.jobs.get(created.ID)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(strings.Join(job.logLines(), "\n"), "The base image is ready") || d.srv.waitingOnImage(waiting.Ref) {
		if time.Now().After(deadline) {
			t.Fatalf("the create didn't go on once the image was ready (waiting: %v):\n%s", d.srv.waitingOnImage(waiting.Ref), strings.Join(job.logLines(), "\n"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if done := job.snapshot(); done.Status == api.JobFailed && strings.Contains(done.Error, "base image") {
		t.Errorf("the create failed on the image once it was ready: %s", done.Error)
	}
	_, _ = d.client.CancelJob(ctx, created.ID)
	waitJobDone(t, d, created.ID)
}

// A build that fails, the local one after the download included, fails the
// agents waiting for it, which leave nothing behind.
func TestAgentWaitingForAFailedImage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, buildingIncus)
	ctx := context.Background()
	addProjects(t, d, "hello")
	_, finish := startFakeBuild(t, d, root)

	created, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello", AI: "none"})
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitForAgent(t, d, func(a api.Agent) bool { return a.WaitingForImage })
	finish(errors.New("debootstrap: no network"))
	if job := waitJobDone(t, d, created.ID); job.Status != api.JobFailed || !strings.Contains(job.Error, "debootstrap: no network") {
		t.Errorf("the create job: %+v", job)
	}
	if agents, _ := d.client.Agents(ctx, "hello"); len(agents) != 0 {
		t.Errorf("agents left after the image failed: %+v", agents)
	}
	if _, err := os.Stat(waiting.Worktree); !os.IsNotExist(err) {
		t.Errorf("worktree left after the image failed (stat: %v)", err)
	}
}

// A new installation's first image starts by itself once Incus and the user
// mapping are ready, once only, and never over an image that's there.
func TestFirstImageIsFetchedOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, buildingIncus)
	ctx := context.Background()
	mapped := errors.New("no mapping yet")
	started := 0
	d.srv.firstImage.checkHost = func(image.User) error { return mapped }
	d.srv.firstImage.start = func(context.Context) (api.Job, error) {
		started++
		id, finish := startFakeBuild(t, d, root)
		t.Cleanup(func() { finish(nil) })
		return api.Job{ID: id}, nil
	}
	if d.srv.tryFirstImage(ctx) || started != 0 {
		t.Fatalf("started %d build(s) before the user mapping was there", started)
	}
	mapped = nil
	if !d.srv.tryFirstImage(ctx) || started != 1 {
		t.Fatalf("started %d build(s) once it could, want 1", started)
	}
	if !d.srv.tryFirstImage(ctx) || started != 1 {
		t.Fatalf("started %d build(s) with one under way, want 1", started)
	}
	if check := setupCheck(t, d, "image"); check.Status != api.SetupBuilding {
		t.Errorf("Setup while the first image is fetched: %+v", check)
	}

	built := t.TempDir()
	if err := os.WriteFile(filepath.Join(built, "incus.log.built"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ready := startTestDaemon(t, built, buildingIncus)
	ready.srv.firstImage.checkHost = func(image.User) error { return nil }
	ready.srv.firstImage.start = func(context.Context) (api.Job, error) {
		t.Error("started a build over an image that's there")
		return api.Job{}, nil
	}
	if !ready.srv.tryFirstImage(ctx) {
		t.Error("kept looking with an image there")
	}
}

func waitForAgent(t *testing.T, d testDaemon, match func(api.Agent) bool) api.Agent {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		agents, err := d.client.Agents(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range agents {
			if match(a) {
				return a
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no agent as wanted")
	return api.Agent{}
}

func waitJobDone(t *testing.T, d testDaemon, id string) api.Job {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		j, err := d.client.Job(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != api.JobRunning {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s never finished", id)
	return api.Job{}
}

// A build that finishes between awaitImage's look at the image and its look
// for a build leaves an image, not a second build to start: here the image
// is missing the first time it's looked at and there the next.
func TestAwaitImageAfterABuildJustFinished(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, `echo "$*" >>"$INCUS_LOG"
case "$1" in
  query)
    case "$2" in
      */agentbox-base/snapshots)
        if [ -f "$INCUS_LOG.missing" ]; then rm "$INCUS_LOG.missing"; echo 'Error: not found' >&2; exit 1; fi
        echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      *) echo '{}' ;;
    esac ;;
  *) echo '[]' ;;
esac
`)
	// The daemon looks at the image as it starts: the missing answer is
	// armed once it has.
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if log, _ := os.ReadFile(filepath.Join(root, "incus.log")); strings.Contains(string(log), "agentbox-base/snapshots") {
			break
		}
	}
	if err := os.WriteFile(filepath.Join(root, "incus.log.missing"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.awaitImage(context.Background(), io.Discard); err != nil {
		t.Fatalf("awaitImage = %v, want the image that's there now", err)
	}
	if j := d.srv.lastImageBuild(); j != nil {
		t.Errorf("started build %s over an image that was there", j.snapshot().ID)
	}
}
