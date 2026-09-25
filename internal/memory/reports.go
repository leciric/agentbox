package memory

import (
	"context"
	"errors"
	"time"
)

// AddReport records what an agent said when it finished. The summary is prose,
// because that is what an agent writes; the three lists are not, because the
// next agent's brief has to be built from them without asking a model to read
// the prose again.
func (s *Store) AddReport(ctx context.Context, r Report) (Report, error) {
	if err := requireProject(r.Project); err != nil {
		return Report{}, err
	}
	if r.Agent == "" {
		return Report{}, errors.New("a report needs the agent it came from")
	}
	if r.Status == "" {
		r.Status = StatusDone
	}
	if err := oneOf("a report's status", r.Status, Statuses); err != nil {
		return Report{}, err
	}
	task, err := text("a report's task", r.Task, MaxTitleLen*4)
	if err != nil {
		return Report{}, err
	}
	summary, err := text("a report's summary", r.Summary, MaxContentLen)
	if err != nil {
		return Report{}, err
	}
	if summary == "" {
		return Report{}, errors.New("a report needs a summary: what happened, in a few sentences")
	}
	r.Task, r.Summary = task, summary
	r.Discoveries, r.Decisions = cleanList(r.Discoveries), cleanList(r.Decisions)
	r.RemainingIssues, r.Artifacts = cleanList(r.RemainingIssues), cleanList(r.Artifacts)
	discoveries, err := jsonList(r.Discoveries)
	if err != nil {
		return Report{}, err
	}
	decisions, err := jsonList(r.Decisions)
	if err != nil {
		return Report{}, err
	}
	remaining, err := jsonList(r.RemainingIssues)
	if err != nil {
		return Report{}, err
	}
	artifacts, err := jsonList(r.Artifacts)
	if err != nil {
		return Report{}, err
	}
	if r.ID == "" {
		r.ID = newID("rep")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	r.CreatedAt = stamp(r.CreatedAt)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO agent_reports (id, project, agent, created_at, task, status, summary, discoveries, decisions, remaining_issues, artifacts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Project, r.Agent, r.CreatedAt.UnixMilli(), r.Task, r.Status, r.Summary,
		discoveries, decisions, remaining, artifacts)
	if err != nil {
		return Report{}, err
	}
	return r, nil
}

// Reports are a project's, newest first; one agent's when agent isn't empty.
func (s *Store) Reports(ctx context.Context, project, agent string) ([]Report, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	if agent == "" {
		return s.queryReports(ctx, `WHERE agent_reports.project = ? ORDER BY agent_reports.created_at DESC, agent_reports.rowid DESC LIMIT ?`, project, MaxLimit)
	}
	return s.queryReports(ctx, `WHERE agent_reports.project = ? AND agent_reports.agent = ? ORDER BY agent_reports.created_at DESC, agent_reports.rowid DESC LIMIT ?`, project, agent, MaxLimit)
}

// Report is one by id.
func (s *Store) Report(ctx context.Context, project, id string) (Report, error) {
	rows, err := s.queryReports(ctx, `WHERE agent_reports.project = ? AND agent_reports.id = ?`, project, id)
	if err != nil {
		return Report{}, err
	}
	if len(rows) == 0 {
		return Report{}, notFound("report", id)
	}
	return rows[0], nil
}

// Qualified, because Search joins this table with an FTS5 index whose
// columns have the same names.
const reportColumns = `agent_reports.id, agent_reports.project, agent_reports.agent, agent_reports.created_at,
	agent_reports.task, agent_reports.status, agent_reports.summary, agent_reports.discoveries,
	agent_reports.decisions, agent_reports.remaining_issues, agent_reports.artifacts`

func (s *Store) queryReports(ctx context.Context, clause string, args ...any) ([]Report, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+reportColumns+` FROM agent_reports `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanReport(rows scanner) (Report, error) {
	var r Report
	var created int64
	var discoveries, decisions, remaining, artifacts string
	if err := rows.Scan(&r.ID, &r.Project, &r.Agent, &created, &r.Task, &r.Status, &r.Summary,
		&discoveries, &decisions, &remaining, &artifacts); err != nil {
		return Report{}, err
	}
	r.CreatedAt = attime(created)
	r.Discoveries, r.Decisions = readList(discoveries), readList(decisions)
	r.RemainingIssues, r.Artifacts = readList(remaining), readList(artifacts)
	return r, nil
}
