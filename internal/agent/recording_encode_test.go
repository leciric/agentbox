package agent

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRecordingsPlayInChromium encodes a clip with the arguments a recording
// is made with, from frames
// like x11grab's (RGB, at a size with odd sides), and checks with ffprobe that
// the file is one the app plays and draws a thumbnail of: H.264 in 4:2:0, not
// High 4:4:4, with its moov before its mdat, and the duration it was given.
func TestRecordingsPlayInChromium(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s isn't installed", tool)
		}
	}
	t.Parallel()
	dir := t.TempDir()
	const source = `-f lavfi -i 'testsrc2=size=1365x767:rate=15,format=bgr0' -t 2`
	cases := []struct{ name, command string }{
		{"recording", "ffmpeg -hide_banner -loglevel error -y " + source + " " + RecordingOutputArgs() + " recording.mp4"},
	}
	for _, c := range cases {
		cmd := exec.Command("sh", "-c", c.command)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", c.name, err, out)
		}
	}
	for _, file := range []string{"recording.mp4"} {
		path := filepath.Join(dir, file)
		out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
			"-show_entries", "stream=codec_name,profile,pix_fmt,width,height:format=duration", "-of", "default=nw=1", path).Output()
		if err != nil {
			t.Fatalf("%s: ffprobe: %v", file, err)
		}
		got := map[string]string{}
		for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				got[k] = v
			}
		}
		if got["codec_name"] != "h264" || got["pix_fmt"] != "yuv420p" || strings.Contains(got["profile"], "4:4:4") {
			t.Errorf("%s is %s %s %s, want h264 in yuv420p", file, got["codec_name"], got["profile"], got["pix_fmt"])
		}
		if got["width"] != "1364" || got["height"] != "766" {
			t.Errorf("%s is %sx%s, want 1364x766: H.264 in 4:2:0 needs even sides", file, got["width"], got["height"])
		}
		if d, _ := strconv.ParseFloat(got["duration"], 64); d < 1.9 || d > 2.1 {
			t.Errorf("%s lasts %s s, want 2", file, got["duration"])
		}
		if atoms := topLevelAtoms(t, path); !before(atoms, "moov", "mdat") {
			t.Errorf("%s's atoms are %v: without moov first, the app has to fetch the end of the file before it can show a frame", file, atoms)
		}
		// The frame the Media grid's thumbnail shows, at #t=0.5.
		if out, err := exec.Command("ffmpeg", "-v", "error", "-ss", "0.5", "-i", path, "-frames:v", "1", "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
			t.Errorf("%s: decoding the frame at 0.5 s: %v %s", file, err, out)
		}
	}
}

// topLevelAtoms lists an MP4's top-level boxes, in order.
func topLevelAtoms(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var atoms []string
	for i := 0; i+8 <= len(data); {
		size := uint64(binary.BigEndian.Uint32(data[i:]))
		atoms = append(atoms, string(data[i+4:i+8]))
		switch size {
		case 0:
			return atoms
		case 1:
			if i+16 > len(data) {
				return atoms
			}
			size = binary.BigEndian.Uint64(data[i+8:])
		}
		if size < 8 {
			t.Fatalf("%s: a box of %d bytes at %d", path, size, i)
		}
		i += int(size)
	}
	return atoms
}

func before(atoms []string, first, then string) bool {
	a, b := -1, -1
	for i, atom := range atoms {
		if atom == first && a < 0 {
			a = i
		}
		if atom == then && b < 0 {
			b = i
		}
	}
	return a >= 0 && b >= 0 && a < b
}
