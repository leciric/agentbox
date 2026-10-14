package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SearchOptions narrow a search (GET /v1/search's kinds, project and since).
// The zero value is everything.
type SearchOptions struct {
	Kinds   []string  // only these groups, of SearchProjects to SearchPulls
	Project string    // only this project's things, by slug
	Since   time.Time // only things from then on; kinds without a time drop out
	// Archived includes past agents' chats.
	Archived bool
}

// Search looks for query across everything AgentBox keeps, at most limit of
// each kind (0 is the daemon's default).
func (c *Client) Search(ctx context.Context, query string, limit int) (SearchResults, error) {
	return c.SearchWith(ctx, query, limit, SearchOptions{})
}

// SearchWith is Search narrowed by opt.
func (c *Client) SearchWith(ctx context.Context, query string, limit int, opt SearchOptions) (SearchResults, error) {
	q := url.Values{"q": {query}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if len(opt.Kinds) > 0 {
		q.Set("kinds", strings.Join(opt.Kinds, ","))
	}
	if opt.Project != "" {
		q.Set("project", opt.Project)
	}
	if !opt.Since.IsZero() {
		q.Set("since", opt.Since.Format(time.RFC3339Nano))
	}
	if opt.Archived {
		q.Set("archived", "true")
	}
	var out SearchResults
	return out, c.do(ctx, http.MethodGet, "/v1/search?"+q.Encode(), nil, &out)
}
