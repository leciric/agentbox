package setupfunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/paths"
)

func TestKVMOutcome(t *testing.T) {
	t.Parallel()
	for err, want := range map[error]string{
		nil: OK,
		fmt.Errorf("the VM needs /dev/kvm (%w)", &fs.PathError{Op: "open", Path: "/dev/kvm", Err: fs.ErrNotExist}):   Missing,
		fmt.Errorf("the VM needs /dev/kvm (%w)", &fs.PathError{Op: "open", Path: "/dev/kvm", Err: fs.ErrPermission}): NoPermission,
		errors.New("device or resource busy"): Failed,
	} {
		if got := KVMOutcome(err); got != want {
			t.Errorf("KVMOutcome(%v) = %s, want %s", err, got, want)
		}
	}
}

func TestNoteKeepsEachStepAndOutcomeOnceUntilHanded(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	p := paths.Paths{Data: t.TempDir()}
	began := time.Now().Add(-90 * time.Second)
	Note(p, KVM, Missing, began, fs.ErrNotExist)
	Note(p, KVM, Missing, began, fs.ErrNotExist)
	Note(p, KVM, OK, time.Now(), nil)
	Note(p, VMReady, OK, began.Add(30*time.Second), errors.New("ignored: it went well"))

	r := read(p)
	if r.Began != began.Unix() || len(r.Steps) != 3 {
		t.Fatalf("kept %+v", r)
	}
	if s := r.Steps[0]; s.Step != KVM || s.Outcome != Missing || s.Error == "" || s.DurationS < 89 {
		t.Errorf("first step = %+v", s)
	}
	if s := r.Steps[2]; s.Step != VMReady || s.Error != "" || s.DurationS < 59 || s.DurationS > 62 {
		t.Errorf("vm_ready = %+v", s)
	}

	// One the daemon couldn't take is kept for the next init.
	Hand(context.Background(), p, func(context.Context, io.Reader) error { return errors.New("no daemon") })
	if len(read(p).Steps) != 3 {
		t.Fatal("a report the daemon didn't take was forgotten")
	}
	var got api.SetupFunnelReport
	Hand(context.Background(), p, func(_ context.Context, report io.Reader) error {
		return json.NewDecoder(report).Decode(&got)
	})
	if len(got.Steps) != 3 || got.Began != r.Began {
		t.Errorf("handed %+v", got)
	}
	if _, err := os.Stat(file(p)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a report the daemon took is still kept: %v", err)
	}
	Hand(context.Background(), p, func(context.Context, io.Reader) error {
		t.Error("handed a report with nothing kept")
		return nil
	})
}

func TestNoteKeepsNothingWithDoNotTrack(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "1")
	p := paths.Paths{Data: t.TempDir()}
	Note(p, KVM, OK, time.Now(), nil)
	if _, err := os.Stat(file(p)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("DO_NOT_TRACK=1 kept a step: %v", err)
	}
}
