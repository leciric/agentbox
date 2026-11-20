package strutil

import "testing"

func TestReverse(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"a", "a"},
		{"abc", "cba"},
		{"héllo", "olléh"},
	} {
		if got := Reverse(c.in); got != c.want {
			t.Errorf("Reverse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsPalindrome(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"", true},
		{"racecar", true},
		{"A man, a plan, a canal: Panama", true},
		{"abc", false},
	} {
		if got := IsPalindrome(c.in); got != c.want {
			t.Errorf("IsPalindrome(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 0, ""},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"héllo", 2, "h…"},
	} {
		if got := Truncate(c.in, c.n); got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestCountWords(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"one", 1},
		{"  two words ", 2},
		{"tabs\tand\nlines", 3},
	} {
		if got := CountWords(c.in); got != c.want {
			t.Errorf("CountWords(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
