package incus

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// waitTimesOut is an operation whose /wait Incus gives up on as ctx's
// deadline comes: the server's own wait times out a moment before ctx does,
// and says so as an error with status 200.
type waitTimesOut struct {
	incusclient.Operation
}

func (waitTimesOut) WaitContext(ctx context.Context) error {
	<-ctx.Done()
	return api.StatusErrorf(http.StatusOK, "%s", context.DeadlineExceeded)
}

func (waitTimesOut) Get() api.Operation { return api.Operation{StatusCode: api.Running} }

// TestWaitPastItsDeadlineIsTheDeadline: an operation waited on past ctx's
// deadline fails with ctx's own error, which callers tell apart from Incus
// failing (errors.Is), not with the text of Incus's wait timing out.
func TestWaitPastItsDeadlineIsTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := wait(ctx, waitTimesOut{}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait = %v, want the deadline", err)
	}
}

func TestServerWaitTimedOut(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{api.StatusErrorf(http.StatusOK, "context deadline exceeded"), true},
		{api.StatusErrorf(http.StatusInternalServerError, "context deadline exceeded"), false},
		{api.StatusErrorf(http.StatusOK, "Instance is not running"), false},
		{context.DeadlineExceeded, false},
	} {
		if got := serverWaitTimedOut(c.err); got != c.want {
			t.Errorf("serverWaitTimedOut(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
