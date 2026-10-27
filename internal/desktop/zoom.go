package desktop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// An edit of a recording zooms in on where the walkthrough is happening, the
// way a demo video does, and puts its chapters on it as captions. It is worked
// out from what the recording already knows, with no model looking at frames:
// the clicks and keys the recorder drew on (RecordOptions.Events), the
// stretches its trimming cut (Cuts), and the notes made while it recorded.
//
// The zoom follows the input. Clicks close together in time and place are one
// shot: the camera eases in on them a moment before the first, holds while
// they go on, pans when the next ones are elsewhere, and eases back out to the
// whole screen when the input stops for a while or a chapter starts. Typing
// stays where the last click was. A shot whose clicks are too far apart to
// zoom in on usefully is the whole screen.

const (
	zoomMax  = 2.0  // the closest the camera goes in
	zoomMin  = 1.35 // closer than the whole screen by less than this isn't worth moving for
	zoomPad  = 260  // pixels of the screen kept around a shot's clicks, at zoom 1
	zoomLead = 0.6  // seconds the camera arrives before a shot's first click
	zoomEase = 0.55 // seconds a move takes
	zoomHold = 1.4  // seconds the camera stays after a shot's last click
	// zoomGap is how long the input may stop for and still be one shot.
	zoomGap = 2.2

	noteShow = 3.2 // seconds a chapter's caption is on screen
)

// eventLog keeps the input it passes on, for writing to opts.Events once the
// recording ends.
type eventLog struct {
	path string
	next func(InputEvent)
	mu   sync.Mutex
	evs  []InputEvent
}

func logEvents(path string, next func(InputEvent)) *eventLog {
	return &eventLog{path: path, next: next}
}

func (l *eventLog) on(e InputEvent) {
	if l.path != "" {
		l.mu.Lock()
		l.evs = append(l.evs, e)
		l.mu.Unlock()
	}
	l.next(e)
}

func (l *eventLog) write() {
	if l.path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	for _, e := range l.evs {
		j, _ := json.Marshal(e)
		b.Write(j)
		b.WriteByte('\n')
	}
	_ = os.WriteFile(l.path, []byte(b.String()), 0o644)
}

// Note is a chapter: its text, and when it was made, in InputEvent.T's clock.
type Note struct {
	T    float64 `json:"t"`
	Text string  `json:"text"`
}

// Edit is what an edit is worked out from.
type Edit struct {
	Size   image.Point // the video's
	Cuts   Cuts
	Events []InputEvent
	Notes  []Note
}

// videoTime is where wall-clock time t falls in the trimmed video, and false
// when it falls outside the recording. A time in a stretch that was cut falls
// where the cut is.
func (e Edit) videoTime(t float64) (float64, bool) {
	s := t - e.Cuts.Start
	if s < 0 || (e.Cuts.Recorded > 0 && s > e.Cuts.Recorded) {
		return 0, false
	}
	v := s
	for _, c := range e.Cuts.Cuts {
		switch {
		case s >= c.To:
			v -= c.To - c.From
		case s >= c.From:
			return v - (s - c.From), true
		}
	}
	return v, true
}

// Shot is where the camera is at video time At: centred on X,Y of the
// screen, zoomed in Zoom times.
type Shot struct {
	At, Zoom, X, Y float64
}

type focus struct {
	t    float64
	x, y int
}

// Plan is the camera's keyframes; between two, it eases from one to the next.
func (e Edit) Plan() []Shot {
	var points []focus
	var last *focus
	for _, ev := range e.Events {
		v, ok := e.videoTime(ev.T)
		if !ok {
			continue
		}
		switch {
		case ev.Button != 0:
			points = append(points, focus{v, ev.X, ev.Y})
			last = &points[len(points)-1]
		case ev.Key != "" && last != nil:
			points = append(points, focus{v, last.x, last.y})
		}
	}
	var chapters []float64
	for _, n := range e.Notes {
		if v, ok := e.videoTime(n.T); ok {
			chapters = append(chapters, v)
		}
	}
	whole := Shot{Zoom: 1, X: float64(e.Size.X) / 2, Y: float64(e.Size.Y) / 2}

	// Group the points into shots.
	type group struct {
		from, to float64
		box      image.Rectangle
	}
	var groups []group
	for _, p := range points {
		pt := image.Rect(p.x, p.y, p.x+1, p.y+1)
		if n := len(groups); n > 0 {
			g := &groups[n-1]
			box := g.box.Union(pt)
			chapter := slices.ContainsFunc(chapters, func(c float64) bool { return c > g.to && c <= p.t })
			if p.t-g.to < zoomGap && !chapter && e.fit(box) >= zoomMin {
				g.to, g.box = p.t, box
				continue
			}
		}
		groups = append(groups, group{from: p.t, to: p.t, box: pt})
	}

	shots := []Shot{whole}
	at := func(s Shot, t float64) {
		s.At = math.Max(t, shots[len(shots)-1].At)
		shots = append(shots, s)
	}
	cur := whole
	for i, g := range groups {
		target := e.frame(g.box)
		if target.Zoom < zoomMin {
			target = whole
		}
		arrive := g.from - zoomLead
		// Hold where the camera is until it has to move to arrive in time.
		at(cur, arrive-zoomEase)
		at(target, arrive)
		cur = target
		leave := g.to + zoomHold
		next := math.Inf(1)
		if i+1 < len(groups) {
			next = groups[i+1].from - zoomLead - zoomEase
		}
		if next-leave > zoomGap {
			// A pause before the next shot: back out to the whole screen.
			at(cur, leave)
			at(whole, leave+zoomEase)
			cur = whole
		}
	}
	return shots
}

// fit is how far the camera can zoom in and still show box with its padding.
func (e Edit) fit(box image.Rectangle) float64 {
	w, h := float64(box.Dx()+2*zoomPad), float64(box.Dy()+2*zoomPad)
	return math.Min(zoomMax, math.Min(float64(e.Size.X)/w, float64(e.Size.Y)/h))
}

// frame is the shot showing box, kept inside the screen.
func (e Edit) frame(box image.Rectangle) Shot {
	z := e.fit(box)
	w, h := float64(e.Size.X)/z, float64(e.Size.Y)/z
	cx := clamp(float64(box.Min.X+box.Max.X)/2, w/2, float64(e.Size.X)-w/2)
	cy := clamp(float64(box.Min.Y+box.Max.Y)/2, h/2, float64(e.Size.Y)-h/2)
	return Shot{Zoom: z, X: cx, Y: cy}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// zoomFilter is the ffmpeg filter that moves the camera along shots: a
// zoompan whose zoom and centre are eased piecewise over the input's time.
func zoomFilter(shots []Shot, size image.Point) string {
	piece := func(val func(Shot) float64) string {
		expr := strconv.FormatFloat(val(shots[len(shots)-1]), 'f', 3, 64)
		for i := len(shots) - 2; i >= 0; i-- {
			a, b := shots[i], shots[i+1]
			var seg string
			if b.At-a.At < 1e-3 || val(a) == val(b) {
				seg = strconv.FormatFloat(val(a), 'f', 3, 64)
			} else {
				// smoothstep from a to b
				p := fmt.Sprintf("clip((it-%.3f)/%.3f,0,1)", a.At, b.At-a.At)
				seg = fmt.Sprintf("(%.3f+%.3f*%s*%s*(3-2*%s))", val(a), val(b)-val(a), p, p, p)
			}
			expr = fmt.Sprintf("if(lt(it,%.3f),%s,%s)", b.At, seg, expr)
		}
		return expr
	}
	z := piece(func(s Shot) float64 { return s.Zoom })
	x := piece(func(s Shot) float64 { return s.X })
	y := piece(func(s Shot) float64 { return s.Y })
	return fmt.Sprintf("zoompan=z='%s':x='clip(%s-iw/zoom/2,0,iw-iw/zoom)':y='clip(%s-ih/zoom/2,0,ih-ih/zoom)':d=1:fps=%d:s=%dx%d",
		z, x, y, FrameRate, size.X, size.Y)
}

// captionFilters draw each chapter's text at the top of the video, fading in
// and out, with its text in a file of its own beside script so nothing in it
// needs escaping.
func captionFilters(e Edit, dir string) ([]string, error) {
	var out []string
	for i, n := range e.Notes {
		v, ok := e.videoTime(n.T)
		if !ok || strings.TrimSpace(n.Text) == "" {
			continue
		}
		file := filepath.Join(dir, fmt.Sprintf("note%d.txt", i))
		if err := os.WriteFile(file, []byte(n.Text), 0o644); err != nil {
			return nil, err
		}
		a, b := v, v+noteShow
		// The next chapter's caption takes the place of this one.
		if i+1 < len(e.Notes) {
			if next, ok := e.videoTime(e.Notes[i+1].T); ok {
				b = math.Min(b, next)
			}
		}
		alpha := fmt.Sprintf("clip(min((t-%.3f)/0.25,(%.3f-t)/0.4),0,1)", a, b)
		out = append(out, fmt.Sprintf("drawtext=textfile='%s':fontfile='%s':fontsize=34:fontcolor=white:alpha='%s':box=1:boxcolor=0x14141a@0.82:boxborderw=22:x=(w-text_w)/2:y=48:enable='between(t,%.3f,%.3f)'",
			file, captionFont(), alpha, a, b))
	}
	return out, nil
}

func captionFont() string {
	for _, f := range []string{"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", "/usr/share/fonts/TTF/DejaVuSans-Bold.ttf"} {
		if _, err := os.Stat(f); err == nil {
			return f
		}
	}
	return captionFonts[0]
}

// Render writes the edit of the recording in to out.
func (e Edit) Render(ctx context.Context, in, out string, log *os.File) error {
	dir, err := os.MkdirTemp("", "zoom")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	filters := []string{zoomFilter(e.Plan(), e.Size)}
	captions, err := captionFilters(e, dir)
	if err != nil {
		return err
	}
	filters = append(filters, captions...)
	script := filepath.Join(dir, "filter")
	if err := os.WriteFile(script, []byte(strings.Join(filters, ",\n")), 0o644); err != nil {
		return err
	}
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-i", in, "-/vf", script}, EncodeArgs...)
	cmd := exec.CommandContext(ctx, "ffmpeg", append(args, "-y", out)...)
	cmd.Stderr = log
	return cmd.Run()
}

// ReadEdit reads what an edit is worked out from: the recorder's events and
// cuts files, and notes, one Note a line.
func ReadEdit(size image.Point, events, cuts, notes string) (Edit, error) {
	e := Edit{Size: size}
	data, err := os.ReadFile(cuts)
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(data, &e.Cuts); err != nil {
		return e, fmt.Errorf("reading %s: %w", cuts, err)
	}
	if err := readLines(events, func(b []byte) error {
		var ev InputEvent
		err := json.Unmarshal(b, &ev)
		e.Events = append(e.Events, ev)
		return err
	}); err != nil {
		return e, err
	}
	if notes != "" {
		if err := readLines(notes, func(b []byte) error {
			var n Note
			err := json.Unmarshal(b, &n)
			e.Notes = append(e.Notes, n)
			return err
		}); err != nil {
			return e, err
		}
	}
	return e, nil
}

func readLines(path string, each func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if len(strings.TrimSpace(s.Text())) == 0 {
			continue
		}
		if err := each(s.Bytes()); err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
	}
	return s.Err()
}
