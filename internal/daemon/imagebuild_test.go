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
