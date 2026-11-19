package state

import (
	"context"
	"fmt"
	"time"
)

// IssueAgent is the agent one of a project's GitHub issues was handed to:
// from the Issues view, by the lead, or picked up by itself because it was
// labelled agentbox (Project.PickUpIssues).
type IssueAgent struct {
	Project string
	// Repo is the GitHub repository, as owner/name: a project's remote can
	// move, and #12 of one repository isn't #12 of another.
	Repo   string
	Number int
	Title  string
	// Agent is empty while Job, the create job, is still making it, and stays
	// once the agent is retired.
	Agent string
	Job   string
	// Auto says it was picked up by itself rather than handed by someone.
	Auto      bool
	CreatedAt time.Time
	// PRNumber, PRURL and PRState are the pull request the agent opened, once
	// one was seen; kept so the issue still shows it after the agent is gone.
	PRNumber int
	PRURL    string
	PRState  string
}

const issueAgentColumns = `project, repo, number, title, agent, job, auto, created_at, pr_number, pr_url, pr_state`

// RecordIssueAgent records that an issue was handed to an agent, replacing
// whoever it was handed to before.
func (s *Store) RecordIssueAgent(ctx context.Context, ia IssueAgent) error {
	if ia.CreatedAt.IsZero() {
		ia.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO issue_agents (`+issueAgentColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ia.Project, ia.Repo, ia.Number, ia.Title, ia.Agent, ia.Job, ia.Auto, ia.CreatedAt.Unix(), ia.PRNumber, ia.PRURL, ia.PRState)
	return err
}

// SetIssueAgentName names the agent an issue's create job made.
func (s *Store) SetIssueAgentName(ctx context.Context, job, agent string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE issue_agents SET agent = ? WHERE job = ? AND agent = ''`, agent, job)
	return err
}

// SetIssueAgentPR records the pull request an issue's agent opened.
func (s *Store) SetIssueAgentPR(ctx context.Context, project, repo string, number, pr int, url, prState string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE issue_agents SET pr_number = ?, pr_url = ?, pr_state = ? WHERE project = ? AND repo = ? AND number = ?`,
		pr, url, prState, project, repo, number)
	return err
}

// ForgetIssueAgent drops an issue's hand-off: its create job failed, so it
// has no agent and may be handed again.
func (s *Store) ForgetIssueAgent(ctx context.Context, job string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM issue_agents WHERE job = ? AND agent = ''`, job)
	return err
}

// IssueAgents lists the issues of a project's repository that were handed to
// an agent, by issue number.
func (s *Store) IssueAgents(ctx context.Context, project, repo string) (map[int]IssueAgent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+issueAgentColumns+` FROM issue_agents WHERE project = ? AND repo = ?`, project, repo)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int]IssueAgent{}
	for rows.Next() {
		var ia IssueAgent
		var created int64
		if err := rows.Scan(&ia.Project, &ia.Repo, &ia.Number, &ia.Title, &ia.Agent, &ia.Job, &ia.Auto, &created,
			&ia.PRNumber, &ia.PRURL, &ia.PRState); err != nil {
			return nil, fmt.Errorf("reading issue agents: %w", err)
		}
		ia.CreatedAt = time.Unix(created, 0)
		out[ia.Number] = ia
	}
	return out, rows.Err()
}

// SetProjectPickUpIssues sets whether a project hands its issues labelled
// agentbox to new agents by itself.
func (s *Store) SetProjectPickUpIssues(ctx context.Context, name string, on bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET pick_up_issues = ? WHERE name = ?`, on, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}
