package hatch

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"agentbox/internal/api"
)

func TestOp(t *testing.T) {
	for _, c := range []struct{ name, title, want string }{
		{"mcp__hatch__publish_page", "", Publish},
		{"", "mcp__hatch__update_page", Update},
		{"", "hatch.publish_page", Publish},
		{"hatch_update_page", "Update page", Update},
		{"", "Publish_Page", Publish},
		{"mcp__hatch__get_page", "", ""},
		{"mcp__hatch__republish_page", "", ""},
		{"mcp__hatch__publish_pages", "", ""},
		{"Bash", "publish_page.sh", ""},
	} {
		if got := Op(c.name, c.title); got != c.want {
			t.Errorf("Op(%q, %q) = %q, want %q", c.name, c.title, got, c.want)
		}
	}
}

func TestArgs(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want *api.ChatPage
	}{
		{`{"title":"Report","html":"<p>x</p>"}`, &api.ChatPage{Title: "Report"}},
		{`{"title":"Report","html":"…","public":true,"expires_in_hours":72}`, &api.ChatPage{Title: "Report", Public: true, ExpiresInHours: 72}},
		{`{"title":"Report","html":"…","expires_in_hours":null}`, &api.ChatPage{Title: "Report", ExpiresInHours: -1}},
		{`{"id":"EUvS","html":"…"}`, &api.ChatPage{ID: "EUvS"}},
		{`{"title":"Rep`, nil}, // still streaming
		{``, nil},
	} {
		if got := Args(json.RawMessage(c.raw)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Args(%s) = %+v, want %+v", c.raw, got, c.want)
		}
	}
}

// What Hatch answers, as the chat keeps it.
const (
	publishedOut = "Published \"Q3 report\" (id EUvSI7W1CLemtkP0, version 1): https://hatch.linting.dev/p/EUvSI7W1CLemtkP0\n" +
		"{\n  \"id\": \"EUvSI7W1CLemtkP0\",\n  \"version\": 1,\n  \"url\": \"https://hatch.linting.dev/p/EUvSI7W1CLemtkP0\"\n}"
	updatedOut = "Published version 3 of EUvSI7W1CLemtkP0. Share link: https://hatch.linting.dev/p/EUvSI7W1CLemtkP0 " +
		"(this version: https://hatch.linting.dev/p/EUvSI7W1CLemtkP0?v=3)\n{\n  \"version\": 3,\n  \"url\": \"https://hatch.linting.dev/p/EUvSI7W1CLemtkP0?v=3\"\n}"
)

func TestCollect(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	tool := func(name, status, output string, page *api.ChatPage) api.ChatTool {
		return api.ChatTool{Name: name, Title: name, Status: status, Output: output, Page: page}
	}
	calls := []Call{
		{Agent: "pawly/lead", Item: "i1", At: t0, Tool: tool("mcp__hatch__publish_page", "completed", publishedOut, &api.ChatPage{Title: "Q3 report"})},
		// Another tool, a failed publish, and a page on somebody else's site.
		{Agent: "pawly/lead", Item: "i2", At: t0.Add(time.Minute), Tool: tool("Bash", "completed", "https://hatch.linting.dev/p/zzz", nil)},
		{Agent: "pawly/agent-02", Item: "i3", At: t0.Add(2 * time.Minute), Tool: tool("mcp__hatch__publish_page", "failed", "Error (rate_limited): slow down", nil)},
		{Agent: "pawly/agent-02", Item: "i4", At: t0.Add(3 * time.Minute), Tool: tool("mcp__hatch__publish_page", "completed", "Published \"x\" (id abc, version 1): https://evil.example/p/abc", nil)},
		// An agent updates the lead's page: the same artifact, newer.
		{Agent: "pawly/agent-02", Item: "i5", At: t0.Add(4 * time.Hour), Tool: tool("mcp__hatch__update_page", "completed", updatedOut, &api.ChatPage{ID: "EUvSI7W1CLemtkP0"})},
		// A permanent page, published with no arguments kept: the title
		// comes from Hatch's answer.
		{Agent: "pawly/agent-03", Item: "i6", At: t0.Add(time.Hour), Tool: tool("hatch.publish_page", "completed",
			"Published \"Design: onboarding\" (id P3rm, version 1): https://hatch.linting.dev/p/P3rm", &api.ChatPage{ExpiresInHours: -1})},
		// An update of a page none of the chats published, with a wrong id.
		{Agent: "pawly/agent-03", Item: "i7", At: t0.Add(5 * time.Hour), Tool: tool("hatch.update_page", "completed",
			"Published version 2 of Orph. Share link: https://hatch.linting.dev/p/Orph", nil)},
		{Agent: "pawly/agent-03", Item: "i8", At: t0.Add(5 * time.Hour), Tool: tool("hatch.update_page", "completed", updatedOut, &api.ChatPage{ID: "other"})},
	}
	now := t0.Add(25 * time.Hour)
	got := Collect(calls, Host, now)
	exp := t0.Add(24 * time.Hour)
	want := []api.Artifact{
		{ID: "Orph", Title: "Orph", URL: "https://hatch.linting.dev/p/Orph", Agent: "pawly/agent-03", Item: "i7", Agents: []string{"pawly/agent-03"},
			Version: 2, CreatedAt: t0.Add(5 * time.Hour), UpdatedAt: t0.Add(5 * time.Hour)},
		{ID: "EUvSI7W1CLemtkP0", Title: "Q3 report", URL: "https://hatch.linting.dev/p/EUvSI7W1CLemtkP0", Agent: "pawly/lead", Item: "i1",
			Agents: []string{"pawly/lead", "pawly/agent-02"}, Version: 3, CreatedAt: t0, UpdatedAt: t0.Add(4 * time.Hour), ExpiresAt: &exp, Expired: true},
		{ID: "P3rm", Title: "Design: onboarding", URL: "https://hatch.linting.dev/p/P3rm", Agent: "pawly/agent-03", Item: "i6", Agents: []string{"pawly/agent-03"},
			Version: 1, CreatedAt: t0.Add(time.Hour), UpdatedAt: t0.Add(time.Hour), Permanent: true},
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		w, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("Collect =\n%s\nwant\n%s", g, w)
	}
}

// An update read before its publish — another chat's, made earlier — still
// leaves the artifact the publisher's.
func TestCollectPublishAfterUpdate(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	got := Collect([]Call{
		{Agent: "p/agent-02", Item: "u", At: t0, Tool: api.ChatTool{Name: "mcp__hatch__update_page", Status: "completed", Output: updatedOut}},
		{Agent: "p/lead", Item: "c", At: t0, Tool: api.ChatTool{Name: "mcp__hatch__publish_page", Status: "completed", Output: publishedOut, Page: &api.ChatPage{Title: "Q3", Public: true, ExpiresInHours: 2}}},
	}, Host, t0)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	a := got[0]
	if a.Agent != "p/lead" || a.Item != "c" || !reflect.DeepEqual(a.Agents, []string{"p/lead", "p/agent-02"}) || a.Version != 3 ||
		!a.Public || a.ExpiresAt == nil || !a.ExpiresAt.Equal(t0.Add(2*time.Hour)) || a.Expired || a.Title != "Q3" {
		t.Errorf("got %+v", a)
	}
}

func TestIsHatch(t *testing.T) {
	for url, want := range map[string]bool{
		"https://hatch.linting.dev/mcp":  true,
		"https://HATCH.linting.dev/mcp/": true,
		"https://mcp.notion.com/mcp":     false,
		"https://hatch.linting.dev.evil": false,
		"not a url ::":                   false,
	} {
		if got := IsHatch(url); got != want {
			t.Errorf("IsHatch(%q) = %v", url, got)
		}
	}
}
