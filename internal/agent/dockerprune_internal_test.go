package agent

import "testing"

// TestDockerReclaimed reads what Docker 29's prunes print.
func TestDockerReclaimed(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want int64
	}{
		{"ID\tRECLAIMABLE\tSIZE\tLAST ACCESSED\nnjrs\ttrue\t6.724MB\tLess than a second ago\nTotal:\t27.72MB\n", 27_720_000},
		{"Deleted Images:\nuntagged: alpine:latest\n\nTotal reclaimed space: 17.2GB\n", 17_200_000_000},
		{"Total:\t0B\n", 0},
		{"Total reclaimed space: 0B", 0},
		{"", 0},
		{"Error: something else", 0},
	} {
		if got := dockerReclaimed(tc.out); got != tc.want {
			t.Errorf("dockerReclaimed(%q) = %d, want %d", tc.out, got, tc.want)
		}
	}
}
