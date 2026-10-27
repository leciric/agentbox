package backup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// installation is a temporary AgentBox installation with one project, a
// secrets key and one Claude Code login in it, made in t.TempDir so no real
// key or credential is ever read.
func installation(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	p := paths.Paths{Config: filepath.Join(root, "config", "agentbox"), Data: filepath.Join(root, "data", "agentbox")}
	if err := os.MkdirAll(p.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(p.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	err = store.AddProject(context.Background(), state.Project{
		Name: "pawly", DisplayName: "Pawly", Root: filepath.Join(root, "pawly"), CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.SecretsKey(), []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	login := filepath.Join(p.Credentials(), "claude")
	if err := os.MkdirAll(login, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(login, "work.json"), []byte(`{"token":"made up for this test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readBackupManifest(t *testing.T, dir string) Manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreateAndVerify(t *testing.T) {
	p := installation(t)
	dir, err := Create(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(dir), "agentbox-backup-") {
		t.Errorf("backup directory %q is not timestamped with agentbox-backup-", filepath.Base(dir))
	}

	m := readBackupManifest(t, dir)
	if m.Format != FormatVersion || m.AgentBox != Version {
		t.Errorf("manifest = format %d, version %q; want %d, %q", m.Format, m.AgentBox, FormatVersion, Version)
	}
	if m.UserVersion <= 0 {
		t.Errorf("sqlite user_version in the manifest is %d, want the version the database was migrated to", m.UserVersion)
	}
	if len(m.Absent) != 0 {
		t.Errorf("absent = %v, want nothing", m.Absent)
	}
	listed := make(map[string]File)
	for _, f := range m.Files {
		listed[f.Path] = f
	}
	for _, want := range []string{"state.db", "secrets.key", filepath.Join("credentials", "claude", "work.json")} {
		if _, ok := listed[want]; !ok {
			t.Errorf("the manifest does not list %s: %v", want, m.Files)
		}
	}

	for _, path := range []string{"state.db", "secrets.key", filepath.Join("credentials", "claude", "work.json")} {
		info, err := os.Stat(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s is mode %o, want 600", path, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "state.db-wal")); !os.IsNotExist(err) {
		t.Error("the backup has a state.db-wal beside it: VACUUM INTO should write one whole file")
	}

	if err := Verify(dir); err != nil {
		t.Errorf("Verify on a fresh backup: %v", err)
	}
}

func TestCreateWithoutKeyOrCredentials(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}
	store, err := state.Open(p.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	dir, err := Create(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := readBackupManifest(t, dir)
	want := []string{"secrets.key", "credentials"}
	if strings.Join(m.Absent, ",") != strings.Join(want, ",") {
		t.Errorf("absent = %v, want %v", m.Absent, want)
	}
	if err := Verify(dir); err != nil {
		t.Errorf("a machine that never had a key should still verify: %v", err)
	}
	if size, err := Size(dir); err != nil || size == 0 {
		t.Errorf("Size = %d, %v, want more than nothing", size, err)
	}
}

func TestCreateCopiesThroughTheWAL(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}
	store, err := state.Open(p.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// No checkpoint, so the commit below is only in the -wal file when the
	// backup reads it: this is what a daemon that is running leaves behind.
	if _, err := store.DB().Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	err = store.AddProject(context.Background(), state.Project{
		Name: "in-the-wal", DisplayName: "In the WAL", Root: filepath.Join(root, "project"), CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(p.StateDB() + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("the commit is not in a -wal to copy through: %v", err)
	}

	dir, err := Create(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir); err != nil {
		t.Errorf("Verify on a backup taken through the WAL: %v", err)
	}

	db, err := openReadOnly(filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var display string
	if err := db.QueryRow("SELECT display_name FROM projects WHERE name = 'in-the-wal'").Scan(&display); err != nil {
		t.Fatalf("the commit that was only in the WAL is not in the backup: %v", err)
	}
	if display != "In the WAL" {
		t.Errorf("display name = %q, want In the WAL", display)
	}
}

func TestCreateRefusesAMachineWithNoDatabase(t *testing.T) {
	root := t.TempDir()
	_, err := Create(paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "start AgentBox once") {
		t.Fatalf("Create with no state.db = %v, want a message about starting AgentBox once", err)
	}
}

func TestVerifyCatchesDamage(t *testing.T) {
	t.Run("a changed file", func(t *testing.T) {
		dir, err := Create(installation(t), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(filepath.Join(dir, "state.db"), os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		// One byte changed, of the same length, so the checksum is what
		// notices rather than the size.
		if _, err := f.WriteAt([]byte{0xff}, 0); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err := Verify(dir); err == nil || !strings.Contains(err.Error(), "does not match its manifest") {
			t.Errorf("Verify on a changed database = %v, want a manifest mismatch", err)
		}
	})

	t.Run("a missing file", func(t *testing.T) {
		dir, err := Create(installation(t), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, "credentials", "claude", "work.json")); err != nil {
			t.Fatal(err)
		}
		if err := Verify(dir); err == nil || !strings.Contains(err.Error(), "work.json") {
			t.Errorf("Verify with a credential missing = %v, want an error naming it", err)
		}
	})

	t.Run("a key that is not a key", func(t *testing.T) {
		p := installation(t)
		if err := os.WriteFile(p.SecretsKey(), []byte("not a key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		dir, err := Create(p, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := Verify(dir); err == nil || !strings.Contains(err.Error(), "not an AgentBox secrets key") {
			t.Errorf("Verify with a short key = %v, want the key check", err)
		}
	})
}
