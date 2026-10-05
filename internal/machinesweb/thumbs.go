package machinesweb

import (
	"context"
	"errors"
	"image"
	"image/draw"
	_ "image/gif" // decoders for image.Decode
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// thumbWidth is a thumbnail's width, twice the grid's largest column so it
// stays sharp on a high-density screen.
const thumbWidth = 640

// errNoThumb says no thumbnail can be made: a format Go can't decode, or a
// recording with no ffmpeg on this machine. The page falls back to the file
// itself (an image) or the video's own first frame.
var errNoThumb = errors.New("no thumbnail for this item")

// makeThumb writes a JPEG thumbnail of the file at src to dst: images decoded
// and scaled here, recordings' poster frames by ffmpeg when there is one.
func makeThumb(ctx context.Context, src, dst, kind string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	defer func() { _ = os.Remove(tmp) }()
	if kind == "recording" {
		ffmpeg, err := exec.LookPath("ffmpeg")
		if err != nil {
			return errNoThumb
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// A second in, past a recording's usual blank first frame; -ss
		// before -i seeks by keyframe, which is fast, and a recording
		// shorter than that yields nothing, so try its start then.
		for _, at := range []string{"1", "0"} {
			cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-y", "-ss", at, "-i", src,
				"-frames:v", "1", "-vf", "scale='min(iw,"+strconv.Itoa(thumbWidth)+")':-2", "-q:v", "4", "-f", "image2", tmp)
			if cmd.Run() == nil {
				if fi, err := os.Stat(tmp); err == nil && fi.Size() > 0 {
					return os.Rename(tmp, dst)
				}
			}
		}
		return errNoThumb
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	if err != nil {
		return errNoThumb
	}
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	err = jpeg.Encode(out, scale(img, thumbWidth), &jpeg.Options{Quality: 82})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// scale shrinks img to width w, keeping its aspect, by averaging each
// destination pixel's box of source pixels: sharp enough for a thumbnail,
// and no dependency for it. An image no wider than w is only flattened.
func scale(img image.Image, w int) *image.RGBA {
	b := img.Bounds()
	src := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	// White under a transparent screenshot, as JPEG has no alpha.
	draw.Draw(src, src.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Over)
	sw, sh := b.Dx(), b.Dy()
	if sw <= w || sw == 0 {
		return src
	}
	h := max(1, sh*w/sw)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := range w {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, n uint32
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r, g, bl = r+uint32(p[0]), g+uint32(p[1]), bl+uint32(p[2])
					n++
				}
			}
			d := dst.Pix[y*dst.Stride+x*4:]
			d[0], d[1], d[2], d[3] = uint8(r/n), uint8(g/n), uint8(bl/n), 255
		}
	}
	return dst
}

// copyToTemp saves r to a temporary file, for a thumbnail of a file only
// reachable through the daemon's API. The caller removes it.
func copyToTemp(dir string, r io.Reader) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".src-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
