package gitrepo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotEmpty is Create's answer for a folder that already has files in it
// and no commits: it commits them only when asked to, since what's in a folder
// isn't always meant for a repository (a .env, a build output).
var ErrNotEmpty = errors.New("folder isn't empty")

// InitialBranch is the branch Create starts a new repository on.
const InitialBranch = "main"

// Create makes path a repository with at least one commit, so agents have
// something to branch from, and opens it:
//
//   - a folder that doesn't exist yet, or is empty, becomes a repository on
//     main with an empty initial commit (created says the folder was made
//     here, so a caller that fails later can take it away again);
//   - the top of a repository that has commits is opened as it is;
//   - a folder with files in it, a repository or not, is refused with
//     ErrNotEmpty unless commitFiles, which commits them all as the initial
//     commit (.gitignore still applies, and what Heavy finds is left out
//     too, in .git/info/exclude);
//   - a folder inside another repository is refused: it would be a repository
//     nested in that one, and adding that one is more likely what was meant.
func Create(path string, commitFiles bool) (repo Repo, created bool, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Repo{}, false, err
	}
	info, statErr := os.Stat(abs)
	switch {
	case statErr == nil && !info.IsDir():
		return Repo{}, false, fmt.Errorf("%s is a file, not a folder", abs)
	case statErr != nil && !errors.Is(statErr, os.ErrNotExist):
		return Repo{}, false, statErr
	}
	exists := statErr == nil

	// The nearest folder that exists says whether this is inside a repository.
	probe := abs
	for !exists {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		if parent := filepath.Dir(probe); parent != probe {
			probe = parent
		} else {
			break
		}
	}
	existing, openErr := Open(probe)
	if openErr != nil && !errors.Is(openErr, ErrNotARepo) {
		// A repository git can't read, or git failing: never init over it.
		return Repo{}, false, openErr
	}
	if openErr == nil {
		if existing.Root != abs {
			return Repo{}, false, fmt.Errorf("%s is inside the repository %s: add that one, or choose a folder outside it", abs, existing.Root)
		}
		if existing.HasCommits() {
			return existing, false, nil
		}
		if dirty, err := run(abs, "status", "--porcelain", "--untracked-files=normal"); err != nil {
			return Repo{}, false, err
		} else if dirty != "" && !commitFiles {
			return Repo{}, false, fmt.Errorf("%s is a repository with files in it and no commits yet: %w", abs, ErrNotEmpty)
		}
		return existing, false, initialCommit(existing.Root)
	}

	if exists {
		if dotGit := DotGitAbove(abs); dotGit != "" {
			return Repo{}, false, fmt.Errorf("%s already has %s: AgentBox won't make a repository over it", abs, dotGit)
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return Repo{}, false, err
		}
		if len(entries) > 0 && !commitFiles {
			return Repo{}, false, fmt.Errorf("%s already has files in it: %w", abs, ErrNotEmpty)
		}
	} else {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return Repo{}, false, err
		}
		created = true
	}
	madeGit := false
	repo, err = func() (Repo, error) {
		if _, err := os.Lstat(filepath.Join(abs, ".git")); err == nil {
			return Repo{}, fmt.Errorf("%s already has a .git: AgentBox won't make a repository over it", abs)
		}
		if _, err := run(abs, "init", "--quiet", "--initial-branch="+InitialBranch); err != nil {
			return Repo{}, err
		}
		madeGit = true
		if err := initialCommit(abs); err != nil {
			return Repo{}, err
		}
		return Open(abs)
	}()
	if err != nil {
		if created {
			_ = os.RemoveAll(abs)
		} else if madeGit {
			// Only the .git this made, which wasn't there a moment ago (checked
			// above): the folder and its files were there before.
			_ = os.RemoveAll(filepath.Join(abs, ".git"))
		}
		return Repo{}, false, err
	}
	return repo, created, nil
}

// initialCommit commits everything in root that .gitignore doesn't leave out,
// or nothing when there's nothing, as the repository's first commit. It's made
// the same whatever the user's git config says about signing and hooks, which
// could otherwise stop on a passphrase prompt nobody sees; and without an
// identity in that config, it's AgentBox's rather than failing.
func initialCommit(root string) error {
	skipped, err := Heavy(root)
	if err != nil {
		return err
	}
	if err := exclude(root, skipped); err != nil {
		return err
	}
	if _, err := run(root, "add", "--all"); err != nil {
		return err
	}
	_, err = output(root, identityEnv(root), "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "--allow-empty", "-m", "Initial commit")
	return err
}
