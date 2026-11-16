package hub

import (
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"agentbox/hubapi"
)

// The hub's own pages, which a browser reaches from a setup link or a
// pairing's approval: they work without the web app, so a hub run with no
// flags has them. Each is the same page with a different part shown; what
// they do, they do with the JSON API, as any client would.

//go:embed pages.html
var pagesHTML string

var pagesTemplate = template.Must(template.New("page").Parse(pagesHTML))

type pageData struct {
	Page      string // home, setup, approve or connect
	SignedIn  bool
	Claimed   bool
	Passwords bool
	Code      string // approve: the code in the link
	Next      string // connect: where to go once this browser is approved
	User      string
	Version   string
}

func (s *Server) pages(mux *http.ServeMux) {
	page := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			d := pageData{Page: name, Version: s.Version}
			d.Claimed = s.claimed(ctx)
			if n, err := s.Store.CountPasswords(ctx); err == nil {
				d.Passwords = n > 0
			}
			if u, _, err := s.authenticate(r); err == nil {
				d.SignedIn, d.User = true, cmpOr(u.Email, u.Name)
			}
			switch name {
			case "approve":
				d.Code = normalizeCode(r.URL.Query().Get("code"))
			case "connect":
				d.Next = r.URL.Query().Get("next")
				if !strings.HasPrefix(d.Next, "/") || strings.HasPrefix(d.Next, "//") {
					d.Next = "/hub/"
				}
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if err := pagesTemplate.Execute(w, d); err != nil {
				s.logf("page %s: %v", name, err)
			}
		}
	}
	mux.HandleFunc("GET /hub/{$}", page("home"))
	mux.HandleFunc("GET "+hubapi.PathSetupPage, page("setup"))
	mux.HandleFunc("GET "+hubapi.PathApprovePage, page("approve"))
	mux.HandleFunc("GET /hub/connect", page("connect"))
	mux.HandleFunc("GET /hub/qr.svg", s.qrImage)
	mux.HandleFunc("POST /hub/session", s.browserSession)
}

// qrImage draws the QR code of a pending pairing's approval link, for the
// page a browser waits on while it's approved.
func (s *Server) qrImage(w http.ResponseWriter, r *http.Request) {
	code := normalizeCode(r.URL.Query().Get("code"))
	s.mu.Lock()
	p := s.pairings[code]
	s.mu.Unlock()
	if p == nil {
		http.NotFound(w, r)
		return
	}
	rows, err := QR(s.baseURL(r) + hubapi.PathApprovePage + "?code=" + code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(svgQR(rows)))
}

// browserSession keeps a device token a page got from an approved pairing in
// the session cookie, where the page's script can't read it any more.
func (s *Server) browserSession(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) { _ = writeJSON(w, status, hubapi.Error{Error: msg}) }
	if !sameOrigin(r) || r.Header.Get("Origin") == "" {
		fail(http.StatusForbidden, errOrigin.msg)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	if !strings.HasPrefix(req.Token, sessionPrefix) {
		fail(http.StatusUnauthorized, errSignIn.msg)
		return
	}
	sess, err := s.Store.Session(r.Context(), hashToken(req.Token), time.Now())
	if errors.Is(err, ErrNotFound) {
		fail(http.StatusUnauthorized, errSignIn.msg)
		return
	} else if err != nil {
		fail(http.StatusInternalServerError, err.Error())
		return
	}
	setCookie(w, r, req.Token, sess.ExpiresAt)
	w.WriteHeader(http.StatusNoContent)
}
