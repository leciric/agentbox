package api

import (
	"errors"
	"fmt"
)

// The exec stream is how a command runs on an agent's machine when the CLI has
// no runtime of its own to run it with, which on Kubernetes it has not: the
// client opens a WebSocket on the daemon and the daemon runs the command
// through the agent's machine. Binary frames carry raw bytes both ways, and
// the only text frames are the two ends of the conversation: the client
// saying its stdin has ended, and the server saying how the command ended.

// The tags an output frame's first byte carries, so one binary stream can be
// split back into the command's two.
const (
	// ExecStdout is a frame of the command's stdout.
	ExecStdout byte = 1
	// ExecStderr is a frame of its stderr.
	ExecStderr byte = 2
)

// ExecExit is the last text frame of an exec stream: the status the command
// exited with. Error is set instead of a plain status when the command never
// ran, such as an agent that stopped between the check and the run.
type ExecExit struct {
	Exit  int    `json:"exit"`
	Error string `json:"error,omitempty"`
}

// ExecStdin is the text frame a client sends when its stdin ends. The stream
// stays open for the command's output: only an exit frame ends it.
type ExecStdin struct {
	Stdin string `json:"stdin"`
}

// ExecStdinEOF is the value that says no more stdin is coming a command's way.
const ExecStdinEOF = "eof"

// ExecOutput is the frame for one chunk of a command's output: its stream's
// tag, then the bytes themselves, with no encoding in between.
func ExecOutput(stream byte, p []byte) []byte {
	frame := make([]byte, 1+len(p))
	frame[0] = stream
	copy(frame[1:], p)
	return frame
}

// SplitExecOutput reads one output frame back into its stream and its bytes.
func SplitExecOutput(frame []byte) (stream byte, payload []byte, err error) {
	if len(frame) == 0 {
		return 0, nil, errors.New("exec: an output frame with no stream tag")
	}
	switch frame[0] {
	case ExecStdout, ExecStderr:
		return frame[0], frame[1:], nil
	}
	return 0, nil, fmt.Errorf("exec: the stream tag %d is neither stdout (%d) nor stderr (%d)", frame[0], ExecStdout, ExecStderr)
}
