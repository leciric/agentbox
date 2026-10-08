package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"agentbox/internal/api"
	"agentbox/internal/skills"
)

// newSkillsCmd manages the Agent Skills AgentBox installs into agents.
func newSkillsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "skills",
		Aliases: []string{"skill"},
		Short:   "Give agents skills: folders with a SKILL.md their AI tool can use",
		Long: `A skill is a folder holding a SKILL.md: a name and a description in its
frontmatter, then instructions, and any scripts or references it points to.
AgentBox stores it and installs it into every agent and lead that gets it,
where Claude Code, Codex, OpenCode and Cursor read skills. Running agents get a
change straight away.

A skill is on or off AgentBox-wide, and a project may say otherwise either way:

  agentbox skills import                    # pick from your own AI tools' skills
  agentbox skills import ~/skills/review    # a folder, or a folder of skills
  agentbox skills import https://github.com/anthropics/skills/tree/main/skills/pdf
  agentbox skills add review-pr < SKILL.md
  agentbox skills disable review-pr --project pawly
  agentbox skills inherit review-pr --project pawly

In a chat, type / and pick a skill, or write $name.`,
	}
	cmd.AddCommand(newSkillsListCmd(a), newSkillsShowCmd(a), newSkillsAddCmd(a), newSkillsImportCmd(a),
		newSkillsSwitchCmd(a, "enable", true), newSkillsSwitchCmd(a, "disable", false), newSkillsInheritCmd(a), newSkillsRemoveCmd(a))
	return cmd
}

func newSkillsListCmd(a *app) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the skills, and whether they're on",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			list, err := c.Skills(cmd.Context(), project)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(list) == 0 {
				_, _ = fmt.Fprintln(out, "No skills yet. Import some with `agentbox skills import`.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			if project != "" {
				_, _ = fmt.Fprintln(tw, "NAME\tIN "+strings.ToUpper(project)+"\tAGENTBOX-WIDE\tDESCRIPTION")
			} else {
				_, _ = fmt.Fprintln(tw, "NAME\tAGENTBOX-WIDE\tPROJECTS\tDESCRIPTION")
			}
			for _, sk := range list {
				if project != "" {
					here := onOffWord(sk.Active != nil && *sk.Active)
					if _, ok := sk.Overrides[project]; ok {
						here += " (this project)"
					}
					_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sk.Name, here, onOffWord(sk.Enabled), clip(oneLine(sk.Description), 60))
					continue
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sk.Name, onOffWord(sk.Enabled), overridesText(sk.Overrides), clip(oneLine(sk.Description), 60))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "say whether this project's agents get each skill")
	return cmd
}

func overridesText(o map[string]bool) string {
	var parts []string
	for p, on := range o {
		parts = append(parts, p+" "+onOffWord(on))
	}
	if len(parts) == 0 {
		return "-"
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}

func onOffWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}


func newSkillsShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name> [file]",
		Short: "Print a skill's SKILL.md, or another of its files",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			sk, err := c.Skill(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			want := skills.File
			if len(args) == 2 {
				want = args[1]
			}
			for _, f := range sk.Files {
				if f.Path != want {
					continue
				}
				if f.Binary {
					return fmt.Errorf("%s is a binary file of %d bytes", f.Path, f.Size)
				}
				_, err := io.WriteString(cmd.OutOrStdout(), f.Content)
				return err
			}
			var paths []string
			for _, f := range sk.Files {
				paths = append(paths, f.Path)
			}
			return fmt.Errorf("skill %s has no %s: it has %s", args[0], want, strings.Join(paths, ", "))
		},
	}
}

func newSkillsAddCmd(a *app) *cobra.Command {
	var file, project string
	var off bool
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a skill, or replace its SKILL.md, from a file or stdin",
		Long: `Reads SKILL.md from --file or stdin; with neither, starts one from a template.
The name in its frontmatter is set to <name>. A skill that already exists keeps
its other files and where it is on.

  agentbox skills add review-pr --file ./SKILL.md
  agentbox skills add review-pr --project pawly < SKILL.md   # on in pawly only`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := skills.ValidateName(name); err != nil {
				return err
			}
			var content []byte
			var err error
			switch {
			case file != "":
				content, err = os.ReadFile(file)
			case !term.IsTerminal(int(os.Stdin.Fd())):
				content, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), skills.MaxFileBytes+1))
			default:
				content = []byte(skills.Template(name, ""))
			}
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			req := api.SaveSkillRequest{Content: string(content), Project: project}
			if off {
				f := false
				req.Enabled = &f
			}
			sk, err := c.SaveSkill(cmd.Context(), name, req)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved skill %s: %s\n", sk.Name, clip(oneLine(sk.Description), 80))
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "the SKILL.md to read")
	cmd.Flags().StringVarP(&project, "project", "p", "", "a new skill is on in this project only")
	cmd.Flags().BoolVar(&off, "off", false, "a new skill starts off AgentBox-wide")
	return cmd
}

func newSkillsImportCmd(a *app) *cobra.Command {
	var project string
	var all, dryRun bool
	cmd := &cobra.Command{
		Use:   "import [folder | SKILL.md | git URL] [name...]",
		Short: "Import skills from a folder, a git repository, or your AI tools",
		Long: `With no source, lists the skills your own AI tools have: Claude Code's
(~/.claude/skills and its plugins'), Codex's, OpenCode's, Cursor's and
~/.agents/skills. With a source, lists the skills in it. Then imports the ones
named, or with --all every one that can be.

  agentbox skills import                              # what your tools have
  agentbox skills import "" review-pr --all           # all of them
  agentbox skills import ~/code/team-skills --all
  agentbox skills import https://github.com/anthropics/skills pdf docx
  agentbox skills import github.com/o/r#skills/deploy

A git URL may point into the repository: GitHub's /tree/<branch>/<folder>
links work, and so does #<folder> after any URL. The folder is read on the
machine AgentBox runs on.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			source := ""
			if len(args) > 0 {
				source = args[0]
				if source != "" && !skills.IsGitURL(source) && !filepath.IsAbs(source) && !strings.HasPrefix(source, "~/") {
					if abs, err := filepath.Abs(source); err == nil {
						source = abs
					}
				}
			}
			names := args[min(len(args), 1):]
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(names) == 0 && !all || dryRun {
				found, err := c.ScanSkills(cmd.Context(), source)
				if err != nil {
					return err
				}
				if len(found) == 0 {
					_, _ = fmt.Fprintln(out, "No skills found there.")
					return nil
				}
				tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
				_, _ = fmt.Fprintln(tw, "NAME\tFROM\tFILES\tDESCRIPTION")
				for _, f := range found {
					desc := clip(oneLine(f.Description), 60)
					if f.Problem != "" {
						desc = "can't import: " + f.Problem
					} else if f.Exists {
						desc = "(replaces yours) " + desc
					}
					from := f.Origin
					if f.Plugin != "" {
						from += " " + f.Plugin
					}
					_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", f.Name, from, f.Files, desc)
				}
				if err := tw.Flush(); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(out, "\nImport some by name after the source, or all of them with --all.")
				return nil
			}
			if all {
				names = nil
			}
			list, err := c.ImportSkills(cmd.Context(), api.ImportSkillsRequest{Source: source, Names: names, Project: project})
			if err != nil {
				return err
			}
			for _, sk := range list {
				_, _ = fmt.Fprintf(out, "Imported %s\n", sk.Name)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "import them on in this project only")
	cmd.Flags().BoolVar(&all, "all", false, "import every skill found")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only list what would be imported")
	return cmd
}

func newSkillsSwitchCmd(a *app, verb string, on bool) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   verb + " <name>...",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " skills AgentBox-wide, or in one project with --project",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			for _, name := range args {
				if project != "" {
					_, err = c.SetSkillOverride(cmd.Context(), project, name, onOffWord(on))
				} else {
					_, err = c.SetSkillEnabled(cmd.Context(), name, on)
				}
				if err != nil {
					return err
				}
				where := "AgentBox-wide"
				if project != "" {
					where = "in " + project
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is %s %s\n", name, onOffWord(on), where)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "only in this project, whatever AgentBox-wide says")
	return cmd
}

func newSkillsInheritCmd(a *app) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "inherit <name>... --project <project>",
		Short: "Drop a project's override, so it follows the AgentBox-wide switch",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				return errors.New("which project? Pass --project")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			for _, name := range args {
				sk, err := c.SetSkillOverride(cmd.Context(), project, name, "")
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s follows AgentBox-wide in %s: %s\n", name, project, onOffWord(sk.Enabled))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "the project")
	return cmd
}

func newSkillsRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>...",
		Aliases: []string{"rm", "delete"},
		Short:   "Delete skills, from every agent too",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			for _, name := range args {
				if err := c.RemoveSkill(cmd.Context(), name); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", name)
			}
			return nil
		},
	}
}
