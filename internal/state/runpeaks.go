package state

import (
	"context"
	"time"
)

// RunPeak is what a project's run phase was learned to need at its peak
// (internal/runslots): the highest an agent's machine's memory reached while
// it held a run slot, as the daemon learned it over Runs runs.
type RunPeak struct {
	Project string
	Bytes   int64
	Runs    int
	At      time.Time
}

// SetRunPeak keeps a project's learned run peak.
func (s *Store) SetRunPeak(ctx context.Context, p RunPeak) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_peaks (project, bytes, runs, at) VALUES (?, ?, ?, ?)
		ON CONFLICT (project) DO UPDATE SET bytes = excluded.bytes, runs = excluded.runs, at = excluded.at`,
		p.Project, p.Bytes, p.Runs, p.At.UnixMilli())
	return err
}

// RunPeaks lists every project's learned run peak, by project.
func (s *Store) RunPeaks(ctx context.Context) ([]RunPeak, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, bytes, runs, at FROM run_peaks ORDER BY project`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RunPeak
	for rows.Next() {
		var p RunPeak
		var at int64
		if err := rows.Scan(&p.Project, &p.Bytes, &p.Runs, &at); err != nil {
			return nil, err
		}
		p.At = time.UnixMilli(at)
		out = append(out, p)
	}
	return out, rows.Err()
}
