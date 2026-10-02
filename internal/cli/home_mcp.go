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

// homeTools are the Home chat's tools: the user's main chat, across every
// project, so each tool names the project it acts on. Its socket serves only
// what these need (homeRoutes in internal/daemon), at the user's own paths.
func homeTools(ctx context.Context, c *api.Client) []mcp.Tool {
	decode := func(args json.RawMessage, into any) error {
		if len(args) == 0 {
			return nil
		}
		return json.Unmarshal(args, into)
	}
	project := str("the project's name, exactly as list_projects gives it")
	return []mcp.Tool{
		{
			Name:        "list_projects",
			Description: "List the user's projects: each one's name, folder, branch, and how many agents it has.",
			Run: func(json.RawMessage) (string, error) {
				projects, err := c.Projects(ctx)
				if err != nil {
					return "", err
				}
				return describeProjects(ctx, c, projects), nil
			},
		},
		{
			Name: "list_agents",
			Description: "List one project's agents: what each one is for, what it is doing now, how much it has " +
				"changed, its pull request, and whether it has finished.",
			Schema: object([]string{"project"}, map[string]any{"project": project}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Project string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				fleet, err := c.Fleet(ctx, in.Project)
				if err != nil {
					return "", err
				}
				return describeFleet(fleet), nil
			},
		},
		{
			Name:        "read_agent",
			Description: "Read what one of a project's agents has been doing: its conversation, newest last.",
			Schema: object([]string{"project", "agent"}, map[string]any{
				"project": project,
				"agent":   str("its name, like agent-03"),
				"last":    map[string]any{"type": "integer", "description": "how many entries to read; 20 by default"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Project, Agent string
					Last           int
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if in.Last <= 0 {
					in.Last = 20
				}
				thread, err := c.HomeAgentChat(ctx, in.Project, in.Agent)
				if err != nil {
					return "", err
				}
				return describeThread(in.Agent, thread, in.Last), nil
			},
		},
		{
			Name: "create_agent",
			Description: "Create an agent in a project and give it a task. It gets its own machine and its own " +
				"branch, and starts on the model new agents start on there. Write the " +
				"task directly: what to do, what done looks like, what to leave alone. When the work needs the " +
				"project's judgement — splitting it up, following it through — use tell_lead instead.",
			Schema: object([]string{"project", "title", "task"}, map[string]any{
				"project": project,
				"title":   str("a short name the user will recognise in the sidebar, like \"Reminders page\""),
				"task":    str("what to do, directly: usually a few lines"),
				"branch":  str("its branch, after the project's prefix: short lowercase kebab-case naming the work, like \"fix-login-redirect\""),
				"size":    sizeParam,
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Project, Title, Task, Branch, Size string
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Project) == "" {
					return "", errors.New("name the project to create the agent in")
				}
				if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Task) == "" {
					return "", errors.New("an agent needs a title and a task")
				}
				autonomous := true
				job, err := c.CreateAgent(ctx, api.CreateAgentRequest{
					Project: in.Project, Title: in.Title, Task: in.Task, Branch: in.Branch,
					AI: "claude", Autonomous: &autonomous, Size: in.Size,
				})
				if err != nil {
					return "", err
				}
				if job.Kind == "queue" {
					var ag api.Agent
					_ = json.Unmarshal(job.Result, &ag)
					return queuedLine(in.Title, in.Project+"'s", ag), nil
				}
				return fmt.Sprintf("Creating %q in %s; it starts on the task by itself. Job %s.", in.Title, in.Project, job.ID), nil
			},
		},
		{
			Name: "tell_lead",
			Description: "Pass something to a project's own chat, its lead, which wakes and acts on it: the usual way " +
				"to get work done in a project, since its lead knows it, briefs its agents and follows them. The " +
				"user sees the message in that project's chat too.",
			Schema: object([]string{"project", "message"}, map[string]any{
				"project": project,
				"message": str("what the user wants done or known, in their terms"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Project, Message string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if err := c.HomeTellLead(ctx, in.Project, in.Message); err != nil {
					return "", err
				}
				return fmt.Sprintf("Told %s's chat; it is working on it.", in.Project), nil
			},
		},
		{
			Name: "search_memory",
			Description: "Search what a project remembers: its memories, the events its agents recorded and the " +
				"reports they filed. Exact words work: a filename, a port, an error string. Leave project out to " +
				"search every project.",
			Schema: object([]string{"query"}, map[string]any{
				"project": str("the project to search, as list_projects names it; every project when left out"),
				"query":   str("what you are looking for"),
				"limit":   map[string]any{"type": "integer", "description": "how many of each kind to return per project; 10 by default"},
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct {
					Project, Query string
					Limit          int
				}
				if err := decode(args, &in); err != nil {
					return "", err
				}
				if in.Limit <= 0 {
					in.Limit = 10
				}
				if in.Project != "" {
					results, err := c.ProjectMemory(in.Project).Search(ctx, in.Query, in.Limit)
					if err != nil {
						return "", err
					}
					return describeSearch(in.Query, results), nil
				}
				projects, err := c.Projects(ctx)
				if err != nil {
					return "", err
				}
				var b strings.Builder
				for _, p := range projects {
					results, err := c.ProjectMemory(p.Name).Search(ctx, in.Query, in.Limit)
					if err != nil || len(results.Memories)+len(results.Events)+len(results.Reports) == 0 {
						continue
					}
					fmt.Fprintf(&b, "## %s\n\n%s\n\n", p.Name, strings.TrimSpace(describeSearch(in.Query, results)))
				}
				if b.Len() == 0 {
					return fmt.Sprintf("No project remembers anything about %q.", in.Query), nil
				}
				return strings.TrimSpace(b.String()), nil
			},
		},
		{
			Name: "add_project",
			Description: "Add a project: a git repository already on this machine (path), or one cloned from a git " +
				"URL first (url), into ~/src/<name> unless path says where. A folder must be the top of a " +
				"repository with at least one commit.",
			Schema: object(nil, map[string]any{
				"path": str("the repository's folder; with url, the folder to clone into"),
				"url":  str("a git URL to clone, like https://github.com/owner/repo"),
				"name": str("the project's name: lowercase letters, digits and hyphens; the folder's or repository's name by default"),
			}),
			Run: func(args json.RawMessage) (string, error) {
				var in struct{ Path, URL, Name string }
				if err := decode(args, &in); err != nil {
					return "", err
				}
				p, err := c.HomeAddProject(ctx, api.HomeAddProjectRequest{Path: in.Path, URL: in.URL, Name: in.Name})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Added %s, at %s, on %s.", p.Name, p.Root, p.Branch), nil
			},
		},
	}
}

// describeProjects is list_projects' answer: one line a project, with what
// its agents are doing.
func describeProjects(ctx context.Context, c *api.Client, projects []api.Project) string {
	if len(projects) == 0 {
		return "There are no projects yet. Add one with add_project."
	}
	var b strings.Builder
	for _, p := range projects {
		fmt.Fprintf(&b, "- %s: %s, on %s", p.Name, p.Root, dash(p.Branch))
		if fleet, err := c.Fleet(ctx, p.Name); err == nil {
			running := 0
			for _, f := range fleet.Agents {
				if f.State == "running" {
					running++
				}
			}
			fmt.Fprintf(&b, "; %d agents, %d running", len(fleet.Agents), running)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
