// Package stats has the summary statistics the generated packages use.
package stats

import "sort"

// Sum returns the sum of xs.
func Sum(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s
}

// Mean returns the arithmetic mean of xs, or 0 for an empty slice.
func Mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	return Sum(xs) / float64(len(xs))
}

// Max returns the largest of xs, or 0 for an empty slice.
func Max(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

// Median returns the middle value of xs, or the mean of the two middle values
// when it has an even number of them; 0 for an empty slice. xs is left as it
// was.
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m+1]) / 2
}
