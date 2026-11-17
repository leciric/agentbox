package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/transfer"
)

// passphraseEnv gives the passphrase without a prompt, for scripts.
const passphraseEnv = "AGENTBOX_EXPORT_PASSPHRASE"

func newExportCmd(a *app) *cobra.Command {
	var projects, categories, exclude, logins []string
	var bundle, secrets, noEncrypt, preview bool
	cmd := &cobra.Command{
		Use:   "export [file]",
		Short: "Export this AgentBox's projects, memory, chats and settings into one encrypted file",
		Long: `Writes this AgentBox's data into one file, to import into another AgentBox
with 'agentbox import'. Machines, images and worktrees aren't in it: they are
made again there.

Each project's data comes in kinds: ` + strings.Join(transfer.Categories, ", ") + `.
All but secrets are exported unless --categories or --exclude say otherwise.
--exclude takes a kind (memory) or one project's kind (web:chats).

A repository goes in by reference (its remote URL and branch); --bundle puts
each one in as a git bundle of every branch, so work that was never pushed
comes along too.

Logins are only exported when asked for: --login ` + strings.Join(transfer.Logins, ",") + ` (or all).

The file is encrypted with a passphrase you type (or ` + passphraseEnv + `).
--no-encrypt writes it in the clear, refused when it holds secrets or logins.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			pv, err := c.ExportPreview(cmd.Context())
			if err != nil {
				return err
			}
			req, err := exportChoices(pv, projects, categories, exclude, logins, bundle, secrets)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			printExportPreview(out, pv, req)
			if preview {
				return nil
			}
			path := filepath.Join(".", "agentbox-"+time.Now().Format("2006-01-02")+".agentbox")
			if len(args) == 1 {
				path = args[0]
			}
			if req.Path, err = filepath.Abs(path); err != nil {
				return err
			}
			req.NoEncrypt = noEncrypt
			if !noEncrypt {
				if req.Passphrase, err = newPassphrase(); err != nil {
					return err
				}
			}
			j, err := c.ExportInstance(cmd.Context(), req)
			if err != nil {
				return err
			}
			j, err = waitJob(cmd, c, j)
			if err != nil {
				return err
			}
			var res api.InstanceExportResult
			if err := json.Unmarshal(j.Result, &res); err != nil {
				return err
			}
			how := "encrypted"
			if !res.Encrypted {
				how = "not encrypted"
			}
			_, _ = fmt.Fprintf(out, "Exported to %s (%s, %s)\n", res.Path, transfer.Size(res.Size), how)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&projects, "project", nil, "export only these projects (default: all)")
	f.StringSliceVar(&categories, "categories", nil, "the kinds of data to export (default: all but secrets)")
	f.StringSliceVar(&exclude, "exclude", nil, "kinds to leave out, as kind or project:kind")
	f.BoolVar(&secrets, "secrets", false, "include the projects' secrets")
	f.BoolVar(&bundle, "bundle", false, "put repositories in as git bundles of every branch, not by reference")
	f.StringSliceVar(&logins, "login", nil, "logins to include: "+strings.Join(transfer.Logins, ", ")+", or all")
	f.BoolVar(&noEncrypt, "no-encrypt", false, "write the file in the clear (refused with secrets or logins)")
	f.BoolVar(&preview, "preview", false, "show what would be exported, and its size, without writing it")
	return cmd
}

// exportChoices turns the flags into a request, checking every name.
func exportChoices(pv api.InstanceExportPreview, projects, categories, exclude, logins []string, bundle, secrets bool) (api.InstanceExportRequest, error) {
	req := api.InstanceExportRequest{Projects: []api.InstanceExportProjectChoice{}, Logins: []string{}}
	kinds := slices.Clone(transfer.DefaultCategories)
	if len(categories) > 0 {
		kinds = categories
	}
	if secrets && !slices.Contains(kinds, transfer.Secrets) {
		kinds = append(kinds, transfer.Secrets)
	}
	for _, k := range kinds {
		if !slices.Contains(transfer.Categories, k) {
			return req, fmt.Errorf("no kind of data %q: choose from %s", k, strings.Join(transfer.Categories, ", "))
		}
	}
	var names []string
	for _, p := range pv.Projects {
		names = append(names, p.Name)
	}
	for _, p := range projects {
		if !slices.Contains(names, p) {
			return req, fmt.Errorf("no project %q: there are %s", p, strings.Join(names, ", "))
		}
	}
	if err := checkExclude(exclude, names, transfer.Categories); err != nil {
		return req, err
	}
	for _, p := range pv.Projects {
		if len(projects) > 0 && !slices.Contains(projects, p.Name) {
			continue
		}
		choice := api.InstanceExportProjectChoice{Name: p.Name, Bundle: bundle}
		for _, k := range kinds {
			if !slices.Contains(exclude, k) && !slices.Contains(exclude, p.Name+":"+k) {
				choice.Categories = append(choice.Categories, k)
			}
		}
		req.Projects = append(req.Projects, choice)
	}
	for _, l := range logins {
		switch {
		case l == "all":
			req.Logins = slices.Clone(transfer.Logins)
		case slices.Contains(transfer.Logins, l):
			if !slices.Contains(req.Logins, l) {
				req.Logins = append(req.Logins, l)
			}
		default:
			return req, fmt.Errorf("no login %q: choose from %s, or all", l, strings.Join(transfer.Logins, ", "))
		}
	}
	return req, nil
}

func checkExclude(exclude, projects, kinds []string) error {
	for _, e := range exclude {
		project, kind, scoped := strings.Cut(e, ":")
		if !scoped {
			kind = project
		}
		if scoped && !slices.Contains(projects, project) {
			return fmt.Errorf("--exclude %s: no project %q", e, project)
		}
		if !slices.Contains(kinds, kind) {
			return fmt.Errorf("--exclude %s: no kind %q, choose from %s", e, kind, strings.Join(kinds, ", "))
		}
	}
	return nil
}

func printExportPreview(out io.Writer, pv api.InstanceExportPreview, req api.InstanceExportRequest) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	var total int64
	for _, choice := range req.Projects {
		i := slices.IndexFunc(pv.Projects, func(p api.InstanceExportProjectPreview) bool { return p.Name == choice.Name })
		p := pv.Projects[i]
		_, _ = fmt.Fprintf(w, "%s\t\t\n", p.DisplayName)
		for _, c := range p.Categories {
			if !slices.Contains(choice.Categories, c.Kind) {
				continue
			}
			size, items := c.Size, fmt.Sprintf("%d", c.Items)
			if c.Kind == transfer.Repository {
				items = cmpOr(p.Remote, "no remote")
				if p.Branch != "" {
					items += " (" + p.Branch + ")"
				}
				if choice.Bundle {
					size = p.BundleSize
					items += ", bundled"
				}
			}
			total += size
			_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\n", c.Kind, items, transfer.Size(size))
		}
	}
	for _, l := range pv.Logins {
		if slices.Contains(req.Logins, l.Kind) {
			_, _ = fmt.Fprintf(w, "login %s\t%s\t\n", l.Kind, cmpOr(strings.Join(l.Accounts, ", "), "none"))
		}
	}
	_ = w.Flush()
	_, _ = fmt.Fprintf(out, "About %s before compression.\n", transfer.Size(total))
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// newPassphrase asks for an export's passphrase twice.
func newPassphrase() (string, error) {
	if p := os.Getenv(passphraseEnv); p != "" {
		return p, nil
	}
	p, err := readSecret("Passphrase to encrypt the export with: ")
	if err != nil {
		return "", fmt.Errorf("%w, or set %s, or --no-encrypt", err, passphraseEnv)
	}
	if len(p) < 8 {
		return "", errors.New("use a passphrase of 8 characters or more")
	}
	again, err := readSecret("Again: ")
	if err != nil {
		return "", err
	}
	if again != p {
		return "", errors.New("the two passphrases differ")
	}
	return p, nil
}

func newImportCmd(a *app) *cobra.Command {
	var projects, exclude, repos []string
	var noLogins, dryRun bool
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import an export from another AgentBox, merging it into this one",
		Long: `Opens a file written by 'agentbox export', shows what it holds, and merges it
into this AgentBox. What is here already is skipped, so importing the same file
twice changes nothing; what is here with other values is a conflict, and this
AgentBox's own is kept. The report says which.

A project that isn't here needs its repository: a checkout already where it was
(or at --repo name=/path) is used, else the export's bundle is restored there,
else the remote is cloned there.

--exclude takes a kind (memory), one project's kind (web:chats), or a login
(github). The passphrase is asked for, or read from ` + passphraseEnv + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			path, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			inspect := api.InstanceImportInspectRequest{Path: path, Passphrase: os.Getenv(passphraseEnv)}
			contents, err := c.InspectImport(cmd.Context(), inspect)
			if api.HasReason(err, api.ErrorPassphraseNeeded) {
				if inspect.Passphrase, err = readSecret("Passphrase: "); err != nil {
					return fmt.Errorf("%w, or set %s", err, passphraseEnv)
				}
				contents, err = c.InspectImport(cmd.Context(), inspect)
			}
			if err != nil {
				return err
			}
			req, err := importChoices(contents, projects, exclude, repos, noLogins)
			if err != nil {
				return err
			}
			req.Path, req.Passphrase = path, inspect.Passphrase
			out := cmd.OutOrStdout()
			printImportContents(out, contents, req)
			if dryRun {
				return nil
			}
			j, err := c.ImportInstance(cmd.Context(), req)
			if err != nil {
				return err
			}
			j, err = waitJob(cmd, c, j)
			if err != nil {
				return err
			}
			var rep api.InstanceImportReport
			if err := json.Unmarshal(j.Result, &rep); err != nil {
				return err
			}
			_, _ = fmt.Fprint(out, transfer.ReportText(path, rep))
			if rep.ReportPath != "" {
				_, _ = fmt.Fprintf(out, "\nReport saved to %s\n", rep.ReportPath)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&projects, "project", nil, "import only these projects (default: all in the file)")
	f.StringSliceVar(&exclude, "exclude", nil, "what to leave out: kind, project:kind, or a login")
	f.StringSliceVar(&repos, "repo", nil, "where a new project's repository goes here, as name=/path")
	f.BoolVar(&noLogins, "no-logins", false, "leave out every login in the file")
	f.BoolVar(&dryRun, "dry-run", false, "show what the file holds and what importing it would do, without importing")
	return cmd
}

func importChoices(contents api.InstanceImportContents, projects, exclude, repos []string, noLogins bool) (api.InstanceImportRequest, error) {
	req := api.InstanceImportRequest{Projects: []api.InstanceImportProjectChoice{}, Logins: []string{}}
	var names []string
	for _, p := range contents.Projects {
		names = append(names, p.Name)
	}
	for _, p := range projects {
		if !slices.Contains(names, p) {
			return req, fmt.Errorf("the file has no project %q: it has %s", p, cmpOr(strings.Join(names, ", "), "none"))
		}
	}
	excludable := append(slices.Clone(transfer.Categories), transfer.Logins...)
	if err := checkExclude(exclude, names, excludable); err != nil {
		return req, err
	}
	paths := map[string]string{}
	for _, r := range repos {
		name, path, ok := strings.Cut(r, "=")
		if !ok || !slices.Contains(names, name) {
			return req, fmt.Errorf("--repo %s: give it as name=/path, for a project in the file", r)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return req, err
		}
		paths[name] = abs
	}
	for _, p := range contents.Projects {
		if len(projects) > 0 && !slices.Contains(projects, p.Name) {
			continue
		}
		choice := api.InstanceImportProjectChoice{Name: p.Name, Path: paths[p.Name], Categories: []string{}}
		for _, c := range p.Categories {
			if !slices.Contains(exclude, c.Kind) && !slices.Contains(exclude, p.Name+":"+c.Kind) {
				choice.Categories = append(choice.Categories, c.Kind)
			}
		}
		req.Projects = append(req.Projects, choice)
	}
	if !noLogins {
		for _, l := range contents.Logins {
			if !slices.Contains(exclude, l.Kind) {
				req.Logins = append(req.Logins, l.Kind)
			}
		}
	}
	return req, nil
}

func printImportContents(out io.Writer, contents api.InstanceImportContents, req api.InstanceImportRequest) {
	how := "encrypted"
	if !contents.Encrypted {
		how = "not encrypted"
	}
	_, _ = fmt.Fprintf(out, "Export of %s by AgentBox %s (%s)\n", contents.CreatedAt.Local().Format("2 Jan 2006 15:04"), contents.App, how)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, choice := range req.Projects {
		i := slices.IndexFunc(contents.Projects, func(p api.InstanceImportProjectContents) bool { return p.Name == choice.Name })
		p := contents.Projects[i]
		switch {
		case p.Exists:
			_, _ = fmt.Fprintf(w, "%s\tmerges into the project here\t\n", p.DisplayName)
		case choice.Path != "":
			_, _ = fmt.Fprintf(w, "%s\tnew, repository at %s\t\n", p.DisplayName, choice.Path)
		case p.PlanError != "":
			_, _ = fmt.Fprintf(w, "%s\tnew, but %s\t\n", p.DisplayName, p.PlanError)
		default:
			_, _ = fmt.Fprintf(w, "%s\tnew, repository: %s to %s\t\n", p.DisplayName, p.Plan, p.Path)
		}
		for _, c := range p.Categories {
			if slices.Contains(choice.Categories, c.Kind) {
				_, _ = fmt.Fprintf(w, "  %s\t%d\t%s\n", c.Kind, c.Items, transfer.Size(c.Size))
			}
		}
	}
	for _, l := range contents.Logins {
		if slices.Contains(req.Logins, l.Kind) {
			_, _ = fmt.Fprintf(w, "login %s\t%d\t\n", l.Kind, l.Items)
		}
	}
	_ = w.Flush()
}
