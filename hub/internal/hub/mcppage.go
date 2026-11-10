package hub

import (
	_ "embed"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"agentbox/hubapi"
)

//go:embed mcp.html
var mcpHTML string

var mcpTemplate = template.Must(template.New("mcp").Parse(mcpHTML))

// authorizeParams are the OAuth parameters the page carries from its first
// request to its last, as hidden fields.
var authorizeParams = []string{"response_type", "client_id", "redirect_uri", "code_challenge", "code_challenge_method", "state", "scope", "resource"}

type authorizeData struct {
	Fatal        string // what's wrong with the request itself: nothing to send the app back with
	Error        string // what's wrong with the code
	Client       string
	RedirectHost string
	Params       map[string]string
	Code         string
	Pairing      *MCPPairing // once the code is right: what to allow
	Machine      string
	AccessText   string
	ProjectsText string
}

func accessText(access string) string {
	switch access {
	case hubapi.MCPAccessRead:
		return "Read only: see projects, agents, pull requests and the lead's chat"
	case hubapi.MCPAccessChat:
		return "Chat with the lead: read everything, and send messages to a project's lead, which can start agents and change code"
	case hubapi.MCPAccessFull:
		return "Full lead tools: everything a project's lead can do, directly — create, direct and retire agents"
	}
	return access
}

// authorizePage is the OAuth authorization endpoint. A GET shows a form
// asking for a pairing's code; posting a right code shows what the pairing
// allows, and posting "allow" there uses the code up and sends the app back
// with an authorization code. Nobody is signed in on this page, and it reads
// no cookie: the pairing code is the whole of what it trusts.
func (s *Server) authorizePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	render := func(status int, d authorizeData) {
		w.WriteHeader(status)
		if err := mcpTemplate.Execute(w, d); err != nil {
			s.logf("page authorize: %v", err)
		}
	}
	if err := r.ParseForm(); err != nil {
		render(http.StatusBadRequest, authorizeData{Fatal: "The request couldn't be read."})
		return
	}
	// The parameters come from the query the app sent the browser here with,
	// and after that from this page's own form.
	values := r.URL.Query()
	if r.Method == http.MethodPost {
		if r.Header.Get("Origin") == "" || !sameOrigin(r) {
			render(http.StatusForbidden, authorizeData{Fatal: errOrigin.msg + "."})
			return
		}
		values = r.PostForm
	}
	d := authorizeData{Params: map[string]string{}}
	for _, name := range authorizeParams {
		if v := values.Get(name); v != "" {
			d.Params[name] = v
		}
	}
	// A client or a redirect URI the hub doesn't know is never redirected
	// to: that would make the hub send people wherever a link says.
	client, err := s.Store.OAuthClient(r.Context(), d.Params["client_id"])
	if err != nil {
		render(http.StatusBadRequest, authorizeData{Fatal: "The app that sent you here isn't registered with this hub. Remove it in that app and add it again."})
		return
	}
	redirect := d.Params["redirect_uri"]
	if redirect == "" && len(client.RedirectURIs) == 1 {
		redirect = client.RedirectURIs[0]
	}
	if !slices.Contains(client.RedirectURIs, redirect) {
		render(http.StatusBadRequest, authorizeData{Fatal: "The app asked to be sent back to an address it didn't register."})
		return
	}
	back, _ := url.Parse(redirect)
	d.Client, d.RedirectHost = client.Name, cmpOr(back.Host, back.Scheme+":")
	// From here on, what's wrong goes back to the app, as OAuth says.
	finish := func(params ...string) {
		q := back.Query()
		for i := 0; i+1 < len(params); i += 2 {
			q.Set(params[i], params[i+1])
		}
		if state := d.Params["state"]; state != "" {
			q.Set("state", state)
		}
		back.RawQuery = q.Encode()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.Redirect(w, r, back.String(), http.StatusSeeOther)
	}
	if d.Params["response_type"] != "code" {
		finish("error", "unsupported_response_type", "error_description", "use response_type=code")
		return
	}
	if d.Params["code_challenge"] == "" || d.Params["code_challenge_method"] != "S256" {
		finish("error", "invalid_request", "error_description", "send a PKCE code_challenge with code_challenge_method=S256")
		return
	}
	if r.Method != http.MethodPost {
		render(http.StatusOK, d)
		return
	}
	step := r.PostForm.Get("step")
	if step == "deny" {
		finish("error", "access_denied", "error_description", "the user cancelled")
		return
	}
	ip := clientIP(r)
	if s.tooManyFailures("mcp:"+ip, false) {
		d.Error = "Too many wrong codes from this address: try again in a few minutes."
		render(http.StatusTooManyRequests, d)
		return
	}
	code := normalizeCode(r.PostForm.Get("pairing_code"))
	s.mu.Lock()
	c, ok := s.mcpCodes[code]
	if ok && !time.Now().Before(c.expires) {
		delete(s.mcpCodes, code)
		ok = false
	}
	if ok && step == "allow" {
		delete(s.mcpCodes, code) // a code works once
	}
	s.mu.Unlock()
	var pairing MCPPairing
	if ok {
		if pairing, err = s.Store.MCPPairing(r.Context(), c.pairingID); err != nil {
			ok = false
		}
	}
	if !ok {
		s.tooManyFailures("mcp:"+ip, true)
		s.logf("wrong MCP pairing code from %s (for %q)", ip, client.Name)
		d.Error = "That code isn't right, or it ran out. Codes last 10 minutes and work once: get a new one in the AgentBox app."
		render(http.StatusOK, d)
		return
	}
	if step != "allow" {
		d.Code, d.Pairing, d.AccessText = code, &pairing, accessText(pairing.Access)
		d.ProjectsText = cmpOr(strings.Join(pairing.Projects, ", "), "All of them")
		if e, err := s.Store.Environment(r.Context(), pairing.EnvironmentID); err == nil {
			d.Machine = cmpOr(e.Hostname, e.Name)
		}
		render(http.StatusOK, d)
		return
	}
	s.logf("the app %q signed in to the pairing %q from %s", client.Name, pairing.Label, ip)
	finish("code", s.newOAuthCode(oauthCode{pairingID: pairing.ID, clientID: client.ID, redirectURI: d.Params["redirect_uri"], challenge: d.Params["code_challenge"]}))
}
