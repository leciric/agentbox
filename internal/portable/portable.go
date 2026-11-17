// Package portable is the file format AgentBox moves its data in: one zip,
// encrypted as a whole with a passphrase, holding a versioned manifest and one
// entry per section of data (a kind of data, for one project or for the whole
// installation).
//
// It knows nothing of what the sections hold: package transfer fills and reads
// them. That split is deliberate, so a later sync between instances can reuse
// the format (stable entry IDs, per-entry hashes, the schema check and the
// encryption) without the export and import that sit on top of it.
//
// The layout of the zip, in this order:
//
//	manifest.json                 Manifest: format, schema, app version, entries
//	projects/<project>/<kind>.json  one per project and kind of data
//	projects/<project>/repository.bundle  a git bundle, when one was asked for
//	installation/<kind>.json      what belongs to no project (logins)
//
// Encrypted, the zip is wrapped in age's passphrase (scrypt) mode: the
// manifest, the names of the entries and their contents are all inside it.
package portable

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
)

// Format names the file in its manifest, so a zip that isn't one of ours is
// told apart from one that is but is newer.
const Format = "agentbox-export"

// Schema is the version of the layout this AgentBox writes and the newest it
// reads. Bump it when an entry's meaning changes in a way an older reader
// would get wrong; adding a kind of entry, or a field older readers ignore,
// doesn't need it.
const Schema = 1

const manifestName = "manifest.json"

// ageHeader is how an age file starts; a zip starts with "PK".
const ageHeader = "age-encryption.org/"

// ScryptWorkFactor is age's scrypt cost (log2 N) for files this package
// encrypts: age's own default, about a second on a laptop. Tests lower it.
var ScryptWorkFactor = 18

var (
	// ErrPassphraseNeeded is opening an encrypted file without a passphrase.
	ErrPassphraseNeeded = errors.New("this export is encrypted: give its passphrase")
	// ErrWrongPassphrase is opening it with the wrong one. age can't tell a
	// wrong passphrase from a damaged header, so neither can this.
	ErrWrongPassphrase = errors.New("wrong passphrase: this export can't be opened with it")
	// ErrNotExport is a file that is neither an age file nor a zip with our
	// manifest.
	ErrNotExport = errors.New("this isn't an AgentBox export")
)

// NewerSchemaError is an archive written by a newer AgentBox than this one.
type NewerSchemaError struct {
	Schema int
	App    string
}

func (e *NewerSchemaError) Error() string {
	return fmt.Sprintf("this export was written by AgentBox %s in format %d, and this AgentBox reads up to format %d: update AgentBox, then import it again", e.App, e.Schema, Schema)
}

// Manifest is manifest.json: what the archive is and what is in it.
type Manifest struct {
	Format string `json:"format"`
	Schema int    `json:"schema"`
	// App is the AgentBox version that wrote it.
	App string `json:"app"`
	// ID is this archive's own, random; a sync would key its snapshots on it.
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Entries   []Entry   `json:"entries"`
}

// Entry is one section: one kind of data, for one project or for none. ID is
// stable across exports of the same data ("project/<name>/memory",
// "installation/github"), which is what lets an importer choose sections by ID
// and a sync compare them by hash.
type Entry struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Project string `json:"project,omitempty"`
	// Path is where it is in the zip.
	Path string `json:"path"`
	// Items is how many things it holds (rows, accounts), for showing the
	// archive's contents before importing it.
	Items  int    `json:"items"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// Meta is what an importer wants to show without opening the entry,
	// such as a repository's remote URL.
	Meta map[string]string `json:"meta,omitempty"`
}

// Find is the entry with an ID.
func (m Manifest) Find(id string) (Entry, bool) {
	for _, e := range m.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Writer builds an archive. Entries are staged in a directory as they are
// added, since the manifest that leads the zip lists them all with their
// hashes; Finish writes the zip.
type Writer struct {
	dir     string
	entries []Entry
	files   []string
}

// NewWriter stages in a new directory under tmp ("" for the system's).
func NewWriter(tmp string) (*Writer, error) {
	dir, err := os.MkdirTemp(tmp, "agentbox-export-")
	if err != nil {
		return nil, err
	}
	return &Writer{dir: dir}, nil
}

// Add stages an entry written by write. e.Path is filled in from its project
// and kind (with ext, such as ".json") when empty; Size and SHA256 always are.
func (w *Writer) Add(e Entry, ext string, write func(io.Writer) error) (Entry, error) {
	if e.ID == "" || e.Kind == "" {
		return e, errors.New("an entry needs an ID and a kind")
	}
	for _, have := range w.entries {
		if have.ID == e.ID {
			return e, fmt.Errorf("entry %s added twice", e.ID)
		}
	}
	if e.Path == "" {
		e.Path = EntryPath(e.Project, e.Kind, ext)
	}
	staged := filepath.Join(w.dir, fmt.Sprintf("%04d", len(w.entries)))
	f, err := os.Create(staged)
	if err != nil {
		return e, err
	}
	sum := sha256.New()
	counted := &countWriter{w: io.MultiWriter(f, sum)}
	buf := bufio.NewWriter(counted)
	err = write(buf)
	if err == nil {
		err = buf.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(staged)
		return e, err
	}
	e.Size, e.SHA256 = counted.n, hex.EncodeToString(sum.Sum(nil))
	w.entries = append(w.entries, e)
	w.files = append(w.files, staged)
	return e, nil
}

// AddJSON stages v as a JSON entry.
func (w *Writer) AddJSON(e Entry, v any) (Entry, error) {
	return w.Add(e, ".json", func(out io.Writer) error {
		enc := json.NewEncoder(out)
		enc.SetIndent("", " ")
		return enc.Encode(v)
	})
}

// EntryPath is where an entry of a project, or of the installation when
// project is "", goes in the zip.
func EntryPath(project, kind, ext string) string {
	if project == "" {
		return "installation/" + kind + ext
	}
	return "projects/" + project + "/" + kind + ext
}

// Entries is what has been staged so far.
func (w *Writer) Entries() []Entry { return w.entries }

// Finish writes the archive to out, encrypted with passphrase unless it is
// "", and removes what was staged. It returns the manifest written.
func (w *Writer) Finish(out io.Writer, app, passphrase string) (Manifest, error) {
	defer w.Discard()
	m := Manifest{Format: Format, Schema: Schema, App: app, ID: newID(), CreatedAt: time.Now().UTC().Truncate(time.Second), Entries: w.entries}
	if m.Entries == nil {
		m.Entries = []Entry{}
	}
	dst := out
	var enc io.WriteCloser
	if passphrase != "" {
		r, err := age.NewScryptRecipient(passphrase)
		if err != nil {
			return m, err
		}
		r.SetWorkFactor(ScryptWorkFactor)
		if enc, err = age.Encrypt(out, r); err != nil {
			return m, err
		}
		dst = enc
	}
	zw := zip.NewWriter(dst)
	manifest, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return m, err
	}
	if err := writeZipEntry(zw, manifestName, bytes.NewReader(manifest), zip.Deflate); err != nil {
		return m, err
	}
	for i, e := range w.entries {
		f, err := os.Open(w.files[i])
		if err != nil {
			return m, err
		}
		// A git bundle is compressed already.
		method := zip.Deflate
		if !strings.HasSuffix(e.Path, ".json") {
			method = zip.Store
		}
		err = writeZipEntry(zw, e.Path, f, method)
		_ = f.Close()
		if err != nil {
			return m, err
		}
	}
	if err := zw.Close(); err != nil {
		return m, err
	}
	if enc != nil {
		if err := enc.Close(); err != nil {
			return m, err
		}
	}
	return m, nil
}

// Discard removes what was staged; Finish does it itself.
func (w *Writer) Discard() { _ = os.RemoveAll(w.dir) }

func writeZipEntry(zw *zip.Writer, name string, r io.Reader, method uint16) error {
	f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: time.Now()})
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	return err
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "exp_" + hex.EncodeToString(b)
}

// Reader is an opened archive.
type Reader struct {
	Manifest  Manifest
	Encrypted bool

	zip   *zip.ReadCloser
	plain string // the decrypted copy, removed by Close
	files map[string]*zip.File
}

// Encrypted says whether the file at path is an encrypted export, so a caller
// knows to ask for a passphrase before opening it.
func Encrypted(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, len(ageHeader))
	if _, err := io.ReadFull(f, head); err != nil {
		return false, ErrNotExport
	}
	return string(head) == ageHeader, nil
}

// Open opens the archive at path. An encrypted one is decrypted into a file
// under tmp first (zip needs to seek), which Close removes. A newer schema is
// a *NewerSchemaError.
func Open(path, passphrase, tmp string) (*Reader, error) {
	encrypted, err := Encrypted(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{Encrypted: encrypted}
	zipPath := path
	if encrypted {
		if passphrase == "" {
			return nil, ErrPassphraseNeeded
		}
		if r.plain, err = decrypt(path, passphrase, tmp); err != nil {
			return nil, err
		}
		zipPath = r.plain
	}
	if r.zip, err = zip.OpenReader(zipPath); err != nil {
		r.Close()
		if errors.Is(err, zip.ErrFormat) {
			return nil, ErrNotExport
		}
		return nil, err
	}
	r.files = map[string]*zip.File{}
	for _, f := range r.zip.File {
		r.files[f.Name] = f
	}
	if err := r.readManifest(); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

func decrypt(path, passphrase, tmp string) (string, error) {
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return "", err
	}
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	plain, err := age.Decrypt(bufio.NewReader(in), id)
	if err != nil {
		var wrong *age.NoIdentityMatchError
		if errors.As(err, &wrong) {
			return "", ErrWrongPassphrase
		}
		return "", fmt.Errorf("decrypting the export: %w", err)
	}
	out, err := os.CreateTemp(tmp, "agentbox-import-*.zip")
	if err != nil {
		return "", err
	}
	if err := out.Chmod(0o600); err != nil {
		_ = out.Close()
		_ = os.Remove(out.Name())
		return "", err
	}
	_, err = io.Copy(out, plain)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("decrypting the export: %w", err)
	}
	return out.Name(), nil
}

func (r *Reader) readManifest() error {
	f, ok := r.files[manifestName]
	if !ok {
		return ErrNotExport
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	// The format and schema are read first and alone, so a newer archive is
	// refused for being newer rather than for a field this one can't parse.
	raw, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	var head struct {
		Format string `json:"format"`
		Schema int    `json:"schema"`
		App    string `json:"app"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.Format != Format {
		return ErrNotExport
	}
	if head.Schema > Schema {
		return &NewerSchemaError{Schema: head.Schema, App: head.App}
	}
	return json.Unmarshal(raw, &r.Manifest)
}

// Open reads an entry, checking it against the manifest's hash: a read that
// reaches the end of a damaged entry fails.
func (r *Reader) Open(id string) (io.ReadCloser, error) {
	e, ok := r.Manifest.Find(id)
	if !ok {
		return nil, fmt.Errorf("the export has no %s", id)
	}
	f, ok := r.files[e.Path]
	if !ok {
		return nil, fmt.Errorf("the export lists %s but doesn't hold it", id)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	return &checked{rc: rc, sum: sha256.New(), want: e.SHA256, id: id}, nil
}

// DecodeJSON reads a JSON entry into v.
func (r *Reader) DecodeJSON(id string, v any) error {
	rc, err := r.Open(id)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	dec := json.NewDecoder(rc)
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("reading %s: %w", id, err)
	}
	// Read to the end, so the hash is checked.
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return err
	}
	return nil
}

// Extract writes an entry to a file at path.
func (r *Reader) Extract(id, path string) error {
	rc, err := r.Open(id)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, rc)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// Close closes the archive and removes its decrypted copy.
func (r *Reader) Close() {
	if r.zip != nil {
		_ = r.zip.Close()
	}
	if r.plain != "" {
		_ = os.Remove(r.plain)
	}
}

type checked struct {
	rc   io.ReadCloser
	sum  hash.Hash
	want string
	id   string
}

func (c *checked) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	c.sum.Write(p[:n])
	if errors.Is(err, io.EOF) && hex.EncodeToString(c.sum.Sum(nil)) != c.want {
		return n, fmt.Errorf("%s is damaged: its contents don't match the manifest", c.id)
	}
	return n, err
}

func (c *checked) Close() error { return c.rc.Close() }
