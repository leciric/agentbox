package api

import (
	"bytes"
	"testing"
)

// An output frame carries its stream in the first byte and the command's own
// bytes after it, which is the whole codec: nothing is encoded on the way.
func TestExecOutputRoundTrip(t *testing.T) {
	t.Parallel()
	for _, stream := range []byte{ExecStdout, ExecStderr} {
		payload := []byte("a line\n")
		frame := ExecOutput(stream, payload)
		if frame[0] != stream || !bytes.Equal(frame[1:], payload) {
			t.Fatalf("ExecOutput(%d, %q) = %v", stream, payload, frame)
		}
		gotStream, gotPayload, err := SplitExecOutput(frame)
		if err != nil {
			t.Fatalf("SplitExecOutput(%v) = %v", frame, err)
		}
		if gotStream != stream || !bytes.Equal(gotPayload, payload) {
			t.Errorf("SplitExecOutput(%v) = %d, %q; want %d, %q", frame, gotStream, gotPayload, stream, payload)
		}
	}

	// A command that printed nothing still gets a frame, with no bytes after
	// its tag.
	stream, payload, err := SplitExecOutput(ExecOutput(ExecStdout, nil))
	if err != nil || stream != ExecStdout || len(payload) != 0 {
		t.Errorf("an empty frame = %d, %q, %v; want stdout and nothing", stream, payload, err)
	}
}

// A frame that says nothing about which stream it is, or says something that
// is neither, is refused rather than guessed: printing it on the wrong one
// would mix a command's output into its errors.
func TestSplitExecOutputRefusesWhatItCannotTag(t *testing.T) {
	t.Parallel()
	if _, _, err := SplitExecOutput(nil); err == nil {
		t.Error("an empty frame was accepted")
	}
	for _, frame := range [][]byte{{0}, {3}, {255, 'x'}} {
		if _, _, err := SplitExecOutput(frame); err == nil {
			t.Errorf("the frame %v was accepted", frame)
		}
	}
}
