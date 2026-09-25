// Package state persists AgentBox projects and agents in SQLite.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"agentbox/internal/gitrepo"
	"agentbox/internal/memory"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

// migrations run in order and are tracked with PRAGMA user_version. Append only.
var migrations = []string{
	`CREATE TABLE projects (
		name       TEXT PRIMARY KEY,
		root       TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE agents (
		project     TEXT NOT NULL REFERENCES projects(name),
		name        TEXT NOT NULL,
		instance    TEXT NOT NULL UNIQUE,
		ai          TEXT NOT NULL,
		autonomous  INTEGER NOT NULL,
		branch      TEXT NOT NULL,
		base_ref    TEXT NOT NULL,
		base_commit TEXT NOT NULL,
		worktree    TEXT NOT NULL,
		status      TEXT NOT NULL,
		created_at  INTEGER NOT NULL,
		PRIMARY KEY (project, name)
	)`,
	`ALTER TABLE agents ADD COLUMN source TEXT NOT NULL DEFAULT ''`,
	`CREATE TABLE jobs (
		id          TEXT PRIMARY KEY,
		kind        TEXT NOT NULL,
		target      TEXT NOT NULL,
		status      TEXT NOT NULL,
		error       TEXT NOT NULL DEFAULT '',
		result      TEXT NOT NULL DEFAULT '',
		log         TEXT NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL,
		finished_at INTEGER NOT NULL DEFAULT 0
	)`,
	`ALTER TABLE agents ADD COLUMN title TEXT NOT NULL DEFAULT ''`,
	`CREATE TABLE media (
		id         TEXT PRIMARY KEY,
		project    TEXT NOT NULL,
		agent      TEXT NOT NULL,
		kind       TEXT NOT NULL,
		name       TEXT NOT NULL,
		file       TEXT NOT NULL DEFAULT '',
		mime       TEXT NOT NULL DEFAULT '',
		size       INTEGER NOT NULL DEFAULT 0,
		sha256     TEXT NOT NULL DEFAULT '',
		source     TEXT NOT NULL,
		text       TEXT NOT NULL DEFAULT '',
		meta       TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL
	)`,
	`CREATE INDEX media_by_agent ON media (project, agent, created_at)`,
	`ALTER TABLE projects ADD COLUMN claude_account TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE agents ADD COLUMN claude_account TEXT NOT NULL DEFAULT ''`,
	// Agents made before the chat use their AI tool's command line.
	`ALTER TABLE agents ADD COLUMN interface TEXT NOT NULL DEFAULT 'cli'`,
	`CREATE TABLE chat_items (
		project  TEXT NOT NULL,
		agent    TEXT NOT NULL,
		id       TEXT NOT NULL,
		position INTEGER NOT NULL,
		data     TEXT NOT NULL,
		PRIMARY KEY (project, agent, id)
	)`,
	`CREATE TABLE chats (
		project    TEXT NOT NULL,
		agent      TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT '',
		options    TEXT NOT NULL DEFAULT '{}',
		PRIMARY KEY (project, agent)
	)`,
	// Every agent made before the project chat does the work itself.
	`ALTER TABLE agents ADD COLUMN role TEXT NOT NULL DEFAULT 'worker'`,
	// What an agent asks its project's chat, and who answered.
	`CREATE TABLE questions (
		id          TEXT PRIMARY KEY,
		project     TEXT NOT NULL,
		agent       TEXT NOT NULL,
		text        TEXT NOT NULL,
		context     TEXT NOT NULL DEFAULT '',
		status      TEXT NOT NULL,
		answer      TEXT NOT NULL DEFAULT '',
		answered_by TEXT NOT NULL DEFAULT '',
		escalation  TEXT NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL,
		answered_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX questions_by_project ON questions (project, status, created_at)`,
	// How much a project's chat may do on its own: ask (the default) or on.
	`ALTER TABLE projects ADD COLUMN autonomy TEXT NOT NULL DEFAULT 'ask'`,
	// GitHub accounts are named too, mirroring Claude Code accounts.
	`ALTER TABLE projects ADD COLUMN github_account TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE agents ADD COLUMN github_account TEXT NOT NULL DEFAULT ''`,
	// Media kept past its agent starts its retention clock here, not at
	// created_at: an item already older than the retention window at the
	// moment it's kept shouldn't expire the instant it outlives its agent.
	// 0 means its agent still exists, so it never expires.
	`ALTER TABLE media ADD COLUMN orphaned_at INTEGER NOT NULL DEFAULT 0`,
	// How long a project keeps media whose agent is gone, in days, before the
	// daemon sweeps it away. Media of an agent that still exists is unaffected.
	`ALTER TABLE projects ADD COLUMN media_retention_days INTEGER NOT NULL DEFAULT 30`,
	// Settings that belong to this installation rather than to one project,
	// like the model new Claude Code agents start on.
	`CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	// What a project does when one of its agents finishes: tell its chat and
	// let it decide ('chat', what every project did before this column), or
	// only record it ('off'). A question from an agent is unaffected: the
	// agent is blocked on it, so it always starts a turn.
	`ALTER TABLE projects ADD COLUMN finish_notices TEXT NOT NULL DEFAULT 'chat'`,
	// The API keys and tokens you hand to agents, at project scope (agent '')
	// or to one agent. value is ciphertext, sealed by package secrets under the
	// key in ~/.config/agentbox/secrets.key: this file never holds a plaintext
	// secret.
	`CREATE TABLE secrets (
		project    TEXT NOT NULL,
		agent      TEXT NOT NULL DEFAULT '',
		name       TEXT NOT NULL,
		value      BLOB NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (project, agent, name)
	)`,
	// The model this project's new agents are created on: '' to follow the
	// installation's setting, a model name, or 'auto' to let the chat choose
	// one per task. Projects made before this follow the installation.
	`ALTER TABLE projects ADD COLUMN agent_model TEXT NOT NULL DEFAULT ''`,
	// What this one agent does to its project's chat when it finishes, which
	// only matters when the project's own finish_notices is 'lead': '' defers
	// to the project ('lead' wakes it, same as an agent that chose 'chat'),
	// 'chat' wakes it and 'off' only records the finish. Meaningless, and
	// ignored, when the project's finish_notices is 'chat' or 'off' — those
	// decide it for every agent themselves.
	`ALTER TABLE agents ADD COLUMN finish_notice TEXT NOT NULL DEFAULT ''`,

	// The project brain (D72),
	// queried by package memory. It lives in this database, and its schema
	// lives here, because migrations are one ordered list per file.

	// events is raw, append-only history: what happened, in whatever shape the
	// thing that happened has. Nothing rewrites a row, and nothing here is
	// summarised — a memory is a separate row somebody wrote on purpose.
	`CREATE TABLE events (
		id          TEXT PRIMARY KEY,
		project     TEXT NOT NULL,
		agent       TEXT NOT NULL DEFAULT '',
		session     TEXT NOT NULL DEFAULT '',
		at          INTEGER NOT NULL,
		type        TEXT NOT NULL,
		payload     TEXT NOT NULL DEFAULT '{}',
		artifact_id TEXT
	)`,
	`CREATE INDEX events_by_project ON events (project, at)`,
	`CREATE INDEX events_by_agent ON events (project, agent, at)`,
	// memories are what somebody decided is worth keeping. supersedes_id
	// points at the memory this one replaces, and is the only record of that:
	// a memory is superseded when another one names it, so the two can't
	// disagree. memories_superseded is the index that reverse lookup needs.
	`CREATE TABLE memories (
		id              TEXT PRIMARY KEY,
		project         TEXT NOT NULL,
		kind            TEXT NOT NULL,
		title           TEXT NOT NULL,
		content         TEXT NOT NULL,
		importance      INTEGER NOT NULL DEFAULT 3,
		created_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL,
		supersedes_id   TEXT,
		source_event_id TEXT
	)`,
	`CREATE INDEX memories_by_project ON memories (project, kind, created_at)`,
	`CREATE INDEX memories_superseded ON memories (supersedes_id)`,
	// One small JSON document per project: what it is doing right now.
	`CREATE TABLE working_memory (
		project    TEXT PRIMARY KEY,
		data       TEXT NOT NULL DEFAULT '{}',
		updated_at INTEGER NOT NULL
	)`,
	// artifacts are references to things that live elsewhere — a file, a
	// branch, a pull request, a media item. Never their contents.
	`CREATE TABLE artifacts (
		id         TEXT PRIMARY KEY,
		project    TEXT NOT NULL,
		agent      TEXT NOT NULL DEFAULT '',
		type       TEXT NOT NULL,
		path       TEXT NOT NULL,
		metadata   TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL
	)`,
	`CREATE INDEX artifacts_by_project ON artifacts (project, created_at)`,
	// What an agent said when it finished, in a shape that can be read back
	// without an LLM: the lists are JSON arrays of strings.
	`CREATE TABLE agent_reports (
		id               TEXT PRIMARY KEY,
		project          TEXT NOT NULL,
		agent            TEXT NOT NULL,
		created_at       INTEGER NOT NULL,
		task             TEXT NOT NULL DEFAULT '',
		status           TEXT NOT NULL DEFAULT '',
		summary          TEXT NOT NULL DEFAULT '',
		discoveries      TEXT NOT NULL DEFAULT '[]',
		decisions        TEXT NOT NULL DEFAULT '[]',
		remaining_issues TEXT NOT NULL DEFAULT '[]',
		artifacts        TEXT NOT NULL DEFAULT '[]'
	)`,
	`CREATE INDEX reports_by_project ON agent_reports (project, created_at)`,
	`CREATE INDEX reports_by_agent ON agent_reports (project, agent, created_at)`,
	// Search is FTS5, external-content: the index holds no copy of the row,
	// only its terms, and triggers keep it in step with every write. The
	// default unicode61 tokenizer splits internal/daemon/server.go into four
	// tokens, so a filename, a port or an error string is found as a phrase.
	`CREATE VIRTUAL TABLE memories_fts USING fts5(title, content, content='memories', content_rowid='rowid')`,
	`CREATE TRIGGER memories_fts_insert AFTER INSERT ON memories BEGIN
		INSERT INTO memories_fts (rowid, title, content) VALUES (new.rowid, new.title, new.content);
	END`,
	`CREATE TRIGGER memories_fts_delete AFTER DELETE ON memories BEGIN
		INSERT INTO memories_fts (memories_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, old.content);
	END`,
	`CREATE TRIGGER memories_fts_update AFTER UPDATE ON memories BEGIN
		INSERT INTO memories_fts (memories_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, old.content);
		INSERT INTO memories_fts (rowid, title, content) VALUES (new.rowid, new.title, new.content);
	END`,
	`CREATE VIRTUAL TABLE events_fts USING fts5(type, payload, content='events', content_rowid='rowid')`,
	`CREATE TRIGGER events_fts_insert AFTER INSERT ON events BEGIN
		INSERT INTO events_fts (rowid, type, payload) VALUES (new.rowid, new.type, new.payload);
	END`,
	`CREATE TRIGGER events_fts_delete AFTER DELETE ON events BEGIN
		INSERT INTO events_fts (events_fts, rowid, type, payload) VALUES ('delete', old.rowid, old.type, old.payload);
	END`,
	// A report's three lists are indexed as they are stored, JSON and all:
	// the tokenizer treats the brackets and quotes as separators, so what is
	// searchable is the words inside them. Every column here is a real column
	// of agent_reports, which is what an external-content index requires.
	`CREATE VIRTUAL TABLE reports_fts USING fts5(task, summary, discoveries, decisions, remaining_issues,
		content='agent_reports', content_rowid='rowid')`,
	`CREATE TRIGGER reports_fts_insert AFTER INSERT ON agent_reports BEGIN
		INSERT INTO reports_fts (rowid, task, summary, discoveries, decisions, remaining_issues)
		VALUES (new.rowid, new.task, new.summary, new.discoveries, new.decisions, new.remaining_issues);
	END`,
	`CREATE TRIGGER reports_fts_delete AFTER DELETE ON agent_reports BEGIN
		INSERT INTO reports_fts (reports_fts, rowid, task, summary, discoveries, decisions, remaining_issues)
		VALUES ('delete', old.rowid, old.task, old.summary, old.discoveries, old.decisions, old.remaining_issues);
	END`,

	// How full this project's chat lets its context get before the daemon
	// compacts the conversation and rolls the session over
	// (D73), as a percentage of
	// the context window: 0 switches it off. Projects made before this get
	// the default, like a new one.
	`ALTER TABLE projects ADD COLUMN rollover_threshold INTEGER NOT NULL DEFAULT 80`,
	// What a project's agents reported — created, finished, asked, answered —
	// which the app shows as a thread per agent beside the project's chat.
	// data is the JSON of api.AgentEvent, the way chat_items holds an item.
	`CREATE TABLE agent_events (
		id         TEXT PRIMARY KEY,
		project    TEXT NOT NULL,
		agent      TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		data       TEXT NOT NULL
	)`,
	`CREATE INDEX agent_events_by_project ON agent_events (project, created_at)`,
	// How many tokens one built context of this project may cost
	// (D75). The default is
	// memory.DefaultBudgetTokens, written out because a migration is SQL and
	// can't name a Go constant; TestContextBudgetDefaultMatchesMemory is what
	// keeps the two the same number.
	`ALTER TABLE projects ADD COLUMN context_budget INTEGER NOT NULL DEFAULT 4000`,

	// Consolidation (D76): what
	// turns a thousand raw events into a page of memories, and the bookkeeping
	// that keeps a pass from doing the same work twice.

	// referenced_at is the last time something put this memory in front of a
	// model — a recap, a brief, a distillation's window. decayed_at is the
	// last time the mechanical pass took a point off its importance. Both are
	// 0 for "never", and both exist so a pass that runs every hour doesn't
	// decay the same memory every hour.
	`ALTER TABLE memories ADD COLUMN referenced_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE memories ADD COLUMN decayed_at INTEGER NOT NULL DEFAULT 0`,
	// resolved_at says an issue was closed without anything replacing it, and
	// resolved_by says what closed it. This is a stored flag where
	// supersedes_id deliberately isn't one, and it can be: supersession has a
	// derived truth beside it (a reverse lookup over supersedes_id) that a
	// second column could contradict, and resolution has none — this column
	// is the only record of its own fact.
	`ALTER TABLE memories ADD COLUMN resolved_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE memories ADD COLUMN resolved_by TEXT NOT NULL DEFAULT ''`,
	// Pairs of live memories of one kind whose titles are near-identical.
	// Candidates, not verdicts: nothing here deletes or hides a memory, and
	// the pass rewrites the whole set each time it runs, so a pair that stops
	// matching stops being listed.
	`CREATE TABLE memory_duplicates (
		project      TEXT NOT NULL,
		memory_id    TEXT NOT NULL,
		duplicate_of TEXT NOT NULL,
		similarity   INTEGER NOT NULL,
		found_at     INTEGER NOT NULL,
		PRIMARY KEY (project, memory_id, duplicate_of)
	)`,
	// One row per consolidation pass: what it read, what it wrote, what it
	// cost. A few integer columns rather than a metrics system — enough for
	// the app to show a project how many events became how many memories.
	// through_event and through_at are the watermark a distillation reached:
	// the newest successful distill pass is where the next one starts, so
	// nothing re-reads what it already folded in.
	`CREATE TABLE consolidation_passes (
		id                  TEXT PRIMARY KEY,
		project             TEXT NOT NULL,
		kind                TEXT NOT NULL,
		at                  INTEGER NOT NULL,
		duration_ms         INTEGER NOT NULL DEFAULT 0,
		events_read         INTEGER NOT NULL DEFAULT 0,
		memories_written    INTEGER NOT NULL DEFAULT 0,
		memories_superseded INTEGER NOT NULL DEFAULT 0,
		memories_resolved   INTEGER NOT NULL DEFAULT 0,
		memories_decayed    INTEGER NOT NULL DEFAULT 0,
		duplicates_found    INTEGER NOT NULL DEFAULT 0,
		input_bytes         INTEGER NOT NULL DEFAULT 0,
		output_bytes        INTEGER NOT NULL DEFAULT 0,
		through_event       TEXT NOT NULL DEFAULT '',
		through_at          INTEGER NOT NULL DEFAULT 0,
		error               TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX consolidation_passes_by_project ON consolidation_passes (project, kind, at)`,
	// How many new events a project gathers before its chat is asked to
	// distil them into memories, 0 to switch consolidation off entirely.
	// Projects made before this get the default, like a new one.
	`ALTER TABLE projects ADD COLUMN consolidation INTEGER NOT NULL DEFAULT 200`,

	// Project state as a graph (D77):
	// the tasks a project has, who is on each and what each is waiting on.
	// It is not memory — a memory is a judgement that outlives the work, and
	// a task is the work — so it lives beside the other tables rather than
	// inside them, and nothing here is indexed for search.

	// One row per task. status is a closed set, checked in Go rather than by
	// SQLite so the error can say what the set is; parent_task_id is the
	// subtask edge, and NULL for a task nothing contains. closed_at is 0 for
	// a task that is still open, and is derived from status on every write:
	// it is a convenience for listings, never a second opinion about whether
	// a task is done.
	`CREATE TABLE tasks (
		id             TEXT PRIMARY KEY,
		project        TEXT NOT NULL,
		agent          TEXT NOT NULL DEFAULT '',
		parent_task_id TEXT,
		status         TEXT NOT NULL DEFAULT 'open',
		goal           TEXT NOT NULL,
		detail         TEXT NOT NULL DEFAULT '',
		created_at     INTEGER NOT NULL,
		updated_at     INTEGER NOT NULL,
		closed_at      INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX tasks_by_project ON tasks (project, status, created_at)`,
	`CREATE INDEX tasks_by_agent ON tasks (project, agent, created_at)`,
	`CREATE INDEX tasks_by_parent ON tasks (parent_task_id)`,
	// The blocking edges, which are a second graph over the same rows and not
	// the parent one: a task is blocked on however many things it is blocked
	// on, and none of them contains it. Both directions are asked for — what
	// is this waiting on, and what is waiting on this — so there is an index
	// each way.
	`CREATE TABLE task_dependencies (
		project       TEXT NOT NULL,
		task_id       TEXT NOT NULL,
		depends_on_id TEXT NOT NULL,
		created_at    INTEGER NOT NULL,
		PRIMARY KEY (project, task_id, depends_on_id)
	)`,
	`CREATE INDEX task_dependencies_by_dependency ON task_dependencies (project, depends_on_id)`,

	// Which model distils this project's events into memories
	// (D78). Reading a window of
	// history and writing a few memories is not frontier work, and billing it
	// to the model the user chats on is how a project's chat spends a usage
	// limit on summarising itself. ConsolidationModelCheap is the default,
	// here and for projects made before this: it names no vendor's model, and
	// is resolved per AI tool when a pass runs.
	`ALTER TABLE projects ADD COLUMN consolidation_model TEXT NOT NULL DEFAULT 'cheap'`,
	// Which model a pass actually ran on, empty for a mechanical pass and for
	// a distillation on whatever the chat runs on. A distillation that fell
	// back — the cheap model was refused, or its session wouldn't start — is
	// a pass whose model isn't the project's setting, which is the only way
	// the cost table can say that falling back happened.
	`ALTER TABLE consolidation_passes ADD COLUMN model TEXT NOT NULL DEFAULT ''`,

	// How the user organised the sidebar
	// (D79): sections, which are
	// rows of their own, and two columns on a project saying where it sits.
	//
	// A section is a table rather than a name on a project so that renaming
	// one touches one row, and so an empty section can exist while the user
	// fills it. It is the one table here that isn't project-scoped: a section
	// is a sibling of a project, not something a project owns.
	`CREATE TABLE project_sections (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		position   INTEGER NOT NULL,
		collapsed  INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	)`,
	// The section a project is in, '' for none, and where it sits in that
	// list. There is no foreign key: '' is a real value and would have to
	// reference a row, so the store clears this column itself when a section
	// is deleted, in the same transaction. Position 0 means "never placed by
	// hand" and sorts last by name, which is why every project of an
	// installation that has organised nothing reads back alphabetically, as
	// it did before there was an order at all; a placed project is numbered
	// from 1.
	`ALTER TABLE projects ADD COLUMN section TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE projects ADD COLUMN position INTEGER NOT NULL DEFAULT 0`,

	// What every chat spent, turn by turn
	// (D83). One row per model a
	// turn used, so a turn whose subagent ran on another model is two rows
	// with one turn id. It is a ledger rather than state: nothing here refers
	// to an agents row, so it outlives the agent it describes — which is when
	// "who spent my limit" usually gets asked. cost_usd is the adapter's own
	// estimate at API prices, carried whole on one row of the turn, and
	// context_tokens is how full the session's context was when the turn
	// ended.
	`CREATE TABLE token_usage (
		id                 INTEGER PRIMARY KEY,
		project            TEXT NOT NULL,
		agent              TEXT NOT NULL,
		ai                 TEXT NOT NULL,
		session_id         TEXT NOT NULL DEFAULT '',
		turn               TEXT NOT NULL DEFAULT '',
		kind               TEXT NOT NULL,
		model              TEXT NOT NULL DEFAULT '',
		at                 INTEGER NOT NULL,
		input_tokens       INTEGER NOT NULL DEFAULT 0,
		output_tokens      INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
		cache_write_tokens INTEGER NOT NULL DEFAULT 0,
		cost_usd           REAL NOT NULL DEFAULT 0,
		context_tokens     INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX token_usage_by_agent ON token_usage (project, agent, at)`,
	`CREATE INDEX token_usage_by_time ON token_usage (at)`,

	// The last thing Anthropic said about each Claude account's usage limits
	// (D85): how much of the
	// five-hour and weekly windows is used and when each resets, as
	// claude-agent-acp relays it on a chat's usage_update. One row per
	// account, replaced by the next reading; reading is the adapter's own JSON,
	// kept whole so a window Anthropic adds later isn't lost on the way in.
	`CREATE TABLE claude_limits (
		account TEXT PRIMARY KEY,
		reading TEXT NOT NULL,
		at      INTEGER NOT NULL
	)`,

	// The Claude Code accounts a project's agents may use, comma-separated.
	// '' allows every account on the machine, which is what every project
	// did before this column.
	`ALTER TABLE projects ADD COLUMN claude_accounts TEXT NOT NULL DEFAULT ''`,

	// What a project's agent branches are named with, before the agent's own
	// name. 'agentbox/' is what every agent's branch was before this column.
	`ALTER TABLE projects ADD COLUMN branch_prefix TEXT NOT NULL DEFAULT 'agentbox/'`,

	// What a question asks for: '' is a decision, which is what every
	// question was before these columns; github and secret are an agent
	// asking the user for a credential, and secret_name is the variable a
	// secret goes into. The value itself is never stored here.
	`ALTER TABLE questions ADD COLUMN kind TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE questions ADD COLUMN secret_name TEXT NOT NULL DEFAULT ''`,
}

// DefaultMediaRetentionDays is what projects.media_retention_days reads as
// when it is zero. Nothing reads that column any more: how long media is kept
// is SettingMediaRetention, the installation's.
const DefaultMediaRetentionDays = 30

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// _txlock=immediate takes the write lock when a transaction begins rather
	// than at its first write. Every transaction in this package writes, and
	// several read before they do — a layout reads the sections it is about to
	// renumber (D79) — and a deferred transaction that upgrades from reading
	// to writing after somebody else has written fails outright: SQLite's busy
	// handler doesn't apply to that one case, so busy_timeout below wouldn't
	// save it. Taking the lock up front turns two writers racing into one
	// waiting for the other.
	dsn := "file:" + path + "?_txlock=immediate&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB is the open database, for the packages that keep their own tables in it
// rather than their own file: package memory, whose schema is migrated here
// with everything else (D72).
// Anything that holds this has already been migrated to the current version.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

type Project struct {
	Name      string
	Root      string
	CreatedAt time.Time
	// ClaudeAccount is the Claude Code account this project's new agents use;
	// empty means the machine's default account.
	ClaudeAccount string
	// ClaudeAccounts are the Claude Code accounts this project's agents may
	// use, in the order they were given; empty allows every account.
	// When it isn't empty, ClaudeAccount is one of them or empty.
	ClaudeAccounts []string
	// GitHubAccount is the GitHub account this project's new agents use;
	// empty means the machine's default account.
	GitHubAccount string
	// Autonomy is how much this project's chat may do without being asked:
	// AutonomyAsk (propose, and wait) or AutonomyOn (act, within a budget).
	Autonomy string
	// AgentModel is the model this project's new agents are created on: empty
	// to follow the installation's setting, a model name every agent of the
	// project starts on unless one is named for it, or AgentModelAuto to let
	// the project's chat choose per task. See DirectAgentModel and
	// LeadPicksModel, which say what each of the three means where it is read.
	AgentModel string
	// BranchPrefix comes before the slug in the branch an agent is created on
	// (agent.branchFor): agentbox/ makes agentbox/fix-login. It may be empty, or have several
	// components, like thiago/agentbox/. Agents keep the branch they were
	// created on when it changes.
	BranchPrefix string
	// MediaRetentionDays is what the project once said about how long media
	// whose agent is gone survives. It is no longer read: SettingMediaRetention
	// replaced it, for the whole installation.
	MediaRetentionDays int
	// FinishNotices is what happens when one of this project's agents
	// finishes: FinishNoticesChat (tell the chat, and let it decide),
	// FinishNoticesOff (record it, and spend nothing), or FinishNoticesLead
	// (let the agent that finished decide, per Agent.FinishNotice).
	FinishNotices string
	// RolloverThreshold is how full this project's chat lets the model's
	// context get, as a percentage of it, before the daemon consolidates the
	// conversation into memory and carries it on in a fresh session (D73).
	// RolloverOff switches that off, and the conversation runs until the
	// model's own compaction, or its limit, deals with it.
	RolloverThreshold int
	// ContextBudget is how many estimated tokens one context built from this
	// project's memory may cost: the chat's recap, a worker's "What the
	// project knows", or an answer to POST /context (D75). A worker's share of
	// it is memory.AgentBudget. Zero is never stored; a project that hasn't
	// chosen gets DefaultContextBudget.
	ContextBudget int
	// Consolidation is how many new events this project gathers before its
	// chat is asked to distil them into memories (D76). ConsolidationOff
	// switches both halves of consolidation off for the project: no
	// distillation, and no mechanical pass either.
	Consolidation int
	// ConsolidationModel is the model that reads those events (D78):
	// ConsolidationModelCheap for the cheap model of whichever AI tool the
	// project already runs, a model id to name one, or ConsolidationModelChat
	// ("") for whatever the project's chat is running on, which is what
	// distillation did before there was a choice.
	ConsolidationModel string
	// Section is the id of the sidebar section this project is in, and "" for
	// a project in no section, which is where every project starts (D79).
	Section string
	// Position is where the project sits in its list — its section's, or the
	// list of projects in no section — from 1. Zero means nobody has placed
	// it by hand, and it sorts last in its list, by name.
	Position int
}

// How much a project's chat does on its own.
const (
	// AutonomyAsk does routine follow-through itself — retiring merged agents,
	// answering agents, the obvious next agent — and proposes product
	// decisions and anything costly or irreversible (D90).
	AutonomyAsk = "ask"
	// AutonomyOn also makes the product calls itself, and says what it did.
	AutonomyOn = "on"
)

// What a project does when one of its agents finishes.
const (
	// FinishNoticesChat puts the notice in front of the project's chat and
	// starts a turn, so the chat decides what happens next. The default.
	FinishNoticesChat = "chat"
	// FinishNoticesOff only records the notice in the chat's conversation:
	// the history shows the agent finished and what it said, but no turn is
	// started and no tokens are spent. Questions are unaffected.
	FinishNoticesOff = "off"
	// FinishNoticesLead leaves it to the agent that finished: its own
	// Agent.FinishNotice, which is FinishNoticesChat or FinishNoticesOff, or,
	// left unset, the same as FinishNoticesChat. The default for a new
	// project; an existing project keeps whatever it was set to.
	FinishNoticesLead = "lead"
)

// How full a project's chat lets its context get before it is compacted.
const (
	// DefaultRolloverThreshold is the percentage of the context window a new
	// project compacts at. It leaves a fifth of the window for the
	// consolidation itself and for the turn that follows it, which has to fit
	// beside the recap.
	DefaultRolloverThreshold = 80
	// RolloverOff is the threshold that never compacts.
	RolloverOff = 0
	// MinRolloverThreshold is the lowest percentage worth setting: below it a
	// fresh session, which starts with a brief and a recap already in it,
	// could be over the threshold from its first turn.
	MinRolloverThreshold = 10
	// MaxRolloverThreshold leaves room for the consolidation prompt to run on
	// the session it is summarising: at 100 there would be none.
	MaxRolloverThreshold = 95
)

// How much a project spends on one context built from its memory (D75). The
// numbers are package memory's, which is what spends them: one source of truth
// for a budget the setting only stores.
const (
	DefaultContextBudget = memory.DefaultBudgetTokens
	MinContextBudget     = memory.MinBudgetTokens
	MaxContextBudget     = memory.MaxBudgetTokens
)

// How much raw history a project gathers before it is distilled (D76).
const (
	// DefaultConsolidation is how many new events a new project gathers
	// before its chat is asked to turn them into memories. It is a few
	// hundred because the point is compression: one prompt that reads a
	// stretch of history costs far less than a stretch of history nobody ever
	// reads, and asking every twenty events would spend more than it saves.
	DefaultConsolidation = 200
	// ConsolidationOff switches consolidation off for a project: nothing
	// distils its events, and the mechanical pass leaves its memories alone.
	ConsolidationOff = 0
	// MinConsolidation is the smallest window worth asking about: below it
	// the chat would be interrupted for a handful of events that say nothing
	// on their own.
	MinConsolidation = 20
	// MaxConsolidation is the largest. Past it the window handed to the model
	// is bounded by memory.MaxDistillEvents anyway, so a bigger number would
	// only mean asking less often about the same amount of history.
	MaxConsolidation = 2000
)

// Which model distils a project's events into memories (D78). A distillation
// reads a window of history and writes a handful of memories: it is
// summarising, not engineering, and it does not need the model the user chats
// on — which is usually the most capable and most expensive one the account
// has.
const (
	// ConsolidationModelCheap asks for the cheap model of whichever AI tool
	// the project already runs, resolved by CheapModelFor when a pass runs.
	// It is a sentinel rather than a model id on purpose: the setting says
	// "the cheap one for this tool", so a project that switches tools doesn't
	// carry another vendor's model name with it.
	ConsolidationModelCheap = "cheap"
	// ConsolidationModelChat runs the distillation on the project chat's own
	// session, on whatever model that is. It is the behaviour D76 shipped,
	// kept as a choice: it starts no second adapter, and it is for somebody
	// who would rather spend the chat's context than the seconds.
	ConsolidationModelChat = ""
	// DefaultConsolidationModel is what a new project distils on, and what a
	// project made before D78 is migrated to.
	DefaultConsolidationModel = ConsolidationModelCheap
	// MaxConsolidationModelLen bounds a named model, which is a model id and
	// never a sentence.
	MaxConsolidationModelLen = 100
)

// cheapModels is the model AgentBox reaches for when a project asks for
// ConsolidationModelCheap, per AI tool.
//
// It is deliberately tiny and deliberately incomplete. Claude Code's Haiku is
// the entry AgentBox can stand behind: it is on the menu every account is
// offered, and it is an order of magnitude cheaper than what a project chat
// runs on. A tool with no entry here has no cheap model AgentBox is willing
// to guess at, and a project on it distils on its chat's own model — today's
// behaviour — until somebody names one. Naming a model in the setting always
// wins over this table.
var cheapModels = map[string]string{
	"claude": "haiku",
}

// CheapModelFor is the cheap model of an AI tool, or "" when AgentBox knows
// of none for it.
func CheapModelFor(ai string) string { return cheapModels[ai] }

// ConsolidationModelFor is the model a project's distillation should run on,
// given the AI tool it runs. "" means the project chat's own session, either
// because that is what the project asked for or because ConsolidationModelCheap
// couldn't be resolved for this tool.
func (p Project) ConsolidationModelFor(ai string) string {
	if p.ConsolidationModel == ConsolidationModelCheap {
		return CheapModelFor(ai)
	}
	return p.ConsolidationModel
}

// AgentModelAuto is the AgentModel that asks the project's chat to choose a
// model for each agent it creates, from the task's difficulty (see the "auto"
// section of internal/brief/lead.md.tmpl). It is not a model name and is never
// stored as one: an agent created without a model of its own falls back to the
// installation's setting exactly as it does when the project names nothing.
const AgentModelAuto = "auto"

// DirectAgentModel is the model every new agent of this project is created on,
// or "" when the project names none — either it was never set, or it is on
// AgentModelAuto, where the choice is the chat's per agent rather than the
// project's for all of them.
func (p Project) DirectAgentModel() string {
	if p.AgentModel == AgentModelAuto {
		return ""
	}
	return p.AgentModel
}

// LeadPicksModel reports whether this project's chat chooses each agent's model.
func (p Project) LeadPicksModel() bool { return p.AgentModel == AgentModelAuto }

const projectColumns = `name, root, created_at, claude_account, autonomy, github_account, media_retention_days, finish_notices, agent_model, rollover_threshold, context_budget, consolidation, consolidation_model, claude_accounts, branch_prefix`

// projectPlacement is where the project sits in the sidebar (D79), read
// beside the columns above rather than with them: it is written by the
// layout, never by AddProject, which is what projectColumns is also the
// argument list of.
const projectPlacement = `section, position`

// projectSelect is projectColumns qualified, for the one query that joins the
// sections in to order by them. One source of truth for the column list, so
// adding a column to a project can't leave this behind.
var projectSelect = "p." + strings.ReplaceAll(projectColumns, ", ", ", p.")

// projectOrder is the order the sidebar draws: the sections, in theirs, then
// the projects in no section. Within a list, a project placed by hand comes
// before one nobody has placed, which falls back to its name — so an
// installation that has organised nothing is alphabetical, exactly as it was
// before there was an order at all.
const projectOrder = `ORDER BY CASE WHEN p.section = '' THEN 1 ELSE 0 END, s.position,
	CASE WHEN p.position = 0 THEN 1 ELSE 0 END, p.position, p.name`

func (s *Store) AddProject(ctx context.Context, p Project) error {
	existing, err := s.projectWhere(ctx, "root = ?", p.Root)
	switch {
	case err == nil:
		return fmt.Errorf("%s is already project %q: %w", p.Root, existing.Name, ErrExists)
	case !errors.Is(err, ErrNotFound):
		return err
	}
	if _, err := s.projectWhere(ctx, "name = ?", p.Name); err == nil {
		return fmt.Errorf("project %q: %w", p.Name, ErrExists)
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if p.Autonomy == "" {
		p.Autonomy = AutonomyAsk
	}
	if p.MediaRetentionDays == 0 {
		p.MediaRetentionDays = DefaultMediaRetentionDays
	}
	if p.FinishNotices == "" {
		p.FinishNotices = FinishNoticesLead
	}
	// A project being added says nothing about compaction, so zero here is
	// "unsaid" rather than RolloverOff: switching it off is something the user
	// does afterwards, with SetProjectRolloverThreshold.
	if p.RolloverThreshold == 0 {
		p.RolloverThreshold = DefaultRolloverThreshold
	}
	if p.ContextBudget == 0 {
		p.ContextBudget = DefaultContextBudget
	}
	// Zero is "unsaid" here too, for the same reason: switching consolidation
	// off is something the user does afterwards.
	if p.Consolidation == 0 {
		p.Consolidation = DefaultConsolidation
	}
	// And here, where the empty string is the real choice "the chat's own
	// model": a project being added has said nothing about it, so it gets the
	// default, and asking for the chat's model is done afterwards with
	// SetProjectConsolidationModel.
	if p.ConsolidationModel == "" {
		p.ConsolidationModel = DefaultConsolidationModel
	}
	// And the empty branch prefix is a real choice too, made afterwards with
	// SetProjectBranchPrefix.
	if p.BranchPrefix == "" {
		p.BranchPrefix = DefaultBranchPrefix
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO projects (`+projectColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.Root, p.CreatedAt.Unix(), p.ClaudeAccount, p.Autonomy, p.GitHubAccount, p.MediaRetentionDays,
		p.FinishNotices, p.AgentModel, p.RolloverThreshold, p.ContextBudget, p.Consolidation, p.ConsolidationModel,
		strings.Join(p.ClaudeAccounts, ","), p.BranchPrefix)
	return err
}

func (s *Store) Project(ctx context.Context, name string) (Project, error) {
	p, err := s.projectWhere(ctx, "name = ?", name)
	if errors.Is(err, ErrNotFound) {
		return Project{}, fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return p, err
}

// Projects lists every project in the order the sidebar shows them (D79).
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectSelect+`, p.section, p.position
		FROM projects p LEFT JOIN project_sections s ON s.id = p.section `+projectOrder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var p Project
		var created int64
		var allowed string
		if err := rows.Scan(&p.Name, &p.Root, &created, &p.ClaudeAccount, &p.Autonomy, &p.GitHubAccount, &p.MediaRetentionDays,
			&p.FinishNotices, &p.AgentModel, &p.RolloverThreshold, &p.ContextBudget, &p.Consolidation,
			&p.ConsolidationModel, &allowed, &p.BranchPrefix, &p.Section, &p.Position); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(created, 0)
		p.ClaudeAccounts = splitAccounts(allowed)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *Store) RemoveProject(ctx context.Context, name string) error {
	var agents int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM agents WHERE project = ?`, name).Scan(&agents); err != nil {
		return err
	}
	if agents > 0 {
		return fmt.Errorf("project %q still has %d agent(s): destroy them first", name, agents)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	// The hole it left in its list is closed up, so positions stay 1..n (D79).
	if err := s.renumberProjects(ctx); err != nil {
		return err
	}
	// Its secrets go with it: nothing left can read them, and a project added
	// again at the same path shouldn't inherit the old one's keys.
	return s.RemoveProjectSecrets(ctx, name)
}

// SetProjectClaudeAccount picks the Claude Code account a project's new agents
// use. An empty name falls back to the machine's default account.
func (s *Store) SetProjectClaudeAccount(ctx context.Context, name, account string) error {
	p, err := s.Project(ctx, name)
	if err != nil {
		return err
	}
	return s.SetProjectClaudeAccounts(ctx, name, account, p.ClaudeAccounts)
}

// SetProjectClaudeAccounts sets a project's own Claude Code account and the
// accounts its agents may use, together, so the app can change both at once;
// an empty list allows every account. A list that leaves out the
// project's own account is refused rather than moving the project to another
// one: which account is billed stays something the user chose.
func (s *Store) SetProjectClaudeAccounts(ctx context.Context, name, account string, allowed []string) error {
	var list []string
	for _, a := range allowed {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(list, a) {
			list = append(list, a)
		}
	}
	if len(list) > 0 && account != "" && !slices.Contains(list, account) {
		return fmt.Errorf("%s may only use the Claude Code accounts %s, and its own account %q isn't one of them: add it, or make one of those the project's account",
			name, strings.Join(list, ", "), account)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET claude_account = ?, claude_accounts = ? WHERE name = ?`,
		account, strings.Join(list, ","), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// ClaudeAccountRename is what renaming a Claude Code account carried over.
type ClaudeAccountRename struct {
	// Projects are the projects whose own account or allow-list named it.
	Projects []string
	// Agents are the agents on it, by project/name, the projects' chats
	// included.
	Agents []string
}

// RenameClaudeAccount carries every reference to a Claude Code account over to
// its new name, in one transaction: each project's own account and allow-list,
// each agent's account (the leads' too), and the account's usage-limit
// reading. A reading already filed under the new name belongs to an account
// that no longer exists, and is replaced. The token ledger names no account,
// so it has nothing to carry. move, when not nil, runs inside the transaction
// — it is where the credentials store renames the token — and an error from
// it undoes the whole rename.
func (s *Store) RenameClaudeAccount(ctx context.Context, old, name string, move func() error) (ClaudeAccountRename, error) {
	var done ClaudeAccountRename
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return done, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT name, claude_account, claude_accounts FROM projects ORDER BY name`)
	if err != nil {
		return done, err
	}
	type project struct {
		name, account string
		allowed       []string
	}
	var changed []project
	for rows.Next() {
		var p project
		var allowed string
		if err := rows.Scan(&p.name, &p.account, &allowed); err != nil {
			rows.Close()
			return done, err
		}
		p.allowed = splitAccounts(allowed)
		if p.account != old && !slices.Contains(p.allowed, old) {
			continue
		}
		if p.account == old {
			p.account = name
		}
		// The new name was free, but an allow-list may still name a removed
		// account of that name: it keeps one entry, where the old one was.
		var renamed []string
		for _, a := range p.allowed {
			if a == old {
				a = name
			}
			if !slices.Contains(renamed, a) {
				renamed = append(renamed, a)
			}
		}
		p.allowed = renamed
		changed = append(changed, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return done, err
	}
	for _, p := range changed {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET claude_account = ?, claude_accounts = ? WHERE name = ?`,
			p.account, strings.Join(p.allowed, ","), p.name); err != nil {
			return done, err
		}
		done.Projects = append(done.Projects, p.name)
	}

	rows, err = tx.QueryContext(ctx, `SELECT project, name FROM agents WHERE claude_account = ? ORDER BY project, name`, old)
	if err != nil {
		return done, err
	}
	for rows.Next() {
		var project, agent string
		if err := rows.Scan(&project, &agent); err != nil {
			rows.Close()
			return done, err
		}
		done.Agents = append(done.Agents, project+"/"+agent)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return done, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agents SET claude_account = ? WHERE claude_account = ?`, name, old); err != nil {
		return done, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM claude_limits WHERE account = ?`, name); err != nil {
		return done, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE claude_limits SET account = ? WHERE account = ?`, name, old); err != nil {
		return done, err
	}
	if move != nil {
		if err := move(); err != nil {
			return ClaudeAccountRename{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ClaudeAccountRename{}, err
	}
	return done, nil
}

func splitAccounts(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// SetProjectGitHubAccount picks the GitHub account a project's new agents use.
// An empty name falls back to the machine's default account.
func (s *Store) SetProjectGitHubAccount(ctx context.Context, name, account string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET github_account = ? WHERE name = ?`, account, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// SetProjectAutonomy sets how much a project's chat does on its own.
func (s *Store) SetProjectAutonomy(ctx context.Context, name, autonomy string) error {
	if autonomy != AutonomyAsk && autonomy != AutonomyOn {
		return fmt.Errorf("unknown autonomy %q: use ask or on", autonomy)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET autonomy = ? WHERE name = ?`, autonomy, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// SetProjectAgentModel sets the model this project's new agents are created
// on. Three values, and each means something different where it is read:
//
//   - "" follows the installation's setting, which is what every project did
//     before this existed.
//   - a model name: every agent of the project starts on it, unless a model is
//     named for one agent as it is created.
//   - AgentModelAuto: the project's chat chooses per task, and an agent it
//     chose nothing for falls back to the installation's setting.
//
// The model is not checked against the account's menu, for the reason
// ChatChoices gives: the adapter resolves aliases the menu never lists
// (D45), so a literal check would
// reject choices that work. "default" is refused, because it is the menu's own
// word for "no model of my own" rather than a model id — stored as one, every
// turn of every agent of the project would fail.
func (s *Store) SetProjectAgentModel(ctx context.Context, name, model string) error {
	if model == "default" {
		return errors.New(`"default" is the model menu's own word for "no model of my own", not a model: leave it empty to follow the model new agents start on`)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET agent_model = ? WHERE name = ?`, model, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// DefaultBranchPrefix is the BranchPrefix of a project that hasn't chosen one.
const DefaultBranchPrefix = "agentbox/"

// SetProjectBranchPrefix sets what this project's new agents' branches are
// named with, before the agent's name. It must make a valid branch name (see
// gitrepo.CheckBranchPrefix); "" names the branch after the agent alone.
func (s *Store) SetProjectBranchPrefix(ctx context.Context, name, prefix string) error {
	if err := gitrepo.CheckBranchPrefix(prefix); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET branch_prefix = ? WHERE name = ?`, prefix, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// SetProjectMediaRetentionDays sets how long this project keeps media whose
// agent is gone before the daemon sweeps it away.
func (s *Store) SetProjectMediaRetentionDays(ctx context.Context, name string, days int) error {
	if days < 1 {
		return fmt.Errorf("invalid media retention %d: use at least 1 day", days)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET media_retention_days = ? WHERE name = ?`, days, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// SetProjectFinishNotices sets what happens when one of a project's agents
// finishes: tell its chat, only record it, or let the agent decide.
func (s *Store) SetProjectFinishNotices(ctx context.Context, name, notices string) error {
	if notices != FinishNoticesChat && notices != FinishNoticesOff && notices != FinishNoticesLead {
		return fmt.Errorf("unknown finish notices %q: use chat, off or lead", notices)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET finish_notices = ? WHERE name = ?`, notices, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// SetProjectRolloverThreshold sets how full this project's chat lets the
// model's context get before the conversation is compacted, as a percentage of
// it. RolloverOff switches compaction off.
func (s *Store) SetProjectRolloverThreshold(ctx context.Context, name string, percent int) error {
	if percent != RolloverOff && (percent < MinRolloverThreshold || percent > MaxRolloverThreshold) {
		return fmt.Errorf("invalid rollover threshold %d: use 0 to switch it off, or a percentage between %d and %d",
			percent, MinRolloverThreshold, MaxRolloverThreshold)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET rollover_threshold = ? WHERE name = ?`, percent, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// SetProjectContextBudget sets how many estimated tokens one context built
// from this project's memory may cost (D75). There is no "off": a consumer
// always gets something, and the smallest budget worth having is
// MinContextBudget.
func (s *Store) SetProjectContextBudget(ctx context.Context, name string, tokens int) error {
	if tokens < MinContextBudget || tokens > MaxContextBudget {
		return fmt.Errorf("invalid context budget %d: use between %d and %d tokens",
			tokens, MinContextBudget, MaxContextBudget)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET context_budget = ? WHERE name = ?`, tokens, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// SetProjectConsolidation sets how many new events this project gathers
// before its chat is asked to distil them into memories. ConsolidationOff
// switches consolidation off for the project altogether.
func (s *Store) SetProjectConsolidation(ctx context.Context, name string, every int) error {
	if every != ConsolidationOff && (every < MinConsolidation || every > MaxConsolidation) {
		return fmt.Errorf("invalid consolidation %d: use 0 to switch it off, or a number of events between %d and %d",
			every, MinConsolidation, MaxConsolidation)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET consolidation = ? WHERE name = ?`, every, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

// SetProjectConsolidationModel sets which model distils this project's events
// into memories (D78): ConsolidationModelCheap for the cheap model of the AI
// tool it runs, a model id to name one, or ConsolidationModelChat ("") for
// whatever the project's chat is running on.
//
// A model id is stored as it is typed and checked no further. AgentBox invents
// no model list (D45): whether an account can run what it named is the AI
// tool's answer, and it gives it when the pass runs — where a refusal falls
// back to the chat's own session rather than losing the consolidation.
func (s *Store) SetProjectConsolidationModel(ctx context.Context, name, model string) error {
	model = strings.TrimSpace(model)
	if len(model) > MaxConsolidationModelLen || strings.ContainsAny(model, " \t\n") {
		return fmt.Errorf("invalid consolidation model %q: use a model id, %q, or nothing at all for whatever the chat runs on",
			model, ConsolidationModelCheap)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET consolidation_model = ? WHERE name = ?`, model, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return nil
}

func (s *Store) projectWhere(ctx context.Context, where string, arg any) (Project, error) {
	var p Project
	var created int64
	var allowed string
	err := s.db.QueryRowContext(ctx, `SELECT `+projectColumns+`, `+projectPlacement+` FROM projects WHERE `+where, arg).
		Scan(&p.Name, &p.Root, &created, &p.ClaudeAccount, &p.Autonomy, &p.GitHubAccount, &p.MediaRetentionDays,
			&p.FinishNotices, &p.AgentModel, &p.RolloverThreshold, &p.ContextBudget, &p.Consolidation,
			&p.ConsolidationModel, &allowed, &p.BranchPrefix, &p.Section, &p.Position)
	p.ClaudeAccounts = splitAccounts(allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	p.CreatedAt = time.Unix(created, 0)
	return p, err
}

// Agent statuses. An agent stays "creating" if AgentBox died mid-create;
// destroy cleans it up.
const (
	AgentCreating = "creating"
	AgentReady    = "ready"
)

type Agent struct {
	Project    string
	Name       string
	Instance   string
	AI         string
	Autonomous bool
	Branch     string
	BaseRef    string
	BaseCommit string
	Worktree   string
	Status     string
	CreatedAt  time.Time
	Source     string // the snapshot the machine was copied from
	Title      string // what the user calls the agent; optional
	// ClaudeAccount is the Claude Code account whose token this agent holds.
	// It is resolved when the agent is created, and empty for agents that
	// don't run Claude Code.
	ClaudeAccount string
	// GitHubAccount is the GitHub account whose token this agent holds. It is
	// resolved when the agent is created, and empty when none is stored.
	GitHubAccount string
	// Interface is how you work with the agent's AI tool: in the app's chat
	// (InterfaceChat), or on its own command line in the terminal (InterfaceCLI).
	Interface string
	// Role is RoleWorker for an ordinary agent, or RoleLead for the one that
	// drives a project's chat. A lead runs on the host: it keeps the reserved
	// instance name, because the column is UNIQUE, but no such machine exists.
	// Ask IsLead, never Instance, before touching a machine.
	Role string
	// FinishNotice is this agent's own choice of what it does to its
	// project's chat when it genuinely finishes: FinishNoticesChat (wake it),
	// FinishNoticesOff (only record it), or "" to leave it unsaid, which
	// means the same as FinishNoticesChat. It is read only when the
	// project's own FinishNotices is FinishNoticesLead; otherwise the
	// project decides for every agent and this is ignored.
	FinishNotice string
}

// IsLead reports whether the agent is a project's lead, which runs on the host
// rather than in a machine of its own.
func (a Agent) IsLead() bool { return a.Role == RoleLead }

// Agent interfaces.
const (
	InterfaceChat = "chat"
	InterfaceCLI  = "cli"
)

// Agent roles.
const (
	RoleWorker = "worker"
	RoleLead   = "lead"
)

// LeadName is the reserved name of a project's lead agent.
const LeadName = "lead"

func (a Agent) Ref() string { return a.Project + "/" + a.Name }

const agentColumns = `project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at, source, title, claude_account, interface, role, github_account, finish_notice`

func (s *Store) AddAgent(ctx context.Context, a Agent) error {
	if a.Interface == "" {
		a.Interface = InterfaceCLI
	}
	if a.Role == "" {
		a.Role = RoleWorker
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (`+agentColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Project, a.Name, a.Instance, a.AI, a.Autonomous, a.Branch, a.BaseRef, a.BaseCommit, a.Worktree, a.Status, a.CreatedAt.Unix(), a.Source, a.Title, a.ClaudeAccount, a.Interface, a.Role, a.GitHubAccount, a.FinishNotice)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("agent %s: %w", a.Ref(), ErrExists)
	}
	if err != nil {
		return err
	}
	// A conversation left by an earlier agent of the same name isn't this one's.
	return removeChat(ctx, s.db, a.Project, a.Name)
}

func (s *Store) SetAgentStatus(ctx context.Context, project, name, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET status = ? WHERE project = ? AND name = ?`, status, project, name)
	return err
}

func (s *Store) SetAgentTitle(ctx context.Context, project, name, title string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET title = ? WHERE project = ? AND name = ?`, title, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("agent %s/%s: %w", project, name, ErrNotFound)
	}
	return nil
}

// SetAgentClaudeAccount records the Claude Code account an agent's token comes
// from. The token itself is written into the agent by package agent.
func (s *Store) SetAgentClaudeAccount(ctx context.Context, project, name, account string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET claude_account = ? WHERE project = ? AND name = ?`, account, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("agent %s/%s: %w", project, name, ErrNotFound)
	}
	return nil
}

// SetAgentGitHubAccount records the GitHub account an agent's token comes
// from. The token itself is written into the agent by package agent.
func (s *Store) SetAgentGitHubAccount(ctx context.Context, project, name, account string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET github_account = ? WHERE project = ? AND name = ?`, account, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("agent %s/%s: %w", project, name, ErrNotFound)
	}
	return nil
}

// SetAgentBaseCommit records the commit an agent's worktree stands on. Only a
// lead moves: an ordinary agent's base commit is where its branch started.
func (s *Store) SetAgentBaseCommit(ctx context.Context, project, name, commit string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET base_commit = ? WHERE project = ? AND name = ?`, commit, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("agent %s/%s: %w", project, name, ErrNotFound)
	}
	return nil
}

// SetAgentInterface records how you work with an agent's AI tool.
func (s *Store) SetAgentInterface(ctx context.Context, project, name, iface string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET interface = ? WHERE project = ? AND name = ?`, iface, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("agent %s/%s: %w", project, name, ErrNotFound)
	}
	return nil
}

func (s *Store) Agent(ctx context.Context, project, name string) (Agent, error) {
	agents, err := s.queryAgents(ctx, `WHERE project = ? AND name = ?`, project, name)
	if err != nil {
		return Agent{}, err
	}
	if len(agents) == 0 {
		return Agent{}, fmt.Errorf("agent %s/%s: %w", project, name, ErrNotFound)
	}
	return agents[0], nil
}

func (s *Store) AgentByInstance(ctx context.Context, instance string) (Agent, error) {
	agents, err := s.queryAgents(ctx, `WHERE instance = ?`, instance)
	if err != nil {
		return Agent{}, err
	}
	if len(agents) == 0 {
		return Agent{}, fmt.Errorf("agent with instance %s: %w", instance, ErrNotFound)
	}
	return agents[0], nil
}

// Agents lists the agents of a project, or of every project when project is "".
func (s *Store) Agents(ctx context.Context, project string) ([]Agent, error) {
	if project == "" {
		return s.queryAgents(ctx, `ORDER BY project, name`)
	}
	return s.queryAgents(ctx, `WHERE project = ? ORDER BY name`, project)
}

func (s *Store) RemoveAgent(ctx context.Context, project, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE project = ? AND name = ?`, project, name); err != nil {
		return err
	}
	// The secrets given to this agent alone go with it, like its conversation.
	// Its project's secrets are untouched: they belong to the project.
	if err := s.RemoveAgentSecrets(ctx, project, name); err != nil {
		return err
	}
	if err := s.removeAgentEvents(ctx, project, name); err != nil {
		return err
	}
	return removeChat(ctx, s.db, project, name)
}

func (s *Store) queryAgents(ctx context.Context, clause string, args ...any) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentColumns+` FROM agents `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []Agent
	for rows.Next() {
		var a Agent
		var created int64
		if err := rows.Scan(&a.Project, &a.Name, &a.Instance, &a.AI, &a.Autonomous, &a.Branch,
			&a.BaseRef, &a.BaseCommit, &a.Worktree, &a.Status, &created, &a.Source, &a.Title, &a.ClaudeAccount, &a.Interface, &a.Role, &a.GitHubAccount, &a.FinishNotice); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(created, 0)
		agents = append(agents, a)
	}
	return agents, rows.Err()
}
