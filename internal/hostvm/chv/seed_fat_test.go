package chv

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// readFAT reads a FAT12 image back: its label and its root directory's files,
// by their long names, followed through the FAT.
func readFAT(t *testing.T, img []byte) (bootLabel, dirLabel string, files map[string][]byte) {
	t.Helper()
	le := binary.LittleEndian
	if img[510] != 0x55 || img[511] != 0xAA {
		t.Fatal("no boot signature")
	}
	sector := int(le.Uint16(img[11:]))
	reserved := int(le.Uint16(img[14:]))
	fats := int(img[16])
	rootEntries := int(le.Uint16(img[17:]))
	total := int(le.Uint16(img[19:]))
	fatSize := int(le.Uint16(img[22:]))
	if sector != 512 || img[13] != 1 || total*sector != len(img) || string(img[54:62]) != "FAT12   " {
		t.Fatalf("unexpected BPB: sector %d, total %d (image %d), fs %q", sector, total, len(img), img[54:62])
	}
	fat := img[reserved*sector : (reserved+fatSize)*sector]
	for k := 1; k < fats; k++ {
		if !bytes.Equal(fat, img[(reserved+k*fatSize)*sector:(reserved+(k+1)*fatSize)*sector]) {
			t.Fatal("the FATs differ")
		}
	}
	entry := func(n int) int {
		off := n * 3 / 2
		v := int(le.Uint16(fat[off:]))
		if n%2 == 1 {
			return v >> 4
		}
		return v & 0xFFF
	}
	rootStart := (reserved + fats*fatSize) * sector
	dataStart := rootStart + rootEntries*32
	files = map[string][]byte{}
	var long []uint16
	for i := range rootEntries {
		d := img[rootStart+i*32 : rootStart+i*32+32]
		switch {
		case d[0] == 0:
			return string(img[43:54]), dirLabel, files
		case d[11] == 0x0F:
			seq := int(d[0] & 0x1F)
			if d[0]&0x40 != 0 {
				long = make([]uint16, seq*13)
			}
			for k, off := range []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
				long[(seq-1)*13+k] = le.Uint16(d[off:])
			}
		case d[11]&0x08 != 0:
			dirLabel = string(d[:11])
		default:
			name := string(d[:11])
			if long != nil {
				end := 0
				for end < len(long) && long[end] != 0 && long[end] != 0xFFFF {
					end++
				}
				name = string(utf16.Decode(long[:end]))
				long = nil
			}
			size := int(le.Uint32(d[28:]))
			var data []byte
			for c := int(le.Uint16(d[26:])); size > 0 && c < 0xFF8; c = entry(c) {
				data = append(data, img[dataStart+(c-2)*sector:dataStart+(c-1)*sector]...)
			}
			files[name] = data[:size]
		}
	}
	return string(img[43:54]), dirLabel, files
}

func TestFATImage(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789abcdef"), 700) // 11200 bytes: 22 clusters
	files := []seedFile{
		{Name: "meta-data", Data: []byte("instance-id: x\n")},
		{Name: "user-data", Data: big},
		{Name: "network-config", Data: []byte("version: 2\n")},
		{Name: "empty", Data: nil},
	}
	img, err := fatImage("CIDATA", files)
	if err != nil {
		t.Fatal(err)
	}
	if len(img) != 1<<20 {
		t.Errorf("image is %d bytes, want 1 MiB", len(img))
	}
	bootLabel, dirLabel, got := readFAT(t, img)
	if bootLabel != "CIDATA     " || dirLabel != "CIDATA     " {
		t.Errorf("labels %q and %q, want CIDATA", bootLabel, dirLabel)
	}
	for _, f := range files {
		if !bytes.Equal(got[f.Name], f.Data) {
			t.Errorf("%s: got %d bytes, want %d", f.Name, len(got[f.Name]), len(f.Data))
		}
	}
	if len(got) != len(files) {
		t.Errorf("got files %v", got)
	}
	again, _ := fatImage("CIDATA", files)
	if !bytes.Equal(img, again) {
		t.Error("the same files made a different image")
	}

	// mtools, when there is one, reads it as FAT does everywhere.
	if _, err := exec.LookPath("mdir"); err == nil {
		file := filepath.Join(t.TempDir(), "seed.img")
		_ = os.WriteFile(file, img, 0o644)
		out, err := exec.Command("mdir", "-i", file, "::").CombinedOutput()
		if err != nil {
			t.Fatalf("mdir: %v\n%s", err, out)
		}
		for _, want := range []string{"CIDATA", "user-data", "meta-data", "network-config"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("mdir shows no %s:\n%s", want, out)
			}
		}
		out, err = exec.Command("mtype", "-i", file, "::user-data").Output()
		if err != nil || !bytes.Equal(out, big) {
			t.Errorf("mtype user-data: %v, %d bytes", err, len(out))
		}
	}
	if _, err := exec.LookPath("fsck.fat"); err == nil {
		file := filepath.Join(t.TempDir(), "seed.img")
		_ = os.WriteFile(file, img, 0o644)
		if out, err := exec.Command("fsck.fat", "-n", file).CombinedOutput(); err != nil {
			t.Errorf("fsck.fat: %v\n%s", err, out)
		}
	}
}

func TestFATImageGrowsForLargeFiles(t *testing.T) {
	img, err := fatImage("CIDATA", []seedFile{{Name: "user-data", Data: bytes.Repeat([]byte{1}, 1500*512)}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, got := readFAT(t, img)
	if len(got["user-data"]) != 1500*512 {
		t.Errorf("got %d bytes", len(got["user-data"]))
	}
	if _, err := fatImage("CIDATA", []seedFile{{Name: "x", Data: make([]byte, 3<<20)}}); err == nil {
		t.Error("3 MiB fit in FAT12")
	}
}

func TestShortName(t *testing.T) {
	for long, want := range map[string]string{
		"user-data":      "USER-D~1   ",
		"network-config": "NETWOR~1   ",
		"a.b":            "A_B~1      ",
	} {
		s := shortName(long, 1)
		if string(s[:]) != want {
			t.Errorf("shortName(%q) = %q, want %q", long, s, want)
		}
	}
}
