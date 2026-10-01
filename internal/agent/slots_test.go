package agent_test

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/agent"
)

const gib = int64(1) << 30

func TestSlotReserve(t *testing.T) {
	for _, c := range []struct{ budget, want int64 }{
		{8 * gib, 2 * gib}, // the floor
		{18 * gib, 18 * gib / 8},
		{64 * gib, 8 * gib},
	} {
		if got := agent.SlotReserve(c.budget); got != c.want {
			t.Errorf("SlotReserve(%d GiB) = %d, want %d", c.budget/gib, got, c.want)
		}
	}
}

// The example the feature was asked for: on an 18 GiB budget, a project whose
// agents peak at 6 GiB gets 2 slots alone, and 1 beside a project whose agents
// peak at 2 GiB, which gets 4.
func TestSplitSlotsSharesMemoryFairly(t *testing.T) {
	usable := 18*gib - agent.SlotReserve(18*gib)
	organic := agent.SlotProject{Name: "organic", Peak: 6 * gib, Queued: 5}
	agentbox := agent.SlotProject{Name: "agentbox", Peak: 2 * gib, Queued: 10}

	alone := agent.SplitSlots(usable, []agent.SlotProject{organic})
	if alone["organic"] != 2 {
		t.Errorf("organic alone: %d slots, want 2", alone["organic"])
	}
	both := agent.SplitSlots(usable, []agent.SlotProject{organic, agentbox})
	if both["organic"] != 1 || both["agentbox"] != 4 {
		t.Errorf("together: organic %d, agentbox %d; want 1 and 4", both["organic"], both["agentbox"])
	}
	// Order doesn't matter.
	swapped := agent.SplitSlots(usable, []agent.SlotProject{agentbox, organic})
	if swapped["organic"] != 1 || swapped["agentbox"] != 4 {
		t.Errorf("swapped: organic %d, agentbox %d; want 1 and 4", swapped["organic"], swapped["agentbox"])
	}
}

// A project with no work shares nothing, and is told what it would get if it
// had some: here, beside organic, the same as when both have work.
func TestSplitSlotsIdleProjectIsToldItsShare(t *testing.T) {
	usable := 18*gib - agent.SlotReserve(18*gib)
	got := agent.SplitSlots(usable, []agent.SlotProject{
		{Name: "organic", Peak: 6 * gib, Running: 1},
		{Name: "agentbox", Peak: 2 * gib},
	})
	// organic has the budget to itself: agentbox's idle share isn't taken
	// from it.
	if got["organic"] != 2 {
		t.Errorf("organic: %d slots, want 2", got["organic"])
	}
	if got["agentbox"] != 4 {
		t.Errorf("agentbox, idle: %d slots, want 4", got["agentbox"])
	}
	// Nothing running is stopped to make room: with two of organic's agents
	// holding 12 GiB, agentbox would get the one slot left.
	got = agent.SplitSlots(usable, []agent.SlotProject{
		{Name: "organic", Peak: 6 * gib, Running: 2},
		{Name: "agentbox", Peak: 2 * gib},
	})
	if got["organic"] != 2 || got["agentbox"] != 1 {
		t.Errorf("organic with two running: %v, want organic 2 and agentbox 1", got)
	}
}

// Memory a project has no work for goes to a project that has.
func TestSplitSlotsLeftoverGoesWhereTheWorkIs(t *testing.T) {
	usable := 16 * gib
	got := agent.SplitSlots(usable, []agent.SlotProject{
		{Name: "small", Peak: 2 * gib, Running: 1},
		{Name: "big", Peak: 4 * gib, Queued: 10},
	})
	// small needs 2 GiB for its one agent; big gets the other 14, three of 4.
	if got["big"] != 3 {
		t.Errorf("big: %d slots, want 3", got["big"])
	}
	if got["small"] < 1 {
		t.Errorf("small: %d slots, want at least 1", got["small"])
	}
}

// However large a project's peak, it always gets one slot.
func TestSplitSlotsAtLeastOne(t *testing.T) {
	got := agent.SplitSlots(4*gib, []agent.SlotProject{
		{Name: "huge", Peak: 32 * gib, Queued: 3},
		{Name: "small", Peak: 1 * gib, Queued: 3},
	})
	if got["huge"] != 1 || got["small"] < 1 {
		t.Errorf("got %v, want huge 1 and small at least 1", got)
	}
	if got := agent.SplitSlots(0, []agent.SlotProject{{Name: "p", Peak: gib, Queued: 1}}); got["p"] != 1 {
		t.Errorf("no budget at all: %d slots, want 1", got["p"])
	}
}

// A pinned project gets its number, and the memory its agents need comes out
// of what the others share.
func TestSplitSlotsPinned(t *testing.T) {
	got := agent.SplitSlots(16*gib, []agent.SlotProject{
		{Name: "pinned", Peak: 4 * gib, Pinned: 3, Queued: 5},
		{Name: "auto", Peak: 2 * gib, Queued: 10},
	})
	if got["pinned"] != 3 {
		t.Errorf("pinned: %d slots, want 3", got["pinned"])
	}
	// 16 - 3×4 = 4 GiB left, two of 2 GiB.
	if got["auto"] != 2 {
		t.Errorf("auto: %d slots, want 2", got["auto"])
	}
	// Pinned to more than it has work for: only its work is set aside.
	got = agent.SplitSlots(16*gib, []agent.SlotProject{
		{Name: "pinned", Peak: 4 * gib, Pinned: 3, Running: 1},
		{Name: "auto", Peak: 2 * gib, Queued: 10},
	})
	if got["auto"] != 6 {
		t.Errorf("auto beside a pinned project with one agent: %d slots, want 6", got["auto"])
	}
}

// A project nothing is known about is expected to peak at 4 GiB; once one of
// its agents was seen, at what its agents reached.
func TestProjectPeak(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	ctx := context.Background()
	if peak, learned, err := f.m.ProjectPeak(ctx, "hello-stack"); err != nil || learned || peak != 4*gib {
		t.Fatalf("before any agent: %d GiB, learned %v, %v", peak/gib, learned, err)
	}
	if err := f.st.RecordUsagePeak(ctx, "hello-stack", "agent-01", 3*gib, 50, time.Now()); err != nil {
		t.Fatal(err)
	}
	if peak, learned, err := f.m.ProjectPeak(ctx, "hello-stack"); err != nil || !learned || peak != 3*gib {
		t.Errorf("after one agent: %d GiB, learned %v, %v", peak/gib, learned, err)
	}
}

// SlotBudget never shares out more memory than the machine has.
func TestSlotBudget(t *testing.T) {
	if b := agent.SlotBudget(); b <= 0 || b > agent.HostMemory() {
		t.Errorf("SlotBudget() = %d, with %d of memory", b, agent.HostMemory())
	}
}
