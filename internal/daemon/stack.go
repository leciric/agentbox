package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/state"
)

// Stacked work: an agent made from another agent's branch (create_agent's
// `from`) stands on that branch, its BaseRef, and opens its pull request
// against it, as its brief says. Its pull request then shows only its own
// commits, and the two are reviewed one after the other.
//
// When the parent's pull request merges, the child's base is a branch that is
// done. AgentBox then, from whichever saw the merge first — a merge made in
// the app (afterMerge) or the pull request watch (watchProject):
//
//   - retargets every open pull request onto the parent's branch to the
//     branch the parent merged into. GitHub does that by itself when it
//     deletes the merged branch; the call is then a no-op.
//   - moves each child agent's base to that branch (Manager.Restack), with
//     the parent's last tip as the commit its own commits are told from, and
//     writes its brief again.
//   - tells the child to rebase with `git rebase --onto <base> <parent's old
//     tip>`. A squash or rebase merge puts the parent's work on the base
//     under new commit ids, so a plain rebase would replay the parent's
//     commits on top of their own squashed copy and conflict with itself;
//     --onto from the old tip replays only the child's commits, and is just
//     as right after a merge commit.
//
// A parent closed without merging leaves its children on its branch: the
// project's chat is told, to decide.

// stackBase is the pull request a stack stood on, as whoever saw it end read
// it.
type stackBase struct {
	Number  int
	URL     string
	State   string // merged or closed
	Head    string // its branch: the children's base
	HeadSHA string // where that branch was when it ended
	Base    string // what it merged into
}

// stackChild is an agent standing on a stack's base, with its open pull
// request when it has one.
type stackChild struct {
	agent state.Agent
	pr    *github.PullRequest
}

// restack moves what stood on a pull request that just merged onto the
// branch it merged into, and tells the project's chat about one that was
// closed. Once per pull request, whichever of the merge and the watch gets
// there first.
func (s *Server) restack(ctx context.Context, p state.Project, client github.Client, repo github.Repo, parent stackBase) {
	if parent.Head == "" || parent.Base == "" || parent.Head == parent.Base {
		return
	}
	if _, done := s.restacked.LoadOrStore(fmt.Sprintf("%s#%d", repo, parent.Number), true); done {
		return
	}
	agents, err := s.store.Agents(ctx, p.Name)
	if err != nil {
		s.logf("restacking on %s's #%d: %v", p.Name, parent.Number, err)
		return
	}
	var children []stackChild
	for _, a := range agents {
		if !a.IsLead() && a.Branch != "" && a.BaseRef == parent.Head {
			children = append(children, stackChild{agent: a})
		}
	}
	if parent.State != "merged" {
		if len(children) > 0 {
			s.prLead(ctx, p.Name, fmt.Sprintf(
				"AgentBox: PR #%d (%s) was closed without merging, and %s stand on its branch `%s`. Decide what becomes of them: rebase them onto `%s` with tell_agent, or retire them.",
				parent.Number, parent.URL, childNames(children), parent.Head, parent.Base), true)
		}
		return
	}

	// Nothing to ask GitHub when nothing stands on it: no agent, and no open
	// pull request the cache knows of.
	entry, _ := s.pulls.state(repo.String())
	if len(children) == 0 && !slices.ContainsFunc(entry.prs, func(pr api.PullRequest) bool {
		return pr.State == "open" && pr.BaseBranch == parent.Head
	}) {
		return
	}

	// The pull requests onto the merged branch, each moved to where it merged.
	onto, err := client.PullRequestsOnto(ctx, repo, parent.Head)
	if err != nil {
		s.logf("restacking on %s's #%d: listing the pull requests onto %s: %v", p.Name, parent.Number, parent.Head, err)
	}
	var unmoved []string
	for i := range onto {
		pr := &onto[i]
		if err := client.Retarget(ctx, repo, pr.Number, parent.Base); err != nil {
			s.logf("restacking on %s's #%d: retargeting #%d to %s: %v", p.Name, parent.Number, pr.Number, parent.Base, err)
			unmoved = append(unmoved, fmt.Sprintf("#%d (%v)", pr.Number, err))
			continue
		}
		pr.BaseBranch = parent.Base
		s.logf("%s: #%d stood on #%d, which merged; it targets %s now", p.Name, pr.Number, parent.Number, parent.Base)
		i := slices.IndexFunc(children, func(c stackChild) bool { return c.agent.Branch == pr.HeadBranch })
		if i < 0 {
			// An agent whose base was set some other way, opening its pull
			// request against the parent anyway: still a child.
			if j := slices.IndexFunc(agents, func(a state.Agent) bool { return !a.IsLead() && a.Branch == pr.HeadBranch }); j >= 0 {
				children = append(children, stackChild{agent: agents[j]})
				i = len(children) - 1
			}
		}
		if i >= 0 {
			children[i].pr = pr
		}
	}
	if len(onto) > 0 {
		s.pulls.invalidate(repo.String())
		s.prWatch.poke(p.Name)
	}

	var told, untold []string
	for _, c := range children {
		a, err := s.manager(nil).Restack(ctx, c.agent, parent.Base, parent.HeadSHA)
		if err != nil {
			s.logf("restacking %s on %s: %v", c.agent.Ref(), parent.Base, err)
		}
		s.captureEvent(ctx, p.Name, a.Name, "restacked", map[string]any{
			"parent": parent.Number, "from": parent.Head, "onto": parent.Base, "old_tip": parent.HeadSHA,
		}, "")
		if _, err := s.prTell(ctx, a, restackMessage(parent, a, c.pr)); err != nil {
			s.logf("restacking: telling %s: %v", a.Ref(), err)
			untold = append(untold, fmt.Sprintf("%s (%v)", a.Name, err))
			continue
		}
		told = append(told, a.Name)
	}
	if len(told) == 0 && len(untold) == 0 && len(unmoved) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "AgentBox: PR #%d (%s) merged into `%s`, and what stood on it moved there.", parent.Number, parent.URL, parent.Base)
	if len(told) > 0 {
		fmt.Fprintf(&b, " Told %s to rebase onto `%s` and push.", strings.Join(told, ", "), parent.Base)
	}
	if len(untold) > 0 {
		fmt.Fprintf(&b, " Couldn't tell %s: get them rebased with `git rebase --onto origin/%s %s`.", strings.Join(untold, ", "), parent.Base, shortSHA(parent.HeadSHA))
	}
	if len(unmoved) > 0 {
		fmt.Fprintf(&b, " Couldn't retarget %s to `%s`: the user can, on GitHub.", strings.Join(unmoved, ", "), parent.Base)
	}
	if len(untold) == 0 && len(unmoved) == 0 {
		b.WriteString(" Nothing to do unless they can't.")
	}
	s.prLead(ctx, p.Name, b.String(), len(untold) > 0 || len(unmoved) > 0)
}

func childNames(children []stackChild) string {
	names := make([]string, len(children))
	for i, c := range children {
		names[i] = c.agent.Name
	}
	return strings.Join(names, ", ")
}

// restackMessage is what a child is sent when its parent merged: rebase only
// its own commits, from the parent's old tip, and push them.
func restackMessage(parent stackBase, a state.Agent, pr *github.PullRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AgentBox: the pull request your branch is stacked on, #%d (%s, `%s`), was merged into `%s`.", parent.Number, parent.URL, parent.Head, parent.Base)
	if pr != nil {
		fmt.Fprintf(&b, " Your pull request #%d targets `%s` now.", pr.Number, parent.Base)
	} else {
		fmt.Fprintf(&b, " Your branch's base is `%s` now: open your pull request against it, not `%s`.", parent.Base, parent.Head)
	}
	fmt.Fprintf(&b, "\n\nIts commits may be on `%s` under new ids (a squash merge), so a plain rebase would replay them over their own copy. Rebase only your own: hand it to a Sonnet subagent with your Agent tool — `git fetch origin`, then `git rebase --onto origin/%s %s %s` (%s is where `%s` was when it merged), resolve any conflicts keeping both sides' intent, regenerate goldens and generated files, and rerun the tests — then check its result: `git log origin/%s..%s` should list only your commits.",
		parent.Base, parent.Base, parent.HeadSHA, a.Branch, shortSHA(parent.HeadSHA), parent.Head, parent.Base, a.Branch)
	if pr != nil {
		fmt.Fprintf(&b, "\n\nThen push `%s` with `--force-with-lease`: you're asked to, for this. Say in a line that it's done.", a.Branch)
	} else {
		fmt.Fprintf(&b, "\n\nIf you've pushed `%s` already, push it again with `--force-with-lease`: you're asked to, for this. Then carry on with your task.", a.Branch)
	}
	return b.String()
}
