package devicestream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// RecordArgs are ffmpeg's arguments to record width x height RGB24 frames,
// fps a second, from its stdin to an MP4 file.
func RecordArgs(width, height, fps int, file string) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-s", fmt.Sprintf("%dx%d", width, height), "-framerate", fmt.Sprint(fps),
		"-i", "pipe:0",
		"-an", "-pix_fmt", "yuv420p",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-movflags", "+faststart",
		file,
	}
}

// recordFPS is the recording's frame rate: the screen's last frame is
// repeated while it doesn't change.
const recordFPS = 30

// Record records the device's screen to an MP4 file until ctx ends or limit
// passes, and finishes the file either way. The video keeps the size of the
// first frame; a frame of another size (the device turned) is fitted into it.
func Record(ctx context.Context, dev Device, file string, limit time.Duration) error {
	screenCtx, stopScreen := context.WithCancel(context.WithoutCancel(ctx))
	defer stopScreen()
	var mu sync.Mutex
	var latest *Frame
	first := make(chan struct{})
	screenDone := make(chan error, 1)
	go func() {
		screenDone <- dev.Screen(screenCtx, func(f Frame) error {
			mu.Lock()
			if latest == nil {
				close(first)
			}
			latest = &f
			mu.Unlock()
			return nil
		})
	}()
	select {
	case <-first:
	case err := <-screenDone:
		if err == nil {
			err = errors.New("the device sent no screen")
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
	mu.Lock()
	width, height := evenSize(latest.Width), evenSize(latest.Height)
	mu.Unlock()

	cmd := exec.Command(FFmpeg, RecordArgs(width, height, recordFPS, file)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limited{w: &stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting ffmpeg: %w", err)
	}
	finish := func(cause error) error {
		_ = stdin.Close()
		err := cmd.Wait()
		if msg := strings.TrimSpace(stderr.String()); err != nil && msg != "" {
			err = fmt.Errorf("ffmpeg: %s", msg)
		}
		if cause != nil {
			return cause
		}
		return err
	}

	end := time.After(limit)
	tick := time.NewTicker(time.Second / recordFPS)
	defer tick.Stop()
	var canvas []byte
	for {
		mu.Lock()
		f := *latest
		mu.Unlock()
		if _, err := stdin.Write(Pixels(f, width, height, &canvas)); err != nil {
			return finish(fmt.Errorf("ffmpeg: %w", err))
		}
		select {
		case <-ctx.Done():
			return finish(nil)
		case <-end:
			return finish(nil)
		case <-screenDone:
			// The device went away: what was recorded is kept.
			return finish(nil)
		case <-tick.C:
		}
	}
}

// Pixels returns f's pixels as a width x height RGB24 image: f's own when it
// is that size, and otherwise f scaled to fit, centered on black, in buf.
func Pixels(f Frame, width, height int, buf *[]byte) []byte {
	if f.Width == width && f.Height == height {
		return f.Pixels
	}
	*buf = fit(f, width, height, *buf)
	return *buf
}

// fit scales f to fit a width x height image, centered on black, into buf
// when it is big enough.
func fit(f Frame, width, height int, buf []byte) []byte {
	size := width * height * 3
	if cap(buf) < size {
		buf = make([]byte, size)
	}
	buf = buf[:size]
	clear(buf)
	// The largest scale at which f fits.
	w, h := width, f.Height*width/f.Width
	if h > height {
		w, h = f.Width*height/f.Height, height
	}
	x0, y0 := (width-w)/2, (height-h)/2
	for y := range h {
		sy := y * f.Height / h
		row := buf[((y0+y)*width+x0)*3:]
		src := f.Pixels[sy*f.Width*3:]
		for x := range w {
			sx := x * f.Width / w * 3
			copy(row[x*3:x*3+3], src[sx:sx+3])
		}
	}
	return buf
}
