// Package cookieimport reads a cookie export the user made themselves — a
// Netscape cookies.txt, or the JSON of a cookie extension (Cookie-Editor,
// EditThisCookie, the chrome.cookies shape) or of Playwright's storageState —
// so a project can hand its agents' Chromium sessions the user is signed in
// to. It never reads a browser's own files: the user exports, picks the
// domains, and the daemon seals what is left as a project secret
// (internal/secrets).
//
// Nothing here logs or returns a cookie's value in an error.
package cookieimport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// The limits on an export: far above a real one, and low enough that a file
// picked by mistake (a video, a database) is refused before it is parsed.
const (
	MaxExportBytes = 4 << 20
	MaxCookies     = 5000
)

// Cookie is one cookie, in the shape the DevTools protocol's Storage.setCookies
// takes. Domain is the browser's host key: a leading dot makes it a domain
// cookie, sent to subdomains too; without one it is the host's alone.
type Cookie struct {
	Domain   string `json:"domain"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Path     string `json:"path"`
	Expires  int64  `json:"expires,omitempty"` // Unix seconds; 0 for a session cookie
	Secure   bool   `json:"secure,omitempty"`
	HTTPOnly bool   `json:"httpOnly,omitempty"`
	SameSite string `json:"sameSite,omitempty"` // Strict, Lax, None, or "" when unset
}

// Host is the cookie's host without the leading dot of a domain cookie.
func (c Cookie) Host() string { return strings.TrimPrefix(c.Domain, ".") }

// The formats Parse tells apart.
const (
	FormatNetscape   = "netscape"   // cookies.txt, as curl, wget and yt-dlp read it
	FormatExtension  = "extension"  // a JSON array, as cookie extensions export it
	FormatPlaywright = "playwright" // {"cookies": [...]}, Playwright's storageState
)

// Parse reads an export in any of the formats, dropping cookies that have
// expired by now and partitioned ones (a partitioned cookie belongs to one
// top-level site and can't be set as a plain cookie). It says which format
// it found.
func Parse(data string, now time.Time) ([]Cookie, string, error) {
	if len(data) > MaxExportBytes {
		return nil, "", fmt.Errorf("the export is %d bytes, more than the %d a cookie export can be: is it the right file?", len(data), MaxExportBytes)
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(data, "\ufeff"))
	if trimmed == "" {
		return nil, "", errors.New("the export is empty")
	}
	var (
		cookies []Cookie
		format  string
		err     error
	)
	switch trimmed[0] {
	case '[':
		format = FormatExtension
		cookies, err = parseJSONArray([]byte(trimmed))
	case '{':
		format = FormatPlaywright
		var state struct {
			Cookies json.RawMessage `json:"cookies"`
		}
		if err = json.Unmarshal([]byte(trimmed), &state); err == nil {
			if len(state.Cookies) == 0 {
				return nil, format, errors.New(`the JSON has no "cookies" list: export the cookies as a list, or as Playwright's storage state`)
			}
			cookies, err = parseJSONArray(state.Cookies)
		}
	default:
		format = FormatNetscape
		cookies, err = parseNetscape(trimmed)
	}
	if err != nil {
		return nil, format, err
	}
	var out []Cookie
	for _, c := range cookies {
		if c.Expires != 0 && c.Expires <= now.Unix() {
			continue
		}
		out = append(out, c)
	}
	if len(out) > MaxCookies {
		return nil, format, fmt.Errorf("the export has %d cookies, more than the %d one import may hold: export fewer sites", len(out), MaxCookies)
	}
	if len(out) == 0 {
		return nil, format, errors.New("the export has no cookies that haven't expired")
	}
	return out, format, nil
}

// parseNetscape reads cookies.txt: seven tab-separated fields per line —
// domain, include subdomains, path, secure, expiry, name, value — with #
// comments, and the #HttpOnly_ prefix curl writes before an HttpOnly
// cookie's domain.
func parseNetscape(data string) ([]Cookie, error) {
	var out []Cookie
	sc := bufio.NewScanner(strings.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), MaxExportBytes)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimRight(sc.Text(), "\r")
		httpOnly := false
		if rest, ok := strings.CutPrefix(text, "#HttpOnly_"); ok {
			text, httpOnly = rest, true
		}
		if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "#") {
			continue
		}
		f := strings.Split(text, "\t")
		if len(f) == 6 { // an empty value with its tab trimmed away
			f = append(f, "")
		}
		if len(f) != 7 {
			return nil, fmt.Errorf("line %d isn't a cookies.txt line (seven fields separated by tabs): is this a Netscape cookie file?", line)
		}
		expires, err := strconv.ParseFloat(f[4], 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: the expiry %q isn't a number", line, f[4])
		}
		domain := strings.ToLower(f[0])
		if strings.EqualFold(f[1], "TRUE") && !strings.HasPrefix(domain, ".") {
			domain = "." + domain
		}
		c := Cookie{Domain: domain, Path: f[2], Secure: strings.EqualFold(f[3], "TRUE"), Expires: int64(expires), Name: f[5], Value: f[6], HTTPOnly: httpOnly}
		if err := c.check(); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		out = append(out, c)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// jsonCookie is the union of the JSON shapes: the chrome.cookies one the
// extensions export (expirationDate, hostOnly, session, sameSite as
// no_restriction/lax/strict/unspecified) and Playwright's (expires, -1 for a
// session cookie; sameSite Strict/Lax/None).
type jsonCookie struct {
	Domain         string   `json:"domain"`
	Name           *string  `json:"name"`
	Value          string   `json:"value"`
	Path           string   `json:"path"`
	ExpirationDate *float64 `json:"expirationDate"`
	Expires        *float64 `json:"expires"`
	Secure         bool     `json:"secure"`
	HTTPOnly       bool     `json:"httpOnly"`
	SameSite       *string  `json:"sameSite"`
	HostOnly       *bool    `json:"hostOnly"`
	Session        bool     `json:"session"`
	PartitionKey   any      `json:"partitionKey"`
}

func parseJSONArray(data []byte) ([]Cookie, error) {
	var raw []jsonCookie
	if err := json.Unmarshal(data, &raw); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return nil, fmt.Errorf("the JSON doesn't parse at byte %d: is the export whole?", syn.Offset)
		}
		return nil, errors.New("the JSON isn't a list of cookies, each with a domain, a name and a value")
	}
	var out []Cookie
	for i, j := range raw {
		if j.PartitionKey != nil && j.PartitionKey != "" {
			continue
		}
		if j.Name == nil {
			return nil, fmt.Errorf("cookie %d has no name: is this a cookie export?", i+1)
		}
		c := Cookie{Domain: strings.ToLower(j.Domain), Name: *j.Name, Value: j.Value, Path: j.Path, Secure: j.Secure, HTTPOnly: j.HTTPOnly}
		if j.HostOnly != nil {
			if *j.HostOnly {
				c.Domain = strings.TrimPrefix(c.Domain, ".")
			} else if !strings.HasPrefix(c.Domain, ".") {
				c.Domain = "." + c.Domain
			}
		}
		exp := j.ExpirationDate
		if exp == nil {
			exp = j.Expires
		}
		if exp != nil && *exp > 0 && !j.Session && !math.IsInf(*exp, 0) {
			c.Expires = int64(*exp)
		}
		if j.SameSite != nil {
			c.SameSite = sameSite(*j.SameSite)
		}
		if c.Path == "" {
			c.Path = "/"
		}
		if err := c.check(); err != nil {
			return nil, fmt.Errorf("cookie %d: %w", i+1, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// sameSite maps both vocabularies onto the DevTools protocol's.
func sameSite(s string) string {
	switch strings.ToLower(s) {
	case "strict":
		return "Strict"
	case "lax":
		return "Lax"
	case "none", "no_restriction":
		return "None"
	}
	return ""
}

func (c Cookie) check() error {
	switch {
	case c.Host() == "":
		return errors.New("a cookie has no domain")
	case c.Name == "" && c.Value == "":
		return errors.New("a cookie has neither a name nor a value")
	case !strings.HasPrefix(c.Path, "/"):
		return fmt.Errorf("a cookie of %s has the path %q, which doesn't start with /", c.Host(), c.Path)
	}
	return nil
}

// Site is the domain a cookie belongs to as a person would name it: its
// registrable domain (github.com for gist.github.com, example.co.uk for
// www.example.co.uk), or its host when it has none (localhost, an IP).
func Site(host string) string {
	host = strings.TrimPrefix(strings.ToLower(host), ".")
	if site, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return site
	}
	return host
}

// DomainCount is how many cookies an export holds for one site: what the
// user picks from.
type DomainCount struct {
	Domain  string `json:"domain"`
	Cookies int    `json:"cookies"`
}

// Domains groups cookies by Site, most cookies first, then by name.
func Domains(cookies []Cookie) []DomainCount {
	counts := map[string]int{}
	for _, c := range cookies {
		counts[Site(c.Host())]++
	}
	out := make([]DomainCount, 0, len(counts))
	for d, n := range counts {
		out = append(out, DomainCount{Domain: d, Cookies: n})
	}
	slices.SortFunc(out, func(a, b DomainCount) int {
		if a.Cookies != b.Cookies {
			return b.Cookies - a.Cookies
		}
		return strings.Compare(a.Domain, b.Domain)
	})
	return out
}

// NormalizeDomain turns what a user typed — a domain, a URL, a host with a
// port, *.example.com — into the domain Filter matches, or "" when nothing
// is left.
func NormalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	if i := strings.Index(d, "://"); i >= 0 {
		d = d[i+3:]
	}
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	if i := strings.LastIndex(d, ":"); i >= 0 && !strings.Contains(d, "[") {
		d = d[:i]
	}
	d = strings.TrimPrefix(d, "*.")
	return strings.Trim(d, ".")
}

// Filter keeps the cookies of the given domains and their subdomains: github.com
// keeps github.com's, .github.com's and gist.github.com's, and not
// notgithub.com's. A parent's domain cookie (.github.com, when the user
// picked gist.github.com) is kept too, since the browser sends it there.
func Filter(cookies []Cookie, domains []string) []Cookie {
	var want []string
	for _, d := range domains {
		if d = NormalizeDomain(d); d != "" {
			want = append(want, d)
		}
	}
	var out []Cookie
	for _, c := range cookies {
		host := strings.ToLower(c.Host())
		for _, d := range want {
			if host == d || strings.HasSuffix(host, "."+d) ||
				(strings.HasPrefix(c.Domain, ".") && strings.HasSuffix(d, "."+host) && Site(host) == Site(d)) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}
