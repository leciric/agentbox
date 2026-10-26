// Package pkgcache is the package managers' caches every agent shares: one
// directory in AgentBox's VM, mounted into each agent at the same path
// (agent/pkgcache.go), that pnpm, npm, Yarn, Go, pip, uv, Corepack,
// Playwright and Electron download into. A dependency, a module or a browser several agents
// need is downloaded once, and outlives the agents that downloaded it.
//
// Agents write into it themselves, through their own package managers: the
// daemon never sees a download go by, as it does with the Docker image cache
// (internal/imagecache). So it measures the directory from time to time and
// evicts what was used longest ago, and only in pieces the tool that owns them
// recovers from losing — a whole download it fetches again — never half of a
// module or of a browser.
//
// Every cache here tolerates several agents at once:
//
//   - pnpm's store is content-addressed, written through temporary files and
//     renames, and its index is SQLite (v11) or one file a package (v10);
//   - npm's cache is cacache, content-addressed with atomic writes, and npx
//     locks its install directory (npm 11);
//   - Yarn Berry's global cache is one zip a package, written then renamed;
//   - Go's module and build caches are made to be shared by concurrent go
//     commands, with lock files of their own;
//   - pip's HTTP cache and wheels are written to a temporary file then
//     renamed, and uv locks its cache;
//   - Playwright locks its browsers' directory while it installs, and
//     Corepack downloads into a temporary directory it renames;
//   - Electron's downloader (@electron/get) writes a zip to a temporary
//     directory and renames it into the cache.
//
// Two caches are left out on purpose. Yarn 1's cache unpacks a package straight
// into its final directory and deletes one it finds incomplete, so two agents
// installing the same package at once can break each other's: it stays in each
// agent (~/.cache/yarn). And Playwright's own clean-up of browsers no project
// uses is turned off (PLAYWRIGHT_SKIP_BROWSER_GC): from one agent, other
// agents' projects look gone, and it would delete their browsers. The eviction
// here does that job instead.
package pkgcache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Subdirectories of the cache, one per tool: agent/pkgcache.go points each
// tool at its own.
const (
	PnpmStore  = "pnpm/store"
	PnpmCache  = "pnpm/cache"
	Npm        = "npm"
	NpmNpx     = "npm/_npx"
	Yarn       = "yarn"
	GoMod      = "go/mod"
	GoBuild    = "go/build"
	Pip        = "pip"
	Uv         = "uv"
	Playwright = "ms-playwright"
	Corepack   = "corepack"
	Electron   = "electron"
)

// unit says how much of a subdirectory goes at once when it's evicted.
type unit int

const (
	// eachFile: any one file can go; its tool fetches it again when it finds
	// it missing.
	eachFile unit = -1
	// whole: only the whole subdirectory can go, because its tool keeps
	// things in it that depend on each other.
	whole unit = 0
	// A positive unit is a depth: each entry that many levels down is
	// evicted whole, like a browser's directory.
)

type part struct {
	dir  string
	unit unit
}

// parts are the subdirectories and how each is evicted. A part inside another
// (npm/_npx in npm) is the inner one's alone.
var parts = []part{
	// pnpm: a file of the store gone is fetched again (checked with pnpm 12),
	// but its index database is never evicted.
	{PnpmStore, eachFile},
	{PnpmCache, eachFile},
	{Npm, eachFile},
	// npx installs a package's whole tree in a directory of its own.
	{NpmNpx, 1},
	{Yarn, eachFile},
	// Go's module cache holds read-only module trees next to the zips and
	// checksums go verifies them with: all or nothing, as `go clean -modcache`.
	{GoMod, whole},
	// The build cache's entries are independent, and go trims it file by
	// file itself.
	{GoBuild, eachFile},
	{Pip, eachFile},
	// uv links its unpacked archives from other entries, and asks that
	// nothing but itself prune the cache.
	{Uv, whole},
	// One directory a browser and version: chromium-1187, firefox-1490.
	{Playwright, 1},
	// v1/<package manager>/<version>.
	{Corepack, 2},
	// Electron's binaries, one directory a download's URL: Electron 44 and
	// later fetch theirs when first run, ~120 MB an agent otherwise.
	{Electron, 1},
}

// Dirs are the subdirectories the tools are pointed at, which Prepare makes.
func Dirs() []string {
	var out []string
	for _, p := range parts {
		out = append(out, p.dir)
	}
	return out
}

// Cache is the shared caches' directory, with what bounds it.
type Cache struct {
	// Dir holds the caches, one subdirectory a tool.
	Dir string
	// Max is the most the caches may hold together, in bytes. Zero or less
	// leaves them as they are: they're off, and agents don't use them.
	Max func() int64
	// Room, when set, is how many more bytes the disk can take before it's at
	// its floor. Below it, Trim evicts until the disk is above it again.
	Room func() int64
	// Busy, when set, says an agent may be installing into or building with
	// the caches right now (a heavy run is under way). Trim leaves them
	// alone then, unless they hold more than BusyCeiling times Max: with
	// many agents there is always one at work, and waiting for none would
	// never trim.
	Busy func() bool
	// Owner, when set, is who the directories Prepare makes belong to: the
	// agents' user, when the daemon runs as root.
	Owner *[2]int
	// Logf, when set, is told what couldn't be evicted.
	Logf func(format string, args ...any)
	// Recent is how long a piece is left alone after it was last used, in
	// case it's being written now; DefaultRecent when zero.
	Recent time.Duration
	// now is time.Now, which tests replace.
	now func() time.Time

	mu    sync.Mutex // held through a walk
	size  atomic.Int64
	sized atomic.Bool
	// parts is what each tool's directory held when last measured.
	partsMu sync.Mutex
	held    []PartSize
}

// PartSize is how much one tool's directory holds, as Dirs names them.
type PartSize struct {
	Dir   string
	Bytes int64
}

// BusyCeiling is how far over Max the caches may grow while Busy says an
// agent is at work, before Trim evicts anyway.
const BusyCeiling = 1.5

// SupersededAfter is how long a browser version that a newer one of the same
// browser replaced is left unused before Trim drops it, whatever the caches
// hold: no project that moved to the newer one reads it.
const SupersededAfter = 7 * 24 * time.Hour

// DefaultRecent is how long Trim leaves a piece alone after it was last used:
// a download in progress is younger than that.
const DefaultRecent = 10 * time.Minute

// Prepare makes the tools' subdirectories, so that the first agent to mount
// the cache finds them, and each tool writes where it was told to.
func (c *Cache) Prepare() error {
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	if err := c.chown(c.Dir); err != nil {
		return err
	}
	for _, dir := range Dirs() {
		path := c.Dir
		for _, name := range strings.Split(dir, "/") {
			path = filepath.Join(path, name)
			if err := os.Mkdir(path, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
			if err := c.chown(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Cache) chown(path string) error {
	if c.Owner == nil {
		return nil
	}
	return os.Lchown(path, c.Owner[0], c.Owner[1])
}

// Size is how many bytes the caches held when last measured, measuring them
// first if they never were. It doesn't wait for a trim under way.
func (c *Cache) Size() int64 {
	if c.sized.Load() {
		return c.size.Load()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.sized.Load() {
		all := c.pieces()
		c.measuredParts(all)
		c.measured(sum(all))
	}
	return c.size.Load()
}

func (c *Cache) measured(n int64) {
	c.size.Store(n)
	c.sized.Store(true)
}

// Breakdown is what each tool's directory held when last measured, biggest
// first, empty ones left out.
func (c *Cache) Breakdown() []PartSize {
	c.Size()
	c.partsMu.Lock()
	defer c.partsMu.Unlock()
	return slices.Clone(c.held)
}

func (c *Cache) measuredParts(all []piece) {
	by := map[string]int64{}
	for _, p := range all {
		by[p.part] += p.size
	}
	var out []PartSize
	for dir, n := range by {
		if n > 0 {
			out = append(out, PartSize{Dir: dir, Bytes: n})
		}
	}
	slices.SortFunc(out, func(a, b PartSize) int {
		if a.Bytes != b.Bytes {
			return int(b.Bytes - a.Bytes)
		}
		return strings.Compare(a.Dir, b.Dir)
	})
	c.partsMu.Lock()
	c.held = out
	c.partsMu.Unlock()
}

// Trim measures the caches, and evicts what was used longest ago while they
// hold more than Max, to nine tenths of it so that the next few downloads
// don't need another trim, or while the disk is below its floor. It returns
// how much it evicted.
func (c *Cache) Trim() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	start := time.Now()
	all := c.pieces()
	walked := fmt.Sprintf("walked %d pieces in %s, %d unreadable", len(all), time.Since(start).Round(time.Millisecond), walkErrors.Load())
	size := sum(all)
	before := size
	c.measured(size)
	c.measuredParts(all)
	defer func() { c.measuredParts(all) }()
	defer func() { c.measured(size) }()
	limit := int64(0)
	if c.Max != nil {
		limit = c.Max()
	}
	if limit <= 0 {
		c.logf("package cache: off, holds %s (%s)", bytesWords(size), walked)
		return 0
	}
	if c.Busy != nil && c.Busy() && float64(size) <= float64(limit)*BusyCeiling {
		c.logf("package cache: skipped, an agent has a heavy run going; holds %s of %s (%s)", bytesWords(size), bytesWords(limit), walked)
		return 0
	}
	recent := c.Recent
	if recent == 0 {
		recent = DefaultRecent
	}
	now := c.clock()
	cutoff := now.Add(-recent)
	var freed int64
	evict := func(p piece) bool {
		if err := removeAll(p.path); err != nil {
			c.logf("package cache: evicting %s: %v", p.path, err)
			return false
		}
		size -= p.size
		freed += p.size
		for i := range all {
			if all[i].path == p.path {
				all = slices.Delete(all, i, i+1)
				break
			}
		}
		return true
	}
	// Browsers a newer version replaced, and nothing has read for a week.
	for _, p := range slices.Clone(all) {
		if p.superseded && p.used.Before(now.Add(-SupersededAfter)) {
			evict(p)
		}
	}
	target := size
	if size > limit {
		target = limit / 10 * 9
	}
	if c.Room != nil {
		if room := c.Room(); room < 0 {
			target = min(target, size+room)
		}
	}
	if target >= size {
		c.logf("package cache: holds %s of %s, nothing to evict; freed %s (%s)", bytesWords(size), bytesWords(limit), bytesWords(freed), walked)
		return freed
	}
	// Replaced browsers go before anything else that's as old.
	slices.SortStableFunc(all, func(a, b piece) int {
		if a.superseded != b.superseded && !a.used.Equal(never) && !b.used.Equal(never) {
			if a.superseded {
				return -1
			}
			return 1
		}
		return a.used.Compare(b.used)
	})
	for _, p := range slices.Clone(all) {
		if size <= target || p.used.After(cutoff) {
			break
		}
		evict(p)
	}
	line := fmt.Sprintf("package cache: held %s of %s, now %s, evicted %s (%s)", bytesWords(before), bytesWords(limit), bytesWords(size), bytesWords(freed), walked)
	if size > target && len(all) > 0 {
		// It couldn't get down: say what stopped it.
		oldest := all[0]
		line += fmt.Sprintf("; short of %s, the oldest piece left, %s, was used %s ago", bytesWords(target), oldest.path, now.Sub(oldest.used).Round(time.Second))
	}
	c.logf("%s", line)
	return freed
}

// bytesWords is a size for the log.
func bytesWords(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for rest := n / unit; rest >= unit; rest /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// RemoveAll removes a file or a directory, read-only directories included:
// Go's module cache makes its module trees read-only, and a plain os.RemoveAll
// fails on them with "permission denied".
func RemoveAll(path string) error { return removeAll(path) }

// Clear empties every cache, and makes their directories again.
func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.Dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var errs []error
	for _, e := range entries {
		errs = append(errs, removeAll(filepath.Join(c.Dir, e.Name())))
	}
	all := c.pieces()
	c.measured(sum(all))
	c.measuredParts(all)
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return c.Prepare()
}

func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Cache) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// piece is what one eviction removes: a file or a directory, how much it
// holds, and when anything in it was last read or written.
type piece struct {
	path string
	size int64
	used time.Time
	// part is the tool's directory it's in; "" for what's in none.
	part string
	// superseded is a browser version a newer one of the same browser replaced.
	superseded bool
}

func sum(all []piece) int64 {
	var n int64
	for _, p := range all {
		n += p.size
	}
	return n
}

// pieces walks the caches, every byte in them counted in exactly one piece.
// What's in Dir but in no part is counted in one that's never evicted.
// walkErrors counts what a walk of the caches couldn't read, for the log; the
// walks are one at a time, under Cache.mu.
var walkErrors atomic.Int64

func (c *Cache) pieces() []piece {
	walkErrors.Store(0)
	var out []piece
	roots := map[string]bool{}
	for _, p := range parts {
		roots[filepath.Join(c.Dir, p.dir)] = true
	}
	for _, p := range parts {
		root := filepath.Join(c.Dir, p.dir)
		got := partPieces(root, p.unit, func(path string) bool { return path != root && roots[path] })
		for i := range got {
			got[i].part = p.dir
		}
		if p.dir == Playwright {
			markSuperseded(got)
		}
		out = append(out, got...)
	}
	stray := piece{used: never}
	_ = filepath.WalkDir(c.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			walkErrors.Add(1)
			return nil
		}
		if d.IsDir() && roots[path] {
			return fs.SkipDir
		}
		if info, err := d.Info(); err == nil && !d.IsDir() {
			stray.size += diskSize(info)
		}
		return nil
	})
	if stray.size > 0 {
		out = append(out, stray)
	}
	return out
}

// markSuperseded marks each browser version's directory (chromium-1187) when
// the cache has a newer one of the same browser (chromium-1194). Which
// versions the pinned tools use isn't written down anywhere but in the
// browsers' own directories; the newest of each is what the most recent
// Playwright wanted.
func markSuperseded(pieces []piece) {
	newest := map[string]int{}
	rev := func(p piece) (string, int, bool) {
		name := filepath.Base(p.path)
		i := strings.LastIndex(name, "-")
		if i <= 0 {
			return "", 0, false
		}
		n, err := strconv.Atoi(name[i+1:])
		return name[:i], n, err == nil
	}
	for _, p := range pieces {
		if name, n, ok := rev(p); ok && n > newest[name] {
			newest[name] = n
		}
	}
	for i, p := range pieces {
		if name, n, ok := rev(p); ok && n < newest[name] {
			pieces[i].superseded = true
		}
	}
}

// never is when a piece that's never evicted was last used: after
// everything else, so Trim stops before it.
var never = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// partPieces is one part's pieces. skip says which directories inside it
// belong to another part.
func partPieces(root string, u unit, skip func(string) bool) []piece {
	if _, err := os.Lstat(root); err != nil {
		return nil
	}
	var out []piece
	switch u {
	case eachFile:
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				walkErrors.Add(1)
				return nil
			}
			if d.IsDir() {
				if skip(path) {
					return fs.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				walkErrors.Add(1)
				return nil
			}
			p := piece{path: path, size: diskSize(info), used: lastUsed(info)}
			if keep(d.Name()) {
				p.used = never
			}
			out = append(out, p)
			return nil
		})
	case whole:
		p := tree(root, skip)
		p.path = root
		if p.size > 0 {
			out = append(out, p)
		}
	default:
		var walk func(dir string, depth int)
		walk = func(dir string, depth int) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				walkErrors.Add(1)
				return
			}
			for _, e := range entries {
				path := filepath.Join(dir, e.Name())
				if e.IsDir() && skip(path) {
					continue
				}
				if e.IsDir() && depth > 1 {
					walk(path, depth-1)
					continue
				}
				p := tree(path, skip)
				p.path = path
				out = append(out, p)
			}
		}
		walk(root, int(u))
	}
	return out
}

// tree is a file's or a directory's size and its newest use.
func tree(root string, skip func(string) bool) piece {
	var p piece
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			walkErrors.Add(1)
			return nil
		}
		if d.IsDir() && path != root && skip(path) {
			return fs.SkipDir
		}
		// Only files: walking a directory, as this does, is reading it,
		// which moves its access time.
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			walkErrors.Add(1)
			return nil
		}
		p.size += diskSize(info)
		if used := lastUsed(info); used.After(p.used) {
			p.used = used
		}
		return nil
	})
	return p
}

// keep is a file Trim never evicts: an index whose loss would cost a tool
// more than the files it indexes. pnpm 11 keeps its store's in SQLite.
func keep(name string) bool {
	return strings.HasPrefix(name, "index.db")
}

// removeAll removes a file or a directory, read-only directories included:
// Go's module cache makes its module trees read-only.
func removeAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	return os.RemoveAll(path)
}
