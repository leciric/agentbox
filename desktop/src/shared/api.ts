// Generated from internal/api/types.go. Don't edit: run UPDATE_TS=1 go test ./internal/api

export interface Project {
  name: string;
  displayName: string;
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
  finishNotices: string;
  rolloverThreshold: number;
  contextBudget: number;
  consolidation: number;
  consolidationModel: string;
  section: string;
  position: number;
  nesting: boolean;
  agentPRs: boolean;
  prWatch: string;
  syncBase: boolean;
  prWatching: boolean;
  createdAt: string;
}

export interface AddProjectRequest {
  path: string;
  name?: string;
  claudeAccount?: string;
  githubAccount?: string;
  copyToLinux?: boolean;
  create?: boolean;
  commitFiles?: boolean;
}

export interface UpdateProjectRequest {
  displayName?: string;
  claudeAccount?: string;
  claudeAccounts?: string[];
  githubAccount?: string;
  moveClaudeAgents?: boolean;
  moveGitHubAgents?: boolean;
  autonomy?: string;
  agentModel?: string;
  branchPrefix?: string;
  finishNotices?: string;
  rolloverThreshold?: number;
  contextBudget?: number;
  consolidation?: number;
  consolidationModel?: string;
  nesting?: boolean;
  agentPRs?: boolean;
  prWatch?: string;
  syncBase?: boolean;
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
  defaultAgentContextWindow: string;
  enforceAgentDefaults: boolean;
  defaultLeadModel: string;
  defaultLeadContextWindow: string;
  claudeModelChoices: ChatOptionChoice[];
  claudeContextWindows: Record<string, number[]>;
  claudeMenuKnown: boolean;
  defaultClaudeEffort: string;
  claudeEffortChoices: ChatOptionChoice[];
  openCodeModelChoices: ChatOptionChoice[];
  openCodeReady: boolean;
  cursorModelChoices: ChatOptionChoice[];
  cursorReady: boolean;
  defaultCursorModel: string;
  defaultCursorEffort: string;
  resumeAfterLimit: boolean;
  continueAfterRestart: boolean;
  claudeCompactWindow: number;
  updateCheck: boolean;
  usageStats: boolean;
  errorReports: boolean;
  errorReportsAsked: boolean;
  prWatch: boolean;
  mediaRetention: string;
  language: string;
  defaultClaudeCompactWindow: number;
  diskFloorMin: number;
  diskFloorPercent: number;
  autoStopIdle: boolean;
  dockerPruneOnStop: boolean;
  idleTimeSeconds: number;
  leadRecheck: boolean;
  leadRecheckMinutes: number;
  imageCache: boolean;
  imageCacheMaxBytes: number;
  defaultImageCacheMaxBytes: number;
  imageCacheBytes: number;
  packageCache: boolean;
  packageCacheMaxBytes: number;
  defaultPackageCacheMaxBytes: number;
  packageCacheBytes: number;
}

export interface UpdateSettingsRequest {
  defaultClaudeModel?: string;
  defaultAgentContextWindow?: string;
  enforceAgentDefaults?: boolean;
  defaultLeadModel?: string;
  defaultLeadContextWindow?: string;
  defaultClaudeEffort?: string;
  defaultCursorModel?: string;
  defaultCursorEffort?: string;
  resumeAfterLimit?: boolean;
  continueAfterRestart?: boolean;
  claudeCompactWindow?: number;
  updateCheck?: boolean;
  updateChannel?: string;
  usageStats?: boolean;
  errorReports?: boolean;
  prWatch?: boolean;
  mediaRetention?: string;
  language?: string;
  autoStopIdle?: boolean;
  dockerPruneOnStop?: boolean;
  idleTimeSeconds?: number;
  leadRecheck?: boolean;
  leadRecheckMinutes?: number;
  diskFloorMin?: number;
  diskFloorPercent?: number;
  imageCache?: boolean;
  imageCacheMaxBytes?: number;
  clearImageCache?: boolean;
  packageCache?: boolean;
  packageCacheMaxBytes?: number;
  clearPackageCache?: boolean;
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
  stalledSince?: string;
  background?: string[];
  state: string;
  memory?: MemoryHold;
  ip: string;
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
  catchUp?: boolean;
  finishNotice?: string;
  queue?: boolean;
  size?: string;
  connectors?: string[];
}

export interface ForkRequest {
  name?: string;
  title?: string;
  snapshot?: string;
  checkpoint?: string;
}

export interface UpdateAgentRequest {
  title?: string;
  claudeAccount?: string;
  githubAccount?: string;
  interface?: string;
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

export interface Checkpoint {
  id: string;
  kind: string;
  turn?: string;
  number: number;
  prompt: string;
  commit: string;
  head: string;
  createdAt: string;
}

export interface RollbackRequest {
  checkpoint: string;
}

export interface RollbackResult {
  saved: Checkpoint;
}

export interface Base {
  snapshot: string;
  savedFrom: string;
  savedAt: string;
  image?: string;
  tools?: string;
  behind?: BaseBehind;
  previous?: Base;
}

export interface BaseBehind {
  imageFrom?: string;
  imageTo?: string;
  changes?: BaseImageChange[];
  components?: string[];
  tools?: BaseToolChange[];
  toolsUnknown?: boolean;
}

export interface BaseImageChange {
  version: string;
  what: string;
}

export interface BaseToolChange {
  name: string;
  from?: string;
  to?: string;
}

export interface SaveBaseRequest {
  agent: string;
}

export interface StopAgentsRequest {
  refs?: string[];
}

export interface StopAgentsResult {
  stopped: StoppedAgent[];
  failed?: StopAgentFailure[];
  freedMemory: number;
  freedCPU: number;
  hostMemoryBefore: number;
  hostMemoryAfter: number;
}

export interface StoppedAgent {
  ref: string;
  title?: string;
  memory: number;
  cpu: number;
  working: boolean;
}

export interface StopAgentFailure {
  ref: string;
  title?: string;
  error: string;
}

export interface MemoryHold {
  paused?: number;
  waiting?: number;
  since: string;
}

export interface PressureStatus {
  some: number;
  full: number;
  runs: HeavyRun[];
}

export interface HeavyRun {
  agent: string;
  key: string;
  command?: string;
  state: string;
  since: string;
  placed?: boolean;
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
  diskRead: number;
  diskWrite: number;
  pressure?: HostPressure;
}

export interface HostPressure {
  ioSome: number;
  ioFull: number;
  memorySome: number;
  memoryFull: number;
  stalling: boolean;
}

export interface AgentUsage {
  ref: string;
  state: string;
  cpu: number;
  memory: number;
  processes: number;
  diskRead: number;
  diskWrite: number;
}

export interface Usage {
  host: HostUsage;
  agents: AgentUsage[];
}

export interface DiskUsageItem {
  label: string;
  bytes: number;
}

export interface DiskUsageCategory {
  kind: string;
  label: string;
  bytes: number;
  items?: DiskUsageItem[];
}

export interface DiskUsage {
  total: number;
  categories: DiskUsageCategory[];
}

export interface AgentDisk {
  machine?: number;
  worktree?: number;
  measuredAt: string;
}

export interface DiskGuard {
  level: string;
  disks: DiskGuardDisk[];
  paused: string[];
  since: string;
  message: string;
}

export interface DiskGuardDisk {
  label: string;
  path?: string;
  free: number;
  total: number;
  floor: number;
  level: string;
  advice?: string;
}

export interface MemoryUsageAgent {
  ref: string;
  title?: string;
  state: string;
  memory: number;
  swap: number;
}

export interface ZramUsage {
  swapBytes: number;
  realBytes: number;
}

export interface MemoryUsage {
  hostTotal: number;
  agentsUsed: number;
  otherUsed: number;
  swapTotal: number;
  swapUsed: number;
  agentsSwap: number;
  zram?: ZramUsage;
  agents: MemoryUsageAgent[];
}

export interface CPUUsageAgent {
  ref: string;
  title?: string;
  state: string;
  cpu: number;
}

export interface CPUUsage {
  hostCPU: number;
  hostCores: number;
  otherCPU: number;
  agents: CPUUsageAgent[];
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
  cursor: boolean;
  cursorEmail?: string;
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
  memory?: MemoryHold;
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
  channel: string;
  nightly?: boolean;
  blocked?: string;
  available?: UpdateAvailable;
  checkedAt?: string;
}

export interface UpdateAvailable {
  version: string;
  url: string;
}

export interface UpdateRelease {
  version: string;
  url: string;
  assets?: ReleaseAsset[];
}

export interface ReleaseAsset {
  name: string;
  url: string;
  size: number;
}

export interface IncusStatus {
  answering: boolean;
  since?: string;
  detail?: string;
  restarted?: string;
}

export interface ProjectChange {
  name: string;
  removed?: boolean;
}

export interface PullsChange {
  project: string;
  github: string;
  fetchedAt: string;
  unchanged?: boolean;
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
  code?: string;
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
  unseen?: boolean;
  favorite?: boolean;
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

export interface Notification {
  id: string;
  kind: string;
  project: string;
  agent: string;
  ref: string;
  title?: string;
  text?: string;
  status?: string;
  pr?: PullRequest;
  media?: MediaItem;
  question?: string;
  at: string;
  seen?: boolean;
}

export interface SeeNotificationsRequest {
  ids?: string[];
  media?: string[];
  all?: boolean;
}

export interface SeeNotificationsResult {
  seen: number;
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

export interface UpdateMediaRequest {
  favorite?: boolean;
}

export interface SetupCheck {
  id: string;
  title: string;
  status: string;
  required: boolean;
  detail?: string;
  fix?: string;
  job?: string;
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
  devCaches: boolean;
  incus: boolean;
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
  devCaches?: boolean;
  incus?: boolean;
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

export interface CursorKeyRequest {
  apiKey: string;
}

export interface CursorKeyResponse {
  email?: string;
}

export interface CursorLogin {
  state: string;
  url?: string;
  email?: string;
  error?: string;
}

export interface RenameGitHubAccountRequest {
  name: string;
}

export interface RenamedGitHubAccount {
  old: string;
  name: string;
  projects: string[];
  agents: string[];
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

export interface Skill {
  name: string;
  description: string;
  source: string;
  enabled: boolean;
  overrides: Record<string, boolean>;
  userInvocable: boolean;
  fileCount: number;
  size: number;
  createdAt: string;
  updatedAt: string;
  active?: boolean;
}

export interface SkillFile {
  path: string;
  size: number;
  binary?: boolean;
  content?: string;
}

export interface SkillDetail {
  name: string;
  description: string;
  source: string;
  enabled: boolean;
  overrides: Record<string, boolean>;
  userInvocable: boolean;
  fileCount: number;
  size: number;
  createdAt: string;
  updatedAt: string;
  active?: boolean;
  files: SkillFile[];
}

export interface SaveSkillRequest {
  content: string;
  enabled?: boolean;
  project?: string;
}

export interface UpdateSkillRequest {
  enabled?: boolean;
}

export interface SkillOverrideRequest {
  override: string;
}

export interface ScanSkillsRequest {
  source: string;
}

export interface SkillCandidate {
  name: string;
  description: string;
  origin: string;
  plugin?: string;
  source: string;
  files: number;
  size: number;
  content: string;
  exists: boolean;
  problem?: string;
}

export interface ImportSkillsRequest {
  source: string;
  names?: string[];
  project?: string;
}

export interface BrowserCookies {
  imported: boolean;
  domains: string[];
  sites?: CookieDomain[];
  cookies: number;
  format?: string;
  source?: string;
  importedAt?: string;
}

export interface BrowserCookiesPreviewRequest {
  export: string;
}

export interface BrowserCookiesPreview {
  format: string;
  cookies: number;
  domains: CookieDomain[];
}

export interface CookieDomain {
  domain: string;
  cookies: number;
}

export interface ImportBrowserCookiesRequest {
  export: string;
  domains: string[];
}

export interface BrowserProfile {
  id: string;
  browser: string;
  browserName: string;
  engine: string;
  name: string;
  keyring?: string;
}

export interface BrowserProfiles {
  profiles: BrowserProfile[];
}

export interface ImportFromBrowserRequest {
  profileId: string;
  keyringSecret: string;
}

export interface SnapRequest {
  image: ChatImageUpload;
  app?: string;
  title?: string;
  desktop?: string;
  window: boolean;
  accessibility?: string;
  takenAt: string;
  project?: string;
}

export interface Snap {
  id: string;
  app?: string;
  title?: string;
  desktop?: string;
  window: boolean;
  accessibility?: string;
  mimeType: string;
  size: number;
  takenAt: string;
  project?: string;
}

export interface SnapSendRequest {
  project: string;
  agent?: string;
  note?: string;
  accessibility: boolean;
}

export interface Connector {
  name: string;
  scope: string;
  project: string;
  agent?: string;
  url: string;
  auth: string;
  secret?: string;
  header?: string;
  scheme?: string;
  enabled: boolean;
  override?: string;
  overrides?: Record<string, boolean>;
  status: string;
  error?: string;
  issuer?: string;
  scopes?: string;
  expiresAt?: string;
  connectedAt?: string;
  updatedAt: string;
  agents: string[];
  removed?: boolean;
}

export interface SetConnectorRequest {
  url: string;
  auth?: string;
  secret?: string;
  header?: string;
  scheme?: string;
  secretValue?: string;
  enabled?: boolean;
}

export interface ConnectorOverrideRequest {
  override: string;
}

export interface ConnectResult {
  authorizationUrl: string;
  redirectUri: string;
  expiresAt: string;
  connector: Connector;
}

export interface SelfConnector {
  name: string;
  url: string;
  status: string;
  error?: string;
}

export interface ConnectorRequest {
  name: string;
  url?: string;
  reason: string;
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
  headSha?: string;
  author?: string;
  authorAvatar?: string;
  agent?: string;
  conflict?: boolean;
  review?: string;
  watched?: boolean;
  labels?: Label[];
}

export interface Label {
  name: string;
  color: string;
  description?: string;
}

export interface ProjectLabels {
  labels: Label[];
}

export interface EditLabelsRequest {
  add?: string[];
  remove?: string[];
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
  githubLogin?: string;
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
  stalledSince?: string;
  background?: string[];
  state: string;
  memory?: MemoryHold;
  ip: string;
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
  kind?: string;
  secretName?: string;
  connector?: string;
  url?: string;
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

export interface CredentialRequest {
  kind: string;
  name?: string;
  reason: string;
}

export interface AnswerCredentialRequest {
  githubAccount?: string;
  value?: string;
  connector?: string;
  refuse?: boolean;
  reason?: string;
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

export interface UsageStatsPending {
  on: boolean;
  usageUrl: string;
  usage: string;
  eventsUrl: string;
  events: string;
  eventsWaiting: number;
}

export interface LANStatus {
  enabled: boolean;
  port: number;
  listening: boolean;
  error?: string;
  urls: string[];
  tunnel: LANTunnel;
  webVersion?: string;
  phones: LANPhone[];
}

export interface LANTunnel {
  enabled: boolean;
  named: boolean;
  hostname?: string;
  state: string;
  url?: string;
  error?: string;
  origin: string;
}

export interface LANPhone {
  id: string;
  name: string;
  paired: string;
  lastSeen?: string;
  lastAddr?: string;
}

export interface UpdateLANRequest {
  enabled?: boolean;
  port?: number;
  tunnel?: boolean;
  tunnelToken?: string;
  tunnelHostname?: string;
}

export interface LANPairing {
  urls: string[];
  qr: string[];
  expires: string;
}

export interface LANHostReport {
  port: number;
  listening: boolean;
  error?: string;
  addresses: string[];
}

export interface LANPairRequest {
  secret: string;
  name: string;
}

export interface LANSession {
  phone: LANPhone;
}

export interface ChatThread {
  agent: string;
  seq: number;
  session: ChatSession;
  items: ChatItem[];
  older?: boolean;
  newer?: boolean;
}

export interface ChatSearch {
  query: string;
  hits: ChatSearchHit[];
  more?: boolean;
}

export interface ChatSearchHit {
  agent: string;
  id: string;
  kind: string;
  snippet: ChatSnippetPart[];
}

export interface ChatSnippetPart {
  text: string;
  match?: boolean;
}

export interface ChatSession {
  state: string;
  tool: string;
  detail?: string;
  error?: string;
  adapter?: string;
  turnStartedAt?: string;
  stalledSince?: string;
  options: ChatOption[];
  commands: ChatCommand[];
  contextUsed?: number;
  contextSize?: number;
  limited?: boolean;
  limitedUntil?: string;
  resumeAt?: string;
  noImages?: boolean;
  background?: string[];
  toolsChanged?: boolean;
  noResume?: boolean;
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
  efforts?: string[];
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
  woken?: boolean;
  hidden?: boolean;
  streaming?: boolean;
  tool?: ChatTool;
  plan?: ChatPlanEntry[];
  permission?: ChatPermission;
  result?: ChatTurnResult;
  subagent?: ChatSubagent;
  compaction?: ChatCompaction;
  handoff?: string;
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
  page?: ChatPage;
}

export interface ChatPage {
  id?: string;
  title?: string;
  public?: boolean;
  expiresInHours?: number;
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
  approval?: string;
  detail?: string;
  diffs?: ChatDiff[];
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

export interface ChatCompaction {
  state: string;
  waiting?: number;
  error?: string;
}

export interface ChatTurnResult {
  state: string;
  stopReason?: string;
  endedAt: string;
  background?: string[];
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
  after?: string;
  checkpoint?: string;
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

export interface ChatCache {
  project: string;
  idleSince?: string;
  ttlSeconds?: number;
  ttlSource?: string;
  dueAt?: string;
  expiresAt?: string;
  contextUsed?: number;
  due: boolean;
}

export interface ChatCacheChoice {
  compact: boolean;
  text?: string;
  images?: ChatImageUpload[];
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
  global?: boolean;
  origin?: string;
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
  confirmations?: number;
  promotion?: string;
  promotionAt?: string;
}

export interface AddMemoryRequest {
  kind?: string;
  title: string;
  content?: string;
  importance?: number;
  supersedesId?: string;
  sourceEventId?: string;
  scope?: string;
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

export interface TidyMemoryRequest {
  olderThanHours?: number;
  apply?: boolean;
}

export interface TidyMemoryResult {
  applied: boolean;
  resolved: Memory[];
  merged: MemoryMerge[];
  kept: number;
  scrubbed: MemoryScrub;
}

export interface MemoryMerge {
  memory: Memory;
  into: Memory;
  score: number;
  why: string;
}

export interface MemoryScrub {
  events: number;
  memories: number;
  reports: number;
  artifacts: number;
  workingMemory: number;
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
  avgTPS?: number;
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
  avgTPS?: number;
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
  avgTPS?: number;
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
  avgTPS?: number;
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
  generationMS?: number;
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

export interface VMStatus {
  mode: string;
  driver: string;
  name?: string;
  state: string;
  since: string;
  problem?: string;
  cpus?: number;
  memory: VMMemory;
  disk: VMDisk;
  limits?: VMLimits;
  live?: VMLimits;
  pausedForDisk?: boolean;
  swap?: VMSwap;
}

export interface VMMemory {
  min: number;
  cap: number;
  granted: number;
  used: number;
  resident: number;
}

export interface VMDisk {
  size: number;
  allocated: number;
  pool: VMDiskImage;
  root: VMDiskImage;
  hostFree?: number;
}

export interface VMDiskImage {
  size: number;
  allocated: number;
}

export interface VMHomeDisk {
  worktrees: number;
  media: number;
}

export interface VMSwap {
  size: number;
  total: number;
  used: number;
}

export interface VMLimits {
  minCpus: number;
  maxCpus: number;
  minMemory: number;
  maxMemory: number;
  minDisk?: number;
  maxDisk?: number;
}

export interface VMResizeRequest {
  cpus?: number;
  memoryCap?: number;
  disk?: number;
}

export interface VMStopRequest {
  agents?: boolean;
}

export interface ReportSection {
  id: string;
  title: string;
  content: string;
}

export interface ReportDraftRequest {
  sections: ReportSection[];
}

export interface ReportDraft {
  install: string;
  version: string;
  os: string;
  arch: string;
  sections: ReportSection[];
  endpoint: string;
}

export interface ReportRequest {
  kind: string;
  message: string;
  sections: ReportSection[];
}

export interface ReportSent {
  id: string;
}

export interface SearchResults {
  query: string;
  groups: SearchGroup[];
}

export interface SearchGroup {
  kind: string;
  hits: SearchHit[];
  more?: boolean;
}

export interface SearchHit {
  id: string;
  title: string;
  detail?: string;
  tag?: string;
  project?: string;
  agent?: string;
  at?: string;
  url?: string;
  memory?: Memory;
  event?: MemoryEvent;
  report?: AgentReport;
  media?: MediaItem;
}

export interface Artifact {
  id: string;
  title: string;
  url: string;
  agent: string;
  item: string;
  agents: string[];
  version: number;
  public?: boolean;
  createdAt: string;
  updatedAt: string;
  expiresAt?: string;
  permanent?: boolean;
  expired?: boolean;
}

export interface Artifacts {
  connector: string;
  artifacts: Artifact[];
}

export interface ArtifactPreview {
  artifact: Artifact;
  status: string;
  page: boolean;
  error?: string;
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
export const EventNotification = "notification";
export const EventNotificationsSeen = "notification.seen";
export const NotifyFinished = "finished";
export const NotifyQuestion = "question";
export const NotifyMedia = "media";
export const EventPulls = "pulls";
export const EventTheme = "theme";
export const EventUpdate = "update";
export const EventDisk = "disk";
export const DiskOK = "ok";
export const DiskLow = "low";
export const DiskFull = "full";
export const EventLAN = "lan";
export const EventSnap = "snap";
export const SetupOK = "ok";
export const SetupMissing = "missing";
export const SetupOutdated = "outdated";
export const SetupOptional = "optional";
export const SetupUpdating = "updating";
export const InAgentSocket = "/run/agentbox.sock";
export const ErrorFolderNotEmpty = "folder-not-empty";
export const DefaultLanguage = "en-US";
export const LeadName = "lead";
export const HomeProject = "_home";
export const AgentModelAuto = "auto";
export const ConsolidationModelCheap = "cheap";
export const ConsolidationModelChat = "";
export const EventChat = "chat";
export const EventChatCache = "chat.cache";
export const EventQuestion = "question";
export const EventAgentEvent = "agent.event";
export const AgentCreated = "created";
export const AgentFinished = "finished";
export const AgentAsked = "asked";
export const AgentAnswered = "answered";
export const CredentialGitHub = "github";
export const CredentialSecret = "secret";
export const ConnectorOAuth = "oauth";
export const ConnectorSecret = "secret";
export const ConnectorNone = "none";
export const ConnectorWide = "agentbox";
export const ConnectorConnected = "connected";
export const ConnectorDisconnected = "disconnected";
export const ConnectorConnecting = "connecting";
export const ConnectorError = "error";
export const EventConnector = "connector";
export const QuestionConnector = "connector";
export const ArtifactActive = "active";
export const ArtifactExpired = "expired";
export const ArtifactUnavailable = "unavailable";
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
export const ReportKindProblem = "problem";
export const ReportKindError = "error";
export const ReportSectionSystem = "system";
export const ReportSectionDaemonLog = "daemon-log";
export const ReportSectionVMLog = "vm-log";
export const ReportSectionAppLog = "app-log";
export const ReportSectionAppErrors = "app-errors";
export const ReportSectionError = "error";
export const ReportDone = "done";
export const ReportPartial = "partial";
export const ReportBlocked = "blocked";
export const ReportFailed = "failed";
export const ContextForLead = "lead";
export const ContextForAgent = "agent";
export const ContextForTool = "tool";
export const ConsolidationMechanical = "mechanical";
export const ConsolidationDistill = "distill";
export const SearchProjects = "projects";
export const SearchAgents = "agents";
export const SearchChats = "chats";
export const SearchMemories = "memories";
export const SearchEvents = "events";
export const SearchReports = "reports";
export const SearchMedia = "media";
export const SearchSkills = "skills";
export const SearchConnectors = "connectors";
export const SearchNotes = "notes";
export const SearchPulls = "pulls";
export const TokensTurn = "turn";
export const TokensBackground = "background";
export const TokensCompaction = "compaction";
export const TokensConsolidation = "consolidation";
export const FeatureDesktopOpen = "desktop.open";
export const FeatureTerminalOpen = "terminal.open";
export const FeatureAndroidOpen = "android.open";
export const FeatureAgentMediaView = "media.view.agent";
export const FeatureProjectMediaView = "media.view.project";
export const FeaturePullList = "pr.list";
export const FeatureMemoryView = "memory.view";
export const FeatureTokensView = "tokens.view";
export const FeatureSettingsEnvironment = "settings.view.environment";
export const FeatureSettingsAccounts = "settings.view.accounts";
export const FeatureSettingsLead = "settings.view.lead";
export const FeatureSettingsAgents = "settings.view.agents";
export const FeatureSettingsGeneral = "settings.view.general";
export const FeatureSettingsModels = "settings.view.models";
export const FeatureSettingsResources = "settings.view.resources";
export const FeatureSettingsProject = "settings.view.project";
export const FeatureSettingsSearch = "settings.search";
export const FeatureChatFind = "chat.find";
export const FeatureSearchOpen = "search.open";
export const FeatureMenuOpenChat = "menu.agent.open_chat";
export const FeatureMenuOpenTerminal = "menu.agent.open_terminal";
export const FeatureMenuInfo = "menu.agent.info";
export const FeatureMenuLifecycle = "menu.agent.lifecycle";
export const FeatureMenuRetire = "menu.agent.retire";
export const FeatureMenuCopyBranch = "menu.agent.copy_branch";
export const FeatureMenuOpenPullRequest = "menu.agent.open_pr";
export const FeatureMenuDestroy = "menu.agent.destroy";
export const ModeHost = "host";
export const ModeVM = "vm";
export const VMDriverLima = "lima";
export const VMDriverCloudHypervisor = "cloud-hypervisor";
export const VMOff = "off";
export const VMStarting = "starting";
export const VMRunning = "running";
export const VMPaused = "paused";
export const VMStopping = "stopping";
export const VMMissing = "missing";
