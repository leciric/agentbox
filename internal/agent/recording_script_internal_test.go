package agent

import (
	"strings"
	"testing"
)

// A display recording is always encoded with libx264, to an MP4 Chromium
// plays; desktop input adds the input log and the quick first pass, and an
// unknown target is refused rather than turned into a script.
func TestStartRecordingScript(t *testing.T) {
	plain, err := startRecordingScript(recordingState{Target: "display", Name: "flow", Limit: 60})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-f x11grab", "-t 60", "-c:v libx264 -preset veryfast -crf 28", "-pix_fmt yuv420p", "+faststart", `"target":"display"`} {
		if !strings.Contains(plain, want) {
			t.Errorf("display script lacks %q", want)
		}
	}
	for _, unwanted := range []string{"input-log", "vaapi", "nvenc"} {
		if strings.Contains(plain, unwanted) {
			t.Errorf("display script has %q", unwanted)
		}
	}

	desktop, err := startRecordingScript(recordingState{Target: "display", Input: RecordInputDesktop, Limit: 60})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agentbox desktop input-log", "timeout 62", "-preset ultrafast -crf 16", "-loglevel info -nostats"} {
		if !strings.Contains(desktop, want) {
			t.Errorf("desktop-input script lacks %q", want)
		}
	}

	android, err := startRecordingScript(recordingState{Target: "android", Limit: 30})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(android, androidScriptPath+` record "$dir/recording.mp4" 30`) || strings.Contains(android, "x11grab") {
		t.Errorf("android script doesn't record the device:\n%s", android)
	}

	if _, err := startRecordingScript(recordingState{Target: "phone"}); err == nil {
		t.Error("an unknown target made a script")
	}
}
