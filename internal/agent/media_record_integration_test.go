//go:build integration

package agent

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDesktopRecordingShowsTheCursorAndTheKeys runs the scripts StartRecording
// and StopRecording generate, against a display of this machine's own rather
// than an agent's, and checks that what comes out shows the pointer moving and
// the keys pressed. It needs :99 up with a desktop on it, as browser.sh leaves
// it, and ffmpeg, xdotool and screenkey:
//
//	Xvnc :99 -geometry 1440x900 -depth 24 -rfbport 5900 -localhost -SecurityTypes None &
//	DISPLAY=:99 openbox & DISPLAY=:99 tint2 & DISPLAY=:99 xfce4-terminal &
//	go test -tags integration ./internal/agent -run DesktopRecording -v
//
// AGENTBOX_KEEP_RECORDING=<dir> keeps the video and the frames it checked, to
// look at them by eye.
func TestDesktopRecordingShowsTheCursorAndTheKeys(t *testing.T) {
	requireDisplay(t)
	home := t.TempDir()
	if keep := os.Getenv("AGENTBOX_KEEP_RECORDING"); keep != "" {
		home = keep
	}
	dir := filepath.Join(home, agentStateDir)

	st := recordingState{Target: "display", Input: RecordInputDesktop, Name: "desktop input", Source: "user", StartedAt: time.Now().UTC(), Limit: 30}
	start, err := startRecordingScript(st)
	if err != nil {
		t.Fatal(err)
	}
	runShell(t, home, start)

	if _, err := os.Stat(filepath.Join(dir, "screenkey.pid")); err != nil {
		t.Fatalf("screenkey left no pid: %v", err)
	}
	// Drive the display the way the desktop tools do: XTEST, which is what
	// makes a recording worth this mode at all.
	for _, at := range [][2]string{{"300", "300"}, {"700", "420"}, {"900", "500"}} {
		runShell(t, home, "DISPLAY=:99 xdotool mousemove "+at[0]+" "+at[1]+"; sleep 0.6")
	}
	runShell(t, home, `DISPLAY=:99 xdotool click 1; sleep 0.4; DISPLAY=:99 xdotool type --delay 120 'agentbox'; sleep 0.4; DISPLAY=:99 xdotool key ctrl+s; sleep 1.5`)

	out := runShell(t, home, stopRecordingScript())
	if !strings.Contains(out, `"input":"desktop"`) {
		t.Errorf("the recording's details don't name the input mode: %s", out)
	}
	video := filepath.Join(dir, "recording.mp4")
	if info, err := os.Stat(video); err != nil || info.Size() == 0 {
		t.Fatalf("no video: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "screenkey.pid")); !os.IsNotExist(err) {
		t.Errorf("stopping left screenkey's pid file behind: %v", err)
	}
	// The bracket keeps the pattern from matching the shell running pgrep.
	if out := runShell(t, home, "pgrep -f 'bin/[s]creenkey' || true"); strings.TrimSpace(out) != "" {
		t.Errorf("screenkey is still running after the recording stopped: %s", out)
	}

	// The typing is in the last second and the pointer was still at the top
	// left in the first, so both the overlay band and the spot the cursor
	// moved to differ between the two frames.
	first, last := filepath.Join(home, "first.png"), filepath.Join(home, "last.png")
	runShell(t, home, "ffmpeg -loglevel error -i "+video+" -frames:v 1 -y "+first)
	runShell(t, home, "ffmpeg -loglevel error -sseof -1.5 -i "+video+" -frames:v 1 -y "+last)
	for _, region := range []struct{ name, crop string }{
		{"the key overlay", "1440:64:0:808"},
		{"the mouse cursor", "60:60:880:480"},
	} {
		if same(t, home, first, last, region.crop) {
			t.Errorf("%s isn't in the recording: %s is identical in the first and last frames", region.name, region.crop)
		}
	}
	t.Logf("recording %s, frames %s and %s", video, first, last)
}

// same reports whether a region is byte-identical in two frames.
func same(t *testing.T, home, a, b, crop string) bool {
	t.Helper()
	cut := func(src, dst string) []byte {
		runShell(t, home, "ffmpeg -loglevel error -i "+src+" -vf crop="+crop+" -y "+dst)
		content, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	return bytes.Equal(cut(a, filepath.Join(home, "a.png")), cut(b, filepath.Join(home, "b.png")))
}

func requireDisplay(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/tmp/.X11-unix/X99"); err != nil {
		t.Skip("no display on :99")
	}
	for _, tool := range []string{"ffmpeg", "ffprobe", "xdotool", "screenkey"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s isn't installed", tool)
		}
	}
}

func runShell(t *testing.T, home, script string) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s\n--- script ---\n%s", err, out, script)
	}
	return string(out)
}
