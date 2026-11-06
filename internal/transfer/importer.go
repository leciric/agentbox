package transfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/notes"
	"agentbox/internal/portable"
	"agentbox/internal/state"
	"agentbox/internal/trace"
)

// Counts is what one entry's import did.
type Counts struct {
	Added, Skipped, Conflicted int
	Notes                      []string
}

const maxNotes = 50

func (c *Counts) conflict(note string) {
	c.Conflicted++
	c.note(note)
}

func (c *Counts) note(note string) {
	switch {
	case len(c.Notes) < maxNotes:
		c.Notes = append(c.Notes, note)
	case len(c.Notes) == maxNotes:
		c.Notes = append(c.Notes, "and more")
	}
}

// Preview is what this instance has to export, with each kind's size as it
// would be written, and how big each repository would be as a bundle.
func (in Instance) Preview(ctx context.Context) (api.InstanceExportPreview, error) {
	projects, err := in.Store.Projects(ctx)
	if err != nil {
		return api.InstanceExportPreview{}, err
	}
	out := api.InstanceExportPreview{Projects: []api.InstanceExportProjectPreview{}, Logins: []api.InstanceExportLoginPreview{}}
	for _, p := range projects {
		if p.Name == state.HomeProject {
			continue
		}
		ref := repoOf(ctx, p.Root)
		pp := api.InstanceExportProjectPreview{Name: p.Name, DisplayName: p.DisplayName, Remote: ref.Remote, Branch: ref.Branch, BundleSize: bundleEstimate(ctx, p.Root)}
		for _, kind := range Categories {
			c, err := in.measure(ctx, p.Name, kind)
			if err != nil {
				return out, fmt.Errorf("%s of %s: %w", kind, p.Name, err)
			}
			pp.Categories = append(pp.Categories, c)
		}
		out.Projects = append(out.Projects, pp)
	}
	for _, kind := range Logins {
		list, err := in.logins(kind)
		if err != nil {
			return out, fmt.Errorf("%s logins: %w", kind, err)
		}
		lp := api.InstanceExportLoginPreview{Kind: kind, Accounts: []string{}}
		for _, l := range list {
			lp.Accounts = append(lp.Accounts, accountName(l))
		}
		out.Logins = append(out.Logins, lp)
	}
	return out, nil
}

func (in Instance) measure(ctx context.Context, project, kind string) (api.TransferCategory, error) {
	c := api.TransferCategory{Kind: kind}
	var v any
	switch kind {
	case Repository:
		c.Items = 1
		return c, nil
	case Notes:
		text, err := notes.Read(in.NotesPath(project))
		if err != nil {
			return c, err
		}
		if text != "" {
			c.Items = 1
		}
		v = notesEntry{Text: text}
	case Sections:
		s, err := in.projectSection(ctx, project)
		if err != nil {
			return c, err
		}
		if s.Section != nil {
			c.Items = 1
		}
		v = s
	case Secrets:
		list, err := in.Secrets.List(ctx, project, "")
		if err != nil {
			return c, err
		}
		c.Items = len(list)
		return c, nil
	default:
		ts, err := in.dumpTables(ctx, kind, project)
		if err != nil {
			return c, err
		}
		c.Items = itemsOf(kind, ts)
		v = tablesEntry{Tables: ts}
	}
	b, err := json.Marshal(v)
	c.Size = int64(len(b))
	return c, err
}

// Inspect opens an export and says what it holds and what importing it here
// would do with each project.
func (in Instance) Inspect(ctx context.Context, path, passphrase string) (api.InstanceImportContents, error) {
	r, err := portable.Open(path, passphrase, in.Tmp)
	if err != nil {
		return api.InstanceImportContents{}, err
	}
	defer r.Close()
	m := r.Manifest
	out := api.InstanceImportContents{Path: path, Encrypted: r.Encrypted, Schema: m.Schema, App: m.App, ID: m.ID, CreatedAt: m.CreatedAt,
		Projects: []api.InstanceImportProjectContents{}, Logins: []api.TransferCategory{}}
	byName := map[string]int{}
	for _, e := range m.Entries {
		cat := api.TransferCategory{Kind: e.Kind, Items: e.Items, Size: e.Size}
		if e.Project == "" {
			out.Logins = append(out.Logins, cat)
			continue
		}
		i, ok := byName[e.Project]
		if !ok {
			i = len(out.Projects)
			byName[e.Project] = i
			out.Projects = append(out.Projects, api.InstanceImportProjectContents{Name: e.Project, DisplayName: e.Project})
		}
		p := &out.Projects[i]
		if d := e.Meta["displayName"]; d != "" {
			p.DisplayName = d
		}
		if e.Kind == RepositoryBundle {
			p.Bundled = true
			// The bundle's size goes with the repository's entry.
			for j := range p.Categories {
				if p.Categories[j].Kind == Repository {
					p.Categories[j].Size += e.Size
				}
			}
			continue
		}
		if e.Kind == Repository {
			p.Remote, p.Branch = e.Meta["remote"], e.Meta["branch"]
			cat.Size += bundleSize(m, e.Project)
		}
		p.Categories = append(p.Categories, cat)
	}
	for i := range out.Projects {
		p := &out.Projects[i]
		if local, err := in.Store.Project(ctx, p.Name); err == nil {
			p.Exists, p.Path, p.Plan = true, local.Root, RepoUse
			continue
		}
		ref, err := readRepo(r, p.Name)
		if err != nil {
			p.PlanError = err.Error()
			continue
		}
		p.Path = in.suggestPath(ctx, p.Name, ref)
		if p.Plan, err = planRepo(ctx, ref, p.Bundled, p.Path); err != nil {
			p.PlanError = err.Error()
		}
	}
	return out, nil
}

func bundleSize(m portable.Manifest, project string) int64 {
	if e, ok := m.Find(ProjectEntryID(project, RepositoryBundle)); ok {
		return e.Size
	}
	return 0
}

func readRepo(r *portable.Reader, project string) (repoRef, error) {
	var ref repoRef
	if _, ok := r.Manifest.Find(ProjectEntryID(project, Repository)); !ok {
		return ref, errors.New("the export doesn't hold its repository: it can only merge into a project of the same name here")
	}
	return ref, r.DecodeJSON(ProjectEntryID(project, Repository), &ref)
}

// Import merges the chosen parts of an export into this instance. Entries
// already here are skipped, so importing the same file twice changes nothing
// the second time. added is called for each project the import created, once
// its data is in.
func (in Instance) Import(ctx context.Context, req api.InstanceImportRequest, log io.Writer, added func(project string)) (api.InstanceImportReport, error) {
	report := api.InstanceImportReport{Entries: []api.InstanceImportEntryReport{}, ProjectsAdded: []string{}}
	r, err := portable.Open(req.Path, req.Passphrase, in.Tmp)
	if err != nil {
		return report, err
	}
	defer r.Close()
	record := func(e api.InstanceImportEntryReport, c Counts) {
		e.Added, e.Skipped, e.Conflicted, e.Notes = c.Added, c.Skipped, c.Conflicted, c.Notes
		report.Entries = append(report.Entries, e)
		report.Added += c.Added
		report.Skipped += c.Skipped
		report.Conflicted += c.Conflicted
		_, _ = fmt.Fprintf(log, "%s: %d added, %d skipped, %d conflicted\n", e.ID, c.Added, c.Skipped, c.Conflicted)
	}

	for _, kind := range Logins {
		id := LoginEntryID(kind)
		if !slices.Contains(req.Logins, kind) {
			continue
		}
		if _, ok := r.Manifest.Find(id); !ok {
			continue
		}
		var list []login
		if err := r.DecodeJSON(id, &list); err != nil {
			return report, err
		}
		var c Counts
		if err := in.importLogins(kind, list, &c); err != nil {
			return report, err
		}
		record(api.InstanceImportEntryReport{ID: id, Kind: kind}, c)
	}

	for _, choice := range req.Projects {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		created, err := in.importProject(ctx, r, choice, record)
		if err != nil {
			return report, fmt.Errorf("project %s: %w", choice.Name, err)
		}
		if created {
			report.ProjectsAdded = append(report.ProjectsAdded, choice.Name)
			if added != nil {
				added(choice.Name)
			}
		}
	}
	report.ReportPath = in.writeReport(req.Path, report)
	return report, nil
}

func (in Instance) importProject(ctx context.Context, r *portable.Reader, choice api.InstanceImportProjectChoice, record func(api.InstanceImportEntryReport, Counts)) (created bool, err error) {
	name := choice.Name
	entry := func(kind string) api.InstanceImportEntryReport {
		return api.InstanceImportEntryReport{ID: ProjectEntryID(name, kind), Kind: kind, Project: name}
	}
	has := func(kind string) bool {
		_, ok := r.Manifest.Find(ProjectEntryID(name, kind))
		return ok && slices.Contains(choice.Categories, kind)
	}
	local, err := in.Store.Project(ctx, name)
	exists := err == nil
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return false, err
	}

	// A project new here needs its repository first, then its row.
	root := local.Root
	if !exists {
		var c Counts
		if !has(Settings) {
			c.Skipped++
			c.note("this project isn't here, and its settings weren't chosen: nothing of it was imported")
			record(entry(Settings), c)
			return false, nil
		}
		var refused string
		root, refused, err = in.importRepository(ctx, r, name, choice.Path)
		if err != nil {
			return false, err
		}
		if refused != "" {
			c.Skipped++
			c.note(refused)
			record(entry(Repository), c)
			return false, nil
		}
		c.Added++
		c.note("repository at " + root)
		record(entry(Repository), c)
	} else if has(Repository) {
		var c Counts
		c.Skipped++
		c.note("the project is here already: its repository stays at " + root)
		record(entry(Repository), c)
	}

	for _, kind := range Categories {
		if kind == Repository || !has(kind) {
			continue
		}
		var c Counts
		if err := in.importEntry(ctx, r, name, kind, root, &c); err != nil {
			return created, fmt.Errorf("%s: %w", kind, err)
		}
		if kind == Settings && !exists && c.Added > 0 {
			created = true
		}
		if kind == Settings && !exists && c.Added == 0 {
			record(entry(kind), c)
			return false, nil
		}
		record(entry(kind), c)
	}
	return created, nil
}

// importRepository gets a new project's repository to path (or where Inspect
// would suggest), and says why not when it can't.
func (in Instance) importRepository(ctx context.Context, r *portable.Reader, project, path string) (root, refused string, err error) {
	ref, err := readRepo(r, project)
	if err != nil {
		return "", err.Error(), nil
	}
	if path == "" {
		path = in.suggestPath(ctx, project, ref)
	}
	path = filepath.Clean(path)
	_, bundled := r.Manifest.Find(ProjectEntryID(project, RepositoryBundle))
	plan, err := planRepo(ctx, ref, bundled, path)
	if err != nil {
		return "", err.Error(), nil
	}
	var other string
	switch err := in.Store.DB().QueryRowContext(ctx, `SELECT name FROM projects WHERE root = ?`, path).Scan(&other); {
	case err == nil:
		return "", fmt.Sprintf("%s is project %s's repository already", path, other), nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", "", err
	}
	bundle := ""
	if plan == RepoBundle {
		f, err := os.CreateTemp(in.Tmp, "agentbox-import-*.bundle")
		if err != nil {
			return "", "", err
		}
		bundle = f.Name()
		_ = f.Close()
		defer func() { _ = os.Remove(bundle) }()
		if err := r.Extract(ProjectEntryID(project, RepositoryBundle), bundle); err != nil {
			return "", "", err
		}
	}
	fresh := !exists(path)
	if err := restoreRepo(ctx, plan, path, ref, bundle); err != nil {
		if fresh {
			_ = os.RemoveAll(path)
		}
		return "", fmt.Sprintf("couldn't get its repository to %s (%s): %v", path, plan, err), nil
	}
	return path, "", nil
}

func (in Instance) importEntry(ctx context.Context, r *portable.Reader, project, kind, root string, c *Counts) error {
	id := ProjectEntryID(project, kind)
	switch kind {
	case Notes:
		var n notesEntry
		if err := r.DecodeJSON(id, &n); err != nil {
			return err
		}
		return in.importNotes(project, n.Text, c)
	case Secrets:
		var list []secretValue
		if err := r.DecodeJSON(id, &list); err != nil {
			return err
		}
		return in.importSecrets(ctx, project, list, c)
	case Sections:
		var s sectionEntry
		if err := r.DecodeJSON(id, &s); err != nil {
			return err
		}
		return in.inTx(ctx, func(tx execer) error { return importSection(ctx, tx, project, s, c) })
	}
	var t importedTables
	if err := r.DecodeJSON(id, &t); err != nil {
		return err
	}
	if kind == Settings {
		if _, err := in.Store.Project(ctx, project); errors.Is(err, state.ErrNotFound) {
			if why := in.nameTaken(ctx, project, t); why != "" {
				c.Skipped++
				c.note(why)
				return nil
			}
		}
	}
	return in.inTx(ctx, func(tx execer) error {
		switch kind {
		case Settings:
			return importSettings(ctx, tx, project, root, t, c)
		case Chats:
			return importChats(ctx, tx, project, t, c)
		}
		for _, rows := range t.Tables {
			if err := merge(ctx, tx, rows, onlyProject(project), c); err != nil {
				return err
			}
		}
		return nil
	})
}

func (in Instance) inTx(ctx context.Context, fn func(execer) error) error {
	tx, err := trace.BeginTx(ctx, in.Store.DB())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// onlyProject keeps an entry's rows to its own project, and the
// AgentBox-wide ones (project ""), whatever the file says.
func onlyProject(project string) rowRewrite {
	return func(row map[string]any) bool {
		p, ok := row["project"]
		return !ok || p == project || p == ""
	}
}

// nameTaken says why a new project's name can't be used here, as adding one
// by hand would: "" when it can.
func (in Instance) nameTaken(ctx context.Context, project string, t importedTables) string {
	if len(t.Tables) == 0 {
		return ""
	}
	rows := t.Tables[0]
	for _, raw := range rows.Rows {
		if columnString(rows, raw, "name") != project {
			continue
		}
		display := columnString(rows, raw, "display_name")
		if display == "" {
			display = project
		}
		if err := in.Store.ProjectNameFree(ctx, display, ""); err != nil {
			return fmt.Sprintf("not added: %v", err)
		}
	}
	return ""
}

func importSettings(ctx context.Context, tx execer, project, root string, t importedTables, c *Counts) error {
	if len(t.Tables) == 0 {
		return nil
	}
	return merge(ctx, tx, t.Tables[0], func(row map[string]any) bool {
		if row["name"] != project {
			return false
		}
		row["root"] = root
		return true
	}, c)
}

// importChats merges the past agents and the chats. A past agent whose name is
// a different agent here (its id differs) keeps its chat out, and so does a
// chat this instance has its own history in: interleaving two conversations
// would make one of neither.
func importChats(ctx context.Context, tx execer, project string, t importedTables, c *Counts) error {
	keep := onlyProject(project)
	blocked := map[string]bool{}
	for _, rows := range t.Tables {
		if rows.Table != "archived_agents" {
			continue
		}
		for _, raw := range rows.Rows {
			one := rows
			one.Rows = [][]json.RawMessage{raw}
			var each Counts
			if err := merge(ctx, tx, one, keep, &each); err != nil {
				return err
			}
			c.Added += each.Added
			c.Skipped += each.Skipped
			if each.Conflicted > 0 {
				name := columnString(rows, raw, "name")
				blocked[name] = true
				c.conflict(fmt.Sprintf("past agent %s: this instance has another agent of that name; its chat wasn't imported", name))
			}
		}
	}
	for _, rows := range t.Tables {
		if rows.Table != "chat_items" {
			continue
		}
		theirs := map[string][]any{}
		for _, raw := range rows.Rows {
			agent := columnString(rows, raw, "agent")
			theirs[agent] = append(theirs[agent], columnString(rows, raw, "id"))
		}
		for agent, ids := range theirs {
			if blocked[agent] {
				continue
			}
			var mine, shared int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM chat_items WHERE project = ? AND agent = ?`, project, agent).Scan(&mine); err != nil {
				return err
			}
			if mine == 0 {
				continue
			}
			q := `SELECT count(*) FROM chat_items WHERE project = ? AND agent = ? AND id IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ") + `)`
			if err := tx.QueryRowContext(ctx, q, append([]any{project, agent}, ids...)...).Scan(&shared); err != nil {
				return err
			}
			if shared == 0 {
				blocked[agent] = true
				c.conflict(fmt.Sprintf("chat of %s: this instance has a chat of its own there, kept", agent))
			}
		}
	}
	for _, rows := range t.Tables {
		if rows.Table == "archived_agents" {
			continue
		}
		if err := merge(ctx, tx, rows, func(row map[string]any) bool {
			agent, _ := row["agent"].(string)
			return keep(row) && !blocked[agent]
		}, c); err != nil {
			return err
		}
	}
	return nil
}

func columnString(rows importedRows, raw []json.RawMessage, col string) string {
	i := slices.Index(rows.Columns, col)
	if i < 0 || i >= len(raw) {
		return ""
	}
	v, _ := decodeValue(raw[i])
	s, _ := v.(string)
	return s
}

// importSection puts the project in its section, found here by id or by name
// and made when there is neither, unless the project is in a section already.
func importSection(ctx context.Context, tx execer, project string, s sectionEntry, c *Counts) error {
	if s.Section == nil {
		c.Skipped++
		return nil
	}
	sec := s.Section
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM project_sections WHERE id = ? OR lower(name) = lower(?) ORDER BY id = ? DESC LIMIT 1`, sec.ID, sec.Name, sec.ID).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = sec.ID
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_sections (id, name, position, collapsed, created_at)
			VALUES (?, ?, (SELECT coalesce(max(position), 0) + 1 FROM project_sections), ?, ?)`, id, sec.Name, sec.Collapsed, sec.CreatedAt); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT section FROM projects WHERE name = ?`, project).Scan(&current); err != nil {
		return err
	}
	switch current {
	case id:
		c.Skipped++
	case "":
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET section = ? WHERE name = ?`, id, project); err != nil {
			return err
		}
		c.Added++
	default:
		c.conflict(fmt.Sprintf("the project is in another section here, kept (the export has it in %s)", sec.Name))
	}
	return nil
}

func (in Instance) importNotes(project, text string, c *Counts) error {
	path := in.NotesPath(project)
	have, err := notes.Read(path)
	if err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(text) == "" || strings.TrimSpace(have) == strings.TrimSpace(text):
		c.Skipped++
	case strings.TrimSpace(have) == "":
		if err := notes.Write(path, text); err != nil {
			return err
		}
		c.Added++
	default:
		c.conflict("the project has notes of its own here, kept")
	}
	return nil
}

func (in Instance) importSecrets(ctx context.Context, project string, list []secretValue, c *Counts) error {
	for _, s := range list {
		have, err := in.Secrets.Resolve(ctx, project, "", s.Name)
		switch {
		case err == nil && have == s.Value:
			c.Skipped++
			continue
		case err == nil:
			c.conflict(fmt.Sprintf("secret %s: this instance has another value, kept", s.Name))
			continue
		case !errors.Is(err, state.ErrNotFound):
			return err
		}
		if _, err := in.Secrets.Set(ctx, project, "", s.Name, s.Value); err != nil {
			return fmt.Errorf("secret %s: %w", s.Name, err)
		}
		c.Added++
	}
	return nil
}

// writeReport writes the report as text beside the instance's other files,
// and returns where; "" when it couldn't.
func (in Instance) writeReport(archive string, rep api.InstanceImportReport) string {
	if in.Reports == "" {
		return ""
	}
	if err := os.MkdirAll(in.Reports, 0o700); err != nil {
		return ""
	}
	path := filepath.Join(in.Reports, "import-"+time.Now().Format("20060102-150405")+".txt")
	if err := os.WriteFile(path, []byte(ReportText(archive, rep)), 0o600); err != nil {
		return ""
	}
	return path
}

// ReportText is an import's report as people read it.
func ReportText(archive string, rep api.InstanceImportReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Import of %s\n%d added, %d skipped (already here), %d conflicted (this instance's kept)\n", archive, rep.Added, rep.Skipped, rep.Conflicted)
	if len(rep.ProjectsAdded) > 0 {
		fmt.Fprintf(&b, "New projects: %s\n", strings.Join(rep.ProjectsAdded, ", "))
	}
	for _, e := range rep.Entries {
		fmt.Fprintf(&b, "\n%s: %d added, %d skipped, %d conflicted\n", e.ID, e.Added, e.Skipped, e.Conflicted)
		for _, n := range e.Notes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}
	return b.String()
}
