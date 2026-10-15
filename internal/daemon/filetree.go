package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/worktreefs"
)

// The file tree and viewer beside a chat. The app lists one folder at a time,
// as you open it, and the daemon watches exactly the folders it listed with
// inotify (fsnotify), so a big repository costs a watch per folder you opened,
// not one per folder it has. A change is told to the app as an EventFiles
// naming the folders and files that moved, a quarter of a second after the
// last one, and the app reads again only those. A worktree nobody has asked
// about for watchIdle stops being watched.
//
// T3Code, which this follows (apps/web/src/components/files/
// useDirectoryEntries.ts), refreshes its tree after its own edits and on a
// button; here agents edit by themselves, so the tree follows the disk.

const (
	// watchIdle is how long a worktree is watched after the app last asked
	// about it. The app asks for its status every half minute while the panel
	// is open, which keeps it.
	watchIdle = 2 * time.Minute
	// watchDirsLimit caps the folders watched in one worktree.
	watchDirsLimit = 4096
	// watchSettle is how long changes gather before the app is told.
	watchSettle = 250 * time.Millisecond
	// watchPendingLimit caps the files one change names; past it the app is
	// told the folders only, and reads open files again from those.
	watchPendingLimit = 500
	// statusTTL is how long a worktree's status is reused when nothing moved.
	statusTTL = 20 * time.Second
)

type fileWatches struct {
	mu      sync.Mutex
	roots   map[string]*rootWatch // by chat ref
	publish func(api.FilesChange)
	now     func() time.Time
}

type rootWatch struct {
	ref, root string
	w         *fsnotify.Watcher // nil when watching failed: the tree still works, without live updates
	dirs      map[string]bool
	used      time.Time
	gen       uint64 // moves with every change, for the status cache
	idle      *time.Timer

	status    *api.FileStatus
	statusGen uint64
	statusAt  time.Time

	pendingDirs  map[string]bool
	pendingFiles map[string]bool
	settle       *time.Timer
}

func newFileWatches(publish func(api.FilesChange)) *fileWatches {
	return &fileWatches{roots: map[string]*rootWatch{}, publish: publish, now: time.Now}
}

// get finds or starts ref's watch, and counts this as a use.
func (f *fileWatches) get(ref, root string) *rootWatch {
	rw := f.roots[ref]
	if rw != nil && rw.root != root {
		f.closeLocked(rw) // the lead's worktree replaced the main checkout
		rw = nil
	}
	if rw == nil {
		rw = &rootWatch{ref: ref, root: root, dirs: map[string]bool{}, pendingDirs: map[string]bool{}, pendingFiles: map[string]bool{}}
		if w, err := fsnotify.NewWatcher(); err == nil {
			rw.w = w
			go f.loop(rw)
		}
		rw.idle = time.AfterFunc(watchIdle, func() { f.expire(rw) })
		f.roots[ref] = rw
	}
	rw.used = f.now()
	return rw
}

// watch adds a folder the app just listed.
func (f *fileWatches) watch(ref, root, dir string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rw := f.get(ref, root)
	if rw.w == nil || rw.dirs[dir] || len(rw.dirs) >= watchDirsLimit {
		return
	}
	if err := rw.w.Add(filepath.Join(root, filepath.FromSlash(dir))); err == nil {
		rw.dirs[dir] = true
	}
}

func (f *fileWatches) expire(rw *rootWatch) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.roots[rw.ref] != rw {
		return
	}
	if left := watchIdle - f.now().Sub(rw.used); left > 0 {
		rw.idle.Reset(left)
		return
	}
	f.closeLocked(rw)
}

// closeAll stops every watch, as the daemon stops.
func (f *fileWatches) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rw := range f.roots {
		f.closeLocked(rw)
	}
}

func (f *fileWatches) closeLocked(rw *rootWatch) {
	delete(f.roots, rw.ref)
	rw.idle.Stop()
	if rw.settle != nil {
		rw.settle.Stop()
	}
	if rw.w != nil {
		_ = rw.w.Close()
	}
}

func (f *fileWatches) loop(rw *rootWatch) {
	for {
		select {
		case ev, ok := <-rw.w.Events:
			if !ok {
				return
			}
			f.changed(rw, ev)
		case _, ok := <-rw.w.Errors:
			if !ok {
				return
			}
		}
	}
}

func (f *fileWatches) changed(rw *rootWatch, ev fsnotify.Event) {
	rel, err := worktreefs.Rel(rw.root, ev.Name)
	if err != nil || rel == "" {
		return // under .git
	}
	dir := relDir(rel)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.roots[rw.ref] != rw {
		return
	}
	rw.gen++
	if ev.Has(fsnotify.Create) || ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		rw.pendingDirs[dir] = true
		if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
			delete(rw.dirs, rel) // a watched folder that went: inotify dropped it
		}
	}
	if ev.Op != fsnotify.Chmod {
		if len(rw.pendingFiles) < watchPendingLimit {
			rw.pendingFiles[rel] = true
		} else {
			rw.pendingDirs[dir] = true
		}
	}
	if rw.settle == nil {
		rw.settle = time.AfterFunc(watchSettle, func() { f.flush(rw) })
	} else {
		rw.settle.Reset(watchSettle)
	}
}

func (f *fileWatches) flush(rw *rootWatch) {
	f.mu.Lock()
	change := api.FilesChange{Ref: rw.ref, Dirs: sortedKeys(rw.pendingDirs), Files: sortedKeys(rw.pendingFiles)}
	rw.pendingDirs, rw.pendingFiles = map[string]bool{}, map[string]bool{}
	live := f.roots[rw.ref] == rw
	f.mu.Unlock()
	if live && (len(change.Dirs) > 0 || len(change.Files) > 0) {
		f.publish(change)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// touch says something may have moved that no watch saw, so the next status
// is read again.
func (f *fileWatches) touch(ref, root string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.get(ref, root).gen++
}

// status is ref's status, from the cache when nothing moved since and it is
// recent; compute reads it again.
func (f *fileWatches) status(ref, root string, compute func() (api.FileStatus, error)) (api.FileStatus, error) {
	f.mu.Lock()
	rw := f.get(ref, root)
	if rw.status != nil && rw.statusGen == rw.gen && f.now().Sub(rw.statusAt) < statusTTL {
		st := *rw.status
		f.mu.Unlock()
		return st, nil
	}
	gen := rw.gen
	f.mu.Unlock()
	st, err := compute()
	if err != nil {
		return st, err
	}
	f.mu.Lock()
	rw.status, rw.statusGen, rw.statusAt = &st, gen, f.now()
	f.mu.Unlock()
	return st, nil
}

// fileRoot is the folder a chat's file tree shows: an agent's worktree, or
// the project's lead's, or before the lead has one, the main checkout its
// first message will stand on. branch is what its changes are measured
// against: the branch the agent was made from; the lead's are its own HEAD.
func (s *Server) fileRoot(r *http.Request, from agentFrom) (a state.Agent, root, branch string, err error) {
	a, err = from(r)
	if err != nil {
		return a, "", "", err
	}
	if a.IsHome() {
		return a, "", "", errors.New("the Home chat has no repository")
	}
	if a.Worktree != "" {
		if !a.IsLead() {
			branch = a.BaseRef
		}
		return a, a.Worktree, branch, nil
	}
	p, err := s.store.Project(r.Context(), a.Project)
	if err != nil {
		return a, "", "", err
	}
	return a, p.Root, "", nil
}

// fileErr makes a missing path a 404.
func fileErr(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %v", state.ErrNotFound, err)
	}
	return err
}

func (s *Server) listFolder(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, root, _, err := s.fileRoot(r, from)
		if err != nil {
			return err
		}
		q := r.URL.Query()
		list, err := worktreefs.List(root, q.Get("path"), q.Get("ignored") == "1")
		if err != nil {
			return fileErr(err)
		}
		s.fileWatches.watch(a.Ref(), root, list.Path)
		return writeJSON(w, http.StatusOK, list)
	}
}

func (s *Server) fileStatus(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, root, branch, err := s.fileRoot(r, from)
		if err != nil {
			return err
		}
		// fresh: the app saw a tool call change files, maybe in a folder
		// nobody watches.
		if r.URL.Query().Get("fresh") == "1" {
			s.fileWatches.touch(a.Ref(), root)
		}
		st, err := s.worktreeStatus(a, root, branch)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, st)
	}
}

func (s *Server) worktreeStatus(a state.Agent, root, branch string) (api.FileStatus, error) {
	return s.fileWatches.status(a.Ref(), root, func() (api.FileStatus, error) {
		commit, name := worktreefs.Base(root, branch)
		changes, truncated, err := worktreefs.Status(root, commit)
		return api.FileStatus{Base: name, Changes: changes, Truncated: truncated}, err
	})
}

func (s *Server) readFile(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, root, branch, err := s.fileRoot(r, from)
		if err != nil {
			return err
		}
		q := r.URL.Query()
		content, err := worktreefs.Read(root, q.Get("path"), q.Get("full") == "1")
		if err != nil {
			return fileErr(err)
		}
		if q.Get("base") == "1" && !content.Binary && !content.TooLarge && (content.Image == "" || content.Text != "") {
			if st, err := s.worktreeStatus(a, root, branch); err == nil && st.Base != "" {
				content.Base = st.Base
				content.Status = statusOf(st.Changes, content.Path)
				if content.Status == api.FileModified {
					commit, _ := worktreefs.Base(root, branch)
					content.BaseText, content.BaseMissing, _ = worktreefs.BaseText(root, commit, content.Path)
				}
			}
		}
		return writeJSON(w, http.StatusOK, content)
	}
}

// statusOf is a path's status, its own or an untracked folder's it is in.
func statusOf(changes map[string]string, p string) string {
	if st, ok := changes[p]; ok {
		return st
	}
	for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if changes[dir+"/"] == api.FileUntracked {
			return api.FileUntracked
		}
	}
	return ""
}

// relDir is p's folder, "" at the root.
func relDir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}
