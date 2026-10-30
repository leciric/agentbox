package workspace

import (
	"context"
	"errors"

	"agentbox/internal/gitrepo"
)

// ErrAbsent is what every Absent method returns: this workspace has no tree on
// this host to read, and nothing here opens a path in an agent's volume, because
// there is no such path on this side.
//
// The workspace that reads a tree inside a pod, over the agent gateway, is Pod
// (pod.go): it is what makes agentbox diff, the app's file list and a checkpoint
// of a pod's tree work.
var ErrAbsent = errors.New("this workspace has no tree on this host, so there is nothing to diff, list or checkpoint here: a tree inside an agent's pod is read over the agent gateway by the pod workspace")

// Absent is the workspace of an agent whose tree is not on this host and cannot
// be read from here. Every method fails, so no host operation runs on a path
// this host doesn't have, and none runs on "" either, which is what Path has to
// report for a tree that isn't here. A cluster agent uses Pod; this is the
// workspace for anything that has no tree at all.
type Absent struct{}

var _ Workspace = Absent{}

// Path is "", because the tree is not on this host at all.
func (Absent) Path() string { return "" }

// Prepare cannot make a pod's tree: its agentd clones it inside the pod, from
// the git profile the gateway hands it (Stage 3.1).
func (Absent) Prepare(context.Context, gitrepo.Repo, PrepareOptions) error { return ErrAbsent }

func (Absent) Diff(context.Context, DiffOptions) (string, error) { return "", ErrAbsent }

func (Absent) Files(context.Context, int) ([]string, bool, error) { return nil, false, ErrAbsent }

func (Absent) ReadFile(context.Context, string) ([]byte, error) { return nil, ErrAbsent }

func (Absent) Checkpoint(context.Context, string, string) (string, error) { return "", ErrAbsent }

func (Absent) Snapshot(context.Context, string, string) (string, error) { return "", ErrAbsent }

func (Absent) Restore(context.Context, string) error { return ErrAbsent }

func (Absent) ResolveCommit(context.Context, string) (string, error) { return "", ErrAbsent }

func (Absent) DeleteRef(context.Context, string) error { return ErrAbsent }

// Status is an error rather than an empty Status: "not here" would read as a
// tree that was removed from this host, and a caller would decide from it.
func (Absent) Status(context.Context) (Status, error) { return Status{}, ErrAbsent }

func (Absent) Destroy(context.Context, gitrepo.Repo) error { return ErrAbsent }
