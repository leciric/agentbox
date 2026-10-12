package image

import (
	"context"
	"errors"
	"os"
	"testing"
)

// SetPartSize changes how big BuildForPublishing's parts are, until the
// returned function is called.
func SetPartSize(n int64) (restore func()) {
	old := partSize
	partSize = n
	return func() { partSize = old }
}

func TestMain(m *testing.M) {
	// Build must not change the firewall of the machine running the tests.
	firewall = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("no firewall in tests")
	}
	os.Exit(m.Run())
}
