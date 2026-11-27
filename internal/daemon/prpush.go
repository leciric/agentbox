package daemon

import "fmt"

// How an agent is told to push its pull request's branch.
//
// A pull request's branch isn't the agent's alone: a reviewer, the user, or
// another tool can push to it — GitHub's "Update branch" button does — and
// the agent's own machine knows nothing of it until it fetches. The pull
// request watch and the restack both tell the agent to rebase and push, and a
// rebase needs a forced push. A bare `--force-with-lease` is no protection
// there: the lease is the remote-tracking ref, and the `git fetch origin` both
// messages ask for first moves it to the other person's commit, so the lease
// passes and the push drops what they pushed.
//
// So the message depends on whose commit the branch is at, which the daemon
// can tell from the head GitHub reported (pushedHeadIsAgents):
//
//   - The agent's own: a forced push loses nothing of anyone's, but the lease
//     is pinned to that head (`--force-with-lease=<branch>:<sha>`), which a
//     fetch doesn't move, so a push that lands between AgentBox's look and the
//     agent's push still makes it fail instead of being overwritten.
//   - Anyone else's: no force at all. The agent takes their commits in
//     (`git pull --rebase origin <branch>` replays only its own unpushed ones
//     on top) and pushes a fast-forward.
//
// The commit's author can't tell them apart: an agent commits as the user.

// pushedHeadIsAgents reports whether sha, where GitHub has a pull request's
// branch, is a commit the agent has: one it made, or its branch's tip (which
// may be a commit it took in from the branch, so nothing there is its alone to
// lose). An agent whose branches git couldn't read has no answer, which is no.
func pushedHeadIsAgents(heads []agentHead, agent, sha string) bool {
	if sha == "" {
		return false
	}
	for _, h := range heads {
		if h.agent == agent && !h.failed && (h.owns(sha) || (h.tip != "" && h.tip == sha)) {
			return true
		}
	}
	return false
}

// forcePush is the forced push of an agent's HEAD to a pull request's branch,
// held to the commit AgentBox saw the branch at.
func forcePush(branch, head string) string {
	return fmt.Sprintf("`git push --force-with-lease=%s:%s origin HEAD:%s`", branch, head, branch)
}
