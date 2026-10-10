package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/mcp"
)

// newMemoryCmd gives an agent's AI tool its project's memory: what every
// agent of this project has found out, decided and left undone, and a way to
// add to it. Like `agentbox desktop mcp` it speaks the Model Context Protocol
// on stdin and stdout and is started by the AI tool from the configuration
// AgentBox writes into the agent, not by hand
// (D64, D72).
func newMemoryCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "What a project remembers: tidy its open items, or (inside an agent) serve it to the AI tool",
	}
	cmd.AddCommand(newMemoryMCPCmd(a), newMemoryTidyCmd(a))
	return cmd
}

// newMemoryTidyCmd cleans a project memory that is already full of open
// items nobody closed: it resolves the old ones and merges the duplicates
// among the rest, with no model involved. It is a dry run unless --apply.
func newMemoryTidyCmd(a *app) *cobra.Command {
	var olderThan string
	var apply bool
	cmd := &cobra.Command{
		Use:   "tidy <project>",
		Short: "Resolve a project's stale open issues and merge duplicate ones (a dry run without --apply)",
		Long: `Cleans up the open issues a project's memory has collected, without asking a model.

Every live issue, and every memory whose title says it is waiting on something
("PRs awaiting the user's merge"), that nobody has mentioned for longer than
--older-than is resolved, with "tidied" as what closed it. Of the ones left,
those about the same problem are merged into the newest, even when their
titles differ. Facts, decisions and discoveries are never touched.

Nothing is deleted: a resolved memory stays readable by id. Without --apply
this only prints what it would do.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			age, err := parseAge(olderThan)
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			plan, err := c.ProjectMemory(args[0]).Tidy(cmd.Context(), api.TidyMemoryRequest{
				OlderThanHours: int(age / time.Hour), Apply: apply,
			})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), describeTidy(args[0], olderThan, plan, time.Now()))
			return nil
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "7d", "resolve open items nobody has mentioned for longer than this (7d, 2w, 36h)")
	cmd.Flags().BoolVar(&apply, "apply", false, "do it, rather than print what it would do")
	return cmd
}

// parseAge reads a cutoff as people write one: days, weeks, or anything
// time.ParseDuration takes. Less than an hour is refused: it would tidy away
// what was written a minute ago.
func parseAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	bad := fmt.Errorf("--older-than is %q: write it as 7d, 2w or 36h", s)
	var age time.Duration
	switch unit := s[max(len(s)-1, 0):]; unit {
	case "d", "w":
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, bad
		}
		age = time.Duration(n) * 24 * time.Hour
		if unit == "w" {
			age *= 7
		}
	default:
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, bad
		}
		age = d
	}
	if age < time.Hour {
		return 0, fmt.Errorf("--older-than is %s: anything under an hour would tidy away what was just written", s)
	}
	return age, nil
}

func describeTidy(project, olderThan string, plan api.TidyMemoryResult, now time.Time) string {
	var b strings.Builder
	verb, merge := "would resolve", "would merge"
	if plan.Applied {
		verb, merge = "resolved", "merged"
	}
	fmt.Fprintf(&b, "%s: %s %d open %s nobody has mentioned in %s, and %s %d %s. %d %s open.\n",
		project, verb, len(plan.Resolved), plural(len(plan.Resolved), "item", "items"), olderThan,
		merge, len(plan.Merged), plural(len(plan.Merged), "duplicate", "duplicates"),
		plan.Kept, plural(plan.Kept, "stays", "stay"))
	if len(plan.Resolved) > 0 {
		fmt.Fprintf(&b, "\nResolved as tidied:\n")
		for _, m := range plan.Resolved {
			fmt.Fprintf(&b, "  %s  [%s] %s (%d days old)\n", m.ID, m.Kind, oneLine(m.Title), int(now.Sub(m.CreatedAt).Hours()/24))
		}
	}
	if len(plan.Merged) > 0 {
		fmt.Fprintf(&b, "\nMerged into the newest that says the same:\n")
		for _, mg := range plan.Merged {
			fmt.Fprintf(&b, "  %s  %s\n    → %s  %s (%s, %.2f)\n", mg.Memory.ID, oneLine(mg.Memory.Title),
				mg.Into.ID, oneLine(mg.Into.Title), mg.Why, mg.Score)
		}
	}
	if !plan.Applied && len(plan.Resolved)+len(plan.Merged) > 0 {
		b.WriteString("\nNothing was changed: run it again with --apply to do this.\n")
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func newMemoryMCPCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:    "mcp",
		Short:  "Serve a project's memory over the Model Context Protocol",
		Hidden: true, // started by the agent's AI tool, not by people
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The memory is the project's, reached over the agent's own
			// socket. On the host there is no agent this would mean, and no
			// project it would be about.
			socket := inAgentSocket()
			if _, err := os.Stat(socket); err != nil {
				return errors.New("agentbox memory mcp runs inside an agent, on its own project's memory; " +
					"from here, a project's memory is on its page in AgentBox")
			}
			c := api.NewClient(socket)
			srv := &mcp.Server{Name: "agentbox-memory", Version: version, Tools: agentMemoryTools(cmd.Context(), c)}
			return srv.Serve(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

// agentMemoryTools are what a worker agent may do with its project's memory:
// read all of it, and add to the parts that are a record of what it did. It
// doesn't curate — writing a project memory down, and saying what the project
// is doing now, belong to the project's chat, which has every agent in view.
func agentMemoryTools(ctx context.Context, c *api.Client) []mcp.Tool {
	decode := func(args json.RawMessage, into any) error {
		if len(args) == 0 {
			return nil
		}
		return json.Unmarshal(args, into)
	}
	m := c.SelfMemory()
	return []mcp.Tool{
		{
			Name: "search_memory",
			Description: "Search what this project remembers: what earlier agents found out, the decisions that " +
				"were made and why, the problems nobody has fixed, and the reports agents filed as they finished. " +
				"Search it before you spend time working something out — the gotcha you are about to hit has " +
				"probably already cost somebody an hour. Exact words work: a filename, a port, a package name, an " +
				"error string. Memory is the project's, not yours: it outlives you and every other agent.",
			Schema: object([]string{"query"}, map[string]any{
				"query": str("what you are looking for: words, a filename, a port, an error message"),
				"limit": map[string]any{"type": "integer", "description": "how many of each kind to return; 20 by default"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Query string
					Limit int
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				results, err := m.Search(ctx, in.Query, in.Limit)
				if err != nil {
					return "", err
				}
				return describeSearch(in.Query, results), nil
			},
		},
		memoryFeedbackTool(ctx, m),
		{
			Name: "report",
			Description: "File what you did, as you finish. This is not your final message to the user — it is the " +
				"structured record the project keeps, which the next agent and the project's chat read long after " +
				"this conversation is gone. Be honest about what is unfinished: a report that says \"done\" over " +
				"something half-built is worse than no report. Put what you found out that wasn't asked for in " +
				"discoveries, what you chose and why in decisions, and what is still wrong in remaining_issues.",
			Schema: object([]string{"summary"}, map[string]any{
				"summary": str("what happened, in a few sentences: what works now, and what doesn't"),
				"task":    str("what you were asked to do, in one line"),
				"status": choiceOf("how it ended. \"done\" is finished with nothing left. \"partial\" is some of it done, "+
					"with the rest in remaining_issues. \"blocked\" is stopped on something you couldn't get past. "+
					"\"failed\" is it didn't work. Anything but done needs remaining_issues filled in.",
					"done", "partial", "blocked", "failed"),
				"discoveries": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "what you found out that nobody asked for: a gotcha, a cause, a command that isn't in the README"},
				"decisions": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "what you chose, and why, so the next agent doesn't quietly undo it"},
				"remaining_issues": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "what is still wrong or unfinished, one line each"},
				"artifacts": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "what you produced: artifact ids from record_artifact, or plain references like a branch or a pull request"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Summary, Task, Status string
					Discoveries           []string
					Decisions             []string
					RemainingIssues       []string `json:"remaining_issues"`
					Artifacts             []string
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Summary) == "" {
					return "", errors.New("a report needs a summary: what happened, in a few sentences")
				}
				r, err := m.AddReport(ctx, api.AddReportRequest{
					Task: in.Task, Status: in.Status, Summary: in.Summary,
					Discoveries: in.Discoveries, Decisions: in.Decisions,
					RemainingIssues: in.RemainingIssues, Artifacts: in.Artifacts,
				})
				if err != nil {
					return "", err
				}
				left := ""
				if len(r.RemainingIssues) > 0 {
					left = fmt.Sprintf(" %d thing(s) left undone are recorded with it.", len(r.RemainingIssues))
				}
				return fmt.Sprintf("Reported as %s (%s). The project's chat and every agent after you can read it.%s",
					r.ID, r.Status, left), nil
			},
		},
		{
			Name: "my_task",
			Description: "The task from the user's task list you were made for, when the user started you from " +
				"one: what it is, in their words. The list is the user's, and only they change it. It can also show " +
				"the rest of the list, so you can see what else is planned before you touch the same files.",
			Schema: object(nil, map[string]any{
				"all": map[string]any{"type": "boolean",
					"description": "show every open task on the user's list, not only yours"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ All bool }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				return describeMyTask(ctx, c, in.All), nil
			},
		},
		{
			Name: "request_credential",
			Description: "Ask the user for a credential you lack, and wait for their answer. Use it when something fails " +
				"on auth: a push or gh answering \"Repository not found\", 403 or \"permission denied\" (kind github), or " +
				"a key the task needs that isn't in your environment (kind secret, with the variable's name). The user " +
				"answers in the app, where they pick a GitHub account or type the value, and AgentBox puts it into your " +
				"environment itself: the value never passes through you or any chat. What you get back is only what " +
				"happened — which account you have now, the variable it is in, or that they refused and why. Never ask " +
				"for a credential in chat or with agentbox ask, and never ask the user to paste one: a value written in " +
				"a conversation is kept as text. Ask once, for what you actually need.",
			Schema: object([]string{"kind", "reason"}, map[string]any{
				"kind": choiceOf("what you need. \"github\" is a GitHub account that can reach this repository, which "+
					"becomes this project's account and replaces GH_TOKEN and GITHUB_TOKEN here. \"secret\" is a value "+
					"in an environment variable, like an API key.", "github", "secret"),
				"name": str("for a secret, the environment variable it should be in, in capitals: STRIPE_SECRET_KEY. " +
					"Leave it out for github"),
				"reason": str("what failed and what you need it for, in a sentence or two, so the user can decide: " +
					"the command and its error, and what you were doing"),
			}),
			// It waits, and the daemon has to hear when the call is given up on
			// — interrupted, or its session gone — so the request doesn't stay
			// waiting on the user for a call nobody is waiting on.
			Wait: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct{ Kind, Name, Reason string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				q, err := c.RequestCredential(ctx, api.CredentialRequest{Kind: in.Kind, Name: in.Name, Reason: in.Reason})
				if err != nil {
					return "", err
				}
				return q.Answer, nil
			},
		},
		{
			Name: "request_connector",
			Description: "Ask the user to connect a remote MCP server you need as tools — Notion, Linear, Sentry, Figma, " +
				"a company's own — and wait for their answer. Use it when the task needs a service you have no tools for: " +
				"the spec is in Notion, the bug is in Linear. The user adds and signs in to it in the app, and AgentBox " +
				"keeps the sign-in: no token ever reaches you. What you get back is what happened — that it's connected " +
				"and how to use it right away, or that they declined and why. Ask once, for what you actually need.",
			Schema: object([]string{"name", "reason"}, map[string]any{
				"name": str("the connector, in lowercase: notion, linear, sentry, figma, or a short name for another " +
					"server. Its tools are called by it (mcp__notion__* in Claude Code)"),
				"url": str("the server's MCP endpoint, when the project may not have this connector yet: " +
					"https://mcp.notion.com/mcp, https://mcp.linear.app/mcp, https://mcp.sentry.dev/mcp, " +
					"https://mcp.figma.com/mcp. Left out for one the project has"),
				"reason": str("what you need it for, in a sentence or two, so the user can decide"),
			}),
			// It waits on the user like request_credential.
			Wait: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct{ Name, URL, Reason string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				q, err := c.RequestConnector(ctx, api.ConnectorRequest{Name: in.Name, URL: in.URL, Reason: in.Reason})
				if err != nil {
					return "", err
				}
				return q.Answer, nil
			},
		},
		{
			Name: "record_artifact",
			Description: "Record where something you produced lives, so it can be found again: a file you wrote, " +
				"a branch, a pull request, a recording. It is a reference and nothing more — the contents stay " +
				"where they are, and are never copied into memory. Use it for what somebody will want to look at " +
				"later, not for every file you touched; the diff already says that.",
			Schema: object([]string{"type", "path"}, map[string]any{
				"type": choiceOf("what kind of thing it is", "file", "branch", "pull_request", "media", "url", "other"),
				"path": str("where it is: a path in the worktree, a branch name, a URL"),
				"metadata": map[string]any{"type": "object", "description": "anything else worth keeping about it, " +
					"as a small JSON object — what it is for, what it replaces"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Type, Path string
					Metadata   json.RawMessage
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				a, err := m.AddArtifact(ctx, api.AddArtifactRequest{Type: in.Type, Path: in.Path, Metadata: in.Metadata})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Recorded %s as %s. Name that id in your report's artifacts.", a.Path, a.ID), nil
			},
		},
	}
}

// myTask is the task this agent is on: the open one the project's plan has
// against its name. An agent is made for one thing at a time, so the newest
// open task of its own is the one it means — and an agent with none is an
// agent nobody wrote work down for, which is worth saying rather than
// answering with the first task it can see.
func myTask(ctx context.Context, c *api.Client) (api.Task, error) {
	tasks, err := c.SelfMemory().Tasks(ctx, api.TaskQuery{Agent: selfAgent(ctx, c), OpenOnly: true})
	if err != nil {
		return api.Task{}, err
	}
	if len(tasks) == 0 {
		return api.Task{}, errors.New("the user's task list has no open task against your name: " +
			"your task is the one in your first message")
	}
	return tasks[0], nil
}

// selfAgent is this agent's name, which its own socket already knows. An
// empty answer means the listing isn't narrowed, and my_task then says the
// plan has nothing for it rather than claiming somebody else's.
func selfAgent(ctx context.Context, c *api.Client) string {
	self, err := c.Self(ctx)
	if err != nil {
		return ""
	}
	return self.Agent
}

// describeMyTask is what an agent reads about its own work, and optionally
// about everybody's. Each part fails quietly: an agent that can't be told its
// task should still be told the plan.
func describeMyTask(ctx context.Context, c *api.Client, all bool) string {
	var b strings.Builder
	mine, err := myTask(ctx, c)
	if err != nil {
		b.WriteString(err.Error() + "\n")
	} else {
		b.WriteString("Your task:\n")
		b.WriteString(describeTasks([]api.Task{mine}))
	}
	if !all {
		return b.String()
	}
	tasks, err := c.SelfMemory().Tasks(ctx, api.TaskQuery{OpenOnly: true})
	if err != nil || len(tasks) == 0 {
		return b.String()
	}
	b.WriteString("\nEverything still open on the user's list:\n")
	b.WriteString(describeTasks(tasks))
	return b.String()
}

// memoryFeedbackTool is how an agent or the lead says that a memory it was
// handed is wrong, stale or helpful (memory/feedback.go). A brief lists
// memories by title, so the tool takes one as readily as an id.
func memoryFeedbackTool(ctx context.Context, m *api.MemoryClient) mcp.Tool {
	return mcp.Tool{
		Name: "memory_feedback",
		Description: "Say that a memory you were handed — in your brief or from search_memory — is wrong, stale " +
			"or helpful, as soon as you find out. \"wrong\" (it says something untrue) and \"stale\" (it was true and " +
			"no longer is) drop it to the bottom of every search and brief at once; stale also closes an open " +
			"problem. \"helpful\" (it saved you the work) raises it a little. Who said it and why is kept. When you " +
			"know what is true instead, the lead can write that down; this only says the old one can't be trusted.",
		Schema: object([]string{"memory", "verdict"}, map[string]any{
			"memory":  str("the memory's id from search_memory, or its title exactly as you were shown it"),
			"verdict": choiceOf("what you found", "wrong", "stale", "helpful"),
			"why":     str("what you found, in a line: the file, command or pull request that shows it; required unless helpful"),
		}),
		Run: func(args json.RawMessage) (string, error) {
			var in api.MemoryFeedbackRequest
			if len(args) > 0 {
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
			}
			out, err := m.Feedback(ctx, in)
			if err != nil {
				return "", err
			}
			switch {
			case out.Resolved:
				return fmt.Sprintf("Closed %s (%q) as %s: it no longer comes back from a search.", out.Memory.ID, out.Memory.Title, in.Verdict), nil
			case out.Memory.Importance == out.Was:
				return fmt.Sprintf("Noted %s (%q) as %s; its importance stays %d.", out.Memory.ID, out.Memory.Title, in.Verdict, out.Was), nil
			}
			return fmt.Sprintf("Noted %s (%q) as %s: importance %d, was %d.", out.Memory.ID, out.Memory.Title, in.Verdict,
				out.Memory.Importance, out.Was), nil
		},
	}
}
