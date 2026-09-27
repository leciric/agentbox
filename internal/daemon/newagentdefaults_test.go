package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// launched is what a chat was started with: the model written into Claude
// Code's settings.json and the window it compacts at.
type launched struct {
	model  string
	window int64
}

// startWithAgentDefaults is a daemon whose Settings → Agents asks for Opus at
// its 1M window and medium effort, on an account where plain "opus" reported
// a 200k window and "opus[1m]" a 1M one, with no "[1m]" entry on the model
// menu: the settings a real installation had when every new agent still came
// out on opus at 200k. Its chats are caught as they launch.
func startWithAgentDefaults(t *testing.T) (testDaemon, chan launched) {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), readyOneAgentIncus)
	ctx := context.Background()
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveClaudeToken("", "sk-ant-oat01-x"); err != nil {
		t.Fatal(err)
	}
	addProjectAllowingEvery(t, d, d.fixtureRepo(t, "hello-stack"))
	for key, value := range map[string]string{
		state.SettingClaudeModelWindows:  `{"opus":200000,"opus[1m]":1000000,"sonnet":1000000}`,
		state.SettingClaudeModelChoices:  `[{"value":"default","name":"Default"},{"value":"opus","name":"Opus"},{"value":"sonnet","name":"Sonnet"},{"value":"haiku","name":"Haiku"}]`,
		state.SettingClaudeEffortChoices: `[{"value":"low","name":"Low"},{"value":"medium","name":"Medium"},{"value":"high","name":"High"}]`,
	} {
		if err := d.srv.store.SetSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	model, window, effort := "opus", "1000000", "medium"
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{
		DefaultClaudeModel: &model, DefaultAgentContextWindow: &window, DefaultClaudeEffort: &effort,
		DefaultLeadModel: &model, DefaultLeadContextWindow: &window,
	}); err != nil {
		t.Fatalf("choosing opus at 1M for new agents and the lead: %v", err)
	}
	starts := make(chan launched, 4)
	d.srv.chat.Prepare = func(_ context.Context, a state.Agent, model string, window int64) error {
		if a.IsLead() {
			model = "lead:" + model
		}
		starts <- launched{model, window}
		return nil
	}
	d.srv.chat.Launch = func(context.Context, state.Agent, func(string)) (*chat.Process, error) {
		return nil, errors.New("this test starts no AI tool")
	}
	return d, starts
}

// createdWith follows a create job to its end and checks what the new agent
// keeps for its chat and what that chat was started on.
func createdWith(t *testing.T, d testDaemon, starts chan launched, job api.Job, err error, want launched) {
	t.Helper()
	ctx := context.Background()
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent to be created", func() bool {
		j, err := d.client.Job(ctx, job.ID)
		return err == nil && j.Done()
	})
	if j, _ := d.client.Job(ctx, job.ID); j.Status != api.JobSucceeded {
		t.Fatalf("create = %s: %s", j.Status, j.Error)
	}
	stored, err := d.srv.store.Chat(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Options["model"] != "opus" || stored.Options[state.ChatOptionContextWindow] != "1000000" || stored.Options["effort"] != "medium" {
		t.Errorf("the new agent keeps %+v, want opus at 1000000 on medium effort", stored.Options)
	}
	select {
	case got := <-starts:
		if got != want {
			t.Errorf("its chat started on %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("its chat never started")
	}
}

// TestAgentDefaultsReachNewAgents is the bug where the model and
// context window chosen in Settings → Agents didn't reach new agents, through
// each way an agent is made: the app's dialog and the command line, which
// leave the model and window out unless one is picked, and the lead's
// create_agent over its own socket.
func TestAgentDefaultsReachNewAgents(t *testing.T) {
	t.Parallel()
	// Opus's 1M window is its "[1m]" variant on this account.
	want := launched{"opus[1m]", 1_000_000}

	t.Run("app", func(t *testing.T) {
		t.Parallel()
		d, starts := startWithAgentDefaults(t)
		job, err := d.client.CreateAgent(context.Background(), api.CreateAgentRequest{
			Project: "hello-stack", Title: "Reminders page", AI: "claude", Interface: "chat", Task: "add it",
		})
		createdWith(t, d, starts, job, err, want)
	})

	t.Run("cli", func(t *testing.T) {
		t.Parallel()
		d, starts := startWithAgentDefaults(t)
		model, window := "opus", "1m"
		job, err := d.client.CreateAgent(context.Background(), api.CreateAgentRequest{
			Project: "hello-stack", Title: "Reminders page", Task: "add it", Model: &model, ContextWindow: &window,
		})
		createdWith(t, d, starts, job, err, want)
	})

	t.Run("lead", func(t *testing.T) {
		t.Parallel()
		d, starts := startWithAgentDefaults(t)
		lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
		job, err := lead.CreateProjectAgent(context.Background(), api.CreateAgentRequest{Title: "Reminders page", Task: "add it"})
		createdWith(t, d, starts, job, err, want)
	})
}

// TestLeadDefaultsReachTheLead: the lead, which keeps no model or window of
// its own until its composer picks one, starts on Settings → Lead's.
func TestLeadDefaultsReachTheLead(t *testing.T) {
	t.Parallel()
	d, starts := startWithAgentDefaults(t)
	if _, err := d.client.SendChat(context.Background(), "hello-stack", "hi"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-starts:
		if want := (launched{"lead:opus[1m]", 1_000_000}); got != want {
			t.Errorf("the lead started on %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the lead's chat never started")
	}
}

// TestAgentDefaultsOffer1M: the settings the app's
// dialog and Settings page read say Opus has a 1M window on that account,
// so the dialog offers it and Settings can keep it.
func TestAgentDefaultsOffer1M(t *testing.T) {
	t.Parallel()
	d, _ := startWithAgentDefaults(t)
	settings, err := d.client.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.ClaudeContextWindows["opus"]; len(got) != 2 || got[1] != state.ClaudeFullWindow {
		t.Errorf("opus offers %v, want 200k and 1M", got)
	}
	if settings.DefaultAgentContextWindow != "1000000" {
		t.Errorf("DefaultAgentContextWindow = %q, want 1000000", settings.DefaultAgentContextWindow)
	}
}

// TestLeadCap: what the lead asks create_agent for is
// checked against Settings → Agents before anything starts. Not enforced, the
// model and window chosen there are a ceiling: a cheaper model or a shorter
// window goes through, a dearer model or a longer window is refused. Enforced,
// they are the only ones. The app and the command line aren't held to either.
func TestLeadCap(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name          string
		settings      api.UpdateSettingsRequest
		model, window *string
		refused       string // what the refusal says; "" for none
	}{
		{"cheaper", api.UpdateSettingsRequest{}, str("sonnet"), nil, ""},
		{"haiku", api.UpdateSettingsRequest{}, str("haiku"), str("200k"), ""},
		{"at cap", api.UpdateSettingsRequest{}, str("opus"), str("1m"), ""},
		{"model above", api.UpdateSettingsRequest{DefaultClaudeModel: str("sonnet")}, str("opus"), nil,
			"opus is above sonnet, the model chosen in Settings → Agents"},
		{"window above", api.UpdateSettingsRequest{DefaultAgentContextWindow: str("200k")}, nil, str("1m"),
			"a 1M window is above 200k"},
		{"enf model", api.UpdateSettingsRequest{EnforceAgentDefaults: new(true)}, str("sonnet"), nil,
			"Settings → Agents enforces opus at 1M"},
		{"enf window", api.UpdateSettingsRequest{EnforceAgentDefaults: new(true)}, nil, str("200k"),
			"Settings → Agents enforces opus at 1M"},
		{"enf same", api.UpdateSettingsRequest{EnforceAgentDefaults: new(true)}, str("opus"), str("1m"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, _ := startWithAgentDefaults(t)
			ctx := context.Background()
			if _, err := d.client.UpdateSettings(ctx, tc.settings); err != nil {
				t.Fatal(err)
			}
			lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
			job, err := lead.CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders page", Task: "add it", Model: tc.model, ContextWindow: tc.window})
			if tc.refused != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refused) {
					t.Fatalf("create_agent = %v, want it refused with %q", err, tc.refused)
				}
				return
			}
			if err != nil {
				t.Fatalf("create_agent was refused: %v", err)
			}
			waitFor(t, "the agent to be created", func() bool {
				j, err := d.client.Job(ctx, job.ID)
				return err == nil && j.Done()
			})
		})
	}

	t.Run("user", func(t *testing.T) {
		t.Parallel()
		d, _ := startWithAgentDefaults(t)
		ctx := context.Background()
		if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{EnforceAgentDefaults: new(true)}); err != nil {
			t.Fatal(err)
		}
		model := "sonnet"
		job, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", Title: "Reminders page", Model: &model})
		if err != nil {
			t.Fatalf("the app's dialog was held to the lead's rule: %v", err)
		}
		waitFor(t, "the agent to be created", func() bool {
			j, err := d.client.Job(ctx, job.ID)
			return err == nil && j.Done()
		})
	})
}

func TestEnforcingTheAgentDefaultsIsASetting(t *testing.T) {
	t.Parallel()
	d, _ := startWithAgentDefaults(t)
	ctx := context.Background()
	for _, want := range []bool{true, false} {
		if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{EnforceAgentDefaults: &want}); err != nil {
			t.Fatal(err)
		}
		if got, err := d.client.Settings(ctx); err != nil || got.EnforceAgentDefaults != want {
			t.Errorf("EnforceAgentDefaults = %v, %v; want %v", got.EnforceAgentDefaults, err, want)
		}
	}
}
