package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
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
// nothing it can send reaches another but a message to another project's lead.
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
	defaultModel, defaultWindow := state.DefaultClaudeModel, strings.ToLower(state.FormatContextWindow(state.DefaultClaudeCompactWindow))
	var enforced bool
	if settings, err := c.ProjectSettings(ctx); err == nil {
		enforced = settings.EnforceAgentDefaults
		if settings.DefaultClaudeModel != "" {
			defaultModel = settings.DefaultClaudeModel
		}
		if n, err := state.ParseContextWindow(settings.DefaultAgentContextWindow); err == nil && n > 0 {
			defaultWindow = strings.ToLower(state.FormatContextWindow(n))
		} else if settings.ClaudeCompactWindow > 0 {
			// "" is the installation's compact window, which is what the
			// agent gets when Settings leaves its window to the default.
			defaultWindow = strings.ToLower(state.FormatContextWindow(settings.ClaudeCompactWindow))
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
	defaultFrom := "Settings → Models' default"
	if p, err := c.ProjectSelf(ctx); err == nil {
		auto = p.AgentModel == api.AgentModelAuto
		if p.AgentModel != "" && !auto {
			defaultModel, defaultFrom = p.AgentModel, "this project's choice"
		}
	}

	// Before any chat has started there is no remembered menu, and AgentBox
	// has nothing honest to name. The description then says what kind of name
	// is wanted, and says plainly that a wrong one fails rather than working.
	// How to choose between the models is the brief's to say (lead.md.tmpl):
	// these descriptions say only what each value does.
	named := "a name Claude Code itself uses, like \"sonnet\", not an API model id"
	if len(models) > 0 {
		named = "one of " + strings.Join(models, ", ")
	}
	model := "its model: " + named + ". One Claude Code won't take fails as the agent starts, so don't guess. " +
		"Leave this out for " + defaultModel + ", " + defaultFrom + "."
	if auto {
		model = "its model: " + named + ". This project asks you to choose one for every agent, from how hard the task is " +
			"(your brief says how), and to say which and why in a line; never Fable unless the user asked. One Claude Code won't " +
			"take fails as the agent starts. Left out, it falls back to " + defaultModel + "."
	}
	// Settings → Models either fixes the model and window (enforced) or caps
	// them, and create_agent refuses what breaks either (agent.CheckLeadChoice):
	// saying so here is what keeps the lead from asking for it in the first
	// place. Enforcing wins over a project that leaves the model to the lead.
	if enforced {
		model = "its model. Settings → Models enforces " + defaultModel + " for every agent you create, so leave this out: any other is refused."
	} else if !auto {
		model += " That is also the most you may use, and a model above it is refused: choose sonnet for small, well-defined " +
			"work and haiku for a mechanical job like a rename."
	} else {
		model += " Never above " + defaultModel + ", the most Settings → Models allows: a model above it is refused."
	}
	// The AI tool itself, offered only when an agent could really run the
	// other one: OpenCode has to be in the base image and have a login, and
	// asking for it otherwise would be a tool call that can only fail. The
	// models go with it, because OpenCode's names and Claude Code's are not
	// interchangeable in either direction.
	params = map[string]any{}
	tools := []string{"claude"}
	aiDescription := "its AI tool: claude (Claude Code), the default"
	if openCode {
		named := "OpenCode's provider/model ids"
		if len(openCodeModels) > 0 {
			named = "one of " + strings.Join(openCodeModels, ", ")
		}
		model += " For ai=\"opencode\" it is " + named + "."
		tools = append(tools, "opencode")
		aiDescription += "; opencode (OpenCode, on the providers this machine is logged in to)"
	}
	if cursorReady {
		named := "Cursor's model ids (\"default\" is Auto)"
		if len(cursorModels) > 0 {
			named = "one of " + cursorMenu(cursorModels)
		}
		model += " For ai=\"cursor\" it is " + named + "."
		tools = append(tools, "cursor")
		aiDescription += "; cursor (Cursor's and other vendors' models on the user's Cursor account: chat only, and it never asks permission)"
	}
	if len(tools) > 1 {
		params["ai"] = choiceOf(aiDescription+". Another than claude only when the task or the user asks for it; say which and why.", tools...)
	}
	// The names above are Claude Code's, and an agent given no ai runs the
	// lead's tool, which may be another: one of these names keeps it on
	// Claude Code (agent.NamesClaudeModel), and leaving the model out gives
	// it its own tool's default rather than Claude Code's.
	model += " These are Claude Code's: on another tool an agent starts on that tool's default, and one of these with no ai makes a Claude Code agent."
	params["model"] = str(model)
	params["permissions"] = choiceOf("autonomous (the default) never asks: its machine is the sandbox. ask stops before every change, "+
		"only for an agent someone is watching", "autonomous", "ask")
	effort := str("how hard it thinks, as Claude Code names it; an unknown level is refused, and a model with none ignores it. " +
		"None for OpenCode; a Cursor model's own levels, listed with it. Leave this out for the effort new agents start on in Settings, else medium.")
	if len(efforts) > 0 {
		effort["enum"] = efforts
	}
	params["effort"] = effort
	switch {
	case enforced:
		params["context_window"] = str("where its chat compacts. Settings → Models enforces " + defaultWindow +
			" for every agent you create, so leave this out: any other window is refused.")
	case defaultWindow == "1m":
		params["context_window"] = str("where its chat compacts, \"200k\" or \"1m\" (not for Haiku). Leave this out for 1m, " +
			"Settings → Models' default and the most it allows. Late in a long task 1m costs up to twice as much a step: " +
			"choose 200k unless the work needs a very large codebase or log in view at once.")
	default:
		params["context_window"] = str("where its chat compacts. Leave this out for " + defaultWindow +
			", Settings → Models' default, which is also the most it allows: a longer one is refused.")
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

// leadAI reads create_agent's "ai": the AI tool the agent runs. Empty stays
// empty, for the daemon to give the agent the tool its lead runs, on the
// lead's account (agent.LeadToolDefault), or Claude Code when agents can't run
// that tool here. A named tool is checked here rather than in the daemon, so the refusal can say why the tool
// isn't on offer — the lead is told about OpenCode only when an agent could
// really run it, and a lead working from an older brief would otherwise get an
// error about a missing login with nothing to do about it.
func leadAI(ai string, ready readyTools) (string, error) {
	switch strings.TrimSpace(strings.ToLower(ai)) {
	case "":
		return "", nil
	case "claude":
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

// variantAI is leadAI for one agent of a group, which may also run Codex:
// comparing tools is what a group is for. Whether this machine is signed in
// to Codex is the daemon's to say, in its own words, before anything starts.
func variantAI(ai string, ready readyTools) (string, error) {
	if strings.TrimSpace(strings.ToLower(ai)) == "codex" {
		return "codex", nil
	}
	return leadAI(ai, ready)
}

// describeChoices names the settings a lead asked for, so the reply confirms
// what was chosen rather than leaving it to be found later in the agent's chat.
// Anything left to fall back to the project's own defaults goes unmentioned.
// describeAccountMap is OpenCode's accounts in a few words: "anthropic: work".
func describeAccountMap(m map[string]string) string {
	var parts []string
	for _, p := range slices.Sorted(maps.Keys(m)) {
		if m[p] != "" {
			parts = append(parts, p+": "+m[p])
		}
	}
	return strings.Join(parts, ", ")
}

// describeAccountSwitch says what switch_agent_account did, in the chat's
// own words when its chat restarted.
func describeAccountSwitch(res api.AgentAccountSwitch) string {
	out := fmt.Sprintf("%s's %s now runs on the account %s.", res.Agent, res.Tool, res.Account)
	switch {
	case res.Note != "":
		out += " Its chat says: " + res.Note
	default:
		out += " Its chat wasn't running: the next one starts on it."
	}
	if !res.Resumes && res.Note == "" {
		out += " " + res.Tool + " can't resume a session, so it keeps the old account until its chat starts again, and then starts a new session."
	}
	return out
}

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

// toolNotice is the daemon's word on why an agent runs Claude Code rather than
// the lead's own tool, after create_agent's reply.
func toolNotice(notice string) string {
	if notice == "" {
		return ""
	}
	return "\n" + notice
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
//
// Every tool's name, description and schema is sent to the model again with
// every call it makes, so the list is kept short: a family of operations on
// one thing (notes, skills, schedules, questions) is one tool with an action
// (actionTool), and a description says what the model needs at the moment it
// fills the call in, leaving the why to the lead's brief (lead.md.tmpl).
// TestLeadToolsBudget holds the list to its size.
func projectTools(ctx context.Context, c *api.Client) []mcp.Tool {
	settings, leadPicksModel, ready := chatSettingParams(ctx, c)
	// Said once here, where every lead reads it, and not only in ai's
	// description, which a machine with Claude Code alone doesn't offer.
	runsTool := " Without ai it runs the tool you run, on your account for it, or Claude Code when agents can't run " +
		"yours here, which the reply says."
	startsOn := "It starts on the model, effort and permissions new agents start on here unless you set them."
	if leadPicksModel {
		startsOn = "Choose its model (see model); effort and permissions default to what new agents start on here."
	}
	agent := str("its name, like agent-03")
	// Each account left out is yours when the agent runs the tool you run,
	// else the project's.
	accounts := map[string]any{
		"claude_account": str("which Claude Code account it logs in as (list_resources accounts), making it a Claude Code agent; " +
			"left out, yours when it runs your tool, else the project's"),
		"codex_account": str("the same for a Codex agent"),
	}
	if ready.cursor {
		accounts["cursor_account"] = str("the same for a Cursor agent")
	}
	if ready.openCode {
		accounts["opencode_accounts"] = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
			"description": "for ai=\"opencode\": provider → account, like {\"anthropic\": \"work\"}; the project's for a provider left out"}
	}
	tools := []mcp.Tool{
		{
			Name: "list_agents",
			Description: "This project's agents: what each is for and doing, its changes, media and pull request, and whether it " +
				"finished and still holds a machine. Queued ones show their place. With archived, past agents instead, which read_agent still reads.",
			Schema: object(nil, archivedParams),
			Run: func(args json.RawMessage) (string, error) {
				var in archivedArgs
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				if in.Archived {
					past, err := c.ProjectArchivedAgents(ctx, in.options(""))
					if err != nil {
						return "", err
					}
					return describeArchived(past), nil
				}
				fleet, err := c.ProjectFleet(ctx)
				if err != nil {
					return "", err
				}
				return describeFleet(fleet), nil
			},
		},
		{
			Name: "create_agent",
			Description: "Create an agent with a task: its own machine and, unless research, its own branch. Write the task " +
				"directly: what to do, what done looks like, what to leave alone. With issue, the task is that GitHub issue and its " +
				"pull request closes it. " +
				startsOn + runsTool,
			Schema: object(nil, merge(merge(map[string]any{
				"title": str("a short name for the sidebar, like \"Reminders page\"; required without issue"),
				"task":  str("what to do, directly: usually a few lines; required without issue, added after it with one"),
				"issue": map[string]any{"type": "integer", "description": "an open issue's number (list_resources issues)"},
				"branch": str("after the project's prefix: short kebab-case naming the work, like \"fix-login-redirect\", at most 48 " +
					"characters. Pass one every time; a taken one gets -2 appended"),
				"from": str("the branch to start from, or an agent to stack this one on (its pull request then targets that " +
					"agent's branch); the project's branch by default"),
				"research": map[string]any{"type": "boolean", "description": "it only investigates, so it gets no branch"},
				"notify": choiceOf("\"off\" only records its finish instead of waking you (\"chat\", the default), for a job you needn't "+
					"react to; it matters only when the project's finish notices go to the lead", "chat", "off"),
				"connectors": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "which connectors (list_resources connectors) it gets, like [\"notion\"]; [] for none, all when left out. Each costs it context every turn"},
			}, accounts), settings)),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Title, Task, From, AI, Branch string
					Issue                         int
					ClaudeAccount                 string            `json:"claude_account"`
					CodexAccount                  string            `json:"codex_account"`
					CursorAccount                 string            `json:"cursor_account"`
					OpenCodeAccounts              map[string]string `json:"opencode_accounts"`
					Research                      bool
					// Pointers: an agent given no model is not the same as one
					// asked for the empty model, and only the first falls back
					// to what new agents start on.
					Model, Effort, Permissions, Notify *string
					ContextWindow                      *string `json:"context_window"`
					Connectors                         *[]string
				}
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				if in.Issue == 0 && (strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Task) == "") {
					return "", errors.New("an agent needs a title and a task, or an issue")
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
					Title: in.Title, Task: in.Task, Issue: in.Issue, From: in.From, AI: ai, Branch: in.Branch,
					Autonomous: &autonomous, Model: in.Model, Effort: in.Effort,
					ContextWindow:    in.ContextWindow,
					ClaudeAccount:    in.ClaudeAccount,
					CodexAccount:     in.CodexAccount,
					CursorAccount:    in.CursorAccount,
					OpenCodeAccounts: in.OpenCodeAccounts,
					FinishNotice:     notify,
					Connectors:       in.Connectors,
				})
				if err != nil {
					return "", err
				}
				what := fmt.Sprintf("%q", in.Title)
				if in.Issue != 0 {
					what = fmt.Sprintf("an agent for issue #%d", in.Issue)
				}
				return fmt.Sprintf("Creating %s%s; it starts on the task by itself. Job %s.%s",
					what, describeChoices(cmp.Or(job.AI, ai), in.Model, in.Effort, autonomous, notify, cmp.Or(in.ClaudeAccount, in.CodexAccount, in.CursorAccount, describeAccountMap(in.OpenCodeAccounts)))+windowChoice(in.ContextWindow)+connectorChoice(in.Connectors), job.ID, toolNotice(job.Notice)), nil
			},
		},
		{
			Name: "fan_out",
			Description: "Run one task on 2 to 6 agents at once, each on the tool and model you give it, as a group the user " +
				"compares and keeps one of (it then opens its pull request; the rest are retired, branches kept). Only when the " +
				"user asks to compare models or tools, or a hard task is worth trying several ways. You hear once, when the last finishes.",
			Schema: object([]string{"title", "task", "variants"}, map[string]any{
				"title":  str("a short name for the group, like \"Login page\"; each agent gets its letter after it"),
				"task":   str("what to do, directly, the same for every agent"),
				"branch": str("the slug every branch starts with, each getting its letter after it; at most 46 characters"),
				"from":   str("the branch or agent branch they start from; the project's branch by default"),
				"judge":  map[string]any{"type": "boolean", "description": "you are asked to compare them and recommend one once all have finished"},
				"variants": map[string]any{"type": "array", "minItems": 2, "maxItems": 6,
					"description": "one entry per agent (A, B, C…). Two alike run the same task twice",
					"items":       object(nil, variantSettings(settings, ready))},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Title, Task, From, Branch string
					Judge                     bool
					Variants                  []struct {
						AI               string
						Model, Effort    *string
						ContextWindow    *string           `json:"context_window"`
						ClaudeAccount    string            `json:"claude_account"`
						CodexAccount     string            `json:"codex_account"`
						CursorAccount    string            `json:"cursor_account"`
						OpenCodeAccounts map[string]string `json:"opencode_accounts"`
					}
				}
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				req := api.FanOutRequest{Title: in.Title, Task: in.Task, From: in.From, Branch: in.Branch, Judge: in.Judge}
				for i, v := range in.Variants {
					ai, err := variantAI(v.AI, ready)
					if err != nil {
						return "", fmt.Errorf("variant %d: %w", i+1, err)
					}
					req.Variants = append(req.Variants, api.FanOutVariant{
						AI: ai, Model: v.Model, Effort: v.Effort, ContextWindow: v.ContextWindow,
						ClaudeAccount: v.ClaudeAccount, CodexAccount: v.CodexAccount, CursorAccount: v.CursorAccount, OpenCodeAccounts: v.OpenCodeAccounts,
					})
				}
				out, err := c.ProjectFanOut(ctx, req)
				if err != nil {
					return "", err
				}
				return describeGroup(out.Group, "Started group "+out.Group.ID+": ") + toolNotice(strings.Join(out.Notices, " ")), nil
			},
		},
		{
			Name: "compare_group",
			Description: "A group's agents side by side (what each runs, finished or not, diff size, pull request and checks, cost, " +
				"time, media, report); the project's groups when group is left out. With recommend and why, records which you would " +
				"keep, shown in the user's Compare view: the user still decides, so never keep or retire one yourself.",
			Schema: object(nil, map[string]any{
				"group":     str("its ID, like grp_1a2b3c4d5e6f"),
				"recommend": str("the agent you would keep, like agent-03"),
				"why":       str("with recommend, two or three sentences: what it got right that the others didn't, and what it still lacks"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Group, Recommend, Why string }
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				if in.Recommend != "" {
					g, err := c.RecommendInGroup(ctx, in.Group, api.RecommendRequest{Agent: in.Recommend, Why: in.Why})
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("Recommended %s in group %s; the user sees it in the Compare view and decides.", g.Recommended, g.ID), nil
				}
				if in.Group == "" {
					groups, err := c.ProjectGroups(ctx)
					if err != nil {
						return "", err
					}
					if len(groups) == 0 {
						return "This project has no groups. Start one with fan_out.", nil
					}
					var b strings.Builder
					for _, g := range groups {
						b.WriteString(describeGroup(g, "") + "\n")
					}
					return b.String(), nil
				}
				g, err := c.ProjectGroup(ctx, in.Group)
				if err != nil {
					return "", err
				}
				return compareText(g), nil
			},
		},
		{
			Name: "tell_agent",
			Description: "Send one of this project's agents a correction, more detail or its next step. A working agent gets it " +
				"mid-work and decides when to act on it, so don't wait for it or stop it first.",
			Schema: object([]string{"agent", "message"}, map[string]any{
				"agent":   agent,
				"message": str("what to tell it, in a line or two"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Agent, Message string }
				if err := decodeArgs(args, &in); err != nil {
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
			Name: "read_agent",
			Description: "What an agent has done: its conversation, newest last, or with show its diff against the commit it " +
				"started from. A large diff comes back as its files and its start: read the rest a path at a time.",
			Schema: object([]string{"agent"}, map[string]any{
				"agent": agent,
				"show":  choiceOf("chat (the default), diff, or stat for the diff's summary", "chat", "diff", "stat"),
				"last":  map[string]any{"type": "integer", "description": "chat: how many entries; 20 by default"},
				"path":  str("diff: only this file or directory, relative to the repository"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Agent, Show, Path string
					Last              int
				}
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				switch in.Show {
				case "", "chat":
					if in.Last <= 0 {
						in.Last = 20
					}
					thread, err := c.AgentChat(ctx, in.Agent)
					if err != nil {
						return "", err
					}
					return describeThread(in.Agent, thread, in.Last), nil
				case "diff", "stat":
					return agentDiff(ctx, c, in.Agent, in.Show == "stat", in.Path)
				}
				return "", fmt.Errorf("show is %q: it is chat, diff or stat", in.Show)
			},
		},
		{
			Name: "run_in_agent",
			Description: "Run a short shell command in an agent's machine, in its worktree, with its toolchain and network: a quick " +
				"check, a git command. You get the exit code and the end of the output, which stays in this conversation, so trim it " +
				"(| tail, | grep, -q). No input; stopped at the timeout. A build, an install or a run of the app is an agent's task.",
			Schema: object([]string{"agent", "command"}, map[string]any{
				"agent":           agent,
				"command":         str("a bash command line, run as the agent's user"),
				"timeout_seconds": map[string]any{"type": "integer", "description": "at most 600; 60 by default"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Agent          string
					Command        string
					TimeoutSeconds int `json:"timeout_seconds"`
				}
				if err := decodeArgs(args, &in); err != nil {
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
			Name: "copy_to_agent",
			Description: "Copy a file or directory into an agent's machine, up to 256 MiB: from another agent's machine as it is " +
				"there, committed or not (from), or else from one of the user's projects' repositories on this machine, such as one " +
				"they @mentioned (what .gitignore excludes stays behind). Work that should last goes through git instead.",
			Schema: object([]string{"to", "path"}, map[string]any{
				"to":   str("the agent to copy to"),
				"path": str("with from, relative to its worktree or absolute; without, an absolute path inside one of the user's projects"),
				"from": str("the agent to copy from; leave it out to copy from the user's projects"),
				"into": str("the directory to put it in, relative to the worktree or absolute: the same place as path with from, the " +
					"worktree's root without. A directory arrives under its own name"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ To, Path, From, Into string }
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				if in.From == "" {
					res, err := c.CopyToAgent(ctx, api.LeadHostCopyRequest{Path: in.Path, To: in.To, Into: in.Into})
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("Copied %s to %s, into %s (%s).", in.Path, in.To, cmp.Or(in.Into, "its worktree"), sizeOf(res.Bytes)), nil
				}
				res, err := c.CopyBetweenAgents(ctx, api.LeadCopyRequest{From: in.From, Path: in.Path, To: in.To, Into: in.Into})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Copied %s from %s to %s, into %s (%s).", in.Path, in.From, in.To, cmp.Or(in.Into, "the same place"), sizeOf(res.Bytes)), nil
			},
		},
		{
			Name: "retire_agent",
			Description: "Free what a finished agent holds; its work stays on its branch. stop shuts its machine down (back in " +
				"seconds); destroy removes machine and worktree and keeps it as a past agent, chat and media included. An agent " +
				"still working (a background command or monitor counts) or with uncommitted work is left alone unless you force it.",
			Schema: object([]string{"agent"}, map[string]any{
				"agent": agent,
				"how":   choiceOf("stop by default", "stop", "pause", "destroy"),
				"force": map[string]any{"type": "boolean", "description": "even though it is working or has uncommitted work"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Agent, How string
					Force      bool
				}
				if err := decodeArgs(args, &in); err != nil {
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
			Name: "switch_agent_account",
			Description: "Move an agent to another account of its AI tool, keeping its conversation, when its account hit its usage " +
				"limit and another has room (list_resources accounts). One paused on the limit carries on at once. Refused mid-turn.",
			Schema: object([]string{"agent"}, map[string]any{
				"agent":   agent,
				"account": str("which of its tool's accounts; the project's own when left out"),
				"opencode_accounts": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
					"description": "for an OpenCode agent instead: provider → account; providers left out keep theirs"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Agent, Account   string
					OpenCodeAccounts map[string]string `json:"opencode_accounts"`
				}
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				res, err := c.SwitchAgentAccount(ctx, in.Agent, api.AgentAccountRequest{Account: in.Account, OpenCodeAccounts: in.OpenCodeAccounts})
				if err != nil {
					return "", err
				}
				return describeAccountSwitch(res), nil
			},
		},
		checkpointsTool(ctx, c, agent),
	}
	tools = append(tools, memoryTools(ctx, c)...)
	return append(tools,
		skillsTool(ctx, c),
		messageProjectTool(ctx, c),
		questionsTool(ctx, c),
		resourcesTool(ctx, c),
		scheduleTool(ctx, c),
	)
}

// agentDiff is read_agent's diff: whole when it is small, else clipped to
// what it touches and how it starts (D87).
func agentDiff(ctx context.Context, c *api.Client, agent string, stat bool, path string) (string, error) {
	var paths []string
	if path != "" {
		paths = []string{path}
	}
	diff, err := c.AgentDiff(ctx, agent, stat, paths...)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(diff) == "" {
		if path != "" {
			return fmt.Sprintf("%s has changed nothing in %s.", agent, path), nil
		}
		return agent + " has changed nothing.", nil
	}
	if stat || len(diff) <= maxChatDiff {
		return diff, nil
	}
	summary, err := c.AgentDiff(ctx, agent, true, paths...)
	if err != nil {
		summary = ""
	}
	return clipDiff(agent, diff, summary), nil
}

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
		if account := cmp.Or(f.ClaudeAccount, f.CodexAccount, f.CursorAccount, describeAccountMap(f.OpenCodeAccounts)); account != "" {
			fmt.Fprintf(&b, ", account: %s", account)
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
// describeToolAccounts lists the Codex, OpenCode and Cursor accounts, and
// says which this project uses; nothing when there are none.
func describeToolAccounts(accounts []api.ToolAccount, p api.Project) string {
	if len(accounts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nThe other tools' accounts, for an agent that runs them:\n")
	for _, a := range accounts {
		name := a.Name
		own := map[string]string{"codex": p.CodexAccount, "cursor": p.CursorAccount, "opencode": p.OpenCodeAccounts[a.Provider]}[a.Tool]
		label := map[string]string{"codex": "Codex", "opencode": "OpenCode", "cursor": "Cursor"}[a.Tool]
		if a.Provider != "" {
			label += " (" + a.Provider + ")"
		}
		var tags []string
		if a.Default {
			tags = append(tags, "machine default")
		}
		if own == a.Name || own == "" && a.Default {
			tags = append(tags, "this project's account")
		}
		if a.Expired {
			tags = append(tags, "expired: can't be used")
		}
		fmt.Fprintf(&b, "- %s: %s", label, name)
		if len(tags) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(tags, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

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
		if s.LeadManaged {
			where += "; managed by you: you keep it fresh, and may fulfil agents' requests for it (questions fulfil)"
		}
		fmt.Fprintf(&b, "- $%s — %s\n", s.Name, where)
	}
	return b.String()
}

// leadManagedSecrets are the names of the project's secrets its lead keeps
// fresh.
func leadManagedSecrets(secrets []api.Secret) map[string]bool {
	managed := map[string]bool{}
	for _, s := range secrets {
		if s.LeadManaged && s.Scope == "project" {
			managed[s.Name] = true
		}
	}
	return managed
}

// describeFulfilled is questions fulfil's answer: who has the secret now,
// and how old the value they got is. Never the value.
func describeFulfilled(out api.FulfilCredentialResponse) string {
	q := out.Question
	text := fmt.Sprintf("Fulfilled: %s has $%s now, the value stored %s.", q.Agent, q.SecretName, out.ValueSetAt.Format(time.RFC3339))
	if out.ValueSetAt.Before(q.CreatedAt) {
		text += " That value was stored before the agent asked: if it asked because the value had expired, refresh it " +
			"(agentbox secrets set … --value-stdin) and tell the agent to run `. ~/.config/agentbox/env` again."
	}
	return text
}

// describeIssues is list_resources' issues: one line per open issue.
func describeIssues(in api.ProjectIssues) string {
	switch {
	case in.NoOrigin:
		return "This project's checkout has no origin remote, so it has no GitHub issues to read."
	case in.NonGitHubRemote != "":
		return fmt.Sprintf("This project's origin (%s) isn't a GitHub repository, so it has no GitHub issues to read.", in.NonGitHubRemote)
	case in.GitHubError != nil:
		return "Couldn't read this project's GitHub issues: " + in.GitHubError.Message
	}
	var b strings.Builder
	pickup := "off: issues are only handed to agents when you or the user do"
	if in.PickUpIssues {
		pickup = "on: open issues labelled \"" + in.Label + "\" get an agent by themselves, a few every five minutes"
	}
	fmt.Fprintf(&b, "Open issues of %s (%d). Picking up labelled issues is %s.\n", in.GitHub, len(in.Issues), pickup)
	for _, is := range in.Issues {
		fmt.Fprintf(&b, "- #%d %s", is.Number, is.Title)
		if len(is.Labels) > 0 {
			names := make([]string, len(is.Labels))
			for i, l := range is.Labels {
				names[i] = l.Name
			}
			fmt.Fprintf(&b, " [%s]", strings.Join(names, ", "))
		}
		if is.Comments > 0 {
			fmt.Fprintf(&b, ", %d comment(s)", is.Comments)
		}
		if h := is.Handed; h != nil {
			switch {
			case h.Agent == "":
				fmt.Fprintf(&b, "; its agent is being made (job %s)", h.Job)
			case h.Retired:
				fmt.Fprintf(&b, "; was handed to %s, since retired", h.Agent)
			default:
				fmt.Fprintf(&b, "; with %s (%s)", h.Agent, h.Status)
			}
			if pr := h.PullRequest; pr != nil {
				fmt.Fprintf(&b, ", pull request #%d %s", pr.Number, pr.State)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// describeConnectors is list_resources' connectors.
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

// maxChatDiff is the most of a diff read_agent hands the chat whole (D87).
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
	fmt.Fprintf(&b, "\nThe start of it:\n\n%s\n[cut here: read one file or directory with read_agent(%q, show diff, path)]", head, agent)
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

func describeQuestions(questions []api.Question, leadManaged map[string]bool) string {
	if len(questions) == 0 {
		return "No agent is waiting on you."
	}
	var b strings.Builder
	for _, q := range questions {
		if q.Kind == api.CredentialSecret && leadManaged[q.SecretName] {
			fmt.Fprintf(&b, "- id %s, from %s: asking for %s, which is managed by you: refresh it if needed, then questions fulfil\n  %s\n",
				q.ID, q.Agent, credentialWanted(q), q.Question)
			continue
		}
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

// describeResolved is memory_feedback's answer for a memory resolved.
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

// describeSkills lists skills for skills list, one a line.
func describeSkills(list []api.Skill) string {
	if len(list) == 0 {
		return "There are no skills yet. skills create writes one."
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

// describeSkill is skills read's answer: SKILL.md, then the other files' names.
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

// describeProjectThreads lists a project's threads with other projects' leads.
func describeProjectThreads(threads []api.ProjectThread) string {
	if len(threads) == 0 {
		return "No threads with other projects' leads yet."
	}
	var b strings.Builder
	for _, t := range threads {
		about := "message"
		if t.Kind == state.ThreadAlign {
			about = fmt.Sprintf("alignment on %q", t.Topic)
		}
		fmt.Fprintf(&b, "- %s with %s: %s, %s, %d %s", t.ID, t.Project, about, t.Status, t.Messages, plural(t.Messages, "message", "messages"))
		if t.Status == state.ThreadSettled {
			fmt.Fprintf(&b, "\n  agreed: %s", t.Summary)
		} else if t.Last != nil {
			waits := ""
			if t.Last.From == t.Project && t.Last.ExpectsReply {
				waits = " (waits on your reply)"
			}
			fmt.Fprintf(&b, "\n  last, from %s%s: %s", t.Last.From, waits, oneLine(t.Last.Text))
		}
		b.WriteString("\n")
	}
	return b.String()
}
