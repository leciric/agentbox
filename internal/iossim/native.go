package iossim

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
)

//go:embed native/simhost.m
var simhostSource []byte

// simhostFrameworks are what simhost links: the private ones (CoreSimulator,
// SimulatorKit) it opens itself, from wherever this Mac's Xcode has them.
var simhostFrameworks = []string{"Foundation", "CoreGraphics", "CoreMedia", "CoreVideo", "IOSurface", "VideoToolbox"}

// SimhostArgs is how xcrun clang compiles simhost.m into out. Warnings don't
// stop it on a user's Mac, whose Xcode may be newer than any it was tried
// with; the macOS CI job compiles it with -Werror (TestSimhostCompiles).
func SimhostArgs(src, out string) []string {
	args := []string{"clang", "-fobjc-arc", "-O2", "-Wall", "-o", out, src}
	for _, f := range simhostFrameworks {
		args = append(args, "-framework", f)
	}
	return args
}

var buildMu sync.Mutex

// Build compiles simhost into dir with the Mac's Xcode, once per version of
// its source, and says where it is. Compiling it here, rather than shipping a
// binary, keeps it out of agentbox's signed build and builds it against the
// Xcode it will drive.
func Build(ctx context.Context, dir string, run Runner) (string, error) {
	buildMu.Lock()
	defer buildMu.Unlock()
	sum := sha256.Sum256(simhostSource)
	bin := filepath.Join(dir, "simhost-"+hex.EncodeToString(sum[:6]))
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	src := filepath.Join(dir, "simhost.m")
	if err := os.WriteFile(src, simhostSource, 0o600); err != nil {
		return "", err
	}
	tmp := bin + ".tmp"
	if _, err := run(ctx, nil, "xcrun", SimhostArgs(src, tmp)...); err != nil {
		return "", fmt.Errorf("compiling the simulator's helper with Xcode: %w", err)
	}
	if err := os.Rename(tmp, bin); err != nil {
		return "", err
	}
	// Older builds are no use once the source moved on.
	old, _ := filepath.Glob(filepath.Join(dir, "simhost-*"))
	for _, f := range old {
		if f != bin {
			_ = os.Remove(f)
		}
	}
	return bin, nil
}

// SimhostLauncher starts simhost, built into dir, for a simulator, with the
// selected Xcode's Developer directory, where it finds SimulatorKit. Its
// standard error goes to log.
func SimhostLauncher(dir string, simctl Simctl, log io.Writer) Launcher {
	return func(ctx context.Context, udid string) (*Proc, error) {
		bin, err := Build(ctx, dir, simctl.Run)
		if err != nil {
			return nil, err
		}
		developer, err := simctl.DeveloperDir(ctx)
		if err != nil {
			return nil, fmt.Errorf("finding Xcode: %w", err)
		}
		// Not ctx: it outlives the request that started it.
		cmd := exec.Command(bin, udid)
		cmd.Env = append(os.Environ(), "DEVELOPER_DIR="+developer)
		cmd.Stderr = &prefixWriter{w: log, prefix: "simhost " + udid[:8] + ": "}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		done := make(chan struct{})
		var waitErr error
		go func() {
			waitErr = cmd.Wait()
			close(done)
		}()
		return &Proc{
			Stdin:  stdin,
			Stdout: stdout,
			Done:   done,
			Stop: func() error {
				_ = stdin.Close()
				_ = cmd.Process.Signal(syscall.SIGTERM)
				<-done
				return waitErr
			},
		}, nil
	}
}

// prefixWriter writes each line with a prefix.
type prefixWriter struct {
	mu     sync.Mutex
	w      io.Writer
	prefix string
	buf    []byte
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		_, _ = fmt.Fprintf(p.w, "%s%s\n", p.prefix, p.buf[:i])
		p.buf = p.buf[i+1:]
	}
	return len(b), nil
}
