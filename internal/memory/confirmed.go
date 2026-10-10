package memory

import (
	"context"
	"math"
	"strconv"
)

// Confirmations: a memory somebody wrote down again.
//
// When dedup folds a restatement into the memory it restates
// (MergeDuplicates, and Consolidate's exact merge), the memory that stays
// counts it. Two agents running into the same thing independently is
// evidence it is real, and that count is the one signal here that nobody
// sets on purpose.
//
// It is weighed the way ai-memory weighs belief strength (its belief.rs):
// a saturating curve, so each further restatement adds less, and never
// enough to outrank what bm25 or a person's importance says. A popular but
// wrong memory must not pin itself at the top: feedback marking it wrong or
// stale clears the count (feedback.go), and superseding it still wins
// outright, since a superseded memory isn't live and is never ranked at all.

// confirmationSaturation is the count at which confirmationScore reaches
// about 63% of its ceiling. Small, because three independent restatements
// already say a memory is real.
const confirmationSaturation = 3.0

// confirmationCap is the most any count scores, below 1 as belief.rs's
// CONFIDENCE_CAP is: the curve alone reaches 1 in floating point by a few
// hundred.
const confirmationCap = 0.95

// confirmationScore stretches a count of confirmations to 0..1: 0 for a
// memory nobody repeated, 0.28 for one, 0.63 for three, and never more than
// confirmationCap.
func confirmationScore(n int) float64 {
	if n <= 0 {
		return 0
	}
	return min(1-math.Exp(-float64(n)/confirmationSaturation), confirmationCap)
}

// ConfirmedAfter is how many confirmations count for one point of importance
// in what a context builder keeps (Standing). One point, however many: a
// memory said five times is not a memory somebody rated 5.
const ConfirmedAfter = 2

// standingSQL is Standing in SQL, for the listings that order by it.
var standingSQL = `(m.importance + (m.confirmations >= ` + strconv.Itoa(ConfirmedAfter) +
	` AND m.importance < ` + strconv.Itoa(MaxImportance) + `))`

// Standing is a memory's importance with its confirmations counted in: one
// point more once ConfirmedAfter restatements were folded into it, never
// past MaxImportance. It is what orders the lists a context builder cuts.
func (m Memory) Standing() int {
	if m.Confirmations >= ConfirmedAfter && m.Importance < MaxImportance {
		return m.Importance + 1
	}
	return m.Importance
}

// confirm counts a restatement on the memory that stays: one for the
// restatement itself, and whatever it had been confirmed already.
func (s *Store) confirm(ctx context.Context, project, into, restated string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE memories SET confirmations = confirmations + 1 +
			(SELECT confirmations FROM memories WHERE project = ? AND id = ?)
		 WHERE project = ? AND id = ?`, project, restated, project, into)
	return err
}
