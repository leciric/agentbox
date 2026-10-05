package agent

import (
	"os"
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
	for _, want := range []string{"agentbox desktop record --target auto --input playwright --limit 60s", `--ready "$dir/recording.ready"`, `out="$dir/recording.mp4"`, `echo "$target"`} {
		if !strings.Contains(plain, want) {
			t.Errorf("display script lacks %q", want)
		}
	}
	for _, unwanted := range []string{"x11grab", "input-log", "overlay", "vaapi", "nvenc"} {
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
