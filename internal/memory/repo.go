package memory

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Memories shared through the repository: a project's memories written out as
// Markdown files under RepoDir in its checkout, one per memory, so that
// whoever clones the project reads what its agents already learned. A file is
// frontmatter (id, kind, title, importance, created, updated, and the ids it
// supersedes, newest first) and the memory's content as its body: something a
// reviewer reads in a pull request like any other document.
//
// Reading them back is an import, not a second store. Every read that a
// person or a model makes of a project's memory (Memories, Search,
// BuildContext) first brings the rows of SourceRepository in line with the
// files, when the files changed since the last look; from then on a shared
// memory is a row like any other, found by the same searches, ranked by the
// same rules and put in the same contexts.
//
// AgentBox's own copy wins. A file whose id, or any id it supersedes, is one
// of this project's own memories, or one this project's own memories
// supersede, is the same memory seen twice, and the row the project already
// has is the one it keeps: it may have been corrected, resolved or replaced
// since the file was written, and the file is at best that row's past. The
// file is left alone and reported as skipped.

// RepoDir is where a project's shared memories live, relative to its checkout.
const RepoDir = ".agentbox/memories"

// SourceRepository is Memory.Source for a memory imported from RepoDir.
const SourceRepository = "repository"

// maxRepoFile is the largest file the import reads: a memory's content is at
// most MaxContentLen, and the frontmatter is a few lines.
const maxRepoFile = MaxContentLen + 4<<10

// RepoFile is one memory written out for the repository.
type RepoFile struct {
	// Path is relative to the checkout, under RepoDir, with forward slashes.
	Path string
	Data []byte
	// ID is the memory it holds.
	ID string
}

// RepoPath is where the memory id is kept in a checkout.
func RepoPath(id string) string { return path.Join(RepoDir, id+".md") }

// EncodeRepoMemory writes a memory as a repository file. supersedes is its
// chain of replaced memories, the one it replaces first. Strings are written
// as JSON strings, which YAML reads the same, so a title with a colon or a
// quote in it survives.
func EncodeRepoMemory(m Memory, supersedes []string) []byte {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var b bytes.Buffer
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", m.ID)
	fmt.Fprintf(&b, "kind: %s\n", m.Kind)
	fmt.Fprintf(&b, "title: %s\n", q(m.Title))
	fmt.Fprintf(&b, "importance: %d\n", m.Importance)
	fmt.Fprintf(&b, "created: %s\n", m.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "updated: %s\n", m.UpdatedAt.UTC().Format(time.RFC3339))
	if len(supersedes) > 0 {
		list, _ := json.Marshal(supersedes)
		fmt.Fprintf(&b, "supersedes: %s\n", list)
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(m.Content))
	b.WriteString("\n")
	return b.Bytes()
}

// DecodeRepoMemory reads a repository file back: the memory, with its
// project unset, and the ids it supersedes. fallbackID is the file's name
// without .md, used when the frontmatter has no id. It accepts what a person
// editing the file by hand is likely to write: bare, single- or
// double-quoted values, a date without a time, and a list in brackets.
func DecodeRepoMemory(data []byte, fallbackID string) (Memory, []string, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return Memory{}, nil, errors.New("it doesn't start with frontmatter (a line of ---)")
	}
	head, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return Memory{}, nil, errors.New("its frontmatter is never closed with a line of ---")
	}
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}
	m := Memory{ID: fallbackID, Content: strings.TrimSpace(body)}
	var supersedes []string
	for i, line := range strings.Split(head, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Memory{}, nil, fmt.Errorf("frontmatter line %d has no key: %q", i+1, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "id":
			m.ID = unquote(value)
		case "kind":
			m.Kind = unquote(value)
		case "title":
			m.Title = unquote(value)
		case "importance":
			n, err := strconv.Atoi(unquote(value))
			if err != nil {
				return Memory{}, nil, fmt.Errorf("importance is %q, not a number from %d to %d", value, MinImportance, MaxImportance)
			}
			m.Importance = n
		case "created", "updated":
			t, err := frontDate(unquote(value))
			if err != nil {
				return Memory{}, nil, fmt.Errorf("%s is %q: %w", key, value, err)
			}
			if key == "created" {
				m.CreatedAt = t
			} else {
				m.UpdatedAt = t
			}
		case "supersedes":
			supersedes = idList(value)
		}
	}
	if !validID(m.ID) {
		return Memory{}, nil, fmt.Errorf("its id %q isn't one: letters, digits, _ and - only", m.ID)
	}
	return m, supersedes, nil
}

func unquote(v string) string {
	switch {
	case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
		var s string
		if json.Unmarshal([]byte(v), &s) == nil {
			return s
		}
		return v[1 : len(v)-1]
	case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}

func idList(v string) []string {
	v = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "["), "]")
	var out []string
	for _, part := range strings.Split(v, ",") {
		if id := unquote(strings.TrimSpace(part)); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func frontDate(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("not a date like 2026-11-16 or 2026-11-16T09:30:00Z")
}

// validID keeps a file's id to what AgentBox writes, so a hand-written one
// can't be a path or anything a query would trip on.
func validID(id string) bool {
	if id == "" || len(id) > 100 {
		return false
	}
	for _, r := range id {
		if !(r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// RepoImport is what an import did.
type RepoImport struct {
	// Added and Updated are the ids of the rows it wrote, Removed those whose
	// file went away.
	Added, Updated, Removed []string
	// Skipped are files the project already has as its own memory: AgentBox's
	// copy wins. By id.
	Skipped []string
	// Problems are files it couldn't read, a line each, naming the file.
	Problems []string
}

// projectRoot is the project's checkout, "" when the project isn't one this
// database knows (a test's, or Global).
func (s *Store) projectRoot(ctx context.Context, project string) string {
	var root string
	if s.db.QueryRowContext(ctx, `SELECT root FROM projects WHERE name = ?`, project).Scan(&root) != nil {
		return ""
	}
	return root
}

// repoSeen is the fingerprint of each project's RepoDir at its last import,
// by database and project: memory.New is made for every request, and the
// fingerprint has to outlive it.
var (
	repoSeen   sync.Map // repoKey → string
	repoImport sync.Mutex
)

type repoKey struct {
	db      *sql.DB
	project string
}

// syncRepo brings the project's imported memories in line with its files,
// when they changed since it last looked: a directory listing when they
// didn't. Nothing it finds fails the read it runs before; a file it can't
// read is left out, and ImportRepo is what says why.
func (s *Store) syncRepo(ctx context.Context, project string) {
	if project == "" || project == Global {
		return
	}
	root := s.projectRoot(ctx, project)
	if root == "" {
		return
	}
	seen := repoFingerprint(root)
	key := repoKey{s.db, project}
	if was, ok := repoSeen.Load(key); ok && was == seen {
		return
	}
	repoImport.Lock()
	defer repoImport.Unlock()
	if was, ok := repoSeen.Load(key); ok && was == seen {
		return
	}
	if _, err := s.ImportRepo(ctx, project, root); err == nil {
		repoSeen.Store(key, seen)
	}
}

// forgetRepo makes the next read import again, after something changed the
// rows an import compares against.
func (s *Store) forgetRepo(project string) { repoSeen.Delete(repoKey{s.db, project}) }

// repoFingerprint names every file in root's RepoDir by size and time: what
// changes when one is added, edited or removed, a pull or a checkout
// included. The root is part of it, so moving the project imports again.
func repoFingerprint(root string) string {
	entries, err := os.ReadDir(filepath.Join(root, RepoDir))
	if err != nil {
		return root + "\x00none"
	}
	var b strings.Builder
	b.WriteString(root)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "\x00%s:%d:%d", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return b.String()
}

// repoMemory is one file as the import reads it.
type repoMemory struct {
	m          Memory
	supersedes []string
}

// readRepo reads root's RepoDir: every .md file directly in it.
func readRepo(root string) (files []repoMemory, problems []string) {
	dir := filepath.Join(root, RepoDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, fmt.Sprintf("%s: %v", RepoDir, err))
		}
		return nil, problems
	}
	ids := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".md") || !e.Type().IsRegular() || strings.EqualFold(name, "README.md") {
			continue
		}
		rel := path.Join(RepoDir, name)
		info, err := e.Info()
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		if info.Size() > maxRepoFile {
			problems = append(problems, fmt.Sprintf("%s is %d bytes, more than a memory can be", rel, info.Size()))
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		m, supersedes, err := DecodeRepoMemory(data, strings.TrimSuffix(name, ".md"))
		if err == nil {
			m, err = checkRepoMemory(m, info.ModTime())
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		if other, dup := ids[m.ID]; dup {
			problems = append(problems, fmt.Sprintf("%s has the id %s, which %s has already", rel, m.ID, other))
			continue
		}
		ids[m.ID] = rel
		m.Source, m.SourcePath = SourceRepository, rel
		files = append(files, repoMemory{m: m, supersedes: supersedes})
	}
	return files, problems
}

// checkRepoMemory holds a file to what AddMemory holds a memory to, filling
// in what a hand-written one may leave out: its dates are the file's, which
// change only when the file does.
func checkRepoMemory(m Memory, modified time.Time) (Memory, error) {
	if m.Kind == "" {
		m.Kind = KindProject
	}
	if err := oneOf("its kind", m.Kind, Kinds); err != nil {
		return Memory{}, err
	}
	title, err := text("its title", m.Title, MaxTitleLen)
	if err != nil {
		return Memory{}, err
	}
	if title == "" {
		return Memory{}, errors.New("it has no title")
	}
	content, err := text("its content", m.Content, MaxContentLen)
	if err != nil {
		return Memory{}, err
	}
	m.Title, m.Content = title, content
	if m.Importance == 0 {
		m.Importance = DefaultImportance
	}
	m.Importance = max(MinImportance, min(MaxImportance, m.Importance))
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = m.CreatedAt
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = m.UpdatedAt
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt, m.UpdatedAt = modified, modified
	}
	m.CreatedAt, m.UpdatedAt = stamp(m.CreatedAt), stamp(m.UpdatedAt)
	return m, nil
}

// ImportRepo brings the project's SourceRepository rows in line with the
// files in root's RepoDir: a row for each file the project doesn't already
// have as its own, rewritten when its file changed, and gone when its file
// is. syncRepo runs it when the files changed; it is exported for the
// daemon's sake, which runs it after sharing, and for tests.
func (s *Store) ImportRepo(ctx context.Context, project, root string) (RepoImport, error) {
	if err := requireProject(project); err != nil {
		return RepoImport{}, err
	}
	if project == Global {
		return RepoImport{}, errors.New("memories for every project aren't shared through a repository")
	}
	files, problems := readRepo(root)
	out := RepoImport{Problems: problems}

	// What the project already has as its own, and what of it is the past
	// of what it has: the two halves of "the same memory".
	own, replaced := map[string]bool{}, map[string]bool{}
	imported := map[string]Memory{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, COALESCE(supersedes_id, ''), source FROM memories WHERE project = ?`, project)
	if err != nil {
		return RepoImport{}, err
	}
	for rows.Next() {
		var id, supersedes, source string
		if err := rows.Scan(&id, &supersedes, &source); err != nil {
			_ = rows.Close()
			return RepoImport{}, err
		}
		if source == SourceRepository {
			imported[id] = Memory{}
			continue
		}
		own[id] = true
		if supersedes != "" {
			replaced[supersedes] = true
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return RepoImport{}, err
	}
	for id := range imported {
		m, err := s.Memory(ctx, project, id)
		if err != nil {
			return RepoImport{}, err
		}
		imported[id] = m
	}

	inRepo := map[string]bool{}
	for _, f := range files {
		inRepo[f.m.ID] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RepoImport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range files {
		m := f.m
		chain := append([]string{m.ID}, f.supersedes...)
		if slices.ContainsFunc(chain, func(id string) bool { return own[id] || replaced[id] }) {
			out.Skipped = append(out.Skipped, m.ID)
			continue
		}
		// The memory it replaces, when that is another shared one: the
		// pointer is what makes the older file's row superseded. One that
		// is nowhere here is history this project never had.
		var supersedes string
		if len(f.supersedes) > 0 && (inRepo[f.supersedes[0]] || imported[f.supersedes[0]].ID != "") && f.supersedes[0] != m.ID {
			supersedes = f.supersedes[0]
		}
		if was, ok := imported[m.ID]; ok {
			if was.Kind == m.Kind && was.Title == m.Title && was.Content == m.Content && was.Importance == m.Importance &&
				was.UpdatedAt.Equal(m.UpdatedAt) && was.SupersedesID == supersedes && was.SourcePath == m.SourcePath {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE memories SET kind = ?, title = ?, content = ?, importance = ?, created_at = ?, updated_at = ?,
				 supersedes_id = ?, source_path = ? WHERE project = ? AND id = ? AND source = ?`,
				m.Kind, m.Title, m.Content, m.Importance, m.CreatedAt.UnixMilli(), m.UpdatedAt.UnixMilli(),
				nullable(supersedes), m.SourcePath, project, m.ID, SourceRepository); err != nil {
				return RepoImport{}, err
			}
			out.Updated = append(out.Updated, m.ID)
			continue
		}
		// An id is unique across every project: one another project (or
		// AgentBox-wide memory) holds is that project's, and this file has
		// to wait for its own id.
		var taken string
		if err := tx.QueryRowContext(ctx, `SELECT project FROM memories WHERE id = ?`, m.ID).Scan(&taken); err == nil {
			out.Problems = append(out.Problems, fmt.Sprintf("%s: the id %s is a memory of another project", m.SourcePath, m.ID))
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return RepoImport{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memories (id, project, kind, title, content, importance, created_at, updated_at, supersedes_id, source, source_path)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.ID, project, m.Kind, m.Title, m.Content, m.Importance, m.CreatedAt.UnixMilli(), m.UpdatedAt.UnixMilli(),
			nullable(supersedes), SourceRepository, m.SourcePath); err != nil {
			return RepoImport{}, err
		}
		out.Added = append(out.Added, m.ID)
	}
	// A shared memory whose file is gone was taken out of the repository:
	// the project stops reading it. One the file still names but the
	// project now has as its own past (skipped above) stays, as history.
	for id := range imported {
		if inRepo[id] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE project = ? AND id = ? AND source = ?`,
			project, id, SourceRepository); err != nil {
			return RepoImport{}, err
		}
		out.Removed = append(out.Removed, id)
	}
	if err := tx.Commit(); err != nil {
		return RepoImport{}, err
	}
	sort.Strings(out.Removed)
	return out, nil
}

// RepoExport writes the project's memories out for the repository: those
// named, or every live one of the project's own when ids is empty. Each comes
// with the chain of memories it supersedes, and remove lists the files of
// that chain, which the newer file replaces in the repository. A memory read
// from the repository is already there, and an AgentBox-wide one is the
// user's, not the project's: neither is written.
func (s *Store) RepoExport(ctx context.Context, project string, ids []string) (files []RepoFile, remove []string, err error) {
	if err := requireProject(project); err != nil {
		return nil, nil, err
	}
	if project == Global {
		return nil, nil, errors.New("memories for every project aren't shared through a repository")
	}
	var memories []Memory
	if len(ids) == 0 {
		memories, err = s.queryMemories(ctx, `WHERE m.project = ? AND m.source = '' AND `+live+` ORDER BY m.created_at, m.id`, project)
		if err != nil {
			return nil, nil, err
		}
	} else {
		for _, id := range ids {
			m, err := s.Memory(ctx, project, id)
			if err != nil {
				return nil, nil, err
			}
			if m.Source == SourceRepository {
				continue
			}
			memories = append(memories, m)
		}
	}
	gone := map[string]bool{}
	for _, m := range memories {
		chain, err := s.supersededChain(ctx, project, m)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, RepoFile{Path: RepoPath(m.ID), ID: m.ID, Data: EncodeRepoMemory(m, chain)})
		for _, id := range chain {
			gone[RepoPath(id)] = true
		}
	}
	for _, f := range files {
		delete(gone, f.Path)
	}
	for p := range gone {
		remove = append(remove, p)
	}
	sort.Strings(remove)
	return files, remove, nil
}

// supersededChain is every memory m replaces, the one it replaces directly
// first, as far back as the project still has them.
func (s *Store) supersededChain(ctx context.Context, project string, m Memory) ([]string, error) {
	var chain []string
	seen := map[string]bool{m.ID: true}
	for next := m.SupersedesID; next != "" && !seen[next] && len(chain) < 50; {
		seen[next] = true
		chain = append(chain, next)
		var up sql.NullString
		err := s.db.QueryRowContext(ctx, `SELECT supersedes_id FROM memories WHERE project = ? AND id = ?`, project, next).Scan(&up)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, err
		}
		next = up.String
	}
	return chain, nil
}
