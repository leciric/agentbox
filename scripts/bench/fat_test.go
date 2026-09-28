package main

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

// readFAT reads back the root directory of an image fatImage made: long names
// joined up, and each file's data followed along its FAT chain.
func readFAT(t *testing.T, img []byte) (label string, files map[string]string) {
	t.Helper()
	const sector, dataStart = 512, 33
	fat := img[sector : 10*sector]
	next := func(n int) int {
		off := n * 3 / 2
		v := int(binary.LittleEndian.Uint16(fat[off:]))
		if n%2 == 0 {
			return v & 0xFFF
		}
		return v >> 4
	}
	files = map[string]string{}
	root := img[19*sector : dataStart*sector]
	var long []uint16
	for i := 0; i+32 <= len(root) && root[i] != 0; i += 32 {
		e := root[i : i+32]
		switch {
		case e[11] == 0x0F:
			var part []uint16
			for _, o := range []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
				part = append(part, binary.LittleEndian.Uint16(e[o:]))
			}
			long = append(part, long...)
		case e[11] == 0x08:
			label = strings.TrimSpace(string(e[:11]))
		default:
			name := strings.TrimSpace(string(e[:8]))
			if len(long) > 0 {
				end := len(long)
				for j, u := range long {
					if u == 0 {
						end = j
						break
					}
				}
				name = string(utf16.Decode(long[:end]))
			}
			long = nil
			size := int(binary.LittleEndian.Uint32(e[28:]))
			var data []byte
			for c := int(binary.LittleEndian.Uint16(e[26:])); c >= 2 && c < 0xFF8 && len(data) < size; c = next(c) {
				data = append(data, img[(dataStart+c-2)*sector:(dataStart+c-1)*sector]...)
			}
			files[name] = string(data[:size])
		}
	}
	return label, files
}

func TestFATImageReadsBack(t *testing.T) {
	big := strings.Repeat("x", 1500) // three clusters
	img, err := fatImage("CIDATA", map[string][]byte{
		"meta-data": []byte("instance-id: a\n"),
		"user-data": []byte("#cloud-config\n" + big),
		"empty":     nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(img) != 1474560 || !bytes.Equal(img[510:512], []byte{0x55, 0xAA}) {
		t.Fatalf("not a 1.44 MB boot sector")
	}
	label, files := readFAT(t, img)
	if label != "CIDATA" {
		t.Errorf("label %q", label)
	}
	want := map[string]string{"meta-data": "instance-id: a\n", "user-data": "#cloud-config\n" + big, "empty": ""}
	for name, data := range want {
		if files[name] != data {
			t.Errorf("%s: got %d bytes, want %d", name, len(files[name]), len(data))
		}
	}

	// With dosfstools there, the kernel's own idea of FAT checks it too.
	if _, err := exec.LookPath("fsck.fat"); err == nil {
		path := filepath.Join(t.TempDir(), "seed.img")
		if err := os.WriteFile(path, img, 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("fsck.fat", "-n", "-v", path).CombinedOutput(); err != nil || !strings.Contains(string(out), "user-data") && !strings.Contains(string(out), "files") {
			t.Errorf("fsck.fat: %v\n%s", err, out)
		}
	}
}

func TestShortNames(t *testing.T) {
	for name, want := range map[string]string{"user-data": "USER-D~1   ", "SEED.IMG": "SEED    IMG", "network-config": "NETWOR~3   "} {
		n := 1
		if name == "network-config" {
			n = 3
		}
		if got := shortName(name, n); got != want {
			t.Errorf("shortName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestWriteSeedForManualCheck(t *testing.T) {
	path := os.Getenv("BENCH_WRITE_SEED")
	if path == "" {
		t.Skip("BENCH_WRITE_SEED names where to write a seed image, to look at with mdir")
	}
	img, err := fatImage("CIDATA", map[string][]byte{"meta-data": []byte("instance-id: a\n"), "user-data": []byte(guestUserData("ssh-ed25519 AAAA test"))})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, img, 0o644); err != nil {
		t.Fatal(err)
	}
}
