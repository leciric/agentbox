package memory

import (
	"context"
	"time"
)

// SearchBM25 is Search before reranking: bm25's own order, with none of the
// signals rerank (in search.go) adds. It is exported only for tests —
// rerank_eval_test.go, in package memory_test, scores it against the
// reranked Search over the same fixtures, which is the only way to say
// plainly whether reranking earned its keep.
func (s *Store) SearchBM25(ctx context.Context, project, query string, limit int) (Results, error) {
	return s.searchBM25(ctx, project, query, limit)
}

// AgeOffer moves when a memory was offered as a note back by d, so a test can
// see an unanswered offer lapse without waiting OfferLasts for it.
func (s *Store) AgeOffer(ctx context.Context, project, id string, d time.Duration) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memories SET promotion_at = promotion_at - ? WHERE project = ? AND id = ?`,
		d.Milliseconds(), project, id)
	return err
}
