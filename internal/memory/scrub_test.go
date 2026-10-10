package memory_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"agentbox/internal/memory"
)

// The secrets each write below carries, none of which may be in the database
// afterwards, and the ids and hashes beside them, all of which must be.
var (
	storedSecrets = []string{
		"ghp_0123456789abcdefABCDEF0123456789abcd",
		"sk-ant-oat01-ZYXWVUTSRQ_0123456789-abc",
		"hunter2",
		"abc.def-ghi_jkl",
		"s3cr3t",
		"cur_abc123",
		"b3BlbnNzaC1rZXk",
	}
	keptIDs = []string{"c769d9d0a1b2c3d4e5f60718293a4b5c6d7e8f90", "mem_01J9ZK3X4Y5Z6A7B8C9D0E1F2G", "#251"}
)

const secretProse = "The push of c769d9d0a1b2c3d4e5f60718293a4b5c6d7e8f90 (#251) failed with " +
	"ghp_0123456789abcdefABCDEF0123456789abcd; retried with Authorization: Bearer abc.def-ghi_jkl " +
	"against https://lint:s3cr3t@github.com/leciric/agentbox and password=hunter2, see mem_01J9ZK3X4Y5Z6A7B8C9D0E1F2G"

func TestNoSecretIsStored(t *testing.T) {
	ctx := context.Background()
	s, db := openDB(t)
	must := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	payload, _ := json.Marshal(map[string]any{
		"summary": secretProse,
		"nested":  map[string]any{"apiKey": "cur_abc123", "lines": []any{"CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-ZYXWVUTSRQ_0123456789-abc", 3}},
		"quoted":  `token="s3cr3t" and a \ backslash`,
		"key":     "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH PRIVATE KEY-----",
	})
	ev, err := s.AppendEvent(ctx, memory.Event{Project: "pawly", Agent: "agent-01", Type: "agent_finished", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(ev.Payload) {
		t.Fatalf("payload isn't JSON any more: %s", ev.Payload)
	}
	m, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Kind: memory.KindIssue,
		Title: "Push fails with ghp_0123456789abcdefABCDEF0123456789abcd", Content: secretProse})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Content, "https://[redacted]@github.com/leciric/agentbox") {
		t.Errorf("returned memory = %q, want it as stored", m.Content)
	}
	must(s.AddReport(ctx, memory.Report{Project: "pawly", Agent: "agent-01",
		Task: "Fix the push, GH_TOKEN=s3cr3t", Summary: secretProse,
		Discoveries: []string{secretProse}, Decisions: []string{"export CURSOR_API_KEY=cur_abc123"},
		RemainingIssues: []string{"password: hunter2"}, Artifacts: []string{"https://lint:s3cr3t@example.com/a.png"}}))
	notes, blockers := secretProse, []string{"Authorization: Bearer abc.def-ghi_jkl"}
	must(s.SetWorkingMemory(ctx, "pawly", memory.WorkingMemoryPatch{Notes: &notes, Blockers: &blockers}))
	must(s.AddArtifact(ctx, memory.Artifact{Project: "pawly", Type: "screenshot",
		Path: "https://lint:s3cr3t@example.com/shot.png", Metadata: json.RawMessage(`{"cookie":"hunter2"}`)}))
	must(s.AddTask(ctx, memory.Task{Project: "pawly", Goal: "Rotate ghp_0123456789abcdefABCDEF0123456789abcd", Detail: secretProse}))
	other, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Kind: memory.KindIssue, Title: "Another"})
	if err != nil {
		t.Fatal(err)
	}
	must(s.ResolveMemory(ctx, "pawly", other.ID, "fixed by rotating password=hunter2"))

	all := dump(t, db)
	for _, secret := range storedSecrets {
		if strings.Contains(all, secret) {
			t.Errorf("%q was stored", secret)
		}
	}
	for _, id := range keptIDs {
		if !strings.Contains(all, id) {
			t.Errorf("%q was lost: hashes and ids aren't secrets", id)
		}
	}

	// And nothing comes back out of search, since its index is built from
	// the same rows.
	res, err := s.Search(ctx, "pawly", "hunter2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Empty() {
		t.Errorf("searching for a secret found %+v", res)
	}
}

// An event with nothing to remove is stored byte for byte, as it was before
// scrubbing.
func TestCleanPayloadIsKeptAsWritten(t *testing.T) {
	s := open(t)
	raw := json.RawMessage(`{"z":1,"a":"<b>","sha":"c769d9d0a1b2c3d4e5f60718293a4b5c6d7e8f90"}`)
	ev, err := s.AppendEvent(context.Background(), memory.Event{Project: "pawly", Type: "pr_merged", Payload: raw})
	if err != nil {
		t.Fatal(err)
	}
	if string(ev.Payload) != string(raw) {
		t.Errorf("payload = %s, want %s", ev.Payload, raw)
	}
}

// dump is every value in every table, as text.
func dump(t *testing.T, db *sql.DB) string {
	t.Helper()
	tables, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	_ = tables.Close()
	var b strings.Builder
	for _, name := range names {
		rows, err := db.Query(`SELECT * FROM "` + name + `"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range values {
				if raw, ok := v.([]byte); ok {
					v = string(raw)
				}
				fmt.Fprintln(&b, v)
			}
		}
		_ = rows.Close()
	}
	return b.String()
}
