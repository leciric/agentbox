// Command fakesim is a stand-in for a Mac's iOS Simulator, for working on
// the app's iOS view on Linux, where there is none: the helper's own server
// (iossim.Server), with a simhost that draws a test screen instead of
// capturing one, encodes it with ffmpeg (internal/devicestream's encoder,
// software x264 where the real one uses the Mac's hardware), and marks each
// touch it's sent with a dot. It also answers the daemon's paths, as the
// daemon's relay would pass them on, so the preview can play it:
//
//	go run ./internal/iossim/fakesim -addr 127.0.0.1:5903
//	npm --prefix desktop run preview, then open
//	…/dev/preview.html?app=project&v2=1&panel=ios:agent-41&ios=booted&stream=ws://127.0.0.1:5903
//
// What it shows is plainly a test pattern: nothing here is an iPhone's.
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"time"

	"agentbox/internal/devicestream"
	"agentbox/internal/iossim"
)

// The screen, at a third of an iPhone 17 Pro's 1206×2622, rounded to even.
const width, height = 402, 874

func main() {
	addr := flag.String("addr", "127.0.0.1:5903", "where to listen")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	srv := iossim.NewServer("fakesim", "fakesim", iossim.Simctl{Run: noXcrun}, nil, launch)
	go srv.Run(ctx)
	helper := srv.Handler()
	daemonPath := regexp.MustCompile(`^/v1/agents/[^/]+/[^/]+/ios/view$`)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The daemon's path for the app's view, and the token the daemon adds.
		if daemonPath.MatchString(r.URL.Path) {
			r.URL.Path = "/v1/devices/" + r.URL.Query().Get("device") + "/view"
		}
		r.Header.Set("Authorization", "Bearer fakesim")
		helper.ServeHTTP(w, r)
	})
	log.Printf("fakesim on ws://%s", *addr)
	hs := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); _ = hs.Close() }()
	if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func noXcrun(context.Context, io.Reader, string, ...string) ([]byte, error) {
	return nil, fmt.Errorf("fakesim has no xcrun")
}

// launch is a simhost in this process, behind pipes, as the real one is
// behind its stdin and stdout.
func launch(_ context.Context, _ string) (*iossim.Proc, error) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	d := &device{out: outW}
	ctx, cancel := context.WithCancel(context.Background())
	go d.run(ctx, inR)
	var once sync.Once
	return &iossim.Proc{
		Stdin: inW, Stdout: outR, Done: done,
		Stop: func() error {
			once.Do(func() {
				cancel()
				_ = inR.Close()
				_ = outW.Close()
				close(done)
			})
			return nil
		},
	}, nil
}

type device struct {
	mu      sync.Mutex
	out     io.Writer
	touches [][2]float64 // where it was touched, newest last
	down    bool
	keys    int
	stream  chan bool // on, off
	restart chan struct{}
}

func (d *device) write(kind byte, payload []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	head := make([]byte, 5)
	binary.BigEndian.PutUint32(head, uint32(len(payload)+1))
	head[4] = kind
	_, _ = d.out.Write(append(head, payload...))
}

func (d *device) run(ctx context.Context, in io.Reader) {
	d.stream, d.restart = make(chan bool, 8), make(chan struct{}, 1)
	d.write(3, fmt.Appendf(nil, `{"screen":{"width":%d,"height":%d}}`, width*3, height*3))
	go d.encode(ctx)
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		var o struct {
			Op, Phase string
			X, Y      float64
			On, Down  bool
		}
		if json.Unmarshal(sc.Bytes(), &o) != nil {
			continue
		}
		switch o.Op {
		case "stream":
			d.stream <- o.On
		case "keyframe":
			select {
			case d.restart <- struct{}{}:
			default:
			}
		case "touch":
			d.mu.Lock()
			d.down = o.Phase != "up"
			d.touches = append(d.touches, [2]float64{o.X, o.Y})
			if len(d.touches) > 40 {
				d.touches = d.touches[1:]
			}
			d.mu.Unlock()
		case "key", "button":
			if o.Down {
				d.mu.Lock()
				d.keys++
				d.mu.Unlock()
			}
		}
	}
}

// encode draws and encodes the screen 30 times a second while it's
// streamed, starting ffmpeg over for a keyframe, as simhost forces one.
func (d *device) encode(ctx context.Context) {
	var enc *devicestream.Encoder
	stopEnc := func() {
		if enc != nil {
			enc.Close()
			enc = nil
		}
	}
	defer stopEnc()
	tick := time.NewTicker(time.Second / 30)
	defer tick.Stop()
	on := false
	pixels := make([]byte, width*height*3)
	for {
		select {
		case <-ctx.Done():
			return
		case on = <-d.stream:
			stopEnc()
			continue
		case <-d.restart:
			stopEnc()
			continue
		case <-tick.C:
		}
		if !on {
			continue
		}
		if enc == nil {
			var err error
			if enc, err = devicestream.StartEncoder(ctx, width, height); err != nil {
				d.write(3, fmt.Appendf(nil, `{"error":%q}`, err.Error()))
				return
			}
			go d.send(enc)
		}
		d.draw(pixels)
		if err := enc.Encode(pixels, time.Now()); err != nil {
			stopEnc()
		}
	}
}

// send passes what the encoder makes on, in simhost's shapes.
func (d *device) send(enc *devicestream.Encoder) {
	for {
		avcC, p, err := enc.Next()
		if err != nil {
			return
		}
		if avcC != nil {
			codec, _ := devicestream.CodecString(avcC)
			config, _ := json.Marshal(map[string]any{"codec": codec, "width": width, "height": height, "description": avcC})
			d.write(1, config)
			continue
		}
		frame := make([]byte, 9, 9+len(p.Data))
		if p.Key {
			frame[0] = 1
		}
		binary.BigEndian.PutUint64(frame[1:], uint64(p.At.UnixMicro()))
		d.write(2, append(frame, p.Data...))
	}
}

// draw is the test screen: bands of color that move, a bar for each key or
// button pressed, and a dot at each touch, the newest brightest.
func (d *device) draw(px []byte) {
	d.mu.Lock()
	touches := append([][2]float64(nil), d.touches...)
	keys, down := d.keys, d.down
	d.mu.Unlock()
	shift := int(time.Now().UnixMilli()/40) % height
	for y := range height {
		band := ((y + shift) / 60) % 2
		for x := range width {
			i := (y*width + x) * 3
			px[i], px[i+1], px[i+2] = byte(20+band*12), byte(24+band*12), byte(48+band*20)
		}
	}
	for k := range min(keys, 20) {
		for y := 40; y < 56; y++ {
			for x := 20 + k*18; x < 32+k*18; x++ {
				i := (y*width + x) * 3
				px[i], px[i+1], px[i+2] = 250, 200, 60
			}
		}
	}
	for n, t := range touches {
		r := 6
		bright := byte(80 + 175*(n+1)/len(touches))
		if n == len(touches)-1 && down {
			r = 14
		}
		cx, cy := int(t[0]*width), int(t[1]*height)
		for y := cy - r; y <= cy+r; y++ {
			for x := cx - r; x <= cx+r; x++ {
				if x < 0 || y < 0 || x >= width || y >= height || (x-cx)*(x-cx)+(y-cy)*(y-cy) > r*r {
					continue
				}
				i := (y*width + x) * 3
				px[i], px[i+1], px[i+2] = bright, 255, bright
			}
		}
	}
}
