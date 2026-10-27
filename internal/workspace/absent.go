package workspace

import (
	"context"
	"errors"

	"agentbox/internal/gitrepo"
)

// ErrAbsent is what every Absent method returns: the agent's tree is inside its
// pod, and this daemon cannot read it from the host it runs on. Nothing here
// opens a path in the pod's volume, because there is no such path on this side.
//
// The workspace that can read it, over the agent gateway, is Stage 3.4 of the
// Kubernetes plan (internal/workspace/pod.go): it is what makes agentbox diff,
// the app's file list and a checkpoint of a pod's tree work.
var ErrAbsent = errors.New("the agent's tree is inside its pod, and reading it from this host over the agent gateway is not built yet: diffs, file lists and checkpoints of a pod's tree come with the pod workspace (Stage 3.4)")

// Absent is the workspace of an agent whose tree is not on this host: a
// Kubernetes pod's volume. Every method fails, so no host operation runs on a
// path this host doesn't have, and none runs on "" either, which is what
// Path has to report for a tree that isn't here.
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

// Status is an error rather than an empty Status: "not here" would read as a
// tree that was removed from this host, and a caller would decide from it.
func (Absent) Status(context.Context) (Status, error) { return Status{}, ErrAbsent }

func (Absent) Destroy(context.Context, gitrepo.Repo) error { return ErrAbsent }
