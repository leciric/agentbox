package image_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/image"
)

// importedAs builds from p with a fake Incus that keeps what it is asked to
// import, and returns that file's name and content.
func importedAs(t *testing.T, opts image.Options) (string, []byte) {
	t.Helper()
	keep := t.TempDir()
	inc := fakeIncus(t, `  image) [ "$2" = import ] && { basename "$3" >`+keep+`/name; cp "$3" `+keep+`/content; } ;;`)
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, opts, &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	inc.neverRan(t, "/root/provision.sh")
	name, err := os.ReadFile(filepath.Join(keep, "name"))
	if err != nil {
		t.Fatalf("nothing was imported:\n%s", log.String())
	}
	content, _ := os.ReadFile(filepath.Join(keep, "content"))
	return strings.TrimSpace(string(name)), content
}

// asked are the requests p had for files starting with prefix.
func asked(p *published, prefix string) []string {
	var out []string
	for _, r := range p.requested() {
		if strings.HasPrefix(r, prefix) {
			out = append(out, r)
		}
	}
	return out
}

func manifestOf(t *testing.T, p *published) image.Published {
	t.Helper()
	var m image.Published
	if err := json.Unmarshal(p.files["image-"+runtime.GOARCH+".json"], &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// partsDir is where the download keeps a's parts until they are joined.
func partsDir(opts image.Options, a image.Archive) string {
	return filepath.Join(opts.DownloadDir, "download-"+a.SHA256[:16])
}

func TestDownloadPrefersZstd(t *testing.T) {
	defer image.SetHaveZstd(true)()
	p := publish(t, specs(image.PublishedTools()), nil)
	name, content := importedAs(t, publishedOptions(t, p))
	if name != zstdName || !bytes.Equal(content, zstdOf(t, tarball)) {
		t.Errorf("imported %s, %d bytes; want the zstd archive", name, len(content))
	}
	if got := asked(p, gzName); len(got) != 0 {
		t.Errorf("the gzipped archive shouldn't be downloaded too: %v", got)
	}
}

// Incus unpacks a .tar.zst with the zstd command: without it, the gzipped
// archive is downloaded instead while there is one.
func TestDownloadWithoutZstdTakesTheGzippedArchive(t *testing.T) {
	defer image.SetHaveZstd(false)()
	p := publish(t, specs(image.PublishedTools()), nil)
	name, content := importedAs(t, publishedOptions(t, p))
	if name != gzName || !bytes.Equal(content, gzipped) {
		t.Errorf("imported %s, %q; want the gzipped archive", name, content)
	}
	if got := asked(p, zstdName); len(got) != 0 {
		t.Errorf("the zstd archive shouldn't be downloaded too: %v", got)
	}
}

// Once only the zstd archive is published, a machine without zstd unpacks it
// itself and Incus imports the plain tarball.
func TestDownloadWithoutZstdUnpacksTheZstdArchive(t *testing.T) {
	defer image.SetHaveZstd(false)()
	p := publishAs(t, zstdOnly, specs(image.PublishedTools()), nil)
	opts := publishedOptions(t, p)
	name, content := importedAs(t, opts)
	if want := strings.TrimSuffix(zstdName, ".zst"); name != want || !bytes.Equal(content, tarball) {
		t.Errorf("imported %s, %d bytes; want %s, the tarball unpacked", name, len(content), want)
	}
	if entries, _ := os.ReadDir(opts.DownloadDir); len(entries) != 0 {
		t.Errorf("the download should be gone once Incus has it, found %v", entries)
	}
}

// What image versions published before zstd have: the gzipped archive alone.
func TestDownloadTheGzippedArchiveAlone(t *testing.T) {
	defer image.SetHaveZstd(true)()
	p := publishAs(t, gzipOnly, specs(image.PublishedTools()), nil)
	name, content := importedAs(t, publishedOptions(t, p))
	if name != gzName || !bytes.Equal(content, gzipped) {
		t.Errorf("imported %s, %q; want the gzipped archive", name, content)
	}
}

func TestDownloadFetchesThePartsAtOnce(t *testing.T) {
	defer image.SetHaveZstd(true)()
	p := publish(t, specs(image.PublishedTools()), nil)
	var mu sync.Mutex
	inFlight, most := 0, 0
	all := make(chan struct{})
	p.setHandle(func(w http.ResponseWriter, r *http.Request, name string) bool {
		if !strings.HasPrefix(name, zstdName+".") {
			return false
		}
		mu.Lock()
		inFlight++
		most = max(most, inFlight)
		if inFlight == 3 {
			close(all)
		}
		mu.Unlock()
		// Each part waits for the others to be asked for, or gives up.
		select {
		case <-all:
		case <-time.After(2 * time.Second):
		}
		mu.Lock()
		inFlight--
		mu.Unlock()
		return false
	})
	importedAs(t, publishedOptions(t, p))
	if most != 3 {
		t.Errorf("at most %d of the 3 parts downloaded at once, want all 3", most)
	}
}

// cut answers a request for name with the first half of it, and then breaks
// the connection, as a dropped connection does.
func cut(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b[:len(b)/2])
	w.(http.Flusher).Flush()
	panic(http.ErrAbortHandler)
}

// A part cut off halfway is resumed from where it stopped, not fetched again.
func TestDownloadResumesAPartThatWasCutOff(t *testing.T) {
	defer image.SetHaveZstd(true)()
	p := publish(t, specs(image.PublishedTools()), nil)
	part := zstdName + ".001"
	var once atomic.Bool
	p.setHandle(func(w http.ResponseWriter, r *http.Request, name string) bool {
		if name == part && once.CompareAndSwap(false, true) {
			cut(w, p.files[part])
		}
		return false
	})
	_, content := importedAs(t, publishedOptions(t, p))
	if !bytes.Equal(content, zstdOf(t, tarball)) {
		t.Error("the resumed download should join into the zstd archive")
	}
	want := []string{part, part + " bytes=" + strconv.Itoa(len(p.files[part])/2) + "-"}
	if got := asked(p, part); !slices.Equal(got, want) {
		t.Errorf("requests for %s = %q, want %q", part, got, want)
	}
}

// A part that stops coming without the connection failing is given up after
// a while, and resumed.
func TestDownloadResumesAPartThatStalls(t *testing.T) {
	defer image.SetHaveZstd(true)()
	defer image.SetStallTimeout(200 * time.Millisecond)()
	p := publish(t, specs(image.PublishedTools()), nil)
	part := zstdName + ".001"
	var once atomic.Bool
	p.setHandle(func(w http.ResponseWriter, r *http.Request, name string) bool {
		if name != part || !once.CompareAndSwap(false, true) {
			return false
		}
		b := p.files[part]
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b[:len(b)/2])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return true
	})
	if _, content := importedAs(t, publishedOptions(t, p)); !bytes.Equal(content, zstdOf(t, tarball)) {
		t.Error("the resumed download should join into the zstd archive")
	}
	want := []string{part, part + " bytes=" + strconv.Itoa(len(p.files[part])/2) + "-"}
	if got := asked(p, part); !slices.Equal(got, want) {
		t.Errorf("requests for %s = %q, want %q", part, got, want)
	}
}

// A download stopped halfway (the daemon restarted, the build cancelled)
// keeps its parts, and the next build carries on from them.
func TestDownloadResumesAfterARestart(t *testing.T) {
	defer image.SetHaveZstd(true)()
	p := publish(t, specs(image.PublishedTools()), nil)
	opts := publishedOptions(t, p)
	part := zstdName + ".002"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.setHandle(func(w http.ResponseWriter, r *http.Request, name string) bool {
		if name != part {
			return false
		}
		b := p.files[part]
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b[:len(b)/2])
		w.(http.Flusher).Flush()
		// Long enough for the client to write it down.
		time.Sleep(100 * time.Millisecond)
		cancel()
		<-r.Context().Done()
		return true
	})
	inc := fakeIncus(t, "")
	if err := image.Build(ctx, inc.Client, host, opts, &bytes.Buffer{}); err == nil {
		t.Fatal("a cancelled build should fail")
	}
	inc.neverRan(t, "/root/provision.sh")
	m := manifestOf(t, p)
	if fi, err := os.Stat(filepath.Join(partsDir(opts, *m.Zstd), part)); err != nil || fi.Size() != int64(len(p.files[part])/2) {
		t.Fatalf("the cancelled download should keep the half of %s it got: %v, %v", part, fi, err)
	}

	p.setHandle(nil)
	before := len(p.requested())
	var log bytes.Buffer
	inc = fakeIncus(t, "")
	if err := image.Build(t.Context(), inc.Client, host, opts, &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	inc.neverRan(t, "/root/provision.sh")
	again := p.requested()[before:]
	if !slices.Contains(again, part+" bytes="+strconv.Itoa(len(p.files[part])/2)+"-") {
		t.Errorf("%s should have been resumed: %q", part, again)
	}
	if !strings.Contains(log.String(), "Resuming the download") {
		t.Errorf("the log should say the download resumes:\n%s", log.String())
	}
}

// A part kept from before that turns out wrong is fetched again whole, and so
// is one whose server doesn't do ranges.
func TestDownloadRedoesAKeptPart(t *testing.T) {
	for name, tc := range map[string]struct {
		kept     func(part []byte) []byte
		noRanges bool
	}{
		"wrong":     {kept: func(part []byte) []byte { return bytes.Repeat([]byte("x"), len(part)/2) }},
		"too long":  {kept: func(part []byte) []byte { return append(slices.Clone(part), "more"...) }},
		"no ranges": {kept: func(part []byte) []byte { return part[:len(part)/2] }, noRanges: true},
	} {
		t.Run(name, func(t *testing.T) {
			defer image.SetHaveZstd(true)()
			p := publish(t, specs(image.PublishedTools()), nil)
			if tc.noRanges {
				p.setHandle(func(w http.ResponseWriter, r *http.Request, name string) bool {
					b, ok := p.files[name]
					if ok {
						_, _ = w.Write(b)
					}
					return ok
				})
			}
			opts := publishedOptions(t, p)
			part := zstdName + ".000"
			dir := partsDir(opts, *manifestOf(t, p).Zstd)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, part), tc.kept(p.files[part]), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, content := importedAs(t, opts); !bytes.Equal(content, zstdOf(t, tarball)) {
				t.Error("the download should join into the zstd archive")
			}
		})
	}
}

// The CDN first, GitHub when the CDN hasn't the image, or a part of it, or
// doesn't answer.
func TestDownloadFallsBackToTheNextMirror(t *testing.T) {
	defer image.SetHaveZstd(true)()
	for name, tc := range map[string]struct {
		cdn func(t *testing.T) string
		// fromGitHub are the parts GitHub should be asked for.
		fromGitHub []string
	}{
		"nothing on the CDN": {
			cdn: func(t *testing.T) string {
				return publish(t, nil, func(files map[string][]byte) { clear(files) }).URL
			},
			fromGitHub: []string{zstdName + ".000", zstdName + ".001", zstdName + ".002"},
		},
		"no answer from the CDN": {
			cdn: func(t *testing.T) string {
				p := publish(t, nil, nil)
				p.Close()
				return p.URL
			},
			fromGitHub: []string{zstdName + ".000", zstdName + ".001", zstdName + ".002"},
		},
		"a part failing on the CDN": {
			cdn: func(t *testing.T) string {
				p := publish(t, specs(image.PublishedTools()), nil)
				p.setHandle(func(w http.ResponseWriter, r *http.Request, name string) bool {
					if name == zstdName+".001" {
						http.Error(w, "boom", http.StatusBadGateway)
						return true
					}
					return false
				})
				return p.URL
			},
			fromGitHub: []string{zstdName + ".001"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			github := publish(t, specs(image.PublishedTools()), nil)
			opts := image.Options{ImagesURLs: []string{tc.cdn(t), github.URL}, DownloadDir: filepath.Join(t.TempDir(), "images")}
			if _, content := importedAs(t, opts); !bytes.Equal(content, zstdOf(t, tarball)) {
				t.Error("the download should join into the zstd archive")
			}
			// In any order: they download at once.
			if got := asked(github, zstdName); !slices.Equal(slices.Sorted(slices.Values(got)), tc.fromGitHub) {
				t.Errorf("GitHub was asked for %q, want %q", got, tc.fromGitHub)
			}
		})
	}
}

// When no mirror gives a part, the image is built here, and what the
// download got is deleted once it is.
func TestDownloadThatFailsEverywhereBuildsHere(t *testing.T) {
	defer image.SetHaveZstd(true)()
	breakPart := func(p *published) *published {
		p.setHandle(func(w http.ResponseWriter, r *http.Request, name string) bool {
			if name == zstdName+".001" {
				cut(w, p.files[name])
			}
			return false
		})
		return p
	}
	cdn := breakPart(publish(t, specs(image.PublishedTools()), nil))
	github := breakPart(publish(t, specs(image.PublishedTools()), nil))
	opts := image.Options{ImagesURLs: []string{cdn.URL, github.URL}, DownloadDir: filepath.Join(t.TempDir(), "images")}
	inc := fakeIncus(t, "")
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, opts, &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}
	fellBack(t, inc, log.String(), zstdName+".001")
	if n := len(asked(cdn, zstdName+".001")); n != 3 {
		t.Errorf("the CDN was asked for the part %d times, want 3", n)
	}
	if entries, _ := os.ReadDir(opts.DownloadDir); len(entries) != 0 {
		t.Errorf("the download should be gone once the image is built here, found %v", entries)
	}
}

// Progress is said at every tenth, as before parts downloaded at once.
func TestDownloadSaysHowFarItGot(t *testing.T) {
	defer image.SetHaveZstd(true)()
	p := publish(t, specs(image.PublishedTools()), nil)
	inc := fakeIncus(t, "")
	var log bytes.Buffer
	if err := image.Build(t.Context(), inc.Client, host, publishedOptions(t, p), &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "100% of 0 MB\n") {
		t.Errorf("the log should say the download got to 100%%:\n%s", log.String())
	}
}
