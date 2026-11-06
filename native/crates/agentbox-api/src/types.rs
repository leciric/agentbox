// Generated from internal/api/types.go. Don't edit: run UPDATE_TS=1 go test ./internal/api
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

use crate::de::*;

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Project {
    pub name: String,
    #[serde(rename = "displayName")]
    pub display_name: String,
    pub root: String,
    pub branch: String,
    #[serde(rename = "envFiles", deserialize_with = "nullable")]
    pub env_files: Vec<String>,
    pub android: bool,
    pub ios: bool,
    #[serde(rename = "claudeAccount")]
    pub claude_account: String,
    #[serde(rename = "claudeAccounts", deserialize_with = "nullable")]
    pub claude_accounts: Vec<String>,
    #[serde(rename = "githubAccount")]
    pub github_account: String,
    #[serde(rename = "codexAccount")]
    pub codex_account: String,
    #[serde(rename = "openCodeAccounts", deserialize_with = "nullable")]
    pub open_code_accounts: BTreeMap<String, String>,
    #[serde(rename = "cursorAccount")]
    pub cursor_account: String,
    #[serde(rename = "geminiAccount")]
    pub gemini_account: String,
    pub autonomy: String,
    #[serde(rename = "agentModel")]
    pub agent_model: String,
    #[serde(rename = "agentTool")]
    pub agent_tool: String,
    #[serde(rename = "agentEffort")]
    pub agent_effort: String,
    #[serde(rename = "agentContextWindow")]
    pub agent_context_window: String,
    #[serde(rename = "agentMode")]
    pub agent_mode: String,
    #[serde(rename = "branchPrefix")]
    pub branch_prefix: String,
    #[serde(rename = "finishNotices")]
    pub finish_notices: String,
    #[serde(rename = "rolloverThreshold")]
    pub rollover_threshold: i64,
    #[serde(rename = "contextBudget")]
    pub context_budget: i64,
    pub consolidation: i64,
    #[serde(rename = "consolidationModel")]
    pub consolidation_model: String,
    pub section: String,
    pub position: i64,
    pub nesting: bool,
    #[serde(rename = "agentPRs")]
    pub agent_p_rs: bool,
    #[serde(rename = "prWatch")]
    pub pr_watch: String,
    #[serde(rename = "syncBase")]
    pub sync_base: bool,
    #[serde(rename = "settleHours")]
    pub settle_hours: i64,
    #[serde(rename = "fullTestSuite")]
    pub full_test_suite: bool,
    #[serde(rename = "pickUpIssues")]
    pub pick_up_issues: bool,
    #[serde(rename = "pullRequests")]
    pub pull_requests: bool,
    #[serde(rename = "prWatching")]
    pub pr_watching: bool,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "iconVersion")]
    pub icon_version: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectActivity {
    pub project: String,
    #[serde(rename = "lastActivity", skip_serializing_if = "Option::is_none")]
    pub last_activity: Option<String>,
    #[serde(rename = "lastMessage", skip_serializing_if = "Option::is_none")]
    pub last_message: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectIcon {
    pub source: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub emoji: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub image: String,
    #[serde(rename = "detectedFrom", skip_serializing_if = "is_empty")]
    pub detected_from: String,
    #[serde(rename = "hasDetected", skip_serializing_if = "is_empty")]
    pub has_detected: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectIconCandidate {
    pub from: String,
    pub image: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub chosen: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SetProjectIconRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub emoji: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub candidate: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub image: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AddProjectRequest {
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(rename = "claudeAccount", skip_serializing_if = "is_empty")]
    pub claude_account: String,
    #[serde(rename = "githubAccount", skip_serializing_if = "is_empty")]
    pub github_account: String,
    #[serde(rename = "codexAccount", skip_serializing_if = "is_empty")]
    pub codex_account: String,
    #[serde(
        rename = "openCodeAccounts",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub open_code_accounts: BTreeMap<String, String>,
    #[serde(rename = "cursorAccount", skip_serializing_if = "is_empty")]
    pub cursor_account: String,
    #[serde(rename = "geminiAccount", skip_serializing_if = "is_empty")]
    pub gemini_account: String,
    #[serde(rename = "copyToLinux", skip_serializing_if = "is_empty")]
    pub copy_to_linux: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub create: bool,
    #[serde(rename = "commitFiles", skip_serializing_if = "is_empty")]
    pub commit_files: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CloneProjectRequest {
    pub url: String,
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(rename = "claudeAccount", skip_serializing_if = "is_empty")]
    pub claude_account: String,
    #[serde(rename = "githubAccount", skip_serializing_if = "is_empty")]
    pub github_account: String,
    #[serde(rename = "codexAccount", skip_serializing_if = "is_empty")]
    pub codex_account: String,
    #[serde(
        rename = "openCodeAccounts",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub open_code_accounts: BTreeMap<String, String>,
    #[serde(rename = "cursorAccount", skip_serializing_if = "is_empty")]
    pub cursor_account: String,
    #[serde(rename = "geminiAccount", skip_serializing_if = "is_empty")]
    pub gemini_account: String,
    #[serde(rename = "copyToLinux", skip_serializing_if = "is_empty")]
    pub copy_to_linux: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub create: bool,
    #[serde(rename = "commitFiles", skip_serializing_if = "is_empty")]
    pub commit_files: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateProjectRequest {
    #[serde(rename = "displayName", skip_serializing_if = "Option::is_none")]
    pub display_name: Option<String>,
    #[serde(rename = "claudeAccount", skip_serializing_if = "Option::is_none")]
    pub claude_account: Option<String>,
    #[serde(rename = "claudeAccounts", skip_serializing_if = "Option::is_none")]
    pub claude_accounts: Option<Vec<String>>,
    #[serde(rename = "githubAccount", skip_serializing_if = "Option::is_none")]
    pub github_account: Option<String>,
    #[serde(rename = "codexAccount", skip_serializing_if = "Option::is_none")]
    pub codex_account: Option<String>,
    #[serde(rename = "openCodeAccounts", skip_serializing_if = "Option::is_none")]
    pub open_code_accounts: Option<BTreeMap<String, String>>,
    #[serde(rename = "cursorAccount", skip_serializing_if = "Option::is_none")]
    pub cursor_account: Option<String>,
    #[serde(rename = "geminiAccount", skip_serializing_if = "Option::is_none")]
    pub gemini_account: Option<String>,
    #[serde(rename = "moveClaudeAgents", skip_serializing_if = "is_empty")]
    pub move_claude_agents: bool,
    #[serde(rename = "moveGitHubAgents", skip_serializing_if = "is_empty")]
    pub move_git_hub_agents: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub autonomy: Option<String>,
    #[serde(rename = "agentModel", skip_serializing_if = "Option::is_none")]
    pub agent_model: Option<String>,
    #[serde(rename = "agentTool", skip_serializing_if = "Option::is_none")]
    pub agent_tool: Option<String>,
    #[serde(rename = "agentEffort", skip_serializing_if = "Option::is_none")]
    pub agent_effort: Option<String>,
    #[serde(rename = "agentContextWindow", skip_serializing_if = "Option::is_none")]
    pub agent_context_window: Option<String>,
    #[serde(rename = "agentMode", skip_serializing_if = "Option::is_none")]
    pub agent_mode: Option<String>,
    #[serde(rename = "branchPrefix", skip_serializing_if = "Option::is_none")]
    pub branch_prefix: Option<String>,
    #[serde(rename = "finishNotices", skip_serializing_if = "Option::is_none")]
    pub finish_notices: Option<String>,
    #[serde(rename = "rolloverThreshold", skip_serializing_if = "Option::is_none")]
    pub rollover_threshold: Option<i64>,
    #[serde(rename = "contextBudget", skip_serializing_if = "Option::is_none")]
    pub context_budget: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub consolidation: Option<i64>,
    #[serde(rename = "consolidationModel", skip_serializing_if = "Option::is_none")]
    pub consolidation_model: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub nesting: Option<bool>,
    #[serde(rename = "agentPRs", skip_serializing_if = "Option::is_none")]
    pub agent_p_rs: Option<bool>,
    #[serde(rename = "prWatch", skip_serializing_if = "Option::is_none")]
    pub pr_watch: Option<String>,
    #[serde(rename = "syncBase", skip_serializing_if = "Option::is_none")]
    pub sync_base: Option<bool>,
    #[serde(rename = "settleHours", skip_serializing_if = "Option::is_none")]
    pub settle_hours: Option<i64>,
    #[serde(rename = "fullTestSuite", skip_serializing_if = "Option::is_none")]
    pub full_test_suite: Option<bool>,
    #[serde(rename = "pickUpIssues", skip_serializing_if = "Option::is_none")]
    pub pick_up_issues: Option<bool>,
    #[serde(rename = "pullRequests", skip_serializing_if = "Option::is_none")]
    pub pull_requests: Option<bool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Section {
    pub id: String,
    pub name: String,
    pub position: i64,
    pub collapsed: bool,
    #[serde(rename = "createdAt")]
    pub created_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AddSectionRequest {
    pub name: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateSectionRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub collapsed: Option<bool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectLayout {
    #[serde(deserialize_with = "nullable")]
    pub sections: Vec<SectionProjects>,
    #[serde(deserialize_with = "nullable")]
    pub loose: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SectionProjects {
    pub id: String,
    #[serde(deserialize_with = "nullable")]
    pub projects: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SidebarLayout {
    #[serde(deserialize_with = "nullable")]
    pub hidden: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Notes {
    pub text: String,
    #[serde(rename = "updatedAt", skip_serializing_if = "is_empty")]
    pub updated_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct NotesRequest {
    pub text: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Settings {
    #[serde(rename = "defaultClaudeModel")]
    pub default_claude_model: String,
    #[serde(rename = "defaultAgentContextWindow")]
    pub default_agent_context_window: String,
    #[serde(rename = "enforceAgentDefaults")]
    pub enforce_agent_defaults: bool,
    #[serde(rename = "defaultLeadModel")]
    pub default_lead_model: String,
    #[serde(rename = "defaultLeadContextWindow")]
    pub default_lead_context_window: String,
    #[serde(rename = "cloneFolder")]
    pub clone_folder: String,
    #[serde(rename = "defaultLeadEffort")]
    pub default_lead_effort: String,
    #[serde(rename = "defaultLeadMode")]
    pub default_lead_mode: String,
    #[serde(rename = "claudeModelChoices", deserialize_with = "nullable")]
    pub claude_model_choices: Vec<ChatOptionChoice>,
    #[serde(rename = "claudeContextWindows", deserialize_with = "nullable")]
    pub claude_context_windows: BTreeMap<String, Vec<i64>>,
    #[serde(rename = "claudeMenuKnown")]
    pub claude_menu_known: bool,
    #[serde(rename = "defaultClaudeEffort")]
    pub default_claude_effort: String,
    #[serde(rename = "claudeEffortChoices", deserialize_with = "nullable")]
    pub claude_effort_choices: Vec<ChatOptionChoice>,
    #[serde(rename = "claudeModeChoices", deserialize_with = "nullable")]
    pub claude_mode_choices: Vec<ChatOptionChoice>,
    #[serde(rename = "openCodeModelChoices", deserialize_with = "nullable")]
    pub open_code_model_choices: Vec<ChatOptionChoice>,
    #[serde(rename = "openCodeReady")]
    pub open_code_ready: bool,
    #[serde(rename = "openCodeVersion")]
    pub open_code_version: String,
    #[serde(rename = "cursorModelChoices", deserialize_with = "nullable")]
    pub cursor_model_choices: Vec<ChatOptionChoice>,
    #[serde(rename = "cursorReady")]
    pub cursor_ready: bool,
    #[serde(rename = "defaultCursorModel")]
    pub default_cursor_model: String,
    #[serde(rename = "defaultCursorEffort")]
    pub default_cursor_effort: String,
    #[serde(rename = "geminiModelChoices", deserialize_with = "nullable")]
    pub gemini_model_choices: Vec<ChatOptionChoice>,
    #[serde(rename = "geminiReady")]
    pub gemini_ready: bool,
    #[serde(rename = "defaultGeminiModel")]
    pub default_gemini_model: String,
    #[serde(rename = "lastAgentPick")]
    pub last_agent_pick: AgentPick,
    #[serde(rename = "firstRun")]
    pub first_run: bool,
    #[serde(rename = "resumeAfterLimit")]
    pub resume_after_limit: bool,
    #[serde(rename = "continueAfterRestart")]
    pub continue_after_restart: bool,
    #[serde(rename = "creditAITool")]
    pub credit_ai_tool: bool,
    #[serde(rename = "builtWithBadge")]
    pub built_with_badge: bool,
    #[serde(rename = "builtWithBadgeOffer")]
    pub built_with_badge_offer: bool,
    #[serde(rename = "seenFeatures", deserialize_with = "nullable")]
    pub seen_features: Vec<String>,
    #[serde(rename = "claudeCompactWindow")]
    pub claude_compact_window: i64,
    #[serde(rename = "updateCheck")]
    pub update_check: bool,
    #[serde(rename = "usageStats")]
    pub usage_stats: bool,
    #[serde(rename = "agentGPU")]
    pub agent_gpu: bool,
    #[serde(rename = "blockFullSuite")]
    pub block_full_suite: bool,
    #[serde(rename = "vmGPU")]
    pub vm_gpu: VMGPU,
    #[serde(rename = "errorReports")]
    pub error_reports: bool,
    #[serde(rename = "prWatch")]
    pub pr_watch: bool,
    #[serde(rename = "mergeWhenGreen")]
    pub merge_when_green: bool,
    #[serde(rename = "agentGoals")]
    pub agent_goals: bool,
    #[serde(rename = "agentFreeze")]
    pub agent_freeze: bool,
    #[serde(rename = "mediaRetention")]
    pub media_retention: String,
    pub language: String,
    pub timezone: String,
    #[serde(rename = "defaultClaudeCompactWindow")]
    pub default_claude_compact_window: i64,
    #[serde(rename = "diskFloorMin")]
    pub disk_floor_min: i64,
    #[serde(rename = "diskFloorPercent")]
    pub disk_floor_percent: f64,
    #[serde(rename = "autoStopIdle")]
    pub auto_stop_idle: bool,
    #[serde(rename = "removeStopped")]
    pub remove_stopped: bool,
    #[serde(rename = "removeStoppedDays")]
    pub remove_stopped_days: i64,
    #[serde(rename = "dockerPruneOnStop")]
    pub docker_prune_on_stop: bool,
    #[serde(rename = "idleTimeSeconds")]
    pub idle_time_seconds: i64,
    #[serde(rename = "idleReclaim")]
    pub idle_reclaim: bool,
    #[serde(rename = "idleFreezeSeconds")]
    pub idle_freeze_seconds: i64,
    #[serde(rename = "idleStopSeconds")]
    pub idle_stop_seconds: i64,
    #[serde(rename = "leadRecheck")]
    pub lead_recheck: bool,
    #[serde(rename = "leadRecheckMinutes")]
    pub lead_recheck_minutes: i64,
    #[serde(rename = "leadSleep")]
    pub lead_sleep: bool,
    #[serde(rename = "leadSleepMinutes")]
    pub lead_sleep_minutes: i64,
    #[serde(rename = "imageCache")]
    pub image_cache: bool,
    #[serde(rename = "imageCacheMaxBytes")]
    pub image_cache_max_bytes: i64,
    #[serde(rename = "defaultImageCacheMaxBytes")]
    pub default_image_cache_max_bytes: i64,
    #[serde(rename = "imageCacheBytes")]
    pub image_cache_bytes: i64,
    #[serde(rename = "packageCache")]
    pub package_cache: bool,
    #[serde(rename = "packageCacheMaxBytes")]
    pub package_cache_max_bytes: i64,
    #[serde(rename = "defaultPackageCacheMaxBytes")]
    pub default_package_cache_max_bytes: i64,
    #[serde(rename = "packageCacheBytes")]
    pub package_cache_bytes: i64,
    #[serde(rename = "localNodeModules")]
    pub local_node_modules: bool,
    #[serde(rename = "claudeCodeUpdate")]
    pub claude_code_update: ToolAutoUpdate,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentPick {
    pub ai: String,
    pub model: String,
    pub effort: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ToolAutoUpdate {
    pub on: bool,
    pub installed: String,
    pub pinned: String,
    pub latest: String,
    #[serde(rename = "checkedAt", skip_serializing_if = "Option::is_none")]
    pub checked_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    pub checking: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub job: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateSettingsRequest {
    #[serde(rename = "defaultClaudeModel", skip_serializing_if = "Option::is_none")]
    pub default_claude_model: Option<String>,
    #[serde(
        rename = "defaultAgentContextWindow",
        skip_serializing_if = "Option::is_none"
    )]
    pub default_agent_context_window: Option<String>,
    #[serde(
        rename = "enforceAgentDefaults",
        skip_serializing_if = "Option::is_none"
    )]
    pub enforce_agent_defaults: Option<bool>,
    #[serde(rename = "defaultLeadModel", skip_serializing_if = "Option::is_none")]
    pub default_lead_model: Option<String>,
    #[serde(
        rename = "defaultLeadContextWindow",
        skip_serializing_if = "Option::is_none"
    )]
    pub default_lead_context_window: Option<String>,
    #[serde(rename = "cloneFolder", skip_serializing_if = "Option::is_none")]
    pub clone_folder: Option<String>,
    #[serde(
        rename = "defaultClaudeEffort",
        skip_serializing_if = "Option::is_none"
    )]
    pub default_claude_effort: Option<String>,
    #[serde(rename = "defaultLeadEffort", skip_serializing_if = "Option::is_none")]
    pub default_lead_effort: Option<String>,
    #[serde(rename = "defaultLeadMode", skip_serializing_if = "Option::is_none")]
    pub default_lead_mode: Option<String>,
    #[serde(rename = "defaultCursorModel", skip_serializing_if = "Option::is_none")]
    pub default_cursor_model: Option<String>,
    #[serde(
        rename = "defaultCursorEffort",
        skip_serializing_if = "Option::is_none"
    )]
    pub default_cursor_effort: Option<String>,
    #[serde(rename = "defaultGeminiModel", skip_serializing_if = "Option::is_none")]
    pub default_gemini_model: Option<String>,
    #[serde(rename = "lastAgentPick", skip_serializing_if = "Option::is_none")]
    pub last_agent_pick: Option<AgentPick>,
    #[serde(rename = "firstRunDone", skip_serializing_if = "Option::is_none")]
    pub first_run_done: Option<bool>,
    #[serde(rename = "openCodeVersion", skip_serializing_if = "Option::is_none")]
    pub open_code_version: Option<String>,
    #[serde(rename = "resumeAfterLimit", skip_serializing_if = "Option::is_none")]
    pub resume_after_limit: Option<bool>,
    #[serde(
        rename = "continueAfterRestart",
        skip_serializing_if = "Option::is_none"
    )]
    pub continue_after_restart: Option<bool>,
    #[serde(rename = "creditAITool", skip_serializing_if = "Option::is_none")]
    pub credit_ai_tool: Option<bool>,
    #[serde(rename = "builtWithBadge", skip_serializing_if = "Option::is_none")]
    pub built_with_badge: Option<bool>,
    #[serde(
        rename = "dismissBuiltWithBadgeOffer",
        skip_serializing_if = "Option::is_none"
    )]
    pub dismiss_built_with_badge_offer: Option<bool>,
    #[serde(rename = "seeFeature", skip_serializing_if = "Option::is_none")]
    pub see_feature: Option<String>,
    #[serde(
        rename = "claudeCompactWindow",
        skip_serializing_if = "Option::is_none"
    )]
    pub claude_compact_window: Option<i64>,
    #[serde(rename = "updateCheck", skip_serializing_if = "Option::is_none")]
    pub update_check: Option<bool>,
    #[serde(rename = "updateChannel", skip_serializing_if = "Option::is_none")]
    pub update_channel: Option<String>,
    #[serde(rename = "usageStats", skip_serializing_if = "Option::is_none")]
    pub usage_stats: Option<bool>,
    #[serde(rename = "agentGPU", skip_serializing_if = "Option::is_none")]
    pub agent_gpu: Option<bool>,
    #[serde(rename = "blockFullSuite", skip_serializing_if = "Option::is_none")]
    pub block_full_suite: Option<bool>,
    #[serde(rename = "errorReports", skip_serializing_if = "Option::is_none")]
    pub error_reports: Option<bool>,
    #[serde(rename = "prWatch", skip_serializing_if = "Option::is_none")]
    pub pr_watch: Option<bool>,
    #[serde(rename = "mergeWhenGreen", skip_serializing_if = "Option::is_none")]
    pub merge_when_green: Option<bool>,
    #[serde(rename = "agentGoals", skip_serializing_if = "Option::is_none")]
    pub agent_goals: Option<bool>,
    #[serde(rename = "agentFreeze", skip_serializing_if = "Option::is_none")]
    pub agent_freeze: Option<bool>,
    #[serde(rename = "mediaRetention", skip_serializing_if = "Option::is_none")]
    pub media_retention: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub language: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub timezone: Option<String>,
    #[serde(rename = "autoStopIdle", skip_serializing_if = "Option::is_none")]
    pub auto_stop_idle: Option<bool>,
    #[serde(rename = "removeStopped", skip_serializing_if = "Option::is_none")]
    pub remove_stopped: Option<bool>,
    #[serde(rename = "removeStoppedDays", skip_serializing_if = "Option::is_none")]
    pub remove_stopped_days: Option<i64>,
    #[serde(rename = "dockerPruneOnStop", skip_serializing_if = "Option::is_none")]
    pub docker_prune_on_stop: Option<bool>,
    #[serde(rename = "idleTimeSeconds", skip_serializing_if = "Option::is_none")]
    pub idle_time_seconds: Option<i64>,
    #[serde(rename = "idleReclaim", skip_serializing_if = "Option::is_none")]
    pub idle_reclaim: Option<bool>,
    #[serde(rename = "idleFreezeSeconds", skip_serializing_if = "Option::is_none")]
    pub idle_freeze_seconds: Option<i64>,
    #[serde(rename = "idleStopSeconds", skip_serializing_if = "Option::is_none")]
    pub idle_stop_seconds: Option<i64>,
    #[serde(rename = "leadRecheck", skip_serializing_if = "Option::is_none")]
    pub lead_recheck: Option<bool>,
    #[serde(rename = "leadRecheckMinutes", skip_serializing_if = "Option::is_none")]
    pub lead_recheck_minutes: Option<i64>,
    #[serde(rename = "leadSleep", skip_serializing_if = "Option::is_none")]
    pub lead_sleep: Option<bool>,
    #[serde(rename = "leadSleepMinutes", skip_serializing_if = "Option::is_none")]
    pub lead_sleep_minutes: Option<i64>,
    #[serde(rename = "diskFloorMin", skip_serializing_if = "Option::is_none")]
    pub disk_floor_min: Option<i64>,
    #[serde(rename = "diskFloorPercent", skip_serializing_if = "Option::is_none")]
    pub disk_floor_percent: Option<f64>,
    #[serde(rename = "imageCache", skip_serializing_if = "Option::is_none")]
    pub image_cache: Option<bool>,
    #[serde(rename = "imageCacheMaxBytes", skip_serializing_if = "Option::is_none")]
    pub image_cache_max_bytes: Option<i64>,
    #[serde(rename = "clearImageCache", skip_serializing_if = "is_empty")]
    pub clear_image_cache: bool,
    #[serde(rename = "packageCache", skip_serializing_if = "Option::is_none")]
    pub package_cache: Option<bool>,
    #[serde(
        rename = "packageCacheMaxBytes",
        skip_serializing_if = "Option::is_none"
    )]
    pub package_cache_max_bytes: Option<i64>,
    #[serde(rename = "clearPackageCache", skip_serializing_if = "is_empty")]
    pub clear_package_cache: bool,
    #[serde(rename = "localNodeModules", skip_serializing_if = "Option::is_none")]
    pub local_node_modules: Option<bool>,
    #[serde(
        rename = "claudeCodeAutoUpdate",
        skip_serializing_if = "Option::is_none"
    )]
    pub claude_code_auto_update: Option<bool>,
    #[serde(rename = "checkClaudeCode", skip_serializing_if = "is_empty")]
    pub check_claude_code: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Agent {
    pub r#ref: String,
    pub project: String,
    pub name: String,
    pub title: String,
    pub instance: String,
    pub ai: String,
    pub autonomous: bool,
    pub branch: String,
    #[serde(rename = "baseRef")]
    pub base_ref: String,
    #[serde(rename = "baseCommit")]
    pub base_commit: String,
    pub worktree: String,
    pub source: String,
    #[serde(rename = "claudeAccount")]
    pub claude_account: String,
    #[serde(rename = "codexAccount", skip_serializing_if = "is_empty")]
    pub codex_account: String,
    #[serde(
        rename = "openCodeAccounts",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub open_code_accounts: BTreeMap<String, String>,
    #[serde(rename = "cursorAccount", skip_serializing_if = "is_empty")]
    pub cursor_account: String,
    #[serde(rename = "geminiAccount", skip_serializing_if = "is_empty")]
    pub gemini_account: String,
    #[serde(rename = "githubAccount")]
    pub github_account: String,
    pub interface: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub chat: String,
    #[serde(rename = "leadSession", skip_serializing_if = "is_empty")]
    pub lead_session: String,
    #[serde(rename = "stalledSince", skip_serializing_if = "Option::is_none")]
    pub stalled_since: Option<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub background: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub outcome: String,
    #[serde(rename = "outcomeNote", skip_serializing_if = "is_empty")]
    pub outcome_note: String,
    #[serde(rename = "outcomeUntil", skip_serializing_if = "Option::is_none")]
    pub outcome_until: Option<String>,
    #[serde(rename = "changesRequested", skip_serializing_if = "is_empty")]
    pub changes_requested: String,
    pub state: String,
    #[serde(rename = "waitingForImage", skip_serializing_if = "is_empty")]
    pub waiting_for_image: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub memory: Option<MemoryHold>,
    #[serde(skip_serializing_if = "is_empty")]
    pub reclaimed: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub goal: Option<AgentGoal>,
    pub ip: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WorktreeFiles {
    #[serde(deserialize_with = "nullable")]
    pub files: Vec<String>,
    pub truncated: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FileEntry {
    pub name: String,
    pub path: String,
    pub dir: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub size: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub ignored: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub symlink: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FileList {
    pub path: String,
    #[serde(deserialize_with = "nullable")]
    pub entries: Vec<FileEntry>,
    pub truncated: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FileStatus {
    pub base: String,
    #[serde(deserialize_with = "nullable")]
    pub changes: BTreeMap<String, String>,
    pub truncated: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FileContent {
    pub path: String,
    pub size: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub binary: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub image: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub data: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub text: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub truncated: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub pdf: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub sqlite: bool,
    #[serde(rename = "tooLarge", skip_serializing_if = "is_empty")]
    pub too_large: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub base: String,
    #[serde(rename = "baseText", skip_serializing_if = "is_empty")]
    pub base_text: String,
    #[serde(rename = "baseMissing", skip_serializing_if = "is_empty")]
    pub base_missing: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SQLiteTable {
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub view: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SQLiteQuery {
    pub path: String,
    #[serde(deserialize_with = "nullable")]
    pub tables: Vec<SQLiteTable>,
    #[serde(skip_serializing_if = "is_empty")]
    pub table: String,
    #[serde(deserialize_with = "nullable")]
    pub columns: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub rows: Vec<Vec<serde_json::Value>>,
    pub offset: i64,
    pub total: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FilesChange {
    pub r#ref: String,
    #[serde(deserialize_with = "nullable")]
    pub dirs: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub files: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ArchivedAgent {
    pub r#ref: String,
    pub project: String,
    pub name: String,
    pub title: String,
    pub task: String,
    pub ai: String,
    pub interface: String,
    pub branch: String,
    #[serde(rename = "baseRef")]
    pub base_ref: String,
    #[serde(rename = "baseCommit")]
    pub base_commit: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "archivedAt")]
    pub archived_at: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr: Option<PullRequest>,
    pub tokens: i64,
    #[serde(rename = "costUsd")]
    pub cost_usd: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DeleteArchivedRequest {
    #[serde(deserialize_with = "nullable")]
    pub refs: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DeleteArchivedResult {
    #[serde(deserialize_with = "nullable")]
    pub deleted: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub failed: BTreeMap<String, String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ContinueArchivedRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub message: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub ai: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CreateAgentRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub task: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub issue: i64,
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub branch: String,
    pub ai: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub interface: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub autonomous: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub effort: Option<String>,
    #[serde(rename = "contextWindow", skip_serializing_if = "Option::is_none")]
    pub context_window: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub from: String,
    #[serde(rename = "startAt", skip_serializing_if = "is_empty")]
    pub start_at: String,
    #[serde(rename = "claudeAccount", skip_serializing_if = "is_empty")]
    pub claude_account: String,
    #[serde(rename = "codexAccount", skip_serializing_if = "is_empty")]
    pub codex_account: String,
    #[serde(
        rename = "openCodeAccounts",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub open_code_accounts: BTreeMap<String, String>,
    #[serde(rename = "cursorAccount", skip_serializing_if = "is_empty")]
    pub cursor_account: String,
    #[serde(rename = "geminiAccount", skip_serializing_if = "is_empty")]
    pub gemini_account: String,
    #[serde(rename = "githubAccount", skip_serializing_if = "is_empty")]
    pub github_account: String,
    #[serde(rename = "noEnv", skip_serializing_if = "is_empty")]
    pub no_env: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub clean: bool,
    #[serde(rename = "catchUp", skip_serializing_if = "is_empty")]
    pub catch_up: bool,
    #[serde(rename = "finishNotice", skip_serializing_if = "is_empty")]
    pub finish_notice: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub goal: Option<GoalSpec>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub queue: Option<bool>,
    #[serde(skip_serializing_if = "is_empty")]
    pub size: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub connectors: Option<Vec<String>>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ForkRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub snapshot: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub checkpoint: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateAgentRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub title: Option<String>,
    #[serde(rename = "claudeAccount", skip_serializing_if = "Option::is_none")]
    pub claude_account: Option<String>,
    #[serde(rename = "githubAccount", skip_serializing_if = "Option::is_none")]
    pub github_account: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub interface: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Snapshot {
    pub name: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    pub head: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SnapshotRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub consistent: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RestoreRequest {
    pub snapshot: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Checkpoint {
    pub id: String,
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub turn: String,
    pub number: i64,
    pub prompt: String,
    pub commit: String,
    pub head: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RollbackRequest {
    pub checkpoint: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RollbackResult {
    pub saved: Checkpoint,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Base {
    pub snapshot: String,
    #[serde(rename = "savedFrom")]
    pub saved_from: String,
    #[serde(rename = "savedAt")]
    pub saved_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub image: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub tools: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub behind: Option<BaseBehind>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub previous: Option<Box<Base>>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BaseBehind {
    #[serde(rename = "imageFrom", skip_serializing_if = "is_empty")]
    pub image_from: String,
    #[serde(rename = "imageTo", skip_serializing_if = "is_empty")]
    pub image_to: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub changes: Vec<BaseImageChange>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub components: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub tools: Vec<BaseToolChange>,
    #[serde(rename = "toolsUnknown", skip_serializing_if = "is_empty")]
    pub tools_unknown: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BaseImageChange {
    pub version: String,
    pub what: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BaseToolChange {
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub from: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub to: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SaveBaseRequest {
    pub agent: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct StopAgentsRequest {
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub refs: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct StopAgentsResult {
    #[serde(deserialize_with = "nullable")]
    pub stopped: Vec<StoppedAgent>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub failed: Vec<StopAgentFailure>,
    #[serde(rename = "freedMemory")]
    pub freed_memory: i64,
    #[serde(rename = "freedCPU")]
    pub freed_cpu: f64,
    #[serde(rename = "hostMemoryBefore")]
    pub host_memory_before: i64,
    #[serde(rename = "hostMemoryAfter")]
    pub host_memory_after: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct StoppedAgent {
    pub r#ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    pub memory: i64,
    pub cpu: f64,
    pub working: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct StopAgentFailure {
    pub r#ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemoryHold {
    #[serde(skip_serializing_if = "is_empty")]
    pub paused: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub waiting: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub machine: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub ahead: i64,
    pub since: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub why: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub stalled: f64,
    #[serde(skip_serializing_if = "is_empty")]
    pub rate: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub available: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub total: i64,
    #[serde(rename = "swapFull", skip_serializing_if = "is_empty")]
    pub swap_full: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PressureStatus {
    pub some: f64,
    pub full: f64,
    #[serde(skip_serializing_if = "is_empty")]
    pub why: String,
    #[serde(deserialize_with = "nullable")]
    pub runs: Vec<HeavyRun>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HeavyRun {
    pub agent: String,
    pub key: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub command: String,
    pub state: String,
    pub since: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub placed: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Job {
    pub id: String,
    pub kind: String,
    pub target: String,
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub result: serde_json::Value,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "finishedAt", skip_serializing_if = "Option::is_none")]
    pub finished_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub ai: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub notice: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HostUsage {
    pub cpu: f64,
    pub cores: i64,
    #[serde(rename = "memUsed")]
    pub mem_used: i64,
    #[serde(rename = "memTotal")]
    pub mem_total: i64,
    #[serde(rename = "poolUsed")]
    pub pool_used: i64,
    #[serde(rename = "poolTotal")]
    pub pool_total: i64,
    #[serde(rename = "diskRead")]
    pub disk_read: i64,
    #[serde(rename = "diskWrite")]
    pub disk_write: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pressure: Option<HostPressure>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HostPressure {
    #[serde(rename = "ioSome")]
    pub io_some: f64,
    #[serde(rename = "ioFull")]
    pub io_full: f64,
    #[serde(rename = "memorySome")]
    pub memory_some: f64,
    #[serde(rename = "memoryFull")]
    pub memory_full: f64,
    pub stalling: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentUsage {
    pub r#ref: String,
    pub state: String,
    pub cpu: f64,
    pub memory: i64,
    pub processes: i64,
    #[serde(rename = "diskRead")]
    pub disk_read: i64,
    #[serde(rename = "diskWrite")]
    pub disk_write: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Usage {
    pub host: HostUsage,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<AgentUsage>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DiskUsageItem {
    pub label: String,
    pub bytes: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DiskUsageCategory {
    pub kind: String,
    pub label: String,
    pub bytes: i64,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub items: Vec<DiskUsageItem>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DiskUsage {
    pub total: i64,
    #[serde(deserialize_with = "nullable")]
    pub categories: Vec<DiskUsageCategory>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentDisk {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub machine: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub worktree: Option<i64>,
    #[serde(rename = "measuredAt")]
    pub measured_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct StoppedMachine {
    pub r#ref: String,
    pub project: String,
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    pub branch: String,
    #[serde(rename = "stoppedSince")]
    pub stopped_since: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub machine: Option<i64>,
    #[serde(skip_serializing_if = "is_empty")]
    pub remove: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub keep: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct StoppedMachines {
    pub days: i64,
    #[serde(deserialize_with = "nullable")]
    pub machines: Vec<StoppedMachine>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RemoveStoppedRequest {
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RemoveStoppedResult {
    #[serde(deserialize_with = "nullable")]
    pub removed: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub kept: Vec<StoppedMachine>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ToolCredential {
    pub agent: String,
    pub tool: String,
    pub running: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub variable: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub others: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DiskGuard {
    pub level: String,
    #[serde(deserialize_with = "nullable")]
    pub disks: Vec<DiskGuardDisk>,
    #[serde(deserialize_with = "nullable")]
    pub paused: Vec<String>,
    pub since: String,
    pub message: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DiskGuardDisk {
    pub label: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub path: String,
    pub free: i64,
    pub total: i64,
    pub floor: i64,
    pub level: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub advice: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemoryUsageAgent {
    pub r#ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    pub state: String,
    pub memory: i64,
    pub swap: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ZramUsage {
    #[serde(rename = "swapBytes")]
    pub swap_bytes: i64,
    #[serde(rename = "realBytes")]
    pub real_bytes: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemoryUsage {
    #[serde(rename = "hostTotal")]
    pub host_total: i64,
    #[serde(rename = "agentsUsed")]
    pub agents_used: i64,
    #[serde(rename = "otherUsed")]
    pub other_used: i64,
    #[serde(rename = "swapTotal")]
    pub swap_total: i64,
    #[serde(rename = "swapUsed")]
    pub swap_used: i64,
    #[serde(rename = "agentsSwap")]
    pub agents_swap: i64,
    pub used: i64,
    #[serde(rename = "fileCache")]
    pub file_cache: i64,
    pub leads: i64,
    pub agentbox: i64,
    pub kernel: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub zram: Option<ZramUsage>,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<MemoryUsageAgent>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HostMemory {
    pub total: i64,
    pub available: i64,
    pub cpus: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CPUUsageAgent {
    pub r#ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    pub state: String,
    pub cpu: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CPUUsage {
    #[serde(rename = "hostCPU")]
    pub host_cpu: f64,
    #[serde(rename = "hostCores")]
    pub host_cores: i64,
    #[serde(rename = "otherCPU")]
    pub other_cpu: f64,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<CPUUsageAgent>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TerminalTail {
    pub r#ref: String,
    pub window: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub command: String,
    pub busy: bool,
    #[serde(deserialize_with = "nullable")]
    pub lines: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TerminalTails {
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<TerminalTail>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ClaudeAccount {
    pub name: String,
    pub default: bool,
    #[serde(rename = "savedAt")]
    pub saved_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub valid: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GitHubAccount {
    pub name: String,
    pub default: bool,
    #[serde(rename = "savedAt")]
    pub saved_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub login: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AuthStatus {
    pub claude: bool,
    pub codex: bool,
    pub opencode: bool,
    pub cursor: bool,
    #[serde(rename = "cursorEmail", skip_serializing_if = "is_empty")]
    pub cursor_email: String,
    pub gemini: bool,
    #[serde(rename = "geminiEmail", skip_serializing_if = "is_empty")]
    pub gemini_email: String,
    #[serde(rename = "claudeAccounts", deserialize_with = "nullable")]
    pub claude_accounts: Vec<ClaudeAccount>,
    #[serde(rename = "codexAccounts", deserialize_with = "nullable")]
    pub codex_accounts: Vec<ToolAccount>,
    #[serde(rename = "openCodeAccounts", deserialize_with = "nullable")]
    pub open_code_accounts: Vec<ToolAccount>,
    #[serde(rename = "cursorAccounts", deserialize_with = "nullable")]
    pub cursor_accounts: Vec<ToolAccount>,
    #[serde(rename = "geminiAccounts", deserialize_with = "nullable")]
    pub gemini_accounts: Vec<ToolAccount>,
    pub github: bool,
    #[serde(rename = "githubAccounts", deserialize_with = "nullable")]
    pub github_accounts: Vec<GitHubAccount>,
    #[serde(rename = "githubUser", skip_serializing_if = "is_empty")]
    pub github_user: String,
    #[serde(rename = "githubError", skip_serializing_if = "is_empty")]
    pub github_error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Event {
    pub r#type: String,
    pub time: String,
    pub data: serde_json::Value,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct JobLogLine {
    pub job: String,
    pub n: i64,
    pub line: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentChange {
    pub r#ref: String,
    pub state: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub ip: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub removed: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub memory: Option<MemoryHold>,
    #[serde(rename = "waitingForImage", skip_serializing_if = "is_empty")]
    pub waiting_for_image: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub reclaimed: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Theme {
    pub appearance: String,
    pub available: bool,
    pub name: String,
    pub mode: String,
    pub background: String,
    pub surface: String,
    pub foreground: String,
    pub muted: String,
    pub accent: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateThemeRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub appearance: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateStatus {
    pub current: String,
    pub enabled: bool,
    pub channel: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub nightly: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub blocked: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub available: Option<UpdateAvailable>,
    #[serde(rename = "checkedAt", skip_serializing_if = "Option::is_none")]
    pub checked_at: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateAvailable {
    pub version: String,
    pub url: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateRelease {
    pub version: String,
    pub url: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub assets: Vec<ReleaseAsset>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReleaseAsset {
    pub name: String,
    pub url: String,
    pub size: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IncusStatus {
    pub answering: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub since: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub detail: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub restarted: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectChange {
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub removed: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PullsChange {
    pub project: String,
    pub github: String,
    #[serde(rename = "fetchedAt")]
    pub fetched_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub unchanged: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SelfInfo {
    pub r#ref: String,
    pub project: String,
    pub agent: String,
    pub branch: String,
    pub worktree: String,
    pub ip: String,
    pub state: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ApiError {
    pub error: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub code: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TerminalResize {
    pub cols: u16,
    pub rows: u16,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BrowserStatus {
    pub display: bool,
    pub running: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub version: String,
    #[serde(deserialize_with = "nullable")]
    pub pages: Vec<BrowserPage>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BrowserPage {
    pub id: String,
    pub url: String,
    pub title: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BrowserOpenRequest {
    pub url: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GPUReport {
    pub device: String,
    pub display: bool,
    pub renderer: String,
    pub accelerated: bool,
    #[serde(deserialize_with = "nullable")]
    pub vulkan: Vec<VulkanDevice>,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VulkanDevice {
    pub name: String,
    pub r#type: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMGPU {
    pub kind: String,
    pub node: String,
    pub reason: String,
    pub setup: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ElementPickRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub hint: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ElementPickResult {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub element: Option<ElementPick>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ElementPick {
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    pub selector: String,
    pub tag: String,
    pub html: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub text: String,
    #[serde(deserialize_with = "nullable")]
    pub styles: Vec<ElementStyle>,
    pub r#box: ElementBox,
    pub viewport: ElementSize,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub image: Option<ChatImageUpload>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ElementStyle {
    pub name: String,
    pub value: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ElementBox {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ElementSize {
    pub width: f64,
    pub height: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PreviewInfo {
    pub addr: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DesktopUse {
    pub r#ref: String,
    #[serde(rename = "usedAt")]
    pub used_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MediaItem {
    pub id: String,
    pub agent: String,
    #[serde(rename = "agentName", skip_serializing_if = "is_empty")]
    pub agent_name: String,
    #[serde(rename = "agentTitle", skip_serializing_if = "is_empty")]
    pub agent_title: String,
    #[serde(rename = "agentGone", skip_serializing_if = "is_empty")]
    pub agent_gone: bool,
    pub kind: String,
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub file: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub mime: String,
    pub size: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub sha256: String,
    pub source: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub text: String,
    pub meta: MediaMeta,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "expiresAt", skip_serializing_if = "Option::is_none")]
    pub expires_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub removed: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub unseen: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub favorite: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MediaMeta {
    #[serde(skip_serializing_if = "is_empty")]
    pub width: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub height: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub duration: f64,
    #[serde(skip_serializing_if = "is_empty")]
    pub target: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub entry: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tests: Option<TestCounts>,
    #[serde(skip_serializing_if = "is_empty")]
    pub recorded: f64,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub cuts: Vec<MediaCut>,
    #[serde(skip_serializing_if = "is_empty")]
    pub pair: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub side: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub raw: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MediaCut {
    pub from: f64,
    pub to: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Notification {
    pub id: String,
    pub kind: String,
    pub project: String,
    pub agent: String,
    pub r#ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub text: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub media: Option<MediaItem>,
    #[serde(skip_serializing_if = "is_empty")]
    pub question: String,
    pub at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub seen: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SeeNotificationsRequest {
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub ids: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub media: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub all: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub kind: String,
    #[serde(rename = "allMedia", skip_serializing_if = "is_empty")]
    pub all_media: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SeeNotificationsResult {
    pub seen: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TestCounts {
    pub passed: i64,
    pub failed: i64,
    pub skipped: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ScreenshotRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub target: String,
    #[serde(rename = "fullPage", skip_serializing_if = "is_empty")]
    pub full_page: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub pair: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub side: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RecordRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub target: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub input: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(rename = "limitSeconds", skip_serializing_if = "is_empty")]
    pub limit_seconds: i64,
    #[serde(rename = "noTrim", skip_serializing_if = "is_empty")]
    pub no_trim: bool,
    #[serde(rename = "wholeDisplay", skip_serializing_if = "is_empty")]
    pub whole_display: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub pair: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub side: String,
    #[serde(rename = "noEdit", skip_serializing_if = "is_empty")]
    pub no_edit: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RecordingStatus {
    pub recording: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub target: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub input: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub source: String,
    #[serde(rename = "startedAt", skip_serializing_if = "Option::is_none")]
    pub started_at: Option<String>,
    #[serde(rename = "limitSeconds", skip_serializing_if = "is_empty")]
    pub limit_seconds: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Timeline {
    #[serde(deserialize_with = "nullable")]
    pub steps: Vec<TimelineStep>,
    pub start: String,
    pub end: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub working: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TimelineStep {
    pub kind: String,
    pub title: String,
    pub start: String,
    pub end: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub calls: i64,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub lines: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub paths: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub added: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub removed: i64,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub media: Vec<MediaItem>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub pages: Vec<TimelinePage>,
    pub item: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TimelinePage {
    #[serde(skip_serializing_if = "is_empty")]
    pub id: String,
    pub title: String,
    pub item: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AddMediaRequest {
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub pair: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub side: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct NoteRequest {
    pub text: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LogsRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub service: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub since: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub terminal: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub window: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub android: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub package: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ExportRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub dir: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ExportResult {
    pub dir: String,
    pub items: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DeleteMediaRequest {
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub ids: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub all: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub query: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DeleteMediaResult {
    pub deleted: i64,
    pub bytes: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateMediaRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub favorite: Option<bool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MediaPage {
    #[serde(deserialize_with = "nullable")]
    pub items: Vec<MediaItem>,
    #[serde(skip_serializing_if = "is_empty")]
    pub next: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct NotificationPage {
    #[serde(deserialize_with = "nullable")]
    pub items: Vec<Notification>,
    #[serde(skip_serializing_if = "is_empty")]
    pub next: String,
    pub total: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReviewPage {
    #[serde(deserialize_with = "nullable")]
    pub items: Vec<ReviewItem>,
    #[serde(skip_serializing_if = "is_empty")]
    pub next: String,
    pub total: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WorkPage {
    #[serde(deserialize_with = "nullable")]
    pub items: Vec<WorkItem>,
    #[serde(skip_serializing_if = "is_empty")]
    pub next: String,
    pub total: i64,
    #[serde(deserialize_with = "nullable")]
    pub counts: BTreeMap<String, i64>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WorkItem {
    pub item: ReviewItem,
    #[serde(skip_serializing_if = "is_empty")]
    pub task: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub past: bool,
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub report: Option<AgentReport>,
    #[serde(skip_serializing_if = "is_empty")]
    pub screenshots: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub recordings: i64,
    pub at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MediaCounts {
    pub total: i64,
    pub bytes: i64,
    #[serde(deserialize_with = "nullable")]
    pub kinds: BTreeMap<String, i64>,
    pub favorites: i64,
    pub unseen: i64,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<MediaAgentCount>,
    #[serde(deserialize_with = "nullable")]
    pub projects: Vec<MediaProjectCount>,
    pub matching: i64,
    #[serde(rename = "matchingBytes")]
    pub matching_bytes: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MediaAgentCount {
    pub agent: String,
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub gone: bool,
    pub count: i64,
    pub bytes: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MediaProjectCount {
    pub project: String,
    pub count: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SetupCheck {
    pub id: String,
    pub title: String,
    pub status: String,
    pub required: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub detail: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub fix: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub job: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SetupStatus {
    pub ready: bool,
    #[serde(deserialize_with = "nullable")]
    pub checks: Vec<SetupCheck>,
    pub image: ImageBuild,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImageComponents {
    pub android: bool,
    pub codex: bool,
    pub opencode: bool,
    #[serde(rename = "devCaches")]
    pub dev_caches: bool,
    pub incus: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImageBuild {
    pub version: String,
    pub components: ImageComponents,
    pub installed: ImageComponents,
    #[serde(deserialize_with = "nullable")]
    pub downloads: Vec<ImageDownload>,
    pub hint: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImageDownload {
    pub name: String,
    pub purpose: String,
    pub mb: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub option: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BuildImageRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub android: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub codex: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub opencode: Option<bool>,
    #[serde(rename = "devCaches", skip_serializing_if = "Option::is_none")]
    pub dev_caches: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub incus: Option<bool>,
    #[serde(skip_serializing_if = "is_empty")]
    pub local: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImageStatus {
    pub ready: bool,
    pub snapshot: String,
    pub build: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub job: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub step: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub progress: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    pub waiting: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ClaudeTokenRequest {
    pub token: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ClaudeLoginRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ClaudeLogin {
    pub job: String,
    pub account: String,
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub url: String,
    #[serde(rename = "codeUrl", skip_serializing_if = "is_empty")]
    pub code_url: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ClaudeLoginCodeRequest {
    pub code: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RenameClaudeAccountRequest {
    pub name: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RenamedClaudeAccount {
    pub old: String,
    pub name: String,
    #[serde(deserialize_with = "nullable")]
    pub projects: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GitHubTokenRequest {
    pub token: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GitHubLoginRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GitHubLogin {
    pub job: String,
    pub account: String,
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(rename = "userCode", skip_serializing_if = "is_empty")]
    pub user_code: String,
    #[serde(rename = "verificationUri", skip_serializing_if = "is_empty")]
    pub verification_uri: String,
    #[serde(rename = "expiresAt", skip_serializing_if = "Option::is_none")]
    pub expires_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub outcome: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub user: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GitHubRepo {
    #[serde(rename = "fullName")]
    pub full_name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub description: String,
    pub private: bool,
    #[serde(rename = "pushedAt", skip_serializing_if = "is_empty")]
    pub pushed_at: String,
    #[serde(rename = "cloneUrl")]
    pub clone_url: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CursorKeyRequest {
    #[serde(rename = "apiKey")]
    pub api_key: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CursorKeyResponse {
    #[serde(skip_serializing_if = "is_empty")]
    pub email: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CursorLogin {
    pub state: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub email: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CursorLoginRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ToolAccount {
    pub tool: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub provider: String,
    pub name: String,
    pub default: bool,
    #[serde(rename = "savedAt")]
    pub saved_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub email: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub expired: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub expiry: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ToolAccountRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub provider: String,
    #[serde(rename = "apiKey")]
    pub api_key: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RenameToolAccountRequest {
    pub name: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RenamedToolAccount {
    pub tool: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub provider: String,
    pub old: String,
    pub name: String,
    #[serde(deserialize_with = "nullable")]
    pub projects: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RenameGitHubAccountRequest {
    pub name: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RenamedGitHubAccount {
    pub old: String,
    pub name: String,
    #[serde(deserialize_with = "nullable")]
    pub projects: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Secret {
    pub name: String,
    pub scope: String,
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub warning: String,
    #[serde(rename = "leadManaged")]
    pub lead_managed: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SetSecretRequest {
    pub value: String,
    #[serde(rename = "leadManaged", skip_serializing_if = "Option::is_none")]
    pub lead_managed: Option<bool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SecretSettingsRequest {
    #[serde(rename = "leadManaged")]
    pub lead_managed: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Skill {
    pub name: String,
    pub description: String,
    pub source: String,
    pub enabled: bool,
    #[serde(deserialize_with = "nullable")]
    pub overrides: BTreeMap<String, bool>,
    #[serde(rename = "userInvocable")]
    pub user_invocable: bool,
    #[serde(rename = "fileCount")]
    pub file_count: i64,
    pub size: i64,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub active: Option<bool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SkillFile {
    pub path: String,
    pub size: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub binary: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub content: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SkillDetail {
    pub name: String,
    pub description: String,
    pub source: String,
    pub enabled: bool,
    #[serde(deserialize_with = "nullable")]
    pub overrides: BTreeMap<String, bool>,
    #[serde(rename = "userInvocable")]
    pub user_invocable: bool,
    #[serde(rename = "fileCount")]
    pub file_count: i64,
    pub size: i64,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub active: Option<bool>,
    #[serde(deserialize_with = "nullable")]
    pub files: Vec<SkillFile>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SaveSkillRequest {
    pub content: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub enabled: Option<bool>,
    #[serde(skip_serializing_if = "is_empty")]
    pub project: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateSkillRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub enabled: Option<bool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SkillOverrideRequest {
    pub r#override: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ScanSkillsRequest {
    pub source: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SkillCandidate {
    pub name: String,
    pub description: String,
    pub origin: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub plugin: String,
    pub source: String,
    pub files: i64,
    pub size: i64,
    pub content: String,
    pub exists: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub problem: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImportSkillsRequest {
    pub source: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub names: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub project: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BrowserCookies {
    pub imported: bool,
    #[serde(deserialize_with = "nullable")]
    pub domains: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub sites: Vec<CookieDomain>,
    pub cookies: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub format: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub source: String,
    #[serde(rename = "importedAt", skip_serializing_if = "Option::is_none")]
    pub imported_at: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BrowserCookiesPreviewRequest {
    pub export: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BrowserCookiesPreview {
    pub format: String,
    pub cookies: i64,
    #[serde(deserialize_with = "nullable")]
    pub domains: Vec<CookieDomain>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CookieDomain {
    pub domain: String,
    pub cookies: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImportBrowserCookiesRequest {
    pub export: String,
    #[serde(deserialize_with = "nullable")]
    pub domains: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BrowserProfile {
    pub id: String,
    pub browser: String,
    #[serde(rename = "browserName")]
    pub browser_name: String,
    pub engine: String,
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub keyring: String,
    #[serde(rename = "appBound", skip_serializing_if = "is_empty")]
    pub app_bound: bool,
    #[serde(rename = "profileDir", skip_serializing_if = "is_empty")]
    pub profile_dir: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct BrowserProfiles {
    #[serde(deserialize_with = "nullable")]
    pub profiles: Vec<BrowserProfile>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImportFromBrowserRequest {
    #[serde(rename = "profileId")]
    pub profile_id: String,
    #[serde(rename = "keyringSecret")]
    pub keyring_secret: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImportedCookie {
    pub domain: String,
    pub name: String,
    pub value: String,
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub expires: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub secure: bool,
    #[serde(rename = "httpOnly", skip_serializing_if = "is_empty")]
    pub http_only: bool,
    #[serde(rename = "sameSite", skip_serializing_if = "is_empty")]
    pub same_site: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImportDecryptedCookiesRequest {
    pub source: String,
    #[serde(deserialize_with = "nullable")]
    pub cookies: Vec<ImportedCookie>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SnapRequest {
    pub image: ChatImageUpload,
    #[serde(skip_serializing_if = "is_empty")]
    pub app: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub desktop: String,
    pub window: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub accessibility: String,
    #[serde(rename = "takenAt")]
    pub taken_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub project: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Snap {
    pub id: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub app: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub desktop: String,
    pub window: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub accessibility: String,
    #[serde(rename = "mimeType")]
    pub mime_type: String,
    pub size: i64,
    #[serde(rename = "takenAt")]
    pub taken_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub project: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SnapSendRequest {
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub note: String,
    pub accessibility: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TransferCategory {
    pub kind: String,
    pub items: i64,
    pub size: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceExportPreview {
    #[serde(deserialize_with = "nullable")]
    pub projects: Vec<InstanceExportProjectPreview>,
    #[serde(deserialize_with = "nullable")]
    pub logins: Vec<InstanceExportLoginPreview>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceExportProjectPreview {
    pub name: String,
    #[serde(rename = "displayName")]
    pub display_name: String,
    #[serde(deserialize_with = "nullable")]
    pub categories: Vec<TransferCategory>,
    pub remote: String,
    pub branch: String,
    #[serde(rename = "bundleSize")]
    pub bundle_size: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceExportLoginPreview {
    pub kind: String,
    #[serde(deserialize_with = "nullable")]
    pub accounts: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceExportRequest {
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub passphrase: String,
    #[serde(rename = "noEncrypt", skip_serializing_if = "is_empty")]
    pub no_encrypt: bool,
    #[serde(deserialize_with = "nullable")]
    pub projects: Vec<InstanceExportProjectChoice>,
    #[serde(deserialize_with = "nullable")]
    pub logins: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceExportProjectChoice {
    pub name: String,
    #[serde(deserialize_with = "nullable")]
    pub categories: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub bundle: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceExportResult {
    pub path: String,
    pub size: i64,
    pub encrypted: bool,
    pub entries: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceImportInspectRequest {
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub passphrase: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceImportContents {
    pub path: String,
    pub encrypted: bool,
    pub schema: i64,
    pub app: String,
    pub id: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(deserialize_with = "nullable")]
    pub projects: Vec<InstanceImportProjectContents>,
    #[serde(deserialize_with = "nullable")]
    pub logins: Vec<TransferCategory>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceImportProjectContents {
    pub name: String,
    #[serde(rename = "displayName")]
    pub display_name: String,
    pub exists: bool,
    #[serde(deserialize_with = "nullable")]
    pub categories: Vec<TransferCategory>,
    pub remote: String,
    pub branch: String,
    pub bundled: bool,
    pub path: String,
    pub plan: String,
    #[serde(rename = "planError", skip_serializing_if = "is_empty")]
    pub plan_error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceImportRequest {
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub passphrase: String,
    #[serde(deserialize_with = "nullable")]
    pub projects: Vec<InstanceImportProjectChoice>,
    #[serde(deserialize_with = "nullable")]
    pub logins: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceImportProjectChoice {
    pub name: String,
    #[serde(deserialize_with = "nullable")]
    pub categories: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub path: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceImportReport {
    #[serde(deserialize_with = "nullable")]
    pub entries: Vec<InstanceImportEntryReport>,
    pub added: i64,
    pub skipped: i64,
    pub conflicted: i64,
    #[serde(rename = "projectsAdded", deserialize_with = "nullable")]
    pub projects_added: Vec<String>,
    #[serde(rename = "reportPath")]
    pub report_path: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct InstanceImportEntryReport {
    pub id: String,
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub project: String,
    pub added: i64,
    pub skipped: i64,
    pub conflicted: i64,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub notes: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Connector {
    pub name: String,
    pub scope: String,
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    pub url: String,
    pub auth: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub secret: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub header: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub scheme: String,
    pub enabled: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub r#override: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub overrides: BTreeMap<String, bool>,
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub issuer: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub scopes: String,
    #[serde(rename = "expiresAt", skip_serializing_if = "Option::is_none")]
    pub expires_at: Option<String>,
    #[serde(rename = "connectedAt", skip_serializing_if = "Option::is_none")]
    pub connected_at: Option<String>,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub removed: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SetConnectorRequest {
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub auth: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub secret: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub header: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub scheme: String,
    #[serde(rename = "secretValue", skip_serializing_if = "is_empty")]
    pub secret_value: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub enabled: Option<bool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ConnectorOverrideRequest {
    pub r#override: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ConnectResult {
    #[serde(rename = "authorizationUrl")]
    pub authorization_url: String,
    #[serde(rename = "redirectUri")]
    pub redirect_uri: String,
    #[serde(rename = "expiresAt")]
    pub expires_at: String,
    pub connector: Connector,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SelfConnector {
    pub name: String,
    pub url: String,
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ConnectorRequest {
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub url: String,
    pub reason: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ScanConnectorsRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub project: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ConnectorScan {
    #[serde(deserialize_with = "nullable")]
    pub candidates: Vec<ConnectorCandidate>,
    #[serde(deserialize_with = "nullable")]
    pub unreadable: Vec<ConnectorUnreadable>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ConnectorUnreadable {
    pub tool: String,
    pub path: String,
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ConnectorCandidate {
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub url: String,
    pub transport: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub command: String,
    #[serde(deserialize_with = "nullable")]
    pub sources: Vec<ConnectorSource>,
    #[serde(skip_serializing_if = "is_empty")]
    pub header: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub scheme: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub secret: String,
    #[serde(rename = "needsValue", skip_serializing_if = "is_empty")]
    pub needs_value: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub variable: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub existing: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub problem: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ConnectorSource {
    pub tool: String,
    pub name: String,
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub project: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImportConnectorsRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub project: String,
    #[serde(deserialize_with = "nullable")]
    pub servers: Vec<ImportConnector>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImportConnector {
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub value: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImportConnectorsResult {
    #[serde(deserialize_with = "nullable")]
    pub added: Vec<Connector>,
    #[serde(deserialize_with = "nullable")]
    pub failed: Vec<ImportConnectorFailure>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ImportConnectorFailure {
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PullRequest {
    pub number: i64,
    pub title: String,
    pub state: String,
    pub checks: String,
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub draft: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub additions: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub deletions: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub comments: i64,
    #[serde(rename = "updatedAt", skip_serializing_if = "Option::is_none")]
    pub updated_at: Option<String>,
    #[serde(rename = "baseBranch", skip_serializing_if = "is_empty")]
    pub base_branch: String,
    #[serde(rename = "headBranch", skip_serializing_if = "is_empty")]
    pub head_branch: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub fork: bool,
    #[serde(rename = "headSha", skip_serializing_if = "is_empty")]
    pub head_sha: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub author: String,
    #[serde(rename = "authorAvatar", skip_serializing_if = "is_empty")]
    pub author_avatar: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub conflict: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub review: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub watched: bool,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub labels: Vec<Label>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Label {
    pub name: String,
    pub color: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub description: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectLabels {
    #[serde(deserialize_with = "nullable")]
    pub labels: Vec<Label>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct EditLabelsRequest {
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub add: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub remove: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GitHubError {
    pub kind: String,
    #[serde(rename = "resetAt", skip_serializing_if = "Option::is_none")]
    pub reset_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub message: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub login: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub repo: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectPullRequests {
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub github: String,
    #[serde(rename = "githubAccount", skip_serializing_if = "is_empty")]
    pub github_account: String,
    #[serde(rename = "githubLogin", skip_serializing_if = "is_empty")]
    pub github_login: String,
    #[serde(rename = "githubError", skip_serializing_if = "Option::is_none")]
    pub github_error: Option<GitHubError>,
    #[serde(rename = "pullRequests", deserialize_with = "nullable")]
    pub pull_requests: Vec<PullRequest>,
    #[serde(rename = "noOrigin", skip_serializing_if = "is_empty")]
    pub no_origin: bool,
    #[serde(rename = "nonGitHubRemote", skip_serializing_if = "is_empty")]
    pub non_git_hub_remote: String,
    #[serde(rename = "canMerge", skip_serializing_if = "is_empty")]
    pub can_merge: bool,
    #[serde(rename = "canMergeKnown", skip_serializing_if = "is_empty")]
    pub can_merge_known: bool,
    #[serde(
        rename = "mergeMethods",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub merge_methods: Vec<String>,
    #[serde(rename = "fetchedAt", skip_serializing_if = "Option::is_none")]
    pub fetched_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub refreshing: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Issue {
    pub number: i64,
    pub title: String,
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub author: String,
    #[serde(rename = "authorAvatar", skip_serializing_if = "is_empty")]
    pub author_avatar: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub labels: Vec<Label>,
    #[serde(skip_serializing_if = "is_empty")]
    pub comments: i64,
    #[serde(rename = "createdAt", skip_serializing_if = "Option::is_none")]
    pub created_at: Option<String>,
    #[serde(rename = "updatedAt", skip_serializing_if = "Option::is_none")]
    pub updated_at: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub handed: Option<IssueHandoff>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IssueHandoff {
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub job: String,
    #[serde(rename = "agentTitle", skip_serializing_if = "is_empty")]
    pub agent_title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub retired: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub auto: bool,
    pub at: String,
    #[serde(rename = "pullRequest", skip_serializing_if = "Option::is_none")]
    pub pull_request: Option<IssuePullRequest>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IssuePullRequest {
    pub number: i64,
    pub url: String,
    pub state: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectIssues {
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub github: String,
    #[serde(rename = "githubAccount", skip_serializing_if = "is_empty")]
    pub github_account: String,
    #[serde(rename = "githubError", skip_serializing_if = "Option::is_none")]
    pub github_error: Option<GitHubError>,
    #[serde(rename = "noOrigin", skip_serializing_if = "is_empty")]
    pub no_origin: bool,
    #[serde(rename = "nonGitHubRemote", skip_serializing_if = "is_empty")]
    pub non_git_hub_remote: String,
    #[serde(deserialize_with = "nullable")]
    pub issues: Vec<Issue>,
    #[serde(rename = "pickUpIssues")]
    pub pick_up_issues: bool,
    pub label: String,
    #[serde(rename = "fetchedAt", skip_serializing_if = "Option::is_none")]
    pub fetched_at: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MergePullRequestRequest {
    pub method: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub confirmed: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MergeWhenGreenRequest {
    pub method: String,
    pub agent: String,
    pub fingerprint: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MergeWhenGreen {
    pub number: i64,
    pub method: String,
    pub at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(rename = "failedAt", skip_serializing_if = "Option::is_none")]
    pub failed_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub waiting: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub detail: String,
    #[serde(rename = "fixRounds", skip_serializing_if = "is_empty")]
    pub fix_rounds: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PullRequestDetail {
    pub number: i64,
    pub title: String,
    pub state: String,
    pub checks: String,
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub draft: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub additions: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub deletions: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub comments: i64,
    #[serde(rename = "updatedAt", skip_serializing_if = "Option::is_none")]
    pub updated_at: Option<String>,
    #[serde(rename = "baseBranch", skip_serializing_if = "is_empty")]
    pub base_branch: String,
    #[serde(rename = "headBranch", skip_serializing_if = "is_empty")]
    pub head_branch: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub fork: bool,
    #[serde(rename = "headSha", skip_serializing_if = "is_empty")]
    pub head_sha: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub author: String,
    #[serde(rename = "authorAvatar", skip_serializing_if = "is_empty")]
    pub author_avatar: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub conflict: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub review: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub watched: bool,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub labels: Vec<Label>,
    pub body: String,
    #[serde(rename = "checkRuns", deserialize_with = "nullable")]
    pub check_runs: Vec<PullCheck>,
    #[serde(rename = "changedFiles")]
    pub changed_files: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PullCheck {
    pub name: String,
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub conclusion: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub url: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PullRequestFiles {
    #[serde(deserialize_with = "nullable")]
    pub files: Vec<PullFile>,
    #[serde(skip_serializing_if = "is_empty")]
    pub truncated: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PullFile {
    pub path: String,
    #[serde(rename = "previousPath", skip_serializing_if = "is_empty")]
    pub previous_path: String,
    pub status: String,
    pub additions: i64,
    pub deletions: i64,
    #[serde(rename = "hasDiff")]
    pub has_diff: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PullFileDiff {
    pub path: String,
    pub patch: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentChanges {
    pub files: i64,
    pub insertions: i64,
    pub deletions: i64,
    pub dirty: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RetireAdvice {
    pub safe: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub reason: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub branch: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FleetAgent {
    pub r#ref: String,
    pub project: String,
    pub name: String,
    pub title: String,
    pub instance: String,
    pub ai: String,
    pub autonomous: bool,
    pub branch: String,
    #[serde(rename = "baseRef")]
    pub base_ref: String,
    #[serde(rename = "baseCommit")]
    pub base_commit: String,
    pub worktree: String,
    pub source: String,
    #[serde(rename = "claudeAccount")]
    pub claude_account: String,
    #[serde(rename = "codexAccount", skip_serializing_if = "is_empty")]
    pub codex_account: String,
    #[serde(
        rename = "openCodeAccounts",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub open_code_accounts: BTreeMap<String, String>,
    #[serde(rename = "cursorAccount", skip_serializing_if = "is_empty")]
    pub cursor_account: String,
    #[serde(rename = "geminiAccount", skip_serializing_if = "is_empty")]
    pub gemini_account: String,
    #[serde(rename = "githubAccount")]
    pub github_account: String,
    pub interface: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub chat: String,
    #[serde(rename = "leadSession", skip_serializing_if = "is_empty")]
    pub lead_session: String,
    #[serde(rename = "stalledSince", skip_serializing_if = "Option::is_none")]
    pub stalled_since: Option<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub background: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub outcome: String,
    #[serde(rename = "outcomeNote", skip_serializing_if = "is_empty")]
    pub outcome_note: String,
    #[serde(rename = "outcomeUntil", skip_serializing_if = "Option::is_none")]
    pub outcome_until: Option<String>,
    #[serde(rename = "changesRequested", skip_serializing_if = "is_empty")]
    pub changes_requested: String,
    pub state: String,
    #[serde(rename = "waitingForImage", skip_serializing_if = "is_empty")]
    pub waiting_for_image: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub memory: Option<MemoryHold>,
    #[serde(skip_serializing_if = "is_empty")]
    pub reclaimed: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub goal: Option<AgentGoal>,
    pub ip: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "sessionTopic", skip_serializing_if = "is_empty")]
    pub session_topic: String,
    pub changes: AgentChanges,
    pub media: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr: Option<PullRequest>,
    #[serde(skip_serializing_if = "is_empty")]
    pub busy: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub idle: bool,
    #[serde(rename = "lastActive", skip_serializing_if = "Option::is_none")]
    pub last_active: Option<String>,
    pub retire: RetireAdvice,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Fleet {
    pub project: String,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<FleetAgent>,
    #[serde(deserialize_with = "nullable")]
    pub creating: Vec<Job>,
    pub idle: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub github: String,
    #[serde(rename = "githubAccount", skip_serializing_if = "is_empty")]
    pub github_account: String,
    #[serde(rename = "githubError", skip_serializing_if = "Option::is_none")]
    pub github_error: Option<GitHubError>,
    #[serde(rename = "pullsFetchedAt", skip_serializing_if = "Option::is_none")]
    pub pulls_fetched_at: Option<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub remote: Vec<RemoteAgent>,
    #[serde(rename = "pullsRefreshing", skip_serializing_if = "is_empty")]
    pub pulls_refreshing: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RemoteAgent {
    pub machine: String,
    #[serde(rename = "machineId")]
    pub machine_id: String,
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub branch: String,
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub summary: String,
    #[serde(rename = "prUrl", skip_serializing_if = "is_empty")]
    pub pr_url: String,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Machine {
    pub id: String,
    pub name: String,
    pub online: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub hostname: String,
    #[serde(rename = "hasProject")]
    pub has_project: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RemoteAgentsChange {
    pub project: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RetireRequest {
    pub how: String,
    #[serde(rename = "idleFor", skip_serializing_if = "is_empty")]
    pub idle_for: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub agents: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub force: bool,
    #[serde(rename = "dryRun", skip_serializing_if = "is_empty")]
    pub dry_run: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RetiredAgent {
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub branch: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub reason: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RetireResult {
    pub how: String,
    #[serde(rename = "dryRun", skip_serializing_if = "is_empty")]
    pub dry_run: bool,
    #[serde(deserialize_with = "nullable")]
    pub retired: Vec<RetiredAgent>,
    #[serde(deserialize_with = "nullable")]
    pub skipped: Vec<RetiredAgent>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Question {
    pub id: String,
    pub project: String,
    pub agent: String,
    pub r#ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub kind: String,
    #[serde(rename = "secretName", skip_serializing_if = "is_empty")]
    pub secret_name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub connector: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub url: String,
    pub question: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub context: String,
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub answer: String,
    #[serde(rename = "answeredBy", skip_serializing_if = "is_empty")]
    pub answered_by: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub escalation: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "answeredAt", skip_serializing_if = "Option::is_none")]
    pub answered_at: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AskRequest {
    pub question: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub context: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AnswerQuestionRequest {
    pub answer: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct EscalateQuestionRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub why: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CredentialRequest {
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    pub reason: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AnswerCredentialRequest {
    #[serde(rename = "githubAccount", skip_serializing_if = "is_empty")]
    pub github_account: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub value: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub connector: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub refuse: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub reason: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentEvent {
    pub id: String,
    pub project: String,
    pub agent: String,
    pub r#ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub summary: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub cut: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub changes: Option<AgentChanges>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr: Option<PullRequest>,
    #[serde(skip_serializing_if = "is_empty")]
    pub outcome: String,
    #[serde(rename = "resumeAt", skip_serializing_if = "Option::is_none")]
    pub resume_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub question: String,
    pub at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReviewItem {
    pub project: String,
    pub agent: String,
    pub r#ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub branch: String,
    #[serde(rename = "baseRef", skip_serializing_if = "is_empty")]
    pub base_ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub ai: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub summary: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub changes: Option<AgentChanges>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr: Option<PullRequest>,
    #[serde(rename = "finishedAt")]
    pub finished_at: String,
    pub fingerprint: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub verdict: String,
    #[serde(rename = "reviewedAt", skip_serializing_if = "Option::is_none")]
    pub reviewed_at: Option<String>,
    #[serde(rename = "machineKept", skip_serializing_if = "is_empty")]
    pub machine_kept: bool,
    #[serde(rename = "mergeWhenGreen", skip_serializing_if = "Option::is_none")]
    pub merge_when_green: Option<MergeWhenGreen>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReviewRequest {
    pub verdict: String,
    pub fingerprint: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RequestChangesRequest {
    pub text: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub fingerprint: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub notes: Vec<i64>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReviewNote {
    pub id: i64,
    pub path: String,
    pub side: String,
    pub start: i64,
    pub end: i64,
    pub code: String,
    pub body: String,
    pub status: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "sentAt", skip_serializing_if = "Option::is_none")]
    pub sent_at: Option<String>,
    #[serde(rename = "resolvedAt", skip_serializing_if = "Option::is_none")]
    pub resolved_at: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AddReviewNoteRequest {
    pub path: String,
    pub side: String,
    pub start: i64,
    pub end: i64,
    pub code: String,
    pub body: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct EditReviewNoteRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub body: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub resolved: Option<bool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AndroidStatus {
    pub available: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub problem: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub sdk: String,
    #[serde(deserialize_with = "nullable")]
    pub images: Vec<String>,
    pub running: bool,
    pub booted: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub image: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub device: String,
    pub gpu: bool,
    #[serde(rename = "gpuProblem", skip_serializing_if = "is_empty")]
    pub gpu_problem: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AndroidStartRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub image: String,
    #[serde(rename = "memoryMB", skip_serializing_if = "is_empty")]
    pub memory_mb: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub cores: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AndroidInstallRequest {
    pub path: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AndroidInstallResult {
    pub output: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IOSStatus {
    pub available: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub problem: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub xcode: String,
    #[serde(deserialize_with = "nullable")]
    pub devices: Vec<IOSDevice>,
    #[serde(skip_serializing_if = "is_empty")]
    pub device: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IOSDevice {
    pub udid: String,
    pub name: String,
    pub runtime: String,
    pub state: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IOSDeviceRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub device: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IOSInput {
    #[serde(skip_serializing_if = "is_empty")]
    pub device: String,
    pub r#type: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub phase: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub x: f64,
    #[serde(skip_serializing_if = "is_empty")]
    pub y: f64,
    #[serde(skip_serializing_if = "is_empty")]
    pub x2: f64,
    #[serde(skip_serializing_if = "is_empty")]
    pub y2: f64,
    #[serde(skip_serializing_if = "is_empty")]
    pub pixels: bool,
    #[serde(rename = "durationMs", skip_serializing_if = "is_empty")]
    pub duration_ms: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub key: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub modifiers: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub text: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub button: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IOSInstallRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub device: String,
    pub path: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IOSLaunchRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub device: String,
    #[serde(rename = "bundleId")]
    pub bundle_id: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub args: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub terminate: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IOSOpenURLRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub device: String,
    pub url: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IOSResult {
    #[serde(skip_serializing_if = "is_empty")]
    pub output: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubUser {
    pub id: String,
    pub email: String,
    pub name: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubInfo {
    pub version: String,
    pub claimed: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub passwords: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubLoginRequest {
    pub email: String,
    pub password: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub label: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubSetupRequest {
    pub secret: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub label: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubSession {
    pub token: String,
    pub user: HubUser,
    #[serde(rename = "expiresAt")]
    pub expires_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubPairRequest {
    pub kind: String,
    pub label: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub hostname: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub version: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubPairing {
    pub secret: String,
    pub code: String,
    #[serde(rename = "approveURL")]
    pub approve_url: String,
    #[serde(rename = "expiresAt")]
    pub expires_at: String,
    pub interval: i64,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub qr: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubPairTokenRequest {
    pub secret: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubPairResult {
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub session: Option<HubSession>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub environment: Option<HubEnvironmentToken>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubPairingInfo {
    pub code: String,
    pub kind: String,
    pub label: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub hostname: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub version: String,
    pub address: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "expiresAt")]
    pub expires_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubDevice {
    pub id: String,
    pub label: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "lastSeenAt", skip_serializing_if = "is_empty")]
    pub last_seen_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub current: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubEnvironment {
    pub id: String,
    pub name: String,
    pub online: bool,
    #[serde(rename = "connectedAt", skip_serializing_if = "is_empty")]
    pub connected_at: String,
    #[serde(rename = "lastSeenAt", skip_serializing_if = "is_empty")]
    pub last_seen_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub version: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub hostname: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub transport: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub address: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubCreateEnvironmentRequest {
    pub name: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HubEnvironmentToken {
    pub environment: HubEnvironment,
    pub token: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RemoteStatus {
    pub configured: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub hub: String,
    pub connected: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub since: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pairing: Option<RemotePairing>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RemoteConnectRequest {
    pub hub: String,
    pub token: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RemotePairRequest {
    pub hub: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RemotePairing {
    pub hub: String,
    pub code: String,
    #[serde(rename = "approveURL")]
    pub approve_url: String,
    #[serde(rename = "expiresAt")]
    pub expires_at: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub qr: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UsageStatsPending {
    pub on: bool,
    #[serde(rename = "usageUrl")]
    pub usage_url: String,
    pub usage: String,
    #[serde(rename = "eventsUrl")]
    pub events_url: String,
    pub events: String,
    #[serde(rename = "eventsWaiting")]
    pub events_waiting: i64,
    pub health: String,
    pub setup: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LANStatus {
    pub enabled: bool,
    pub port: i64,
    pub listening: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub warning: String,
    #[serde(deserialize_with = "nullable")]
    pub urls: Vec<String>,
    pub tunnel: LANTunnel,
    #[serde(rename = "webVersion", skip_serializing_if = "is_empty")]
    pub web_version: String,
    #[serde(deserialize_with = "nullable")]
    pub phones: Vec<LANPhone>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LANTunnel {
    pub enabled: bool,
    pub mode: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub hub: String,
    #[serde(rename = "hubHasNoAddresses", skip_serializing_if = "is_empty")]
    pub hub_has_no_addresses: bool,
    pub named: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub hostname: String,
    #[serde(rename = "namedHostname", skip_serializing_if = "is_empty")]
    pub named_hostname: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub transport: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub tailnet: String,
    pub state: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    pub origin: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LANPhone {
    pub id: String,
    pub name: String,
    pub paired: String,
    #[serde(rename = "lastSeen", skip_serializing_if = "is_empty")]
    pub last_seen: String,
    #[serde(rename = "lastAddr", skip_serializing_if = "is_empty")]
    pub last_addr: String,
    #[serde(deserialize_with = "nullable")]
    pub scopes: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PhonePush {
    #[serde(skip_serializing_if = "is_empty")]
    pub token: String,
    pub registered: bool,
    #[serde(deserialize_with = "nullable")]
    pub kinds: Vec<String>,
    #[serde(rename = "quietFrom")]
    pub quiet_from: i64,
    #[serde(rename = "quietTo")]
    pub quiet_to: i64,
    #[serde(rename = "timeZone", skip_serializing_if = "is_empty")]
    pub time_zone: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PhonePushTest {
    pub sent: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    pub title: String,
    pub body: String,
    #[serde(deserialize_with = "nullable")]
    pub data: BTreeMap<String, String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateLANRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub enabled: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub port: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tunnel: Option<bool>,
    #[serde(rename = "tunnelToken", skip_serializing_if = "Option::is_none")]
    pub tunnel_token: Option<String>,
    #[serde(rename = "tunnelHostname", skip_serializing_if = "Option::is_none")]
    pub tunnel_hostname: Option<String>,
    #[serde(rename = "tunnelMode", skip_serializing_if = "Option::is_none")]
    pub tunnel_mode: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LANPairing {
    #[serde(deserialize_with = "nullable")]
    pub urls: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub qr: Vec<String>,
    pub expires: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LANHostReport {
    pub port: i64,
    pub listening: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub warning: String,
    #[serde(deserialize_with = "nullable")]
    pub addresses: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LANPairRequest {
    pub secret: String,
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub bearer: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LANSession {
    pub phone: LANPhone,
    #[serde(skip_serializing_if = "is_empty")]
    pub token: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateLANPhoneRequest {
    #[serde(deserialize_with = "nullable")]
    pub scopes: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LANAuditEntry {
    pub id: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub scope: String,
    pub route: String,
    pub status: i64,
    pub result: String,
    pub addr: String,
    pub at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LANAudit {
    #[serde(deserialize_with = "nullable")]
    pub entries: Vec<LANAuditEntry>,
    #[serde(skip_serializing_if = "is_empty")]
    pub more: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatThread {
    pub agent: String,
    pub seq: i64,
    pub session: ChatSession,
    #[serde(deserialize_with = "nullable")]
    pub items: Vec<ChatItem>,
    #[serde(skip_serializing_if = "is_empty")]
    pub older: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub newer: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatSearch {
    pub query: String,
    #[serde(deserialize_with = "nullable")]
    pub hits: Vec<ChatSearchHit>,
    #[serde(skip_serializing_if = "is_empty")]
    pub more: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatSearchHit {
    pub agent: String,
    pub id: String,
    pub kind: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub at: Option<String>,
    #[serde(deserialize_with = "nullable")]
    pub snippet: Vec<ChatSnippetPart>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatSnippetPart {
    pub text: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub r#match: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatSession {
    pub state: String,
    pub tool: String,
    #[serde(rename = "nextTool", skip_serializing_if = "is_empty")]
    pub next_tool: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub detail: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub adapter: String,
    #[serde(rename = "turnStartedAt", skip_serializing_if = "Option::is_none")]
    pub turn_started_at: Option<String>,
    #[serde(rename = "stalledSince", skip_serializing_if = "Option::is_none")]
    pub stalled_since: Option<String>,
    #[serde(deserialize_with = "nullable")]
    pub options: Vec<ChatOption>,
    #[serde(deserialize_with = "nullable")]
    pub commands: Vec<ChatCommand>,
    #[serde(rename = "contextUsed", skip_serializing_if = "is_empty")]
    pub context_used: i64,
    #[serde(rename = "contextSize", skip_serializing_if = "is_empty")]
    pub context_size: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub limited: bool,
    #[serde(rename = "limitedUntil", skip_serializing_if = "Option::is_none")]
    pub limited_until: Option<String>,
    #[serde(rename = "resumeAt", skip_serializing_if = "Option::is_none")]
    pub resume_at: Option<String>,
    #[serde(rename = "noImages", skip_serializing_if = "is_empty")]
    pub no_images: bool,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub background: Vec<String>,
    #[serde(rename = "toolsChanged", skip_serializing_if = "is_empty")]
    pub tools_changed: bool,
    #[serde(rename = "noResume", skip_serializing_if = "is_empty")]
    pub no_resume: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatOption {
    pub id: String,
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub description: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub category: String,
    pub r#type: String,
    pub value: String,
    #[serde(deserialize_with = "nullable")]
    pub choices: Vec<ChatOptionChoice>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatOptionChoice {
    pub value: String,
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub description: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub group: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub kind: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub efforts: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatCommand {
    pub name: String,
    pub description: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub hint: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatItem {
    pub id: String,
    pub turn: String,
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub text: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub images: Vec<ChatImage>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub files: Vec<ChatFile>,
    #[serde(skip_serializing_if = "is_empty")]
    pub delivery: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub woken: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub hidden: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub to: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub streaming: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tool: Option<ChatTool>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub plan: Vec<ChatPlanEntry>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub permission: Option<ChatPermission>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<ChatTurnResult>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub subagent: Option<ChatSubagent>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub compaction: Option<ChatCompaction>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub goal: Option<AgentGoal>,
    #[serde(skip_serializing_if = "is_empty")]
    pub handoff: String,
    #[serde(rename = "projectMessage", skip_serializing_if = "Option::is_none")]
    pub project_message: Option<ChatProjectMessage>,
    #[serde(rename = "scheduledRun", skip_serializing_if = "Option::is_none")]
    pub scheduled_run: Option<ChatScheduledRun>,
    #[serde(rename = "agentPermission", skip_serializing_if = "Option::is_none")]
    pub agent_permission: Option<ChatAgentPermission>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub finished: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub group: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub parent: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatTool {
    #[serde(rename = "callId")]
    pub call_id: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    pub title: String,
    pub kind: String,
    pub status: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub command: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub paths: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub line: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub output: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub diffs: Vec<ChatDiff>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub page: Option<ChatPage>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatPage {
    #[serde(skip_serializing_if = "is_empty")]
    pub id: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub public: bool,
    #[serde(rename = "expiresInHours", skip_serializing_if = "is_empty")]
    pub expires_in_hours: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatDiff {
    pub path: String,
    #[serde(rename = "oldText")]
    pub old_text: String,
    #[serde(rename = "newText")]
    pub new_text: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub created: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub truncated: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatPlanEntry {
    pub content: String,
    pub status: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatPermission {
    #[serde(rename = "callId")]
    pub call_id: String,
    pub title: String,
    #[serde(deserialize_with = "nullable")]
    pub options: Vec<ChatPermissionOption>,
    #[serde(skip_serializing_if = "is_empty")]
    pub outcome: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub approval: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub detail: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub diffs: Vec<ChatDiff>,
    #[serde(rename = "planApprove", skip_serializing_if = "is_empty")]
    pub plan_approve: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatPermissionOption {
    pub id: String,
    pub name: String,
    pub kind: String,
    #[serde(rename = "modeKind", skip_serializing_if = "is_empty")]
    pub mode_kind: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatSubagent {
    pub name: String,
    pub task: String,
    pub state: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatCompaction {
    pub state: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub waiting: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentGoal {
    pub objective: String,
    pub source: String,
    pub status: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub checks: Vec<GoalCheck>,
    pub checked: i64,
    #[serde(rename = "lastUnmet", skip_serializing_if = "is_empty")]
    pub last_unmet: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub budget: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub continuations: i64,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GoalSpec {
    pub objective: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub checks: Vec<GoalCheck>,
    #[serde(skip_serializing_if = "is_empty")]
    pub budget: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GoalCheck {
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub command: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub met: Option<bool>,
    #[serde(skip_serializing_if = "is_empty")]
    pub detail: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatProjectMessage {
    pub project: String,
    pub direction: String,
    pub thread: String,
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub topic: String,
    #[serde(rename = "expectsReply", skip_serializing_if = "is_empty")]
    pub expects_reply: bool,
    pub hop: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatScheduledRun {
    pub schedule: String,
    pub run: String,
    pub title: String,
    pub trigger: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub event: String,
    #[serde(rename = "eventUrl", skip_serializing_if = "is_empty")]
    pub event_url: String,
    #[serde(rename = "dueAt", skip_serializing_if = "Option::is_none")]
    pub due_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub late: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub missed: i64,
    pub target: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Schedule {
    pub id: String,
    pub project: String,
    pub title: String,
    pub task: String,
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub event: String,
    #[serde(rename = "eventLabel", skip_serializing_if = "is_empty")]
    pub event_label: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub at: Option<String>,
    #[serde(rename = "everyMinutes", skip_serializing_if = "is_empty")]
    pub every_minutes: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub cron: String,
    pub timezone: String,
    pub describe: String,
    pub target: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub session: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub model: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub effort: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub precheck: String,
    #[serde(rename = "graceMinutes", skip_serializing_if = "is_empty")]
    pub grace_minutes: i64,
    pub paused: bool,
    #[serde(rename = "nextRun", skip_serializing_if = "Option::is_none")]
    pub next_run: Option<String>,
    #[serde(rename = "lastRun", skip_serializing_if = "Option::is_none")]
    pub last_run: Option<ScheduleRun>,
    pub runs: i64,
    #[serde(rename = "createdBy")]
    pub created_by: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ScheduleRun {
    pub id: String,
    pub schedule: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    pub trigger: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub event: String,
    #[serde(rename = "eventUrl", skip_serializing_if = "is_empty")]
    pub event_url: String,
    #[serde(rename = "dueAt", skip_serializing_if = "Option::is_none")]
    pub due_at: Option<String>,
    #[serde(rename = "ranAt")]
    pub ran_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub late: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub missed: i64,
    pub outcome: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub job: String,
    #[serde(rename = "chatItem", skip_serializing_if = "is_empty")]
    pub chat_item: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ScheduleRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub title: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub task: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub at: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub r#in: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub every: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cron: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub on: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub label: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub timezone: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub target: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub session: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub effort: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub precheck: Option<String>,
    #[serde(rename = "graceMinutes", skip_serializing_if = "Option::is_none")]
    pub grace_minutes: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub paused: Option<bool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ScheduleChange {
    pub project: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatAgentPermission {
    pub agent: String,
    pub r#ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    pub ai: String,
    pub item: String,
    pub permission: ChatPermission,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tool: Option<ChatTool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatTurnResult {
    pub state: String,
    #[serde(rename = "stopReason", skip_serializing_if = "is_empty")]
    pub stop_reason: String,
    #[serde(rename = "endedAt")]
    pub ended_at: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub background: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatMessageRequest {
    pub text: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub images: Vec<ChatImageUpload>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub files: Vec<ChatFileUpload>,
    #[serde(skip_serializing_if = "is_empty")]
    pub to: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatImage {
    pub id: String,
    #[serde(rename = "mimeType")]
    pub mime_type: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    pub size: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatImageUpload {
    #[serde(rename = "mimeType")]
    pub mime_type: String,
    pub data: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatFile {
    pub id: String,
    pub name: String,
    #[serde(rename = "mimeType", skip_serializing_if = "is_empty")]
    pub mime_type: String,
    pub size: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub path: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatFileUpload {
    pub name: String,
    #[serde(rename = "mimeType", skip_serializing_if = "is_empty")]
    pub mime_type: String,
    pub data: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatAnswerRequest {
    #[serde(rename = "optionId")]
    pub option_id: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatOptionRequest {
    pub value: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatEvent {
    pub agent: String,
    pub seq: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub item: Option<ChatItem>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub append: Option<ChatAppend>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub session: Option<ChatSession>,
    #[serde(skip_serializing_if = "is_empty")]
    pub cleared: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub after: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub checkpoint: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatAppend {
    pub id: String,
    pub text: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectChat {
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub session: String,
    pub r#ref: String,
    pub started: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub worktree: String,
    #[serde(rename = "baseRef", skip_serializing_if = "is_empty")]
    pub base_ref: String,
    pub chat: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last: Option<ChatLine>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadSession {
    pub project: String,
    pub id: String,
    pub r#ref: String,
    pub topic: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub untitled: bool,
    pub color: String,
    pub chat: String,
    pub agents: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last: Option<ChatLine>,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "activeAt")]
    pub active_at: String,
    #[serde(rename = "closedAt", skip_serializing_if = "Option::is_none")]
    pub closed_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub status: String,
    #[serde(rename = "needsYou", skip_serializing_if = "is_empty")]
    pub needs_you: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub questions: i64,
    #[serde(rename = "agentsWorking", skip_serializing_if = "is_empty")]
    pub agents_working: i64,
    #[serde(rename = "seenAt", skip_serializing_if = "Option::is_none")]
    pub seen_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub unread: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub settled: bool,
    #[serde(rename = "snoozedUntil", skip_serializing_if = "Option::is_none")]
    pub snoozed_until: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parent: Option<LeadSessionParent>,
    #[serde(skip_serializing_if = "is_empty")]
    pub pinned: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadSessionParent {
    pub session: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub item: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub topic: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub closed: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadSessionFork {
    pub session: String,
    pub item: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct NewLeadSessionRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub topic: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub from: String,
    #[serde(rename = "continueFrom", skip_serializing_if = "is_empty")]
    pub continue_from: String,
    #[serde(rename = "forkFrom", skip_serializing_if = "Option::is_none")]
    pub fork_from: Option<LeadSessionFork>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadSessionPatch {
    #[serde(skip_serializing_if = "is_empty")]
    pub topic: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pinned: Option<bool>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentSessionRequest {
    pub session: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadSessionsChange {
    pub project: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadSessionStatusChange {
    pub project: String,
    #[serde(deserialize_with = "nullable")]
    pub sessions: Vec<LeadSession>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SeeLeadSessionRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub at: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadSessionSnooze {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub until: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SessionsSettled {
    pub project: String,
    #[serde(deserialize_with = "nullable")]
    pub sessions: Vec<LeadSession>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SessionOutput {
    pub project: String,
    pub session: String,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<SessionAgent>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SessionAgent {
    pub r#ref: String,
    pub name: String,
    pub title: String,
    pub branch: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub archived: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub working: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr: Option<PullRequest>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatLine {
    pub kind: String,
    pub text: String,
    pub at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatCache {
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub session: String,
    #[serde(rename = "idleSince", skip_serializing_if = "Option::is_none")]
    pub idle_since: Option<String>,
    #[serde(rename = "ttlSeconds", skip_serializing_if = "is_empty")]
    pub ttl_seconds: i64,
    #[serde(rename = "ttlSource", skip_serializing_if = "is_empty")]
    pub ttl_source: String,
    #[serde(rename = "dueAt", skip_serializing_if = "Option::is_none")]
    pub due_at: Option<String>,
    #[serde(rename = "expiresAt", skip_serializing_if = "Option::is_none")]
    pub expires_at: Option<String>,
    #[serde(rename = "contextUsed", skip_serializing_if = "is_empty")]
    pub context_used: i64,
    pub due: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ChatCacheChoice {
    pub compact: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub text: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub images: Vec<ChatImageUpload>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub files: Vec<ChatFileUpload>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadRunners {
    pub tool: String,
    #[serde(rename = "nextTool", skip_serializing_if = "is_empty")]
    pub next_tool: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub chosen: String,
    #[serde(deserialize_with = "nullable")]
    pub runners: Vec<LeadRunner>,
    #[serde(
        rename = "toolAccounts",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub tool_accounts: BTreeMap<String, String>,
    #[serde(
        rename = "toolChosen",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub tool_chosen: BTreeMap<String, String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadRunner {
    pub tool: String,
    pub name: String,
    pub ready: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub reason: String,
    pub installed: bool,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub accounts: Vec<LeadAccount>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadRunnerRequest {
    pub tool: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub account: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadAccount {
    pub name: String,
    pub default: bool,
    pub project: bool,
    pub agents: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub limit: Option<ClaudeLimit>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LeadSessionOrder {
    #[serde(deserialize_with = "nullable")]
    pub ids: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemoryEvent {
    pub id: String,
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub session: String,
    pub at: String,
    pub r#type: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub payload: serde_json::Value,
    #[serde(rename = "artifactId", skip_serializing_if = "is_empty")]
    pub artifact_id: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AddMemoryEventRequest {
    pub r#type: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub session: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub payload: serde_json::Value,
    #[serde(rename = "artifactId", skip_serializing_if = "is_empty")]
    pub artifact_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub at: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Memory {
    pub id: String,
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub global: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub origin: String,
    pub kind: String,
    pub title: String,
    pub content: String,
    pub importance: i64,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
    #[serde(rename = "supersedesId", skip_serializing_if = "is_empty")]
    pub supersedes_id: String,
    #[serde(rename = "sourceEventId", skip_serializing_if = "is_empty")]
    pub source_event_id: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub superseded: bool,
    #[serde(rename = "resolvedAt", skip_serializing_if = "is_empty")]
    pub resolved_at: String,
    #[serde(rename = "resolvedBy", skip_serializing_if = "is_empty")]
    pub resolved_by: String,
    #[serde(rename = "referencedAt", skip_serializing_if = "is_empty")]
    pub referenced_at: String,
    #[serde(rename = "decayedAt", skip_serializing_if = "is_empty")]
    pub decayed_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub confirmations: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub promotion: String,
    #[serde(rename = "promotionAt", skip_serializing_if = "is_empty")]
    pub promotion_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AddMemoryRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub kind: String,
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub content: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub importance: i64,
    #[serde(rename = "supersedesId", skip_serializing_if = "is_empty")]
    pub supersedes_id: String,
    #[serde(rename = "sourceEventId", skip_serializing_if = "is_empty")]
    pub source_event_id: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub scope: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemorySearchRequest {
    pub query: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub limit: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemorySearchResults {
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub memories: Vec<Memory>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub events: Vec<MemoryEvent>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub reports: Vec<AgentReport>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WorkingMemory {
    #[serde(skip_serializing_if = "is_empty")]
    pub goal: String,
    #[serde(rename = "currentTask", skip_serializing_if = "is_empty")]
    pub current_task: String,
    #[serde(
        rename = "activeAgents",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub active_agents: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub blockers: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub notes: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub sessions: BTreeMap<String, String>,
    #[serde(rename = "updatedAt", skip_serializing_if = "is_empty")]
    pub updated_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WorkingMemoryPatch {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub goal: Option<String>,
    #[serde(rename = "currentTask", skip_serializing_if = "Option::is_none")]
    pub current_task: Option<String>,
    #[serde(rename = "activeAgents", skip_serializing_if = "Option::is_none")]
    pub active_agents: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub blockers: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub notes: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemoryArtifact {
    pub id: String,
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    pub r#type: String,
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub metadata: serde_json::Value,
    #[serde(rename = "createdAt")]
    pub created_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AddArtifactRequest {
    pub r#type: String,
    pub path: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub metadata: serde_json::Value,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentReport {
    pub id: String,
    pub project: String,
    pub agent: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub task: String,
    pub status: String,
    pub summary: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub discoveries: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub decisions: Vec<String>,
    #[serde(
        rename = "remainingIssues",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub remaining_issues: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub artifacts: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AddReportRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub task: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub status: String,
    pub summary: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub discoveries: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub decisions: Vec<String>,
    #[serde(
        rename = "remainingIssues",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub remaining_issues: Vec<String>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub artifacts: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ContextRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub query: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub budget: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub r#for: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ContextResult {
    pub text: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub sections: Vec<ContextSection>,
    pub stats: ContextStats,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ContextSection {
    pub kind: String,
    pub title: String,
    pub rows: i64,
    pub tokens: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ContextStats {
    pub project: String,
    pub r#for: String,
    pub at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub query: String,
    pub budget: i64,
    pub tokens: i64,
    pub rows: i64,
    #[serde(rename = "consideredRows")]
    pub considered_rows: i64,
    #[serde(rename = "droppedRows")]
    pub dropped_rows: i64,
    #[serde(
        rename = "droppedSections",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub dropped_sections: Vec<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub truncated: bool,
    #[serde(rename = "corpusTokens")]
    pub corpus_tokens: i64,
    pub ratio: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ContextAccount {
    pub builds: i64,
    pub tokens: i64,
    #[serde(rename = "droppedRows")]
    pub dropped_rows: i64,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub recent: Vec<ContextStats>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ResolveMemoryRequest {
    pub id: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub why: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ConsolidateRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub distil: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TidyMemoryRequest {
    #[serde(rename = "olderThanHours", skip_serializing_if = "is_empty")]
    pub older_than_hours: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub apply: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TidyMemoryResult {
    pub applied: bool,
    #[serde(deserialize_with = "nullable")]
    pub resolved: Vec<Memory>,
    #[serde(deserialize_with = "nullable")]
    pub merged: Vec<MemoryMerge>,
    pub kept: i64,
    pub scrubbed: MemoryScrub,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemoryMerge {
    pub memory: Memory,
    pub into: Memory,
    pub score: f64,
    pub why: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemoryScrub {
    pub events: i64,
    pub memories: i64,
    pub reports: i64,
    pub artifacts: i64,
    #[serde(rename = "workingMemory")]
    pub working_memory: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ConsolidationPass {
    pub id: String,
    pub project: String,
    pub kind: String,
    pub at: String,
    #[serde(rename = "durationMs")]
    pub duration_ms: i64,
    #[serde(rename = "eventsRead")]
    pub events_read: i64,
    #[serde(rename = "memoriesWritten")]
    pub memories_written: i64,
    #[serde(rename = "memoriesSuperseded")]
    pub memories_superseded: i64,
    #[serde(rename = "memoriesResolved")]
    pub memories_resolved: i64,
    #[serde(rename = "memoriesDecayed")]
    pub memories_decayed: i64,
    #[serde(rename = "duplicatesFound")]
    pub duplicates_found: i64,
    #[serde(rename = "inputBytes")]
    pub input_bytes: i64,
    #[serde(rename = "outputBytes")]
    pub output_bytes: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub model: String,
    #[serde(rename = "throughEventId", skip_serializing_if = "is_empty")]
    pub through_event_id: String,
    #[serde(rename = "throughAt", skip_serializing_if = "is_empty")]
    pub through_at: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemoryConsolidation {
    pub project: String,
    pub setting: i64,
    pub events: i64,
    pub memories: i64,
    pub superseded: i64,
    pub resolved: i64,
    pub duplicates: i64,
    pub pending: i64,
    pub passes: i64,
    #[serde(rename = "eventsRead")]
    pub events_read: i64,
    #[serde(rename = "memoriesWritten")]
    pub memories_written: i64,
    #[serde(rename = "memoriesSuperseded")]
    pub memories_superseded: i64,
    #[serde(rename = "memoriesResolved")]
    pub memories_resolved: i64,
    #[serde(rename = "memoriesDecayed")]
    pub memories_decayed: i64,
    #[serde(rename = "inputBytes")]
    pub input_bytes: i64,
    #[serde(rename = "outputBytes")]
    pub output_bytes: i64,
    #[serde(rename = "lastPass", skip_serializing_if = "is_empty")]
    pub last_pass: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub watermark: String,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub recent: Vec<ConsolidationPass>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MemoryDuplicate {
    pub memory: Memory,
    pub of: Memory,
    pub similarity: f64,
    #[serde(rename = "foundAt")]
    pub found_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TokenCounts {
    pub input: i64,
    pub output: i64,
    #[serde(rename = "cacheRead")]
    pub cache_read: i64,
    #[serde(rename = "cacheWrite")]
    pub cache_write: i64,
    pub total: i64,
    #[serde(rename = "costUSD")]
    pub cost_usd: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ModelTokens {
    pub model: String,
    pub input: i64,
    pub output: i64,
    #[serde(rename = "cacheRead")]
    pub cache_read: i64,
    #[serde(rename = "cacheWrite")]
    pub cache_write: i64,
    pub total: i64,
    #[serde(rename = "costUSD")]
    pub cost_usd: f64,
    #[serde(rename = "avgTPS", skip_serializing_if = "is_empty")]
    pub avg_tps: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentTokens {
    pub project: String,
    pub agent: String,
    pub r#ref: String,
    pub ai: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    pub exists: bool,
    pub turns: i64,
    #[serde(rename = "maxContext")]
    pub max_context: i64,
    #[serde(rename = "lastAt")]
    pub last_at: String,
    pub input: i64,
    pub output: i64,
    #[serde(rename = "cacheRead")]
    pub cache_read: i64,
    #[serde(rename = "cacheWrite")]
    pub cache_write: i64,
    pub total: i64,
    #[serde(rename = "costUSD")]
    pub cost_usd: f64,
    #[serde(rename = "avgTPS", skip_serializing_if = "is_empty")]
    pub avg_tps: f64,
    #[serde(deserialize_with = "nullable")]
    pub models: Vec<ModelTokens>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TokenBucket {
    pub start: String,
    pub input: i64,
    pub output: i64,
    #[serde(rename = "cacheRead")]
    pub cache_read: i64,
    #[serde(rename = "cacheWrite")]
    pub cache_write: i64,
    pub total: i64,
    #[serde(rename = "costUSD")]
    pub cost_usd: f64,
    #[serde(rename = "avgTPS", skip_serializing_if = "is_empty")]
    pub avg_tps: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TokenReport {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub since: Option<String>,
    pub until: String,
    pub input: i64,
    pub output: i64,
    #[serde(rename = "cacheRead")]
    pub cache_read: i64,
    #[serde(rename = "cacheWrite")]
    pub cache_write: i64,
    pub total: i64,
    #[serde(rename = "costUSD")]
    pub cost_usd: f64,
    #[serde(rename = "avgTPS", skip_serializing_if = "is_empty")]
    pub avg_tps: f64,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<AgentTokens>,
    #[serde(deserialize_with = "nullable")]
    pub buckets: Vec<TokenBucket>,
    #[serde(rename = "bucketSeconds")]
    pub bucket_seconds: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TokenTurn {
    pub project: String,
    pub agent: String,
    pub ai: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub session: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub turn: String,
    pub kind: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub model: String,
    pub at: String,
    pub context: i64,
    #[serde(rename = "generationMS", skip_serializing_if = "is_empty")]
    pub generation_ms: i64,
    pub input: i64,
    pub output: i64,
    #[serde(rename = "cacheRead")]
    pub cache_read: i64,
    #[serde(rename = "cacheWrite")]
    pub cache_write: i64,
    pub total: i64,
    #[serde(rename = "costUSD")]
    pub cost_usd: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ClaudeLimit {
    #[serde(skip_serializing_if = "is_empty")]
    pub tool: String,
    pub account: String,
    pub default: bool,
    pub at: String,
    pub status: String,
    #[serde(rename = "usingOverage", skip_serializing_if = "is_empty")]
    pub using_overage: bool,
    #[serde(deserialize_with = "nullable")]
    pub windows: Vec<ClaudeLimitWindow>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ClaudeLimitWindow {
    pub name: String,
    pub label: String,
    pub utilization: f64,
    #[serde(rename = "resetsAt")]
    pub resets_at: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Recap {
    pub start: String,
    pub end: String,
    pub agents: i64,
    #[serde(rename = "prsOpened")]
    pub prs_opened: i64,
    #[serde(rename = "prsMerged")]
    pub prs_merged: i64,
    pub insertions: i64,
    pub deletions: i64,
    pub files: i64,
    #[serde(rename = "workSeconds")]
    pub work_seconds: i64,
    #[serde(rename = "awaySeconds")]
    pub away_seconds: i64,
    #[serde(deserialize_with = "nullable")]
    pub days: Vec<RecapDay>,
    #[serde(deserialize_with = "nullable")]
    pub hours: Vec<i64>,
    pub projects: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub busiest: Option<RecapProject>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub longest: Option<RecapAgent>,
    #[serde(deserialize_with = "nullable")]
    pub models: Vec<RecapModel>,
    pub tokens: i64,
    #[serde(rename = "costUsd")]
    pub cost_usd: f64,
    #[serde(rename = "estimatedUsd")]
    pub estimated_usd: f64,
    #[serde(rename = "unpricedTokens")]
    pub unpriced_tokens: i64,
    pub streak: i64,
    #[serde(rename = "bestStreak")]
    pub best_streak: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RecapDay {
    pub date: String,
    #[serde(rename = "workSeconds")]
    pub work_seconds: i64,
    #[serde(rename = "awaySeconds")]
    pub away_seconds: i64,
    pub agents: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RecapProject {
    pub project: String,
    #[serde(rename = "workSeconds")]
    pub work_seconds: i64,
    pub agents: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RecapAgent {
    pub project: String,
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub ai: String,
    #[serde(rename = "workSeconds")]
    pub work_seconds: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RecapModel {
    pub model: String,
    pub tokens: i64,
    pub output: i64,
    #[serde(rename = "costUsd")]
    pub cost_usd: f64,
    #[serde(rename = "estimatedUsd")]
    pub estimated_usd: f64,
    #[serde(rename = "unpricedTokens")]
    pub unpriced_tokens: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMStatus {
    pub mode: String,
    pub driver: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub name: String,
    pub state: String,
    pub since: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub problem: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub cpus: i64,
    pub memory: VMMemory,
    pub disk: VMDisk,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub limits: Option<VMLimits>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub live: Option<VMLimits>,
    #[serde(rename = "pausedForDisk", skip_serializing_if = "is_empty")]
    pub paused_for_disk: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub swap: Option<VMSwap>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMMemory {
    pub min: i64,
    pub cap: i64,
    pub granted: i64,
    #[serde(rename = "hostReserve", skip_serializing_if = "is_empty")]
    pub host_reserve: i64,
    pub used: i64,
    pub resident: i64,
    #[serde(rename = "hostCache")]
    pub host_cache: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMDisk {
    pub size: i64,
    pub allocated: i64,
    pub pool: VMDiskImage,
    pub root: VMDiskImage,
    #[serde(rename = "hostFree", skip_serializing_if = "is_empty")]
    pub host_free: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMDiskImage {
    pub size: i64,
    pub allocated: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMHomeDisk {
    pub worktrees: i64,
    pub media: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMSwap {
    pub size: i64,
    pub total: i64,
    pub used: i64,
    #[serde(rename = "diskUsed", skip_serializing_if = "is_empty")]
    pub disk_used: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMLimits {
    #[serde(rename = "minCpus")]
    pub min_cpus: i64,
    #[serde(rename = "maxCpus")]
    pub max_cpus: i64,
    #[serde(rename = "minMemory")]
    pub min_memory: i64,
    #[serde(rename = "maxMemory")]
    pub max_memory: i64,
    #[serde(rename = "minDisk", skip_serializing_if = "is_empty")]
    pub min_disk: i64,
    #[serde(rename = "maxDisk", skip_serializing_if = "is_empty")]
    pub max_disk: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMResizeRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub cpus: i64,
    #[serde(rename = "memoryCap", skip_serializing_if = "is_empty")]
    pub memory_cap: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub disk: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMStopRequest {
    #[serde(skip_serializing_if = "is_empty")]
    pub agents: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReportSection {
    pub id: String,
    pub title: String,
    pub content: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReportDraftRequest {
    #[serde(deserialize_with = "nullable")]
    pub sections: Vec<ReportSection>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReportDraft {
    pub install: String,
    pub version: String,
    pub os: String,
    pub arch: String,
    #[serde(deserialize_with = "nullable")]
    pub sections: Vec<ReportSection>,
    pub endpoint: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReportRequest {
    pub kind: String,
    pub message: String,
    #[serde(deserialize_with = "nullable")]
    pub sections: Vec<ReportSection>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReportSent {
    pub id: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SearchResults {
    pub query: String,
    #[serde(deserialize_with = "nullable")]
    pub groups: Vec<SearchGroup>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SearchGroup {
    pub kind: String,
    #[serde(deserialize_with = "nullable")]
    pub hits: Vec<SearchHit>,
    #[serde(skip_serializing_if = "is_empty")]
    pub more: bool,
    pub total: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub capped: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SearchHit {
    pub id: String,
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub detail: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub tag: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub url: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub archived: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub session: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub item: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub memory: Option<Memory>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub event: Option<MemoryEvent>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub report: Option<AgentReport>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub media: Option<MediaItem>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Artifact {
    pub id: String,
    pub title: String,
    pub url: String,
    pub agent: String,
    pub item: String,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<String>,
    pub version: i64,
    #[serde(skip_serializing_if = "is_empty")]
    pub public: bool,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
    #[serde(rename = "expiresAt", skip_serializing_if = "Option::is_none")]
    pub expires_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub permanent: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub expired: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Artifacts {
    pub connector: String,
    #[serde(deserialize_with = "nullable")]
    pub artifacts: Vec<Artifact>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ArtifactPreview {
    pub artifact: Artifact,
    pub status: String,
    pub page: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PublishRenderRequest {
    pub title: String,
    pub html: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PublishRenderResult {
    pub item: ChatItem,
    pub url: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FanOutRequest {
    pub project: String,
    pub title: String,
    pub task: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub branch: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub from: String,
    #[serde(deserialize_with = "nullable")]
    pub variants: Vec<FanOutVariant>,
    #[serde(skip_serializing_if = "is_empty")]
    pub judge: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FanOutVariant {
    pub ai: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub effort: Option<String>,
    #[serde(rename = "contextWindow", skip_serializing_if = "Option::is_none")]
    pub context_window: Option<String>,
    #[serde(rename = "claudeAccount", skip_serializing_if = "is_empty")]
    pub claude_account: String,
    #[serde(rename = "codexAccount", skip_serializing_if = "is_empty")]
    pub codex_account: String,
    #[serde(
        rename = "openCodeAccounts",
        deserialize_with = "nullable",
        skip_serializing_if = "is_empty"
    )]
    pub open_code_accounts: BTreeMap<String, String>,
    #[serde(rename = "cursorAccount", skip_serializing_if = "is_empty")]
    pub cursor_account: String,
    #[serde(rename = "geminiAccount", skip_serializing_if = "is_empty")]
    pub gemini_account: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FanOutResult {
    pub group: Group,
    #[serde(deserialize_with = "nullable")]
    pub jobs: Vec<Job>,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub notices: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Group {
    pub id: String,
    pub project: String,
    pub title: String,
    pub task: String,
    #[serde(rename = "byLead", skip_serializing_if = "is_empty")]
    pub by_lead: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub kept: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub recommended: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub recommendation: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub judging: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub judge: bool,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "settledAt", skip_serializing_if = "Option::is_none")]
    pub settled_at: Option<String>,
    pub finished: i64,
    #[serde(deserialize_with = "nullable")]
    pub members: Vec<GroupMember>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GroupMember {
    pub label: String,
    pub ai: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub model: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub effort: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub job: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub r#ref: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(rename = "finishedAt", skip_serializing_if = "Option::is_none")]
    pub finished_at: Option<String>,
    #[serde(rename = "startedAt", skip_serializing_if = "Option::is_none")]
    pub started_at: Option<String>,
    #[serde(skip_serializing_if = "is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub branch: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub state: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub busy: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub changes: Option<AgentChanges>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr: Option<PullRequest>,
    #[serde(skip_serializing_if = "is_empty")]
    pub tokens: i64,
    #[serde(rename = "costUsd", skip_serializing_if = "is_empty")]
    pub cost_usd: f64,
    #[serde(skip_serializing_if = "is_empty")]
    pub media: i64,
    #[serde(rename = "reportStatus", skip_serializing_if = "is_empty")]
    pub report_status: String,
    #[serde(rename = "reportSummary", skip_serializing_if = "is_empty")]
    pub report_summary: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct KeepRequest {
    pub agent: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RecommendRequest {
    pub agent: String,
    pub why: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct GroupChange {
    pub id: String,
    pub project: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TraceSpan {
    #[serde(skip_serializing_if = "is_empty")]
    pub id: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub parent: String,
    pub name: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub source: String,
    pub start: String,
    #[serde(rename = "durationMs")]
    pub duration_ms: f64,
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub attrs: BTreeMap<String, serde_json::Value>,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub level: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SpanStats {
    pub name: String,
    pub count: i64,
    pub errors: i64,
    #[serde(rename = "p50Ms")]
    pub p50_ms: f64,
    #[serde(rename = "p95Ms")]
    pub p95_ms: f64,
    #[serde(rename = "maxMs")]
    pub max_ms: f64,
    #[serde(rename = "totalMs")]
    pub total_ms: f64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DiagnosticsSpans {
    pub since: String,
    #[serde(deserialize_with = "nullable")]
    pub stats: Vec<SpanStats>,
    #[serde(deserialize_with = "nullable")]
    pub slowest: Vec<TraceSpan>,
    #[serde(deserialize_with = "nullable")]
    pub failures: Vec<TraceSpan>,
    pub dropped: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DiagnosticsStalls {
    pub since: String,
    #[serde(deserialize_with = "nullable")]
    pub stalls: Vec<TraceSpan>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VMResourceSample {
    pub at: String,
    pub cpu: f64,
    pub cores: i64,
    #[serde(rename = "memUsed")]
    pub mem_used: i64,
    #[serde(rename = "memTotal")]
    pub mem_total: i64,
    #[serde(rename = "diskRead")]
    pub disk_read: i64,
    #[serde(rename = "diskWrite")]
    pub disk_write: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pressure: Option<HostPressure>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentResourceSample {
    pub at: String,
    pub state: String,
    pub cpu: f64,
    pub memory: i64,
    pub processes: i64,
    #[serde(rename = "diskRead")]
    pub disk_read: i64,
    #[serde(rename = "diskWrite")]
    pub disk_write: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentResources {
    pub r#ref: String,
    #[serde(deserialize_with = "nullable")]
    pub samples: Vec<AgentResourceSample>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DiagnosticsResources {
    pub since: String,
    #[serde(rename = "intervalSeconds")]
    pub interval_seconds: i64,
    #[serde(deserialize_with = "nullable")]
    pub vm: Vec<VMResourceSample>,
    #[serde(deserialize_with = "nullable")]
    pub agents: Vec<AgentResources>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PresenceRequest {
    pub client: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub kind: String,
    pub visible: bool,
    #[serde(deserialize_with = "nullable")]
    pub views: Vec<PresenceView>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub power: Option<HostPower>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PresenceView {
    pub project: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub resources: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HostPower {
    #[serde(rename = "onBattery")]
    pub on_battery: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub thermal: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PresenceResponse {
    #[serde(rename = "ttlSeconds")]
    pub ttl_seconds: i64,
    pub idle: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PresenceState {
    #[serde(deserialize_with = "nullable")]
    pub leases: Vec<PresenceLease>,
    pub idle: bool,
    pub power: HostPower,
    #[serde(deserialize_with = "nullable")]
    pub sampling: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PresenceLease {
    pub client: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub kind: String,
    pub visible: bool,
    #[serde(deserialize_with = "nullable")]
    pub views: Vec<PresenceView>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub power: Option<HostPower>,
    pub expires: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentResourcePoint {
    pub at: String,
    pub cpu: f64,
    pub memory: i64,
    pub swap: i64,
    #[serde(rename = "diskRead")]
    pub disk_read: i64,
    #[serde(rename = "diskWrite")]
    pub disk_write: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentResourceHistory {
    pub r#ref: String,
    #[serde(rename = "intervalSeconds")]
    pub interval_seconds: i64,
    #[serde(rename = "windowSeconds")]
    pub window_seconds: i64,
    pub sampling: bool,
    #[serde(deserialize_with = "nullable")]
    pub points: Vec<AgentResourcePoint>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentResourcesEvent {
    pub r#ref: String,
    pub point: AgentResourcePoint,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PackageCachePart {
    pub name: String,
    pub bytes: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DiagnosticsPackageCache {
    pub on: bool,
    pub bytes: i64,
    #[serde(rename = "maxBytes")]
    pub max_bytes: i64,
    #[serde(deserialize_with = "nullable")]
    pub parts: Vec<PackageCachePart>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ClientSpansRequest {
    #[serde(deserialize_with = "nullable")]
    pub spans: Vec<TraceSpan>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ShareReport {
    pub open: i64,
    pub limit: i64,
    pub trims: i64,
    #[serde(rename = "lastTrim", skip_serializing_if = "Option::is_none")]
    pub last_trim: Option<String>,
    pub pinned: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FileCount {
    pub open: i64,
    pub limit: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DiagnosticsFiles {
    pub daemon: FileCount,
    pub system: FileCount,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub share: Option<ShareReport>,
    pub waiting: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DoctorCheck {
    pub id: String,
    pub title: String,
    pub status: String,
    pub message: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub fix: String,
    #[serde(rename = "autoFix", skip_serializing_if = "is_empty")]
    pub auto_fix: bool,
    #[serde(skip_serializing_if = "is_empty")]
    pub job: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DoctorReport {
    pub version: String,
    #[serde(deserialize_with = "nullable")]
    pub checks: Vec<DoctorCheck>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DoctorFixRequest {
    #[serde(deserialize_with = "nullable", skip_serializing_if = "is_empty")]
    pub checks: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DoctorFix {
    pub id: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub job: String,
    #[serde(skip_serializing_if = "is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DoctorFixResult {
    #[serde(deserialize_with = "nullable")]
    pub fixes: Vec<DoctorFix>,
    pub report: DoctorReport,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DoctorHomeRequest {
    pub check: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DoctorHomeResult {
    pub session: String,
}

pub const JOB_RUNNING: &str = "running";
pub const JOB_SUCCEEDED: &str = "succeeded";
pub const JOB_FAILED: &str = "failed";
pub const JOB_CANCELLED: &str = "cancelled";
pub const EVENT_JOB: &str = "job";
pub const EVENT_JOB_LOG: &str = "job.log";
pub const EVENT_AGENT: &str = "agent";
pub const EVENT_USAGE: &str = "usage";
pub const EVENT_AGENT_RESOURCES: &str = "agent-resources";
pub const EVENT_PROJECT: &str = "project";
pub const EVENT_MEDIA: &str = "media";
pub const EVENT_NOTIFICATION: &str = "notification";
pub const EVENT_NOTIFICATIONS_SEEN: &str = "notification.seen";
pub const NOTIFY_QUESTION: &str = "question";
pub const NOTIFY_MEDIA: &str = "media";
pub const EVENT_PULLS: &str = "pulls";
pub const EVENT_REMOTE_AGENTS: &str = "remote-agents";
pub const EVENT_THEME: &str = "theme";
pub const EVENT_UPDATE: &str = "update";
pub const EVENT_DISK: &str = "disk";
pub const DISK_OK: &str = "ok";
pub const DISK_LOW: &str = "low";
pub const DISK_FULL: &str = "full";
pub const EVENT_LAN: &str = "lan";
pub const EVENT_SNAP: &str = "snap";
pub const EVENT_FILES: &str = "files";
pub const PUSH_FINISHED: &str = "finished";
pub const PUSH_NEEDS_YOU: &str = "needs_you";
pub const PUSH_PR_READY: &str = "pr_ready";
pub const PUSH_CI_FAILED: &str = "ci_failed";
pub const FILE_MODIFIED: &str = "modified";
pub const FILE_ADDED: &str = "added";
pub const FILE_DELETED: &str = "deleted";
pub const FILE_UNTRACKED: &str = "untracked";
pub const DOCTOR_OK: &str = "ok";
pub const DOCTOR_WARN: &str = "warn";
pub const DOCTOR_FAIL: &str = "fail";
pub const DOCTOR_AI_LOGIN: &str = "ai-login";
pub const SETUP_OK: &str = "ok";
pub const SETUP_MISSING: &str = "missing";
pub const SETUP_OUTDATED: &str = "outdated";
pub const SETUP_OPTIONAL: &str = "optional";
pub const SETUP_UPDATING: &str = "updating";
pub const SETUP_BUILDING: &str = "building";
pub const IMAGE_BUILD_IDLE: &str = "idle";
pub const IMAGE_BUILDING: &str = "building";
pub const IMAGE_BUILD_FAILED: &str = "failed";
pub const IN_AGENT_SOCKET: &str = "/run/agentbox.sock";
pub const ERROR_FOLDER_NOT_EMPTY: &str = "folder-not-empty";
pub const ERROR_FOLDER_EXISTS: &str = "folder-exists";
pub const ERROR_BAD_REPO_URL: &str = "bad-repo-url";
pub const ERROR_PASSPHRASE_NEEDED: &str = "passphrase-needed";
pub const ERROR_WRONG_PASSPHRASE: &str = "wrong-passphrase";
pub const ERROR_NEWER_EXPORT: &str = "newer-export";
pub const DEFAULT_LANGUAGE: &str = "en-US";
pub const PR_STATE_OPEN: &str = "open";
pub const PR_STATE_MERGED: &str = "merged";
pub const PR_STATE_CLOSED: &str = "closed";
pub const PR_STATE_NONE: &str = "none";
pub const LEAD_NAME: &str = "lead";
pub const HOME_PROJECT: &str = "_home";
pub const AGENT_MODEL_AUTO: &str = "auto";
pub const CONSOLIDATION_MODEL_CHEAP: &str = "cheap";
pub const CONSOLIDATION_MODEL_CHAT: &str = "";
pub const EVENT_GROUP: &str = "group";
pub const EVENT_CHAT: &str = "chat";
pub const EVENT_CHAT_CACHE: &str = "chat.cache";
pub const EVENT_LEAD_SESSIONS: &str = "lead.sessions";
pub const EVENT_SESSIONS_SETTLED: &str = "lead.sessions.settled";
pub const EVENT_LEAD_SESSION_STATUS: &str = "lead.session.status";
pub const SESSION_NEEDS_YOU: &str = "needsYou";
pub const SESSION_FAILED: &str = "failed";
pub const SESSION_WORKING: &str = "working";
pub const SESSION_IDLE: &str = "idle";
pub const EVENT_QUESTION: &str = "question";
pub const EVENT_SCHEDULE: &str = "schedule";
pub const EVENT_AGENT_EVENT: &str = "agent.event";
pub const REVIEW_REVIEWED: &str = "reviewed";
pub const REVIEW_DISMISSED: &str = "dismissed";
pub const EVENT_REVIEW: &str = "review";
pub const AGENT_CREATED: &str = "created";
pub const AGENT_FINISHED: &str = "finished";
pub const AGENT_ASKED: &str = "asked";
pub const AGENT_ANSWERED: &str = "answered";
pub const CREDENTIAL_GIT_HUB: &str = "github";
pub const CREDENTIAL_SECRET: &str = "secret";
pub const CLAUDE_CREDENTIAL_VARIABLES: &str = "ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL ANTHROPIC_CUSTOM_HEADERS ANTHROPIC_PROFILE ANTHROPIC_FEDERATION_RULE_ID ANTHROPIC_ORGANIZATION_ID ANTHROPIC_WORKSPACE_ID CLAUDE_CODE_OAUTH_REFRESH_TOKEN CLAUDE_CONFIG_DIR CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST CLAUDE_CODE_USE_BEDROCK AWS_BEARER_TOKEN_BEDROCK ANTHROPIC_BEDROCK_BASE_URL ANTHROPIC_BEDROCK_MANTLE_BASE_URL ANTHROPIC_AWS_API_KEY ANTHROPIC_AWS_BASE_URL CLAUDE_CODE_USE_VERTEX ANTHROPIC_VERTEX_BASE_URL ANTHROPIC_VERTEX_PROJECT_ID CLAUDE_CODE_USE_FOUNDRY ANTHROPIC_FOUNDRY_API_KEY ANTHROPIC_FOUNDRY_AUTH_TOKEN ANTHROPIC_FOUNDRY_BASE_URL ANTHROPIC_FOUNDRY_RESOURCE";
pub const CODEX_CREDENTIAL_VARIABLES: &str =
    "OPENAI_API_KEY CODEX_API_KEY OPENAI_BASE_URL CODEX_HOME";
pub const OPEN_CODE_CREDENTIAL_VARIABLES: &str = "ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL OPENAI_API_KEY OPENAI_BASE_URL OPENROUTER_API_KEY GEMINI_API_KEY GOOGLE_GENERATIVE_AI_API_KEY GROQ_API_KEY XAI_API_KEY DEEPSEEK_API_KEY MISTRAL_API_KEY OPENCODE_API_KEY";
pub const GEMINI_CREDENTIAL_VARIABLES: &str = "GEMINI_API_KEY GOOGLE_API_KEY GOOGLE_GENAI_USE_VERTEXAI GOOGLE_GENAI_USE_GCA GOOGLE_CLOUD_PROJECT GOOGLE_CLOUD_LOCATION GOOGLE_APPLICATION_CREDENTIALS GOOGLE_CLOUD_ACCESS_TOKEN GOOGLE_GEMINI_BASE_URL GEMINI_CLI_HOME";
pub const CONNECTOR_O_AUTH: &str = "oauth";
pub const CONNECTOR_SECRET: &str = "secret";
pub const CONNECTOR_NONE: &str = "none";
pub const CONNECTOR_WIDE: &str = "agentbox";
pub const CONNECTOR_CONNECTED: &str = "connected";
pub const CONNECTOR_DISCONNECTED: &str = "disconnected";
pub const CONNECTOR_CONNECTING: &str = "connecting";
pub const CONNECTOR_ERROR: &str = "error";
pub const EVENT_CONNECTOR: &str = "connector";
pub const QUESTION_CONNECTOR: &str = "connector";
pub const TRANSPORT_HTTP: &str = "http";
pub const TRANSPORT_SSE: &str = "sse";
pub const TRANSPORT_STDIO: &str = "stdio";
pub const CONNECTOR_PROBLEM_STDIO: &str = "stdio";
pub const CONNECTOR_PROBLEM_SSE: &str = "sse";
pub const CONNECTOR_PROBLEM_URL: &str = "url";
pub const CONNECTOR_PROBLEM_VARIABLE: &str = "variable";
pub const ARTIFACT_ACTIVE: &str = "active";
pub const ARTIFACT_EXPIRED: &str = "expired";
pub const ARTIFACT_UNAVAILABLE: &str = "unavailable";
pub const GIT_HUB_NO_ACCOUNT: &str = "noAccount";
pub const GIT_HUB_NO_ACCESS: &str = "noAccess";
pub const GIT_HUB_BAD_TOKEN: &str = "badToken";
pub const GIT_HUB_RATE_LIMITED: &str = "rateLimited";
pub const GIT_HUB_OTHER_ERR: &str = "other";
pub const CHAT_OFF: &str = "off";
pub const CHAT_STARTING: &str = "starting";
pub const CHAT_READY: &str = "ready";
pub const CHAT_RUNNING: &str = "running";
pub const CHAT_WAITING: &str = "waiting";
pub const CHAT_ERROR: &str = "error";
pub const CHAT_OPTION_PLAN: &str = "plan";
pub const MEMORY_KIND_PROJECT: &str = "project";
pub const MEMORY_KIND_EPISODIC: &str = "episodic";
pub const MEMORY_KIND_DECISION: &str = "decision";
pub const MEMORY_KIND_DISCOVERY: &str = "discovery";
pub const MEMORY_KIND_ISSUE: &str = "issue";
pub const REPORT_KIND_PROBLEM: &str = "problem";
pub const REPORT_KIND_ERROR: &str = "error";
pub const REPORT_SECTION_SYSTEM: &str = "system";
pub const REPORT_SECTION_DAEMON_LOG: &str = "daemon-log";
pub const REPORT_SECTION_VM_LOG: &str = "vm-log";
pub const REPORT_SECTION_APP_LOG: &str = "app-log";
pub const REPORT_SECTION_APP_ERRORS: &str = "app-errors";
pub const REPORT_SECTION_ERROR: &str = "error";
pub const REPORT_DONE: &str = "done";
pub const REPORT_PARTIAL: &str = "partial";
pub const REPORT_BLOCKED: &str = "blocked";
pub const REPORT_FAILED: &str = "failed";
pub const CONTEXT_FOR_LEAD: &str = "lead";
pub const CONTEXT_FOR_AGENT: &str = "agent";
pub const CONTEXT_FOR_TOOL: &str = "tool";
pub const CONSOLIDATION_MECHANICAL: &str = "mechanical";
pub const CONSOLIDATION_DISTILL: &str = "distill";
pub const SEARCH_PROJECTS: &str = "projects";
pub const SEARCH_SESSIONS: &str = "sessions";
pub const SEARCH_AGENTS: &str = "agents";
pub const SEARCH_CHATS: &str = "chats";
pub const SEARCH_MEMORIES: &str = "memories";
pub const SEARCH_EVENTS: &str = "events";
pub const SEARCH_REPORTS: &str = "reports";
pub const SEARCH_MEDIA: &str = "media";
pub const SEARCH_SKILLS: &str = "skills";
pub const SEARCH_CONNECTORS: &str = "connectors";
pub const SEARCH_NOTES: &str = "notes";
pub const SEARCH_PULLS: &str = "pulls";
pub const TOKENS_TURN: &str = "turn";
pub const TOKENS_BACKGROUND: &str = "background";
pub const TOKENS_COMPACTION: &str = "compaction";
pub const TOKENS_CONSOLIDATION: &str = "consolidation";
pub const TOKENS_KEEPALIVE: &str = "keepalive";
pub const FEATURE_DESKTOP_OPEN: &str = "desktop.open";
pub const FEATURE_TERMINAL_OPEN: &str = "terminal.open";
pub const FEATURE_ANDROID_OPEN: &str = "android.open";
pub const FEATURE_AGENT_MEDIA_VIEW: &str = "media.view.agent";
pub const FEATURE_PROJECT_MEDIA_VIEW: &str = "media.view.project";
pub const FEATURE_PULL_LIST: &str = "pr.list";
pub const FEATURE_MEMORY_VIEW: &str = "memory.view";
pub const FEATURE_TOKENS_VIEW: &str = "tokens.view";
pub const FEATURE_SETTINGS_ENVIRONMENT: &str = "settings.view.environment";
pub const FEATURE_SETTINGS_ACCOUNTS: &str = "settings.view.accounts";
pub const FEATURE_SETTINGS_LEAD: &str = "settings.view.lead";
pub const FEATURE_SETTINGS_AGENTS: &str = "settings.view.agents";
pub const FEATURE_SETTINGS_GENERAL: &str = "settings.view.general";
pub const FEATURE_SETTINGS_MODELS: &str = "settings.view.models";
pub const FEATURE_SETTINGS_RESOURCES: &str = "settings.view.resources";
pub const FEATURE_SETTINGS_PROJECT: &str = "settings.view.project";
pub const FEATURE_SETTINGS_SEARCH: &str = "settings.search";
pub const FEATURE_CHAT_FIND: &str = "chat.find";
pub const FEATURE_SEARCH_OPEN: &str = "search.open";
pub const FEATURE_MENU_OPEN_CHAT: &str = "menu.agent.open_chat";
pub const FEATURE_MENU_OPEN_TERMINAL: &str = "menu.agent.open_terminal";
pub const FEATURE_MENU_INFO: &str = "menu.agent.info";
pub const FEATURE_MENU_LIFECYCLE: &str = "menu.agent.lifecycle";
pub const FEATURE_MENU_RETIRE: &str = "menu.agent.retire";
pub const FEATURE_MENU_COPY_BRANCH: &str = "menu.agent.copy_branch";
pub const FEATURE_MENU_OPEN_PULL_REQUEST: &str = "menu.agent.open_pr";
pub const FEATURE_MENU_DESTROY: &str = "menu.agent.destroy";
pub const MODE_HOST: &str = "host";
pub const MODE_VM: &str = "vm";
pub const VM_DRIVER_LIMA: &str = "lima";
pub const VM_DRIVER_CLOUD_HYPERVISOR: &str = "cloud-hypervisor";
pub const VM_OFF: &str = "off";
pub const VM_STARTING: &str = "starting";
pub const VM_RUNNING: &str = "running";
pub const VM_PAUSED: &str = "paused";
pub const VM_STOPPING: &str = "stopping";
pub const VM_MISSING: &str = "missing";
pub const SPAN_LEVEL_WARN: &str = "warn";
pub const SPAN_SOURCE_APP: &str = "app";
