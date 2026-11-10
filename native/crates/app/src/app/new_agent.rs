//! Start an agent yourself (NewAgentDialog.tsx): what it works on and its
//! branch, the project, the AI tool and how you work with it, and under More
//! options its name, the branch it starts from, its model, effort and context
//! window, the Claude and GitHub accounts, what it does to the project's chat
//! when it finishes, autonomy and a clean start. Sent, the create job's log
//! shows until it's done (cancel rolls it back), then the agent opens.
use std::rc::Rc;
use std::time::Duration;

use agentbox_api::client::Method;
use agentbox_api::{
    Agent, AgentPick, AuthStatus, Base, ChatOptionChoice, CreateAgentRequest, ImageStatus, Job,
    Project, Settings, SetupStatus, UpdateSettingsRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::add_project::{field, form_modal, job_log};
use super::agent::OnCancel;
use super::controls::{self, Btn, Choice};
use super::settings::defaults::format_tokens;
use super::settings::kit::plain;
use super::sidebar_actions::label_of;
use super::sidebar_dialogs::Dialog;
use crate::chat::tool_label;
use crate::{theme, toasts};

const MAX_SLUG: usize = 48;

/// Click is a click handler kept to hand on.
type Click = Box<dyn Fn(&ClickEvent, &mut Window, &mut App)>;

pub struct NewAgent {
    project: String,
    title: Entity<InputState>,
    branch: Entity<InputState>,
    branch_touched: bool,
    name: Entity<InputState>,
    from: Entity<InputState>,
    ai: String,
    iface: &'static str,
    autonomous: bool,
    model: String,
    effort: String,
    window: String,
    clean: bool,
    claude_account: String,
    github_account: String,
    notify: String,
    advanced: bool,
    /// The project the tool, model and effort were last started on.
    started_for: String,
    auth: Option<AuthStatus>,
    settings: Option<Settings>,
    image: Option<ImageStatus>,
    setup: Option<SetupStatus>,
    base: Option<(String, Base)>,
    busy: bool,
    error: Option<String>,
    job: Option<Job>,
    log: Vec<String>,
    _subs: Vec<Subscription>,
}

/// branch_slug turns a title into a branch slug (lib/branch.ts).
pub fn branch_slug(title: &str) -> String {
    let line = title.trim().lines().next().unwrap_or("").to_lowercase();
    let mut slug = String::new();
    for c in line.chars() {
        if c.is_ascii_lowercase() || c.is_ascii_digit() {
            slug.push(c);
        } else if !slug.ends_with('-') {
            slug.push('-');
        }
    }
    let slug = slug.trim_matches('-').to_string();
    if slug.len() <= MAX_SLUG {
        return slug;
    }
    let cut = &slug[..=MAX_SLUG];
    match cut.rfind('-') {
        Some(i) if i > 0 => cut[..i].to_string(),
        _ => slug[..MAX_SLUG].trim_end_matches('-').to_string(),
    }
}

/// project_defaults is what a project names for its agents on a tool.
fn project_defaults(p: &Project, ai: &str) -> (String, String, String) {
    let tool = if p.agent_tool.is_empty() {
        "claude"
    } else {
        &p.agent_tool
    };
    if tool != ai {
        return Default::default();
    }
    let model = if p.agent_model == "auto" {
        String::new()
    } else {
        p.agent_model.clone()
    };
    (
        model,
        p.agent_effort.clone(),
        p.agent_context_window.clone(),
    )
}

/// setting_label names what an unchosen model or effort falls back to.
fn setting_label(choices: &[ChatOptionChoice], value: &str, own: &str) -> String {
    if value.is_empty() {
        return own.to_string();
    }
    choices
        .iter()
        .find(|c| c.value == value)
        .map(|c| {
            if c.name.is_empty() {
                c.value.clone()
            } else {
                c.name.clone()
            }
        })
        .unwrap_or_else(|| value.to_string())
}

fn choices_of(list: &[ChatOptionChoice]) -> Vec<Choice> {
    list.iter()
        .map(|c| {
            Choice::new(
                c.value.clone(),
                if c.name.is_empty() {
                    c.value.clone()
                } else {
                    c.name.clone()
                },
            )
        })
        .collect()
}

impl NewAgent {
    fn selected<'a>(&self, projects: &'a [Project]) -> Option<&'a Project> {
        projects.iter().find(|p| p.name == self.project)
    }
    fn iface(&self) -> &'static str {
        if self.ai == "cursor" {
            "chat"
        } else {
            self.iface
        }
    }
    fn defaults(&self, projects: &[Project]) -> (String, String, String) {
        self.selected(projects)
            .map(|p| project_defaults(p, &self.ai))
            .unwrap_or_default()
    }
    fn cursor_model(&self, projects: &[Project]) -> String {
        let (m, _, _) = self.defaults(projects);
        [
            self.model.clone(),
            m,
            self.settings
                .as_ref()
                .map(|s| s.default_cursor_model.clone())
                .unwrap_or_default(),
        ]
        .into_iter()
        .find(|v| !v.is_empty())
        .unwrap_or_default()
    }
    fn cursor_efforts(&self, projects: &[Project]) -> Vec<String> {
        let m = self.cursor_model(projects);
        self.settings
            .as_ref()
            .and_then(|s| s.cursor_model_choices.iter().find(|c| c.value == m))
            .map(|c| c.efforts.clone())
            .unwrap_or_default()
    }
    /// windows is the context windows the model it runs on has.
    fn windows(&self, projects: &[Project]) -> Vec<i64> {
        let (m, _, _) = self.defaults(projects);
        let runs = [
            self.model.clone(),
            m,
            self.settings
                .as_ref()
                .map(|s| s.default_claude_model.clone())
                .unwrap_or_default(),
        ]
        .into_iter()
        .find(|v| !v.is_empty())
        .unwrap_or_else(|| "opus".into());
        self.settings
            .as_ref()
            .and_then(|s| s.claude_context_windows.get(&runs).cloned())
            .unwrap_or_default()
    }
}

impl AppView {
    fn new_agent_dialog(&mut self) -> Option<&mut NewAgent> {
        match &mut self.side.dialog {
            Some(Dialog::NewAgent(d)) => Some(d),
            _ => None,
        }
    }

    pub(super) fn open_new_agent(
        &mut self,
        project: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let input = |p: String, window: &mut Window, cx: &mut Context<Self>| {
            cx.new(|cx| InputState::new(window, cx).placeholder(p))
        };
        let title = input(t("project.newAgent.workPlaceholder"), window, cx);
        let branch = input("agent-NN".into(), window, cx);
        let name = input("agent-NN".into(), window, cx);
        let from = input("main".into(), window, cx);
        title.update(cx, |s, cx| s.focus(window, cx));
        let subs = vec![
            cx.subscribe_in(
                &title,
                window,
                |this, title, ev: &InputEvent, window, cx| {
                    if let InputEvent::Change = ev {
                        let slug = branch_slug(title.read(cx).value().as_ref());
                        if let Some(d) = this.new_agent_dialog()
                            && !d.branch_touched
                        {
                            d.branch.update(cx, |s, cx| s.set_value(slug, window, cx));
                        }
                    }
                    if let InputEvent::PressEnter { .. } = ev {
                        this.create_agent(cx);
                    }
                },
            ),
            cx.subscribe(&branch, |this, branch, ev: &InputEvent, cx| {
                if let InputEvent::Change = ev
                    && let Some(d) = this.new_agent_dialog()
                {
                    let typed = branch.read(cx).value().to_string();
                    if typed != branch_slug(d.title.read(cx).value().as_ref()) {
                        d.branch_touched = true;
                    }
                }
            }),
        ];
        let project = if project.is_empty() || project == agentbox_api::HOME_PROJECT {
            self.projects
                .first()
                .map(|p| p.name.clone())
                .unwrap_or_default()
        } else {
            project
        };
        self.side.dialog = Some(Dialog::NewAgent(Box::new(NewAgent {
            project,
            title,
            branch,
            branch_touched: false,
            name,
            from,
            ai: "claude".into(),
            iface: "chat",
            autonomous: true,
            model: String::new(),
            effort: String::new(),
            window: String::new(),
            clean: false,
            claude_account: String::new(),
            github_account: String::new(),
            notify: String::new(),
            advanced: false,
            started_for: String::new(),
            auth: None,
            settings: None,
            image: None,
            setup: None,
            base: None,
            busy: false,
            error: None,
            job: None,
            log: Vec::new(),
            _subs: subs,
        })));
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let auth = client.get::<AuthStatus>("/v1/auth").await.ok();
            let settings = client.get::<Settings>("/v1/settings").await.ok();
            let image = client.get::<ImageStatus>("/v1/image").await.ok();
            let setup = client.get::<SetupStatus>("/v1/setup").await.ok();
            this.update(cx, |this, cx| {
                if let Some(d) = this.new_agent_dialog() {
                    d.auth = auth;
                    d.settings = settings;
                    d.image = image;
                    d.setup = setup;
                }
                this.start_pick(cx);
            })
            .ok();
        })
        .detach();
    }

    /// start_pick puts the form on the project's tool, else the last pick,
    /// else Claude Code, once per project (agentPick.ts startingPick); and
    /// reads the project's saved base.
    fn start_pick(&mut self, cx: &mut Context<Self>) {
        let projects = self.projects.clone();
        let client = self.client.clone();
        let Some(d) = self.new_agent_dialog() else {
            return;
        };
        let Some(p) = d.selected(&projects).cloned() else {
            return;
        };
        if d.settings.is_none() || d.started_for == p.name {
            return;
        }
        d.started_for = p.name.clone();
        let last = d
            .settings
            .as_ref()
            .map(|s| s.last_agent_pick.clone())
            .unwrap_or_default();
        let (ai, model, effort) = if !p.agent_tool.is_empty() {
            (p.agent_tool.clone(), String::new(), String::new())
        } else if !last.ai.is_empty() {
            (last.ai, last.model, last.effort)
        } else {
            ("claude".into(), String::new(), String::new())
        };
        d.ai = ai;
        d.model = model;
        d.effort = effort;
        d.base = None;
        let name = p.name.clone();
        cx.notify();
        cx.spawn(async move |this, cx| {
            let base = client
                .get::<Base>(&format!("/v1/projects/{name}/base"))
                .await
                .ok();
            this.update(cx, |this, cx| {
                if let Some(d) = this.new_agent_dialog()
                    && d.project == name
                {
                    d.base = base
                        .filter(|b| !b.snapshot.is_empty())
                        .map(|b| (name.clone(), b));
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn create_agent(&mut self, cx: &mut Context<Self>) {
        let projects = self.projects.clone();
        let client = self.client.clone();
        let Some(d) = self.new_agent_dialog() else {
            return;
        };
        if d.project.is_empty() || d.busy || d.job.is_some() {
            return;
        }
        let ai = d.ai.clone();
        let cursor_effort = {
            let e = d.cursor_efforts(&projects);
            if e.contains(&d.effort) {
                d.effort.clone()
            } else {
                String::new()
            }
        };
        let windows = d.windows(&projects);
        let window = d
            .window
            .parse::<i64>()
            .ok()
            .filter(|w| windows.contains(w))
            .map(|w| w.to_string());
        let some = |s: &String| (!s.is_empty()).then(|| s.clone());
        let req = CreateAgentRequest {
            project: d.project.clone(),
            title: d.title.read(cx).value().trim().to_string(),
            name: d.name.read(cx).value().trim().to_string(),
            branch: d.branch.read(cx).value().trim().to_string(),
            ai: ai.clone(),
            interface: if ai == "none" {
                String::new()
            } else {
                d.iface().to_string()
            },
            autonomous: Some(d.autonomous),
            model: if ["claude", "opencode", "cursor", "gemini"].contains(&ai.as_str()) {
                some(&d.model)
            } else {
                None
            },
            effort: match ai.as_str() {
                "claude" => some(&d.effort),
                "cursor" => some(&cursor_effort),
                _ => None,
            },
            context_window: if ai == "claude" { window } else { None },
            from: d.from.read(cx).value().trim().to_string(),
            clean: d.clean,
            claude_account: if ai == "claude" {
                d.claude_account.clone()
            } else {
                String::new()
            },
            github_account: d.github_account.clone(),
            finish_notice: d.notify.clone(),
            ..Default::default()
        };
        let pick = AgentPick {
            ai: ai.clone(),
            model: if ai == "none" || ai == "codex" {
                String::new()
            } else {
                d.model.clone()
            },
            effort: match ai.as_str() {
                "claude" => d.effort.clone(),
                "cursor" => cursor_effort,
                _ => String::new(),
            },
        };
        d.busy = true;
        d.error = None;
        cx.notify();
        cx.spawn(async move |this, cx| {
            let job = match client.post::<_, Job>("/v1/agents", &req).await {
                Ok(j) => j,
                Err(e) => {
                    this.update(cx, |this, cx| {
                        if let Some(d) = this.new_agent_dialog() {
                            d.busy = false;
                            d.error = Some(e.to_string());
                        }
                        cx.notify();
                    })
                    .ok();
                    return;
                }
            };
            // The next agent starts where this one did.
            let _ = client
                .raw(
                    Method::PATCH,
                    "/v1/settings",
                    serde_json::to_vec(&UpdateSettingsRequest {
                        last_agent_pick: Some(pick),
                        ..Default::default()
                    })
                    .ok(),
                )
                .await;
            let id = job.id.clone();
            this.update(cx, |this, cx| {
                if let Some(d) = this.new_agent_dialog() {
                    d.busy = false;
                    d.job = Some(job);
                }
                cx.notify();
            })
            .ok();
            loop {
                cx.background_executor()
                    .timer(Duration::from_millis(800))
                    .await;
                let job = client.get::<Job>(&format!("/v1/jobs/{id}")).await;
                let log = client
                    .raw(Method::GET, &format!("/v1/jobs/{id}/log"), None)
                    .await;
                let mut done = None;
                let gone = this
                    .update(cx, |this, cx| {
                        let Some(d) = this
                            .new_agent_dialog()
                            .filter(|d| d.job.as_ref().is_some_and(|j| j.id == id))
                        else {
                            return true;
                        };
                        if let Ok(b) = log {
                            d.log = String::from_utf8_lossy(&b)
                                .lines()
                                .map(String::from)
                                .collect();
                        }
                        if let Ok(j) = job {
                            if j.status != "running" {
                                done = Some(j.clone());
                            }
                            d.job = Some(j);
                        }
                        cx.notify();
                        false
                    })
                    .unwrap_or(true);
                if let Some(j) = done {
                    if j.status == "succeeded"
                        && let Ok(a) = serde_json::from_value::<Agent>(j.result.clone())
                    {
                        let r = a.r#ref.clone();
                        if let Some(w) = cx.update(|cx| cx.active_window()) {
                            w.update(cx, |_, window, cx| {
                                this.update(cx, |this, cx| {
                                    this.side.dialog = None;
                                    this.open_agent_ref(r, window, cx);
                                    cx.notify();
                                })
                                .ok();
                            })
                            .ok();
                        }
                    }
                    return;
                }
                if gone {
                    // Kept in the background: say when it's done there.
                    loop {
                        cx.background_executor().timer(Duration::from_secs(2)).await;
                        match client.get::<Job>(&format!("/v1/jobs/{id}")).await {
                            Ok(j) if j.status == "running" => continue,
                            Ok(j) if j.status != "succeeded" => {
                                let e = if j.error.is_empty() {
                                    j.status
                                } else {
                                    j.error
                                };
                                cx.update(|cx| toasts::error(cx, e));
                                return;
                            }
                            _ => return,
                        }
                    }
                }
            }
        })
        .detach();
    }

    fn cancel_create(&mut self, cx: &mut Context<Self>) {
        let Some(id) = self
            .new_agent_dialog()
            .and_then(|d| d.job.as_ref().map(|j| j.id.clone()))
        else {
            return;
        };
        let client = self.client.clone();
        cx.spawn(async move |_, cx| {
            if let Err(e) = client
                .raw(Method::POST, &format!("/v1/jobs/{id}/cancel"), None)
                .await
            {
                cx.update(|cx| toasts::error(cx, e.to_string()));
            }
        })
        .detach();
    }

    pub(super) fn render_new_agent(
        &self,
        d: &NewAgent,
        cancel: OnCancel,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let view = cx.entity().downgrade();
        let projects = &self.projects;
        let selected = d.selected(projects);
        let title = d.title.read(cx).value().trim().to_string();
        // The create job: its log until it's done.
        if let Some(job) = &d.job {
            let running = job.status == "running";
            let heading = if title.is_empty() {
                t("project.newAgent.creating")
            } else {
                tv(
                    "project.newAgent.creatingTitled",
                    &[("title", title.into())],
                )
            };
            let body = vec![
                div()
                    .text_sm()
                    .text_color(theme::muted())
                    .child(t("project.newAgent.keepsRunning"))
                    .into_any_element(),
                job_log(&d.log, Some(job)),
            ];
            let left = running.then(|| {
                controls::button(
                    "new-agent-rollback",
                    t("project.newAgent.cancelRollback"),
                    Btn::Ghost,
                    false,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.cancel_create(cx)),
                )
            });
            let close = cancel.clone();
            return form_modal(
                "new-agent",
                heading,
                body,
                left,
                cancel,
                t(if running {
                    "project.newAgent.keepInBackground"
                } else {
                    "common.close"
                }),
                false,
                false,
                move |_, w, cx| close(w, cx),
            );
        }
        // Every pick sets one field and redraws.
        let set = |f: fn(&mut NewAgent, String)| -> controls::Pick<str> {
            let view = view.clone();
            Rc::new(move |v: &str, _, cx| {
                let v = v.to_string();
                view.update(cx, |this, cx| {
                    if let Some(d) = this.new_agent_dialog() {
                        f(d, v);
                    }
                    this.start_pick(cx);
                    cx.notify();
                })
                .ok();
            })
        };
        let flip = |f: fn(&mut NewAgent)| -> Click {
            let view = view.clone();
            Box::new(move |_: &ClickEvent, _: &mut Window, cx: &mut App| {
                view.update(cx, |this, cx| {
                    if let Some(d) = this.new_agent_dialog() {
                        f(d);
                    }
                    cx.notify();
                })
                .ok();
            })
        };
        let mut body: Vec<AnyElement> = vec![
            div()
                .text_sm()
                .text_color(theme::muted())
                .child(t("project.newAgent.description"))
                .into_any_element(),
            field(
                t("project.newAgent.workLabel"),
                Input::new(&d.title).into_any_element(),
                Some(t("project.newAgent.workHint")),
            ),
            field(
                t("project.newAgent.branchLabel"),
                h_flex()
                    .gap_1p5()
                    .when_some(selected.filter(|p| !p.branch_prefix.is_empty()), |el, p| {
                        el.child(
                            div()
                                .flex_none()
                                .font_family(theme::mono())
                                .text_sm()
                                .text_color(theme::faint())
                                .child(p.branch_prefix.clone()),
                        )
                    })
                    .child(div().flex_1().min_w_0().child(Input::new(&d.branch)))
                    .into_any_element(),
                Some(t("project.newAgent.branchHint")),
            ),
            field(
                t("project.newAgent.projectLabel"),
                controls::select_at(
                    "new-agent-project",
                    projects
                        .iter()
                        .map(|p| Choice::new(p.name.clone(), label_of(p)))
                        .collect(),
                    &d.project,
                    false,
                    set(|d, v| d.project = v),
                    Anchor::TopLeft,
                ),
                None,
            ),
        ];
        // The AI tool, two to a row.
        let tools: [(&str, String, String); 6] = [
            ("claude", "Claude Code".into(), "Anthropic".into()),
            ("codex", "Codex".into(), "OpenAI".into()),
            (
                "opencode",
                "OpenCode".into(),
                t("project.newAgent.tool.openSource"),
            ),
            ("cursor", "Cursor".into(), t("project.newAgent.tool.cursor")),
            ("gemini", "Gemini CLI".into(), "Google".into()),
            (
                "none",
                t("project.newAgent.tool.shellOnly"),
                t("project.newAgent.tool.noAI"),
            ),
        ];
        let card =
            |id: String, on: bool, disabled: bool, title: String, hint: String, click: Click| {
                v_flex()
                    .id(SharedString::from(id))
                    .w(relative(0.49))
                    .px_3()
                    .py_2()
                    .rounded_xl()
                    .border_1()
                    .border_color(if on {
                        theme::accent().opacity(0.6)
                    } else {
                        theme::line()
                    })
                    .when(on, |el| el.bg(theme::accent().opacity(0.1)))
                    .when(disabled, |el| el.opacity(0.5))
                    .when(!disabled, |el| {
                        el.cursor_pointer()
                            .hover(|s| s.bg(theme::surface_raised()))
                            .active(|s| s.bg(theme::surface_strong()))
                            .on_click(click)
                    })
                    .child(
                        div()
                            .text_sm()
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::primary())
                            .child(title),
                    )
                    .child(div().text_xs().text_color(theme::faint()).child(hint))
                    .into_any_element()
            };
        let tool_cards: Vec<AnyElement> = tools
            .into_iter()
            .map(|(ai, label, hint)| {
                let v = view.clone();
                card(
                    format!("new-agent-ai-{ai}"),
                    d.ai == ai,
                    false,
                    label,
                    hint,
                    Box::new(move |_, _, cx| {
                        v.update(cx, |this, cx| {
                            if let Some(d) = this.new_agent_dialog() {
                                d.ai = ai.to_string();
                                d.model.clear();
                                d.effort.clear();
                            }
                            cx.notify();
                        })
                        .ok();
                    }),
                )
            })
            .collect();
        body.push(field(
            t("project.newAgent.aiTool"),
            h_flex()
                .flex_wrap()
                .gap_2()
                .children(tool_cards)
                .into_any_element(),
            None,
        ));
        if d.ai != "none" {
            let iface = d.iface();
            let cards: Vec<AnyElement> = [
                ("chat", t("project.newAgent.interface.chat")),
                ("cli", t("project.newAgent.interface.terminal")),
            ]
            .into_iter()
            .map(|(value, label)| {
                let hint = if value == "chat" {
                    t("project.newAgent.chatHint")
                } else if d.ai == "cursor" {
                    t("project.newAgent.cursorNoCli")
                } else {
                    tv(
                        "project.newAgent.cliHint",
                        &[("tool", tool_label(&d.ai).into())],
                    )
                };
                let v = view.clone();
                card(
                    format!("new-agent-iface-{value}"),
                    iface == value,
                    d.ai == "cursor" && value == "cli",
                    label,
                    hint,
                    Box::new(move |_, _, cx| {
                        v.update(cx, |this, cx| {
                            if let Some(d) = this.new_agent_dialog() {
                                d.iface = value;
                            }
                            cx.notify();
                        })
                        .ok();
                    }),
                )
            })
            .collect();
            body.push(field(
                t("project.newAgent.workWith"),
                h_flex().gap_2().children(cards).into_any_element(),
                None,
            ));
        }
        body.push(
            div()
                .id("new-agent-more")
                .text_sm()
                .text_color(theme::muted())
                .cursor_pointer()
                .hover(|s| s.text_color(theme::primary()))
                .active(|s| s.opacity(0.7))
                .on_click(flip(|d| d.advanced = !d.advanced))
                .flex()
                .items_center()
                .gap_1()
                .child(crate::motion::chevron("chev", d.advanced, Some(px(14.))))
                .child(t("project.newAgent.moreOptions"))
                .into_any_element(),
        );
        // The body slides up when it has just been opened (Electron's slide-up).
        let opened = crate::motion::changed("new-agent-more", &d.advanced);
        if d.advanced {
            let more = div().child(self.more_options(d, selected, &set, &flip));
            body.push(match opened {
                Some(n) => crate::motion::enter(more, ("new-agent-more-body", n as usize), 0)
                    .into_any_element(),
                None => more.into_any_element(),
            });
        }
        let auth = d.auth.as_ref();
        let needs_login = auth.is_some_and(|a| match d.ai.as_str() {
            "claude" => !a.claude,
            "codex" => !a.codex,
            "opencode" => !a.opencode,
            "cursor" => !a.cursor,
            "gemini" => !a.gemini,
            _ => false,
        });
        if needs_login {
            body.push(controls::note_in(
                plain(&tv(
                    "project.newAgent.needsLogin",
                    &[
                        ("tool", tool_label(&d.ai).into()),
                        ("command", format!("agentbox auth {}", d.ai).into()),
                    ],
                )),
                theme::starting(),
            ));
        }
        if d.ai == "opencode"
            && d.setup
                .as_ref()
                .is_some_and(|s| !s.image.installed.opencode)
        {
            body.push(controls::note_in(
                plain(&t("project.newAgent.noOpenCode")),
                theme::starting(),
            ));
        }
        if let Some(i) = d.image.as_ref().filter(|i| !i.ready) {
            body.push(controls::note_in(
                t(if i.build == "building" {
                    "project.newAgent.imageBuilding"
                } else {
                    "project.newAgent.noImage"
                }),
                theme::accent(),
            ));
        }
        if let Some(e) = &d.error {
            body.push(controls::error_line(e.clone()));
        }
        // The same work on several agents at once, to compare.
        let compare = controls::button(
            "new-agent-fan-out",
            t("group.newAgent.compare"),
            Btn::Ghost,
            d.project.is_empty(),
            cx.listener(|this, _: &ClickEvent, window, cx| {
                let Some(d) = this.new_agent_dialog() else {
                    return;
                };
                let (p, title) = (
                    d.project.clone(),
                    d.title.read(cx).value().trim().to_string(),
                );
                this.side.dialog = None;
                this.open_fan_out(p, window, cx);
                if let Some(Dialog::FanOut { title: input, .. }) = &this.side.dialog {
                    input.update(cx, |s, cx| s.set_value(title, window, cx));
                }
            }),
        );
        form_modal(
            "new-agent",
            t("project.newAgent.title"),
            body,
            Some(compare),
            cancel,
            t("project.newAgent.create"),
            d.project.is_empty() || d.busy,
            d.busy,
            cx.listener(|this, _: &ClickEvent, _, cx| this.create_agent(cx)),
        )
    }

    fn more_options(
        &self,
        d: &NewAgent,
        selected: Option<&Project>,
        set: &dyn Fn(fn(&mut NewAgent, String)) -> controls::Pick<str>,
        flip: &dyn Fn(fn(&mut NewAgent)) -> Click,
    ) -> AnyElement {
        let projects = &self.projects;
        let s = d.settings.as_ref();
        let empty = Vec::new();
        let (dm, de, dw) = d.defaults(projects);
        let pair = |a: AnyElement, b: Option<AnyElement>| {
            h_flex()
                .gap_4()
                .items_start()
                .child(div().flex_1().min_w_0().child(a))
                .when_some(b, |el, b| el.child(div().flex_1().min_w_0().child(b)))
                .into_any_element()
        };
        let select =
            |id: &str, first: String, list: Vec<Choice>, value: &str, pick: controls::Pick<str>| {
                let mut c = vec![Choice::new("", first)];
                c.extend(list);
                controls::select_at(id, c, value, false, pick, Anchor::TopLeft)
            };
        let mut rows: Vec<AnyElement> = vec![pair(
            field(
                t("project.newAgent.nameLabel"),
                Input::new(&d.name).into_any_element(),
                Some(t("project.newAgent.nameHint")),
            ),
            Some(field(
                t("project.newAgent.fromLabel"),
                Input::new(&d.from).into_any_element(),
                // No "Default:" with nothing after it for a project whose
                // branch isn't known.
                selected.filter(|p| !p.branch.is_empty()).map(|p| {
                    tv(
                        "project.newAgent.fromHint",
                        &[("branch", p.branch.clone().into())],
                    )
                }),
            )),
        )];
        let default_of = |list: &[ChatOptionChoice], fallback: &str| {
            if dm.is_empty() {
                t(fallback)
            } else {
                tv(
                    "project.newAgent.defaultOption",
                    &[("value", setting_label(list, &dm, "").into())],
                )
            }
        };
        match d.ai.as_str() {
            "opencode" => {
                let list = s.map_or(&empty, |s| &s.open_code_model_choices);
                rows.push(field(
                    t("project.newAgent.modelLabel"),
                    select(
                        "new-agent-oc-model",
                        default_of(list, "project.newAgent.openCodeDefault"),
                        choices_of(list),
                        &d.model,
                        set(|d, v| d.model = v),
                    ),
                    Some(t(if list.is_empty() {
                        "project.newAgent.openCodeLoginHint"
                    } else {
                        "project.newAgent.openCodeModelHint"
                    })),
                ));
            }
            "cursor" => {
                let list = s.map_or(&empty, |s| &s.cursor_model_choices);
                let efforts = d.cursor_efforts(projects);
                let effort = if efforts.contains(&d.effort) {
                    d.effort.clone()
                } else {
                    String::new()
                };
                rows.push(pair(
                    field(
                        t("project.newAgent.modelLabel"),
                        select(
                            "new-agent-cursor-model",
                            default_of(list, "project.newAgent.cursorDefault"),
                            choices_of(list),
                            &d.model,
                            set(|d, v| {
                                d.model = v;
                                d.effort.clear();
                            }),
                        ),
                        Some(t("project.newAgent.cursorModelHint")),
                    ),
                    (!efforts.is_empty()).then(|| {
                        field(
                            t("project.newAgent.effortLabel"),
                            select(
                                "new-agent-cursor-effort",
                                t("project.newAgent.cursorEffortDefault"),
                                efforts
                                    .iter()
                                    .map(|e| Choice::new(e.clone(), e.clone()))
                                    .collect(),
                                &effort,
                                set(|d, v| d.effort = v),
                            ),
                            Some(t("project.newAgent.effortHint")),
                        )
                    }),
                ));
            }
            "gemini" => {
                let list = s.map_or(&empty, |s| &s.gemini_model_choices);
                rows.push(field(
                    t("project.newAgent.modelLabel"),
                    select(
                        "new-agent-gemini-model",
                        default_of(list, "project.newAgent.geminiDefault"),
                        choices_of(list),
                        &d.model,
                        set(|d, v| d.model = v),
                    ),
                    Some(t("project.newAgent.geminiModelHint")),
                ));
            }
            "claude" => {
                let models = s.map_or(&empty, |s| &s.claude_model_choices);
                let efforts = s.map_or(&empty, |s| &s.claude_effort_choices);
                let fallback_model = setting_label(
                    models,
                    if dm.is_empty() {
                        s.map_or("", |s| s.default_claude_model.as_str())
                    } else {
                        &dm
                    },
                    "opus",
                );
                let fallback_effort = setting_label(
                    efforts,
                    if de.is_empty() {
                        s.map_or("", |s| s.default_claude_effort.as_str())
                    } else {
                        &de
                    },
                    "medium",
                );
                rows.push(pair(
                    field(
                        t("project.newAgent.modelLabel"),
                        select(
                            "new-agent-model",
                            tv(
                                "project.newAgent.defaultOption",
                                &[("value", fallback_model.into())],
                            ),
                            choices_of(models),
                            &d.model,
                            set(|d, v| d.model = v),
                        ),
                        Some(t("project.newAgent.claudeModelHint")),
                    ),
                    Some(field(
                        t("project.newAgent.effortLabel"),
                        select(
                            "new-agent-effort",
                            tv(
                                "project.newAgent.defaultOption",
                                &[("value", fallback_effort.into())],
                            ),
                            choices_of(efforts),
                            &d.effort,
                            set(|d, v| d.effort = v),
                        ),
                        Some(t("project.newAgent.effortHint")),
                    )),
                ));
                let windows = d.windows(projects);
                if windows.len() > 1 {
                    let wanted: i64 = if dw.is_empty() {
                        s.map_or("", |s| s.default_agent_context_window.as_str())
                    } else {
                        &dw
                    }
                    .parse()
                    .unwrap_or(0);
                    let default_window = if windows.contains(&wanted) {
                        wanted
                    } else {
                        windows[0]
                    };
                    let value = d
                        .window
                        .parse::<i64>()
                        .ok()
                        .filter(|w| windows.contains(w))
                        .map(|w| w.to_string())
                        .unwrap_or_default();
                    rows.push(field(
                        t("project.newAgent.windowLabel"),
                        select(
                            "new-agent-window",
                            tv(
                                "project.newAgent.defaultOption",
                                &[("value", format_tokens(default_window).into())],
                            ),
                            windows[1..]
                                .iter()
                                .map(|n| {
                                    Choice::new(
                                        n.to_string(),
                                        tv(
                                            "project.newAgent.windowWhole",
                                            &[("size", format_tokens(*n).into())],
                                        ),
                                    )
                                })
                                .collect(),
                            &value,
                            set(|d, v| d.window = v),
                        ),
                        Some(t("project.newAgent.windowHint")),
                    ));
                }
                let allowed = selected
                    .map(|p| p.claude_accounts.clone())
                    .unwrap_or_default();
                let accounts: Vec<String> = d
                    .auth
                    .as_ref()
                    .map(|a| {
                        a.claude_accounts
                            .iter()
                            .filter(|x| allowed.is_empty() || allowed.contains(&x.name))
                            .map(|x| x.name.clone())
                            .collect()
                    })
                    .unwrap_or_default();
                if accounts.len() > 1 {
                    let default = d
                        .auth
                        .as_ref()
                        .and_then(|a| a.claude_accounts.iter().find(|x| x.default))
                        .map_or("none".to_string(), |x| x.name.clone());
                    rows.push(account_field(
                        "claude",
                        t("project.newAgent.claudeAccountLabel"),
                        selected.map(|p| p.claude_account.clone()),
                        selected,
                        default,
                        accounts,
                        &d.claude_account,
                        set(|d, v| d.claude_account = v),
                    ));
                }
            }
            _ => {}
        }
        let github: Vec<String> = d
            .auth
            .as_ref()
            .map(|a| a.github_accounts.iter().map(|x| x.name.clone()).collect())
            .unwrap_or_default();
        if github.len() > 1 {
            let default = d
                .auth
                .as_ref()
                .and_then(|a| a.github_accounts.iter().find(|x| x.default))
                .map_or("none".to_string(), |x| x.name.clone());
            rows.push(account_field(
                "github",
                t("project.newAgent.githubAccountLabel"),
                selected.map(|p| p.github_account.clone()),
                selected,
                default,
                github,
                &d.github_account,
                set(|d, v| d.github_account = v),
            ));
        }
        rows.push(field(
            t("project.newAgent.finishLabel"),
            controls::select_at(
                "new-agent-notify",
                vec![
                    Choice::new("", t("project.newAgent.finishProject")),
                    Choice::new("chat", t("project.newAgent.finishWake")),
                    Choice::new("off", t("project.newAgent.finishRecord")),
                ],
                &d.notify,
                false,
                set(|d, v| d.notify = v),
                Anchor::TopLeft,
            ),
            Some(t("project.newAgent.finishHint")),
        ));
        rows.push(switch_row(
            "new-agent-autonomous",
            d.autonomous,
            d.ai == "none",
            t("project.newAgent.autonomousLabel"),
            t("project.newAgent.autonomousHint"),
            flip(|d| d.autonomous = !d.autonomous),
        ));
        if let Some((_, b)) = d.base.as_ref().filter(|(p, _)| *p == d.project) {
            rows.push(switch_row(
                "new-agent-clean",
                d.clean,
                false,
                t("project.newAgent.cleanLabel"),
                tv(
                    "project.newAgent.cleanHint",
                    &[("from", b.saved_from.clone().into())],
                ),
                flip(|d| d.clean = !d.clean),
            ));
        }
        v_flex()
            .gap_4()
            .p_4()
            .rounded_xl()
            .border_1()
            .border_color(theme::line())
            .bg(theme::ink())
            .children(rows)
            .into_any_element()
    }
}

#[allow(clippy::too_many_arguments)]
fn account_field(
    id: &str,
    label: String,
    project_account: Option<String>,
    selected: Option<&Project>,
    default: String,
    accounts: Vec<String>,
    value: &str,
    pick: controls::Pick<str>,
) -> AnyElement {
    let own = project_account.filter(|a| !a.is_empty());
    let hint = match (&own, selected) {
        (Some(a), Some(p)) => tv(
            "project.newAgent.accountUses",
            &[
                ("project", p.name.clone().into()),
                ("account", a.clone().into()),
            ],
        ),
        _ => tv(
            "project.newAgent.accountDefault",
            &[("account", default.clone().into())],
        ),
    };
    let first = match &own {
        Some(a) => tv(
            "project.newAgent.projectsAccount",
            &[("account", a.clone().into())],
        ),
        None => tv(
            "project.newAgent.defaultOption",
            &[("value", default.into())],
        ),
    };
    let mut c = vec![Choice::new("", first)];
    c.extend(accounts.into_iter().map(|a| Choice::new(a.clone(), a)));
    field(
        label,
        controls::select_at(
            &format!("new-agent-{id}-account"),
            c,
            value,
            false,
            pick,
            Anchor::TopLeft,
        ),
        Some(hint),
    )
}

fn switch_row(
    id: &'static str,
    on: bool,
    disabled: bool,
    label: String,
    hint: String,
    click: Click,
) -> AnyElement {
    h_flex()
        .gap_3()
        .items_start()
        .child(controls::toggle(id, on, disabled, move |e, w, cx| {
            click(e, w, cx)
        }))
        .child(
            v_flex()
                .flex_1()
                .min_w_0()
                .gap_0p5()
                .child(div().text_sm().text_color(theme::primary()).child(label))
                .child(div().text_xs().text_color(theme::faint()).child(hint)),
        )
        .into_any_element()
}

#[cfg(test)]
mod tests {
    use super::branch_slug;

    #[test]
    fn slugs_like_the_daemon() {
        assert_eq!(branch_slug("Fix the Login page!"), "fix-the-login-page");
        let long = "word ".repeat(20);
        assert!(branch_slug(&long).len() <= 48);
        assert!(!branch_slug(&long).ends_with('-'));
    }
}
