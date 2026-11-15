package github

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// DebugLog, when set, is told of every request sent: who asked, for what,
// how it went and what is left of the budget. The daemon points its log at
// it with AGENTBOX_GITHUB_DEBUG=1.
var DebugLog func(format string, args ...any)

// flights shares a GET in flight among everyone asking for the same thing
// with the same token.
var flights singleflight.Group

// etagCacheBytes bounds what is kept to answer 304s from, and
// etagEntryBytes the largest single answer kept: a big pull request's page
// of files with their diffs is a few hundred kilobytes.
const (
	etagCacheBytes = 32 << 20
	etagEntryBytes = 4 << 20
)

type etagEntry struct {
	etag string
	body []byte
	used time.Time
}

// etags keeps GitHub's last answer to each read, by token and URL, to ask
// for it again conditionally.
var etags = struct {
	mu    sync.Mutex
	by    map[string]*etagEntry
	bytes int
}{by: map[string]*etagEntry{}}

func etagOf(key string) (*etagEntry, bool) {
	etags.mu.Lock()
	defer etags.mu.Unlock()
	e, ok := etags.by[key]
	if ok {
		e.used = time.Now()
	}
	return e, ok
}

func keepETag(key, etag string, body []byte) {
	if etag == "" || len(body) > etagEntryBytes {
		return
	}
	etags.mu.Lock()
	defer etags.mu.Unlock()
	if old, ok := etags.by[key]; ok {
		etags.bytes -= len(old.body)
	}
	etags.by[key] = &etagEntry{etag: etag, body: body, used: time.Now()}
	etags.bytes += len(body)
	for etags.bytes > etagCacheBytes {
		oldest := ""
		for k, e := range etags.by {
			if oldest == "" || e.used.Before(etags.by[oldest].used) {
				oldest = k
			}
		}
		etags.bytes -= len(etags.by[oldest].body)
		delete(etags.by, oldest)
	}
}

// send is every request this package makes: a GET shared with any the same
// one in flight and asked conditionally, the budget checked first and read
// after, and the request counted. It returns the body of a 2xx answer, and
// the response's header whatever came back.
func (c Client) send(ctx context.Context, method, path string, body any) ([]byte, http.Header, error) {
	if method != http.MethodGet {
		return c.roundTrip(ctx, method, path, body)
	}
	who := callerOf(ctx)
	key := Fingerprint(c.Token) + " " + c.base() + path
	led := false
	ch := flights.DoChan(key, func() (any, error) {
		led = true
		// Not cut short by the first caller leaving: others may be waiting.
		data, header, err := c.roundTrip(context.WithoutCancel(ctx), method, path, nil)
		return result{data, header}, err
	})
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case r := <-ch:
		if !led {
			c.account().record(who, method, path, resourceOf(path), func(n *CallCount) { n.Shared++ })
		}
		res, _ := r.Val.(result)
		return res.data, res.header, r.Err
	}
}

type result struct {
	data   []byte
	header http.Header
}

func (c Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return apiRoot()
}

func resourceOf(path string) string {
	if path == "/graphql" {
		return "graphql"
	}
	return "core"
}

func (c Client) roundTrip(ctx context.Context, method, path string, body any) ([]byte, http.Header, error) {
	who := callerOf(ctx)
	resource := resourceOf(path)
	acct := c.account()
	if limited := acct.admit(who, resource, time.Now()); limited != nil {
		acct.record(who, method, path, resource, func(n *CallCount) { n.Held++ })
		return nil, nil, limited
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, reader)
	if err != nil {
		return nil, nil, err
	}
	if resource == "core" {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	key := Fingerprint(c.Token) + " " + c.base() + path
	kept, haveKept := (*etagEntry)(nil), false
	if method == http.MethodGet {
		if kept, haveKept = etagOf(key); haveKept {
			req.Header.Set("If-None-Match", kept.etag)
		}
	}
	client := c.HTTP
	if client == nil {
		timeout := 15 * time.Second
		if resource == "graphql" {
			timeout = 30 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	res, err := client.Do(req)
	if err != nil {
		acct.record(who, method, path, resource, func(n *CallCount) { n.Sent++; n.Failed++ })
		return nil, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	data, readErr := io.ReadAll(res.Body)
	notModified := res.StatusCode == http.StatusNotModified && haveKept
	now := time.Now()
	acct.observe(res.Header, resource, !notModified, now)
	acct.record(who, method, path, resource, func(n *CallCount) {
		n.Sent++
		if notModified {
			n.NotModified++
		} else if res.StatusCode >= 300 {
			n.Failed++
		}
	})
	if DebugLog != nil {
		DebugLog("github: %s %s %s by %s: %d, %s left %s", Fingerprint(c.Token), method, endpointOf(path), who.Name, res.StatusCode,
			resource, res.Header.Get("X-RateLimit-Remaining"))
	}
	limited := rateLimitOf(res)
	if limited == nil && res.StatusCode == http.StatusForbidden && bytes.Contains(bytes.ToLower(data), []byte("rate limit")) {
		// A secondary limit with no Retry-After: GitHub's advice is to wait
		// at least a minute.
		limited = &RateLimitError{Until: now.Add(time.Minute), Message: "too many requests at once"}
	}
	if limited != nil {
		var said struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &said) == nil && said.Message != "" {
			limited.Message = said.Message
		}
		acct.pause(resource, limited.Until)
		return nil, res.Header, limited
	}
	if notModified {
		return kept.body, res.Header, nil
	}
	if readErr != nil {
		return nil, res.Header, readErr
	}
	if res.StatusCode >= 300 {
		return nil, res.Header, apiErrorOf(path, res, data)
	}
	if resource == "graphql" {
		if limited := graphQLLimited(data, res.Header); limited != nil {
			acct.pause(resource, limited.Until)
			return nil, res.Header, limited
		}
	}
	if method == http.MethodGet {
		keepETag(key, res.Header.Get("ETag"), data)
	}
	return data, res.Header, nil
}

// graphQLLimited reads a GraphQL answer for GitHub saying the points are
// spent, which it says with a 200 and an error of type RATE_LIMITED.
func graphQLLimited(data []byte, h http.Header) *RateLimitError {
	if !bytes.Contains(data, []byte("RATE_LIMITED")) {
		return nil
	}
	var probe struct {
		Data struct {
			RateLimit *struct {
				ResetAt time.Time `json:"resetAt"`
			} `json:"rateLimit"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return nil
	}
	for _, e := range probe.Errors {
		if e.Type == "RATE_LIMITED" {
			until := resetOf(h)
			if rl := probe.Data.RateLimit; rl != nil && !rl.ResetAt.IsZero() {
				until = rl.ResetAt
			}
			if until.IsZero() {
				until = time.Now().Add(time.Hour)
			}
			return &RateLimitError{Until: until, Message: e.Message}
		}
	}
	return nil
}
