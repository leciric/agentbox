// Package workspace is the seam between the agent manager and the tree an
// agent edits.
//
// On this host a workspace is a linked worktree of the project's repository,
// which is what every AgentBox installation has (Local). On Kubernetes it is a
// clone on the agent pod's volume, whose git commands, files and refs reach the
// master over the agent gateway. That is why every operation takes a context,
// and why the tree's directory is something the caller is told about rather
// than something it may stand on: a pod's volume has no host path.
package workspace

import (
	"context"

	"agentbox/internal/gitrepo"
)

// Workspace is one agent's tree. Every method names the tree the value was
// made for, so the manager never passes a path around.
type Workspace interface {
	// Path is where the tree is on this host, and "" when it is not on this
	// host at all: a pod's volume is not.
	Path() string

	// Prepare makes the tree for an agent in the project's repository: its
	// branch at Base, Tree's files on top when given, and the project's env
	// files copied in. A failure leaves nothing behind, and names the step
	// that failed (StepError).
	Prepare(ctx context.Context, repo gitrepo.Repo, opts PrepareOptions) error

	// Diff shows what the tree changed against BaseRef, commits,
	// uncommitted edits and untracked files alike.
	Diff(ctx context.Context, opts DiffOptions) (string, error)

	// Files lists the tree's tracked and untracked files, at most limit of
	// them, and whether there are more.
	Files(ctx context.Context, limit int) (files []string, truncated bool, err error)

	// ReadFile reads one file of the tree, by its path relative to the
	// tree's root.
	ReadFile(ctx context.Context, name string) ([]byte, error)

	// Checkpoint records the tree's files as a commit on ref, without
	// touching its branch, index or files: the cheap half of a snapshot,
	// taken at the end of a chat turn.
	Checkpoint(ctx context.Context, ref, message string) (commit string, err error)

	// Snapshot records the tree as the workspace half of a machine snapshot:
	// its files as a commit on ref, and what the tree's own disk holds beyond
	// git when its runtime has such a disk. The machine half is the
	// Machine's.
	Snapshot(ctx context.Context, ref, message string) (commit string, err error)

	// Restore puts the tree's branch and files back where commit has them,
	// leaving ignored files alone.
	Restore(ctx context.Context, commit string) error

	// Status reads whether the tree is there, and whether it has changes.
	Status(ctx context.Context) (Status, error)

	// Destroy deletes the tree from repo. It is not an error for the tree to
	// be gone already.
	Destroy(ctx context.Context, repo gitrepo.Repo) error
}

// PrepareOptions is Prepare's.
type PrepareOptions struct {
	// Branch is the branch the agent works on, made from Base.
	Branch string
	// Base is the commit the branch starts at.
	Base string
	// Tree, when set, is a snapshot commit whose files are put on top of
	// Base as uncommitted changes.
	Tree string
	// EnvFrom is the project's checkout, and Env the files to copy from it
	// into the new tree.
	EnvFrom string
	Env     []string
	// Reason is what git records in the worktree's record, like
	// "agentbox: <project>/<agent>".
	Reason string
}

// DiffOptions is Diff's.
type DiffOptions struct {
	// BaseRef is the branch the agent was made from, and BaseCommit the
	// commit it started at, which is what its changes are measured from when
	// BaseRef doesn't resolve.
	BaseRef    string
	BaseCommit string
	// Onto, when given, is the branches the diff is measured from instead:
	// the base branch of the agent's pull request.
	Onto []string
	Stat bool
	// Paths limits the diff to those paths, relative to the tree's root.
	Paths []string
}

// Status is what a workspace says about itself: whether it is there, and
// whether it has changes.
type Status struct {
	// Exists says whether the tree is there at all.
	Exists bool
	// Dirty says whether it has uncommitted changes or untracked files.
	Dirty bool
}

// StepError is what Prepare returns when one of its steps failed, so the
// manager can name that step in the create error as it did when it ran the
// steps itself.
type StepError struct {
	Step string
	Err  error
}

func (e *StepError) Error() string { return e.Step + ": " + e.Err.Error() }

func (e *StepError) Unwrap() error { return e.Err }

var _ Workspace = Local{}
