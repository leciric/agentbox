package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Every request this package sends goes through one place (send), which keeps
// count of it per account, by who asked and for what, and reads what GitHub
// says is left of the account's hourly budget. The token is shared — the
// daemon, the app's views behind it, and every agent's own `gh` spend the same
// 5,000 requests an hour — so the count also says how much of what GitHub
// reports used was not AgentBox's own: that is the agents and the user.
//
// It is also what keeps AgentBox inside the budget:
//
//   - A read GitHub has answered before is asked for again with its ETag, and
//     a 304, which GitHub doesn't count, is answered from what was kept.
//   - Two reads of the same thing at once are one request.
//   - Once GitHub says the budget is spent (a 403 or 429 with no requests
//     left, or a Retry-After), nothing more is sent for it until it resets.
//   - A background caller — a poll nobody is waiting on — stands aside while
//     what is left is under lowBudgetShare, leaving that to the user's own
//     clicks and the agents; Slowdown tells the pollers to spread out before
//     that.

// Caller names who sent a request, for the count. A background caller is a
// poll no one is waiting on, which gives way first when the budget runs low.
type Caller struct {
	Name       string
	Background bool
}

type callerKey struct{}

// WithCaller marks the requests made with ctx as name's: something a person
// is waiting on, such as a click in the app.
func WithCaller(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, callerKey{}, Caller{Name: name})
}

// InBackground marks the requests made with ctx as a background poll's.
func InBackground(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, callerKey{}, Caller{Name: name, Background: true})
}

func callerOf(ctx context.Context) Caller {
	if c, ok := ctx.Value(callerKey{}).(Caller); ok && c.Name != "" {
		return c
	}
	return Caller{Name: "other"}
}

const (
	// slowShare is the share of an hour's budget left below which Slowdown
	// starts spreading the pollers out.
	slowShare = 0.2
	// lowBudgetShare is the share left below which background callers stop
	// until GitHub's reset: 250 of a personal account's 5,000.
	lowBudgetShare = 0.05
	// maxSlowdown is the most Slowdown stretches an interval by.
	maxSlowdown = 8
)

// Rate is what GitHub last said of one of an account's budgets: core is the
// REST API's, graphql the GraphQL API's points.
type Rate struct {
	Resource  string    `json:"resource"`
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Used      int       `json:"used"`
	Reset     time.Time `json:"reset"`
	At        time.Time `json:"at"` // when GitHub said so
	// Ours is how many requests AgentBox itself had GitHub count against
	// this budget since its last reset, and Others what GitHub counted
	// besides: agents' own gh, the user, anything else with the token. Only
	// kept for core, where a request is one of the budget; a GraphQL query
	// costs points by its size.
	Ours   int `json:"ours,omitempty"`
	Others int `json:"others,omitempty"`
	// PausedUntil is set while nothing is sent for it, after GitHub said the
	// budget is spent.
	PausedUntil time.Time `json:"pausedUntil,omitzero"`
}

// CallCount is how many requests one caller made of one endpoint.
type CallCount struct {
	Caller   string `json:"caller"`
	Method   string `json:"method"`
	Endpoint string `json:"endpoint"` // the path, with owner, repository, numbers and commits as {placeholders}
	Resource string `json:"resource"`
	// Sent is requests that went to GitHub; NotModified those of them that
	// came back 304, which GitHub doesn't count. Shared is reads that joined
	// one already in flight and sent nothing, Held ones not sent because the
	// budget was spent or low, and Failed ones GitHub refused.
	Sent        int       `json:"sent"`
	NotModified int       `json:"notModified,omitempty"`
	Shared      int       `json:"shared,omitempty"`
	Held        int       `json:"held,omitempty"`
	Failed      int       `json:"failed,omitempty"`
	Last        time.Time `json:"last"`
}

// Counted is how many of the requests GitHub counted against the budget.
func (c CallCount) Counted() int { return c.Sent - c.NotModified }

// AccountUsage is one token's use of the API since the daemon started.
type AccountUsage struct {
	// Token is a fingerprint of the token, never the token itself.
	Token string      `json:"token"`
	Since time.Time   `json:"since"`
	Rates []Rate      `json:"rates"`
	Calls []CallCount `json:"calls"`
}

type callKey struct{ caller, method, endpoint, resource string }

type window struct {
	reset time.Time
	ours  int
}

type account struct {
	mu      sync.Mutex
	token   string // Fingerprint of the token
	since   time.Time
	calls   map[callKey]*CallCount
	rates   map[string]*Rate
	windows map[string]*window
	paused  map[string]time.Time
}

// usage is every account's, by token fingerprint and API root: the root is
// one in use, and many in tests, each against a stub of its own.
var usage = struct {
	mu sync.Mutex
	by map[string]*account
}{by: map[string]*account{}}

// Fingerprint names a token without giving it away: what Usage reports an
// account under.
func Fingerprint(token string) string {
	if token == "" {
		return "anonymous"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:6])
}

func (c Client) account() *account {
	fp := Fingerprint(c.Token)
	key := fp + " " + c.base()
	usage.mu.Lock()
	defer usage.mu.Unlock()
	a, ok := usage.by[key]
	if !ok {
		a = &account{token: fp, since: time.Now(), calls: map[callKey]*CallCount{}, rates: map[string]*Rate{}, windows: map[string]*window{}, paused: map[string]time.Time{}}
		usage.by[key] = a
	}
	return a
}

// Usage is what every token was used for since the daemon started, busiest
// first within each.
func Usage() []AccountUsage {
	usage.mu.Lock()
	accounts := make([]*account, 0, len(usage.by))
	for _, a := range usage.by {
		accounts = append(accounts, a)
	}
	usage.mu.Unlock()
	out := make([]AccountUsage, len(accounts))
	for i, a := range accounts {
		out[i] = a.snapshot()
	}
	slices.SortFunc(out, func(a, b AccountUsage) int { return strings.Compare(a.Token, b.Token) })
	return out
}

func (a *account) snapshot() AccountUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := AccountUsage{Token: a.token, Since: a.since, Rates: []Rate{}, Calls: []CallCount{}}
	for res, r := range a.rates {
		rate := *r
		if w := a.windows[res]; w != nil && res == "core" && w.reset.Equal(r.Reset) {
			rate.Ours = w.ours
			rate.Others = max(0, r.Used-w.ours)
		}
		if until := a.paused[res]; time.Now().Before(until) {
			rate.PausedUntil = until
		}
		out.Rates = append(out.Rates, rate)
	}
	slices.SortFunc(out.Rates, func(a, b Rate) int { return strings.Compare(a.Resource, b.Resource) })
	for _, c := range a.calls {
		out.Calls = append(out.Calls, *c)
	}
	slices.SortFunc(out.Calls, func(a, b CallCount) int {
		if a.Counted() != b.Counted() {
			return b.Counted() - a.Counted()
		}
		return strings.Compare(a.Caller+a.Endpoint, b.Caller+b.Endpoint)
	})
	return out
}

// count is the entry for one caller's requests of one endpoint.
func (a *account) count(c Caller, method, path, resource string) *CallCount {
	k := callKey{c.Name, method, endpointOf(path), resource}
	n, ok := a.calls[k]
	if !ok {
		n = &CallCount{Caller: k.caller, Method: k.method, Endpoint: k.endpoint, Resource: k.resource}
		a.calls[k] = n
	}
	n.Last = time.Now()
	return n
}

func (a *account) record(c Caller, method, path, resource string, f func(*CallCount)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f(a.count(c, method, path, resource))
}

// admit says whether a request for resource may go now, or when it may.
func (a *account) admit(c Caller, resource string, now time.Time) *RateLimitError {
	a.mu.Lock()
	defer a.mu.Unlock()
	if until := a.paused[resource]; now.Before(until) {
		return &RateLimitError{Until: until, Message: "the hourly limit is used up"}
	}
	if !c.Background {
		return nil
	}
	if r := a.rates[resource]; r != nil && now.Before(r.Reset) && r.Limit > 0 && float64(r.Remaining) < lowBudgetShare*float64(r.Limit) {
		return &RateLimitError{Until: r.Reset, Message: fmt.Sprintf("only %d of %d requests left this hour, kept for you and your agents", r.Remaining, r.Limit), Low: true}
	}
	return nil
}

// observe reads what a response says of the budget. counted is whether
// GitHub counted the request against it.
func (a *account) observe(h http.Header, resource string, counted bool, now time.Time) {
	limit, err1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	remaining, err2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	if res := h.Get("X-RateLimit-Resource"); res != "" {
		resource = res
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err1 != nil || err2 != nil {
		return
	}
	used, err := strconv.Atoi(h.Get("X-RateLimit-Used"))
	if err != nil {
		used = limit - remaining
	}
	r := &Rate{Resource: resource, Limit: limit, Remaining: remaining, Used: used, Reset: resetOf(h), At: now}
	a.rates[resource] = r
	w := a.windows[resource]
	if w == nil || !w.reset.Equal(r.Reset) {
		w = &window{reset: r.Reset}
		a.windows[resource] = w
	}
	if counted {
		w.ours++
	}
}

func (a *account) pause(resource string, until time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if until.After(a.paused[resource]) {
		a.paused[resource] = until
	}
}

// Slowdown is how much a poller reading with c's token should stretch its interval: 1
// while at least a fifth of every budget is left, rising to 8 as one nears
// the floor where background reads stop.
func (c Client) Slowdown() float64 {
	a := c.account()
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	factor := 1.0
	for _, r := range a.rates {
		if r.Limit <= 0 || !now.Before(r.Reset) {
			continue
		}
		share := float64(r.Remaining) / float64(r.Limit)
		if share < slowShare {
			factor = max(factor, min(maxSlowdown, slowShare/max(share, slowShare/maxSlowdown)))
		}
	}
	return factor
}

// Placeholders for endpointOf: what in a path is one repository's, one pull
// request's or one commit's, rather than which endpoint it is.
var (
	numberSegment = regexp.MustCompile(`^\d+$`)
	shaSegment    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// endpointOf is a request's path with what varies from one repository, pull
// request or commit to another replaced: /repos/{owner}/{repo}/pulls/{n}.
func endpointOf(path string) string {
	path, _, _ = strings.Cut(path, "?")
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, s := range segs {
		switch {
		case i > 0 && i < 3 && segs[0] == "repos":
			segs[i] = map[int]string{1: "{owner}", 2: "{repo}"}[i]
		case numberSegment.MatchString(s):
			segs[i] = "{n}"
		case i > 0 && (segs[i-1] == "commits" || shaSegment.MatchString(s)):
			segs[i] = "{sha}"
		}
	}
	return "/" + strings.Join(segs, "/")
}
