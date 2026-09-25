package state

import "context"

// CountFeature adds one use of feature to day's count.
func (s *Store) CountFeature(ctx context.Context, day, feature string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feature_usage (day, feature, count) VALUES (?, ?, 1)
		 ON CONFLICT(day, feature) DO UPDATE SET count = count + 1`, day, feature)
	return err
}

// FeatureUsage is every day before before (a UTC day, YYYY-MM-DD) with its
// counts, oldest first. The day still going on is left out, since its counts
// aren't final.
func (s *Store) FeatureUsage(ctx context.Context, before string) ([]string, map[string]map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT day, feature, count FROM feature_usage WHERE day < ? ORDER BY day, feature`, before)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var days []string
	counts := map[string]map[string]int64{}
	for rows.Next() {
		var day, feature string
		var n int64
		if err := rows.Scan(&day, &feature, &n); err != nil {
			return nil, nil, err
		}
		if counts[day] == nil {
			days = append(days, day)
			counts[day] = map[string]int64{}
		}
		counts[day][feature] = n
	}
	return days, counts, rows.Err()
}

// ForgetFeatureUsage deletes the counts of every day up to and including
// through, or all of them when through is "".
func (s *Store) ForgetFeatureUsage(ctx context.Context, through string) error {
	if through == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM feature_usage`)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM feature_usage WHERE day <= ?`, through)
	return err
}
