// Generated from internal/api/types.go. Don't edit: run UPDATE_TS=1 go test ./internal/api

export interface Project {
  name: string;
  root: string;
  branch: string;
  envFiles: string[];
  android: boolean;
  claudeAccount: string;
  claudeAccounts: string[];
  githubAccount: string;
  autonomy: string;
  agentModel: string;
  branchPrefix: string;
  mediaRetentionDays: number;
  finishNotices: string;
  rolloverThreshold: number;
  contextBudget: number;
  consolidation: number;
  consolidationModel: string;
  section: string;
  position: number;
  createdAt: string;
}

export interface AddProjectRequest {
  path: string;
  name?: string;
  claudeAccount?: string;
  githubAccount?: string;
  copyToLinux?: boolean;
}

export interface UpdateProjectRequest {
  claudeAccount?: string;
  claudeAccounts?: string[];
  githubAccount?: string;
  autonomy?: string;
  agentModel?: string;
  branchPrefix?: string;
  mediaRetentionDays?: number;
  finishNotices?: string;
  rolloverThreshold?: number;
  contextBudget?: number;
  consolidation?: number;
  consolidationModel?: string;
}

export interface Section {
  id: string;
  name: string;
  position: number;
  collapsed: boolean;
  createdAt: string;
}

export interface AddSectionRequest {
  name: string;
}

export interface UpdateSectionRequest {
  name?: string;
  collapsed?: boolean;
}

export interface ProjectLayout {
  sections: SectionProjects[];
  loose: string[];
}

export interface SectionProjects {
  id: string;
  projects: string[];
}

export interface Notes {
  text: string;
  updatedAt?: string;
}

export interface NotesRequest {
  text: string;
}

export interface Settings {
  defaultClaudeModel: string;
  claudeModelChoices: ChatOptionChoice[];
  claudeContextWindows: Record<string, number[]>;
  claudeMenuKnown: boolean;
  defaultClaudeEffort: string;
  claudeEffortChoices: ChatOptionChoice[];
  openCodeModelChoices: ChatOptionChoice[];
  openCodeReady: boolean;
  defaultCPU: string;
  defaultCPUAllowance: string;
  defaultMemory: string;
  hostCores: number;
  hostMemory: number;
  resumeAfterLimit: boolean;
  claudeCompactWindow: number;
  updateCheck: boolean;
  defaultClaudeCompactWindow: number;
}

export interface UpdateSettingsRequest {
  defaultClaudeModel?: string;
  defaultClaudeEffort?: string;
  defaultCPU?: string;
  defaultCPUAllowance?: string;
  defaultMemory?: string;
  resumeAfterLimit?: boolean;
  claudeCompactWindow?: number;
  updateCheck?: boolean;
}

export interface Limits {
  cpu: string;
  allowance: string;
  memory: string;
}

export interface Agent {
  ref: string;
  project: string;
  name: string;
  title: string;
  instance: string;
  ai: string;
  autonomous: boolean;
  branch: string;
  baseRef: string;
  baseCommit: string;
  worktree: string;
  source: string;
  claudeAccount: string;
  githubAccount: string;
  interface: string;
  chat?: string;
  state: string;
  ip: string;
  limits: Limits;
  createdAt: string;
}

export interface WorktreeFiles {
  files: string[];
  truncated: boolean;
}

export interface CreateAgentRequest {
  task?: string;
  project: string;
  name?: string;
  title?: string;
  branch?: string;
  ai: string;
  interface?: string;
  autonomous?: boolean;
  model?: string;
  effort?: string;
  contextWindow?: string;
  from?: string;
  claudeAccount?: string;
  githubAccount?: string;
  noEnv?: boolean;
  clean?: boolean;
  cpu?: string;
  memory?: string;
  cpuAllowance?: string;
  finishNotice?: string;
}

export interface ForkRequest {
  name?: string;
  title?: string;
  snapshot?: string;
}

export interface UpdateAgentRequest {
  title?: string;
  claudeAccount?: string;
  githubAccount?: string;
  interface?: string;
  cpu?: string;
  memory?: string;
  cpuAllowance?: string;
}

export interface Snapshot {
  name: string;
  createdAt: string;
  head: string;
}

export interface SnapshotRequest {
  name?: string;
  consistent?: boolean;
}

export interface RestoreRequest {
  snapshot: string;
}

export interface Base {
  snapshot: string;
  savedFrom: string;
  savedAt: string;
  previous?: Base;
}

export interface SaveBaseRequest {
  agent: string;
}

export interface Job {
  id: string;
  kind: string;
  target: string;
  status: string;
  error?: string;
  result?: unknown;
  createdAt: string;
  finishedAt?: string;
}

export interface HostUsage {
  cpu: number;
  cores: number;
  memUsed: number;
  memTotal: number;
  poolUsed: number;
  poolTotal: number;
}

export interface AgentUsage {
  ref: string;
  state: string;
  cpu: number;
  memory: number;
  processes: number;
  limits: Limits;
  cores: number;
}

export interface Usage {
  host: HostUsage;
  agents: AgentUsage[];
}

export interface ClaudeAccount {
  name: string;
  default: boolean;
  savedAt: string;
  valid?: string;
}

export interface GitHubAccount {
  name: string;
  default: boolean;
  savedAt: string;
  login?: string;
}

export interface AuthStatus {
  claude: boolean;
  codex: boolean;
  opencode: boolean;
  claudeAccounts: ClaudeAccount[];
  github: boolean;
  githubAccounts: GitHubAccount[];
  githubUser?: string;
  githubError?: string;
}

export interface Event {
  type: string;
  time: string;
  data: unknown;
}

export interface JobLogLine {
  job: string;
  n: number;
  line: string;
}

export interface AgentChange {
  ref: string;
  state: string;
  ip?: string;
  removed?: boolean;
}

export interface Theme {
  appearance: string;
  available: boolean;
  name: string;
  mode: string;
  background: string;
  surface: string;
  foreground: string;
  muted: string;
  accent: string;
}

export interface UpdateThemeRequest {
  appearance?: string;
}

export interface UpdateStatus {
  current: string;
  enabled: boolean;
  blocked?: string;
  available?: UpdateAvailable;
  checkedAt?: string;
}

export interface UpdateAvailable {
  version: string;
  url: string;
}

export interface ProjectChange {
  name: string;
  removed?: boolean;
}

export interface PullsChange {
  project: string;
  github: string;
  fetchedAt: string;
}

export interface Self {
  ref: string;
  project: string;
  agent: string;
  branch: string;
  worktree: string;
  ip: string;
  state: string;
}

export interface Error {
  error: string;
}

export interface TerminalResize {
  cols: number;
  rows: number;
}

export interface BrowserStatus {
  display: boolean;
  running: boolean;
  version?: string;
  pages: BrowserPage[];
}

export interface BrowserPage {
  id: string;
  url: string;
  title: string;
}

export interface BrowserOpenRequest {
  url: string;
}

export interface PreviewInfo {
  addr: string;
}

export interface MediaItem {
  id: string;
  agent: string;
  agentName?: string;
  agentTitle?: string;
  agentGone?: boolean;
  kind: string;
  name: string;
  file?: string;
  path?: string;
  mime?: string;
  size: number;
  sha256?: string;
  source: string;
  text?: string;
  meta: MediaMeta;
  createdAt: string;
  expiresAt?: string;
  removed?: boolean;
}

export interface MediaMeta {
  width?: number;
  height?: number;
  duration?: number;
  target?: string;
  url?: string;
  entry?: string;
  tests?: TestCounts;
}

export interface TestCounts {
  passed: number;
  failed: number;
  skipped: number;
}

export interface ScreenshotRequest {
  target?: string;
  fullPage?: boolean;
  name?: string;
}

export interface RecordRequest {
  target?: string;
  input?: string;
  name?: string;
  limitSeconds?: number;
}

export interface RecordingStatus {
  recording: boolean;
  target?: string;
  input?: string;
  name?: string;
  source?: string;
  startedAt?: string;
  limitSeconds?: number;
}

export interface AddMediaRequest {
  path: string;
  kind?: string;
  name?: string;
}

export interface NoteRequest {
  text: string;
  name?: string;
}

export interface LogsRequest {
  service?: string;
  since?: string;
  terminal?: boolean;
  window?: string;
  android?: boolean;
  package?: string;
  name?: string;
}

export interface ExportRequest {
  dir?: string;
}

export interface ExportResult {
  dir: string;
  items: number;
}

export interface DeleteMediaRequest {
  ids?: string[];
  all?: boolean;
  agent?: string;
  kind?: string;
}

export interface DeleteMediaResult {
  deleted: number;
  bytes: number;
}

export interface SetupCheck {
  id: string;
  title: string;
  status: string;
  required: boolean;
  detail?: string;
  fix?: string;
}

export interface SetupStatus {
  ready: boolean;
  checks: SetupCheck[];
  image: ImageBuild;
}

export interface ImageComponents {
  android: boolean;
  codex: boolean;
  opencode: boolean;
}

export interface ImageBuild {
  version: string;
  components: ImageComponents;
  installed: ImageComponents;
  downloads: ImageDownload[];
  hint: string;
}

export interface ImageDownload {
  name: string;
  purpose: string;
  mb: number;
  option?: string;
}

export interface BuildImageRequest {
  android?: boolean;
  codex?: boolean;
  opencode?: boolean;
}

export interface ClaudeTokenRequest {
  token: string;
  account?: string;
}

export interface ClaudeLoginRequest {
  account?: string;
}

export interface ClaudeLogin {
  job: string;
  account: string;
  status: string;
  error?: string;
  url?: string;
  codeUrl?: string;
}

export interface ClaudeLoginCodeRequest {
  code: string;
}

export interface RenameClaudeAccountRequest {
  name: string;
}

export interface RenamedClaudeAccount {
  old: string;
  name: string;
  projects: string[];
  agents: string[];
}

export interface GitHubTokenRequest {
  token: string;
  account?: string;
}

export interface Secret {
  name: string;
  scope: string;
  project: string;
  agent?: string;
  updatedAt: string;
  agents: string[];
}

export interface SetSecretRequest {
  value: string;
}

export interface PullRequest {
  number: number;
  title: string;
  state: string;
  checks: string;
  url: string;
  draft?: boolean;
  additions?: number;
  deletions?: number;
  comments?: number;
  updatedAt?: string;
  baseBranch?: string;
  headBranch?: string;
  agent?: string;
}

export interface GitHubError {
  kind: string;
  message?: string;
  account?: string;
  login?: string;
  repo?: string;
}

export interface ProjectPullRequests {
  project: string;
  github?: string;
  githubAccount?: string;
  githubError?: GitHubError;
  pullRequests: PullRequest[];
  noOrigin?: boolean;
  nonGitHubRemote?: string;
  canMerge?: boolean;
  canMergeKnown?: boolean;
  mergeMethods?: string[];
  fetchedAt?: string;
  refreshing?: boolean;
}

export interface MergePullRequestRequest {
  method: string;
}

export interface AgentChanges {
  files: number;
  insertions: number;
  deletions: number;
  dirty: boolean;
}

export interface RetireAdvice {
  safe: boolean;
  reason?: string;
  branch?: string;
}

export interface FleetAgent {
  ref: string;
  project: string;
  name: string;
  title: string;
  instance: string;
  ai: string;
  autonomous: boolean;
  branch: string;
  baseRef: string;
  baseCommit: string;
  worktree: string;
  source: string;
  claudeAccount: string;
  githubAccount: string;
  interface: string;
  chat?: string;
  state: string;
  ip: string;
  limits: Limits;
  createdAt: string;
  changes: AgentChanges;
  media: number;
  pr?: PullRequest;
  busy?: boolean;
  idle?: boolean;
  lastActive?: string;
  retire: RetireAdvice;
}

export interface Fleet {
  project: string;
  agents: FleetAgent[];
  creating: Job[];
  idle: number;
  github?: string;
  githubAccount?: string;
  githubError?: GitHubError;
  pullsFetchedAt?: string;
  pullsRefreshing?: boolean;
}

export interface RetireRequest {
  how: string;
  idleFor?: string;
  agents?: string[];
  force?: boolean;
  dryRun?: boolean;
}

export interface RetiredAgent {
  name: string;
  title?: string;
  branch?: string;
  reason?: string;
}

export interface RetireResult {
  how: string;
  dryRun?: boolean;
  retired: RetiredAgent[];
  skipped: RetiredAgent[];
}

export interface Question {
  id: string;
  project: string;
  agent: string;
  ref: string;
  question: string;
  context?: string;
  status: string;
  answer?: string;
  answeredBy?: string;
  escalation?: string;
  createdAt: string;
  answeredAt?: string;
}

export interface AskRequest {
  question: string;
  context?: string;
}

export interface AnswerQuestionRequest {
  answer: string;
}

export interface EscalateQuestionRequest {
  why?: string;
}

export interface AgentEvent {
  id: string;
  project: string;
  agent: string;
  ref: string;
  title?: string;
  kind: string;
  summary?: string;
  cut?: boolean;
  changes?: AgentChanges;
  pr?: PullRequest;
  question?: string;
  at: string;
}

export interface AndroidStatus {
  available: boolean;
  problem?: string;
  sdk?: string;
  images: string[];
  running: boolean;
  booted: boolean;
  image?: string;
  device?: string;
}

export interface AndroidStartRequest {
  image?: string;
  memoryMB?: number;
  cores?: number;
}

export interface AndroidInstallRequest {
  path: string;
}

export interface AndroidInstallResult {
  output: string;
}

export interface HubUser {
  id: string;
  email: string;
  name: string;
}

export interface HubSignupRequest {
  email: string;
  name?: string;
  password: string;
  label?: string;
}

export interface HubLoginRequest {
  email: string;
  password: string;
  label?: string;
}

export interface HubSession {
  token: string;
  user: HubUser;
  expiresAt: string;
}

export interface HubEnvironment {
  id: string;
  name: string;
  online: boolean;
  connectedAt?: string;
  lastSeenAt?: string;
  version?: string;
  hostname?: string;
  createdAt: string;
}

export interface HubCreateEnvironmentRequest {
  name: string;
}

export interface HubEnvironmentToken {
  environment: HubEnvironment;
  token: string;
}

export interface RemoteStatus {
  configured: boolean;
  hub?: string;
  connected: boolean;
  since?: string;
  error?: string;
}

export interface RemoteConnectRequest {
  hub: string;
  token: string;
}

export interface ChatThread {
  agent: string;
  seq: number;
  session: ChatSession;
  items: ChatItem[];
}

export interface ChatSession {
  state: string;
  tool: string;
  detail?: string;
  error?: string;
  adapter?: string;
  turnStartedAt?: string;
  options: ChatOption[];
  commands: ChatCommand[];
  contextUsed?: number;
  contextSize?: number;
  limited?: boolean;
  limitedUntil?: string;
  resumeAt?: string;
  noImages?: boolean;
}

export interface ChatOption {
  id: string;
  name: string;
  description?: string;
  category?: string;
  type: string;
  value: string;
  choices: ChatOptionChoice[];
}

export interface ChatOptionChoice {
  value: string;
  name: string;
  description?: string;
  group?: string;
  kind?: string;
}

export interface ChatCommand {
  name: string;
  description: string;
  hint?: string;
}

export interface ChatItem {
  id: string;
  turn: string;
  kind: string;
  text?: string;
  images?: ChatImage[];
  delivery?: string;
  hidden?: boolean;
  streaming?: boolean;
  tool?: ChatTool;
  plan?: ChatPlanEntry[];
  permission?: ChatPermission;
  result?: ChatTurnResult;
  subagent?: ChatSubagent;
  parent?: string;
  createdAt: string;
  updatedAt: string;
}

export interface ChatTool {
  callId: string;
  name?: string;
  title: string;
  kind: string;
  status: string;
  command?: string;
  paths?: string[];
  output?: string;
  diffs?: ChatDiff[];
}

export interface ChatDiff {
  path: string;
  oldText: string;
  newText: string;
  created?: boolean;
  truncated?: boolean;
}

export interface ChatPlanEntry {
  content: string;
  status: string;
}

export interface ChatPermission {
  callId: string;
  title: string;
  options: ChatPermissionOption[];
  outcome?: string;
}

export interface ChatPermissionOption {
  id: string;
  name: string;
  kind: string;
}

export interface ChatSubagent {
  name: string;
  task: string;
  state: string;
}

export interface ChatTurnResult {
  state: string;
  stopReason?: string;
  endedAt: string;
}

export interface ChatMessageRequest {
  text: string;
  images?: ChatImageUpload[];
}

export interface ChatImage {
  id: string;
  mimeType: string;
  name?: string;
  size: number;
}

export interface ChatImageUpload {
  mimeType: string;
  data: string;
  name?: string;
}

export interface ChatAnswerRequest {
  optionId: string;
}

export interface ChatOptionRequest {
  value: string;
}

export interface ChatEvent {
  agent: string;
  seq: number;
  item?: ChatItem;
  append?: ChatAppend;
  session?: ChatSession;
  cleared?: boolean;
}

export interface ChatAppend {
  id: string;
  text: string;
}

export interface ProjectChat {
  project: string;
  ref: string;
  started: boolean;
  worktree?: string;
  baseRef?: string;
  chat: string;
}

export interface MemoryEvent {
  id: string;
  project: string;
  agent?: string;
  session?: string;
  at: string;
  type: string;
  payload?: unknown;
  artifactId?: string;
}

export interface AddMemoryEventRequest {
  type: string;
  agent?: string;
  session?: string;
  payload?: unknown;
  artifactId?: string;
  at?: string;
}

export interface Memory {
  id: string;
  project: string;
  kind: string;
  title: string;
  content: string;
  importance: number;
  createdAt: string;
  updatedAt: string;
  supersedesId?: string;
  sourceEventId?: string;
  superseded?: boolean;
  resolvedAt?: string;
  resolvedBy?: string;
  referencedAt?: string;
  decayedAt?: string;
}

export interface AddMemoryRequest {
  kind?: string;
  title: string;
  content?: string;
  importance?: number;
  supersedesId?: string;
  sourceEventId?: string;
}

export interface MemorySearchRequest {
  query: string;
  limit?: number;
}

export interface MemorySearchResults {
  memories?: Memory[];
  events?: MemoryEvent[];
  reports?: AgentReport[];
}

export interface WorkingMemory {
  goal?: string;
  currentTask?: string;
  activeAgents?: string[];
  blockers?: string[];
  notes?: string;
  updatedAt?: string;
}

export interface WorkingMemoryPatch {
  goal?: string;
  currentTask?: string;
  activeAgents?: string[];
  blockers?: string[];
  notes?: string;
}

export interface MemoryArtifact {
  id: string;
  project: string;
  agent?: string;
  type: string;
  path: string;
  metadata?: unknown;
  createdAt: string;
}

export interface AddArtifactRequest {
  type: string;
  path: string;
  agent?: string;
  metadata?: unknown;
}

export interface AgentReport {
  id: string;
  project: string;
  agent: string;
  createdAt: string;
  task?: string;
  status: string;
  summary: string;
  discoveries?: string[];
  decisions?: string[];
  remainingIssues?: string[];
  artifacts?: string[];
}

export interface AddReportRequest {
  agent?: string;
  task?: string;
  status?: string;
  summary: string;
  discoveries?: string[];
  decisions?: string[];
  remainingIssues?: string[];
  artifacts?: string[];
}

export interface ContextRequest {
  query?: string;
  budget?: number;
  for?: string;
}

export interface ContextResult {
  text: string;
  sections?: ContextSection[];
  stats: ContextStats;
}

export interface ContextSection {
  kind: string;
  title: string;
  rows: number;
  tokens: number;
}

export interface ContextStats {
  project: string;
  for: string;
  at: string;
  query?: string;
  budget: number;
  tokens: number;
  rows: number;
  consideredRows: number;
  droppedRows: number;
  droppedSections?: string[];
  truncated?: boolean;
  corpusTokens: number;
  ratio: number;
}

export interface ContextAccount {
  builds: number;
  tokens: number;
  droppedRows: number;
  recent?: ContextStats[];
}

export interface ResolveMemoryRequest {
  id: string;
  why?: string;
}

export interface ConsolidateRequest {
  distil?: boolean;
}

export interface ConsolidationPass {
  id: string;
  project: string;
  kind: string;
  at: string;
  durationMs: number;
  eventsRead: number;
  memoriesWritten: number;
  memoriesSuperseded: number;
  memoriesResolved: number;
  memoriesDecayed: number;
  duplicatesFound: number;
  inputBytes: number;
  outputBytes: number;
  model?: string;
  throughEventId?: string;
  throughAt?: string;
  error?: string;
}

export interface MemoryConsolidation {
  project: string;
  setting: number;
  events: number;
  memories: number;
  superseded: number;
  resolved: number;
  duplicates: number;
  pending: number;
  passes: number;
  eventsRead: number;
  memoriesWritten: number;
  memoriesSuperseded: number;
  memoriesResolved: number;
  memoriesDecayed: number;
  inputBytes: number;
  outputBytes: number;
  lastPass?: string;
  watermark?: string;
  recent?: ConsolidationPass[];
}

export interface MemoryDuplicate {
  memory: Memory;
  of: Memory;
  similarity: number;
  foundAt: string;
}

export interface Task {
  id: string;
  project: string;
  agent?: string;
  parentId?: string;
  status: string;
  goal: string;
  detail?: string;
  createdAt: string;
  updatedAt: string;
  closedAt?: string;
  dependsOn?: string[];
  blocks?: string[];
}

export interface AddTaskRequest {
  goal: string;
  detail?: string;
  agent?: string;
  parentId?: string;
  status?: string;
  dependsOn?: string[];
}

export interface UpdateTaskRequest {
  status?: string;
  goal?: string;
  detail?: string;
  agent?: string;
  parentId?: string;
}

export interface LinkTasksRequest {
  taskId: string;
  dependsOnId: string;
}

export interface TokenCounts {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
  costUSD: number;
}

export interface ModelTokens {
  model: string;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
  costUSD: number;
}

export interface AgentTokens {
  project: string;
  agent: string;
  ref: string;
  ai: string;
  title?: string;
  exists: boolean;
  turns: number;
  maxContext: number;
  lastAt: string;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
  costUSD: number;
  models: ModelTokens[];
}

export interface TokenBucket {
  start: string;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
  costUSD: number;
}

export interface TokenReport {
  since?: string;
  until: string;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
  costUSD: number;
  agents: AgentTokens[];
  buckets: TokenBucket[];
  bucketSeconds: number;
}

export interface TokenTurn {
  project: string;
  agent: string;
  ai: string;
  session?: string;
  turn?: string;
  kind: string;
  model?: string;
  at: string;
  context: number;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
  costUSD: number;
}

export interface ClaudeLimit {
  account: string;
  default: boolean;
  at: string;
  status: string;
  usingOverage?: boolean;
  windows: ClaudeLimitWindow[];
}

export interface ClaudeLimitWindow {
  name: string;
  label: string;
  utilization: number;
  resetsAt: string;
}

export const JobRunning = "running";
export const JobSucceeded = "succeeded";
export const JobFailed = "failed";
export const JobCancelled = "cancelled";
export const EventJob = "job";
export const EventJobLog = "job.log";
export const EventAgent = "agent";
export const EventUsage = "usage";
export const EventProject = "project";
export const EventMedia = "media";
export const EventPulls = "pulls";
export const EventTheme = "theme";
export const EventUpdate = "update";
export const SetupOK = "ok";
export const SetupMissing = "missing";
export const SetupOutdated = "outdated";
export const SetupOptional = "optional";
export const InAgentSocket = "/run/agentbox.sock";
export const LeadName = "lead";
export const AgentModelAuto = "auto";
export const ConsolidationModelCheap = "cheap";
export const ConsolidationModelChat = "";
export const EventChat = "chat";
export const EventQuestion = "question";
export const EventAgentEvent = "agent.event";
export const AgentCreated = "created";
export const AgentFinished = "finished";
export const AgentAsked = "asked";
export const AgentAnswered = "answered";
export const GitHubNoAccount = "noAccount";
export const GitHubNoAccess = "noAccess";
export const GitHubBadToken = "badToken";
export const GitHubOtherErr = "other";
export const ChatOff = "off";
export const ChatStarting = "starting";
export const ChatReady = "ready";
export const ChatRunning = "running";
export const ChatWaiting = "waiting";
export const ChatError = "error";
export const MemoryKindProject = "project";
export const MemoryKindEpisodic = "episodic";
export const MemoryKindDecision = "decision";
export const MemoryKindDiscovery = "discovery";
export const MemoryKindIssue = "issue";
export const ReportDone = "done";
export const ReportPartial = "partial";
export const ReportBlocked = "blocked";
export const ReportFailed = "failed";
export const ContextForLead = "lead";
export const ContextForAgent = "agent";
export const ContextForTool = "tool";
export const ConsolidationMechanical = "mechanical";
export const ConsolidationDistill = "distill";
export const TaskOpen = "open";
export const TaskActive = "active";
export const TaskBlocked = "blocked";
export const TaskDone = "done";
export const TaskAbandoned = "abandoned";
export const TokensTurn = "turn";
export const TokensBackground = "background";
export const TokensCompaction = "compaction";
export const TokensConsolidation = "consolidation";
