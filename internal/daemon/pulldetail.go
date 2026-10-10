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

// pullRequestDetail reads one pull request fresh, with its description,
// labels and check runs, marked with the agent behind it like the list's.
func (s *Server) pullRequestDetail(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, client, repo, number, err := s.pullRequestOf(r)
	if err != nil {
		return err
	}
	pr, err := client.PullRequestDetail(ctx, repo, number)
	if err != nil {
		return err
	}
	out := api.PullRequestDetail{
		PullRequest: toAPIPullRequests([]github.PullRequest{pr.PullRequest})[0],
		Body:        pr.Body,
		Labels:      []api.PullLabel{},
		CheckRuns:   []api.PullCheck{},
	}
	for _, l := range pr.Labels {
		out.Labels = append(out.Labels, api.PullLabel{Name: l.Name, Color: l.Color, Description: l.Description})
	}
	for _, c := range pr.CheckRuns {
		out.CheckRuns = append(out.CheckRuns, api.PullCheck{Name: c.Name, Status: c.Status, Conclusion: c.Conclusion, URL: c.URL})
	}
	s.prWatch.overlay(p.Name, &out.PullRequest)
	out.Agent = agentOfCommit(s.agentsOf(ctx, p), pr.HeadSHA)
	return writeJSON(w, http.StatusOK, out)
}

// pullRequestFiles lists the files a pull request changes, without their
// diffs, and keeps the diffs for pullFileDiff.
func (s *Server) pullRequestFiles(w http.ResponseWriter, r *http.Request) error {
	_, client, repo, number, err := s.pullRequestOf(r)
	if err != nil {
		return err
	}
	e, err := s.readPullFiles(r.Context(), client, repo, number)
	if err != nil {
		return err
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
