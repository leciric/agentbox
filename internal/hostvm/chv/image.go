package chv

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Debian 13's cloud image, the VM's system: genericcloud, the one with
// virtio's drivers and no others, as a dated build so the pin holds
// (cloud.debian.org keeps a year of them) and checked against its SHA512SUMS.
// It's a 3 GiB disk.raw, sparse, in a tarball; cloud-init grows its root
// partition to the disk's size on the first boot. There's one for each
// architecture the VM runs on: amd64 for Cloud Hypervisor and an Intel Mac,
// arm64 for Apple silicon (vz.go).
const debianBuild = "20260914-2601"

var debianImageSHA512 = map[string]string{
	"amd64": "ba03aae045d06ee3ccd8fe6d4fdac58f7fbb25e635927f23055223ad8be75c21850fef84b8146066ec1f24ffab65f4fc86d55344845e72e73d9fd569b83f26a0",
	"arm64": "0c7bc088d7435060d806e84c7cb1604d0880da99b8d8b33dd951cdfbbf919119de766ab2ddddccd5de2fffb2d918a9fa145989ef036c4b513c737c410bb2a130",
}

// debianImage is the tarball for arch, where it's fetched from, and its
// SHA512.
func debianImage(arch string) (file, url, sha512 string, err error) {
	sha512, ok := debianImageSHA512[arch]
	if !ok {
		return "", "", "", fmt.Errorf("AgentBox's VM has no Debian image for %s", arch)
	}
	file = "debian-13-genericcloud-" + arch + "-" + debianBuild + ".tar.xz"
	return file, "https://cloud.debian.org/images/cloud/trixie/" + debianBuild + "/" + file, sha512, nil
}

// MakeDisks makes what the VM boots from, the parts not made yet: its root
// disk from Debian's cloud image, its pool disk for Incus, and the
// cloud-init seed that sets it up on first boot.
func MakeDisks(ctx context.Context, c Config, l Layout, log io.Writer) error {
	if err := os.MkdirAll(l.Dir(), 0o755); err != nil {
		return err
	}
	// Before any disk is in it, so each one is made without copy on write.
	if err := setNoCOW(l.Dir()); err != nil {
		return fmt.Errorf("turning copy on write off for %s: %w", l.Dir(), err)
	}
	if err := makeRootDisk(ctx, l, log); err != nil {
		return err
	}
	if err := makeSparse(l.PoolDisk(), c.Disk); err != nil {
		return fmt.Errorf("making the pool disk: %w", err)
	}
	pub, err := ensureKey(ctx, l)
	if err != nil {
		return err
	}
	// The seed is cheap and the same for the same Config, so it's always
	// written again: a Config that changed makes a new instance-id.
	files, err := seedFiles(c, pub)
	if err != nil {
		return err
	}
	img, err := fatImage("CIDATA", files)
	if err != nil {
		return err
	}
	return writeFileAtomic(l.Seed(), img, 0o644)
}

func makeRootDisk(ctx context.Context, l Layout, log io.Writer) error {
	if _, err := os.Stat(l.RootDisk()); err == nil {
		return nil
	}
	if err := os.MkdirAll(l.Cache(), 0o755); err != nil {
		return err
	}
	image, url, sha512, err := debianImage(runtime.GOARCH)
	if err != nil {
		return err
	}
	tarball := filepath.Join(l.Cache(), image)
	if sum, err := fileSum(tarball, newSHA512()); err != nil || sum != sha512 {
		imageLogf(log, "Fetching Debian 13's cloud image (%s, %s)…\n", debianBuild, runtime.GOARCH)
		start := time.Now()
		if _, err := download(ctx, url, tarball, newSHA512(), sha512, log); err != nil {
			return fmt.Errorf("fetching Debian's cloud image: %w", err)
		}
		imageLogf(log, "Fetched it in %s\n", time.Since(start).Round(time.Second))
		// Earlier builds' tarballs have no more use.
		old, _ := filepath.Glob(filepath.Join(l.Cache(), "debian-13-genericcloud-*.tar.xz"))
		for _, f := range old {
			if f != tarball {
				_ = os.Remove(f)
			}
		}
	}
	imageLogf(log, "Making the VM's system disk…\n")
	start := time.Now()
	part := l.RootDisk() + ".part"
	defer func() { _ = os.Remove(part) }()
	if err := extractDiskRaw(ctx, tarball, part); err != nil {
		return fmt.Errorf("unpacking Debian's cloud image: %w", err)
	}
	if err := os.Truncate(part, RootDisk); err != nil {
		return err
	}
	if err := os.Rename(part, l.RootDisk()); err != nil {
		return err
	}
	imageLogf(log, "Made it in %s\n", time.Since(start).Round(100*time.Millisecond))
	return nil
}

// extractDiskRaw writes the tarball's disk.raw to dst, leaving its zeros as
// holes. xz does the decompressing (Go has none, and xz-utils is on every
// Linux desktop); archive/tar reads a sparse member as its full size, zeros
// and all, which sparseCopy skips over again. A Mac has no xz, but its tar
// (bsdtar) reads the tarball itself.
func extractDiskRaw(ctx context.Context, tarball, dst string) error {
	if runtime.GOOS == "darwin" {
		return extractDiskRawBSD(ctx, "tar", tarball, dst)
	}
	xz := exec.CommandContext(ctx, "xz", "-dc", "-T0", tarball)
	var stderr bytes.Buffer
	xz.Stderr = &stderr
	out, err := xz.StdoutPipe()
	if err != nil {
		return err
	}
	if err := xz.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("xz isn't installed: install xz-utils (or xz), then run this again")
		}
		return err
	}
	copyErr := func() error {
		tr := tar.NewReader(out)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return errors.New("it has no disk.raw")
			}
			if err != nil {
				return err
			}
			if filepath.Base(h.Name) != "disk.raw" || h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeGNUSparse {
				continue
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if err := sparseCopy(f, tr, h.Size); err != nil {
				_ = f.Close()
				return err
			}
			return f.Close()
		}
	}()
	// Stop xz on the way out, having read what it needed.
	_, _ = io.Copy(io.Discard, out)
	waitErr := xz.Wait()
	if copyErr != nil {
		return copyErr
	}
	if waitErr != nil {
		return fmt.Errorf("xz: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// extractDiskRawBSD is extractDiskRaw with bsdtar (tar, on a Mac), which
// decompresses xz itself and writes disk.raw out whole, zeros and all.
func extractDiskRawBSD(ctx context.Context, bsdtar, tarball, dst string) error {
	cmd := exec.CommandContext(ctx, bsdtar, "-xOf", tarball, "disk.raw")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	copyErr := func() error {
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if err := sparseCopy(f, out, -1); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	}()
	_, _ = io.Copy(io.Discard, out)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return fmt.Errorf("%s: %w: %s", bsdtar, waitErr, strings.TrimSpace(stderr.String()))
	}
	if copyErr == nil {
		if fi, err := os.Stat(dst); err == nil && fi.Size() == 0 {
			copyErr = errors.New("it has no disk.raw")
		}
	}
	return copyErr
}

// sparseCopy copies size bytes of r to f, or all of it when size is -1, seeking past every block of zeros
// rather than writing it, so they stay holes.
func sparseCopy(f *os.File, r io.Reader, size int64) error {
	buf := make([]byte, 1<<20)
	var off int64
	for size < 0 || off < size {
		want := int64(len(buf))
		if size >= 0 {
			want = min(want, size-off)
		}
		n, err := io.ReadFull(r, buf[:want])
		if n > 0 {
			// Block by block, in 4 KiB (a page and a filesystem block).
			for b := 0; b < n; b += 4096 {
				e := min(b+4096, n)
				if !allZero(buf[b:e]) {
					if _, err := f.WriteAt(buf[b:e], off+int64(b)); err != nil {
						return err
					}
				}
			}
			off += int64(n)
		}
		if size < 0 && (err == io.EOF || err == io.ErrUnexpectedEOF) {
			size = off
			break
		}
		if err != nil {
			return err
		}
	}
	return f.Truncate(size)
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// makeSparse makes file, size bytes of holes, unless it's there.
func makeSparse(file string, size int64) error {
	if _, err := os.Stat(file); err == nil {
		return nil
	}
	if size <= 0 {
		return fmt.Errorf("a disk of %d bytes", size)
	}
	part := file + ".part"
	defer func() { _ = os.Remove(part) }()
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, file)
}

// ensureKey makes the key the front end logs into the VM with, unless it's
// there, and returns its public half.
func ensureKey(ctx context.Context, l Layout) (string, error) {
	pub, err := os.ReadFile(l.Key() + ".pub")
	if _, kerr := os.Stat(l.Key()); err == nil && kerr == nil {
		return string(pub), nil
	}
	if err := os.MkdirAll(filepath.Dir(l.Key()), 0o700); err != nil {
		return "", err
	}
	tmp := l.Key() + ".new"
	_ = os.Remove(tmp)
	_ = os.Remove(tmp + ".pub")
	out, err := exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "agentbox-vm", "-f", tmp).CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("ssh-keygen isn't installed: install OpenSSH's client (openssh-client or openssh), then run this again")
		}
		return "", fmt.Errorf("ssh-keygen: %w: %s", err, bytes.TrimSpace(out))
	}
	// The public half first: the private one there is what says it's done.
	if err := os.Rename(tmp+".pub", l.Key()+".pub"); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, l.Key()); err != nil {
		return "", err
	}
	pub, err = os.ReadFile(l.Key() + ".pub")
	return string(pub), err
}

// imageLogf says how making the VM is getting on.
func imageLogf(log io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(log, format, a...) }

func writeFileAtomic(file string, data []byte, mode os.FileMode) error {
	tmp := file + ".new"
	defer func() { _ = os.Remove(tmp) }()
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// WaitProvisioned waits until the VM's first boot has finished setting it up
// (cloud-init), and fails with what went wrong if it didn't.
func WaitProvisioned(ctx context.Context, c Config, l Layout, log io.Writer) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	run := func(ctx context.Context, argv ...string) ([]byte, error) {
		args := SSHArgs(c, l, self, "", false, argv)
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return out, err
	}
	return waitCloudInit(ctx, run, log, 2*time.Second)
}

// runInVM runs argv in the VM and returns its standard output: a command that
// exits non-zero returns what it wrote along with the error.
type runInVM func(ctx context.Context, argv ...string) ([]byte, error)

// cloudInitStatus is `cloud-init status --format json`, what of it is used.
type cloudInitStatus struct {
	Status            string              `json:"status"`
	ExtendedStatus    string              `json:"extended_status"`
	Detail            string              `json:"detail"`
	Errors            []string            `json:"errors"`
	RecoverableErrors map[string][]string `json:"recoverable_errors"`
}

// provisionTimeout is the longest the first boot's setup is waited for: it
// installs packages, so it's as slow as the network.
const provisionTimeout = 30 * time.Minute

func waitCloudInit(ctx context.Context, run runInVM, log io.Writer, every time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, provisionTimeout)
	defer cancel()
	start := time.Now()
	var lastErr error
	reached := false
	for {
		attempt, cancelAttempt := context.WithTimeout(ctx, 30*time.Second)
		out, err := run(attempt, "cloud-init", "status", "--format", "json")
		cancelAttempt()
		var st cloudInitStatus
		if jerr := json.Unmarshal(out, &st); jerr == nil && st.Status != "" {
			if !reached {
				reached = true
				imageLogf(log, "Setting the VM up (cloud-init)…\n")
			}
			switch st.Status {
			case "done":
				for level, errs := range st.RecoverableErrors {
					for _, e := range errs {
						imageLogf(log, "cloud-init %s: %s\n", strings.ToLower(level), e)
					}
				}
				imageLogf(log, "The VM is set up (%s)\n", time.Since(start).Round(time.Second))
				return nil
			case "error":
				tail, _ := run(ctx, "sudo", "-n", "tail", "-n", "40", "/var/log/cloud-init-output.log")
				return fmt.Errorf("setting the VM up failed (cloud-init: %s): %s\n%s",
					st.ExtendedStatus, strings.Join(st.Errors, "; "), strings.TrimRight(string(tail), "\n"))
			case "disabled":
				return errors.New("cloud-init is disabled in the VM, so nothing sets it up")
			}
		} else if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil && !reached {
				return fmt.Errorf("the VM didn't answer in time: %w", lastErr)
			}
			return fmt.Errorf("waiting for the VM to be set up: %w", ctx.Err())
		case <-time.After(every):
		}
	}
}
