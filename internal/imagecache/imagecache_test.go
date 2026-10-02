package imagecache

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const indexType = "application/vnd.oci.image.index.v1+json"

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fakeHub is an upstream registry that asks for a bearer token, as Docker
// Hub does, and counts what it is asked.
type fakeHub struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	blobs    map[string][]byte
	tags     map[string][]byte // "name:tag" → manifest
	requests map[string]int    // "METHOD path" → count
	tokens   int
	down     bool
	lie      bool // serve the wrong bytes for blobs
}

func newFakeHub(t *testing.T) *fakeHub {
	h := &fakeHub{t: t, blobs: map[string][]byte{}, tags: map[string][]byte{}, requests: map[string]int{}}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) addBlob(b []byte) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := digestOf(b)
	h.blobs[d] = b
	return d
}

func (h *fakeHub) tag(name, tag string, manifest []byte) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tags[name+":"+tag] = manifest
	return digestOf(manifest)
}

func (h *fakeHub) count(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requests[key]
}

func (h *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/token" {
		h.tokens++
		if r.URL.Query().Get("service") != "registry.example" || !strings.HasPrefix(r.URL.Query().Get("scope"), "repository:") {
			http.Error(w, "bad token request", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"token":"t0k","expires_in":300}`)
		return
	}
	if r.Header.Get("Authorization") != "Bearer t0k" {
		w.Header().Set("Www-Authenticate", `Bearer realm="`+h.srv.URL+`/token",service="registry.example",scope="repository:x:pull"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	h.requests[r.Method+" "+r.URL.Path]++
	rest := strings.TrimPrefix(r.URL.Path, "/v2/")
	if i := strings.LastIndex(rest, "/blobs/"); i > 0 {
		b, ok := h.blobs[rest[i+len("/blobs/"):]]
		if !ok {
			http.Error(w, "unknown", http.StatusNotFound)
			return
		}
		if h.lie {
			b = append([]byte("x"), b[1:]...)
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(b)
		}
		return
	}
	if i := strings.LastIndex(rest, "/manifests/"); i > 0 {
		name, ref := rest[:i], rest[i+len("/manifests/"):]
		var body []byte
		if strings.HasPrefix(ref, "sha256:") {
			for _, m := range h.tags {
				if digestOf(m) == ref {
					body = m
				}
			}
		} else {
			body = h.tags[name+":"+ref]
		}
		if body == nil {
			http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", indexType)
		w.Header().Set("Docker-Content-Digest", digestOf(body))
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
		return
	}
	http.NotFound(w, r)
}

func newCache(t *testing.T, hub *fakeHub, max int64) (*Cache, *httptest.Server) {
	c := &Cache{Dir: t.TempDir(), Upstream: hub.srv.URL, Max: func() int64 { return max }}
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	return c, srv
}

func get(t *testing.T, method, url string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Add("Accept", indexType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestVersionCheckNeedsNoAuth(t *testing.T) {
	t.Parallel()
	_, srv := newCache(t, newFakeHub(t), 1<<20)
	resp, _ := get(t, http.MethodGet, srv.URL+"/v2/")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Docker-Distribution-API-Version") != "registry/2.0" {
		t.Fatalf("/v2/ = %d %q", resp.StatusCode, resp.Header.Get("Docker-Distribution-API-Version"))
	}
}

func TestABlobIsDownloadedOnceAndServedFromTheCacheAfter(t *testing.T) {
	t.Parallel()
	hub := newFakeHub(t)
	layer := []byte(strings.Repeat("layer", 1000))
	d := hub.addBlob(layer)
	c, srv := newCache(t, hub, 1<<20)
	for range 3 {
		resp, body := get(t, http.MethodGet, srv.URL+"/v2/library/alpine/blobs/"+d)
		if resp.StatusCode != http.StatusOK || string(body) != string(layer) {
			t.Fatalf("blob = %d, %d bytes", resp.StatusCode, len(body))
		}
		if resp.Header.Get("Docker-Content-Digest") != d {
			t.Errorf("Docker-Content-Digest = %q", resp.Header.Get("Docker-Content-Digest"))
		}
	}
	if n := hub.count("GET /v2/library/alpine/blobs/" + d); n != 1 {
		t.Errorf("the upstream was asked for the blob %d times, want 1", n)
	}
	if hub.tokens != 1 {
		t.Errorf("tokens fetched = %d, want 1 (reused while valid)", hub.tokens)
	}
	if got := c.Size(); got != int64(len(layer)) {
		t.Errorf("Size() = %d, want %d", got, len(layer))
	}
	// Another image with the same layer gets it from the cache too.
	if resp, _ := get(t, http.MethodHead, srv.URL+"/v2/library/other/blobs/"+d); resp.StatusCode != http.StatusOK || resp.ContentLength != int64(len(layer)) {
		t.Errorf("HEAD = %d, length %d", resp.StatusCode, resp.ContentLength)
	}
	if n := hub.count("HEAD /v2/library/other/blobs/" + d); n != 0 {
		t.Errorf("HEAD of a cached blob reached the upstream")
	}
}

func TestABlobThatIsntWhatItsDigestSaysIsntKept(t *testing.T) {
	t.Parallel()
	hub := newFakeHub(t)
	d := hub.addBlob([]byte("the real layer"))
	hub.lie = true
	c, srv := newCache(t, hub, 1<<20)
	get(t, http.MethodGet, srv.URL+"/v2/library/alpine/blobs/"+d)
	if c.Size() != 0 {
		t.Errorf("a corrupt blob was kept")
	}
	if _, err := os.Stat(c.blobPath(d)); err == nil {
		t.Errorf("a corrupt blob is on disk")
	}
}

func TestAManifestByTagIsCheckedWithAHeadAndServedFromTheCache(t *testing.T) {
	t.Parallel()
	hub := newFakeHub(t)
	manifest := []byte(`{"schemaVersion":2,"mediaType":"` + indexType + `","manifests":[]}`)
	d := hub.tag("library/alpine", "3.20", manifest)
	_, srv := newCache(t, hub, 1<<20)
	for range 2 {
		resp, body := get(t, http.MethodGet, srv.URL+"/v2/library/alpine/manifests/3.20")
		if resp.StatusCode != http.StatusOK || string(body) != string(manifest) {
			t.Fatalf("manifest = %d %q", resp.StatusCode, body)
		}
		if resp.Header.Get("Content-Type") != indexType || resp.Header.Get("Docker-Content-Digest") != d {
			t.Errorf("headers = %v", resp.Header)
		}
	}
	if n := hub.count("GET /v2/library/alpine/manifests/" + d); n != 1 {
		t.Errorf("manifest GETs upstream = %d, want 1", n)
	}
	if n := hub.count("HEAD /v2/library/alpine/manifests/3.20"); n != 2 {
		t.Errorf("tag HEADs upstream = %d, want 2: a tag is always checked", n)
	}
	// By digest, it needs no upstream at all.
	if resp, _ := get(t, http.MethodGet, srv.URL+"/v2/library/alpine/manifests/"+d); resp.StatusCode != http.StatusOK {
		t.Errorf("by digest = %d", resp.StatusCode)
	}
	if n := hub.count("GET /v2/library/alpine/manifests/" + d); n != 1 {
		t.Errorf("a cached manifest by digest reached the upstream")
	}
}

func TestATagMovedOnIsFetchedAgain(t *testing.T) {
	t.Parallel()
	hub := newFakeHub(t)
	hub.tag("library/alpine", "latest", []byte(`{"v":1}`))
	_, srv := newCache(t, hub, 1<<20)
	get(t, http.MethodGet, srv.URL+"/v2/library/alpine/manifests/latest")
	hub.tag("library/alpine", "latest", []byte(`{"v":2}`))
	if _, body := get(t, http.MethodGet, srv.URL+"/v2/library/alpine/manifests/latest"); string(body) != `{"v":2}` {
		t.Errorf("after the tag moved, got %q", body)
	}
}

func TestWhenTheUpstreamIsDownATagServesTheDigestItHadLast(t *testing.T) {
	t.Parallel()
	hub := newFakeHub(t)
	manifest := []byte(`{"v":1}`)
	hub.tag("library/alpine", "latest", manifest)
	_, srv := newCache(t, hub, 1<<20)
	get(t, http.MethodGet, srv.URL+"/v2/library/alpine/manifests/latest")
	hub.mu.Lock()
	hub.down = true
	hub.mu.Unlock()
	if resp, body := get(t, http.MethodGet, srv.URL+"/v2/library/alpine/manifests/latest"); resp.StatusCode != http.StatusOK || string(body) != string(manifest) {
		t.Errorf("with the upstream down = %d %q", resp.StatusCode, body)
	}
	// What was never cached is an error, which sends Docker to the registry itself.
	if resp, _ := get(t, http.MethodGet, srv.URL+"/v2/library/redis/manifests/latest"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("uncached with the upstream down = %d, want 502", resp.StatusCode)
	}
}

func TestAnUnknownManifestIsTheUpstreamsNotFound(t *testing.T) {
	t.Parallel()
	_, srv := newCache(t, newFakeHub(t), 1<<20)
	if resp, _ := get(t, http.MethodGet, srv.URL+"/v2/library/nope/manifests/latest"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown = %d, want 404", resp.StatusCode)
	}
}

func TestTheCacheStaysUnderItsCapEvictingWhatWasReadLongestAgo(t *testing.T) {
	t.Parallel()
	hub := newFakeHub(t)
	a := hub.addBlob([]byte(strings.Repeat("a", 400)))
	b := hub.addBlob([]byte(strings.Repeat("b", 400)))
	cc := hub.addBlob([]byte(strings.Repeat("c", 400)))
	c, srv := newCache(t, hub, 1000)
	get(t, http.MethodGet, srv.URL+"/v2/x/blobs/"+a)
	get(t, http.MethodGet, srv.URL+"/v2/x/blobs/"+b)
	// a is read again, later than b, so b is what goes.
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(c.blobPath(b), old, old)
	get(t, http.MethodGet, srv.URL+"/v2/x/blobs/"+cc)
	if got := c.Size(); got > 1000 {
		t.Fatalf("Size() = %d, over the cap", got)
	}
	if _, err := os.Stat(c.blobPath(b)); err == nil {
		t.Errorf("the blob read longest ago is still there")
	}
	for _, d := range []string{a, cc} {
		if _, err := os.Stat(c.blobPath(d)); err != nil {
			t.Errorf("%s was evicted: %v", d, err)
		}
	}
}

func TestABlobOverTheCapGoesThroughUncached(t *testing.T) {
	t.Parallel()
	hub := newFakeHub(t)
	big := []byte(strings.Repeat("z", 2000))
	d := hub.addBlob(big)
	c, srv := newCache(t, hub, 1000)
	if _, body := get(t, http.MethodGet, srv.URL+"/v2/x/blobs/"+d); len(body) != len(big) {
		t.Fatalf("got %d bytes", len(body))
	}
	if c.Size() != 0 {
		t.Errorf("a blob over the cap was kept")
	}
}

func TestLoweringTheCapAndTrimmingEvicts(t *testing.T) {
	t.Parallel()
	hub := newFakeHub(t)
	d := hub.addBlob([]byte(strings.Repeat("a", 500)))
	limit := int64(1000)
	c := &Cache{Dir: t.TempDir(), Upstream: hub.srv.URL, Max: func() int64 { return limit }}
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	get(t, http.MethodGet, srv.URL+"/v2/x/blobs/"+d)
	limit = 100
	c.Trim()
	if c.Size() != 0 {
		t.Errorf("Size() = %d after trimming to 100", c.Size())
	}
}

func TestClearEmptiesIt(t *testing.T) {
	t.Parallel()
	hub := newFakeHub(t)
	d := hub.addBlob([]byte("abc"))
	c, srv := newCache(t, hub, 1000)
	get(t, http.MethodGet, srv.URL+"/v2/x/blobs/"+d)
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if c.Size() != 0 {
		t.Errorf("Size() = %d after Clear", c.Size())
	}
	// Measured afresh, it is still empty.
	fresh := &Cache{Dir: c.Dir}
	if fresh.Size() != 0 {
		t.Errorf("a fresh look finds %d bytes", fresh.Size())
	}
}

func TestPushesAndBadPathsAreRefused(t *testing.T) {
	t.Parallel()
	_, srv := newCache(t, newFakeHub(t), 1000)
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPut, "/v2/x/manifests/latest", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v2/../../etc/blobs/sha256:" + strings.Repeat("0", 64), http.StatusNotFound},
		{http.MethodGet, "/v2/x/blobs/sha256:short", http.StatusNotFound},
		{http.MethodGet, "/v2/x/manifests/..", http.StatusNotFound},
		{http.MethodGet, "/v2/_catalog", http.StatusNotFound},
	} {
		if resp, _ := get(t, tc.method, srv.URL+tc.path); resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
}

func TestParseChallenge(t *testing.T) {
	t.Parallel()
	scheme, p := parseChallenge(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/alpine:pull"`)
	if scheme != "Bearer" || p["realm"] != "https://auth.docker.io/token" || p["service"] != "registry.docker.io" || p["scope"] != "repository:library/alpine:pull" {
		t.Errorf("parseChallenge = %q %v", scheme, p)
	}
}

func TestNothingIsKeptWhenTheDiskHasNoRoom(t *testing.T) {
	t.Parallel()
	hub := newFakeHub(t)
	d := hub.addBlob([]byte(strings.Repeat("a", 500)))
	c := &Cache{Dir: t.TempDir(), Upstream: hub.srv.URL, Max: func() int64 { return 1 << 20 }, Room: func() int64 { return 100 }}
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	if _, body := get(t, http.MethodGet, srv.URL+"/v2/x/blobs/"+d); len(body) != 500 {
		t.Fatalf("got %d bytes", len(body))
	}
	if c.Size() != 0 {
		t.Errorf("a blob was kept on a disk without room")
	}
}
