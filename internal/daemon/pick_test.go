package daemon

import (
	"context"
	"testing"
)

// One element picker per agent: a second ends the first, and cancelling ends
// the one showing, not one started after it.
func TestElementPicksOnePerAgent(t *testing.T) {
	t.Parallel()
	var picks elementPicks
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	doneFirst := picks.start("p/agent-01", cancelFirst)
	other, cancelOther := context.WithCancel(context.Background())
	defer cancelOther()
	defer picks.start("p/agent-02", cancelOther)()

	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	doneSecond := picks.start("p/agent-01", cancelSecond)
	if first.Err() == nil {
		t.Error("a second picker left the first one showing")
	}
	doneFirst() // the first one's request ends after the second began
	picks.stop("p/agent-01")
	if second.Err() == nil {
		t.Error("cancel didn't end the picker showing")
	}
	if other.Err() != nil {
		t.Error("cancelling one agent's picker ended another's")
	}
	doneSecond()
	picks.stop("p/agent-01") // nothing showing: nothing to do
}
