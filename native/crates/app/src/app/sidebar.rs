//! The v2 sidebar (desktop/src/renderer/components/v2/SidebarV2.tsx): Home,
//! a search, the user's sections, the other projects by what needs attention,
//! and docks for the idle and the hidden. Each project's row has its time, its
//! lead's last line or the question waiting, and counts of what's going on.
use std::collections::{HashMap, HashSet};
use std::time::SystemTime;

use agentbox_api::{
    Agent, ChatLine, HOME_PROJECT, Project, ProjectActivity, ProjectChat, Question, Section,
    SidebarLayout,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::{h_flex, v_flex};

use super::AppView;
use crate::theme;

/// What the sidebar reads besides the projects.
pub struct Sidebar {
    sections: Vec<Section>,
    hidden: HashSet<String>,
    activity: HashMap<String, ProjectActivity>,
    pub(super) agents: Vec<Agent>,
    questions: HashMap<String, Vec<Question>>,
    /// Each project's lead's last line (GET /v1/projects/<p>/lead).
    lines: HashMap<String, ChatLine>,
    search: Entity<InputState>,
    idle_open: bool,
    hidden_open: bool,
    _sub: Subscription,
    _poll: Task<()>,
}

impl Sidebar {
    pub fn new(window: &mut Window, cx: &mut Context<AppView>) -> Self {
        let search = cx.new(|cx| InputState::new(window, cx).placeholder(t("v2.filters.search")));
        let sub = cx.subscribe(&search, |_, _, ev: &InputEvent, cx| {
            if matches!(ev, InputEvent::Change) {
                cx.notify();
            }
        });
        // Activity is polled, as the Electron app does: nothing announces it.
        let poll = cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor()
                    .timer(std::time::Duration::from_secs(60))
                    .await;
                if this.update(cx, |this, cx| this.load_activity(cx)).is_err() {
                    return;
                }
            }
        });
        Self {
            sections: Vec::new(),
            hidden: HashSet::new(),
            activity: HashMap::new(),
            agents: Vec::new(),
            questions: HashMap::new(),
            lines: HashMap::new(),
            search,
            idle_open: false,
            hidden_open: false,
            _sub: sub,
            _poll: poll,
        }
    }

    /// apply_agent keeps an agent's state and chat state current from events.
    pub fn apply_agent(&mut self, r#ref: &str, state: Option<&str>, chat: Option<&str>) -> bool {
        let Some(a) = self.agents.iter_mut().find(|a| a.r#ref == r#ref) else {
            return false;
        };
        if let Some(s) = state {
            a.state = s.to_string();
        }
        if let Some(c) = chat {
            a.chat = c.to_string();
        }
        true
    }
}

/// Counts is what a project's row says is going on.
#[derive(Default)]
struct Counts {
    asks: usize,
    working: usize,
    failed: usize,
    running: usize,
}

impl AppView {
    /// load_sidebar reads sections, the hidden list, activity, agents and
    /// open questions.
    pub(super) fn load_sidebar(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let sections = client
                .get::<Vec<Section>>("/v1/sections")
                .await
                .unwrap_or_default();
            let layout = client
                .get::<SidebarLayout>("/v1/sidebar")
                .await
                .unwrap_or_default();
            let agents = client
                .get::<Vec<Agent>>("/v1/agents")
                .await
                .unwrap_or_default();
            this.update(cx, |this, cx| {
                this.side.sections = sections;
                this.side.sections.sort_by_key(|s| s.position);
                this.side.hidden = layout.hidden.into_iter().collect();
                this.side.agents = agents;
                cx.notify();
            })
            .ok();
        })
        .detach();
        self.load_activity(cx);
        let names: Vec<String> = self.projects.iter().map(|p| p.name.clone()).collect();
        for p in names.into_iter().chain([HOME_PROJECT.to_string()]) {
            self.load_questions(p.clone(), cx);
            self.load_line(p, cx);
        }
    }

    /// load_line reads a project's lead's last line.
    pub(super) fn load_line(&mut self, project: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            if let Ok(c) = client
                .get::<ProjectChat>(&format!("/v1/projects/{project}/lead"))
                .await
                && let Some(last) = c.last
            {
                this.update(cx, |this, cx| {
                    this.side.lines.insert(project, last);
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    /// line is what a row says its lead last said: "You: …" when you spoke last.
    fn line(&self, project: &str) -> Option<String> {
        let l = self.side.lines.get(project)?;
        let text = l.text.lines().next().unwrap_or("").to_string();
        if text.is_empty() {
            return None;
        }
        Some(if l.kind == "user" {
            tv("v2.sidebar.you", &[("text", (&text).into())])
        } else {
            text
        })
    }

    pub(super) fn load_activity(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            if let Ok(a) = client
                .get::<Vec<ProjectActivity>>("/v1/project-activity")
                .await
            {
                this.update(cx, |this, cx| {
                    this.side.activity = a.into_iter().map(|a| (a.project.clone(), a)).collect();
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    pub(super) fn load_questions(&mut self, project: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let q = client
                .get::<Vec<Question>>(&format!("/v1/projects/{project}/questions"))
                .await
                .unwrap_or_default();
            this.update(cx, |this, cx| {
                this.side.questions.insert(
                    project,
                    q.into_iter().filter(|q| q.status == "open").collect(),
                );
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn counts(&self, project: &str) -> Counts {
        let mut c = Counts {
            asks: self.side.questions.get(project).map_or(0, Vec::len),
            ..Default::default()
        };
        for a in self.side.agents.iter().filter(|a| a.project == project) {
            if a.state == "running" {
                c.running += 1;
            }
            match a.chat.as_str() {
                "running" => c.working += 1,
                "error" => c.failed += 1,
                _ => {}
            }
        }
        c
    }

    fn matches(&self, p: &Project, query: &str) -> bool {
        if query.is_empty() {
            return true;
        }
        let q = query.to_lowercase();
        p.name.to_lowercase().contains(&q)
            || p.display_name.to_lowercase().contains(&q)
            || self.side.agents.iter().any(|a| {
                a.project == p.name
                    && (a.name.to_lowercase().contains(&q) || a.title.to_lowercase().contains(&q))
            })
    }

    pub(super) fn render_sidebar(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let query = self.side.search.read(cx).value().trim().to_string();
        let shown: Vec<&Project> = self
            .projects
            .iter()
            .filter(|p| self.matches(p, &query))
            .collect();
        let hidden = &self.side.hidden;
        // Sections keep the user's order; the rest go by attention, then activity.
        let mut placed = HashSet::new();
        let mut sections = Vec::new();
        for s in &self.side.sections {
            let mut ps: Vec<&Project> = shown
                .iter()
                .copied()
                .filter(|p| p.section == s.id && !hidden.contains(&p.name))
                .collect();
            ps.sort_by_key(|p| p.position);
            placed.extend(ps.iter().map(|p| p.name.clone()));
            sections.push((s, ps));
        }
        let (mut rest, mut idle, mut hid) = (Vec::new(), Vec::new(), Vec::new());
        for p in shown.iter().copied().filter(|p| !placed.contains(&p.name)) {
            if hidden.contains(&p.name) && self.project.as_deref() != Some(&p.name) {
                hid.push(p);
            } else if self.counts(&p.name).running == 0 && self.counts(&p.name).asks == 0 {
                idle.push(p);
            } else {
                rest.push(p);
            }
        }
        rest.sort_by(|a, b| {
            let (ca, cb) = (self.counts(&a.name), self.counts(&b.name));
            (cb.asks + cb.failed, cb.working)
                .cmp(&(ca.asks + ca.failed, ca.working))
                .then_with(|| self.last(&b.name).cmp(&self.last(&a.name)))
        });
        idle.sort_by_key(|p| std::cmp::Reverse(self.last(&p.name)));
        let nothing_else = rest.is_empty() && sections.iter().all(|(_, ps)| ps.is_empty());
        let idle_open = self.side.idle_open || nothing_else || !query.is_empty();
        let hidden_open = self.side.hidden_open || !query.is_empty();
        let nothing = !self.projects.is_empty() && shown.is_empty();

        v_flex()
            .w(px(288.))
            .flex_none()
            .h_full()
            .bg(theme::panel())
            .border_r_1()
            .border_color(theme::border())
            .child(
                h_flex()
                    .h(px(56.))
                    .flex_none()
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
                    .id("sidebar-scroll")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .child(
                        v_flex()
                            .pb_4()
                            .child(
                                self.row("new-task", false, cx)
                                    .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                                        this.open_new_task(window, cx)
                                    }))
                                    .child(
                                        div()
                                            .flex_none()
                                            .size_6()
                                            .flex()
                                            .items_center()
                                            .justify_center()
                                            .text_color(theme::muted())
                                            .child("✎"),
                                    )
                                    .child(
                                        div()
                                            .flex_1()
                                            .text_sm()
                                            .text_color(theme::muted())
                                            .child(t("v2.sidebar.newTask")),
                                    )
                                    .child(
                                        div().text_xs().text_color(theme::faint()).child("Ctrl N"),
                                    ),
                            )
                            .child(self.home_row(cx))
                            .child(
                                div()
                                    .px_3()
                                    .py_2()
                                    .child(Input::new(&self.side.search).cleanable(true)),
                            )
                            .when(self.projects.is_empty(), |el| {
                                el.child(
                                    div()
                                        .mx_3()
                                        .p_3()
                                        .rounded_md()
                                        .border_1()
                                        .border_dashed()
                                        .border_color(theme::border())
                                        .text_sm()
                                        .text_color(theme::muted())
                                        .child(t("shell.sidebar.addFirst")),
                                )
                            })
                            .children(sections.into_iter().filter(|(_, ps)| !ps.is_empty()).map(
                                |(s, ps)| {
                                    let n = ps.len();
                                    v_flex()
                                        .child(self.heading(s.name.clone(), Some(n)))
                                        .children(ps.into_iter().map(|p| self.project_row(p, cx)))
                                },
                            ))
                            .when(!rest.is_empty() && !self.side.sections.is_empty(), |el| {
                                el.child(self.heading(t("v2.sidebar.noSection"), None))
                            })
                            .children(rest.into_iter().map(|p| self.project_row(p, cx)))
                            .when(!idle.is_empty(), |el| {
                                let n = idle.len();
                                el.child(self.dock(
                                    "dock-idle",
                                    format!(
                                        "{} · {n} · {}",
                                        t("v2.sidebar.idle"),
                                        t("v2.sidebar.idleHint")
                                    ),
                                    idle_open,
                                    cx,
                                    |s| s.idle_open = !s.idle_open,
                                ))
                                .when(idle_open, |el| {
                                    el.children(idle.into_iter().map(|p| self.project_row(p, cx)))
                                })
                            })
                            .when(!hid.is_empty(), |el| {
                                let n = hid.len();
                                el.child(self.dock(
                                    "dock-hidden",
                                    format!("{} · {n}", t("v2.sidebar.hidden")),
                                    hidden_open,
                                    cx,
                                    |s| s.hidden_open = !s.hidden_open,
                                ))
                                .when(hidden_open, |el| {
                                    el.children(
                                        hid.into_iter().map(|p| {
                                            div().opacity(0.6).child(self.project_row(p, cx))
                                        }),
                                    )
                                })
                            })
                            .when(nothing, |el| {
                                el.child(
                                    div()
                                        .px_4()
                                        .py_3()
                                        .text_sm()
                                        .text_color(theme::faint())
                                        .child(t("v2.sidebar.nothingMatches")),
                                )
                            })
                            .when_some(self.error.clone(), |el, e| {
                                el.child(div().p_4().text_xs().text_color(theme::error()).child(e))
                            }),
                    ),
            )
    }

    fn last(&self, project: &str) -> String {
        self.side
            .activity
            .get(project)
            .and_then(|a| a.last_activity.clone())
            .unwrap_or_default()
    }

    fn heading(&self, label: String, count: Option<usize>) -> impl IntoElement {
        h_flex()
            .px_4()
            .pt_3()
            .pb_1()
            .gap_2()
            .text_xs()
            .text_color(theme::faint())
            .child(label.to_uppercase())
            .when_some(count, |el, n| el.child(div().child(n.to_string())))
    }

    fn dock(
        &self,
        id: &'static str,
        label: String,
        open: bool,
        cx: &mut Context<Self>,
        toggle: fn(&mut Sidebar),
    ) -> impl IntoElement {
        h_flex()
            .id(id)
            .mt_2()
            .px_4()
            .py_1()
            .gap_2()
            .cursor_pointer()
            .text_xs()
            .text_color(theme::faint())
            .hover(|el| el.text_color(theme::muted()))
            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                toggle(&mut this.side);
                cx.notify();
            }))
            .child(if open { "▾" } else { "▸" })
            .child(label)
    }

    fn home_row(&self, cx: &mut Context<Self>) -> AnyElement {
        let selected = self.project.as_deref() == Some(HOME_PROJECT);
        let line = self
            .line(HOME_PROJECT)
            .unwrap_or_else(|| t("v2.sidebar.homeHint"));
        self.row("home", selected, cx)
            .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                this.select_project(HOME_PROJECT.into(), window, cx)
            }))
            .child(
                div()
                    .flex_none()
                    .size_6()
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_md()
                    .bg(theme::raised())
                    .text_color(theme::muted())
                    .child("⌂"),
            )
            .child(
                v_flex()
                    .min_w_0()
                    .flex_1()
                    .child(
                        div()
                            .text_sm()
                            .text_color(theme::fg())
                            .child(t("shell.nav.home")),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .truncate()
                            .child(line),
                    ),
            )
            .into_any_element()
    }

    fn row(&self, id: &str, selected: bool, _: &mut Context<Self>) -> Stateful<Div> {
        h_flex()
            .id(SharedString::from(format!("row-{id}")))
            .mx_2()
            .px_2()
            .py_1p5()
            .gap_2()
            .rounded_md()
            .cursor_pointer()
            .when(selected, |el| el.bg(theme::hover()))
            .hover(|el| el.bg(theme::raised()))
    }

    fn project_row(&self, p: &Project, cx: &mut Context<Self>) -> AnyElement {
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
        let c = self.counts(&p.name);
        let needs = c.asks + c.failed;
        let activity = self.side.activity.get(&p.name);
        let question = self
            .side
            .questions
            .get(&p.name)
            .and_then(|q| q.first())
            .map(|q| q.question.clone());
        let line = question
            .clone()
            .or_else(|| self.line(&p.name))
            .unwrap_or_else(|| t("v2.sidebar.leadHint"));
        let right = if needs > 0 {
            div()
                .px_1p5()
                .rounded_sm()
                .bg(theme::starting().opacity(0.15))
                .text_color(theme::starting())
                .child(tv("v2.attention.needs", &[("count", needs.into())]))
        } else {
            div().text_color(theme::faint()).child(
                activity
                    .and_then(|a| a.last_activity.as_deref())
                    .map(ago)
                    .unwrap_or_default(),
            )
        };
        let mut counts: Vec<(String, Hsla)> = Vec::new();
        if c.asks > 0 {
            counts.push((
                tv("v2.count.needs", &[("count", c.asks.into())]),
                theme::starting(),
            ));
        }
        if c.failed > 0 {
            counts.push((
                tv("v2.count.error", &[("count", c.failed.into())]),
                theme::error(),
            ));
        }
        if c.working > 0 {
            counts.push((
                tv("v2.count.working", &[("count", c.working.into())]),
                theme::accent(),
            ));
        }
        self.row(&p.name, selected, cx)
            .items_start()
            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                this.select_project(name.clone(), window, cx)
            }))
            .child(
                div()
                    .relative()
                    .flex_none()
                    .size_6()
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_md()
                    .bg(theme::raised())
                    .text_xs()
                    .text_color(theme::muted())
                    .child(initial)
                    .when(needs > 0, |el| {
                        el.child(
                            div()
                                .absolute()
                                .top(px(-2.))
                                .right(px(-2.))
                                .size_2()
                                .rounded_full()
                                .bg(theme::starting()),
                        )
                    }),
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
                                    .flex_1()
                                    .min_w_0()
                                    .text_sm()
                                    .truncate()
                                    .text_color(if selected {
                                        theme::fg()
                                    } else {
                                        theme::muted()
                                    })
                                    .child(display),
                            )
                            .when(c.working > 0, |el| {
                                el.child(div().text_xs().text_color(theme::accent()).child("●"))
                            })
                            .child(right.flex_none().text_xs()),
                    )
                    .child(
                        div()
                            .text_xs()
                            .truncate()
                            .text_color(if question.is_some() {
                                theme::starting()
                            } else {
                                theme::faint()
                            })
                            .child(line),
                    )
                    .when(!counts.is_empty(), |el| {
                        el.child(
                            h_flex().gap_2().text_xs().children(
                                counts
                                    .into_iter()
                                    .map(|(s, c)| div().text_color(c).child(s)),
                            ),
                        )
                    }),
            )
            .into_any_element()
    }
}

/// ago is how long since an RFC 3339 time, as the sidebar writes it: now, 5m,
/// 3h, 2d.
fn ago(time: &str) -> String {
    let Ok(at) =
        humantime::parse_rfc3339_weak(time.split('.').next().unwrap_or(time).trim_end_matches('Z'))
    else {
        return String::new();
    };
    let secs = SystemTime::now()
        .duration_since(at)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    match secs {
        0..60 => t("panel.list.agoNow"),
        60..3600 => tv(
            "panel.list.agoMinutes",
            &[("n", ((secs / 60) as usize).into())],
        ),
        3600..86400 => tv(
            "panel.list.agoHours",
            &[("n", ((secs / 3600) as usize).into())],
        ),
        _ => tv(
            "panel.list.agoDays",
            &[("n", ((secs / 86400) as usize).into())],
        ),
    }
}
