// Package projecticon finds the icon a project already has — a favicon, a
// logo, the icon its package or web manifest names, or its GitHub owner's
// avatar — and checks the images and emoji a user picks instead. It reads
// files and fetches URLs and nothing else: the daemon keeps what it finds.
package projecticon

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	// Avatar is whether it is the GitHub owner's avatar rather than a file
	// in the repository.
	Avatar bool
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

// MaxCandidates is the most icons Candidates returns.
const MaxCandidates = 8

// Detect looks for root's icons, best first: the repository's own
// (Candidates), else its GitHub owner's avatar alone. None is nil.
func Detect(ctx context.Context, root string, opts Options) []Icon {
	if icons := Candidates(root); len(icons) > 0 {
		return icons
	}
	if opts.Owner == nil || opts.Fetch == nil {
		return nil
	}
	owner := opts.Owner()
	if owner == "" {
		return nil
	}
	if icon, ok := Avatar(ctx, owner, opts.Fetch); ok {
		return []Icon{icon}
	}
	return nil
}

var (
	faviconDirs = []string{"", "public", "static", "assets", "src", "app", "web", "src/app"}
	logoDirs    = []string{"", "public", "assets", "static", "src/assets", ".github"}
	logoNames   = []string{"logo.svg", "logo.png", "icon.svg", "icon.png", "apple-touch-icon.png"}
	manifests   = []string{"public/manifest.json", "public/site.webmanifest", "manifest.webmanifest", "public/manifest.webmanifest", "site.webmanifest", "manifest.json"}
)

// FindInRepo is the best of Candidates.
func FindInRepo(root string) (Icon, bool) {
	if icons := Candidates(root); len(icons) > 0 {
		return icons[0], true
	}
	return Icon{}, false
}

// Candidates is the icons in root, best first, at most MaxCandidates, none
// twice: first the usual places in their order — favicons, then logos and
// icons by their usual names, then logo files in public/ or assets/, then
// what package.json or a web manifest names — then what a shallow look
// through the rest of the tree finds (walk). Nothing outside root is read,
// symlinks included.
func Candidates(root string) []Icon {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil
	}
	defer func() { _ = r.Close() }()
	var out []Icon
	tried := map[string]bool{}
	seen := map[[sha256.Size]byte]bool{}
	for _, name := range append(usualPlaces(r), walk(r)...) {
		if name = path.Clean(name); tried[name] {
			continue
		}
		tried[name] = true
		icon, ok := readIcon(r, name)
		if !ok {
			continue
		}
		if sum := sha256.Sum256(icon.Data); !seen[sum] {
			seen[sum] = true
			if out = append(out, icon); len(out) == MaxCandidates {
				break
			}
		}
	}
	return out
}

// usualPlaces is where a repository's icon usually is, in order.
func usualPlaces(r *os.Root) []string {
	var names []string
	for _, dir := range faviconDirs {
		names = append(names, path.Join(dir, "favicon.svg"), path.Join(dir, "favicon.ico"))
		// Next.js's app router serves app/icon.* as the favicon.
		if dir == "app" || dir == "src/app" {
			names = append(names, path.Join(dir, "icon.svg"), path.Join(dir, "icon.png"))
		}
	}
	for _, dir := range logoDirs {
		for _, name := range logoNames {
			names = append(names, path.Join(dir, name))
		}
	}
	for _, dir := range []string{"public", "assets"} {
		names = append(names, logosIn(r, dir)...)
	}
	names = append(names, packageIcons(r, "package.json")...)
	for _, m := range manifests {
		names = append(names, manifestIcons(r, m)...)
	}
	return names
}

// Read reads one of root's files as an icon, as Candidates would: name is
// repository-relative, and nothing outside root is read.
func Read(root, name string) (Icon, bool) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return Icon{}, false
	}
	defer func() { _ = r.Close() }()
	return readIcon(r, name)
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

// logosIn is the svgs, then pngs, directly in dir whose name has "logo" in
// it.
func logosIn(r *os.Root, dir string) []string {
	entries, err := fs.ReadDir(r.FS(), dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, ext := range []string{".svg", ".png"} {
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			if !e.IsDir() && strings.Contains(name, "logo") && path.Ext(name) == ext {
				names = append(names, path.Join(dir, e.Name()))
			}
		}
	}
	return names
}

// iconFile is a path a package or manifest names that may be an icon.
func iconFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".svg", ".ico":
		return true
	}
	return false
}

// packageIcons is what a package.json names as its icon, relative to the
// repository: its "icon", electron-builder's build.icon and the icon of each
// platform's build, when they name an image file.
func packageIcons(r *os.Root, name string) []string {
	data, err := fs.ReadFile(r.FS(), name)
	if err != nil || len(data) > 4*MaxBytes {
		return nil
	}
	type icon struct {
		Icon any `json:"icon"`
	}
	var pkg struct {
		Icon  any `json:"icon"`
		Build struct {
			Icon  any  `json:"icon"`
			Mac   icon `json:"mac"`
			Win   icon `json:"win"`
			Linux icon `json:"linux"`
		} `json:"build"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	return inDir(path.Dir(name), pkg.Icon, pkg.Build.Icon, pkg.Build.Mac.Icon, pkg.Build.Win.Icon, pkg.Build.Linux.Icon)
}

// appJSONIcons is what an Expo app.json names as its icon.
func appJSONIcons(r *os.Root, name string) []string {
	data, err := fs.ReadFile(r.FS(), name)
	if err != nil || len(data) > MaxBytes {
		return nil
	}
	var app struct {
		Icon any `json:"icon"`
		Expo struct {
			Icon any `json:"icon"`
		} `json:"expo"`
	}
	if json.Unmarshal(data, &app) != nil {
		return nil
	}
	return inDir(path.Dir(name), app.Expo.Icon, app.Icon)
}

// inDir is each of names that is a string naming an image file, joined to
// dir.
func inDir(dir string, names ...any) []string {
	var out []string
	for _, v := range names {
		if name, _ := v.(string); name != "" && iconFile(name) {
			out = append(out, path.Join(dir, name))
		}
	}
	return out
}

// manifestIcons is a web app manifest's icons, best first: svgs, then its
// pngs no bigger than 512px, largest first. Their src is relative to the
// manifest, and one starting with / is relative to the folder it is served
// from — the manifest's own, for the usual public/.
func manifestIcons(r *os.Root, name string) []string {
	data, err := fs.ReadFile(r.FS(), name)
	if err != nil || len(data) > MaxBytes {
		return nil
	}
	var m struct {
		Icons []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
			Type  string `json:"type"`
		} `json:"icons"`
	}
	if json.Unmarshal(data, &m) != nil {
		return nil
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
	var out []string
	for _, c := range choices {
		out = append(out, path.Join(dir, strings.TrimPrefix(c.src, "/")))
	}
	return out
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
	return Icon{Data: body, MIME: mime, From: "github.com/" + owner, Avatar: true}, true
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
