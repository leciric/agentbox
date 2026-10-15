// Package projecticon finds the icon a project already has — a favicon, a
// logo, the icon its package or web manifest names, or its GitHub owner's
// avatar — and checks the images and emoji a user picks instead. It reads
// files and fetches URLs and nothing else: the daemon keeps what it finds.
package projecticon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxBytes is the most an icon may be, found or chosen.
const MaxBytes = 512 << 10

// Icon is an image and where it came from.
type Icon struct {
	Data []byte
	MIME string
	// From is the repository-relative file or the URL's host and path it
	// came from, e.g. "public/favicon.svg" or "github.com/leciric".
	From string
}

// Fetcher gets a URL, for the avatar: its body, at most MaxBytes+1 of it,
// and its Content-Type. Tests pass a fake.
type Fetcher func(ctx context.Context, url string) (body []byte, contentType string, err error)

// HTTPFetcher fetches over the network, following redirects, giving up after
// timeout.
func HTTPFetcher(timeout time.Duration) Fetcher {
	client := &http.Client{Timeout: timeout}
	return func(ctx context.Context, u string) ([]byte, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("%s: %s", u, resp.Status)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
		return body, resp.Header.Get("Content-Type"), err
	}
}

// Options are what Detect needs beyond the repository.
type Options struct {
	// Owner is the GitHub owner of the repository's origin, called only when
	// nothing in the repository is an icon; nil or "" skips the avatar.
	Owner func() string
	// Fetch gets the avatar; nil skips it.
	Fetch Fetcher
}

// Detect looks for root's icon, first match wins: favicons, then logos and
// icons by their usual names, then logo files in public/ or assets/, then
// what package.json or a web manifest names, then the GitHub owner's
// avatar. ok is false when there is none.
func Detect(ctx context.Context, root string, opts Options) (Icon, bool) {
	if icon, ok := FindInRepo(root); ok {
		return icon, true
	}
	if opts.Owner == nil || opts.Fetch == nil {
		return Icon{}, false
	}
	owner := opts.Owner()
	if owner == "" {
		return Icon{}, false
	}
	return Avatar(ctx, owner, opts.Fetch)
}

var (
	faviconDirs = []string{"", "public", "static", "assets", "src", "app", "web", "src/app"}
	logoDirs    = []string{"", "public", "assets", "static", "src/assets", ".github"}
	logoNames   = []string{"logo.svg", "logo.png", "icon.svg", "icon.png", "apple-touch-icon.png"}
	manifests   = []string{"public/manifest.json", "public/site.webmanifest", "manifest.webmanifest", "public/manifest.webmanifest", "site.webmanifest", "manifest.json"}
)

// FindInRepo is Detect without the avatar: only the repository's own files.
// Nothing outside root is read, symlinks included.
func FindInRepo(root string) (Icon, bool) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return Icon{}, false
	}
	defer func() { _ = r.Close() }()
	var candidates []string
	for _, dir := range faviconDirs {
		candidates = append(candidates, path.Join(dir, "favicon.svg"), path.Join(dir, "favicon.ico"))
		// Next.js's app router serves app/icon.* as the favicon.
		if dir == "app" || dir == "src/app" {
			candidates = append(candidates, path.Join(dir, "icon.svg"), path.Join(dir, "icon.png"))
		}
	}
	for _, dir := range logoDirs {
		for _, name := range logoNames {
			candidates = append(candidates, path.Join(dir, name))
		}
	}
	for _, c := range candidates {
		if icon, ok := readIcon(r, c); ok {
			return icon, true
		}
	}
	for _, dir := range []string{"public", "assets"} {
		if icon, ok := logoIn(r, dir); ok {
			return icon, true
		}
	}
	if icon, ok := packageIcon(r); ok {
		return icon, true
	}
	for _, m := range manifests {
		if icon, ok := manifestIcon(r, m); ok {
			return icon, true
		}
	}
	return Icon{}, false
}

// readIcon reads one file under r as an icon: a regular file of at most
// MaxBytes whose content is an image.
func readIcon(r *os.Root, name string) (Icon, bool) {
	name = path.Clean(name)
	if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
		return Icon{}, false
	}
	f, err := r.Open(name)
	if err != nil {
		return Icon{}, false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return Icon{}, false
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil || len(data) > MaxBytes {
		return Icon{}, false
	}
	mime := Sniff(data)
	if mime == "" {
		return Icon{}, false
	}
	return Icon{Data: data, MIME: mime, From: name}, true
}

// logoIn is the first svg, then png, directly in dir whose name has "logo"
// in it.
func logoIn(r *os.Root, dir string) (Icon, bool) {
	entries, err := fs.ReadDir(r.FS(), dir)
	if err != nil {
		return Icon{}, false
	}
	for _, ext := range []string{".svg", ".png"} {
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			if e.IsDir() || !strings.Contains(name, "logo") || path.Ext(name) != ext {
				continue
			}
			if icon, ok := readIcon(r, path.Join(dir, e.Name())); ok {
				return icon, true
			}
		}
	}
	return Icon{}, false
}

// iconFile is a path a package or manifest names that may be an icon.
func iconFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".svg", ".ico":
		return true
	}
	return false
}

// packageIcon is package.json's "icon", or electron-builder's build.icon,
// when it names an image file.
func packageIcon(r *os.Root) (Icon, bool) {
	data, err := fs.ReadFile(r.FS(), "package.json")
	if err != nil || len(data) > 4*MaxBytes {
		return Icon{}, false
	}
	var pkg struct {
		Icon  any `json:"icon"`
		Build struct {
			Icon any `json:"icon"`
		} `json:"build"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return Icon{}, false
	}
	for _, v := range []any{pkg.Icon, pkg.Build.Icon} {
		name, _ := v.(string)
		if name != "" && iconFile(name) {
			if icon, ok := readIcon(r, strings.TrimPrefix(name, "./")); ok {
				return icon, true
			}
		}
	}
	return Icon{}, false
}

// manifestIcon is a web app manifest's best icon: an svg, else its largest
// png no bigger than 512px. Its src is relative to the manifest, and one
// starting with / is relative to the folder it is served from — the
// manifest's own, for the usual public/.
func manifestIcon(r *os.Root, name string) (Icon, bool) {
	data, err := fs.ReadFile(r.FS(), name)
	if err != nil || len(data) > MaxBytes {
		return Icon{}, false
	}
	var m struct {
		Icons []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
			Type  string `json:"type"`
		} `json:"icons"`
	}
	if json.Unmarshal(data, &m) != nil {
		return Icon{}, false
	}
	type choice struct {
		src  string
		rank int
	}
	var choices []choice
	for _, ic := range m.Icons {
		src := ic.Src
		if u, err := url.Parse(src); err != nil || u.Scheme != "" || u.Host != "" {
			continue
		} else {
			src = u.Path
		}
		ext := strings.ToLower(path.Ext(src))
		switch {
		case ext == ".svg" || ic.Type == "image/svg+xml":
			choices = append(choices, choice{src, 1 << 20})
		case ext == ".png" || ic.Type == "image/png":
			if size := largestSize(ic.Sizes); size <= 512 {
				choices = append(choices, choice{src, size})
			}
		}
	}
	slices.SortStableFunc(choices, func(a, b choice) int { return b.rank - a.rank })
	dir := path.Dir(name)
	for _, c := range choices {
		if icon, ok := readIcon(r, path.Join(dir, strings.TrimPrefix(c.src, "/"))); ok {
			return icon, true
		}
	}
	return Icon{}, false
}

// largestSize is the largest width in a manifest's sizes ("48x48 96x96"),
// 0 for "any" or none.
func largestSize(sizes string) int {
	largest := 0
	for _, s := range strings.Fields(sizes) {
		w, _, _ := strings.Cut(strings.ToLower(s), "x")
		if n, err := strconv.Atoi(w); err == nil && n > largest {
			largest = n
		}
	}
	return largest
}

// Avatar is the GitHub owner's avatar, when GitHub answers with an image.
func Avatar(ctx context.Context, owner string, fetch Fetcher) (Icon, bool) {
	if owner == "" || strings.ContainsAny(owner, "/?#@:") {
		return Icon{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, contentType, err := fetch(ctx, "https://github.com/"+url.PathEscape(owner)+".png?size=128")
	if err != nil || len(body) > MaxBytes || !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return Icon{}, false
	}
	mime := Sniff(body)
	if mime == "" {
		return Icon{}, false
	}
	return Icon{Data: body, MIME: mime, From: "github.com/" + owner}, true
}

// Sniff is the image type of data, from its content rather than any name:
// png, jpeg, gif, webp, x-icon or svg+xml, "" for anything else.
func Sniff(data []byte) string {
	switch t := http.DetectContentType(data); t {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon":
		return t
	}
	if looksSVG(data) {
		return "image/svg+xml"
	}
	return ""
}

// looksSVG is whether data is an SVG document: XML whose first element,
// after any prolog, comments and doctype, is <svg.
func looksSVG(data []byte) bool {
	s := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	for {
		s = bytes.TrimLeftFunc(s, unicode.IsSpace)
		switch {
		case bytes.HasPrefix(s, []byte("<?")):
			end := bytes.Index(s, []byte("?>"))
			if end < 0 {
				return false
			}
			s = s[end+2:]
		case bytes.HasPrefix(s, []byte("<!--")):
			end := bytes.Index(s, []byte("-->"))
			if end < 0 {
				return false
			}
			s = s[end+3:]
		case bytes.HasPrefix(s, []byte("<!")):
			end := bytes.IndexByte(s, '>')
			if end < 0 {
				return false
			}
			s = s[end+1:]
		default:
			return bytes.HasPrefix(s, []byte("<svg")) && len(s) > 4 && (s[4] == '>' || unicode.IsSpace(rune(s[4])) || s[4] == '/')
		}
	}
}

// DataURL is data as a base64 data: URL.
func DataURL(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// allowedMIME is what a chosen image may say it is, and what each means.
var allowedMIME = map[string]string{
	"image/png":                "image/png",
	"image/jpeg":               "image/jpeg",
	"image/gif":                "image/gif",
	"image/webp":               "image/webp",
	"image/svg+xml":            "image/svg+xml",
	"image/x-icon":             "image/x-icon",
	"image/vnd.microsoft.icon": "image/x-icon",
}

// ParseImage checks an image the user chose, a base64 data: URL, and returns
// its bytes and type: one of the allowed types, at most MaxBytes, and an
// image of that type in fact, so a renamed file is refused.
func ParseImage(dataURL string) ([]byte, string, error) {
	rest, ok := strings.CutPrefix(dataURL, "data:")
	if !ok {
		return nil, "", errors.New("the image must be a data: URL")
	}
	meta, payload, ok := strings.Cut(rest, ",")
	if !ok {
		return nil, "", errors.New("the image's data: URL has no data")
	}
	mime, isBase64 := strings.CutSuffix(meta, ";base64")
	if !isBase64 {
		return nil, "", errors.New("the image's data: URL must be base64")
	}
	mime, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(mime)), ";")
	want, ok := allowedMIME[mime]
	if !ok {
		return nil, "", fmt.Errorf("an icon can't be %q: use a png, jpeg, gif, webp, svg or ico", mime)
	}
	if base64.StdEncoding.DecodedLen(len(payload)) > MaxBytes+3 {
		return nil, "", fmt.Errorf("the image is larger than %d KiB", MaxBytes>>10)
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", fmt.Errorf("the image's data isn't valid base64: %w", err)
	}
	if len(data) > MaxBytes {
		return nil, "", fmt.Errorf("the image is larger than %d KiB", MaxBytes>>10)
	}
	if got := Sniff(data); got != want {
		return nil, "", fmt.Errorf("the file isn't the %s it says it is", mime)
	}
	return data, want, nil
}

// ParseEmoji checks an emoji the user chose and returns it trimmed: one
// short run with no spaces or control characters, which is what an emoji
// is, however many code points its joiners and modifiers take.
func ParseEmoji(emoji string) (string, error) {
	emoji = strings.TrimSpace(emoji)
	switch {
	case emoji == "":
		return "", errors.New("the emoji is empty")
	case !utf8.ValidString(emoji):
		return "", errors.New("the emoji isn't valid text")
	case len(emoji) > 32 || utf8.RuneCountInString(emoji) > 10:
		return "", errors.New("give one emoji")
	}
	for _, r := range emoji {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("give one emoji, without spaces")
		}
	}
	return emoji, nil
}
