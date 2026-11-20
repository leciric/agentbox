// Package strutil has small string helpers.
package strutil

import (
	"strings"
	"unicode"
)

// Reverse returns s with its runes in reverse order.
func Reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// IsPalindrome reports whether s reads the same backwards, ignoring case and
// anything that isn't a letter or a digit.
func IsPalindrome(s string) bool {
	var r []rune
	for _, c := range s {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			r = append(r, unicode.ToLower(c))
		}
	}
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		if r[i] != r[j] {
			return false
		}
	}
	return true
}

// Truncate returns s cut to at most n runes, the last of them "…" when it
// was cut. An n below 1 gives "".
func Truncate(s string, n int) string {
	if n < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// CountWords returns how many words s has, words being what is separated by
// white space.
func CountWords(s string) int {
	return len(strings.Fields(s))
}
