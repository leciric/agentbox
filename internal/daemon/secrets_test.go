package daemon

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// recordingIncus is fakeIncus plus the files AgentBox writes into agents, kept
// under $INCUS_FILES so a test can read what really arrived in a machine.
// incus.WriteFile runs `incus exec <instance> -T -- sh -c <script> sh <path>
// <uid:gid> <mode>` with the content on stdin.
const recordingIncus = `case "$1" in
  list) echo "${INCUS_INSTANCES:-[]}" ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
  exec)
    if [ "$5" = "sh" ] && [ "$6" = "-c" ] && [ $# -eq 11 ]; then
      dest="$INCUS_FILES/$2$9"
      mkdir -p "$(dirname "$dest")"
      cat > "$dest"
      chmod "${11}" "$dest"
    fi ;;
  delete) echo "$*" >> "$INCUS_LOG" ;;
esac
exit 0
`

// secretsDaemon starts a daemon with a project, one ready agent whose machine
// is running, and a place where files written into that machine land.
func secretsDaemon(t *testing.T) (testDaemon, string) {
	t.Helper()
	root := t.TempDir()
	files := filepath.Join(root, "files")
	t.Setenv("INCUS_FILES", files)
	t.Setenv("INCUS_INSTANCES", `[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]`)
	d := startTestDaemon(t, root, recordingIncus)
	ctx := context.Background()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01", AI: "none",
		Branch: "agentbox/agent-01", Worktree: filepath.Join(root, "worktree"), Status: state.AgentReady, CreatedAt: time.Now()}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	return d, files
}

func agentFile(t *testing.T, files, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(files, "ab-hello-stack-agent-01", path))
	if err != nil {
		t.Fatalf("%s was not written into the agent: %v", path, err)
	}
	return string(content)
}

// TestSecretsAPI walks the API the app and the command line both use: a
// project's secrets, an agent's own, what a list says, and what a list must
// never say.
func TestSecretsAPI(t *testing.T) {
	d, files := secretsDaemon(t)
	ctx := context.Background()

	if secrets, err := d.client.Secrets(ctx, "hello-stack"); err != nil || len(secrets) != 0 {
		t.Fatalf("Secrets() on a fresh project = %+v, %v; want none", secrets, err)
	}

	stored, err := d.client.SetSecret(ctx, "hello-stack", "OPENAI_API_KEY", "sk-project-value")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "OPENAI_API_KEY" || stored.Scope != "project" || stored.Project != "hello-stack" {
		t.Errorf("SetSecret() = %+v", stored)
	}
	if stored.UpdatedAt.IsZero() {
		t.Error("SetSecret() reported no update time")
	}
	// Where it is delivered, which is what the interface shows next to it.
	if len(stored.Agents) != 1 || stored.Agents[0] != "hello-stack/agent-01" {
		t.Errorf("SetSecret().Agents = %v; want the project's one agent", stored.Agents)
	}

	// It reached the agent, and nothing else did.
	file := agentFile(t, files, "/home/dev/.config/agentbox/secrets.env")
	if !strings.Contains(file, "export OPENAI_API_KEY='sk-project-value'") {
		t.Errorf("the secret didn't reach the running agent:\n%s", file)
	}

	// The list carries names, scopes and times — and no value, in any field.
	secrets, err := d.client.Secrets(ctx, "hello-stack")
	if err != nil || len(secrets) != 1 {
		t.Fatalf("Secrets() = %+v, %v", secrets, err)
	}
	if body := rawBody(t, d, "GET", "/v1/projects/hello-stack/secrets"); strings.Contains(body, "sk-project-value") {
		t.Errorf("a value came back in a GET response:\n%s", body)
	}

	// An agent's own secret, and the agent's list showing both scopes.
	own, err := d.client.SetSecret(ctx, "hello-stack/agent-01", "STRIPE_SECRET_KEY", "sk-agent-value")
	if err != nil {
		t.Fatal(err)
	}
	if own.Scope != "agent" || own.Agent != "agent-01" {
		t.Errorf("SetSecret() for one agent = %+v", own)
	}
	mine, err := d.client.Secrets(ctx, "hello-stack/agent-01")
	if err != nil || len(mine) != 2 {
		t.Fatalf("Secrets(agent) = %+v, %v; want its project's and its own", mine, err)
	}
	scopes := map[string]string{}
	for _, s := range mine {
		scopes[s.Name] = s.Scope
	}
	if scopes["OPENAI_API_KEY"] != "project" || scopes["STRIPE_SECRET_KEY"] != "agent" {
		t.Errorf("an agent's list doesn't say where each secret comes from: %+v", scopes)
	}
	// A project's list is still only the project's own.
	if secrets, _ := d.client.Secrets(ctx, "hello-stack"); len(secrets) != 1 {
		t.Errorf("the project's list picked up an agent's own secret: %+v", secrets)
	}
	file = agentFile(t, files, "/home/dev/.config/agentbox/secrets.env")
	if !strings.Contains(file, "export STRIPE_SECRET_KEY='sk-agent-value'") || !strings.Contains(file, "OPENAI_API_KEY") {
		t.Errorf("the agent should hold both secrets:\n%s", file)
	}

	// Removing takes it out of the agent, leaving the other.
	if err := d.client.RemoveSecret(ctx, "hello-stack", "OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	file = agentFile(t, files, "/home/dev/.config/agentbox/secrets.env")
	if strings.Contains(file, "OPENAI_API_KEY") {
		t.Errorf("a removed project secret is still in the agent:\n%s", file)
	}
	if !strings.Contains(file, "STRIPE_SECRET_KEY") {
		t.Errorf("removing the project's secret took the agent's own:\n%s", file)
	}
	if err := d.client.RemoveSecret(ctx, "hello-stack", "OPENAI_API_KEY"); !api.IsNotFound(err) {
		t.Errorf("removing it twice = %v, want a 404", err)
	}
}

// TestSecretNamesAreChecked keeps the API from storing what no agent could
// read, and from replacing what AgentBox writes itself.
func TestSecretNamesAreChecked(t *testing.T) {
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	for _, bad := range []string{"lower_case", "1DIGIT", "HAS-HYPHEN", "CLAUDE_CODE_OAUTH_TOKEN", "GH_TOKEN"} {
		if _, err := d.client.SetSecret(ctx, "hello-stack", bad, "value"); err == nil {
			t.Errorf("SetSecret(%q) was accepted", bad)
		}
	}
	if _, err := d.client.SetSecret(ctx, "hello-stack", "EMPTY", ""); err == nil {
		t.Error("an empty value was accepted")
	}
	// A project that doesn't exist is a 404, not a stored secret.
	if _, err := d.client.SetSecret(ctx, "nope", "KEY", "value"); !api.IsNotFound(err) {
		t.Errorf("SetSecret() for an unknown project = %v, want a 404", err)
	}
	if secrets, _ := d.client.Secrets(ctx, "hello-stack"); len(secrets) != 0 {
		t.Errorf("a refused secret was stored: %+v", secrets)
	}
}

// TestProjectSecretsReachAgentsMadeLater is the other half of delivery: the
// file is written when an agent is created, so a secret set before it existed
// is already there.
func TestProjectSecretsReachAgentsMadeLater(t *testing.T) {
	d, files := secretsDaemon(t)
	ctx := context.Background()
	if _, err := d.client.SetSecret(ctx, "hello-stack", "SHARED_KEY", "shared-value"); err != nil {
		t.Fatal(err)
	}
	// agent-01 is the one the harness made; a second agent stands in for "made
	// later", built by the daemon's own create path.
	t.Setenv("INCUS_INSTANCES", `[{"name":"ab-hello-stack-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}},`+
		`{"name":"ab-hello-stack-agent-02","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.6"}]}}}}]`)
	job, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", Name: "agent-02", AI: "none"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "agent-02 to be created", func() bool {
		j, err := d.client.Job(ctx, job.ID)
		return err == nil && j.Done()
	})
	if j, _ := d.client.Job(ctx, job.ID); j.Status != api.JobSucceeded {
		t.Fatalf("creating agent-02 = %s: %s", j.Status, j.Error)
	}

	content, err := os.ReadFile(filepath.Join(files, "ab-hello-stack-agent-02", "home/dev/.config/agentbox/secrets.env"))
	if err != nil {
		t.Fatalf("a new agent got no secrets file: %v", err)
	}
	if !strings.Contains(string(content), "export SHARED_KEY='shared-value'") {
		t.Errorf("an agent made after the secret was set didn't get it:\n%s", content)
	}
	// And the secret now says it is in both.
	secrets, err := d.client.Secrets(ctx, "hello-stack")
	if err != nil || len(secrets) != 1 || len(secrets[0].Agents) != 2 {
		t.Errorf("Secrets() = %+v, %v; want it delivered to both agents", secrets, err)
	}
}

// TestLeadSecretsAreNamesOnly checks what a project's chat can see: the names
// of its agents' secrets, and no route to a value.
func TestLeadSecretsAreNamesOnly(t *testing.T) {
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	if _, err := d.client.SetSecret(ctx, "hello-stack", "OPENAI_API_KEY", "sk-project-value"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetSecret(ctx, "hello-stack/agent-01", "STRIPE_SECRET_KEY", "sk-agent-value"); err != nil {
		t.Fatal(err)
	}

	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	secrets, err := lead.ProjectSecretNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 2 {
		t.Fatalf("ProjectSecretNames() = %+v; want both", secrets)
	}
	names := map[string]string{}
	for _, s := range secrets {
		names[s.Name] = s.Scope
	}
	if names["OPENAI_API_KEY"] != "project" || names["STRIPE_SECRET_KEY"] != "agent" {
		t.Errorf("the chat can't tell the scopes apart: %+v", names)
	}
	// There is no value in the answer, and no way for it to ask for one.
	if body := rawBody(t, d, "GET", "/v1/projects/hello-stack/secrets"); strings.Contains(body, "sk-") {
		t.Errorf("a value came back:\n%s", body)
	}
	if _, err := lead.SetSecret(ctx, "hello-stack", "NEW_KEY", "value"); err == nil {
		t.Error("a project's chat could store a secret through its own socket")
	}
}

// TestSecretsForTheLeadAreRefused: the project's chat runs on this machine, not
// in an agent, so a secret for it would land in the user's own environment.
func TestSecretsForTheLeadAreRefused(t *testing.T) {
	d, _ := secretsDaemon(t)
	ctx := context.Background()
	// Give the project a lead, the way the chat does.
	if _, err := d.client.ProjectChat(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetSecret(ctx, "hello-stack/"+state.LeadName, "KEY", "value"); err == nil {
		t.Error("a secret for the project's chat was accepted")
	}
}

// rawBody reads a response body as it goes over the socket, so a test can
// check what is *not* in it, whatever the Go types say.
func rawBody(t *testing.T, d testDaemon, method, path string) string {
	t.Helper()
	req, err := http.NewRequest(method, "http://agentbox"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := d.client.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
