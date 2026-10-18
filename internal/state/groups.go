package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/trace"
)

// Groups: one task run on several agents at once, each perhaps on another
// tool or model, so their work can be compared side by side and one kept.
//
// A group is its own row rather than a column on agents: its members outlive
// their machines (the ones not kept are retired, their branches kept, and a
// destroyed agent leaves the agents table), and a member exists before its
// agent does, while its create job copies a machine, or instead of one when
// that failed.

// MaxGroupSize is how many agents one task can be run on at once. Each is a
// machine, a context window and a share of the user's limits, and more than
// a handful can't be compared side by side anyway.
const MaxGroupSize = 6

// Group is one task given to several agents.
type Group struct {
	ID      string
	Project string
	Title   string
	Task    string
	// ByLead says the project's chat made it, with fan_out; otherwise the
	// user did, in the app or on the command line.
	ByLead bool
	// Kept is the agent the user chose to keep, once they have.
	Kept string
	// Recommended and Recommendation are the lead's pick and its reasons,
	// once it was asked to judge and answered. Judging says it was asked and
	// hasn't answered yet.
	Recommended    string
	Recommendation string
	// Judge asks the lead to judge as soon as every member has finished.
	Judge     bool
	Judging   bool
	CreatedAt time.Time
	// SettledAt is when the group was decided, by keeping one or closing it
	// with none kept; zero while it is open.
	SettledAt time.Time
	Members   []GroupMember
}

// GroupMember is one of the agents a group asked for.
type GroupMember struct {
	Position int
	AI       string
	Model    string
	Effort   string
	// Job makes the agent, and Agent is its name once it is made. Error is
	// why it couldn't be.
	Job   string
	Agent string
	Error string
	// FinishedAt is when it first finished its task; zero while it works.
	FinishedAt time.Time
}

// Settled says the group was decided.
func (g Group) Settled() bool { return !g.SettledAt.IsZero() }

// Member is the member running agent, if any.
func (g Group) Member(agent string) (GroupMember, bool) {
	for _, m := range g.Members {
		if agent != "" && m.Agent == agent {
			return m, true
		}
	}
	return GroupMember{}, false
}

func newGroupID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("reading random bytes: %v", err))
	}
	return "grp_" + hex.EncodeToString(b)
}

// AddGroup records a new group and the agents it asks for, and gives it its
// ID. Its members' jobs and agents are filled in as they start and finish.
func (s *Store) AddGroup(ctx context.Context, g Group) (Group, error) {
	if len(g.Members) < 2 {
		return Group{}, errors.New("a group runs its task on at least two agents")
	}
	if len(g.Members) > MaxGroupSize {
		return Group{}, fmt.Errorf("a group runs its task on at most %d agents", MaxGroupSize)
	}
	g.ID = newGroupID()
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_groups (id, project, title, task, by_lead, judge, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.Project, g.Title, g.Task, g.ByLead, g.Judge, g.CreatedAt.UnixMilli()); err != nil {
		return Group{}, err
	}
	for i := range g.Members {
		m := &g.Members[i]
		m.Position = i + 1
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_group_members (group_id, position, project, agent, ai, model, effort, job, error)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, g.ID, m.Position, g.Project, m.Agent, m.AI, m.Model, m.Effort, m.Job, m.Error); err != nil {
			return Group{}, err
		}
	}
	return g, tx.Commit()
}

const groupColumns = `id, project, title, task, by_lead, kept, recommended, recommendation, judge, judging, created_at, settled_at`

// GroupByID is one group with its members.
func (s *Store) GroupByID(ctx context.Context, id string) (Group, error) {
	groups, err := s.queryGroups(ctx, `WHERE id = ?`, id)
	if err != nil {
		return Group{}, err
	}
	if len(groups) == 0 {
		return Group{}, fmt.Errorf("group %q: %w", id, ErrNotFound)
	}
	return groups[0], nil
}

// Groups lists a project's groups, or every project's for "", newest first:
// every open one, and the settled ones of the last since (all of them for
// zero).
func (s *Store) Groups(ctx context.Context, project string, since time.Duration) ([]Group, error) {
	var clauses []string
	var args []any
	if project != "" {
		clauses = append(clauses, "project = ?")
		args = append(args, project)
	}
	if since > 0 {
		clauses = append(clauses, "(settled_at = 0 OR settled_at >= ?)")
		args = append(args, time.Now().Add(-since).UnixMilli())
	}
	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}
	return s.queryGroups(ctx, where, args...)
}

// GroupOf is the group an agent was made for, if any.
func (s *Store) GroupOf(ctx context.Context, project, agent string) (Group, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT group_id FROM agent_group_members WHERE project = ? AND agent = ? AND agent != ''`, project, agent).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, false, nil
	}
	if err != nil {
		return Group{}, false, err
	}
	g, err := s.GroupByID(ctx, id)
	return g, err == nil, err
}

func (s *Store) queryGroups(ctx context.Context, where string, args ...any) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+groupColumns+` FROM agent_groups `+where+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	var out []Group
	index := map[string]int{}
	for rows.Next() {
		var g Group
		var created, settled int64
		if err := rows.Scan(&g.ID, &g.Project, &g.Title, &g.Task, &g.ByLead, &g.Kept, &g.Recommended, &g.Recommendation, &g.Judge, &g.Judging, &created, &settled); err != nil {
			_ = rows.Close()
			return nil, err
		}
		g.CreatedAt = time.UnixMilli(created)
		if settled > 0 {
			g.SettledAt = time.UnixMilli(settled)
		}
		index[g.ID] = len(out)
		out = append(out, g)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]any, 0, len(out))
	for _, g := range out {
		ids = append(ids, g.ID)
	}
	members, err := s.db.QueryContext(ctx, `SELECT group_id, position, agent, ai, model, effort, job, error, finished_at
		FROM agent_group_members WHERE group_id IN (?`+strings.Repeat(", ?", len(ids)-1)+`) ORDER BY group_id, position`, ids...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = members.Close() }()
	for members.Next() {
		var id string
		var m GroupMember
		var finished int64
		if err := members.Scan(&id, &m.Position, &m.Agent, &m.AI, &m.Model, &m.Effort, &m.Job, &m.Error, &finished); err != nil {
			return nil, err
		}
		if finished > 0 {
			m.FinishedAt = time.UnixMilli(finished)
		}
		if i, ok := index[id]; ok {
			out[i].Members = append(out[i].Members, m)
		}
	}
	return out, members.Err()
}

// SetGroupMember records what became of one member's create job: its job, and
// then the agent it made or why it couldn't.
func (s *Store) SetGroupMember(ctx context.Context, group string, position int, job, agent, failure string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent_group_members SET
		job = CASE WHEN ? != '' THEN ? ELSE job END,
		agent = CASE WHEN ? != '' THEN ? ELSE agent END,
		error = ? WHERE group_id = ? AND position = ?`, job, job, agent, agent, failure, group, position)
	return err
}

// GroupMemberFinished records that an agent finished its group's task, the
// first time it does: later finishes, after a word from the lead, don't move
// the time its work is compared by. It reports whether the agent is in a
// group that is still open.
func (s *Store) GroupMemberFinished(ctx context.Context, project, agent string, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE agent_group_members SET finished_at = ?
		WHERE project = ? AND agent = ? AND agent != '' AND finished_at = 0
		AND group_id IN (SELECT id FROM agent_groups WHERE settled_at = 0)`, at.UnixMilli(), project, agent)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// KeepInGroup settles a group on the agent the user chose, which must be one
// of its members. A settled group can't be decided again.
func (s *Store) KeepInGroup(ctx context.Context, id, agent string) error {
	return s.settleGroup(ctx, id, agent)
}

// CloseGroup settles a group keeping none of its agents.
func (s *Store) CloseGroup(ctx context.Context, id string) error {
	return s.settleGroup(ctx, id, "")
}

func (s *Store) settleGroup(ctx context.Context, id, agent string) error {
	g, err := s.GroupByID(ctx, id)
	if err != nil {
		return err
	}
	if g.Settled() {
		if g.Kept != "" {
			return fmt.Errorf("this group was already decided: %s was kept", g.Kept)
		}
		return errors.New("this group was already closed")
	}
	if agent != "" {
		if _, ok := g.Member(agent); !ok {
			return fmt.Errorf("%s isn't one of this group's agents", agent)
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE agent_groups SET kept = ?, judging = 0, settled_at = ? WHERE id = ? AND settled_at = 0`,
		agent, time.Now().UnixMilli(), id)
	return err
}

// SetGroupJudging records that the lead was asked to judge a group, or that
// asking it failed.
func (s *Store) SetGroupJudging(ctx context.Context, id string, judging bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent_groups SET judging = ? WHERE id = ?`, judging, id)
	return err
}

// RecommendInGroup records the lead's pick of a group's agents, and why.
func (s *Store) RecommendInGroup(ctx context.Context, id, agent, why string) error {
	g, err := s.GroupByID(ctx, id)
	if err != nil {
		return err
	}
	if _, ok := g.Member(agent); !ok {
		return fmt.Errorf("%s isn't one of this group's agents", agent)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE agent_groups SET recommended = ?, recommendation = ?, judging = 0 WHERE id = ?`, agent, why, id)
	return err
}
