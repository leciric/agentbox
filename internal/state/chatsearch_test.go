package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func chatRows(t *testing.T, items ...map[string]any) []ChatItem {
	t.Helper()
	rows := make([]ChatItem, len(items))
	for i, it := range items {
		data, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		rows[i] = ChatItem{ID: it["id"].(string), Position: int64(i), Data: data}
	}
	return rows
}

func hitIDs(hits []ChatHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func searchIDs(t *testing.T, s *Store, q ChatSearch) []string {
	t.Helper()
	hits, _, err := s.SearchChats(context.Background(), q)
	if err != nil {
		t.Fatalf("SearchChats(%+v): %v", q, err)
	}
	return hitIDs(hits)
}

func openSearchStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// What the conversation shows you is found; tool calls, a subagent's work
// and hidden prose aren't, and the hits come in the conversation's order.
func TestSearchChatsFindsWhatTheChatShows(t *testing.T) {
	t.Parallel()
	s := openSearchStore(t)
	ctx := context.Background()
	if err := s.SaveChatItems(ctx, "pawly", "agent-01", chatRows(t,
		map[string]any{"id": "u1", "kind": "user", "text": "Fix the Login page"},
		map[string]any{"id": "t1", "kind": "tool", "text": "login", "tool": map[string]any{"title": "grep login"}},
		map[string]any{"id": "s1", "kind": "assistant", "parent": "sub", "text": "the login form"},
		map[string]any{"id": "h1", "kind": "user", "hidden": true, "text": "agent-02 finished: login"},
		map[string]any{"id": "a1", "kind": "assistant", "text": "The **login** page is fixed. Logins work."},
		map[string]any{"id": "n1", "kind": "notice", "text": "Rolled back to before the login change"},
		map[string]any{"id": "u2", "kind": "aside", "text": "and the lógin button?"},
	)); err != nil {
		t.Fatal(err)
	}
	if got, want := searchIDs(t, s, ChatSearch{Query: "LOGIN", Project: "pawly", Agent: "agent-01"}), []string{"u1", "a1", "n1", "u2"}; !slices.Equal(got, want) {
		t.Errorf("hits = %v, want %v", got, want)
	}
	// A phrase is found as one piece, punctuation and all.
	if got := searchIDs(t, s, ChatSearch{Query: "page is fixed.", Project: "pawly", Agent: "agent-01"}); !slices.Equal(got, []string{"a1"}) {
		t.Errorf("phrase hits = %v, want [a1]", got)
	}
	if got := searchIDs(t, s, ChatSearch{Query: "fixed page", Project: "pawly", Agent: "agent-01"}); len(got) != 0 {
		t.Errorf("words out of order found %v", got)
	}
	// FTS5's own syntax is just text.
	for _, q := range []string{`"login`, `login*`, `NOT login`, `a:b`, `100%`, `x_y`} {
		if _, _, err := s.SearchChats(ctx, ChatSearch{Query: q, Project: "pawly", Agent: "agent-01"}); err != nil {
			t.Errorf("SearchChats(%q): %v", q, err)
		}
	}
	if got := searchIDs(t, s, ChatSearch{Query: "   ", Project: "pawly", Agent: "agent-01"}); len(got) != 0 {
		t.Errorf("blank query found %v", got)
	}
}

// Under three characters there are no trigrams to look up: the items are
// read one by one instead, and LIKE's wildcards are only characters.
func TestSearchChatsShortQueries(t *testing.T) {
	t.Parallel()
	s := openSearchStore(t)
	ctx := context.Background()
	if err := s.SaveChatItems(ctx, "pawly", "agent-01", chatRows(t,
		map[string]any{"id": "u1", "kind": "user", "text": "Go 1.26"},
		map[string]any{"id": "a1", "kind": "assistant", "text": "100% done"},
		map[string]any{"id": "a2", "kind": "assistant", "text": "1000 done"},
		map[string]any{"id": "t1", "kind": "tool", "text": "go"},
	)); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string][]string{"go": {"u1"}, "GO": {"u1"}, "%": {"a1"}, "0%": {"a1"}, "1": {"u1", "a1", "a2"}} {
		if got := searchIDs(t, s, ChatSearch{Query: q, Project: "pawly", Agent: "agent-01"}); !slices.Equal(got, want) {
			t.Errorf("SearchChats(%q) = %v, want %v", q, got, want)
		}
	}
}

// The index follows every write: a message streaming in, a rollback, a
// cleared chat, a deleted agent.
func TestSearchChatsFollowsTheConversation(t *testing.T) {
	t.Parallel()
	s := openSearchStore(t)
	ctx := context.Background()
	one := ChatSearch{Project: "pawly", Agent: "agent-01"}
	find := func(q string) []string { one.Query = q; return searchIDs(t, s, one) }
	save := func(items ...map[string]any) {
		t.Helper()
		if err := s.SaveChatItems(ctx, "pawly", "agent-01", chatRows(t, items...)); err != nil {
			t.Fatal(err)
		}
	}
	save(map[string]any{"id": "u1", "kind": "user", "text": "deploy it"}, map[string]any{"id": "a1", "kind": "assistant", "text": "Deploying", "streaming": true})
	save(map[string]any{"id": "u1", "kind": "user", "text": "deploy it"}, map[string]any{"id": "a1", "kind": "assistant", "text": "Deployed to staging"})
	if got := find("staging"); !slices.Equal(got, []string{"a1"}) {
		t.Errorf("after the answer streamed in, staging = %v", got)
	}
	if got := find("deploying"); len(got) != 0 {
		t.Errorf("the streamed text's old version is still found: %v", got)
	}
	// An item that stops being shown leaves the index.
	save(map[string]any{"id": "u1", "kind": "user", "text": "deploy it"}, map[string]any{"id": "a1", "kind": "assistant", "text": "Deployed to staging", "hidden": true})
	if got := find("staging"); len(got) != 0 {
		t.Errorf("hidden item found: %v", got)
	}
	save(map[string]any{"id": "u1", "kind": "user", "text": "deploy it"}, map[string]any{"id": "a1", "kind": "assistant", "text": "Deployed to staging"})
	if err := s.TruncateChatItems(ctx, "pawly", "agent-01", 1); err != nil {
		t.Fatal(err)
	}
	if got := find("deploy"); !slices.Equal(got, []string{"u1"}) {
		t.Errorf("after a rollback, deploy = %v", got)
	}
	if err := s.ClearChat(ctx, "pawly", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if got := find("deploy"); len(got) != 0 {
		t.Errorf("after clearing the chat, deploy = %v", got)
	}
	// The index agrees with its table: FTS5's own check.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO chat_items_fts (chat_items_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		t.Errorf("integrity-check: %v", err)
	}
}

// Without an agent a search covers a project's chats, and without a project
// every chat, best match first; Limit cuts it and says there was more.
func TestSearchChatsAcrossChats(t *testing.T) {
	t.Parallel()
	s := openSearchStore(t)
	ctx := context.Background()
	for _, c := range []struct{ project, agent string }{{"pawly", "lead"}, {"pawly", "agent-01"}, {"other", "agent-01"}} {
		if err := s.SaveChatItems(ctx, c.project, c.agent, chatRows(t,
			map[string]any{"id": "u1", "kind": "user", "text": "the release notes for " + c.project},
		)); err != nil {
			t.Fatal(err)
		}
	}
	hits, more, err := s.SearchChats(ctx, ChatSearch{Query: "release", Project: "pawly"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || more || hits[0].Project != "pawly" || hits[1].Project != "pawly" {
		t.Errorf("project-wide = %+v, more %v; want pawly's two chats", hits, more)
	}
	hits, _, err = s.SearchChats(ctx, ChatSearch{Query: "release"})
	if err != nil || len(hits) != 3 {
		t.Errorf("everywhere = %d hits, %v; want 3", len(hits), err)
	}
	hits, more, err = s.SearchChats(ctx, ChatSearch{Query: "release", Limit: 2})
	if err != nil || len(hits) != 2 || !more {
		t.Errorf("limit 2 = %d hits, more %v, %v; want 2 and more", len(hits), more, err)
	}
	if hits, _, _ := s.SearchChats(ctx, ChatSearch{Query: "re"}); len(hits) != 3 {
		t.Errorf("short query everywhere = %d hits, want 3", len(hits))
	}
}

// Upgrading indexes the conversations already stored.
func TestChatSearchMigrationIndexesStoredChats(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(migrations, func(m string) bool { return strings.Contains(m, "ADD COLUMN search_text") })
	for i, m := range migrations[:at] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", at)); err != nil {
		t.Fatal(err)
	}
	for i, data := range []string{
		`{"id":"u1","kind":"user","text":"Where is the config?"}`,
		`{"id":"t1","kind":"tool","tool":{"title":"config"}}`,
		`not json`,
		`{"id":"a1","kind":"assistant","text":"The config is in config.toml"}`,
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO chat_items (project, agent, id, position, data) VALUES ('pawly', 'agent-01', ?, ?, ?)`, fmt.Sprint(i), i, data); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got := searchIDs(t, s, ChatSearch{Query: "config", Project: "pawly", Agent: "agent-01"}); !slices.Equal(got, []string{"0", "3"}) {
		t.Errorf("after the upgrade, config = %v, want [0 3]", got)
	}
}

func TestSnippet(t *testing.T) {
	t.Parallel()
	show := func(parts []SnippetPart) string {
		var b strings.Builder
		for _, p := range parts {
			if p.Match {
				b.WriteString("[" + p.Text + "]")
			} else {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	long := strings.Repeat("lorem ipsum ", 10) + "the Ação went\nwell, and the acao again " + strings.Repeat("dolor sit ", 20)
	for _, c := range []struct{ text, query, want string }{
		{"Fix the login page", "LOGIN", "Fix the [login] page"},
		{"Não sei", "nao", "[Não] sei"},
		{"a  b\n\nc", "b c", "a [b c]"},
		{"nothing here", "absent", "nothing here"},
		{long, "acao", "…ipsum lorem ipsum lorem ipsum the [Ação] went well, and the [acao] again dolor sit dolor sit dolor sit dolor sit dolor sit dolor sit dolor sit dolor sit dolor sit d…"},
	} {
		if got := show(Snippet(c.text, c.query)); got != c.want {
			t.Errorf("Snippet(%.30q, %q) =\n %q\nwant\n %q", c.text, c.query, got, c.want)
		}
	}
}

// A hit says when its item was made; Kinds and Since narrow the search in the
// query, so the limit and more count only what is left, and CountChats counts
// the same items up to a cap.
func TestSearchChatsKindsSinceAndCount(t *testing.T) {
	t.Parallel()
	s := openSearchStore(t)
	ctx := context.Background()
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("BRT", -3*3600))
	recent := time.Now().Add(-time.Minute)
	if err := s.SaveChatItems(ctx, "pawly", "agent-01", chatRows(t,
		map[string]any{"id": "u1", "kind": "user", "text": "ship the build", "createdAt": old},
		map[string]any{"id": "a1", "kind": "assistant", "text": "the build shipped", "createdAt": recent},
		map[string]any{"id": "e1", "kind": "error", "text": "build failed", "createdAt": recent},
		map[string]any{"id": "n1", "kind": "notice", "text": "build restarted"},
	)); err != nil {
		t.Fatal(err)
	}
	one := ChatSearch{Query: "build", Project: "pawly", Agent: "agent-01"}
	hits, _, err := s.SearchChats(ctx, one)
	if err != nil || len(hits) != 4 {
		t.Fatalf("hits = %+v, %v", hits, err)
	}
	if !hits[0].At.Equal(old) || !hits[1].At.Equal(recent) || !hits[3].At.IsZero() {
		t.Errorf("At = %v, %v, %v; want %v, %v and none", hits[0].At, hits[1].At, hits[3].At, old, recent)
	}
	q := one
	q.Kinds = []string{"user", "error"}
	if got := searchIDs(t, s, q); !slices.Equal(got, []string{"u1", "e1"}) {
		t.Errorf("kinds user, error = %v", got)
	}
	q.Limit = 1
	if hits, more, err := s.SearchChats(ctx, q); err != nil || len(hits) != 1 || hits[0].ID != "u1" || !more {
		t.Errorf("kinds with limit 1 = %+v, more %v, %v", hits, more, err)
	}
	q = one
	q.Since = old.Add(time.Hour)
	if got := searchIDs(t, s, q); !slices.Equal(got, []string{"a1", "e1"}) {
		t.Errorf("since = %v, want the recent two", got)
	}
	for _, c := range []struct {
		q    ChatSearch
		most int
		want int
	}{{one, 10, 4}, {one, 2, 3}, {q, 10, 2}, {ChatSearch{Query: "bu", Kinds: []string{"notice"}}, 10, 1}, {ChatSearch{Query: " "}, 10, 0}} {
		if n, err := s.CountChats(ctx, c.q, c.most); err != nil || n != c.want {
			t.Errorf("CountChats(%+v, %d) = %d, %v; want %d", c.q, c.most, n, err, c.want)
		}
	}
	q = one
	q.Kinds = []string{"tool"}
	if _, _, err := s.SearchChats(ctx, q); err == nil {
		t.Error("an unknown kind was taken")
	}
}

// A chat's last line is the last message the user or the AI tool wrote, past
// what the timeline hides and what came after it that nobody wrote.
func TestLastChatLineSkipsWhatNobodyWrote(t *testing.T) {
	t.Parallel()
	s := openSearchStore(t)
	ctx := context.Background()
	if _, ok, err := s.LastChatLine(ctx, "pawly", "lead"); ok || err != nil {
		t.Fatalf("an empty chat has a last line (%v)", err)
	}
	if err := s.SaveChatItems(ctx, "pawly", "lead", chatRows(t,
		map[string]any{"id": "u1", "kind": "user", "text": "Fix the Login page"},
		map[string]any{"id": "a1", "kind": "assistant", "text": "On it: agent-02 has it."},
		map[string]any{"id": "t1", "kind": "tool", "text": "start_agent"},
		map[string]any{"id": "s1", "kind": "assistant", "parent": "sub", "text": "a subagent's"},
		map[string]any{"id": "h1", "kind": "user", "hidden": true, "text": "agent-02 finished"},
		map[string]any{"id": "w1", "kind": "user", "woken": true, "text": "monitor spoke"},
		map[string]any{"id": "n1", "kind": "notice", "text": "Rolled back"},
	)); err != nil {
		t.Fatal(err)
	}
	it, ok, err := s.LastChatLine(ctx, "pawly", "lead")
	if err != nil || !ok || it.ID != "a1" {
		t.Fatalf("LastChatLine = %q (%v, %v), want a1", it.ID, ok, err)
	}
}
