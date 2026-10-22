package gitrepo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// A read that fails says nothing about the repository: a machine out of file
// descriptors (ENFILE, EMFILE) fails every git process and every file open at
// once, and an answer made of those failures — no diff, a clean worktree, no
// branch — reads exactly like a finished agent with nothing to lose. What
// decides anything from git has to tell "git answered no" from "git couldn't
// answer", and this file is where the two are told apart.

// faults is what FailUnder injected: a directory, and the error every git
// command run in it, and every file read under it, fails with.
var faults sync.Map // cleaned dir -> error

// FailUnder makes every git command run in dir or below it, and every file
// this package reads there, fail with err until the returned function is
// called: how a test puts a repository through ENFILE without exhausting the
// machine it runs on. Directories are independent, so parallel tests each
// fail their own.
func FailUnder(dir string, err error) (undo func()) {
	dir = filepath.Clean(dir)
	faults.Store(dir, err)
	return func() { faults.Delete(dir) }
}

// faultAt is the injected error for path, if one of FailUnder's directories
// holds it.
func faultAt(path string) error {
	if path == "" {
		return nil
	}
	path = filepath.Clean(path)
	var out error
	faults.Range(func(k, v any) bool {
		dir := k.(string)
		if path == dir || strings.HasPrefix(path, dir+string(filepath.Separator)) {
			out = v.(error)
			return false
		}
		return true
	})
	return out
}

// readFile is os.ReadFile, failing as FailUnder says.
func readFile(path string) ([]byte, error) {
	if err := faultAt(path); err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.ReadFile(path)
}

// openFile is os.Open, failing as FailUnder says.
func openFile(path string) (*os.File, error) {
	if err := faultAt(path); err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.Open(path)
}

// gitError is a git command that failed, keeping what it printed so a
// refusal can be told from a failure.
type gitError struct {
	args   []string
	err    error
	stderr string
}

func (e *gitError) Error() string {
	return "git " + strings.Join(e.args, " ") + ": " + e.err.Error() + ": " + e.stderr
}

func (e *gitError) Unwrap() error { return e.err }

// NotFound reports whether err is git answering that what it was asked about
// doesn't exist: `rev-parse --verify --quiet` exiting 1 having printed
// nothing. Anything else — git not starting, git dying on a file it couldn't
// open, a timeout — is no answer at all.
func NotFound(err error) bool {
	var g *gitError
	if !errors.As(err, &g) || g.stderr != "" {
		return false
	}
	var exit *exec.ExitError
	return errors.As(g.err, &exit) && exit.ExitCode() == 1
}

// ErrNoRemote is RemoteURL's answer for a checkout without that remote, or a
// directory that isn't a checkout at all.
var ErrNoRemote = errors.New("no such remote")

// RemoteURL is the URL of one of a checkout's remotes. ErrNoRemote is git
// saying there is none; any other error is git failing to say.
func RemoteURL(dir, name string) (string, error) {
	url, err := run(dir, "remote", "get-url", name)
	var g *gitError
	var exit *exec.ExitError
	if errors.As(err, &g) && errors.As(g.err, &exit) &&
		(exit.ExitCode() == 2 || strings.Contains(g.stderr, "not a git repository") || strings.Contains(g.stderr, "cannot change to")) {
		return "", ErrNoRemote
	}
	return url, err
}
