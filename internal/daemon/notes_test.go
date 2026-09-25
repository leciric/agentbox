package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/notes"
	"agentbox/internal/testutil"
)

// A project's notes are one markdown file, and what they say is folded into
// the brief every agent of the project is given — including the lead's, which
// is rebuilt when they change.
func TestProjectNotesReachEveryBrief(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}

	// A project with no notes has no notes section at all.
	n, err := d.client.Notes(ctx, "hello-stack")
	if err != nil || n.Text != "" {
		t.Fatalf("Notes() = %+v, %v; want empty", n, err)
	}
	brief, err := d.client.Brief(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(brief, "Project notes") {
		t.Errorf("the brief has a notes section without notes:\n%s", brief)
	}

	// A login, so the project has a lead with a brief of its own to rebuild.
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.manager(nil).EnsureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}

	const written = "The app is a pnpm monorepo: `pnpm install` at the root."
	if n, err = d.client.SetNotes(ctx, "hello-stack", written+"\n"); err != nil {
		t.Fatal(err)
	}
	if n.Text != written+"\n" || n.UpdatedAt.IsZero() {
		t.Errorf("SetNotes() = %+v, want the notes back with the time they were written", n)
	}

	// They are a file a person can read and diff, not a column.
	onDisk, err := os.ReadFile(d.paths.ProjectNotes("hello-stack"))
	if err != nil {
		t.Fatalf("the notes aren't on disk: %v", err)
	}
	if string(onDisk) != written+"\n" {
		t.Errorf("the notes file holds %q, want %q", onDisk, written)
	}

	// The brief preview shows the merged result: what an agent made now reads.
	if brief, err = d.client.Brief(ctx, "hello-stack", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief, "## Project notes") || !strings.Contains(brief, written) {
		t.Errorf("the brief is missing the notes:\n%s", brief)
	}

	// The lead reads the same notes, in the brief that was rewritten under it.
	leadBrief, err := os.ReadFile(filepath.Join(d.paths.LeadHome("hello-stack"), "AGENTBOX.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(leadBrief), written) {
		t.Errorf("the lead's brief is missing the notes:\n%s", leadBrief)
	}
}

// The lead adds what it learns through its own socket, and only ever adds:
// what the user wrote stays exactly as they left it.
func TestLeadAppendsToProjectNotes(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetNotes(ctx, "hello-stack", "Squash before merging.\n"); err != nil {
		t.Fatal(err)
	}

	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	n, err := lead.ProjectNotes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n.Text != "Squash before merging.\n" {
		t.Errorf("ProjectNotes() = %q, want what the user wrote", n.Text)
	}

	if n, err = lead.AppendProjectNote(ctx, "the e2e tests need a Postgres on 5432"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(n.Text, "Squash before merging.\n") {
		t.Errorf("appending changed what the user wrote:\n%s", n.Text)
	}
	if !strings.Contains(n.Text, "## From the lead") || !strings.Contains(n.Text, ": the e2e tests need a Postgres on 5432") {
		t.Errorf("the entry isn't under the lead's heading:\n%s", n.Text)
	}
	// An empty note is refused rather than dating a blank bullet.
	if _, err := lead.AppendProjectNote(ctx, "  "); err == nil {
		t.Error("AppendProjectNote() took an empty note")
	}
	// The user reads the lead's entries as part of the same notes.
	if user, err := d.client.Notes(ctx, "hello-stack"); err != nil || user.Text != n.Text {
		t.Errorf("Notes() = %q, %v; want %q", user.Text, err, n.Text)
	}
}

// The lead is asked, in the conversation, to correct a note and to take one
// out. It names the entry by quoting it, because the file has no ids in it to
// name one by (D82), and every change goes the same way an append does: the
// briefs of the agents that already exist are rewritten under them.
func TestLeadEditsAndRemovesNotes(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.manager(nil).EnsureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetNotes(ctx, "hello-stack", "Squash before merging.\n"); err != nil {
		t.Fatal(err)
	}
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	for _, entry := range []string{"the build needs Java 21", "the e2e tests need a Postgres on 5432"} {
		if _, err := lead.AppendProjectNote(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}

	// Editing names what it changed and what it became, and keeps the date the
	// entry was written under: it corrects a note rather than writing a new one.
	change, err := lead.EditProjectNote(ctx, "the build needs Java 21", "the build needs Java 25")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(change.Was, "Java 21") || !strings.Contains(change.Now, "Java 25") {
		t.Errorf("EditProjectNote() reported %+v, want the entry as it was and as it now is", change)
	}
	if !change.FromLead || change.Section != notes.LeadHeading {
		t.Errorf("EditProjectNote() reported %+v, want the lead's own section", change)
	}
	if !strings.Contains(change.Text, "Java 25") || strings.Contains(change.Text, "Java 21") {
		t.Errorf("the notes still read:\n%s", change.Text)
	}
	if !strings.HasPrefix(change.Text, "Squash before merging.\n") {
		t.Errorf("editing one entry disturbed what the user wrote:\n%s", change.Text)
	}

	// The change reaches the agents that already exist, the same way a saved
	// note does: the lead's own brief is rewritten under it.
	leadBrief, err := os.ReadFile(filepath.Join(d.paths.LeadHome("hello-stack"), "AGENTBOX.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(leadBrief), "Java 25") || strings.Contains(string(leadBrief), "Java 21") {
		t.Errorf("the lead is still briefed with the note as it was:\n%s", leadBrief)
	}

	// A quote that doesn't pick out exactly one entry changes nothing, and
	// says which ones it matched rather than guessing between them.
	if _, err := lead.EditProjectNote(ctx, "the", "something else"); err == nil {
		t.Error("EditProjectNote() acted on a quote matching more than one note")
	} else if !strings.Contains(err.Error(), "more than one note matches") {
		t.Errorf("EditProjectNote() said %q, want it to name what it matched", err)
	}
	if _, err := lead.RemoveProjectNote(ctx, "the build needs Maven"); err == nil {
		t.Error("RemoveProjectNote() acted on a quote matching nothing")
	} else if !strings.Contains(err.Error(), "no note matches") {
		t.Errorf("RemoveProjectNote() said %q, want a refusal naming the quote", err)
	}
	if n, err := d.client.Notes(ctx, "hello-stack"); err != nil || n.Text != change.Text {
		t.Errorf("a refused change touched the notes:\n%s", n.Text)
	}

	// Removing text the user wrote themselves is possible — they asked for it
	// — but the answer says so, so it can't be done quietly.
	gone, err := lead.RemoveProjectNote(ctx, "Squash before merging.")
	if err != nil {
		t.Fatal(err)
	}
	if gone.FromLead || gone.Now != "" || !strings.Contains(gone.Was, "Squash") {
		t.Errorf("RemoveProjectNote() reported %+v, want the user's own line named as gone", gone)
	}
	if strings.Contains(gone.Text, "Squash") {
		t.Errorf("the line is still in the notes:\n%s", gone.Text)
	}
	// And the file on disk is the notes: it is markdown, still, either way.
	onDisk, err := os.ReadFile(d.paths.ProjectNotes("hello-stack"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != gone.Text || !strings.HasPrefix(string(onDisk), notes.LeadHeading) {
		t.Errorf("the notes file holds %q, want %q", onDisk, gone.Text)
	}
}
