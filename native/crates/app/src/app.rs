//! The window: the v2 layout's sidebar (projects), main (the open chat) and
//! rail (the project's lead and agents, with their live state).
use std::time::Duration;

use agentbox_api::{
    AgentChange, ChatEvent, Client, EVENT_AGENT, EVENT_CHAT, EVENT_PROJECT, Event, Fleet,
    FleetAgent, Project,
};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

mod controls;
mod new_task;
mod settings;
mod sidebar;

use crate::chat::ChatView;
use crate::terminal::TerminalView;
use crate::vnc::VncView;

/// One agent in the rail.
struct RailRow {
    project: String,
    name: String,
    title: String,
    state: String,
    busy: bool,
}

/// What the main area shows of an agent.
#[derive(Clone, Copy, PartialEq)]
enum Tab {
    Chat,
    Terminal,
    Desktop,
}
use crate::{bench, theme};
use agentbox_i18n::t;

/// state_label is an agent's state in the user's language (agent.state.*).
fn state_label(state: &str) -> String {
    let key = format!("agent.state.{state}");
    if agentbox_i18n::has(&key) {
        t(&key)
    } else {
        state.to_string()
    }
}

pub struct AppView {
    client: Client,
    projects: Vec<Project>,
    project: Option<String>,
    fleet: Vec<FleetAgent>,
    fleet_error: Option<SharedString>,
    error: Option<SharedString>,
    connected: bool,
    /// Which chat is open: "lead" or an agent's name.
    open: String,
    chat: Option<Entity<ChatView>>,
    /// An agent's terminal, opened from its chat's tab bar.
    terminal: Option<Entity<TerminalView>>,
    /// An agent's desktop (VNC), opened the same way.
    desktop: Option<Entity<VncView>>,
    tab: Tab,
    side: sidebar::Sidebar,
    /// The New task picker, while it's open.
    new_task: Option<new_task::NewTask>,
    /// The Settings page is open in the main area.
    settings: bool,
    settings_page: settings::SettingsPage,
    _events: Task<()>,
}

impl AppView {
    pub fn new(client: Client, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let events = Self::follow(client.clone(), window, cx);
        let mut this = Self {
            client,
            projects: Vec::new(),
            project: None,
            fleet: Vec::new(),
            fleet_error: None,
            error: None,
            connected: false,
            open: "lead".into(),
            chat: None,
            terminal: None,
            desktop: None,
            tab: Tab::Chat,
            side: sidebar::Sidebar::new(window, cx),
            new_task: None,
            settings: false,
            settings_page: settings::SettingsPage::new(window, cx),
            _events: events,
        };
        this.load_settings(window, cx);
        this.load_theme(window, cx);
        this.load_projects(window, cx);
        if let Ok(secs) =
            std::env::var("AGENTBOX_NATIVE_SCROLL_BENCH").map(|s| s.parse::<u64>().unwrap_or(3))
        {
            // Once the chat is in, scroll it up for a while and report frame times.
            cx.spawn_in(window, async move |this, cx| {
                cx.background_executor()
                    .timer(Duration::from_secs(secs))
                    .await;
                this.update_in(cx, |this, window, cx| {
                    if let Some(chat) = &this.chat {
                        chat.update(cx, |c, cx| c.scroll_bench(-24., 600, window, cx));
                    }
                })
                .ok();
            })
            .detach();
        }
        this
    }

    /// follow applies the daemon's events for as long as the window is open.
    fn follow(client: Client, window: &mut Window, cx: &mut Context<Self>) -> Task<()> {
        let mut rx = client.events();
        cx.spawn_in(window, async move |this, cx| {
            while let Some(msg) = rx.recv().await {
                let ok = this.update_in(cx, |this, window, cx| this.on_event(msg, window, cx));
                if ok.is_err() {
                    return;
                }
            }
        })
    }

    fn on_event(&mut self, msg: Option<Event>, window: &mut Window, cx: &mut Context<Self>) {
        let Some(ev) = msg else {
            // (Re)connected: whatever happened meanwhile is in a fresh read.
            self.connected = true;
            self.load_settings(window, cx);
            self.load_theme(window, cx);
            if self.settings {
                self.load_settings_page(cx);
            }
            self.load_projects(window, cx);
            if let Some(chat) = &self.chat {
                chat.update(cx, |c, cx| c.load(cx));
            }
            return;
        };
        match ev.r#type.as_str() {
            EVENT_AGENT => {
                let Ok(change) = serde_json::from_value::<AgentChange>(ev.data) else {
                    return;
                };
                if !self
                    .side
                    .apply_agent(&change.r#ref, Some(&change.state), None)
                    && !change.removed
                {
                    self.load_sidebar(cx);
                }
                cx.notify();
                let mine = self
                    .project
                    .as_deref()
                    .is_some_and(|p| change.r#ref.starts_with(&format!("{p}/")));
                if !mine {
                    return;
                }
                match self.fleet.iter().position(|a| a.r#ref == change.r#ref) {
                    Some(ix) if change.removed => {
                        self.fleet.remove(ix);
                    }
                    Some(ix) => self.fleet[ix].state = change.state,
                    None => self.load_fleet(window, cx),
                }
                cx.notify();
            }
            EVENT_CHAT => {
                let Ok(chat_ev) = serde_json::from_value::<ChatEvent>(ev.data) else {
                    return;
                };
                if let Some(s) = &chat_ev.session
                    && self.side.apply_agent(&chat_ev.agent, None, Some(&s.state))
                {
                    cx.notify();
                }
                if let Some(s) = &chat_ev.session {
                    // An agent's chat working is what the rail shows as busy.
                    if let Some(a) = self.fleet.iter_mut().find(|a| a.r#ref == chat_ev.agent) {
                        a.busy = s.state == "running";
                        cx.notify();
                    }
                }
                // A lead's message changes its project's line in the sidebar.
                if let Some(p) = chat_ev.agent.strip_suffix("/lead")
                    && chat_ev.item.as_ref().is_some_and(|i| {
                        !i.streaming && (i.kind == "user" || i.kind == "assistant")
                    })
                {
                    self.load_line(p.to_string(), cx);
                }
                if let Some(chat) = &self.chat
                    && chat.read(cx).agent == chat_ev.agent
                {
                    chat.update(cx, |c, cx| c.apply(chat_ev, cx));
                }
            }
            EVENT_PROJECT => self.load_projects(window, cx),
            agentbox_api::EVENT_THEME => self.load_theme(window, cx),
            agentbox_api::EVENT_UPDATE => self.reload_update(cx),
            agentbox_api::EVENT_QUESTION => {
                if let Some(p) = ev.data.get("project").and_then(|v| v.as_str()) {
                    self.load_questions(p.to_string(), cx);
                }
            }
            _ => {}
        }
    }

    /// load_settings takes the language the user picked (Settings.language),
    /// unless AGENTBOX_NATIVE_LANG overrides it.
    fn load_settings(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if let Ok(lang) = std::env::var("AGENTBOX_NATIVE_LANG") {
            agentbox_i18n::set_language(&lang);
            return;
        }
        let client = self.client.clone();
        cx.spawn_in(window, async move |_, cx| {
            if let Ok(s) = client.get::<agentbox_api::Settings>("/v1/settings").await {
                agentbox_i18n::set_language(&s.language);
                cx.update(|window, _| window.refresh()).ok();
            }
        })
        .detach();
    }

    fn load_projects(&mut self, _window: &mut Window, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn_in(_window, async move |this, cx| {
            let res = client.get::<Vec<Project>>("/v1/projects").await;
            this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(p) => {
                        this.projects = p;
                        this.load_sidebar(cx);
                        this.error = None;
                        bench::mark("projects-loaded");
                        if this.project.is_none() {
                            // The app opens on Home, as v2 does; a benchmark
                            // opening an agent (AGENTBOX_NATIVE_OPEN) looks for
                            // it in fake-daemon's project, demo.
                            let first = match std::env::var_os("AGENTBOX_NATIVE_OPEN") {
                                Some(_) => this
                                    .projects
                                    .iter()
                                    .find(|p| p.name == "demo")
                                    .or(this.projects.first())
                                    .map(|p| p.name.clone()),
                                None => Some(agentbox_api::HOME_PROJECT.to_string()),
                            };
                            if let Some(first) = first {
                                this.select_project(first, window, cx);
                            }
                        }
                    }
                    Err(e) => this.error = Some(e.to_string().into()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn load_fleet(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if project == agentbox_api::HOME_PROJECT {
            return; // Home's rail is every project's agents (rail_rows)
        }
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let res = client
                .get::<Fleet>(&format!("/v1/projects/{project}/fleet"))
                .await;
            this.update_in(cx, |this, window, cx| {
                if this.project.as_deref() != Some(&project) {
                    return;
                }
                match res {
                    Ok(f) => {
                        this.fleet = f.agents;
                        this.fleet_error = None;
                        bench::mark("fleet-loaded");
                        // AGENTBOX_NATIVE_OPEN=<agent> opens that agent's chat at start (benchmarks).
                        if let Ok(open) = std::env::var("AGENTBOX_NATIVE_OPEN")
                            && this.open == "lead"
                            && this.fleet.iter().any(|a| a.name == open)
                        {
                            this.open_chat(open, window, cx);
                        }
                    }
                    Err(e) => this.fleet_error = Some(e.to_string().into()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn select_project(&mut self, name: String, window: &mut Window, cx: &mut Context<Self>) {
        self.settings = false;
        self.project = Some(name);
        self.fleet.clear();
        self.load_fleet(window, cx);
        self.open_chat("lead".into(), window, cx);
    }

    fn open_chat(&mut self, who: String, window: &mut Window, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        let display = self
            .projects
            .iter()
            .find(|p| p.name == project)
            .map(|p| p.display_name.clone())
            .unwrap_or_else(|| {
                if project == agentbox_api::HOME_PROJECT {
                    t("shell.nav.home")
                } else {
                    project.clone()
                }
            });
        let (prefix, agent, title) = if who == "lead" {
            (
                format!("/v1/projects/{project}"),
                format!("{project}/lead"),
                format!("{display} · {}", t("v2.header.lead")),
            )
        } else {
            let t = self
                .fleet
                .iter()
                .find(|a| a.name == who)
                .map(|a| a.title.clone())
                .or_else(|| {
                    self.side
                        .agents
                        .iter()
                        .find(|a| a.project == project && a.name == who)
                        .map(|a| a.title.clone())
                })
                .unwrap_or_default();
            (
                format!("/v1/agents/{project}/{who}"),
                format!("{project}/{who}"),
                format!("{who} · {t}"),
            )
        };
        self.open = who;
        self.terminal = None;
        self.desktop = None;
        self.tab = Tab::Chat;
        let client = self.client.clone();
        self.chat =
            Some(cx.new(|cx| ChatView::new(client, prefix, agent, title.into(), window, cx)));
        cx.notify();
    }

    fn is_home(&self) -> bool {
        self.project.as_deref() == Some(agentbox_api::HOME_PROJECT)
    }

    /// rail_rows are the agents the rail lists: the project's, or on Home
    /// every project's.
    fn rail_rows(&self) -> Vec<RailRow> {
        if self.is_home() {
            return self
                .side
                .agents
                .iter()
                .map(|a| RailRow {
                    project: a.project.clone(),
                    name: a.name.clone(),
                    title: format!("{} · {}", a.project, a.title),
                    state: a.state.clone(),
                    busy: a.chat == "running",
                })
                .collect();
        }
        self.fleet
            .iter()
            .map(|a| RailRow {
                project: a.project.clone(),
                name: a.name.clone(),
                title: a.title.clone(),
                state: a.state.clone(),
                busy: a.busy,
            })
            .collect()
    }

    fn render_rail(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let row = |id: String, selected: bool| {
            h_flex()
                .id(SharedString::from(id))
                .mx_2()
                .px_2()
                .py_2()
                .gap_2()
                .rounded_md()
                .cursor_pointer()
                .when(selected, |el| el.bg(theme::hover()))
                .hover(|el| el.bg(theme::raised()))
        };
        let lead = row("rail-lead".into(), self.open == "lead")
            .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                this.open_chat("lead".into(), window, cx)
            }))
            .child(div().size_2().rounded_full().bg(theme::accent()))
            .child(
                v_flex()
                    .child(
                        div()
                            .text_sm()
                            .text_color(theme::fg())
                            .child(t("v2.header.lead")),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(t("v2.header.openLead")),
                    ),
            );
        v_flex()
            .w(px(300.))
            .flex_none()
            .h_full()
            .bg(theme::panel())
            .border_l_1()
            .border_color(theme::border())
            .child(
                h_flex()
                    .h_12()
                    .px_4()
                    .child(
                        div()
                            .text_sm()
                            .text_color(theme::fg())
                            .font_weight(FontWeight::SEMIBOLD)
                            .child(t("shell.home.agents")),
                    )
                    .child(div().flex_1())
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(format!("{}", self.rail_rows().len())),
                    ),
            )
            .child(lead)
            .child(div().mx_4().my_1().h(px(1.)).bg(theme::border()))
            .children(self.rail_rows().into_iter().map(|a| {
                let (name, project) = (a.name.clone(), a.project.clone());
                let busy = a.busy;
                let home = self.is_home();
                row(
                    format!("rail-{}-{}", a.project, a.name),
                    self.open == a.name && !home,
                )
                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                    // From Home, an agent opens in its own project.
                    if this.is_home() {
                        this.select_project(project.clone(), window, cx);
                    }
                    this.open_chat(name.clone(), window, cx)
                }))
                .child(
                    div()
                        .flex_none()
                        .size_2()
                        .rounded_full()
                        .bg(theme::state_color(&a.state)),
                )
                .child(
                    v_flex()
                        .min_w_0()
                        .flex_1()
                        .child(
                            h_flex()
                                .gap_2()
                                .child(
                                    div()
                                        .text_sm()
                                        .text_color(theme::fg())
                                        .child(a.name.clone()),
                                )
                                .when(busy, |el| {
                                    el.child(
                                        div()
                                            .text_xs()
                                            .text_color(theme::starting())
                                            .child(t("chat.subagent.working")),
                                    )
                                }),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .truncate()
                                .child(a.title.clone()),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::faint())
                                .child(state_label(&a.state)),
                        ),
                )
            }))
            .when_some(self.fleet_error.clone(), |el, e| {
                el.child(div().p_4().text_xs().text_color(theme::error()).child(e))
            })
    }
}

impl AppView {
    /// render_tabs is an agent's Chat | Terminal switch.
    fn render_tabs(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let tab = |id: &'static str, label: String, on: bool| {
            div()
                .id(id)
                .px_3()
                .py_1()
                .rounded_md()
                .cursor_pointer()
                .text_sm()
                .text_color(if on { theme::fg() } else { theme::muted() })
                .when(on, |el| el.bg(theme::hover()))
                .hover(|el| el.bg(theme::raised()))
                .child(label)
        };
        h_flex()
            .flex_none()
            .gap_1()
            .px_4()
            .py_1p5()
            .bg(theme::panel())
            .border_b_1()
            .border_color(theme::border())
            .child(
                tab("tab-chat", t("agent.tab.chat"), self.tab == Tab::Chat).on_click(cx.listener(
                    |this, _: &ClickEvent, _, cx| {
                        this.tab = Tab::Chat;
                        cx.notify();
                    },
                )),
            )
            .child(
                tab(
                    "tab-terminal",
                    t("agent.tab.terminal"),
                    self.tab == Tab::Terminal,
                )
                .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.show_terminal(window, cx);
                })),
            )
            .child(
                tab(
                    "tab-desktop",
                    t("agent.tab.desktop"),
                    self.tab == Tab::Desktop,
                )
                .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.show_desktop(window, cx);
                })),
            )
    }

    fn show_terminal(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if self.terminal.is_none() {
            let path = format!("/v1/agents/{project}/{}/terminal", self.open);
            let client = self.client.clone();
            self.terminal = Some(cx.new(|cx| TerminalView::new(client, path, cx)));
        }
        if let Some(t) = &self.terminal {
            let focus = t.read(cx).focus_handle().clone();
            focus.focus(window, cx);
        }
        self.tab = Tab::Terminal;
        cx.notify();
    }

    fn show_desktop(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if self.desktop.is_none() {
            let path = format!("/v1/agents/{project}/{}/browser/view", self.open);
            let client = self.client.clone();
            self.desktop = Some(cx.new(|cx| VncView::new(client, path, cx)));
        }
        if let Some(d) = &self.desktop {
            let focus = d.read(cx).focus_handle().clone();
            focus.focus(window, cx);
        }
        self.tab = Tab::Desktop;
        cx.notify();
    }
}

impl Render for AppView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        bench::mark("first-render");
        let overlay = self.render_new_task(cx);
        h_flex()
            .id("app")
            .relative()
            // Ctrl+N anywhere opens New task.
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, window, cx| {
                if ev.keystroke.modifiers.control
                    && ev.keystroke.key == "n"
                    && this.new_task.is_none()
                {
                    this.open_new_task(window, cx);
                    cx.stop_propagation();
                }
            }))
            .size_full()
            .bg(theme::bg())
            .text_color(theme::fg())
            .child(self.render_sidebar(cx))
            .child(
                div().flex_1().min_w_0().h_full().child(match &self.chat {
                    _ if self.settings => self.render_settings(cx),
                    Some(chat) if self.open != "lead" => v_flex()
                        .size_full()
                        .child(self.render_tabs(cx))
                        .child(div().flex_1().min_h_0().child(
                            match (self.tab, &self.terminal, &self.desktop) {
                                (Tab::Terminal, Some(t), _) => t.clone().into_any_element(),
                                (Tab::Desktop, _, Some(d)) => d.clone().into_any_element(),
                                _ => chat.clone().into_any_element(),
                            },
                        ))
                        .into_any_element(),
                    Some(chat) => chat.clone().into_any_element(),
                    None => div()
                        .size_full()
                        .flex()
                        .items_center()
                        .justify_center()
                        .text_color(theme::faint())
                        .when(self.projects.is_empty(), |el| {
                            el.child(t("shell.sidebar.addFirst"))
                        })
                        .into_any_element(),
                }),
            )
            .child(self.render_rail(cx))
            // Last, so it draws over the rest.
            .children(overlay)
    }
}
