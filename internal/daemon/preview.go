package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
)

// The preview proxy makes http://<port>.<agent>.<project>.localhost:7777 reach
// port <port> of that agent, so you can open agents' servers in your own
// browser without knowing their IP addresses. Browsers and curl resolve
// *.localhost to the loopback address, and the proxy listens only there.
// Config.PreviewAddr — AGENTBOX_PREVIEW_ADDR, for `agentbox daemon` — changes
// the address, or turns the proxy off ("off").

const defaultPreviewAddr = "127.0.0.1:7777"

type previewTarget struct {
	ip string
	at time.Time
}

func (s *Server) servePreview(ctx context.Context) {
	addr := s.cfg.PreviewAddr
	switch addr {
	case "off":
		return
	case "":
		addr = defaultPreviewAddr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.logf("preview proxy off: %v", err)
		return
	}
	s.mu.Lock()
	s.previewAddr = ln.Addr().String()
	s.mu.Unlock()
	srv := &http.Server{Handler: http.HandlerFunc(s.preview), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() { _ = srv.Serve(ln) }()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	s.logf("preview proxy on http://<port>.<agent>.<project>.localhost:%s", port)
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	name, ok := strings.CutSuffix(strings.ToLower(host), ".localhost")
	labels := strings.Split(name, ".")
	if !ok || len(labels) != 3 {
		http.Error(w, "AgentBox preview: open http://<port>.<agent>.<project>.localhost with this port", http.StatusNotFound)
		return
	}
	port, err := strconv.Atoi(labels[0])
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, fmt.Sprintf("AgentBox preview: %q isn't a port", labels[0]), http.StatusNotFound)
		return
	}
	project, agentName := labels[2], labels[1]
	ip, err := s.previewIP(r.Context(), project, agentName)
	if err != nil {
		http.Error(w, "AgentBox preview: "+err.Error(), http.StatusBadGateway)
		return
	}
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort(ip, strconv.Itoa(port))}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Dev servers often check the Host header, and usually accept *.localhost.
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, fmt.Sprintf("AgentBox preview: nothing answers on port %d of %s/%s (%v)", port, project, agentName, err), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// previewIP returns an agent's IP address, cached for a few seconds so that a
// page's many requests don't each list the instances.
func (s *Server) previewIP(ctx context.Context, project, name string) (string, error) {
	key := project + "/" + name
	s.mu.Lock()
	cached, ok := s.previewIPs[key]
	s.mu.Unlock()
	if ok && time.Since(cached.at) < 5*time.Second {
		return cached.ip, nil
	}
	a, err := s.store.Agent(ctx, project, name)
	if err != nil {
		return "", fmt.Errorf("there's no agent %s", key)
	}
	info, err := s.describe(ctx, a)
	if err != nil {
		return "", err
	}
	if info.IP == "" {
		return "", fmt.Errorf("%s is %s", key, info.State)
	}
	s.mu.Lock()
	s.previewIPs[key] = previewTarget{ip: info.IP, at: time.Now()}
	s.mu.Unlock()
	return info.IP, nil
}

func (s *Server) previewInfo(w http.ResponseWriter, _ *http.Request) error {
	s.mu.Lock()
	addr := s.previewAddr
	s.mu.Unlock()
	return writeJSON(w, http.StatusOK, api.PreviewInfo{Addr: addr})
}
