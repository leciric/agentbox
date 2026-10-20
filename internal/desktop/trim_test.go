package desktop

import (
	"bytes"
	"reflect"
	"testing"
)

const trimW, trimH = 320, 200

// frameOf is a grey frame with the rectangles given painted white.
func frameOf(rects ...[4]int) []byte {
	f := bytes.Repeat([]byte{60, 60, 60, 0}, trimW*trimH)
	for _, r := range rects {
		for y := r[1]; y < r[1]+r[3]; y++ {
			for x := r[0]; x < r[0]+r[2]; x++ {
				copy(f[(y*trimW+x)*4:], []byte{255, 255, 255, 0})
			}
		}
	}
	return f
}

// trimRun feeds the frames through a trimmer and gives back the frames it
// emitted, by which of the inputs they show, and what it cut.
func trimRun(t *testing.T, frames [][]byte) ([]int, Cuts) {
	t.Helper()
	tr := newTrimmer(trimW, trimH)
	var out []int
	emit := func(f []byte) error {
		for i := range frames {
			if bytes.Equal(f, frames[i]) {
				out = append(out, i)
				return nil
			}
		}
		t.Fatal("emitted a frame that isn't one of the inputs")
		return nil
	}
	for _, f := range frames {
		if err := tr.next(f, emit); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.finish(emit); err != nil {
		t.Fatal(err)
	}
	return out, tr.result()
}

// still repeats frame n times.
func still(frame []byte, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = frame
	}
	return out
}

func TestTrimKeepsShortStillnessWhole(t *testing.T) {
	a, b := frameOf(), frameOf([4]int{100, 50, 80, 60})
	frames := append(still(a, stillFrames+1), b) // still for exactly StillFor after the first
	out, cuts := trimRun(t, frames)
	if len(out) != len(frames) {
		t.Fatalf("kept %d frames of %d; want them all", len(out), len(frames))
	}
	if len(cuts.Cuts) != 0 {
		t.Errorf("cuts = %v; want none", cuts.Cuts)
	}
}

func TestTrimCutsLongStillnessToAMoment(t *testing.T) {
	a, b := frameOf(), frameOf([4]int{100, 50, 80, 60})
	// 1 frame, then 5s of the same (75 frames), then a change and 1s of it.
	frames := append(still(a, 1+5*FrameRate), still(b, FrameRate)...)
	out, cuts := trimRun(t, frames)
	// The first, the 0.4s of stillness kept, then all of b's second (its own
	// stillness is no longer than StillFor).
	// The emitted frames are named by the first input they equal.
	var shows []int
	for range 1 + keepFrames {
		shows = append(shows, 0)
	}
	for range FrameRate {
		shows = append(shows, 1+5*FrameRate)
	}
	if !reflect.DeepEqual(out, shows) {
		t.Fatalf("kept %v; want %v", out, shows)
	}
	want := Cuts{Recorded: seconds(len(frames)), Cuts: []Cut{{From: seconds(1 + keepFrames), To: seconds(1 + 5*FrameRate)}}}
	if !reflect.DeepEqual(cuts, want) {
		t.Errorf("cuts = %+v; want %+v", cuts, want)
	}
}

func TestTrimCutsStillnessAtTheEnd(t *testing.T) {
	frames := still(frameOf(), 1+3*FrameRate)
	out, cuts := trimRun(t, frames)
	if want := 1 + keepFrames; len(out) != want {
		t.Fatalf("kept %d frames; want %d", len(out), want)
	}
	if want := []Cut{{From: seconds(1 + keepFrames), To: seconds(len(frames))}}; !reflect.DeepEqual(cuts.Cuts, want) {
		t.Errorf("cuts = %+v; want %+v", cuts.Cuts, want)
	}
}

func TestTrimIgnoresABlinkingCursorAndAClock(t *testing.T) {
	base := frameOf()
	caret := frameOf([4]int{40, 40, 2, 18})                            // a text caret, on
	clock := frameOf([4]int{280, 180, 7, 12}, [4]int{289, 180, 7, 12}) // two digits of a clock
	var frames [][]byte
	frames = append(frames, base)
	for i := range 4 * FrameRate {
		switch {
		case i == 2*FrameRate:
			frames = append(frames, clock)
		case (i/8)%2 == 1:
			frames = append(frames, caret)
		default:
			frames = append(frames, base)
		}
	}
	out, cuts := trimRun(t, frames)
	if want := 1 + keepFrames; len(out) != want {
		t.Errorf("kept %d frames of a still screen with a caret blinking; want %d", len(out), want)
	}
	if len(cuts.Cuts) != 1 {
		t.Errorf("cuts = %+v; want one", cuts.Cuts)
	}
}

func TestTrimSeesThePointerMove(t *testing.T) {
	// A 12×19 arrow, moved by 60 pixels a frame: every frame changes.
	var frames [][]byte
	for i := range 2 * FrameRate {
		frames = append(frames, frameOf([4]int{10 + i%4*60, 100, 12, 19}))
	}
	out, cuts := trimRun(t, frames)
	if len(out) != len(frames) || len(cuts.Cuts) != 0 {
		t.Errorf("kept %d frames of %d, cut %v; want every frame of a pointer moving", len(out), len(frames), cuts.Cuts)
	}
}

func TestTrimAddsUpASlowChange(t *testing.T) {
	// Text typed a character a second: each one is too small to count, but
	// against the last frame kept, the third makes a change.
	var frames [][]byte
	for c := range 6 {
		var rects [][4]int
		for i := range c {
			rects = append(rects, [4]int{40 + i*10, 40, 8, 12})
		}
		frames = append(frames, still(frameOf(rects...), FrameRate)...)
	}
	_, a := trimRun(t, frames)
	_, b := trimRun(t, frames)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("the same frames cut differently: %+v, then %+v", a, b)
	}
	if len(a.Cuts) == 0 || len(a.Cuts) >= 5 {
		t.Errorf("cuts = %+v; want the slow typing cut, but not between every character", a.Cuts)
	}
}

func TestTrimShrinksPartialBlocks(t *testing.T) {
	// A width and height that aren't a multiple of BlockSize: the edge blocks
	// are averaged over the pixels they have.
	tr := newTrimmer(20, 18)
	f := bytes.Repeat([]byte{100, 100, 100, 0}, 20*18)
	tr.shrink(f, tr.thumb, nil)
	for i, v := range tr.thumb {
		if v != 100-1 && v != 100 {
			t.Fatalf("block %d = %d; want 100", i, v)
		}
	}
}
