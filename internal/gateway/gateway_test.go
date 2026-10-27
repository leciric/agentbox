package gateway_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/gateway"
)

const goodToken = "0123456789abcdef0123456789abcdef"

var agentRef = gateway.Ref{Project: "pawly", Agent: "agent-01"}

// testHandler is a pod: it runs commands, keeps files in a map and echoes what
// is written to a dialed connection.
func testHandler(t *testing.T) func(ctx context.Context, h gateway.Header, s *gateway.Stream) error {
	t.Helper()
	var mu sync.Mutex
	files := map[string][]byte{
		"/workspace/README.md": []byte("# pawly\n"),
	}
	return func(ctx context.Context, h gateway.Header, s *gateway.Stream) error {
		switch h.Op {
		case gateway.OpExec:
			body, err := io.ReadAll(gateway.NewFrameReader(s, gateway.FrameStdin))
			if err != nil {
				return err
			}
			if err := s.Write(gateway.FrameStdout, append([]byte(strings.Join(h.Argv, " ")+": "), body...)); err != nil {
				return err
			}
			return s.Exit(0)
		case gateway.OpWrite:
			body, err := io.ReadAll(gateway.NewFrameReader(s, gateway.FrameData))
			if err != nil {
				return err
			}
			mu.Lock()
			files[h.Path] = body
			mu.Unlock()
			return s.Exit(0)
		case gateway.OpRead:
			mu.Lock()
			body, ok := files[h.Path]
			mu.Unlock()
			if !ok {
				_ = s.Write(gateway.FrameStderr, []byte("no such file"))
				return s.Exit(1)
			}
			if err := s.Write(gateway.FrameData, body); err != nil {
				return err
			}
			return s.Exit(0)
		case gateway.OpList:
			mu.Lock()
			entries := make([]gateway.Entry, 0, len(files))
			for name := range files {
				entries = append(entries, gateway.Entry{Name: name, Size: int64(len(files[name]))})
			}
			mu.Unlock()
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
			return s.WriteResponse(gateway.Response{Entries: entries})
		case gateway.OpDial:
			// The far end says hello, then repeats what it is sent.
			if err := s.Write(gateway.FrameData, []byte("ready\n")); err != nil {
				return err
			}
			for {
				kind, payload, err := s.Read()
				if err != nil {
					return nil
				}
				if kind != gateway.FrameData {
					continue
				}
				if err := s.Write(gateway.FrameData, payload); err != nil {
					_ = err
					return nil
				}
			}
		}
		return nil
	}
}

// startPair is a master and a connected pod.
func startPair(t *testing.T) (*gateway.Server, context.CancelFunc) {
	t.Helper()
	server := gateway.NewServer(func(ctx context.Context, token string) (gateway.Ref, error) {
		if token != goodToken {
			return gateway.Ref{}, errors.New("not a token this daemon issued")
		}
		return agentRef, nil
	})
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client := &gateway.Client{
		URL:    httpServer.URL,
		Token:  goodToken,
		Handle: testHandler(t),
		Log:    func(format string, args ...any) { t.Logf(format, args...) },
	}
	go func() { _ = client.Run(ctx) }()
	if err := server.WaitFor(ctx, agentRef, 10*time.Second); err != nil {
		cancel()
		t.Fatalf("the pod never connected: %v", err)
	}
	return server, cancel
}

func TestACommandRunsInThePod(t *testing.T) {
	server, cancel := startPair(t)
	defer cancel()
	ctx := context.Background()
	stream, err := server.Dial(ctx, agentRef, gateway.Header{Op: gateway.OpExec, Argv: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if err := stream.Write(gateway.FrameStdin, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	var code = -1
	for {
		kind, payload, err := stream.Read()
		if err != nil {
			t.Fatalf("reading the stream: %v", err)
		}
		switch kind {
		case gateway.FrameStdout:
			out.Write(payload)
		case gateway.FrameExit:
			code = int(payload[0])
		}
		if code >= 0 {
			break
		}
	}
	if out.String() != "cat: hello" || code != 0 {
		t.Errorf("output %q, exit %d, want \"cat: hello\" and 0", out.String(), code)
	}
}

func TestFilesGoBothWays(t *testing.T) {
	server, cancel := startPair(t)
	defer cancel()
	ctx := context.Background()

	write, err := server.Dial(ctx, agentRef, gateway.Header{Op: gateway.OpWrite, Path: "/workspace/new.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := write.Write(gateway.FrameData, []byte("written by the master")); err != nil {
		t.Fatal(err)
	}
	if err := write.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := readExit(write); err != nil {
		t.Fatalf("the write: %v", err)
	}

	read, err := server.Dial(ctx, agentRef, gateway.Header{Op: gateway.OpRead, Path: "/workspace/new.txt"})
	if err != nil {
		t.Fatal(err)
	}
	body, exited, err := readAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if body != "written by the master" || exited != 0 {
		t.Errorf("read %q, exit %d, want what was written", body, exited)
	}
	missing, err := server.Dial(ctx, agentRef, gateway.Header{Op: gateway.OpRead, Path: "/workspace/nope"})
	if err != nil {
		t.Fatal(err)
	}
	if _, code, err := readAll(missing); err != nil || code != 1 {
		t.Errorf("a missing file read = %v, exit %d, want exit 1", err, code)
	}

	list, err := server.Dial(ctx, agentRef, gateway.Header{Op: gateway.OpList})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := list.ReadResponse()
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.Entries) != 2 || answer.Entries[0].Name != "/workspace/README.md" {
		t.Errorf("list = %+v, want the two files by name", answer.Entries)
	}
}

func TestADialedConnectionCarriesBytesBothWays(t *testing.T) {
	server, cancel := startPair(t)
	defer cancel()
	stream, err := server.Dial(context.Background(), agentRef, gateway.Header{Op: gateway.OpDial, Network: "tcp", Addr: "127.0.0.1:5900"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	kind, payload, err := stream.Read()
	if err != nil || kind != gateway.FrameData || string(payload) != "ready\n" {
		t.Fatalf("the far end said %q (kind %d, %v), want \"ready\\n\"", payload, kind, err)
	}
	if err := stream.Write(gateway.FrameData, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	kind, payload, err = stream.Read()
	if err != nil || kind != gateway.FrameData || string(payload) != "ping" {
		t.Errorf("echo = %q (kind %d, %v)", payload, kind, err)
	}
}

func TestATokenTheDaemonDidNotIssueIsRefused(t *testing.T) {
	server := gateway.NewServer(func(ctx context.Context, token string) (gateway.Ref, error) {
		return gateway.Ref{}, errors.New("no")
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client := &gateway.Client{URL: httpServer.URL, Token: "wrong", Handle: testHandler(t), Log: t.Logf}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = client.Run(ctx) }()
	if err := server.WaitFor(ctx, agentRef, 700*time.Millisecond); !errors.Is(err, gateway.ErrNotConnected) {
		t.Errorf("WaitFor() with a refused token = %v, want ErrNotConnected", err)
	}
	if server.Connected(agentRef) {
		t.Error("a pod with a token the daemon never issued has a session")
	}
}

func TestANewerSessionReplacesTheOlder(t *testing.T) {
	// Two pods with the same token: the one that connected last is the one
	// the master talks to, and the earlier connection is closed.
	server := gateway.NewServer(func(ctx context.Context, token string) (gateway.Ref, error) {
		return agentRef, nil
	})
	first := httptest.NewServer(server)
	defer first.Close()
	second := httptest.NewServer(server)
	defer second.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	says := func(answer string) func(ctx context.Context, h gateway.Header, s *gateway.Stream) error {
		return func(ctx context.Context, h gateway.Header, s *gateway.Stream) error {
			if err := s.Write(gateway.FrameStdout, []byte(answer)); err != nil {
				return err
			}
			return s.Exit(0)
		}
	}
	go func() { _ = (&gateway.Client{URL: first.URL, Token: goodToken, Handle: says("old")}).Run(ctx) }()
	if err := server.WaitFor(ctx, agentRef, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	go func() { _ = (&gateway.Client{URL: second.URL, Token: goodToken, Handle: says("new")}).Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stream, err := server.Dial(ctx, agentRef, gateway.Header{Op: gateway.OpExec})
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		body, code, err := readAll(stream)
		_ = stream.Close()
		if err == nil && code == 0 && body == "new" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the newer session never became the agent's")
}

// readAll reads a stream to its exit frame.
func readAll(s *gateway.Stream) (string, int, error) {
	var out strings.Builder
	for {
		kind, payload, err := s.Read()
		if err != nil {
			return out.String(), -1, err
		}
		switch kind {
		case gateway.FrameStdout, gateway.FrameData:
			out.Write(payload)
		case gateway.FrameStderr:
			out.Write(payload)
		case gateway.FrameExit:
			return out.String(), int(payload[0]), nil
		}
	}
}

// readExit reads a stream that carries no answer, only its exit code.
func readExit(s *gateway.Stream) error {
	for {
		kind, payload, err := s.Read()
		if err != nil {
			return err
		}
		if kind == gateway.FrameExit && len(payload) == 1 && payload[0] == 0 {
			return nil
		}
	}
}
