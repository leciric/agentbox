//! Settings' Models, Agents and Resources sections (NewAgentDefaults.tsx and
//! the switches beside it in SettingsView.tsx): what new agents and every
//! project's lead start on, what the daemon does with their pull requests and
//! idle machines, and how much disk it keeps for itself. Everything is a field
//! of GET/PATCH /v1/settings, and the choices are the ones the daemon read
//! from the AI tools (Settings.*Choices), never a list of this app's own.
//!
//! The VM's size and swap aren't here: Electron sets them through its main
//! process (the host-setup bridge), not the daemon, and this app has no such
//! bridge.
use std::rc::Rc;
use std::time::Duration;

use agentbox_api::client::Method;
use agentbox_api::{
    AgentPick, ChatOptionChoice, RemoveStoppedRequest, RemoveStoppedResult, Settings,
    StoppedMachines, UpdateSettingsRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::Input;
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{
    Btn, Choice, Row, button, labeled, note_in, settings_select as select,
    settings_select_at as select_at,
};
use super::kit::note;
use super::{Entry, Group, entry, group};
use crate::theme;

/// The number fields: typed, and saved as they lose focus or on Enter.
pub(super) const FIELDS: [&str; 8] = [
    "lead-sleep",
    "lead-recheck",
    "disk-min",
    "disk-percent",
    "image-max",
    "package-max",
    "agent-model-name",
    "lead-model-name",
];

const GIB: i64 = 1024 * 1024 * 1024;
const FULL_WINDOW: &str = "1000000";
const LEAD_SLEEP: i64 = 15;
const LEAD_RECHECK: i64 = 20;
const IDLE_TIMES: [i64; 5] = [1800, 3600, 7200, 14400, 28800];
const IDLE_FALLBACK: i64 = 7200;
const REMOVE_DAYS: [i64; 5] = [1, 3, 7, 14, 30];
const COMPACT_WINDOWS: [i64; 6] = [100_000, 150_000, 200_000, 300_000, 500_000, 1_000_000];
const RETENTIONS: [&str; 5] = ["immediately", "1d", "7d", "30d", "forever"];
const TOOLS: [&str; 5] = ["claude", "codex", "opencode", "cursor", "gemini"];

/// ai_label is an AI tool's name, which isn't translated (state.tsx's aiLabel).
pub(super) fn ai_label(ai: &str) -> &'static str {
    match ai {
        "claude" => "Claude Code",
        "codex" => "Codex",
        "opencode" => "OpenCode",
        "cursor" => "Cursor",
        "gemini" => "Gemini CLI",
        _ => "",
    }
}

/// decimal is a number with one decimal in the user's language: 1.5, 1,5.
fn decimal(n: f64, digits: usize) -> String {
    let s = format!("{n:.digits$}");
    if agentbox_i18n::language().starts_with("pt") {
        s.replace('.', ",")
    } else {
        s
    }
}

/// human_bytes reads a size as utils.ts's humanBytes: 0 B, 1.5 GiB.
pub(super) fn human_bytes(n: i64) -> String {
    let units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let (mut v, mut i) = (n as f64, 0);
    while v >= 1024. && i < units.len() - 1 {
        v /= 1024.;
        i += 1;
    }
    if i == 0 {
        format!("{n} B")
    } else {
        format!("{} {}", decimal(v, 1), units[i])
    }
}

/// parse_bytes reads "10GiB", "500 MB" or a plain count, as utils.ts's.
fn parse_bytes(text: &str) -> Option<i64> {
    let text = text.trim();
    let split = text
        .find(|c: char| !(c.is_ascii_digit() || c == '.'))
        .unwrap_or(text.len());
    let (num, unit) = text.split_at(split);
    let n: f64 = num.parse().ok()?;
    let mult = match unit.trim() {
        "" | "B" => 1.,
        "kB" => 1e3,
        "MB" => 1e6,
        "GB" => 1e9,
        "TB" => 1e12,
        "KiB" => 1024.,
        "MiB" => 1024f64.powi(2),
        "GiB" => 1024f64.powi(3),
        "TiB" => 1024f64.powi(4),
        _ => return None,
    };
    Some((n * mult).round() as i64)
}

/// size_field is a size as its field shows it: whole GiB when it is one.
fn size_field(n: i64) -> String {
    if n % GIB == 0 {
        format!("{}GiB", n / GIB)
    } else {
        human_bytes(n).replace(' ', "")
    }
}

/// format_tokens reads a count of tokens as chat.ts's: 200k, 1M, 1.5M.
pub(in crate::app) fn format_tokens(n: i64) -> String {
    if n >= 1_000_000 {
        let digits = if n % 1_000_000 == 0 { 0 } else { 1 };
        let m = decimal(n as f64 / 1e6, digits);
        tv("chat.tokens.millions", &[("n", (&m).into())])
    } else if n >= 1000 {
        tv("chat.tokens.thousands", &[("n", ((n + 500) / 1000).into())])
    } else {
        n.to_string()
    }
}

/// time_ago is utils.ts's timeAgo: just now, 5m ago, 3h ago, 2d ago.
pub(crate) fn time_ago(time: &str) -> String {
    let Ok(at) =
        humantime::parse_rfc3339_weak(time.split('.').next().unwrap_or(time).trim_end_matches('Z'))
    else {
        return String::new();
    };
    let s = std::time::SystemTime::now()
        .duration_since(at)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0);
    match s {
        ..45 => t("common.justNow"),
        45..3600 => tv("common.minutesAgo", &[("n", ((s + 30) / 60).max(1).into())]),
        3600..86400 => tv("common.hoursAgo", &[("n", ((s + 1800) / 3600).into())]),
        _ => tv("common.daysAgo", &[("n", ((s + 43200) / 86400).into())]),
    }
}

fn idle_words(seconds: i64) -> String {
    if seconds >= 3600 && seconds % 3600 == 0 {
        format!("{}h", seconds / 3600)
    } else {
        format!("{}m", seconds / 60)
    }
}

/// choice_name drops the "(recommended)" a badge says (modelChoices.ts).
fn choice_name(c: &ChatOptionChoice) -> String {
    let n = c.name.trim();
    let lower = n.to_lowercase();
    let name = if lower.ends_with("(recommended)") {
        n[..n.len() - "(recommended)".len()].trim_end()
    } else {
        n
    };
    if name.is_empty() {
        c.value.clone()
    } else {
        name.to_string()
    }
}

fn recommended(c: &ChatOptionChoice) -> bool {
    c.value == "default" || c.name.to_lowercase().contains("recommended")
}

fn with_default(label: &str) -> String {
    tv("defaults.newAgent.withDefault", &[("label", label.into())])
}

/// Role is whose model a row sets: new agents', or every project's lead's.
#[derive(Clone, Copy, PartialEq)]
enum Role {
    Agents,
    Lead,
}

impl Role {
    fn key(self) -> &'static str {
        match self {
            Role::Agents => "agents",
            Role::Lead => "lead",
        }
    }
    /// empty_model is what "" means: AgentBox's default for agents, Claude
    /// Code's own for the lead.
    fn empty_model(self) -> &'static str {
        match self {
            Role::Agents => "opus",
            Role::Lead => "default",
        }
    }
    fn model(self, s: &Settings) -> &str {
        match self {
            Role::Agents => &s.default_claude_model,
            Role::Lead => &s.default_lead_model,
        }
    }
    fn window(self, s: &Settings) -> &str {
        match self {
            Role::Agents => &s.default_agent_context_window,
            Role::Lead => &s.default_lead_context_window,
        }
    }
    fn save(self, model: String, window: Option<String>) -> UpdateSettingsRequest {
        match self {
            Role::Agents => UpdateSettingsRequest {
                default_claude_model: Some(model),
                default_agent_context_window: window,
                ..Default::default()
            },
            Role::Lead => UpdateSettingsRequest {
                default_lead_model: Some(model),
                default_lead_context_window: window,
                ..Default::default()
            },
        }
    }
    fn save_window(self, window: String) -> UpdateSettingsRequest {
        match self {
            Role::Agents => UpdateSettingsRequest {
                default_agent_context_window: Some(window),
                ..Default::default()
            },
            Role::Lead => UpdateSettingsRequest {
                default_lead_context_window: Some(window),
                ..Default::default()
            },
        }
    }
}

/// windows_of are the context windows a model has, smallest first, as the
/// daemon worked them out; one it never saw run has only the first default.
fn windows_of(s: &Settings, model: &str) -> Vec<i64> {
    let all = &s.claude_context_windows;
    all.get(model)
        .or_else(|| all.get(model.trim_end_matches("[1m]")))
        .cloned()
        .unwrap_or_else(|| {
            all.get("default")
                .map(|w| w.iter().take(1).copied().collect())
                .unwrap_or_default()
        })
}

/// model_request is the change picking a model makes: a model without the
/// 1M window takes the role's window back to the compact one in the same
/// request, which the daemon refuses otherwise.
fn model_request(s: &Settings, role: Role, model: &str) -> UpdateSettingsRequest {
    let windows = windows_of(
        s,
        if model.is_empty() {
            role.empty_model()
        } else {
            model
        },
    );
    let drop = role.window(s) == FULL_WINDOW && !windows.contains(&1_000_000);
    role.save(model.to_string(), drop.then(String::new))
}

/// The effort and mode menus: short lists the adapter advertised.
#[derive(Clone, Copy)]
enum Pickable {
    AgentEffort,
    LeadEffort,
    LeadMode,
}

impl Pickable {
    fn keys(self) -> (&'static str, &'static str, &'static str, &'static str) {
        // description, later, details, empty
        match self {
            Pickable::AgentEffort => (
                "defaults.newAgent.effortDescription",
                "defaults.newAgent.effortLevelsLater",
                "defaults.newAgent.effortDetails",
                "defaults.newAgent.effortDefault",
            ),
            Pickable::LeadEffort => (
                "defaults.newAgent.lead.effortDescription",
                "defaults.newAgent.effortLevelsLater",
                "defaults.newAgent.lead.effortDetails",
                "defaults.newAgent.lead.emptyName",
            ),
            Pickable::LeadMode => (
                "defaults.newAgent.lead.modeDescription",
                "defaults.newAgent.lead.modesLater",
                "defaults.newAgent.lead.modeDetails",
                "defaults.newAgent.lead.emptyName",
            ),
        }
    }
    fn choices(self, s: &Settings) -> &[ChatOptionChoice] {
        match self {
            Pickable::LeadMode => &s.claude_mode_choices,
            _ => &s.claude_effort_choices,
        }
    }
    fn value(self, s: &Settings) -> &str {
        match self {
            Pickable::AgentEffort => &s.default_claude_effort,
            Pickable::LeadEffort => &s.default_lead_effort,
            Pickable::LeadMode => &s.default_lead_mode,
        }
    }
    fn save(self, v: String) -> UpdateSettingsRequest {
        let mut req = UpdateSettingsRequest::default();
        match self {
            Pickable::AgentEffort => req.default_claude_effort = Some(v),
            Pickable::LeadEffort => req.default_lead_effort = Some(v),
            Pickable::LeadMode => req.default_lead_mode = Some(v),
        }
        req
    }
}

/// plain_choices are a tool's choices under an empty one, and the current
/// one kept, marked, when the tool stopped offering it.
fn plain_choices(empty: String, list: &[ChatOptionChoice], value: &str) -> Vec<Choice> {
    let mut out = vec![Choice::new("", empty)];
    out.extend(
        list.iter()
            .map(|c| Choice::new(c.value.clone(), choice_name(c))),
    );
    if !value.is_empty() && !list.iter().any(|c| c.value == value) {
        out.push(Choice::new(
            value.to_string(),
            tv("defaults.newAgent.unavailable", &[("value", value.into())]),
        ));
    }
    out
}

/// What the Agents section's preview of "remove stopped agents" read.
#[derive(Default)]
pub(super) struct Stopped {
    pub loaded: Option<Result<StoppedMachines, String>>,
    pub removing: bool,
    pub said: Option<String>,
}

impl AppView {
    pub(super) fn models_groups(&self) -> Vec<Group> {
        let s = self.settings_page.settings.as_ref();
        let changed = |f: fn(&Settings) -> bool| s.is_some_and(f);
        // Cursor's effort is a setting only for a model that has levels.
        let cursor_efforts = s.is_some_and(|s| {
            s.cursor_model_choices
                .iter()
                .any(|c| c.value == s.default_cursor_model && !c.efforts.is_empty())
        });
        let mut new_agents = vec![
            entry(
                "default-tool",
                "defaults.tool.label",
                "defaults.tool.description",
                "defaults.tool.keywords",
            )
            .modified(changed(|s| {
                !s.last_agent_pick.ai.is_empty() && s.last_agent_pick.ai != "claude"
            })),
            entry(
                "agent-model",
                "settings.entry.agent-model.label",
                "defaults.newAgent.agents.about",
                "settings.entry.agent-model.keywords",
            )
            .essential()
            .modified(changed(|s| !s.default_claude_model.is_empty())),
            entry(
                "agent-window",
                "settings.entry.agent-window.label",
                "defaults.newAgent.agents.windowAbout",
                "settings.entry.agent-window.keywords",
            )
            .modified(changed(|s| !s.default_agent_context_window.is_empty())),
            entry(
                "agent-effort",
                "settings.entry.agent-effort.label",
                "defaults.newAgent.effortDescription",
                "settings.entry.agent-effort.keywords",
            )
            .modified(changed(|s| !s.default_claude_effort.is_empty())),
            entry(
                "cursor-model",
                "settings.entry.cursor-model.label",
                "defaults.cursor.modelDescription",
                "settings.entry.cursor-model.keywords",
            )
            .modified(changed(|s| !s.default_cursor_model.is_empty())),
        ];
        if cursor_efforts {
            new_agents.push(
                entry(
                    "cursor-effort",
                    "settings.entry.cursor-effort.label",
                    "defaults.cursor.effortDescription",
                    "settings.entry.cursor-effort.keywords",
                )
                .modified(changed(|s| !s.default_cursor_effort.is_empty())),
            );
        }
        let mut enforce = entry(
            "enforce",
            "settings.entry.enforce.label",
            "settings.enforce.descriptionOff",
            "settings.entry.enforce.keywords",
        )
        .modified(changed(|s| s.enforce_agent_defaults));
        enforce.description = format!(
            "{} {}",
            t("settings.enforce.descriptionOff"),
            t("settings.enforce.descriptionOn")
        );
        new_agents.extend([
            entry(
                "gemini-model",
                "settings.entry.gemini-model.label",
                "defaults.gemini.modelDescription",
                "settings.entry.gemini-model.keywords",
            )
            .modified(changed(|s| !s.default_gemini_model.is_empty())),
            enforce,
        ]);
        vec![
            group("new-agents", "settings.group.new-agents.title", new_agents)
                .described("settings.group.new-agents.description"),
            group(
                "lead",
                "settings.group.lead.title",
                vec![
                    entry(
                        "lead-model",
                        "settings.entry.lead-model.label",
                        "defaults.newAgent.lead.about",
                        "settings.entry.lead-model.keywords",
                    )
                    .modified(changed(|s| !s.default_lead_model.is_empty())),
                    entry(
                        "lead-window",
                        "settings.entry.lead-window.label",
                        "defaults.newAgent.lead.windowAbout",
                        "settings.entry.lead-window.keywords",
                    )
                    .modified(changed(|s| !s.default_lead_context_window.is_empty())),
                    entry(
                        "lead-effort",
                        "settings.entry.lead-effort.label",
                        "defaults.newAgent.lead.effortDescription",
                        "settings.entry.lead-effort.keywords",
                    )
                    .modified(changed(|s| !s.default_lead_effort.is_empty())),
                    entry(
                        "lead-mode",
                        "settings.entry.lead-mode.label",
                        "defaults.newAgent.lead.modeDescription",
                        "settings.entry.lead-mode.keywords",
                    )
                    .modified(changed(|s| !s.default_lead_mode.is_empty())),
                ],
            )
            .described("settings.group.lead.description"),
            group(
                "tool-versions",
                "settings.group.tool-versions.title",
                vec![
                    entry(
                        "claude-code-update",
                        "settings.entry.claude-code-update.label",
                        "settings.claudeCodeUpdate.description",
                        "settings.entry.claude-code-update.keywords",
                    )
                    .modified(changed(|s| s.claude_code_update.on)),
                ],
            ),
            group(
                "chats",
                "settings.group.chats.title",
                vec![
                    entry(
                        "compact",
                        "settings.entry.compact.label",
                        "defaults.newAgent.compactDescription",
                        "settings.entry.compact.keywords",
                    )
                    .modified(changed(|s| {
                        s.claude_compact_window != s.default_claude_compact_window
                    })),
                    entry(
                        "resume",
                        "settings.entry.resume.label",
                        "defaults.newAgent.resumeDescription",
                        "settings.entry.resume.keywords",
                    )
                    .modified(changed(|s| !s.resume_after_limit)),
                    entry(
                        "continue-after-restart",
                        "settings.entry.continueAfterRestart.label",
                        "defaults.newAgent.continueDescription",
                        "settings.entry.continueAfterRestart.keywords",
                    )
                    .modified(changed(|s| !s.continue_after_restart)),
                    entry(
                        "lead-sleep",
                        "settings.entry.leadSleep.label",
                        "defaults.newAgent.sleepDescription",
                        "settings.entry.leadSleep.keywords",
                    )
                    .modified(changed(|s| {
                        !s.lead_sleep || s.lead_sleep_minutes != LEAD_SLEEP
                    })),
                ],
            )
            .described("settings.group.chats.description"),
        ]
    }

    pub(super) fn agents_groups(&self) -> Vec<Group> {
        let s = self.settings_page.settings.as_ref();
        let changed = |f: fn(&Settings) -> bool| s.is_some_and(f);
        let mut remove = entry(
            "remove-stopped",
            "settings.entry.remove-stopped.label",
            "",
            "settings.entry.remove-stopped.keywords",
        )
        .modified(changed(|s| s.remove_stopped || s.remove_stopped_days != 7));
        remove.description = tv(
            "defaults.newAgent.removeStoppedDescription",
            &[("days", 7i64.into())],
        );
        vec![
            group(
                "pulls",
                "settings.group.pulls.title",
                vec![
                    entry(
                        "pr-watch",
                        "settings.entry.pr-watch.label",
                        "settings.prWatch.description",
                        "settings.entry.pr-watch.keywords",
                    )
                    .modified(changed(|s| !s.pr_watch)),
                    entry(
                        "merge-when-green",
                        "settings.entry.merge-when-green.label",
                        "settings.mergeWhenGreen.description",
                        "settings.entry.merge-when-green.keywords",
                    )
                    .modified(changed(|s| s.merge_when_green)),
                ],
            )
            .described("settings.group.pulls.description"),
            group(
                "recheck",
                "settings.group.recheck.title",
                vec![
                    entry(
                        "lead-recheck",
                        "settings.entry.lead-recheck.label",
                        "defaults.newAgent.recheckDescription",
                        "settings.entry.lead-recheck.keywords",
                    )
                    .modified(changed(|s| s.lead_recheck)),
                ],
            )
            .described("settings.group.recheck.description"),
            group(
                "lifecycle",
                "settings.group.lifecycle.title",
                vec![
                    entry(
                        "auto-stop",
                        "settings.entry.auto-stop.label",
                        "defaults.newAgent.idleDescription",
                        "settings.entry.auto-stop.keywords",
                    )
                    .modified(changed(|s| s.auto_stop_idle)),
                    remove,
                ],
            ),
            group(
                "machines",
                "settings.group.machines.title",
                vec![
                    entry(
                        "agent-gpu",
                        "settings.entry.agent-gpu.label",
                        "settings.agentGPU.description",
                        "settings.entry.agent-gpu.keywords",
                    )
                    .modified(changed(|s| s.agent_gpu)),
                ],
            ),
        ]
    }

    pub(super) fn resources_groups(&self) -> Vec<Group> {
        let s = self.settings_page.settings.as_ref();
        let changed = |f: fn(&Settings) -> bool| s.is_some_and(f);
        vec![group(
            "disk",
            "settings.group.disk.title",
            vec![
                entry(
                    "disk-floor",
                    "settings.entry.disk-floor.label",
                    "defaults.newAgent.diskDescription",
                    "settings.entry.disk-floor.keywords",
                )
                .modified(changed(|s| {
                    s.disk_floor_min != 10 * GIB || s.disk_floor_percent != 5.
                })),
                entry(
                    "media-retention",
                    "settings.entry.media-retention.label",
                    "defaults.newAgent.mediaDescription",
                    "settings.entry.media-retention.keywords",
                )
                .modified(changed(|s| s.media_retention != "1d")),
                entry(
                    "docker-prune",
                    "settings.entry.docker-prune.label",
                    "defaults.newAgent.pruneDescription",
                    "settings.entry.docker-prune.keywords",
                )
                .modified(changed(|s| !s.docker_prune_on_stop)),
                entry(
                    "docker-image-cache",
                    "settings.entry.docker-image-cache.label",
                    "defaults.newAgent.imageCacheDescription",
                    "settings.entry.docker-image-cache.keywords",
                )
                .modified(changed(|s| {
                    !s.image_cache || s.image_cache_max_bytes != s.default_image_cache_max_bytes
                })),
                entry(
                    "package-caches",
                    "settings.entry.package-caches.label",
                    "defaults.newAgent.pkgCacheDescription",
                    "settings.entry.package-caches.keywords",
                )
                .modified(changed(|s| {
                    !s.package_cache
                        || s.package_cache_max_bytes != s.default_package_cache_max_bytes
                })),
                entry(
                    "local-node-modules",
                    "settings.entry.local-node-modules.label",
                    "defaults.newAgent.localNodeModulesDescription",
                    "settings.entry.local-node-modules.keywords",
                )
                .modified(changed(|s| !s.local_node_modules)),
            ],
        )]
    }

    /// sync_fields writes the settings into the number fields, but not into
    /// the one being typed in.
    pub(super) fn sync_fields(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(s) = self.settings_page.settings.clone() else {
            return;
        };
        let values = [
            ("lead-sleep", s.lead_sleep_minutes.to_string()),
            ("lead-recheck", s.lead_recheck_minutes.to_string()),
            ("disk-min", size_field(s.disk_floor_min)),
            ("disk-percent", format!("{}%", s.disk_floor_percent)),
            ("image-max", size_field(s.image_cache_max_bytes)),
            ("package-max", size_field(s.package_cache_max_bytes)),
        ];
        for (name, value) in values {
            let Some(input) = self.settings_page.fields.get(name) else {
                continue;
            };
            let focused = input.read(cx).focus_handle(cx).is_focused(window);
            if !focused && input.read(cx).value() != value {
                input.update(cx, |i, cx| i.set_value(value, window, cx));
            }
        }
    }

    fn field_text(&self, name: &str, cx: &App) -> String {
        self.settings_page
            .fields
            .get(name)
            .map(|i| i.read(cx).value().trim().to_string())
            .unwrap_or_default()
    }

    /// commit_field saves what a number field holds, when it changed.
    pub(super) fn commit_field(&mut self, name: &str, window: &mut Window, cx: &mut Context<Self>) {
        let Some(s) = self.settings_page.settings.clone() else {
            return;
        };
        let text = self.field_text(name, cx);
        let minutes = |fallback: i64| -> i64 {
            text.parse::<f64>()
                .ok()
                .filter(|n| *n > 0.)
                .map_or(fallback, |n| n.round() as i64)
                .clamp(5, 1440)
        };
        let size = |example: &str| -> Result<i64, String> {
            if text.is_empty() {
                return Ok(0);
            }
            parse_bytes(&text)
                .ok_or_else(|| tv("defaults.newAgent.sizeLike", &[("example", example.into())]))
        };
        let mut req = UpdateSettingsRequest::default();
        let res: Result<bool, String> = match name {
            "lead-sleep" => {
                let n = minutes(LEAD_SLEEP);
                req.lead_sleep_minutes = Some(n);
                Ok(n != s.lead_sleep_minutes)
            }
            "lead-recheck" => {
                let n = minutes(LEAD_RECHECK);
                req.lead_recheck_minutes = Some(n);
                Ok(n != s.lead_recheck_minutes)
            }
            "disk-min" => size("10GiB").map(|n| {
                req.disk_floor_min = Some(n);
                n != s.disk_floor_min
            }),
            "disk-percent" => {
                if text.is_empty() {
                    req.disk_floor_percent = Some(-1.);
                    Ok(true)
                } else {
                    match text.trim_end_matches('%').trim().parse::<f64>() {
                        Ok(n) if n >= 0. => {
                            req.disk_floor_percent = Some(n);
                            Ok(n != s.disk_floor_percent)
                        }
                        _ => Err(tv(
                            "defaults.newAgent.percentLike",
                            &[("example", "5%".into())],
                        )),
                    }
                }
            }
            "image-max" => size("20GiB").map(|n| {
                req.image_cache_max_bytes = Some(n);
                n != s.image_cache_max_bytes
            }),
            "package-max" => size("10GiB").map(|n| {
                req.package_cache_max_bytes = Some(n);
                n != s.package_cache_max_bytes
            }),
            "agent-model-name" | "lead-model-name" => {
                // "default" is the menu's word for no model of one's own:
                // written as a model, every turn fails.
                let role = if name == "agent-model-name" {
                    Role::Agents
                } else {
                    Role::Lead
                };
                if text.is_empty() || text == "default" {
                    return;
                }
                req = model_request(&s, role, &text);
                if let Some(i) = self.settings_page.fields.get(name) {
                    i.update(cx, |i, cx| i.set_value("", window, cx));
                }
                Ok(true)
            }
            _ => Ok(false),
        };
        match res {
            Ok(true) => self.save_in(req, window, cx),
            Ok(false) => self.sync_fields(window, cx),
            Err(e) => {
                self.error = Some(e.into());
                cx.notify();
            }
        }
    }

    /// save_in is save, then the number fields read what was saved.
    pub(super) fn save_in(
        &mut self,
        req: UpdateSettingsRequest,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let body = serde_json::to_vec(&req).unwrap_or_default();
            let res = client.raw(Method::PATCH, "/v1/settings", Some(body)).await;
            this.update_in(cx, |this, window, cx| {
                match res.map(|b| serde_json::from_slice::<Settings>(&b)) {
                    Ok(Ok(s)) => {
                        this.settings_page.settings = Some(s);
                        this.error = None;
                    }
                    Ok(Err(e)) => crate::toasts::error(cx, e.to_string()),
                    Err(e) => crate::toasts::error(cx, e.to_string()),
                }
                this.sync_fields(window, cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// watch_claude_code reads the settings again every two seconds while
    /// Claude Code's version check or its update runs.
    pub(super) fn watch_claude_code(&mut self, cx: &mut Context<Self>) {
        let checking = self
            .settings_page
            .settings
            .as_ref()
            .is_some_and(|s| s.claude_code_update.checking);
        if !checking || self.settings_page.watching_claude_code {
            return;
        }
        self.settings_page.watching_claude_code = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor().timer(Duration::from_secs(2)).await;
                let s = client.get::<Settings>("/v1/settings").await;
                let more = this
                    .update(cx, |this, cx| {
                        if let Ok(s) = s {
                            this.settings_page.settings = Some(s);
                        }
                        cx.notify();
                        let more = this.settings
                            && this
                                .settings_page
                                .settings
                                .as_ref()
                                .is_some_and(|s| s.claude_code_update.checking);
                        this.settings_page.watching_claude_code = more;
                        more
                    })
                    .unwrap_or(false);
                if !more {
                    return;
                }
            }
        })
        .detach();
    }

    /// load_stopped reads what removing stopped agents' machines would do.
    pub(super) fn load_stopped(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<StoppedMachines>("/v1/stopped-machines?check=1")
                .await;
            this.update(cx, |this, cx| {
                this.settings_page.stopped.loaded = Some(res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn remove_stopped_now(&mut self, refs: Vec<String>, cx: &mut Context<Self>) {
        let client = self.client.clone();
        self.settings_page.stopped.removing = true;
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, RemoveStoppedResult>(
                    "/v1/stopped-machines/remove",
                    &RemoveStoppedRequest { agents: refs },
                )
                .await;
            this.update(cx, |this, cx| {
                let st = &mut this.settings_page.stopped;
                st.removing = false;
                match res {
                    Ok(r) => {
                        let mut said = Vec::new();
                        if !r.removed.is_empty() {
                            said.push(tv(
                                "defaults.newAgent.removeStoppedDone",
                                &[("count", r.removed.len().into())],
                            ));
                        }
                        if !r.kept.is_empty() {
                            said.push(tv(
                                "defaults.newAgent.removeStoppedKept",
                                &[("count", r.kept.len().into())],
                            ));
                        }
                        st.said = Some(said.join(" "));
                    }
                    Err(e) => crate::toasts::error(cx, e.to_string()),
                }
                this.load_stopped(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// select_row is a setting that's a select over one field of Settings.
    #[allow(clippy::too_many_arguments)]
    fn select_row(
        &self,
        e: &Entry,
        details: Option<String>,
        choices: Vec<Choice>,
        current: &str,
        disabled: bool,
        set: impl Fn(&str) -> UpdateSettingsRequest + 'static,
        cx: &mut Context<Self>,
    ) -> Row {
        let mut row = self.more_row(e, details, cx);
        row.control = Some(select(
            e.id,
            choices,
            current,
            disabled || self.settings_page.settings.is_none(),
            Rc::new(cx.listener(move |this, v: &str, _, cx| this.save(set(v), cx))),
        ));
        row
    }

    /// field is one of the number fields, with what it's measured in.
    fn field(&self, name: &str, width: f32) -> AnyElement {
        match self.settings_page.fields.get(name) {
            Some(i) => div()
                .w(px(width))
                .child(Input::new(i).small())
                .into_any_element(),
            None => div().into_any_element(),
        }
    }

    /// minutes_body is a number of minutes under a switch that's on.
    fn minutes_body(&self, name: &str, minutes: i64, fallback: i64) -> AnyElement {
        h_flex()
            .gap_2()
            .child(self.field(name, 96.))
            .child(
                div()
                    .text_size(theme::text::UI)
                    .text_color(theme::muted())
                    .child(if minutes == fallback {
                        t("defaults.newAgent.minutesDefault")
                    } else {
                        t("defaults.newAgent.minutes")
                    }),
            )
            .into_any_element()
    }

    /// accounts_link opens Accounts, where a tool's sign-in is.
    fn accounts_link(&self, cx: &mut Context<Self>) -> AnyElement {
        div()
            .id("defaults-accounts-link")
            .text_size(theme::text::CAPTION)
            .font_weight(FontWeight::MEDIUM)
            .text_color(theme::brand(300))
            .cursor_pointer()
            .hover(|s| s.text_color(theme::brand(200)).underline())
            .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                this.open_settings_section("accounts".into(), window, cx)
            }))
            .child(t("defaults.openAccounts"))
            .into_any_element()
    }

    fn model_row(&self, e: &Entry, role: Role, cx: &mut Context<Self>) -> Row {
        let s = self.settings_page.settings.as_ref();
        let r = role.key();
        let mut row = self.more_row(e, Some(t(&format!("defaults.newAgent.{r}.more"))), cx);
        let list = s
            .map(|s| s.claude_model_choices.clone())
            .unwrap_or_default();
        let value = s.map(|s| role.model(s).to_string()).unwrap_or_default();
        let mut choices = vec![
            Choice::new("", t(&format!("defaults.newAgent.{r}.emptyName")))
                .described(t(&format!("defaults.newAgent.{r}.emptyDescription"))),
        ];
        // A model this account stopped offering keeps its place, marked.
        if !value.is_empty() && !list.iter().any(|c| c.value == value) {
            let mut c = Choice::new(
                value.clone(),
                format!("{value} · {}", t("defaults.newAgent.offMenu")),
            )
            .described(t("defaults.newAgent.offMenuNote"));
            c.disabled = true;
            choices.push(c);
        }
        for c in &list {
            let mut name = choice_name(c);
            if recommended(c) {
                name = format!("{name} · {}", t("defaults.newAgent.recommended"));
            }
            let mut choice = Choice::new(c.value.clone(), name).described(c.description.clone());
            choice.group = c.group.clone();
            choices.push(choice);
        }
        let mut control = select(
            e.id,
            choices,
            &value,
            s.is_none(),
            Rc::new(cx.listener(move |this, v: &str, _, cx| {
                if let Some(s) = this.settings_page.settings.as_ref() {
                    let req = model_request(s, role, v);
                    this.save(req, cx)
                }
            })),
        );
        if !value.is_empty() && !list.iter().any(|c| c.value == value) {
            control = h_flex()
                .gap_1p5()
                .child(div().text_color(theme::starting()).child("!"))
                .child(control)
                .into_any_element();
        }
        row.control = Some(control);
        if s.is_some_and(|s| !s.claude_menu_known) {
            row.body.push(note(t("defaults.newAgent.onlyOwnPicks")));
        }
        // ModelByName: a model the menu doesn't list, named.
        let name = if role == Role::Agents {
            "agent-model-name"
        } else {
            "lead-model-name"
        };
        row.body.push(
            v_flex()
                .gap_1()
                .child(
                    div()
                        .text_xs()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .child(t("chat.modelByName.label")),
                )
                .child(h_flex().gap_2().child(self.field(name, 240.)).child(button(
                    SharedString::from(format!("{name}-use")),
                    t("chat.modelByName.use"),
                    Btn::Secondary,
                    s.is_none(),
                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.commit_field(name, window, cx)
                    }),
                )))
                .child(note(t("chat.modelByName.hint")))
                .into_any_element(),
        );
        row
    }

    fn window_row(&self, e: &Entry, role: Role, cx: &mut Context<Self>) -> Row {
        let s = self.settings_page.settings.as_ref();
        let r = role.key();
        let windows = s
            .map(|s| {
                let m = role.model(s);
                windows_of(s, if m.is_empty() { role.empty_model() } else { m })
            })
            .unwrap_or_default();
        let standard = windows
            .first()
            .copied()
            .or(s.map(|s| s.claude_compact_window))
            .unwrap_or(500_000);
        let has_full = windows.contains(&1_000_000) && standard != 1_000_000;
        let value = s.map(|s| role.window(s).to_string()).unwrap_or_default();
        let details = format!(
            "{} {}",
            t(&format!("defaults.newAgent.{r}.windowMore")),
            t("defaults.newAgent.windowCost")
        );
        let mut choices = vec![Choice::new(
            "",
            with_default(&if standard > 0 {
                format_tokens(standard)
            } else {
                t("defaults.newAgent.wholeWindow")
            }),
        )];
        if has_full || value == FULL_WINDOW {
            choices.push(Choice::new(FULL_WINDOW, t("defaults.newAgent.fullWindow")));
        }
        let mut row = self.select_row(
            e,
            Some(details),
            choices,
            &value,
            false,
            move |v| role.save_window(v.to_string()),
            cx,
        );
        if s.is_some() && !has_full {
            row.description = format!(
                "{} {}",
                row.description,
                t("defaults.newAgent.noFullWindow")
            );
        }
        row
    }

    fn pick_row(&self, e: &Entry, p: Pickable, cx: &mut Context<Self>) -> Row {
        let s = self.settings_page.settings.as_ref();
        let (_, later, details, empty) = p.keys();
        let list = s.map(|s| p.choices(s).to_vec()).unwrap_or_default();
        let value = s.map(|s| p.value(s).to_string()).unwrap_or_default();
        let mut row = self.select_row(
            e,
            Some(t(details)),
            plain_choices(t(empty), &list, &value),
            &value,
            false,
            move |v| p.save(v.to_string()),
            cx,
        );
        if s.is_some() && list.is_empty() {
            row.description = format!("{} {}", row.description, t(later));
        }
        row
    }

    /// defaults_row is the row of one of these sections' settings, or None
    /// for a setting that isn't theirs.
    pub(super) fn defaults_row(&self, e: &Entry, cx: &mut Context<Self>) -> Option<Row> {
        let s = self.settings_page.settings.as_ref();
        let req = UpdateSettingsRequest::default;
        let row =
            match e.id {
                "default-tool" => {
                    let current = s
                        .map(|s| s.last_agent_pick.ai.as_str())
                        .filter(|ai| TOOLS.contains(ai))
                        .unwrap_or("claude");
                    let choices = TOOLS
                        .iter()
                        .map(|ai| Choice::new(*ai, ai_label(ai)))
                        .collect();
                    self.select_row(
                        e,
                        Some(t("defaults.tool.details")),
                        choices,
                        current,
                        false,
                        |v| UpdateSettingsRequest {
                            last_agent_pick: Some(AgentPick {
                                ai: v.to_string(),
                                ..Default::default()
                            }),
                            ..Default::default()
                        },
                        cx,
                    )
                }
                "agent-model" => self.model_row(e, Role::Agents, cx),
                "lead-model" => self.model_row(e, Role::Lead, cx),
                "agent-window" => self.window_row(e, Role::Agents, cx),
                "lead-window" => self.window_row(e, Role::Lead, cx),
                "agent-effort" => self.pick_row(e, Pickable::AgentEffort, cx),
                "lead-effort" => self.pick_row(e, Pickable::LeadEffort, cx),
                "lead-mode" => self.pick_row(e, Pickable::LeadMode, cx),
                "cursor-model" | "gemini-model" => {
                    let cursor = e.id == "cursor-model";
                    let (list, value, ready) = match s {
                        Some(s) if cursor => (
                            s.cursor_model_choices.clone(),
                            s.default_cursor_model.clone(),
                            s.cursor_ready,
                        ),
                        Some(s) => (
                            s.gemini_model_choices.clone(),
                            s.default_gemini_model.clone(),
                            s.gemini_ready,
                        ),
                        None => (Vec::new(), String::new(), false),
                    };
                    let tool = if cursor { "cursor" } else { "gemini" };
                    let mut row = self.select_row(
                        e,
                        None,
                        plain_choices(t(&format!("defaults.{tool}.modelAuto")), &list, &value),
                        &value,
                        !ready,
                        move |v| {
                            let mut r = UpdateSettingsRequest::default();
                            if cursor {
                                r.default_cursor_model = Some(v.to_string());
                            } else {
                                r.default_gemini_model = Some(v.to_string());
                            }
                            r
                        },
                        cx,
                    );
                    if s.is_some() && !ready {
                        row.body.push(
                            h_flex()
                                .flex_wrap()
                                .gap_2()
                                .child(note_in(
                                    t(&format!("defaults.{tool}.signIn")),
                                    theme::amber(300),
                                ))
                                .child(self.accounts_link(cx))
                                .into_any_element(),
                        );
                    } else if s.is_some() && list.is_empty() {
                        row.body
                            .push(note(t(&format!("defaults.{tool}.listLater"))));
                    }
                    row
                }
                "cursor-effort" => {
                    let (efforts, value, ready) = s
                        .map(|s| {
                            (
                                s.cursor_model_choices
                                    .iter()
                                    .find(|c| c.value == s.default_cursor_model)
                                    .map(|c| c.efforts.clone())
                                    .unwrap_or_default(),
                                s.default_cursor_effort.clone(),
                                s.cursor_ready,
                            )
                        })
                        .unwrap_or_default();
                    let mut choices = vec![Choice::new("", t("defaults.cursor.effortDefault"))];
                    choices.extend(efforts.iter().map(|l| Choice::new(l.clone(), l.clone())));
                    self.select_row(
                        e,
                        None,
                        choices,
                        &value,
                        !ready,
                        |v| UpdateSettingsRequest {
                            default_cursor_effort: Some(v.to_string()),
                            ..Default::default()
                        },
                        cx,
                    )
                }
                "enforce" => {
                    let on = s.is_some_and(|s| s.enforce_agent_defaults);
                    let details = format!(
                        "{} {}",
                        t(if on {
                            "settings.enforce.detailsOn"
                        } else {
                            "settings.enforce.detailsOff"
                        }),
                        t("settings.enforce.detailsNote")
                    );
                    let mut row = self.switch_row(
                        e,
                        "",
                        |s| s.enforce_agent_defaults,
                        |v| UpdateSettingsRequest {
                            enforce_agent_defaults: Some(v),
                            ..Default::default()
                        },
                        cx,
                    );
                    row.description = t(if on {
                        "settings.enforce.descriptionOn"
                    } else {
                        "settings.enforce.descriptionOff"
                    });
                    row.details = Some(details);
                    self.wire_more(&mut row, e.id, cx);
                    row
                }
                "claude-code-update" => {
                    let u = s.map(|s| s.claude_code_update.clone());
                    let pinned = u.as_ref().map(|u| u.pinned.clone()).unwrap_or_default();
                    let mut row = self.switch_row(
                        e,
                        "",
                        |s| s.claude_code_update.on,
                        |v| UpdateSettingsRequest {
                            claude_code_auto_update: Some(v),
                            ..Default::default()
                        },
                        cx,
                    );
                    row.details = Some(tv(
                        "settings.claudeCodeUpdate.details",
                        &[("pinned", (&pinned).into())],
                    ));
                    self.wire_more(&mut row, e.id, cx);
                    if let Some(u) = u {
                        let installed = if u.installed.is_empty() {
                            u.pinned.clone()
                        } else {
                            u.installed.clone()
                        };
                        let mut line = h_flex().flex_wrap().items_center().gap_4().child(
                            div()
                                .font_family(theme::mono())
                                .text_xs()
                                .text_color(theme::muted())
                                .child(tv(
                                    "settings.claudeCodeUpdate.installed",
                                    &[("version", (&installed).into())],
                                )),
                        );
                        if u.on {
                            if !u.latest.is_empty() {
                                line = line.child(
                                    div()
                                        .font_family(theme::mono())
                                        .text_xs()
                                        .text_color(theme::muted())
                                        .child(tv(
                                            "settings.claudeCodeUpdate.latest",
                                            &[("version", (&u.latest).into())],
                                        )),
                                );
                            }
                            line = line
                                .child(note(match &u.checked_at {
                                    Some(at) => tv(
                                        "settings.claudeCodeUpdate.checked",
                                        &[("when", (&time_ago(at)).into())],
                                    ),
                                    None => t("settings.claudeCodeUpdate.neverChecked"),
                                }))
                                .child(button(
                                    "claude-code-check",
                                    if u.checking {
                                        t("settings.claudeCodeUpdate.checking")
                                    } else {
                                        t("settings.claudeCodeUpdate.checkNow")
                                    },
                                    Btn::Secondary,
                                    u.checking,
                                    cx.listener(|this, _: &ClickEvent, _, cx| {
                                        this.save(
                                            UpdateSettingsRequest {
                                                check_claude_code: true,
                                                ..Default::default()
                                            },
                                            cx,
                                        )
                                    }),
                                ));
                        }
                        row.body.push(line.into_any_element());
                        if u.on && !u.checking && !u.error.is_empty() {
                            row.body.push(note_in(
                                tv(
                                    "settings.claudeCodeUpdate.failed",
                                    &[
                                        ("reason", (&u.error).into()),
                                        ("version", (&installed).into()),
                                    ],
                                ),
                                theme::rose(300),
                            ));
                        }
                        if u.on
                            && !u.checking
                            && u.error.is_empty()
                            && !u.latest.is_empty()
                            && u.latest == u.installed
                        {
                            row.body.push(note(t("settings.claudeCodeUpdate.upToDate")));
                        }
                    }
                    row
                }
                "compact" => {
                    let fallback = s.map_or(500_000, |s| s.default_claude_compact_window);
                    let value = s.map_or(fallback, |s| s.claude_compact_window);
                    let words = |n: i64| {
                        let w = tv(
                            "defaults.newAgent.tokens",
                            &[("count", (&format_tokens(n)).into())],
                        );
                        if n == fallback { with_default(&w) } else { w }
                    };
                    let mut choices: Vec<Choice> = COMPACT_WINDOWS
                        .iter()
                        .map(|n| Choice::new(n.to_string(), words(*n)))
                        .collect();
                    if value != 0 && !COMPACT_WINDOWS.contains(&value) {
                        choices.push(Choice::new(value.to_string(), words(value)));
                    }
                    choices.push(Choice::new("0", t("defaults.newAgent.wholeWindow")));
                    self.select_row(
                        e,
                        Some(t("defaults.newAgent.compactDetails")),
                        choices,
                        &value.to_string(),
                        false,
                        |v| UpdateSettingsRequest {
                            claude_compact_window: v.parse().ok(),
                            ..Default::default()
                        },
                        cx,
                    )
                }
                "resume" => self.switch_row(
                    e,
                    "defaults.newAgent.resumeDetails",
                    |s| s.resume_after_limit,
                    |v| UpdateSettingsRequest {
                        resume_after_limit: Some(v),
                        ..Default::default()
                    },
                    cx,
                ),
                "continue-after-restart" => self.switch_row(
                    e,
                    "defaults.newAgent.continueDetails",
                    |s| s.continue_after_restart,
                    |v| UpdateSettingsRequest {
                        continue_after_restart: Some(v),
                        ..Default::default()
                    },
                    cx,
                ),
                "lead-sleep" => {
                    let mut row = self.switch_row(
                        e,
                        "defaults.newAgent.sleepDetails",
                        |s| s.lead_sleep,
                        |v| UpdateSettingsRequest {
                            lead_sleep: Some(v),
                            ..Default::default()
                        },
                        cx,
                    );
                    if let Some(s) = s.filter(|s| s.lead_sleep) {
                        row.body.push(self.minutes_body(
                            "lead-sleep",
                            s.lead_sleep_minutes,
                            LEAD_SLEEP,
                        ));
                    }
                    row
                }
                "pr-watch" => self.switch_row(
                    e,
                    "settings.prWatch.details",
                    |s| s.pr_watch,
                    |v| UpdateSettingsRequest {
                        pr_watch: Some(v),
                        ..Default::default()
                    },
                    cx,
                ),
                "merge-when-green" => self.switch_row(
                    e,
                    "settings.mergeWhenGreen.details",
                    |s| s.merge_when_green,
                    |v| UpdateSettingsRequest {
                        merge_when_green: Some(v),
                        ..Default::default()
                    },
                    cx,
                ),
                "lead-recheck" => {
                    let mut row = self.switch_row(
                        e,
                        "",
                        |s| s.lead_recheck,
                        |v| UpdateSettingsRequest {
                            lead_recheck: Some(v),
                            ..Default::default()
                        },
                        cx,
                    );
                    if let Some(s) = s.filter(|s| s.lead_recheck) {
                        row.body.push(self.minutes_body(
                            "lead-recheck",
                            s.lead_recheck_minutes,
                            LEAD_RECHECK,
                        ));
                    }
                    row
                }
                "auto-stop" => {
                    let mut row = self.switch_row(
                        e,
                        "defaults.newAgent.idleDetails",
                        |s| s.auto_stop_idle,
                        |v| UpdateSettingsRequest {
                            auto_stop_idle: Some(v),
                            ..Default::default()
                        },
                        cx,
                    );
                    if let Some(s) = s.filter(|s| s.auto_stop_idle) {
                        let value = if s.idle_time_seconds > 0 {
                            s.idle_time_seconds
                        } else {
                            IDLE_FALLBACK
                        };
                        let mut times = IDLE_TIMES.to_vec();
                        if !times.contains(&value) {
                            times.push(value);
                        }
                        let choices = times
                            .iter()
                            .map(|n| {
                                let w = idle_words(*n);
                                Choice::new(
                                    n.to_string(),
                                    if *n == IDLE_FALLBACK {
                                        with_default(&w)
                                    } else {
                                        w
                                    },
                                )
                            })
                            .collect();
                        row.body.push(select_at(
                            "idle-time",
                            choices,
                            &value.to_string(),
                            false,
                            Rc::new(cx.listener(|this, v: &str, _, cx| {
                                this.save(
                                    UpdateSettingsRequest {
                                        idle_time_seconds: v.parse().ok(),
                                        ..Default::default()
                                    },
                                    cx,
                                )
                            })),
                            Anchor::TopLeft,
                        ));
                    }
                    row
                }
                "remove-stopped" => self.remove_stopped_row(e, cx),
                "agent-gpu" => {
                    let gpu = s.map(|s| s.vm_gpu.clone());
                    let unavailable = gpu
                        .as_ref()
                        .is_some_and(|g| g.kind.is_empty() && g.setup.is_empty());
                    let on = !unavailable && s.is_some_and(|s| s.agent_gpu);
                    let mut row = self.more_row(e, Some(t("settings.agentGPU.details")), cx);
                    row.control = Some(super::super::controls::toggle(
                        "switch-agent-gpu",
                        on,
                        s.is_none() || unavailable,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.save(
                                UpdateSettingsRequest {
                                    agent_gpu: Some(!on),
                                    ..Default::default()
                                },
                                cx,
                            )
                        }),
                    ));
                    if let Some(g) = gpu {
                        row.body.push(note(if unavailable {
                            tv(
                                "settings.agentGPU.unavailable",
                                &[("reason", (&g.reason).into())],
                            )
                        } else {
                            vm_gpu_note(&g)
                        }));
                    }
                    row
                }
                "disk-floor" => {
                    let mut row = self.more_row(e, Some(t("defaults.newAgent.diskDetails")), cx);
                    row.body.push(
                        h_flex()
                            .flex_wrap()
                            .items_start()
                            .gap_3()
                            .child(labeled(
                                t("defaults.newAgent.atLeast"),
                                v_flex()
                                    .gap_1()
                                    .child(self.field("disk-min", 180.))
                                    .child(note(t("defaults.newAgent.atLeastHint"))),
                            ))
                            .child(labeled(
                                t("defaults.newAgent.share"),
                                v_flex()
                                    .gap_1()
                                    .child(self.field("disk-percent", 180.))
                                    .child(note(t("defaults.newAgent.shareHint"))),
                            ))
                            .into_any_element(),
                    );
                    row
                }
                "media-retention" => {
                    let choices = RETENTIONS
                        .iter()
                        .map(|r| {
                            let w = t(&format!("defaults.newAgent.retention.{r}"));
                            Choice::new(*r, if *r == "1d" { with_default(&w) } else { w })
                        })
                        .collect();
                    let value = s.map_or("1d".to_string(), |s| s.media_retention.clone());
                    self.select_row(
                        e,
                        Some(t("defaults.newAgent.mediaDetails")),
                        choices,
                        &value,
                        false,
                        |v| UpdateSettingsRequest {
                            media_retention: Some(v.to_string()),
                            ..Default::default()
                        },
                        cx,
                    )
                }
                "docker-prune" => self.switch_row(
                    e,
                    "defaults.newAgent.pruneDetails",
                    |s| s.docker_prune_on_stop,
                    |v| UpdateSettingsRequest {
                        docker_prune_on_stop: Some(v),
                        ..Default::default()
                    },
                    cx,
                ),
                "docker-image-cache" | "package-caches" => {
                    let image = e.id == "docker-image-cache";
                    let mut row = if image {
                        self.switch_row(
                            e,
                            "defaults.newAgent.imageCacheDetails",
                            |s| s.image_cache,
                            |v| UpdateSettingsRequest {
                                image_cache: Some(v),
                                ..Default::default()
                            },
                            cx,
                        )
                    } else {
                        self.switch_row(
                            e,
                            "defaults.newAgent.pkgCacheDetails",
                            |s| s.package_cache,
                            |v| UpdateSettingsRequest {
                                package_cache: Some(v),
                                ..Default::default()
                            },
                            cx,
                        )
                    };
                    let held = s.map_or(0, |s| {
                        if image {
                            s.image_cache_bytes
                        } else {
                            s.package_cache_bytes
                        }
                    });
                    row.body.push(
                        h_flex()
                            .flex_wrap()
                            .items_start()
                            .gap_3()
                            .child(labeled(
                                t("defaults.newAgent.holdAtMost"),
                                v_flex()
                                    .gap_1()
                                    .child(self.field(
                                        if image { "image-max" } else { "package-max" },
                                        180.,
                                    ))
                                    .child(note(t("defaults.newAgent.holdHint"))),
                            ))
                            .child(labeled(
                                tv(
                                    "defaults.newAgent.holds",
                                    &[("size", (&human_bytes(held)).into())],
                                ),
                                h_flex().child(button(
                                    SharedString::from(format!("{}-empty", e.id)),
                                    t("defaults.newAgent.emptyIt"),
                                    Btn::Secondary,
                                    s.is_none() || held == 0,
                                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        let mut r = req();
                                        if image {
                                            r.clear_image_cache = true;
                                        } else {
                                            r.clear_package_cache = true;
                                        }
                                        this.save(r, cx)
                                    }),
                                )),
                            ))
                            .into_any_element(),
                    );
                    row
                }
                "local-node-modules" => self.switch_row(
                    e,
                    "defaults.newAgent.localNodeModulesDetails",
                    |s| s.local_node_modules,
                    |v| UpdateSettingsRequest {
                        local_node_modules: Some(v),
                        ..Default::default()
                    },
                    cx,
                ),
                _ => return None,
            };
        Some(row)
    }

    fn remove_stopped_row(&self, e: &Entry, cx: &mut Context<Self>) -> Row {
        let s = self.settings_page.settings.as_ref();
        let days = s.map_or(7, |s| s.remove_stopped_days);
        let mut row = self.switch_row(
            e,
            "defaults.newAgent.removeStoppedDetails",
            |s| s.remove_stopped,
            |v| UpdateSettingsRequest {
                remove_stopped: Some(v),
                ..Default::default()
            },
            cx,
        );
        row.description = tv(
            "defaults.newAgent.removeStoppedDescription",
            &[("days", days.into())],
        );
        let day_words = |n: i64| {
            let w = tv("defaults.newAgent.days", &[("count", n.into())]);
            if n == 7 { with_default(&w) } else { w }
        };
        let mut all_days = REMOVE_DAYS.to_vec();
        if !all_days.contains(&days) {
            all_days.push(days);
        }
        let choices = all_days
            .iter()
            .map(|n| Choice::new(n.to_string(), day_words(*n)))
            .collect();
        row.body.push(select_at(
            "remove-stopped-days",
            choices,
            &days.to_string(),
            s.is_none(),
            Rc::new(cx.listener(|this, v: &str, _, cx| {
                this.save(
                    UpdateSettingsRequest {
                        remove_stopped_days: v.parse().ok(),
                        ..Default::default()
                    },
                    cx,
                );
                this.load_stopped(cx);
            })),
            Anchor::TopLeft,
        ));
        let st = &self.settings_page.stopped;
        let preview = match &st.loaded {
            None => v_flex().child(note(t("defaults.newAgent.removeStoppedChecking"))),
            Some(Err(err)) => v_flex().child(note_in(err.clone(), theme::rose(300))),
            Some(Ok(m)) => {
                let removable: Vec<_> =
                    m.machines.iter().filter(|m| !m.remove.is_empty()).collect();
                let kept = m.machines.len() - removable.len();
                let bytes: i64 = removable.iter().filter_map(|m| m.machine).sum();
                let refs: Vec<String> = removable.iter().map(|m| m.r#ref.clone()).collect();
                v_flex()
                    .gap_2()
                    .child(note_in(
                        if removable.is_empty() {
                            t("defaults.newAgent.removeStoppedNone")
                        } else {
                            tv(
                                "defaults.newAgent.removeStoppedWould",
                                &[
                                    ("count", removable.len().into()),
                                    ("size", (&human_bytes(bytes)).into()),
                                ],
                            )
                        },
                        theme::secondary(),
                    ))
                    .children(removable.iter().map(|m| {
                        h_flex()
                            .gap_2()
                            .text_xs()
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .truncate()
                                    .text_color(theme::primary())
                                    .child(format!(
                                        "{} · {}",
                                        if m.title.is_empty() {
                                            &m.name
                                        } else {
                                            &m.title
                                        },
                                        m.project
                                    )),
                            )
                            .child(div().flex_none().text_color(theme::subtle()).child(tv(
                                "defaults.newAgent.removeStoppedSince",
                                &[("ago", (&time_ago(&m.stopped_since)).into())],
                            )))
                            .when_some(m.machine, |el, b| {
                                el.child(
                                    div()
                                        .w_16()
                                        .flex_none()
                                        .text_right()
                                        .font_family(theme::mono())
                                        .text_color(theme::secondary())
                                        .child(human_bytes(b)),
                                )
                            })
                    }))
                    .when(kept > 0, |el| {
                        el.child(note(tv(
                            "defaults.newAgent.removeStoppedOthers",
                            &[("count", kept.into())],
                        )))
                    })
                    .when(!refs.is_empty(), |el| {
                        el.child(h_flex().child(button(
                            "remove-stopped-now",
                            if st.removing {
                                t("defaults.newAgent.removeStoppedRemoving")
                            } else {
                                t("defaults.newAgent.removeStoppedNow")
                            },
                            Btn::Secondary,
                            st.removing,
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.remove_stopped_now(refs.clone(), cx)
                            }),
                        )))
                    })
            }
        };
        row.body.push(
            div()
                .px_3()
                .py_2p5()
                .rounded_lg()
                .border_1()
                .border_color(theme::line())
                .bg(theme::surface())
                .child(preview)
                .into_any_element(),
        );
        if let Some(said) = &st.said {
            row.body.push(note_in(said.clone(), theme::emerald(300)));
        }
        row
    }
}

/// vm_gpu_note says which GPU agents get, or how far readying it has got.
fn vm_gpu_note(g: &agentbox_api::VMGPU) -> String {
    let reason = [("reason", (&g.reason).into())];
    if !g.kind.is_empty() && g.setup == "running" {
        return t("settings.agentGPU.setup.updating");
    }
    if !g.kind.is_empty() && g.setup == "failed" {
        return tv("settings.agentGPU.setup.updateFailed", &reason);
    }
    if ["available", "running", "restart", "failed"].contains(&g.setup.as_str()) {
        return tv(&format!("settings.agentGPU.setup.{}", g.setup), &reason);
    }
    if g.kind == "host" {
        if g.node.is_empty() {
            return t("settings.agentGPU.hostOld");
        }
        return if g.reason.is_empty() {
            tv("settings.agentGPU.host", &[("node", (&g.node).into())])
        } else {
            tv(
                "settings.agentGPU.hostPending",
                &[("node", (&g.node).into()), ("reason", (&g.reason).into())],
            )
        };
    }
    tv(
        if g.kind == "dxg" {
            "settings.agentGPU.wsl"
        } else {
            "settings.agentGPU.renderNode"
        },
        &[("node", (&g.node).into())],
    )
}

#[cfg(test)]
mod tests {
    use super::{format_tokens, parse_bytes, size_field};

    #[test]
    fn sizes_read_and_write_as_electron_does() {
        assert_eq!(parse_bytes("10GiB"), Some(10 * 1024 * 1024 * 1024));
        assert_eq!(parse_bytes("500 MB"), Some(500_000_000));
        assert_eq!(parse_bytes("12"), Some(12));
        assert_eq!(parse_bytes("ten"), None);
        assert_eq!(parse_bytes("5 parsecs"), None);
        assert_eq!(size_field(20 * 1024 * 1024 * 1024), "20GiB");
        assert_eq!(size_field(1536 * 1024 * 1024), "1.5GiB");
        assert_eq!(format_tokens(500_000), "500k");
        assert_eq!(format_tokens(1_000_000), "1M");
        assert_eq!(format_tokens(1_500_000), "1.5M");
    }
}
