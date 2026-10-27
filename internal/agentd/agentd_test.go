package agentd_test

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"agentbox/internal/agentd"
	"agentbox/internal/gateway"
	"agentbox/internal/k8s"
)

const testToken = "0123456789abcdef0123456789abcdef"

var testRef = gateway.Ref{Project: "pawly", Agent: "agent-01"}

// startAgentd is a master and a pod that has joined it: a real gateway server,
// a real agentd running against a workspace on a temporary volume, and the
// token in a file the way the pod gets it.
func startAgentd(t *testing.T) (server *gateway.Server, dir string) {
	t.Helper()
	dir, base := testRepo(t)
	return startAgentdAt(t, dir, base), dir
}

// startAgentdAt is startAgentd over the workspace the test prepared: a
// repository for most cases, an empty volume for a pod's first start.
func startAgentdAt(t *testing.T, dir, base string) (server *gateway.Server) {
	t.Helper()
	server = gateway.NewServer(func(ctx context.Context, token string) (gateway.Ref, error) {
		if token != testToken {
			return gateway.Ref{}, errors.New("not a token this daemon issued")
		}
		return testRef, nil
	})
	t.Cleanup(func() { _ = server.Close() })
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- agentd.Run(ctx, agentd.Config{
			Ref:        testRef.String(),
			MasterURL:  httpServer.URL + gateway.Path, // the pod may be given the full path
			Branch:     "agent/vm/test-01",
			BaseCommit: base,
			Workspace:  dir,
			TokenFile:  tokenFile,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("agentd stopped with %v, and only its context ended", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("agentd did not stop when its context ended")
		}
	})
	waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Second)
	defer waitCancel()
	if err := server.WaitFor(waitCtx, testRef, 10*time.Second); err != nil {
		t.Fatalf("the pod never connected: %v", err)
	}
	return server
}

func TestACommandRunsInThePod(t *testing.T) {
	server, _ := startAgentd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpExec, Argv: []string{"/bin/sh", "-c", "echo hi"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	got := readAnswer(t, stream)
	if got.stdout != "hi\n" || got.code != 0 {
		t.Errorf("the command printed %q and exited %d, want \"hi\\n\" and 0 (stderr %q)", got.stdout, got.code, got.stderr)
	}
}

func TestACommandThatFailsSaysSo(t *testing.T) {
	server, _ := startAgentd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpExec, Argv: []string{"/bin/sh", "-c", "echo no >&2; exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	got := readAnswer(t, stream)
	if got.stderr != "no\n" || got.code != 3 {
		t.Errorf("the command said %q on stderr and exited %d, want \"no\\n\" and 3", got.stderr, got.code)
	}
}

// TestAFreshPodServesWithoutARepository is a pod's first start: its volume is
// empty and the clone arrives with the git profile work. agentd must connect
// and serve rather than exit and be restarted forever.
func TestAFreshPodServesWithoutARepository(t *testing.T) {
	workspace := t.TempDir()
	server := startAgentdAt(t, workspace, "0123456789abcdef0123456789abcdef01234567")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpExec, Argv: []string{"/bin/sh", "-c", "pwd"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	got := readAnswer(t, stream)
	want, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got.code != 0 || strings.TrimSpace(got.stdout) != want {
		t.Errorf("a fresh pod's command printed %q and exited %d, want %q and 0 (stderr %q)", got.stdout, got.code, want, got.stderr)
	}
}

// TestACommandThatLeftAChildKeepsItsExitCode is the child a command leaves
// holding its output pipe: the wait gives up on the pipe, which is not the
// command failing.
func TestACommandThatLeftAChildKeepsItsExitCode(t *testing.T) {
	server, _ := startAgentd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpExec, Argv: []string{"/bin/sh", "-c", "sleep 5 & exit 0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	start := time.Now()
	got := readAnswer(t, stream)
	if got.code != 0 {
		t.Errorf("a command that left a child exited %d, and it exited 0 (stderr %q)", got.code, got.stderr)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the command's wait took %v, and the pipe a child holds costs at most the wait delay", took)
	}
}

func TestExecRefusesAUserThePodIsNot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("this test runs as root, which is the pod's own user here")
	}
	server, _ := startAgentd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpExec, Argv: []string{"/bin/true"}, User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	got := readAnswer(t, stream)
	if got.code != 2 {
		t.Errorf("exec as root exited %d, want 2 (stderr %q)", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "runs as") || !strings.Contains(got.stderr, "cannot become root") {
		t.Errorf("exec as root said %q, and it says which user the pod runs as and that it cannot become root", got.stderr)
	}
}

func TestExecRunsAsThePodsUser(t *testing.T) {
	// The master names the pod's user for every command it runs there, which is
	// the user agentd itself already is.
	name := os.Getenv("USER")
	if name == "" {
		if me, err := user.Current(); err == nil {
			name = me.Username
		}
	}
	if name == "" {
		t.Skip("this machine doesn't name the user its uid has")
	}
	server, _ := startAgentd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpExec, Argv: []string{"/bin/sh", "-c", "echo hi"}, User: name})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if got := readAnswer(t, stream); got.code != 0 || got.stdout != "hi\n" {
		t.Errorf("exec as %s printed %q and exited %d, want \"hi\\n\" and 0 (stderr %q)", name, got.stdout, got.code, got.stderr)
	}
}

func TestStdinReachesTheCommand(t *testing.T) {
	server, _ := startAgentd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpExec, Argv: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if err := stream.Write(gateway.FrameStdin, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	// A half-close is the end of the input, not the end of the command: cat
	// finishes on its own and its exit code comes back.
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got := readAnswer(t, stream)
	if got.stdout != "hello" || got.code != 0 {
		t.Errorf("the command read %q and exited %d, want \"hello\" and 0", got.stdout, got.code)
	}
}

func TestFilesGoBothWays(t *testing.T) {
	server, dir := startAgentd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	path := filepath.Join(dir, "notes.txt")
	body := "written by the master"

	write, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpWrite, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := write.Write(gateway.FrameData, []byte(body)); err != nil {
		t.Fatal(err)
	}
	// The master half-closes when it has sent the file: no frame means the
	// file's bytes are over.
	if err := write.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if got := readAnswer(t, write); got.code != 0 {
		t.Fatalf("the write exited %d: %q", got.code, got.stderr)
	}
	_ = write.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the new file is %o, and a new file is 0600", perm)
	}

	read, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpRead, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	if got := readAnswer(t, read); got.stdout != body || got.code != 0 {
		t.Errorf("the read answered %q and exited %d, want %q and 0", got.stdout, got.code, body)
	}

	list, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpList, Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = list.Close() }()
	answer, err := list.ReadResponse()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, entry := range answer.Entries {
		if entry.Name == "notes.txt" {
			found = true
			if entry.Size != int64(len(body)) || entry.Dir {
				t.Errorf("notes.txt is listed as %+v, and it is a file of %d bytes", entry, len(body))
			}
		}
	}
	if !found {
		t.Errorf("the listing is %+v, and notes.txt is in the directory", answer.Entries)
	}
}

func TestAWriteOutsideThePodIsRefused(t *testing.T) {
	server, _ := startAgentd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	write, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpWrite, Path: outside})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = write.Close() }()
	if err := write.Write(gateway.FrameData, []byte("no")); err != nil {
		t.Fatal(err)
	}
	if err := write.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got := readAnswer(t, write)
	if got.code != 1 || !strings.Contains(got.stderr, "outside every directory") {
		t.Errorf("a write to %s answered %d (%q), want exit 1 and the directories in the reason", outside, got.code, got.stderr)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("%s was written, and a write outside the pod's directories is refused", outside)
	}
}

// TestWritesGoToTheDirectoriesTheAgentsFilesAreIn covers what configure does
// when an agent starts: every file it writes is under the home, in directories
// the pod's image doesn't have yet, and the workspace is where the agent's own
// files go. A test running as another user can't create /home/agent, so HOME
// points at a temporary one with the image's shape: nothing in it yet.
func TestWritesGoToTheDirectoriesTheAgentsFilesAreIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	server, workspace := startAgentd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, path := range []string{
		filepath.Join(home, ".gitconfig"),
		filepath.Join(home, ".config/agentbox/git-hooks/commit-msg"),
		filepath.Join(home, ".config/agentbox/env"),
		filepath.Join(home, ".claude/CLAUDE.md"),
		filepath.Join(workspace, "x"),
	} {
		if got := writeTo(t, server, ctx, gateway.Header{Path: path}, "written"); got.code != 0 {
			t.Errorf("a write to %s exited %d: %s", path, got.code, got.stderr)
		}
		if body, err := os.ReadFile(path); err != nil || string(body) != "written" {
			t.Errorf("%s holds %q (%v), and the write is what put it there", path, body, err)
		}
	}

	// agentd's own run directory is a tmpfs owned by root, so the test can only
	// write there on a machine that mounts it as a pod does.
	run := filepath.Join(k8s.RunDir, "token")
	if _, err := os.Stat(k8s.RunDir); err == nil {
		if got := writeTo(t, server, ctx, gateway.Header{Path: run}, "credential"); got.code != 0 {
			t.Errorf("a write to %s exited %d: %s", run, got.code, got.stderr)
		}
	} else {
		t.Logf("not writing %s: not this machine's, it is the pod's run directory", run)
	}

	// And the paths no root holds are refused, however they are reached.
	if err := os.Symlink("/etc", filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(workspace, "link")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		what string
		path string
		want string
	}{
		{"a path no root holds", "/etc/passwd", "outside every directory"},
		{"a relative path that climbs out", "../outside.txt", "outside the workspace"},
		{"a path that climbs out of the home", filepath.Join(home, "..", "..", "etc", "passwd"), ""},
		{"a symlinked parent", filepath.Join(workspace, "escape", "hostname"), "outside every directory"},
		{"a symlinked file", filepath.Join(workspace, "link"), "is a symlink"},
	} {
		got := writeTo(t, server, ctx, gateway.Header{Path: tc.path}, "no")
		if got.code == 0 {
			t.Errorf("%s (%s) was written, and it is not the pod's to write", tc.what, tc.path)
			continue
		}
		if tc.want != "" && !strings.Contains(got.stderr, tc.want) {
			t.Errorf("%s (%s) was refused with %q, and the reason is %q", tc.what, tc.path, got.stderr, tc.want)
		}
	}

	// A refusal names all three directories, the run directory included, so the
	// master can see where a write may go.
	if got := writeTo(t, server, ctx, gateway.Header{Path: "/etc/passwd"}, "no"); !strings.Contains(got.stderr, k8s.RunDir) {
		t.Errorf("a refused write said %q, and it names the run directory %s", got.stderr, k8s.RunDir)
	}
}

// TestWriteRefusesAnOwnerThePodIsNot covers a file the master says belongs to
// someone else: the pod's agent writes it and cannot claim it is another user's.
func TestWriteRefusesAnOwnerThePodIsNot(t *testing.T) {
	server, workspace := startAgentd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// What configure sends for every file it writes: the pod's own ids.
	owned := filepath.Join(workspace, "owned")
	if got := writeTo(t, server, ctx, gateway.Header{Path: owned, UID: os.Getuid(), GID: os.Getgid()}, "mine"); got.code != 0 {
		t.Errorf("a write owned by %d:%d exited %d: %s", os.Getuid(), os.Getgid(), got.code, got.stderr)
	}
	// 0 is unset, because the header leaves it out, so a write that names no
	// owner is the pod's and lands.
	if got := writeTo(t, server, ctx, gateway.Header{Path: filepath.Join(workspace, "unset")}, "mine"); got.code != 0 {
		t.Errorf("a write that names no owner exited %d: %s", got.code, got.stderr)
	}

	other := filepath.Join(workspace, "other")
	got := writeTo(t, server, ctx, gateway.Header{Path: other, UID: os.Getuid() + 1, GID: os.Getgid()}, "no")
	if got.code != 2 {
		t.Errorf("a write owned by another uid exited %d, want 2 (stderr %q)", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "cannot write a file owned by") {
		t.Errorf("a write owned by another uid said %q, and it says it cannot write a file owned by it", got.stderr)
	}
	if _, err := os.Stat(other); err == nil {
		t.Errorf("%s was written for an owner the pod isn't", other)
	}
}

func TestADialedConnectionCarriesBytesBothWays(t *testing.T) {
	server, _ := startAgentd(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.Copy(conn, conn) // what is sent comes back, as VNC or CDP would answer
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := server.Dial(ctx, testRef, gateway.Header{Op: gateway.OpDial, Network: "tcp", Addr: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if err := stream.Write(gateway.FrameData, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	kind, payload, err := stream.Read()
	if err != nil {
		t.Fatal(err)
	}
	if kind != gateway.FrameData || string(payload) != "ping" {
		t.Errorf("the connection answered %q (frame %d, %v), want \"ping\"", payload, kind, err)
	}
}

// writeTo sends body to the header's path over one write stream and answers
// what the pod said.
func writeTo(t *testing.T, server *gateway.Server, ctx context.Context, h gateway.Header, body string) answer {
	t.Helper()
	h.Op = gateway.OpWrite
	stream, err := server.Dial(ctx, testRef, h)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if err := stream.Write(gateway.FrameData, []byte(body)); err != nil {
		t.Fatalf("sending the file's bytes: %v", err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatalf("half-closing the write: %v", err)
	}
	return readAnswer(t, stream)
}

// answer is what a stream said before its exit code.
type answer struct {
	stdout string
	stderr string
	code   int
}

func readAnswer(t *testing.T, s *gateway.Stream) answer {
	t.Helper()
	got := answer{code: -1}
	for {
		kind, payload, err := s.Read()
		if err != nil {
			t.Fatalf("reading the stream: %v", err)
		}
		switch kind {
		case gateway.FrameStdout, gateway.FrameData:
			got.stdout += string(payload)
		case gateway.FrameStderr:
			got.stderr += string(payload)
		case gateway.FrameExit:
			if len(payload) != 1 {
				t.Fatalf("the exit frame is %d bytes, and it is one", len(payload))
			}
			got.code = int(payload[0])
			return got
		}
	}
}

func TestAttachRunsAProgramOnATerminal(t *testing.T) {
	server, _ := startAgentd(t)
	a := attachTo(t, server, gateway.Header{Op: gateway.OpAttach, Argv: []string{"/bin/sh", "-c", "echo hello"}})
	// The terminal turns the newline into a carriage return and one, so the
	// output is compared trimmed.
	if out, code := a.waitForExit(); code != 0 || strings.TrimSpace(out) != "hello" {
		t.Errorf("the terminal printed %q and exited %d, want hello and 0", out, code)
	}
}

func TestAttachSaysHowTheProgramEnded(t *testing.T) {
	server, _ := startAgentd(t)
	a := attachTo(t, server, gateway.Header{Op: gateway.OpAttach, Argv: []string{"/bin/sh", "-c", "exit 3"}})
	if out, code := a.waitForExit(); code != 3 {
		t.Errorf("the terminal exited %d, want 3 (it printed %q)", code, out)
	}
}

func TestAttachReportsASignalAsTheShellWould(t *testing.T) {
	server, _ := startAgentd(t)
	// A program killed by a signal exits with 128 and the signal, the way a
	// shell reports it, so a terminal whose program was killed says 143 for
	// SIGTERM rather than 0.
	a := attachTo(t, server, gateway.Header{Op: gateway.OpAttach, Argv: []string{"/bin/sh", "-c", "kill -TERM $$"}})
	if out, code := a.waitForExit(); code != 128+15 {
		t.Errorf("the terminal exited %d, want %d (it printed %q)", code, 128+15, out)
	}
}

func TestAttachOpensAShellWithoutAnArgv(t *testing.T) {
	// A pod is given no SHELL, so this is also what the passwd entry answers.
	t.Setenv("SHELL", "")
	server, _ := startAgentd(t)
	a := attachTo(t, server, gateway.Header{Op: gateway.OpAttach})
	// A shell that read this is a shell: it is also the only program the test
	// can be sure answers, since no argv was sent.
	if err := a.s.Write(gateway.FrameStdin, []byte("exit 7\n")); err != nil {
		t.Fatalf("typing into the terminal: %v", err)
	}
	if out, code := a.waitForExit(); code != 7 {
		t.Errorf("the terminal exited %d, want 7 (it printed %q)", code, out)
	}
}

func TestAttachResizesTheTerminal(t *testing.T) {
	server, _ := startAgentd(t)
	a := attachTo(t, server, gateway.Header{
		Op:   gateway.OpAttach,
		Argv: []string{"/bin/sh", "-c", "stty size; sleep 1; stty size"},
		Cols: 80,
		Rows: 24,
	})
	// stty size prints the rows first, so the terminal opens at 24 by 80.
	a.waitFor("24 80")
	var size [4]byte
	binary.BigEndian.PutUint16(size[0:2], 40)
	binary.BigEndian.PutUint16(size[2:4], 10)
	if err := a.s.Write(gateway.FrameResize, size[:]); err != nil {
		t.Fatalf("resizing the terminal: %v", err)
	}
	a.waitFor("10 40")
	if out, code := a.waitForExit(); code != 0 {
		t.Errorf("the terminal exited %d, want 0 (it printed %q)", code, out)
	}
}

func TestAttachStdinReachesTheProgram(t *testing.T) {
	server, _ := startAgentd(t)
	a := attachTo(t, server, gateway.Header{
		Op:   gateway.OpAttach,
		Argv: []string{"/bin/sh", "-c", "read x; echo got-$x"},
	})
	// The newline is what ends a line on a terminal, the same as pressing
	// return.
	if err := a.s.Write(gateway.FrameStdin, []byte("hi\n")); err != nil {
		t.Fatalf("typing into the terminal: %v", err)
	}
	a.waitFor("got-hi")
	if out, code := a.waitForExit(); code != 0 {
		t.Errorf("the terminal exited %d, want 0 (it printed %q)", code, out)
	}
}

func TestAttachDiesWithItsStream(t *testing.T) {
	server, _ := startAgentd(t)
	a := attachTo(t, server, gateway.Header{
		Op:   gateway.OpAttach,
		Argv: []string{"/bin/sh", "-c", "echo pid=$$; exec sleep 30"},
	})
	// The line has to be complete before its number means anything: the pty
	// can hand over half a number in one read.
	a.waitFor("pid=")
	a.waitFor("\n")
	_, rest, _ := strings.Cut(a.out.String(), "pid=")
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		t.Fatalf("the terminal printed %q, and no pid in it", a.out.String())
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("the terminal printed %q for its pid: %v", rest, err)
	}
	// Closing the stream is the app closing its terminal pane. The program on
	// the terminal is a client the app can open again, and it must not be left
	// running with nothing to show it.
	if err := a.s.Close(); err != nil {
		t.Fatalf("closing the terminal: %v", err)
	}
	waitForGone(t, pid)
}

// TestAttachEndsWithItsFrames is the frame loop ending, not the context: a
// master that half-closes its side of the terminal is one the program must not
// outlive, and the exit frame still reaches the side that is reading.
func TestAttachEndsWithItsFrames(t *testing.T) {
	server, _ := startAgentd(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	a := attachTo(t, server, gateway.Header{
		Op:   gateway.OpAttach,
		Argv: []string{"/bin/sh", "-c", "echo $$ > '" + pidFile + "'; exec sleep 30"},
	})
	// The shell execs the program, so the pid it wrote is the one to look for.
	pid := waitForPid(t, pidFile)
	if err := a.s.CloseWrite(); err != nil {
		t.Fatalf("half-closing the terminal: %v", err)
	}
	if out, code := a.waitForExit(); code != 128+9 {
		t.Errorf("the terminal exited %d, want %d from the kill (it printed %q)", code, 128+9, out)
	}
	waitForGone(t, pid)
}

// TestAttachEndsItsOutputBeforeTheExitFrame is the ordering: the exit frame is
// the stream's last, so output the terminal still holds when its program is
// killed is read out first rather than written after it.
func TestAttachEndsItsOutputBeforeTheExitFrame(t *testing.T) {
	server, _ := startAgentd(t)
	a := attachTo(t, server, gateway.Header{Op: gateway.OpAttach, Argv: []string{"/bin/sh", "-c", "exec yes hello"}})
	// Read some output first, so the terminal is mid-stream when the frames end.
	for i := 0; i < 3; i++ {
		if !a.next() {
			t.Fatalf("the terminal ended after %d frames, and it prints until it is killed", i)
		}
	}
	// A half-close leaves the pod's side of the stream readable, so what it
	// writes next is what the master sees after the frames have ended.
	if err := a.s.CloseWrite(); err != nil {
		t.Fatalf("half-closing the terminal: %v", err)
	}
	exited := false
	for {
		kind, payload, err := a.s.Read()
		if err != nil {
			break
		}
		switch kind {
		case gateway.FrameExit:
			exited = true
		case gateway.FrameStdout:
			if exited {
				t.Errorf("the terminal wrote %q after its exit frame", payload)
			}
		}
	}
	if !exited {
		t.Error("the terminal's stream ended without an exit frame")
	}
}

// waitForPid reads the pid a terminal's program wrote, once it is there.
func waitForPid(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(file)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never wrote its pid to %s: %v", file, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForGone waits until a pid is no longer there: a terminal's program that
// its stream ended has nothing left to show it.
func waitForGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d is still there two seconds after its terminal ended (%v)", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// attach is the master's side of a test's terminal: its stream, and what has
// been read from it so far.
type attach struct {
	t    *testing.T
	s    *gateway.Stream
	out  strings.Builder
	code int
}

func attachTo(t *testing.T, server *gateway.Server, h gateway.Header) *attach {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	stream, err := server.Dial(ctx, testRef, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	return &attach{t: t, s: stream, code: -1}
}

// next reads one frame into the output. It reports false at the exit frame,
// which is what ends a terminal's stream.
func (a *attach) next() bool {
	a.t.Helper()
	kind, payload, err := a.s.Read()
	if err != nil {
		a.t.Fatalf("reading the terminal: %v", err)
	}
	switch kind {
	case gateway.FrameStdout, gateway.FrameStderr:
		a.out.Write(payload)
		return true
	case gateway.FrameExit:
		if len(payload) != 1 {
			a.t.Fatalf("the exit frame is %d bytes, and it is one", len(payload))
		}
		a.code = int(payload[0])
		return false
	}
	return true
}

// waitFor reads frames until the terminal has printed want.
func (a *attach) waitFor(want string) {
	a.t.Helper()
	for !strings.Contains(a.out.String(), want) {
		if !a.next() {
			a.t.Fatalf("the terminal exited %d before printing %q: it printed %q", a.code, want, a.out.String())
		}
	}
}

// waitForExit reads frames until the exit frame.
func (a *attach) waitForExit() (string, int) {
	a.t.Helper()
	for a.code < 0 {
		if !a.next() {
			break
		}
	}
	if a.code < 0 {
		a.t.Fatalf("the terminal's stream ended without an exit code: it printed %q", a.out.String())
	}
	return a.out.String(), a.code
}
