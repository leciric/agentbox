package transfer

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/notes"
	"agentbox/internal/portable"
	"agentbox/internal/secrets"
	"agentbox/internal/state"
)

func init() { portable.ScryptWorkFactor = 10 }

const pass = "a long passphrase"

func newInstance(t *testing.T) Instance {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return Instance{
		Store:     st,
		Secrets:   secrets.Store{State: st, KeyPath: filepath.Join(dir, "secrets.key")},
		Creds:     credentials.Store{Dir: filepath.Join(dir, "credentials")},
		NotesPath: func(p string) string { return filepath.Join(dir, "notes", p+".md") },
		Version:   "test",
		Tmp:       t.TempDir(),
		Home:      filepath.Join(dir, "home"),
		Reports:   filepath.Join(dir, "reports"),
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo is a checkout of a bare "remote", with main pushed and a branch
// that never was.
func newRepo(t *testing.T) (root, remote, unpushed string) {
	t.Helper()
	base := t.TempDir()
	remote = filepath.Join(base, "remote.git")
	root = filepath.Join(base, "web")
	run(t, base, "git", "init", "--quiet", "--bare", "-b", "main", remote)
	run(t, base, "git", "init", "--quiet", "-b", "main", root)
	run(t, root, "git", "commit", "--quiet", "--allow-empty", "-m", "first")
	run(t, root, "git", "remote", "add", "origin", remote)
	run(t, root, "git", "push", "--quiet", "origin", "main")
	run(t, root, "git", "checkout", "--quiet", "-b", "wip")
	run(t, root, "git", "commit", "--quiet", "--allow-empty", "-m", "never pushed")
	unpushed = run(t, root, "git", "rev-parse", "HEAD")
	run(t, root, "git", "checkout", "--quiet", "main")
	return root, remote, unpushed
}

// seed fills an instance with one project and some of everything.
func seed(t *testing.T, in Instance, root string) {
	t.Helper()
	ctx := context.Background()
	if err := in.Store.AddProject(ctx, state.Project{Name: "web", DisplayName: "Web", Root: root, CreatedAt: time.Unix(1700000000, 0), Autonomy: "auto"}); err != nil {
		t.Fatal(err)
	}
	db := in.Store.DB()
	for _, q := range []string{
		`INSERT INTO project_sections (id, name, position, created_at) VALUES ('sec1', 'Work', 1, 1)`,
		`UPDATE projects SET section = 'sec1' WHERE name = 'web'`,
		`INSERT INTO project_icons (project, chosen, emoji, image, mime) VALUES ('web', 'image', '', x'89504e47', 'image/png')`,
		`INSERT INTO skills (name, description, created_at, updated_at, enabled) VALUES ('deploy', 'how to deploy', 1, 1, 0)`,
		`INSERT INTO skill_files (skill, path, mode, content) VALUES ('deploy', 'SKILL.md', 420, x'2320686921')`,
		`INSERT INTO skill_projects (skill, project, enabled) VALUES ('deploy', 'web', 1)`,
		`INSERT INTO schedules (id, project, title, task, kind, created_at, updated_at) VALUES ('sch1', 'web', 'Nightly', 'run it', 'every', 1, 1)`,
		`INSERT INTO memories (id, project, kind, title, content, importance, created_at, updated_at) VALUES ('mem1', 'web', 'decision', 'Use zip', 'because', 3, 1, 1)`,
		`INSERT INTO memory_anchors (project, memory_id, kind, value) VALUES ('web', 'mem1', 'file', 'a.go')`,
		`INSERT INTO events (id, project, agent, session, at, type, payload) VALUES ('ev1', 'web', 'agent-01', '', 1, 'note', '{}')`,
		`INSERT INTO agent_reports (id, project, agent, created_at, summary) VALUES ('rep1', 'web', 'agent-01', 1, 'done')`,
		`INSERT INTO token_usage (project, agent, ai, session_id, turn, kind, model, at, input_tokens, cost_usd) VALUES ('web', 'lead', 'claude', 's', 't1', 'turn', 'opus', 5, 100, 0.25)`,
		`INSERT INTO archived_agents (project, name, id, title, created_at, archived_at) VALUES ('web', 'agent-01', 'id-1', 'Fix it', 1, 2)`,
		`INSERT INTO chats (project, agent, session_id) VALUES ('web', 'lead', 'local-session'), ('web', 'agent-01', '')`,
		`INSERT INTO chat_items (project, agent, id, position, data) VALUES ('web', 'lead', 'i1', 1, '{"kind":"user","text":"hi"}'), ('web', 'agent-01', 'i2', 1, '{"kind":"user","text":"go"}')`,
		`INSERT INTO connectors (project, agent, name, url, auth, updated_at, access_token) VALUES ('web', '', 'linear', 'https://mcp.linear.app', 'oauth', 1, x'00')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := notes.Write(in.NotesPath("web"), "Be kind."); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Secrets.Set(ctx, "web", "", "OPENAI_API_KEY", "sk-secret"); err != nil {
		t.Fatal(err)
	}
	if err := in.Creds.SaveGitHubToken("work", "ghp_example"); err != nil {
		t.Fatal(err)
	}
	if err := in.Creds.SaveClaudeToken("default", "sk-ant-oat01-example"); err != nil {
		t.Fatal(err)
	}
}

func everything(bundle bool) ExportOptions {
	return ExportOptions{
		Projects:   []ProjectChoice{{Name: "web", Categories: Categories, Bundle: bundle}},
		Logins:     Logins,
		Passphrase: pass,
	}
}

func export(t *testing.T, in Instance, opts ExportOptions) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out.agentbox")
	if _, err := in.Export(context.Background(), path, opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	return path
}

func importAll(t *testing.T, in Instance, path, repoPath string) api.InstanceImportReport {
	t.Helper()
	contents, err := in.Inspect(context.Background(), path, pass)
	if err != nil {
		t.Fatal(err)
	}
	req := api.InstanceImportRequest{Path: path, Passphrase: pass, Logins: Logins}
	for _, p := range contents.Projects {
		var kinds []string
		for _, c := range p.Categories {
			kinds = append(kinds, c.Kind)
		}
		req.Projects = append(req.Projects, api.InstanceImportProjectChoice{Name: p.Name, Categories: kinds, Path: repoPath})
	}
	rep, err := in.Import(context.Background(), req, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// snapshot is every exported table's rows of the project, as an export reads
// them, with what an import sets afresh (the repository's place) left out.
func snapshot(t *testing.T, in Instance) map[string][]tableRows {
	t.Helper()
	out := map[string][]tableRows{}
	for kind := range tables {
		ts, err := in.dumpTables(context.Background(), kind, "web")
		if err != nil {
			t.Fatal(err)
		}
		for i := range ts {
			if ts[i].Table == "projects" {
				col := indexOf(ts[i].Columns, "root")
				for _, row := range ts[i].Rows {
					row[col] = ""
				}
			}
		}
		out[kind] = ts
	}
	return out
}

func indexOf(cols []string, name string) int {
	for i, c := range cols {
		if c == name {
			return i
		}
	}
	return -1
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	root, _, _ := newRepo(t)
	src := newInstance(t)
	seed(t, src, root)
	path := export(t, src, everything(false))

	dst := newInstance(t)
	rep := importAll(t, dst, path, "")
	if rep.Conflicted != 0 || !reflect.DeepEqual(rep.ProjectsAdded, []string{"web"}) {
		t.Fatalf("report = %+v", rep)
	}
	if rep.ReportPath == "" {
		t.Fatal("no report written")
	}

	want, got := snapshot(t, src), snapshot(t, dst)
	// The chat's session is the AI tool's on the other machine: it isn't carried.
	for kind := range want {
		if !reflect.DeepEqual(want[kind], got[kind]) {
			t.Errorf("%s differs:\n want %v\n  got %v", kind, want[kind], got[kind])
		}
	}
	p, err := dst.Store.Project(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != root {
		t.Errorf("root = %s, want the checkout that's still there, %s", p.Root, root)
	}
	if p.Section != "sec1" {
		t.Errorf("section = %q", p.Section)
	}
	if text, _ := notes.Read(dst.NotesPath("web")); text != "Be kind.\n" && text != "Be kind." {
		t.Errorf("notes = %q", text)
	}
	if v, err := dst.Secrets.Resolve(ctx, "web", "", "OPENAI_API_KEY"); err != nil || v != "sk-secret" {
		t.Errorf("secret = %q, %v", v, err)
	}
	if tok, err := dst.Creds.GitHubToken("work"); err != nil || tok != "ghp_example" {
		t.Errorf("github = %q, %v", tok, err)
	}
	if tok, err := dst.Creds.ClaudeToken("default"); err != nil || tok != "sk-ant-oat01-example" {
		t.Errorf("claude = %q, %v", tok, err)
	}
	var token []byte
	_ = dst.Store.DB().QueryRow(`SELECT access_token FROM connectors WHERE name = 'linear'`).Scan(&token)
	if token != nil {
		t.Errorf("a connector's sign-in was carried: %x", token)
	}

	// The same file again adds nothing.
	again := importAll(t, dst, path, "")
	if again.Added != 0 || again.Conflicted != 0 || len(again.ProjectsAdded) != 0 {
		t.Fatalf("second import = %s", ReportText(path, again))
	}
	if after := snapshot(t, dst); !reflect.DeepEqual(after, got) {
		t.Fatal("the second import changed the data")
	}
}

func TestConflictsKeepLocal(t *testing.T) {
	ctx := context.Background()
	root, _, _ := newRepo(t)
	src := newInstance(t)
	seed(t, src, root)
	path := export(t, src, everything(false))

	dst := newInstance(t)
	importAll(t, dst, path, "")
	if _, err := dst.Store.DB().ExecContext(ctx, `UPDATE memories SET title = 'Mine' WHERE id = 'mem1'`); err != nil {
		t.Fatal(err)
	}
	rep := importAll(t, dst, path, "")
	if rep.Conflicted != 1 || rep.Added != 0 {
		t.Fatalf("report = %s", ReportText(path, rep))
	}
	var title string
	_ = dst.Store.DB().QueryRow(`SELECT title FROM memories WHERE id = 'mem1'`).Scan(&title)
	if title != "Mine" {
		t.Fatalf("title = %q: this instance's row was overwritten", title)
	}
}

func TestBundleRestoresUnpushedBranch(t *testing.T) {
	ctx := context.Background()
	root, remote, unpushed := newRepo(t)
	src := newInstance(t)
	seed(t, src, root)
	path := export(t, src, ExportOptions{Projects: []ProjectChoice{{Name: "web", Categories: []string{Repository, Settings}, Bundle: true}}, Passphrase: pass})

	dst := newInstance(t)
	contents, err := dst.Inspect(ctx, path, pass)
	if err != nil {
		t.Fatal(err)
	}
	if p := contents.Projects[0]; !p.Bundled || p.Remote != remote || p.Plan != RepoUse {
		t.Fatalf("contents = %+v", p)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if plan, _ := planRepo(ctx, repoRef{Remote: remote}, true, target); plan != RepoBundle {
		t.Fatalf("plan = %s", plan)
	}
	rep := importAll(t, dst, path, target)
	if len(rep.ProjectsAdded) != 1 {
		t.Fatalf("report = %s", ReportText(path, rep))
	}
	if got := run(t, target, "git", "rev-parse", "wip"); got != unpushed {
		t.Fatalf("wip = %s, want %s", got, unpushed)
	}
	if got := run(t, target, "git", "branch", "--show-current"); got != "main" {
		t.Fatalf("checked out %s", got)
	}
	if got := run(t, target, "git", "remote", "get-url", "origin"); got != remote {
		t.Fatalf("origin = %s", got)
	}
	if p, _ := dst.Store.Project(ctx, "web"); p.Root != target {
		t.Fatalf("root = %s", p.Root)
	}
}

func TestCloneByReference(t *testing.T) {
	ctx := context.Background()
	root, remote, _ := newRepo(t)
	src := newInstance(t)
	seed(t, src, root)
	path := export(t, src, ExportOptions{Projects: []ProjectChoice{{Name: "web", Categories: []string{Repository, Settings}}}, Passphrase: pass})
	dst := newInstance(t)
	target := filepath.Join(t.TempDir(), "cloned")
	importAll(t, dst, path, target)
	if got := run(t, target, "git", "remote", "get-url", "origin"); got != remote {
		t.Fatalf("origin = %s", got)
	}
	if p, _ := dst.Store.Project(ctx, "web"); p.Root != target {
		t.Fatalf("root = %s", p.Root)
	}
}

func TestWrongPassphrase(t *testing.T) {
	root, _, _ := newRepo(t)
	src := newInstance(t)
	seed(t, src, root)
	path := export(t, src, everything(false))
	dst := newInstance(t)
	if _, err := dst.Inspect(context.Background(), path, "not it"); !errors.Is(err, portable.ErrWrongPassphrase) {
		t.Fatalf("inspect: %v", err)
	}
	_, err := dst.Import(context.Background(), api.InstanceImportRequest{Path: path, Passphrase: "not it"}, io.Discard, nil)
	if !errors.Is(err, portable.ErrWrongPassphrase) {
		t.Fatalf("import: %v", err)
	}
	if ps, _ := dst.Store.Projects(context.Background()); len(ps) != 0 {
		t.Fatal("a failed import added projects")
	}
}

func TestPlaintextRefusedWithSecrets(t *testing.T) {
	for _, opts := range []ExportOptions{
		{NoEncrypt: true, Logins: []string{LoginGitHub}},
		{NoEncrypt: true, Projects: []ProjectChoice{{Name: "web", Categories: []string{Secrets}}}},
	} {
		if err := opts.Check(); !errors.Is(err, ErrPlaintextSecrets) {
			t.Errorf("%+v: %v", opts, err)
		}
	}
	if err := (ExportOptions{}).Check(); !errors.Is(err, ErrNoPassphrase) {
		t.Errorf("no passphrase: %v", err)
	}

	root, _, _ := newRepo(t)
	src := newInstance(t)
	seed(t, src, root)
	path := export(t, src, ExportOptions{Projects: []ProjectChoice{{Name: "web", Categories: DefaultCategories}}, NoEncrypt: true})
	if enc, _ := portable.Encrypted(path); enc {
		t.Fatal("a plaintext export was encrypted")
	}
	contents, err := newInstance(t).Inspect(context.Background(), path, "")
	if err != nil || contents.Encrypted || len(contents.Projects) != 1 {
		t.Fatalf("contents = %+v, %v", contents, err)
	}
}

func TestPreview(t *testing.T) {
	root, _, _ := newRepo(t)
	src := newInstance(t)
	seed(t, src, root)
	pv, err := src.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Projects) != 1 || pv.Projects[0].BundleSize == 0 {
		t.Fatalf("preview = %+v", pv)
	}
	counts := map[string]int{}
	for _, c := range pv.Projects[0].Categories {
		counts[c.Kind] = c.Items
	}
	if counts[Memory] != 1 || counts[Chats] != 2 || counts[Secrets] != 1 || counts[Skills] != 1 {
		t.Fatalf("counts = %v", counts)
	}
	if pv.Logins[0].Kind != LoginGitHub || !reflect.DeepEqual(pv.Logins[0].Accounts, []string{"work"}) {
		t.Fatalf("logins = %+v", pv.Logins)
	}
}

func TestSameRemote(t *testing.T) {
	for _, pair := range [][2]string{
		{"git@github.com:o/r.git", "https://github.com/o/r"},
		{"https://github.com/O/r.git/", "ssh://git@github.com/o/r"},
	} {
		if !sameRemote(pair[0], pair[1]) {
			t.Errorf("%s and %s should match", pair[0], pair[1])
		}
	}
	if sameRemote("https://github.com/o/r", "https://github.com/o/other") {
		t.Error("different repositories matched")
	}
}
