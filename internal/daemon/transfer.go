package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/portable"
	"agentbox/internal/transfer"
)

// Export and import of this instance's data (package transfer). The preview
// and the look inside a file answer at once; writing and merging are jobs, so
// the CLI and the app follow the same progress.

func (s *Server) transferInstance() transfer.Instance {
	home, _ := os.UserHomeDir()
	return transfer.Instance{
		Store:     s.store,
		Secrets:   s.secrets(),
		Creds:     credentials.Store{Dir: s.cfg.Paths.Credentials()},
		NotesPath: s.cfg.Paths.ProjectNotes,
		Version:   Version,
		Tmp:       os.TempDir(),
		Home:      home,
		Reports:   filepath.Join(s.cfg.Paths.Data, "imports"),
	}
}

// transferCode is the api.Error code of an export that can't be opened.
func transferCode(err error) string {
	var newer *portable.NewerSchemaError
	switch {
	case errors.Is(err, portable.ErrPassphraseNeeded):
		return api.ErrorPassphraseNeeded
	case errors.Is(err, portable.ErrWrongPassphrase):
		return api.ErrorWrongPassphrase
	case errors.As(err, &newer):
		return api.ErrorNewerExport
	}
	return ""
}

func fullPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q isn't a full path", path)
	}
	return filepath.Clean(path), nil
}

func (s *Server) previewExport(w http.ResponseWriter, r *http.Request) error {
	preview, err := s.transferInstance().Preview(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, preview)
}

func (s *Server) exportInstance(w http.ResponseWriter, r *http.Request) error {
	var req api.InstanceExportRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	path, err := fullPath(req.Path)
	if err != nil {
		return err
	}
	opts := transfer.ExportOptions{Logins: req.Logins, Passphrase: req.Passphrase, NoEncrypt: req.NoEncrypt}
	for _, p := range req.Projects {
		opts.Projects = append(opts.Projects, transfer.ProjectChoice{Name: p.Name, Categories: p.Categories, Bundle: p.Bundle})
	}
	// Refused before a job starts, so the answer says why.
	if err := opts.Check(); err != nil {
		return err
	}
	if len(opts.Projects) == 0 && len(opts.Logins) == 0 {
		return errors.New("nothing to export: choose a project or a login")
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		return fmt.Errorf("%s isn't a folder here", filepath.Dir(path))
	}
	in := s.transferInstance()
	return s.startJob(w, "export", path, func(ctx context.Context, log io.Writer) (any, error) {
		res, err := in.Export(ctx, path, opts, log)
		if err != nil {
			return nil, err
		}
		return api.InstanceExportResult{Path: res.Path, Size: res.Size, Encrypted: res.Encrypted, Entries: len(res.Entries)}, nil
	})
}

func (s *Server) inspectImport(w http.ResponseWriter, r *http.Request) error {
	var req api.InstanceImportInspectRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	path, err := fullPath(req.Path)
	if err != nil {
		return err
	}
	contents, err := s.transferInstance().Inspect(r.Context(), path, req.Passphrase)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, contents)
}

func (s *Server) importInstance(w http.ResponseWriter, r *http.Request) error {
	var req api.InstanceImportRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	path, err := fullPath(req.Path)
	if err != nil {
		return err
	}
	req.Path = path
	for i, p := range req.Projects {
		if p.Path == "" {
			continue
		}
		if req.Projects[i].Path, err = fullPath(p.Path); err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
	}
	// The file is opened once before the job, so a wrong passphrase or a
	// newer export is the answer rather than a failed job.
	in := s.transferInstance()
	contents, err := in.Inspect(r.Context(), path, req.Passphrase)
	if err != nil {
		return err
	}
	for _, p := range req.Projects {
		if !slices.ContainsFunc(contents.Projects, func(c api.InstanceImportProjectContents) bool { return c.Name == p.Name }) {
			return fmt.Errorf("the export has no project %s", p.Name)
		}
	}
	return s.startJob(w, "import", path, func(ctx context.Context, log io.Writer) (any, error) {
		report, err := in.Import(ctx, req, log, func(project string) {
			if err := s.serveLeadAPI(project); err != nil {
				s.logf("lead API socket for %s: %v", project, err)
			}
			s.events.publish(api.EventProject, api.ProjectChange{Name: project})
		})
		if err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(log, "%d added, %d skipped, %d conflicted\n", report.Added, report.Skipped, report.Conflicted)
		if report.ReportPath != "" {
			_, _ = fmt.Fprintf(log, "Report: %s\n", report.ReportPath)
		}
		return report, nil
	})
}
