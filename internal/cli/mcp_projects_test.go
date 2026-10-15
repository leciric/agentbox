package cli

import (
	"strings"
	"testing"

	"agentbox/internal/api"
)

// list_project_threads says which threads wait on this lead, and what a
// settled one agreed.
func TestDescribeProjectThreads(t *testing.T) {
	if got := describeProjectThreads(nil); !strings.Contains(got, "No threads") {
		t.Errorf("no threads = %q", got)
	}
	got := describeProjectThreads([]api.ProjectThread{
		{ID: "t1", Project: "api", Kind: "message", Status: "open", Messages: 1,
			Last: &api.ProjectThreadMessage{From: "api", Text: "Which\nformat?", ExpectsReply: true}},
		{ID: "t2", Project: "web", Kind: "align", Topic: "dates", Status: "settled", Messages: 4, Summary: "RFC 3339"},
	})
	for _, want := range []string{"t1 with api: message, open, 1 message", "waits on your reply", "Which format?", `alignment on "dates", settled, 4 messages`, "agreed: RFC 3339"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeProjectThreads() is missing %q:\n%s", want, got)
		}
	}
}
