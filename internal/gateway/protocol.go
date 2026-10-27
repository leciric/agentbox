// Package gateway is the one connection a pod opens out to this machine: the
// master's way to run a command, read a file, open an agent's terminal or reach
// its desktop, when the agent's machine is a pod in a cluster and there is no
// local socket to talk to.
//
// A pod is not on this machine's network and cannot be connected to. It opens a
// WebSocket to the master, authenticates with the token it was given when it was
// made, and keeps it open; the master multiplexes streams over it with yamux. So
// every operation is master to pod, in one direction, and the cluster needs no
// ingress.
//
// Each stream begins with a small JSON header naming the operation, then carries
// frames: stdin, stdout, stderr, an exit code, a terminal resize, or raw bytes.
// Nothing is buffered whole, so a 100 MiB file and an interactive terminal both
// work through the same connection.
package gateway

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Path is where a pod connects, and the route the daemon serves it on.
const Path = "/v1/agent-gateway"

// Subprotocol is what both ends ask for, so a proxy in between doesn't hand the
// socket to something that speaks another one.
const Subprotocol = "agentbox-gateway/1"

// The operations a stream can ask for.
const (
	// OpExec runs argv in the pod and streams what it prints.
	OpExec = "exec"
	// OpRead streams a file's bytes.
	OpRead = "read"
	// OpWrite writes a stream's bytes to a file.
	OpWrite = "write"
	// OpList lists a directory.
	OpList = "list"
	// OpDial opens a TCP connection from the pod, for a desktop's VNC or a
	// browser's debugging port, and carries the bytes both ways.
	OpDial = "dial"
	// OpAttach opens a terminal: the agent's shell, with resizes.
	OpAttach = "attach"
	// OpControl is a request with one JSON answer: stop an agent, read a stat.
	OpControl = "control"
)

// The frame types on a stream. The first frame after the header decides what the
// rest of the stream means.
const (
	// FrameStdin is bytes for a command or a terminal.
	FrameStdin byte = 0
	// FrameStdout is what the command printed.
	FrameStdout byte = 1
	// FrameStderr is what it printed to its error stream.
	FrameStderr byte = 2
	// FrameExit is the command's exit code, as one byte. It ends an exec or an
	// attach stream.
	FrameExit byte = 3
	// FrameResize is a terminal's size: two big-endian uint16s, columns then
	// rows.
	FrameResize byte = 4
	// FrameData is raw bytes: a file's content, or a connection's payload.
	FrameData byte = 5
)

// The limits one stream and one connection keep to.
const (
	// MaxFrame is the largest payload a frame carries.
	MaxFrame = 1 << 20
	// MaxHeader is the largest stream header.
	MaxHeader = 64 << 10
	// MaxStreams is how many streams one pod may have open at once. A busy app
	// has a terminal, a chat, a file read and a desktop at once, and not many
	// more.
	MaxStreams = 256
	// MaxHeaderBytes bounds the JSON header a master sends.
	MaxHeaderBytes = 64 << 10
)

// ErrNotConnected is the error for an agent whose pod has no session: it is
// stopped, still starting, or its connection died.
var ErrNotConnected = errors.New("the agent's pod isn't connected")

// Header is what a stream begins with: the operation and its arguments.
type Header struct {
	Op string `json:"op"`
	// Argv is the command for exec, or the terminal's program for attach.
	Argv []string `json:"argv,omitempty"`
	// User is who runs it, "" for root.
	User string `json:"user,omitempty"`
	// Dir is where it runs.
	Dir string `json:"dir,omitempty"`
	// Env is extra environment for it.
	Env []string `json:"env,omitempty"`
	// Path is the file or directory read, written or listed.
	Path string `json:"path,omitempty"`
	// Network and Addr are what dial connects to.
	Network string `json:"network,omitempty"`
	Addr    string `json:"addr,omitempty"`
	// UID, GID and Mode are what a written file is owned by, as a Unix mode.
	UID  int    `json:"uid,omitempty"`
	GID  int    `json:"gid,omitempty"`
	Mode uint32 `json:"mode,omitempty"`
	// Cols and Rows are a terminal's size as it opens.
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	// TimeoutMs bounds the operation on the pod's side; zero is no bound.
	TimeoutMs int64 `json:"timeoutMs,omitempty"`
}

// Entry is one file a list answers with.
type Entry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mode uint32 `json:"mode"`
	Dir  bool   `json:"dir"`
}

// Response is a control op's answer.
type Response struct {
	Entries []Entry `json:"entries,omitempty"`
	Data    []byte  `json:"data,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// Stream is one multiplexed stream, framed. It is not safe to write from two
// goroutines at once; it is safe to read in one and write in another, which is
// what an interactive terminal does.
type Stream struct {
	conn io.ReadWriteCloser
	r    *bufio.Reader
	w    *frameWriter
}

// NewStream wraps a stream's connection. The caller writes the header with
// WriteHeader before the frames, on the side that opens it.
func NewStream(conn io.ReadWriteCloser) *Stream {
	return &Stream{conn: conn, r: bufio.NewReaderSize(conn, 32<<10), w: &frameWriter{w: conn}}
}

// WriteHeader sends h, which every stream begins with.
func (s *Stream) WriteHeader(h Header) error {
	body, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if len(body) > MaxHeaderBytes {
		return fmt.Errorf("the header is %d bytes, over the %d a stream may carry", len(body), MaxHeaderBytes)
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(body)))
	if _, err := s.conn.Write(size[:]); err != nil {
		return err
	}
	_, err = s.conn.Write(body)
	return err
}

// ReadHeader reads the header a stream begins with.
func (s *Stream) ReadHeader() (Header, error) {
	var size [4]byte
	if _, err := io.ReadFull(s.r, size[:]); err != nil {
		return Header{}, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n > MaxHeaderBytes {
		return Header{}, fmt.Errorf("the header is %d bytes, over the %d a stream may carry", n, MaxHeaderBytes)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(s.r, body); err != nil {
		return Header{}, err
	}
	var h Header
	if err := json.Unmarshal(body, &h); err != nil {
		return Header{}, fmt.Errorf("the stream header: %w", err)
	}
	return h, nil
}

// Write sends one frame.
func (s *Stream) Write(kind byte, payload []byte) error {
	return s.w.write(kind, payload)
}

// Read reads one frame.
func (s *Stream) Read() (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(s.r, header[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n > MaxFrame {
		return 0, nil, fmt.Errorf("a %d byte frame is over the %d one may carry", n, MaxFrame)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(s.r, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

// Exit writes an exit code frame, which ends a command's stream.
func (s *Stream) Exit(code int) error {
	return s.Write(FrameExit, []byte{byte(code)})
}

// Close ends the stream.
func (s *Stream) Close() error { return s.conn.Close() }

// CloseWrite stops this side sending while the stream stays readable, which is
// how the master ends a command's stdin or a file's bytes: the pod reads until
// EOF, does its work, and answers on the same stream. No frame means end of
// input, and only the sender knows when it has sent it all.
func (s *Stream) CloseWrite() error {
	if cw, ok := s.conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	// A yamux stream has no separate half-close: Close sends a FIN, which ends
	// our writes and leaves the peer's answer coming. A connection with no
	// half-close at all is closed whole, and nothing more can be read.
	return s.conn.Close()
}

// WriteResponse answers a control or list op: one JSON frame, then the stream
// is closed by its handler.
func (s *Stream) WriteResponse(r Response) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.Write(FrameData, body)
}

// ReadResponse reads the answer WriteResponse wrote.
func (s *Stream) ReadResponse() (Response, error) {
	kind, payload, err := s.Read()
	if err != nil {
		return Response{}, err
	}
	if kind != FrameData {
		return Response{}, fmt.Errorf("frame %d where an answer was expected", kind)
	}
	var r Response
	if err := json.Unmarshal(payload, &r); err != nil {
		return Response{}, fmt.Errorf("the answer: %w", err)
	}
	return r, nil
}

// frameWriter writes frames one at a time.
type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (f *frameWriter) write(kind byte, payload []byte) error {
	if len(payload) > MaxFrame {
		return fmt.Errorf("a %d byte frame is over the %d one may carry", len(payload), MaxFrame)
	}
	var header [5]byte
	header[0] = kind
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.w.Write(header[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := f.w.Write(payload)
	return err
}

// CopyFrames copies r to a stream in frames of kind, which is how a file or a
// command's output travels without being held whole in memory.
func CopyFrames(s *Stream, kind byte, r io.Reader) error {
	buf := make([]byte, 64<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if err := s.Write(kind, buf[:n]); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// FrameReader is a stream read as one kind of frame: stdin for a command, or the
// bytes of a write. Frames of another kind are refused, so a mistyped op can't
// be mistaken for data.
type FrameReader struct {
	s       *Stream
	kind    byte
	pending []byte
}

// NewFrameReader reads payloads of kind from s.
func NewFrameReader(s *Stream, kind byte) *FrameReader { return &FrameReader{s: s, kind: kind} }

// Read fills p from the next payloads, and returns io.EOF when the peer stops
// sending them.
func (f *FrameReader) Read(p []byte) (int, error) {
	for len(f.pending) == 0 {
		kind, payload, err := f.s.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, io.EOF
			}
			return 0, err
		}
		if kind != f.kind {
			if kind == FrameExit {
				// The command finished while the peer still had bytes to
				// send: what it sent is all there was.
				return 0, io.EOF
			}
			return 0, fmt.Errorf("frame %d where %d was expected", kind, f.kind)
		}
		f.pending = payload
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}
