package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The display and the browser are recorded by the agentbox binary in one pass,
// overlay and all; a staged recording is written to the recordings device, and
// an unknown target is refused rather than turned into a script.
func TestStartRecordingScript(t *testing.T) {
	plain, err := startRecordingScript(recordingState{Target: "auto", Input: RecordInputPlaywright, Name: "flow", Limit: 60})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agentbox desktop record --target auto --input playwright --limit 60s", `--ready "$dir/recording.ready"`, `--cuts "$dir/recording.cuts"`, `out="$dir/recording.mp4"`, `echo "$target"`} {
		if !strings.Contains(plain, want) {
			t.Errorf("display script lacks %q", want)
		}
	}
	for _, unwanted := range []string{"x11grab", "input-log", "overlay", "vaapi", "nvenc", "--no-trim"} {
		if strings.Contains(plain, unwanted) {
			t.Errorf("display script has %q", unwanted)
		}
	}

	staged, err := startRecordingScript(recordingState{Target: "browser", Input: RecordInputDesktop, Limit: 60, Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--target browser --input desktop", `out="$dir/out/recording.mp4"`, `"staged":true`} {
		if !strings.Contains(staged, want) {
			t.Errorf("staged script lacks %q", want)
		}
	}

	whole, err := startRecordingScript(recordingState{Target: "display", Input: RecordInputDesktop, Limit: 60, NoTrim: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(whole, "--bottom 38 --no-trim --ready") {
		t.Errorf("a recording kept whole isn't recorded with --no-trim:\n%s", whole)
	}

	android, err := startRecordingScript(recordingState{Target: "android", Limit: 30})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(android, androidScriptPath+` record "$out" 30`) || strings.Contains(android, "desktop record") {
		t.Errorf("android script doesn't record the device:\n%s", android)
	}

	if _, err := startRecordingScript(recordingState{Target: "phone"}); err == nil {
		t.Error("an unknown target made a script")
	}
}

// A recording the agent left on its stage is moved into the media item, unless
// it's a link, which saving it would follow out of the agent's reach.
func TestTakeStaged(t *testing.T) {
	dir := t.TempDir()
	staged, file := filepath.Join(dir, "recording.mp4"), filepath.Join(dir, "item.mp4")
	if err := os.WriteFile(staged, []byte("mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := takeStaged(staged, file); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(file); string(data) != "mp4" {
		t.Errorf("the item holds %q", data)
	}

	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, staged); err != nil {
		t.Fatal(err)
	}
	if err := takeStaged(staged, filepath.Join(dir, "other.mp4")); err == nil {
		t.Error("a link was taken as a recording")
	}
	if _, err := os.Lstat(staged); !os.IsNotExist(err) {
		t.Error("the link is still on the stage")
	}
	if err := takeStaged(staged, file); err == nil {
		t.Error("an empty stage gave a recording")
	}
}

// stopRecording runs the stop script in a home whose recording is the one
// given: its details, a pid that has exited, the recorder's log and files.
func stopRecording(t *testing.T, staged bool, files map[string]string) (string, string, error) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, agentStateDir)
	if err := os.MkdirAll(filepath.Join(dir, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	details := `{"name":"flow","target":"display","staged":false}`
	if staged {
		details = `{"name":"flow","target":"display","staged":true}`
	}
	files["recording.json"] = details
	files["recording.pid"] = "2147483646" // no such process: the recorder had stopped
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", stopRecordingScript())
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return dir, stdout.String() + stderr.String(), err
}

// A recorder that was killed, or crashed, before it finished the file left
// what it captured in its part: stopping keeps that as the recording, rather
// than saying it is empty and deleting it (agent-80's lost recording).
func TestStopRecordingKeepsTheRecordersPart(t *testing.T) {
	for _, staged := range []bool{false, true} {
		out := "recording.mp4"
		if staged {
			out = "out/recording.mp4"
		}
		dir, output, err := stopRecording(t, staged, map[string]string{out + ".part": "frames so far", "recording.log": ""})
		if err != nil {
			t.Fatalf("staged=%v: stop = %v\n%s", staged, err, output)
		}
		// Not an MP4, so ffmpeg (if there is one) can't remux it: kept as it is.
		if b, err := os.ReadFile(filepath.Join(dir, out)); err != nil || string(b) != "frames so far" {
			t.Errorf("staged=%v: the recording is %q, %v; want the part", staged, b, err)
		}
		if !strings.Contains(output, `"name":"flow"`) {
			t.Errorf("staged=%v: the details weren't printed: %s", staged, output)
		}
	}
}

// With nothing captured, stopping says so, and what the recorder said.
func TestStopRecordingSaysWhyNothingWasRecorded(t *testing.T) {
	_, output, err := stopRecording(t, false, map[string]string{"recording.log": "Error: the display isn't running: start the browser first\n"})
	if err == nil {
		t.Fatalf("stop with nothing recorded succeeded: %s", output)
	}
	for _, want := range []string{"nothing was recorded: the recorder had already stopped by itself", "the display isn't running"} {
		if !strings.Contains(output, want) {
			t.Errorf("stop said %q, want %q in it", output, want)
		}
	}
}
