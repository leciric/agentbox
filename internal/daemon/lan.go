package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/lan"
	"agentbox/internal/state"
)

// Chatting from a phone on the local network. Off by default; turned on, the
// daemon serves the app's web version (desktop web.html, the one a hub
// serves) on a TCP port of the network, and the part of its API a chat needs
// under /api, to phones paired with a QR code and to nothing else.
//
// Pairing: the app or the CLI asks for a pairing (POST /v1/lan/pairings), a
// one-time secret valid for pairingTTL, which the QR code carries in the
// URL's fragment. The page posts it to /lan/pair and gets a token of its own
// in a cookie; state keeps only the token's hash (state.AddPhone). Revoking a
// phone deletes it and ends whatever it has open, its event stream first.
//
// Where the port is opened depends on where the daemon runs. On a machine of
// its own, it listens itself, on every interface. In AgentBox's VM on a Linux
// host (hostos.Linux), the network is the host's: the VM's supervisor opens
// the port there and passes each request on over the daemon's socket, under
// /v1/lan/net/, and reports how that went (PUT /v1/lan/host). The socket is
// already forwarded to the host, so this needs nothing new of the VM. In the
// Mac's and Windows' VMs nothing brings the network to the daemon yet.
//
// It is plain HTTP: the phone and the daemon share no certificate. The
// cookie is HttpOnly and SameSite=Strict, and a phone reaches only the chat
// (lanAllowed), so what someone on the same network could take by listening
// is a chat, not the machine.

// pairingTTL is how long a QR code pairs a phone.
const pairingTTL = 10 * time.Minute

// hostReportTTL is how old the supervisor's last report may be before the
// supervisor is taken as gone: it reports every lanReportEvery.
const hostReportTTL = 10 * time.Second

// lanCookie holds a paired phone's token.
const lanCookie = "agentbox_phone"

// lanNetPrefix is where the supervisor passes the network's requests on to
// the daemon, over its socket.
const lanNetPrefix = "/v1/lan/net"

type lanState struct {
	mu        sync.Mutex
	srv       *http.Server // the daemon's own listener, when it has one
	port      int          // what srv listens on
	listenErr string
	pairings  map[string]time.Time // one-time secrets, to when each expires
	host      *api.LANHostReport   // the supervisor's last report
	hostAt    time.Time
	open      map[string]map[*context.CancelFunc]struct{} // what each phone has open, by phone
	seen      map[string]time.Time                        // when each phone's last visit was recorded
	api       http.Handler                                // routes(), as phones reach it

	// own says the daemon opens the port itself: everywhere but in the
	// Linux host's VM, whose supervisor opens it on the host. addresses are
	// this machine's, for when it does. Fields for tests.
	own       bool
	addresses func() []string
}

func newLANState() *lanState {
	return &lanState{
		pairings: map[string]time.Time{}, open: map[string]map[*context.CancelFunc]struct{}{}, seen: map[string]time.Time{},
		own: hostos.OS() != hostos.Linux, addresses: lan.Addresses,
	}
}

func (s *Server) lanPort(ctx context.Context) (int, error) {
	v, err := s.store.Setting(ctx, state.SettingLANPort)
	if err != nil || v == "" {
		return state.DefaultLANPort, err
	}
	port, err := strconv.Atoi(v)
	if err != nil || port < 1 || port > 65535 {
		return state.DefaultLANPort, nil
	}
	return port, nil
}

// applyLAN opens or closes the daemon's own port to match the settings, and
// ends everything phones have open when it's turned off.
func (s *Server) applyLAN(ctx context.Context) {
	on, err := s.store.Flag(ctx, state.SettingLAN)
	if err != nil {
		s.logf("phones: %v", err)
		return
	}
	port, _ := s.lanPort(ctx)
	l := s.lan
	l.mu.Lock()
	defer l.mu.Unlock()
	if !on {
		for _, conns := range l.open {
			for cancel := range conns {
				(*cancel)()
			}
		}
		clear(l.pairings)
	}
	want := on && l.own
	if l.srv != nil && (!want || l.port != port) {
		_ = l.srv.Close()
		l.srv = nil
	}
	l.listenErr = ""
	if !want || l.srv != nil {
		return
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		l.listenErr = fmt.Sprintf("port %d can't be opened: %v", port, err)
		s.logf("phones: %s", l.listenErr)
		return
	}
	srv := &http.Server{
		Handler:           s.lanHandler(false),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	l.srv, l.port = srv, port
	go func() { _ = srv.Serve(ln) }()
	s.logf("phones can pair on port %d", port)
}

func (s *Server) closeLAN() {
	s.lan.mu.Lock()
	defer s.lan.mu.Unlock()
	if s.lan.srv != nil {
		_ = s.lan.srv.Close()
		s.lan.srv = nil
	}
}

func (s *Server) lanStatus(ctx context.Context) (api.LANStatus, error) {
	on, err := s.store.Flag(ctx, state.SettingLAN)
	if err != nil {
		return api.LANStatus{}, err
	}
	port, err := s.lanPort(ctx)
	if err != nil {
		return api.LANStatus{}, err
	}
	out := api.LANStatus{Enabled: on, Port: port, URLs: []string{}, Phones: []api.LANPhone{}, WebVersion: s.lanWebVersion()}
	phones, err := s.store.Phones(ctx)
	if err != nil {
		return api.LANStatus{}, err
	}
	for _, p := range phones {
		out.Phones = append(out.Phones, lanPhone(p))
	}
	var addrs []string
	l := s.lan
	l.mu.Lock()
	switch {
	case l.own:
		out.Listening = l.srv != nil
		out.Error = l.listenErr
		addrs = l.addresses()
		if on && hostos.InVM() {
			// Nothing forwards the VM's port to the network of the computer
			// it runs on yet.
			out.Listening = false
			out.Error = fmt.Sprintf("phones can't reach AgentBox's VM on %s yet: this works on Linux for now", hostos.Name())
			addrs = nil
		}
	case l.host != nil && time.Since(l.hostAt) < hostReportTTL:
		out.Listening = l.host.Listening && l.host.Port == port
		out.Error = l.host.Error
		addrs = l.host.Addresses
	case on:
		out.Error = "waiting for AgentBox's VM to open the port on this computer"
	}
	l.mu.Unlock()
	if on && out.Error == "" && out.Listening && len(addrs) == 0 {
		out.Error = "this computer isn't on a network a phone could reach it on"
	}
	for _, a := range addrs {
		out.URLs = append(out.URLs, "http://"+net.JoinHostPort(a, strconv.Itoa(port))+"/")
	}
	return out, nil
}

func lanPhone(p state.Phone) api.LANPhone {
	return api.LANPhone{ID: p.ID, Name: p.Name, Paired: p.Created, LastSeen: p.LastSeen, LastAddr: p.LastAddr}
}

// publishLAN says something in GET /v1/lan changed, without saying what: the
// event stream reaches phones too, which have no business with the list of
// phones.
func (s *Server) publishLAN(context.Context) {
	s.events.publish(api.EventLAN, struct{}{})
}

func (s *Server) getLAN(w http.ResponseWriter, r *http.Request) error {
	st, err := s.lanStatus(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, st)
}

func (s *Server) updateLAN(w http.ResponseWriter, r *http.Request) error {
	var req api.UpdateLANRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if req.Port != nil {
		if *req.Port < 1024 || *req.Port > 65535 {
			return fmt.Errorf("the port must be between 1024 and 65535, not %d", *req.Port)
		}
		if err := s.store.SetSetting(ctx, state.SettingLANPort, strconv.Itoa(*req.Port)); err != nil {
			return err
		}
	}
	if req.Enabled != nil {
		if err := s.store.SetFlag(ctx, state.SettingLAN, *req.Enabled); err != nil {
			return err
		}
	}
	s.applyLAN(s.runCtx)
	s.publishLAN(ctx)
	return s.getLAN(w, r)
}

func (s *Server) addLANPairing(w http.ResponseWriter, r *http.Request) error {
	st, err := s.lanStatus(r.Context())
	if err != nil {
		return err
	}
	if !st.Enabled {
		return errors.New("chatting from a phone is off: turn it on first")
	}
	if len(st.URLs) == 0 {
		msg := "there's no address a phone could open"
		if st.Error != "" {
			msg += ": " + st.Error
		}
		return errors.New(msg)
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	out := api.LANPairing{Expires: time.Now().Add(pairingTTL)}
	for _, u := range st.URLs {
		out.URLs = append(out.URLs, u+"#pair="+secret)
	}
	if out.QR, err = lan.QR(out.URLs[0]); err != nil {
		return err
	}
	s.lan.mu.Lock()
	for k, exp := range s.lan.pairings {
		if time.Now().After(exp) {
			delete(s.lan.pairings, k)
		}
	}
	s.lan.pairings[secret] = out.Expires
	s.lan.mu.Unlock()
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) removeLANPhone(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if err := s.store.RemovePhone(r.Context(), id); err != nil {
		return err
	}
	s.lan.mu.Lock()
	for cancel := range s.lan.open[id] {
		(*cancel)()
	}
	delete(s.lan.seen, id)
	s.lan.mu.Unlock()
	s.publishLAN(r.Context())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// lanHostReport is the VM's supervisor saying how the port on the host is.
func (s *Server) lanHostReport(w http.ResponseWriter, r *http.Request) error {
	var req api.LANHostReport
	if err := readJSON(r, &req); err != nil {
		return err
	}
	s.lan.mu.Lock()
	changed := s.lan.host == nil || time.Since(s.lan.hostAt) >= hostReportTTL || s.lan.host.Listening != req.Listening ||
		s.lan.host.Port != req.Port || s.lan.host.Error != req.Error || strings.Join(s.lan.host.Addresses, ",") != strings.Join(req.Addresses, ",")
	s.lan.host, s.lan.hostAt = &req, time.Now()
	s.lan.mu.Unlock()
	if changed {
		s.publishLAN(r.Context())
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// The app's web version, which the desktop app installs in the daemon, since
// it's the one that has it: it is a build of the app's renderer, and the
// daemon's binary is built without it. Each version goes in a directory of
// its own; "current" names the one served.

var lanWebVersionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

func (s *Server) lanWebDir() string { return filepath.Join(s.cfg.Paths.Data, "lan-web") }

func (s *Server) lanWebVersion() string {
	b, err := os.ReadFile(filepath.Join(s.lanWebDir(), "current"))
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(b))
	if !lanWebVersionRe.MatchString(v) {
		return ""
	}
	return v
}

// maxLANWebFile bounds one file of the web app: the largest, the app's main
// script, is a little over a megabyte.
const maxLANWebFile = 16 << 20

func (s *Server) putLANWebFile(w http.ResponseWriter, r *http.Request) error {
	version, name := r.PathValue("version"), r.PathValue("path")
	if !lanWebVersionRe.MatchString(version) {
		return fmt.Errorf("%q isn't a version", version)
	}
	clean := filepath.Clean("/" + name)
	if name == "" || clean != "/"+name || strings.Contains(name, "\\") {
		return fmt.Errorf("%q isn't a file of the web app", name)
	}
	path := filepath.Join(s.lanWebDir(), ".staging-"+version, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, maxLANWebFile))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// installLANWeb makes the files put for a version the ones served, and
// removes every other version.
func (s *Server) installLANWeb(w http.ResponseWriter, r *http.Request) error {
	version := r.PathValue("version")
	if !lanWebVersionRe.MatchString(version) {
		return fmt.Errorf("%q isn't a version", version)
	}
	dir := s.lanWebDir()
	staging := filepath.Join(dir, ".staging-"+version)
	if _, err := os.Stat(filepath.Join(staging, "web.html")); err != nil {
		return fmt.Errorf("version %s has no web.html: put its files first", version)
	}
	final := filepath.Join(dir, version)
	_ = os.RemoveAll(final)
	if err := os.Rename(staging, final); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "current.tmp")
	if err := os.WriteFile(tmp, []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "current")); err != nil {
		return err
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() && e.Name() != version {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	s.publishLAN(r.Context())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// lanHandler is what a phone reaches: the web app, pairing, and the chat's
// part of the API. viaSocket says the request came from the VM's supervisor
// over the daemon's socket, which says in X-Forwarded-For who it came from.
func (s *Server) lanHandler(viaSocket bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		on, err := s.store.Flag(r.Context(), state.SettingLAN)
		if err != nil || !on {
			http.Error(w, "Chatting from a phone is off in AgentBox.", http.StatusServiceUnavailable)
			return
		}
		addr := r.RemoteAddr
		if viaSocket {
			if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
				parts := strings.Split(fwd, ",")
				addr = strings.TrimSpace(parts[len(parts)-1])
			}
		} else if host, _, err := net.SplitHostPort(addr); err == nil {
			addr = host
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeLANError(w, http.StatusForbidden, "this request didn't come from AgentBox's own page")
			return
		}
		switch {
		case r.URL.Path == "/lan/pair" && r.Method == http.MethodPost:
			s.lanPair(w, r, addr)
		case r.URL.Path == "/lan/session" && r.Method == http.MethodGet:
			p, ok := s.lanPhoneOf(w, r)
			if ok {
				_ = writeJSON(w, http.StatusOK, api.LANSession{Phone: lanPhone(p)})
			}
		case r.URL.Path == "/lan/unpair" && r.Method == http.MethodPost:
			p, ok := s.lanPhoneOf(w, r)
			if !ok {
				return
			}
			_ = s.store.RemovePhone(r.Context(), p.ID)
			http.SetCookie(w, &http.Cookie{Name: lanCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			s.publishLAN(r.Context())
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/api/"):
			s.lanAPI(w, r, addr)
		default:
			s.serveLANWeb(w, r)
		}
	})
}

// sameOrigin says a request that changes something came from a page of this
// same origin, as far as the browser says: a page elsewhere can't send the
// phone's cookie anyway (SameSite=Strict), and this also stops one that
// rebinds its own name to this address.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		host := r.Header.Get("X-Forwarded-Host")
		if host == "" {
			host = r.Host
		}
		return origin == "http://"+host || origin == "https://"+host
	}
	return true
}

func writeLANError(w http.ResponseWriter, status int, msg string) {
	_ = writeJSON(w, status, api.Error{Error: msg})
}

func (s *Server) lanPair(w http.ResponseWriter, r *http.Request, addr string) {
	var req api.LANPairRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := readJSON(r, &req); err != nil {
		writeLANError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.lan.mu.Lock()
	expires, ok := s.lan.pairings[req.Secret]
	delete(s.lan.pairings, req.Secret)
	s.lan.mu.Unlock()
	if req.Secret == "" || !ok || time.Now().After(expires) {
		writeLANError(w, http.StatusForbidden, "this QR code has expired or was used already: show a new one in AgentBox on your computer")
		return
	}
	p, token, err := s.store.AddPhone(r.Context(), req.Name)
	if err != nil {
		writeLANError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	if err := s.store.SawPhone(r.Context(), p.ID, addr, now); err == nil {
		p.LastSeen, p.LastAddr = now, addr
	}
	s.lan.mu.Lock()
	s.lan.seen[p.ID] = now
	s.lan.mu.Unlock()
	// 400 days is the longest a browser keeps a cookie.
	http.SetCookie(w, &http.Cookie{Name: lanCookie, Value: token, Path: "/", MaxAge: 400 * 24 * 60 * 60, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	s.logf("phones: paired %s (%s) from %s", p.Name, p.ID, addr)
	s.publishLAN(r.Context())
	_ = writeJSON(w, http.StatusOK, api.LANSession{Phone: lanPhone(p)})
}

func (s *Server) lanPhoneOf(w http.ResponseWriter, r *http.Request) (state.Phone, bool) {
	c, err := r.Cookie(lanCookie)
	if err == nil {
		p, err := s.store.PhoneByToken(r.Context(), c.Value)
		if err == nil {
			return p, true
		}
		if !errors.Is(err, state.ErrNotFound) {
			writeLANError(w, http.StatusInternalServerError, err.Error())
			return state.Phone{}, false
		}
	}
	writeLANError(w, http.StatusUnauthorized, "this phone isn't paired with AgentBox: scan the QR code in AgentBox's settings on your computer")
	return state.Phone{}, false
}

// lanAPI passes a paired phone's request on to the API, if a phone may make
// it, and ends it when the phone is revoked or the whole thing turned off.
func (s *Server) lanAPI(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := s.lanPhoneOf(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api")
	if !lanAllowed(r.Method, path) {
		writeLANError(w, http.StatusForbidden, fmt.Sprintf("%s %s works in the app on your computer, not from a phone", r.Method, path))
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	l := s.lan
	l.mu.Lock()
	if l.open[p.ID] == nil {
		l.open[p.ID] = map[*context.CancelFunc]struct{}{}
	}
	l.open[p.ID][&cancel] = struct{}{}
	record := time.Since(l.seen[p.ID]) > time.Minute
	if record {
		l.seen[p.ID] = time.Now()
	}
	if l.api == nil {
		l.api = s.routes()
	}
	routes := l.api
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.open[p.ID], &cancel)
		if len(l.open[p.ID]) == 0 {
			delete(l.open, p.ID)
		}
		l.mu.Unlock()
	}()
	if record {
		if err := s.store.SawPhone(ctx, p.ID, addr, time.Now()); err == nil {
			s.publishLAN(ctx)
		}
	}
	r2 := r.Clone(ctx)
	r2.URL.Path = path
	r2.URL.RawPath = ""
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// Images attached to a message are the largest thing a phone sends.
		r2.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	}
	routes.ServeHTTP(w, r2)
}

// lanRoutes are what a phone may do beyond reading: the chat's own actions,
// on the project's chat and on each agent's, and answering an agent's
// question. Reading is anything but what hands over the machine or its
// secrets.
var (
	lanWrites = []*regexp.Regexp{
		regexp.MustCompile(`^POST /v1/(projects/[^/]+|agents/[^/]+/[^/]+)/chat/(start|messages|cancel|permissions/[^/]+)$`),
		regexp.MustCompile(`^PUT /v1/(projects/[^/]+|agents/[^/]+/[^/]+)/chat/options/[^/]+$`),
		regexp.MustCompile(`^POST /v1/projects/[^/]+/chat/cache$`),
		regexp.MustCompile(`^POST /v1/projects/[^/]+/questions/[^/]+/answer$`),
		regexp.MustCompile(`^POST /v1/agents/[^/]+/[^/]+/(start|resume)$`),
		regexp.MustCompile(`^POST /v1/usage-stats/[^/]+$`),
	}
	lanUnreadable = regexp.MustCompile(`^/v1/(lan|remote|auth/claude/login|jobs/[^/]+/log)(/|$)|/(terminal|secrets|browser/view|android/view)(/|$)`)
)

func lanAllowed(method, path string) bool {
	if strings.Contains(path, "/../") || strings.HasSuffix(path, "/..") {
		return false
	}
	if method == http.MethodGet || method == http.MethodHead {
		return !lanUnreadable.MatchString(path)
	}
	line := method + " " + path
	for _, re := range lanWrites {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// lanWebAsset is a file of the web app, served as it is: its name has its
// content's hash in it, so it can be kept for good.
var lanWebAsset = regexp.MustCompile(`^/assets/[A-Za-z0-9._-]+$`)

func (s *Server) serveLANWeb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	version := s.lanWebVersion()
	if version == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, lanNoWebPage)
		return
	}
	root := filepath.Join(s.lanWebDir(), version)
	if lanWebAsset.MatchString(r.URL.Path) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeFile(w, r, filepath.Join(root, filepath.FromSlash(r.URL.Path)))
		return
	}
	// Everything else is the page: the app has one, and finds its way from
	// there.
	page, err := os.ReadFile(filepath.Join(root, "web.html"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	page = bytes.Replace(page, []byte("<head>"), []byte(`<head><meta name="agentbox-lan" content="1">`), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page)
}

const lanNoWebPage = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>AgentBox</title></head>
<body style="font-family:system-ui,sans-serif;background:#07070b;color:#e8e8ee;padding:2rem;line-height:1.5">
<h1 style="font-size:1.3rem">AgentBox isn't ready for phones yet</h1>
<p>Open the AgentBox app on your computer once: it installs what this page shows.</p>
</body></html>`
