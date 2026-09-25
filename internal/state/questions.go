package state

import (
	"context"
	"fmt"
	"time"
)

// A question is an agent asking its project's chat for a decision it can't make
// alone. The lead answers it, or escalates it to the user when it can't. The
// agent waits for the answer, so a question is always resolved one way or the
// other: answered, escalated then answered, or cancelled.

// Question statuses.
const (
	// QuestionPending is waiting for the lead.
	QuestionPending = "pending"
	// QuestionEscalated is waiting for the user, because the lead couldn't answer.
	QuestionEscalated = "escalated"
	QuestionAnswered  = "answered"
	// QuestionCancelled means nobody will answer: the agent gave up or went away.
	QuestionCancelled = "cancelled"
)

// Question kinds. A decision is the question above; the other two are an
// agent asking the user for a credential it lacks (request_credential), which
// only the user can answer, from the app, and which never carries the value:
// the answer is what happened ("pushing works now"), not the credential.
const (
	QuestionDecision = ""
	QuestionGitHub   = "github"
	QuestionSecret   = "secret"
)

type Question struct {
	ID      string
	Project string
	Agent   string
	Kind    string
	// SecretName is the variable a secret request's value goes into.
	SecretName string
	Text       string
	Context    string // what the agent was doing, for whoever answers
	Status     string
	Answer     string
	// AnsweredBy is "lead" or "user"; Escalation is why the lead passed it on.
	AnsweredBy string
	Escalation string
	CreatedAt  time.Time
	AnsweredAt time.Time
}

func (q Question) Ref() string { return q.Project + "/" + q.Agent }

// Credential reports whether the question asks for a credential rather than a
// decision.
func (q Question) Credential() bool { return q.Kind != QuestionDecision }

// Waiting reports whether somebody still has to answer.
func (q Question) Waiting() bool { return q.Status == QuestionPending || q.Status == QuestionEscalated }

const questionColumns = `id, project, agent, kind, secret_name, text, context, status, answer, answered_by, escalation, created_at, answered_at`

func (s *Store) AddQuestion(ctx context.Context, q Question) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO questions (`+questionColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		q.ID, q.Project, q.Agent, q.Kind, q.SecretName, q.Text, q.Context, q.Status, q.Answer, q.AnsweredBy, q.Escalation,
		q.CreatedAt.UnixMilli(), unixMilli(q.AnsweredAt))
	return err
}

func (s *Store) Question(ctx context.Context, id string) (Question, error) {
	questions, err := s.queryQuestions(ctx, `WHERE id = ?`, id)
	if err != nil {
		return Question{}, err
	}
	if len(questions) == 0 {
		return Question{}, fmt.Errorf("question %s: %w", id, ErrNotFound)
	}
	return questions[0], nil
}

// Questions lists a project's, newest first. Waiting limits it to the ones
// somebody still has to answer.
func (s *Store) Questions(ctx context.Context, project string, waiting bool) ([]Question, error) {
	where := `WHERE project = ?`
	args := []any{project}
	if waiting {
		where += ` AND status IN (?, ?)`
		args = append(args, QuestionPending, QuestionEscalated)
	}
	return s.queryQuestions(ctx, where+` ORDER BY created_at DESC, rowid DESC`, args...)
}

// AnswerQuestion records an answer, unless somebody answered it first.
func (s *Store) AnswerQuestion(ctx context.Context, id, answer, by string) (Question, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE questions SET status = ?, answer = ?, answered_by = ?, answered_at = ? WHERE id = ? AND status IN (?, ?)`,
		QuestionAnswered, answer, by, time.Now().UnixMilli(), id, QuestionPending, QuestionEscalated)
	if err != nil {
		return Question{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		q, err := s.Question(ctx, id)
		if err != nil {
			return Question{}, err
		}
		return q, fmt.Errorf("question %s was already %s", id, q.Status)
	}
	return s.Question(ctx, id)
}

// EscalateQuestion passes a question to the user, with why the lead couldn't
// answer it.
func (s *Store) EscalateQuestion(ctx context.Context, id, why string) (Question, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE questions SET status = ?, escalation = ? WHERE id = ? AND status = ?`,
		QuestionEscalated, why, id, QuestionPending)
	if err != nil {
		return Question{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		q, err := s.Question(ctx, id)
		if err != nil {
			return Question{}, err
		}
		return q, fmt.Errorf("question %s is %s, not waiting for the chat", id, q.Status)
	}
	return s.Question(ctx, id)
}

// CancelQuestions gives up on an agent's unanswered questions, when it is
// retired or its turn ends without waiting for them.
func (s *Store) CancelQuestions(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE questions SET status = ? WHERE project = ? AND agent = ? AND status IN (?, ?)`,
		QuestionCancelled, project, agent, QuestionPending, QuestionEscalated)
	return err
}

func (s *Store) queryQuestions(ctx context.Context, clause string, args ...any) ([]Question, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+questionColumns+` FROM questions `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var questions []Question
	for rows.Next() {
		var q Question
		var created, answered int64
		if err := rows.Scan(&q.ID, &q.Project, &q.Agent, &q.Kind, &q.SecretName, &q.Text, &q.Context, &q.Status,
			&q.Answer, &q.AnsweredBy, &q.Escalation, &created, &answered); err != nil {
			return nil, err
		}
		q.CreatedAt = time.UnixMilli(created)
		if answered > 0 {
			q.AnsweredAt = time.UnixMilli(answered)
		}
		questions = append(questions, q)
	}
	return questions, rows.Err()
}
