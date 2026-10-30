// Package transfer exports an AgentBox instance's data into one file and
// imports it into another: projects' settings, notes, skills, schedules,
// sections and icons, their memory and chats, connectors (without sign-ins),
// repositories (by reference or as git bundles), and, only when asked,
// secrets and logins. Machines, images and worktrees stay behind: they are
// made again on the other side.
//
// The file is package portable's; this package decides what goes in each of
// its entries and how an entry merges into an instance that may already hold
// some of it (tables.go). The daemon runs both directions as jobs, for the CLI
// and the app alike.
package transfer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"agentbox/internal/credentials"
	"agentbox/internal/notes"
	"agentbox/internal/portable"
	"agentbox/internal/secrets"
	"agentbox/internal/state"
)

// A project's kinds of data, each its own entry and its own checkbox.
const (
	Settings   = "settings"
	Sections   = "sections"
	Icon       = "icon"
	Notes      = "notes"
	Skills     = "skills"
	Schedules  = "schedules"
	Memory     = "memory"
	Chats      = "chats"
	Connectors = "connectors"
	Repository = "repository"
	Secrets    = "secrets"
)

// Categories is every kind of a project's data, in the order an import
// applies them: the repository before the project that needs it, the project
// before everything that belongs to it.
var Categories = []string{Repository, Settings, Sections, Icon, Notes, Skills, Schedules, Memory, Chats, Connectors, Secrets}

// DefaultCategories is what an export takes when it isn't told: everything
// but secrets, which are opt-in.
var DefaultCategories = []string{Repository, Settings, Sections, Icon, Notes, Skills, Schedules, Memory, Chats, Connectors}

// Logins AgentBox keeps, each opt-in: GitHub accounts, then the AI tools'.
const (
	LoginGitHub   = "github"
	LoginClaude   = "claude"
	LoginCodex    = credentials.ToolCodex
	LoginOpenCode = credentials.ToolOpenCode
	LoginCursor   = credentials.ToolCursor
)

var Logins = []string{LoginGitHub, LoginClaude, LoginCodex, LoginOpenCode, LoginCursor}

// ProjectEntryID and LoginEntryID are the stable IDs of entries.
func ProjectEntryID(project, kind string) string { return "project/" + project + "/" + kind }
func LoginEntryID(kind string) string            { return "installation/" + kind }

// Instance is the AgentBox being exported from or imported into.
type Instance struct {
	Store   *state.Store
	Secrets secrets.Store
	Creds   credentials.Store
	// NotesPath is where a project's notes file is.
	NotesPath func(project string) string
	// Version is this AgentBox's, written into the manifest.
	Version string
	// Tmp is where files are staged and decrypted.
	Tmp string
	// Home is where a cloned repository goes by default (~/src/<project>).
	Home string
	// Reports is where an import writes its report.
	Reports string
}

// tables is which rows of state.db each kind of a project's data is.
var tables = map[string][]tableSpec{
	// The project row, without what is chosen again on import: where its
	// repository is, and its place in the sidebar (Sections).
	Settings: {{table: "projects", where: "name = ?", omit: []string{"section", "position"}}},
	Icon:     {{table: "project_icons", where: "project = ?"}},
	// The skills the project has switched on or off, and every one that is on
	// for all projects, so its agents get the same skills on the other side.
	Skills: {
		{table: "skills", where: skillsOf},
		{table: "skill_files", where: "skill IN (SELECT name FROM skills WHERE " + skillsOf + ")"},
		{table: "skill_projects", where: "project = ?"},
	},
	Schedules: {{table: "schedules", where: "project = ?"}},
	Memory: {
		{table: "memories", where: "project = ?"},
		{table: "memory_anchors", where: "project = ?"},
		{table: "events", where: "project = ?"},
		{table: "agent_reports", where: "project = ?"},
		{table: "token_usage", where: "project = ?", omit: []string{"id"}},
	},
	// The lead's chat and every past agent's. The AI tool's own session isn't
	// exported (it lives in the tool's files), so the chat carries on in a new
	// one on the other side.
	Chats: {
		{table: "archived_agents", where: "project = ?"},
		{table: "chats", where: chatsOf, omit: []string{"session_id"}},
		{table: "chat_items", where: chatsOf},
	},
	// The project's connectors and the AgentBox-wide ones, as a list: the
	// OAuth registration and tokens stay behind, so each is signed in to
	// again.
	Connectors: {
		{table: "connectors", where: "agent = '' AND (project = ? OR project = '')", omit: connectorSignIn},
		{table: "connector_projects", where: "project = ?"},
	},
}

const (
	skillsOf = "name IN (SELECT skill FROM skill_projects WHERE project = ?) OR enabled = 1"
	chatsOf  = "project = ? AND (agent = '" + state.LeadName + "' OR agent IN (SELECT name FROM archived_agents WHERE project = ?))"
)

var connectorSignIn = []string{"client_id", "client_secret", "token_auth", "redirect_uri", "access_token", "refresh_token", "expires_at", "error", "connected_at"}

// Errors an export refuses with.
var (
	ErrPlaintextSecrets = errors.New("an export holding secrets or logins must be encrypted: give a passphrase, or leave secrets and logins out")
	ErrNoPassphrase     = errors.New("give a passphrase to encrypt the export with, or ask for a plaintext one")
)

// ProjectChoice is what to export of one project.
type ProjectChoice struct {
	Name       string
	Categories []string
	// Bundle puts the repository in as a git bundle of every branch, rather
	// than its remote URL and branch alone.
	Bundle bool
}

// ExportOptions is an export's choices.
type ExportOptions struct {
	Projects []ProjectChoice
	Logins   []string
	// Passphrase encrypts the file; "" with NoEncrypt writes it in the clear.
	Passphrase string
	NoEncrypt  bool
}

// Check refuses choices an export can't be made with.
func (o ExportOptions) Check() error {
	for _, l := range o.Logins {
		if !slices.Contains(Logins, l) {
			return fmt.Errorf("no login %q: choose from %s", l, strings.Join(Logins, ", "))
		}
	}
	secret := len(o.Logins) > 0
	for _, p := range o.Projects {
		for _, c := range p.Categories {
			if !slices.Contains(Categories, c) {
				return fmt.Errorf("no kind of data %q: choose from %s", c, strings.Join(Categories, ", "))
			}
		}
		secret = secret || slices.Contains(p.Categories, Secrets)
	}
	switch {
	case o.NoEncrypt && o.Passphrase != "":
		return errors.New("a passphrase and no encryption were both asked for")
	case o.NoEncrypt && secret:
		return ErrPlaintextSecrets
	case !o.NoEncrypt && o.Passphrase == "":
		return ErrNoPassphrase
	}
	return nil
}

// ExportResult is what an export wrote.
type ExportResult struct {
	Path      string
	Size      int64
	Encrypted bool
	Entries   []portable.Entry
}

// Export writes the chosen data to the file at path, through a temporary file
// beside it, so a failed export leaves no half-written file behind.
func (in Instance) Export(ctx context.Context, path string, opts ExportOptions, log io.Writer) (ExportResult, error) {
	if err := opts.Check(); err != nil {
		return ExportResult{}, err
	}
	w, err := portable.NewWriter(in.Tmp)
	if err != nil {
		return ExportResult{}, err
	}
	defer w.Discard()
	for _, choice := range opts.Projects {
		p, err := in.Store.Project(ctx, choice.Name)
		if err != nil {
			return ExportResult{}, fmt.Errorf("project %s: %w", choice.Name, err)
		}
		for _, kind := range Categories {
			if !slices.Contains(choice.Categories, kind) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return ExportResult{}, err
			}
			e, err := in.exportProjectEntry(ctx, w, p, kind, choice.Bundle)
			if err != nil {
				return ExportResult{}, fmt.Errorf("%s of %s: %w", kind, p.Name, err)
			}
			_, _ = fmt.Fprintf(log, "%s: %s, %d items, %s\n", p.Name, kind, e.Items, Size(e.Size))
		}
	}
	for _, kind := range Logins {
		if !slices.Contains(opts.Logins, kind) {
			continue
		}
		e, err := in.exportLogins(w, kind)
		if err != nil {
			return ExportResult{}, fmt.Errorf("%s logins: %w", kind, err)
		}
		_, _ = fmt.Fprintf(log, "logins: %s, %d accounts\n", kind, e.Items)
	}

	f, err := os.CreateTemp(dirOf(path), ".agentbox-export-*")
	if err != nil {
		return ExportResult{}, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return ExportResult{}, err
	}
	pass := opts.Passphrase
	if opts.NoEncrypt {
		pass = ""
	}
	m, err := w.Finish(f, in.Version, pass)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return ExportResult{}, err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return ExportResult{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return ExportResult{}, err
	}
	_, _ = fmt.Fprintf(log, "Wrote %s (%s)\n", path, Size(info.Size()))
	return ExportResult{Path: path, Size: info.Size(), Encrypted: pass != "", Entries: m.Entries}, nil
}

func dirOf(path string) string {
	if i := strings.LastIndexByte(path, os.PathSeparator); i >= 0 {
		return path[:i+1]
	}
	return "."
}

func (in Instance) exportProjectEntry(ctx context.Context, w *portable.Writer, p state.Project, kind string, bundle bool) (portable.Entry, error) {
	e := portable.Entry{ID: ProjectEntryID(p.Name, kind), Kind: kind, Project: p.Name, Meta: map[string]string{"displayName": p.DisplayName}}
	switch kind {
	case Repository:
		return in.exportRepository(ctx, w, e, p, bundle)
	case Notes:
		text, err := notes.Read(in.NotesPath(p.Name))
		if err != nil {
			return e, err
		}
		if text != "" {
			e.Items = 1
		}
		return w.AddJSON(e, notesEntry{Text: text})
	case Sections:
		s, err := in.projectSection(ctx, p.Name)
		if err != nil {
			return e, err
		}
		if s.Section != nil {
			e.Items = 1
		}
		return w.AddJSON(e, s)
	case Secrets:
		list, err := in.projectSecrets(ctx, p.Name)
		if err != nil {
			return e, err
		}
		e.Items = len(list)
		return w.AddJSON(e, list)
	}
	ts, err := in.dumpTables(ctx, kind, p.Name)
	if err != nil {
		return e, err
	}
	e.Items = itemsOf(kind, ts)
	return w.AddJSON(e, tablesEntry{Tables: ts})
}

func (in Instance) dumpTables(ctx context.Context, kind, project string) ([]tableRows, error) {
	specs, ok := tables[kind]
	if !ok {
		return nil, fmt.Errorf("no kind of data %q", kind)
	}
	ts := make([]tableRows, 0, len(specs))
	for _, spec := range specs {
		t, err := dump(ctx, in.Store.DB(), spec, project)
		if err != nil {
			return nil, err
		}
		ts = append(ts, t)
	}
	return ts, nil
}

// counted is the table whose rows an entry counts as its items: its first,
// unless named here.
var counted = map[string]string{Chats: "chat_items"}

func itemsOf(kind string, ts []tableRows) int {
	for _, t := range ts {
		if t.Table == counted[kind] {
			return len(t.Rows)
		}
	}
	return len(ts[0].Rows)
}

// tablesEntry is a JSON entry of rows.
type tablesEntry struct {
	Tables []tableRows `json:"tables"`
}

type importedTables struct {
	Tables []importedRows `json:"tables"`
}

type notesEntry struct {
	Text string `json:"text"`
}

// sectionEntry is the section a project is in, if any.
type sectionEntry struct {
	Section *section `json:"section"`
}

type section struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Position  int    `json:"position"`
	Collapsed bool   `json:"collapsed"`
	CreatedAt int64  `json:"createdAt"`
}

func (in Instance) projectSection(ctx context.Context, project string) (sectionEntry, error) {
	var s section
	err := in.Store.DB().QueryRowContext(ctx, `SELECT s.id, s.name, s.position, s.collapsed, s.created_at
		FROM project_sections s JOIN projects p ON p.section = s.id WHERE p.name = ?`, project).
		Scan(&s.ID, &s.Name, &s.Position, &s.Collapsed, &s.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return sectionEntry{}, nil
	case err != nil:
		return sectionEntry{}, err
	}
	return sectionEntry{Section: &s}, nil
}

// secretValue is a project secret in the clear, which is why an export holding
// any is always encrypted.
type secretValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (in Instance) projectSecrets(ctx context.Context, project string) ([]secretValue, error) {
	list, err := in.Secrets.List(ctx, project, "")
	if err != nil {
		return nil, err
	}
	out := make([]secretValue, 0, len(list))
	for _, s := range list {
		v, err := in.Secrets.Resolve(ctx, project, "", s.Name)
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", s.Name, err)
		}
		out = append(out, secretValue{Name: s.Name, Value: v})
	}
	return out, nil
}

// Size is a byte count as people read it.
func Size(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
