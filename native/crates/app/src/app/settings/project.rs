//! A project's settings, under Projects in Settings' column
//! (ProjectSettings.tsx, ProjectAccounts.tsx, ToolCredential.tsx), with its
//! secrets (SecretsTab.tsx for a project) and skills (SkillsPanel for a
//! project) after them. Every field is GET/PATCH /v1/projects/{p}; secrets are
//! /v1/projects/{p}/secrets (a value only ever goes in), and what its agents
//! run on is GET /v1/projects/{p}/credentials.
//!
//! Choosing another account doesn't offer to move the agents that already
//! run on the old one, as Electron's dialog does: they keep theirs, and only
//! new agents get the new one.
use std::collections::BTreeMap;
use std::rc::Rc;

use agentbox_api::client::Method;
use agentbox_api::{
    AGENT_MODEL_AUTO, ChatOptionChoice, Project, Secret, SecretSettingsRequest, SetSecretRequest,
    ToolAccount, ToolCredential, UpdateProjectRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{
    Btn, Choice, Row, button, labeled, note_in, settings_select as select, toggle,
};
use super::defaults::{ai_label, format_tokens, time_ago};
use super::kit::{ask, card, enc, link, mono, plain, title};
use super::kit::{badge, note};
use super::{Entry, Group};
use crate::theme;

const TOOLS: [&str; 5] = ["claude", "codex", "opencode", "cursor", "gemini"];
const MODES: [&str; 4] = ["standard", "auto_review", "full_access", "plan"];
const SETTLE: [i64; 6] = [1, 4, 24, 72, 168, 0];

#[derive(Default)]
pub(super) struct ProjectPage {
    secrets: std::collections::HashMap<String, Result<Vec<Secret>, String>>,
    credentials: std::collections::HashMap<String, Result<Vec<ToolCredential>, String>>,
    said: std::collections::HashMap<String, String>,
    errors: std::collections::HashMap<String, String>,
    removing: Option<(String, String)>,
    /// An account change that asks whether the agents still on the old one
    /// move too (MoveAgentsDialog).
    moving: Option<Moving>,
    busy: bool,
    show_credentials: bool,
}

/// Moving is a project's Claude Code or GitHub account about to change, with
/// how many of its agents still use the one it replaces.
pub(super) struct Moving {
    project: String,
    github: bool,
    account: String,
    from: String,
    to: String,
    count: usize,
}

/// account_label names an account as the question reads best: quoted, or
/// "the default account" for the machine's own.
fn account_label(name: &str) -> String {
    if name.is_empty() {
        t("project.accounts.theDefault")
    } else {
        format!("\u{201c}{name}\u{201d}")
    }
}

/// Rows is which of a tool's settings a project offers (agentToolRows).
struct Rows {
    model: bool,
    effort: bool,
    window: bool,
    modes: &'static [&'static str],
}

fn tool_rows(p: &Project) -> Rows {
    match if p.agent_tool.is_empty() {
        "claude"
    } else {
        p.agent_tool.as_str()
    } {
        "codex" => Rows {
            model: false,
            effort: false,
            window: false,
            modes: &["standard", "auto_review", "full_access"],
        },
        "opencode" => Rows {
            model: true,
            effort: false,
            window: false,
            modes: &["plan"],
        },
        "cursor" => Rows {
            model: true,
            effort: true,
            window: false,
            modes: &[],
        },
        "gemini" => Rows {
            model: true,
            effort: false,
            window: false,
            modes: &["standard", "full_access", "plan"],
        },
        _ => Rows {
            model: true,
            effort: true,
            window: true,
            modes: &MODES,
        },
    }
}

fn mode_key(m: &str) -> &'static str {
    match m {
        "standard" => "defaults.project.modeStandard",
        "auto_review" => "defaults.project.modeAutoReview",
        "full_access" => "defaults.project.modeFullAccess",
        "plan" => "defaults.project.modePlan",
        _ => "defaults.project.modeNone",
    }
}

fn label_of(p: &Project) -> String {
    if p.display_name.is_empty() {
        p.name.clone()
    } else {
        p.display_name.clone()
    }
}

/// pentry is one of a project's settings, as the search and Essentials see it.
fn pentry(
    id: &'static str,
    label: &str,
    description: String,
    keywords: &str,
    project: &str,
) -> Entry {
    Entry {
        id,
        label: t(label),
        description,
        keywords: if keywords.is_empty() {
            String::new()
        } else {
            t(keywords)
        },
        essential: false,
        modified: false,
        scope: project.to_string(),
    }
}

fn pgroup(id: &'static str, title: String, description: String, entries: Vec<Entry>) -> Group {
    Group {
        id,
        title,
        description,
        cards: false,
        entries,
    }
}

fn choice_label(c: &ChatOptionChoice) -> String {
    let n = c.name.trim();
    let n = n
        .strip_suffix("(recommended)")
        .or_else(|| n.strip_suffix("(Recommended)"))
        .unwrap_or(n)
        .trim_end();
    if n.is_empty() {
        c.value.clone()
    } else {
        n.to_string()
    }
}

/// tone is credentialTone: idle when not running, a warning when an
/// agent's AI tool bills a variable rather than its account.
fn tone(c: &ToolCredential) -> &'static str {
    if !c.running || !c.error.is_empty() {
        "idle"
    } else if c.variable.is_empty() || c.variable == "CLAUDE_CODE_OAUTH_TOKEN" {
        "ok"
    } else {
        "warning"
    }
}

/// credential_text is ToolCredentialText: what an agent's AI tool runs on.
fn credential_text(c: &ToolCredential) -> (String, Hsla) {
    let mut text = if !c.error.is_empty() {
        t("agent.credential.error")
    } else if !c.running {
        t("agent.credential.notRunning")
    } else if c.variable == "CLAUDE_CODE_OAUTH_TOKEN" {
        if c.account.is_empty() {
            t("agent.credential.unknownToken")
        } else {
            tv(
                "agent.credential.account",
                &[("account", (&c.account).into())],
            )
        }
    } else if !c.variable.is_empty() {
        if c.account.is_empty() {
            tv(
                "agent.credential.variable",
                &[("variable", (&c.variable).into())],
            )
        } else {
            tv(
                "agent.credential.variableOver",
                &[
                    ("variable", (&c.variable).into()),
                    ("account", (&c.account).into()),
                ],
            )
        }
    } else if !c.account.is_empty() {
        tv(
            "agent.credential.toolAccount",
            &[("account", (&c.account).into())],
        )
    } else {
        t("agent.credential.ownLogin")
    };
    if !c.others.is_empty() {
        text = format!(
            "{text} · {}",
            tv(
                "agent.credential.others",
                &[("names", (&c.others.join(", ")).into())]
            )
        );
    }
    let color = match tone(c) {
        "warning" => theme::starting(),
        "idle" => theme::faint(),
        _ => theme::primary(),
    };
    (text, color)
}

/// credentials_summary is summarizeCredentials: how many agents bill a
/// variable, then how many run on each account, the idle last.
fn credentials_summary(list: &[ToolCredential]) -> (String, bool) {
    let mut off: usize = 0;
    let mut variables: Vec<String> = Vec::new();
    let mut counts: Vec<(String, usize)> = Vec::new();
    for c in list {
        let on = match tone(c) {
            "warning" => {
                off += 1;
                if !variables.contains(&c.variable) {
                    variables.push(c.variable.clone());
                }
                continue;
            }
            "idle" => "idle".to_string(),
            _ if !c.variable.is_empty() => {
                if c.account.is_empty() {
                    "token".into()
                } else {
                    c.account.clone()
                }
            }
            _ => {
                if c.account.is_empty() {
                    "own".into()
                } else {
                    c.account.clone()
                }
            }
        };
        match counts.iter_mut().find(|(k, _)| *k == on) {
            Some((_, n)) => *n += 1,
            None => counts.push((on, 1)),
        }
    }
    variables.sort();
    counts.sort_by(|a, b| (a.0 == "idle").cmp(&(b.0 == "idle")).then(b.1.cmp(&a.1)));
    let mut parts = Vec::new();
    if off > 0 {
        parts.push(tv(
            "project.accounts.runningOnOff",
            &[
                ("count", off.into()),
                ("variables", (&variables.join(", ")).into()),
            ],
        ));
    }
    for (on, count) in counts {
        parts.push(if on == "idle" {
            tv("project.accounts.runningOnIdle", &[("count", count.into())])
        } else {
            let on = match on.as_str() {
                "own" => t("project.accounts.runningOnOwn"),
                "token" => t("project.accounts.runningOnToken"),
                _ => on,
            };
            tv(
                "project.accounts.runningOnGroup",
                &[("count", count.into()), ("on", (&on).into())],
            )
        });
    }
    (parts.join(" · "), off > 0)
}

impl AppView {
    fn project(&self, name: &str) -> Option<&Project> {
        self.projects.iter().find(|p| p.name == name)
    }

    /// project_groups is a project's section: its settings, its secrets and
    /// its skills.
    pub(super) fn project_groups(&self, p: &Project) -> Vec<Group> {
        let name = label_of(p);
        let n = p.name.as_str();
        let rows = tool_rows(p);
        let mut agents = vec![
            pentry(
                "p-tool",
                "defaults.project.toolLabel",
                t("defaults.project.toolDescription"),
                "defaults.project.toolKeywords",
                n,
            )
            .modified(!p.agent_tool.is_empty()),
        ];
        if rows.model {
            agents.push(
                pentry(
                    "p-model",
                    "defaults.project.modelLabel",
                    t("defaults.project.modelDescription"),
                    "defaults.project.modelKeywords",
                    n,
                )
                .essential()
                .modified(!p.agent_model.is_empty()),
            );
        }
        if rows.effort {
            agents.push(
                pentry(
                    "p-effort",
                    "defaults.project.effortLabel",
                    t("defaults.project.effortDescription"),
                    "defaults.project.effortKeywords",
                    n,
                )
                .modified(!p.agent_effort.is_empty()),
            );
        }
        if rows.window {
            agents.push(
                pentry(
                    "p-window",
                    "defaults.project.windowLabel",
                    t("defaults.project.windowDescription"),
                    "defaults.project.windowKeywords",
                    n,
                )
                .modified(!p.agent_context_window.is_empty()),
            );
        }
        if !rows.modes.is_empty() {
            agents.push(
                pentry(
                    "p-mode",
                    "defaults.project.modeLabel",
                    t("defaults.project.modeDescription"),
                    "defaults.project.modeKeywords",
                    n,
                )
                .modified(!p.agent_mode.is_empty()),
            );
        }
        agents.push(
            pentry(
                "p-prefix",
                "defaults.project.prefixLabel",
                plain(&tv(
                    "defaults.project.prefixDescription",
                    &[(
                        "example",
                        (&format!("{}fix-login-redirect", p.branch_prefix)).into(),
                    )],
                )),
                "defaults.project.prefixKeywords",
                n,
            )
            .modified(p.branch_prefix != "agentbox/"),
        );
        let secrets = match self.settings_page.project.secrets.get(n) {
            Some(Ok(l)) => l.len(),
            _ => 0,
        };
        let card_entry = |id: &'static str, label: String, description: String| Entry {
            id,
            label,
            description,
            keywords: String::new(),
            essential: false,
            modified: false,
            scope: n.to_string(),
        };
        vec![
            pgroup(
                "p-name",
                t("defaults.project.groupProject"),
                String::new(),
                vec![
                    pentry(
                        "p-name",
                        "defaults.project.nameLabel",
                        plain(&tv("defaults.project.nameDescription", &[("id", n.into())])),
                        "defaults.project.nameKeywords",
                        n,
                    )
                    .essential(),
                ],
            ),
            pgroup(
                "p-agents",
                t("defaults.project.groupAgents"),
                tv(
                    "defaults.project.groupAgentsDescription",
                    &[("name", (&name).into())],
                ),
                agents,
            ),
            pgroup(
                "p-accounts",
                t("defaults.project.groupAccounts"),
                t("defaults.project.groupAccountsDescription"),
                vec![
                    pentry(
                        "p-claude-account",
                        "defaults.project.claudeAccountLabel",
                        t("project.accounts.claudeDescription"),
                        "defaults.project.claudeAccountKeywords",
                        n,
                    )
                    .essential()
                    .modified(!p.claude_account.is_empty()),
                    pentry(
                        "p-claude-accounts",
                        "defaults.project.allowedLabel",
                        String::new(),
                        "defaults.project.allowedKeywords",
                        n,
                    )
                    .modified(!p.claude_accounts.is_empty()),
                    pentry(
                        "p-codex-account",
                        "project.accounts.codexLabel",
                        t("project.accounts.codexDescription"),
                        "project.accounts.codexKeywords",
                        n,
                    )
                    .modified(!p.codex_account.is_empty()),
                    pentry(
                        "p-opencode-accounts",
                        "project.accounts.openCodeLabel",
                        t("project.accounts.openCodeDescription"),
                        "project.accounts.openCodeKeywords",
                        n,
                    )
                    .modified(!p.open_code_accounts.is_empty()),
                    pentry(
                        "p-cursor-account",
                        "project.accounts.cursorLabel",
                        t("project.accounts.cursorDescription"),
                        "project.accounts.cursorKeywords",
                        n,
                    )
                    .modified(!p.cursor_account.is_empty()),
                    pentry(
                        "p-gemini-account",
                        "project.accounts.geminiLabel",
                        t("project.accounts.geminiDescription"),
                        "project.accounts.geminiKeywords",
                        n,
                    )
                    .modified(!p.gemini_account.is_empty()),
                    pentry(
                        "p-credentials",
                        "project.accounts.runningOnLabel",
                        t("project.accounts.runningOnDescription"),
                        "project.accounts.runningOnKeywords",
                        n,
                    ),
                    pentry(
                        "p-github-account",
                        "defaults.project.githubAccountLabel",
                        t("project.accounts.githubDescription"),
                        "defaults.project.githubAccountKeywords",
                        n,
                    )
                    .essential()
                    .modified(!p.github_account.is_empty()),
                ],
            ),
            pgroup(
                "p-repository",
                t("defaults.project.groupRepo"),
                t("defaults.project.groupRepoDescription"),
                vec![
                    pentry(
                        "p-sync-base",
                        "defaults.project.syncLabel",
                        t("defaults.project.syncOnDescription"),
                        "defaults.project.syncKeywords",
                        n,
                    )
                    .modified(!p.sync_base),
                ],
            ),
            pgroup(
                "p-pulls",
                t("defaults.project.groupPulls"),
                t("defaults.project.groupPullsDescription"),
                vec![
                    pentry(
                        "p-pulls-tab",
                        "defaults.project.pullsTabLabel",
                        t("defaults.project.pullsTabOnDescription"),
                        "defaults.project.pullsTabKeywords",
                        n,
                    )
                    .modified(!p.pull_requests),
                    pentry(
                        "p-agent-prs",
                        "defaults.project.agentPRsLabel",
                        t("defaults.project.agentPRsOnDescription"),
                        "defaults.project.agentPRsKeywords",
                        n,
                    )
                    .modified(p.agent_p_rs),
                    pentry(
                        "p-pick-up",
                        "defaults.project.pickUpLabel",
                        t("defaults.project.pickUpOnDescription"),
                        "defaults.project.pickUpKeywords",
                        n,
                    )
                    .modified(p.pick_up_issues),
                    pentry(
                        "p-pr-watch",
                        "defaults.project.prWatchLabel",
                        t("defaults.project.prWatchDescription"),
                        "defaults.project.prWatchKeywords",
                        n,
                    )
                    .modified(!p.pr_watch.is_empty()),
                ],
            ),
            pgroup(
                "p-chat",
                t("defaults.project.groupChat"),
                tv(
                    "defaults.project.groupChatDescription",
                    &[("name", (&name).into())],
                ),
                vec![
                    pentry(
                        "p-autonomy",
                        "defaults.project.autonomyLabel",
                        t("defaults.project.autonomyOnDescription"),
                        "defaults.project.autonomyKeywords",
                        n,
                    )
                    .modified(p.autonomy == "on"),
                    pentry(
                        "p-finish",
                        "defaults.project.finishLabel",
                        t("defaults.project.finishDescription"),
                        "defaults.project.finishKeywords",
                        n,
                    )
                    .modified(p.finish_notices != "lead"),
                    pentry(
                        "p-settle",
                        "defaults.project.settleLabel",
                        t("defaults.project.settleDescription"),
                        "defaults.project.settleKeywords",
                        n,
                    )
                    .modified(p.settle_hours != 24),
                ],
            ),
            pgroup(
                "p-tests",
                t("defaults.project.groupTests"),
                String::new(),
                vec![
                    pentry(
                        "p-full-suite",
                        "defaults.project.fullSuiteLabel",
                        t("defaults.project.fullSuiteOnDescription"),
                        "defaults.project.fullSuiteKeywords",
                        n,
                    )
                    .modified(p.full_test_suite),
                ],
            ),
            pgroup(
                "p-testing",
                t("defaults.project.groupTesting"),
                String::new(),
                vec![
                    pentry(
                        "p-nesting",
                        "defaults.project.nestingLabel",
                        t("defaults.project.nestingDescription"),
                        "defaults.project.nestingKeywords",
                        n,
                    )
                    .modified(p.nesting),
                ],
            ),
            Group {
                id: "p-secrets",
                title: tv("agent.secrets.projectTitle", &[("count", secrets.into())]),
                description: t("agent.secrets.projectDescription"),
                cards: true,
                entries: vec![card_entry(
                    "p-secrets",
                    tv("agent.secrets.projectTitle", &[("count", secrets.into())]),
                    t("agent.secrets.giveProjectDescription"),
                )],
            },
            Group {
                id: "p-tokens",
                title: t("project.view.section.tokens"),
                description: String::new(),
                cards: true,
                entries: vec![Entry {
                    id: "p-tokens",
                    label: t("project.view.section.tokens"),
                    description: t("vm.tokens.intro"),
                    keywords: String::new(),
                    essential: false,
                    modified: false,
                    scope: n.to_string(),
                }],
            },
            Group {
                id: "p-skills",
                title: t("skills.projectTitle"),
                description: String::new(),
                cards: true,
                entries: vec![card_entry(
                    "skills",
                    t("skills.projectTitle"),
                    t("skills.projectDescription"),
                )],
            },
        ]
    }

    /// load_project reads what a project's section shows besides the
    /// project itself, and puts its name and prefix in their fields.
    pub(super) fn load_project(
        &mut self,
        name: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if let Some(p) = self.project(&name).cloned() {
            for (field, v) in [
                ("project-name", label_of(&p)),
                ("project-prefix", p.branch_prefix.clone()),
            ] {
                if let Some(i) = self.settings_page.fields.get(field) {
                    i.update(cx, |i, cx| i.set_value(v, window, cx));
                }
            }
        }
        self.load_skills(name.clone(), cx);
        self.load_secrets(name.clone(), cx);
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let creds = client
                .get::<Vec<ToolCredential>>(&format!("/v1/projects/{}/credentials", enc(&name)))
                .await;
            this.update(cx, |this, cx| {
                this.settings_page
                    .project
                    .credentials
                    .insert(name, creds.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn load_secrets(&mut self, name: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<Vec<Secret>>(&format!("/v1/projects/{}/secrets", enc(&name)))
                .await;
            this.update(cx, |this, cx| {
                this.settings_page
                    .project
                    .secrets
                    .insert(name, res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// patch_project sends one change to PATCH /v1/projects/{p}, keeps the
    /// project it answers, and says what changed.
    fn patch_project(
        &mut self,
        name: String,
        req: UpdateProjectRequest,
        describe: impl FnOnce(&Project) -> Option<String> + 'static,
        cx: &mut Context<Self>,
    ) {
        let client = self.client.clone();
        self.settings_page.project.busy = true;
        cx.spawn(async move |this, cx| {
            let body = serde_json::to_vec(&req).unwrap_or_default();
            let res = client
                .raw(
                    Method::PATCH,
                    &format!("/v1/projects/{}", enc(&name)),
                    Some(body),
                )
                .await
                .and_then(|b| Ok(serde_json::from_slice::<Project>(&b)?));
            this.update(cx, |this, cx| {
                let pp = &mut this.settings_page.project;
                pp.busy = false;
                match res {
                    Ok(p) => {
                        pp.errors.remove(&name);
                        if let Some(s) = describe(&p) {
                            pp.said.insert(name.clone(), s);
                        }
                        if let Some(old) = this.projects.iter_mut().find(|x| x.name == p.name) {
                            *old = p;
                        }
                    }
                    Err(e) => {
                        pp.errors.insert(name, e.to_string());
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    pub(super) fn save_project_name(&mut self, name: String, cx: &mut Context<Self>) {
        let v = self.field_value("project-name", cx);
        if v.is_empty() || self.project(&name).is_some_and(|p| label_of(p) == v) {
            return;
        }
        self.patch_project(
            name,
            UpdateProjectRequest {
                display_name: Some(v),
                ..Default::default()
            },
            |p| {
                Some(tv(
                    "defaults.project.renamedToast",
                    &[("name", (&label_of(p)).into())],
                ))
            },
            cx,
        );
    }

    pub(super) fn save_project_prefix(&mut self, name: String, cx: &mut Context<Self>) {
        let v = self.field_value("project-prefix", cx);
        if self.project(&name).is_some_and(|p| p.branch_prefix == v) {
            return;
        }
        self.patch_project(
            name,
            UpdateProjectRequest {
                branch_prefix: Some(v),
                ..Default::default()
            },
            |p| {
                Some(tv(
                    "defaults.project.prefixToast",
                    &[
                        ("name", (&label_of(p)).into()),
                        (
                            "branch",
                            (&format!("{}<{}>", p.branch_prefix, t("defaults.project.theirWork")))
                                .into(),
                        ),
                    ],
                ))
            },
            cx,
        );
    }

    /// open_project_name is the project whose name and prefix fields are
    /// shown: the open section's.
    pub(super) fn open_project_name(&self) -> Option<String> {
        // Or the lead's, whose panel shows its settings.
        if !self.settings && self.panel_settings.is_some() {
            return self.project.clone();
        }
        self.settings_page
            .open
            .strip_prefix("project:")
            .map(String::from)
    }

    fn psave(
        &self,
        name: &str,
        req: impl Fn(&str) -> UpdateProjectRequest + 'static,
        describe: fn(&Project, &str) -> Option<String>,
        cx: &mut Context<Self>,
    ) -> super::super::controls::Pick<str> {
        let name = name.to_string();
        Rc::new(cx.listener(move |this, v: &str, _, cx| {
            let v2 = v.to_string();
            this.patch_project(name.clone(), req(v), move |p| describe(p, &v2), cx)
        }))
    }

    /// pick_account changes a project's Claude Code (or GitHub) account,
    /// asking first whether the agents still on the old one move too when
    /// any do (ClaudeAccountPicker, GitHubAccountPicker).
    fn pick_account(
        &self,
        name: &str,
        github: bool,
        current: String,
        fallback: Option<String>,
        cx: &mut Context<Self>,
    ) -> super::super::controls::Pick<str> {
        let name = name.to_string();
        Rc::new(cx.listener(move |this, v: &str, _, cx| {
            let from = if current.is_empty() {
                fallback.clone().unwrap_or_default()
            } else {
                current.clone()
            };
            let count = this
                .fleet
                .iter()
                .filter(|a| a.project == name)
                .filter(|a| {
                    if github {
                        a.github_account == from
                    } else {
                        a.ai == "claude" && a.claude_account == from
                    }
                })
                .count();
            if v == current || count == 0 {
                this.set_account(name.clone(), github, v.to_string(), false, cx);
                return;
            }
            this.settings_page.project.moving = Some(Moving {
                project: name.clone(),
                github,
                account: v.to_string(),
                from,
                to: if v.is_empty() {
                    fallback.clone().unwrap_or_default()
                } else {
                    v.to_string()
                },
                count,
            });
            cx.notify();
        }))
    }

    fn set_account(
        &mut self,
        project: String,
        github: bool,
        account: String,
        move_agents: bool,
        cx: &mut Context<Self>,
    ) {
        let req = if github {
            UpdateProjectRequest {
                github_account: Some(account),
                move_git_hub_agents: move_agents,
                ..Default::default()
            }
        } else {
            UpdateProjectRequest {
                claude_account: Some(account),
                move_claude_agents: move_agents,
                ..Default::default()
            }
        };
        self.patch_project(
            project,
            req,
            move |p| {
                let (key, account) = match (
                    github,
                    if github {
                        &p.github_account
                    } else {
                        &p.claude_account
                    },
                ) {
                    (true, a) if a.is_empty() => ("project.accounts.githubDefault", a),
                    (true, a) => ("project.accounts.githubSet", a),
                    (false, a) if a.is_empty() => ("project.accounts.claudeDefault", a),
                    (false, a) => ("project.accounts.claudeSet", a),
                };
                Some(tv(
                    key,
                    &[("name", (&p.name).into()), ("account", account.into())],
                ))
            },
            cx,
        );
    }

    /// render_move_agents is MoveAgentsDialog: the change goes ahead either
    /// way; only whether the agents come with it differs.
    pub(crate) fn render_move_agents(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let m = self.settings_page.project.moving.as_ref()?;
        let n = m.count;
        let close: crate::app::agent::OnCancel = {
            let me = cx.entity().downgrade();
            Rc::new(move |_, cx: &mut App| {
                me.update(cx, |this, cx| {
                    this.settings_page.project.moving = None;
                    cx.notify();
                })
                .ok();
            })
        };
        let choose = |move_agents: bool| {
            cx.listener(move |this: &mut Self, _: &ClickEvent, _, cx| {
                if let Some(m) = this.settings_page.project.moving.take() {
                    this.set_account(m.project, m.github, m.account, move_agents, cx);
                }
            })
        };
        Some(crate::app::agent::modal(
            "move-agents",
            t("project.accounts.moveTitle"),
            Some(tv(
                "project.accounts.moveQuestion",
                &[
                    ("count", n.into()),
                    ("from", account_label(&m.from).into()),
                    ("to", account_label(&m.to).into()),
                ],
            )),
            vec![
                h_flex()
                    .justify_end()
                    .child(super::super::controls::button(
                        "move-agents-leave",
                        tv("project.accounts.leave", &[("count", n.into())]),
                        super::super::controls::Btn::Secondary,
                        false,
                        choose(false),
                    ))
                    .into_any_element(),
            ],
            close,
            tv("project.accounts.move", &[("count", n.into())]),
            false,
            false,
            choose(true),
        ))
    }

    fn pswitch(
        &self,
        e: &Entry,
        on: bool,
        details: Option<String>,
        req: fn(bool) -> UpdateProjectRequest,
        describe: fn(&Project) -> String,
        cx: &mut Context<Self>,
    ) -> Row {
        let mut row = self.more_row(e, details, cx);
        let name = e.scope.clone();
        row.control = Some(toggle(
            SharedString::from(format!("switch-{}-{}", e.scope, e.id)),
            on,
            self.settings_page.project.busy,
            cx.listener(move |this, _: &ClickEvent, _, cx| {
                this.patch_project(name.clone(), req(!on), move |p| Some(describe(p)), cx)
            }),
        ));
        row
    }

    /// account_select is a picker over a tool's stored accounts, with the
    /// default first and an account since removed kept in its place.
    fn account_choices(names: Vec<String>, fallback: Option<String>, chosen: &str) -> Vec<Choice> {
        let mut choices = vec![Choice::new(
            "",
            match fallback {
                Some(f) => tv("project.newAgent.defaultOption", &[("value", (&f).into())]),
                None => t("common.default"),
            },
        )];
        if !chosen.is_empty() && !names.iter().any(|n| n == chosen) {
            let mut c = Choice::new(
                chosen.to_string(),
                tv("project.accounts.removed", &[("account", chosen.into())]),
            );
            c.disabled = true;
            choices.push(c);
        }
        choices.extend(names.into_iter().map(|n| Choice::new(n.clone(), n)));
        choices
    }

    fn tool_accounts(&self, tool: &str) -> Vec<ToolAccount> {
        let Some(a) = self.settings_page.accounts.auth.as_ref() else {
            return Vec::new();
        };
        match tool {
            "codex" => a.codex_accounts.clone(),
            "opencode" => a.open_code_accounts.clone(),
            "cursor" => a.cursor_accounts.clone(),
            "gemini" => a.gemini_accounts.clone(),
            _ => Vec::new(),
        }
    }

    /// tool_account_state is ToolAccountState: whom the chosen account is.
    fn tool_account_state(accounts: &[ToolAccount], chosen: &str) -> AnyElement {
        let on = if chosen.is_empty() {
            accounts.iter().find(|a| a.default)
        } else {
            accounts.iter().find(|a| a.name == chosen)
        };
        let (text, color) = match on {
            None if accounts.is_empty() => (t("project.accounts.notSignedIn"), theme::starting()),
            None => (
                tv("project.accounts.goneState", &[("account", chosen.into())]),
                theme::starting(),
            ),
            Some(a) if a.expired => (
                tv(
                    "project.accounts.expiredState",
                    &[("account", (&a.name).into())],
                ),
                theme::starting(),
            ),
            Some(a) => {
                let mut s = tv(
                    "project.accounts.signedIn",
                    &[("account", (&a.name).into())],
                );
                if !a.email.is_empty() {
                    s = format!("{s} · {}", a.email);
                }
                (s, theme::muted())
            }
        };
        note_in(text, color)
    }

    pub(super) fn project_entry(&self, e: &Entry, cx: &mut Context<Self>) -> Option<AnyElement> {
        if !e.id.starts_with("p-") {
            return None;
        }
        let p = self.project(&e.scope)?.clone();
        let pp = &self.settings_page.project;
        let s = self.settings_page.settings.as_ref();
        let auth = self.settings_page.accounts.auth.as_ref();
        let n = p.name.clone();
        let busy = pp.busy;
        let row: Row = match e.id {
            "p-name" => {
                let mut row = self.more_row(e, None, cx);
                row.code = Some(n.clone());
                let open = self.open_project_name().as_deref() == Some(n.as_str());
                if open {
                    let (a, b) = (n.clone(), n.clone());
                    let draft = self.field_value("project-name", cx);
                    let changed = draft.trim() != label_of(&p);
                    row.body.push(
                        h_flex()
                            .flex_wrap()
                            .gap_2()
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .max_w(px(384.))
                                    .child(self.input_field("project-name")),
                            )
                            .when(changed, |el| {
                                el.child(button(
                                    "project-name-save",
                                    t("common.save"),
                                    Btn::Primary,
                                    busy || draft.trim().is_empty(),
                                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        this.save_project_name(a.clone(), cx)
                                    }),
                                ))
                            })
                            .when(changed, |el| {
                                el.child(link(
                                    "project-name-cancel",
                                    t("common.cancel"),
                                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                                        if let Some(p) = this.project(&b).cloned()
                                            && let Some(i) =
                                                this.settings_page.fields.get("project-name")
                                        {
                                            i.update(cx, |i, cx| {
                                                i.set_value(label_of(&p), window, cx)
                                            });
                                        }
                                    }),
                                ))
                            })
                            .into_any_element(),
                    );
                } else {
                    row.control = Some(
                        div()
                            .text_size(theme::text::UI)
                            .text_color(theme::secondary())
                            .child(label_of(&p))
                            .into_any_element(),
                    );
                }
                if let Some(m) = pp.said.get(&n) {
                    row.body.push(note_in(m.clone(), theme::emerald(300)));
                }
                if let Some(m) = pp.errors.get(&n) {
                    row.body.push(note_in(m.clone(), theme::rose(300)));
                }
                row
            }
            "p-tool" => {
                let mut row = self.more_row(e, Some(t("defaults.project.toolDetails")), cx);
                let mut choices = vec![Choice::new("", t("defaults.project.toolNone"))];
                choices.extend(TOOLS.iter().map(|x| Choice::new(*x, ai_label(x))));
                row.control = Some(select(
                    &format!("p-tool-{n}"),
                    choices,
                    &p.agent_tool,
                    busy,
                    self.psave(
                        &n,
                        |v| UpdateProjectRequest {
                            agent_tool: Some(v.into()),
                            ..Default::default()
                        },
                        |p, _| {
                            Some(if p.agent_tool.is_empty() {
                                tv(
                                    "defaults.project.describeNoTool",
                                    &[("name", (&label_of(p)).into())],
                                )
                            } else {
                                tv(
                                    "defaults.project.describeTool",
                                    &[
                                        ("name", (&label_of(p)).into()),
                                        ("tool", ai_label(&p.agent_tool).into()),
                                    ],
                                )
                            })
                        },
                        cx,
                    ),
                ));
                row
            }
            "p-model" => {
                let tool = if p.agent_tool.is_empty() {
                    "claude"
                } else {
                    p.agent_tool.as_str()
                };
                let list: Vec<ChatOptionChoice> = s
                    .map(|s| match tool {
                        "opencode" => s.open_code_model_choices.clone(),
                        "cursor" => s.cursor_model_choices.clone(),
                        "gemini" => s.gemini_model_choices.clone(),
                        _ => s.claude_model_choices.clone(),
                    })
                    .unwrap_or_default()
                    .into_iter()
                    .filter(|c| c.value != "default")
                    .collect();
                let home = match tool {
                    "opencode" => t("defaults.project.homeOpenCode"),
                    "cursor" => s
                        .map(|s| s.default_cursor_model.clone())
                        .filter(|m| !m.is_empty())
                        .unwrap_or_else(|| t("defaults.project.homeCursor")),
                    "gemini" => s
                        .map(|s| s.default_gemini_model.clone())
                        .filter(|m| !m.is_empty())
                        .unwrap_or_else(|| t("defaults.project.homeCursor")),
                    _ => s
                        .map(|s| s.default_claude_model.clone())
                        .filter(|m| !m.is_empty())
                        .unwrap_or_else(|| t("defaults.project.homeFallback")),
                };
                let mut row = self.more_row(e, Some(t("defaults.project.modelDetails")), cx);
                if p.agent_model == AGENT_MODEL_AUTO {
                    row.description = t("defaults.project.autoDescription");
                }
                let mut choices = vec![
                    Choice::new("", t("defaults.project.sameAsSettings")).described(tv(
                        "defaults.project.currently",
                        &[("home", (&home).into())],
                    )),
                ];
                if tool == "claude" {
                    choices.push(
                        Choice::new(AGENT_MODEL_AUTO, t("defaults.project.autoName"))
                            .described(t("defaults.project.autoDescription")),
                    );
                }
                if !p.agent_model.is_empty()
                    && p.agent_model != AGENT_MODEL_AUTO
                    && !list.iter().any(|c| c.value == p.agent_model)
                {
                    choices.push(
                        Choice::new(p.agent_model.clone(), p.agent_model.clone())
                            .described(t("defaults.newAgent.offMenuNote")),
                    );
                }
                choices.extend(list.iter().map(|c| {
                    let mut ch = Choice::new(c.value.clone(), choice_label(c))
                        .described(c.description.clone());
                    ch.group = c.group.clone();
                    ch
                }));
                // AgentModelPicker: under the words, full width, Sparkles
                // before the choice.
                row.body.push(super::super::controls::field_select(
                    &format!("p-model-{n}"),
                    choices,
                    &p.agent_model,
                    busy || s.is_none(),
                    self.psave(
                        &n,
                        |v| UpdateProjectRequest {
                            agent_model: Some(v.into()),
                            ..Default::default()
                        },
                        |p, _| {
                            let name = label_of(p);
                            Some(if p.agent_model == AGENT_MODEL_AUTO {
                                tv("defaults.project.describeAuto", &[("name", (&name).into())])
                            } else if p.agent_model.is_empty() {
                                tv("defaults.project.describeHome", &[("name", (&name).into())])
                            } else {
                                tv(
                                    "defaults.project.describeModel",
                                    &[("name", (&name).into()), ("model", (&p.agent_model).into())],
                                )
                            })
                        },
                        cx,
                    ),
                    Anchor::TopLeft,
                    Some(("sparkles", theme::brand(300))),
                ));
                row
            }
            "p-effort" => {
                let cursor = p.agent_tool == "cursor";
                let levels: Vec<(String, String)> = if cursor {
                    let model = if p.agent_model.is_empty() {
                        s.map(|s| s.default_cursor_model.clone())
                            .unwrap_or_default()
                    } else {
                        p.agent_model.clone()
                    };
                    s.and_then(|s| s.cursor_model_choices.iter().find(|c| c.value == model))
                        .map(|c| c.efforts.iter().map(|l| (l.clone(), l.clone())).collect())
                        .unwrap_or_default()
                } else {
                    s.map(|s| {
                        s.claude_effort_choices
                            .iter()
                            .map(|c| (c.value.clone(), choice_label(c)))
                            .collect()
                    })
                    .unwrap_or_default()
                };
                let home = if cursor {
                    s.map(|s| s.default_cursor_effort.clone())
                        .filter(|v| !v.is_empty())
                        .unwrap_or_else(|| t("defaults.project.effortModelOwn"))
                } else {
                    s.map(|s| s.default_claude_effort.clone())
                        .filter(|v| !v.is_empty())
                        .unwrap_or_else(|| "medium".into())
                };
                let mut choices = vec![Choice::new(
                    "",
                    tv(
                        "defaults.project.sameAsSettingsValue",
                        &[("value", (&home).into())],
                    ),
                )];
                if !p.agent_effort.is_empty() && !levels.iter().any(|(v, _)| *v == p.agent_effort) {
                    choices.push(Choice::new(p.agent_effort.clone(), p.agent_effort.clone()));
                }
                choices.extend(levels.into_iter().map(|(v, l)| Choice::new(v, l)));
                let mut row = self.more_row(e, None, cx);
                row.control = Some(select(
                    &format!("p-effort-{n}"),
                    choices,
                    &p.agent_effort,
                    busy || s.is_none(),
                    self.psave(
                        &n,
                        |v| UpdateProjectRequest {
                            agent_effort: Some(v.into()),
                            ..Default::default()
                        },
                        |p, _| {
                            let name = label_of(p);
                            Some(if p.agent_effort.is_empty() {
                                tv(
                                    "defaults.project.describeEffortHome",
                                    &[("name", (&name).into())],
                                )
                            } else {
                                tv(
                                    "defaults.project.describeEffort",
                                    &[
                                        ("name", (&name).into()),
                                        ("effort", (&p.agent_effort).into()),
                                    ],
                                )
                            })
                        },
                        cx,
                    ),
                ));
                row
            }
            "p-window" => {
                let model = if !p.agent_model.is_empty() && p.agent_model != AGENT_MODEL_AUTO {
                    p.agent_model.clone()
                } else {
                    s.map(|s| s.default_claude_model.clone())
                        .filter(|m| !m.is_empty())
                        .unwrap_or_else(|| "opus".into())
                };
                let windows: Vec<i64> = s
                    .and_then(|s| s.claude_context_windows.get(&model).cloned())
                    .unwrap_or_default();
                let home = s
                    .map(|s| {
                        let n: i64 = s.default_agent_context_window.parse().unwrap_or(0);
                        format_tokens(if n > 0 { n } else { s.claude_compact_window })
                    })
                    .unwrap_or_default();
                let mut choices = vec![Choice::new(
                    "",
                    tv(
                        "defaults.project.sameAsSettingsValue",
                        &[("value", (&home).into())],
                    ),
                )];
                let cur: i64 = p.agent_context_window.parse().unwrap_or(0);
                if cur > 0 && !windows.contains(&cur) {
                    choices.push(Choice::new(
                        p.agent_context_window.clone(),
                        format_tokens(cur),
                    ));
                }
                choices.extend(
                    windows
                        .iter()
                        .map(|w| Choice::new(w.to_string(), format_tokens(*w))),
                );
                let mut row = self.more_row(e, None, cx);
                row.control = Some(select(
                    &format!("p-window-{n}"),
                    choices,
                    &p.agent_context_window,
                    busy || s.is_none(),
                    self.psave(
                        &n,
                        |v| UpdateProjectRequest {
                            agent_context_window: Some(v.into()),
                            ..Default::default()
                        },
                        |p, _| {
                            let name = label_of(p);
                            Some(if p.agent_context_window.is_empty() {
                                tv(
                                    "defaults.project.describeWindowHome",
                                    &[("name", (&name).into())],
                                )
                            } else {
                                let w = format_tokens(p.agent_context_window.parse().unwrap_or(0));
                                tv(
                                    "defaults.project.describeWindow",
                                    &[("name", (&name).into()), ("window", (&w).into())],
                                )
                            })
                        },
                        cx,
                    ),
                ));
                row
            }
            "p-mode" => {
                let mut choices = vec![Choice::new("", t("defaults.project.modeNone"))];
                choices.extend(
                    tool_rows(&p)
                        .modes
                        .iter()
                        .map(|m| Choice::new(*m, t(mode_key(m)))),
                );
                let mut row = self.more_row(e, None, cx);
                row.control = Some(select(
                    &format!("p-mode-{n}"),
                    choices,
                    &p.agent_mode,
                    busy,
                    self.psave(
                        &n,
                        |v| UpdateProjectRequest {
                            agent_mode: Some(v.into()),
                            ..Default::default()
                        },
                        |p, _| {
                            let name = label_of(p);
                            Some(if p.agent_mode.is_empty() {
                                tv(
                                    "defaults.project.describeModeHome",
                                    &[("name", (&name).into())],
                                )
                            } else {
                                tv(
                                    "defaults.project.describeMode",
                                    &[
                                        ("name", (&name).into()),
                                        ("mode", (&t(mode_key(&p.agent_mode))).into()),
                                    ],
                                )
                            })
                        },
                        cx,
                    ),
                ));
                row
            }
            "p-prefix" => {
                let mut row = self.more_row(e, Some(t("defaults.project.prefixDetails")), cx);
                if self.open_project_name().as_deref() == Some(n.as_str()) {
                    let a = n.clone();
                    let typed = self.field_value("project-prefix", cx);
                    row.body.push(
                        h_flex()
                            .gap_2()
                            .child(div().w(px(220.)).child(self.input_small("project-prefix")))
                            .child(button(
                                "project-prefix-save",
                                t("common.save"),
                                Btn::Secondary,
                                busy || typed == p.branch_prefix,
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.save_project_prefix(a.clone(), cx)
                                }),
                            ))
                            .into_any_element(),
                    );
                } else {
                    row.control = Some(mono(p.branch_prefix.clone()));
                }
                row
            }
            "p-claude-account" => {
                let accounts = auth.map(|a| a.claude_accounts.clone()).unwrap_or_default();
                let fallback = accounts.iter().find(|a| a.default).map(|a| a.name.clone());
                let mut row = self.more_row(
                    e,
                    (!accounts.is_empty()).then(|| t("project.accounts.claudeDetails")),
                    cx,
                );
                if auth.is_some() && accounts.is_empty() {
                    row.description = t("project.accounts.noneStored");
                }
                if !accounts.is_empty() {
                    row.control = Some(select(
                        &format!("p-claude-{n}"),
                        Self::account_choices(
                            accounts.iter().map(|a| a.name.clone()).collect(),
                            fallback.clone(),
                            &p.claude_account,
                        ),
                        &p.claude_account,
                        busy,
                        self.pick_account(
                            &n,
                            false,
                            p.claude_account.clone(),
                            fallback.clone(),
                            cx,
                        ),
                    ));
                }
                row
            }
            "p-claude-accounts" => {
                let accounts = auth.map(|a| a.claude_accounts.clone()).unwrap_or_default();
                let mut row = self.more_row(
                    e,
                    (accounts.len() >= 2).then(|| t("project.accounts.allowedDetails")),
                    cx,
                );
                if accounts.len() < 2 {
                    row.description = if auth.is_none() {
                        t("common.loading")
                    } else {
                        tv(
                            "project.accounts.onlyOne",
                            &[("count", accounts.len().into())],
                        )
                    };
                    return Some(row.render());
                }
                row.description = tv(
                    "project.accounts.allowedDescription",
                    &[
                        (
                            "state",
                            if p.claude_accounts.is_empty() {
                                "all"
                            } else {
                                "some"
                            }
                            .into(),
                        ),
                        ("allowed", p.claude_accounts.len().into()),
                        ("total", accounts.len().into()),
                    ],
                );
                let fallback = accounts.iter().find(|a| a.default).map(|a| a.name.clone());
                let effective = if p.claude_account.is_empty() {
                    fallback.unwrap_or_default()
                } else {
                    p.claude_account.clone()
                };
                let allowed = |name: &str| {
                    p.claude_accounts.is_empty() || p.claude_accounts.iter().any(|x| x == name)
                };
                let names: Vec<String> = accounts.iter().map(|a| a.name.clone()).collect();
                row.body.push(
                    h_flex()
                        .flex_wrap()
                        .gap_1p5()
                        .children(accounts.iter().map(|a| {
                            let on = allowed(&a.name);
                            let locked = a.name == effective;
                            let (name, all, project, current) = (
                                a.name.clone(),
                                names.clone(),
                                n.clone(),
                                p.claude_accounts.clone(),
                            );
                            div()
                                .id(SharedString::from(format!("p-allowed-{n}-{}", a.name)))
                                .px_2p5()
                                .py_0p5()
                                .rounded_full()
                                .border_1()
                                .text_size(theme::text::CAPTION)
                                .border_color(if on {
                                    theme::brand(400).opacity(0.5)
                                } else {
                                    theme::line()
                                })
                                .when(on, |el| {
                                    el.bg(theme::brand(500).opacity(0.15))
                                        .text_color(theme::primary())
                                })
                                .when(!on, |el| el.text_color(theme::subtle()).line_through())
                                .when(!locked && !busy, |el| {
                                    el.cursor_pointer().on_click(cx.listener(
                                        move |this, _: &ClickEvent, _, cx| {
                                            let mut now: Vec<String> = all
                                                .iter()
                                                .filter(|x| {
                                                    current.is_empty() || current.contains(x)
                                                })
                                                .cloned()
                                                .collect();
                                            if let Some(i) = now.iter().position(|x| *x == name) {
                                                now.remove(i);
                                            } else {
                                                now.push(name.clone());
                                            }
                                            if now.is_empty() {
                                                return;
                                            }
                                            let next = if now.len() == all.len() {
                                                Vec::new()
                                            } else {
                                                now
                                            };
                                            this.patch_project(
                                                project.clone(),
                                                UpdateProjectRequest {
                                                    claude_accounts: Some(next),
                                                    ..Default::default()
                                                },
                                                |_| None,
                                                cx,
                                            )
                                        },
                                    ))
                                })
                                .child(if a.name == effective {
                                    format!("{} {}", a.name, t("project.accounts.defaultChip"))
                                } else {
                                    a.name.clone()
                                })
                        }))
                        .into_any_element(),
                );
                if !effective.is_empty() && !allowed(&effective) {
                    row.body.push(note_in(
                        tv(
                            "project.accounts.notAllowed",
                            &[("account", (&effective).into())],
                        ),
                        theme::amber(300),
                    ));
                }
                row
            }
            "p-codex-account" | "p-cursor-account" | "p-gemini-account" => {
                let tool = &e.id[2..e.id.len() - "-account".len()];
                let accounts = self.tool_accounts(tool);
                let chosen = match tool {
                    "codex" => p.codex_account.clone(),
                    "cursor" => p.cursor_account.clone(),
                    _ => p.gemini_account.clone(),
                };
                let fallback = accounts.iter().find(|a| a.default).map(|a| a.name.clone());
                let mut row = self.more_row(
                    e,
                    (!accounts.is_empty()).then(|| t("project.accounts.toolDetails")),
                    cx,
                );
                if !accounts.is_empty() {
                    let set: fn(&str) -> UpdateProjectRequest = match tool {
                        "codex" => |v| UpdateProjectRequest {
                            codex_account: Some(v.into()),
                            ..Default::default()
                        },
                        "cursor" => |v| UpdateProjectRequest {
                            cursor_account: Some(v.into()),
                            ..Default::default()
                        },
                        _ => |v| UpdateProjectRequest {
                            gemini_account: Some(v.into()),
                            ..Default::default()
                        },
                    };
                    let tool_s = tool.to_string();
                    let name = n.clone();
                    row.control = Some(select(
                        &format!("{}-{n}", e.id),
                        Self::account_choices(
                            accounts.iter().map(|a| a.name.clone()).collect(),
                            fallback,
                            &chosen,
                        ),
                        &chosen,
                        busy,
                        Rc::new(cx.listener(move |this, v: &str, _, cx| {
                            let (tool, account) = (tool_s.clone(), v.to_string());
                            this.patch_project(
                                name.clone(),
                                set(v),
                                move |p| {
                                    Some(if account.is_empty() {
                                        tv(
                                            "project.accounts.toolDefault",
                                            &[
                                                ("tool", ai_label(&tool).into()),
                                                ("name", (&p.name).into()),
                                            ],
                                        )
                                    } else {
                                        tv(
                                            "project.accounts.toolSet",
                                            &[
                                                ("tool", ai_label(&tool).into()),
                                                ("name", (&p.name).into()),
                                                ("account", (&account).into()),
                                            ],
                                        )
                                    })
                                },
                                cx,
                            )
                        })),
                    ));
                }
                if auth.is_some() {
                    row.body.push(Self::tool_account_state(&accounts, &chosen));
                }
                row
            }
            "p-opencode-accounts" => {
                let accounts = self.tool_accounts("opencode");
                let mut providers: Vec<String> = accounts
                    .iter()
                    .map(|a| a.provider.clone())
                    .filter(|x| !x.is_empty())
                    .collect();
                providers.sort();
                providers.dedup();
                let mut row = self.more_row(
                    e,
                    (!providers.is_empty()).then(|| t("project.accounts.toolDetails")),
                    cx,
                );
                if providers.is_empty() && auth.is_some() {
                    row.body.push(Self::tool_account_state(&[], ""));
                }
                for provider in providers {
                    let mine: Vec<ToolAccount> = accounts
                        .iter()
                        .filter(|a| a.provider == provider)
                        .cloned()
                        .collect();
                    let chosen = p
                        .open_code_accounts
                        .get(&provider)
                        .cloned()
                        .unwrap_or_default();
                    let fallback = mine.iter().find(|a| a.default).map(|a| a.name.clone());
                    let (name, prov, all) =
                        (n.clone(), provider.clone(), p.open_code_accounts.clone());
                    row.body.push(
                        h_flex()
                            .gap_3()
                            .child(div().w(px(120.)).child(mono(provider.clone())))
                            .child(select(
                                &format!("p-opencode-{n}-{provider}"),
                                Self::account_choices(
                                    mine.iter().map(|a| a.name.clone()).collect(),
                                    fallback,
                                    &chosen,
                                ),
                                &chosen,
                                busy,
                                Rc::new(cx.listener(move |this, v: &str, _, cx| {
                                    let mut next: BTreeMap<String, String> = all.clone();
                                    if v.is_empty() {
                                        next.remove(&prov);
                                    } else {
                                        next.insert(prov.clone(), v.to_string());
                                    }
                                    this.patch_project(
                                        name.clone(),
                                        UpdateProjectRequest {
                                            open_code_accounts: Some(next),
                                            ..Default::default()
                                        },
                                        |p| {
                                            Some(tv(
                                                "project.accounts.openCodeSet",
                                                &[("name", (&p.name).into())],
                                            ))
                                        },
                                        cx,
                                    )
                                })),
                            ))
                            .child(
                                div()
                                    .flex_1()
                                    .child(Self::tool_account_state(&mine, &chosen)),
                            )
                            .into_any_element(),
                    );
                }
                row
            }
            "p-credentials" => {
                let mut row = self.more_row(e, None, cx);
                match pp.credentials.get(&n) {
                    None => row.body.push(note(t("common.loading"))),
                    Some(Err(err)) => row.body.push(note_in(err.clone(), theme::rose(300))),
                    Some(Ok(list)) if list.is_empty() => {
                        row.body.push(note(t("project.accounts.runningOnNone")))
                    }
                    Some(Ok(list)) => {
                        let open = pp.show_credentials;
                        let (summary, warn) = credentials_summary(list);
                        row.body.push(
                            div()
                                .id(SharedString::from(format!("p-creds-{n}")))
                                .text_xs()
                                .cursor_pointer()
                                .text_color(if warn {
                                    theme::starting()
                                } else {
                                    theme::muted()
                                })
                                .hover(|st| st.text_color(theme::primary()))
                                .active(|s| s.opacity(0.7))
                                .flex()
                                .items_center()
                                .gap_1()
                                .child(crate::motion::chevron("chev", open, Some(px(12.))))
                                .child(summary)
                                .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                    let pp = &mut this.settings_page.project;
                                    pp.show_credentials = !pp.show_credentials;
                                    cx.notify();
                                }))
                                .into_any_element(),
                        );
                        if open {
                            row.body.push(
                                v_flex()
                                    .gap_1()
                                    .pl_4()
                                    .children(list.iter().map(|c| {
                                        let (text, color) = credential_text(c);
                                        h_flex()
                                            .gap_3()
                                            .justify_between()
                                            .child(mono(format!(
                                                "{} · {}",
                                                c.agent.split('/').nth(1).unwrap_or(&c.agent),
                                                ai_label(&c.tool)
                                            )))
                                            .child(
                                                div()
                                                    .text_xs()
                                                    .text_color(color)
                                                    .truncate()
                                                    .child(text),
                                            )
                                    }))
                                    .into_any_element(),
                            );
                        }
                    }
                }
                row
            }
            "p-github-account" => {
                let accounts = auth.map(|a| a.github_accounts.clone()).unwrap_or_default();
                let fallback = accounts.iter().find(|a| a.default).map(|a| a.name.clone());
                let mut row = self.more_row(
                    e,
                    (!accounts.is_empty()).then(|| t("project.accounts.githubDetails")),
                    cx,
                );
                if auth.is_some() && accounts.is_empty() {
                    row.description = t("project.accounts.noneStored");
                }
                if !accounts.is_empty() {
                    row.control = Some(select(
                        &format!("p-github-{n}"),
                        Self::account_choices(
                            accounts.iter().map(|a| a.name.clone()).collect(),
                            fallback.clone(),
                            &p.github_account,
                        ),
                        &p.github_account,
                        busy,
                        self.pick_account(&n, true, p.github_account.clone(), fallback.clone(), cx),
                    ));
                }
                row
            }
            "p-sync-base" => {
                let mut row = self.pswitch(
                    e,
                    p.sync_base,
                    Some(if p.sync_base {
                        t("defaults.project.syncOnDetails")
                    } else {
                        t("defaults.project.syncOffDetails")
                    }),
                    |v| UpdateProjectRequest {
                        sync_base: Some(v),
                        ..Default::default()
                    },
                    |p| {
                        let k = if p.sync_base {
                            "defaults.project.syncOnToast"
                        } else {
                            "defaults.project.syncOffToast"
                        };
                        tv(k, &[("name", (&p.name).into())])
                    },
                    cx,
                );
                row.description = if p.sync_base {
                    t("defaults.project.syncOnDescription")
                } else {
                    t("defaults.project.syncOffDescription")
                };
                row
            }
            "p-pulls-tab" => {
                let mut row = self.pswitch(
                    e,
                    p.pull_requests,
                    Some(t("defaults.project.pullsTabDetails")),
                    |v| UpdateProjectRequest {
                        pull_requests: Some(v),
                        ..Default::default()
                    },
                    |p| {
                        let k = if p.pull_requests {
                            "defaults.project.pullsTabOnToast"
                        } else {
                            "defaults.project.pullsTabOffToast"
                        };
                        tv(k, &[("name", (&p.name).into())])
                    },
                    cx,
                );
                row.description = if p.pull_requests {
                    t("defaults.project.pullsTabOnDescription")
                } else {
                    t("defaults.project.pullsTabOffDescription")
                };
                row
            }
            "p-agent-prs" => {
                let mut row = self.pswitch(
                    e,
                    p.agent_p_rs,
                    Some(if p.agent_p_rs {
                        t("defaults.project.agentPRsOnDetails")
                    } else {
                        t("defaults.project.agentPRsOffDetails")
                    }),
                    |v| UpdateProjectRequest {
                        agent_p_rs: Some(v),
                        ..Default::default()
                    },
                    |p| {
                        let k = if p.agent_p_rs {
                            "defaults.project.agentPRsOnToast"
                        } else {
                            "defaults.project.agentPRsOffToast"
                        };
                        tv(k, &[("name", (&p.name).into())])
                    },
                    cx,
                );
                row.description = if p.agent_p_rs {
                    t("defaults.project.agentPRsOnDescription")
                } else {
                    t("defaults.project.agentPRsOffDescription")
                };
                row
            }
            "p-pick-up" => {
                let mut row = self.pswitch(
                    e,
                    p.pick_up_issues,
                    Some(t("defaults.project.pickUpDetails")),
                    |v| UpdateProjectRequest {
                        pick_up_issues: Some(v),
                        ..Default::default()
                    },
                    |p| {
                        let k = if p.pick_up_issues {
                            "defaults.project.pickUpOnToast"
                        } else {
                            "defaults.project.pickUpOffToast"
                        };
                        tv(k, &[("name", (&p.name).into())])
                    },
                    cx,
                );
                row.description = if p.pick_up_issues {
                    t("defaults.project.pickUpOnDescription")
                } else {
                    t("defaults.project.pickUpOffDescription")
                };
                row
            }
            "p-pr-watch" => {
                let mut row = self.more_row(e, Some(t("defaults.project.prWatchDetails")), cx);
                let home = match s {
                    Some(s) => tv(
                        "defaults.project.asInSettingsState",
                        &[("on", if s.pr_watch { "yes" } else { "no" }.into())],
                    ),
                    None => t("defaults.project.asInSettings"),
                };
                row.control = Some(select(
                    &format!("p-prwatch-{n}"),
                    vec![
                        Choice::new("", home),
                        Choice::new("on", t("defaults.project.onForProject")),
                        Choice::new("off", t("defaults.project.offForProject")),
                    ],
                    &p.pr_watch,
                    busy,
                    self.psave(
                        &n,
                        |v| UpdateProjectRequest {
                            pr_watch: Some(v.into()),
                            ..Default::default()
                        },
                        |p, _| {
                            let k = if p.pr_watching {
                                "defaults.project.prWatchOnToast"
                            } else {
                                "defaults.project.prWatchOffToast"
                            };
                            Some(tv(k, &[("name", (&p.name).into())]))
                        },
                        cx,
                    ),
                ));
                row
            }
            "p-autonomy" => {
                let on = p.autonomy == "on";
                let mut row = self.more_row(e, Some(t("defaults.project.autonomyDetails")), cx);
                row.description = if on {
                    t("defaults.project.autonomyOnDescription")
                } else {
                    t("defaults.project.autonomyOffDescription")
                };
                let name = n.clone();
                row.control = Some(toggle(
                    SharedString::from(format!("switch-autonomy-{n}")),
                    on,
                    busy,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.patch_project(
                            name.clone(),
                            UpdateProjectRequest {
                                autonomy: Some(if on { "ask" } else { "on" }.into()),
                                ..Default::default()
                            },
                            |p| {
                                let k = if p.autonomy == "on" {
                                    "defaults.project.autonomyOnToast"
                                } else {
                                    "defaults.project.autonomyOffToast"
                                };
                                Some(tv(k, &[("name", (&p.name).into())]))
                            },
                            cx,
                        )
                    }),
                ));
                row
            }
            "p-finish" => {
                let mut row = self.more_row(e, Some(t("defaults.project.finishDetails")), cx);
                row.control = Some(select(
                    &format!("p-finish-{n}"),
                    vec![
                        Choice::new("lead", t("defaults.project.finishLead")),
                        Choice::new("chat", t("defaults.project.finishChat")),
                        Choice::new("off", t("defaults.project.finishOff")),
                    ],
                    &p.finish_notices,
                    busy,
                    self.psave(
                        &n,
                        |v| UpdateProjectRequest {
                            finish_notices: Some(v.into()),
                            ..Default::default()
                        },
                        |p, _| {
                            let k = match p.finish_notices.as_str() {
                                "off" => "defaults.project.finishOffToast",
                                "chat" => "defaults.project.finishChatToast",
                                _ => "defaults.project.finishLeadToast",
                            };
                            Some(tv(k, &[("name", (&p.name).into())]))
                        },
                        cx,
                    ),
                ));
                row
            }
            "p-settle" => {
                let mut values = SETTLE.to_vec();
                if !values.contains(&p.settle_hours) {
                    values.push(p.settle_hours);
                }
                let mut row = self.more_row(e, Some(t("defaults.project.settleDetails")), cx);
                row.control = Some(select(
                    &format!("p-settle-{n}"),
                    values
                        .into_iter()
                        .map(|h| {
                            Choice::new(
                                h.to_string(),
                                if h == 0 {
                                    t("defaults.project.settleNever")
                                } else {
                                    tv("defaults.project.settleAfter", &[("hours", h.into())])
                                },
                            )
                        })
                        .collect(),
                    &p.settle_hours.to_string(),
                    busy,
                    self.psave(
                        &n,
                        |v| UpdateProjectRequest {
                            settle_hours: v.parse().ok(),
                            ..Default::default()
                        },
                        |_, _| None,
                        cx,
                    ),
                ));
                row
            }
            "p-full-suite" => {
                let mut row = self.pswitch(
                    e,
                    p.full_test_suite,
                    Some(t("defaults.project.fullSuiteDetails")),
                    |v| UpdateProjectRequest {
                        full_test_suite: Some(v),
                        ..Default::default()
                    },
                    |p| {
                        let k = if p.full_test_suite {
                            "defaults.project.fullSuiteOnToast"
                        } else {
                            "defaults.project.fullSuiteOffToast"
                        };
                        tv(k, &[("name", (&p.name).into())])
                    },
                    cx,
                );
                row.description = if p.full_test_suite {
                    t("defaults.project.fullSuiteOnDescription")
                } else {
                    t("defaults.project.fullSuiteOffDescription")
                };
                if s.is_some_and(|s| !s.block_full_suite) {
                    row.body.push(
                        h_flex()
                            .gap_1()
                            .child(note(t("defaults.project.fullSuiteInactive")))
                            .child(link(
                                SharedString::from(format!("p-open-experimental-{n}")),
                                t("defaults.project.fullSuiteOpenExperimental"),
                                cx.listener(|this, _: &ClickEvent, window, cx| {
                                    this.open_settings_section("experimental".into(), window, cx)
                                }),
                            ))
                            .into_any_element(),
                    );
                }
                row
            }
            "p-nesting" => {
                let has_incus = self
                    .settings_page
                    .accounts
                    .setup
                    .as_ref()
                    .is_some_and(|s| s.image.installed.incus || s.image.components.incus);
                let mut row = self.pswitch(
                    e,
                    p.nesting,
                    Some(t("defaults.project.nestingDetails")),
                    |v| UpdateProjectRequest {
                        nesting: Some(v),
                        ..Default::default()
                    },
                    |p| {
                        let k = if p.nesting {
                            "defaults.project.nestingOnToast"
                        } else {
                            "defaults.project.nestingOffToast"
                        };
                        tv(k, &[("name", (&p.name).into())])
                    },
                    cx,
                );
                if !has_incus && !p.nesting {
                    row.description = t("defaults.project.nestingNeedsIncus");
                }
                row
            }
            _ => return None,
        };
        Some(row.render())
    }

    pub(super) fn save_secret(
        &mut self,
        project: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let name = self.field_value("secret-name", cx);
        let value = self
            .settings_page
            .fields
            .get("secret-value")
            .map(|i| i.read(cx).value().to_string())
            .unwrap_or_default();
        if name.is_empty() || value.is_empty() {
            return;
        }
        let body = serde_json::to_vec(&SetSecretRequest {
            value,
            lead_managed: None,
        })
        .ok();
        let client = self.client.clone();
        self.settings_page.project.busy = true;
        cx.spawn_in(window, async move |this, cx| {
            let res = client
                .raw(
                    Method::PUT,
                    &format!("/v1/projects/{}/secrets/{}", enc(&project), enc(&name)),
                    body,
                )
                .await
                .and_then(|b| Ok(serde_json::from_slice::<Secret>(&b)?));
            this.update_in(cx, |this, window, cx| {
                let pp = &mut this.settings_page.project;
                pp.busy = false;
                match res {
                    Ok(s) => {
                        pp.errors.remove(&project);
                        let stored = tv("agent.secrets.stored", &[("name", (&s.name).into())]);
                        let next = if s.agents.is_empty() {
                            t("agent.secrets.nextAgents")
                        } else {
                            tv(
                                "agent.secrets.writtenInto",
                                &[("agents", (&s.agents.join(", ")).into())],
                            )
                        };
                        pp.said.insert(project.clone(), format!("{stored}. {next}"));
                        // The value only ever goes in: the form starts over.
                        this.clear_field("secret-name", window, cx);
                        this.clear_field("secret-value", window, cx);
                    }
                    Err(e) => {
                        pp.errors.insert(project.clone(), e.to_string());
                    }
                }
                this.load_secrets(project, cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    #[allow(clippy::type_complexity)]
    fn secret_call(
        &mut self,
        project: String,
        method: Method,
        rest: String,
        body: Option<Vec<u8>>,
        said: Option<Box<dyn FnOnce(&[u8]) -> String>>,
        cx: &mut Context<Self>,
    ) {
        let client = self.client.clone();
        self.settings_page.project.busy = true;
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(
                    method,
                    &format!("/v1/projects/{}/secrets{rest}", enc(&project)),
                    body,
                )
                .await;
            this.update(cx, |this, cx| {
                let pp = &mut this.settings_page.project;
                pp.busy = false;
                match res {
                    Ok(b) => {
                        pp.errors.remove(&project);
                        if let Some(f) = said {
                            pp.said.insert(project.clone(), f(&b));
                        }
                    }
                    Err(e) => {
                        pp.errors.insert(project.clone(), e.to_string());
                    }
                }
                this.load_secrets(project, cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    pub(super) fn secrets_card(&self, project: &str, cx: &mut Context<Self>) -> AnyElement {
        let pp = &self.settings_page.project;
        let open = self.open_project_name().as_deref() == Some(project);
        let list = pp.secrets.get(project);
        let (a, b) = (project.to_string(), project.to_string());
        let ready = !self.field_value("secret-name", cx).is_empty()
            && self
                .settings_page
                .fields
                .get("secret-value")
                .is_some_and(|i| !i.read(cx).value().is_empty());
        v_flex()
            .id(SharedString::from(format!("secrets-{project}")))
            .gap_3()
            .when_some(pp.errors.get(project), |el, e| {
                el.child(note_in(e.clone(), theme::rose(300)))
            })
            .when_some(pp.said.get(project), |el, s| {
                el.child(note_in(s.clone(), theme::emerald(300)))
            })
            .when(open, |el| {
                el.child(
                    card("secret-form")
                        .gap_3()
                        .child(title(t("agent.secrets.giveProject")))
                        .child(note(t("agent.secrets.giveProjectDescription")))
                        .child(
                            h_flex()
                                .gap_3()
                                .items_start()
                                .child(labeled(
                                    t("agent.secrets.name"),
                                    v_flex()
                                        .gap_1()
                                        .child(self.input_small("secret-name"))
                                        .child(note(t("agent.secrets.nameHint"))),
                                ))
                                .child(labeled(
                                    t("agent.secrets.value"),
                                    v_flex()
                                        .gap_1()
                                        .child(self.input_small("secret-value"))
                                        .child(note(t("agent.secrets.valueHint"))),
                                )),
                        )
                        .child(
                            h_flex()
                                .gap_3()
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .child(note(t("agent.secrets.note"))),
                                )
                                .child(button(
                                    "secret-store",
                                    t("agent.secrets.store"),
                                    Btn::Primary,
                                    !ready || pp.busy,
                                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                                        this.save_secret(a.clone(), window, cx)
                                    }),
                                )),
                        ),
                )
            })
            .child(match list {
                None => super::super::panel::loading("secrets"),
                Some(Err(e)) => note_in(e.clone(), theme::rose(300)),
                Some(Ok(l)) if l.is_empty() => note(t("agent.secrets.none")),
                Some(Ok(l)) => v_flex()
                    .gap_2()
                    .children(l.iter().map(|s| self.secret_row(&b, s, cx)))
                    .into_any_element(),
            })
            .into_any_element()
    }

    fn secret_row(&self, project: &str, s: &Secret, cx: &mut Context<Self>) -> AnyElement {
        let pp = &self.settings_page.project;
        let where_ = if s.agents.is_empty() {
            t("agent.secrets.inNone")
        } else if s.scope == "agent" {
            t("agent.secrets.inThis")
        } else if s.agents.len() == 1 {
            tv(
                "agent.secrets.inOne",
                &[(
                    "agent",
                    s.agents[0].split('/').nth(1).unwrap_or(&s.agents[0]).into(),
                )],
            )
        } else {
            tv("agent.secrets.inMany", &[("count", s.agents.len().into())])
        };
        let removing = pp
            .removing
            .as_ref()
            .is_some_and(|(p, n)| p == project && *n == s.name);
        let (lp, ln, managed) = (project.to_string(), s.name.clone(), s.lead_managed);
        let (rp, rn) = (project.to_string(), s.name.clone());
        v_flex()
            .id(SharedString::from(format!("secret-{project}-{}", s.name)))
            .gap_2()
            .px_3()
            .py_2p5()
            .rounded_xl()
            .border_1()
            .border_color(theme::line_faint())
            .bg(theme::surface_faint())
            .hover(|st| st.border_color(theme::line_strong()))
            .child(
                h_flex()
                    .flex_wrap()
                    .gap_3()
                    .child(
                        div()
                            .font_family(crate::theme::mono())
                            .text_size(theme::text::UI)
                            .text_color(theme::primary())
                            .child(format!("${}", s.name)),
                    )
                    .child(badge(
                        if s.scope == "project" {
                            t("agent.secrets.scopeProject")
                        } else {
                            t("agent.secrets.scopeAgent")
                        },
                        theme::accent(),
                    ))
                    .child(note(tv(
                        "agent.secrets.updated",
                        &[("when", (&time_ago(&s.updated_at)).into())],
                    )))
                    .child(note(where_))
                    .when(!s.warning.is_empty(), |el| {
                        el.child(badge(t("agent.secrets.forCodeOnly"), theme::starting()))
                    })
                    .child(div().flex_1())
                    .when(s.scope == "project", |el| {
                        el.child(
                            h_flex()
                                .gap_2()
                                .child(note(t("agent.secrets.leadManaged")))
                                .child(toggle(
                                    SharedString::from(format!("secret-lead-{project}-{}", s.name)),
                                    managed,
                                    pp.busy,
                                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        let body = serde_json::to_vec(&SecretSettingsRequest {
                                            lead_managed: !managed,
                                        })
                                        .ok();
                                        this.secret_call(
                                            lp.clone(),
                                            Method::PATCH,
                                            format!("/{}", enc(&ln)),
                                            body,
                                            Some(Box::new(|b: &[u8]| {
                                                let s: Secret =
                                                    serde_json::from_slice(b).unwrap_or_default();
                                                let k = if s.lead_managed {
                                                    "agent.secrets.leadManagedOn"
                                                } else {
                                                    "agent.secrets.leadManagedOff"
                                                };
                                                tv(k, &[("name", (&s.name).into())])
                                            })),
                                            cx,
                                        )
                                    }),
                                )),
                        )
                    })
                    .child(button(
                        SharedString::from(format!("secret-remove-{project}-{}", s.name)),
                        t("common.remove"),
                        Btn::Ghost,
                        false,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.settings_page.project.removing = Some((rp.clone(), rn.clone()));
                            cx.notify();
                        }),
                    )),
            )
            .when(removing, |el| {
                let (p, n) = (project.to_string(), s.name.clone());
                let label = self
                    .project(project)
                    .map(label_of)
                    .unwrap_or_else(|| project.to_string());
                el.child(ask(
                    "secret-remove",
                    tv("agent.secrets.removeTitle", &[("name", (&s.name).into())]),
                    tv(
                        "agent.secrets.removeProject",
                        &[("project", (&label).into())],
                    ),
                    t("common.remove"),
                    true,
                    pp.busy,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        let name = n.clone();
                        this.settings_page.project.removing = None;
                        this.secret_call(
                            p.clone(),
                            Method::DELETE,
                            format!("/{}", enc(&name)),
                            None,
                            Some(Box::new(move |_: &[u8]| {
                                tv("agent.secrets.removed", &[("name", (&name).into())])
                            })),
                            cx,
                        )
                    }),
                    cx.listener(|this, _: &ClickEvent, _, cx| {
                        this.settings_page.project.removing = None;
                        cx.notify();
                    }),
                ))
            })
            .into_any_element()
    }
}
