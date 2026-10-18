package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func noticeOf(id, project, agent, question string) Notification {
	return Notification{ID: id, Project: project, Agent: agent, Kind: "question", CreatedAt: time.Now(),
		Data: []byte(fmt.Sprintf(`{"question":%q}`, question))}
}

func unseenIDs(t *testing.T, st *Store) map[string]bool {
	t.Helper()
	rows, _, _, err := st.NotificationPage(context.Background(), time.Now(), NotificationFilter{Kind: "question", Unseen: true}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, n := range rows {
		out[n.ID] = true
	}
	return out
}

// A question that is answered, cancelled or left by a destroyed agent stops
// being unseen in the inbox.
func TestSettledQuestionsAreSeen(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.AddProject(ctx, Project{Name: "sharing", Root: "/src/sharing", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []Question{
		{ID: "q-open", Project: "sharing", Agent: "agent-30"},
		{ID: "q-answered", Project: "sharing", Agent: "agent-30"},
		{ID: "q-cancelled", Project: "sharing", Agent: "agent-30"},
		{ID: "q-gone", Project: "sharing", Agent: "agent-28"},
	} {
		q.Status, q.CreatedAt = QuestionEscalated, time.Now()
		if err := st.AddQuestion(ctx, q); err != nil {
			t.Fatal(err)
		}
		if err := st.AddNotification(ctx, noticeOf("n-"+q.ID, q.Project, q.Agent, q.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AnswerQuestion(ctx, "q-answered", "yes", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CancelQuestion(ctx, "q-cancelled", "no longer needed"); err != nil {
		t.Fatal(err)
	}
	if err := st.CancelQuestions(ctx, "sharing", "agent-28"); err != nil {
		t.Fatal(err)
	}
	if got := unseenIDs(t, st); len(got) != 1 || !got["n-q-open"] {
		t.Fatalf("unseen = %v, want only n-q-open", got)
	}
}

// The migration settles the notifications of questions that were already over.
func TestQuestionNoticeMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	last := slices.IndexFunc(migrations, func(m string) bool {
		return strings.Contains(m, "kind = 'question'") && strings.HasPrefix(m, "UPDATE notifications")
	})
	if last < 0 {
		t.Fatal("the question notice migration is gone; point this test at it")
	}
	for i, m := range migrations[:last] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", last)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO projects (name, root, created_at) VALUES ('sharing', '/s', 1)`,
		`INSERT INTO questions (id, project, agent, text, status, created_at) VALUES ('open', 'sharing', 'agent-30', 't', 'escalated', 1)`,
		`INSERT INTO questions (id, project, agent, text, status, created_at) VALUES ('dead', 'sharing', 'agent-28', 't', 'cancelled', 1)`,
		`INSERT INTO notifications (id, project, agent, kind, created_at, seen_at, data) VALUES ('n-open', 'sharing', 'agent-30', 'question', 9999999999999, 0, '{"question":"open"}')`,
		`INSERT INTO notifications (id, project, agent, kind, created_at, seen_at, data) VALUES ('n-dead', 'sharing', 'agent-28', 'question', 9999999999999, 0, '{"question":"dead"}')`,
		`INSERT INTO notifications (id, project, agent, kind, created_at, seen_at, data) VALUES ('n-none', 'sharing', 'agent-27', 'question', 9999999999999, 0, '{"question":"deleted"}')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if got := unseenIDs(t, st); len(got) != 1 || !got["n-open"] {
		t.Fatalf("unseen = %v, want only n-open", got)
	}
}
