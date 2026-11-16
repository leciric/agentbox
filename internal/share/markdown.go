package share

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// markdown is the little of Markdown a chat message uses, as HTML: headings,
// lists, quotes, fenced code, and inline code, bold, italics and links. It
// escapes everything first, so a message can't put markup of its own on the
// page, and a link goes only to http(s).
func markdown(s string) template.HTML {
	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var para []string
	list := "" // ul or ol while one is open
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + inline(strings.Join(para, "\n")) + "</p>")
			para = nil
		}
	}
	closeList := func() {
		if list != "" {
			b.WriteString("</" + list + ">")
			list = ""
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			flush()
			closeList()
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			b.WriteString(`<pre class="code"`)
			if lang != "" {
				b.WriteString(` data-lang="` + html.EscapeString(strings.Fields(lang)[0]) + `"`)
			}
			b.WriteString("><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>")
		case trimmed == "":
			flush()
			closeList()
		case heading.MatchString(trimmed):
			flush()
			closeList()
			m := heading.FindStringSubmatch(trimmed)
			b.WriteString("<h4>" + inline(m[2]) + "</h4>")
		case bullet.MatchString(line), numbered.MatchString(line):
			flush()
			kind, text := "ul", ""
			if m := bullet.FindStringSubmatch(line); m != nil {
				text = m[1]
			} else {
				kind, text = "ol", numbered.FindStringSubmatch(line)[1]
			}
			if list != kind {
				closeList()
				b.WriteString("<" + kind + ">")
				list = kind
			}
			b.WriteString("<li>" + inline(text) + "</li>")
		case strings.HasPrefix(trimmed, ">"):
			flush()
			closeList()
			b.WriteString("<blockquote>" + inline(strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))) + "</blockquote>")
		case list != "" && strings.HasPrefix(line, "  "):
			// A list item's continued line.
			b.WriteString("<p class=\"cont\">" + inline(trimmed) + "</p>")
		default:
			closeList()
			para = append(para, trimmed)
		}
	}
	flush()
	closeList()
	return template.HTML(b.String()) //nolint:gosec // every piece of text was escaped above
}

var (
	heading  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	bullet   = regexp.MustCompile(`^\s{0,3}[-*+]\s+(.*)$`)
	numbered = regexp.MustCompile(`^\s{0,3}\d+[.)]\s+(.*)$`)

	inlineCode = regexp.MustCompile("`([^`\n]+)`")
	bold       = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	italic     = regexp.MustCompile(`(^|[\s(])\*([^*\s][^*\n]*)\*`)
	link       = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	bareURL    = regexp.MustCompile(`(^|[\s(])(https?://[^\s<)]+[^\s<).,;:!?'"])`)
)

// inline is one block's text as HTML: escaped, with its code spans kept
// apart from the rest so nothing inside them is read as markup.
func inline(s string) string {
	var b strings.Builder
	for {
		loc := inlineCode.FindStringSubmatchIndex(s)
		if loc == nil {
			b.WriteString(spans(s))
			break
		}
		b.WriteString(spans(s[:loc[0]]))
		b.WriteString("<code>" + html.EscapeString(s[loc[2]:loc[3]]) + "</code>")
		s = s[loc[1]:]
	}
	return strings.ReplaceAll(b.String(), "\n", "<br>")
}

func spans(s string) string {
	s = html.EscapeString(s)
	// The escaping turned a link's & into &amp;, which is still the same URL.
	s = link.ReplaceAllString(s, `<a href="$2" rel="noopener noreferrer">$1</a>`)
	s = bareURL.ReplaceAllString(s, `$1<a href="$2" rel="noopener noreferrer">$2</a>`)
	s = bold.ReplaceAllString(s, "<strong>$1</strong>")
	return italic.ReplaceAllString(s, "$1<em>$2</em>")
}
