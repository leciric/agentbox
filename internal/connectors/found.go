package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"agentbox/internal/api"
	"agentbox/internal/secrets"
)

// Importing the MCP servers the user's own AI tools already have: Claude Code,
// Claude Desktop, Codex, OpenCode, Cursor and Gemini CLI keep them in files in
// the user's home, which the daemon reads where the VM shares it. Only what a
// connector can be is imported — a remote server over streamable HTTP — and
// nothing of the other tools' sign-ins: the user signs in again through
// AgentBox. A header the config sends (an API key) is kept as a secret, and
// never leaves the daemon: a scan says which header, not what is in it.

// The AI tools whose MCP servers are looked for, as api.ConnectorSource's
// Tool has them.
const (
	ToolClaudeCode    = "claude"
	ToolClaudeDesktop = "claude-desktop"
	ToolCodex         = "codex"
	ToolOpenCode      = "opencode"
	ToolCursor        = "cursor"
	ToolGemini        = "gemini"
)

// Found is one MCP server in one AI tool's configuration.
type Found struct {
	Tool string
	// Path is the configuration file; Project, for Claude Code's servers of
	// one project, that project's folder.
	Path    string
	Project string
	// Name is what the tool calls it.
	Name string
	// Transport is api.TransportHTTP, TransportSSE or TransportStdio.
	Transport string
	URL       string
	// Command is a stdio server's program, without its arguments, which can
	// hold a token.
	Command string
	// Header is the one header the configuration sends, Scheme what comes
	// before the value in it (Bearer), Value the rest: never shown or logged.
	// Variable is an environment variable Value refers to, which the tool
	// fills in from the user's shell and AgentBox can't: Unresolved, the user
	// enters it.
	Header, Scheme, Value, Variable string
	Unresolved                      bool
}

// Unreadable is a tool's configuration that is there but couldn't be read.
type Unreadable struct {
	Tool, Path string
	Err        error
}

// configFile is where a tool keeps its MCP servers, under a home, and how it
// is read.
type configFile struct {
	tool  string
	path  []string
	parse func(data []byte) ([]Found, error)
}

var configFiles = []configFile{
	{ToolClaudeCode, []string{".claude.json"}, parseClaudeCode},
	{ToolClaudeDesktop, []string{"Library", "Application Support", "Claude", "claude_desktop_config.json"}, parseMCPServers},
	{ToolClaudeDesktop, []string{".config", "Claude", "claude_desktop_config.json"}, parseMCPServers},
	{ToolClaudeDesktop, []string{"AppData", "Roaming", "Claude", "claude_desktop_config.json"}, parseMCPServers},
	{ToolCodex, []string{".codex", "config.toml"}, parseCodex},
	{ToolOpenCode, []string{".config", "opencode", "opencode.json"}, parseOpenCode},
	{ToolOpenCode, []string{".config", "opencode", "opencode.jsonc"}, parseOpenCode},
	{ToolCursor, []string{".cursor", "mcp.json"}, parseMCPServers},
	{ToolGemini, []string{".gemini", "settings.json"}, parseGemini},
}

// maxConfig bounds a file read: Claude Code's ~/.claude.json keeps history
// too, and grows.
const maxConfig = 64 << 20

// FindServers reads every AI tool's MCP servers under homes: the host's home
// where the VM shares it, the Windows user's, this machine's. A file that
// isn't there is skipped; one that is and can't be read is in unreadable.
func FindServers(homes []string) (found []Found, unreadable []Unreadable) {
	seen := map[string]bool{}
	for _, home := range homes {
		for _, f := range configFiles {
			p := filepath.Join(append([]string{home}, f.path...)...)
			if seen[p] {
				continue
			}
			seen[p] = true
			servers, err := readConfig(p, f.parse)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				unreadable = append(unreadable, Unreadable{Tool: f.tool, Path: p, Err: err})
				continue
			}
			for _, s := range servers {
				s.Tool, s.Path = f.tool, p
				found = append(found, s)
			}
		}
	}
	return found, unreadable
}

func readConfig(p string, parse func([]byte) ([]Found, error)) ([]Found, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a folder", p)
	}
	if info.Size() > maxConfig {
		return nil, fmt.Errorf("%s is larger than %d MB", p, maxConfig>>20)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return parse(data)
}

// jsonServer is one server as the JSON configurations have it, all of them
// together: Claude Code's and Claude Desktop's, Cursor's and Gemini CLI's
// (mcpServers), and OpenCode's (mcp).
type jsonServer struct {
	// Type is "http", "streamable-http", "sse" or "stdio" (Claude Code,
	// Cursor), or "remote" and "local" (OpenCode).
	Type string `json:"type"`
	URL  string `json:"url"`
	// HTTPURL is Gemini CLI's streamable HTTP address; its URL is SSE.
	HTTPURL string `json:"httpUrl"`
	// Command is a string, or OpenCode's argv.
	Command json.RawMessage `json:"command"`
	Headers map[string]any  `json:"headers"`
}

// parseClaudeCode reads ~/.claude.json: the servers of every project, under
// "user" scope at the top and "local" scope under each project's folder.
func parseClaudeCode(data []byte) ([]Found, error) {
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
		Projects   map[string]struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(stripJSONC(data), &cfg); err != nil {
		return nil, err
	}
	out := jsonServers(cfg.MCPServers, "")
	dirs := make([]string, 0, len(cfg.Projects))
	for dir := range cfg.Projects {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		for _, f := range jsonServers(cfg.Projects[dir].MCPServers, "") {
			f.Project = dir
			out = append(out, f)
		}
	}
	return out, nil
}

// parseMCPServers reads the "mcpServers" of Claude Desktop, Cursor and
// Gemini CLI.
func parseMCPServers(data []byte) ([]Found, error) {
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(stripJSONC(data), &cfg); err != nil {
		return nil, err
	}
	return jsonServers(cfg.MCPServers, ""), nil
}

// parseGemini reads Gemini CLI's settings.json, whose "url" is an SSE server
// unless its type says http, and "httpUrl" a streamable HTTP one.
func parseGemini(data []byte) ([]Found, error) {
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(stripJSONC(data), &cfg); err != nil {
		return nil, err
	}
	return jsonServers(cfg.MCPServers, ToolGemini), nil
}

// parseOpenCode reads opencode.json(c)'s "mcp".
func parseOpenCode(data []byte) ([]Found, error) {
	var cfg struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	}
	if err := json.Unmarshal(stripJSONC(data), &cfg); err != nil {
		return nil, err
	}
	return jsonServers(cfg.MCP, ToolOpenCode), nil
}

// jsonServers reads each server of a JSON configuration, in name order. One
// that isn't an object is left out rather than failing the file. tool is the
// one whose reading differs: Gemini CLI's, or OpenCode's.
func jsonServers(servers map[string]json.RawMessage, tool string) []Found {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []Found
	for _, name := range names {
		var s jsonServer
		if err := json.Unmarshal(servers[name], &s); err != nil {
			continue
		}
		f := Found{Name: name}
		headers := map[string]string{}
		for k, v := range s.Headers {
			if v, ok := v.(string); ok {
				headers[k] = v
			}
		}
		switch t := strings.ToLower(s.Type); {
		case s.HTTPURL != "":
			f.Transport, f.URL = api.TransportHTTP, s.HTTPURL
		case t == "sse":
			f.Transport, f.URL = api.TransportSSE, s.URL
		case s.URL != "" && tool == ToolGemini && t != "http":
			f.Transport, f.URL = api.TransportSSE, s.URL
		case s.URL != "" && t != "stdio" && t != "local":
			// http, streamable-http, streamableHttp, OpenCode's remote (which
			// tries streamable HTTP first), or no type with a URL.
			f.Transport, f.URL = api.TransportHTTP, s.URL
		case len(s.Command) > 0:
			f.Transport, f.Command = api.TransportStdio, program(s.Command)
		default:
			continue
		}
		f.URL = strings.TrimSpace(f.URL)
		if f.Transport != api.TransportStdio {
			f.Header, f.Scheme, f.Value, f.Variable, f.Unresolved = pickHeader(headers)
		}
		out = append(out, f)
	}
	return out
}

// program is a command's program, without its arguments: a string's first
// word, or an argv's first element.
func program(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		fields := strings.Fields(s)
		if len(fields) == 0 {
			return ""
		}
		return filepath.Base(fields[0])
	}
	var argv []string
	if json.Unmarshal(raw, &argv) == nil && len(argv) > 0 {
		return filepath.Base(argv[0])
	}
	return ""
}

// parseCodex reads ~/.codex/config.toml's [mcp_servers.<name>] tables.
// Codex speaks streamable HTTP to a server with a url; its bearer_token_env_var
// and env_http_headers name variables of the user's shell.
func parseCodex(data []byte) ([]Found, error) {
	var cfg struct {
		MCPServers map[string]struct {
			Command           string            `toml:"command"`
			URL               string            `toml:"url"`
			BearerTokenEnvVar string            `toml:"bearer_token_env_var"`
			HTTPHeaders       map[string]string `toml:"http_headers"`
			EnvHTTPHeaders    map[string]string `toml:"env_http_headers"`
		} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []Found
	for _, name := range names {
		s := cfg.MCPServers[name]
		f := Found{Name: name}
		switch {
		case s.URL != "":
			f.Transport, f.URL = api.TransportHTTP, strings.TrimSpace(s.URL)
			headers := map[string]string{}
			maps.Copy(headers, s.HTTPHeaders)
			for h, v := range s.EnvHTTPHeaders {
				headers[h] = "${" + v + "}"
			}
			if s.BearerTokenEnvVar != "" {
				headers["Authorization"] = "Bearer ${" + s.BearerTokenEnvVar + "}"
			}
			f.Header, f.Scheme, f.Value, f.Variable, f.Unresolved = pickHeader(headers)
		case strings.TrimSpace(s.Command) != "":
			f.Transport, f.Command = api.TransportStdio, filepath.Base(strings.Fields(s.Command)[0])
		default:
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// AuthOf is how a connector of a server found elsewhere, with no header to
// send, signs in: ConnectorNone when the server answers an unauthenticated
// initialize, ConnectorOAuth when it asks for a sign-in or says nothing either
// way, as a connector added by URL does.
func (o OAuth) AuthOf(ctx context.Context, server string) string {
	if _, _, err := o.challenge(ctx, server); errors.Is(err, ErrNoAuth) {
		return api.ConnectorNone
	}
	return api.ConnectorOAuth
}

// variable is an environment variable in a value, the ways the tools write
// one: ${VAR} and ${VAR:-default} (Claude Code), ${env:VAR} (Cursor), and
// {env:VAR} (OpenCode).
var variable = regexp.MustCompile(`\$\{(?:env:)?([A-Za-z_][A-Za-z0-9_]*)(?::-[^}]*)?\}|\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

// variableIn is the first variable in s, and whether there is one.
func variableIn(s string) (string, bool) {
	m := variable.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1] + m[2], true
}

// pickHeader is the one header a connector sends, of those a configuration
// has: Authorization, else the only one, else the one that looks like a
// credential. A connector sends one header; the rest are left behind.
func pickHeader(headers map[string]string) (header, scheme, value, variable string, unresolved bool) {
	if len(headers) == 0 {
		return
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	header = keys[0]
	for _, k := range keys {
		l := strings.ToLower(k)
		if l == "authorization" {
			header = k
			break
		}
		if strings.Contains(l, "key") || strings.Contains(l, "token") || strings.Contains(l, "auth") || strings.Contains(l, "secret") {
			header = k
		}
	}
	value = strings.TrimSpace(headers[header])
	if strings.EqualFold(header, "Authorization") {
		if s, rest, ok := strings.Cut(value, " "); ok {
			scheme, value = s, strings.TrimSpace(rest)
		}
	}
	variable, unresolved = variableIn(value)
	return
}

// stripJSONC makes JSON with comments and trailing commas, as OpenCode and
// some hand-edited files have it, plain JSON. Strings are left as they are.
func stripJSONC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case c == '"':
			j := i + 1
			for ; j < len(data) && data[j] != '"'; j++ {
				if data[j] == '\\' {
					j++
				}
			}
			end := min(j+1, len(data))
			out = append(out, data[i:end]...)
			i = end - 1
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && (data[i] != '*' || data[i+1] != '/') {
				i++
			}
			i++
		case c == ',':
			j := i + 1
			for j < len(data) && strings.ContainsRune(" \t\r\n", rune(data[j])) {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// Candidate is a server found, as it would be imported: an api.ConnectorCandidate,
// and the value of the header it sends, which never leaves the daemon.
type Candidate struct {
	api.ConnectorCandidate
	Value string
}

// Candidates turns what the tools have into what can be imported into a
// scope: one per server, the same URL in several tools being one, each given
// a name no connector there has. have are the connectors the scope already
// sees (a project's own and the AgentBox-wide ones), whose servers are
// marked Existing; check refuses a URL a connector can't have.
func Candidates(found []Found, have []api.Connector, check func(string) error) []Candidate {
	existing := map[string]string{}
	taken := map[string]bool{}
	for _, c := range have {
		existing[urlKey(c.URL)] = c.Name
		taken[c.Name] = true
	}
	var out []Candidate
	index := map[string]int{}
	for _, f := range found {
		src := api.ConnectorSource{Tool: f.Tool, Name: f.Name, Path: f.Path, Project: f.Project}
		key := urlKey(f.URL)
		if f.Transport == api.TransportStdio {
			key = "stdio:" + f.Name + ":" + f.Command
		}
		if i, ok := index[key]; ok {
			c := &out[i]
			c.Sources = append(c.Sources, src)
			if c.Header == "" && f.Header != "" {
				setHeader(c, f)
			}
			continue
		}
		c := Candidate{ConnectorCandidate: api.ConnectorCandidate{
			URL: f.URL, Transport: f.Transport, Command: f.Command, Sources: []api.ConnectorSource{src},
		}}
		setHeader(&c, f)
		_, inURL := variableIn(f.URL)
		switch {
		case f.Transport == api.TransportStdio:
			c.Problem = api.ConnectorProblemStdio
		case f.Transport == api.TransportSSE:
			c.Problem = api.ConnectorProblemSSE
		case inURL:
			c.Problem = api.ConnectorProblemVariable
		case check(f.URL) != nil:
			c.Problem = api.ConnectorProblemURL
		}
		if name, ok := existing[key]; ok && f.Transport != api.TransportStdio {
			c.Existing = name
		}
		c.Name = f.Name
		index[key] = len(out)
		out = append(out, c)
	}
	// Names come last, to the importable first, so one that can be imported
	// keeps its own name over one that can't.
	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	rank := func(c Candidate) int {
		switch {
		case c.Problem == "" && c.Existing == "":
			return 0
		case c.Problem == "":
			return 1
		}
		return 2
	}
	sort.SliceStable(order, func(a, b int) bool { return rank(out[order[a]]) < rank(out[order[b]]) })
	for _, i := range order {
		c := &out[i]
		if c.Existing != "" {
			c.Name = c.Existing
			continue
		}
		c.Name = uniqueName(slugName(c.Name, c.URL), taken)
		taken[c.Name] = true
		if c.Header != "" {
			c.Secret = secretName(c.Variable, c.Name)
		}
	}
	sorted := make([]Candidate, len(out))
	for i, j := range order {
		sorted[i] = out[j]
	}
	sort.SliceStable(sorted, func(a, b int) bool {
		if ra, rb := rank(sorted[a]), rank(sorted[b]); ra != rb {
			return ra < rb
		}
		return sorted[a].Name < sorted[b].Name
	})
	return sorted
}

func setHeader(c *Candidate, f Found) {
	c.Header, c.Scheme, c.Variable, c.NeedsValue, c.Value = f.Header, f.Scheme, f.Variable, f.Unresolved, ""
	if !f.Unresolved {
		c.Value = f.Value
	}
}

// urlKey is what two addresses of the same server have in common: the
// scheme and host in lower case, and no trailing slash.
func urlKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	u.Fragment, u.RawFragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String()
}

var notName = regexp.MustCompile(`[^a-z0-9_-]+`)

// slugName is a tool's name for a server made a valid connector name, or one
// made from its address when nothing of it is left.
func slugName(name, address string) string {
	n := strings.Trim(notName.ReplaceAllString(strings.ToLower(name), "-"), "-_")
	if n == "" {
		if u, err := url.Parse(address); err == nil {
			parts := slices.DeleteFunc(strings.Split(u.Hostname(), "."), func(p string) bool { return p == "www" || p == "mcp" || p == "api" })
			if len(parts) > 1 {
				n = parts[len(parts)-2]
			} else if len(parts) == 1 {
				n = parts[0]
			}
			n = strings.Trim(notName.ReplaceAllString(strings.ToLower(n), "-"), "-_")
		}
	}
	if n == "" {
		n = "server"
	}
	if len(n) > 40 {
		n = strings.TrimRight(n[:40], "-_")
	}
	if slices.Contains(Reserved, n) {
		n += "-mcp"
	}
	return n
}

// uniqueName is name, or name-2, -3… when a connector has it.
func uniqueName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf("-%d", i)
		base := name
		if len(base)+len(suffix) > 40 {
			base = base[:40-len(suffix)]
		}
		if n := base + suffix; !taken[n] {
			return n
		}
	}
}

// secretName is what the header's value is stored as: the variable the tool
// read it from, when that is a name a secret can have, else NAME_TOKEN.
func secretName(variable, connector string) string {
	if variable != "" && secrets.ValidateName(variable) == nil {
		return variable
	}
	n := strings.ToUpper(strings.ReplaceAll(connector, "-", "_")) + "_TOKEN"
	if n[0] >= '0' && n[0] <= '9' {
		n = "MCP_" + n
	}
	return n
}
