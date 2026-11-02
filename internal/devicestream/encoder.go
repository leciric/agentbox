package devicestream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// EncoderArgs are ffmpeg's arguments to encode width x height RGB24 frames
// from its stdin as H.264 for a viewer to show at once:
//   - libx264 at ultrafast with -tune zerolatency: no lookahead, no B-frames
//     (so no reordering), and each frame out as soon as it is in;
//   - Constrained Baseline, which every hardware decoder takes;
//   - quality by CRF rather than bitrate, since frames come only when the
//     screen changes and their rate says nothing;
//   - no keyframes but the first: the WebSocket loses nothing, and a viewer
//     that needs one asks (the encoder starts over);
//   - one frame out for each in (passthrough), as FLV, whose tags carry each
//     frame's length. An Annex-B stream would only show where a frame ends
//     when the next one begins, holding the last frame of every change
//     back until the screen changes again.
func EncoderArgs(width, height int) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-s", fmt.Sprintf("%dx%d", width, height), "-framerate", "60",
		"-i", "pipe:0",
		"-an", "-fps_mode", "passthrough",
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-profile:v", "baseline", "-crf", "23", "-bf", "0",
		"-x264-params", "keyint=infinite:min-keyint=1:scenecut=0",
		"-f", "flv", "-flvflags", "no_duration_filesize+no_metadata", "-flush_packets", "1",
		"pipe:1",
	}
}

// Encoder is one ffmpeg encoding frames of one size.
type Encoder struct {
	Width, Height int
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stderr        *bytes.Buffer
	out           *FLVReader
	mu            sync.Mutex
	at            []time.Time // when each frame not yet out came in
}

// FFmpeg is the ffmpeg to run, a variable for tests.
var FFmpeg = "ffmpeg"

// StartEncoder starts ffmpeg for width x height frames.
func StartEncoder(ctx context.Context, width, height int) (*Encoder, error) {
	if width <= 0 || height <= 0 || width%2 != 0 || height%2 != 0 {
		return nil, fmt.Errorf("can't encode a %dx%d screen: H.264 here needs even sizes", width, height)
	}
	cmd := exec.CommandContext(ctx, FFmpeg, EncoderArgs(width, height)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = &limited{w: stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("this machine has no ffmpeg to encode the screen with")
		}
		return nil, err
	}
	return &Encoder{Width: width, Height: height, cmd: cmd, stdin: stdin, stderr: stderr, out: NewFLVReader(stdout)}, nil
}

// Encode sends one frame in. It blocks while ffmpeg is busy.
func (e *Encoder) Encode(pixels []byte, at time.Time) error {
	if len(pixels) != e.Width*e.Height*3 {
		return fmt.Errorf("a %d-byte frame isn't %dx%d RGB24", len(pixels), e.Width, e.Height)
	}
	e.mu.Lock()
	e.at = append(e.at, at)
	e.mu.Unlock()
	if _, err := e.stdin.Write(pixels); err != nil {
		return e.failed(err)
	}
	return nil
}

// Next returns the next encoded frame, or the config when the stream's
// decoder configuration (the first tag) comes.
func (e *Encoder) Next() (avcC []byte, p Packet, err error) {
	tag, err := e.out.Next()
	if err != nil {
		return nil, Packet{}, e.failed(err)
	}
	if tag.Config {
		return tag.Data, Packet{}, nil
	}
	e.mu.Lock()
	at := time.Now()
	if len(e.at) > 0 {
		at, e.at = e.at[0], e.at[1:]
	}
	e.mu.Unlock()
	return nil, Packet{Key: tag.Key, At: at, Data: tag.Data}, nil
}

func (e *Encoder) failed(err error) error {
	if msg := strings.TrimSpace(e.stderr.String()); msg != "" {
		return fmt.Errorf("ffmpeg: %s", msg)
	}
	return fmt.Errorf("ffmpeg: %w", err)
}

// Close stops ffmpeg.
func (e *Encoder) Close() {
	_ = e.stdin.Close()
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
	_ = e.cmd.Wait()
}

type limited struct {
	w *bytes.Buffer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.n - l.w.Len(); room > 0 {
		l.w.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// FLVTag is one H.264 video tag of an FLV stream.
type FLVTag struct {
	Config bool   // Data is the AVCDecoderConfigurationRecord
	Key    bool   // a keyframe
	Data   []byte // or a frame, AVCC
}

// FLVReader reads the H.264 video of an FLV stream as ffmpeg writes it.
type FLVReader struct {
	r      *bufio.Reader
	header bool
}

func NewFLVReader(r io.Reader) *FLVReader {
	return &FLVReader{r: bufio.NewReaderSize(r, 256<<10)}
}

// maxTag bounds one tag: a keyframe of a 4K screen is well under it.
const maxTag = 16 << 20

// Next returns the next video tag, skipping others.
func (f *FLVReader) Next() (FLVTag, error) {
	if !f.header {
		var head [13]byte // the header, then PreviousTagSize0
		if _, err := io.ReadFull(f.r, head[:]); err != nil {
			return FLVTag{}, err
		}
		if string(head[:3]) != "FLV" {
			return FLVTag{}, errors.New("not an FLV stream")
		}
		if skip := int(binary.BigEndian.Uint32(head[5:9])) - 9; skip > 0 {
			if _, err := f.r.Discard(skip); err != nil {
				return FLVTag{}, err
			}
		}
		f.header = true
	}
	for {
		var head [11]byte
		if _, err := io.ReadFull(f.r, head[:]); err != nil {
			return FLVTag{}, err
		}
		size := int(head[1])<<16 | int(head[2])<<8 | int(head[3])
		if size > maxTag {
			return FLVTag{}, fmt.Errorf("an FLV tag of %d bytes", size)
		}
		data := make([]byte, size+4) // and PreviousTagSize
		if _, err := io.ReadFull(f.r, data); err != nil {
			return FLVTag{}, err
		}
		data = data[:size]
		if head[0]&0x1f != 9 { // not video
			continue
		}
		if len(data) < 5 {
			return FLVTag{}, errors.New("an FLV video tag too short")
		}
		if codec := data[0] & 0x0f; codec != 7 || data[0]&0x80 != 0 {
			return FLVTag{}, fmt.Errorf("FLV video that isn't H.264 (%#x)", data[0])
		}
		switch data[1] {
		case 0:
			return FLVTag{Config: true, Data: data[5:]}, nil
		case 1:
			return FLVTag{Key: data[0]>>4 == 1, Data: data[5:]}, nil
		}
		// 2, the end of the sequence
	}
}

// ffmpegHasX264 reports whether this machine's ffmpeg has libx264.
func ffmpegHasX264(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, FFmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("this machine has no ffmpeg to encode the screen with")
		}
		return fmt.Errorf("ffmpeg: %w", err)
	}
	if !bytes.Contains(out, []byte(" libx264 ")) {
		return errors.New("this machine's ffmpeg has no libx264 to encode the screen with")
	}
	return nil
}

// evenSize rounds a size down to even, which yuv420p needs.
func evenSize(n int) int { return n &^ 1 }
