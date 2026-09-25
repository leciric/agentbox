package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// tsFile is the desktop app's copy of the API types, generated from this
// package so the two can't drift apart.
const tsFile = "../../desktop/src/shared/api.ts"

var tsTypes = []any{
	Project{}, AddProjectRequest{}, UpdateProjectRequest{}, Section{}, AddSectionRequest{}, UpdateSectionRequest{},
	ProjectLayout{}, SectionProjects{}, Notes{}, NotesRequest{}, Settings{}, UpdateSettingsRequest{}, Limits{}, Agent{}, WorktreeFiles{}, CreateAgentRequest{}, ForkRequest{}, UpdateAgentRequest{},
	Snapshot{}, SnapshotRequest{}, RestoreRequest{}, Base{}, SaveBaseRequest{},
	Job{}, HostUsage{}, AgentUsage{}, Usage{}, ClaudeAccount{}, GitHubAccount{}, AuthStatus{},
	Event{}, JobLogLine{}, AgentChange{}, Theme{}, UpdateThemeRequest{}, UpdateStatus{}, UpdateAvailable{}, ProjectChange{}, PullsChange{}, Self{}, Error{}, TerminalResize{},
	BrowserStatus{}, BrowserPage{}, BrowserOpenRequest{}, PreviewInfo{},
	MediaItem{}, MediaMeta{}, TestCounts{}, ScreenshotRequest{}, RecordRequest{}, RecordingStatus{},
	AddMediaRequest{}, NoteRequest{}, LogsRequest{}, ExportRequest{}, ExportResult{}, DeleteMediaRequest{}, DeleteMediaResult{},
	SetupCheck{}, SetupStatus{}, ImageComponents{}, ImageBuild{}, ImageDownload{}, BuildImageRequest{},
	ClaudeTokenRequest{}, ClaudeLoginRequest{}, ClaudeLogin{}, ClaudeLoginCodeRequest{}, RenameClaudeAccountRequest{}, RenamedClaudeAccount{}, GitHubTokenRequest{}, RenameGitHubAccountRequest{}, RenamedGitHubAccount{},
	Secret{}, SetSecretRequest{},
	PullRequest{}, GitHubError{}, ProjectPullRequests{}, MergePullRequestRequest{}, AgentChanges{}, RetireAdvice{}, FleetAgent{}, Fleet{},
	RetireRequest{}, RetiredAgent{}, RetireResult{},
	Question{}, AskRequest{}, AnswerQuestionRequest{}, EscalateQuestionRequest{}, CredentialRequest{}, AnswerCredentialRequest{}, AgentEvent{},
	AndroidStatus{}, AndroidStartRequest{}, AndroidInstallRequest{}, AndroidInstallResult{},
	HubUser{}, HubSignupRequest{}, HubLoginRequest{}, HubSession{}, HubEnvironment{}, HubCreateEnvironmentRequest{}, HubEnvironmentToken{},
	RemoteStatus{}, RemoteConnectRequest{},
	ChatThread{}, ChatSession{}, ChatOption{}, ChatOptionChoice{}, ChatCommand{}, ChatItem{}, ChatTool{}, ChatDiff{}, ChatPlanEntry{},
	ChatPermission{}, ChatPermissionOption{}, ChatSubagent{}, ChatCompaction{}, ChatTurnResult{}, ChatMessageRequest{}, ChatImage{}, ChatImageUpload{}, ChatAnswerRequest{}, ChatOptionRequest{}, ChatEvent{}, ChatAppend{}, ProjectChat{}, ChatCache{}, ChatCacheChoice{},
	MemoryEvent{}, AddMemoryEventRequest{}, Memory{}, AddMemoryRequest{}, MemorySearchRequest{}, MemorySearchResults{},
	WorkingMemory{}, WorkingMemoryPatch{}, MemoryArtifact{}, AddArtifactRequest{}, AgentReport{}, AddReportRequest{},
	ContextRequest{}, ContextResult{}, ContextSection{}, ContextStats{}, ContextAccount{},
	ResolveMemoryRequest{}, ConsolidateRequest{}, ConsolidationPass{}, MemoryConsolidation{}, MemoryDuplicate{},
	Task{}, AddTaskRequest{}, UpdateTaskRequest{}, LinkTasksRequest{},
	TokenCounts{}, ModelTokens{}, AgentTokens{}, TokenBucket{}, TokenReport{}, TokenTurn{}, ClaudeLimit{}, ClaudeLimitWindow{},
}

var tsConstants = [][2]string{
	{"JobRunning", JobRunning}, {"JobSucceeded", JobSucceeded}, {"JobFailed", JobFailed}, {"JobCancelled", JobCancelled},
	{"EventJob", EventJob}, {"EventJobLog", EventJobLog}, {"EventAgent", EventAgent}, {"EventUsage", EventUsage}, {"EventProject", EventProject}, {"EventMedia", EventMedia}, {"EventPulls", EventPulls}, {"EventTheme", EventTheme}, {"EventUpdate", EventUpdate},
	{"SetupOK", SetupOK}, {"SetupMissing", SetupMissing}, {"SetupOutdated", SetupOutdated}, {"SetupOptional", SetupOptional},
	{"InAgentSocket", InAgentSocket},
	{"LeadName", LeadName}, {"AgentModelAuto", AgentModelAuto},
	{"ConsolidationModelCheap", ConsolidationModelCheap}, {"ConsolidationModelChat", ConsolidationModelChat},
	{"EventChat", EventChat}, {"EventChatCache", EventChatCache}, {"EventQuestion", EventQuestion}, {"EventAgentEvent", EventAgentEvent},
	{"AgentCreated", AgentCreated}, {"AgentFinished", AgentFinished}, {"AgentAsked", AgentAsked}, {"AgentAnswered", AgentAnswered},
	{"CredentialGitHub", CredentialGitHub}, {"CredentialSecret", CredentialSecret},
	{"GitHubNoAccount", GitHubNoAccount}, {"GitHubNoAccess", GitHubNoAccess}, {"GitHubBadToken", GitHubBadToken}, {"GitHubOtherErr", GitHubOtherErr},
	{"ChatOff", ChatOff}, {"ChatStarting", ChatStarting}, {"ChatReady", ChatReady}, {"ChatRunning", ChatRunning}, {"ChatWaiting", ChatWaiting}, {"ChatError", ChatError},
	{"MemoryKindProject", MemoryKindProject}, {"MemoryKindEpisodic", MemoryKindEpisodic}, {"MemoryKindDecision", MemoryKindDecision},
	{"MemoryKindDiscovery", MemoryKindDiscovery}, {"MemoryKindIssue", MemoryKindIssue},
	{"ReportDone", ReportDone}, {"ReportPartial", ReportPartial}, {"ReportBlocked", ReportBlocked}, {"ReportFailed", ReportFailed},
	{"ContextForLead", ContextForLead}, {"ContextForAgent", ContextForAgent}, {"ContextForTool", ContextForTool},
	{"ConsolidationMechanical", ConsolidationMechanical}, {"ConsolidationDistill", ConsolidationDistill},
	{"TaskOpen", TaskOpen}, {"TaskActive", TaskActive}, {"TaskBlocked", TaskBlocked},
	{"TaskDone", TaskDone}, {"TaskAbandoned", TaskAbandoned},
	{"TokensTurn", TokensTurn}, {"TokensBackground", TokensBackground}, {"TokensCompaction", TokensCompaction}, {"TokensConsolidation", TokensConsolidation},
}

func TestTypeScriptTypesAreUpToDate(t *testing.T) {
	want := typeScript()
	if os.Getenv("UPDATE_TS") != "" {
		if err := os.MkdirAll(filepath.Dir(tsFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tsFile, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(tsFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s is out of date: run UPDATE_TS=1 go test ./internal/api", tsFile)
	}
}

// writeFields renders a struct's fields. An embedded struct without a json tag
// is inlined, because that is what encoding/json does with it: FleetAgent
// embeds Agent, and its JSON has Agent's fields at the top level.
func writeFields(b *strings.Builder, t reflect.Type) {
	for i := range t.NumField() {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			writeFields(b, f.Type)
			continue
		}
		if name == "" {
			name = f.Name
		}
		optional := ""
		if strings.Contains(opts, "omitempty") || f.Type.Kind() == reflect.Pointer {
			optional = "?"
		}
		fmt.Fprintf(b, "  %s%s: %s;\n", name, optional, tsType(f.Type))
	}
}

func typeScript() string {
	var b strings.Builder
	b.WriteString("// Generated from internal/api/types.go. Don't edit: run UPDATE_TS=1 go test ./internal/api\n")
	for _, v := range tsTypes {
		t := reflect.TypeOf(v)
		fmt.Fprintf(&b, "\nexport interface %s {\n", t.Name())
		writeFields(&b, t)
		b.WriteString("}\n")
	}
	b.WriteString("\n")
	for _, c := range tsConstants {
		fmt.Fprintf(&b, "export const %s = %q;\n", c[0], c[1])
	}
	return b.String()
}

func tsType(t reflect.Type) string {
	switch t {
	case reflect.TypeFor[time.Time]():
		return "string"
	case reflect.TypeFor[json.RawMessage]():
		return "unknown"
	}
	switch t.Kind() {
	case reflect.Pointer:
		return tsType(t.Elem())
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int64, reflect.Uint16, reflect.Float64:
		return "number"
	case reflect.Slice:
		return tsType(t.Elem()) + "[]"
	case reflect.Map:
		return "Record<" + tsType(t.Key()) + ", " + tsType(t.Elem()) + ">"
	case reflect.Struct:
		return t.Name()
	}
	panic("no TypeScript type for " + t.String())
}
