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
	"agentbox/internal/state"
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
			tools := projectTools
			// The Home chat's socket reaches every project, and its tools
			// name the one they act on (homeTools).
			if os.Getenv("AGENTBOX_CHAT") == "home" {
				tools = homeTools
			}
			srv := &mcp.Server{Name: "agentbox", Version: version, Tools: tools(cmd.Context(), c)}
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
func chatSettingParams(ctx context.Context, c *api.Client) (params map[string]any, leadPicksModel bool, ready readyTools) {
	var models, efforts, openCodeModels []string
	var openCode, cursorReady bool
	var cursorModels []api.ChatOptionChoice
	// The defaults an agent created with none of these gets, named in the
	// descriptions below: Settings → Models, with AgentBox's own defaults
	// under them. The lead's own defaults are another section of Settings and
	// never reach its agents.
	defaultModel, defaultWindow := state.DefaultClaudeModel, "200k"
	var enforced bool
	if settings, err := c.ProjectSettings(ctx); err == nil {
		enforced = settings.EnforceAgentDefaults
		if settings.DefaultClaudeModel != "" {
			defaultModel = settings.DefaultClaudeModel
		}
		if n, err := state.ParseContextWindow(settings.DefaultAgentContextWindow); err == nil && n > 0 {
			defaultWindow = strings.ToLower(state.FormatContextWindow(n))
		}
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
		cursorReady, cursorModels = settings.CursorReady, settings.CursorModelChoices
	}
	// What the project asks of this parameter. The lead's brief says the same
	// thing at greater length; this is what a model reads at the moment it
	// fills the tool call in.
	var auto bool
	defaultFrom := "the model new agents start on in AgentBox's Settings → Models"
	if p, err := c.ProjectSelf(ctx); err == nil {
		auto = p.AgentModel == api.AgentModelAuto
		if p.AgentModel != "" && !auto {
			defaultModel, defaultFrom = p.AgentModel, "the model this project's settings name for its agents"
		}
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
		"Leave this out for " + defaultModel + ", " + defaultFrom + "."
	if auto {
		model = "the model this agent runs on: " + named + ". This project asks you to choose one for every agent you create, " +
			"from how hard the task is: the cheapest model on that list that can do a mechanical or small job (a rename, a config " +
			"change, one test, a doc); the ordinary strong one (opus) for feature work; and that one at a higher effort for hard " +
			"design work, or a bug whose cause nobody has found. Say which you chose, and why, in one line as you create the agent. " +
			"Never choose Fable unless the user has asked for it, for this agent or for this project. A model Claude Code won't " +
			"accept is refused when the agent starts, and the agent says so in its own chat, so don't guess one. Leaving this out " +
			"doesn't fail: the agent falls back to " + defaultModel + ", the model new agents start on in AgentBox's Settings → Models."
	}
	// Settings → Models either fixes the model and window (enforced) or caps
	// them, and create_agent refuses what breaks either (agent.CheckLeadChoice):
	// saying so here is what keeps the lead from asking for it in the first
	// place. Enforcing wins over a project that leaves the model to the lead.
	if enforced {
		model = "the model this agent runs on. AgentBox's Settings → Models enforces " + defaultModel + " for every agent you " +
			"create, so leave this out: any other model is refused."
	} else if !auto {
		model += " That model is also the most you may use: choose a cheaper one for an easy task (sonnet for small, " +
			"well-defined work, haiku for a mechanical job like a rename or a config change) and keep " + defaultModel +
			" for medium and hard ones. A model above it is refused."
	} else {
		model += " Never choose one above " + defaultModel + ": it is the most Settings → Models allows, and a model above it is refused."
	}
	// The AI tool itself, offered only when an agent could really run the
	// other one: OpenCode has to be in the base image and have a login, and
	// asking for it otherwise would be a tool call that can only fail. The
	// models go with it, because OpenCode's names and Claude Code's are not
	// interchangeable in either direction.
	params = map[string]any{}
	tools := []string{"claude"}
	aiDescription := "which AI tool the agent runs. \"claude\" is Claude Code and the default, and what this project's " +
		"agents are set up for."
	if openCode {
		named := "OpenCode's own provider/model ids"
		if len(openCodeModels) > 0 {
			named = "one of " + strings.Join(openCodeModels, ", ")
		}
		model += " For an agent with ai=\"opencode\", the model is not a Claude Code name but " + named + " instead."
		tools = append(tools, "opencode")
		aiDescription += " \"opencode\" is OpenCode, the open-source agent, which runs the models of whichever " +
			"providers this machine has logged in to."
	}
	if cursorReady {
		named := "Cursor's own model ids (\"default\" is Cursor's Auto)"
		if len(cursorModels) > 0 {
			named = "one of " + cursorMenu(cursorModels)
		}
		model += " For an agent with ai=\"cursor\", the model is " + named + " instead."
		tools = append(tools, "cursor")
		aiDescription += " \"cursor\" is Cursor, which runs Cursor's own Composer models and other vendors' through " +
			"the user's Cursor account. It only has the chat, and can't stop to ask permission: it runs every tool call."
	}
	if len(tools) > 1 {
		params["ai"] = choiceOf(aiDescription+" Choose another tool than Claude Code when the task asks for it, or when the user "+
			"asked for it or for one of its models. Say which you chose and why, in the same line as the model.", tools...)
	}
	params["model"] = str(model)
	params["permissions"] = choiceOf("how the agent's AI tool asks permission. \"autonomous\" never asks: the agent's own machine "+
		"is the sandbox, and it works unattended. \"ask\" makes it stop before it changes anything, which only makes sense "+
		"if someone is watching its chat to answer. Leave this out for autonomous.", "autonomous", "ask")
	effort := str("how hard this agent thinks, as one of Claude Code's own effort levels — an OpenCode agent has none, so " +
		"leave it out for one, and a Cursor agent takes the levels its model lists (in the model's description above), " +
		"or none. For Claude Code it is checked when the agent is " +
		"made, so a level Claude Code has never offered is refused here rather than silently ignored. Some models have no effort " +
		"levels at all, and then it simply doesn't apply. Leave this out to use the effort chosen for new agents in AgentBox's " +
		"settings, and AgentBox's own default (high) when nothing is chosen there.")
	if len(efforts) > 0 {
		effort["enum"] = efforts
	}
	params["effort"] = effort
	params["context_window"] = str("where this agent's chat compacts, \"200k\" or \"1m\" — a Claude Code setting, checked " +
		"against the model: Haiku has no 1M window, and asking for one is refused. Leave this out for " + defaultWindow +
		", the window new agents start with in AgentBox's Settings → Models (an agent whose model has no 1M window gets 200k). " +
		"200k (the installation's compact window) is right for nearly every task: past it, every step of the agent resends the " +
		"whole conversation, so 1M costs up to five times as much per step late in a long task. Choose 1m only for work that " +
		"really needs a very large codebase or log in view at once.")
	switch {
	case enforced:
		params["context_window"] = str("where this agent's chat compacts. AgentBox's Settings → Models enforces " + defaultWindow +
			" for every agent you create, so leave this out: any other window is refused.")
	case defaultWindow == "1m":
		params["context_window"] = str(params["context_window"].(map[string]any)["description"].(string) +
			" 1m is the most Settings → Models allows.")
	default:
		params["context_window"] = str("where this agent's chat compacts, a Claude Code setting. Leave this out for " + defaultWindow +
			", the window new agents start with in AgentBox's Settings → Models. It is also the most Settings allows: a longer one is refused.")
	}
	return params, auto, readyTools{openCode: openCode, cursor: cursorReady}
}

// readyTools says which AI tools besides Claude Code an agent could run right
// now, so create_agent offers only those.
type readyTools struct{ openCode, cursor bool }

// cursorMenu names Cursor's models for the lead, each with the effort levels
// it takes, like "claude-opus-5-5 (effort: low, high)".
func cursorMenu(models []api.ChatOptionChoice) string {
	names := make([]string, 0, len(models))
	for _, m := range models {
		if len(m.Efforts) > 0 {
			names = append(names, m.Value+" (effort: "+strings.Join(m.Efforts, ", ")+")")
			continue
		}
		names = append(names, m.Value)
	}
	return strings.Join(names, ", ")
}

// leadAI reads create_agent's "ai": the AI tool the agent runs. Empty is
// Claude Code, which is what create_agent has always made. Anything else is
// refused here rather than in the daemon, so the refusal can say why the tool
// isn't on offer — the lead is told about OpenCode only when an agent could
// really run it, and a lead working from an older brief would otherwise get an
// error about a missing login with nothing to do about it.
func leadAI(ai string, ready readyTools) (string, error) {
	switch strings.TrimSpace(strings.ToLower(ai)) {
	case "", "claude":
		return "claude", nil
	case "opencode":
		if !ready.openCode {
			return "", errors.New("this machine can't run OpenCode agents: OpenCode has to be built into the base image " +
				"(agentbox image build --opencode) and logged in (agentbox auth opencode). Create the agent with ai=\"claude\", " +
				"or ask the user to set OpenCode up first")
		}
		return "opencode", nil
	case "cursor":
		if !ready.cursor {
			return "", errors.New("this machine can't run Cursor agents: agents aren't signed in to Cursor (agentbox auth cursor, or " +
				"Settings → Accounts in the app). Create the agent with ai=\"claude\", or ask the user to sign in to Cursor first")
		}
		return "cursor", nil
	default:
		return "", fmt.Errorf("ai is %q: it is \"claude\" (Claude Code), \"opencode\" (OpenCode) or \"cursor\" (Cursor)", ai)
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
	settings, leadPicksModel, ready := chatSettingParams(ctx, c)
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
				"finished and is holding a machine for nothing. Queued agents are listed too, with their " +
				"place in the queue: they have a name and a branch, but no machine until a slot is free.",
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
				"listed, ask the user to add it (Settings → Secrets on the project's page, or agentbox secrets " +
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
			Name: "list_connectors",
			Description: "This project's connectors: remote MCP servers like Notion or Linear that its agents get as tools, " +
				"signed in once by the user and held by AgentBox, so no agent ever sees a token — the project's own and the " +
				"AgentBox-wide ones it gets. Shows each one's status and " +
				"which agents have it. The connected ones are your own tools too (mcp__<name>__*). Pass create_agent's " +
				"connectors to give an agent only some of them. To add one, ask the user to (Settings → Connectors on the project's " +
				"page, or in AgentBox's Settings for every project); an agent that finds it needs one asks the user itself, with request_connector.",
			Run: func(json.RawMessage) (string, error) {
				found, err := c.ProjectConnectors(ctx)
				if err != nil {
					return "", err
				}
				return describeConnectors(found), nil
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
				"queue":    map[string]any{"type": "boolean", "description": "accepted and ignored: every agent starts at once"},
				"size":     sizeParam,
				"notify": choiceOf("what a genuine finish does to your chat: \"chat\" to be told and woken when this agent finishes, "+
					"\"off\" to only have the finish recorded — for a small, mechanical job you don't need to react to. This only "+
					"matters when this project's finish notices are set to \"lead\"; otherwise the project's own setting decides for "+
					"every agent, whatever you choose here. Leave this out to be woken, the same as \"chat\".", "chat", "off"),
				"connectors": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "which of this project's connectors (list_connectors) the agent gets as tools, by name, like " +
						"[\"notion\"]; [] for none. Leave it out to give it all of them. Each tool it has costs context in every " +
						"turn, so give an agent only the ones its task needs."},
				"claude_account": str("which of this project's Claude Code accounts (list_accounts) this agent logs in as. Only applies " +
					"when it runs Claude Code; sending it for an agent with ai=\"opencode\" or \"cursor\" is an error. An unknown or disallowed name is " +
					"refused, and the error names the ones it may use — use list_accounts to see them, along with how much " +
					"of each is left. Leave this out to use the project's own account, and the machine's default account when " +
					"the project has none set."),
			}, settings)),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Title, Task, From, AI, Branch string
					ClaudeAccount                 string `json:"claude_account"`
					Research                      bool
					Queue                         *bool
					Size                          string
					// Pointers: an agent given no model is not the same as one
					// asked for the empty model, and only the first falls back
					// to what new agents start on.
					Model, Effort, Permissions, Notify *string
					ContextWindow                      *string `json:"context_window"`
					Connectors                         *[]string
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Task) == "" {
					return "", errors.New("an agent needs a title and a task")
				}
				ai, err := leadAI(in.AI, ready)
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
					Queue:         in.Queue,
					Size:          in.Size,
					Connectors:    in.Connectors,
				})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Creating %q%s; it starts on the task by itself. Job %s.",
					in.Title, describeChoices(ai, in.Model, in.Effort, autonomous, notify, in.ClaudeAccount)+windowChoice(in.ContextWindow)+connectorChoice(in.Connectors), job.ID), nil
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
				switch {
				case it.Kind == "aside":
					return fmt.Sprintf("Told %s, mid-work; it decides when to act on it.", in.Agent), nil
				case it.Woke != "":
					return fmt.Sprintf("%s was %s, so AgentBox %s its machine first. Told it; it is working on it.", in.Agent, map[string]string{"started": "stopped", "resumed": "paused"}[it.Woke], it.Woke), nil
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
				"the project's media view. An agent with uncommitted work is left alone, and so is one " +
				"still working, which includes waiting on a background command or monitor it left " +
				"running, unless you force it. The next task is a new agent, not this one.",
			Schema: object([]string{"agent"}, map[string]any{
				"agent": str("its name, like agent-03"),
				"how":   str("stop (the default), pause or destroy"),
				"force": map[string]any{"type": "boolean", "description": "retire it even though it is still working or has uncommitted work"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Agent, How string
					Force      bool
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				result, err := c.RetireProject(ctx, api.RetireRequest{How: in.How, Agents: []string{in.Agent}, Force: in.Force})
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
			Name: "agent_checkpoints",
			Description: "An agent's checkpoints, oldest first: one at the end of each of its chat turns (its files, " +
				"uncommitted ones included), and saved-… ones holding what it had before a rollback. " +
				"roll_back_agent and fork_agent take their IDs.",
			Schema: object([]string{"agent"}, map[string]any{
				"agent": str("its name, like agent-03"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Agent string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				list, err := c.AgentCheckpoints(ctx, in.Agent)
				if err != nil {
					return "", err
				}
				if len(list) == 0 {
					return in.Agent + " has no checkpoints yet: one is taken as each of its chat turns ends.", nil
				}
				var b strings.Builder
				for _, cp := range list {
					fmt.Fprintf(&b, "%s, %s: %s\n", cp.ID, ago(cp.CreatedAt), oneLine(cp.Prompt))
				}
				return b.String(), nil
			},
		},
		{
			Name: "roll_back_agent",
			Description: "Put an agent's worktree and chat back to the end of one of its turns: the turns after it " +
				"leave its conversation, its files go back, and its AI session restarts told the conversation up to there. " +
				"What it had is saved as a checkpoint first. It undoes the agent's work, so do it only when the user " +
				"asked for it. Refused while the agent is mid-turn.",
			Schema: object([]string{"agent", "checkpoint"}, map[string]any{
				"agent":      str("its name, like agent-03"),
				"checkpoint": str("a turn's checkpoint from agent_checkpoints, like turn-4, or just the turn's number"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Agent, Checkpoint string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				res, err := c.RollbackAgent(ctx, in.Agent, in.Checkpoint)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s is back at %s. What it had is saved as %s, which fork_agent can start from.", in.Agent, in.Checkpoint, res.Saved.ID), nil
			},
		},
		{
			Name: "fork_agent",
			Description: "Create a new agent from another's work: from one of its checkpoints (its branch starts from " +
				"the files as they were at that turn, and its chat from the conversation up to it), or, without one, " +
				"from its machine and files as they are now. Tell the fork what to do next with tell_agent once it exists.",
			Schema: object([]string{"agent"}, map[string]any{
				"agent":      str("the agent to fork, like agent-03"),
				"checkpoint": str("a checkpoint from agent_checkpoints, like turn-4, or just the turn's number"),
				"title":      str("the new agent's title; the source's with \" (fork)\" by default"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Agent, Checkpoint, Title string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				job, err := c.ForkAgent(ctx, in.Agent, api.ForkRequest{Checkpoint: in.Checkpoint, Title: in.Title})
				if err != nil {
					return "", err
				}
				from := "as it is now"
				if in.Checkpoint != "" {
					from = "at " + in.Checkpoint
				}
				return fmt.Sprintf("Forking %s %s; list_agents shows the new agent when it is ready. Job %s.", in.Agent, from, job.ID), nil
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
				"For a problem, name the pull requests (#234), branches and question ids it waits on: AgentBox closes " +
				"it by itself once they are all merged, closed or answered. " +
				"This is not append_note: notes are the short standing brief every agent is handed, and memory is the " +
				"much larger store they search. scope \"all\" keeps it for every project instead of this one: only " +
				"when the user asks for something to hold in all their projects.",
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
				"supersedes": str("the id of the memory this replaces, from search_memory; one marked \"all projects\" " +
					"is replaced with scope \"all\""),
				"scope": choiceOf("\"project\", the default, is this project's. \"all\" is AgentBox-wide: every project's "+
					"chat and agents read it. Use \"all\" only when the user asked for this to apply to all their projects.",
					api.MemoryScopeProject, api.MemoryScopeAll),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Title, Content, Kind, Supersedes, Scope string
					Importance                              int
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Title) == "" {
					return "", errors.New("a memory needs a title: one line somebody would recognise it by")
				}
				m, err := c.LeadMemory().AddMemory(ctx, api.AddMemoryRequest{
					Kind: in.Kind, Title: in.Title, Content: in.Content,
					Importance: in.Importance, SupersedesID: in.Supersedes, Scope: in.Scope,
				})
				if err != nil {
					return "", err
				}
				return describeRemembered(m, in.Supersedes), nil
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
				return describeResolved(m), nil
			},
		},
		memoryFeedbackTool(ctx, c.LeadMemory()),
		{
			Name: "promote_memory",
			Description: "Make a memory one of this project's notes, when your brief offers it under \"Memories that " +
				"could be notes\": AgentBox kept handing it to agent after agent, and a note says it to every agent " +
				"instead. The note is added like append_note's, and the memory stops being served in briefs, so the " +
				"fact isn't paid for twice; search_memory still finds it. Promote only a standing rule — every note " +
				"costs every agent context — and word it as one short sentence in text: without it the note is the " +
				"memory's title and the start of its content. dismiss_promotion is the other answer.",
			Schema: object([]string{"id"}, map[string]any{
				"id":   str("the memory's id, from the offer in your brief"),
				"text": str("the note as every agent should read it, in a sentence"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ ID, Text string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.ID) == "" {
					return "", errors.New("say which memory to promote, by its id")
				}
				out, err := c.LeadMemory().PromoteMemory(ctx, in.ID, in.Text)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s (%q) is a note now: new agents get it in their brief, running ones from their "+
					"next session, and no brief serves the memory beside it.", out.Memory.ID, out.Memory.Title), nil
			},
		},
		{
			Name: "dismiss_promotion",
			Description: "Answer an offer under \"Memories that could be notes\" in your brief with no: the memory " +
				"stays an ordinary memory, served to the agents whose tasks it matches, and is never offered as a " +
				"note again. For one that matters to many tasks for now but isn't a standing rule.",
			Schema: object([]string{"id"}, map[string]any{
				"id": str("the memory's id, from the offer in your brief"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ ID string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.ID) == "" {
					return "", errors.New("say which memory, by its id")
				}
				m, err := c.LeadMemory().DismissPromotion(ctx, in.ID)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s (%q) stays a memory and won't be offered as a note again.", m.ID, m.Title), nil
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
			Description: "Where this project stands: what it is doing now, the problems nobody has fixed, and what " +
				"its agents reported as they finished. Short on purpose — read it at the start of a conversation, before deciding what " +
				"to do next, and use search_memory when you need the detail behind a line of it.",
			Run: func(json.RawMessage) (string, error) {
				return projectState(ctx, c), nil
			},
		},
		{
			Name: "list_skills",
			Description: "The skills AgentBox installs into agents (and you): each one's name, what it is for, and " +
				"whether this project's agents get it. read_skill shows one whole.",
			Run: func(json.RawMessage) (string, error) {
				list, err := c.ProjectSkills(ctx)
				if err != nil {
					return "", err
				}
				return describeSkills(list), nil
			},
		},
		{
			Name:        "read_skill",
			Description: "One skill's SKILL.md, and the other files in its folder.",
			Schema:      object([]string{"name"}, map[string]any{"name": str("the skill's name")}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Name string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				detail, err := c.ProjectSkill(ctx, in.Name)
				if err != nil {
					return "", err
				}
				return describeSkill(detail), nil
			},
		},
		{
			Name: "create_skill",
			Description: "Write a new skill: a procedure agents follow when its description matches their task. " +
				"Write one when a procedure recurs across agents' tasks, or the user repeats an instruction. It is " +
				"on in this project only, unless everywhere is set because it is general. Agents get it from their " +
				"next session; tell the user in a line. An existing name is refused: edit_skill changes a skill.",
			Schema: object([]string{"name", "content"}, map[string]any{
				"name": str("lowercase-with-dashes, like release-checklist"),
				"content": str("the whole SKILL.md: YAML front matter with name and description (when to use it, " +
					"in a sentence the model matches tasks against), then the steps"),
				"everywhere": map[string]any{"type": "boolean", "description": "on in every project, for a general skill"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Name, Content string
					Everywhere    bool
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if _, err := c.CreateProjectSkill(ctx, api.LeadNewSkillRequest{Name: in.Name, Content: in.Content, Everywhere: in.Everywhere}); err != nil {
					return "", err
				}
				if in.Everywhere {
					return "Created " + in.Name + ", on in every project.", nil
				}
				return "Created " + in.Name + ", on in this project.", nil
			},
		},
		{
			Name: "edit_skill",
			Description: "Replace a skill's SKILL.md. The user approves it first, from a card with the diff in your " +
				"chat, and this waits for them; a refusal comes back as an error, and nothing changes.",
			Schema: object([]string{"name", "content"}, map[string]any{
				"name":    str("the skill's name"),
				"content": str("the whole new SKILL.md"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Name, Content string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if _, err := c.EditProjectSkill(ctx, in.Name, in.Content); err != nil {
					return "", err
				}
				return "The user approved it: " + in.Name + " is changed.", nil
			},
		},
		{
			Name: "switch_skill",
			Description: "Turn a skill on or off, in this project or everywhere. Turning one on is yours to do; " +
				"turning one off where it was on waits for the user's approval, from a card in your chat.",
			Schema: object([]string{"name", "state"}, map[string]any{
				"name": str("the skill's name"),
				"state": choiceOf("on, off, or (in this project only) \"default\" to follow the AgentBox-wide switch",
					"on", "off", "default"),
				"everywhere": map[string]any{"type": "boolean", "description": "switch it AgentBox-wide rather than in this project"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Name, State string
					Everywhere  bool
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				override := in.State
				if override == "default" {
					override = ""
				}
				sk, err := c.SwitchProjectSkill(ctx, in.Name, api.LeadSkillSwitchRequest{Override: override, Everywhere: in.Everywhere})
				if err != nil {
					return "", err
				}
				return in.Name + " is now " + skillState(sk), nil
			},
		},
		{
			Name: "delete_skill",
			Description: "Delete a skill from AgentBox and every agent. The user approves it first, from a card in " +
				"your chat, and this waits for them; a refusal comes back as an error. To stop one reaching this " +
				"project alone, switch_skill it off here instead.",
			Schema: object([]string{"name"}, map[string]any{"name": str("the skill's name")}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Name string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if err := c.RemoveProjectSkill(ctx, in.Name); err != nil {
					return "", err
				}
				return "The user approved it: " + in.Name + " is deleted.", nil
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

// sizeParam is create_agent's size, accepted from leads that still send it
// and ignored: the VM's memory pressure decides when an agent's tests and
// builds run, not a size.
var sizeParam = map[string]any{"type": "string", "description": "accepted and ignored"}

// memoryHeld says what the VM's memory pressure holds of an agent's tests and
// builds, "" for nothing.
func memoryHeld(h *api.MemoryHold) string {
	switch {
	case h == nil:
		return ""
	case h.Paused > 0 && h.Waiting > 0:
		return fmt.Sprintf("%d test/build %s paused and %d waiting for memory", h.Paused, plural(h.Paused, "command", "commands"), h.Waiting)
	case h.Paused > 0:
		return fmt.Sprintf("%d test/build %s paused for memory", h.Paused, plural(h.Paused, "command", "commands"))
	}
	return fmt.Sprintf("%d test/build %s waiting for memory", h.Waiting, plural(h.Waiting, "command", "commands"))
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
		if doing := agentDoing(f.Agent); doing != "" {
			fmt.Fprintf(&b, ", %s", doing)
		}
		if held := memoryHeld(f.Memory); held != "" {
			fmt.Fprintf(&b, ", %s", held)
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
			"under Settings → Secrets on the project's page (or with agentbox secrets set), and it becomes an environment " +
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

// describeConnectors is list_connectors' answer.
func describeConnectors(found []api.Connector) string {
	if len(found) == 0 {
		return "This project has no connectors. If an agent needs a service like Notion or Linear as tools, ask the user " +
			"to add it under Settings → Connectors on the project's page (or with agentbox connector add), or let the agent ask with request_connector."
	}
	var b strings.Builder
	b.WriteString("This project's connectors:\n")
	for _, conn := range found {
		status := conn.Status
		if conn.Error != "" {
			status += " (" + conn.Error + ")"
		}
		if !conn.Enabled {
			status = "turned off"
		}
		given := "no agent"
		if len(conn.Agents) > 0 {
			given = strings.Join(conn.Agents, ", ")
		}
		if conn.Scope == api.ConnectorWide {
			given += " (AgentBox-wide)"
		}
		fmt.Fprintf(&b, "- %s — %s, %s; given to %s\n", conn.Name, conn.URL, status, given)
	}
	return b.String()
}

// connectorChoice says which connectors an agent was given, when it wasn't
// every one.
func connectorChoice(connectors *[]string) string {
	switch {
	case connectors == nil:
		return ""
	case len(*connectors) == 0:
		return ", with no connectors"
	}
	return ", with the connectors " + strings.Join(*connectors, ", ")
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
		if q.Kind != "" {
			// Not the lead's to answer: the user does, in the app, and the value
			// never passes through a chat (D95).
			fmt.Fprintf(&b, "- id %s, from %s: asking the user for %s, which only the user can answer, in the app\n  %s\n",
				q.ID, q.Agent, credentialWanted(q), q.Question)
			continue
		}
		fmt.Fprintf(&b, "- id %s, from %s (%s)\n  %s\n", q.ID, q.Agent, q.Status, q.Question)
		if q.Context != "" {
			fmt.Fprintf(&b, "  what it was doing: %s\n", q.Context)
		}
	}
	return b.String()
}

// credentialWanted names what a credential request asks for.
func credentialWanted(q api.Question) string {
	if q.Kind == api.CredentialSecret {
		return "the secret $" + q.SecretName
	}
	return "a GitHub account"
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
			scope := ""
			if m.Global {
				scope = ", all projects"
			}
			fmt.Fprintf(&b, "- [%s] %s (%s, importance %d%s%s)\n", m.ID, m.Title, m.Kind, m.Importance, scope, confirmed(m.Confirmations))
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

// describeResolved is resolve_memory's answer.
func describeResolved(m api.Memory) string {
	who := "for you or for any agent of this project"
	if m.Global {
		who = "in any project"
	}
	return fmt.Sprintf("Closed %s (%q). It no longer comes back from a search %s, and is still readable by id.", m.ID, m.Title, who)
}

// describeRemembered is remember's answer: where the memory went, and who
// reads it now.
func describeRemembered(m api.Memory, supersedes string) string {
	who := "for you and for every agent of this project"
	if m.Global {
		who = "for every project's chat and agents, and in Settings → Memory"
	}
	if supersedes != "" {
		return fmt.Sprintf("Remembered as %s, replacing %s, which no longer comes back from a search. search_memory finds it, %s.", m.ID, supersedes, who)
	}
	return fmt.Sprintf("Remembered as %s. search_memory finds it, %s.", m.ID, who)
}

// confirmed says how many times a memory was written down again, when it was.
func confirmed(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf(", said %d more %s", n, plural(n, "time", "times"))
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

// projectState is working memory, the issues nobody has fixed, and
// the last few reports: the least a chat needs before it decides what happens
// next. Each part fails quietly on its own, because a state that is missing
// one section is worth more than an error.
func projectState(ctx context.Context, c *api.Client) string {
	m := c.LeadMemory()
	var b strings.Builder
	if w, err := m.WorkingMemory(ctx); err == nil {
		b.WriteString(describeWorking(w))
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
	b.WriteString("\nsearch_memory has the detail behind any of this, and remember writes something new down.\n")
	return b.String()
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

// describeSkills lists skills for list_skills, one a line.
func describeSkills(list []api.Skill) string {
	if len(list) == 0 {
		return "There are no skills yet. create_skill writes one."
	}
	var b strings.Builder
	for _, sk := range list {
		fmt.Fprintf(&b, "- %s (%s): %s\n", sk.Name, skillState(sk), oneLine(sk.Description))
	}
	return b.String()
}

// skillState says where a skill is on, from the lead's project.
func skillState(sk api.Skill) string {
	here := "off here"
	if sk.Active != nil && *sk.Active {
		here = "on here"
	}
	everywhere := "off AgentBox-wide"
	if sk.Enabled {
		everywhere = "on AgentBox-wide"
	}
	return here + ", " + everywhere
}

// describeSkill is read_skill's answer: SKILL.md, then the other files' names.
func describeSkill(detail api.SkillDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s)\n\n", detail.Name, skillState(detail.Skill))
	var others []string
	for _, f := range detail.Files {
		if f.Path == "SKILL.md" {
			b.WriteString(f.Content)
		} else {
			others = append(others, fmt.Sprintf("- %s (%s)", f.Path, sizeOf(f.Size)))
		}
	}
	if len(others) > 0 {
		b.WriteString("\n\nOther files:\n" + strings.Join(others, "\n"))
	}
	return b.String()
}
