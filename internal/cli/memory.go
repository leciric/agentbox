package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

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
		Short: "What this project remembers: search it, report on your task, record what you produced",
	}
	cmd.AddCommand(newMemoryMCPCmd(a))
	return cmd
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
			Description: "The task you were made for, as the project's plan has it: what it is, where it stands, " +
				"what it is part of, and what it is waiting on. Read it before you start — it is what the project's " +
				"chat wrote down, which is not always the same as what your first message said — and read it again " +
				"if you are about to go beyond it. It also shows the rest of the plan, so you can see what other " +
				"agents are on before you touch the same files.",
			Schema: object(nil, map[string]any{
				"all": map[string]any{"type": "boolean",
					"description": "show every open task of the project, not only yours. Useful before you change " +
						"something somebody else is working on"},
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
			Name: "update_my_task",
			Description: "Say how your own task is going, so the project's plan is true while you are still " +
				"working rather than only after you finish. Mark it \"active\" when you pick it up, \"blocked\" the " +
				"moment something stops you — say what in detail, and ask the project's chat as well if you are " +
				"waiting on a decision — and \"done\" when it is finished. This is not report: report is the " +
				"structured record you file as you finish, and this is the one line of state the plan is read by " +
				"while you are still going. You can only change your own task, and only its status and its detail: " +
				"what the task is, who it belongs to and what it is part of are the project chat's to decide.",
			Schema: object(nil, map[string]any{
				"status": choiceOf("where your task now stands. \"active\" is you are on it. \"blocked\" is something "+
					"is stopping you. \"done\" is finished. \"open\" is you put it back down without finishing it.",
					"open", "active", "blocked", "done"),
				"detail": str("what to record about it now, replacing what was there: how far you have got, and " +
					"what is stopping you if anything is"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Status *string
					Detail *string
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				mine, err := myTask(ctx, c)
				if err != nil {
					return "", err
				}
				t, err := m.UpdateTask(ctx, mine.ID, api.UpdateTaskRequest{Status: in.Status, Detail: in.Detail})
				if err != nil {
					return "", err
				}
				if t.Status == api.TaskBlocked {
					return fmt.Sprintf("%s is blocked. The project's chat sees it in project_state and in its next "+
						"brief; if you need a decision to get past it, ask for one as well.", t.ID), nil
				}
				return fmt.Sprintf("%s is %s.", t.ID, t.Status), nil
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
		return api.Task{}, errors.New("the project's plan has no open task against your name. " +
			"Say what you are doing in your report as you finish; the project's chat is what writes tasks down")
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
		if len(mine.DependsOn) > 0 {
			b.WriteString("You are waiting on those. update_my_task marks yourself blocked; " +
				"the project's chat is what unblocks the plan.\n")
		}
	}
	if !all {
		return b.String()
	}
	tasks, err := c.SelfMemory().Tasks(ctx, api.TaskQuery{OpenOnly: true})
	if err != nil || len(tasks) == 0 {
		return b.String()
	}
	b.WriteString("\nEverything still open in this project:\n")
	b.WriteString(describeTasks(tasks))
	return b.String()
}
