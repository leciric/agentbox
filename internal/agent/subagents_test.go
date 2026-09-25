package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestDesktopAgentDefinition checks the frontmatter Claude Code reads: a name,
// a description, a model, and the desktop server declared inline as a list of
// one-key maps — the shape its parser insists on ("expected exactly one key").
func TestDesktopAgentDefinition(t *testing.T) {
	def, err := desktopAgent(mcpServer{"desktop", AgentBinaryPath, []string{"desktop", "mcp"}, false})
	if err != nil {
		t.Fatal(err)
	}
	front, body, ok := strings.Cut(strings.TrimPrefix(def, "---\n"), "\n---\n")
	if !strings.HasPrefix(def, "---\n") || !ok {
		t.Fatalf("no frontmatter:\n%s", def)
	}
	fields := map[string]string{}
	var serverLine string
	for _, line := range strings.Split(front, "\n") {
		if strings.HasPrefix(line, "  - ") {
			serverLine = strings.TrimPrefix(line, "  - ")
			continue
		}
		key, value, _ := strings.Cut(line, ": ")
		fields[strings.TrimSuffix(key, ":")] = value
	}
	if fields["name"] != "desktop" || fields["model"] != "haiku" {
		t.Errorf("frontmatter = %v", fields)
	}
	var description string
	if err := json.Unmarshal([]byte(fields["description"]), &description); err != nil || !strings.Contains(description, "screenshots") {
		t.Errorf("description = %q (%v), want one quoted value that says what it's for", fields["description"], err)
	}
	if _, ok := fields["mcpServers"]; !ok {
		t.Error("no mcpServers key")
	}
	name, spec, _ := strings.Cut(serverLine, ": ")
	var server struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(spec), &server); err != nil {
		t.Fatalf("the server %q isn't one flow map: %v", serverLine, err)
	}
	if name != "desktop" || server.Type != "stdio" || server.Command != AgentBinaryPath || !slices.Equal(server.Args, []string{"desktop", "mcp"}) {
		t.Errorf("server %s = %+v", name, server)
	}
	if !strings.Contains(body, "Never paste an image") {
		t.Error("the subagent isn't told to answer in words")
	}
}

// TestExploreAgentDefinition: Explore keeps the name Claude Code's own search
// subagent has, so it takes that one's place, and runs on Haiku, read-only and
// bounded.
func TestExploreAgentDefinition(t *testing.T) {
	def, err := exploreAgent()
	if err != nil {
		t.Fatal(err)
	}
	front, body, ok := strings.Cut(strings.TrimPrefix(def, "---\n"), "\n---\n")
	if !strings.HasPrefix(def, "---\n") || !ok {
		t.Fatalf("no frontmatter:\n%s", def)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(front, "\n") {
		key, value, _ := strings.Cut(line, ": ")
		fields[key] = value
	}
	if fields["name"] != "Explore" || fields["model"] != "haiku" || fields["maxTurns"] != "40" {
		t.Errorf("frontmatter = %v", fields)
	}
	for _, tool := range []string{"Agent", "Edit", "Write", "NotebookEdit"} {
		if !strings.Contains(fields["disallowedTools"], tool) {
			t.Errorf("Explore may still use %s: %q", tool, fields["disallowedTools"])
		}
	}
	var description string
	if err := json.Unmarshal([]byte(fields["description"]), &description); err != nil || !strings.Contains(description, "Haiku") {
		t.Errorf("description = %q (%v)", fields["description"], err)
	}
	if !strings.Contains(body, "Never paste whole files") {
		t.Error("Explore isn't told to answer briefly")
	}
}
