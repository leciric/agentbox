package state

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// ChatSearch is what SearchChats looks for, and in which chats.
type ChatSearch struct {
	// Query is found as typed, as one piece: any run of characters, words or
	// not, ignoring case and accents. Spaces at its ends count; a query of
	// nothing but spaces finds nothing.
	Query string
	// Project and Agent narrow the search: Agent to one chat (a lead's is its
	// project's chat), Project to a project's chats, neither to every chat
	// AgentBox holds. Agent needs Project.
	Project, Agent string
	// Limit is how many hits at most; 0 is DefaultChatSearchLimit.
	Limit int
}

const DefaultChatSearchLimit = 100

// ChatHit is a conversation item a search found.
type ChatHit struct {
	Project, Agent string
	ID             string
	Position       int64  // in its conversation
	Kind           string // user, aside, assistant, notice or error
	// Snippet is the item's text around the first match, cut to a line's
	// length, with every match in it marked.
	Snippet []SnippetPart
}

// SnippetPart is a piece of a snippet: a match, or the text between.
type SnippetPart struct {
	Text  string
	Match bool
}

// SearchChats finds what was said in chats: the items chat_items_fts
// indexes, what a conversation shows you rather than its tool calls. In one
// chat the hits come in the conversation's order; across chats, best match
// first. more says there were hits past the limit.
//
// Three characters or more go through the index. Fewer are too short for its
// trigrams and are looked for item by item instead, which SQLite's LIKE does
// ignoring case only in ASCII.
func (s *Store) SearchChats(ctx context.Context, q ChatSearch) (hits []ChatHit, more bool, err error) {
	if strings.TrimSpace(q.Query) == "" {
		return nil, false, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultChatSearchLimit
	}
	from, where, order := `chat_items i`, `i.search_text LIKE ? ESCAPE '\'`, `i.rowid DESC`
	args := []any{"%" + likeEscaper.Replace(q.Query) + "%"}
	if utf8.RuneCountInString(q.Query) >= 3 {
		// A quoted string is a phrase, which the trigram tokenizer reads as
		// a substring; a quote inside one is written twice.
		from = `chat_items_fts JOIN chat_items i ON i.rowid = chat_items_fts.rowid`
		where, order = `chat_items_fts MATCH ?`, `chat_items_fts.rank`
		args = []any{`"` + strings.ReplaceAll(q.Query, `"`, `""`) + `"`}
	}
	if q.Project != "" {
		where += ` AND i.project = ?`
		args = append(args, q.Project)
	}
	if q.Agent != "" {
		where += ` AND i.agent = ?`
		args = append(args, q.Agent)
		order = `i.position`
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT i.project, i.agent, i.id, i.position, json_extract(i.data, '$.kind'), i.search_text
		FROM `+from+` WHERE `+where+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var h ChatHit
		var text string
		if err := rows.Scan(&h.Project, &h.Agent, &h.ID, &h.Position, &h.Kind, &text); err != nil {
			return nil, false, err
		}
		if len(hits) == limit {
			more = true
			continue
		}
		h.Snippet = Snippet(text, q.Query)
		hits = append(hits, h)
	}
	return hits, more, rows.Err()
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// How much of an item's text a snippet keeps: some before the first match,
// and up to snippetRunes in all.
const (
	snippetBefore = 40
	snippetRunes  = 160
)

// Snippet cuts text down to a line around the first place query is in it,
// marking every match it keeps. Matching ignores case and accents, as the
// index does; a query the text doesn't hold leaves its start, unmarked. Runs
// of whitespace, line breaks included, read as one space.
func Snippet(text, query string) []SnippetPart {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	folded, at := foldRunes(runes)
	needle, _ := foldRunes([]rune(strings.Join(strings.Fields(query), " ")))
	first := index(folded, needle, 0)
	if first < 0 || len(needle) == 0 {
		return []SnippetPart{{Text: cut(runes, 0, snippetRunes)}}
	}
	start := max(0, at[first]-snippetBefore)
	// Start at a word, unless that throws away most of what comes before.
	if start > 0 {
		for i := start; i < at[first] && i < start+snippetBefore/2; i++ {
			if runes[i] == ' ' {
				start = i + 1
				break
			}
		}
	}
	end := min(len(runes), start+snippetRunes)
	var parts []SnippetPart
	add := func(from, to int, match bool) {
		if from < to {
			parts = append(parts, SnippetPart{Text: string(runes[from:to]), Match: match})
		}
	}
	if start > 0 {
		parts = append(parts, SnippetPart{Text: "…"})
	}
	pos := start
	for i := first; i >= 0; i = index(folded, needle, i+len(needle)) {
		from, to := at[i], at[i+len(needle)-1]+1
		if to > end {
			break
		}
		add(pos, from, false)
		add(from, to, true)
		pos = to
	}
	add(pos, end, false)
	if end < len(runes) {
		parts = append(parts, SnippetPart{Text: "…"})
	}
	return mergeText(parts)
}

// foldRunes is s without case or accents, and for each of its runes the index
// of the rune of s it came from.
func foldRunes(s []rune) (folded []rune, at []int) {
	for i, r := range s {
		for _, d := range norm.NFD.String(string(r)) {
			if unicode.Is(unicode.Mn, d) {
				continue
			}
			folded = append(folded, unicode.ToLower(d))
			at = append(at, i)
		}
	}
	return folded, at
}

func index(s, sub []rune, from int) int {
	if len(sub) == 0 {
		return -1
	}
	for i := from; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func cut(runes []rune, from, n int) string {
	if len(runes)-from <= n {
		return string(runes[from:])
	}
	return string(runes[from:from+n]) + "…"
}

// mergeText joins neighbouring parts that aren't matches, like an ellipsis
// and the text after it.
func mergeText(parts []SnippetPart) []SnippetPart {
	var out []SnippetPart
	for _, p := range parts {
		if n := len(out); n > 0 && !p.Match && !out[n-1].Match {
			out[n-1].Text += p.Text
			continue
		}
		out = append(out, p)
	}
	return out
}
