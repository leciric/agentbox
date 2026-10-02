// Package imagecache is the pull-through cache of container images that every
// agent's Docker shares, so an image is downloaded once rather than once per
// agent: two agents pulling the same stack used to keep two copies of every
// layer, and a busy VM filled its disk with them.
//
// It is a small Docker Registry v2 server, read-only, in front of one upstream
// registry (Docker Hub). It was preferred to running registry:2 in proxy mode
// because that has no size cap, only a time to live, and collects garbage
// offline; this one keeps the cache under a cap at all times, evicting the
// blobs read longest ago, and needs nothing installed or pulled to run.
//
// What it caches is content-addressed: blobs, and manifests by digest, which
// never change. A tag is asked of the upstream every time (a HEAD, which Docker
// Hub doesn't count against its pull limit), and only the manifest it names is
// served from the cache; when the upstream can't be reached, the digest the tag
// had last is served instead. Anything it can't answer is an error, and Docker
// then pulls from the registry itself: the cache only ever saves a download.
package imagecache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DockerHub is the registry Docker's registry-mirrors setting stands in for,
// and the only one it does: images from ghcr.io, quay.io and others are
// pulled from their registry directly.
const DockerHub = "https://registry-1.docker.io"

// maxManifest is the largest manifest the cache reads. Registries refuse
// bigger ones themselves (Docker Hub's limit is 4 MiB).
const maxManifest = 4 << 20

// blobTimeout bounds one blob's download from the upstream. It goes on after
// the agent that asked hangs up, so the next one finds it cached.
const blobTimeout = time.Hour

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
	tagRe    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestRe = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// Cache is the cache's HTTP handler and its store on disk.
type Cache struct {
	// Dir holds the cache: blobs/ by digest, the media types of the manifests
	// among them in types/, the digest each tag had last in tags/, and
	// downloads in progress in tmp/.
	Dir string
	// Upstream is the registry it caches, DockerHub when empty.
	Upstream string
	// Client asks the upstream; http.DefaultClient when nil.
	Client *http.Client
	// Max is the most the cache may hold, in bytes, asked again whenever it
	// grows. Zero or less keeps nothing: every request goes through.
	Max func() int64
	// Room, when set, is how many more bytes the disk can take: a download
	// bigger than that isn't kept, so the cache never fills the disk past
	// what the rest of the machine needs.
	Room func() int64
	// Logf, when set, is told about what failed on the way.
	Logf func(format string, args ...any)

	mu       sync.Mutex
	size     int64
	sized    bool
	inflight map[string]bool
	tokens   map[string]token
}

type token struct {
	value   string
	expires time.Time
}

// Size is how many bytes the cache holds.
func (c *Cache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.measureLocked()
	return c.size
}

func (c *Cache) measureLocked() {
	if c.sized {
		return
	}
	c.size = 0
	for _, b := range c.blobs() {
		c.size += b.size
	}
	c.sized = true
	// A download a daemon stopped in the middle of is never finished. One
	// younger than the longest a download may take could be this daemon's own.
	tmp := filepath.Join(c.Dir, "tmp")
	entries, _ := os.ReadDir(tmp)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 2*blobTimeout {
			_ = os.Remove(filepath.Join(tmp, e.Name()))
		}
	}
}

type blob struct {
	path string
	size int64
	used time.Time
}

func (c *Cache) blobs() []blob {
	var out []blob
	root := filepath.Join(c.Dir, "blobs")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, blob{path: path, size: info.Size(), used: info.ModTime()})
		return nil
	})
	return out
}

// Trim evicts the blobs read longest ago until the cache is under Max, to
// nine tenths of it so that it isn't trimmed again with every new blob.
// Called by itself as the cache grows, and by whoever lowers Max.
func (c *Cache) Trim() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trimLocked()
}

func (c *Cache) trimLocked() {
	c.measureLocked()
	limit := c.max()
	if c.size <= limit {
		return
	}
	target := limit / 10 * 9
	all := c.blobs()
	slices.SortFunc(all, func(a, b blob) int { return a.used.Compare(b.used) })
	for _, b := range all {
		if c.size <= target {
			break
		}
		if err := os.Remove(b.path); err != nil {
			continue
		}
		_ = os.Remove(filepath.Join(c.Dir, "types", filepath.Base(b.path)))
		c.size -= b.size
	}
}

// Clear empties the cache.
func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, dir := range []string{"blobs", "types", "tags"} {
		if err := os.RemoveAll(filepath.Join(c.Dir, dir)); err != nil {
			return err
		}
	}
	c.size, c.sized = 0, true
	return nil
}

func (c *Cache) max() int64 {
	if c.Max == nil {
		return 0
	}
	return c.Max()
}

func (c *Cache) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Cache) upstreamURL() string {
	if c.Upstream != "" {
		return strings.TrimRight(c.Upstream, "/")
	}
	return DockerHub
}

func (c *Cache) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}

// ServeHTTP answers the read half of the registry API: /v2/, manifests and
// blobs. Pushes and the catalog aren't for a cache.
func (c *Cache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		registryError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "the image cache only serves pulls")
		return
	}
	if r.URL.Path == "/v2/" || r.URL.Path == "/v2" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/v2/")
	if !ok {
		registryError(w, http.StatusNotFound, "NOT_FOUND", "not a registry path")
		return
	}
	if i := strings.LastIndex(rest, "/manifests/"); i > 0 {
		name, ref := rest[:i], rest[i+len("/manifests/"):]
		if !nameRe.MatchString(name) || (!tagRe.MatchString(ref) && !digestRe.MatchString(ref)) {
			registryError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "invalid name or reference")
			return
		}
		c.manifest(w, r, name, ref)
		return
	}
	if i := strings.LastIndex(rest, "/blobs/"); i > 0 {
		name, digest := rest[:i], rest[i+len("/blobs/"):]
		if !nameRe.MatchString(name) || !digestRe.MatchString(digest) {
			registryError(w, http.StatusNotFound, "BLOB_UNKNOWN", "invalid name or digest")
			return
		}
		c.blob(w, r, name, digest)
		return
	}
	registryError(w, http.StatusNotFound, "NOT_FOUND", "not a registry path")
}

func registryError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": code, "message": message}}})
}

func (c *Cache) blobPath(digest string) string {
	return filepath.Join(c.Dir, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
}

func (c *Cache) typePath(digest string) string {
	return filepath.Join(c.Dir, "types", strings.TrimPrefix(digest, "sha256:"))
}

func (c *Cache) tagPath(name, tag string) string {
	return filepath.Join(c.Dir, "tags", filepath.FromSlash(name), "_tags", tag)
}

// blob serves a blob from the cache, or from the upstream while keeping a
// copy. A second agent asking for a blob that is still downloading gets it
// from the upstream as well, uncached, rather than waiting on the first.
func (c *Cache) blob(w http.ResponseWriter, r *http.Request, name, digest string) {
	if f, err := os.Open(c.blobPath(digest)); err == nil {
		defer func() { _ = f.Close() }()
		c.touch(c.blobPath(digest))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Etag", `"`+digest+`"`)
		http.ServeContent(w, r, "", time.Time{}, f)
		return
	}
	keep := r.Method == http.MethodGet && r.Header.Get("Range") == "" && c.max() > 0 && c.claim(digest)
	if keep {
		defer c.release(digest)
	}
	ctx := r.Context()
	if keep {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), blobTimeout)
		defer cancel()
	}
	header := http.Header{}
	if v := r.Header.Get("Range"); v != "" {
		header.Set("Range", v)
	}
	resp, err := c.upstream(ctx, r.Method, name, "/blobs/"+digest, header)
	if err != nil {
		c.logf("image cache: %s@%s: %v", name, digest, err)
		registryError(w, http.StatusBadGateway, "UNAVAILABLE", "the upstream registry can't be reached")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	copyHeaders(w, resp, "Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Docker-Content-Digest", "Etag")
	if resp.StatusCode != http.StatusOK || !keep {
		w.WriteHeader(resp.StatusCode)
		if r.Method == http.MethodGet {
			_, _ = io.Copy(w, resp.Body)
		}
		return
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusOK)
	if err := c.store(digest, resp.Body, &lenient{w: w}); err != nil {
		c.logf("image cache: %s@%s: %v", name, digest, err)
	}
}

// store writes body to the cache as digest, copying it to w as it goes, and
// keeps it only when all of it arrived and it is what its digest says.
func (c *Cache) store(digest string, body io.Reader, w io.Writer) error {
	tmpDir := filepath.Join(c.Dir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		_, _ = io.Copy(w, body)
		return err
	}
	tmp, err := os.CreateTemp(tmpDir, "blob-*")
	if err != nil {
		_, _ = io.Copy(w, body)
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h, w), body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != digest {
		return fmt.Errorf("the upstream sent %s for %s", got, digest)
	}
	return c.keep(tmp.Name(), digest, n)
}

// keep moves a verified download into the cache, unless it alone is over the
// cap, and trims the cache back under it.
func (c *Cache) keep(tmp, digest string, n int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.measureLocked()
	if n > c.max() || (c.Room != nil && n > c.Room()) {
		return nil
	}
	path := c.blobPath(digest)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	c.size += n
	c.trimLocked()
	return nil
}

func (c *Cache) claim(digest string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight[digest] {
		return false
	}
	if c.inflight == nil {
		c.inflight = map[string]bool{}
	}
	c.inflight[digest] = true
	return true
}

func (c *Cache) release(digest string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, digest)
}

// touch marks a blob as read now, which is what eviction goes by.
func (c *Cache) touch(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// lenient is the agent's end of a download being cached: once the agent hangs
// up, the rest goes to the cache alone.
type lenient struct {
	w   io.Writer
	err error
}

func (l *lenient) Write(p []byte) (int, error) {
	if l.err == nil {
		_, l.err = l.w.Write(p)
	}
	return len(p), nil
}

func copyHeaders(w http.ResponseWriter, resp *http.Response, keys ...string) {
	for _, k := range keys {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
}

// manifest serves a manifest. One asked for by digest comes from the cache
// when it's there. One asked for by tag is asked of the upstream with a HEAD,
// which says which digest the tag has now; that digest comes from the cache
// when it's there, and from the upstream otherwise.
func (c *Cache) manifest(w http.ResponseWriter, r *http.Request, name, ref string) {
	accept := r.Header.Values("Accept")
	digest := ref
	if !digestRe.MatchString(ref) {
		resp, err := c.upstream(r.Context(), http.MethodHead, name, "/manifests/"+ref, http.Header{"Accept": accept})
		if err != nil || resp.StatusCode >= 500 {
			if err == nil {
				_ = resp.Body.Close()
				err = errors.New(resp.Status)
			}
			// The upstream is down: the digest the tag had last will do.
			if last, rerr := os.ReadFile(c.tagPath(name, ref)); rerr == nil && c.serveCached(w, r, string(last)) {
				return
			}
			c.logf("image cache: %s:%s: %v", name, ref, err)
			registryError(w, http.StatusBadGateway, "UNAVAILABLE", "the upstream registry can't be reached")
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			passStatus(w, r, resp)
			return
		}
		digest = resp.Header.Get("Docker-Content-Digest")
		if !digestRe.MatchString(digest) {
			// No digest to go by: fetch by tag, uncached.
			c.proxyManifest(w, r, name, ref, accept)
			return
		}
		c.rememberTag(name, ref, digest)
	}
	if c.serveCached(w, r, digest) {
		return
	}
	resp, err := c.upstream(r.Context(), http.MethodGet, name, "/manifests/"+digest, http.Header{"Accept": accept})
	if err != nil {
		c.logf("image cache: %s@%s: %v", name, digest, err)
		registryError(w, http.StatusBadGateway, "UNAVAILABLE", "the upstream registry can't be reached")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		passStatus(w, r, resp)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifest+1))
	if err != nil || len(body) > maxManifest {
		registryError(w, http.StatusBadGateway, "UNAVAILABLE", "the upstream's manifest didn't arrive whole")
		return
	}
	sum := sha256.Sum256(body)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != digest {
		registryError(w, http.StatusBadGateway, "DIGEST_INVALID", "the upstream sent "+got+" for "+digest)
		return
	}
	mediaType := resp.Header.Get("Content-Type")
	c.keepManifest(digest, mediaType, body)
	writeManifest(w, r, digest, mediaType, body)
}

// proxyManifest passes a manifest through from the upstream, keeping nothing.
func (c *Cache) proxyManifest(w http.ResponseWriter, r *http.Request, name, ref string, accept []string) {
	resp, err := c.upstream(r.Context(), r.Method, name, "/manifests/"+ref, http.Header{"Accept": accept})
	if err != nil {
		registryError(w, http.StatusBadGateway, "UNAVAILABLE", "the upstream registry can't be reached")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	copyHeaders(w, resp, "Content-Type", "Content-Length", "Docker-Content-Digest")
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, io.LimitReader(resp.Body, maxManifest))
	}
}

func passStatus(w http.ResponseWriter, r *http.Request, resp *http.Response) {
	copyHeaders(w, resp, "Content-Type")
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 64<<10))
	}
}

func (c *Cache) serveCached(w http.ResponseWriter, r *http.Request, digest string) bool {
	if !digestRe.MatchString(digest) {
		return false
	}
	body, err := os.ReadFile(c.blobPath(digest))
	if err != nil {
		return false
	}
	mediaType, err := os.ReadFile(c.typePath(digest))
	if err != nil {
		return false
	}
	c.touch(c.blobPath(digest))
	writeManifest(w, r, digest, string(mediaType), body)
	return true
}

func writeManifest(w http.ResponseWriter, r *http.Request, digest, mediaType string, body []byte) {
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Etag", `"`+digest+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

func (c *Cache) keepManifest(digest, mediaType string, body []byte) {
	if mediaType == "" || int64(len(body)) > c.max() {
		return
	}
	if err := writeFile(c.typePath(digest), []byte(mediaType)); err != nil {
		c.logf("image cache: %v", err)
		return
	}
	tmpDir := filepath.Join(c.Dir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(tmpDir, "manifest-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = c.keep(tmp.Name(), digest, int64(len(body)))
	}
	if err != nil {
		c.logf("image cache: %v", err)
	}
}

func (c *Cache) rememberTag(name, tag, digest string) {
	if c.max() <= 0 {
		return
	}
	if err := writeFile(c.tagPath(name, tag), []byte(digest)); err != nil {
		c.logf("image cache: %v", err)
	}
}

func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o600)
}

// upstream asks the upstream registry for name's path, with an anonymous pull
// token when the registry asks for one.
func (c *Cache) upstream(ctx context.Context, method, name, path string, header http.Header) (*http.Response, error) {
	scope := "repository:" + name + ":pull"
	do := func(bearer string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, c.upstreamURL()+"/v2/"+name+path, nil)
		if err != nil {
			return nil, err
		}
		for k, vs := range header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		return c.client().Do(req)
	}
	resp, err := do(c.cachedToken(scope))
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	challenge := resp.Header.Get("Www-Authenticate")
	_ = resp.Body.Close()
	bearer, err := c.fetchToken(ctx, challenge, scope)
	if err != nil {
		return nil, err
	}
	return do(bearer)
}

func (c *Cache) cachedToken(scope string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.tokens[scope]; ok && time.Now().Before(t.expires) {
		return t.value
	}
	return ""
}

// fetchToken gets an anonymous token for scope from the realm a 401's
// challenge names.
func (c *Cache) fetchToken(ctx context.Context, challenge, scope string) (string, error) {
	scheme, params := parseChallenge(challenge)
	if !strings.EqualFold(scheme, "bearer") || params["realm"] == "" {
		return "", fmt.Errorf("the upstream asks for credentials the cache doesn't have (%q)", challenge)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, params["realm"], nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	// The challenge's own scope is what the registry wants; the token is
	// still kept under the one asked for, which is what the next request asks.
	asked := scope
	if s := params["scope"]; s != "" {
		asked = s
	}
	q.Set("scope", asked)
	req.URL.RawQuery = q.Encode()
	resp, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token: %s", resp.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	value := body.Token
	if value == "" {
		value = body.AccessToken
	}
	if value == "" {
		return "", errors.New("token: the registry sent none")
	}
	life := time.Duration(body.ExpiresIn) * time.Second
	if life < time.Minute {
		life = time.Minute
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokens == nil {
		c.tokens = map[string]token{}
	}
	// Taken back a little early, so it never runs out mid-request.
	c.tokens[scope] = token{value: value, expires: time.Now().Add(life - 30*time.Second)}
	return value, nil
}

// parseChallenge reads a WWW-Authenticate header: its scheme, and its
// key="value" parameters.
func parseChallenge(header string) (string, map[string]string) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(header), " ")
	params := map[string]string{}
	for rest = strings.TrimSpace(rest); rest != ""; {
		key, after, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		key = strings.ToLower(strings.TrimSpace(key))
		var value string
		if strings.HasPrefix(after, `"`) {
			end := strings.Index(after[1:], `"`)
			if end < 0 {
				value, rest = after[1:], ""
			} else {
				value, rest = after[1:1+end], after[2+end:]
			}
		} else {
			value, rest, _ = strings.Cut(after, ",")
		}
		params[key] = value
		rest = strings.TrimLeft(strings.TrimSpace(rest), ",")
		rest = strings.TrimSpace(rest)
	}
	return scheme, params
}
