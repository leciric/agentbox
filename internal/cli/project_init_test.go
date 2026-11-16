package cli

import (
	"strings"
	"testing"

	"agentbox/internal/api"
)

func TestSkippedLinesSayWhatTheFirstCommitLeavesOut(t *testing.T) {
	err := &api.StatusError{Message: "x is not inside a git repository", Reason: api.ErrorNotARepository, Skipped: []api.SkippedPath{
		{Path: "node_modules", Bytes: 412_000_000, Reason: "dependencies"},
		{Path: "data/dump.sql", Bytes: 80_000_000, Reason: "large-file"},
	}}
	got := skippedLines(err)
	for _, want := range []string{"left out of the initial commit", "node_modules (dependencies, ", "data/dump.sql (large-file, "} {
		if !strings.Contains(got, want) {
			t.Errorf("skippedLines() = %q, missing %q", got, want)
		}
	}
	if skippedLines(&api.StatusError{Message: "no"}) != "" {
		t.Error("nothing left out still printed a list")
	}
	if noRemoteNote(api.Project{}) != "" || !strings.Contains(noRemoteNote(api.Project{NoRemote: true}), "agentbox merge") {
		t.Error("noRemoteNote")
	}
}
