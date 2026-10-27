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

	"agentbox/internal/toolcreds"
)

// tsFile is the desktop app's copy of the API types, generated from this
// package so the two can't drift apart.
const tsFile = "../../desktop/src/shared/api.ts"

var tsTypes = []any{
	Project{}, ProjectActivity{}, ProjectIcon{}, SetProjectIconRequest{}, AddProjectRequest{}, UpdateProjectRequest{}, Section{}, AddSectionRequest{}, UpdateSectionRequest{},
	ProjectLayout{}, SectionProjects{}, SidebarLayout{}, Notes{}, NotesRequest{}, Settings{}, ToolAutoUpdate{}, UpdateSettingsRequest{}, Agent{}, WorktreeFiles{}, FileEntry{}, FileList{}, FileStatus{}, FileContent{}, FilesChange{}, ArchivedAgent{}, DeleteArchivedRequest{}, DeleteArchivedResult{}, ContinueArchivedRequest{}, CreateAgentRequest{}, ForkRequest{}, UpdateAgentRequest{},
	Snapshot{}, SnapshotRequest{}, RestoreRequest{}, Checkpoint{}, RollbackRequest{}, RollbackResult{}, Base{}, BaseBehind{}, BaseImageChange{}, BaseToolChange{}, SaveBaseRequest{},
	StopAgentsRequest{}, StopAgentsResult{}, StoppedAgent{}, StopAgentFailure{},
	MemoryHold{}, PressureStatus{}, HeavyRun{},
	Job{}, HostUsage{}, HostPressure{}, AgentUsage{}, Usage{}, DiskUsageItem{}, DiskUsageCategory{}, DiskUsage{}, AgentDisk{}, StoppedMachine{}, StoppedMachines{}, RemoveStoppedRequest{}, RemoveStoppedResult{}, ToolCredential{}, DiskGuard{}, DiskGuardDisk{},
	MemoryUsageAgent{}, ZramUsage{}, MemoryUsage{}, HostMemory{}, CPUUsageAgent{}, CPUUsage{}, TerminalTail{}, TerminalTails{}, ClaudeAccount{}, GitHubAccount{}, AuthStatus{},
	Event{}, JobLogLine{}, AgentChange{}, Theme{}, UpdateThemeRequest{}, UpdateStatus{}, UpdateAvailable{}, UpdateRelease{}, ReleaseAsset{}, IncusStatus{}, ProjectChange{}, PullsChange{}, Self{}, Error{}, TerminalResize{},
	BrowserStatus{}, BrowserPage{}, BrowserOpenRequest{}, GPUReport{}, VulkanDevice{}, VMGPU{}, ElementPickRequest{}, ElementPickResult{}, ElementPick{}, ElementStyle{}, ElementBox{}, ElementSize{}, PreviewInfo{}, DesktopUse{},
	MediaItem{}, MediaMeta{}, MediaCut{}, Notification{}, SeeNotificationsRequest{}, SeeNotificationsResult{}, TestCounts{}, ScreenshotRequest{}, RecordRequest{}, RecordingStatus{}, Timeline{}, TimelineStep{}, TimelinePage{},
	AddMediaRequest{}, NoteRequest{}, LogsRequest{}, ExportRequest{}, ExportResult{}, DeleteMediaRequest{}, DeleteMediaResult{}, UpdateMediaRequest{}, MediaPage{}, NotificationPage{}, ReviewPage{}, WorkPage{}, WorkItem{}, MediaCounts{}, MediaAgentCount{}, MediaProjectCount{},
	SetupCheck{}, SetupStatus{}, ImageComponents{}, ImageBuild{}, ImageDownload{}, BuildImageRequest{}, ImageStatus{},
	ClaudeTokenRequest{}, ClaudeLoginRequest{}, ClaudeLogin{}, ClaudeLoginCodeRequest{}, RenameClaudeAccountRequest{}, RenamedClaudeAccount{}, GitHubTokenRequest{}, CursorKeyRequest{}, CursorKeyResponse{}, CursorLogin{}, CursorLoginRequest{}, ToolAccount{}, ToolAccountRequest{}, RenameToolAccountRequest{}, RenamedToolAccount{}, RenameGitHubAccountRequest{}, RenamedGitHubAccount{},
	Secret{}, SetSecretRequest{}, SecretSettingsRequest{},
	Skill{}, SkillFile{}, SkillDetail{}, SaveSkillRequest{}, UpdateSkillRequest{}, SkillOverrideRequest{}, ScanSkillsRequest{}, SkillCandidate{}, ImportSkillsRequest{},
	BrowserCookies{}, BrowserCookiesPreviewRequest{}, BrowserCookiesPreview{}, CookieDomain{}, ImportBrowserCookiesRequest{},
	BrowserProfile{}, BrowserProfiles{}, UnreadableBrowser{}, ImportFromBrowserRequest{},
	SnapRequest{}, Snap{}, SnapSendRequest{},
	Connector{}, SetConnectorRequest{}, ConnectorOverrideRequest{}, ConnectResult{}, SelfConnector{}, ConnectorRequest{},
	ScanConnectorsRequest{}, ConnectorScan{}, ConnectorUnreadable{}, ConnectorCandidate{}, ConnectorSource{}, ImportConnectorsRequest{}, ImportConnector{}, ImportConnectorsResult{}, ImportConnectorFailure{},
	PullRequest{}, Label{}, ProjectLabels{}, EditLabelsRequest{}, GitHubError{}, ProjectPullRequests{}, Issue{}, IssueHandoff{}, IssuePullRequest{}, ProjectIssues{}, MergePullRequestRequest{}, PullRequestDetail{}, PullCheck{}, PullRequestFiles{}, PullFile{}, PullFileDiff{}, AgentChanges{}, RetireAdvice{}, FleetAgent{}, Fleet{},
	RetireRequest{}, RetiredAgent{}, RetireResult{},
	Question{}, AskRequest{}, AnswerQuestionRequest{}, EscalateQuestionRequest{}, CredentialRequest{}, AnswerCredentialRequest{}, AgentEvent{}, ReviewItem{}, ReviewRequest{}, RequestChangesRequest{}, ReviewNote{}, AddReviewNoteRequest{}, EditReviewNoteRequest{},
	AndroidStatus{}, AndroidStartRequest{}, AndroidInstallRequest{}, AndroidInstallResult{},
	HubUser{}, HubInfo{}, HubLoginRequest{}, HubSetupRequest{}, HubSession{}, HubPairRequest{}, HubPairing{}, HubPairTokenRequest{}, HubPairResult{}, HubPairingInfo{}, HubDevice{}, HubEnvironment{}, HubCreateEnvironmentRequest{}, HubEnvironmentToken{},
	RemoteStatus{}, RemoteConnectRequest{}, RemotePairRequest{}, RemotePairing{}, UsageStatsPending{},
	LANStatus{}, LANTunnel{}, LANPhone{}, PhonePush{}, PhonePushTest{}, UpdateLANRequest{}, LANPairing{}, LANHostReport{}, LANPairRequest{}, LANSession{}, UpdateLANPhoneRequest{}, LANAuditEntry{}, LANAudit{},
	ChatThread{}, ChatSearch{}, ChatSearchHit{}, ChatSnippetPart{}, ChatSession{}, ChatOption{}, ChatOptionChoice{}, ChatCommand{}, ChatItem{}, ChatTool{}, ChatPage{}, ChatDiff{}, ChatPlanEntry{},
	ChatPermission{}, ChatPermissionOption{}, ChatSubagent{}, ChatCompaction{}, ChatProjectMessage{}, ChatScheduledRun{}, Schedule{}, ScheduleRun{}, ScheduleRequest{}, ScheduleChange{}, ChatAgentPermission{}, ChatTurnResult{}, ChatMessageRequest{}, ChatImage{}, ChatImageUpload{}, ChatFile{}, ChatFileUpload{}, ChatAnswerRequest{}, ChatOptionRequest{}, ChatEvent{}, ChatAppend{}, ProjectChat{}, ChatLine{}, ChatCache{}, ChatCacheChoice{}, LeadRunners{}, LeadRunner{}, LeadRunnerRequest{}, LeadAccount{},
	MemoryEvent{}, AddMemoryEventRequest{}, Memory{}, AddMemoryRequest{}, MemorySearchRequest{}, MemorySearchResults{},
	WorkingMemory{}, WorkingMemoryPatch{}, MemoryArtifact{}, AddArtifactRequest{}, AgentReport{}, AddReportRequest{},
	ContextRequest{}, ContextResult{}, ContextSection{}, ContextStats{}, ContextAccount{},
	ResolveMemoryRequest{}, ConsolidateRequest{}, TidyMemoryRequest{}, TidyMemoryResult{}, MemoryMerge{}, MemoryScrub{}, ConsolidationPass{}, MemoryConsolidation{}, MemoryDuplicate{},
	TokenCounts{}, ModelTokens{}, AgentTokens{}, TokenBucket{}, TokenReport{}, TokenTurn{}, ClaudeLimit{}, ClaudeLimitWindow{},
	Recap{}, RecapDay{}, RecapProject{}, RecapAgent{}, RecapModel{},
	VMStatus{}, VMMemory{}, VMDisk{}, VMDiskImage{}, VMHomeDisk{}, VMSwap{}, VMLimits{}, VMResizeRequest{}, VMStopRequest{},
	ReportSection{}, ReportDraftRequest{}, ReportDraft{}, ReportRequest{}, ReportSent{},
	SearchResults{}, SearchGroup{}, SearchHit{},
	Artifact{}, Artifacts{}, ArtifactPreview{}, PublishRenderRequest{}, PublishRenderResult{},
	FanOutRequest{}, FanOutVariant{}, FanOutResult{}, Group{}, GroupMember{}, KeepRequest{}, RecommendRequest{}, GroupChange{},
	TraceSpan{}, SpanStats{}, DiagnosticsSpans{}, DiagnosticsStalls{}, VMResourceSample{}, AgentResourceSample{}, AgentResources{}, DiagnosticsResources{}, PackageCachePart{}, DiagnosticsPackageCache{}, ClientSpansRequest{},
	ShareReport{}, FileCount{}, DiagnosticsFiles{},
}

var tsConstants = [][2]string{
	{"JobRunning", JobRunning}, {"JobSucceeded", JobSucceeded}, {"JobFailed", JobFailed}, {"JobCancelled", JobCancelled},
	{"EventJob", EventJob}, {"EventJobLog", EventJobLog}, {"EventAgent", EventAgent}, {"EventUsage", EventUsage}, {"EventProject", EventProject}, {"EventMedia", EventMedia}, {"EventNotification", EventNotification}, {"EventNotificationsSeen", EventNotificationsSeen}, {"NotifyQuestion", NotifyQuestion}, {"NotifyMedia", NotifyMedia}, {"EventPulls", EventPulls}, {"EventTheme", EventTheme}, {"EventUpdate", EventUpdate}, {"EventDisk", EventDisk}, {"DiskOK", DiskOK}, {"DiskLow", DiskLow}, {"DiskFull", DiskFull}, {"EventLAN", EventLAN}, {"EventSnap", EventSnap}, {"EventFiles", EventFiles},
	{"PushFinished", PushFinished}, {"PushNeedsYou", PushNeedsYou}, {"PushPRReady", PushPRReady}, {"PushCIFailed", PushCIFailed},
	{"FileModified", FileModified}, {"FileAdded", FileAdded}, {"FileDeleted", FileDeleted}, {"FileUntracked", FileUntracked},
	{"SetupOK", SetupOK}, {"SetupMissing", SetupMissing}, {"SetupOutdated", SetupOutdated}, {"SetupOptional", SetupOptional}, {"SetupUpdating", SetupUpdating}, {"SetupBuilding", SetupBuilding},
	{"ImageBuildIdle", ImageBuildIdle}, {"ImageBuilding", ImageBuilding}, {"ImageBuildFailed", ImageBuildFailed},
	{"InAgentSocket", InAgentSocket}, {"ErrorFolderNotEmpty", ErrorFolderNotEmpty},
	{"DefaultLanguage", DefaultLanguage},
	{"PRStateOpen", PRStateOpen}, {"PRStateMerged", PRStateMerged}, {"PRStateClosed", PRStateClosed}, {"PRStateNone", PRStateNone},
	{"LeadName", LeadName}, {"HomeProject", HomeProject}, {"AgentModelAuto", AgentModelAuto},
	{"ConsolidationModelCheap", ConsolidationModelCheap}, {"ConsolidationModelChat", ConsolidationModelChat},
	{"EventGroup", EventGroup}, {"EventChat", EventChat}, {"EventChatCache", EventChatCache}, {"EventQuestion", EventQuestion}, {"EventSchedule", EventSchedule}, {"EventAgentEvent", EventAgentEvent},
	{"ReviewReviewed", ReviewReviewed}, {"ReviewDismissed", ReviewDismissed}, {"EventReview", EventReview},
	{"AgentCreated", AgentCreated}, {"AgentFinished", AgentFinished}, {"AgentAsked", AgentAsked}, {"AgentAnswered", AgentAnswered},
	{"CredentialGitHub", CredentialGitHub}, {"CredentialSecret", CredentialSecret},
	{"ClaudeCredentialVariables", toolcreds.Joined("claude")}, {"CodexCredentialVariables", toolcreds.Joined("codex")}, {"OpenCodeCredentialVariables", toolcreds.Joined("opencode")},
	{"ConnectorOAuth", ConnectorOAuth}, {"ConnectorSecret", ConnectorSecret}, {"ConnectorNone", ConnectorNone}, {"ConnectorWide", ConnectorWide},
	{"ConnectorConnected", ConnectorConnected}, {"ConnectorDisconnected", ConnectorDisconnected}, {"ConnectorConnecting", ConnectorConnecting}, {"ConnectorError", ConnectorError},
	{"EventConnector", EventConnector}, {"QuestionConnector", QuestionConnector},
	{"TransportHTTP", TransportHTTP}, {"TransportSSE", TransportSSE}, {"TransportStdio", TransportStdio},
	{"ConnectorProblemStdio", ConnectorProblemStdio}, {"ConnectorProblemSSE", ConnectorProblemSSE}, {"ConnectorProblemURL", ConnectorProblemURL}, {"ConnectorProblemVariable", ConnectorProblemVariable},
	{"ArtifactActive", ArtifactActive}, {"ArtifactExpired", ArtifactExpired}, {"ArtifactUnavailable", ArtifactUnavailable},
	{"GitHubNoAccount", GitHubNoAccount}, {"GitHubNoAccess", GitHubNoAccess}, {"GitHubBadToken", GitHubBadToken}, {"GitHubOtherErr", GitHubOtherErr},
	{"ChatOff", ChatOff}, {"ChatStarting", ChatStarting}, {"ChatReady", ChatReady}, {"ChatRunning", ChatRunning}, {"ChatWaiting", ChatWaiting}, {"ChatError", ChatError}, {"ChatOptionPlan", ChatOptionPlan},
	{"MemoryKindProject", MemoryKindProject}, {"MemoryKindEpisodic", MemoryKindEpisodic}, {"MemoryKindDecision", MemoryKindDecision},
	{"MemoryKindDiscovery", MemoryKindDiscovery}, {"MemoryKindIssue", MemoryKindIssue},
	{"ReportKindProblem", ReportKindProblem}, {"ReportKindError", ReportKindError},
	{"ReportSectionSystem", ReportSectionSystem}, {"ReportSectionDaemonLog", ReportSectionDaemonLog}, {"ReportSectionVMLog", ReportSectionVMLog},
	{"ReportSectionAppLog", ReportSectionAppLog}, {"ReportSectionAppErrors", ReportSectionAppErrors}, {"ReportSectionError", ReportSectionError},
	{"ReportDone", ReportDone}, {"ReportPartial", ReportPartial}, {"ReportBlocked", ReportBlocked}, {"ReportFailed", ReportFailed},
	{"ContextForLead", ContextForLead}, {"ContextForAgent", ContextForAgent}, {"ContextForTool", ContextForTool},
	{"ConsolidationMechanical", ConsolidationMechanical}, {"ConsolidationDistill", ConsolidationDistill},
	{"SearchProjects", SearchProjects}, {"SearchAgents", SearchAgents}, {"SearchChats", SearchChats}, {"SearchMemories", SearchMemories},
	{"SearchEvents", SearchEvents}, {"SearchReports", SearchReports}, {"SearchMedia", SearchMedia}, {"SearchSkills", SearchSkills},
	{"SearchConnectors", SearchConnectors}, {"SearchNotes", SearchNotes}, {"SearchPulls", SearchPulls},
	{"TokensTurn", TokensTurn}, {"TokensBackground", TokensBackground}, {"TokensCompaction", TokensCompaction}, {"TokensConsolidation", TokensConsolidation},
	{"FeatureDesktopOpen", FeatureDesktopOpen}, {"FeatureTerminalOpen", FeatureTerminalOpen}, {"FeatureAndroidOpen", FeatureAndroidOpen}, {"FeatureAgentMediaView", FeatureAgentMediaView},
	{"FeatureProjectMediaView", FeatureProjectMediaView}, {"FeaturePullList", FeaturePullList}, {"FeatureMemoryView", FeatureMemoryView}, {"FeatureTokensView", FeatureTokensView},
	{"FeatureSettingsEnvironment", FeatureSettingsEnvironment}, {"FeatureSettingsAccounts", FeatureSettingsAccounts}, {"FeatureSettingsLead", FeatureSettingsLead}, {"FeatureSettingsAgents", FeatureSettingsAgents},
	{"FeatureSettingsGeneral", FeatureSettingsGeneral}, {"FeatureSettingsModels", FeatureSettingsModels}, {"FeatureSettingsResources", FeatureSettingsResources}, {"FeatureSettingsProject", FeatureSettingsProject}, {"FeatureSettingsSearch", FeatureSettingsSearch}, {"FeatureChatFind", FeatureChatFind}, {"FeatureSearchOpen", FeatureSearchOpen},
	{"FeatureMenuOpenChat", FeatureMenuOpenChat}, {"FeatureMenuOpenTerminal", FeatureMenuOpenTerminal}, {"FeatureMenuInfo", FeatureMenuInfo}, {"FeatureMenuLifecycle", FeatureMenuLifecycle}, {"FeatureMenuRetire", FeatureMenuRetire},
	{"FeatureMenuCopyBranch", FeatureMenuCopyBranch}, {"FeatureMenuOpenPullRequest", FeatureMenuOpenPullRequest}, {"FeatureMenuDestroy", FeatureMenuDestroy},
	{"ModeHost", ModeHost}, {"ModeVM", ModeVM}, {"VMDriverLima", VMDriverLima}, {"VMDriverCloudHypervisor", VMDriverCloudHypervisor},
	{"VMOff", VMOff}, {"VMStarting", VMStarting}, {"VMRunning", VMRunning}, {"VMPaused", VMPaused}, {"VMStopping", VMStopping}, {"VMMissing", VMMissing},
	{"SpanLevelWarn", SpanLevelWarn}, {"SpanSourceApp", SpanSourceApp},
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
	case reflect.Interface:
		return "unknown"
	}
	panic("no TypeScript type for " + t.String())
}
