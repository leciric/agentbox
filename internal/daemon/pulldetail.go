package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/state"

	"golang.org/x/sync/singleflight"
)

// The app's pull request view: one pull request read fresh, its files, each
// file's diff, and the pictures in its description — everything the user
// would otherwise open GitHub for. Unlike the list (pulls.go) these are read
// when asked, since only one pull request is open at a time.

// pullFilesTTL is how long a pull request's files, diffs included, are kept
// for the diffs the view asks for one by one as files are expanded. Listing
// the files reads them again.
const pullFilesTTL = 10 * time.Minute

// pullFilesKept bounds how many pull requests' files are kept.
const pullFilesKept = 16

// pullFiles keeps the files of the pull requests the app has recently opened,
// by repository and number: GitHub has no endpoint for one file's diff, only
// the whole list, so each diff is served from the list the files came from.
type pullFiles struct {
	mu   sync.Mutex
	byPR map[string]pullFilesEntry
}

type pullFilesEntry struct {
	files     []github.PullFile
	truncated bool
	at        time.Time
}

func pullFilesKey(repo github.Repo, number int) string {
	return repo.String() + "#" + strconv.Itoa(number)
}

func (c *pullFiles) get(key string, now time.Time) (pullFilesEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byPR[key]
	return e, ok && now.Sub(e.at) < pullFilesTTL
}

func (c *pullFiles) put(key string, e pullFilesEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byPR == nil {
		c.byPR = map[string]pullFilesEntry{}
	}
	if _, ok := c.byPR[key]; !ok && len(c.byPR) >= pullFilesKept {
		oldest := ""
		for k, v := range c.byPR {
			if oldest == "" || v.at.Before(c.byPR[oldest].at) {
				oldest = k
			}
		}
		delete(c.byPR, oldest)
	}
	c.byPR[key] = e
}

// pullDetailTTL is how long a pull request read for the view is served again
// without asking GitHub: long enough that the read the app starts when the
// pointer reaches a row is the one the click then shows, short enough that
// checks finishing behind it are seen on the next open. A list read since that
// shows the pull request moved — a new head, a later update — overrides it,
// and so does a merge or a label edit made through the app.
const pullDetailTTL = 30 * time.Second

// pullDetails keeps the pull requests the view read recently, and shares a
// read in flight: the hover's read and the click's are one GitHub call.
type pullDetails struct {
	mu   sync.Mutex
	byPR map[string]pullDetailEntry
	// gen counts drops, so a read that started before one neither stores
	// what it found nor is joined by a read after it.
	gen     int
	flights singleflight.Group
}

type pullDetailEntry struct {
	pr github.PullRequestDetail
	at time.Time
}

// get is the pull request kept for key while it's fresh and the list hasn't
// seen it move since; listed is the list's row for it, nil when it has none.
func (c *pullDetails) get(key string, now time.Time, listed *api.PullRequest) (github.PullRequestDetail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byPR[key]
	if !ok || now.Sub(e.at) >= pullDetailTTL {
		return github.PullRequestDetail{}, false
	}
	if listed != nil && (listed.HeadSHA != e.pr.HeadSHA || listed.State != e.pr.State ||
		listed.UpdatedAt != nil && e.pr.UpdatedAt != nil && listed.UpdatedAt.After(*e.pr.UpdatedAt)) {
		return github.PullRequestDetail{}, false
	}
	return e.pr, true
}

func (c *pullDetails) generation() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// put keeps pr for key unless a drop came after gen.
func (c *pullDetails) put(key string, gen int, e pullDetailEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	if c.byPR == nil {
		c.byPR = map[string]pullDetailEntry{}
	}
	if _, ok := c.byPR[key]; !ok && len(c.byPR) >= pullFilesKept {
		oldest := ""
		for k, v := range c.byPR {
			if oldest == "" || v.at.Before(c.byPR[oldest].at) {
				oldest = k
			}
		}
		delete(c.byPR, oldest)
	}
	c.byPR[key] = e
}

// drop forgets key, after the app changed it on GitHub.
func (c *pullDetails) drop(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byPR, key)
	c.gen++
}

// pullRequestOf resolves the project, its GitHub client and repository, and
// the pull request number from a request's path.
func (s *Server) pullRequestOf(r *http.Request) (state.Project, github.Client, github.Repo, int, error) {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return p, github.Client{}, github.Repo{}, 0, err
	}
	number := 0
	if n := r.PathValue("number"); n != "" {
		if number, err = strconv.Atoi(n); err != nil || number <= 0 {
			return p, github.Client{}, github.Repo{}, 0, fmt.Errorf("invalid pull request number %q", n)
		}
	}
	client, repo, err := s.githubFor(p)
	if err != nil {
		if errors.Is(err, errNoGitHubToken) {
			return p, client, repo, 0, errors.New("this project has no GitHub token: add one with agentbox auth github")
		}
		return p, client, repo, 0, err
	}
	return p, client, repo, number, nil
}

// pullRequestDetail reads one pull request, with its description, labels and
// check runs, marked with the agent behind it like the list's. It is read
// fresh unless the view read it moments ago (pullDetailTTL).
func (s *Server) pullRequestDetail(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, client, repo, number, err := s.pullRequestOf(r)
	if err != nil {
		return err
	}
	pr, err := s.readPullDetail(ctx, client, repo, number)
	if err != nil {
		return err
	}
	out := api.PullRequestDetail{
		PullRequest:  toAPIPullRequests([]github.PullRequest{pr.PullRequest})[0],
		Body:         pr.Body,
		ChangedFiles: pr.ChangedFiles,
		CheckRuns:    []api.PullCheck{},
	}
	for _, c := range pr.CheckRuns {
		out.CheckRuns = append(out.CheckRuns, api.PullCheck{Name: c.Name, Status: c.Status, Conclusion: c.Conclusion, URL: c.URL})
	}
	s.prWatch.overlay(p.Name, &out.PullRequest)
	out.Agent = agentOfPR(s.agentsOf(ctx, p), pr.HeadBranch, pr.HeadSHA)
	return writeJSON(w, http.StatusOK, out)
}

// readPullDetail is the pull request the view shows, from pullDetails or from
// GitHub. The list's row for it, when the list cache has one, says where its
// head was, so its check runs are read alongside it.
func (s *Server) readPullDetail(ctx context.Context, client github.Client, repo github.Repo, number int) (github.PullRequestDetail, error) {
	key := pullFilesKey(repo, number)
	listed := s.listedPull(repo, number)
	if pr, ok := s.pullDetails.get(key, time.Now(), listed); ok {
		return pr, nil
	}
	head := ""
	if listed != nil {
		head = listed.HeadSHA
	}
	gen := s.pullDetails.generation()
	// Shared, so not cut short when the request that started it goes.
	shared := context.WithoutCancel(ctx)
	v, err, _ := s.pullDetails.flights.Do(key+"@"+strconv.Itoa(gen), func() (any, error) {
		pr, err := client.PullRequestDetailAt(shared, repo, number, head)
		if err == nil {
			s.pullDetails.put(key, gen, pullDetailEntry{pr: pr, at: time.Now()})
		}
		return pr, err
	})
	if err != nil {
		return github.PullRequestDetail{}, err
	}
	return v.(github.PullRequestDetail), nil
}

// listedPull is the list cache's row for a pull request, nil when it has none.
func (s *Server) listedPull(repo github.Repo, number int) *api.PullRequest {
	entry, _ := s.pulls.state(repo.String())
	for i := range entry.prs {
		if entry.prs[i].Number == number {
			pr := entry.prs[i]
			return &pr
		}
	}
	return nil
}

// pullRequestFiles lists the files a pull request changes, without their
// diffs, and keeps the diffs for pullFileDiff. A listing younger than
// pullDetailTTL is served again.
func (s *Server) pullRequestFiles(w http.ResponseWriter, r *http.Request) error {
	_, client, repo, number, err := s.pullRequestOf(r)
	if err != nil {
		return err
	}
	// The files read moments ago, by the read the app starts when the
	// pointer reaches a row, are the ones to show; older ones are read again.
	key := pullFilesKey(repo, number)
	e, ok := s.pullFiles.get(key, time.Now())
	if !ok || time.Since(e.at) >= pullDetailTTL {
		v, err, _ := s.pullDetails.flights.Do("files:"+key, func() (any, error) {
			return s.readPullFiles(context.WithoutCancel(r.Context()), client, repo, number)
		})
		if err != nil {
			return err
		}
		e = v.(pullFilesEntry)
	}
	out := api.PullRequestFiles{Files: make([]api.PullFile, len(e.files)), Truncated: e.truncated}
	for i, f := range e.files {
		out.Files[i] = api.PullFile{
			Path: f.Path, PreviousPath: f.PreviousPath, Status: f.Status,
			Additions: f.Additions, Deletions: f.Deletions, HasDiff: f.Patch != "",
		}
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) readPullFiles(ctx context.Context, client github.Client, repo github.Repo, number int) (pullFilesEntry, error) {
	files, truncated, err := client.PullRequestFiles(ctx, repo, number)
	if err != nil {
		return pullFilesEntry{}, err
	}
	e := pullFilesEntry{files: files, truncated: truncated, at: time.Now()}
	s.pullFiles.put(pullFilesKey(repo, number), e)
	return e, nil
}

// pullFileDiff is one file's diff, from the files pullRequestFiles read, or
// read again when those have gone.
func (s *Server) pullFileDiff(w http.ResponseWriter, r *http.Request) error {
	_, client, repo, number, err := s.pullRequestOf(r)
	if err != nil {
		return err
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		return errors.New("path is required")
	}
	e, ok := s.pullFiles.get(pullFilesKey(repo, number), time.Now())
	if !ok {
		if e, err = s.readPullFiles(r.Context(), client, repo, number); err != nil {
			return err
		}
	}
	for _, f := range e.files {
		if f.Path == path {
			return writeJSON(w, http.StatusOK, api.PullFileDiff{Path: f.Path, Patch: f.Patch})
		}
	}
	return fmt.Errorf("pull request #%d changes no %s: %w", number, path, state.ErrNotFound)
}

// pullRequestImage serves a picture from a pull request's description, read
// with the project's GitHub account, which a private repository's
// attachments need; the token itself never leaves the daemon. Only GitHub's
// own hosts are read (github.Client.Image).
func (s *Server) pullRequestImage(w http.ResponseWriter, r *http.Request) error {
	_, client, repo, _, err := s.pullRequestOf(r)
	if err != nil {
		return err
	}
	res, err := client.Image(r.Context(), repo, r.URL.Query().Get("src"))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
	if res.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(res.ContentLength, 10))
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// An SVG is an image here, never a page that runs.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, res.Body)
	return nil
}
