package agent_test

import (
	"slices"
	"testing"

	"agentbox/internal/agent"
)

func repeat(n, v int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestAllocateCPU(t *testing.T) {
	tests := []struct {
		name     string
		budget   int
		ceilings []int
		want     []int
	}{
		{"no agents", 14, nil, nil},
		{"seven agents at the ceiling of two, fit in fourteen", 14, repeat(7, 2), repeat(7, 2)},
		{"an eighth leaves six agents with two and two with one", 14, repeat(8, 2),
			append(repeat(6, 2), repeat(2, 1)...)},
		{"nine agents leaves five with two and four with one", 14, repeat(9, 2),
			append(repeat(5, 2), repeat(4, 1)...)},
		{"one unlimited agent gets the whole budget", 15, []int{15}, []int{15}},
		{"two unlimited agents split into seven and eight", 15, []int{15, 15}, []int{7, 8}},
		{"more agents than the budget: everyone gets one", 4, repeat(6, 2), repeat(6, 1)},
		{"a ceiling below the budget is never raised to fill it", 14, []int{1, 1}, []int{1, 1}},
		{"a smaller ceiling is never taken below itself to spare a larger one",
			5, []int{1, 8}, []int{1, 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agent.AllocateCPU(tt.budget, tt.ceilings)
			gotSorted, wantSorted := slices.Clone(got), slices.Clone(tt.want)
			slices.Sort(gotSorted)
			slices.Sort(wantSorted)
			if !slices.Equal(gotSorted, wantSorted) {
				t.Fatalf("AllocateCPU(%d, %v) = %v, want %v", tt.budget, tt.ceilings, got, tt.want)
			}
			total := 0
			for _, c := range got {
				total += c
			}
			if total > tt.budget && len(got) > 0 && total > len(got) {
				t.Fatalf("AllocateCPU(%d, %v) = %v sums to %d, over budget", tt.budget, tt.ceilings, got, total)
			}
		})
	}
}
