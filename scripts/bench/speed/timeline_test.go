package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func at(sec float64) time.Time { return t0.Add(time.Duration(sec * float64(time.Second))) }

func ev(sec float64, js string) event {
	var m claudeMsg
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		panic(err)
	}
	return event{At: at(sec), Msg: m}
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.5 {
		t.Errorf("%s = %.1f, want %.1f", what, got, want)
	}
}

// A stream like claude -p's: two Bash calls asked for together (one a build),
// a hook around each, an Edit, and the result.
func sampleStream() []event {
	return []event{
		ev(0.6, `{"type":"system","subtype":"init","model":"claude-opus-5-5","claude_code_version":"2.1.329","apiKeySource":"none","mcp_servers":[{"name":"memory","status":"connected"}]}`),
		ev(0.8, `{"type":"rate_limit_event"}`),
		ev(3.0, `{"type":"assistant","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"./check.sh"}}],"usage":{"input_tokens":5,"cache_read_input_tokens":1000,"cache_creation_input_tokens":200,"output_tokens":10}}}`),
		ev(3.1, `{"type":"system","subtype":"hook_started","hook_id":"h1","hook_name":"PreToolUse:Bash"}`),
		ev(3.2, `{"type":"system","subtype":"hook_response","hook_id":"h1","hook_name":"PreToolUse:Bash"}`),
		ev(3.5, `{"type":"assistant","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"b","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":5,"cache_read_input_tokens":1000,"cache_creation_input_tokens":200,"output_tokens":30}}}`),
		ev(5.0, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b"}]}}`),
		ev(13.0, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a"}]}}`),
		// A subagent's message, which is the subagent's time, not this run's.
		ev(14.0, `{"type":"assistant","parent_tool_use_id":"x","message":{"id":"s1","model":"claude-haiku-5-5","content":[{"type":"tool_use","id":"s","name":"Grep","input":{}}]}}`),
		ev(20.0, `{"type":"assistant","message":{"id":"m2","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"c","name":"Edit","input":{}}],"usage":{"input_tokens":3,"cache_read_input_tokens":1500,"output_tokens":100}}}`),
		ev(20.5, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"c"}]}}`),
		ev(24.0, `{"type":"assistant","message":{"id":"m3","model":"claude-opus-5-5","content":[{"type":"text","text":"Done."}],"usage":{"input_tokens":1,"cache_read_input_tokens":1700,"output_tokens":5}}}`),
		ev(24.1, `{"type":"result","subtype":"success","duration_ms":23400,"duration_api_ms":11000,"num_turns":3,"total_cost_usd":0.12,"is_error":false,"result":"Done.","usage":{"input_tokens":9,"cache_read_input_tokens":4200,"cache_creation_input_tokens":200,"output_tokens":900}}`),
	}
}

func TestAnalyzeStream(t *testing.T) {
	b := analyze(sampleStream())
	near(t, "active", b.ActiveMS, 23500) // 0.6 → 24.1
	// Tools: a 3.0→13.0 and b 3.5→5.0 overlap: 10 s; c 20.0→20.5: 0.5 s.
	near(t, "tool", b.ToolMS, 10500)
	near(t, "model", b.ModelMS, 13000)
	near(t, "build", b.BuildToolMS, 10000)
	near(t, "hook", b.HookMS, 100)
	near(t, "api", b.APIMS, 11000)
	if b.Turns != 3 || b.ToolCalls != 3 || b.Hooks != 1 {
		t.Errorf("turns %d, tool calls %d, hooks %d; want 3, 3, 1", b.Turns, b.ToolCalls, b.Hooks)
	}
	if b.Tools["Bash"].Calls != 2 || b.Tools["Edit"].Calls != 1 || b.Tools["Grep"].Calls != 0 {
		t.Errorf("tools = %v", b.Tools)
	}
	// The result's usage is the total.
	if want := (tokens{Input: 9, CacheRead: 4200, CacheCreation: 200, Output: 900}); b.Tokens != want {
		t.Errorf("tokens = %+v, want %+v", b.Tokens, want)
	}
	// Without one, each message counts once, at its last usage.
	events := sampleStream()
	if want := (tokens{Input: 9, CacheRead: 4200, CacheCreation: 200, Output: 135}); analyze(events[:len(events)-1]).Tokens != want {
		t.Errorf("tokens without a result = %+v, want %+v", analyze(events[:len(events)-1]).Tokens, want)
	}
	if b.Model != "claude-opus-5-5" || b.Version != "2.1.329" || len(b.MCP) != 1 || b.Result != "Done." {
		t.Errorf("model %q, version %q, mcp %v, result %q", b.Model, b.Version, b.MCP, b.Result)
	}
	if len(b.Builds) != 1 || b.Builds[0].Command != "./check.sh" {
		t.Errorf("builds = %v", b.Builds)
	}
}

// A build that outlasts the Bash tool's timeout goes on in the background:
// its call answers at once, and the build counts until its task ends.
func TestAnalyzeBackgroundedBuild(t *testing.T) {
	b := analyze([]event{
		ev(0, `{"type":"system","subtype":"init"}`),
		ev(1, `{"type":"assistant","message":{"id":"m1","model":"x","content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"./check.sh 2>&1 | tail"}}]}}`),
		ev(1.1, `{"type":"system","subtype":"task_started","task_id":"t1","tool_use_id":"a","is_backgrounded":false}`),
		ev(121, `{"type":"system","subtype":"task_updated","task_id":"t1","patch":{"is_backgrounded":true}}`),
		ev(122, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"moved to the background"}]}}`),
		ev(125, `{"type":"assistant","message":{"id":"m2","model":"x","content":[{"type":"tool_use","id":"b","name":"Bash","input":{"command":"sleep 60; cat out"}}]}}`),
		ev(185, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b"}]}}`),
		ev(200, `{"type":"system","subtype":"task_updated","task_id":"t1","patch":{"status":"completed"}}`),
		ev(200.1, `{"type":"system","subtype":"task_notification","task_id":"t1","tool_use_id":"a","status":"completed"}`),
		ev(210, `{"type":"result"}`),
	})
	near(t, "build", b.BuildToolMS, 199000) // 1 → 200
	near(t, "tool", b.ToolMS, 199000)
	near(t, "model", b.ModelMS, 11000)
	if b.Backgrounded != 1 {
		t.Errorf("backgrounded = %d, want 1", b.Backgrounded)
	}
}

// A transcript has no task messages: the end of a background command is the
// notification it queues for the model.
func TestTranscriptBackgroundedBuild(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2026-11-20T12:00:00.000Z","message":{"role":"user","content":"Fix it"}}`,
		`{"type":"assistant","timestamp":"2026-11-20T12:00:01.000Z","message":{"id":"m1","model":"x","content":[{"type":"tool_use","id":"toolu_a","name":"Bash","input":{"command":"./check.sh"}}]}}`,
		`{"type":"user","timestamp":"2026-11-20T12:02:01.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_a","content":"Command did not complete within its 120s timeout and was moved to the background"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-20T12:02:03.000Z","message":{"id":"m2","model":"x","content":[{"type":"text","text":"Waiting."}]}}`,
		`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-11-20T12:03:20.000Z","content":"<task-notification>\n<task-id>b1</task-id>\n<tool-use-id>toolu_a</tool-use-id>\n<status>completed</status>\n</task-notification>"}`,
		`{"type":"user","timestamp":"2026-11-20T12:03:20.100Z","message":{"role":"user","content":"<task-notification>…</task-notification>"}}`,
		`{"type":"assistant","timestamp":"2026-11-20T12:03:25.000Z","message":{"id":"m3","model":"x","content":[{"type":"text","text":"Done"}]}}`,
	}
	events, err := readTranscript(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	b := analyze(events)
	near(t, "build", b.BuildToolMS, 199000) // 12:00:01 → 12:03:20
	near(t, "model", b.ModelMS, 6000)
	if b.Backgrounded != 1 {
		t.Errorf("backgrounded = %d, want 1", b.Backgrounded)
	}
}

func TestAnalyzeUnansweredToolRunsToTheEnd(t *testing.T) {
	b := analyze([]event{
		ev(0, `{"type":"system","subtype":"init"}`),
		ev(1, `{"type":"assistant","message":{"id":"m1","model":"x","content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"go test ./..."}}]}}`),
		ev(9, `{"type":"result","is_error":true,"result":"killed"}`),
	})
	near(t, "tool", b.ToolMS, 8000)
	near(t, "build", b.BuildToolMS, 8000)
	if !b.IsError {
		t.Error("the result's error was lost")
	}
}

func TestAnalyzeNothing(t *testing.T) {
	// claude that never got going: no init, only the "Not logged in" message
	// it makes up itself.
	b := analyze([]event{ev(0, `{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"Not logged in"}]}}`)})
	if !b.Start.IsZero() || b.Turns != 0 {
		t.Errorf("got %+v from a run that never started", b)
	}
}

func TestReadTranscript(t *testing.T) {
	lines := []string{
		`{"type":"queue-operation","timestamp":"2026-11-20T12:00:00.000Z"}`,
		`{"type":"user","timestamp":"2026-11-20T12:00:01.000Z","message":{"role":"user","content":"Fix it"}}`,
		`{"type":"api-request","timestamp":"2026-11-20T12:00:01.100Z"}`,
		`{"type":"assistant","timestamp":"2026-11-20T12:00:04.000Z","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"./check.sh"}}]}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-11-20T12:00:05.000Z","message":{"id":"s","model":"m","content":[]}}`,
		`{"type":"last-prompt"}`,
		`{"type":"user","timestamp":"2026-11-20T12:00:34.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"a"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-20T12:00:40.000Z","message":{"id":"m2","model":"claude-opus-5-5","content":[{"type":"text","text":"Done"}]}}`,
		`not json`,
	}
	events, err := readTranscript(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	b := analyze(events)
	if !b.Start.Equal(at(1)) || !b.End.Equal(at(40)) {
		t.Errorf("span %s → %s, want the prompt at +1s to the answer at +40s", b.Start, b.End)
	}
	near(t, "tool", b.ToolMS, 30000)
	near(t, "model", b.ModelMS, 9000)
	if b.Turns != 2 {
		t.Errorf("turns = %d, want 2 (the sidechain's isn't one)", b.Turns)
	}
}

func TestReadStamped(t *testing.T) {
	var buf bytes.Buffer
	for i, e := range []string{`{"type":"system","subtype":"init"}`, `{"type":"result"}`} {
		j, _ := json.Marshal(stamped{At: at(float64(i)), Event: json.RawMessage(e)})
		fmt.Fprintf(&buf, "%s\n", j)
	}
	buf.WriteString("garbage\n")
	events, err := readStamped(&buf)
	if err != nil || len(events) != 2 || events[1].Msg.Type != "result" || !events[1].At.Equal(at(1)) {
		t.Fatalf("got %+v, %v", events, err)
	}
}

func TestUnion(t *testing.T) {
	spans := []span{{at(5), at(8)}, {at(0), at(2)}, {at(1), at(3)}, {at(7), at(9)}, {at(20), at(30)}}
	if got := union(spans, at(0), at(25)); got != 12*time.Second { // 0–3, 5–9, 20–25
		t.Errorf("union = %s, want 12s", got)
	}
	if got := union(nil, at(0), at(1)); got != 0 {
		t.Errorf("union of nothing = %s", got)
	}
}

func TestBuildCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"./check.sh":                       true,
		"cd /w && ./check.sh 2>&1 | tail":  true,
		"go test ./stats/":                 true,
		"go vet ./...":                     true,
		"cargo test --release":             true,
		"make -j4":                         true,
		"cat stats/stats.go":               false,
		"git diff":                         false,
		"echo going to test it":            false,
		"grep -rn 'go testing' README.md":  false,
		"sed -n 1,20p stats/stats_test.go": false,
	} {
		if got := buildCommand.MatchString(cmd); got != want {
			t.Errorf("%q: build = %v, want %v", cmd, got, want)
		}
	}
}
