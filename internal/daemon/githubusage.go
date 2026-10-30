package daemon

import (
	"net/http"

	"agentbox/internal/api"
	"agentbox/internal/github"
)

// githubUsage answers GET /v1/github/usage, `agentbox github usage`: every
// request the daemon made of GitHub since it started, by token, caller and
// endpoint, with what GitHub last said is left. Each token is named by the
// stored account it is, when one is.
func (s *Server) githubUsage(w http.ResponseWriter, _ *http.Request) error {
	type named struct{ account, login string }
	names := map[string]named{}
	creds := s.manager(nil).Creds
	if accounts, err := creds.GitHubAccounts(); err == nil {
		for _, a := range accounts {
			if token, err := creds.GitHubToken(a.Name); err == nil && token != "" {
				names[github.Fingerprint(token)] = named{a.Name, a.Login}
			}
		}
	}
	out := api.GitHubUsage{Accounts: []api.GitHubAccountUsage{}}
	for _, u := range github.Usage() {
		one := api.GitHubAccountUsage{Token: u.Token, Account: names[u.Token].account, Login: names[u.Token].login, Since: u.Since,
			Rates: []api.GitHubRate{}, Calls: []api.GitHubCallCount{}}
		for _, r := range u.Rates {
			rate := api.GitHubRate{Resource: r.Resource, Limit: r.Limit, Remaining: r.Remaining, Used: r.Used, Reset: r.Reset, At: r.At, Ours: r.Ours, Others: r.Others}
			if !r.PausedUntil.IsZero() {
				until := r.PausedUntil
				rate.PausedUntil = &until
			}
			one.Rates = append(one.Rates, rate)
		}
		for _, c := range u.Calls {
			one.Calls = append(one.Calls, api.GitHubCallCount{
				Caller: c.Caller, Method: c.Method, Endpoint: c.Endpoint, Resource: c.Resource,
				Sent: c.Sent, Counted: c.Counted(), NotModified: c.NotModified, Shared: c.Shared, Held: c.Held, Failed: c.Failed, Last: c.Last,
			})
		}
		out.Accounts = append(out.Accounts, one)
	}
	return writeJSON(w, http.StatusOK, out)
}
