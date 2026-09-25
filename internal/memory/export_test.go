package memory

import "context"

// SearchBM25 is Search before reranking: bm25's own order, with none of the
// signals rerank (in search.go) adds. It is exported only for tests —
// rerank_eval_test.go, in package memory_test, scores it against the
// reranked Search over the same fixtures, which is the only way to say
// plainly whether reranking earned its keep.
func (s *Store) SearchBM25(ctx context.Context, project, query string, limit int) (Results, error) {
	return s.searchBM25(ctx, project, query, limit)
}
