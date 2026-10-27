package desktop

import (
	"image"
	"strings"
	"testing"
)

func TestVideoTimeSkipsCuts(t *testing.T) {
	e := Edit{Cuts: Cuts{Start: 100, Recorded: 10, Cuts: []Cut{{From: 2, To: 5}}}}
	for _, c := range []struct {
		t, want float64
		ok      bool
	}{{101, 1, true}, {103, 2, true}, {106, 3, true}, {99, 0, false}, {111, 0, false}} {
		if v, ok := e.videoTime(c.t); v != c.want || ok != c.ok {
			t.Errorf("videoTime(%v) = %v, %v; want %v, %v", c.t, v, ok, c.want, c.ok)
		}
	}
}

func TestPlanZoomsOnClicksAndBacksOut(t *testing.T) {
	e := Edit{Size: image.Pt(1440, 900), Cuts: Cuts{Start: 0, Recorded: 20}, Events: []InputEvent{
		{T: 2, Button: 1, X: 1100, Y: 120},
		{T: 2.5, Key: "a"},
		{T: 12, Button: 1, X: 300, Y: 700},
	}}
	shots := e.Plan()
	var zoomed, back int
	for i, s := range shots {
		if i > 0 && s.At < shots[i-1].At {
			t.Fatalf("shots go back in time: %+v", shots)
		}
		if s.Zoom >= zoomMin {
			zoomed++
			if s.X-720/s.Zoom < 0 || s.X+720/s.Zoom > 1440 {
				t.Errorf("shot leaves the screen: %+v", s)
			}
		} else if i > 0 {
			back++
		}
	}
	if zoomed == 0 || back == 0 {
		t.Errorf("want shots that zoom in and back out, got %+v", shots)
	}
	if f := zoomFilter(shots, e.Size); !strings.HasPrefix(f, "zoompan=") {
		t.Errorf("filter: %s", f)
	}
}
