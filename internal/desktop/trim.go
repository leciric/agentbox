package desktop

import (
	"bytes"
	"encoding/json"
	"os"
)

// A recording is mostly the screen standing still: the desktop subagent
// spends seconds on each step thinking, and the recorder keeps wall-clock
// time. The trimmer cuts those stretches out as the frames go by, on their
// way to the encoder, so that a recording plays as the action alone: any
// stretch the screen stands still for longer than StillFor plays as KeepStill,
// and anything shorter plays as it happened. Cutting here, rather than with
// ffmpeg's mpdecimate once the recording stops, costs nothing more than a
// comparison per frame, and stopping stays as quick as it was.
//
// Whether the screen changed is plain arithmetic, the same frames always
// cutting the same way: each frame is shrunk to the mean brightness of every
// block of BlockSize×BlockSize pixels and compared with the last frame kept,
// shrunk the same way. A block whose mean moved by more than BlockTolerance
// changed, and a frame changed when at least ChangedBlocks blocks did. A
// blinking text cursor or the dock's clock touches one or two blocks, too few;
// the pointer moving touches both where it was and where it went, a click's
// ripple or a key's caption many more. Comparing with the last frame kept
// rather than the one before means a slow change still adds up to one.
const (
	BlockSize      = 16
	BlockTolerance = 20 // of 255
	ChangedBlocks  = 3

	stillFrames = FrameRate * 2     // StillFor, 2s
	keepFrames  = FrameRate * 6 / 5 // KeepStill, 1.2s
)

// A Cut is a stretch of the recording, in seconds of the wall clock since it
// started, that the video leaves out.
type Cut struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
}

// Cuts is what a trimmed recording left out, written beside it for the
// review to place its chapters on the shorter timeline.
type Cuts struct {
	Recorded float64 `json:"recorded"` // seconds of wall clock the video covers
	// Start is when the first frame was taken, in InputEvent.T's clock.
	Start float64 `json:"start,omitempty"`
	Cuts  []Cut   `json:"cuts"`
}

// trimmer decides, frame by frame, which frames of a recording go to the
// encoder. Its frames are BGR0, width×height.
type trimmer struct {
	width, height int
	cols, rows    int

	ref      []byte // the last frame kept
	refThumb []uint8
	thumb    []uint8
	sums     []uint32

	seen     int  // frames so far
	pending  int  // still frames held back, to be kept if the stillness ends soon
	dropping bool // past StillFor: frames are being cut
	cutFrom  int
	cuts     []Cut
}

func newTrimmer(width, height int) *trimmer {
	cols, rows := (width+BlockSize-1)/BlockSize, (height+BlockSize-1)/BlockSize
	return &trimmer{width: width, height: height, cols: cols, rows: rows,
		refThumb: make([]uint8, cols*rows), thumb: make([]uint8, cols*rows), sums: make([]uint32, cols)}
}

// next takes one frame and emits what of it, and of the frames held back
// before it, goes to the encoder. emit may be called with the trimmer's own
// copy of an earlier frame, which it mustn't keep.
func (t *trimmer) next(frame []byte, emit func([]byte) error) error {
	n := t.seen
	t.seen++
	if t.ref == nil {
		t.ref = bytes.Clone(frame)
		t.shrink(frame, t.refThumb, nil)
		return emit(frame)
	}
	if !t.changed(frame) {
		if t.dropping {
			return nil
		}
		t.pending++
		if t.pending <= stillFrames {
			return nil
		}
		// Still for longer than StillFor: the start of it plays, the rest is
		// cut until the screen changes.
		t.cutFrom = n - t.pending + 1 + keepFrames
		t.pending, t.dropping = 0, true
		return t.repeat(keepFrames, emit)
	}
	if t.dropping {
		t.cuts = append(t.cuts, Cut{From: seconds(t.cutFrom), To: seconds(n)})
		t.dropping = false
	} else if err := t.repeat(t.pending, emit); err != nil {
		return err
	}
	t.pending = 0
	copy(t.ref, frame)
	t.refThumb, t.thumb = t.thumb, t.refThumb
	return emit(frame)
}

// finish emits what is held back at the end of the recording.
func (t *trimmer) finish(emit func([]byte) error) error {
	if t.dropping {
		t.cuts = append(t.cuts, Cut{From: seconds(t.cutFrom), To: seconds(t.seen)})
		t.dropping = false
	}
	err := t.repeat(t.pending, emit)
	t.pending = 0
	return err
}

func (t *trimmer) result() Cuts {
	return Cuts{Recorded: seconds(t.seen), Cuts: append([]Cut{}, t.cuts...)}
}

// repeat emits the last frame kept n times: the frames held back were all
// the same as it, near enough.
func (t *trimmer) repeat(n int, emit func([]byte) error) error {
	for range n {
		if err := emit(t.ref); err != nil {
			return err
		}
	}
	return nil
}

// changed shrinks frame into t.thumb and says whether it differs from the
// last frame kept.
func (t *trimmer) changed(frame []byte) bool {
	t.shrink(frame, t.thumb, t.ref)
	blocks := 0
	for i, v := range t.thumb {
		d := int(v) - int(t.refThumb[i])
		if d > BlockTolerance || d < -BlockTolerance {
			blocks++
		}
	}
	return blocks >= ChangedBlocks
}

// shrink writes the mean brightness of each of frame's blocks into thumb. A
// band of blocks whose rows are byte for byte ref's, as most are, takes the
// last frame kept's values without being added up.
func (t *trimmer) shrink(frame []byte, thumb []uint8, ref []byte) {
	stride := t.width * 4
	sums := t.sums
	for by := range t.rows {
		y0, y1 := by*BlockSize, min((by+1)*BlockSize, t.height)
		band := frame[y0*stride : y1*stride]
		if ref != nil && bytes.Equal(band, ref[y0*stride:y1*stride]) {
			copy(thumb[by*t.cols:(by+1)*t.cols], t.refThumb[by*t.cols:(by+1)*t.cols])
			continue
		}
		clear(sums)
		for y := y0; y < y1; y++ {
			row := frame[y*stride : (y+1)*stride]
			for x := range t.width {
				p := row[x*4:]
				// BT.601 luma, from blue, green and red.
				sums[x/BlockSize] += (29*uint32(p[0]) + 150*uint32(p[1]) + 77*uint32(p[2])) >> 8
			}
		}
		for bx := range t.cols {
			w := min((bx+1)*BlockSize, t.width) - bx*BlockSize
			thumb[by*t.cols+bx] = uint8(sums[bx] / uint32(w*(y1-y0)))
		}
	}
}

func seconds(frames int) float64 { return float64(frames) / FrameRate }

func writeCuts(path string, c Cuts) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// removed is how many seconds of the recording the cuts took out.
func (c Cuts) removed() float64 {
	var s float64
	for _, cut := range c.Cuts {
		s += cut.To - cut.From
	}
	return s
}
