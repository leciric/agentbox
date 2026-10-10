package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// PullRequestDetail is one pull request as the app's pull request view shows
// it: what the list carries, and its description, labels and every check run
// on its head, which the list leaves out.
type PullRequestDetail struct {
	PullRequest
	Body      string
	Labels    []Label
	CheckRuns []CheckRun
}

// Label is a label on a pull request; Color is GitHub's hex, without "#".
type Label struct {
	Name, Color, Description string
}

// CheckRun is one check on a commit. Status is queued, in_progress or
// completed; Conclusion, once it has completed, is success, failure, neutral,
// cancelled, skipped, timed_out or action_required.
type CheckRun struct {
	Name, Status, Conclusion, URL string
}

// PullFile is one file a pull request changes. Patch is its unified diff,
// without the file header; GitHub leaves it out for a binary file and for a
// diff too big to send.
type PullFile struct {
	Path, PreviousPath, Status string // Status: added, removed, modified, renamed, copied, changed, unchanged
	Additions, Deletions       int
	Patch                      string
}

// PullRequestDetail reads one pull request with its description, labels and
// check runs.
func (c Client) PullRequestDetail(ctx context.Context, repo Repo, number int) (PullRequestDetail, error) {
	var raw struct {
		rawPR
		Body   *string `json:"body"`
		Labels []struct {
			Name        string `json:"name"`
			Color       string `json:"color"`
			Description string `json:"description"`
		} `json:"labels"`
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(repo.Owner), url.PathEscape(repo.Name), number)
	if err := c.get(ctx, path, &raw); err != nil {
		return PullRequestDetail{}, err
	}
	out := PullRequestDetail{PullRequest: raw.pullRequest()}
	if raw.Body != nil {
		out.Body = *raw.Body
	}
	for _, l := range raw.Labels {
		out.Labels = append(out.Labels, Label{Name: l.Name, Color: l.Color, Description: l.Description})
	}
	if raw.Head.SHA != "" {
		// Like checks, never a reason to fail: a pull request whose checks
		// couldn't be read is still worth showing.
		if runs, err := c.checkRuns(ctx, repo, raw.Head.SHA); err == nil {
			out.CheckRuns = runs
			out.Checks = foldChecks(runs)
		}
	}
	return out, nil
}

// pullFilesPages bounds how many pages of 100 files PullRequestFiles reads:
// GitHub itself stops at 3000.
const pullFilesPages = 30

// PullRequestFiles lists the files a pull request changes, with their diffs,
// in the order GitHub gives them. Truncated is set when there were more than
// it read.
func (c Client) PullRequestFiles(ctx context.Context, repo Repo, number int) (files []PullFile, truncated bool, err error) {
	files = []PullFile{}
	for page := 1; page <= pullFilesPages; page++ {
		var list []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
			Status           string `json:"status"`
			Additions        int    `json:"additions"`
			Deletions        int    `json:"deletions"`
			Patch            string `json:"patch"`
		}
		path := fmt.Sprintf("/repos/%s/%s/pulls/%d/files?per_page=100&page=%d", url.PathEscape(repo.Owner), url.PathEscape(repo.Name), number, page)
		if err := c.get(ctx, path, &list); err != nil {
			return nil, false, err
		}
		for _, f := range list {
			files = append(files, PullFile{
				Path: f.Filename, PreviousPath: f.PreviousFilename, Status: f.Status,
				Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch,
			})
		}
		if len(list) < 100 {
			return files, false, nil
		}
	}
	return files, true, nil
}

// checkRuns reads every check run on a commit, up to GitHub's page of 100.
func (c Client) checkRuns(ctx context.Context, repo Repo, sha string) ([]CheckRun, error) {
	var runs struct {
		Runs []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
		} `json:"check_runs"`
	}
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=100", url.PathEscape(repo.Owner), url.PathEscape(repo.Name), url.PathEscape(sha))
	if err := c.get(ctx, path, &runs); err != nil {
		return nil, err
	}
	out := make([]CheckRun, 0, len(runs.Runs))
	for _, r := range runs.Runs {
		out = append(out, CheckRun{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, URL: r.HTMLURL})
	}
	return out, nil
}

// foldChecks is checks' one word for a set of check runs.
func foldChecks(runs []CheckRun) string {
	if len(runs) == 0 {
		return ""
	}
	state := "passing"
	for _, run := range runs {
		switch {
		case run.Status != "completed":
			return "pending"
		case run.Conclusion == "failure", run.Conclusion == "timed_out", run.Conclusion == "cancelled":
			state = "failing"
		}
	}
	return state
}

// ErrNotAGitHubImage is an image Image won't fetch: anything not on one of
// GitHub's own hosts.
var ErrNotAGitHubImage = errors.New("not an image GitHub hosts")

// maxImageBytes bounds an image Image hands back.
const maxImageBytes = 32 << 20

// Image fetches a picture a pull request's description shows. Pictures
// attached to a private repository's pull requests (github.com/user-attachments,
// private-user-images.githubusercontent.com) need the account's token, which
// a browser would have as a GitHub session cookie and the app has nowhere;
// so they are fetched here, and the token never leaves for anywhere but
// GitHub itself. Only GitHub's hosts are fetched at all — the description is
// anyone's text, and this must not be a way to make the daemon read any URL.
// A private-user-images link carries a signed token that expires in minutes,
// so it is read as the attachment it names instead.
//
// The caller closes the response's body. Its Content-Type is an image's.
func (c Client) Image(ctx context.Context, repo Repo, src string) (*http.Response, error) {
	u, err := imageURL(repo, src)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" && tokenHost(u.Host) {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := http.Client{Timeout: 30 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	// An attachment redirects to a signed address on a storage host: the
	// token stays behind, and nothing is followed off HTTPS.
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if next.URL.Scheme != "https" {
			return fmt.Errorf("redirected off HTTPS to %s", next.URL.Host)
		}
		if !tokenHost(next.URL.Host) {
			next.Header.Del("Authorization")
		}
		return nil
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		defer func() { _ = res.Body.Close() }()
		return nil, apiError(u.Path, res)
	}
	if res.ContentLength > maxImageBytes {
		_ = res.Body.Close()
		return nil, fmt.Errorf("image is %d bytes, more than %d", res.ContentLength, maxImageBytes)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
		_ = res.Body.Close()
		return nil, fmt.Errorf("%s is %q, not an image", u.Path, res.Header.Get("Content-Type"))
	}
	res.Body = readCloser{io.LimitReader(res.Body, maxImageBytes), res.Body}
	return res, nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

// privateImage is the attachment's UUID in a private-user-images path,
// /<user id>/<asset id>-<uuid>.<ext>.
var privateImage = regexp.MustCompile(`^/\d+/\d+-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.\w+$`)

// imageURL is where Image reads src from, or ErrNotAGitHubImage. On
// github.com only attachments and the project's own repository are read: the
// token is sent there.
func imageURL(repo Repo, src string) (*url.URL, error) {
	u, err := url.Parse(src)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return nil, fmt.Errorf("%w: %s", ErrNotAGitHubImage, src)
	}
	own := "/" + strings.ToLower(repo.Owner+"/"+repo.Name) + "/"
	switch host := strings.ToLower(u.Host); host {
	case "github.com":
		if strings.HasPrefix(u.Path, "/user-attachments/") || strings.HasPrefix(strings.ToLower(u.Path), own) {
			return u, nil
		}
	case "raw.githubusercontent.com":
		if strings.HasPrefix(strings.ToLower(u.Path), own) {
			return u, nil
		}
	case "private-user-images.githubusercontent.com":
		if m := privateImage.FindStringSubmatch(u.Path); m != nil {
			return &url.URL{Scheme: "https", Host: "github.com", Path: "/user-attachments/assets/" + m[1]}, nil
		}
		return u, nil // signed, so it works without the token until it expires
	case "user-images.githubusercontent.com", "camo.githubusercontent.com", "avatars.githubusercontent.com", "objects.githubusercontent.com":
		return u, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotAGitHubImage, src)
}

// tokenHost is a host the account's token may be sent to.
func tokenHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || host == "raw.githubusercontent.com"
}
