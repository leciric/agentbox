// Package backup copies an installation's own state into one directory another
// machine can read: the SQLite database, the secrets key and the credentials
// directory.
//
// state.db is copied with SQLite's VACUUM INTO rather than with cp. A plain
// copy of a live database is not a database: the latest committed transactions
// can be in the -wal file beside it, and a copy taken mid-write can hold one
// page from before a transaction and another from after it. VACUUM INTO reads
// a consistent snapshot, through the WAL, from a connection opened read-only,
// and writes it as one file with no -wal beside it, so what lands in the backup
// is a database on its own. Nothing here migrates or writes the database: a
// backup is taken from a machine whose daemon may be down, and must not change
// it.
//
// The secrets key is not in state.db and cannot be: the database holds the
// sealed values, and the key in secrets.key is the one thing that opens them
// (package secrets). A backup with state.db and without secrets.key restores
// every row and none of the secrets, so the two travel together.
package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"agentbox/internal/paths"
)

// FormatVersion is the layout of the directory and its manifest. A reader
// refuses a version it doesn't know rather than guess at the files.
const FormatVersion = 1

// keySize is internal/secrets.KeySize: the file holds that many bytes in
// base64, which is what readKey decodes.
const keySize = 32

// Version is what a backup records as the AgentBox that took it. The CLI sets
// it to its own, which the release build stamps into the binary; anything else
// leaves it as "dev".
var Version = "dev"

// Manifest is manifest.json: what the directory holds, and enough about it to
// tell a complete backup from a partial copy without opening the database.
type Manifest struct {
	Format      int       `json:"format"`
	CreatedAt   time.Time `json:"created_at"`
	AgentBox    string    `json:"agentbox_version"`
	UserVersion int       `json:"sqlite_user_version"`
	Files       []File    `json:"files"`
	// Absent names what this machine never had, so a restore can tell "there
	// was no key here" from "the copy lost it".
	Absent []string `json:"absent,omitempty"`
}

// File is one file of the backup, at a path relative to its directory.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
}

// Create writes a backup of p into a new timestamped directory under to and
// returns it. A failure removes the partial directory: a backup that is half
// written is worse than none, because it looks like one.
func Create(p paths.Paths, to string) (string, error) {
	if to == "" {
		return "", errors.New("no directory to write the backup into")
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return "", err
	}
	dir, err := newDir(to)
	if err != nil {
		return "", err
	}
	if err := write(p, dir); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// newDir makes <to>/agentbox-backup-YYYYMMDD-HHMMSS. A second backup in the
// same second gets -2, -3 and so on, so a timer that runs twice doesn't fail.
func newDir(to string) (string, error) {
	stamp := time.Now().Format("20060102-150405")
	for i := 1; i < 100; i++ {
		name := "agentbox-backup-" + stamp
		if i > 1 {
			name = fmt.Sprintf("%s-%d", name, i)
		}
		dir := filepath.Join(to, name)
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("%s has too many backups taken in the same second", to)
}

func write(p paths.Paths, dir string) error {
	if _, err := os.Stat(p.StateDB()); err != nil {
		return fmt.Errorf("%s: %w (start AgentBox once before taking a backup)", p.StateDB(), err)
	}
	db := filepath.Join(dir, "state.db")
	if err := copyDatabase(p.StateDB(), db); err != nil {
		return err
	}
	if err := os.Chmod(db, 0o600); err != nil {
		return err
	}

	var absent []string
	switch _, err := os.Stat(p.SecretsKey()); {
	case err == nil:
		if err := copyFile(p.SecretsKey(), filepath.Join(dir, "secrets.key")); err != nil {
			return err
		}
	case errors.Is(err, fs.ErrNotExist):
		absent = append(absent, "secrets.key")
	default:
		return err
	}
	switch _, err := os.Stat(p.Credentials()); {
	case err == nil:
		if err := copyTree(p.Credentials(), filepath.Join(dir, "credentials")); err != nil {
			return err
		}
	case errors.Is(err, fs.ErrNotExist):
		absent = append(absent, "credentials")
	default:
		return err
	}

	version, err := userVersion(db)
	if err != nil {
		return err
	}
	files, err := describe(dir)
	if err != nil {
		return err
	}
	m := Manifest{
		Format:      FormatVersion,
		CreatedAt:   time.Now().UTC(),
		AgentBox:    Version,
		UserVersion: version,
		Files:       files,
		Absent:      absent,
	}
	b, err := marshalManifest(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600)
}

// marshalManifest writes the manifest with one field per line: the restore
// script reads it with bash and no JSON parser, so its shape is part of the
// format. HTML escaping is off so a file name in it stays the name on disk.
func marshalManifest(m Manifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// copyDatabase writes a consistent one-file copy of the database at src to
// dst. The source is opened read-only, so a daemon may keep running and a
// backup can't change what it copies.
func copyDatabase(src, dst string) error {
	db, err := openReadOnly(src, false)
	if err != nil {
		return fmt.Errorf("opening %s read-only: %w", src, err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}
	return nil
}

// openReadOnly opens a database so that nothing in it can be written.
// immutable skips locking and the WAL, for a file nothing else has open: a
// copy being checked, which must not gain a -shm file beside it.
func openReadOnly(path string, immutable bool) (*sql.DB, error) {
	// A URI path is resolved against a working directory that can change
	// under a long-running process, so the file is named absolutely.
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	query := "mode=ro"
	if immutable {
		query += "&immutable=1"
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: query}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func userVersion(path string) (int, error) {
	db, err := openReadOnly(path, true)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

// describe lists every file a backup holds, but its manifest, with the hash
// that proves it arrived whole.
func describe(dir string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == "manifest.json" {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum, _, err := hashFile(path)
		if err != nil {
			return err
		}
		files = append(files, File{Path: rel, Size: info.Size(), Mode: fmt.Sprintf("%04o", info.Mode().Perm()), SHA256: sum})
		return nil
	})
	return files, err
}

// copyTree copies a directory of files, each 0600 in a 0700 directory: these
// are logins and keys, and the backup is as sensitive as the original.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file: refusing to copy it", path)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(dst, 0o600)
}

// Verify checks a backup directory against its manifest without writing
// anything: every listed file's SHA-256 and size, the database's own
// integrity_check, and that the key is a key. It is what makes a backup
// trustworthy before it is the only copy left.
func Verify(dir string) error {
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	if m.Format != FormatVersion {
		return fmt.Errorf("%s is backup format %d, and this AgentBox reads format %d", dir, m.Format, FormatVersion)
	}
	if len(m.Files) == 0 {
		return fmt.Errorf("%s's manifest lists no files", dir)
	}
	present := make(map[string]bool, len(m.Files))
	for _, f := range m.Files {
		if !filepath.IsLocal(f.Path) {
			return fmt.Errorf("%s's manifest names %q, which is not inside the backup", dir, f.Path)
		}
		path := filepath.Join(dir, f.Path)
		sum, size, err := hashFile(path)
		if err != nil {
			return fmt.Errorf("verifying %s: %w", f.Path, err)
		}
		if size != f.Size {
			return fmt.Errorf("%s is %d bytes, and the manifest says %d", f.Path, size, f.Size)
		}
		if sum != f.SHA256 {
			return fmt.Errorf("%s does not match its manifest: it changed, or the copy is incomplete", f.Path)
		}
		present[f.Path] = true
	}
	if !present["state.db"] {
		return fmt.Errorf("%s holds no state.db", dir)
	}
	if err := checkDatabase(filepath.Join(dir, "state.db")); err != nil {
		return err
	}
	if present["secrets.key"] {
		if err := checkKey(filepath.Join(dir, "secrets.key")); err != nil {
			return err
		}
	}
	return nil
}

func readManifest(dir string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Manifest{}, fmt.Errorf("%s is not an AgentBox backup: it has no manifest.json", dir)
		}
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("%s/manifest.json: %w", dir, err)
	}
	return m, nil
}

func checkDatabase(path string) error {
	db, err := openReadOnly(path, true)
	if err != nil {
		return fmt.Errorf("opening the copied database: %w", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(context.Background(), "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("checking the copied database: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		problems = append(problems, line)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(problems) != 1 || problems[0] != "ok" {
		return fmt.Errorf("the copied database fails SQLite's integrity check: %v", problems)
	}
	return nil
}

// checkKey decodes the secrets key the way package secrets reads it: the file
// holds base64, and a restored copy that is truncated or is some other file
// must be caught here rather than when an agent needs a secret.
func checkKey(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(b)))
	if err != nil || len(key) != keySize {
		return fmt.Errorf("%s is not an AgentBox secrets key (%d base64 bytes expected)", path, keySize)
	}
	return nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Size is the number of bytes a backup directory holds, for the line the CLI
// prints after taking one.
func Size(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
