package daemon

import (
	"context"
	"errors"
	"net/http"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/cookieimport"
	"agentbox/internal/secrets"
)

// A project's browser cookie import: an export the user made of their own
// browser's cookies, parsed here (internal/cookieimport), cut down to the
// domains they picked, and sealed as a project secret. Agents created after
// it get the cookies in their Chromium (agent.Manager.applyBrowserCookies).
// No answer here carries a cookie: the preview counts them by site, and the
// status names the domains.

func (s *Server) browserCookiesInfo(ctx context.Context, project string) (api.BrowserCookies, error) {
	bc, ok, err := s.secrets().BrowserCookies(ctx, project)
	if err != nil || !ok {
		return api.BrowserCookies{Domains: []string{}}, err
	}
	at := bc.ImportedAt
	return api.BrowserCookies{Imported: true, Domains: bc.Domains, Cookies: len(bc.Cookies), Format: bc.Format, ImportedAt: &at}, nil
}

func (s *Server) getBrowserCookies(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	info, err := s.browserCookiesInfo(r.Context(), p.Name)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

// previewBrowserCookies says what an export holds, by site, so the user can
// pick domains. Nothing is stored.
func (s *Server) previewBrowserCookies(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.store.Project(r.Context(), r.PathValue("project")); err != nil {
		return err
	}
	var req api.BrowserCookiesPreviewRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	cookies, format, err := cookieimport.Parse(req.Export, time.Now())
	if err != nil {
		return err
	}
	out := api.BrowserCookiesPreview{Format: format, Cookies: len(cookies), Domains: []api.CookieDomain{}}
	for _, d := range cookieimport.Domains(cookies) {
		out.Domains = append(out.Domains, api.CookieDomain{Domain: d.Domain, Cookies: d.Cookies})
	}
	return writeJSON(w, http.StatusOK, out)
}

// importBrowserCookies replaces the project's import with an export's cookies
// of the domains picked.
func (s *Server) importBrowserCookies(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	var req api.ImportBrowserCookiesRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	var domains []string
	for _, d := range req.Domains {
		if d = cookieimport.NormalizeDomain(d); d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		return errors.New("pick the domains to import cookies for: only those are kept")
	}
	cookies, format, err := cookieimport.Parse(req.Export, time.Now())
	if err != nil {
		return err
	}
	kept := cookieimport.Filter(cookies, domains)
	if err := s.secrets().SetBrowserCookies(r.Context(), p.Name, secrets.BrowserCookies{
		Domains: domains, Format: format, Cookies: kept, ImportedAt: time.Now().Truncate(time.Second),
	}); err != nil {
		return err
	}
	info, err := s.browserCookiesInfo(r.Context(), p.Name)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

func (s *Server) removeBrowserCookies(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	if err := s.secrets().RemoveBrowserCookies(r.Context(), p.Name); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
