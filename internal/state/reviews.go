package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AgentReview is what the user decided about a finished agent in "Ready to
// review": reviewed, dismissed or changes requested (with Note, what to
// change), and the fingerprint of the work as it was when they did. The daemon decides what a fingerprint is; here it is only
// compared.
type AgentReview struct {
	Project     string
	Agent       string
	Verdict     string
	Fingerprint string
	Note        string
	At          time.Time
}

// SetAgentReview records a verdict on an agent, replacing any earlier one.
func (s *Store) SetAgentReview(ctx context.Context, r AgentReview) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_reviews (project, agent, verdict, fingerprint, note, at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, agent) DO UPDATE SET verdict = excluded.verdict, fingerprint = excluded.fingerprint, note = excluded.note, at = excluded.at`,
		r.Project, r.Agent, r.Verdict, r.Fingerprint, r.Note, r.At.UnixMilli())
	return err
}

// AgentReviewOf is the verdict on an agent, and false when there is none.
func (s *Store) AgentReviewOf(ctx context.Context, project, agent string) (AgentReview, bool, error) {
	r := AgentReview{Project: project, Agent: agent}
	var at int64
	err := s.db.QueryRowContext(ctx,
		`SELECT verdict, fingerprint, note, at FROM agent_reviews WHERE project = ? AND agent = ?`, project, agent).
		Scan(&r.Verdict, &r.Fingerprint, &r.Note, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentReview{}, false, nil
	}
	if err != nil {
		return AgentReview{}, false, err
	}
	r.At = time.UnixMilli(at)
	return r, true, nil
}

// removeAgentReview drops the verdict with its agent: the name may go to
// another agent, whose work nobody has looked at.
func (s *Store) removeAgentReview(ctx context.Context, project, agent string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_reviews WHERE project = ? AND agent = ?`, project, agent)
	return err
}
