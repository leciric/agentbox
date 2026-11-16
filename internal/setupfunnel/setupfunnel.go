// Package setupfunnel keeps the steps of Setup a front end takes on the
// user's machine before AgentBox's daemon is up, the KVM check and the VM's
// making, until the daemon is up to take them (POST /v1/usage-events/setup).
// Nothing here is sent anywhere: whether the daemon keeps them as usage
// events is its usage-stats setting's to say, which can't be read before
// there is a daemon. They are kept in a file of their own, beside the VM's,
// at most one of each step and outcome.
package setupfunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/paths"
)

// The steps a front end takes.
const (
	KVM     = "kvm_check"
	VMReady = "vm_ready"
)

// A step's outcomes. Missing and NoPermission are the KVM check's.
const (
	OK           = "ok"
	Failed       = "failed"
	Missing      = "missing"
	NoPermission = "no_permission"
)

// maxError is the most of a failed step's error kept, for the daemon to
// classify.
const maxError = 500

func file(p paths.Paths) string { return filepath.Join(p.Data, "setup-funnel.json") }

// KVMOutcome is the KVM check's outcome for err, opening /dev/kvm's.
func KVMOutcome(err error) string {
	switch {
	case err == nil:
		return OK
	case errors.Is(err, fs.ErrNotExist):
		return Missing
	case errors.Is(err, fs.ErrPermission):
		return NoPermission
	}
	return Failed
}

// Note keeps step, begun at began, ended now with outcome. A failure's err
// is kept for the daemon to classify. Like the daemon's usage events, one
// that can't be kept is dropped, and DO_NOT_TRACK=1 keeps none.
func Note(p paths.Paths, step, outcome string, began time.Time, err error) {
	if os.Getenv("DO_NOT_TRACK") == "1" || p.Data == "" {
		return
	}
	r := read(p)
	if slices.ContainsFunc(r.Steps, func(s api.SetupFunnelStep) bool { return s.Step == step && s.Outcome == outcome }) {
		return
	}
	now := time.Now()
	if r.Began == 0 || began.Unix() < r.Began {
		r.Began = began.Unix()
	}
	st := api.SetupFunnelStep{Step: step, Outcome: outcome, At: now.Unix(), DurationS: int64(max(now.Sub(began), 0) / time.Second)}
	if outcome != OK && err != nil {
		st.Error = err.Error()[:min(len(err.Error()), maxError)]
	}
	r.Steps = append(r.Steps, st)
	raw, jerr := json.Marshal(r)
	if jerr != nil || os.MkdirAll(p.Data, 0o700) != nil {
		return
	}
	_ = os.WriteFile(file(p), raw, 0o600)
}

func read(p paths.Paths) api.SetupFunnelReport {
	var r api.SetupFunnelReport
	if raw, err := os.ReadFile(file(p)); err == nil {
		_ = json.Unmarshal(raw, &r)
	}
	return r
}

// Hand gives the daemon what was kept, through send, which runs
// `agentbox setup-funnel` where the daemon is with the report on its
// standard input, and forgets it once the daemon has it. One it couldn't
// take is kept for the next `vm init`.
func Hand(ctx context.Context, p paths.Paths, send func(ctx context.Context, report io.Reader) error) {
	raw, err := os.ReadFile(file(p))
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if send(ctx, bytes.NewReader(raw)) == nil {
		_ = os.Remove(file(p))
	}
}
