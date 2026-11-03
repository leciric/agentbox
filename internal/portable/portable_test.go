package portable

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func init() { ScryptWorkFactor = 10 }

func write(t *testing.T, passphrase string, add func(w *Writer)) string {
	t.Helper()
	w, err := NewWriter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	add(w)
	path := filepath.Join(t.TempDir(), "out.agentbox")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Finish(f, "1.2.3", passphrase); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRoundTrip(t *testing.T) {
	for _, pass := range []string{"", "correct horse"} {
		path := write(t, pass, func(w *Writer) {
			if _, err := w.AddJSON(Entry{ID: "project/web/notes", Kind: "notes", Project: "web", Items: 1}, map[string]string{"text": "hi"}); err != nil {
				t.Fatal(err)
			}
			if _, err := w.Add(Entry{ID: "project/web/repository", Kind: "repository", Project: "web"}, ".bundle", func(out io.Writer) error {
				_, err := out.Write([]byte("bundle bytes"))
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
		encrypted, err := Encrypted(path)
		if err != nil || encrypted != (pass != "") {
			t.Fatalf("Encrypted = %v, %v", encrypted, err)
		}
		r, err := Open(path, pass, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if r.Manifest.Schema != Schema || r.Manifest.App != "1.2.3" || len(r.Manifest.Entries) != 2 {
			t.Fatalf("manifest = %+v", r.Manifest)
		}
		if e := r.Manifest.Entries[1]; e.Path != "projects/web/repository.bundle" || e.Size != 12 {
			t.Fatalf("entry = %+v", e)
		}
		var notes map[string]string
		if err := r.DecodeJSON("project/web/notes", &notes); err != nil || notes["text"] != "hi" {
			t.Fatalf("notes = %v, %v", notes, err)
		}
		out := filepath.Join(t.TempDir(), "b")
		if err := r.Extract("project/web/repository", out); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(out); string(b) != "bundle bytes" {
			t.Fatalf("bundle = %q", b)
		}
		r.Close()
	}
}

func TestPassphrase(t *testing.T) {
	path := write(t, "right", func(*Writer) {})
	if _, err := Open(path, "", t.TempDir()); !errors.Is(err, ErrPassphraseNeeded) {
		t.Fatalf("no passphrase: %v", err)
	}
	tmp := t.TempDir()
	if _, err := Open(path, "wrong", tmp); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("a failed open left %d files behind", len(left))
	}
}

// zipWith writes a plain zip holding one manifest.
func zipWith(t *testing.T, manifest any) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create(manifestName)
	_ = json.NewEncoder(f).Encode(manifest)
	_ = zw.Close()
	path := filepath.Join(t.TempDir(), "x.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewerSchemaIsRefused(t *testing.T) {
	path := zipWith(t, map[string]any{"format": Format, "schema": Schema + 1, "app": "9.0.0", "entries": "a shape this version can't read"})
	_, err := Open(path, "", t.TempDir())
	var newer *NewerSchemaError
	if !errors.As(err, &newer) || newer.App != "9.0.0" {
		t.Fatalf("err = %v", err)
	}
}

func TestNotAnExport(t *testing.T) {
	path := zipWith(t, map[string]any{"format": "something else"})
	if _, err := Open(path, "", t.TempDir()); !errors.Is(err, ErrNotExport) {
		t.Fatalf("other zip: %v", err)
	}
	text := filepath.Join(t.TempDir(), "t")
	_ = os.WriteFile(text, []byte("hello, not a zip at all"), 0o600)
	if _, err := Open(text, "", t.TempDir()); !errors.Is(err, ErrNotExport) {
		t.Fatalf("text file: %v", err)
	}
}

func TestDamagedEntryFails(t *testing.T) {
	path := write(t, "", func(w *Writer) {
		_, _ = w.AddJSON(Entry{ID: "a", Kind: "notes", Project: "p"}, "text")
	})
	r, err := Open(path, "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.Manifest.Entries[0].SHA256 = "00"
	var s string
	if err := r.DecodeJSON("a", &s); err == nil {
		t.Fatal("a damaged entry read without an error")
	}
}
