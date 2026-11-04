package acp

import (
	"encoding/json"
	"testing"
)

func TestSessionUpdateGoal(t *testing.T) {
	for _, tc := range []struct {
		name, update string
		ok           bool
		objective    string
		status       string
		iterations   int
	}{
		{"claude's set", `{"sessionUpdate":"session_info_update","_meta":{"jetbrains":{"air":{"version":1,"goal":{"objective":" Ship it ","status":"active","controlMethod":"_session/goal"}}}}}`, true, "Ship it", "active", 0},
		{"claude's check", `{"sessionUpdate":"session_info_update","_meta":{"jetbrains":{"air":{"goal":{"objective":"x","status":"active","iterations":2,"lastReason":"no file"}}}}}`, true, "x", "active", 2},
		{"claude's clear", `{"sessionUpdate":"session_info_update","_meta":{"jetbrains":{"air":{"version":1,"goal":null}}}}`, true, "", "", 0},
		{"codex's complete", `{"sessionUpdate":"session_info_update","_meta":{"goal":{"objective":"y","status":"complete"}}}`, true, "y", "complete", 0},
		{"codex's clear", `{"sessionUpdate":"session_info_update","_meta":{"goal":null}}`, true, "", "", 0},
		{"a title", `{"sessionUpdate":"session_info_update","title":"/goal"}`, false, "", "", 0},
		{"suggestions", `{"sessionUpdate":"session_info_update","_meta":{"promptSuggestions":{"suggestions":[]}}}`, false, "", "", 0},
		{"another update", `{"sessionUpdate":"usage_update","_meta":{"goal":{"objective":"z"}}}`, false, "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var u SessionUpdate
			if err := json.Unmarshal([]byte(tc.update), &u); err != nil {
				t.Fatal(err)
			}
			snap, ok := u.Goal()
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if tc.objective == "" {
				if snap != nil {
					t.Fatalf("snap = %+v, want nil", snap)
				}
				return
			}
			if snap == nil || snap.Objective != tc.objective || snap.Status != tc.status || snap.Iterations != tc.iterations {
				t.Fatalf("snap = %+v", snap)
			}
		})
	}
}
