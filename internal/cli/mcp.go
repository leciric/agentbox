package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/mcp"
)

// newMCPCmd gives a project's chat its tools. It speaks the Model Context
// Protocol on stdin and stdout, and is started by Claude Code from the lead's
// own configuration, with AGENTBOX_SOCKET pointing at that project's socket.
// The socket decides which project it acts on: nothing here names one, and
// nothing it can send reaches another.
func newMCPCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:    "mcp",
		Short:  "Serve a project chat's tools over the Model Context Protocol",
		Hidden: true, // started by Claude Code, not by people
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			socket := os.Getenv("AGENTBOX_SOCKET")
			if socket == "" {
				return errors.New("AGENTBOX_SOCKET isn't set: agentbox mcp is started by a project's chat, not by hand")
			}
			c := api.NewClient(socket)
			srv := &mcp.Server{Name: "agentbox", Version: version, Tools: projectTools(cmd.Context(), c)}
			return srv.Serve(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

// merge adds one set of schema properties to another, for parameters built at
// startup from what the daemon knows.
func merge(props, more map[string]any) map[string]any {
	for name, schema := range more {
		props[name] = schema
	}
	return props
}

// object builds a JSON Schema for a tool's arguments.
func object(required []string, props map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// choiceOf is a string parameter limited to a fixed set of values.
func choiceOf(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}

// chatSettingParams describe the Claude Code settings a lead may choose for an
// agent it creates. Only a model listed in its description is worth passing —
// the rest of the world's model names are not Claude Code's — so the list has
// to be this account's own, not one compiled into AgentBox. It comes from the
// menus a Claude Code adapter really advertised, remembered by the daemon
// (rememberChoices in internal/chat) and read back over the lead socket.
//
// An LLM reads these descriptions and nothing else. Each one says what happens
// when it is left out, because leaving it out is the common case and it must
// not look like "then nothing happens" — and in a project on
// api.AgentModelAuto leaving the model out is no longer the common case, so
// there the description asks for a choice instead.
//
// It also reports whether the project leaves the model to the lead, so the
// tool's own description agrees with the parameter's rather than telling it in
// one sentence that agents start on a model already chosen and in the next
// that choosing is its job.
func chatSettingParams(ctx context.Context, c *api.Client) (params map[string]any, leadPicksModel, openCodeReady bool) {
	var models, efforts, openCodeModels []string
	var openCode bool
	if settings, err := c.ProjectSettings(ctx); err == nil {
		for _, choice := range settings.ClaudeModelChoices {
			models = append(models, choice.Value)
		}
		for _, choice := range settings.ClaudeEffortChoices {
			efforts = append(efforts, choice.Value)
		}
		for _, choice := range settings.OpenCodeModelChoices {
			openCodeModels = append(openCodeModels, choice.Value)
		}
		openCode = settings.OpenCodeReady
	}
	// What the project asks of this parameter. The lead's brief says the same
	// thing at greater length; this is what a model reads at the moment it
	// fills the tool call in.
	var auto bool
	if p, err := c.ProjectSelf(ctx); err == nil {
		auto = p.AgentModel == api.AgentModelAuto
	}

	// Before any chat has started there is no remembered menu, and AgentBox
	// has nothing honest to name. The description then says what kind of name
	// is wanted, and says plainly that a wrong one fails rather than working.
	named := "a name Claude Code itself uses for a model, like \"sonnet\" — not an API model id, and not any model you have heard of"
	if len(models) > 0 {
		named = "one of the models this account's Claude Code offers: " + strings.Join(models, ", ")
	}
	model := "the model this agent runs on: " + named + ". A model Claude Code won't accept is refused when the agent " +
		"starts, and the agent says so in its own chat instead of quietly running on something else, so don't guess one. " +
		"Leave this out to use the model chosen for new agents in AgentBox's settings, and AgentBox's own default " +
		"(opus) when nothing is chosen there."
	if auto {
		model = "the model this agent runs on: " + named + ". This project asks you to choose one for every agent you create, " +
			"from how hard the task is: the cheapest model on that list that can do a mechanical or small job (a rename, a config " +
			"change, one test, a doc); the ordinary strong one (opus) for feature work; and that one at a higher effort for hard " +
			"design work, or a bug whose cause nobody has found. Say which you chose, and why, in one line as you create the agent. " +
			"Never choose Fable unless the user has asked for it, for this agent or for this project. A model Claude Code won't " +
			"accept is refused when the agent starts, and the agent says so in its own chat, so don't guess one. Leaving this out " +
			"doesn't fail: the agent falls back to the model new agents start on in AgentBox's settings."
	}
	// The AI tool itself, offered only when an agent could really run the
	// other one: OpenCode has to be in the base image and have a login, and
	// asking for it otherwise would be a tool call that can only fail. The
	// models go with it, because OpenCode's names and Claude Code's are not
	// interchangeable in either direction.
	if openCode {
		named := "OpenCode's own provider/model ids"
		if len(openCodeModels) > 0 {
			named = "one of " + strings.Join(openCodeModels, ", ")
		}
		model += " For an agent with ai=\"opencode\", the model is not a Claude Code name but " + named + " instead."
		params = map[string]any{
			"ai": choiceOf("which AI tool the agent runs. \"claude\" is Claude Code and the default, and what this project's "+
				"agents are set up for. \"opencode\" is OpenCode, the open-source agent, which runs the models of whichever "+
				"providers this machine has logged in to; choose it when the task asks for it, or when the user asked for one "+
				"of those models. Say which you chose and why, in the same line as the model.", "claude", "opencode"),
		}
	} else {
		params = map[string]any{}
	}
	params["model"] = str(model)
	params["permissions"] = choiceOf("how the agent's AI tool asks permission. \"autonomous\" never asks: the agent's own machine "+
		"is the sandbox, and it works unattended. \"ask\" makes it stop before it changes anything, which only makes sense "+
		"if someone is watching its chat to answer. Leave this out for autonomous.", "autonomous", "ask")
	effort := str("how hard this agent thinks, as one of Claude Code's own effort levels — a Claude Code setting, which an " +
		"OpenCode agent doesn't have, so leave it out for one. This one is checked when the agent is " +
		"made, so a level Claude Code has never offered is refused here rather than silently ignored. Some models have no effort " +
		"levels at all, and then it simply doesn't apply. Leave this out to use the effort chosen for new agents in AgentBox's " +
		"settings, and AgentBox's own default (high) when nothing is chosen there.")
	if len(efforts) > 0 {
		effort["enum"] = efforts
	}
	params["effort"] = effort
	params["context_window"] = str("where this agent's chat compacts, \"200k\" or \"1m\" — a Claude Code setting, checked " +
		"against the model: Haiku has no 1M window, and asking for one is refused. Leave this out for 200k (the installation's " +
		"compact window), which is right for nearly every task: past it, every step of the agent resends the whole " +
		"conversation, so 1M costs up to five times as much per step late in a long task. Choose 1m only for work that " +
		"really needs a very large codebase or log in view at once.")
	return params, auto, openCode
}

// leadAI reads create_agent's "ai": the AI tool the agent runs. Empty is
// Claude Code, which is what create_agent has always made. Anything else is
// refused here rather than in the daemon, so the refusal can say why the tool
// isn't on offer — the lead is told about OpenCode only when an agent could
// really run it, and a lead working from an older brief would otherwise get an
// error about a missing login with nothing to do about it.
func leadAI(ai string, openCodeReady bool) (string, error) {
	switch strings.TrimSpace(strings.ToLower(ai)) {
	case "", "claude":
		return "claude", nil
	case "opencode":
		if !openCodeReady {
			return "", errors.New("this machine can't run OpenCode agents: OpenCode has to be built into the base image " +
				"(agentbox image build --opencode) and logged in (agentbox auth opencode). Create the agent with ai=\"claude\", " +
				"or ask the user to set OpenCode up first")
		}
		return "opencode", nil
	default:
		return "", fmt.Errorf("ai is %q: it is \"claude\" (Claude Code) or \"opencode\" (OpenCode)", ai)
	}
}

// describeChoices names the settings a lead asked for, so the reply confirms
// what was chosen rather than leaving it to be found later in the agent's chat.
// Anything left to fall back to the project's own defaults goes unmentioned.
func describeChoices(ai string, model, effort *string, autonomous bool, notify, account string) string {
	var chosen []string
	if ai != "" && ai != "claude" {
		chosen = append(chosen, "on "+ai)
	}
	if model != nil {
		chosen = append(chosen, "on "+*model)
	}
	if effort != nil {
		chosen = append(chosen, "at "+*effort+" effort")
	}
	if !autonomous {
		chosen = append(chosen, "stopping to ask before it changes anything")
	}
	if notify != "" {
		chosen = append(chosen, "notify "+notify)
	}
	if account != "" {
		chosen = append(chosen, "on the "+account+" account")
	}
	if len(chosen) == 0 {
		return ""
	}
	return ", " + strings.Join(chosen, ", ")
}

// windowChoice names a context window a lead asked for, in describeChoices'
// shape.
func windowChoice(window *string) string {
	if window == nil {
		return ""
	}
	return ", with a " + *window + " context window"
}

// projectTools are everything a project's chat can do. Each one is scoped to
// the project behind the socket.
func projectTools(ctx context.Context, c *api.Client) []mcp.Tool {
	decode := func(args json.RawMessage, into any) error {
		if len(args) == 0 {
			return nil
		}
		return json.Unmarshal(args, into)
	}
	settings, leadPicksModel, openCodeReady := chatSettingParams(ctx, c)
	startsOn := "It starts on the model, effort " +
		"and permissions new agents start on here; set those below only when this agent needs " +
		"something different, such as a cheaper model for a small, mechanical job."
	if leadPicksModel {
		startsOn = "This project asks you to choose the model for each agent you create, from how hard " +
			"the task is; the effort and permissions below are the ones new agents start on here unless you set them."
	}
	return []mcp.Tool{
		{
			Name: "list_agents",
			Description: "List this project's agents: what each one is for, what it is doing now, " +
				"how much it has changed, what it has shown, its pull request, and whether it has " +
				"finished and is holding a machine for nothing.",
			Run: func(json.RawMessage) (string, error) {
				fleet, err := c.ProjectFleet(ctx)
				if err != nil {
					return "", err
				}
				return describeFleet(fleet), nil
			},
		},
		{
			Name: "list_accounts",
			Description: "The Claude Code accounts this project may use, so you can spread agents across them instead of piling " +
				"them onto one: which is the machine's default, which this project uses when an agent doesn't choose " +
				"its own, how many of this project's running agents already hold each one, and its latest usage " +
				"reading (5-hour and weekly, and when each resets). Never a token — pass the name to create_agent's " +
				"claude_account to choose one.",
			Run: func(json.RawMessage) (string, error) {
				accounts, err := c.ProjectAccounts(ctx)
				if err != nil {
					return "", err
				}
				return describeAccounts(accounts), nil
			},
		},
		{
			Name: "list_secrets",
			Description: "The names of the API keys and tokens the user has given this project's agents. " +
				"Names only, and there is no tool that reads a value — not for you and not for an agent's " +
				"model, which reads them from its environment. Use this to brief an agent precisely: " +
				"\"the Stripe key is in $STRIPE_SECRET_KEY, don't print it\". If a key an agent needs isn't " +
				"listed, ask the user to add it (the Secrets tab on the project's page, or agentbox secrets " +
				"set) instead of asking them to paste it into this chat, where it would be stored as text.",
			Run: func(json.RawMessage) (string, error) {
				secrets, err := c.ProjectSecretNames(ctx)
				if err != nil {
					return "", err
				}
				return describeSecrets(secrets), nil
			},
		},
		{
			Name: "create_agent",
			Description: "Create an agent and give it a task. It gets its own machine and, unless " +
				"research is true, its own branch. Prefer several small well-briefed agents over one " +
				"that is asked to do everything. Write the task directly: what to do, what done looks like, " +
				"what to leave alone, and nothing its brief or the project already says. " + startsOn,
			Schema: object([]string{"title", "task"}, merge(map[string]any{
				"title": str("a short name the user will recognise in the sidebar, like \"Reminders page\""),
				"task":  str("what to do, directly: usually a few lines"),
				"branch": str("its branch, after the project's prefix: short lowercase kebab-case naming the work, like " +
					"\"fix-login-redirect\" or \"feat-csv-export\", at most 48 characters. Pass one every time; left out, it is made " +
					"from the title. A branch that is already taken gets -2, -3… appended."),
				"from":     str("the branch or agent branch to start from; the project's branch by default"),
				"research": map[string]any{"type": "boolean", "description": "it only investigates, so it gets no branch of its own"},
				"notify": choiceOf("what a genuine finish does to your chat: \"chat\" to be told and woken when this agent finishes, "+
					"\"off\" to only have the finish recorded — for a small, mechanical job you don't need to react to. This only "+
					"matters when this project's finish notices are set to \"lead\"; otherwise the project's own setting decides for "+
					"every agent, whatever you choose here. Leave this out to be woken, the same as \"chat\".", "chat", "off"),
				"claude_account": str("which of this project's Claude Code accounts (list_accounts) this agent logs in as. Only applies " +
					"when it runs Claude Code; sending it for an agent with ai=\"opencode\" is an error. An unknown or disallowed name is " +
					"refused, and the error names the ones it may use — use list_accounts to see them, along with how much " +
					"of each is left. Leave this out to use the project's own account, and the machine's default account when " +
					"the project has none set."),
			}, settings)),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Title, Task, From, AI, Branch string
					ClaudeAccount                 string `json:"claude_account"`
					Research                      bool
					// Pointers: an agent given no model is not the same as one
					// asked for the empty model, and only the first falls back
					// to what new agents start on.
					Model, Effort, Permissions, Notify *string
					ContextWindow                      *string `json:"context_window"`
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Task) == "" {
					return "", errors.New("an agent needs a title and a task")
				}
				ai, err := leadAI(in.AI, openCodeReady)
				if err != nil {
					return "", err
				}
				// Autonomous unless the lead asked for an agent that stops to
				// ask, which is what create_agent has always made.
				autonomous := true
				if in.Permissions != nil {
					switch *in.Permissions {
					case "autonomous":
					case "ask":
						autonomous = false
					default:
						return "", fmt.Errorf("permissions is %q: it is \"autonomous\" (never asks) or \"ask\" (stops before it changes anything)", *in.Permissions)
					}
				}
				var notify string
				if in.Notify != nil {
					switch *in.Notify {
					case "chat", "off":
						notify = *in.Notify
					default:
						return "", fmt.Errorf("notify is %q: it is \"chat\" (wakes you when this agent finishes) or \"off\" (only records it)", *in.Notify)
					}
				}
				job, err := c.CreateProjectAgent(ctx, api.CreateAgentRequest{
					Title: in.Title, Task: in.Task, From: in.From, AI: ai, Branch: in.Branch,
					Autonomous: &autonomous, Model: in.Model, Effort: in.Effort,
					ContextWindow: in.ContextWindow,
					ClaudeAccount: in.ClaudeAccount,
					FinishNotice:  notify,
				})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Creating %q%s; it starts on the task by itself. Job %s.",
					in.Title, describeChoices(ai, in.Model, in.Effort, autonomous, notify, in.ClaudeAccount)+windowChoice(in.ContextWindow), job.ID), nil
			},
		},
		{
			Name: "tell_agent",
			Description: "Send a message to one of this project's agents: a correction, more detail, or the next thing to do. " +
				"An agent that is still working gets it mid-work and decides for itself whether to change course now " +
				"or finish first, so there is no need to wait for it or to stop it.",
			Schema: object([]string{"agent", "message"}, map[string]any{
				"agent":   str("its name, like agent-03"),
				"message": str("what to tell it, in a line or two"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Agent, Message string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				it, err := c.TellAgent(ctx, in.Agent, in.Message)
				if err != nil {
					return "", err
				}
				if it.Kind == "aside" {
					return fmt.Sprintf("Told %s, mid-work; it decides when to act on it.", in.Agent), nil
				}
				return fmt.Sprintf("Told %s; it is working on it.", in.Agent), nil
			},
		},
		{
			Name:        "read_agent",
			Description: "Read what one of this project's agents has been doing: its conversation, newest last.",
			Schema: object([]string{"agent"}, map[string]any{
				"agent": str("its name, like agent-03"),
				"last":  map[string]any{"type": "integer", "description": "how many entries to read; 20 by default"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Agent string
					Last  int
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if in.Last <= 0 {
					in.Last = 20
				}
				thread, err := c.AgentChat(ctx, in.Agent)
				if err != nil {
					return "", err
				}
				return describeThread(in.Agent, thread, in.Last), nil
			},
		},
		{
			Name: "agent_diff",
			Description: "What an agent has changed, against the commit it started from. A large change comes back " +
				"as the list of files and the start of the diff: ask for one file or directory with path to read the rest.",
			Schema: object([]string{"agent"}, map[string]any{
				"agent": str("its name, like agent-03"),
				"stat":  map[string]any{"type": "boolean", "description": "just the summary, not the whole diff"},
				"path":  str("only this file or directory, relative to the repository"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Agent string
					Stat  bool
					Path  string
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				var paths []string
				if in.Path != "" {
					paths = []string{in.Path}
				}
				diff, err := c.AgentDiff(ctx, in.Agent, in.Stat, paths...)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(diff) == "" {
					if in.Path != "" {
						return fmt.Sprintf("%s has changed nothing in %s.", in.Agent, in.Path), nil
					}
					return in.Agent + " has changed nothing.", nil
				}
				if in.Stat || len(diff) <= maxChatDiff {
					return diff, nil
				}
				stat, err := c.AgentDiff(ctx, in.Agent, true, paths...)
				if err != nil {
					stat = ""
				}
				return clipDiff(in.Agent, diff, stat), nil
			},
		},
		{
			Name: "run_in_agent",
			Description: "Run a short shell command in one of the project's agents' machines, in its worktree, " +
				"with the project's toolchain and the network: a quick check, a git command, a look at a service. " +
				"You get the exit code and only the end of the output, so trim it yourself (| tail, | grep, -q): " +
				"all of it stays in this conversation. It has no input and stops at the timeout. Anything longer " +
				"than a few commands, an install, a build or a run of the app is a task for an agent, not this.",
			Schema: object([]string{"agent", "command"}, map[string]any{
				"agent":           str("its name, like agent-03"),
				"command":         str("a bash command line, run as the agent's user"),
				"timeout_seconds": map[string]any{"type": "integer", "description": "at most 600; 60 when left out"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Agent          string
					Command        string
					TimeoutSeconds int `json:"timeout_seconds"`
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				res, err := c.RunInAgent(ctx, in.Agent, api.LeadRunRequest{Command: in.Command, Timeout: in.TimeoutSeconds})
				if err != nil {
					return "", err
				}
				return describeRun(res), nil
			},
		},
		{
			Name: "copy_between_agents",
			Description: "Copy a file or directory from one agent's machine into another's, as it is there, " +
				"committed or not: a fixture one agent made that another needs, a build output, a log. " +
				"Up to 256 MiB. Work that should last goes through git instead.",
			Schema: object([]string{"from", "path", "to"}, map[string]any{
				"from": str("the agent to copy from"),
				"path": str("the file or directory, relative to its worktree or absolute"),
				"to":   str("the agent to copy to"),
				"into": str("the directory to put it in, relative to that agent's worktree or absolute; the same place as path when left out"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ From, Path, To, Into string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				res, err := c.CopyBetweenAgents(ctx, api.LeadCopyRequest{From: in.From, Path: in.Path, To: in.To, Into: in.Into})
				if err != nil {
					return "", err
				}
				into := in.Into
				if into == "" {
					into = "the same place"
				}
				return fmt.Sprintf("Copied %s from %s to %s, into %s (%s).", in.Path, in.From, in.To, into, sizeOf(res.Bytes)), nil
			},
		},
		{
			Name: "retire_agent",
			Description: "Free what an agent is holding once it has finished. Its work stays on its " +
				"branch whichever way you retire it: stop shuts the machine down and it comes back in " +
				"seconds, destroy removes the machine and the worktree but keeps its media, findable in " +
				"the project's media view. An agent with uncommitted work is left alone. The next task " +
				"is a new agent, not this one.",
			Schema: object([]string{"agent"}, map[string]any{
				"agent": str("its name, like agent-03"),
				"how":   str("stop (the default), pause or destroy"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Agent, How string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				result, err := c.RetireProject(ctx, api.RetireRequest{How: in.How, Agents: []string{in.Agent}})
				if err != nil {
					return "", err
				}
				for _, who := range result.Skipped {
					return fmt.Sprintf("%s was left alone: %s", who.Name, who.Reason), nil
				}
				for _, who := range result.Retired {
					return fmt.Sprintf("%s is retired (%s). Its work stays on %s.", who.Name, result.How, who.Branch), nil
				}
				return "Nothing happened: " + in.Agent + " may not exist.", nil
			},
		},
		{
			Name: "read_notes",
			Description: "This project's notes: what every one of its agents is told before it starts, " +
				"written by the user and by you. They are in your own brief too, as they were when this " +
				"conversation began; read them when you want them as they are now, or before adding to them.",
			Run: func(json.RawMessage) (string, error) {
				n, err := c.ProjectNotes(ctx)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(n.Text) == "" {
					return "This project has no notes yet. append_note starts them.", nil
				}
				return n.Text, nil
			},
		},
		{
			Name: "append_note",
			Description: "Write down a standing rule every agent of this project needs before it starts, so it " +
				"reaches them in their brief instead of being explained one at a time. What belongs here: a " +
				"convention the project keeps, a command that isn't in the README, a decision the user made. What " +
				"does not: what an agent is doing or how a task went, anything the repository already says, and " +
				"anything that will be false next week. Keep them few — every note is pasted into every agent's " +
				"brief whether or not it is relevant, so a gotcha with an error message, a version or a filename " +
				"in it goes to remember instead, where search_memory puts it in front of whoever hits it. One " +
				"short entry at a time, added dated under a heading of yours. Tell the user which of the two you " +
				"used, and why.",
			Schema: object([]string{"text"}, map[string]any{
				"text": str("the fact every agent should know, in a sentence"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Text string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Text) == "" {
					return "", errors.New("a note needs some text")
				}
				if _, err := c.AppendProjectNote(ctx, in.Text); err != nil {
					return "", err
				}
				return "Written down: new agents get it in their brief, running ones from their next session.", nil
			},
		},
		{
			Name: "edit_note",
			Description: "Change what one of this project's notes says, when the user asks you to. The notes are " +
				"their standing brief, not yours to tidy: correct one because you were told to, not because you " +
				"noticed. Name it by quoting it — enough of the entry to pick out one — and say what it should " +
				"say instead. A quote that matches nothing, or matches more than one note, changes nothing and " +
				"tells you what it matched, so quote more of the one you mean rather than guessing. The entry " +
				"keeps the date it was first written under. You can reach what the user wrote themselves as well " +
				"as your own entries; the answer says which it was, and a change to theirs is theirs to be told " +
				"about, in the words it reports back.",
			Schema: object([]string{"match", "text"}, map[string]any{
				"match": str("a quote of the note to change, as it reads now"),
				"text":  str("what that note should say instead, in a sentence"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Match, Text string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Match) == "" {
					return "", errors.New("name the note to change by quoting it")
				}
				if strings.TrimSpace(in.Text) == "" {
					return "", errors.New("an edited note needs some text: remove_note is how one goes")
				}
				change, err := c.EditProjectNote(ctx, in.Match, in.Text)
				if err != nil {
					return "", err
				}
				return describeNoteChange(change, "Edited"), nil
			},
		},
		{
			Name: "remove_note",
			Description: "Take one of this project's notes out, when the user asks you to — one that is wrong, " +
				"or that they want gone. Name it by quoting it, the same way as edit_note, and the same refusals " +
				"apply: a quote that doesn't pick out exactly one note removes nothing and says what it matched. " +
				"This is not how you correct a note — edit_note keeps its place and its date. It can remove text " +
				"the user wrote themselves, so say what went, in their words, not just that it is done.",
			Schema: object([]string{"match"}, map[string]any{
				"match": str("a quote of the note to remove, as it reads now"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Match string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Match) == "" {
					return "", errors.New("name the note to remove by quoting it")
				}
				change, err := c.RemoveProjectNote(ctx, in.Match)
				if err != nil {
					return "", err
				}
				return describeNoteChange(change, "Removed"), nil
			},
		},
		{
			Name: "search_memory",
			Description: "Search what this project remembers: the memories you and the user have written down, " +
				"the raw events its agents recorded, and the reports they filed as they finished. Search it before " +
				"you answer from what you assume, and before you brief an agent — somebody has probably hit this " +
				"already. Exact words work: a filename, a port, a package name, an error string. It never returns " +
				"a memory that a later one has replaced.",
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
				results, err := c.LeadMemory().Search(ctx, in.Query, in.Limit)
				if err != nil {
					return "", err
				}
				return describeSearch(in.Query, results), nil
			},
		},
		{
			Name: "remember",
			Description: "Write down something this project should still know months from now, so the next agent " +
				"is told it instead of working it out again. What belongs here: how the project is put together, a " +
				"convention it keeps, a decision and why it was made, a gotcha somebody lost an hour to, a known " +
				"problem nobody has fixed. What does not: what an agent is doing right now (that is " +
				"update_working_memory), and anything that will be false next week. One fact at a time, with a title " +
				"somebody would recognise it by. When this corrects something already remembered, pass supersedes — " +
				"the old memory stops coming back from searches, and stays readable so the change is followable. " +
				"This is not append_note: notes are the short standing brief every agent is handed, and memory is the " +
				"much larger store they search.",
			Schema: object([]string{"title"}, map[string]any{
				"title":   str("one line somebody would recognise this by, like \"The API listens on port 7777\""),
				"content": str("the fact in full: what it is, and what somebody should do about it"),
				"kind": choiceOf("what sort of thing this is. \"project\" is a long-lived fact about the project "+
					"(architecture, a convention, a configuration value, a constraint) and the default. \"decision\" is a "+
					"choice and its reason. \"discovery\" is something found out the hard way. \"issue\" is a known problem "+
					"nobody has fixed. \"episodic\" is something that happened, kept because it explains later things.",
					"project", "decision", "discovery", "issue", "episodic"),
				"importance": map[string]any{"type": "integer", "description": "1 to 5. 3 is ordinary and the default; " +
					"5 is for what nobody should work on this project without knowing. It decides what survives when " +
					"an agent's brief can't hold everything, so don't spend 5 freely."},
				"supersedes": str("the id of the memory this replaces, from search_memory"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Title, Content, Kind, Supersedes string
					Importance                       int
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Title) == "" {
					return "", errors.New("a memory needs a title: one line somebody would recognise it by")
				}
				m, err := c.LeadMemory().AddMemory(ctx, api.AddMemoryRequest{
					Kind: in.Kind, Title: in.Title, Content: in.Content,
					Importance: in.Importance, SupersedesID: in.Supersedes,
				})
				if err != nil {
					return "", err
				}
				if in.Supersedes != "" {
					return fmt.Sprintf("Remembered as %s, replacing %s, which no longer comes back from a search.", m.ID, in.Supersedes), nil
				}
				return fmt.Sprintf("Remembered as %s. search_memory finds it, for you and for every agent of this project.", m.ID), nil
			},
		},
		{
			Name: "resolve_memory",
			Description: "Close a memory that is simply over, with nothing to put in its place: the bug was " +
				"fixed, the flaky test was deleted, the thing nobody had got to stopped mattering. It stops " +
				"coming back from searches and stays readable, exactly as a superseded memory does. Use this " +
				"rather than remember+supersedes when there is no new fact to write down — inventing a memory " +
				"of a fix nobody made is worse than the stale issue was. Mostly for issues; it means the same " +
				"thing for any kind.",
			Schema: object([]string{"id"}, map[string]any{
				"id":  str("the id of the memory to close, from search_memory"),
				"why": str("what closed it, in a line: the pull request, the change, or that it stopped mattering"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ ID, Why string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.ID) == "" {
					return "", errors.New("say which memory to close, by its id from search_memory")
				}
				m, err := c.LeadMemory().ResolveMemory(ctx, in.ID, in.Why)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Closed %s (%q). It no longer comes back from a search, for you or for any agent "+
					"of this project, and is still readable by id.", m.ID, m.Title), nil
			},
		},
		{
			Name: "update_working_memory",
			Description: "Keep the one short note of what this project is doing right now: the goal, the task in " +
				"hand, who is on it, what is in the way. It is what you and every agent read first, so it has to be " +
				"true today rather than complete. Set only the fields that changed; the rest stay as they were, and " +
				"an empty value clears one. Anything that will still matter in a month belongs in remember instead.",
			Schema: object(nil, map[string]any{
				"goal":         str("what the project is trying to achieve at the moment"),
				"current_task": str("what is being worked on now"),
				"active_agents": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "the agents on it, by name, like [\"agent-01\", \"agent-03\"]"},
				"blockers": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "what is in the way, one line each"},
				"notes": str("anything else that matters today and won't next month"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Goal         *string   `json:"goal"`
					CurrentTask  *string   `json:"current_task"`
					ActiveAgents *[]string `json:"active_agents"`
					Blockers     *[]string `json:"blockers"`
					Notes        *string   `json:"notes"`
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				patch := api.WorkingMemoryPatch{Goal: in.Goal, CurrentTask: in.CurrentTask,
					ActiveAgents: in.ActiveAgents, Blockers: in.Blockers, Notes: in.Notes}
				w, err := c.LeadMemory().SetWorkingMemory(ctx, patch)
				if err != nil {
					return "", err
				}
				return "Updated. What the project is doing now:\n\n" + describeWorking(w), nil
			},
		},
		{
			Name: "project_state",
			Description: "Where this project stands: what it is doing now, the plan — every task that is open, who " +
				"is on it and what it is waiting on — the problems nobody has fixed, and what its agents reported " +
				"as they finished. Short on purpose — read it at the start of a conversation, before deciding what " +
				"to do next, and use search_memory when you need the detail behind a line of it.",
			Run: func(json.RawMessage) (string, error) {
				return projectState(ctx, c), nil
			},
		},
		{
			Name: "add_task",
			Description: "Write a piece of work down as a task, so the project has a plan and not only a list of " +
				"agents. A task is what is to be done and who is doing it; it is state, not memory — it changes as " +
				"the work moves and is closed when the work is over, which is what remember is not for. Give it to " +
				"an agent by name when you know who is on it, put it under a parent when it is part of something " +
				"larger, and name what it is blocked on in depends_on. An agent you create with a task already has " +
				"one written down for it, so add_task is for the work you are planning rather than handing over now.",
			Schema: object([]string{"goal"}, map[string]any{
				"goal":   str("what is to be done, in one line somebody would recognise it by"),
				"detail": str("the task in full: what it covers, what it must not touch, how you will know it worked"),
				"agent":  str("the agent doing it, by name, when one is. Leave it out for work nobody is on yet"),
				"parent": str("the id of the task this is part of, from project_state"),
				"status": choiceOf("where it stands. \"open\" is written down and not started, and the default. "+
					"\"active\" is being worked on now. \"blocked\" is waiting on something.",
					"open", "active", "blocked"),
				"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "the ids of the tasks this one is waiting on. A cycle is refused, and the task is still written down"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Goal, Detail, Agent, Parent, Status string
					DependsOn                           []string `json:"depends_on"`
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Goal) == "" {
					return "", errors.New("a task needs a goal: what is to be done, in a line")
				}
				t, err := c.LeadMemory().AddTask(ctx, api.AddTaskRequest{
					Goal: in.Goal, Detail: in.Detail, Agent: in.Agent,
					ParentID: in.Parent, Status: in.Status, DependsOn: in.DependsOn,
				})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Task %s (%s). It is in project_state, in every agent's brief, and in %s's own tools.",
					t.ID, t.Status, taskOwner(t)), nil
			},
		},
		{
			Name: "link_tasks",
			Description: "Say that one task can't be finished until another is, or take that back with unlink. This " +
				"is the blocking edge, not the subtask one: a task can be waiting on several things at once, and " +
				"none of them contains it — use add_task's parent for that. A link that would make a cycle is " +
				"refused, and the answer says which edge closed it.",
			Schema: object([]string{"task", "depends_on"}, map[string]any{
				"task":       str("the id of the task that is waiting"),
				"depends_on": str("the id of the task it is waiting on"),
				"unlink": map[string]any{"type": "boolean",
					"description": "take the edge back out instead, because the dependency turned out not to exist"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Task      string
					DependsOn string `json:"depends_on"`
					Unlink    bool
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				m := c.LeadMemory()
				if in.Unlink {
					if err := m.UnlinkTasks(ctx, in.Task, in.DependsOn); err != nil {
						return "", err
					}
					return fmt.Sprintf("%s is no longer waiting on %s.", in.Task, in.DependsOn), nil
				}
				if err := m.LinkTasks(ctx, in.Task, in.DependsOn); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s is waiting on %s. The agent on %s sees it in its own tools, and project_state "+
					"shows the edge.", in.Task, in.DependsOn, in.Task), nil
			},
		},
		{
			Name: "set_task_status",
			Description: "Move a task, which is mostly closing one: \"done\" when the work is finished and " +
				"\"abandoned\" when it is over without being finished — it stopped mattering, or it was tried and " +
				"didn't work. Closing a task is how the plan stays readable; a plan where nothing is ever closed is " +
				"a list. An agent's own task is closed for you when it finishes and reports, so this is for the work " +
				"you decided about rather than the work an agent reported on.",
			Schema: object([]string{"task", "status"}, map[string]any{
				"task": str("the id of the task, from project_state"),
				"status": choiceOf("where it now stands. \"done\" is finished. \"abandoned\" is over without being "+
					"finished. \"blocked\" is waiting on something. \"active\" is being worked on now. \"open\" is "+
					"written down and not started, which also reopens a task closed too early.",
					"open", "active", "blocked", "done", "abandoned"),
				"detail": str("what to record about it now, replacing what was there: why it was abandoned, what is left"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Task, Status string
					Detail       *string
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Task) == "" || strings.TrimSpace(in.Status) == "" {
					return "", errors.New("say which task, by its id from project_state, and what it now is")
				}
				t, err := c.LeadMemory().UpdateTask(ctx, in.Task, api.UpdateTaskRequest{Status: &in.Status, Detail: in.Detail})
				if err != nil {
					return "", err
				}
				if len(t.Blocks) > 0 && !openTask(t.Status) {
					return fmt.Sprintf("%s is %s. %d task(s) were waiting on it: %s — they can start now.",
						t.ID, t.Status, len(t.Blocks), strings.Join(t.Blocks, ", ")), nil
				}
				return fmt.Sprintf("%s is %s.", t.ID, t.Status), nil
			},
		},
		{
			Name: "list_questions",
			Description: "The questions this project's agents are waiting on. An agent asking one is " +
				"blocked until it is answered, so deal with these first.",
			Run: func(json.RawMessage) (string, error) {
				questions, err := c.ProjectQuestions(ctx)
				if err != nil {
					return "", err
				}
				return describeQuestions(questions), nil
			},
		},
		{
			Name: "answer_question",
			Description: "Answer a question an agent asked. Answer it yourself whenever you can: you " +
				"have read the project and the user has not. The agent is waiting.",
			Schema: object([]string{"id", "answer"}, map[string]any{
				"id":     str("the question's id"),
				"answer": str("what the agent should do, and why"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ ID, Answer string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if _, err := c.LeadAnswerQuestion(ctx, in.ID, in.Answer); err != nil {
					return "", err
				}
				return "Answered; the agent is carrying on.", nil
			},
		},
		{
			Name: "escalate_question",
			Description: "Pass a question to the user, because you can't answer it: it is their call " +
				"about what to build, or it needs something only they know. Say what you would have " +
				"answered and why you can't, so they can decide quickly.",
			Schema: object([]string{"id", "why"}, map[string]any{
				"id":  str("the question's id"),
				"why": str("why this is the user's call, and what you would suggest"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ ID, Why string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if _, err := c.LeadEscalateQuestion(ctx, in.ID, in.Why); err != nil {
					return "", err
				}
				return "Passed to the user; say in your reply what you need from them. The agent waits.", nil
			},
		},
	}
}

func describeFleet(fleet api.Fleet) string {
	if len(fleet.Agents) == 0 && len(fleet.Creating) == 0 {
		return "This project has no agents yet. Create one with create_agent."
	}
	var b strings.Builder
	for range fleet.Creating {
		b.WriteString("- (being created)\n")
	}
	for _, f := range fleet.Agents {
		fmt.Fprintf(&b, "- %s", f.Name)
		if f.Title != "" {
			fmt.Fprintf(&b, " — %s", f.Title)
		}
		fmt.Fprintf(&b, "\n  branch: %s, machine: %s", dash(f.Branch), f.State)
		if doing := chatDoing(f.Chat); doing != "" {
			fmt.Fprintf(&b, ", %s", doing)
		}
		if f.AI == "claude" && f.ClaudeAccount != "" {
			fmt.Fprintf(&b, ", account: %s", f.ClaudeAccount)
		}
		if f.Idle {
			b.WriteString(" (finished, still holding its machine)")
		}
		fmt.Fprintf(&b, "\n  changes: %s", changes(f.Changes))
		if f.Media > 0 {
			fmt.Fprintf(&b, ", %d thing(s) shown", f.Media)
		}
		if f.PR != nil {
			fmt.Fprintf(&b, "\n  pull request: %s", pullRequest(f.PR))
		}
		b.WriteString("\n")
	}
	if fleet.Idle > 0 {
		fmt.Fprintf(&b, "\n%d agent(s) finished and are holding a machine. retire_agent frees one; its work stays on its branch.\n", fleet.Idle)
	}
	return b.String()
}

// describeAccounts names this machine's Claude Code accounts, so a chat can
// choose one with create_agent's claude_account rather than leaving every
// agent on whichever one it started with. No token appears here — only the
// name, what it is to this machine and project, and its latest usage reading
// from D85, said the same honest way agentbox tokens says it: a window past
// its reset is said to have reset, not shown with a number that no longer
// describes it.
func describeAccounts(accounts []api.LeadAccount) string {
	if len(accounts) <= 1 {
		return "This project has only one Claude Code account, so there is nothing to spread across; " +
			"create_agent's claude_account can be left out."
	}
	now := time.Now()
	var b strings.Builder
	b.WriteString("The Claude Code accounts this project may use. Prefer the one with the most headroom left when you " +
		"create an agent, and avoid one close to its 5-hour limit.\n")
	for _, a := range accounts {
		fmt.Fprintf(&b, "- %s", a.Name)
		var tags []string
		if a.Default {
			tags = append(tags, "machine default")
		}
		if a.Project {
			tags = append(tags, "this project's account")
		}
		if len(tags) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(tags, ", "))
		}
		fmt.Fprintf(&b, " — %d running agent(s) of this project", a.Agents)
		if a.Limit == nil {
			b.WriteString(", no usage reading yet\n")
			continue
		}
		parts := make([]string, 0, len(a.Limit.Windows))
		for _, win := range a.Limit.Windows {
			if !win.ResetsAt.IsZero() && now.After(win.ResetsAt) {
				parts = append(parts, fmt.Sprintf("%s reset since the reading", win.Label))
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %.0f%% used (resets %s)", win.Label, win.Utilization*100, win.ResetsAt.Local().Format("Jan 2 15:04")))
		}
		status := ""
		if a.Limit.Status != "" && a.Limit.Status != "allowed" {
			status = " · " + strings.ReplaceAll(a.Limit.Status, "_", " ")
		}
		fmt.Fprintf(&b, ", %s%s (as of %s)\n", strings.Join(parts, ", "), status, a.Limit.At.Local().Format("Jan 2 15:04"))
	}
	return b.String()
}

// describeSecrets names what the agents have, and where each one applies. No
// value appears here, because none is fetched: the route behind this carries
// names (D52).
func describeSecrets(secrets []api.Secret) string {
	if len(secrets) == 0 {
		return "The agents of this project have no secrets. If one needs an API key, ask the user to add it " +
			"on the project's Secrets tab (or with agentbox secrets set), and it becomes an environment " +
			"variable in the agents."
	}
	var b strings.Builder
	b.WriteString("Environment variables the agents have. You can name them, not read them.\n")
	for _, s := range secrets {
		where := "every agent of this project"
		if s.Scope == "agent" {
			where = s.Agent + " only"
		}
		fmt.Fprintf(&b, "- $%s — %s\n", s.Name, where)
	}
	return b.String()
}

// maxChatDiff is the most of a diff agent_diff hands the chat whole (D87).
// Whatever a tool answers stays in the chat's context for the rest of its
// session and is sent again with every step after it, so a change of hundreds
// of files is answered with what it touched and where to look instead.
const maxChatDiff = 24 << 10

// describeRun is a command's result as the chat reads it: how it ended, then
// what it printed, saying so when that is only the end of it.
func describeRun(res api.LeadRunResult) string {
	var b strings.Builder
	switch {
	case res.TimedOut:
		b.WriteString("It ran out of time and was stopped.")
	case res.ExitCode == 0:
		b.WriteString("Exit code 0.")
	default:
		fmt.Fprintf(&b, "Exit code %d.", res.ExitCode)
	}
	out := strings.TrimRight(res.Output, "\n")
	switch {
	case out == "":
		b.WriteString(" It printed nothing.")
	case int64(len(res.Output)) < res.Bytes:
		fmt.Fprintf(&b, " It printed %s; this is the end of it:\n\n%s", sizeOf(res.Bytes), out)
	default:
		b.WriteString("\n\n" + out)
	}
	return b.String()
}

func sizeOf(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// clipDiff is a diff too long to hand over whole: the files it touches, and as
// much of the diff itself as fits, cut at a line.
func clipDiff(agent, diff, stat string) string {
	head := diff[:maxChatDiff*2/3]
	if i := strings.LastIndexByte(head, '\n'); i > 0 {
		head = head[:i+1]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s's change is %d KiB of diff, too much to read here whole.", agent, len(diff)>>10)
	if stat = strings.TrimSpace(stat); stat != "" {
		fmt.Fprintf(&b, " What it touches:\n\n%s\n", stat)
	}
	fmt.Fprintf(&b, "\nThe start of it:\n\n%s\n[cut here: read one file or directory with agent_diff(%q, path)]", head, agent)
	return b.String()
}

// maxEntry is the most of one entry read_agent hands the chat (D87): a task
// the chat wrote itself, or a summary it has already been sent, isn't worth
// reading whole a second time.
const maxEntry = 2000

// describeThread is an agent's conversation as the chat reads it: what it was
// told, what it said and what it ran. A subagent is one line — who, what it
// was asked, how it ended — and what it did is left out: its words are its
// report to the agent, not the agent's (D86).
func describeThread(name string, thread api.ChatThread, last int) string {
	var items []api.ChatItem
	for _, it := range thread.Items {
		if it.Parent == "" {
			items = append(items, it)
		}
	}
	if len(items) > last {
		items = items[len(items)-last:]
	}
	if len(items) == 0 {
		return name + " has said nothing yet."
	}
	var b strings.Builder
	for _, it := range items {
		switch it.Kind {
		case "user":
			fmt.Fprintf(&b, "\n[it was told] %s\n", clip(it.Text, maxEntry))
		case "assistant":
			fmt.Fprintf(&b, "[it said] %s\n", clip(it.Text, maxEntry))
		case "subagent":
			if sa := it.Subagent; sa != nil {
				fmt.Fprintf(&b, "[it started a subagent] %s: %s (%s)\n", sa.Name, oneLine(sa.Task), sa.State)
			}
		case "tool":
			if it.Tool != nil {
				fmt.Fprintf(&b, "[it ran] %s (%s)\n", it.Tool.Title, it.Tool.Status)
			}
		case "error":
			fmt.Fprintf(&b, "[error] %s\n", it.Text)
		case "notice":
			fmt.Fprintf(&b, "[note] %s\n", it.Text)
		}
	}
	return b.String()
}

func describeQuestions(questions []api.Question) string {
	if len(questions) == 0 {
		return "No agent is waiting on you."
	}
	var b strings.Builder
	for _, q := range questions {
		fmt.Fprintf(&b, "- id %s, from %s (%s)\n  %s\n", q.ID, q.Agent, q.Status, q.Question)
		if q.Context != "" {
			fmt.Fprintf(&b, "  what it was doing: %s\n", q.Context)
		}
	}
	return b.String()
}

// describeNoteChange is what the chat is told after an edit or a removal: the
// entry as it was and as it now is, in full, so the wrong note being changed
// is visible in the answer rather than found later in a brief. A change to
// something the user wrote themselves says so — the chat may make one when it
// is asked to, but it may not make one quietly.
func describeNoteChange(change api.NoteChange, what string) string {
	var b strings.Builder
	b.WriteString(what)
	if change.Section != "" {
		fmt.Fprintf(&b, ", under %q", change.Section)
	}
	fmt.Fprintf(&b, ".\nwas: %s\n", change.Was)
	if change.Now != "" {
		fmt.Fprintf(&b, "now: %s\n", change.Now)
	}
	if !change.FromLead {
		b.WriteString("That was the user's own text, not an entry of yours: tell them exactly what you changed.\n")
	}
	b.WriteString("Every agent made from now on is given the notes as they now are, and the ones that are " +
		"running have them from their next session.")
	return b.String()
}

// The memory tools' answers. A tool's reply is read by a model and by nobody
// else, so each of these is the shortest thing that still carries the ids it
// would need to follow something up.

func describeSearch(query string, results api.MemorySearchResults) string {
	if len(results.Memories) == 0 && len(results.Events) == 0 && len(results.Reports) == 0 {
		return fmt.Sprintf("Nothing remembered about %q yet. If you work it out, remember it: the next agent "+
			"searches the same thing.", query)
	}
	var b strings.Builder
	if len(results.Memories) > 0 {
		b.WriteString("Remembered:\n")
		for _, m := range results.Memories {
			fmt.Fprintf(&b, "- [%s] %s (%s, importance %d)\n", m.ID, m.Title, m.Kind, m.Importance)
			if m.Content != "" {
				fmt.Fprintf(&b, "  %s\n", oneLine(m.Content))
			}
		}
	}
	if len(results.Reports) > 0 {
		b.WriteString("\nAgents reported:\n")
		for _, r := range results.Reports {
			fmt.Fprintf(&b, "- %s (%s): %s\n", r.Agent, r.Status, oneLine(r.Summary))
		}
	}
	if len(results.Events) > 0 {
		b.WriteString("\nWhat happened:\n")
		for _, e := range results.Events {
			fmt.Fprintf(&b, "- %s %s", e.At.Format(time.DateTime), e.Type)
			if e.Agent != "" {
				fmt.Fprintf(&b, " (%s)", e.Agent)
			}
			fmt.Fprintf(&b, ": %s\n", oneLine(string(e.Payload)))
		}
	}
	return b.String()
}

func describeWorking(w api.WorkingMemory) string {
	var b strings.Builder
	if w.Goal != "" {
		fmt.Fprintf(&b, "Goal: %s\n", w.Goal)
	}
	if w.CurrentTask != "" {
		fmt.Fprintf(&b, "Now: %s\n", w.CurrentTask)
	}
	if len(w.ActiveAgents) > 0 {
		fmt.Fprintf(&b, "On it: %s\n", strings.Join(w.ActiveAgents, ", "))
	}
	for _, blocker := range w.Blockers {
		fmt.Fprintf(&b, "Blocked: %s\n", blocker)
	}
	if w.Notes != "" {
		fmt.Fprintf(&b, "Notes: %s\n", w.Notes)
	}
	if b.Len() == 0 {
		return "Nothing recorded about what this project is doing right now.\n"
	}
	return b.String()
}

// projectState is working memory, the plan, the issues nobody has fixed, and
// the last few reports: the least a chat needs before it decides what happens
// next. Each part fails quietly on its own, because a state that is missing
// one section is worth more than an error.
func projectState(ctx context.Context, c *api.Client) string {
	m := c.LeadMemory()
	var b strings.Builder
	if w, err := m.WorkingMemory(ctx); err == nil {
		b.WriteString(describeWorking(w))
	}
	if tasks, err := m.Tasks(ctx, api.TaskQuery{OpenOnly: true, Limit: projectStateTasks}); err == nil && len(tasks) > 0 {
		b.WriteString("\nThe plan — what is still open, what is in the way first:\n")
		b.WriteString(describeTasks(tasks))
		if len(tasks) == projectStateTasks {
			b.WriteString("(the plan is longer than this; it is cut at what is most in the way)\n")
		}
	}
	if issues, err := m.Memories(ctx, api.MemoryKindIssue); err == nil && len(issues) > 0 {
		b.WriteString("\nKnown problems nobody has fixed:\n")
		for _, issue := range issues {
			fmt.Fprintf(&b, "- [%s] %s\n", issue.ID, issue.Title)
		}
	}
	if reports, err := m.Reports(ctx, ""); err == nil && len(reports) > 0 {
		b.WriteString("\nWhat the agents last reported:\n")
		for _, r := range reports[:min(len(reports), 5)] {
			fmt.Fprintf(&b, "- %s (%s): %s\n", r.Agent, r.Status, oneLine(r.Summary))
			for _, issue := range r.RemainingIssues {
				fmt.Fprintf(&b, "  left undone: %s\n", issue)
			}
		}
	}
	b.WriteString("\nsearch_memory has the detail behind any of this; remember writes something new down, " +
		"and add_task writes work down.\n")
	return b.String()
}

// projectStateTasks is how much of a plan project_state carries. A project
// with more open tasks than this has a plan its chat should be closing, and
// the ones that come first are the ones in the way.
const projectStateTasks = 20

// describeTasks renders a plan for a model: what each task is, who is on it,
// and what it is waiting on, by id. The ids are there because the next thing
// anybody does with a task is name it — to link it, to close it, to hand it
// over — and a plan whose rows can't be named is a plan nobody can change.
//
// It is one flat list rather than a tree: the graph has two kinds of edge and
// a nested rendering can only show one of them, so both are said in words.
func describeTasks(tasks []api.Task) string {
	goals := make(map[string]string, len(tasks))
	for _, t := range tasks {
		goals[t.ID] = t.Goal
	}
	var b strings.Builder
	for _, t := range tasks {
		fmt.Fprintf(&b, "- [%s] %s (%s", t.ID, t.Goal, t.Status)
		if t.Agent != "" {
			fmt.Fprintf(&b, ", %s", t.Agent)
		}
		b.WriteString(")\n")
		if t.ParentID != "" {
			fmt.Fprintf(&b, "  part of: %s\n", taskName(t.ParentID, goals))
		}
		for _, on := range t.DependsOn {
			fmt.Fprintf(&b, "  waiting on: %s\n", taskName(on, goals))
		}
		for _, waiting := range t.Blocks {
			fmt.Fprintf(&b, "  holding up: %s\n", taskName(waiting, goals))
		}
		if t.Detail != "" {
			fmt.Fprintf(&b, "  %s\n", oneLine(t.Detail))
		}
	}
	return b.String()
}

// taskName is a task by goal when it is one of the ones in hand, and by id
// when it isn't — a closed task still shows up as something's blocker.
func taskName(id string, goals map[string]string) string {
	if goal := goals[id]; goal != "" {
		return goal + " (" + id + ")"
	}
	return id
}

// taskOwner is who a task belongs to, for a tool's answer.
func taskOwner(t api.Task) string {
	if t.Agent == "" {
		return "whichever agent picks it up"
	}
	return t.Agent
}

func openTask(status string) bool {
	return status != api.TaskDone && status != api.TaskAbandoned
}

// oneLine folds text onto one line and cuts it, so a list of results stays a
// list rather than becoming the thing it is listing.
// clip cuts text to at most n runes, saying it did.
func clip(text string, n int) string {
	if runes := []rune(text); len(runes) > n {
		return string(runes[:n]) + " […]"
	}
	return text
}

func oneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	// Cut on runes, so a summary that ends in anything but ASCII isn't cut
	// in the middle of a character.
	if runes := []rune(text); len(runes) > 200 {
		return string(runes[:200]) + "…"
	}
	return text
}
