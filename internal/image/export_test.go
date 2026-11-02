package image

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"agentbox/internal/bridgecheck"
	"agentbox/internal/incus"
)

// SetPartSize changes how big BuildForPublishing's parts are, until the
// returned function is called.
func SetPartSize(n int64) (restore func()) {
	old := partSize
	partSize = n
	return func() { partSize = old }
}

// SetStallTimeout changes how long a part may get nothing, until the
// returned function is called.
func SetStallTimeout(d time.Duration) (restore func()) {
	old := stallTimeout
	stallTimeout = d
	return func() { stallTimeout = old }
}

// SetHaveZstd says whether this machine has the zstd command, until the
// returned function is called.
func SetHaveZstd(have bool) (restore func()) {
	old := haveZstd
	haveZstd = func() bool { return have }
	return func() { haveZstd = old }
}

// RecompressWithGo is the zstd command's work done with the Go encoder, for
// machines without it.
func RecompressWithGo(_ context.Context, gz, zst string) error {
	in, err := os.Open(gz)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	r, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	out, err := os.Create(zst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	w, err := zstd.NewWriter(out)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return out.Close()
}

// Recompress is BuildForPublishing's zstd step.
var Recompress = recompress

func TestMain(m *testing.M) {
	// A part tried again is tried at once.
	retryDelay = 0
	// The zstd command may not be on the machine running the tests
	// (TestRecompress runs it when it is).
	recompress = RecompressWithGo
	// Build must not change the firewall of the machine running the tests.
	firewall = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("no firewall in tests")
	}
	// Nor its bridge.
	checkBridge = func(context.Context, incus.Client) (bridgecheck.Result, error) {
		return bridgecheck.Result{}, nil
	}
	os.Exit(m.Run())
}
