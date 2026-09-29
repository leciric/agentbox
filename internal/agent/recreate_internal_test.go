package agent

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestFitsHere(t *testing.T) {
	const gib = int64(1) << 30
	for _, c := range []struct {
		memory string
		fits   bool
	}{
		{"", true},
		{"8GiB", true},
		{"16GiB", true},
		{"24GiB", false},
		{"50%", true},
	} {
		err := fitsHere(Limits{Memory: c.memory}, 16*gib)
		if (err == nil) != c.fits {
			t.Errorf("fitsHere(%q) = %v, want fits=%v", c.memory, err, c.fits)
		}
	}
}

// What the old machine's chat sessions were goes into the new one's home with
// its paths, and a symlink as a symlink.
func TestTarDir(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, ".claude", "projects", "-home-u-wt", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(session), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(session, []byte(`{"type":"user"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("s1.jsonl", filepath.Join(filepath.Dir(session), "latest")); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tarDir(&b, dir); err != nil {
		t.Fatal(err)
	}
	r := tar.NewReader(&b)
	var names []string
	for {
		hdr, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entry := hdr.Name
		if hdr.Typeflag == tar.TypeSymlink {
			entry += " -> " + hdr.Linkname
		}
		if hdr.Typeflag == tar.TypeReg {
			data, _ := io.ReadAll(r)
			entry += " = " + string(data)
		}
		names = append(names, entry)
	}
	sort.Strings(names)
	want := []string{
		".claude",
		".claude/projects",
		".claude/projects/-home-u-wt",
		`.claude/projects/-home-u-wt/latest -> s1.jsonl`,
		`.claude/projects/-home-u-wt/s1.jsonl = {"type":"user"}`,
	}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		t.Errorf("archive:\n%s\nwant:\n%s", strings.Join(names, "\n"), strings.Join(want, "\n"))
	}
}
