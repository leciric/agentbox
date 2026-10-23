package gitrepo

import "testing"

// SetGitHubURL has Fetch offer its GitHub token to url, a test's server,
// until the test ends.
func SetGitHubURL(t *testing.T, url string) {
	old := githubURL
	githubURL = url
	t.Cleanup(func() { githubURL = old })
}
