package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sync/errgroup"
)

// How the published image is downloaded: its parts at the same time, each
// from the first mirror that has it, into a directory of its own under
// DownloadDir that stays when the download fails or the daemon stops, so the
// next build carries on from where this one got to. A part is checked against
// its sum before it is joined, and a part that doesn't match is fetched again
// whole.

// parallelParts is how many parts are downloaded at once. One connection to
// GitHub or the CDN rarely fills a fast line, a few do.
const parallelParts = 4

// partAttempts is how many times a part is tried on each mirror, resuming from
// where the last try stopped, before the next mirror is asked.
const partAttempts = 3

// retryDelay is how long to wait before trying a part again, times the
// attempt. A variable for the tests.
var retryDelay = 2 * time.Second

// stallTimeout is how long a part's download may get nothing before it is
// given up and tried again, resuming: a connection can hang without failing.
// A variable for the tests.
var stallTimeout = time.Minute

// haveZstd says whether this machine, where Incus unpacks the images it
// imports, has the zstd command Incus runs for a .tar.zst. A variable for
// the tests.
var haveZstd = func() bool {
	_, err := exec.LookPath("zstd")
	return err == nil
}

// errNotFound is a mirror answering 404 for a file: no use asking it again.
var errNotFound = errors.New("not found")

// mirrors are the places a published image's files may be fetched from, each
// ending in /image-<version>/, the first preferred.
type mirrors []string

func newMirrors(bases []string) mirrors {
	var m mirrors
	for _, b := range bases {
		m = append(m, strings.TrimRight(b, "/")+"/image-"+Version+"/")
	}
	return m
}

// downloadDir is where the parts of a are kept while they download: named
// after a's sum, so that parts of another image are never taken for its own.
func downloadDir(dir string, a Archive) string {
	return filepath.Join(dir, "download-"+a.SHA256[:16])
}

// cleanDownloads removes what earlier downloads left in dir, keeping keep: the
// parts of another image, and the joined file a download of before parts
// resumed left behind.
func cleanDownloads(dir, keep string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if path != keep && (strings.HasPrefix(e.Name(), "download-") || strings.HasSuffix(e.Name(), ".part")) {
			_ = os.RemoveAll(path)
		}
	}
}

// fetchArchive downloads a's parts from m into dir, joins them into one file
// and checks it against a's sum. It returns the joined file's path. Parts
// already downloaded, whole or in part, by an earlier try are kept and only
// what they lack is fetched.
func fetchArchive(ctx context.Context, client *http.Client, m mirrors, dir string, a Archive, sums map[string]string, log io.Writer) (string, error) {
	parts := downloadDir(dir, a)
	cleanDownloads(dir, parts)
	if err := os.MkdirAll(parts, 0o755); err != nil {
		return "", err
	}
	prog := &progress{log: log, total: a.Size}
	for _, pt := range a.Parts {
		if fi, err := os.Stat(filepath.Join(parts, pt.File)); err == nil && fi.Size() <= pt.Size {
			prog.done += fi.Size()
		}
	}
	if prog.done > 0 {
		_, _ = fmt.Fprintf(log, "Resuming the download: %d of %d MB are already here\n", prog.done>>20, a.Size>>20)
		prog.shown = int(prog.done * 100 / a.Size)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelParts)
	for _, pt := range a.Parts {
		g.Go(func() error {
			return fetchPartFrom(gctx, client, m, filepath.Join(parts, pt.File), pt, sums[pt.File], prog)
		})
	}
	if err := g.Wait(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, a.File)
	if err := join(path, parts, a); err != nil {
		return "", err
	}
	return path, nil
}

// fetchPartFrom gets one part into path, from each mirror in turn until one
// gives it whole and with the right sum.
func fetchPartFrom(ctx context.Context, client *http.Client, m mirrors, path string, pt Part, wantSum string, prog *progress) error {
	var errs []error
	for _, base := range m {
		for attempt := range partAttempts {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retryDelay * time.Duration(attempt)):
				}
			}
			err := fetchPart(ctx, client, base+pt.File, path, pt, wantSum, prog)
			if err == nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			errs = append(errs, err)
			if errors.Is(err, errNotFound) {
				break
			}
		}
	}
	return errors.Join(errs...)
}

// fetchPart downloads what path lacks of pt from url, appending to it, and
// checks the whole part's sum. A part that turns out wrong is deleted, so the
// next try starts it again.
func fetchPart(ctx context.Context, client *http.Client, url, path string, pt Part, wantSum string, prog *progress) (err error) {
	if wantSum == "" || wantSum != pt.SHA256 {
		return fmt.Errorf("%s doesn't match its SHA256SUMS line", pt.File)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	h := sha256.New()
	have, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if have > pt.Size {
		// Not counted in the progress: fetchArchive skips a part this long.
		have = 0
		h.Reset()
		if err := f.Truncate(0); err != nil {
			return err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	if have < pt.Size {
		n, err := fetchRange(ctx, client, url, f, h, have, pt.Size, prog)
		if err != nil {
			return err
		}
		have = n
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSum {
		prog.add(-have)
		_ = os.Remove(path)
		return fmt.Errorf("%s doesn't match its SHA256SUMS line: its sum is %s, not %s", pt.File, got, wantSum)
	}
	return nil
}

// fetchRange appends to f, which holds the first have bytes of a file of size
// bytes, the rest from url, hashing it into h. A server that ignores the range
// sends the whole file, which is then written again from the start. It
// returns how much of the file f holds.
func fetchRange(ctx context.Context, client *http.Client, url string, f *os.File, h hash.Hash, have, size int64, prog *progress) (int64, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stalled := fmt.Errorf("downloading %s: nothing came for %s", url, stallTimeout)
	watchdog := time.AfterFunc(stallTimeout, func() { cancel(stalled) })
	defer watchdog.Stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return have, err
	}
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := client.Do(req)
	if err != nil {
		if context.Cause(ctx) == stalled {
			return have, stalled
		}
		return have, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
	case resp.StatusCode == http.StatusOK:
		if have > 0 {
			prog.add(-have)
			have = 0
			h.Reset()
			if err := f.Truncate(0); err != nil {
				return 0, err
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return 0, err
			}
		}
	case resp.StatusCode == http.StatusNotFound:
		return have, fmt.Errorf("downloading %s: %w (404)", url, errNotFound)
	default:
		return have, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	// One byte more than the part should have, to tell a longer one.
	body := &watched{r: io.LimitReader(resp.Body, size-have+1), watchdog: watchdog}
	n, err := io.Copy(io.MultiWriter(f, h, prog), body)
	have += n
	if err != nil {
		if context.Cause(ctx) == stalled {
			return have, stalled
		}
		return have, fmt.Errorf("downloading %s: %w", url, err)
	}
	if have != size {
		// A longer file is wrong whatever its start; a shorter one may
		// have been cut off, and the next try resumes it.
		if have > size {
			prog.add(-have)
			_ = f.Truncate(0)
			have = 0
		}
		return have, fmt.Errorf("downloading %s: got %d bytes, not the %d image-<arch>.json says", url, have, size)
	}
	return have, nil
}

// watched pushes its watchdog back whenever something is read.
type watched struct {
	r        io.Reader
	watchdog *time.Timer
}

func (w *watched) Read(b []byte) (int, error) {
	n, err := w.r.Read(b)
	if n > 0 {
		w.watchdog.Reset(stallTimeout)
	}
	return n, err
}

// join writes a's parts, from dir, into path through a .part file, deleting
// each once it is in, and renames it only once the whole has a's sum. dir is
// removed once it is empty.
func join(path, dir string, a Archive) (err error) {
	tmp := path + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	whole := sha256.New()
	for _, pt := range a.Parts {
		in, err := os.Open(filepath.Join(dir, pt.File))
		if err != nil {
			return err
		}
		_, err = io.Copy(io.MultiWriter(out, whole), in)
		_ = in.Close()
		if err != nil {
			return err
		}
	}
	if got := hex.EncodeToString(whole.Sum(nil)); got != a.SHA256 {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("%s doesn't match its SHA256SUMS line: its sum is %s, not %s", a.File, got, a.SHA256)
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// unzstd decompresses a .tar.zst into a plain .tar next to it, which Incus
// imports without the zstd command, and removes the .tar.zst. It returns the
// .tar's path.
func unzstd(path string) (_ string, err error) {
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	dec, err := zstd.NewReader(in, zstd.WithDecoderMaxWindow(1<<31))
	if err != nil {
		return "", err
	}
	defer dec.Close()
	tar := strings.TrimSuffix(path, ".zst")
	out, err := os.Create(tar + ".part")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = out.Close()
		if err != nil {
			_ = os.Remove(tar + ".part")
		}
	}()
	if _, err := io.Copy(out, dec); err != nil {
		return "", fmt.Errorf("decompressing %s: %w", filepath.Base(path), err)
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tar+".part", tar); err != nil {
		return "", err
	}
	_ = os.Remove(path)
	return tar, nil
}

// progress writes a line to the build log at every tenth of the download.
// Parts download at once, so it takes a lock.
type progress struct {
	mu          sync.Mutex
	log         io.Writer
	total, done int64
	shown       int
}

func (p *progress) Write(b []byte) (int, error) {
	p.add(int64(len(b)))
	return len(b), nil
}

// add counts n more bytes, or takes -n back for bytes thrown away.
func (p *progress) add(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += n
	if p.total > 0 {
		if pct := int(p.done * 100 / p.total); pct/progressEvery > p.shown/progressEvery && pct <= 100 {
			p.shown = pct
			_, _ = fmt.Fprintf(p.log, "%d%% of %d MB\n", pct/progressEvery*progressEvery, p.total>>20)
		}
	}
}
