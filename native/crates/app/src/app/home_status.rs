//! Home's Status tab (components/HomeView.tsx, via v2/PagesV2.tsx
//! HomeStatusV2): the summary line with the project filter, Add project and
//! New agent beside it; the fleet's status chips; the host's load strip;
//! every agent as AllAgentsPanel's dense list; and the recent jobs.
use std::collections::HashSet;
use std::rc::Rc;

use agentbox_api::{Agent, HostUsage, Job};
use agentbox_i18n::{t, tv};
use gpui::Anchor;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::agent_status::{self as st, St, Tone};
use super::controls::{self, Choice};
use super::settings::defaults::time_ago;
use crate::files::human_bytes;
use crate::theme;

/// What the tab keeps: the project its list is narrowed to, the recent jobs
/// and its scroll.
#[derive(Default)]
pub(super) struct HomeStatus {
    pub project: String,
    pub jobs: Vec<Job>,
    pub scroll: ScrollHandle,
}

/// short_rate is utils.ts's shortRate: "118M/s", "4.2M/s", "0B/s".
fn short_rate(n: i64) -> String {
    let units = ["B", "K", "M", "G"];
    let (mut v, mut i) = (n as f64, 0);
    while v >= 1024. && i < units.len() - 1 {
        v /= 1024.;
        i += 1;
    }
    if i > 0 && v < 10. {
        format!("{v:.1}{}/s", units[i])
    } else {
        format!("{}{}/s", v.round() as i64, units[i])
    }
}

/// meter_percent is meter.ts's meterPercent.
fn meter_percent(used: f64, total: f64) -> Option<f32> {
    (total > 0. && used.is_finite()).then(|| ((used / total).clamp(0., 1.) * 100.) as f32)
}

/// STALL is utils.ts's stallPressure.
const STALL: f64 = 10.;

/// Mood is state.tsx's useMood, for an Agent.
#[derive(PartialEq, Clone, Copy)]
enum Mood {
    Working,
    Idle,
    Asking,
    Sleeping,
    Error,
}

fn mood(a: &Agent, asking: bool) -> Mood {
    let s = a.state.as_str();
    if s == "initializing" || s == "queued" {
        return Mood::Sleeping;
    }
    if s == "incomplete" || s == "missing" || a.chat == "error" {
        return Mood::Error;
    }
    if s == "running" && a.chat == "running" && a.stalled_since.is_some() {
        return Mood::Error;
    }
    if s != "running" {
        return Mood::Sleeping;
    }
    if a.chat == "waiting" || asking {
        return Mood::Asking;
    }
    if a.chat == "running" || a.chat == "starting" {
        return Mood::Working;
    }
    match a.outcome.as_str() {
        "asking" => Mood::Asking,
        "failed" => Mood::Error,
        _ => Mood::Idle,
    }
}

/// avatar is LiveAgentAvatar at size-10 (state.tsx AgentAvatar): a rounded
/// tile tinted by its mood, the tool's character in it (asleep in grey,
/// with its z's), its state's dot at the corner.
fn avatar(a: &Agent, asking: bool) -> AnyElement {
    let size = 40.;
    let m = mood(a, asking);
    let border = match m {
        Mood::Asking => theme::amber(400).opacity(0.45),
        Mood::Error => theme::rose(400).opacity(0.4),
        _ => theme::line_strong(),
    };
    let from = match m {
        Mood::Working | Mood::Idle => theme::brand(500).opacity(0.2),
        Mood::Asking => theme::amber(400).opacity(0.2),
        Mood::Sleeping => theme::surface_raised(),
        Mood::Error => theme::rose(400).opacity(0.18),
    };
    let sleeping = m == Mood::Sleeping;
    let color = if sleeping || m == Mood::Error {
        theme::muted()
    } else {
        theme::brand(200)
    };
    let z = |right: f32, top: f32, s: f32| {
        div()
            .absolute()
            .right(relative(right))
            .top(relative(top))
            .text_size(px(size * s))
            .line_height(relative(1.))
            .font_weight(FontWeight::SEMIBOLD)
            .text_color(theme::subtle())
            .child("z")
    };
    div()
        .relative()
        .flex_none()
        .size(px(size))
        .flex()
        .items_center()
        .justify_center()
        .rounded_xl()
        .border_1()
        .border_color(border)
        .child(div().absolute().inset_0().rounded_xl().bg(linear_gradient(
            135.,
            linear_color_stop(from, 0.),
            linear_color_stop(from.opacity(0.), if sleeping { 0.5 } else { 0.85 }),
        )))
        .child(
            div()
                .when(sleeping, |el| el.opacity(0.45))
                .child(crate::icons::tool_mark_grey(
                    &a.ai,
                    px((size * 0.58).round()),
                    color,
                    sleeping,
                )),
        )
        .when(sleeping, |el| {
            el.child(z(0.16, 0.30, 0.14))
                .child(z(0.09, 0.17, 0.16))
                .child(z(0.02, 0.04, 0.19))
        })
        .child(
            div()
                .absolute()
                .right(px(-4.))
                .bottom(px(-4.))
                .size(px(14.))
                .rounded_full()
                .border_2()
                .border_color(theme::ink())
                .bg(theme::state_color(&a.state)),
        )
        .into_any_element()
}

/// panel is ui/card.tsx's Panel: `.panel rounded-2xl`.
fn panel() -> Div {
    super::settings::kit::panel(div())
}

/// stat is HomeView's Stat: one cell of the load strip, its bar when
/// percent is known (meter alone draws the empty track), a note under it.
fn stat(
    label: String,
    value: String,
    detail: String,
    percent: Option<f32>,
    meter: bool,
    note: Vec<AnyElement>,
) -> Div {
    let caption = |el: Div| {
        el.text_size(theme::text::CAPTION)
            .line_height(px(16.))
            .text_color(theme::subtle())
    };
    v_flex()
        .flex_1()
        .min_w(px(144.))
        .border_l_1()
        .border_t_1()
        .border_color(theme::line_faint())
        .px_4()
        .py_3()
        .child(caption(div()).child(label.clone()))
        .child(
            h_flex()
                .mt(px(6.))
                .flex_wrap()
                // items-baseline: the detail's baseline on the value's,
                // 3px over its foot (a 20/28 mono over a 12/16 Inter).
                .items_end()
                .gap_x(px(6.))
                .child(
                    h_flex()
                        .flex_none()
                        .font_family(theme::mono())
                        .text_size(theme::text::DISPLAY)
                        .line_height(px(28.))
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::title())
                        // tracking-tight, which gpui's text hasn't: each of
                        // JetBrains Mono's 0.6em cells half a pixel closer.
                        .children(
                            value.chars().map(|c| {
                                div().w(px(11.5)).whitespace_nowrap().child(c.to_string())
                            }),
                        ),
                )
                .child(caption(div().mb(px(3.))).child(detail)),
        )
        .when(percent.is_some() || meter, |el| {
            el.child(
                div()
                    .mt(px(10.))
                    .h_1()
                    .rounded_full()
                    .overflow_hidden()
                    .bg(theme::surface_raised())
                    .when_some(percent, |el, p| {
                        el.child(crate::motion::progress(
                            div().h_full().rounded_full().bg(linear_gradient(
                                90.,
                                linear_color_stop(theme::brand(400), 0.),
                                linear_color_stop(theme::sky(400), 1.),
                            )),
                            // Each meter its own: one id would have
                            // CPU and memory share a spring.
                            SharedString::from(format!("home-progress-fill-{label}")),
                            p / 100.,
                        ))
                    }),
            )
        })
        .when(!note.is_empty(), |el| {
            el.child(caption(div().mt_2()).children(note))
        })
}

/// stalled is HomeView's Stalled: one resource's pressure under its stat.
fn stalled(percent: f64, on: &str) -> AnyElement {
    let bad = percent > STALL;
    h_flex()
        .when(bad, |el| {
            el.font_weight(FontWeight::MEDIUM)
                .text_color(theme::rose(300))
                .child(crate::icons::ic("triangle-alert").size_3().mr_1())
        })
        .child(tv(
            "shell.home.stalled",
            &[
                ("percent", (percent.round() as i64).into()),
                ("on", on.into()),
            ],
        ))
        .into_any_element()
}

/// section_title is HomeView's SectionTitle: mb-3 mt-8 text-base semibold.
fn section_title(label: String, count: Option<usize>) -> Div {
    h_flex()
        .mb_3()
        .mt_8()
        .items_center()
        .text_size(theme::text::HEADING)
        .line_height(px(24.))
        .font_weight(FontWeight::SEMIBOLD)
        .text_color(theme::title())
        .child(label)
        .when_some(count, |el, n| {
            el.child(
                div()
                    .ml(px(6.))
                    .rounded_full()
                    .bg(theme::surface_raised())
                    .px(px(6.))
                    .text_size(theme::text::CAPTION)
                    .line_height(px(16.))
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::subtle())
                    .child(n.to_string()),
            )
        })
}

/// pill is HomeView's StatusPill: neutral, its dot in the status's colour.
fn pill(text: String, tone: Tone) -> AnyElement {
    let dot = match tone {
        Tone::Urgent => theme::amber(400),
        Tone::Error => theme::rose(400),
        Tone::Warning => theme::amber(400).opacity(0.7),
        Tone::Live => theme::sky(400),
        _ => theme::faint(),
    };
    h_flex()
        .flex_none()
        .items_center()
        .gap(px(6.))
        .whitespace_nowrap()
        .rounded_full()
        .border_1()
        .border_color(theme::line())
        .bg(theme::surface_faint())
        .px(px(10.))
        .py_1()
        .text_size(theme::text::CAPTION)
        .line_height(px(16.))
        .font_weight(FontWeight::MEDIUM)
        .text_color(theme::secondary())
        .child(div().flex_none().size(px(6.)).rounded_full().bg(dot))
        .child(text)
        .into_any_element()
}

impl AppView {
    /// render_home_status is HomeView, under Home's header.
    pub(super) fn render_home_status(&self, cx: &mut Context<Self>) -> AnyElement {
        let hs = &self.home_status;
        let all = &self.side.agents;
        let running = all.iter().filter(|a| a.state == "running").count();
        let host: Option<HostUsage> = self.meters.host();
        let asking: HashSet<String> = self
            .side
            .questions
            .values()
            .flatten()
            .map(|q| q.r#ref.clone())
            .collect();
        let review: HashSet<String> = self.side.review.iter().map(|i| i.r#ref.clone()).collect();

        // The summary line, the filter and the two buttons (flex-wrap gap-4).
        let me = cx.entity().downgrade();
        let pick: controls::Pick<str> = Rc::new(move |v: &str, _, cx| {
            let v = v.to_string();
            me.update(cx, |this, cx| {
                this.home_status.project = v;
                cx.notify();
            })
            .ok();
        });
        let mut choices = vec![Choice::new("", t("shell.allMedia.allProjects"))];
        choices.extend(self.projects.iter().map(|p| {
            Choice::new(
                p.name.clone(),
                if p.display_name.is_empty() {
                    p.name.clone()
                } else {
                    p.display_name.clone()
                },
            )
        }));
        // Button, size default: h-9 px-4 text-body, its svg size-4.
        let button = |id: &'static str, icon: &'static str, label: String, primary: bool| {
            h_flex()
                .id(id)
                .flex_none()
                .h_9()
                .px_4()
                .gap_2()
                .items_center()
                .justify_center()
                .rounded_lg()
                .text_size(theme::text::BODY)
                .font_weight(FontWeight::MEDIUM)
                .cursor_pointer()
                .map(|el| {
                    if primary {
                        el.bg(theme::brand(500))
                            .text_color(theme::on_brand())
                            .hover(|s| s.bg(theme::brand(600)))
                            .active(|s| s.bg(theme::brand(700)))
                    } else {
                        el.text_color(theme::muted())
                            .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                    }
                })
                .child(crate::icons::ic(icon).size_4())
                .child(label)
        };
        let head = h_flex()
            .flex_wrap()
            .items_center()
            .gap_4()
            .child(
                div()
                    .text_size(theme::text::BODY)
                    .line_height(px(20.))
                    .text_color(theme::muted())
                    .child(tv(
                        "shell.home.summary",
                        &[
                            ("running", running.into()),
                            ("total", all.len().into()),
                            ("projects", self.projects.len().into()),
                        ],
                    )),
            )
            .child(
                h_flex()
                    .ml_auto()
                    .flex_wrap()
                    .justify_end()
                    .gap_2()
                    .child(div().w(px(224.)).child(controls::field_select(
                        "home-project",
                        choices,
                        &hs.project,
                        false,
                        pick,
                        Anchor::TopRight,
                        None,
                    )))
                    .child(
                        button(
                            "home-add-project",
                            "folder-plus",
                            t("shell.home.addProject"),
                            false,
                        )
                        .on_click(cx.listener(
                            |this, _: &ClickEvent, window, cx| this.open_add_project(window, cx),
                        )),
                    )
                    .child(
                        button("home-new-agent", "plus", t("shell.home.newAgent"), true).on_click(
                            cx.listener(|this, _: &ClickEvent, window, cx| {
                                let project = this
                                    .projects
                                    .first()
                                    .map(|p| p.name.clone())
                                    .unwrap_or_default();
                                this.open_new_agent(project, window, cx)
                            }),
                        ),
                    ),
            );

        // StatusSummary: how many agents are in each state, by urgency.
        let mut counts = [0usize; 5];
        for a in all.iter() {
            let s = st::agent_st(a, asking.contains(&a.r#ref), review.contains(&a.r#ref));
            counts[St::ORDER.iter().position(|o| *o == s).unwrap_or(4)] += 1;
        }
        let summary_keys = [
            ("v2.count.needs", Tone::Urgent),
            ("v2.count.error", Tone::Error),
            ("v2.count.review", Tone::Muted),
            ("v2.count.working", Tone::Live),
            ("v2.count.idle", Tone::Muted),
        ];
        let pills: Vec<AnyElement> = summary_keys
            .iter()
            .zip(counts)
            .filter(|(_, n)| *n > 0)
            .map(|((key, tone), n)| pill(tv(key, &[("count", n.into())]), *tone))
            .collect();

        // The load strip: its cells divided by lines (each cell's left and
        // top border, the strip pulled up and left a pixel under the edge).
        let pressure = host.as_ref().and_then(|h| h.pressure.clone());
        let dash = || "—".to_string();
        let mut cells = vec![stat(
            t("shell.home.agentsRunning"),
            running.to_string(),
            tv("shell.home.ofCount", &[("count", all.len().into())]),
            None,
            false,
            vec![],
        )];
        cells.push(stat(
            t("shell.home.hostCpu"),
            host.as_ref()
                .map_or_else(dash, |h| format!("{}%", h.cpu.round() as i64)),
            host.as_ref()
                .filter(|h| h.cores > 0)
                .map(|h| tv("shell.home.ofCores", &[("count", h.cores.into())]))
                .unwrap_or_default(),
            host.as_ref()
                .filter(|h| h.cores > 0)
                .and_then(|h| meter_percent(h.cpu, 100.)),
            host.is_some(),
            vec![],
        ));
        cells.push(stat(
            t("shell.home.hostMemory"),
            host.as_ref()
                .filter(|h| h.mem_total > 0)
                .map_or_else(dash, |h| human_bytes(h.mem_used)),
            host.as_ref()
                .map(|h| {
                    if h.mem_total > 0 {
                        tv(
                            "shell.home.ofSize",
                            &[("size", human_bytes(h.mem_total).into())],
                        )
                    } else {
                        t("shell.home.notMeasured")
                    }
                })
                .unwrap_or_default(),
            host.as_ref()
                .and_then(|h| meter_percent(h.mem_used as f64, h.mem_total as f64)),
            host.as_ref().is_some_and(|h| h.mem_total > 0),
            pressure
                .as_ref()
                .map(|p| vec![stalled(p.memory_full, "memory")])
                .unwrap_or_default(),
        ));
        let mut disk_note = vec![];
        if let Some(h) = host.as_ref() {
            disk_note.push(
                div()
                    .truncate()
                    .child(tv(
                        "shell.home.diskNote",
                        &[
                            ("read", short_rate(h.disk_read).into()),
                            ("write", short_rate(h.disk_write).into()),
                        ],
                    ))
                    .into_any_element(),
            );
            if let Some(p) = pressure.as_ref() {
                disk_note.push(stalled(p.io_full, "disk"));
            }
        }
        cells.push(stat(
            t("shell.home.hostDisk"),
            host.as_ref()
                .map_or_else(dash, |h| short_rate(h.disk_read + h.disk_write)),
            String::new(),
            None,
            false,
            disk_note,
        ));
        // A pool the host can't measure loses its tile once the host answers.
        if host.as_ref().is_none_or(|h| h.pool_total > 0) {
            cells.push(stat(
                t("shell.home.pool"),
                host.as_ref()
                    .filter(|h| h.pool_total > 0)
                    .map_or_else(dash, |h| human_bytes(h.pool_used)),
                host.as_ref()
                    .filter(|h| h.pool_total > 0)
                    .map(|h| {
                        tv(
                            "shell.home.ofSize",
                            &[("size", human_bytes(h.pool_total).into())],
                        )
                    })
                    .unwrap_or_default(),
                host.as_ref()
                    .and_then(|h| meter_percent(h.pool_used as f64, h.pool_total as f64)),
                false,
                vec![],
            ));
        }
        let strip = panel().mt_6().overflow_hidden().child(
            h_flex()
                .ml(px(-1.))
                .mt(px(-1.))
                .flex_wrap()
                .items_stretch()
                .children(cells),
        );

        let body = v_flex()
            .max_w(px(1152.))
            .px_8()
            .pb_8()
            .pt_2()
            .child(head)
            .when(!all.is_empty(), |el| {
                el.child(
                    h_flex()
                        .mt_4()
                        .min_w_0()
                        .gap_2()
                        .overflow_hidden()
                        .children(pills),
                )
            })
            .child(strip)
            .child(section_title(
                t("shell.home.agents"),
                (!all.is_empty()).then_some(all.len()),
            ))
            .child(self.render_all_agents(&asking, cx))
            .child(section_title(t("shell.home.recentJobs"), None))
            .child(self.render_home_jobs());

        let gutter = hs.scroll.max_offset().y > px(0.5);
        div()
            .relative()
            .size_full()
            .child(
                div()
                    .id("home-status")
                    .size_full()
                    .overflow_y_scroll()
                    .track_scroll(&hs.scroll)
                    .when(gutter, |el| el.pr(controls::SCROLLBAR))
                    .child(body),
            )
            .child(controls::scrollbar(&hs.scroll, gutter))
            .into_any_element()
    }

    /// render_all_agents is AllAgentsPanel: every agent (of the filter's
    /// project), urgent first, as one dense list with its column heads.
    fn render_all_agents(&self, asking: &HashSet<String>, cx: &mut Context<Self>) -> AnyElement {
        let project = &self.home_status.project;
        let mut all: Vec<&Agent> = self
            .side
            .agents
            .iter()
            .filter(|a| project.is_empty() || &a.project == project)
            .collect();
        let tone_order = |a: &Agent| match st::chat_label(a).1 {
            Tone::Urgent => 0,
            Tone::Error => 1,
            Tone::Warning => 2,
            Tone::Live => 3,
            _ => 4,
        };
        all.sort_by(|a, b| {
            st::rank(*a)
                .cmp(&st::rank(*b))
                .then(tone_order(a).cmp(&tone_order(b)))
                .then_with(|| {
                    let (x, y) = (
                        if a.title.is_empty() {
                            &a.name
                        } else {
                            &a.title
                        },
                        if b.title.is_empty() {
                            &b.name
                        } else {
                            &b.title
                        },
                    );
                    x.to_lowercase().cmp(&y.to_lowercase())
                })
        });
        if all.is_empty() {
            return panel()
                .flex()
                .flex_col()
                .items_center()
                .gap_3()
                .px_4()
                .py_10()
                .child(
                    div()
                        .text_size(theme::text::UI)
                        .text_color(theme::muted())
                        .child(t("project.all.empty")),
                )
                .into_any_element();
        }
        // Project only when the rows are from more than one.
        let by_project = all.iter().any(|a| a.project != all[0].project);
        // lg: is the window's width, as Tailwind reads it.
        let lg = controls::viewport().width >= px(1024.);
        let status_w = px(if lg { 176. } else { 124. });
        let project_w = px(if lg { 160. } else { 128. });
        let label_of = |name: &str| {
            self.projects
                .iter()
                .find(|p| p.name == name)
                .map(|p| {
                    if p.display_name.is_empty() {
                        p.name.clone()
                    } else {
                        p.display_name.clone()
                    }
                })
                .unwrap_or_else(|| name.to_string())
        };
        let heads = h_flex()
            .items_center()
            .gap_3()
            .pb(px(6.))
            .pl_4()
            .pr_3()
            .text_size(theme::text::CAPTION)
            .line_height(px(16.))
            .font_weight(FontWeight::MEDIUM)
            .text_color(theme::subtle())
            .child(div().w_10().flex_none())
            .when(by_project, |el| {
                el.child(
                    div()
                        .w(project_w)
                        .flex_none()
                        .child(t("project.all.project")),
                )
            })
            .child(div().flex_1().min_w_0().child(t("project.all.agent")))
            .child(div().w(status_w).flex_none().child(t("project.all.status")))
            .child(div().w_4().flex_none());
        let rows = all.iter().enumerate().map(|(i, a)| {
            let (status, tone) = st::chat_label(*a);
            let quiet = a.state == "stopped" || a.state == "paused";
            let (name, proj) = (a.name.clone(), a.project.clone());
            let p = self.projects.iter().find(|p| p.name == a.project);
            h_flex()
                .id(SharedString::from(format!("home-agent-{}", a.r#ref)))
                .w_full()
                .items_center()
                .gap_3()
                .py_2()
                .pl_4()
                .pr_3()
                .cursor_pointer()
                .when(i > 0, |el| {
                    el.border_t_1().border_color(theme::line_faint())
                })
                .hover(|s| s.bg(theme::surface()))
                .active(|s| s.bg(theme::surface_raised()))
                .when(quiet, |el| el.opacity(0.55))
                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                    this.select_project(proj.clone(), window, cx);
                    this.open_chat(name.clone(), window, cx)
                }))
                .child(avatar(a, asking.contains(&a.r#ref)))
                .when(by_project, |el| {
                    el.child(
                        h_flex()
                            .w(project_w)
                            .flex_none()
                            .gap(px(6.))
                            .items_center()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::subtle())
                            .when_some(p, |el, p| el.child(self.project_icon(p, 16.)))
                            .child(div().truncate().child(label_of(&a.project))),
                    )
                })
                .child(
                    h_flex()
                        .flex_1()
                        .min_w_0()
                        .items_center()
                        .gap_2()
                        .child(
                            div()
                                .truncate()
                                .text_size(theme::text::UI)
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::primary())
                                .child(if a.title.is_empty() {
                                    a.name.clone()
                                } else {
                                    a.title.clone()
                                }),
                        )
                        .when(!a.title.is_empty() && lg, |el| {
                            el.child(
                                div()
                                    .flex_none()
                                    .font_family(theme::mono())
                                    .text_size(theme::text::CAPTION)
                                    .line_height(px(16.))
                                    .text_color(theme::faint())
                                    .child(a.name.clone()),
                            )
                        }),
                )
                .child(
                    h_flex()
                        .w(status_w)
                        .flex_none()
                        .items_center()
                        .gap(px(6.))
                        .text_size(theme::text::CAPTION)
                        .line_height(px(16.))
                        .when(matches!(tone, Tone::Urgent | Tone::Error), |el| {
                            el.child(div().flex_none().size(px(6.)).rounded_full().bg(
                                if tone == Tone::Urgent {
                                    theme::amber(400)
                                } else {
                                    theme::rose(400)
                                },
                            ))
                        })
                        .child(
                            div()
                                .truncate()
                                .map(|el| match tone {
                                    Tone::Urgent => el
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(theme::amber(300)),
                                    Tone::Error => el
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(theme::rose(300)),
                                    Tone::Warning => el
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(theme::amber(300).opacity(0.8)),
                                    // chat-shine, at rest: the subtle under its sweep.
                                    Tone::Live => el
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(theme::subtle()),
                                    _ => el.text_color(theme::subtle()),
                                })
                                .child(status),
                        ),
                )
                .child(
                    crate::icons::ic("chevron-right")
                        .size_4()
                        .flex_none()
                        .text_color(theme::ghost()),
                )
        });
        v_flex()
            .child(heads)
            .child(panel().overflow_hidden().children(rows))
            .into_any_element()
    }

    /// render_home_jobs is HomeView's Recent jobs: the newest six.
    fn render_home_jobs(&self) -> AnyElement {
        let jobs = &self.home_status.jobs;
        panel()
            .overflow_hidden()
            .children(jobs.iter().take(6).enumerate().map(|(i, j)| {
                h_flex()
                    .items_center()
                    .gap_3()
                    .px_4()
                    .py(px(10.))
                    .text_size(theme::text::UI)
                    .when(i > 0, |el| {
                        el.border_t_1().border_color(theme::line_faint())
                    })
                    .child(super::foot::status_badge(&j.status))
                    .child(
                        div()
                            .flex_none()
                            .text_color(theme::secondary())
                            .child(j.kind.clone()),
                    )
                    .child(
                        div()
                            .truncate()
                            .text_color(theme::subtle())
                            .child(j.target.clone()),
                    )
                    .child(
                        div()
                            .ml_auto()
                            .flex_none()
                            .text_size(theme::text::CAPTION)
                            .line_height(px(16.))
                            .text_color(theme::subtle())
                            .child(time_ago(&j.created_at)),
                    )
            }))
            .when(jobs.is_empty(), |el| {
                el.child(
                    div()
                        .px_4()
                        .py_6()
                        .text_center()
                        .text_size(theme::text::UI)
                        .text_color(theme::subtle())
                        .child(t("shell.home.noJobs")),
                )
            })
            .into_any_element()
    }
}
