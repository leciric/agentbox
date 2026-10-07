package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/hostos"
	"agentbox/internal/memory"
	"agentbox/internal/state"
	"agentbox/internal/update"
)

// Anonymous usage events: what a count can't say, like how long a turn took
// or how many tokens it spent, kept in state.db as they happen and sent with
// the update check (sendUsage) to agentbox.linting.dev's /api/v1/events. Each
// is a name and fixed fields, every one a number, a yes or no, or a value from
// a fixed list here: never a prompt, a path, a name, a branch, or anything
// typed. The server refuses an event with any other name or field
// (agentbox-landing's functions/api/v1/events.ts), so the two lists change
// together. The same rules as the counts decide whether one is kept at all
// (usageStatsOn), and turning the stats off forgets those not sent.

const (
	eventTurn          = "turn.completed"
	eventHeartbeat     = "heartbeat"
	eventAgentFinished = "agent.finished"
	eventError         = "error"
	eventSetup         = "setup"
)

// turnEvent is a turn.completed's fields.
type turnEvent struct {
	Tool                string `json:"tool"`
	Model               string `json:"model"`
	Effort              string `json:"effort"`
	Context             string `json:"context"`
	Mode                string `json:"mode"`
	Role                string `json:"role"`    // lead or agent
	Outcome             string `json:"outcome"` // done, cancelled or error
	DurationMS          int64  `json:"duration_ms"`
	InputTokens         int64  `json:"input_tokens"`
	CachedTokens        int64  `json:"cached_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	ReasoningTokens     int64  `json:"reasoning_tokens"`
	Subagents           bool   `json:"subagents"`
}

// heartbeatEvent is the day's heartbeat: the shape of the installation.
type heartbeatEvent struct {
	Projects       int    `json:"projects"`
	Agents         int    `json:"agents"`
	PeakAgents     int    `json:"peak_agents"`
	FrontEnd       string `json:"front_end"`
	VMMemory       string `json:"vm_memory"`
	Language       string `json:"language"`
	UpdateChannel  string `json:"update_channel"`
	Queue          bool   `json:"queue"`
	SharedBudget   bool   `json:"shared_budget"`
	Nesting        bool   `json:"nesting"`
	Connectors     int    `json:"connectors"`
	ClaudeAccounts int    `json:"claude_accounts"`
	GitHubAccounts int    `json:"github_accounts"`
}

// agentFinishedEvent is an agent's life, as it is destroyed or retired.
type agentFinishedEvent struct {
	Tool     string `json:"tool"`
	How      string `json:"how"` // destroyed or retired
	Lifetime string `json:"lifetime"`
	Queued   string `json:"queued"`
	PROpened bool   `json:"pr_opened"`
	PRMerged bool   `json:"pr_merged"`
}

// errorEvent is something that failed, by a fixed code.
type errorEvent struct {
	Code string `json:"code"`
	Tool string `json:"tool"`
}

// setupEvent is a step of a new installation's setup, the first time it
// happens, with how long after the setup began.
type setupEvent struct {
	Step  string `json:"step"`
	Since string `json:"since"`
}

// The error codes. vm_start_failed is the front end's, which runs outside the
// VM this daemon is in: the server takes it, and nothing here sends it yet.
const (
	errCreateFailed     = "create_failed"
	errStartFailed      = "start_failed"
	errImageBuildFailed = "image_build_failed"
	errAdapterCrashed   = "adapter_crashed"
	errAuthFailed       = "auth_failed"
	errPRMergeFailed    = "pr_merge_failed"
)

// The setup steps, in the order a new installation usually reaches them.
const (
	setupStarted      = "setup_started"
	setupImageBuilt   = "image_built"
	setupFirstProject = "first_project"
	setupFirstAgent   = "first_agent"
	setupFirstPR      = "first_pr"
)

// The settings the events keep between days: when the setup began, which of
// its steps were sent, the day the last heartbeat was for, the most agents
// there were at once and on which day, and how long each agent waited in the
// queue (by its id) until it finishes.
const (
	settingUsageSetupAt     = "usage_setup_at"
	settingUsageSetupSteps  = "usage_setup_steps"
	settingUsageHeartbeat   = "usage_heartbeat_day"
	settingUsagePeak        = "usage_peak"
	settingUsageQueuedAgent = "usage_queued."
)

// recordEvent keeps one event, when the stats are on. Like a count, one that
// fails is dropped.
func (s *Server) recordEvent(name string, props any) {
	ctx := s.background()
	if !s.usageStatsOn(ctx) {
		return
	}
	raw, err := json.Marshal(props)
	if err != nil {
		return
	}
	e := state.UsageEvent{ID: uuid.NewString(), Day: usageDay(time.Now()), Name: name, Props: raw}
	if err := s.store.AddUsageEvent(ctx, e); err != nil {
		s.logf("recording %s: %v", name, err)
	}
}

// recordTurn is the chat's TurnFinished. It counts the turn's model, effort
// and window too.
func (s *Server) recordTurn(a state.Agent, st chat.TurnStats) {
	if st.State == "completed" {
		s.countModelUse(a.AI, st.Model, st.Effort, st.Window, api.FeatureTurnModel, api.FeatureTurnEffort, api.FeatureTurnContext)
	}
	model, effort := usageModelEffort(st.Model, st.Effort)
	e := turnEvent{
		Tool: usageTool(a.AI), Model: model, Effort: effort, Context: usageWindow(st.Window), Mode: usageMode(st.Mode),
		Role: "agent", Outcome: usageOutcome(st.State), DurationMS: min(st.Duration.Milliseconds(), maxTurnMS),
		InputTokens: st.Input, CachedTokens: st.Cached, CacheCreationTokens: st.CacheCreation,
		OutputTokens: st.Output, ReasoningTokens: st.Reasoning, Subagents: st.Subagents,
	}
	if a.IsLead() {
		e.Role = "lead"
	}
	s.recordEvent(eventTurn, e)
}

// maxTurnMS is the longest turn the server takes, a week.
const maxTurnMS = int64(7 * 24 * time.Hour / time.Millisecond)

func usageOutcome(state string) string {
	switch state {
	case "completed":
		return "done"
	case "cancelled":
		return "cancelled"
	}
	return "error"
}

// usageModes are the permission modes counted by name, keyed by the mode's id
// without "-" or "_", lowercase: Claude Code's, Codex's and OpenCode's.
var usageModes = map[string]string{
	"default": "default", "build": "default", "acceptedits": "accept_edits", "plan": "plan",
	"bypasspermissions": "bypass", "bypass": "bypass", "dontask": "dont_ask",
	"readonly": "read_only", "auto": "auto", "fullaccess": "full_access",
}

func usageMode(mode string) string {
	m := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.TrimSpace(mode)))
	if m == "" {
		return usageDefault
	}
	if known, ok := usageModes[m]; ok {
		return known
	}
	return usageOther
}

// usageSpan is the key a length of time counts under.
func usageSpan(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "lt_1m"
	case d < 5*time.Minute:
		return "1m_5m"
	case d < 30*time.Minute:
		return "5m_30m"
	case d < 2*time.Hour:
		return "30m_2h"
	case d < 24*time.Hour:
		return "2h_1d"
	case d < 7*24*time.Hour:
		return "1d_1w"
	}
	return "gt_1w"
}

// recordError keeps an error event, with the agent's tool when there is one.
func (s *Server) recordError(code, ai string) {
	tool := "none"
	if ai == "claude" || ai == "codex" || ai == "opencode" {
		tool = ai
	}
	s.recordEvent(eventError, errorEvent{Code: code, Tool: tool})
}

// noteAgentQueued keeps how long a queued agent waited, for its
// agent.finished.
func (s *Server) noteAgentQueued(ctx context.Context, a state.Agent, waited time.Duration) {
	if a.ID == "" || !s.usageStatsOn(ctx) {
		return
	}
	_ = s.store.SetSetting(ctx, settingUsageQueuedAgent+a.ID, strconv.FormatInt(int64(waited/time.Second), 10))
}

// recordAgentFinished keeps an agent.finished for a, which was just destroyed
// or retired (how). Whether it opened a pull request, and whether one was
// merged, is what the project's memory recorded about it.
func (s *Server) recordAgentFinished(ctx context.Context, a state.Agent, how string) {
	if a.IsLead() {
		return
	}
	queued := "none"
	if a.ID != "" {
		key := settingUsageQueuedAgent + a.ID
		if raw, err := s.store.Setting(ctx, key); err == nil && raw != "" {
			if secs, err := strconv.ParseInt(raw, 10, 64); err == nil {
				queued = usageSpan(time.Duration(secs) * time.Second)
			}
			_ = s.store.DeleteSetting(ctx, key)
		}
	}
	if !s.usageStatsOn(ctx) {
		return
	}
	e := agentFinishedEvent{Tool: "none", How: how, Lifetime: usageSpan(time.Since(a.CreatedAt)), Queued: queued}
	if a.AI == "claude" || a.AI == "codex" || a.AI == "opencode" {
		e.Tool = a.AI
	}
	events, err := s.memory().Events(ctx, a.Project, memory.EventFilter{
		Agent: a.Name, Types: []string{"agent_finished", "pr_merged"}, Since: a.CreatedAt, Limit: 200,
	})
	if err == nil {
		for _, ev := range events {
			switch ev.Type {
			case "pr_merged":
				e.PROpened, e.PRMerged = true, true
			case "agent_finished":
				var payload struct {
					PR *struct{} `json:"pr"`
				}
				if json.Unmarshal(ev.Payload, &payload) == nil && payload.PR != nil {
					e.PROpened = true
				}
			}
		}
	}
	s.recordEvent(eventAgentFinished, e)
}

// beginSetupFunnel decides, once, whether this installation's setup is still
// ahead of it: one with no projects yet starts it now, and one already in use
// never sends a step.
func (s *Server) beginSetupFunnel(ctx context.Context) {
	if at, err := s.store.Setting(ctx, settingUsageSetupAt); err != nil || at != "" {
		return
	}
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return
	}
	if len(projects) > 0 {
		_ = s.store.SetSetting(ctx, settingUsageSetupAt, "existing")
		return
	}
	_ = s.store.SetSetting(ctx, settingUsageSetupAt, strconv.FormatInt(time.Now().Unix(), 10))
	s.setupStep(ctx, setupStarted)
}

// setupStep keeps a setup event the first time this installation reaches
// step, with how long after its setup began.
func (s *Server) setupStep(ctx context.Context, step string) {
	if !s.usageStatsOn(ctx) {
		return
	}
	at, err := s.store.Setting(ctx, settingUsageSetupAt)
	if err != nil {
		return
	}
	began, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return // never begun here, or "existing"
	}
	steps, err := s.store.Setting(ctx, settingUsageSetupSteps)
	if err != nil {
		return
	}
	done := strings.Split(steps, ",")
	if slices.Contains(done, step) {
		return
	}
	if err := s.store.SetSetting(ctx, settingUsageSetupSteps, strings.Trim(steps+","+step, ",")); err != nil {
		return
	}
	s.recordEvent(eventSetup, setupEvent{Step: step, Since: usageSpan(time.Since(time.Unix(began, 0)))})
}

// usagePeak is the most agents there were at once: the day it was for, and
// how many.
type usagePeak struct {
	Day  string `json:"day"`
	Peak int    `json:"peak"`
	// Prev is the day before's, so a heartbeat early in a day still has it.
	Prev int `json:"prev"`
}

// notePeakAgents keeps how many agents there are now, if that is the most
// there have been today.
func (s *Server) notePeakAgents(ctx context.Context) {
	if !s.usageStatsOn(ctx) {
		return
	}
	n, err := s.workerCount(ctx)
	if err != nil {
		return
	}
	p := s.readPeak(ctx)
	if n <= p.Peak {
		return
	}
	p.Peak = n
	if raw, err := json.Marshal(p); err == nil {
		_ = s.store.SetSetting(ctx, settingUsagePeak, string(raw))
	}
}

// readPeak is the peak kept for today, moved on from an earlier day.
func (s *Server) readPeak(ctx context.Context) usagePeak {
	var p usagePeak
	if raw, err := s.store.Setting(ctx, settingUsagePeak); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}
	today := usageDay(time.Now())
	switch {
	case p.Day == today:
	case p.Day == usageDay(time.Now().AddDate(0, 0, -1)):
		p = usagePeak{Day: today, Prev: p.Peak}
	default:
		p = usagePeak{Day: today}
	}
	return p
}

// workerCount is how many agents there are, queued or not, the leads aside.
func (s *Server) workerCount(ctx context.Context) (int, error) {
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range agents {
		if !a.IsLead() && a.Status != state.AgentQueued {
			n++
		}
	}
	return n, nil
}

// recordHeartbeat keeps the day's heartbeat, once a day.
func (s *Server) recordHeartbeat(ctx context.Context) {
	today := usageDay(time.Now())
	if last, err := s.store.Setting(ctx, settingUsageHeartbeat); err != nil || last == today {
		return
	}
	hb, err := s.heartbeat(ctx)
	if err != nil {
		return
	}
	if err := s.store.SetSetting(ctx, settingUsageHeartbeat, today); err != nil {
		return
	}
	s.recordEvent(eventHeartbeat, hb)
}

func (s *Server) heartbeat(ctx context.Context) (heartbeatEvent, error) {
	var hb heartbeatEvent
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return hb, err
	}
	hb.Projects = len(projects)
	if hb.Agents, err = s.workerCount(ctx); err != nil {
		return hb, err
	}
	p := s.readPeak(ctx)
	hb.PeakAgents = max(p.Peak, p.Prev, hb.Agents)
	hb.FrontEnd = usageFrontEnd(hostos.OS())
	hb.VMMemory = usageMemory(memTotal())
	language, _ := s.store.Setting(ctx, state.SettingLanguage)
	hb.Language = usageLanguage(language)
	channel, _ := s.updateChannel(ctx)
	hb.UpdateChannel = usageOther
	if channel == update.ChannelStable || channel == update.ChannelNightly {
		hb.UpdateChannel = channel
	}
	hb.Queue, _ = s.store.FlagOn(ctx, state.SettingAgentQueue)
	for _, p := range projects {
		hb.Nesting = hb.Nesting || p.Nesting
		// Auto slots are shared out of the VM's memory; a fixed number is
		// the project's own.
		hb.SharedBudget = hb.SharedBudget || (hb.Queue && p.Slots == 0)
	}
	if connectors, err := s.store.AllConnectors(ctx); err == nil {
		hb.Connectors = len(connectors)
	}
	creds := s.manager(nil).Creds
	if accounts, err := creds.ClaudeAccounts(); err == nil {
		hb.ClaudeAccounts = len(accounts)
	}
	if accounts, err := creds.GitHubAccounts(); err == nil {
		hb.GitHubAccounts = len(accounts)
	}
	return hb, nil
}

// usageFrontEnd is how AgentBox runs, from the OS of the machine its VM is
// on. A Mac's Lima VM looks the same from inside under krunkit and vz, so it
// is "lima" either way.
func usageFrontEnd(os string) string {
	switch os {
	case "":
		return "host"
	case hostos.Linux:
		return "chv"
	case hostos.Windows:
		return "wsl"
	case "darwin", "mac", "macos":
		return "lima"
	}
	return usageOther
}

// usageMemory is the key the memory this runs in counts under.
func usageMemory(bytes int64) string {
	const g = 1 << 30
	switch {
	case bytes <= 0:
		return "unknown"
	case bytes < 8*g:
		return "lt_8g"
	case bytes < 16*g:
		return "8_16g"
	case bytes < 32*g:
		return "16_32g"
	case bytes < 64*g:
		return "32_64g"
	}
	return "64g_plus"
}

// memTotal is the memory this machine has, in bytes, 0 when unknown: in
// AgentBox's VM, the VM's.
func memTotal() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "MemTotal:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			if err != nil {
				return 0
			}
			return kb << 10
		}
	}
	return 0
}

func usageLanguage(language string) string {
	switch strings.ToLower(language) {
	case "":
		return "system"
	case "en-us", "en":
		return "en_us"
	case "pt-br", "pt":
		return "pt_br"
	}
	return usageOther
}

// pendingEvents is what the next report sends, oldest first, and the seq of
// the last.
func (s *Server) pendingEvents(ctx context.Context) ([]update.UsageEvent, int64, error) {
	rows, err := s.store.UsageEvents(ctx, update.MaxEventsPerReport)
	if err != nil || len(rows) == 0 {
		return nil, 0, err
	}
	out := make([]update.UsageEvent, len(rows))
	for i, r := range rows {
		out[i] = update.UsageEvent{ID: r.ID, Day: r.Day, Name: r.Name, Props: r.Props}
	}
	return out, rows[len(rows)-1].Seq, nil
}

// sendEvents sends what events are waiting, a report at a time, and forgets
// each once the server has it, or refused it: a server that refuses an event
// will refuse it again. Anything else stops here and is tried with the next
// check.
func (s *Server) sendEvents(ctx context.Context, req update.Request) {
	_ = s.store.ForgetUsageEvents(ctx, 0, usageDay(time.Now().AddDate(0, 0, -update.MaxUsageDays)))
	for range 20 {
		events, last, err := s.pendingEvents(ctx)
		if err != nil || len(events) == 0 {
			return
		}
		if err := update.SendEvents(ctx, s.cfg.UpdateURL, req, events); err != nil && !errors.Is(err, update.ErrRefused) {
			return
		}
		if err := s.store.ForgetUsageEvents(ctx, last, ""); err != nil {
			return
		}
	}
}
