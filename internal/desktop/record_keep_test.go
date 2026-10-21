package desktop

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// encodePart encodes seconds of a test pattern the way a recording's part is
// written.
func encodePart(t *testing.T, part string, seconds int) {
	t.Helper()
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=15", "-t", strconv.Itoa(seconds)}, partArgs()...)
	if out, err := exec.Command("ffmpeg", append(args, "-y", part)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
}

func duration(t *testing.T, file string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", file, err)
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return d
}

// A recording is written to its part and kept from it: whole, when it
// finished, and as far as it got when its recorder was killed before it could
// finish, which a file written with +faststart from the start couldn't be.
func TestKeepMakesTheRecordingFromItsPart(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s isn't installed", tool)
		}
	}
	t.Parallel()
	dir := t.TempDir()

	out := filepath.Join(dir, "whole.mp4")
	encodePart(t, PartFile(out), 3)
	if err := keep(PartFile(out), out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if d := duration(t, out); d < 2.9 || d > 3.1 {
		t.Errorf("the finished recording lasts %v s, want 3", d)
	}
	if _, err := os.Stat(PartFile(out)); !os.IsNotExist(err) {
		t.Errorf("the part is left behind: %v", err)
	}
	data, _ := os.ReadFile(out)
	if moov, mdat := strings.Index(string(data), "moov"), strings.Index(string(data), "mdat"); moov < 0 || moov > mdat {
		t.Errorf("the recording's moov isn't before its mdat (%d, %d): the app couldn't show its first frame", moov, mdat)
	}

	// Cut off partway, like a part whose recorder was killed mid-write.
	out = filepath.Join(dir, "killed.mp4")
	encodePart(t, PartFile(out), 6)
	info, _ := os.Stat(PartFile(out))
	if err := os.Truncate(PartFile(out), info.Size()*2/3); err != nil {
		t.Fatal(err)
	}
	if err := keep(PartFile(out), out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if d := duration(t, out); d < 1 {
		t.Errorf("the killed recording kept %v s, want what was written before it was cut off", d)
	}
	if err := exec.Command("ffmpeg", "-v", "error", "-ss", "0.5", "-i", out, "-frames:v", "1", "-f", "null", "-").Run(); err != nil {
		t.Errorf("the killed recording doesn't decode: %v", err)
	}
}

// What ffmpeg can't read is kept as it was written, and no part is nothing
// recorded, which isn't keep's to report.
func TestKeepKeepsWhatItCantRemux(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "r.mp4")
	if err := os.WriteFile(PartFile(out), []byte("not an mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := keep(PartFile(out), out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != "not an mp4" {
		t.Errorf("kept %q, %v; want the part as it was", b, err)
	}
	if err := keep(filepath.Join(dir, "none.part"), filepath.Join(dir, "none.mp4"), io.Discard); err != nil {
		t.Errorf("keep() with no part = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "none.mp4")); !os.IsNotExist(err) {
		t.Errorf("a recording was made from nothing: %v", err)
	}
}
