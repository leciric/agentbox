package agent

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestCmpOrFallsBackOnlyWhenEmpty(t *testing.T) {
	if got := cmpOr("chosen", "fallback"); got != "chosen" {
		t.Errorf("cmpOr(chosen, fallback) = %q", got)
	}
	if got := cmpOr("", "fallback"); got != "fallback" {
		t.Errorf(`cmpOr("", fallback) = %q`, got)
	}
}

// TestFileNameSanitizesAndCapsTheBaseName checks the three things AddMedia
// relies on: nothing unsafe survives into a filename, an empty name falls
// back, and the extension is added once, never doubled.
func TestFileNameSanitizesAndCapsTheBaseName(t *testing.T) {
	cases := []struct{ name, fallback, ext, want string }{
		{"my report!! v2", "fallback", ".png", "my-report-v2.png"},
		{"", "screenshot", ".png", "screenshot.png"},
		{"  ", "screenshot", ".png", "screenshot.png"},
		{"already.png", "fallback", ".png", "already.png"},
		{"../../etc/passwd", "fallback", "", "etc-passwd"},
		{"no-ext-wanted", "fallback", "", "no-ext-wanted"},
	}
	for _, c := range cases {
		if got := fileName(c.name, c.fallback, c.ext); got != c.want {
			t.Errorf("fileName(%q, %q, %q) = %q, want %q", c.name, c.fallback, c.ext, got, c.want)
		}
	}
	long := ""
	for i := 0; i < 200; i++ {
		long += "a"
	}
	if got := fileName(long, "fallback", ".log"); len(got) > 80+len(".log") {
		t.Errorf("fileName() of a long name wasn't capped: %d bytes (%q)", len(got), got)
	}
}

func TestGuessKindReadsTheExtension(t *testing.T) {
	cases := []struct {
		name            string
		isDir, hasIndex bool
		want            string
	}{
		{"shot.png", false, false, MediaScreenshot},
		{"shot.JPG", false, false, MediaScreenshot},
		{"clip.mp4", false, false, MediaRecording},
		{"results.xml", false, false, MediaReport},
		{"report.html", false, false, MediaReport},
		{"build.log", false, false, MediaLog},
		{"notes.txt", false, false, MediaLog},
		{"data.bin", false, false, MediaFile},
		{"coverage", true, true, MediaReport},
	}
	for _, c := range cases {
		if got := guessKind(c.name, c.isDir, c.hasIndex); got != c.want {
			t.Errorf("guessKind(%q, dir=%v, index=%v) = %q, want %q", c.name, c.isDir, c.hasIndex, got, c.want)
		}
	}
}

func TestMimeTypeKnowsTheKindsAddMediaProduces(t *testing.T) {
	cases := map[string]string{
		"clip.mp4":    "video/mp4",
		"clip.webm":   "video/webm",
		"build.log":   "text/plain; charset=utf-8",
		"README.md":   "text/markdown; charset=utf-8",
		"shot.png":    "image/png",
		"unknown.zzz": "",
	}
	for file, want := range cases {
		if got := mimeType(file); got != want {
			t.Errorf("mimeType(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestPNGSizeReadsDimensionsWithoutDecodingThePixels(t *testing.T) {
	file := filepath.Join(t.TempDir(), "shot.png")
	img := image.NewRGBA(image.Rect(0, 0, 37, 21))
	for x := 0; x < 37; x++ {
		img.Set(x, 0, color.White)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if w, h := pngSize(file); w != 37 || h != 21 {
		t.Errorf("pngSize() = %d, %d, want 37, 21", w, h)
	}
	if w, h := pngSize(filepath.Join(t.TempDir(), "missing.png")); w != 0 || h != 0 {
		t.Errorf("pngSize() of a missing file = %d, %d, want 0, 0", w, h)
	}
	notPNG := filepath.Join(t.TempDir(), "not-a-png.png")
	if err := os.WriteFile(notPNG, []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w, h := pngSize(notPNG); w != 0 || h != 0 {
		t.Errorf("pngSize() of a corrupt file = %d, %d, want 0, 0", w, h)
	}
}

func TestFileSHA256MatchesAKnownDigest(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(file, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	// sha256("hello world")
	want := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	got, err := fileSHA256(file)
	if err != nil || got != want {
		t.Errorf("fileSHA256() = %q, %v, want %q", got, err, want)
	}
	if _, err := fileSHA256(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("fileSHA256() of a missing file should fail")
	}
}

func TestCopyPathCopiesFilesAndDirectories(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst.txt")
	if err := copyPath(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "content" {
		t.Errorf("copyPath() file = %q, %v", got, err)
	}

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(t.TempDir(), "out")
	if err := copyPath(srcDir, dstDir); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dstDir, "index.html")); err != nil || string(got) != "<html></html>" {
		t.Errorf("copyPath() dir = %q, %v", got, err)
	}

	if err := copyPath(filepath.Join(t.TempDir(), "missing"), dst); err == nil {
		t.Error("copyPath() of a missing source should fail")
	}
}
