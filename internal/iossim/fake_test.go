package iossim

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testUDID = "0A1B2C3D-0000-4000-8000-00000000000A"

// fakeXcrun is xcrun and xcode-select on a Mac with two simulators: it
// records every command and answers the ones the helper runs.
type fakeXcrun struct {
	mu       sync.Mutex
	calls    []string
	noXcode  bool
	pasted   string
	failBoot error
}

const listJSON = `{"devices":{
 "com.apple.CoreSimulator.SimRuntime.iOS-18-4":[{"udid":"0A1B2C3D-0000-4000-8000-00000000000B","name":"iPhone 16","state":"Shutdown","isAvailable":true}],
 "com.apple.CoreSimulator.SimRuntime.iOS-26-0":[
  {"udid":"0A1B2C3D-0000-4000-8000-00000000000A","name":"iPhone 17 Pro","state":"Booted","isAvailable":true},
  {"udid":"0A1B2C3D-0000-4000-8000-00000000000C","name":"iPad Air","state":"Shutdown","isAvailable":true},
  {"udid":"0A1B2C3D-0000-4000-8000-00000000000D","name":"Broken","state":"Shutdown","isAvailable":false}],
 "com.apple.CoreSimulator.SimRuntime.watchOS-11-0":[{"udid":"0A1B2C3D-0000-4000-8000-00000000000E","name":"Apple Watch","state":"Shutdown","isAvailable":true}]
}}`

func (f *fakeXcrun) run(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)
	switch {
	case name == "xcode-select":
		return []byte("/Applications/Xcode.app/Contents/Developer\n"), nil
	case line == "xcrun --find simctl":
		if f.noXcode {
			return nil, fmt.Errorf(`xcrun --find: unable to find utility "simctl"`)
		}
		return []byte("/Applications/Xcode.app/.../simctl\n"), nil
	case line == "xcrun xcodebuild -version":
		return []byte("Xcode 26.1\nBuild version 17B55\n"), nil
	case strings.HasPrefix(line, "xcrun simctl list"):
		return []byte(listJSON), nil
	case strings.HasPrefix(line, "xcrun simctl boot "):
		return nil, f.failBoot
	case strings.HasPrefix(line, "xcrun simctl launch"):
		return []byte("com.example.app: 4242\n"), nil
	case strings.HasPrefix(line, "xcrun simctl pbcopy"):
		b, _ := io.ReadAll(stdin)
		f.pasted = string(b)
	case strings.HasPrefix(line, "xcrun simctl io") && strings.Contains(line, "screenshot"):
		return nil, os.WriteFile(args[len(args)-1], []byte("\x89PNG fake"), 0o600)
	}
	return nil, nil
}

func (f *fakeXcrun) ran() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeXcrun) ranLike(prefix string) bool {
	for _, c := range f.ran() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// fakeSimhost is simhost behind pipes: the ops the helper sends it, and
// frames the test writes as simhost would.
type fakeSimhost struct {
	mu       sync.Mutex
	ops      []op
	launched int
	out      *io.PipeWriter
	opSeen   chan op
	stopped  chan struct{}
}

func newFakeSimhost() *fakeSimhost {
	return &fakeSimhost{opSeen: make(chan op, 256)}
}

func (f *fakeSimhost) launch(ctx context.Context, udid string) (*Proc, error) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	f.mu.Lock()
	f.launched++
	f.out = outW
	f.stopped = done
	f.mu.Unlock()
	go func() {
		sc := bufio.NewScanner(inR)
		for sc.Scan() {
			var o op
			if json.Unmarshal(sc.Bytes(), &o) == nil {
				f.mu.Lock()
				f.ops = append(f.ops, o)
				f.mu.Unlock()
				f.opSeen <- o
			}
		}
	}()
	var once sync.Once
	stop := func() error {
		once.Do(func() {
			_ = inR.Close()
			_ = outW.Close()
			close(done)
		})
		return nil
	}
	return &Proc{Stdin: inW, Stdout: outR, Done: done, Stop: stop}, nil
}

func (f *fakeSimhost) write(t *testing.T, kind byte, payload []byte) {
	t.Helper()
	f.mu.Lock()
	w := f.out
	f.mu.Unlock()
	head := make([]byte, 5)
	binary.BigEndian.PutUint32(head, uint32(len(payload)+1))
	head[4] = kind
	if _, err := w.Write(append(head, payload...)); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeSimhost) screen(t *testing.T, w, h int) {
	f.write(t, kindEvent, fmt.Appendf(nil, `{"screen":{"width":%d,"height":%d}}`, w, h))
}

// frame is a kindFrame payload: flags, a timestamp, and some bytes.
func frame(key bool, pts uint64, data string) []byte {
	b := make([]byte, 9, 9+len(data))
	if key {
		b[0] = 1
	}
	binary.BigEndian.PutUint64(b[1:], pts)
	return append(b, data...)
}

// waitLaunched waits for the helper to start simhost.
func (f *fakeSimhost) waitLaunched(t *testing.T) {
	t.Helper()
	ctx := testContext(t)
	for {
		f.mu.Lock()
		n := f.launched
		f.mu.Unlock()
		if n > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("simhost wasn't started")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// next waits for the next op simhost gets.
func (f *fakeSimhost) next(t *testing.T) op {
	t.Helper()
	select {
	case o := <-f.opSeen:
		return o
	case <-testContext(t).Done():
		t.Fatal("simhost got no op")
		return op{}
	}
}

func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// appDir makes a .app directory under root.
func appDir(t *testing.T, root, rel string) string {
	t.Helper()
	dir := filepath.Join(root, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
