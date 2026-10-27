package credentials_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

func gitStore(t *testing.T) credentials.Store {
	t.Helper()
	return credentials.Store{Dir: filepath.Join(t.TempDir(), "credentials")}
}

func TestProjectGitCredentialRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := gitStore(t)

	// A project with no credentials has none, which is not an error.
	if kind, secret, err := s.ProjectGitCredential(ctx, "pawly", "forgejo"); kind != "" || secret != "" || err != nil {
		t.Fatalf("ProjectGitCredential before storing anything = %q, %q, %v", kind, secret, err)
	}

	if err := s.SetProjectGitCredential(ctx, "pawly", "forgejo", credentials.GitCredentialToken, "  ghp_token  "); err != nil {
		t.Fatal(err)
	}
	kind, secret, err := s.ProjectGitCredential(ctx, "pawly", "forgejo")
	if err != nil {
		t.Fatal(err)
	}
	if kind != credentials.GitCredentialToken || secret != "ghp_token" {
		t.Errorf("ProjectGitCredential = %q, %q, want the trimmed token", kind, secret)
	}

	// The file is the credential, readable only by this user.
	info, err := os.Stat(s.GitCredentialPath("pawly", "forgejo"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("%s is mode %o, want 600", s.GitCredentialPath("pawly", "forgejo"), mode)
	}

	// Another project's credential of the same name is another file.
	if err := s.SetProjectGitCredential(ctx, "other", "forgejo", credentials.GitCredentialToken, "ghp_other"); err != nil {
		t.Fatal(err)
	}
	if _, secret, err := s.ProjectGitCredential(ctx, "pawly", "forgejo"); err != nil || secret != "ghp_token" {
		t.Errorf("pawly's credential = %q, %v, after other stored one of the same name", secret, err)
	}

	// Setting it again replaces it, and an SSH key may be several lines.
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH PRIVATE KEY-----"
	if err := s.SetProjectGitCredential(ctx, "pawly", "hub-key", credentials.GitCredentialSSHKey, key); err != nil {
		t.Fatal(err)
	}
	if kind, secret, err := s.ProjectGitCredential(ctx, "pawly", "hub-key"); err != nil || kind != credentials.GitCredentialSSHKey || secret != key {
		t.Errorf("an ssh key = %q, %q, %v, want it stored whole", kind, secret, err)
	}

	if err := s.RemoveProjectGitCredential(ctx, "pawly", "forgejo"); err != nil {
		t.Fatal(err)
	}
	if kind, secret, err := s.ProjectGitCredential(ctx, "pawly", "forgejo"); kind != "" || secret != "" || err != nil {
		t.Errorf("after removing it, ProjectGitCredential = %q, %q, %v", kind, secret, err)
	}
	// Removing one that is already gone is what a project's cleanup does.
	if err := s.RemoveProjectGitCredential(ctx, "pawly", "forgejo"); err != nil {
		t.Errorf("removing a credential twice: %v", err)
	}
}

func TestProjectGitCredentialRefusesWhatItCantUse(t *testing.T) {
	ctx := context.Background()
	s := gitStore(t)
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH PRIVATE KEY-----"
	for _, tc := range []struct {
		name                string
		project, cred, kind string
		secret              string
	}{
		{"a project that isn't a slug", "Pawly App", "forgejo", credentials.GitCredentialToken, "ghp_token"},
		{"a credential name that isn't one", "pawly", "Forgejo Token", credentials.GitCredentialToken, "ghp_token"},
		{"a kind this store doesn't hold", "pawly", "forgejo", "password", "ghp_token"},
		{"a github account", "pawly", "forgejo", "github-account", "ghp_token"},
		{"an empty token", "pawly", "forgejo", credentials.GitCredentialToken, "  "},
		{"a token with a line break", "pawly", "forgejo", credentials.GitCredentialToken, "ghp_one\nghp_two"},
		{"a token stored as a key", "pawly", "forgejo", credentials.GitCredentialSSHKey, "ghp_token"},
	} {
		err := s.SetProjectGitCredential(ctx, tc.project, tc.cred, tc.kind, tc.secret)
		if err == nil {
			t.Errorf("%s was accepted", tc.name)
		}
	}
	if err := s.SetProjectGitCredential(ctx, "pawly", "hub-key", credentials.GitCredentialSSHKey, key); err != nil {
		t.Errorf("a private key was refused: %v", err)
	}
}

// A git credential is a file only this user can read, and never a value in
// state.db: the row that names it holds the name. A copy of the database is
// then no way to push.
func TestProjectGitCredentialIsNotInTheDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: filepath.Join(dir, "repo"), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s := credentials.Store{Dir: filepath.Join(dir, "credentials")}
	const secret = "ghp_this-is-the-token"
	if err := s.SetProjectGitCredential(ctx, "pawly", "forgejo", credentials.GitCredentialToken, secret); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectGitRemote(ctx, state.GitRemote{
		Project: "pawly", Profile: state.GitProfilePrivate,
		URL: "https://hub.example.com/pawly/app.git", Auth: state.GitAuthToken, Credential: "forgejo",
	}); err != nil {
		t.Fatal(err)
	}
	remotes, err := st.ProjectGitRemotes(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 1 || remotes[0].Credential != "forgejo" {
		t.Fatalf("ProjectGitRemotes = %+v, want the credential's name", remotes)
	}

	// The database, and the write-ahead log beside it, hold no plaintext.
	for _, name := range []string{"state.db", "state.db-wal"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(secret)) {
			t.Errorf("%s holds the token in plain text", name)
		}
	}
	if _, got, err := s.ProjectGitCredential(ctx, "pawly", "forgejo"); err != nil || got != secret {
		t.Errorf("the stored credential = %q, %v, want the token back", got, err)
	}
	if path := s.GitCredentialPath("pawly", "forgejo"); !strings.HasPrefix(path, filepath.Join(dir, "credentials")) {
		t.Errorf("the credential is at %s, outside the store", path)
	}
}
