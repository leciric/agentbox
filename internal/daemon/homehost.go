package daemon

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/coder/websocket"

	"agentbox/internal/acp"
	"agentbox/internal/agent"
	"agentbox/internal/chat"
	"agentbox/internal/homehost"
	"agentbox/internal/state"
)

// The Home chat on the user's computer (package homehost): the desktop app
// starts `agentbox home-host` there, which connects to attachHomeHost, and the
// Home chat's adapter is then started through it rather than beside the
// daemon. Without it, or without the tool on the host, the chat runs here as
// it always did, and says why.

// attachHomeHost serves GET /v1/home-host, the helper's connection.
func (s *Server) attachHomeHost(w http.ResponseWriter, r *http.Request) error {
	// The socket is only reachable by this user, as the terminal's is.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil // Accept has already written the response
	}
	defer func() { _ = conn.CloseNow() }()
	ctx := r.Context()
	return s.homeHost.Attach(ctx, websocket.NetConn(ctx, conn, websocket.MessageBinary))
}

// homeHostMCP serves GET /v1/home-host/mcp: the stdio of the Home chat's MCP
// server, started on the host, carried to `agentbox mcp` here, which reaches
// the Home chat's own API as it does when the chat runs here.
func (s *Server) homeHostMCP(w http.ResponseWriter, r *http.Request) error {
	if err := s.serveLeadAPI(state.HomeProject); err != nil {
		return err
	}
	bin := s.cfg.Binary
	if bin == "" {
		var err error
		if bin, err = os.Executable(); err != nil {
			return err
		}
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil
	}
	defer func() { _ = conn.CloseNow() }()
	ctx := r.Context()
	stream := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	// The session's own socket, which is how the daemon tells sessions apart.
	session := cmp.Or(r.URL.Query().Get("session"), state.MigratedSession)
	cmd := exec.CommandContext(ctx, bin, "mcp")
	cmd.Env = append(os.Environ(), "AGENTBOX_SOCKET="+s.sessionSocketPath(state.HomeProject, session),
		"AGENTBOX_NO_AUTOSTART=1", "AGENTBOX_CHAT=home")
	cmd.Stdin, cmd.Stdout = stream, stream
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		s.logf("The Home chat's MCP server on the host ended: %v", err)
	}
	return nil
}

// homeHostChanged follows the helper connecting or going: each Home chat
// session's adapter starts again before its next turn, on the host or here.
func (s *Server) homeHostChanged(connected bool) {
	if connected {
		s.logf("The desktop app's home-host connected: the Home chat runs on the user's computer")
	} else {
		s.logf("The desktop app's home-host went: the Home chat runs in this machine")
	}
	refs := []string{agent.HomeRef}
	sessions, err := s.store.LeadSessions(context.Background(), state.HomeProject)
	if err != nil {
		s.logf("The Home chat's sessions: %v", err)
	}
	for _, ls := range sessions {
		refs = append(refs, agent.SessionRef(state.HomeProject, ls.ID))
	}
	for _, ref := range refs {
		s.chat.Relaunch(ref)
	}
}

// launchHome starts the Home chat's adapter on the user's computer when it
// can. When it can't, it returns nil and what to tell the chat, and
// launchChat starts it here.
func (s *Server) launchHome(ctx context.Context, m *agent.Manager, a state.Agent, status func(string)) (*chat.Process, placed) {
	r := s.homeHost.Runner()
	if r == nil {
		return nil, s.homePlaced(a.Ref(), "vm:app",
			"The AgentBox app isn't open on your computer, so the Home chat runs in AgentBox's VM until it is, without your computer's programs and files.")
	}
	tool := chat.ToolNames[a.AI]
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	info, err := r.Probe(pctx)
	cancel()
	if err != nil {
		return nil, s.homePlaced(a.Ref(), "vm:probe", fmt.Sprintf("AgentBox couldn't reach your computer (%v), so the Home chat runs in AgentBox's VM.", err))
	}
	host := info.Tools[a.AI]
	switch {
	case a.AI == "cursor":
		return nil, s.homePlaced(a.Ref(), "vm:cursor", "Cursor runs only in AgentBox's VM, so the Home chat runs there while it is on Cursor.")
	case host.Path == "":
		return nil, s.homePlaced(a.Ref(), "vm:missing:"+a.AI, fmt.Sprintf(
			"%s isn't installed on your computer, so the Home chat runs in AgentBox's VM, without your computer's programs and files. Install it there and the chat moves to your computer when it next starts.", tool))
	case a.AI == "codex" && !host.Login:
		return nil, s.homePlaced(a.Ref(), "vm:login:codex",
			"Codex isn't signed in on your computer (codex login), so the Home chat runs in AgentBox's VM.")
	case a.AI == "gemini" && !host.Login && !s.homeGeminiKey(a):
		return nil, s.homePlaced(a.Ref(), "vm:login:gemini",
			"Gemini CLI isn't signed in on your computer (run gemini once and sign in), so the Home chat runs in AgentBox's VM.")
	}
	spec, err := m.HostHomeSpec(ctx, a, info)
	if err != nil {
		return nil, s.homePlaced(a.Ref(), "vm:spec", fmt.Sprintf("The Home chat couldn't be readied for your computer (%v), so it runs in AgentBox's VM.", err))
	}
	p, err := r.Spawn(ctx, spec, status)
	if err != nil {
		s.logf("The Home chat couldn't start %s on the host: %v", tool, err)
		return nil, s.homePlaced(a.Ref(), "vm:spawn", fmt.Sprintf("The Home chat couldn't start %s on your computer (%v), so it runs in AgentBox's VM.", tool, err))
	}
	login := "its own login there"
	if !host.Login && a.AI == "claude" {
		login = "AgentBox's Claude Code account, as it has no login of its own there"
	}
	here := s.homePlaced(a.Ref(), "host", fmt.Sprintf(
		"The Home chat runs on your computer (%s), as you: %s with %s. It asks before each command and edit.",
		hostOf(info), tool, login))
	return &chat.Process{
		Stdin: p.Stdin, Stdout: p.Stdout, Stop: p.Stop, Wait: p.Wait, Stderr: p.Stderr,
		Cwd: p.Cwd, MCPServers: acpServers(p.MCP), Notice: here.notice, Moved: here.moved,
	}, placed{}
}

// placed is what launchHome has to say about where the Home chat starts.
type placed struct {
	notice string
	// moved is between the user's computer and the VM, which keep their own
	// sessions: the new one is told the conversation (chat.Process.Moved).
	moved bool
}

// homePlaced records where a Home chat session's adapter, ref, starts. It says so when
// that changed, but not for the VM when the app has never been open since the
// daemon started, which is the CLI's and the phone's usual case rather than
// news.
func (s *Server) homePlaced(ref, place, text string) placed {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.homePlace == nil {
		s.homePlace = map[string]string{}
	}
	prev := s.homePlace[ref]
	s.homePlace[ref] = place
	if prev == place || (place == "vm:app" && prev == "") {
		return placed{}
	}
	return placed{notice: text, moved: prev != "" && (prev == "host") != (place == "host")}
}

func hostOf(info homehost.Info) string {
	if info.Host != "" {
		return info.Host
	}
	return info.OS
}

func acpServers(in []homehost.MCPServer) []acp.McpServer {
	out := make([]acp.McpServer, 0, len(in))
	for _, s := range in {
		env := make([]acp.EnvVariable, 0, len(s.Env))
		for _, kv := range s.Env {
			name, value, _ := strings.Cut(kv, "=")
			env = append(env, acp.EnvVariable{Name: name, Value: value})
		}
		out = append(out, acp.McpServer{Name: s.Name, Command: s.Command, Args: s.Args, Env: env})
	}
	return out
}

// homeGeminiKey reports whether the Home chat's Gemini CLI account is an API
// key, which the host's Gemini CLI can be given in its environment when it has
// no login of its own (agent.HostHomeSpec).
func (s *Server) homeGeminiKey(a state.Agent) bool {
	login, ok := s.manager(nil).Creds.GeminiLogin(a.GeminiAccount)
	return ok && login.APIKey != ""
}
