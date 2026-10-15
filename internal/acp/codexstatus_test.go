package acp

import (
	"testing"
	"time"
)

func TestParseCodexStatus(t *testing.T) {
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	text := "**Model:** gpt-5  \n**Limits:** x\n**5h limit:** 80% left (resets 14:30)  \n**Weekly limit:** 41% left (resets 09:05 on 18 Oct)  \n**codex Spark 3h limit:** 100% left\n**Credits:** unlimited"
	got := ParseCodexStatus(text, now, time.UTC)
	if len(got) != 3 {
		t.Fatalf("got %d windows: %+v", len(got), got)
	}
	if got[0].Name != "five_hour" || got[0].Used != 0.2 || !got[0].ResetsAt.Equal(time.Date(2026, 10, 15, 14, 30, 0, 0, time.UTC)) {
		t.Errorf("5h = %+v", got[0])
	}
	if got[1].Name != "seven_day" || got[1].Used != 0.59 || !got[1].ResetsAt.Equal(time.Date(2026, 10, 18, 9, 5, 0, 0, time.UTC)) {
		t.Errorf("weekly = %+v", got[1])
	}
	if got[2].Name != "codex Spark 3h-limit" || got[2].Used != 0 || !got[2].ResetsAt.IsZero() {
		t.Errorf("named = %+v", got[2])
	}
	if w := ParseCodexStatus("**Weekly limit:** 10% left (resets 01:00 on Jan 2)", now, time.UTC); len(w) != 1 || w[0].ResetsAt.Year() != 2027 {
		t.Errorf("month-first date: %+v", w)
	}
	if got := ParseCodexStatus("**Limits:** data not available yet", now, time.UTC); len(got) != 0 {
		t.Errorf("no data gave %+v", got)
	}
}
