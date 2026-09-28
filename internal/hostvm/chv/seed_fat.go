package chv

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"strings"
	"unicode/utf16"
)

// seedFile is one file of the seed's root directory.
type seedFile struct {
	Name string
	Data []byte
}

// A FAT12 image, the smallest thing cloud-init's NoCloud reads a seed from,
// written here rather than with mkfs.vfat or mtools so the host needs neither.
// One 512-byte sector a cluster, files in consecutive clusters, every file in
// the root directory with a long (VFAT) name, since NoCloud's names
// (user-data, meta-data, network-config) aren't 8.3 names.
const (
	fatSector      = 512
	fatReserved    = 1  // the boot sector
	fatRootEntries = 64 // 4 sectors of 32-byte entries
	fatMaxClusters = 4084
)

// fatImage is a FAT12 volume labelled label, holding files, of at least 1 MiB.
// It's the same bytes for the same files: its serial number is taken from them.
func fatImage(label string, files []seedFile) ([]byte, error) {
	if len(label) > 11 {
		return nil, fmt.Errorf("volume label %q is longer than 11 characters", label)
	}
	// The clusters every file takes, and the root directory's entries.
	clusters := 0
	entries := 1 // the volume label
	for _, f := range files {
		clusters += (len(f.Data) + fatSector - 1) / fatSector
		entries += 1 + (len(utf16.Encode([]rune(f.Name)))+12)/13
	}
	if entries > fatRootEntries {
		return nil, fmt.Errorf("too many files for the seed's root directory")
	}
	total := 2048 // sectors: 1 MiB, for cloud-init's sake no smaller
	fatSectors := 0
	for {
		fatSectors = ((total+2)*3/2 + fatSector - 1) / fatSector
		dataStart := fatReserved + 2*fatSectors + fatRootEntries*32/fatSector
		if total-dataStart >= clusters {
			break
		}
		total += 256
	}
	dataStart := fatReserved + 2*fatSectors + fatRootEntries*32/fatSector
	if total-dataStart > fatMaxClusters {
		return nil, fmt.Errorf("the seed's files are too large for FAT12 (%d clusters)", clusters)
	}

	img := make([]byte, total*fatSector)
	serial := crc32.ChecksumIEEE([]byte(label))
	for _, f := range files {
		serial = crc32.Update(serial, crc32.IEEETable, []byte(f.Name))
		serial = crc32.Update(serial, crc32.IEEETable, f.Data)
	}
	padLabel := fmt.Sprintf("%-11s", strings.ToUpper(label))

	// The boot sector, with its BIOS parameter block.
	b := img[:fatSector]
	copy(b[0:], []byte{0xEB, 0x3C, 0x90})
	copy(b[3:], "AGENTBOX")
	binary.LittleEndian.PutUint16(b[11:], fatSector)
	b[13] = 1 // sectors a cluster
	binary.LittleEndian.PutUint16(b[14:], fatReserved)
	b[16] = 2 // FATs
	binary.LittleEndian.PutUint16(b[17:], fatRootEntries)
	binary.LittleEndian.PutUint16(b[19:], uint16(total))
	b[21] = 0xF8 // media: fixed disk
	binary.LittleEndian.PutUint16(b[22:], uint16(fatSectors))
	binary.LittleEndian.PutUint16(b[24:], 32) // sectors a track
	binary.LittleEndian.PutUint16(b[26:], 64) // heads
	b[36] = 0x80                              // drive number
	b[38] = 0x29                              // extended boot signature
	binary.LittleEndian.PutUint32(b[39:], serial)
	copy(b[43:], padLabel)
	copy(b[54:], "FAT12   ")
	b[510], b[511] = 0x55, 0xAA

	fat := make([]byte, fatSectors*fatSector)
	setFAT12(fat, 0, 0xFF8)
	setFAT12(fat, 1, 0xFFF)

	root := img[(fatReserved+2*fatSectors)*fatSector : dataStart*fatSector]
	e := 0
	entry := func() []byte { d := root[e*32 : e*32+32]; e++; return d }
	// The volume label, which blkid reads before the boot sector's.
	d := entry()
	copy(d[0:11], padLabel)
	d[11] = 0x08
	putFATTime(d)

	next := 2 // the first data cluster
	for i, f := range files {
		short := shortName(f.Name, i+1)
		sum := byte(0)
		for _, c := range short {
			sum = (sum&1)<<7 + sum>>1 + c
		}
		name := utf16.Encode([]rune(f.Name))
		n := (len(name) + 12) / 13
		for seq := n; seq >= 1; seq-- {
			d := entry()
			d[0] = byte(seq)
			if seq == n {
				d[0] |= 0x40
			}
			d[11] = 0x0F // a long name's part
			d[13] = sum
			for k, off := range []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
				j := (seq-1)*13 + k
				c := uint16(0xFFFF)
				switch {
				case j < len(name):
					c = name[j]
				case j == len(name):
					c = 0
				}
				binary.LittleEndian.PutUint16(d[off:], c)
			}
		}
		d := entry()
		copy(d[0:11], short[:])
		d[11] = 0x20 // archive
		putFATTime(d)
		size := len(f.Data)
		if size > 0 {
			binary.LittleEndian.PutUint16(d[26:], uint16(next))
			count := (size + fatSector - 1) / fatSector
			for k := range count {
				v := 0xFFF
				if k < count-1 {
					v = next + k + 1
				}
				setFAT12(fat, next+k, v)
			}
			copy(img[(dataStart+next-2)*fatSector:], f.Data)
			next += count
		}
		binary.LittleEndian.PutUint32(d[28:], uint32(size))
	}
	for k := range 2 {
		copy(img[(fatReserved+k*fatSectors)*fatSector:], fat)
	}
	return img, nil
}

// shortName is the 8.3 alias of a long name, like USER-D~1: upper case, with
// what 8.3 names can't hold replaced, and n to tell files apart.
func shortName(long string, n int) [11]byte {
	var s [11]byte
	for i := range s {
		s[i] = ' '
	}
	tail := fmt.Sprintf("~%d", n)
	base := []byte{}
	for _, r := range strings.ToUpper(long) {
		if len(base) == 8-len(tail) {
			break
		}
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			base = append(base, byte(r))
		default:
			base = append(base, '_')
		}
	}
	copy(s[:], append(base, tail...))
	return s
}

// setFAT12 sets FAT12 entry n to v: entries are 12 bits, two to three bytes.
func setFAT12(fat []byte, n, v int) {
	off := n * 3 / 2
	if n%2 == 0 {
		fat[off] = byte(v)
		fat[off+1] = fat[off+1]&0xF0 | byte(v>>8)&0x0F
	} else {
		fat[off] = fat[off]&0x0F | byte(v<<4)
		fat[off+1] = byte(v >> 4)
	}
}

// putFATTime gives a directory entry a fixed time, 2020-01-01 00:00, so the
// same files make the same image.
func putFATTime(d []byte) {
	const date = (2020-1980)<<9 | 1<<5 | 1
	for _, off := range []int{16, 18, 24} { // created, accessed, written
		binary.LittleEndian.PutUint16(d[off:], date)
	}
}
