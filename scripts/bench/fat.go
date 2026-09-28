package main

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
)

// fatImage makes a 1.44 MB FAT12 filesystem labelled label, holding files in
// its root directory. It is the cloud-init NoCloud seed of the prototype's VM:
// cloud-init looks for a vfat or iso9660 filesystem labelled CIDATA, and
// writing one here spares the host mkisofs or mtools, and a seed server that a
// host firewall (ufw on Omarchy) would stand between the VM and.
//
// Names longer than 8.3, like user-data, get VFAT long-name entries.
func fatImage(label string, files map[string][]byte) ([]byte, error) {
	const (
		sector      = 512
		sectors     = 2880
		reserved    = 1
		fats        = 2
		fatSectors  = 9
		rootEntries = 224
		rootSectors = rootEntries * 32 / sector
		dataStart   = reserved + fats*fatSectors + rootSectors
		clusters    = sectors - dataStart
	)
	img := make([]byte, sectors*sector)

	// The boot sector: a 1.44 MB floppy's BIOS parameter block.
	b := img[:sector]
	copy(b, []byte{0xEB, 0x3C, 0x90})
	copy(b[3:], "MSWIN4.1")
	binary.LittleEndian.PutUint16(b[11:], sector)
	b[13] = 1 // sectors per cluster
	binary.LittleEndian.PutUint16(b[14:], reserved)
	b[16] = fats
	binary.LittleEndian.PutUint16(b[17:], rootEntries)
	binary.LittleEndian.PutUint16(b[19:], sectors)
	b[21] = 0xF0 // media
	binary.LittleEndian.PutUint16(b[22:], fatSectors)
	binary.LittleEndian.PutUint16(b[24:], 18) // sectors per track
	binary.LittleEndian.PutUint16(b[26:], 2)  // heads
	b[38] = 0x29                              // extended boot signature
	binary.LittleEndian.PutUint32(b[39:], 0xAB0BE0C1)
	copy(b[43:54], pad(strings.ToUpper(label), 11))
	copy(b[54:62], "FAT12   ")
	b[510], b[511] = 0x55, 0xAA

	fat := make([]byte, fatSectors*sector)
	setFAT := func(n int, v uint16) {
		off := n * 3 / 2
		if n%2 == 0 {
			fat[off] = byte(v)
			fat[off+1] = fat[off+1]&0xF0 | byte(v>>8)&0x0F
		} else {
			fat[off] = fat[off]&0x0F | byte(v<<4)
			fat[off+1] = byte(v >> 4)
		}
	}
	setFAT(0, 0xFF0)
	setFAT(1, 0xFFF)

	root := img[(reserved+fats*fatSectors)*sector : dataStart*sector]
	var entries [][]byte
	vol := make([]byte, 32)
	copy(vol, pad(strings.ToUpper(label), 11))
	vol[11] = 0x08 // volume label
	entries = append(entries, vol)

	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	next := 2 // the first data cluster
	for i, name := range names {
		data := files[name]
		short := shortName(name, i+1)
		if !fitsShort(name) {
			entries = append(entries, longNameEntries(name, short)...)
		}
		e := make([]byte, 32)
		copy(e, short)
		e[11] = 0x20                                // archive
		binary.LittleEndian.PutUint16(e[24:], 0x21) // 1980-01-01, a valid date
		if len(data) > 0 {
			n := (len(data) + sector - 1) / sector
			if next+n > clusters+2 {
				return nil, fmt.Errorf("the seed's files don't fit in %d KiB", clusters/2)
			}
			binary.LittleEndian.PutUint16(e[26:], uint16(next))
			binary.LittleEndian.PutUint32(e[28:], uint32(len(data)))
			for c := range n {
				v := uint16(next + c + 1)
				if c == n-1 {
					v = 0xFFF
				}
				setFAT(next+c, v)
			}
			copy(img[(dataStart+next-2)*sector:], data)
			next += n
		}
		entries = append(entries, e)
	}
	if len(entries) > rootEntries {
		return nil, fmt.Errorf("too many files for the seed's root directory")
	}
	for i, e := range entries {
		copy(root[i*32:], e)
	}
	for i := range fats {
		copy(img[(reserved+i*fatSectors)*sector:], fat)
	}
	return img, nil
}

func pad(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

// fitsShort says whether name is already a valid 8.3 name, in upper case.
func fitsShort(name string) bool {
	base, ext, _ := strings.Cut(name, ".")
	return name == strings.ToUpper(name) && len(base) <= 8 && len(ext) <= 3 && !strings.Contains(ext, ".")
}

// shortName is the 11-byte 8.3 name of a file: the name itself when it fits,
// or its first letters and ~n.
func shortName(name string, n int) string {
	if fitsShort(name) {
		base, ext, _ := strings.Cut(name, ".")
		return pad(base, 8) + pad(ext, 3)
	}
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return -1
	}, name)
	tag := fmt.Sprintf("~%d", n)
	return pad(clean[:min(len(clean), 8-len(tag))]+tag, 8) + "   "
}

// longNameEntries are the VFAT entries that carry name, in the order they go
// on disk: the last part first.
func longNameEntries(name, short string) [][]byte {
	var sum byte
	for i := range 11 {
		sum = (sum&1)<<7 + sum>>1 + short[i]
	}
	units := utf16.Encode([]rune(name))
	if len(units)%13 != 0 {
		units = append(units, 0)
		for len(units)%13 != 0 {
			units = append(units, 0xFFFF)
		}
	}
	parts := len(units) / 13
	var out [][]byte
	for p := parts; p >= 1; p-- {
		e := make([]byte, 32)
		e[0] = byte(p)
		if p == parts {
			e[0] |= 0x40
		}
		e[11] = 0x0F
		e[13] = sum
		chunk := units[(p-1)*13 : p*13]
		offs := []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30}
		for i, u := range chunk {
			binary.LittleEndian.PutUint16(e[offs[i]:], u)
		}
		out = append(out, e)
	}
	return out
}
