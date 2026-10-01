package cli

import (
	"strings"
	"testing"

	"agentbox/internal/api"
)

func TestOrUnknown(t *testing.T) {
	if got := orUnknown(""); got != "unknown" {
		t.Errorf("orUnknown(%q) = %q, want %q", "", got, "unknown")
	}
	if got := orUnknown("1.2.3"); got != "1.2.3" {
		t.Errorf("orUnknown(%q) = %q, want %q", "1.2.3", got, "1.2.3")
	}
}

func TestDescribeBehind(t *testing.T) {
	t.Run("image unknown from", func(t *testing.T) {
		got := describeBehind(&api.BaseBehind{ImageTo: "v2"})
		if !strings.Contains(got, "image: unknown → v2") {
			t.Errorf("describeBehind = %q, want it to mention unknown → v2", got)
		}
	})

	t.Run("image from and to", func(t *testing.T) {
		got := describeBehind(&api.BaseBehind{ImageFrom: "v1", ImageTo: "v2"})
		if !strings.Contains(got, "image: v1 → v2") {
			t.Errorf("describeBehind = %q, want it to mention v1 → v2", got)
		}
	})

	t.Run("changes", func(t *testing.T) {
		got := describeBehind(&api.BaseBehind{
			ImageFrom: "v1", ImageTo: "v2",
			Changes: []api.BaseImageChange{{Version: "v2", What: "bumped Node"}},
		})
		if !strings.Contains(got, "v2: bumped Node") {
			t.Errorf("describeBehind = %q, want it to list the change", got)
		}
	})

	t.Run("components", func(t *testing.T) {
		got := describeBehind(&api.BaseBehind{Components: []string{"Incus", "Codex"}})
		if !strings.Contains(got, "new in the image: Incus, Codex") {
			t.Errorf("describeBehind = %q, want it to list new components", got)
		}
	})

	t.Run("tools unknown", func(t *testing.T) {
		got := describeBehind(&api.BaseBehind{ToolsUnknown: true})
		if !strings.Contains(got, "not recorded") {
			t.Errorf("describeBehind = %q, want it to say tools aren't recorded", got)
		}
	})

	t.Run("tool new, removed and changed", func(t *testing.T) {
		got := describeBehind(&api.BaseBehind{
			Tools: []api.BaseToolChange{
				{Name: "go", From: "", To: "1.23"},
				{Name: "android", From: "1.0", To: ""},
				{Name: "node", From: "20", To: "22"},
			},
		})
		for _, want := range []string{
			"go: new, 1.23",
			"android: 1.0, no longer pinned",
			"node: 20 → 22",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("describeBehind = %q, want it to contain %q", got, want)
			}
		}
	})

	t.Run("no image change", func(t *testing.T) {
		got := describeBehind(&api.BaseBehind{})
		if strings.Contains(got, "image:") {
			t.Errorf("describeBehind = %q, want no image line when nothing changed", got)
		}
	})
}

func TestDiskLevelWords(t *testing.T) {
	cases := []struct {
		level string
		want  string
	}{
		{api.DiskLow, "nearing the floor"},
		{api.DiskFull, "at the floor"},
		{api.DiskOK, "ok"},
		{"", "ok"},
	}
	for _, c := range cases {
		if got := diskLevelWords(c.level); got != c.want {
			t.Errorf("diskLevelWords(%q) = %q, want %q", c.level, got, c.want)
		}
	}
}

func TestNestingWords(t *testing.T) {
	if got := nestingWords(true); !strings.Contains(got, "on") {
		t.Errorf("nestingWords(true) = %q, want it to say on", got)
	}
	if got := nestingWords(false); got != "off" {
		t.Errorf("nestingWords(false) = %q, want %q", got, "off")
	}
}

func TestAgentPRsWords(t *testing.T) {
	if got := agentPRsWords(true); !strings.Contains(got, "on") {
		t.Errorf("agentPRsWords(true) = %q, want it to say on", got)
	}
	if got := agentPRsWords(false); !strings.Contains(got, "off") {
		t.Errorf("agentPRsWords(false) = %q, want it to say off", got)
	}
}

func TestParseOnOff(t *testing.T) {
	on, err := parseOnOff("on")
	if err != nil || !on {
		t.Fatalf("parseOnOff(on) = %v, %v, want true, nil", on, err)
	}
	off, err := parseOnOff("off")
	if err != nil || off {
		t.Fatalf("parseOnOff(off) = %v, %v, want false, nil", off, err)
	}
	if _, err := parseOnOff("maybe"); err == nil {
		t.Fatalf("parseOnOff(maybe) = nil error, want an error")
	}
}

func TestDescribeConsolidation(t *testing.T) {
	t.Run("no memories yet", func(t *testing.T) {
		got := describeConsolidation(api.MemoryConsolidation{Events: 5})
		if !strings.Contains(got, "5 events, 0 live memories") {
			t.Errorf("describeConsolidation = %q, missing event/memory count", got)
		}
		if strings.Contains(got, "events per memory") {
			t.Errorf("describeConsolidation = %q, shouldn't compute a ratio with no memories", got)
		}
	})

	t.Run("ratio and pending", func(t *testing.T) {
		got := describeConsolidation(api.MemoryConsolidation{Events: 20, Memories: 4, Pending: 3, Duplicates: 2,
			Passes: 1, MemoriesWritten: 4, MemoriesSuperseded: 1, MemoriesResolved: 0})
		for _, want := range []string{
			"20 events, 4 live memories",
			"(5 events per memory)",
			"3 events waiting to be distilled",
			"2 near-duplicate pairs flagged",
			"1 passes, 4 memories written, 1 replaced, 0 closed",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("describeConsolidation = %q, want it to contain %q", got, want)
			}
		}
	})
}

func TestPassWords(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		got := passWords(api.ConsolidationPass{Kind: api.ConsolidationDistill, Error: "model unavailable"})
		if !strings.Contains(got, "didn't finish: model unavailable") {
			t.Errorf("passWords = %q, want it to report the error", got)
		}
	})

	t.Run("distill", func(t *testing.T) {
		got := passWords(api.ConsolidationPass{Kind: api.ConsolidationDistill,
			EventsRead: 10, MemoriesWritten: 2, MemoriesSuperseded: 1, MemoriesResolved: 1})
		if !strings.Contains(got, "10 events became 2 memories, 1 replaced, 1 closed") {
			t.Errorf("passWords = %q, want the distill summary", got)
		}
	})

	t.Run("mechanical", func(t *testing.T) {
		got := passWords(api.ConsolidationPass{Kind: api.ConsolidationMechanical,
			MemoriesSuperseded: 3, MemoriesDecayed: 2, DuplicatesFound: 1})
		if !strings.Contains(got, "merged 3 memories, aged 2, flagged 1 near-duplicate pairs") {
			t.Errorf("passWords = %q, want the mechanical summary", got)
		}
	})
}
