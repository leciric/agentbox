package daemon

import (
	"net/http"

	"agentbox/internal/api"
	"agentbox/internal/worktreefs"
)

// A past agent's files, read-only in the app's file panel: its worktree went
// with its machine, so they are read from its branch's commit in the
// project's repository (worktreefs.ListAt and the rest), the local branch or,
// once that was deleted as merged or pushed, origin's. Same answers as the
// live routes in filetree.go and files.go; a commit doesn't change, so nothing
// is watched or cached.

// archivedCommit is the past agent's project repository, the commit its
// branch is at and what its changes are measured against: the commit, and
// its name for FileStatus.Base.
func (s *Server) archivedCommit(r *http.Request) (root, commit, base, baseName string, err error) {
	a, err := s.archivedFromPath(r)
	if err != nil {
		return "", "", "", "", err
	}
	p, err := s.store.Project(r.Context(), a.Project)
	if err != nil {
		return "", "", "", "", err
	}
	if commit, base, err = archivedRange(p.Root, a); err != nil {
		return "", "", "", "", err
	}
	baseName = a.BaseRef
	if baseName == "" && base != "" {
		baseName = base[:min(len(base), 12)]
	}
	return p.Root, commit, base, baseName, nil
}

func (s *Server) archivedFiles(w http.ResponseWriter, r *http.Request) error {
	root, commit, _, _, err := s.archivedCommit(r)
	if err != nil {
		return err
	}
	files, truncated, err := worktreefs.FilesAt(root, commit, filesLimit)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.WorktreeFiles{Files: files, Truncated: truncated})
}

// archivedFolder takes ignored=1 as the live route does, but a commit has
// nothing ignored.
func (s *Server) archivedFolder(w http.ResponseWriter, r *http.Request) error {
	root, commit, _, _, err := s.archivedCommit(r)
	if err != nil {
		return err
	}
	list, err := worktreefs.ListAt(root, commit, r.URL.Query().Get("path"))
	if err != nil {
		return fileErr(err)
	}
	return writeJSON(w, http.StatusOK, list)
}

func (s *Server) archivedStatus(w http.ResponseWriter, r *http.Request) error {
	root, commit, base, baseName, err := s.archivedCommit(r)
	if err != nil {
		return err
	}
	changes, truncated, err := worktreefs.StatusAt(root, base, commit)
	if err != nil {
		return err
	}
	if base == "" {
		baseName = ""
	}
	return writeJSON(w, http.StatusOK, api.FileStatus{Base: baseName, Changes: changes, Truncated: truncated})
}

func (s *Server) archivedRead(w http.ResponseWriter, r *http.Request) error {
	root, commit, base, baseName, err := s.archivedCommit(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	content, err := worktreefs.ReadAt(root, commit, q.Get("path"), q.Get("full") == "1")
	if err != nil {
		return fileErr(err)
	}
	if q.Get("base") == "1" && base != "" && !content.Binary && !content.PDF && !content.TooLarge && (content.Image == "" || content.Text != "") {
		if changes, _, err := worktreefs.StatusAt(root, base, commit); err == nil {
			content.Base = baseName
			content.Status = changes[content.Path]
			if content.Status == api.FileModified {
				content.BaseText, content.BaseMissing, _ = worktreefs.BaseText(root, base, content.Path)
			}
		}
	}
	return writeJSON(w, http.StatusOK, content)
}

func (s *Server) archivedSQLite(w http.ResponseWriter, r *http.Request) error {
	root, commit, _, _, err := s.archivedCommit(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	offset, limit := sqlitePage(q)
	out, err := worktreefs.SQLiteAt(r.Context(), root, commit, q.Get("path"), q.Get("table"), offset, limit)
	if err != nil {
		return sqliteErr(err)
	}
	return writeJSON(w, http.StatusOK, out)
}
