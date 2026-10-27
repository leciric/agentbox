package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/mcp"
)

// decodeArgs reads a tool call's arguments; a call with none leaves into as
// it was.
func decodeArgs(args json.RawMessage, into any) error {
	if len(args) == 0 {
		return nil
	}
	return json.Unmarshal(args, into)
}

// action is one of the things a family tool does (actionTool).
type action struct {
	name string
	run  func(args json.RawMessage) (string, error)
}

// actionTool is one tool for a family of operations on one thing — the notes,
// the skills, the schedules — told apart by its required action. Every tool's
// schema is sent to the model again with each call it makes, and a family's
// operations share most of their parameters, so one schema costs a fraction
// of one per operation. help says what each action does, and which of the
// parameters it takes.
func actionTool(name, description, help string, props map[string]any, actions ...action) mcp.Tool {
	names := make([]string, len(actions))
	for i, a := range actions {
		names[i] = a.name
	}
	props["action"] = choiceOf(help, names...)
	return mcp.Tool{
		Name: name, Description: description,
		Schema: object([]string{"action"}, props),
		Run: func(args json.RawMessage) (string, error) {
			var in struct{ Action string }
			if err := decodeArgs(args, &in); err != nil {
				return "", err
			}
			for _, a := range actions {
				if a.name == in.Action {
					return a.run(args)
				}
			}
			return "", fmt.Errorf("%s's action is %q: it is one of %s", name, in.Action, strings.Join(names, ", "))
		},
	}
}

// checkpointsTool lists an agent's checkpoints, rolls it back to one, or forks
// a new agent from one.
func checkpointsTool(ctx context.Context, c *api.Client, agent map[string]any) mcp.Tool {
	type args struct{ Agent, Checkpoint, Title string }
	return actionTool("checkpoints",
		"An agent's checkpoints: one at the end of each of its turns, its files (uncommitted ones too) and chat as they were then.",
		"list: oldest first, with saved-… ones holding what it had before a rollback. roll_back: put the agent's files and chat "+
			"back to checkpoint, saving what it had first; it undoes its work, so only when the user asked, and never mid-turn. "+
			"fork: a new agent from checkpoint, or from the agent as it is now without one; then tell_agent it what to do",
		map[string]any{
			"agent":      agent,
			"checkpoint": str("like turn-4, or just the turn's number"),
			"title":      str("fork: the new agent's title; the source's with \" (fork)\" by default"),
		},
		action{"list", func(raw json.RawMessage) (string, error) {
			var in args
			if err := decodeArgs(raw, &in); err != nil {
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
		}},
		action{"roll_back", func(raw json.RawMessage) (string, error) {
			var in args
			if err := decodeArgs(raw, &in); err != nil {
				return "", err
			}
			res, err := c.RollbackAgent(ctx, in.Agent, in.Checkpoint)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s is back at %s. What it had is saved as %s, which fork can start from.", in.Agent, in.Checkpoint, res.Saved.ID), nil
		}},
		action{"fork", func(raw json.RawMessage) (string, error) {
			var in args
			if err := decodeArgs(raw, &in); err != nil {
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
		}},
	)
}

// memoryTools are what the lead keeps: the project's notes, its memory, and
// where it stands.
func memoryTools(ctx context.Context, c *api.Client) []mcp.Tool {
	memory := c.LeadMemory()
	return []mcp.Tool{
		notesTool(ctx, c),
		{
			Name: "search_memory",
			Description: "Search what this project remembers: memories, its agents' events and their reports. Search before you " +
				"answer from assumption or brief an agent. Exact words work: a filename, a port, an error string. A replaced memory never comes back.",
			Schema: object([]string{"query"}, map[string]any{
				"query": str("words, a filename, a port, an error message"),
				"limit": map[string]any{"type": "integer", "description": "how many of each kind; 20 by default"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Query string
					Limit int
				}
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				results, err := memory.Search(ctx, in.Query, in.Limit)
				if err != nil {
					return "", err
				}
				return describeSearch(in.Query, results), nil
			},
		},
		{
			Name: "remember",
			Description: "Write down one fact this project should still know in months: how it is built, a convention, a decision " +
				"and why, a gotcha somebody lost an hour to, a known problem. Not what is happening now (project_state), nor what " +
				"every agent must be told up front (a note, which is in every brief; a memory is only searched). For a problem, " +
				"name the pull requests (#234), branches and question ids it waits on: it closes once they are all done.",
			Schema: object([]string{"title"}, map[string]any{
				"title":   str("one line somebody would recognise it by, like \"The API listens on port 7777\""),
				"content": str("the fact in full, and what to do about it"),
				"kind": choiceOf("project (a lasting fact, the default), decision (a choice and its reason), discovery (found the hard way), "+
					"issue (a problem nobody has fixed) or episodic (an event that explains later ones)",
					"project", "decision", "discovery", "issue", "episodic"),
				"importance": map[string]any{"type": "integer", "description": "1 to 5, 3 by default; it decides what a full brief keeps. 5 is rare: nobody should work here without it"},
				"supersedes": str("the id of the memory this corrects, from search_memory: the old one stops coming back"),
				"scope": choiceOf("all keeps it for every project, only when the user asks for that; project by default",
					api.MemoryScopeProject, api.MemoryScopeAll),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Title, Content, Kind, Supersedes, Scope string
					Importance                              int
				}
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Title) == "" {
					return "", errors.New("a memory needs a title: one line somebody would recognise it by")
				}
				m, err := memory.AddMemory(ctx, api.AddMemoryRequest{
					Kind: in.Kind, Title: in.Title, Content: in.Content,
					Importance: in.Importance, SupersedesID: in.Supersedes, Scope: in.Scope,
				})
				if err != nil {
					return "", err
				}
				return describeRemembered(m, in.Supersedes), nil
			},
		},
		leadFeedbackTool(ctx, memory),
		{
			Name: "project_state",
			Description: "Where this project stands: its working memory (what it is doing now), the problems nobody has fixed, and " +
				"its agents' latest reports. Read it before deciding what to do next. Pass any field to change the working memory, which " +
				"you and every agent read first, so keep it true today: the rest stay as they were, and an empty value clears one.",
			Schema: object(nil, map[string]any{
				"goal":         str("what the project is trying to achieve now"),
				"current_task": str("what is being worked on"),
				"active_agents": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "the agents on it, like [\"agent-01\"]"},
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
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				patch := api.WorkingMemoryPatch{Goal: in.Goal, CurrentTask: in.CurrentTask,
					ActiveAgents: in.ActiveAgents, Blockers: in.Blockers, Notes: in.Notes}
				if patch == (api.WorkingMemoryPatch{}) {
					return projectState(ctx, c), nil
				}
				w, err := memory.SetWorkingMemory(ctx, patch)
				if err != nil {
					return "", err
				}
				return "Updated. What the project is doing now:\n\n" + describeWorking(w), nil
			},
		},
	}
}

// leadFeedbackTool is memory_feedback as agents have it, and one verdict more
// that only the lead gives: resolved, which closes a memory that is simply
// over (memory.Store.ResolveMemory), as resolve_memory did.
func leadFeedbackTool(ctx context.Context, memory *api.MemoryClient) mcp.Tool {
	tool := memoryFeedbackTool(ctx, memory)
	tool.Description = "Say that a memory you were handed, in your brief or from search_memory, is wrong (untrue), stale (no " +
		"longer true) or helpful (it saved you the work), as soon as you find out. wrong and stale drop it to the bottom of every " +
		"search and brief, and stale closes an open problem; then remember what is true. resolved closes one that is simply over, " +
		"with nothing to replace it (the bug was fixed): it stops coming back from searches."
	tool.Schema = object([]string{"memory", "verdict"}, map[string]any{
		"memory":  str("its id from search_memory, or its title exactly as you were shown it; resolved takes the id"),
		"verdict": choiceOf("what you found", "wrong", "stale", "helpful", "resolved"),
		"why":     str("what you found, in a line: the file, command or pull request that shows it; required unless helpful"),
	})
	feedback := tool.Run
	tool.Run = func(args json.RawMessage) (string, error) {
		var in api.MemoryFeedbackRequest
		if err := decodeArgs(args, &in); err != nil {
			return "", err
		}
		if in.Verdict != "resolved" {
			return feedback(args)
		}
		if strings.TrimSpace(in.Memory) == "" {
			return "", errors.New("say which memory to close, by its id from search_memory")
		}
		m, err := memory.ResolveMemory(ctx, in.Memory, in.Why)
		if err != nil {
			return "", err
		}
		return describeResolved(m), nil
	}
	return tool
}

// notesTool reads and changes the project's notes, the standing brief every
// agent is handed, and answers the brief's offers to make a memory one.
func notesTool(ctx context.Context, c *api.Client) mcp.Tool {
	type args struct{ Match, Text, ID string }
	read := func(raw json.RawMessage) (args, error) {
		var in args
		return in, decodeArgs(raw, &in)
	}
	return actionTool("notes",
		"This project's notes: what every agent is told before it starts, written by the user and by you; your brief has them "+
			"as they were when this conversation began. Only standing rules belong: a convention, a command the README lacks, a "+
			"decision the user made. Every note is in every agent's brief, so a gotcha with an error, a version or a filename is "+
			"a memory instead (remember), which search_memory finds when needed. Tell the user which you used, and why. edit and "+
			"remove are for when the user asks you to: the notes are theirs, not yours to tidy.",
		"read: the notes now. append: text as one short dated entry. edit: the one entry match quotes now says text, keeping its "+
			"date. remove: take out the entry match quotes. A quote that matches no entry or several changes nothing and says what "+
			"it matched. promote: make memory id (offered in your brief) a note, worded as text, and stop serving the memory. "+
			"dismiss: keep memory id a memory, never offered again",
		map[string]any{
			"text":  str("append, edit, promote: the note as every agent should read it, in a sentence"),
			"match": str("edit, remove: a quote of the entry as it reads now"),
			"id":    str("promote, dismiss: the memory's id"),
		},
		action{"read", func(json.RawMessage) (string, error) {
			n, err := c.ProjectNotes(ctx)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(n.Text) == "" {
				return "This project has no notes yet. append starts them.", nil
			}
			return n.Text, nil
		}},
		action{"append", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Text) == "" {
				return "", errors.New("a note needs some text")
			}
			if _, err := c.AppendProjectNote(ctx, in.Text); err != nil {
				return "", err
			}
			return "Written down: new agents get it in their brief, running ones from their next session.", nil
		}},
		action{"edit", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Match) == "" {
				return "", errors.New("name the note to change by quoting it in match")
			}
			if strings.TrimSpace(in.Text) == "" {
				return "", errors.New("an edited note needs some text: remove is how one goes")
			}
			change, err := c.EditProjectNote(ctx, in.Match, in.Text)
			if err != nil {
				return "", err
			}
			return describeNoteChange(change, "Edited"), nil
		}},
		action{"remove", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Match) == "" {
				return "", errors.New("name the note to remove by quoting it in match")
			}
			change, err := c.RemoveProjectNote(ctx, in.Match)
			if err != nil {
				return "", err
			}
			return describeNoteChange(change, "Removed"), nil
		}},
		action{"promote", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
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
		}},
		action{"dismiss", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
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
		}},
	)
}

// skillsTool lists, reads, writes and switches the skills AgentBox installs
// into agents.
func skillsTool(ctx context.Context, c *api.Client) mcp.Tool {
	type args struct {
		Name, Content, State string
		Everywhere           bool
	}
	read := func(raw json.RawMessage) (args, error) {
		var in args
		return in, decodeArgs(raw, &in)
	}
	return actionTool("skills",
		"The skills AgentBox installs into agents and you: procedures an agent follows when a skill's description matches its "+
			"task. Write one when a procedure recurs across tasks or the user repeats an instruction, and tell the user in a line.",
		"list: each skill and where it is on. read: name's SKILL.md and its other files. create: a new skill, on in this "+
			"project unless everywhere; agents get it from their next session. edit: replace name's SKILL.md. switch: turn name "+
			"to state, here or everywhere. delete: remove name from AgentBox and every agent (to stop it here only, switch it "+
			"off). edit, delete and switching one off wait for the user's approval on a card in this chat; a refusal is an error",
		map[string]any{
			"name": str("the skill's name, lowercase-with-dashes like release-checklist"),
			"content": str("create, edit: the whole SKILL.md: YAML front matter with name and description (when to use it, " +
				"in a sentence the model matches tasks against), then the steps"),
			"state":      choiceOf("switch: on, off, or default to follow the AgentBox-wide switch (here only)", "on", "off", "default"),
			"everywhere": map[string]any{"type": "boolean", "description": "create, switch: in every project, for a general skill"},
		},
		action{"list", func(json.RawMessage) (string, error) {
			list, err := c.ProjectSkills(ctx)
			if err != nil {
				return "", err
			}
			return describeSkills(list), nil
		}},
		action{"read", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			detail, err := c.ProjectSkill(ctx, in.Name)
			if err != nil {
				return "", err
			}
			return describeSkill(detail), nil
		}},
		action{"create", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if _, err := c.CreateProjectSkill(ctx, api.LeadNewSkillRequest{Name: in.Name, Content: in.Content, Everywhere: in.Everywhere}); err != nil {
				return "", err
			}
			if in.Everywhere {
				return "Created " + in.Name + ", on in every project.", nil
			}
			return "Created " + in.Name + ", on in this project.", nil
		}},
		action{"edit", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if _, err := c.EditProjectSkill(ctx, in.Name, in.Content); err != nil {
				return "", err
			}
			return "The user approved it: " + in.Name + " is changed.", nil
		}},
		action{"switch", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
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
		}},
		action{"delete", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if err := c.RemoveProjectSkill(ctx, in.Name); err != nil {
				return "", err
			}
			return "The user approved it: " + in.Name + " is deleted.", nil
		}},
	)
}

// messageProjectTool is the lead's side of talking to other projects' leads
// (internal/daemon/projectmessages.go): a message, an alignment on a topic, a
// reply in a thread, settling an alignment, and the threads so far.
func messageProjectTool(ctx context.Context, c *api.Client) mcp.Tool {
	type args struct {
		Project, Message, Topic, Summary string
		ThreadID                         string `json:"thread_id"`
		ExpectsReply                     bool   `json:"expects_reply"`
	}
	read := func(raw json.RawMessage) (args, error) {
		var in args
		return in, decodeArgs(raw, &in)
	}
	return actionTool("message_project",
		"Talk to another of the user's projects' leads: a question about how it does something, a heads-up about a change "+
			"that touches it, an ask the user made (\"@other ask them …\"). Its lead is woken, and its reply comes into this chat. "+
			"Write for a colleague who hasn't read this conversation, in a few lines; point at paths rather than pasting files. "+
			"The first message between two projects waits for the user's approval.",
		"send: message to project's lead. align: start agreeing with project on topic, when both make the same change (one "+
			"API shape, one library); you exchange replies until one of you settles it. reply: answer in thread_id, when its "+
			"last message asked or it is an alignment (12 messages at most). settle: end alignment thread_id with summary, saved "+
			"to both projects' memory; then tell your agents what changes. threads: this project's threads, newest first",
		map[string]any{
			"project":       str("send, align: the other project's name, as @mentions write it"),
			"thread_id":     str("reply, settle: the thread's id, from the message you are answering"),
			"message":       str("send, align, reply: up to 4000 characters"),
			"topic":         str("align: what to agree on, in a few words"),
			"summary":       str("settle: what you agreed, in a few lines"),
			"expects_reply": map[string]any{"type": "boolean", "description": "send, reply: you need an answer; a heads-up gets none"},
		},
		action{"send", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			t, err := c.MessageProject(ctx, api.ProjectMessageRequest{Project: in.Project, Message: in.Message, ExpectsReply: in.ExpectsReply})
			if err != nil {
				return "", err
			}
			if in.ExpectsReply {
				return fmt.Sprintf("Sent to %s's lead (thread %s). Its reply will come into this chat; carry on meanwhile.", t.Project, t.ID), nil
			}
			return fmt.Sprintf("Sent to %s's lead (thread %s), as a heads-up: it won't reply.", t.Project, t.ID), nil
		}},
		action{"align", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Topic) == "" {
				return "", errors.New("no topic: say what to align on")
			}
			t, err := c.MessageProject(ctx, api.ProjectMessageRequest{Project: in.Project, Message: in.Message, Topic: in.Topic})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Asked %s's lead to align on %q (thread %s). Its answer will come into this chat.", t.Project, t.Topic, t.ID), nil
		}},
		action{"reply", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			t, err := c.ReplyProject(ctx, in.ThreadID, api.ProjectReplyRequest{Message: in.Message, ExpectsReply: in.ExpectsReply})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Replied to %s's lead (thread %s, %d messages).", t.Project, t.ID, t.Messages), nil
		}},
		action{"settle", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			t, err := c.SettleAlignment(ctx, in.ThreadID, in.Summary)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Settled the alignment with %s on %q; the summary is in both projects' memory.", t.Project, t.Topic), nil
		}},
		action{"threads", func(json.RawMessage) (string, error) {
			threads, err := c.ProjectThreads(ctx)
			if err != nil {
				return "", err
			}
			return describeProjectThreads(threads), nil
		}},
	)
}

// questionsTool is the questions this project's agents are waiting on.
func questionsTool(ctx context.Context, c *api.Client) mcp.Tool {
	type args struct{ ID, Text string }
	read := func(raw json.RawMessage) (args, error) {
		var in args
		return in, decodeArgs(raw, &in)
	}
	return actionTool("questions",
		"The questions this project's agents asked. An agent asking is blocked until it is answered, so deal with these first, "+
			"and answer yourself whenever you can: you have read the project and the user hasn't.",
		"list: the questions waiting. answer: answer question id with text, what the agent should do and why. escalate: pass "+
			"id to the user, only when it is their call (what to build, or something only they know), with text saying what you would suggest. "+
			"fulfil: give request id a secret managed by you its stored value, which you never see (your brief says how to refresh it); "+
			"every other credential request is the user's",
		map[string]any{
			"id":   str("answer, escalate, fulfil: the question's id"),
			"text": str("answer: what to do and why. escalate: why it is the user's call, and your suggestion"),
		},
		action{"list", func(json.RawMessage) (string, error) {
			questions, err := c.ProjectQuestions(ctx)
			if err != nil {
				return "", err
			}
			secrets, err := c.ProjectSecretNames(ctx)
			if err != nil {
				return "", err
			}
			return describeQuestions(questions, leadManagedSecrets(secrets)), nil
		}},
		action{"answer", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if _, err := c.LeadAnswerQuestion(ctx, in.ID, in.Text); err != nil {
				return "", err
			}
			return "Answered; the agent is carrying on.", nil
		}},
		action{"escalate", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if _, err := c.LeadEscalateQuestion(ctx, in.ID, in.Text); err != nil {
				return "", err
			}
			return "Passed to the user; say in your reply what you need from them. The agent waits.", nil
		}},
		action{"fulfil", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			out, err := c.LeadFulfilCredential(ctx, in.ID)
			if err != nil {
				return "", err
			}
			return describeFulfilled(out), nil
		}},
	)
}

// resourcesTool names what the project's agents may use besides their
// machine (accounts, secrets and connectors; never a token or a value), and
// the GitHub issues it may hand them.
func resourcesTool(ctx context.Context, c *api.Client) mcp.Tool {
	return mcp.Tool{
		Name: "list_resources",
		Description: "What this project's agents may use, never a value or token. accounts: the Claude Code accounts it may use, " +
			"how many agents hold each and its latest 5-hour and weekly usage, then the Codex, OpenCode and Cursor ones; spread agents " +
			"across them with create_agent's accounts. secrets: the names of the keys agents have as environment variables, to brief " +
			"one precisely (\"the Stripe key is in $STRIPE_SECRET_KEY\"), and which you manage. connectors: remote MCP servers like Notion, each one's " +
			"status and agents; the connected ones are your tools too (mcp__<name>__*). A missing secret or connector is the user's " +
			"to add, in the project's Settings: never ask for a value in this chat. issues: open GitHub issues and which agent has each.",
		Schema: object([]string{"kind"}, map[string]any{
			"kind": choiceOf("what to list", "accounts", "secrets", "connectors", "issues"),
		}),
		Run: func(args json.RawMessage) (string, error) {
			var in struct{ Kind string }
			if err := decodeArgs(args, &in); err != nil {
				return "", err
			}
			switch in.Kind {
			case "accounts":
				accounts, err := c.ProjectAccounts(ctx)
				if err != nil {
					return "", err
				}
				text := describeAccounts(accounts)
				tools, err := c.ProjectToolAccounts(ctx)
				if err != nil {
					return "", err
				}
				p, err := c.ProjectSelf(ctx)
				if err != nil {
					return "", err
				}
				return text + describeToolAccounts(tools, p), nil
			case "secrets":
				secrets, err := c.ProjectSecretNames(ctx)
				if err != nil {
					return "", err
				}
				return describeSecrets(secrets), nil
			case "connectors":
				found, err := c.ProjectConnectors(ctx)
				if err != nil {
					return "", err
				}
				return describeConnectors(found), nil
			case "issues":
				issues, err := c.LeadIssues(ctx)
				if err != nil {
					return "", err
				}
				return describeIssues(issues), nil
			}
			return "", fmt.Errorf("kind is %q: it is accounts, secrets, connectors or issues", in.Kind)
		},
	}
}
