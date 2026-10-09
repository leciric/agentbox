package daemon

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/skills"
)

// approvals stands in for the user: it answers each request for approval with
// the next of its answers, and keeps what it was asked.
type approvals struct {
	mu      sync.Mutex
	answers []bool
	asked   []api.ChatPermission
}

func (a *approvals) approve(_ context.Context, project string, req api.ChatPermission) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if project != "hello-stack" {
		panic("asked in " + project)
	}
	a.asked = append(a.asked, req)
	if len(a.answers) == 0 {
		panic("asked more often than the test expected: " + req.Title)
	}
	ok := a.answers[0]
	a.answers = a.answers[1:]
	return ok, nil
}

func (a *approvals) will(answers ...bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.answers, a.asked = answers, nil
}

func (a *approvals) titles() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, req := range a.asked {
		out = append(out, req.Title)
	}
	return out
}

func skillText(description, body string) string {
	return "---\nname: x\ndescription: " + description + "\n---\n\n" + body + "\n"
}

// TestTheLeadsSkillChangesWaitForTheUser walks a lead's skill tools: what it
// may do alone, and what the user approves — or refuses, which changes nothing.
func TestTheLeadsSkillChangesWaitForTheUser(t *testing.T) {
	t.Parallel()
	d, _, synced := skillsDaemon(t)
	go func() {
		for range synced {
		}
	}()
	user := &approvals{}
	d.srv.approve = user.approve
	ctx := context.Background()
	if err := d.srv.serveLeadAPI("hello-stack"); err != nil {
		t.Fatal(err)
	}
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	stored := func(name string) string {
		t.Helper()
		f, err := d.srv.store.SkillFile(ctx, name, skills.File)
		if err != nil {
			t.Fatal(err)
		}
		return string(f.Content)
	}

	// Creating: no card. Project-scoped is on here alone; everywhere is on
	// AgentBox-wide.
	user.will()
	sk, err := lead.CreateProjectSkill(ctx, api.LeadNewSkillRequest{Name: "release", Content: skillText("Cut a release.", "Run the script.")})
	if err != nil {
		t.Fatal(err)
	}
	if sk.Enabled || !sk.Overrides["hello-stack"] || sk.Active == nil || !*sk.Active {
		t.Errorf("a project's new skill is %+v", sk)
	}
	sk, err = lead.CreateProjectSkill(ctx, api.LeadNewSkillRequest{Name: "commits", Content: skillText("Write commits.", "Conventional."), Everywhere: true})
	if err != nil || !sk.Enabled || len(sk.Overrides) != 0 {
		t.Errorf("an AgentBox-wide new skill is %+v, %v", sk, err)
	}
	if _, err := lead.CreateProjectSkill(ctx, api.LeadNewSkillRequest{Name: "release", Content: skillText("Other.", "Other.")}); err == nil || !strings.Contains(err.Error(), "edit_skill") {
		t.Errorf("creating over an existing skill: %v", err)
	}
	if list, err := lead.ProjectSkills(ctx); err != nil || len(list) != 2 {
		t.Errorf("ProjectSkills() = %+v, %v", list, err)
	}
	if detail, err := lead.ProjectSkill(ctx, "release"); err != nil || !strings.Contains(detail.Files[0].Content, "Run the script.") || detail.Active == nil || !*detail.Active {
		t.Errorf("ProjectSkill() = %+v, %v", detail, err)
	}
	if got := user.titles(); len(got) != 0 {
		t.Errorf("creating asked the user %v", got)
	}

	// Editing: a refusal changes nothing and says so; an approval saves what
	// the card showed.
	edited := skillText("Cut a release.", "Run the script, then tag.")
	user.will(false)
	if _, err := lead.EditProjectSkill(ctx, "release", edited); err == nil || !strings.Contains(err.Error(), "the user refused") || !strings.Contains(err.Error(), "edit the skill release") {
		t.Errorf("a refused edit: %v", err)
	}
	if strings.Contains(stored("release"), "then tag") {
		t.Error("a refused edit was saved")
	}
	user.will(true)
	if _, err := lead.EditProjectSkill(ctx, "release", edited); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored("release"), "then tag") {
		t.Error("an approved edit wasn't saved")
	}
	card := user.asked[0]
	if card.Approval != "skill" || len(card.Diffs) != 1 || !strings.Contains(card.Diffs[0].OldText, "Run the script.\n") ||
		card.Diffs[0].NewText != stored("release") || card.Diffs[0].Path != "release/SKILL.md" {
		t.Errorf("the edit's card is %+v", card)
	}
	// The same text again is no change, and asks nothing.
	user.will()
	if _, err := lead.EditProjectSkill(ctx, "release", edited); err != nil {
		t.Errorf("an edit that changes nothing: %v", err)
	}
	// A SKILL.md that can't be saved is refused before the user is asked.
	if _, err := lead.EditProjectSkill(ctx, "release", "no front matter"); err == nil {
		t.Error("a broken SKILL.md was accepted")
	}
	if got := user.titles(); len(got) != 0 {
		t.Errorf("asked the user %v", got)
	}

	// Turning on is the lead's; turning off where it was on is the user's.
	user.will()
	if sk, err := lead.SwitchProjectSkill(ctx, "commits", api.LeadSkillSwitchRequest{Override: "on"}); err != nil || !*sk.Active {
		t.Errorf("turning a skill on here: %+v, %v", sk, err)
	}
	if sk, err := lead.SwitchProjectSkill(ctx, "release", api.LeadSkillSwitchRequest{Override: "on", Everywhere: true}); err != nil || !sk.Enabled {
		t.Errorf("turning a skill on everywhere: %+v, %v", sk, err)
	}
	// Following AgentBox's switch, which is on, takes nothing away.
	if sk, err := lead.SwitchProjectSkill(ctx, "commits", api.LeadSkillSwitchRequest{}); err != nil || !*sk.Active || len(sk.Overrides) != 0 {
		t.Errorf("following AgentBox's switch: %+v, %v", sk, err)
	}
	if got := user.titles(); len(got) != 0 {
		t.Errorf("turning skills on asked the user %v", got)
	}
	user.will(false, true)
	if _, err := lead.SwitchProjectSkill(ctx, "commits", api.LeadSkillSwitchRequest{Override: "off"}); err == nil || !strings.Contains(err.Error(), "the user refused") {
		t.Errorf("a refused switch: %v", err)
	}
	if sk, _ := d.srv.store.Skill(ctx, "commits"); !sk.EnabledFor("hello-stack") {
		t.Error("a refused switch turned the skill off")
	}
	if sk, err := lead.SwitchProjectSkill(ctx, "commits", api.LeadSkillSwitchRequest{Override: "off"}); err != nil || *sk.Active {
		t.Errorf("an approved switch: %+v, %v", sk, err)
	}
	user.will(true)
	if sk, err := lead.SwitchProjectSkill(ctx, "release", api.LeadSkillSwitchRequest{Override: "off", Everywhere: true}); err != nil || sk.Enabled {
		t.Errorf("an approved switch everywhere: %+v, %v", sk, err)
	}
	want := []string{"Turn the skill release off AgentBox-wide"}
	if got := user.titles(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("asked %v, want %v", got, want)
	}

	// Deleting: refused, it stays; approved, it goes.
	user.will(false, true)
	if err := lead.RemoveProjectSkill(ctx, "release"); err == nil || !strings.Contains(err.Error(), "the user refused") {
		t.Errorf("a refused delete: %v", err)
	}
	if _, err := d.srv.store.Skill(ctx, "release"); err != nil {
		t.Errorf("a refused delete removed the skill: %v", err)
	}
	if err := lead.RemoveProjectSkill(ctx, "release"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.store.Skill(ctx, "release"); err == nil {
		t.Error("an approved delete left the skill")
	}
	if card := user.asked[0]; card.Title != "Delete the skill release" || len(card.Diffs) != 1 || card.Diffs[0].NewText != "" {
		t.Errorf("the delete's card is %+v", card)
	}
}

// An agent's API has no skill routes: agents don't manage skills.
func TestAgentsCantManageSkills(t *testing.T) {
	t.Parallel()
	d, _, _ := skillsDaemon(t)
	h := d.srv.inAgentRoutes("ab-hello-stack-agent-01")
	for _, route := range []string{"GET /v1/skills", "PUT /v1/skills/x", "GET /v1/project/skills", "POST /v1/project/skills", "DELETE /v1/project/skills/x"} {
		method, path, _ := strings.Cut(route, " ")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("{}")))
		if rec.Code < 400 {
			t.Errorf("an agent's %s answered %d", route, rec.Code)
		}
	}
}
