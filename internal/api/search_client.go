package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Search looks for query across everything AgentBox keeps, at most limit of
// each kind (0 is the daemon's default).
func (c *Client) Search(ctx context.Context, query string, limit int) (SearchResults, error) {
	q := url.Values{"q": {query}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out SearchResults
	return out, c.do(ctx, http.MethodGet, "/v1/search?"+q.Encode(), nil, &out)
}
