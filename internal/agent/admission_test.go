package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckSizeAndReservation(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"": "", "auto": "", "light": "light", "normal": "normal", "heavy": "heavy"} {
		if got, err := CheckSize(in); err != nil || got != want {
			t.Errorf("CheckSize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := CheckSize("huge"); err == nil {
		t.Error("an unknown size was accepted")
	}
	peak := 5 * gib
	for size, want := range map[string]int64{"": peak, "normal": peak, "light": 2 * gib, "heavy": 8 * gib} {
		if got := Reservation(size, peak); got != want {
			t.Errorf("Reservation(%q) = %d, want %d", size, got, want)
		}
	}
}

func TestAdmit(t *testing.T) {
	t.Parallel()
	now := time.Now()
	holders := []Holder{
		{Ref: "p/a", Project: "p", Reserved: 4 * gib},
		{Ref: "r/b", Project: "r", Reserved: 4 * gib, Using: 6 * gib}, // grew: counts 6
	}
	verdicts := func(capacity int64, holders []Holder, waiting []Waiter, free map[string]int) map[string]Verdict {
		return Admit(capacity, holders, waiting, free, now)
	}

	t.Run("across projects, by memory", func(t *testing.T) {
		got := verdicts(15*gib, holders, []Waiter{
			{Ref: "p/big", Project: "p", Need: 8 * gib, Since: now.Add(-time.Minute)},
			{Ref: "r/small", Project: "r", Need: 2 * gib, Since: now},
			{Ref: "p/small", Project: "p", Need: 2 * gib, Since: now},
			{Ref: "r/more", Project: "r", Need: 2 * gib, Since: now},
		}, nil)
		// 10 used of 15: the 8 doesn't fit, the first two 2s do, the third doesn't.
		if got["p/big"] != (Verdict{Why: WaitMemory}) || !got["r/small"].Start || !got["p/small"].Start || got["r/more"].Start {
			t.Errorf("verdicts = %+v", got)
		}
	})

	t.Run("a heavy agent that has waited long enough isn't passed", func(t *testing.T) {
		got := verdicts(16*gib, holders, []Waiter{
			{Ref: "p/big", Project: "p", Need: 8 * gib, Since: now.Add(-StarveAfter)},
			{Ref: "r/small", Project: "r", Need: 2 * gib, Since: now},
		}, nil)
		if got["r/small"] != (Verdict{Why: WaitBehind, Behind: "p/big"}) {
			t.Errorf("the light agent = %+v, want it behind p/big", got["r/small"])
		}
	})

	t.Run("one agent always starts on an empty VM", func(t *testing.T) {
		got := verdicts(4*gib, nil, []Waiter{{Ref: "p/huge", Need: 8 * gib}, {Ref: "p/next", Need: gib}}, nil)
		if !got["p/huge"].Start || got["p/next"].Start {
			t.Errorf("verdicts = %+v, want the first alone", got)
		}
	})

	t.Run("slots, unless the create didn't ask to queue", func(t *testing.T) {
		free := map[string]int{"p": 1, "r": 0}
		got := verdicts(64*gib, holders, []Waiter{
			{Ref: "r/q", Project: "r", Need: gib},
			{Ref: "p/q1", Project: "p", Need: gib},
			{Ref: "p/q2", Project: "p", Need: gib},
			{Ref: "r/new", Project: "r", Need: gib, AnySlot: true},
		}, free)
		if got["r/q"].Why != WaitSlot || !got["p/q1"].Start || got["p/q2"].Why != WaitSlot || !got["r/new"].Start {
			t.Errorf("verdicts = %+v", got)
		}
	})
}

func TestWaitMessage(t *testing.T) {
	t.Parallel()
	holders := []Holder{{Project: "p", Reserved: 4 * gib}, {Project: "r", Reserved: 4 * gib, Using: 7 * gib}}
	if got, want := WaitMessage(Verdict{Why: WaitMemory}, 18*gib, 8*gib, holders, 0), "queued: 2 agents in 2 projects reserve 11 of 18 GB; starts when ~8 GB is free"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := WaitMessage(Verdict{Why: WaitSlot}, 0, 0, nil, 1), "queued: its project's 1 slot is taken; starts when one is free"; got != want {
		t.Errorf("got %q", got)
	}
}

func TestMemoryHighs(t *testing.T) {
	t.Parallel()
	holders := []Holder{
		{Ref: "fat", Reserved: 4 * gib, Using: 9 * gib},
		{Ref: "grown", Reserved: 4 * gib, Using: 6 * gib},
		{Ref: "lean", Reserved: 4 * gib, Using: 2 * gib},
	}
	// Room to spare: no limits.
	for ref, high := range MemoryHighs(32*gib, holders) {
		if high != 0 {
			t.Errorf("with room, %s's memory.high = %d", ref, high)
		}
	}
	// 19 counted of 20: short of the 2 GiB slack by 1, taken from the
	// furthest over first, nobody under reservation plus headroom.
	highs := MemoryHighs(20*gib, holders)
	if highs["fat"] >= 9*gib {
		t.Errorf("the agent furthest over isn't held below what it uses: %d", highs["fat"])
	}
	if highs["grown"] < 6*gib {
		t.Errorf("the agent less far over gave memory up before the one furthest over: %d", highs["grown"])
	}
	for ref, high := range highs {
		if high < 4*gib+HighHeadroom(4*gib) {
			t.Errorf("%s's memory.high %d is below its reservation and headroom", ref, high)
		}
	}
	// Far past the cap, the furthest over is held at its floor.
	if got := MemoryHighs(14*gib, holders)["fat"]; got != 4*gib+HighHeadroom(4*gib) {
		t.Errorf("with the VM over its cap, the fattest agent's memory.high = %d, want its floor", got)
	}
}

func TestSetMemoryHigh(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "lxc.payload.m")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "memory.high")
	if err := os.WriteFile(file, []byte("max\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var writes []string
	write := func(path, value string) error {
		writes = append(writes, value)
		return os.WriteFile(path, []byte(value+"\n"), 0o644)
	}
	if err := setMemoryHigh(root, "m", 0, write); err != nil || len(writes) != 0 {
		t.Errorf("max over max wrote %v, %v", writes, err)
	}
	if err := setMemoryHigh(root, "m", 5*gib+1, write); err != nil || len(writes) != 1 || writes[0] != "5368709120" {
		t.Errorf("writes = %v, %v; want 5 GiB, page-aligned", writes, err)
	}
	if err := setMemoryHigh(root, "m", 5*gib, write); err != nil || len(writes) != 1 {
		t.Errorf("an unchanged value was written again: %v, %v", writes, err)
	}
	if err := setMemoryHigh(root, "m", 0, write); err != nil || writes[len(writes)-1] != "max" {
		t.Errorf("lifting it wrote %v, %v", writes, err)
	}
	if err := setMemoryHigh(root, "gone", gib, write); err != nil {
		t.Errorf("a machine with no cgroup: %v", err)
	}
}
