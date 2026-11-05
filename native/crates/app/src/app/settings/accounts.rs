//! Settings → Accounts (SettingsView.tsx's ClaudeAccounts, ClaudeLogin,
//! GitHubAccounts, GitHubLogin, CursorAccount, GeminiAccount, and
//! ToolAccounts.tsx): the logins agents run on, one card per AI tool and
//! GitHub's, each listing its stored accounts and the ways to add one.
//!
//! Everything is the daemon's: GET /v1/auth and /v1/setup say what's there,
//! and a sign-in is a job it runs (`claude setup-token`, Gemini CLI's Google
//! sign-in, `gh auth login --web`, Cursor's browser flow) that this page
//! polls once a second, opening the page it asks for with the system's
//! opener, and copying GitHub's device code, as Electron does.
use std::collections::{HashMap, HashSet};
use std::time::Duration;

use agentbox_api::client::Method;
use agentbox_api::{
    AuthStatus, ClaudeLogin, ClaudeLoginCodeRequest, ClaudeLoginRequest, ClaudeTokenRequest,
    CursorKeyRequest, CursorLogin, CursorLoginRequest, GitHubLogin, GitHubLoginRequest,
    GitHubTokenRequest, Job, RenameClaudeAccountRequest, RenamedClaudeAccount, SetupStatus,
    ToolAccount, ToolAccountRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::Input;
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, badge, button, labeled, note, note_in};
use super::defaults::ai_label;
use super::{Entry, Group, group};
use crate::theme;

/// The tools with a card under "AI tools", in the order the daemon lists
/// its checks when it hasn't answered yet.
const TOOLS: [&str; 5] = ["claude", "codex", "opencode", "cursor", "gemini"];

/// The form fields: (name, placeholder key or literal, masked).
pub(super) const FIELDS: [(&str, &str, bool); 19] = [
    ("claude-login-account", "default", false),
    ("claude-code", "settings.claudeLogin.codePlaceholder", false),
    ("claude-token-account", "default", false),
    ("claude-token", "sk-ant-oat01-…", true),
    ("gemini-login-account", "default", false),
    ("gemini-code", "settings.claudeLogin.codePlaceholder", false),
    ("gemini-account", "default", false),
    (
        "gemini-key",
        "settings.toolAccounts.keyPlaceholderGemini",
        true,
    ),
    ("codex-account", "default", false),
    (
        "codex-key",
        "settings.toolAccounts.keyPlaceholderCodex",
        true,
    ),
    ("opencode-provider", "anthropic", false),
    ("opencode-account", "default", false),
    (
        "opencode-key",
        "settings.toolAccounts.keyPlaceholderOpenCode",
        true,
    ),
    ("cursor-account", "default", false),
    ("cursor-key", "settings.cursor.keyPlaceholder", true),
    ("github-login-account", "default", false),
    ("github-token-account", "default", false),
    ("github-token", "settings.github.tokenPlaceholder", true),
    ("rename", "", false),
];

/// placeholder is a field's placeholder in the user's language.
pub(super) fn placeholder(p: &str) -> String {
    if p.contains('.') && agentbox_i18n::has(p) {
        t(p)
    } else {
        p.to_string()
    }
}

/// Login is one sign-in job this page follows.
#[derive(Default)]
pub(super) struct Login {
    job: Option<String>,
    status: Option<ClaudeLogin>,
    github: Option<GitHubLogin>,
    /// opened is the page (or GitHub's code) already opened, so a poll
    /// doesn't open another tab every second.
    opened: Option<String>,
    polling: bool,
    copied: bool,
    /// The sign-in job's log, as it's written (JobProgress).
    log: Vec<String>,
}

/// The Accounts section's own state.
#[derive(Default)]
pub(super) struct Accounts {
    pub auth: Option<AuthStatus>,
    pub setup: Option<SetupStatus>,
    /// renaming is the account being renamed: its card, name and provider.
    renaming: Option<(&'static str, String, String)>,
    /// pasting holds the cards whose "paste a token" form is open.
    pasting: HashSet<&'static str>,
    /// said is each card's last word on what it did (Electron's toasts).
    said: HashMap<&'static str, String>,
    errors: HashMap<&'static str, String>,
    logins: HashMap<&'static str, Login>,
    cursor: Option<CursorLogin>,
    cursor_polling: bool,
    cursor_opened: String,
}

/// enc is a path segment, percent-encoded.
fn enc(s: &str) -> String {
    let mut out = String::new();
    for b in s.bytes() {
        if b.is_ascii_alphanumeric() || b"-_.~".contains(&b) {
            out.push(b as char);
        } else {
            out.push_str(&format!("%{b:02X}"));
        }
    }
    out
}

/// static_id is a tool's id as the 'static an Entry keeps.
fn static_id(id: &str) -> Option<&'static str> {
    TOOLS.into_iter().chain(["github"]).find(|t| *t == id)
}

fn saved_label(saved_at: &str) -> String {
    let year: i32 = saved_at.get(..4).and_then(|y| y.parse().ok()).unwrap_or(0);
    if year < 2000 {
        return t("settings.accounts.savedUnknown");
    }
    tv(
        "settings.accounts.savedOn",
        &[("date", crate::dates::date_of(saved_at).into())],
    )
}

fn tool_accounts<'a>(auth: &'a AuthStatus, tool: &str) -> &'a [ToolAccount] {
    match tool {
        "codex" => &auth.codex_accounts,
        "opencode" => &auth.open_code_accounts,
        "cursor" => &auth.cursor_accounts,
        "gemini" => &auth.gemini_accounts,
        _ => &[],
    }
}

fn account_kind(a: &ToolAccount) -> String {
    let kind = match a.kind.as_str() {
        "apikey" => t("settings.toolAccounts.kindAPIKey"),
        "chatgpt" => t("settings.toolAccounts.kindChatGPT"),
        "google" => t("settings.toolAccounts.kindGoogle"),
        "oauth" => t("settings.toolAccounts.kindOAuth"),
        "wellknown" => t("settings.toolAccounts.kindWellKnown"),
        k => k.to_string(),
    };
    [kind, a.email.clone()]
        .into_iter()
        .filter(|s| !s.is_empty())
        .collect::<Vec<_>>()
        .join(" · ")
}

/// sentences joins a message and what follows it, as a toast's title and
/// description read.
fn sentences(a: &str, b: &str) -> String {
    if a.ends_with(['.', '!', '?']) {
        format!("{a} {b}")
    } else {
        format!("{a}. {b}")
    }
}

/// Line is one stored account, as a card lists it.
struct Line {
    name: String,
    provider: String,
    /// about is what follows the name: GitHub's login, a tool's sign-in.
    about: String,
    default: bool,
    badges: Vec<AnyElement>,
}

impl AppView {
    /// accounts_groups is the Accounts section: a card per AI tool, in the
    /// daemon's order, and GitHub's.
    pub(super) fn accounts_groups(&self) -> Vec<Group> {
        let acc = &self.settings_page.accounts;
        let mut ids: Vec<&'static str> = acc
            .setup
            .as_ref()
            .map(|s| {
                s.checks
                    .iter()
                    .filter_map(|c| static_id(&c.id).filter(|id| *id != "github"))
                    .collect()
            })
            .unwrap_or_default();
        if ids.is_empty() {
            ids = TOOLS.to_vec();
        }
        let card = |id: &'static str| {
            let (_, _, detail) = self.step(id);
            Entry {
                id,
                label: self.step_title(id),
                description: t(&format!("settings.setup.desc.{id}")),
                keywords: detail,
                essential: true,
                modified: false,
                scope: String::new(),
            }
        };
        vec![
            group(
                "ai",
                "settings.group.ai.title",
                ids.into_iter().map(card).collect(),
            )
            .cards(),
            group(
                "github",
                "settings.group.github.title",
                vec![card("github")],
            )
            .cards(),
        ]
    }

    fn step_title(&self, id: &str) -> String {
        let key = format!("settings.setup.title.{id}");
        if agentbox_i18n::has(&key) {
            return t(&key);
        }
        self.check(id).map(|c| c.title.clone()).unwrap_or_default()
    }

    fn check(&self, id: &str) -> Option<&agentbox_api::SetupCheck> {
        self.settings_page
            .accounts
            .setup
            .as_ref()
            .and_then(|s| s.checks.iter().find(|c| c.id == id))
    }

    /// step is a card's status, whether the daemon requires it, and its
    /// detail: GitHub's is the app's own, from GET /v1/auth.
    fn step(&self, id: &str) -> (String, bool, String) {
        if id == "github" {
            let auth = self.settings_page.accounts.auth.as_ref();
            let status = if auth.is_some_and(|a| a.github) {
                "ok"
            } else {
                "optional"
            };
            let detail = match auth.filter(|a| a.github) {
                Some(a) if !a.github_error.is_empty() => a.github_error.clone(),
                Some(a) if !a.github_user.is_empty() => tv(
                    "settings.setup.githubAs",
                    &[("user", (&a.github_user).into())],
                ),
                Some(_) => t("settings.setup.githubOwn"),
                None => String::new(),
            };
            return (status.into(), false, detail);
        }
        match self.check(id) {
            Some(c) => (c.status.clone(), c.required, c.detail.clone()),
            None => ("checking".into(), false, String::new()),
        }
    }

    /// load_accounts reads the logins, the checks, and any sign-in already
    /// running, which a page opened later picks back up.
    pub(super) fn load_accounts(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let (auth, setup, jobs) = (
                client.get::<AuthStatus>("/v1/auth").await,
                client.get::<SetupStatus>("/v1/setup").await,
                client.get::<Vec<Job>>("/v1/jobs").await,
            );
            this.update_in(cx, |this, window, cx| {
                let acc = &mut this.settings_page.accounts;
                if let Ok(a) = auth {
                    acc.auth = Some(a);
                }
                if let Ok(s) = setup {
                    acc.setup = Some(s);
                }
                let mut follow = Vec::new();
                for job in jobs.unwrap_or_default() {
                    let flow = match job.kind.as_str() {
                        "claude-login" => "claude",
                        "gemini-login" => "gemini",
                        "github-login" => "github",
                        _ => continue,
                    };
                    let l = acc.logins.entry(flow).or_default();
                    if job.status == "running" && l.job.is_none() {
                        l.job = Some(job.id);
                        follow.push(flow);
                    }
                }
                for flow in follow {
                    this.poll_login(flow, window, cx);
                }
                this.name_placeholders(window, cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// name_placeholders suggests "default" for a tool's first account and
    /// "work" once it has one (ToolAccounts.tsx).
    pub(super) fn name_placeholders(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(a) = self.settings_page.accounts.auth.as_ref() else {
            return;
        };
        let count = |field: &str| match field.split('-').next().unwrap_or("") {
            "claude" => a.claude_accounts.len(),
            "gemini" => a.gemini_accounts.len(),
            "codex" => a.codex_accounts.len(),
            "opencode" => a.open_code_accounts.len(),
            "cursor" => a.cursor_accounts.len(),
            "github" => a.github_accounts.len(),
            _ => 0,
        };
        let names: Vec<(&str, &str)> = FIELDS
            .iter()
            .filter(|(_, p, _)| *p == "default")
            .map(|(n, _, _)| (*n, if count(n) == 0 { "default" } else { "work" }))
            .collect();
        for (name, p) in names {
            if let Some(i) = self.settings_page.fields.get(name) {
                i.update(cx, |i, cx| i.set_placeholder(p, window, cx));
            }
        }
    }

    pub(super) fn field_value(&self, name: &str, cx: &App) -> String {
        self.settings_page
            .fields
            .get(name)
            .map(|i| i.read(cx).value().trim().to_string())
            .unwrap_or_default()
    }

    pub(super) fn clear_field(&self, name: &str, window: &mut Window, cx: &mut Context<Self>) {
        if let Some(i) = self.settings_page.fields.get(name) {
            i.update(cx, |i, cx| i.set_value("", window, cx));
        }
    }

    /// account_call sends one request for a card, says what went wrong on
    /// the card, and reads the accounts again once it's done.
    #[allow(clippy::too_many_arguments)]
    fn account_call(
        &mut self,
        card: &'static str,
        method: Method,
        path: String,
        body: Option<Vec<u8>>,
        window: &mut Window,
        cx: &mut Context<Self>,
        done: impl FnOnce(&mut Self, &[u8], &mut Window, &mut Context<Self>) + 'static,
    ) {
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let res = client.raw(method, &path, body).await;
            this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(b) => {
                        this.settings_page.accounts.errors.remove(card);
                        done(this, &b, window, cx);
                    }
                    Err(e) => {
                        this.settings_page
                            .accounts
                            .errors
                            .insert(card, e.to_string());
                    }
                }
                this.load_accounts(window, cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn say(&mut self, card: &'static str, text: String) {
        self.settings_page.accounts.said.insert(card, text);
    }

    /// account_base is where a card's stored account lives in the API.
    fn account_path(card: &str, name: &str, provider: &str) -> (String, String) {
        let query = if provider.is_empty() {
            String::new()
        } else {
            format!("?provider={}", enc(provider))
        };
        let base = match card {
            "claude" | "github" => format!("/v1/auth/{card}/{}", enc(name)),
            tool => format!("/v1/auth/accounts/{tool}/{}", enc(name)),
        };
        (base, query)
    }

    fn make_default(
        &mut self,
        card: &'static str,
        name: String,
        provider: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let (base, query) = Self::account_path(card, &name, &provider);
        self.account_call(
            card,
            Method::POST,
            format!("{base}/default{query}"),
            None,
            window,
            cx,
            move |this, _, _, _| {
                this.say(
                    card,
                    tv(
                        "settings.accounts.newAgentsUse",
                        &[("name", (&name).into())],
                    ),
                )
            },
        );
    }

    fn remove_account(
        &mut self,
        card: &'static str,
        name: String,
        provider: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let (base, query) = Self::account_path(card, &name, &provider);
        self.account_call(
            card,
            Method::DELETE,
            format!("{base}{query}"),
            None,
            window,
            cx,
            move |this, _, _, _| {
                let n = [("name", (&name).into())];
                let said = match card {
                    "claude" => sentences(
                        &tv("settings.accounts.removedClaude", &n),
                        &t("settings.accounts.removedNote"),
                    ),
                    "github" => sentences(
                        &tv("settings.github.removed", &n),
                        &t("settings.accounts.removedNote"),
                    ),
                    tool => sentences(
                        &tv(
                            "settings.toolAccounts.removed",
                            &[("tool", ai_label(tool).into()), ("name", (&name).into())],
                        ),
                        &t("settings.toolAccounts.removedNote"),
                    ),
                };
                this.say(card, said);
            },
        );
    }

    fn start_rename(
        &mut self,
        card: &'static str,
        name: String,
        provider: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if let Some(i) = self.settings_page.fields.get("rename") {
            i.update(cx, |i, cx| {
                i.set_value(name.clone(), window, cx);
                i.focus(window, cx);
            });
        }
        self.settings_page.accounts.renaming = Some((card, name, provider));
        cx.notify();
    }

    fn submit_rename(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some((card, from, provider)) = self.settings_page.accounts.renaming.clone() else {
            return;
        };
        let to = self.field_value("rename", cx);
        if to.is_empty() {
            return;
        }
        if to == from {
            self.settings_page.accounts.renaming = None;
            cx.notify();
            return;
        }
        let (base, query) = Self::account_path(card, &from, &provider);
        let body = serde_json::to_vec(&RenameClaudeAccountRequest { name: to }).ok();
        self.account_call(
            card,
            Method::POST,
            format!("{base}/rename{query}"),
            body,
            window,
            cx,
            move |this, b, _, _| {
                this.settings_page.accounts.renaming = None;
                let Ok(got) = serde_json::from_slice::<RenamedClaudeAccount>(b) else {
                    return;
                };
                let mut carried = Vec::new();
                if !got.projects.is_empty() {
                    carried.push(tv(
                        "settings.accounts.nProjects",
                        &[("count", got.projects.len().into())],
                    ));
                }
                if !got.agents.is_empty() {
                    carried.push(tv(
                        "settings.accounts.nAgents",
                        &[("count", got.agents.len().into())],
                    ));
                }
                let mut said = tv(
                    "settings.accounts.renamed",
                    &[("old", (&got.old).into()), ("name", (&got.name).into())],
                );
                if !carried.is_empty() {
                    said = sentences(
                        &said,
                        &tv(
                            "settings.accounts.movedWithIt",
                            &[("what", (&carried.join(", ")).into())],
                        ),
                    );
                }
                said = sentences(
                    &said,
                    &t(match card {
                        "claude" => "settings.accounts.sameToken",
                        "github" => "settings.github.keepToken",
                        _ => "settings.toolAccounts.keepLogin",
                    }),
                );
                this.say(card, said);
            },
        );
    }

    /// field_enter is Enter in one of the cards' fields: what its form does.
    pub(super) fn field_enter(&mut self, name: &str, window: &mut Window, cx: &mut Context<Self>) {
        match name {
            "rename" => self.submit_rename(window, cx),
            "claude-code" => self.send_code("claude", window, cx),
            "gemini-code" => self.send_code("gemini", window, cx),
            "claude-login-account" => self.start_login("claude", window, cx),
            "gemini-login-account" => self.start_login("gemini", window, cx),
            "github-login-account" => self.start_login("github", window, cx),
            "claude-token" | "claude-token-account" => self.save_claude_token(window, cx),
            "github-token" | "github-token-account" => self.save_github_token(window, cx),
            "cursor-key" | "cursor-account" => self.save_cursor_key(window, cx),
            "codex-key" | "codex-account" => self.save_tool_key("codex", window, cx),
            "gemini-key" | "gemini-account" => self.save_tool_key("gemini", window, cx),
            "opencode-key" | "opencode-account" | "opencode-provider" => {
                self.save_tool_key("opencode", window, cx)
            }
            _ => {}
        }
    }

    fn save_claude_token(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let token = self.field_value("claude-token", cx);
        if token.is_empty() {
            return;
        }
        let account = self.field_value("claude-token-account", cx);
        let shown = if account.is_empty() {
            "default".into()
        } else {
            account.clone()
        };
        let body = serde_json::to_vec(&ClaudeTokenRequest { token, account }).ok();
        self.account_call(
            "claude",
            Method::POST,
            "/v1/auth/claude".into(),
            body,
            window,
            cx,
            move |this, _, window, cx| {
                this.say(
                    "claude",
                    tv(
                        "settings.claudeLogin.saved",
                        &[("account", (&shown).into())],
                    ),
                );
                this.clear_field("claude-token", window, cx);
                this.clear_field("claude-token-account", window, cx);
            },
        );
    }

    fn save_github_token(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let token = self.field_value("github-token", cx);
        if token.is_empty() {
            return;
        }
        let account = self.field_value("github-token-account", cx);
        let shown = if account.is_empty() {
            "default".into()
        } else {
            account.clone()
        };
        let body = serde_json::to_vec(&GitHubTokenRequest { token, account }).ok();
        self.account_call(
            "github",
            Method::POST,
            "/v1/auth/github".into(),
            body,
            window,
            cx,
            move |this, b, window, cx| {
                let user = serde_json::from_slice::<serde_json::Value>(b)
                    .ok()
                    .and_then(|v| v["user"].as_str().map(String::from))
                    .unwrap_or_default();
                this.say(
                    "github",
                    tv(
                        "settings.github.saved",
                        &[("account", (&shown).into()), ("user", (&user).into())],
                    ),
                );
                this.clear_field("github-token", window, cx);
                this.clear_field("github-token-account", window, cx);
            },
        );
    }

    fn save_cursor_key(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let api_key = self.field_value("cursor-key", cx);
        if api_key.is_empty() {
            return;
        }
        let account = self.field_value("cursor-account", cx);
        let body = serde_json::to_vec(&CursorKeyRequest { api_key, account }).ok();
        self.account_call(
            "cursor",
            Method::POST,
            "/v1/auth/cursor".into(),
            body,
            window,
            cx,
            |this, _, window, cx| {
                this.say("cursor", t("settings.cursor.keySaved"));
                this.clear_field("cursor-key", window, cx);
                this.clear_field("cursor-account", window, cx);
            },
        );
    }

    fn save_tool_key(&mut self, tool: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        let api_key = self.field_value(&format!("{tool}-key"), cx);
        let provider = if tool == "opencode" {
            self.field_value("opencode-provider", cx)
        } else {
            String::new()
        };
        if api_key.is_empty() || (tool == "opencode" && provider.is_empty()) {
            return;
        }
        let account = self.field_value(&format!("{tool}-account"), cx);
        let shown = if account.is_empty() {
            "default".into()
        } else {
            account.clone()
        };
        let body = serde_json::to_vec(&ToolAccountRequest {
            account,
            provider,
            api_key,
        })
        .ok();
        self.account_call(
            tool,
            Method::POST,
            format!("/v1/auth/accounts/{tool}"),
            body,
            window,
            cx,
            move |this, _, window, cx| {
                this.say(
                    tool,
                    tv(
                        "settings.toolAccounts.saved",
                        &[
                            ("tool", ai_label(tool).into()),
                            ("account", (&shown).into()),
                        ],
                    ),
                );
                this.clear_field(&format!("{tool}-key"), window, cx);
                this.clear_field(&format!("{tool}-account"), window, cx);
            },
        );
    }

    /// start_login asks the daemon for a sign-in job and follows it.
    fn start_login(&mut self, flow: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        if self
            .settings_page
            .accounts
            .logins
            .get(flow)
            .is_some_and(|l| l.job.is_some() && Self::login_running(l))
        {
            return;
        }
        let account = self.field_value(&format!("{flow}-login-account"), cx);
        let body = if flow == "github" {
            serde_json::to_vec(&GitHubLoginRequest { account })
        } else {
            serde_json::to_vec(&ClaudeLoginRequest { account })
        }
        .ok();
        self.account_call(
            flow,
            Method::POST,
            format!("/v1/auth/{flow}/login"),
            body,
            window,
            cx,
            move |this, b, window, cx| {
                if let Ok(job) = serde_json::from_slice::<Job>(b) {
                    this.settings_page.accounts.said.remove(flow);
                    this.settings_page.accounts.logins.insert(
                        flow,
                        Login {
                            job: Some(job.id),
                            ..Default::default()
                        },
                    );
                    this.poll_login(flow, window, cx);
                }
            },
        );
    }

    fn login_running(l: &Login) -> bool {
        let status = l
            .status
            .as_ref()
            .map(|s| s.status.as_str())
            .or(l.github.as_ref().map(|s| s.status.as_str()));
        status.is_none_or(|s| s == "running")
    }

    fn cancel_login(&mut self, flow: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        let Some(job) = self
            .settings_page
            .accounts
            .logins
            .get(flow)
            .and_then(|l| l.job.clone())
        else {
            return;
        };
        self.account_call(
            flow,
            Method::POST,
            format!("/v1/jobs/{}/cancel", enc(&job)),
            None,
            window,
            cx,
            |_, _, _, _| {},
        );
    }

    fn send_code(&mut self, flow: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        let code = self.field_value(&format!("{flow}-code"), cx);
        let job = self
            .settings_page
            .accounts
            .logins
            .get(flow)
            .and_then(|l| l.job.clone());
        let (Some(job), false) = (job, code.is_empty()) else {
            return;
        };
        let body = serde_json::to_vec(&ClaudeLoginCodeRequest { code }).ok();
        self.account_call(
            flow,
            Method::POST,
            format!("/v1/auth/{flow}/login/{}/code", enc(&job)),
            body,
            window,
            cx,
            move |this, _, window, cx| this.clear_field(&format!("{flow}-code"), window, cx),
        );
    }

    /// poll_login reads a sign-in's status every second until it ends.
    fn poll_login(&mut self, flow: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        let l = self.settings_page.accounts.logins.entry(flow).or_default();
        let Some(job) = l.job.clone() else {
            return;
        };
        if l.polling {
            return;
        }
        l.polling = true;
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let path = format!("/v1/auth/{flow}/login/{}", enc(&job));
            loop {
                let more = if flow == "github" {
                    let r = client.get::<GitHubLogin>(&path).await;
                    this.update_in(cx, |this, window, cx| this.got_github(&job, r, window, cx))
                } else {
                    let r = client.get::<ClaudeLogin>(&path).await;
                    let log = client
                        .raw(Method::GET, &format!("/v1/jobs/{}/log", enc(&job)), None)
                        .await;
                    this.update_in(cx, |this, window, cx| {
                        if let Ok(b) = log
                            && let Some(l) = this.settings_page.accounts.logins.get_mut(flow)
                        {
                            l.log = String::from_utf8_lossy(&b)
                                .lines()
                                .map(String::from)
                                .collect();
                        }
                        this.got_login(flow, &job, r, window, cx)
                    })
                }
                .unwrap_or(false);
                if !more {
                    return;
                }
                cx.background_executor().timer(Duration::from_secs(1)).await;
            }
        })
        .detach();
    }

    /// got_login takes in a Claude Code or Gemini CLI sign-in's status, and
    /// says whether to keep polling.
    fn got_login(
        &mut self,
        flow: &'static str,
        job: &str,
        res: anyhow::Result<ClaudeLogin>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> bool {
        let acc = &mut self.settings_page.accounts;
        let Some(l) = acc
            .logins
            .get_mut(flow)
            .filter(|l| l.job.as_deref() == Some(job))
        else {
            return false;
        };
        let s = match res {
            Ok(s) => s,
            Err(e) => {
                l.polling = false;
                acc.errors.insert(flow, e.to_string());
                cx.notify();
                return false;
            }
        };
        // Open the page once it's known, and only once. Gemini's only page is
        // the one that ends on a code; Claude Code's code page is a second,
        // parallel login, and never opened by itself.
        let first = if !s.url.is_empty() {
            Some(s.url.clone())
        } else if flow == "gemini" && !s.code_url.is_empty() {
            Some(s.code_url.clone())
        } else {
            None
        };
        if let Some(url) = first
            && l.opened.as_ref() != Some(&url)
        {
            cx.open_url(&url);
            l.opened = Some(url);
        }
        let running = s.status == "running";
        let (status, account) = (s.status.clone(), s.account.clone());
        l.status = Some(s);
        if !running {
            l.polling = false;
            if status == "succeeded" {
                let key = if flow == "gemini" {
                    "settings.geminiLogin.saved"
                } else {
                    "settings.claudeLogin.saved"
                };
                acc.said
                    .insert(flow, tv(key, &[("account", (&account).into())]));
                self.clear_field(&format!("{flow}-login-account"), window, cx);
                self.load_accounts(window, cx);
            }
        }
        cx.notify();
        running
    }

    fn got_github(
        &mut self,
        job: &str,
        res: anyhow::Result<GitHubLogin>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> bool {
        let acc = &mut self.settings_page.accounts;
        let Some(l) = acc
            .logins
            .get_mut("github")
            .filter(|l| l.job.as_deref() == Some(job))
        else {
            return false;
        };
        let s = match res {
            Ok(s) => s,
            Err(e) => {
                l.polling = false;
                acc.errors.insert("github", e.to_string());
                cx.notify();
                return false;
            }
        };
        // Copy the code and open GitHub's page as soon as there is a code,
        // once: the page asks for exactly what is on the clipboard.
        if !s.user_code.is_empty()
            && !s.verification_uri.is_empty()
            && l.opened.as_ref() != Some(&s.user_code)
        {
            cx.write_to_clipboard(ClipboardItem::new_string(s.user_code.clone()));
            l.copied = true;
            cx.open_url(&s.verification_uri);
            l.opened = Some(s.user_code.clone());
        }
        let running = s.status == "running";
        if !running {
            l.polling = false;
            if s.status == "succeeded" {
                acc.said.insert(
                    "github",
                    tv(
                        "settings.github.saved",
                        &[("account", (&s.account).into()), ("user", (&s.user).into())],
                    ),
                );
                self.clear_field("github-login-account", window, cx);
                self.load_accounts(window, cx);
            }
        }
        if let Some(l) = self.settings_page.accounts.logins.get_mut("github") {
            l.github = Some(s);
        }
        cx.notify();
        running
    }

    fn start_cursor_login(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let account = self.field_value("cursor-account", cx);
        let body = serde_json::to_vec(&CursorLoginRequest { account }).ok();
        self.account_call(
            "cursor",
            Method::POST,
            "/v1/auth/cursor/login".into(),
            body,
            window,
            cx,
            |this, b, window, cx| {
                let acc = &mut this.settings_page.accounts;
                acc.cursor = serde_json::from_slice::<CursorLogin>(b).ok();
                acc.cursor_opened.clear();
                acc.said.remove("cursor");
                this.poll_cursor(window, cx);
            },
        );
    }

    fn poll_cursor(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.settings_page.accounts.cursor_polling {
            return;
        }
        self.settings_page.accounts.cursor_polling = true;
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            loop {
                cx.background_executor().timer(Duration::from_secs(1)).await;
                let r = client.get::<CursorLogin>("/v1/auth/cursor/login").await;
                let more = this
                    .update_in(cx, |this, window, cx| {
                        let acc = &mut this.settings_page.accounts;
                        let Ok(s) = r else {
                            acc.cursor_polling = false;
                            return false;
                        };
                        if s.state == "waiting" && !s.url.is_empty() && acc.cursor_opened != s.url {
                            cx.open_url(&s.url);
                            acc.cursor_opened = s.url.clone();
                        }
                        let more = s.state == "starting" || s.state == "waiting";
                        let done = s.state == "done";
                        acc.cursor = Some(s);
                        if !more {
                            acc.cursor_polling = false;
                        }
                        if done {
                            acc.said.insert("cursor", t("settings.cursor.signedIn"));
                            acc.cursor = None;
                            this.load_accounts(window, cx);
                        }
                        cx.notify();
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

    // ---- drawing ----

    fn input(&self, name: &str) -> AnyElement {
        match self.settings_page.fields.get(name) {
            Some(i) => Input::new(i).small().into_any_element(),
            None => div().into_any_element(),
        }
    }

    /// account_card is one tool's card (ChecklistStep): its state, what it's
    /// for, and everything there is to do with its accounts.
    pub(super) fn account_card(&self, e: &Entry, cx: &mut Context<Self>) -> AnyElement {
        let (status, required, detail) = self.step(e.id);
        let ok = status == "ok";
        let (mark, color) = match status.as_str() {
            "ok" => ("✓", theme::running()),
            "optional" => ("○", theme::faint()),
            "checking" => ("…", theme::faint()),
            _ => ("!", theme::starting()),
        };
        let warn = theme::starting();
        let step_badge = match status.as_str() {
            "warn" => Some(badge(t("settings.setup.badgeCheck"), warn)),
            "optional" => Some(badge(t("settings.setup.badgeOptional"), theme::muted())),
            "ok" | "checking" => None,
            _ if !required => Some(badge(t("settings.setup.badgeNotSetUp"), warn)),
            "outdated" => Some(badge(t("settings.setup.badgeOutdated"), warn)),
            _ => Some(badge(t("settings.setup.badgeNeeded"), warn)),
        };
        let acc = &self.settings_page.accounts;
        let body = self.card_body(e.id, cx);
        v_flex()
            .id(SharedString::from(format!("account-card-{}", e.id)))
            .p_4()
            .rounded_xl()
            .border_1()
            .border_color(if !ok && required && status != "checking" {
                warn.opacity(0.3)
            } else {
                theme::border()
            })
            .bg(theme::panel())
            .child(
                h_flex()
                    .items_start()
                    .gap_3()
                    .child(
                        div()
                            .flex_none()
                            .w(px(20.))
                            .h(px(20.))
                            .rounded_full()
                            .flex()
                            .items_center()
                            .justify_center()
                            .bg(color.opacity(0.15))
                            .text_xs()
                            .font_weight(FontWeight::BOLD)
                            .text_color(color)
                            .child(mark),
                    )
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_1()
                            .child(
                                h_flex()
                                    .gap_2()
                                    .child(
                                        div()
                                            .text_sm()
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(theme::fg())
                                            .child(e.label.clone()),
                                    )
                                    .children(step_badge),
                            )
                            .child(note(e.description.clone()))
                            .when(!detail.is_empty(), |el| {
                                el.child(note_in(
                                    detail.clone(),
                                    if ok { theme::running() } else { theme::faint() },
                                ))
                            })
                            .child(v_flex().pt_3().gap_3().children(body))
                            .when_some(acc.said.get(e.id), |el, said| {
                                el.child(note_in(said.clone(), theme::running()))
                            })
                            .when_some(acc.errors.get(e.id), |el, err| {
                                el.child(note_in(err.clone(), theme::error()))
                            }),
                    ),
            )
            .into_any_element()
    }

    fn card_body(&self, id: &'static str, cx: &mut Context<Self>) -> Vec<AnyElement> {
        let Some(auth) = self.settings_page.accounts.auth.clone() else {
            return vec![note(t("common.loading"))];
        };
        let fix = self.check(id).map(|c| c.fix.clone()).unwrap_or_default();
        let command = |fallback: &str| {
            if fix.is_empty() {
                fallback.to_string()
            } else {
                fix.clone()
            }
        };
        let mut out = Vec::new();
        match id {
            "claude" => {
                out.extend(self.default_reaches(!auth.claude_accounts.is_empty(), |p| {
                    !p.claude_account.is_empty()
                }));
                let lines = auth
                    .claude_accounts
                    .iter()
                    .map(|a| Line {
                        name: a.name.clone(),
                        provider: String::new(),
                        about: saved_label(&a.saved_at),
                        default: a.default,
                        badges: [
                            a.default.then(|| {
                                badge(t("settings.accounts.badgeDefault"), theme::accent())
                            }),
                            (a.valid == "rejected").then(|| {
                                badge(t("settings.accounts.badgeRejected"), theme::error())
                            }),
                            (a.valid == "valid").then(|| {
                                badge(t("settings.accounts.badgeValid"), theme::running())
                            }),
                        ]
                        .into_iter()
                        .flatten()
                        .collect(),
                    })
                    .collect();
                out.push(self.account_lines("claude", lines, cx));
                out.push(self.login_form("claude", !auth.claude_accounts.is_empty(), cx));
                out.push(self.paste_form("claude", cx));
            }
            "github" => {
                out.extend(self.default_reaches(!auth.github_accounts.is_empty(), |p| {
                    !p.github_account.is_empty()
                }));
                let lines = auth
                    .github_accounts
                    .iter()
                    .map(|a| Line {
                        name: a.name.clone(),
                        provider: String::new(),
                        about: a.login.clone(),
                        default: a.default,
                        badges: a
                            .default
                            .then(|| badge(t("settings.accounts.badgeDefault"), theme::accent()))
                            .into_iter()
                            .collect(),
                    })
                    .collect();
                out.push(self.account_lines("github", lines, cx));
                out.push(self.github_login(!auth.github_accounts.is_empty(), cx));
                out.push(self.paste_form("github", cx));
                out.push(note(t("settings.github.note")));
            }
            "codex" | "opencode" => {
                out.push(self.tool_lines(id, &auth, cx));
                let no_image = fix == format!("agentbox image build --{id}");
                if !no_image {
                    out.push(self.key_form(id, cx));
                }
                out.push(note(if no_image {
                    t(&format!("settings.setup.{id}NoImage"))
                } else {
                    t(&format!("settings.setup.{id}Body"))
                }));
                out.push(command_box(id, command(&format!("agentbox auth {id}")), cx));
            }
            "cursor" => {
                out.push(signed_in_line(
                    auth.cursor,
                    if auth.cursor_email.is_empty() {
                        t("settings.cursor.signedInPlain")
                    } else {
                        tv(
                            "settings.cursor.signedInAs",
                            &[("email", (&auth.cursor_email).into())],
                        )
                    },
                    t("settings.cursor.notSignedIn"),
                ));
                out.push(self.tool_lines("cursor", &auth, cx));
                out.push(self.key_form("cursor", cx));
                out.push(self.cursor_login(cx));
                out.push(note(t("settings.cursor.cliBody")));
                out.push(command_box(id, command("agentbox auth cursor"), cx));
            }
            "gemini" => {
                out.push(signed_in_line(
                    auth.gemini,
                    if auth.gemini_email.is_empty() {
                        t("settings.gemini.signedInPlain")
                    } else {
                        tv(
                            "settings.gemini.signedInAs",
                            &[("email", (&auth.gemini_email).into())],
                        )
                    },
                    t("settings.gemini.notSignedIn"),
                ));
                out.push(self.tool_lines("gemini", &auth, cx));
                out.push(self.login_form("gemini", !auth.gemini_accounts.is_empty(), cx));
                out.push(self.key_form("gemini", cx));
                out.push(note(t("settings.gemini.cliBody")));
                out.push(command_box(id, command("agentbox auth gemini"), cx));
            }
            _ => {}
        }
        out
    }

    /// default_reaches says the default reaches only projects set to it, and
    /// names the rest.
    fn default_reaches(
        &self,
        any: bool,
        own: impl Fn(&agentbox_api::Project) -> bool,
    ) -> Option<AnyElement> {
        if !any {
            return None;
        }
        let rest: Vec<&str> = self
            .projects
            .iter()
            .filter(|p| own(p))
            .map(|p| p.name.as_str())
            .collect();
        let mut text = t("settings.accounts.defaultReaches");
        if !rest.is_empty() {
            text.push(' ');
            text.push_str(&tv(
                "settings.accounts.theRest",
                &[("names", (&rest.join(", ")).into())],
            ));
        }
        Some(note(text))
    }

    fn tool_lines(
        &self,
        tool: &'static str,
        auth: &AuthStatus,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let lines = tool_accounts(auth, tool)
            .iter()
            .map(|a| Line {
                name: a.name.clone(),
                provider: a.provider.clone(),
                about: account_kind(a),
                default: a.default,
                badges: [
                    a.default
                        .then(|| badge(t("settings.accounts.badgeDefault"), theme::accent())),
                    a.expired
                        .then(|| badge(t("settings.toolAccounts.expired"), theme::error())),
                ]
                .into_iter()
                .flatten()
                .collect(),
            })
            .collect();
        self.account_lines(tool, lines, cx)
    }

    /// account_lines lists a card's stored accounts, each with what can be
    /// done to it: made the default, renamed, removed.
    fn account_lines(
        &self,
        card: &'static str,
        lines: Vec<Line>,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        if lines.is_empty() {
            return div().into_any_element();
        }
        let renaming = self.settings_page.accounts.renaming.clone();
        v_flex()
            .rounded_lg()
            .border_1()
            .border_color(theme::border())
            .bg(theme::bg())
            .children(lines.into_iter().enumerate().map(|(i, l)| {
                let this_one = renaming
                    .as_ref()
                    .is_some_and(|(c, n, p)| *c == card && *n == l.name && *p == l.provider);
                let key = format!("{card}-{}-{}", l.provider, l.name);
                let (name, provider) = (l.name.clone(), l.provider.clone());
                let (n2, p2) = (name.clone(), provider.clone());
                let (n3, p3) = (name.clone(), provider.clone());
                h_flex()
                    .id(SharedString::from(format!("account-{key}")))
                    .gap_2()
                    .px_3()
                    .py_2()
                    .when(i > 0, |el| el.border_t_1().border_color(theme::border()))
                    .child(
                        h_flex()
                            .flex_1()
                            .min_w_0()
                            .flex_wrap()
                            .gap_2()
                            .when(!l.provider.is_empty(), |el| {
                                el.child(
                                    div()
                                        .text_xs()
                                        .text_color(theme::faint())
                                        .child(l.provider.clone()),
                                )
                            })
                            .map(|el| {
                                if this_one {
                                    el.child(div().w(px(180.)).child(self.input("rename")))
                                        .child(button(
                                            SharedString::from(format!("rename-do-{key}")),
                                            t("common.rename"),
                                            Btn::Secondary,
                                            false,
                                            cx.listener(|this, _: &ClickEvent, window, cx| {
                                                this.submit_rename(window, cx)
                                            }),
                                        ))
                                        .child(button(
                                            SharedString::from(format!("rename-cancel-{key}")),
                                            t("common.cancel"),
                                            Btn::Ghost,
                                            false,
                                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                                this.settings_page.accounts.renaming = None;
                                                cx.notify();
                                            }),
                                        ))
                                } else {
                                    el.child(
                                        div()
                                            .text_sm()
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(theme::fg())
                                            .child(l.name.clone()),
                                    )
                                }
                            })
                            .when(!l.about.is_empty(), |el| {
                                el.child(
                                    div()
                                        .text_xs()
                                        .text_color(theme::muted())
                                        .child(l.about.clone()),
                                )
                            })
                            .children(l.badges),
                    )
                    .child(
                        h_flex()
                            .flex_none()
                            .gap_1()
                            .when(!l.default, |el| {
                                el.child(button(
                                    SharedString::from(format!("default-{key}")),
                                    t("settings.accounts.makeDefault"),
                                    Btn::Ghost,
                                    false,
                                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                                        this.make_default(
                                            card,
                                            name.clone(),
                                            provider.clone(),
                                            window,
                                            cx,
                                        )
                                    }),
                                ))
                            })
                            .when(!this_one, |el| {
                                el.child(button(
                                    SharedString::from(format!("rename-{key}")),
                                    t("common.rename"),
                                    Btn::Ghost,
                                    false,
                                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                                        this.start_rename(card, n2.clone(), p2.clone(), window, cx)
                                    }),
                                ))
                            })
                            .child(button(
                                SharedString::from(format!("remove-{key}")),
                                t("common.remove"),
                                Btn::Ghost,
                                false,
                                cx.listener(move |this, _: &ClickEvent, window, cx| {
                                    this.remove_account(card, n3.clone(), p3.clone(), window, cx)
                                }),
                            )),
                    )
            }))
            .into_any_element()
    }

    /// login_form is Claude Code's, or Gemini CLI's, sign-in (ClaudeLogin):
    /// a name for the account, and once it runs, how far it got.
    fn login_form(&self, flow: &'static str, any: bool, cx: &mut Context<Self>) -> AnyElement {
        let l = self.settings_page.accounts.logins.get(flow);
        let job = l.and_then(|l| l.job.clone());
        let st = l.and_then(|l| l.status.clone());
        let running = job.is_some() && st.as_ref().is_none_or(|s| s.status == "running");
        let (intro, log_in, add, starting, saved) = if flow == "gemini" {
            (
                "settings.geminiLogin.intro",
                "settings.geminiLogin.logIn",
                "settings.geminiLogin.addAccount",
                "settings.geminiLogin.starting",
                "settings.geminiLogin.saved",
            )
        } else {
            (
                "settings.claudeLogin.intro",
                "settings.claudeLogin.logIn",
                "settings.claudeLogin.addAccount",
                "settings.claudeLogin.starting",
                "settings.claudeLogin.saved",
            )
        };
        let mut col = v_flex().gap_2().child(note(t(intro))).child(
            h_flex()
                .items_end()
                .gap_2()
                .child(labeled(
                    t("settings.accounts.account"),
                    self.input(&format!("{flow}-login-account")),
                ))
                .child(button(
                    SharedString::from(format!("{flow}-login")),
                    if running {
                        format!("{}…", t(if any { add } else { log_in }))
                    } else {
                        t(if any { add } else { log_in })
                    },
                    Btn::Primary,
                    running,
                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.start_login(flow, window, cx)
                    }),
                ))
                .when(running, |el| {
                    el.child(button(
                        SharedString::from(format!("{flow}-login-cancel")),
                        t("common.cancel"),
                        Btn::Ghost,
                        false,
                        cx.listener(move |this, _: &ClickEvent, window, cx| {
                            this.cancel_login(flow, window, cx)
                        }),
                    ))
                }),
        );
        if job.is_some() {
            let url = st.as_ref().map(|s| s.url.clone()).filter(|u| !u.is_empty());
            let code_url = st
                .as_ref()
                .map(|s| s.code_url.clone())
                .filter(|u| !u.is_empty());
            let words = match st.as_ref().map(|s| s.status.as_str()) {
                Some("succeeded") => tv(
                    saved,
                    &[("account", (&st.as_ref().unwrap().account).into())],
                ),
                Some("cancelled") => t("settings.claudeLogin.cancelled"),
                Some("failed") => t("settings.claudeLogin.failed"),
                _ if url.is_some() => t("settings.claudeLogin.waiting"),
                _ if code_url.is_some() => t("settings.claudeLogin.approveThenCode"),
                _ => t(starting),
            };
            let failed = st.as_ref().is_some_and(|s| s.status == "failed");
            col = col.child(
                v_flex()
                    .gap_2()
                    .px_3()
                    .py_2p5()
                    .rounded_lg()
                    .border_1()
                    .border_color(theme::border())
                    .bg(theme::bg())
                    .child(
                        h_flex()
                            .flex_wrap()
                            .gap_2()
                            .child(note_in(
                                words,
                                if running { theme::muted() } else { theme::fg() },
                            ))
                            .when_some(url.clone().filter(|_| running), |el, url| {
                                el.child(button(
                                    SharedString::from(format!("{flow}-open-again")),
                                    t("settings.claudeLogin.openAgain"),
                                    Btn::Secondary,
                                    false,
                                    move |_, _, cx| cx.open_url(&url),
                                ))
                            }),
                    )
                    .when(failed, |el| {
                        el.when_some(st.as_ref().map(|s| s.error.clone()), |el, e| {
                            el.child(note_in(e, theme::error()))
                        })
                    })
                    .when_some(code_url.filter(|_| running), |el, code_url| {
                        el.child(note(t(if url.is_some() {
                            "settings.claudeLogin.codeHintAlt"
                        } else {
                            "settings.claudeLogin.codeHint"
                        })))
                        .child(
                            h_flex()
                                .gap_2()
                                .child(div().flex_1().child(self.input(&format!("{flow}-code"))))
                                .child(button(
                                    SharedString::from(format!("{flow}-code-page")),
                                    t("native.settings.openCodePage"),
                                    Btn::Ghost,
                                    false,
                                    move |_, _, cx| cx.open_url(&code_url),
                                ))
                                .child(button(
                                    SharedString::from(format!("{flow}-send")),
                                    t("settings.claudeLogin.send"),
                                    Btn::Secondary,
                                    false,
                                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                                        this.send_code(flow, window, cx)
                                    }),
                                )),
                        )
                    }),
            );
            if let Some(l) = l.filter(|l| !l.log.is_empty()) {
                col = col.child(crate::app::add_project::job_log(&l.log, None));
            }
        }
        col.into_any_element()
    }

    /// github_login is `gh auth login --web` through the daemon: gh's code,
    /// copied, and GitHub's page opened.
    fn github_login(&self, any: bool, cx: &mut Context<Self>) -> AnyElement {
        let l = self.settings_page.accounts.logins.get("github");
        let job = l.and_then(|l| l.job.clone());
        let st = l.and_then(|l| l.github.clone());
        let copied = l.is_some_and(|l| l.copied);
        let running = job.is_some() && st.as_ref().is_none_or(|s| s.status == "running");
        let label = t(if any {
            "settings.githubLogin.addAccount"
        } else {
            "settings.githubLogin.signIn"
        });
        let mut col = v_flex()
            .gap_2()
            .child(note(t("settings.github.intro")))
            .child(
                h_flex()
                    .items_end()
                    .gap_2()
                    .child(labeled(
                        t("settings.accounts.account"),
                        self.input("github-login-account"),
                    ))
                    .child(button(
                        "github-login",
                        if running {
                            format!("{label}…")
                        } else {
                            label
                        },
                        Btn::Primary,
                        running,
                        cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.start_login("github", window, cx)
                        }),
                    ))
                    .when(running, |el| {
                        el.child(button(
                            "github-login-cancel",
                            t("common.cancel"),
                            Btn::Ghost,
                            false,
                            cx.listener(|this, _: &ClickEvent, window, cx| {
                                this.cancel_login("github", window, cx)
                            }),
                        ))
                    }),
            );
        if job.is_some() {
            let s = st.clone().unwrap_or_default();
            let ended = match (s.status.as_str(), s.outcome.as_str()) {
                ("succeeded", _) => Some(tv(
                    "settings.github.saved",
                    &[("account", (&s.account).into()), ("user", (&s.user).into())],
                )),
                ("cancelled", _) => Some(t("settings.githubLogin.cancelled")),
                (_, "expired") => Some(t("settings.githubLogin.expired")),
                (_, "denied") => Some(t("settings.githubLogin.denied")),
                ("failed", _) => Some(t("settings.githubLogin.failed")),
                _ => None,
            };
            let words = ended.unwrap_or_else(|| {
                t(if s.user_code.is_empty() {
                    "settings.githubLogin.starting"
                } else {
                    "settings.githubLogin.waiting"
                })
            });
            let code = s.user_code.clone();
            let uri = s.verification_uri.clone();
            col = col.child(
                v_flex()
                    .gap_2()
                    .px_3()
                    .py_2p5()
                    .rounded_lg()
                    .border_1()
                    .border_color(theme::border())
                    .bg(theme::bg())
                    .when(running && !code.is_empty(), |el| {
                        let (c2, u2) = (code.clone(), uri.clone());
                        el.child(note(t("settings.githubLogin.enterCode")))
                            .child(
                                h_flex()
                                    .flex_wrap()
                                    .gap_2()
                                    .child(
                                        div()
                                            .px_3()
                                            .py_1()
                                            .rounded_md()
                                            .border_1()
                                            .border_color(theme::border())
                                            .bg(theme::raised())
                                            .font_family(crate::theme::mono())
                                            .text_lg()
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(theme::fg())
                                            .child(code.clone()),
                                    )
                                    .child(button(
                                        "github-code-copy",
                                        if copied {
                                            t("settings.githubLogin.copied")
                                        } else {
                                            t("settings.githubLogin.copy")
                                        },
                                        Btn::Secondary,
                                        false,
                                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                                            cx.write_to_clipboard(ClipboardItem::new_string(
                                                c2.clone(),
                                            ));
                                            if let Some(l) =
                                                this.settings_page.accounts.logins.get_mut("github")
                                            {
                                                l.copied = true;
                                            }
                                            cx.notify();
                                        }),
                                    ))
                                    .when(!uri.is_empty(), |el| {
                                        el.child(button(
                                            "github-open",
                                            t("settings.githubLogin.open"),
                                            Btn::Secondary,
                                            false,
                                            move |_, _, cx| cx.open_url(&u2),
                                        ))
                                    }),
                            )
                            .when(!uri.is_empty(), |el| {
                                el.child(note(tv(
                                    "settings.githubLogin.where",
                                    &[("url", (&uri).into())],
                                )))
                            })
                    })
                    .child(
                        h_flex()
                            .flex_wrap()
                            .gap_2()
                            .child(note_in(
                                words,
                                if running { theme::muted() } else { theme::fg() },
                            ))
                            .when(!running && s.status != "succeeded", |el| {
                                el.child(button(
                                    "github-again",
                                    t(if s.outcome == "expired" {
                                        "settings.githubLogin.newCode"
                                    } else {
                                        "settings.githubLogin.tryAgain"
                                    }),
                                    Btn::Secondary,
                                    false,
                                    cx.listener(|this, _: &ClickEvent, window, cx| {
                                        this.start_login("github", window, cx)
                                    }),
                                ))
                            }),
                    )
                    .when(s.status == "failed" && s.outcome != "expired", |el| {
                        el.child(note_in(
                            if s.outcome == "denied" {
                                t("settings.githubLogin.deniedHint")
                            } else {
                                s.error.clone()
                            },
                            theme::muted(),
                        ))
                    }),
            );
        }
        col.into_any_element()
    }

    /// paste_form is "paste a token instead": Claude Code's or GitHub's.
    fn paste_form(&self, card: &'static str, cx: &mut Context<Self>) -> AnyElement {
        let open = self.settings_page.accounts.pasting.contains(card);
        let toggle = div()
            .id(SharedString::from(format!("{card}-paste")))
            .w_auto()
            .text_xs()
            .text_color(theme::muted())
            .cursor_pointer()
            .hover(|s| s.text_color(theme::fg()))
            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                let p = &mut this.settings_page.accounts.pasting;
                if !p.remove(card) {
                    p.insert(card);
                }
                cx.notify();
            }))
            .child(format!(
                "{} {}",
                if open { "▾" } else { "▸" },
                t(if card == "github" {
                    "settings.github.useToken"
                } else {
                    "settings.accounts.pasteToken"
                })
            ));
        let mut col = v_flex().gap_2().child(h_flex().child(toggle));
        if open {
            let (intro, after) = if card == "github" {
                ("settings.github.tokenIntro", None)
            } else {
                (
                    "settings.claudeToken.intro",
                    Some("settings.claudeToken.note"),
                )
            };
            col = col
                .child(note(t(intro)))
                .child(
                    h_flex()
                        .flex_wrap()
                        .items_end()
                        .gap_2()
                        .child(labeled(
                            t("settings.accounts.account"),
                            self.input(&format!("{card}-token-account")),
                        ))
                        .child(labeled(
                            t("settings.accounts.token"),
                            self.input(&format!("{card}-token")),
                        ))
                        .child(button(
                            SharedString::from(format!("{card}-token-save")),
                            t("common.save"),
                            Btn::Primary,
                            false,
                            cx.listener(move |this, _: &ClickEvent, window, cx| {
                                if card == "github" {
                                    this.save_github_token(window, cx)
                                } else {
                                    this.save_claude_token(window, cx)
                                }
                            }),
                        )),
                )
                .children(after.map(|k| note(t(k))));
        }
        col.into_any_element()
    }

    /// key_form adds an account from an API key: Codex's, OpenCode's (for a
    /// provider), Gemini CLI's, or Cursor's.
    fn key_form(&self, tool: &'static str, cx: &mut Context<Self>) -> AnyElement {
        let intro = match tool {
            "codex" => Some("settings.toolAccounts.introCodex"),
            "opencode" => Some("settings.toolAccounts.introOpenCode"),
            "gemini" => Some("settings.toolAccounts.introGemini"),
            _ => None,
        };
        let key_label = if tool == "cursor" {
            t("settings.cursor.keyLabel")
        } else {
            t("settings.toolAccounts.apiKey")
        };
        v_flex()
            .gap_2()
            .children(intro.map(|k| note(t(k))))
            .child(
                h_flex()
                    .flex_wrap()
                    .items_end()
                    .gap_2()
                    .when(tool == "opencode", |el| {
                        el.child(labeled(
                            t("settings.toolAccounts.provider"),
                            self.input("opencode-provider"),
                        ))
                    })
                    .child(labeled(
                        t("settings.accounts.account"),
                        self.input(&format!("{tool}-account")),
                    ))
                    .child(labeled(key_label, self.input(&format!("{tool}-key"))))
                    .child(button(
                        SharedString::from(format!("{tool}-key-save")),
                        if tool == "cursor" {
                            t("settings.cursor.saveKey")
                        } else {
                            t("common.save")
                        },
                        Btn::Secondary,
                        false,
                        cx.listener(move |this, _: &ClickEvent, window, cx| {
                            if tool == "cursor" {
                                this.save_cursor_key(window, cx)
                            } else {
                                this.save_tool_key(tool, window, cx)
                            }
                        }),
                    )),
            )
            .into_any_element()
    }

    fn cursor_login(&self, cx: &mut Context<Self>) -> AnyElement {
        let st = self.settings_page.accounts.cursor.clone();
        let state = st.as_ref().map(|s| s.state.clone()).unwrap_or_default();
        let waiting = state == "starting" || state == "waiting";
        let url = st.as_ref().map(|s| s.url.clone()).unwrap_or_default();
        v_flex()
            .gap_2()
            .child(
                h_flex()
                    .gap_3()
                    .child(button(
                        "cursor-login",
                        t("settings.cursor.signIn"),
                        Btn::Secondary,
                        waiting,
                        cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.start_cursor_login(window, cx)
                        }),
                    ))
                    .when(state == "waiting" && !url.is_empty(), |el| {
                        el.child(button(
                            "cursor-open",
                            t("settings.cursor.openPage"),
                            Btn::Ghost,
                            false,
                            move |_, _, cx| cx.open_url(&url),
                        ))
                    }),
            )
            .when(waiting, |el| {
                el.child(note(t(if state == "waiting" {
                    "settings.cursor.waiting"
                } else {
                    "settings.cursor.starting"
                })))
            })
            .when(state == "failed", |el| {
                let e = st.as_ref().map(|s| s.error.clone()).unwrap_or_default();
                el.child(note_in(
                    if e.is_empty() {
                        t("settings.cursor.failed")
                    } else {
                        e
                    },
                    theme::error(),
                ))
            })
            .into_any_element()
    }
}

fn signed_in_line(on: bool, yes: String, no: String) -> AnyElement {
    h_flex()
        .gap_2()
        .child(div().size(px(8.)).rounded_full().bg(if on {
            theme::running()
        } else {
            theme::faint()
        }))
        .child(
            div()
                .text_sm()
                .text_color(theme::fg())
                .child(if on { yes } else { no }),
        )
        .into_any_element()
}

/// command_box is a command to run in a terminal, with a button that copies it.
fn command_box(id: &str, command: String, _cx: &mut Context<AppView>) -> AnyElement {
    let copy = command.clone();
    h_flex()
        .gap_2()
        .px_3()
        .py_1p5()
        .rounded_lg()
        .border_1()
        .border_color(theme::border())
        .bg(theme::bg())
        .child(
            div()
                .flex_1()
                .min_w_0()
                .font_family(crate::theme::mono())
                .text_xs()
                .text_color(theme::fg())
                .child(command),
        )
        .child(button(
            SharedString::from(format!("{id}-command-copy")),
            t("common.copy"),
            Btn::Ghost,
            false,
            move |_, _, cx| cx.write_to_clipboard(ClipboardItem::new_string(copy.clone())),
        ))
        .into_any_element()
}

#[cfg(test)]
mod tests {
    use super::{enc, saved_label};

    #[test]
    fn names_are_escaped_in_paths() {
        assert_eq!(enc("work"), "work");
        assert_eq!(enc("my work/2"), "my%20work%2F2");
    }

    #[test]
    fn a_zero_time_is_no_date() {
        assert_eq!(
            saved_label("0001-01-01T00:00:00Z"),
            "saved on an unknown date"
        );
        assert!(saved_label("2026-11-05T13:55:18Z").starts_with("saved Nov "));
    }
}
