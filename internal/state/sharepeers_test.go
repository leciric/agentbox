package state

import "testing"

// A peer reaches the projects it was given, or every one of this machine's
// own; never one another AgentBox shares with this one, which two AgentBoxes
// sharing everything with each other would pass back and forth for good.
func TestSharePeerShares(t *testing.T) {
	t.Parallel()
	all := SharePeer{AllProjects: true}
	some := SharePeer{Projects: []string{"web", "r1~api"}}
	for _, c := range []struct {
		peer SharePeer
		slug string
		want bool
	}{
		{all, "web", true},
		{all, "r1" + RemoteProjectSep + "web", false},
		{all, HomeProject, false},
		{all, "", false},
		{some, "web", true},
		{some, "api", false},
		{some, "r1~api", false},
	} {
		if got := c.peer.Shares(c.slug); got != c.want {
			t.Errorf("%+v.Shares(%q) = %v, want %v", c.peer, c.slug, got, c.want)
		}
	}
}
