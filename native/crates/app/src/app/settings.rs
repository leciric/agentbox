//! Settings (components/SettingsView.tsx, drawn by SettingsPage.tsx): a
//! column of sections beside the open one, with a search across every
//! setting above it. It opens on Essentials, the few settings a new user
//! changes, and keeps the rest under Advanced. A setting changed from its
//! default is marked at its edge, and "@changed" finds every such one.
//!
//! Everything here is the daemon's (GET/PATCH /v1/settings, /v1/theme,
//! GET /v1/update, and each section's own routes, in its module) except
//! group-idle and Notifications, which are how this window behaves and so
//! live on this computer (crate::prefs). A section opens at its top, and
//! reads what it shows as it opens (load_section).
mod accounts;
pub(super) mod connectors;
pub(super) mod defaults;
mod diagnostics;
pub(super) mod first_run;
pub(super) mod kit;
mod memory;
mod notify;
mod phone;
mod project;
mod sent;
mod setup;
mod skills;
mod tokens;
mod transfer;

use std::collections::{HashMap, HashSet};
use std::rc::Rc;

use agentbox_api::client::Method;
use agentbox_api::{DEFAULT_LANGUAGE, Settings, Theme, UpdateSettingsRequest, UpdateStatus};
use agentbox_i18n::{LANGUAGES, t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{Btn, Row, button, segmented, toggle};
use crate::{prefs, theme};
use kit::note;

const ESSENTIALS: &str = "essentials";

/// MORE_FIELDS are the later sections' text fields: (name, placeholder
/// key, masked). Typing in one redraws the page, and Enter does what its
/// form does (more_enter).
const MORE_FIELDS: &[(&str, &str, bool)] = &[
    ("memory-filter", "memory.search.placeholder", false),
    ("skills-search", "skills.search", false),
    ("skill-name", "review-pr", false),
    ("skills-folder", "~/skills", false),
    ("skills-git", "https://github.com/…", false),
    ("connector-name", "acme", false),
    ("connector-url", "https://mcp.example.com/mcp", false),
    (
        "connector-token",
        "project.connectors.tokenPlaceholder",
        true,
    ),
    ("connector-secret", "ACME_TOKEN", false),
    ("connector-header", "Authorization", false),
    (
        "transfer-export-path",
        "transfer.export.pathPlaceholder",
        false,
    ),
    ("transfer-pass", "transfer.passphrase.label", true),
    ("transfer-pass-again", "transfer.passphrase.again", true),
    (
        "transfer-import-path",
        "transfer.import.pathPlaceholder",
        false,
    ),
    ("transfer-import-pass", "transfer.passphrase.label", true),
    ("tunnel-hostname", "chat.example.com", false),
    ("tunnel-token", "eyJhIjoi…", true),
    ("project-name", "", false),
    ("project-prefix", "defaults.project.noPrefix", false),
    ("secret-name", "API_KEY", false),
    ("secret-value", "agent.secrets.valuePlaceholder", true),
];

/// The page's own state: what's open, what's searched, and what the daemon said.
pub struct SettingsPage {
    search: Entity<InputState>,
    open: String,
    /// advanced is whether the nav lists every section, not only Essentials.
    advanced: bool,
    /// more holds the settings whose "More" is open.
    more: HashSet<&'static str>,
    settings: Option<Settings>,
    theme: Option<Theme>,
    update: Option<UpdateStatus>,
    /// fields are the page's text fields, by name: Accounts' forms and the
    /// number fields of Models, Agents and Resources.
    fields: HashMap<&'static str, Entity<InputState>>,
    accounts: accounts::Accounts,
    stopped: defaults::Stopped,
    memory: memory::GlobalMemory,
    skills: skills::Skills,
    connectors: connectors::Connectors,
    diagnostics: diagnostics::Diagnostics,
    setup: setup::SetupPage,
    /// first_run is the first run's page (first_run.rs).
    pub(super) first_run: first_run::FirstRun,
    sent: sent::Sent,
    tokens: tokens::Tokens,
    transfer: transfer::Transfer,
    phone: phone::Phone,
    project: project::ProjectPage,
    /// skill_draft is the SKILL.md being written or edited.
    skill_draft: Entity<gpui_component::input::TextareaState>,
    /// scroll is the open section's, put back at its top as another opens.
    scroll: ScrollHandle,
    watching_claude_code: bool,
    _sub: Subscription,
    _field_subs: Vec<Subscription>,
}

impl SettingsPage {
    pub fn new(window: &mut Window, cx: &mut Context<AppView>) -> Self {
        let search =
            cx.new(|cx| InputState::new(window, cx).placeholder(t("settings.page.search")));
        let sub = cx.subscribe_in(
            &search,
            window,
            |this, _, ev: &InputEvent, window, cx| match ev {
                InputEvent::Change => cx.notify(),
                // Enter opens the first section the search found.
                InputEvent::PressEnter { .. } => {
                    let q = this.settings_page.search.read(cx).value().to_string();
                    let first = super::settings::search(&this.settings_sections(cx), &q)
                        .first()
                        .map(|s| s.id.clone());
                    if let Some(id) = first {
                        this.open_settings_section(id, window, cx);
                    }
                }
                _ => {}
            },
        );
        let mut fields = HashMap::new();
        let mut field_subs = Vec::new();
        let named = accounts::FIELDS.iter().map(|(n, p, m)| (*n, *p, *m)).chain(
            defaults::FIELDS.iter().map(|n| {
                let p = if n.ends_with("model-name") {
                    "claude-fable-5-1"
                } else {
                    ""
                };
                (*n, p, false)
            }),
        );
        let named = named.chain(MORE_FIELDS.iter().map(|(n, p, m)| (*n, *p, *m)));
        for (name, placeholder, masked) in named {
            let input = cx.new(|cx| {
                InputState::new(window, cx)
                    .placeholder(accounts::placeholder(placeholder))
                    .masked(masked)
            });
            let number = defaults::FIELDS.contains(&name);
            let more = MORE_FIELDS.iter().any(|(n, _, _)| *n == name);
            field_subs.push(cx.subscribe_in(
                &input,
                window,
                move |this, _, ev: &InputEvent, window, cx| match ev {
                    InputEvent::Change if name == "skill-name" => {
                        this.skill_name_typed(window, cx);
                        cx.notify()
                    }
                    InputEvent::Change if more => cx.notify(),
                    InputEvent::PressEnter { .. } if more => this.more_enter(name, window, cx),
                    InputEvent::PressEnter { .. } if number => this.commit_field(name, window, cx),
                    InputEvent::Blur if number && !name.ends_with("model-name") => {
                        this.commit_field(name, window, cx)
                    }
                    InputEvent::PressEnter { .. } => this.field_enter(name, window, cx),
                    _ => {}
                },
            ));
            fields.insert(name, input);
        }
        Self {
            search,
            open: ESSENTIALS.into(),
            advanced: false,
            more: HashSet::new(),
            settings: None,
            theme: None,
            update: None,
            fields,
            accounts: accounts::Accounts::default(),
            stopped: defaults::Stopped::default(),
            memory: Default::default(),
            skills: Default::default(),
            connectors: Default::default(),
            diagnostics: Default::default(),
            setup: Default::default(),
            first_run: first_run::FirstRun::new(window, cx),
            sent: Default::default(),
            tokens: Default::default(),
            transfer: Default::default(),
            phone: Default::default(),
            project: Default::default(),
            skill_draft: cx.new(|cx| gpui_component::input::TextareaState::new(window, cx)),
            scroll: ScrollHandle::new(),
            watching_claude_code: false,
            _sub: sub,
            _field_subs: field_subs,
        }
    }
}

/// PaletteSetting is one setting as the search palette lists it.
pub(super) struct PaletteSetting {
    pub section: String,
    pub place: String,
    pub id: String,
    pub label: String,
    pub keywords: String,
}

/// Entry is one setting, as the search sees it (lib/settingsSearch.ts).
struct Entry {
    id: &'static str,
    label: String,
    description: String,
    keywords: String,
    essential: bool,
    modified: bool,
    /// scope is the project a project's setting is of, empty for the
    /// installation's.
    scope: String,
}

struct Group {
    id: &'static str,
    title: String,
    description: String,
    /// cards: each setting is a card of its own (Accounts' tools), not a
    /// row of the group's one panel.
    cards: bool,
    entries: Vec<Entry>,
}

impl Group {
    /// with is this group holding other entries: a search's, Essentials'.
    fn with(&self, entries: Vec<Entry>) -> Group {
        Group {
            id: self.id,
            title: self.title.clone(),
            description: self.description.clone(),
            cards: self.cards,
            entries,
        }
    }
    fn described(mut self, key: &str) -> Group {
        self.description = t(key);
        self
    }
    fn cards(mut self) -> Group {
        self.cards = true;
        self
    }
}

struct Section {
    id: String,
    title: String,
    description: String,
    /// nav is the heading it sits under in the column, when it has one.
    nav: Option<String>,
    project: bool,
    groups: Vec<Group>,
    /// later: this app doesn't draw its settings yet.
    later: bool,
}

impl Section {
    fn count(&self) -> usize {
        self.groups.iter().map(|g| g.entries.len()).sum()
    }
    fn changed(&self) -> usize {
        self.groups
            .iter()
            .flat_map(|g| &g.entries)
            .filter(|e| e.modified)
            .count()
    }
}

/// normalize lowercases, drops accents and apostrophes, so "cafe" finds
/// "café" and "agents" finds "agent's", as settingsSearch.ts's normalize.
fn normalize(text: &str) -> String {
    text.chars()
        .filter(|c| *c != '\'' && *c != '’')
        .flat_map(char::to_lowercase)
        .map(|c| match c {
            'á' | 'à' | 'â' | 'ã' | 'ä' => 'a',
            'é' | 'è' | 'ê' | 'ë' => 'e',
            'í' | 'ì' | 'î' | 'ï' => 'i',
            'ó' | 'ò' | 'ô' | 'õ' | 'ö' => 'o',
            'ú' | 'ù' | 'û' | 'ü' => 'u',
            'ç' => 'c',
            'ñ' => 'n',
            c => c,
        })
        .collect()
}

/// parse_query splits a search into its words and whether it asks for the
/// changed settings only (@changed or @modified, as VS Code's).
fn parse_query(query: &str) -> (Vec<String>, bool) {
    let mut changed = false;
    let words = normalize(query)
        .split_whitespace()
        .filter(|w| {
            let token = *w == "@changed" || *w == "@modified";
            changed |= token;
            !token
        })
        .map(String::from)
        .collect();
    (words, changed)
}

/// search keeps the sections, groups and settings that match, in the page's
/// order: every word is somewhere in the section's title, the group's, or the
/// setting's name, description or keywords.
fn search(sections: &[Section], query: &str) -> Vec<Section> {
    let (words, changed) = parse_query(query);
    let mut out = Vec::new();
    for s in sections {
        let mut groups = Vec::new();
        for g in &s.groups {
            let entries: Vec<Entry> = g
                .entries
                .iter()
                .filter(|e| {
                    if changed && !e.modified {
                        return false;
                    }
                    let hay = normalize(&format!(
                        "{} {} {} {} {}",
                        s.title, g.title, e.label, e.description, e.keywords
                    ));
                    words.iter().all(|w| hay.contains(w.as_str()))
                })
                .map(|e| Entry {
                    id: e.id,
                    label: e.label.clone(),
                    description: e.description.clone(),
                    keywords: String::new(),
                    essential: e.essential,
                    modified: e.modified,
                    scope: e.scope.clone(),
                })
                .collect();
            if !entries.is_empty() {
                groups.push(g.with(entries));
            }
        }
        if !groups.is_empty() {
            out.push(Section {
                id: s.id.clone(),
                title: s.title.clone(),
                description: s.description.clone(),
                nav: s.nav.clone(),
                project: s.project,
                groups,
                later: s.later,
            });
        }
    }
    out
}

/// essentials is the section Settings opens on: the essential settings of
/// every installation section, under their own groups.
fn essentials(sections: &[Section]) -> Section {
    let mut groups = Vec::new();
    for s in sections.iter().filter(|s| !s.project) {
        for g in &s.groups {
            let entries: Vec<Entry> = g
                .entries
                .iter()
                .filter(|e| e.essential)
                .map(|e| Entry {
                    id: e.id,
                    label: e.label.clone(),
                    description: e.description.clone(),
                    keywords: e.keywords.clone(),
                    essential: true,
                    modified: e.modified,
                    scope: e.scope.clone(),
                })
                .collect();
            if !entries.is_empty() {
                groups.push(g.with(entries));
            }
        }
    }
    Section {
        id: ESSENTIALS.into(),
        title: t("settings.page.essentials"),
        description: t("settings.page.essentialsDescription"),
        nav: None,
        project: false,
        groups,
        later: false,
    }
}

fn entry(id: &'static str, label: &str, description: &str, keywords: &str) -> Entry {
    Entry {
        id,
        label: t(label),
        description: if description.is_empty() {
            String::new()
        } else {
            t(description)
        },
        keywords: t(keywords),
        essential: false,
        modified: false,
        scope: String::new(),
    }
}

impl Entry {
    fn essential(mut self) -> Self {
        self.essential = true;
        self
    }
    fn modified(mut self, on: bool) -> Self {
        self.modified = on;
        self
    }
}

fn group(id: &'static str, title: &str, entries: Vec<Entry>) -> Group {
    Group {
        id,
        title: t(title),
        description: String::new(),
        cards: false,
        entries,
    }
}

fn section(id: &str, nav: Option<&str>, groups: Vec<Group>) -> Section {
    Section {
        id: id.into(),
        title: t(&format!("settings.section.{id}.title")),
        description: t(&format!("settings.section.{id}.description")),
        nav: nav.map(t),
        project: false,
        later: groups.is_empty(),
        groups,
    }
}

/// Seconds a freeze or a stop may wait, as IdleReclaim.tsx offers them.
const FREEZE_TIMES: [i64; 5] = [120, 300, 600, 900, 1800];
const STOP_TIMES: [i64; 5] = [300, 600, 1800, 3600, 7200];
const DEFAULT_FREEZE: i64 = 300;
const DEFAULT_STOP: i64 = 600;

fn minutes_words(seconds: i64) -> String {
    if seconds >= 3600 && seconds % 3600 == 0 {
        format!("{}h", seconds / 3600)
    } else {
        format!("{}m", (seconds + 30) / 60)
    }
}

impl AppView {
    /// load_settings_page reads what the page shows, as it opens and when
    /// the daemon says the theme or the update status changed.
    pub(super) fn load_settings_page(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let (s, th, up) = (
                client.get::<Settings>("/v1/settings").await,
                client.get::<Theme>("/v1/theme").await,
                client.get::<UpdateStatus>("/v1/update").await,
            );
            this.update(cx, |this, cx| {
                let page = &mut this.settings_page;
                page.settings = s.ok().or(page.settings.take());
                page.update = up.ok().or(page.update.take());
                if let Ok(th) = th {
                    page.theme = Some(th);
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// load_theme wears the daemon's appearance, at start and on its event.
    pub(super) fn load_theme(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            if let Ok(th) = client.get::<Theme>("/v1/theme").await {
                this.update_in(cx, |this, window, cx| this.wear(th, window, cx))
                    .ok();
            }
        })
        .detach();
    }

    fn wear(&mut self, th: Theme, window: &mut Window, cx: &mut Context<Self>) {
        if theme::apply(&th) {
            theme::sync_widgets(cx);
            window.refresh();
        }
        self.settings_page.theme = Some(th);
        cx.notify();
    }

    pub(super) fn reload_update(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            if let Ok(up) = client.get::<UpdateStatus>("/v1/update").await {
                this.update(cx, |this, cx| {
                    this.settings_page.update = Some(up);
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    pub(super) fn open_settings(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.settings = true;
        self.work = None;
        self.side.page = None;
        self.load_settings_page(cx);
        self.load_accounts(window, cx);
        self.load_stopped(cx);
        let open = self.settings_page.open.clone();
        self.load_section(&open, window, cx);
        self.translate_settings_search(window, cx);
        // The number fields read the settings once they're in.
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            if let Ok(s) = client.get::<Settings>("/v1/settings").await {
                this.update_in(cx, |this, window, cx| {
                    this.settings_page.settings = Some(s);
                    this.sync_fields(window, cx);
                    this.watch_claude_code(cx);
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
        cx.notify();
    }

    /// translate_settings_search puts the search's placeholder in the
    /// language picked since it was made.
    fn translate_settings_search(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.settings_page.search.update(cx, |s, cx| {
            s.set_placeholder(t("settings.page.search"), window, cx)
        });
        for (name, placeholder, _) in accounts::FIELDS {
            if let Some(i) = self.settings_page.fields.get(name) {
                i.update(cx, |i, cx| {
                    i.set_placeholder(accounts::placeholder(placeholder), window, cx)
                });
            }
        }
        self.name_placeholders(window, cx);
    }

    /// open_settings_at opens Settings on one section.
    pub(super) fn open_settings_at(
        &mut self,
        id: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.open_settings(window, cx);
        self.open_settings_section(id, window, cx);
    }

    pub(super) fn close_settings(&mut self, cx: &mut Context<Self>) {
        self.settings = false;
        cx.notify();
    }

    fn open_settings_section(&mut self, id: String, window: &mut Window, cx: &mut Context<Self>) {
        if id != ESSENTIALS {
            self.settings_page.advanced = true;
        }
        self.settings_page.open = id.clone();
        self.settings_page
            .search
            .update(cx, |s, cx| s.set_value("", window, cx));
        self.settings_page.scroll.set_offset(point(px(0.), px(0.)));
        self.load_section(&id, window, cx);
        cx.notify();
    }

    /// save sends one change to PATCH /v1/settings and shows what it answers.
    fn save(&mut self, req: UpdateSettingsRequest, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let channel = req.update_channel.is_some();
        cx.spawn(async move |this, cx| {
            let body = serde_json::to_vec(&req).unwrap_or_default();
            let res = client.raw(Method::PATCH, "/v1/settings", Some(body)).await;
            this.update(cx, |this, cx| {
                match res.map(|b| serde_json::from_slice::<Settings>(&b)) {
                    Ok(Ok(s)) => {
                        this.settings_page.settings = Some(s);
                        this.error = None;
                    }
                    // A save that fails says why in a toast (SettingsView's onError).
                    Ok(Err(e)) => crate::toasts::error(cx, e.to_string()),
                    Err(e) => crate::toasts::error(cx, e.to_string()),
                }
                // The daemon checks again on a new channel; read what it found.
                if channel {
                    this.reload_update(cx);
                }
                this.watch_claude_code(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn set_appearance(&mut self, appearance: String, window: &mut Window, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let body = serde_json::to_vec(&agentbox_api::UpdateThemeRequest {
                appearance: Some(appearance),
            })
            .unwrap_or_default();
            let res = client.raw(Method::PATCH, "/v1/theme", Some(body)).await;
            this.update_in(cx, |this, window, cx| {
                match res.map(|b| serde_json::from_slice::<Theme>(&b)) {
                    Ok(Ok(th)) => this.wear(th, window, cx),
                    Ok(Err(e)) => crate::toasts::error(cx, e.to_string()),
                    Err(e) => crate::toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn set_language(&mut self, tag: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        agentbox_i18n::set_language(tag);
        self.translate_settings_search(window, cx);
        window.refresh();
        self.save(
            UpdateSettingsRequest {
                language: Some(tag.into()),
                ..Default::default()
            },
            cx,
        );
    }

    /// settings_sections is Settings as Electron's InstalledSettings lists
    /// it, each project's after the installation's.
    fn settings_sections(&self, _cx: &App) -> Vec<Section> {
        let page = &self.settings_page;
        let s = page.settings.as_ref();
        let changed = |f: fn(&Settings) -> bool| s.is_some_and(f);
        let general =
            vec![
                group(
                    "appearance",
                    "settings.group.appearance.title",
                    vec![
                        entry(
                            "appearance",
                            "settings.entry.appearance.label",
                            "settings.appearance.description",
                            "settings.entry.appearance.keywords",
                        )
                        .essential()
                        .modified(
                            page.theme
                                .as_ref()
                                .is_some_and(|t| t.appearance != "follow"),
                        ),
                        entry(
                            "language",
                            "common.language",
                            "common.languageDescription",
                            "settings.languageKeywords",
                        )
                        .essential()
                        .modified(changed(|s| s.language != DEFAULT_LANGUAGE)),
                        entry(
                            "group-idle",
                            "settings.entry.group-idle.label",
                            "settings.groupIdle.description",
                            "settings.entry.group-idle.keywords",
                        )
                        .modified(prefs::group_idle()),
                        entry(
                            "tray",
                            "settings.entry.tray.label",
                            "settings.tray.description",
                            "settings.entry.tray.keywords",
                        )
                        .modified(prefs::tray_keep_running()),
                        entry(
                            "office",
                            "native.settings.entry.office.label",
                            "native.settings.office.description",
                            "native.settings.entry.office.keywords",
                        )
                        .modified(crate::office::enabled()),
                    ],
                ),
                group(
                    "commits",
                    "settings.group.commits.title",
                    vec![
                        entry(
                            "credit-ai-tool",
                            "settings.entry.credit-ai-tool.label",
                            "settings.creditAITool.description",
                            "settings.entry.credit-ai-tool.keywords",
                        )
                        .modified(changed(|s| s.credit_ai_tool)),
                        entry(
                            "built-with-badge",
                            "settings.entry.built-with-badge.label",
                            "settings.builtWithBadge.description",
                            "settings.entry.built-with-badge.keywords",
                        )
                        .modified(changed(|s| s.built_with_badge)),
                    ],
                ),
                group(
                    "updates",
                    "settings.group.updates.title",
                    vec![
                        entry(
                            "update-channel",
                            "settings.entry.update-channel.label",
                            "settings.channel.description",
                            "settings.entry.update-channel.keywords",
                        )
                        .modified(page.update.as_ref().is_some_and(|u| {
                            u.channel != if u.nightly { "nightly" } else { "stable" }
                        })),
                        entry(
                            "update-check",
                            "settings.entry.update-check.label",
                            "settings.updateCheck.description",
                            "settings.entry.update-check.keywords",
                        )
                        .modified(changed(|s| !s.update_check)),
                    ],
                ),
                group(
                    "privacy",
                    "settings.group.privacy.title",
                    vec![
                        entry(
                            "usage-stats",
                            "settings.entry.usage-stats.label",
                            "settings.usageStats.description",
                            "settings.entry.usage-stats.keywords",
                        )
                        .modified(changed(|s| !s.usage_stats)),
                        entry(
                            "error-reports",
                            "settings.entry.error-reports.label",
                            "settings.errorReports.description",
                            "settings.entry.error-reports.keywords",
                        )
                        .modified(changed(|s| !s.error_reports)),
                    ],
                ),
            ];
        let experimental = vec![
            group(
                "experimental-tests",
                "settings.group.experimental-tests.title",
                vec![
                    entry(
                        "block-full-suite",
                        "settings.entry.block-full-suite.label",
                        "settings.blockFullSuite.description",
                        "settings.entry.block-full-suite.keywords",
                    )
                    .modified(changed(|s| s.block_full_suite)),
                ],
            ),
            group(
                "experimental-memory",
                "settings.group.experimental-memory.title",
                vec![
                    entry(
                        "idle-reclaim",
                        "settings.idleReclaim.label",
                        "settings.idleReclaim.description",
                        "settings.entry.idle-reclaim.keywords",
                    )
                    .modified(changed(|s| s.idle_reclaim)),
                    entry(
                        "agent-freeze",
                        "settings.entry.agent-freeze.label",
                        "settings.agentFreeze.description",
                        "settings.entry.agent-freeze.keywords",
                    )
                    .modified(changed(|s| s.agent_freeze)),
                ],
            ),
        ];
        let ai = Some("settings.nav.ai");
        let machine = Some("settings.nav.machine");
        let mut out = vec![
            section("general", None, general),
            // Notifications are the app's own (lib/notifyPrefs.ts): kept here,
            // given once this app notifies.
            section("notifications", None, self.notify_groups()),
            section("phone", None, self.phone_groups()),
            section("experimental", None, experimental),
            section("accounts", ai, self.accounts_groups()),
            section("models", ai, self.models_groups()),
            section("agents", ai, self.agents_groups()),
            section("skills", ai, self.skills_groups()),
            section("connectors", ai, self.connectors_groups()),
            section("memory", ai, self.memory_groups()),
            section("resources", machine, self.resources_groups()),
            section("setup", machine, self.setup_groups()),
            section("transfer", machine, self.transfer_groups()),
            section("diagnostics", machine, self.diagnostics_groups()),
        ];
        for p in &self.projects {
            out.push(Section {
                id: format!("project:{}", p.name),
                title: if p.display_name.is_empty() {
                    p.name.clone()
                } else {
                    p.display_name.clone()
                },
                description: t("defaults.project.sectionDescription"),
                nav: None,
                project: true,
                groups: self.project_groups(p),
                later: false,
            });
        }
        out
    }

    /// palette_settings is every setting this app draws, for the search
    /// palette (paletteCommands.tsx's settings): (section, its title and
    /// group's, the setting's id, label and keywords).
    pub(super) fn palette_settings(&self, cx: &App) -> Vec<PaletteSetting> {
        let mut out = Vec::new();
        for s in self.settings_sections(cx).into_iter().filter(|s| !s.later) {
            for g in &s.groups {
                for e in &g.entries {
                    out.push(PaletteSetting {
                        section: s.id.clone(),
                        place: format!("{} · {}", s.title, g.title),
                        id: e.id.to_string(),
                        label: e.label.clone(),
                        keywords: format!("{} {}", e.keywords, e.description),
                    });
                }
            }
        }
        out
    }

    /// open_setting opens Settings at one setting: its section, searched for
    /// it, so it is the one shown (paletteCommands.tsx openSetting).
    pub(super) fn open_setting(
        &mut self,
        section: String,
        label: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.open_settings_at(section, window, cx);
        self.settings_page
            .search
            .update(cx, |s, cx| s.set_value(label, window, cx));
        cx.notify();
    }

    pub(super) fn render_settings(&self, cx: &mut Context<Self>) -> AnyElement {
        let sections = self.settings_sections(cx);
        let page = &self.settings_page;
        let query = page.search.read(cx).value().to_string();
        let searching = !query.trim().is_empty();
        let ess = essentials(&sections);
        let found = if searching {
            search(&sections, &query)
        } else {
            Vec::new()
        };
        let changed: usize = sections.iter().map(Section::changed).sum();

        let main = if searching {
            self.render_found(&found, &query, cx)
        } else if page.open == ESSENTIALS {
            v_flex()
                .gap_8()
                .child(self.render_section(&ess, cx))
                .child(self.render_advanced_list(&sections, cx))
                .into_any_element()
        } else {
            match sections.iter().find(|s| s.id == page.open) {
                Some(s) => self.render_section(s, cx),
                None => self.render_section(&ess, cx),
            }
        };

        h_flex()
            .id("settings")
            .size_full()
            .items_start()
            .bg(theme::ink())
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, window, cx| {
                let k = &ev.keystroke;
                if k.key == "f" && (k.modifiers.control || k.modifiers.platform) {
                    this.settings_page
                        .search
                        .update(cx, |s, cx| s.focus(window, cx));
                    cx.stop_propagation();
                } else if k.key == "escape" {
                    let empty = this.settings_page.search.read(cx).value().is_empty();
                    if empty {
                        this.close_settings(cx);
                    } else {
                        this.settings_page
                            .search
                            .update(cx, |s, cx| s.set_value("", window, cx));
                    }
                    cx.stop_propagation();
                }
            }))
            .child(
                self.render_settings_nav(&sections, &ess, &found, searching, changed, &query, cx),
            )
            .child(
                div()
                    .id("settings-main")
                    .flex_1()
                    .min_w_0()
                    .h_full()
                    .overflow_y_scroll()
                    .track_scroll(&page.scroll)
                    .child(
                        v_flex()
                            .w_full()
                            .max_w(px(768.))
                            .mx_auto()
                            .px_8()
                            .py_8()
                            .gap_4()
                            .when_some(self.error.clone(), |el, e| {
                                el.child(
                                    h_flex()
                                        .mb_1()
                                        .items_start()
                                        .gap_2p5()
                                        .px(px(14.))
                                        .py_2p5()
                                        .rounded_xl()
                                        .border_1()
                                        .border_color(theme::rose(400).opacity(0.25))
                                        .bg(theme::rose(400).opacity(0.08))
                                        .text_size(theme::text::UI)
                                        .text_color(theme::rose(100))
                                        .child(
                                            div()
                                                .flex_none()
                                                .mt(px(1.))
                                                .text_size(px(16.))
                                                .text_color(theme::rose(300))
                                                .child(crate::icons::of("circle-alert")),
                                        )
                                        .child(div().min_w_0().flex_1().child(e)),
                                )
                            })
                            .child(main),
                    ),
            )
            .into_any_element()
    }

    #[allow(clippy::too_many_arguments)]
    fn render_settings_nav(
        &self,
        sections: &[Section],
        ess: &Section,
        found: &[Section],
        searching: bool,
        changed: usize,
        query: &str,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let page = &self.settings_page;
        let hits = |id: &str| -> usize {
            if id == ESSENTIALS {
                search(std::slice::from_ref(ess), query)
                    .iter()
                    .map(Section::count)
                    .sum()
            } else {
                found.iter().find(|s| s.id == id).map_or(0, Section::count)
            }
        };
        // What needs a look (SettingsView.tsx needsLook): the checks of
        // Accounts' tools, and of Setup's machine, missing, outdated or warning.
        let look = |ids: &[&str]| {
            page.accounts.setup.as_ref().map_or(0, |st| {
                st.checks
                    .iter()
                    .filter(|c| {
                        ids.contains(&c.id.as_str())
                            && ["missing", "outdated", "warn"].contains(&c.status.as_str())
                    })
                    .count()
            })
        };
        let attention = |id: &str| match id {
            "accounts" => look(&["claude", "codex", "opencode", "cursor", "gemini", "github"]),
            "setup" => look(&["incus", "host", "image", "storage", "android", "preview"]),
            _ => 0,
        };
        let nav_item = |s: &Section, cx: &mut Context<Self>| {
            let active = !searching && s.id == page.open;
            let n = if searching { hits(&s.id) } else { 0 };
            let look = if searching { 0 } else { attention(&s.id) };
            let id = s.id.clone();
            let icon = if s.project {
                "folder-git-2"
            } else {
                kit::section_icon(&s.id)
            };
            h_flex()
                .id(SharedString::from(format!("settings-nav-{}", s.id)))
                .gap_2()
                .px_2p5()
                .py(px(7.))
                .rounded_lg()
                .text_size(theme::text::UI)
                .cursor_pointer()
                .when(active, |el| {
                    el.bg(theme::surface_raised()).text_color(theme::title())
                })
                .when(!active, |el| {
                    el.text_color(theme::muted())
                        .hover(|st| st.bg(theme::surface()).text_color(theme::primary()))
                })
                .when(searching && n == 0, |el| el.opacity(0.4))
                .child(
                    div()
                        .flex_none()
                        .text_size(px(16.))
                        .text_color(if active {
                            theme::brand(300)
                        } else {
                            theme::subtle()
                        })
                        .child(crate::icons::of(icon)),
                )
                .child(div().flex_1().min_w_0().truncate().child(s.title.clone()))
                .when(n > 0, |el| {
                    el.child(
                        div()
                            .px_1p5()
                            .rounded_full()
                            .bg(theme::surface_raised())
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::tertiary())
                            .child(n.to_string()),
                    )
                })
                .when(look > 0, |el| {
                    el.child(
                        div()
                            .id(SharedString::from(format!("settings-look-{}", s.id)))
                            .px_1p5()
                            .rounded_full()
                            .bg(theme::amber(400).opacity(0.15))
                            .text_size(theme::text::CAPTION)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::amber(300))
                            .tooltip(move |w, cx| {
                                gpui_component::tooltip::Tooltip::new(tv(
                                    "settings.page.toLookAt",
                                    &[("n", look.into())],
                                ))
                                .build(w, cx)
                            })
                            .child(look.to_string()),
                    )
                })
                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                    this.open_settings_section(id.clone(), window, cx)
                }))
                .into_any_element()
        };
        let heading = |text: String| {
            div()
                .px_2p5()
                .pt_4()
                .pb_1()
                .text_size(theme::text::CAPTION)
                .font_weight(FontWeight::MEDIUM)
                .text_color(theme::muted())
                .child(text)
                .into_any_element()
        };

        let show_advanced = page.advanced || searching;
        let mut items = vec![nav_item(ess, cx)];
        items.push(
            h_flex()
                .id("settings-advanced-nav")
                .mt_3()
                .gap_1p5()
                .px_2p5()
                .py(px(7.))
                .rounded_lg()
                .text_size(theme::text::UI)
                .text_color(theme::muted())
                .when(!searching, |el| {
                    el.cursor_pointer()
                        .hover(|st| st.bg(theme::surface()).text_color(theme::primary()))
                        .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.settings_page.advanced = !this.settings_page.advanced;
                            cx.notify();
                        }))
                })
                .child(
                    div()
                        .flex_none()
                        .text_size(px(16.))
                        .text_color(theme::subtle())
                        .child(crate::icons::of(if show_advanced { "▾" } else { "▸" })),
                )
                .child(t("settings.page.advanced"))
                .into_any_element(),
        );
        if show_advanced {
            let mut last: Option<&String> = None;
            for s in sections.iter().filter(|s| !s.project) {
                if let Some(nav) = &s.nav
                    && last != Some(nav)
                {
                    items.push(heading(nav.clone()));
                }
                last = s.nav.as_ref();
                items.push(nav_item(s, cx));
            }
            if sections.iter().any(|s| s.project) {
                items.push(heading(t("settings.page.projects")));
                for s in sections.iter().filter(|s| s.project) {
                    items.push(nav_item(s, cx));
                }
            }
        }
        let changed_on = query.to_lowercase().contains("@changed");
        v_flex()
            .id("settings-nav")
            .w(px(240.))
            .flex_none()
            .h_full()
            .overflow_y_scroll()
            .border_r_1()
            .border_color(theme::line())
            .px_3()
            .pt(px(28.))
            .pb_6()
            .gap_3()
            .child(
                h_flex()
                    .id("settings-back")
                    .w_auto()
                    .gap_1p5()
                    .px_2()
                    .py_1()
                    .rounded_lg()
                    .text_size(theme::text::UI)
                    .text_color(theme::muted())
                    .cursor_pointer()
                    .hover(|st| st.bg(theme::surface()).text_color(theme::primary()))
                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.close_settings(cx)))
                    .child(
                        div()
                            .text_size(px(16.))
                            .child(crate::icons::of("arrow-left")),
                    )
                    .child(t("common.back"))
                    .child(theme::kbd("Esc")),
            )
            .child(
                div()
                    .px_2()
                    .text_size(theme::text::DISPLAY)
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(theme::title())
                    .child(t("common.settings")),
            )
            .child(
                v_flex()
                    .gap_1p5()
                    .child(Input::new(&page.search).cleanable(true))
                    .when(changed > 0, |el| {
                        el.child(
                            h_flex()
                                .id("settings-changed")
                                .w_auto()
                                .gap_1p5()
                                .px_2()
                                .rounded_md()
                                .text_size(theme::text::CAPTION)
                                .cursor_pointer()
                                .text_color(if changed_on {
                                    theme::brand(300)
                                } else {
                                    theme::subtle()
                                })
                                .hover(|st| st.text_color(theme::primary()))
                                .child(
                                    div()
                                        .w(px(2.))
                                        .h(px(10.))
                                        .rounded_full()
                                        .bg(theme::brand(400)),
                                )
                                .child(tv(
                                    "settings.page.changedFromDefault",
                                    &[("n", changed.into())],
                                ))
                                .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                                    let q = this.settings_page.search.read(cx).value().to_string();
                                    let next = if q.to_lowercase().contains("@changed") {
                                        q.split_whitespace()
                                            .filter(|w| !w.eq_ignore_ascii_case("@changed"))
                                            .collect::<Vec<_>>()
                                            .join(" ")
                                    } else {
                                        format!("@changed {q}").trim().to_string()
                                    };
                                    this.settings_page
                                        .search
                                        .update(cx, |s, cx| s.set_value(next, window, cx));
                                    cx.notify();
                                })),
                        )
                    }),
            )
            .child(v_flex().gap(px(2.)).children(items))
            .into_any_element()
    }

    /// render_section draws one section: its title and what it holds, then
    /// its groups, or a line saying this app hasn't these settings yet.
    fn render_section(&self, s: &Section, cx: &mut Context<Self>) -> AnyElement {
        let icon = if s.project {
            "folder-git-2"
        } else {
            kit::section_icon(&s.id)
        };
        v_flex()
            .gap_8()
            .child(
                h_flex()
                    .items_start()
                    .gap_3()
                    .child(
                        div()
                            .flex_none()
                            .mt_0p5()
                            .size_9()
                            .flex()
                            .items_center()
                            .justify_center()
                            .rounded_xl()
                            .border_1()
                            .border_color(theme::line_strong())
                            .bg(theme::overlay())
                            .text_size(px(18.))
                            .text_color(theme::brand(300))
                            .child(crate::icons::of(icon)),
                    )
                    .child(
                        v_flex()
                            .min_w_0()
                            .gap_0p5()
                            .child(
                                h_flex()
                                    .gap_2()
                                    .items_baseline()
                                    .when(s.project, |el| {
                                        el.child(
                                            div()
                                                .text_size(theme::text::HEADING)
                                                .text_color(theme::subtle())
                                                .child(t("settings.page.project")),
                                        )
                                    })
                                    .child(
                                        div()
                                            .text_size(theme::text::HEADING)
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(theme::title())
                                            .child(s.title.clone()),
                                    ),
                            )
                            .child(
                                div()
                                    .text_size(theme::text::UI)
                                    .line_height(rems(1.4))
                                    .text_color(theme::muted())
                                    .child(s.description.clone()),
                            ),
                    ),
            )
            .when(s.later, |el| {
                el.child(
                    kit::panel(div())
                        .px_5()
                        .py_4()
                        .text_size(theme::text::UI)
                        .text_color(theme::muted())
                        .child(t("native.settings.later")),
                )
            })
            .children(s.groups.iter().map(|g| self.render_group(g, cx)))
            // The lead keeps a preference like this with remember, and its
            // brief has it search memory before every create_agent.
            .when(s.id == "models", |el| {
                el.child(
                    div()
                        .px_1()
                        .text_size(theme::text::CAPTION)
                        .line_height(rems(1.4))
                        .text_color(theme::subtle())
                        .child(format!("💡 {}", t("settings.models.tip"))),
                )
            })
            .when(s.id == "general", |el| {
                el.when_some(self.settings_page.update.as_ref(), |el, u| {
                    el.child(
                        div()
                            .w_full()
                            .text_center()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::faint())
                            .child(format!(
                                "AgentBox {} · {}",
                                u.current,
                                self.client.socket().display()
                            )),
                    )
                })
            })
            .into_any_element()
    }

    fn render_group(&self, g: &Group, cx: &mut Context<Self>) -> AnyElement {
        let heading = v_flex()
            .px_1()
            .gap_0p5()
            .child(
                div()
                    .text_size(theme::text::UI)
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(theme::primary())
                    .child(g.title.clone()),
            )
            .when(!g.description.is_empty(), |el| {
                el.child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .line_height(rems(1.4))
                        .text_color(theme::subtle())
                        .child(g.description.clone()),
                )
            });
        if g.cards {
            return v_flex()
                .id(SharedString::from(format!("settings-group-{}", g.id)))
                .gap_2p5()
                .child(heading)
                .children(g.entries.iter().map(|e| self.render_card(e, cx)))
                .into_any_element();
        }
        let mut rows = Vec::new();
        for (i, e) in g.entries.iter().enumerate() {
            rows.push(
                div()
                    .when(i > 0, |el| {
                        el.border_t_1().border_color(theme::line_faint())
                    })
                    .child(self.render_entry(e, cx))
                    .into_any_element(),
            );
        }
        v_flex()
            .id(SharedString::from(format!("settings-group-{}", g.id)))
            .gap_2p5()
            .child(heading)
            .child(kit::panel(v_flex()).children(rows))
            .into_any_element()
    }

    /// render_advanced_list is Essentials' end: every section, with how many
    /// settings it holds and how many are changed, a click from opening it.
    fn render_advanced_list(&self, sections: &[Section], cx: &mut Context<Self>) -> AnyElement {
        let open = self.settings_page.advanced;
        let total: usize = sections.iter().map(Section::count).sum();
        v_flex()
            .gap_2p5()
            .child(
                h_flex()
                    .id("settings-advanced")
                    .w_auto()
                    .gap_1p5()
                    .px_1()
                    .rounded_md()
                    .text_size(theme::text::UI)
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(theme::primary())
                    .cursor_pointer()
                    .hover(|st| st.text_color(theme::title()))
                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                        this.settings_page.advanced = !this.settings_page.advanced;
                        cx.notify();
                    }))
                    .child(
                        div()
                            .text_size(px(16.))
                            .text_color(theme::subtle())
                            .child(crate::icons::of(if open { "▾" } else { "▸" })),
                    )
                    .child(t("settings.page.advanced"))
                    .child(
                        div()
                            .font_weight(FontWeight::NORMAL)
                            .text_color(theme::subtle())
                            .child(format!(
                                "· {}",
                                tv(
                                    "settings.page.advancedSections",
                                    &[("count", total.into()), ("sections", sections.len().into())],
                                )
                            )),
                    ),
            )
            .when(open, |el| {
                el.child(kit::panel(v_flex()).overflow_hidden().children(
                    sections.iter().enumerate().map(|(i, s)| {
                        let id = s.id.clone();
                        let (n, changed) = (s.count(), s.changed());
                        let icon = if s.project {
                            "folder-git-2"
                        } else {
                            kit::section_icon(&s.id)
                        };
                        h_flex()
                            .id(SharedString::from(format!("settings-adv-{}", s.id)))
                            .gap_3()
                            .px_5()
                            .py_3()
                            .when(i > 0, |el| {
                                el.border_t_1().border_color(theme::line_faint())
                            })
                            .cursor_pointer()
                            .hover(|st| st.bg(theme::surface()))
                            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                this.open_settings_section(id.clone(), window, cx)
                            }))
                            .child(
                                div()
                                    .flex_none()
                                    .text_size(px(16.))
                                    .text_color(theme::subtle())
                                    .child(crate::icons::of(icon)),
                            )
                            .child(
                                v_flex()
                                    .flex_1()
                                    .min_w_0()
                                    .child(
                                        h_flex()
                                            .gap_2()
                                            .items_baseline()
                                            .when(s.project, |el| {
                                                el.child(
                                                    div()
                                                        .flex_none()
                                                        .text_size(theme::text::CAPTION)
                                                        .text_color(theme::subtle())
                                                        .child(t("settings.page.project")),
                                                )
                                            })
                                            .child(
                                                div()
                                                    .text_size(theme::text::UI)
                                                    .font_weight(FontWeight::MEDIUM)
                                                    .text_color(theme::primary())
                                                    .truncate()
                                                    .child(s.title.clone()),
                                            ),
                                    )
                                    .child(
                                        div()
                                            .text_size(theme::text::CAPTION)
                                            .text_color(theme::subtle())
                                            .truncate()
                                            .child(s.description.clone()),
                                    ),
                            )
                            .when(n > 0, |el| {
                                el.child(
                                    div()
                                        .flex_none()
                                        .text_size(px(11.5))
                                        .text_color(theme::subtle())
                                        .child(if changed > 0 {
                                            tv(
                                                "settings.page.advancedCountChanged",
                                                &[("count", n.into()), ("changed", changed.into())],
                                            )
                                        } else {
                                            tv(
                                                "settings.page.advancedCount",
                                                &[("count", n.into())],
                                            )
                                        }),
                                )
                            })
                            .child(
                                div()
                                    .flex_none()
                                    .text_size(px(16.))
                                    .text_color(theme::subtle())
                                    .child(crate::icons::of("›")),
                            )
                    }),
                ))
            })
            .into_any_element()
    }

    /// render_found is a search's result: every setting that matched, under
    /// its section and group.
    fn render_found(&self, found: &[Section], query: &str, cx: &mut Context<Self>) -> AnyElement {
        if found.is_empty() {
            return v_flex()
                .items_center()
                .gap_2()
                .py_16()
                .child(
                    div()
                        .text_size(px(24.))
                        .text_color(theme::subtle())
                        .child(crate::icons::of("search")),
                )
                .child(
                    div()
                        .text_size(theme::text::BODY)
                        .text_color(theme::secondary())
                        .child(tv(
                            "settings.page.noMatch",
                            &[("query", query.trim().into())],
                        )),
                )
                .into_any_element();
        }
        let total: usize = found.iter().map(Section::count).sum();
        v_flex()
            .gap_10()
            .child(
                div()
                    .px_1()
                    .text_size(theme::text::UI)
                    .text_color(theme::subtle())
                    .child(tv("settings.page.found", &[("count", total.into())])),
            )
            .children(found.iter().map(|s| {
                let icon = if s.project {
                    "folder-git-2"
                } else {
                    kit::section_icon(&s.id)
                };
                v_flex()
                    .gap_5()
                    .child(
                        h_flex()
                            .gap_2()
                            .text_size(theme::text::HEADING)
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::title())
                            .child(
                                div()
                                    .text_size(px(16.))
                                    .text_color(theme::brand(300))
                                    .child(crate::icons::of(icon)),
                            )
                            .when(s.project, |el| {
                                el.child(
                                    div()
                                        .font_weight(FontWeight::NORMAL)
                                        .text_color(theme::subtle())
                                        .child(t("settings.page.project")),
                                )
                            })
                            .child(div().min_w_0().truncate().child(s.title.clone())),
                    )
                    .children(s.groups.iter().map(|g| self.render_group(g, cx)))
            }))
            .into_any_element()
    }

    /// more_row is a Row with its "More" wired to the page's state.
    fn more_row(&self, e: &Entry, details: Option<String>, cx: &mut Context<Self>) -> Row {
        let mut row = Row::new(
            SharedString::from(format!("setting-{}", e.id)),
            e.label.clone(),
            e.description.clone(),
        );
        row.modified = e.modified;
        if details.is_some() {
            row.details = details;
            self.wire_more(&mut row, e.id, cx);
        }
        row
    }

    /// wire_more opens and closes a row's details from the page's state.
    fn wire_more(&self, row: &mut Row, id: &'static str, cx: &mut Context<Self>) {
        row.more = self.settings_page.more.contains(id);
        row.on_more = Some(Rc::new(cx.listener(move |this, _: &ClickEvent, _, cx| {
            let more = &mut this.settings_page.more;
            if !more.remove(id) {
                more.insert(id);
            }
            cx.notify();
        })));
    }

    /// switch_row is a setting that's a switch over one field of Settings.
    fn switch_row(
        &self,
        e: &Entry,
        details: &str,
        on: impl Fn(&Settings) -> bool,
        set: fn(bool) -> UpdateSettingsRequest,
        cx: &mut Context<Self>,
    ) -> Row {
        let s = self.settings_page.settings.as_ref();
        let value = s.is_some_and(on);
        let mut row = self.more_row(e, (!details.is_empty()).then(|| t(details)), cx);
        row.control = Some(toggle(
            SharedString::from(format!("switch-{}", e.id)),
            value,
            s.is_none(),
            cx.listener(move |this, _: &ClickEvent, _, cx| this.save(set(!value), cx)),
        ));
        row
    }

    /// render_card draws a setting that's a card of its own: a tool's
    /// accounts, or a whole panel (Memory, Skills, a checklist).
    fn render_card(&self, e: &Entry, cx: &mut Context<Self>) -> AnyElement {
        match e.id {
            "global-memory" => self.memory_card(cx),
            "skills" => self.skills_card(&e.scope, cx),
            "connectors" => self.connectors_card(cx),
            "p-secrets" => self.secrets_card(&e.scope, cx),
            "p-tokens" => self.tokens_card(&e.scope, cx),
            "diagnostics" => self.diagnostics_card(cx),
            "doctor" => self.doctor_card(cx),
            "checklist" => self.checklist_card(cx),
            "incus" | "host" | "image" | "storage" | "android" | "preview" => {
                self.optional_step_card(e, cx)
            }
            _ => self.account_card(e, cx),
        }
    }

    /// load_section reads what a section shows as it opens.
    fn load_section(&mut self, id: &str, window: &mut Window, cx: &mut Context<Self>) {
        if let Some(name) = id.strip_prefix("project:") {
            self.load_accounts(window, cx);
            self.load_project(name.to_string(), window, cx);
            self.load_tokens(name.to_string(), cx);
            return;
        }
        match id {
            "memory" => self.load_global_memory(cx),
            "skills" => self.load_skills(String::new(), cx),
            "connectors" => self.load_connectors(cx),
            "diagnostics" => self.load_diagnostics(cx),
            "setup" => self.load_setup(cx),
            "transfer" => self.load_transfer_preview(cx),
            "phone" => self.load_lan(cx),
            _ => {}
        }
    }

    /// more_enter is Enter in a later section's field.
    fn more_enter(&mut self, name: &str, window: &mut Window, cx: &mut Context<Self>) {
        match name {
            "skills-folder" | "skills-git" => self.skills_look(cx),
            n if n.starts_with("connector-") => self.add_connector(cx),
            "transfer-import-path" | "transfer-import-pass" => self.inspect_import(cx),
            "tunnel-hostname" | "tunnel-token" => self.save_tunnel(cx),
            "project-name" | "project-prefix" | "secret-name" | "secret-value" => {
                if let Some(p) = self.open_project_name() {
                    match name {
                        "project-name" => self.save_project_name(p, cx),
                        "project-prefix" => self.save_project_prefix(p, cx),
                        _ => self.save_secret(p, window, cx),
                    }
                }
            }
            _ => {}
        }
    }

    fn render_entry(&self, e: &Entry, cx: &mut Context<Self>) -> AnyElement {
        if let Some(el) = self
            .notify_entry(e, cx)
            .or_else(|| self.setup_entry(e, cx))
            .or_else(|| self.transfer_entry(e, cx))
            .or_else(|| self.phone_entry(e, cx))
            .or_else(|| self.project_entry(e, cx))
        {
            return el;
        }
        let page = &self.settings_page;
        let s = page.settings.as_ref();
        let update = page.update.as_ref();
        let blocked = update.map(|u| u.blocked.clone()).filter(|b| !b.is_empty());
        let row = match e.id {
            "appearance" => {
                let mut row = self.more_row(e, Some(t("settings.appearance.details")), cx);
                let th = page.theme.as_ref();
                let options = ["follow", "light", "dark", "dracula"]
                    .into_iter()
                    .map(|v| {
                        (
                            SharedString::from(v),
                            t(&format!("settings.appearance.{v}")),
                        )
                    })
                    .collect();
                row.body.push(segmented(
                    "appearance",
                    options,
                    th.map(|t| t.appearance.as_str()),
                    th.is_none(),
                    Rc::new(cx.listener(|this, v: &str, window, cx| {
                        this.set_appearance(v.to_string(), window, cx)
                    })),
                ));
                if let Some(th) = th {
                    let name = if th.name.is_empty() {
                        t("settings.appearance.noName")
                    } else {
                        th.name.clone()
                    };
                    row.body.push(note(if !th.available {
                        t("settings.appearance.noTheme")
                    } else {
                        let key = match th.appearance.as_str() {
                            "dracula" => "settings.appearance.onDracula",
                            "follow" => "settings.appearance.onThemeFollowing",
                            _ => "settings.appearance.onThemeNot",
                        };
                        let mode = if th.mode == "light" { "light" } else { "dark" };
                        tv(key, &[("name", (&name).into()), ("mode", mode.into())])
                    }));
                }
                row
            }
            "language" => {
                let mut row = self.more_row(e, None, cx);
                let options = LANGUAGES
                    .iter()
                    .map(|(tag, name)| (SharedString::from(*tag), name.to_string()))
                    .collect();
                row.body.push(segmented(
                    "lang",
                    options,
                    Some(agentbox_i18n::language()),
                    false,
                    Rc::new(cx.listener(|this, v: &str, window, cx| {
                        if let Some((tag, _)) = LANGUAGES.iter().find(|(tag, _)| *tag == v) {
                            this.set_language(tag, window, cx)
                        }
                    })),
                ));
                row
            }
            "group-idle" => {
                let on = prefs::group_idle();
                let mut row = self.more_row(e, None, cx);
                row.control = Some(toggle(
                    "switch-group-idle",
                    on,
                    false,
                    cx.listener(move |_, _: &ClickEvent, _, cx| {
                        prefs::set_bool(prefs::GROUP_IDLE, !on);
                        cx.notify();
                    }),
                ));
                row
            }
            "office" => {
                // The Office (#668) in the sidebar: off by default.
                let on = crate::office::enabled();
                let mut row = self.more_row(e, None, cx);
                row.control = Some(toggle(
                    "switch-office",
                    on,
                    false,
                    cx.listener(move |_, _: &ClickEvent, _, cx| {
                        prefs::set_bool(crate::office::PREF, !on);
                        cx.notify();
                    }),
                ));
                row
            }
            "tray" => {
                let on = prefs::tray_keep_running();
                let mut row = self.more_row(e, None, cx);
                row.control = Some(toggle(
                    "switch-tray",
                    on,
                    false,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        prefs::set_bool(prefs::TRAY_KEEP_RUNNING, !on);
                        this.tray_changed();
                        cx.notify();
                    }),
                ));
                row
            }
            "credit-ai-tool" => self.switch_row(
                e,
                "settings.creditAITool.details",
                |s| s.credit_ai_tool,
                |v| UpdateSettingsRequest {
                    credit_ai_tool: Some(v),
                    ..Default::default()
                },
                cx,
            ),
            "built-with-badge" => self.switch_row(
                e,
                "settings.builtWithBadge.details",
                |s| s.built_with_badge,
                |v| UpdateSettingsRequest {
                    built_with_badge: Some(v),
                    ..Default::default()
                },
                cx,
            ),
            "update-channel" => {
                let mut row = self.more_row(e, Some(t("settings.channel.details")), cx);
                let options = ["stable", "nightly"]
                    .into_iter()
                    .map(|v| (SharedString::from(v), t(&format!("settings.channel.{v}"))))
                    .collect();
                row.body.push(segmented(
                    "channel",
                    options,
                    update.map(|u| u.channel.as_str()),
                    update.is_none() || blocked.is_some(),
                    Rc::new(cx.listener(|this, v: &str, _, cx| {
                        if let Some(u) = this.settings_page.update.as_mut() {
                            u.channel = v.to_string();
                            u.available = None;
                        }
                        this.save(
                            UpdateSettingsRequest {
                                update_channel: Some(v.to_string()),
                                ..Default::default()
                            },
                            cx,
                        )
                    })),
                ));
                if let Some(b) = &blocked {
                    row.body.push(note(tv(
                        "settings.update.offBecause",
                        &[("reason", b.into())],
                    )));
                }
                row
            }
            "update-check" => {
                let version = update
                    .filter(|u| !u.current.is_empty())
                    .map(|u| format!(" ({})", u.current))
                    .unwrap_or_default();
                let details = tv(
                    "settings.updateCheck.details",
                    &[("version", (&version).into())],
                );
                let on = blocked.is_none() && s.is_some_and(|s| s.update_check);
                let mut row = self.more_row(e, Some(details), cx);
                row.control = Some(toggle(
                    "switch-update-check",
                    on,
                    s.is_none() || blocked.is_some(),
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.save(
                            UpdateSettingsRequest {
                                update_check: Some(!on),
                                ..Default::default()
                            },
                            cx,
                        )
                    }),
                ));
                if let Some(b) = &blocked {
                    row.body.push(note(tv(
                        "settings.update.offBecause",
                        &[("reason", b.into())],
                    )));
                }
                if let Some(a) = update.and_then(|u| u.available.as_ref())
                    && update.is_some_and(|u| u.current != a.version)
                {
                    row.body.push(note(tv(
                        "settings.updateCheck.isOut",
                        &[("version", (&a.version).into())],
                    )));
                }
                row
            }
            "usage-stats" => {
                let check_off = s.is_some_and(|s| !s.update_check);
                let on = blocked.is_none() && !check_off && s.is_some_and(|s| s.usage_stats);
                let mut row = self.more_row(e, Some(t("settings.usageStats.details")), cx);
                row.control = Some(toggle(
                    "switch-usage-stats",
                    on,
                    s.is_none() || blocked.is_some() || check_off,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.save(
                            UpdateSettingsRequest {
                                usage_stats: Some(!on),
                                ..Default::default()
                            },
                            cx,
                        )
                    }),
                ));
                if blocked.is_some() || check_off {
                    let reason = blocked.unwrap_or_else(|| t("settings.usageStats.checkOff"));
                    row.body.push(note(tv(
                        "settings.update.offBecause",
                        &[("reason", (&reason).into())],
                    )));
                }
                row.body.push(
                    h_flex()
                        .child(button(
                            "usage-see",
                            t("settings.usageStats.see"),
                            Btn::Ghost,
                            false,
                            cx.listener(|this, _: &ClickEvent, _, cx| this.see_sent(cx)),
                        ))
                        .into_any_element(),
                );
                row
            }
            "error-reports" => self.switch_row(
                e,
                "settings.errorReports.details",
                |s| s.error_reports,
                |v| UpdateSettingsRequest {
                    error_reports: Some(v),
                    ..Default::default()
                },
                cx,
            ),
            "block-full-suite" => self.switch_row(
                e,
                "settings.blockFullSuite.details",
                |s| s.block_full_suite,
                |v| UpdateSettingsRequest {
                    block_full_suite: Some(v),
                    ..Default::default()
                },
                cx,
            ),
            "agent-freeze" => self.switch_row(
                e,
                "settings.agentFreeze.details",
                |s| s.agent_freeze,
                |v| UpdateSettingsRequest {
                    agent_freeze: Some(v),
                    ..Default::default()
                },
                cx,
            ),
            "idle-reclaim" => {
                let mut row = self.switch_row(
                    e,
                    "settings.idleReclaim.details",
                    |s| s.idle_reclaim,
                    |v| UpdateSettingsRequest {
                        idle_reclaim: Some(v),
                        ..Default::default()
                    },
                    cx,
                );
                if let Some(s) = s.filter(|s| s.idle_reclaim) {
                    let wait = |id: &str,
                                label: &str,
                                value: i64,
                                choices: &[i64],
                                fallback: i64,
                                set: fn(i64) -> UpdateSettingsRequest,
                                cx: &mut Context<Self>| {
                        let mut values = choices.to_vec();
                        if !values.contains(&value) {
                            values.push(value);
                        }
                        let options = values
                            .iter()
                            .map(|n| {
                                let words = minutes_words(*n);
                                let label = if *n == fallback {
                                    tv(
                                        "defaults.newAgent.withDefault",
                                        &[("label", (&words).into())],
                                    )
                                } else {
                                    words
                                };
                                (SharedString::from(n.to_string()), label)
                            })
                            .collect();
                        v_flex()
                            .gap_1()
                            .child(kit::label(t(label)))
                            .child(segmented(
                                id,
                                options,
                                Some(&value.to_string()),
                                false,
                                Rc::new(cx.listener(move |this, v: &str, _, cx| {
                                    if let Ok(n) = v.parse::<i64>() {
                                        this.save(set(n), cx)
                                    }
                                })),
                            ))
                            .into_any_element()
                    };
                    row.body.push(
                        v_flex()
                            .gap_3()
                            .child(wait(
                                "freeze",
                                "settings.idleReclaim.freezeAfter",
                                if s.idle_freeze_seconds > 0 {
                                    s.idle_freeze_seconds
                                } else {
                                    DEFAULT_FREEZE
                                },
                                &FREEZE_TIMES,
                                DEFAULT_FREEZE,
                                |n| UpdateSettingsRequest {
                                    idle_freeze_seconds: Some(n),
                                    ..Default::default()
                                },
                                cx,
                            ))
                            .child(wait(
                                "stop",
                                "settings.idleReclaim.stopAfter",
                                if s.idle_stop_seconds > 0 {
                                    s.idle_stop_seconds
                                } else {
                                    DEFAULT_STOP
                                },
                                &STOP_TIMES,
                                DEFAULT_STOP,
                                |n| UpdateSettingsRequest {
                                    idle_stop_seconds: Some(n),
                                    ..Default::default()
                                },
                                cx,
                            ))
                            .into_any_element(),
                    );
                }
                row
            }
            _ => match self.defaults_row(e, cx) {
                Some(row) => row,
                None => self.more_row(e, None, cx),
            },
        };
        row.render()
    }
}

#[cfg(test)]
mod tests {
    use super::{Entry, Group, Section, essentials, minutes_words, search};

    fn sample() -> Vec<Section> {
        vec![Section {
            id: "general".into(),
            title: "General".into(),
            description: String::new(),
            nav: None,
            project: false,
            later: false,
            groups: vec![Group {
                id: "appearance",
                title: "Appearance".into(),
                description: String::new(),
                cards: false,
                entries: vec![
                    Entry {
                        id: "appearance",
                        label: "Aparência".into(),
                        description: "light or dark".into(),
                        keywords: "theme".into(),
                        essential: true,
                        modified: true,
                        scope: String::new(),
                    },
                    Entry {
                        id: "group-idle",
                        label: "Group idle projects".into(),
                        description: "the agent's sidebar".into(),
                        keywords: String::new(),
                        essential: false,
                        modified: false,
                        scope: String::new(),
                    },
                ],
            }],
        }]
    }

    #[test]
    fn search_matches_words_accents_and_changed() {
        let s = sample();
        let ids = |q: &str| -> Vec<&'static str> {
            search(&s, q)
                .iter()
                .flat_map(|s| s.groups.iter().flat_map(|g| g.entries.iter().map(|e| e.id)))
                .collect()
        };
        assert_eq!(ids("theme"), ["appearance"]);
        assert_eq!(ids("aparencia"), ["appearance"], "accents fold");
        assert_eq!(ids("agents sidebar"), ["group-idle"], "apostrophes fold");
        assert_eq!(ids("general idle"), ["group-idle"], "section title counts");
        assert_eq!(ids("@changed"), ["appearance"]);
        assert!(ids("nothing").is_empty());
        assert_eq!(essentials(&s).count(), 1);
    }

    #[test]
    fn minutes_read_as_electron_writes_them() {
        assert_eq!(minutes_words(300), "5m");
        assert_eq!(minutes_words(3600), "1h");
        assert_eq!(minutes_words(7200), "2h");
        assert_eq!(minutes_words(5400), "90m");
    }
}
