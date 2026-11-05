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

use crate::chat::ChatView;
use crate::terminal::TerminalView;
use crate::{bench, theme};

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
    showing_terminal: bool,
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
            showing_terminal: false,
            _events: events,
        };
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
                if let Some(s) = &chat_ev.session {
                    // An agent's chat working is what the rail shows as busy.
                    if let Some(a) = self.fleet.iter_mut().find(|a| a.r#ref == chat_ev.agent) {
                        a.busy = s.state == "running";
                        cx.notify();
                    }
                }
                if let Some(chat) = &self.chat
                    && chat.read(cx).agent == chat_ev.agent
                {
                    chat.update(cx, |c, cx| c.apply(chat_ev, cx));
                }
            }
            EVENT_PROJECT => self.load_projects(window, cx),
            _ => {}
        }
    }

    fn load_projects(&mut self, _window: &mut Window, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn_in(_window, async move |this, cx| {
            let res = client.get::<Vec<Project>>("/v1/projects").await;
            this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(p) => {
                        this.projects = p;
                        this.error = None;
                        bench::mark("projects-loaded");
                        if this.project.is_none()
                            && let Some(first) = this.projects.first().map(|p| p.name.clone())
                        {
                            this.select_project(first, window, cx);
                        }
                    }
                    Err(e) => this.error = Some(format!("Can't reach the daemon: {e}").into()),
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
            .unwrap_or(project.clone());
        let (prefix, agent, title) = if who == "lead" {
            (
                format!("/v1/projects/{project}"),
                format!("{project}/lead"),
                format!("{display} · Lead"),
            )
        } else {
            let t = self
                .fleet
                .iter()
                .find(|a| a.name == who)
                .map(|a| a.title.clone())
                .unwrap_or_default();
            (
                format!("/v1/agents/{project}/{who}"),
                format!("{project}/{who}"),
                format!("{who} · {t}"),
            )
        };
        self.open = who;
        self.terminal = None;
        self.showing_terminal = false;
        let client = self.client.clone();
        self.chat =
            Some(cx.new(|cx| ChatView::new(client, prefix, agent, title.into(), window, cx)));
        cx.notify();
    }

    fn render_sidebar(&self, cx: &mut Context<Self>) -> impl IntoElement {
        v_flex()
            .w(px(232.))
            .flex_none()
            .h_full()
            .bg(theme::panel())
            .border_r_1()
            .border_color(theme::border())
            .child(
                h_flex()
                    .h_12()
                    .px_4()
                    .gap_2()
                    .child(div().size_5().rounded_md().bg(theme::accent()))
                    .child(
                        div()
                            .text_color(theme::fg())
                            .font_weight(FontWeight::SEMIBOLD)
                            .child("AgentBox"),
                    )
                    .child(div().flex_1())
                    .child(div().size_2().rounded_full().bg(if self.connected {
                        theme::running()
                    } else {
                        theme::error()
                    })),
            )
            .child(
                div()
                    .px_4()
                    .pt_3()
                    .pb_1()
                    .text_xs()
                    .text_color(theme::faint())
                    .child("PROJECTS"),
            )
            .children(self.projects.iter().map(|p| {
                let selected = self.project.as_deref() == Some(p.name.as_str());
                let name = p.name.clone();
                let display = if p.display_name.is_empty() {
                    p.name.clone()
                } else {
                    p.display_name.clone()
                };
                let initial = display
                    .chars()
                    .next()
                    .unwrap_or('?')
                    .to_uppercase()
                    .to_string();
                h_flex()
                    .id(SharedString::from(format!("project-{}", p.name)))
                    .mx_2()
                    .px_2()
                    .py_1p5()
                    .gap_2()
                    .rounded_md()
                    .cursor_pointer()
                    .when(selected, |el| el.bg(theme::hover()))
                    .hover(|el| el.bg(theme::raised()))
                    .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.select_project(name.clone(), window, cx)
                    }))
                    .child(
                        div()
                            .size_5()
                            .flex()
                            .items_center()
                            .justify_center()
                            .rounded_sm()
                            .bg(theme::raised())
                            .text_xs()
                            .text_color(theme::muted())
                            .child(initial),
                    )
                    .child(
                        div()
                            .text_sm()
                            .text_color(if selected {
                                theme::fg()
                            } else {
                                theme::muted()
                            })
                            .truncate()
                            .child(display),
                    )
            }))
            .when_some(self.error.clone(), |el, e| {
                el.child(div().p_4().text_xs().text_color(theme::error()).child(e))
            })
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
                    .child(div().text_sm().text_color(theme::fg()).child("Lead"))
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child("The project's chat"),
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
                            .child("Agents"),
                    )
                    .child(div().flex_1())
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(format!("{}", self.fleet.len())),
                    ),
            )
            .child(lead)
            .child(div().mx_4().my_1().h(px(1.)).bg(theme::border()))
            .children(self.fleet.iter().map(|a| {
                let name = a.name.clone();
                let busy = a.busy;
                row(format!("rail-{}", a.name), self.open == a.name)
                    .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
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
                                                .child("working"),
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
                                    .child(a.state.clone()),
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
        let tab = |id: &'static str, label: &'static str, on: bool| {
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
                tab("tab-chat", "Chat", !self.showing_terminal).on_click(cx.listener(
                    |this, _: &ClickEvent, _, cx| {
                        this.showing_terminal = false;
                        cx.notify();
                    },
                )),
            )
            .child(
                tab("tab-terminal", "Terminal", self.showing_terminal).on_click(cx.listener(
                    |this, _: &ClickEvent, window, cx| {
                        this.show_terminal(window, cx);
                    },
                )),
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
        self.showing_terminal = true;
        cx.notify();
    }
}

impl Render for AppView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        bench::mark("first-render");
        h_flex()
            .size_full()
            .bg(theme::bg())
            .text_color(theme::fg())
            .child(self.render_sidebar(cx))
            .child(
                div().flex_1().min_w_0().h_full().child(match &self.chat {
                    Some(chat) if self.open != "lead" => v_flex()
                        .size_full()
                        .child(self.render_tabs(cx))
                        .child(div().flex_1().min_h_0().child(
                            match (&self.terminal, self.showing_terminal) {
                                (Some(t), true) => t.clone().into_any_element(),
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
                        .child(if self.projects.is_empty() {
                            "No projects yet"
                        } else {
                            "Pick a project"
                        })
                        .into_any_element(),
                }),
            )
            .child(self.render_rail(cx))
    }
}
