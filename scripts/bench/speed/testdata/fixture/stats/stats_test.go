package stats

import "testing"

func TestSum(t *testing.T) {
	for _, c := range []struct {
		xs   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{1}, 1},
		{[]float64{1, 2, 3.5}, 6.5},
	} {
		if got := Sum(c.xs); got != c.want {
			t.Errorf("Sum(%v) = %v, want %v", c.xs, got, c.want)
		}
	}
}

func TestMean(t *testing.T) {
	for _, c := range []struct {
		xs   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{4}, 4},
		{[]float64{1, 2, 3, 6}, 3},
	} {
		if got := Mean(c.xs); got != c.want {
			t.Errorf("Mean(%v) = %v, want %v", c.xs, got, c.want)
		}
	}
}

func TestMax(t *testing.T) {
	for _, c := range []struct {
		xs   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{-3}, -3},
		{[]float64{1, 9, 3}, 9},
	} {
		if got := Max(c.xs); got != c.want {
			t.Errorf("Max(%v) = %v, want %v", c.xs, got, c.want)
		}
	}
}

func TestMedian(t *testing.T) {
	for _, c := range []struct {
		xs   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{7}, 7},
		{[]float64{3, 1, 2}, 2},
		{[]float64{4, 1, 3, 2}, 2.5},
		{[]float64{11, 1, 9, 3, 7, 5}, 6},
	} {
		if got := Median(c.xs); got != c.want {
			t.Errorf("Median(%v) = %v, want %v", c.xs, got, c.want)
		}
	}
}

func TestMedianKeepsInput(t *testing.T) {
	xs := []float64{3, 1, 2}
	Median(xs)
	if xs[0] != 3 || xs[1] != 1 || xs[2] != 2 {
		t.Errorf("Median reordered its input: %v", xs)
	}
}
