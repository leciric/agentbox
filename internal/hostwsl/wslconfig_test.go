package hostwsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWSLCeiling(t *testing.T) {
	for total, want := range map[int64]int64{
		32 * gibBytes: 24 * gibBytes, // a quarter kept
		16 * gibBytes: 12 * gibBytes, // 4 GiB kept
		6 * gibBytes:  4 * gibBytes,  // never under 4
		0:             0,
	} {
		if got := wslCeiling(total); got != want {
			t.Errorf("wslCeiling(%d GiB) = %d GiB, want %d", total/gibBytes, got/gibBytes, want/gibBytes)
		}
	}
}

func TestFitWSLConfig(t *testing.T) {
	ceiling := wslCeiling(32 * gibBytes) // 24 GiB
	for _, tc := range []struct{ name, in, want string }{
		{"too much", "[wsl2]\nmemory=30GB\nprocessors=8\n", "[wsl2]\nmemory=24GB\nprocessors=8\n"},
		{"in MB, Windows line ends", "[wsl2]\r\n memory = 28672MB\r\n", "[wsl2]\r\nmemory=24GB\r\n"},
		{"within", "[WSL2]\nmemory=16GB\n", "[WSL2]\nmemory=16GB\n"},
		{"another section's", "[experimental]\nmemory=30GB\n", "[experimental]\nmemory=30GB\n"},
		{"unset: WSL's half is within", "[wsl2]\nnestedVirtualization=true\n", "[wsl2]\nnestedVirtualization=true\n"},
		{"no file", "", ""},
	} {
		got, changed := fitWSLConfig(tc.in, ceiling)
		if got != tc.want || changed != (tc.in != tc.want) {
			t.Errorf("%s: %q, changed %v; want %q", tc.name, got, changed, tc.want)
		}
	}
}

func TestFitMemoryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".wslconfig")
	if err := os.WriteFile(path, []byte("[wsl2]\nmemory=31GB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var said strings.Builder
	logf := func(format string, args ...any) { said.WriteString(format) }
	if err := fitMemoryFile(path, 32*gibBytes, logf); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "[wsl2]\nmemory=24GB\n" || !strings.Contains(said.String(), "wsl --shutdown") {
		t.Errorf("file %q, said %q", b, said.String())
	}
	// The computer's memory unknown: nothing touched.
	if err := fitMemoryFile(filepath.Join(t.TempDir(), "none"), 0, logf); err != nil {
		t.Error(err)
	}
}
