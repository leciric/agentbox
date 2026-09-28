package chv

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
)

// forward serves a listener on the host by connecting each of its
// connections to a vsock port in the VM: the daemon's socket and the preview
// proxy reach the host this way, and nothing of the VM listens on the host's
// network.
type forward struct {
	name string
	ln   net.Listener
	dial func(context.Context) (net.Conn, error)
	// open says whether to connect a new connection at all: while the VM is
	// paused nothing in it would answer, so a connection is closed at once
	// rather than left hanging until it resumes.
	open func() bool
	logf func(format string, args ...any)

	wg sync.WaitGroup
}

// serve accepts until the listener is closed, then waits for the
// connections it's carrying to end.
func (f *forward) serve(ctx context.Context) {
	defer f.wg.Wait()
	for {
		c, err := f.ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				f.logf("%s: %v", f.name, err)
			}
			return
		}
		if f.open != nil && !f.open() {
			_ = c.Close()
			continue
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			vm, err := f.dial(ctx)
			if err != nil {
				_ = c.Close()
				return
			}
			pipe(c, vm)
		}()
	}
}

type closeWriter interface{ CloseWrite() error }

// pipe copies a to b and b to a until both have ended, passing each side's
// end on to the other as a half-close, so a request can end its half and
// still read the answer.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if cw, ok := dst.(closeWriter); ok && err == nil {
			_ = cw.CloseWrite()
			return
		}
		// An error either way ends both.
		_ = dst.Close()
		_ = src.Close()
	}
	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}
