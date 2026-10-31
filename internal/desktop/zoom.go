package desktop

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/fs"
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
// It keeps the video's timeline, frame for frame, so a chapter is at the same
// second in the edit as in the original.
//
// The zoom follows the input. Clicks close together in time and place are one
// shot: the camera eases in on them a moment before the first, holds while
// they go on, pans when the next ones are elsewhere, and eases back out to the
// whole screen when the input stops for a while or a chapter starts. Typing
// stays where the last click was. A shot whose clicks are too far apart to
// zoom in on usefully is the whole screen.

const (
	zoomMax = 2.0  // the closest the camera goes in
	zoomMin = 1.35 // closer than the whole screen by less than this isn't worth moving for
	// zoomPad is how much of the screen is kept around a shot's clicks: this
	// share of its width.
	zoomPad  = 0.18
	zoomLead = 0.5 // seconds the camera arrives before a shot's first click
	zoomEase = 0.7 // seconds a move takes
	zoomHold = 1.4 // seconds the camera stays after a shot's last click
	// zoomHoldMin is the least it stays, when a new chapter calls it back.
	zoomHoldMin = 0.5
	// zoomGap is how long the input may stop for and still be one shot.
	zoomGap = 2.2

	// A caption is on screen for long enough to read it: noteShow, and
	// noteShowWord more for each word, up to noteShowMax, unless the next
	// chapter starts first.
	noteShow     = 2.4
	noteShowWord = 0.25
	noteShowMax  = 7.0
	fadeIn       = 0.3
	fadeOut      = 0.45
	// titleShow is how long the title card stays over the opening, its fade
	// out included.
	titleShow = 2.2
)

// eventLog keeps the input it passes on to the overlay, for writing to
// opts.Events once the recording ends, with the clicks where they are in the
// frame rather than on the display.
type eventLog struct {
	path string
	ov   *overlay
	mu   sync.Mutex
	evs  []InputEvent
}

func logEvents(path string, ov *overlay) *eventLog {
	return &eventLog{path: path, ov: ov}
}

func (l *eventLog) on(e InputEvent) {
	l.ov.add(e)
	if l.path == "" {
		return
	}
	if e.Button != 0 {
		e.X, e.Y = l.ov.inFrame(e.X, e.Y)
	}
	l.mu.Lock()
	l.evs = append(l.evs, e)
	l.mu.Unlock()
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
	Size   image.Point // the video's; Render asks ffprobe when it is zero
	Cuts   Cuts
	Events []InputEvent
	Notes  []Note
	// Title, when set, is a card over the opening moments, with Subtitle
	// under it.
	Title, Subtitle string
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
		next, chapter := math.Inf(1), false
		if i+1 < len(groups) {
			next = groups[i+1].from - zoomLead - zoomEase
			if c := slices.IndexFunc(chapters, func(c float64) bool { return c > g.to && c <= groups[i+1].from }); c >= 0 {
				// A new chapter: the camera backs out as its caption comes
				// on, sooner than it would otherwise, to set the scene.
				chapter = true
				leave = math.Max(g.to+zoomHoldMin, math.Min(leave, chapters[c]-zoomEase/2))
			}
		}
		if next-leave > zoomGap || chapter && next-leave >= zoomEase {
			// A pause or a new chapter before the next shot: back out to the
			// whole screen.
			at(cur, leave)
			at(whole, leave+zoomEase)
			cur = whole
		}
	}
	return shots
}

// fit is how far the camera can zoom in and still show box with its padding.
func (e Edit) fit(box image.Rectangle) float64 {
	pad := zoomPad * float64(e.Size.X)
	w, h := float64(box.Dx())+2*pad, float64(box.Dy())+2*pad
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

// cameraAt is where the camera is at video time t, as zoomFilter moves it.
func cameraAt(shots []Shot, t float64) Shot {
	for i := 0; i+1 < len(shots); i++ {
		a, b := shots[i], shots[i+1]
		if t >= b.At {
			continue
		}
		if b.At-a.At < 1e-3 {
			return a
		}
		p := clamp((t-a.At)/(b.At-a.At), 0, 1)
		p = p * p * (3 - 2*p)
		return Shot{At: t, Zoom: a.Zoom + (b.Zoom-a.Zoom)*p, X: a.X + (b.X-a.X)*p, Y: a.Y + (b.Y-a.Y)*p}
	}
	last := shots[len(shots)-1]
	last.At = t
	return last
}

// onScreen is where point x,y of the screen is in the video, with the camera
// at s.
func (s Shot) onScreen(size image.Point, x, y int) image.Point {
	left := clamp(s.X-float64(size.X)/s.Zoom/2, 0, float64(size.X)-float64(size.X)/s.Zoom)
	top := clamp(s.Y-float64(size.Y)/s.Zoom/2, 0, float64(size.Y)-float64(size.Y)/s.Zoom)
	return image.Pt(int((float64(x)-left)*s.Zoom), int((float64(y)-top)*s.Zoom))
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

// overlayCard is a card laid over the video from video time from to to,
// fading in and out, at At.
type overlayCard struct {
	file     string
	at       image.Point
	from, to float64
}

// cards draws the title and the chapters' captions into dir. A caption goes
// to the top of the video rather than the bottom when it would cover a click
// made while it is on, with the camera along shots.
func (e Edit) cards(dir string, shots []Shot) ([]overlayCard, error) {
	var out []overlayCard
	save := func(img *image.NRGBA, at image.Point, from, to float64) error {
		file := filepath.Join(dir, fmt.Sprintf("card%d.png", len(out)))
		f, err := os.Create(file)
		if err != nil {
			return err
		}
		err = png.Encode(f, img)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		out = append(out, overlayCard{file: file, at: at, from: from, to: to})
		return err
	}
	if strings.TrimSpace(e.Title) != "" {
		img, err := titleCard(e.Size, e.Title, e.Subtitle)
		if err != nil {
			return nil, err
		}
		if err := save(img, image.Point{}, 0, titleShow); err != nil {
			return nil, err
		}
	}
	type chapter struct {
		at   float64
		text string
	}
	var chapters []chapter
	for _, n := range e.Notes {
		if v, ok := e.videoTime(n.T); ok && strings.TrimSpace(n.Text) != "" {
			chapters = append(chapters, chapter{v, strings.TrimSpace(n.Text)})
		}
	}
	end := math.Inf(1)
	if e.Cuts.Recorded > 0 {
		end = e.Cuts.Recorded - e.Cuts.removed()
	}
	for i, c := range chapters {
		// A caption gives way to the next chapter's. One over the title
		// card is clear of its title, in the middle.
		from := c.at
		to := from + math.Min(noteShowMax, noteShow+noteShowWord*float64(len(strings.Fields(c.text))))
		if i+1 < len(chapters) {
			to = math.Min(to, chapters[i+1].at)
		}
		to = math.Min(to, end)
		if to-from < fadeIn+fadeOut {
			continue
		}
		step := ""
		if len(chapters) > 1 {
			step = fmt.Sprintf("%d/%d", i+1, len(chapters))
		}
		img, err := captionCard(e.Size, step, c.text)
		if err != nil {
			return nil, err
		}
		if err := save(img, e.placeCaption(img.Rect, shots, from, to), from, to); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// placeCaption is where a caption card on from to to goes: the bottom of the
// video, unless a click made then would be under it.
func (e Edit) placeCaption(card image.Rectangle, shots []Shot, from, to float64) image.Point {
	at := cardAt(e.Size, card)
	margin := int(captionSize(e.Size.Y))
	under := image.Rectangle{Min: at, Max: at.Add(card.Size())}.Inset(-margin)
	for _, ev := range e.Events {
		v, ok := e.videoTime(ev.T)
		if !ok || ev.Button == 0 || v < from-0.2 || v > to {
			continue
		}
		if cameraAt(shots, v).onScreen(e.Size, ev.X, ev.Y).In(under) {
			return image.Pt(at.X, int(float64(e.Size.Y)*0.06))
		}
	}
	return at
}

// filterGraph is the ffmpeg filter graph of the edit: the camera moving over
// input 0, then each card, input 1 on, laid over it.
func filterGraph(shots []Shot, size image.Point, cards []overlayCard) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[0:v]%s,setsar=1[v0]", zoomFilter(shots, size))
	for i, c := range cards {
		d := c.to - c.from
		// The card's own clock starts at 0: it fades on that, then is moved
		// to where it goes on the video's.
		fmt.Fprintf(&b, ";\n[%d:v]format=rgba,fade=t=in:st=0:d=%.3f:alpha=1,fade=t=out:st=%.3f:d=%.3f:alpha=1,setpts=PTS+%.3f/TB[c%d]",
			i+1, fadeIn, d-fadeOut, fadeOut, c.from, i)
		fmt.Fprintf(&b, ";\n[v%d][c%d]overlay=x=%d:y=%d:eof_action=pass:enable='between(t,%.3f,%.3f)'[v%d]",
			i, i, c.at.X, c.at.Y, c.from, c.to, i+1)
	}
	return b.String()
}

// Render writes the edit of the recording in to out.
func (e Edit) Render(ctx context.Context, in, out string, log io.Writer) error {
	if e.Size.X == 0 || e.Size.Y == 0 {
		size, err := videoSize(ctx, in)
		if err != nil {
			return err
		}
		e.Size = size
	}
	dir, err := os.MkdirTemp("", "edit")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	shots := e.Plan()
	cards, err := e.cards(dir, shots)
	if err != nil {
		return err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-i", in}
	for _, c := range cards {
		// Each card is one picture, shown for as long as it is on.
		args = append(args, "-loop", "1", "-framerate", strconv.Itoa(FrameRate), "-t", fmt.Sprintf("%.3f", c.to-c.from), "-i", c.file)
	}
	// The graph goes on the command line rather than in a script file:
	// -/filter_complex, which reads one from a file, only arrived in ffmpeg
	// 7.1, and CI's runner has 6.1. -filter_complex takes the graph itself
	// on every version, 5 through 7.
	args = append(args, "-filter_complex", filterGraph(shots, e.Size, cards), "-map", fmt.Sprintf("[v%d]", len(cards)), "-map", "0:a?")
	args = append(args, EncodeArgs...)
	cmd := exec.CommandContext(ctx, "ffmpeg", append(args, "-y", out)...)
	cmd.Stderr = log
	return cmd.Run()
}

// videoSize is the size of the video in file, as ffprobe reads it.
func videoSize(ctx context.Context, file string) (image.Point, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0", file).Output()
	if err != nil {
		return image.Point{}, fmt.Errorf("reading the size of %s: %w", file, err)
	}
	w, h, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
	x, _ := strconv.Atoi(w)
	y, _ := strconv.Atoi(strings.Trim(h, ","))
	if x == 0 || y == 0 {
		return image.Point{}, fmt.Errorf("ffprobe found no video in %s", file)
	}
	return image.Pt(x, y), nil
}

// ReadEdit reads what an edit is worked out from: the recorder's events and
// cuts files, and notes, one Note a line. Any of them may be "", or missing:
// a recording that wasn't trimmed or had no input drawn on has neither of
// the first two, and start is then when its first frame was taken.
func ReadEdit(events, cuts, notes string, start float64) (Edit, error) {
	e := Edit{Cuts: Cuts{Start: start}}
	if cuts != "" {
		data, err := os.ReadFile(cuts)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return e, err
		default:
			if err := json.Unmarshal(data, &e.Cuts); err != nil {
				return e, fmt.Errorf("reading %s: %w", cuts, err)
			}
			if e.Cuts.Start == 0 {
				e.Cuts.Start = start
			}
		}
	}
	if err := readLines(events, func(b []byte) error {
		var ev InputEvent
		err := json.Unmarshal(b, &ev)
		e.Events = append(e.Events, ev)
		return err
	}); err != nil {
		return e, err
	}
	err := readLines(notes, func(b []byte) error {
		var n Note
		err := json.Unmarshal(b, &n)
		e.Notes = append(e.Notes, n)
		return err
	})
	return e, err
}

// readLines calls each with every line of path that isn't blank; a path
// that is "" or doesn't exist has none.
func readLines(path string, each func([]byte) error) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
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
