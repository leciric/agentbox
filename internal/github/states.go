package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PRState is where one pull request stands, for something that only needs to
// know whether it is over: the daemon closing the memories that wait on it.
type PRState struct {
	Number     int
	Title      string
	State      string // open, closed or merged
	HeadBranch string
	// ClosedAt is when it was merged or closed; zero while it is open.
	ClosedAt time.Time
}

// maxStatesPerQuery bounds the aliases in one request, well inside what
// GitHub allows a query to ask for.
const maxStatesPerQuery = 60

// branchStatePage is how many pull requests are read per branch: a branch
// is pushed again and opened again now and then, rarely more often.
const branchStatePage = 5

const stateFields = `number title state headRefName closedAt`

// PullRequestStates reads the pull requests numbered, and those opened from
// the branches named, in as few requests as fit: one for anything a project
// has. A number GitHub doesn't know, or a branch nobody opened one from, is
// left out of the answer.
func (c Client) PullRequestStates(ctx context.Context, repo Repo, numbers []int, branches []string) (map[int]PRState, map[string][]PRState, error) {
	byNumber, byBranch := map[int]PRState{}, map[string][]PRState{}
	type ask struct {
		number int
		branch string
	}
	var asks []ask
	for _, n := range numbers {
		asks = append(asks, ask{number: n})
	}
	for _, b := range branches {
		asks = append(asks, ask{branch: b})
	}
	for len(asks) > 0 {
		batch := asks[:min(len(asks), maxStatesPerQuery)]
		asks = asks[len(batch):]
		var b strings.Builder
		b.WriteString("query($owner: String!, $name: String!) {\n  repository(owner: $owner, name: $name) {\n")
		for i, a := range batch {
			if a.branch == "" {
				fmt.Fprintf(&b, "    pr%d: pullRequest(number: %d) { %s }\n", a.number, a.number, stateFields)
				continue
			}
			name, _ := json.Marshal(a.branch)
			fmt.Fprintf(&b, "    b%d: pullRequests(headRefName: %s, first: %d, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { %s } }\n",
				i, name, branchStatePage, stateFields)
		}
		b.WriteString("  }\n}\n")
		var out struct {
			Data struct {
				Repository map[string]json.RawMessage `json:"repository"`
			} `json:"data"`
			Errors []struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"errors"`
		}
		if _, err := c.graphQL(ctx, map[string]any{
			"query":     b.String(),
			"variables": map[string]string{"owner": repo.Owner, "name": repo.Name},
		}, &out); err != nil {
			return nil, nil, err
		}
		for _, e := range out.Errors {
			if e.Type == "RATE_LIMITED" {
				return nil, nil, &RateLimitError{Until: time.Now().Add(time.Hour), Message: e.Message}
			}
		}
		if out.Data.Repository == nil {
			msg := "the repository wasn't returned"
			if len(out.Errors) > 0 {
				msg = out.Errors[0].Message
			}
			return nil, nil, fmt.Errorf("%w: %s", ErrNoAccess, msg)
		}
		for i, a := range batch {
			if a.branch == "" {
				raw, ok := out.Data.Repository["pr"+strconv.Itoa(a.number)]
				if !ok || string(raw) == "null" {
					continue
				}
				var pr gqlState
				if err := json.Unmarshal(raw, &pr); err != nil {
					return nil, nil, err
				}
				byNumber[a.number] = pr.state()
				continue
			}
			raw, ok := out.Data.Repository["b"+strconv.Itoa(i)]
			if !ok || string(raw) == "null" {
				continue
			}
			var page struct {
				Nodes []gqlState `json:"nodes"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return nil, nil, err
			}
			for _, pr := range page.Nodes {
				byBranch[a.branch] = append(byBranch[a.branch], pr.state())
			}
		}
	}
	return byNumber, byBranch, nil
}

type gqlState struct {
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	HeadRefName string     `json:"headRefName"`
	ClosedAt    *time.Time `json:"closedAt"`
}

func (s gqlState) state() PRState {
	out := PRState{Number: s.Number, Title: s.Title, State: strings.ToLower(s.State), HeadBranch: s.HeadRefName}
	if s.ClosedAt != nil {
		out.ClosedAt = *s.ClosedAt
	}
	return out
}
