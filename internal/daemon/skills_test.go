package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// skillsIncus is fakeIncus that runs skills.InstallScript for real, with the
// agent's HOME under $INCUS_FILES/<instance> and owned by whoever runs the
// test: `incus exec <instance> -T -- sh -c <script> sh <home> <owner>`.
const skillsIncus = `case "$1" in
  list) cat "$INCUS_INSTANCES_FILE" ;;
  query) echo '{"config": {}, "devices": {}}' ;;
  exec)
    if [ "$5" = "sh" ] && [ "$6" = "-c" ] && [ $# -eq 10 ]; then
      home="$INCUS_FILES/$2$9"
      mkdir -p "$home"
      sh -c "$7" sh "$home" "$(id -u):$(id -g)"
    fi ;;
esac
exit 0
`

func skillsDaemon(t *testing.T) (testDaemon, string, chan struct{}) {
	t.Helper()
	root := t.TempDir()
	files := filepath.Join(root, "files")
	d := startTestDaemon(t, root, skillsIncus, testConfig{
		env:       map[string]string{"INCUS_FILES": files},
		instances: runningAgent01,
	})
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", AI: "none",
		Branch: "agentbox/agent-01", Worktree: filepath.Join(root, "worktree"), Status: state.AgentReady, CreatedAt: time.Now()}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	synced := make(chan struct{}, 16)
	d.srv.skillsSynced = func() { synced <- struct{}{} }
	return d, files, synced
}

func waitSynced(t *testing.T, synced chan struct{}) {
	t.Helper()
	select {
	case <-synced:
	case <-time.After(20 * time.Second):
		t.Fatal("the skills were never installed")
	}
}

// TestSkillsReachRunningAgents walks the API: a skill written in the app, one
// imported from the user's Claude Code, a project's override each way, and a
// delete, checking each time what a running agent has in the folders its AI
// tool reads.
func TestSkillsReachRunningAgents(t *testing.T) {
	t.Parallel()
	d, files, synced := skillsDaemon(t)
	ctx := context.Background()
	home := filepath.Join(files, "ab-hello-stack-agent-01", "home", "dev")
	has := func(rel string) bool {
		_, err := os.Stat(filepath.Join(home, rel))
		return err == nil
	}

	sk, err := d.client.SaveSkill(ctx, "review-pr", api.SaveSkillRequest{Content: "---\nname: whatever\ndescription: Review a pull request.\n---\n\nRead the diff.\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !sk.Enabled || sk.Description != "Review a pull request." || sk.FileCount != 1 {
		t.Errorf("SaveSkill() = %+v", sk)
	}
	waitSynced(t, synced)
	for _, dir := range []string{".claude/skills", ".agents/skills"} {
		content, err := os.ReadFile(filepath.Join(home, dir, "review-pr", "SKILL.md"))
		if err != nil {
			t.Fatalf("review-pr isn't in the agent's %s: %v", dir, err)
		}
		// The folder's name wins over a SKILL.md naming another.
		if !strings.Contains(string(content), "name: review-pr\n") {
			t.Errorf("%s/review-pr/SKILL.md =\n%s", dir, content)
		}
	}

	// Importing from the user's own Claude Code.
	userHome := t.TempDir()
	writeSkill(t, filepath.Join(userHome, ".claude", "skills", "Deploy_App"), "---\nname: deploy-app\ndescription: Ship it.\n---\nSteps.\n", "scripts/run.sh")
	d.srv.cfg.SkillHomes = []string{userHome}
	found, err := d.client.ScanSkills(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "deploy-app" || found[0].Origin != "claude" || found[0].Files != 2 || found[0].Exists {
		t.Fatalf("ScanSkills(\"\") = %+v", found)
	}
	// From the project's settings: on there, off AgentBox-wide.
	imported, err := d.client.ImportSkills(ctx, api.ImportSkillsRequest{Names: []string{"deploy-app"}, Project: "hello-stack"})
	if err != nil {
		t.Fatal(err)
	}
	if len(imported) != 1 || imported[0].Enabled || imported[0].Active == nil || !*imported[0].Active || !imported[0].Overrides["hello-stack"] {
		t.Fatalf("ImportSkills() = %+v", imported)
	}
	waitSynced(t, synced)
	if !has(".claude/skills/deploy-app/scripts/run.sh") {
		t.Error("the imported skill's script didn't reach the agent")
	}

	// Off in the project: it leaves the agent, and the other stays.
	if _, err := d.client.SetSkillOverride(ctx, "hello-stack", "review-pr", "off"); err != nil {
		t.Fatal(err)
	}
	waitSynced(t, synced)
	if has(".claude/skills/review-pr") || has(".agents/skills/review-pr") || !has(".agents/skills/deploy-app") {
		t.Error("turning review-pr off in the project didn't take it out of the agent alone")
	}
	list, err := d.client.Skills(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[1].Name != "review-pr" || *list[1].Active || !list[1].Enabled {
		t.Errorf("Skills(hello-stack) = %+v", list)
	}

	// Back to AgentBox-wide (on), then deleted.
	if _, err := d.client.SetSkillOverride(ctx, "hello-stack", "review-pr", ""); err != nil {
		t.Fatal(err)
	}
	waitSynced(t, synced)
	if !has(".claude/skills/review-pr/SKILL.md") {
		t.Error("following AgentBox-wide again didn't bring review-pr back")
	}
	if err := d.client.RemoveSkill(ctx, "review-pr"); err != nil {
		t.Fatal(err)
	}
	waitSynced(t, synced)
	if has(".claude/skills/review-pr") {
		t.Error("a deleted skill stayed in the agent")
	}
	if _, err := d.client.Skill(ctx, "review-pr"); err == nil {
		t.Error("a deleted skill can still be fetched")
	}

}

func TestSkillsAPIRefusesWhatCantBeASkill(t *testing.T) {
	t.Parallel()
	d, _, _ := skillsDaemon(t)
	ctx := context.Background()
	if _, err := d.client.SaveSkill(ctx, "Bad Name", api.SaveSkillRequest{Content: "---\ndescription: x\n---\n"}); err == nil {
		t.Error("a skill named Bad Name was saved")
	}
	if _, err := d.client.SaveSkill(ctx, "no-desc", api.SaveSkillRequest{Content: "---\nname: no-desc\n---\nbody\n"}); err == nil || !strings.Contains(err.Error(), "description") {
		t.Errorf("a skill without a description: %v", err)
	}
	if _, err := d.client.SetSkillOverride(ctx, "hello-stack", "missing", "on"); err == nil {
		t.Error("an override of a skill that doesn't exist was stored")
	}
	if _, err := d.client.ImportSkills(ctx, api.ImportSkillsRequest{Source: "/nonexistent/skills"}); err == nil {
		t.Error("importing from a folder that isn't there worked")
	}
}

func writeSkill(t *testing.T, dir, skillMD string, extra ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range extra {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("echo hi\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
