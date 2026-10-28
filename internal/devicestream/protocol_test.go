package devicestream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMessagesRoundTrip(t *testing.T) {
	avcC := []byte{1, 0x42, 0xc0, 0x1f, 0xff}
	m, err := DecodeMessage(EncodeConfig(Config{Platform: "android", Codec: "avc1.42c01f", Width: 720, Height: 1600, Description: avcC}))
	if err != nil || m.Config == nil {
		t.Fatalf("config: %+v, %v", m, err)
	}
	if c := m.Config; c.V != Version || c.Platform != "android" || c.Width != 720 || c.Height != 1600 || !bytes.Equal(c.Description, avcC) {
		t.Errorf("config = %+v", c)
	}

	at := time.UnixMicro(1_700_000_000_123_456)
	frame := EncodeFrame(Packet{Key: true, At: at, Data: []byte{0, 0, 0, 1, 0x65}})
	if frame[0] != 0x02 || frame[1] != 1 || binary.BigEndian.Uint64(frame[2:10]) != uint64(at.UnixMicro()) {
		t.Errorf("frame header = % x", frame[:10])
	}
	m, err = DecodeMessage(frame)
	if err != nil || m.Packet == nil || !m.Packet.Key || !m.Packet.At.Equal(at) || !bytes.Equal(m.Packet.Data, []byte{0, 0, 0, 1, 0x65}) {
		t.Errorf("frame: %+v, %v", m.Packet, err)
	}
	m, err = DecodeMessage(EncodeFrame(Packet{At: at}))
	if err != nil || m.Packet.Key {
		t.Errorf("a frame that isn't a keyframe: %+v, %v", m.Packet, err)
	}

	m, err = DecodeMessage(EncodeStatus(errors.New("the emulator stopped")))
	if err != nil || m.Err != "the emulator stopped" {
		t.Errorf("status: %+v, %v", m, err)
	}

	for _, bad := range [][]byte{nil, {0x02, 1}, {0x09}, {0x01, '{'}} {
		if _, err := DecodeMessage(bad); err == nil {
			t.Errorf("DecodeMessage(% x) took it", bad)
		}
	}
}

func TestDecodeInput(t *testing.T) {
	in, err := DecodeInput([]byte(`{"type":"touch","action":"move","x":0.25,"y":0.75,"id":1,"future":true}`))
	if err != nil || in != (Input{Type: InputTouch, Action: "move", X: 0.25, Y: 0.75, ID: 1}) {
		t.Errorf("touch = %+v, %v", in, err)
	}
	in, err = DecodeInput([]byte(`{"type":"text","text":"héllo"}`))
	if err != nil || in.Text != "héllo" {
		t.Errorf("text = %+v, %v", in, err)
	}
	for _, bad := range []string{`{}`, `nope`, `{"x":1}`} {
		if _, err := DecodeInput([]byte(bad)); err == nil {
			t.Errorf("DecodeInput(%s) took it", bad)
		}
	}
}

func TestCodecString(t *testing.T) {
	codec, err := CodecString([]byte{1, 0x42, 0xc0, 0x2a, 0xff, 0xe1})
	if err != nil || codec != "avc1.42c02a" {
		t.Errorf("CodecString = %q, %v", codec, err)
	}
	if _, err := CodecString([]byte{0, 0x42, 0xc0, 0x2a}); err == nil {
		t.Error("took a record whose version isn't 1")
	}
}

func TestEncoderArgs(t *testing.T) {
	args := EncoderArgs(720, 1600)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-f rawvideo -pix_fmt rgb24 -s 720x1600",
		"-i pipe:0",
		"-fps_mode passthrough", // one frame out for each in
		"-c:v libx264",
		"-tune zerolatency",
		"-profile:v baseline",
		"-bf 0",
		"keyint=infinite",
		"scenecut=0",
		"-f flv",
		"-flush_packets 1",
		"pipe:1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("EncoderArgs lacks %q: %s", want, joined)
		}
	}
	if slices.Contains(args, "-b:v") {
		t.Errorf("EncoderArgs set a bitrate, which frames sent only on change make meaningless: %s", joined)
	}
}

func TestRecordArgs(t *testing.T) {
	joined := strings.Join(RecordArgs(720, 1600, 30, "/tmp/out.mp4"), " ")
	for _, want := range []string{"-s 720x1600 -framerate 30", "-c:v libx264", "-movflags +faststart", "/tmp/out.mp4"} {
		if !strings.Contains(joined, want) {
			t.Errorf("RecordArgs lacks %q: %s", want, joined)
		}
	}
}

// flvTag is an FLV tag of type typ, and its PreviousTagSize.
func flvTag(typ byte, data []byte) []byte {
	out := []byte{typ, byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data)), 0, 0, 0, 0, 0, 0, 0}
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, uint32(len(data)+11))
}

func TestFLVReader(t *testing.T) {
	stream := []byte{'F', 'L', 'V', 1, 1, 0, 0, 0, 9, 0, 0, 0, 0}
	stream = append(stream, flvTag(18, []byte("onMetaData"))...)                         // script: skipped
	stream = append(stream, flvTag(9, []byte{0x17, 0, 0, 0, 0, 1, 0x42, 0xc0, 0x1f})...) // the decoder's configuration
	stream = append(stream, flvTag(9, []byte{0x17, 1, 0, 0, 0, 0, 0, 0, 1, 0x65})...)    // a keyframe
	stream = append(stream, flvTag(8, []byte{0xaf, 1})...)                               // audio: skipped
	stream = append(stream, flvTag(9, []byte{0x27, 1, 0, 0, 0, 0, 0, 0, 1, 0x41})...)    // a frame
	stream = append(stream, flvTag(9, []byte{0x17, 2, 0, 0, 0})...)                      // the end
	r := NewFLVReader(bytes.NewReader(stream))

	want := []FLVTag{
		{Config: true, Data: []byte{1, 0x42, 0xc0, 0x1f}},
		{Key: true, Data: []byte{0, 0, 0, 1, 0x65}},
		{Data: []byte{0, 0, 0, 1, 0x41}},
	}
	for i, w := range want {
		tag, err := r.Next()
		if err != nil {
			t.Fatalf("tag %d: %v", i, err)
		}
		if tag.Config != w.Config || tag.Key != w.Key || !bytes.Equal(tag.Data, w.Data) {
			t.Errorf("tag %d = %+v, want %+v", i, tag, w)
		}
	}
	if _, err := r.Next(); err == nil {
		t.Error("read past the end")
	}

	if _, err := NewFLVReader(strings.NewReader("MP4 nonsense!")).Next(); err == nil {
		t.Error("took a stream that isn't FLV")
	}
	hevc := append([]byte{'F', 'L', 'V', 1, 1, 0, 0, 0, 9, 0, 0, 0, 0}, flvTag(9, []byte{0x1c, 1, 0, 0, 0})...)
	if _, err := NewFLVReader(bytes.NewReader(hevc)).Next(); err == nil {
		t.Error("took video that isn't H.264")
	}
}

func TestPixels(t *testing.T) {
	// A 4x2 frame whose left half is red and right half blue.
	f := Frame{Width: 4, Height: 2, Pixels: make([]byte, 4*2*3)}
	for y := range 2 {
		for x := range 4 {
			if x < 2 {
				f.Pixels[(y*4+x)*3] = 255
			} else {
				f.Pixels[(y*4+x)*3+2] = 255
			}
		}
	}
	var buf []byte
	if got := Pixels(f, 4, 2, &buf); &got[0] != &f.Pixels[0] || buf != nil {
		t.Error("a frame of the right size was copied")
	}
	// Turned on its side into 2x4: scaled to 2x1, centered, black around it.
	got := Pixels(f, 2, 4, &buf)
	if len(got) != 2*4*3 || &got[0] != &buf[0] {
		t.Fatalf("Pixels into a 2x4 image gave %d bytes", len(got))
	}
	row := func(y int) []byte { return got[y*2*3 : (y+1)*2*3] }
	for _, y := range []int{0, 2, 3} {
		if !bytes.Equal(row(y), make([]byte, 6)) {
			t.Errorf("row %d = % x, want black", y, row(y))
		}
	}
	if want := []byte{255, 0, 0, 0, 0, 255}; !bytes.Equal(row(1), want) {
		t.Errorf("row 1 = % x, want % x", row(1), want)
	}
	if evenSize(721) != 720 || evenSize(720) != 720 {
		t.Error("evenSize doesn't round down to even")
	}
}

func TestPoint(t *testing.T) {
	for _, c := range []struct {
		x, y   float64
		px, py int
	}{
		{0, 0, 0, 0},
		{0.5, 0.5, 540, 1200},
		{1, 1, 1079, 2399},
		{-0.2, 1.5, 0, 2399},
	} {
		if px, py := Point(c.x, c.y, 1080, 2400); px != c.px || py != c.py {
			t.Errorf("Point(%v, %v) = %d, %d, want %d, %d", c.x, c.y, px, py, c.px, c.py)
		}
	}
}
