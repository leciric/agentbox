package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentbox/internal/state"
)

// CopyHostForLead copies a file or directory from one of the user's projects,
// on the host, into an agent's machine, for the chat that was pointed at
// another project by an @mention. roots are the projects' repositories: a
// path outside every one of them is refused, symlinks resolved first, and
// what .gitignore excludes stays behind (tracked files and untracked ones that
// aren't ignored are copied, as they are on disk). A directory lands as into/<its name>.
func (m *Manager) CopyHostForLead(ctx context.Context, roots []string, from string, dst state.Agent, into string, timeout time.Duration) (int64, error) {
	switch {
	case timeout <= 0:
		timeout = DefaultLeadRunTimeout
	case timeout > MaxLeadRunTimeout:
		return 0, fmt.Errorf("a timeout of %s is longer than the %s a copy may take", timeout, MaxLeadRunTimeout)
	}
	if !filepath.IsAbs(from) {
		return 0, fmt.Errorf("%q is not an absolute path: give the path under the project's repository", from)
	}
	dir, names, err := hostCopyFiles(ctx, roots, from)
	if err != nil {
		return 0, err
	}
	if err := m.ReachableForLead(ctx, dst); err != nil {
		return 0, err
	}
	if into = strings.TrimSpace(into); into == "" {
		into = "."
	}
	copyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pr, pw := io.Pipe()
	sent := &limitWriter{w: pw, max: MaxLeadCopy}
	srcErr := &tailBuffer{max: 1 << 10}
	dstErr := &tailBuffer{max: 1 << 10}
	pack := exec.CommandContext(copyCtx, "tar", "-C", dir, "--null", "-T", "-", "-cf", "-")
	pack.Stdin = bytes.NewReader(names)
	pack.Stdout = sent
	pack.Stderr = srcErr
	packed := make(chan error, 1)
	go func() {
		err := pack.Run()
		pw.CloseWithError(err)
		packed <- err
	}()
	unpack := ExecCommand(dst.Worktree, "mkdir -p -- "+shellQuote(into)+" && tar -C "+shellQuote(into)+" -xf -")
	unpackErr := m.Incus.UserExec(copyCtx, dst.Instance, m.User.Name, unpack, pr, io.Discard, dstErr)
	pr.CloseWithError(errors.New("the destination stopped reading"))
	packErr := <-packed
	switch {
	case sent.over:
		return sent.n, fmt.Errorf("%s is more than %d MiB: copy part of it instead", from, MaxLeadCopy>>20)
	case copyCtx.Err() != nil && ctx.Err() == nil:
		return sent.n, fmt.Errorf("the copy took longer than %s and was stopped", timeout)
	case packErr != nil:
		return sent.n, fmt.Errorf("reading %s: %s", from, firstLine(srcErr.String(), packErr))
	case unpackErr != nil:
		return sent.n, fmt.Errorf("writing into %s in %s: %s", into, dst.Name, firstLine(dstErr.String(), unpackErr))
	}
	return sent.n, nil
}

// hostCopyFiles checks that from is inside one of roots and lists what of it
// may be copied: the directory to pack from, and the NUL-separated names,
// relative to it, of the files git doesn't ignore.
func hostCopyFiles(ctx context.Context, roots []string, from string) (string, []byte, error) {
	real, err := filepath.EvalSymlinks(from)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %v", from, err)
	}
	inside := false
	for _, root := range roots {
		root, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(root, real); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			inside = true
			break
		}
	}
	if !inside {
		return "", nil, fmt.Errorf("%s is outside the user's projects: only a project's repository may be copied from", from)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", nil, err
	}
	// Names are listed from the item itself, and packed under its own name.
	list, base, parent := real, "", filepath.Dir(real)
	if !info.IsDir() {
		list, base = parent, filepath.Base(real)
	}
	args := []string{"-C", list, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}
	if base != "" {
		args = append(args, base)
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", nil, fmt.Errorf("listing %s with git: %s", from, firstLine(stderr.String(), err))
	}
	prefix := ""
	if info.IsDir() {
		prefix = filepath.Base(real) + "/"
	}
	var names []byte
	count := 0
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		// Deleted but still tracked, or a submodule: nothing to pack.
		if fi, err := os.Lstat(filepath.Join(list, name)); err != nil || fi.IsDir() {
			continue
		}
		names = append(append(names, prefix+name...), 0)
		count++
	}
	if count == 0 {
		return "", nil, fmt.Errorf("%s has nothing to copy: it is empty, or everything in it is ignored by git", from)
	}
	return parent, names, nil
}
