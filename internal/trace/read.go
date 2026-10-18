package trace

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"agentbox/internal/api"
)

// Read reads the spans that ended at or after since from a file in dir and
// its backups, oldest first. A backup last written before since is skipped
// unread, since every span in it ended before then; a line that doesn't
// parse (the last one, cut off by a crash) is skipped too. A missing file is
// no spans.
func Read(dir, file string, since time.Time) ([]Span, error) {
	path := filepath.Join(dir, file)
	var paths []string
	for i := maxBackups; i >= 1; i-- {
		paths = append(paths, Backup(path, i))
	}
	paths = append(paths, path)
	var out []Span
	for _, p := range paths {
		info, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		if info.ModTime().Before(since) {
			continue
		}
		if out, err = readFile(p, since, out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// maxBackups is how many backups Read looks for: more than either kind of
// file keeps, so a file written with other limits is still read whole.
const maxBackups = 9

func readFile(path string, since time.Time, out []Span) ([]Span, error) {
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var s Span
		if json.Unmarshal(line, &s) != nil || s.Name == "" {
			continue
		}
		if End(s).Before(since) {
			continue
		}
		out = append(out, s)
	}
	return out, sc.Err()
}

// Summary is what a set of spans says: per name, busiest first, and the
// slowest and the latest failures among them.
type Summary struct {
	Stats    []api.SpanStats
	Slowest  []Span
	Failures []Span
	// First and Last are when the earliest and latest spans ended.
	First, Last time.Time
}

// Summarize sums spans up, keeping the slowest and the latest failures, at
// most limit of each. Stats are sorted by total time, then count, then name.
func Summarize(spans []Span, limit int) Summary {
	var sum Summary
	durations := map[string][]float64{}
	errs := map[string]int{}
	for _, s := range spans {
		durations[s.Name] = append(durations[s.Name], s.DurationMS)
		if s.Error != "" {
			errs[s.Name]++
		}
		end := End(s)
		if sum.First.IsZero() || end.Before(sum.First) {
			sum.First = end
		}
		if end.After(sum.Last) {
			sum.Last = end
		}
	}
	for name, d := range durations {
		sort.Float64s(d)
		total := 0.0
		for _, v := range d {
			total += v
		}
		sum.Stats = append(sum.Stats, api.SpanStats{
			Name: name, Count: len(d), Errors: errs[name],
			P50MS: Percentile(d, 0.50), P95MS: Percentile(d, 0.95), MaxMS: d[len(d)-1],
			TotalMS: round(total),
		})
	}
	sort.Slice(sum.Stats, func(i, j int) bool {
		a, b := sum.Stats[i], sum.Stats[j]
		if a.TotalMS != b.TotalMS {
			return a.TotalMS > b.TotalMS
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Name < b.Name
	})

	slowest := append([]Span(nil), spans...)
	sort.SliceStable(slowest, func(i, j int) bool { return slowest[i].DurationMS > slowest[j].DurationMS })
	sum.Slowest = head(slowest, limit)

	var failures []Span
	for i := len(spans) - 1; i >= 0 && len(failures) < limit; i-- {
		if spans[i].Error != "" {
			failures = append(failures, spans[i])
		}
	}
	sort.SliceStable(failures, func(i, j int) bool { return End(failures[i]).After(End(failures[j])) })
	sum.Failures = failures
	if sum.Stats == nil {
		sum.Stats = []api.SpanStats{}
	}
	if sum.Failures == nil {
		sum.Failures = []Span{}
	}
	return sum
}

// Percentile is the nearest-rank percentile p (0 to 1) of sorted values: the
// smallest value at least p of them are at or under. None is 0.
func Percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p * float64(len(sorted))))
	rank = min(max(rank, 1), len(sorted))
	return sorted[rank-1]
}

func round(v float64) float64 { return math.Round(v*1000) / 1000 }

func head(s []Span, n int) []Span {
	if n >= 0 && len(s) > n {
		s = s[:n]
	}
	if s == nil {
		return []Span{}
	}
	return s
}
