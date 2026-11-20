package state

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/trace"
)

// Timing: where an agent's time goes. The chat writes a row as each turn
// ends and as each tool call in one does (internal/chat/timing.go), and the
// daemon one as each heavy command ends (daemon/timing.go). The token ledger
// says what a turn spent, not how long anything in it took; this does.
//
// What it times must not wait for it: RecordTiming only queues rows, and
// they are written together a moment later, in one transaction, by a timer
// of their own. A row that doesn't fit the queue, or can't be written, is
// lost and counted in the log.

// Timing kinds.
const (
	TimingTurn    = "turn"
	TimingTool    = "tool"
	TimingCommand = "command"
)

// KeepTiming is how long timing rows are kept: long enough to compare this
// week's with last week's, short enough that a busy installation's rows,
// one per tool call, don't pile up.
const KeepTiming = 30 * 24 * time.Hour

// timingDelay is how long a queued row waits for others to be written with,
// and maxTimingQueue how many may wait at most.
const (
	timingDelay    = time.Second
	maxTimingQueue = 10_000
	timingPruneGap = time.Hour
)

// TimingRow is one timed thing: a turn, a tool call or a heavy command.
// Fields another kind has are left zero.
type TimingRow struct {
	Kind    string
	Project string
	Agent   string
	AgentID string
	AI      string
	// Turn is the chat turn, a turn's own id or the one a tool call ran in.
	Turn string
	// Name is a tool's name or a command's program; for a turn, the model
	// the chat was set to.
	Name string
	// Detail is a tool call's title or a command's command line, cut short.
	Detail string
	// Sub is the subagent that made a tool call, "" for the main loop.
	Sub string
	// Status is how a turn ended (completed, cancelled, failed) or a tool
	// call did (completed, failed, stopped).
	Status string
	At     time.Time     // when it started
	Wall   time.Duration // how long it took

	// A turn's: from the message to the prompt reaching the tool (Start);
	// the model's time from each request to its last token (Model), the
	// wait for the first call's first token (FirstToken) and for each
	// call's (TTFT, summed over ModelCalls); the time at least one of its
	// main loop's ToolCalls ran (Tools).
	Start, Model, FirstToken, TTFT, Tools time.Duration
	ModelCalls, ToolCalls                 int

	// A command's: the worker count the daemon gave it (HeavyStart.Workers), 0 when none, and its exit
	// status, -1 when it didn't say.
	Jobs     int
	ExitCode int
}

// timingBuffer is the queue RecordTiming fills and FlushTiming empties.
type timingBuffer struct {
	mu       sync.Mutex
	rows     []TimingRow
	timer    *time.Timer
	dropped  int
	prunedAt time.Time
	closed   bool
	// write is held while a batch is written, so Close can wait for one.
	write sync.Mutex
}

// RecordTiming queues rows to be written within timingDelay. It never
// waits on the database.
func (s *Store) RecordTiming(rows ...TimingRow) {
	b := &s.timing
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(rows) == 0 {
		return
	}
	if room := maxTimingQueue - len(b.rows); len(rows) > room {
		b.dropped += len(rows) - room
		rows = rows[:room]
	}
	b.rows = append(b.rows, rows...)
	if b.timer == nil && len(b.rows) > 0 {
		b.timer = time.AfterFunc(timingDelay, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.FlushTiming(ctx); err != nil {
				s.gate.log("timing: writing rows: %v", err)
			}
		})
	}
}

// FlushTiming writes what RecordTiming queued now, and drops rows older than
// KeepTiming at most once every timingPruneGap.
func (s *Store) FlushTiming(ctx context.Context) error {
	b := &s.timing
	b.write.Lock()
	defer b.write.Unlock()
	b.mu.Lock()
	rows, dropped := b.rows, b.dropped
	b.rows, b.dropped = nil, 0
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	now := time.Now()
	prune := !b.closed && now.Sub(b.prunedAt) >= timingPruneGap
	if prune {
		b.prunedAt = now
	}
	b.mu.Unlock()
	if dropped > 0 {
		s.gate.log("timing: %d rows lost: more than %d waited to be written", dropped, maxTimingQueue)
	}
	if err := s.addTimings(ctx, rows); err != nil {
		return err
	}
	if prune {
		return s.pruneTiming(ctx, now.Add(-KeepTiming))
	}
	return nil
}

// closeTiming writes what is queued, and refuses rows from here on: the
// store is closing.
func (s *Store) closeTiming() {
	b := &s.timing
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.FlushTiming(ctx); err != nil {
		s.gate.log("timing: writing rows as the store closes: %v", err)
	}
}

func (s *Store) addTimings(ctx context.Context, rows []TimingRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := trace.BeginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO timing
		(kind, project, agent, agent_id, ai, turn, name, detail, sub, status, started_at, ms,
		 start_ms, model_ms, first_token_ms, ttft_ms, model_calls, tool_ms, tool_calls, jobs, exit_code)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.Kind, r.Project, r.Agent, r.AgentID, r.AI, r.Turn, r.Name, r.Detail, r.Sub, r.Status,
			r.At.UnixMilli(), r.Wall.Milliseconds(), r.Start.Milliseconds(), r.Model.Milliseconds(), r.FirstToken.Milliseconds(),
			r.TTFT.Milliseconds(), r.ModelCalls, r.Tools.Milliseconds(), r.ToolCalls, r.Jobs, r.ExitCode); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// pruneTiming drops rows that started before cutoff, kind by kind, so each
// delete reads timing_by_time.
func (s *Store) pruneTiming(ctx context.Context, cutoff time.Time) error {
	for _, kind := range []string{TimingTurn, TimingTool, TimingCommand} {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM timing WHERE kind = ? AND started_at < ?`, kind, cutoff.UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

// TimingFilter narrows the timing rows read. Every field is optional: Agent
// alone is that agent name in every project.
type TimingFilter struct {
	Project string
	Agent   string
	Since   time.Time
}

func (f TimingFilter) where(kind string) (string, []any) {
	clauses, args := []string{"kind = ?"}, []any{kind}
	if f.Project != "" {
		clauses = append(clauses, "project = ?")
		args = append(args, f.Project)
	}
	if f.Agent != "" {
		clauses = append(clauses, "agent = ?")
		args = append(args, f.Agent)
	}
	if !f.Since.IsZero() {
		clauses = append(clauses, "started_at >= ?")
		args = append(args, f.Since.UnixMilli())
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// TimingSum adds up one agent's turns and commands.
type TimingSum struct {
	Project, Agent string
	Turns          int64
	// Answered is how many of the turns the model answered at all, which
	// FirstToken averages over.
	Answered                                    int64
	Wall, Start, Model, FirstToken, TTFT, Tools time.Duration
	ModelCalls, ToolCalls                       int64
	Commands, FailedCommands                    int64
	CommandTime                                 time.Duration
	LastAt                                      time.Time
}

// TimingSums adds up each agent's turns and heavy commands, busiest first.
func (s *Store) TimingSums(ctx context.Context, f TimingFilter) ([]TimingSum, error) {
	byRef := map[string]*TimingSum{}
	var order []*TimingSum
	sum := func(project, agent string) *TimingSum {
		ref := project + "/" + agent
		if byRef[ref] == nil {
			byRef[ref] = &TimingSum{Project: project, Agent: agent}
			order = append(order, byRef[ref])
		}
		return byRef[ref]
	}
	ms := func(n int64) time.Duration { return time.Duration(n) * time.Millisecond }
	where, args := f.where(TimingTurn)
	rows, err := s.db.QueryContext(ctx, `SELECT project, agent, COUNT(*), SUM(model_calls > 0), SUM(ms), SUM(start_ms),
		SUM(model_ms), SUM(first_token_ms), SUM(ttft_ms), SUM(model_calls), SUM(tool_ms), SUM(tool_calls), MAX(started_at + ms)
		FROM timing`+where+` GROUP BY project, agent`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var project, agent string
		var turns, answered, wall, start, model, first, ttft, calls, tools, toolCalls, last int64
		if err := rows.Scan(&project, &agent, &turns, &answered, &wall, &start, &model, &first, &ttft, &calls, &tools, &toolCalls, &last); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t := sum(project, agent)
		t.Turns, t.Answered, t.ModelCalls, t.ToolCalls = turns, answered, calls, toolCalls
		t.Wall, t.Start, t.Model, t.FirstToken, t.TTFT, t.Tools = ms(wall), ms(start), ms(model), ms(first), ms(ttft), ms(tools)
		t.LastAt = time.UnixMilli(last)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	where, args = f.where(TimingCommand)
	rows, err = s.db.QueryContext(ctx, `SELECT project, agent, COUNT(*), SUM(exit_code > 0), SUM(ms), MAX(started_at + ms)
		FROM timing`+where+` GROUP BY project, agent`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var project, agent string
		var n, failed, total, last int64
		if err := rows.Scan(&project, &agent, &n, &failed, &total, &last); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t := sum(project, agent)
		t.Commands, t.FailedCommands, t.CommandTime = n, failed, ms(total)
		if at := time.UnixMilli(last); at.After(t.LastAt) {
			t.LastAt = at
		}
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	out := make([]TimingSum, len(order))
	for i, t := range order {
		out[i] = *t
	}
	slices.SortStableFunc(out, func(a, b TimingSum) int { return cmp.Compare(b.Wall+b.CommandTime, a.Wall+a.CommandTime) })
	return out, nil
}

// closeRows closes rows, saying whether reading them failed.
func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

// ToolTimeSum is one tool's calls added up.
type ToolTimeSum struct {
	Name, Sub  string
	Calls      int64
	Failed     int64
	Total, Max time.Duration
}

// ToolTimeSums adds up tool calls by tool and by who made them, the most
// time first, at most limit.
func (s *Store) ToolTimeSums(ctx context.Context, f TimingFilter, limit int) ([]ToolTimeSum, error) {
	where, args := f.where(TimingTool)
	rows, err := s.db.QueryContext(ctx, `SELECT name, sub, COUNT(*), SUM(status = 'failed'), SUM(ms), MAX(ms)
		FROM timing`+where+` GROUP BY name, sub ORDER BY SUM(ms) DESC, name LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	var out []ToolTimeSum
	for rows.Next() {
		var t ToolTimeSum
		var total, most int64
		if err := rows.Scan(&t.Name, &t.Sub, &t.Calls, &t.Failed, &total, &most); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t.Total, t.Max = time.Duration(total)*time.Millisecond, time.Duration(most)*time.Millisecond
		out = append(out, t)
	}
	return out, closeRows(rows)
}

// SlowestTimings is the longest rows of kind, longest first, at most limit.
func (s *Store) SlowestTimings(ctx context.Context, kind string, f TimingFilter, limit int) ([]TimingRow, error) {
	where, args := f.where(kind)
	rows, err := s.db.QueryContext(ctx, `SELECT kind, project, agent, agent_id, ai, turn, name, detail, sub, status,
		started_at, ms, start_ms, model_ms, first_token_ms, ttft_ms, model_calls, tool_ms, tool_calls, jobs, exit_code
		FROM timing`+where+` ORDER BY ms DESC, started_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	var out []TimingRow
	for rows.Next() {
		var r TimingRow
		var at, wall, start, model, first, ttft, tools int64
		if err := rows.Scan(&r.Kind, &r.Project, &r.Agent, &r.AgentID, &r.AI, &r.Turn, &r.Name, &r.Detail, &r.Sub, &r.Status,
			&at, &wall, &start, &model, &first, &ttft, &r.ModelCalls, &tools, &r.ToolCalls, &r.Jobs, &r.ExitCode); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ms := func(n int64) time.Duration { return time.Duration(n) * time.Millisecond }
		r.At, r.Wall, r.Start, r.Model, r.FirstToken, r.TTFT, r.Tools = time.UnixMilli(at), ms(wall), ms(start), ms(model), ms(first), ms(ttft), ms(tools)
		out = append(out, r)
	}
	return out, closeRows(rows)
}
