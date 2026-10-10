package memory

import (
	"testing"
	"time"
)

// Confirmations reorder what bm25 and everything else score alike, and are
// bounded: no count of them lifts a row over bm25's clear best match.
func TestConfirmationsAreABoundedRerankSignal(t *testing.T) {
	at := time.Now().Add(-time.Hour)
	plain := Memory{ID: "plain", Kind: KindProject, Importance: 3, CreatedAt: at}
	said := Memory{ID: "said", Kind: KindProject, Importance: 3, CreatedAt: at, Confirmations: 3}

	// Neighbours in a full pool: bm25 barely prefers plain, and three
	// confirmations are worth more than that.
	pool := []Memory{plain, said}
	for i := range rerankPool - 2 {
		pool = append(pool, Memory{ID: "filler", Kind: KindEpisodic, Importance: 1, CreatedAt: at.Add(-time.Duration(i+1) * 24 * time.Hour)})
	}
	if got := rerank(Results{Memories: pool}, 1).Memories[0].ID; got != "said" {
		t.Errorf("a memory said three times should edge past its neighbour, got %s first", got)
	}

	// bm25's best and its worst: a thousand confirmations don't close that.
	said.Confirmations = 1000
	if got := rerank(Results{Memories: []Memory{plain, said}}, 2).Memories[0].ID; got != "plain" {
		t.Errorf("confirmations outranked bm25's best match: %s first", got)
	}
	if s := confirmationScore(1000); s > confirmationCap || s <= confirmationScore(3) {
		t.Errorf("confirmationScore(1000) = %v: it saturates at confirmationCap", s)
	}
	if confirmationScore(0) != 0 || confirmationScore(-1) != 0 {
		t.Error("no confirmations should score 0")
	}
}

func TestImportanceAfterFeedback(t *testing.T) {
	for _, c := range []struct {
		current int
		verdict string
		want    int
	}{
		{5, FeedbackWrong, MinImportance}, {3, FeedbackStale, MinImportance}, {1, FeedbackWrong, MinImportance},
		{1, FeedbackHelpful, 2}, {3, FeedbackHelpful, 4}, {4, FeedbackHelpful, 4}, {5, FeedbackHelpful, 5},
		{3, "meh", 3},
	} {
		if got := importanceAfterFeedback(c.current, c.verdict); got != c.want {
			t.Errorf("%s on %d = %d, want %d", c.verdict, c.current, got, c.want)
		}
	}
}
