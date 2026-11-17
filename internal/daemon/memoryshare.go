package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// Sharing a project's memories through its repository (memory/repo.go).
//
// The files are committed on a branch of their own, <prefix>memories, and
// not written into the user's checkout. A checkout is the user's: it may be on
// any branch, with work of their own half done in it and in its index, and
// files appearing there would ride along in whatever they commit next. A
// commit on a branch changes nothing they have open, is a pull request away
// from review, and is where the next share goes on adding to until it is
// merged. Pushing it is theirs: it is the one step that leaves the machine.

// memoryShareBranch is the branch shared memories are committed on.
func memoryShareBranch(p state.Project) string {
	prefix := p.BranchPrefix
	if prefix == "" {
		prefix = state.DefaultBranchPrefix
	}
	return prefix + "memories"
}

// memoryShareReadme explains the folder to whoever finds it in a review.
const memoryShareReadme = `# AgentBox memories

What this project's AgentBox agents learned, shared with everyone who works
on it: one memory per file, frontmatter (id, kind, title, importance, dates)
and its text.

AgentBox reads these files from the checkout whenever it reads the project's
memory, so they reach every agent's context. A memory you already have in
AgentBox — the same id, or one in the same superseded chain — stays as
AgentBox has it; the file is skipped.

Edit a file to correct a memory for everyone, or delete it to take the memory
out. Share more from the Memories page in AgentBox.
`

func (s *Server) shareMemories(ctx context.Context, m *memory.Store, project string, req api.ShareMemoriesRequest) (api.ShareMemoriesResult, error) {
	if project == memory.Global {
		return api.ShareMemoriesResult{}, errors.New("memories for every project are yours, not one repository's: share a project's memories")
	}
	if !req.All && len(req.IDs) == 0 {
		return api.ShareMemoriesResult{}, errors.New("say which memories to share (ids), or all of them (all)")
	}
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return api.ShareMemoriesResult{}, err
	}
	repo, err := gitrepo.Open(p.Root)
	if err != nil {
		return api.ShareMemoriesResult{}, err
	}
	if !repo.HasCommits() {
		return api.ShareMemoriesResult{}, fmt.Errorf("%s has no commits yet: commit something first, and share memories on top of it", p.Root)
	}
	ids := req.IDs
	if req.All {
		ids = nil
	}
	files, remove, err := m.RepoExport(ctx, project, ids)
	if err != nil {
		return api.ShareMemoriesResult{}, err
	}
	if len(files) == 0 {
		return api.ShareMemoriesResult{}, errors.New("nothing to share: the memories named are already in the repository, or the project has none of its own")
	}

	branch := memoryShareBranch(p)
	base, up, ok := repo.BaseBranch()
	if !ok {
		base = repo.CurrentBranch()
	}
	remote := "origin"
	if ok {
		remote = up.Remote
	}
	// Go on from the branch while its last share is still waiting for
	// review; start again from the base once that has been merged, so the
	// next pull request holds only what is new.
	start := base
	tip, err := repo.BranchTip(branch)
	if err != nil {
		return api.ShareMemoriesResult{}, err
	}
	if tip != "" && !repo.MergedInto(branch, base) {
		start = tip
	}

	write := map[string][]byte{}
	out := api.ShareMemoriesResult{Branch: branch, Base: base, Removed: remove,
		Push: "git push -u " + remote + " " + branch}
	for _, f := range files {
		write[f.Path] = f.Data
		out.Files = append(out.Files, f.Path)
	}
	if readme := memory.RepoDir + "/README.md"; !repo.HasPath(start, readme) {
		write[readme] = []byte(memoryShareReadme)
	}

	commit, changed, err := repo.CommitFiles(branch, start, write, remove, shareMessage(ctx, m, project, files))
	if err != nil {
		return api.ShareMemoriesResult{}, err
	}
	out.Commit, out.Unchanged = commit, !changed
	return out, nil
}

// shareMessage is the commit's message: a conventional subject, and the
// memories' titles, which is what a reviewer reads first.
func shareMessage(ctx context.Context, m *memory.Store, project string, files []memory.RepoFile) string {
	var b strings.Builder
	if len(files) == 1 {
		b.WriteString("docs: share 1 AgentBox memory\n\n")
	} else {
		fmt.Fprintf(&b, "docs: share %d AgentBox memories\n\n", len(files))
	}
	for _, f := range files {
		title := f.ID
		if mem, err := m.Memory(ctx, project, f.ID); err == nil {
			title = mem.Title
		}
		fmt.Fprintf(&b, "- %s\n", title)
	}
	return b.String()
}
