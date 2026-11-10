//! Work (desktop/src/renderer/components/WorkView.tsx): every piece of work
//! agents did, live and past, newest first, grouped by day, of one project
//! or every one (GET /v1/work). Filters (to review, by pull request) and a
//! search narrow it; a row opens the review on that agent, walking on through
//! the others waiting when it waits too.
use std::collections::HashMap;
use std::rc::Rc;
use std::time::Duration;

use agentbox_api::{Client, Project, WorkItem, WorkPage};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::AppView;
use super::controls::{self, Choice};
use super::recap::{RecapEvent, RecapView};
use super::review::{ReviewEvent, ReviewView};
use crate::theme;

/// The daemon's filters (api.WorkFilter*), "" being everything.
const FILTERS: &[(&str, &str)] = &[
    ("", "work.filter.all"),
    ("review", "work.filter.review"),
    ("open", "work.filter.open"),
    ("merged", "work.filter.merged"),
    ("none", "work.filter.none"),
    ("closed", "work.filter.closed"),
];

pub enum WorkEvent {
    /// Open the review on an item; queue is the walk through what waits,
    /// empty when the item is only looked at.
    Open(WorkItem, Vec<WorkItem>),
}

pub struct WorkView {
    client: Client,
    projects: Vec<Project>,
    /// The project shown, "" for every one.
    project: String,
    filter: String,
    search: Entity<InputState>,
    q: String,
    page: Option<WorkPage>,
    error: Option<SharedString>,
    loading: bool,
    /// Agents whose state keeps their work out of "to review" (needs, error,
    /// working), as the sidebar counts them (lib/agentCounts.ts workStatus).
    held: HashMap<String, &'static str>,
    _debounce: Option<Task<()>>,
    _sub: Subscription,
    /// Your week, open over the page.
    recap: Option<(Entity<RecapView>, Subscription)>,
}

impl EventEmitter<WorkEvent> for WorkView {}

impl WorkView {
    pub fn new(
        client: Client,
        project: String,
        filter: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        // Its placeholder is drawn under it, in faint (render).
        let search = cx.new(|cx| InputState::new(window, cx));
        let sub = cx.subscribe(&search, |this: &mut Self, s, ev: &InputEvent, cx| {
            if !matches!(ev, InputEvent::Change) {
                return;
            }
            // Read when the typing pauses.
            let q = s.read(cx).value().trim().to_string();
            this._debounce = Some(cx.spawn(async move |this, cx| {
                cx.background_executor()
                    .timer(Duration::from_millis(250))
                    .await;
                this.update(cx, |this, cx| {
                    if this.q != q {
                        this.q = q;
                        this.load(cx);
                    }
                })
                .ok();
            }));
        });
        let mut this = Self {
            client,
            projects: Vec::new(),
            project,
            filter,
            search,
            q: String::new(),
            page: None,
            error: None,
            loading: false,
            held: HashMap::new(),
            _debounce: None,
            _sub: sub,
            recap: None,
        };
        this.load(cx);
        this
    }

    pub fn set_projects(&mut self, projects: Vec<Project>, cx: &mut Context<Self>) {
        if self.projects != projects {
            self.projects = projects;
            cx.notify();
        }
    }

    pub fn set_held(&mut self, held: HashMap<String, &'static str>, cx: &mut Context<Self>) {
        if self.held != held {
            self.held = held;
            cx.notify();
        }
    }

    /// project is the project whose work is shown, "" for every one's.
    pub fn project(&self) -> &str {
        &self.project
    }

    /// open_on shows another project or filter, as the sidebar's buttons ask.
    pub fn open_on(&mut self, project: String, filter: String, cx: &mut Context<Self>) {
        self.project = project;
        self.filter = filter;
        self.load(cx);
    }

    /// load reads the page as the filters have it now.
    pub fn load(&mut self, cx: &mut Context<Self>) {
        self.fetch(None, cx);
    }

    /// load_more reads the page after the ones shown, adding it to them.
    fn load_more(&mut self, cx: &mut Context<Self>) {
        if let Some(next) = self.page.as_ref().map(|p| p.next.clone())
            && !next.is_empty()
            && !self.loading
        {
            self.fetch(Some(next), cx);
        }
    }

    fn fetch(&mut self, cursor: Option<String>, cx: &mut Context<Self>) {
        let mut query = vec!["limit=200".to_string()];
        if let Some(c) = &cursor {
            query.push(format!("cursor={}", enc(c)));
        }
        if !self.filter.is_empty() {
            query.push(format!("filter={}", self.filter));
        }
        if !self.q.is_empty() {
            query.push(format!("q={}", enc(&self.q)));
        }
        let base = if self.project.is_empty() {
            "/v1/work".to_string()
        } else {
            format!("/v1/projects/{}/work", self.project)
        };
        let path = format!("{base}?{}", query.join("&"));
        let client = self.client.clone();
        self.loading = true;
        cx.spawn(async move |this, cx| {
            let res = client.get::<WorkPage>(&path).await;
            this.update(cx, |this, cx| {
                this.loading = false;
                match res {
                    Ok(mut p) => {
                        if cursor.is_some()
                            && let Some(old) = this.page.take()
                        {
                            let mut items = old.items;
                            items.append(&mut p.items);
                            p.items = items;
                            p.counts = old.counts;
                        }
                        this.page = Some(p);
                        this.error = None;
                    }
                    Err(e) => this.error = Some(e.to_string().into()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// status is a row's status as the counts read it: to review only while
    /// the agent isn't asking, failing or at work again.
    fn status(&self, w: &WorkItem) -> String {
        if w.status != "review" {
            return w.status.clone();
        }
        self.held
            .get(&w.item.r#ref)
            .map_or("review".into(), |s| s.to_string())
    }

    fn items(&self) -> Vec<WorkItem> {
        let all = self
            .page
            .as_ref()
            .map(|p| p.items.clone())
            .unwrap_or_default();
        if self.filter == "review" {
            all.into_iter()
                .filter(|w| self.status(w) == "review")
                .collect()
        } else {
            all
        }
    }

    fn open(&mut self, w: WorkItem, cx: &mut Context<Self>) {
        let queue = if self.status(&w) == "review" {
            self.items()
                .into_iter()
                .filter(|i| self.status(i) == "review")
                .collect()
        } else {
            Vec::new()
        };
        cx.emit(WorkEvent::Open(w, queue));
    }

    fn open_recap(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let view = cx.new(|cx| RecapView::new(client, window, cx));
        let sub = cx.subscribe(&view, |this: &mut Self, _, ev: &RecapEvent, cx| match ev {
            RecapEvent::Close => {
                this.recap = None;
                cx.notify();
            }
        });
        self.recap = Some((view, sub));
        cx.notify();
    }

    fn project_label(&self, name: &str) -> String {
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
    }

    fn render_filters(&self, cx: &mut Context<Self>) -> AnyElement {
        let counts = self
            .page
            .as_ref()
            .map(|p| p.counts.clone())
            .unwrap_or_default();
        let review = self.page.as_ref().map(|p| {
            p.items
                .iter()
                .filter(|w| self.status(w) == "review")
                .count() as i64
        });
        h_flex()
            .min_w_0()
            .flex_wrap()
            .items_center()
            .p(px(2.))
            .gap(px(2.))
            .rounded_lg()
            .border_1()
            .border_color(theme::line())
            .bg(theme::surface_faint())
            .children(FILTERS.iter().map(|(f, key)| {
                let on = self.filter == *f;
                let n = if *f == "review" && self.filter.is_empty() {
                    review.or(counts.get(*f).copied())
                } else {
                    counts.get(*f).copied()
                };
                let f = f.to_string();
                h_flex()
                    .id(SharedString::from(format!("work-filter-{f}")))
                    .h(px(28.))
                    .gap_1p5()
                    .px_2p5()
                    .rounded_md()
                    .text_size(theme::text::UI)
                    .font_weight(FontWeight::MEDIUM)
                    .cursor_pointer()
                    .when(on, |el| {
                        el.bg(theme::surface_strong()).text_color(theme::title())
                    })
                    .when(!on, |el| {
                        el.text_color(theme::subtle())
                            .hover(|s| s.text_color(theme::primary()))
                            .active(|s| s.opacity(0.7))
                    })
                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.filter = f.clone();
                        this.load(cx);
                    }))
                    .child(t(key))
                    .when_some(n, |el, n| {
                        el.child(
                            div()
                                .text_color(theme::subtle())
                                .font_features(theme::tabular_features())
                                .child(n.to_string()),
                        )
                    })
            }))
            .into_any_element()
    }

    fn render_row(&self, w: &WorkItem, cx: &mut Context<Self>) -> AnyElement {
        let i = &w.item;
        let status = self.status(w);
        let outcome = match &w.report {
            Some(r) if !r.status.is_empty() => r.status.clone(),
            _ if w.status == "working" => "working".into(),
            _ => String::new(),
        };
        let name = if i.title.is_empty() {
            i.agent.clone()
        } else {
            i.title.clone()
        };
        let mut badges: Vec<AnyElement> = Vec::new();
        if status == "review" {
            badges.push(tag(t("work.toReview"), Tone::Info));
        }
        match status.as_str() {
            "needs" => badges.push(tag(t("v2.st.needs"), Tone::Warning)),
            "error" => badges.push(tag(t("v2.st.error"), Tone::Danger)),
            "working" => badges.push(tag(t("v2.st.working"), Tone::Plain)),
            _ => {}
        }
        if w.status == "reviewed" {
            badges.push(
                div()
                    .flex_none()
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::subtle())
                    .child(t(match i.verdict.as_str() {
                        "changes" => "work.verdict.changes",
                        "dismissed" => "work.verdict.dismissed",
                        _ => "work.verdict.reviewed",
                    }))
                    .into_any_element(),
            );
        }
        let at_work = matches!(status.as_str(), "working" | "needs" | "error");
        if !outcome.is_empty() && !(outcome == "working" && at_work) {
            badges.push(outcome_badge(&outcome));
        }
        let mut meta: Vec<AnyElement> = Vec::new();
        if self.project.is_empty() {
            meta.push(
                div()
                    .flex_none()
                    .max_w(relative(0.4))
                    .truncate()
                    .child(self.project_label(&i.project))
                    .into_any_element(),
            );
        }
        if !i.branch.is_empty() {
            meta.push(
                div()
                    .min_w_0()
                    .truncate()
                    .font_family(crate::theme::mono())
                    .child(crate::icons::lead("git-branch", i.branch.to_string()))
                    .into_any_element(),
            );
        }
        if let Some(c) = i.changes.as_ref().filter(|c| c.files > 0) {
            meta.push(
                h_flex()
                    .flex_none()
                    .gap_1()
                    .font_family(crate::theme::mono())
                    .child(tv("work.files", &[("count", c.files.into())]))
                    .child(
                        div()
                            .text_color(theme::emerald(400))
                            .child(format!("+{}", c.insertions)),
                    )
                    .child(
                        div()
                            .text_color(theme::rose(400))
                            .child(format!("−{}", c.deletions)),
                    )
                    .into_any_element(),
            );
        }
        if w.screenshots > 0 {
            meta.push(
                h_flex()
                    .flex_none()
                    .gap_1()
                    .child(
                        crate::icons::ic("images")
                            .size_3p5()
                            .text_color(theme::faint()),
                    )
                    .child(w.screenshots.to_string())
                    .into_any_element(),
            );
        }
        if w.recordings > 0 {
            meta.push(
                h_flex()
                    .flex_none()
                    .gap_1()
                    .child(
                        crate::icons::ic("film")
                            .size_3p5()
                            .text_color(theme::faint()),
                    )
                    .child(w.recordings.to_string())
                    .into_any_element(),
            );
        }
        let open = w.clone();
        h_flex()
            .id(SharedString::from(format!("work-{}", i.r#ref)))
            .items_start()
            .gap_3()
            .min_w_0()
            .px_4()
            .py_2p5()
            .cursor_pointer()
            .hover(|el| el.bg(theme::surface_faint()))
            .active(|s| s.bg(theme::surface()))
            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| this.open(open.clone(), cx)))
            .child(
                div()
                    .flex_none()
                    .mt(px(2.))
                    .when(w.past, |el| el.opacity(0.6))
                    .child(crate::icons::ai_icon(&i.ai, px(16.), theme::primary())),
            )
            .child(
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .gap_1()
                    .child(
                        h_flex()
                            .min_w_0()
                            .flex_wrap()
                            .gap_x_2()
                            .gap_y_1()
                            .child(
                                div()
                                    .min_w_0()
                                    .truncate()
                                    .text_size(theme::text::UI)
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .when(i.title.is_empty(), |el| {
                                        el.font_family(crate::theme::mono())
                                    })
                                    .child(name),
                            )
                            .when(!i.title.is_empty(), |el| {
                                el.child(
                                    div()
                                        .flex_none()
                                        .text_size(theme::text::CAPTION)
                                        .font_family(crate::theme::mono())
                                        .text_color(theme::subtle())
                                        .child(i.agent.clone()),
                                )
                            })
                            .children(badges),
                    )
                    .child(
                        h_flex()
                            .min_w_0()
                            .flex_wrap()
                            .gap_x_2p5()
                            .gap_y_1()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::subtle())
                            .children(meta),
                    ),
            )
            .child(match i.pr.as_ref().filter(|p| p.number > 0) {
                // PrChip className="mt-0.5"
                Some(pr) => div()
                    .flex_none()
                    .mt_0p5()
                    .child(pr_chip(pr))
                    .into_any_element(),
                None => div()
                    .flex_none()
                    .mt_0p5()
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::faint())
                    .child(t("work.noPr"))
                    .into_any_element(),
            })
            .child(
                div()
                    .flex_none()
                    .mt_0p5()
                    .whitespace_nowrap()
                    .text_right()
                    .font_features(theme::tabular_features())
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::subtle())
                    .child(clock_time(&w.at)),
            )
            .into_any_element()
    }
}

impl Render for WorkView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let items = self.items();
        // By day, newest first, as they come.
        let mut days: Vec<(String, Vec<WorkItem>)> = Vec::new();
        for w in items {
            let day = day_of(&w.at);
            match days.last_mut() {
                Some((d, list)) if *d == day => list.push(w),
                _ => days.push((day, vec![w])),
            }
        }
        let empty = days.is_empty();
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
        let me = cx.entity().downgrade();
        let pick: controls::Pick<str> = Rc::new(move |v: &str, _, cx| {
            let v = v.to_string();
            me.update(cx, |this, cx| {
                this.project = v;
                this.load(cx);
            })
            .ok();
        });
        let recap = self.recap.as_ref().map(|(v, _)| v.clone());
        div()
            .relative()
            .size_full()
            .child(
                div().id("work-page").size_full().overflow_y_scroll().child(
                    v_flex()
                        .w_full()
                        .max_w(px(1152.)) // max-w-6xl
                        .mx_auto()
                        .px_8()
                        .py_7()
                        .gap_3()
                        .child(
                            h_flex()
                                .flex_wrap()
                                .items_end()
                                .gap_3()
                                .mb_2()
                                .child(
                                    v_flex()
                                        .min_w_0()
                                        .gap_1()
                                        .child(
                                            div()
                                                .text_size(theme::text::DISPLAY)
                                                .font_weight(FontWeight::SEMIBOLD)
                                                .text_color(theme::title())
                                                .child(t("work.title")),
                                        )
                                        .child(
                                            div()
                                                .text_size(theme::text::BODY)
                                                .line_height(px(20.))
                                                .text_color(theme::muted())
                                                .child(t("work.description")),
                                        ),
                                )
                                .child(div().flex_1())
                                // ml-auto flex items-center gap-2
                                .child(
                                    h_flex()
                                        .gap_2()
                                        .child(
                                            h_flex()
                                                .id("work-recap")
                                                .h(px(36.))
                                                .px_3()
                                                .gap_1p5()
                                                .rounded_xl()
                                                .border_1()
                                                .border_color(theme::brand(500).opacity(0.3))
                                                .bg(gpui::linear_gradient(
                                                    180.,
                                                    gpui::linear_color_stop(
                                                        theme::brand(500).opacity(0.15),
                                                        0.,
                                                    ),
                                                    gpui::linear_color_stop(
                                                        theme::brand(600).opacity(0.1),
                                                        1.,
                                                    ),
                                                ))
                                                .cursor_pointer()
                                                .hover(|el| {
                                                    el.border_color(theme::brand(400).opacity(0.5))
                                                        .bg(theme::brand(500).opacity(0.25))
                                                })
                                                .text_size(theme::text::UI)
                                                .font_weight(FontWeight::MEDIUM)
                                                .text_color(theme::primary())
                                                .on_click(cx.listener(
                                                    |this, _: &ClickEvent, window, cx| {
                                                        this.open_recap(window, cx)
                                                    },
                                                ))
                                                .child(
                                                    crate::icons::ic("sparkles")
                                                        .size_3p5()
                                                        .text_color(theme::brand(400)),
                                                )
                                                .child(t("recap.open")),
                                        )
                                        // ProjectFilter: a Select, w-56.
                                        .child(div().w(px(224.)).child(controls::field_select(
                                            "work-project",
                                            choices,
                                            &self.project,
                                            false,
                                            pick,
                                            Anchor::TopRight,
                                            None,
                                        ))),
                                ),
                        )
                        .child(
                            h_flex()
                                .min_w_0()
                                .flex_wrap()
                                .items_center()
                                .gap_2()
                                .child(self.render_filters(cx))
                                .child(
                                    // ui/input.tsx's Input, h-9 pl-8, the Search
                                    // icon at left-3.
                                    h_flex()
                                        .flex_1()
                                        .min_w(px(192.))
                                        .h_9()
                                        .pl(px(11.))
                                        .pr_3()
                                        .gap(px(7.))
                                        .rounded_lg()
                                        .border_1()
                                        .border_color(theme::line_strong())
                                        .bg(theme::sunken())
                                        .text_size(theme::text::BODY)
                                        .child(
                                            crate::icons::ic("search")
                                                .size_3p5()
                                                .flex_none()
                                                .text_color(theme::subtle()),
                                        )
                                        .child(
                                            div()
                                                .relative()
                                                .flex_1()
                                                .min_w_0()
                                                // placeholder:text-faint
                                                .when(
                                                    self.search.read(cx).value().is_empty(),
                                                    |el| {
                                                        el.child(
                                                            div()
                                                                .absolute()
                                                                .inset_0()
                                                                .flex()
                                                                .items_center()
                                                                .truncate()
                                                                .text_color(theme::faint())
                                                                .child(t("work.search")),
                                                        )
                                                    },
                                                )
                                                .child(
                                                    Input::new(&self.search)
                                                        .appearance(false)
                                                        .with_size(gpui_component::Size::Size(px(
                                                            14. / 0.875,
                                                        )))
                                                        .px_0()
                                                        .cleanable(true),
                                                ),
                                        ),
                                )
                                .when(self.loading && self.page.is_some(), |el| {
                                    el.child(
                                        div()
                                            .text_size(theme::text::CAPTION)
                                            .text_color(theme::subtle())
                                            .child("…"),
                                    )
                                }),
                        )
                        .child(
                            v_flex()
                                .min_w_0()
                                .rounded(theme::radius::XXL)
                                .border_1()
                                .border_color(theme::line())
                                .bg(theme::linear_panel())
                                .shadow(theme::shadow_card())
                                .overflow_hidden()
                                .map(|el| match (&self.error, &self.page) {
                                    (Some(e), _) => el.child(
                                        div()
                                            .p_4()
                                            .child(controls::note_in(e.clone(), theme::rose(300))),
                                    ),
                                    (None, None) => el.children(
                                        [0.33f32, 0.5, 0.25, 0.42, 0.3]
                                            .into_iter()
                                            .enumerate()
                                            .map(|(n, width)| {
                                                h_flex()
                                                    .gap_3()
                                                    .px_4()
                                                    .py_3()
                                                    .when(n > 0, |el| {
                                                        el.border_t_1()
                                                            .border_color(theme::line_faint())
                                                    })
                                                    .child(crate::motion::skeleton(
                                                        ElementId::Name(
                                                            format!("work-skel-a-{n}").into(),
                                                        ),
                                                        relative(width),
                                                        px(14.),
                                                    ))
                                                    .child(div().flex_1())
                                                    .child(crate::motion::skeleton(
                                                        ElementId::Name(
                                                            format!("work-skel-b-{n}").into(),
                                                        ),
                                                        px(56.),
                                                        px(12.),
                                                    ))
                                            }),
                                    ),
                                    (None, Some(_)) if empty => el.child(
                                        v_flex()
                                            .items_center()
                                            .gap_2()
                                            .px_4()
                                            .py_8()
                                            .text_size(theme::text::UI)
                                            .text_color(theme::subtle())
                                            .child(
                                                crate::icons::ic("briefcase")
                                                    .size_5()
                                                    .text_color(theme::faint()),
                                            )
                                            .child(
                                                if self.q.is_empty() && self.filter.is_empty() {
                                                    t("work.empty")
                                                } else {
                                                    t("work.noMatch")
                                                },
                                            ),
                                    ),
                                    (None, Some(_)) => {
                                        el.children(days.into_iter().map(|(day, list)| {
                                            v_flex()
                                                .child(
                                                    div()
                                                        .px_4()
                                                        .py_1p5()
                                                        .border_b_1()
                                                        .border_color(theme::line_faint())
                                                        .bg(theme::surface_faint())
                                                        // text-caption font-semibold uppercase tracking-wide text-subtle
                                                        .text_size(theme::text::CAPTION)
                                                        .font_weight(FontWeight::SEMIBOLD)
                                                        .text_color(theme::subtle())
                                                        .child(day_label(&day).to_uppercase()),
                                                )
                                                .children(list.iter().enumerate().map(|(n, w)| {
                                                    crate::motion::enter(
                                                        // ul divide-y: a line between rows only.
                                                        div()
                                                            .when(n > 0, |el| {
                                                                el.border_t_1().border_color(
                                                                    theme::line_faint(),
                                                                )
                                                            })
                                                            .child(self.render_row(w, cx)),
                                                        SharedString::from(format!(
                                                            "work-in-{}",
                                                            w.item.r#ref
                                                        )),
                                                        n,
                                                    )
                                                }))
                                        }))
                                    }
                                }),
                        )
                        .when(
                            self.error.is_none()
                                && self.page.as_ref().is_some_and(|p| !p.next.is_empty()),
                            |el| {
                                el.child(h_flex().justify_center().child(controls::button(
                                    "work-more",
                                    if self.loading {
                                        t("agent.mediaTab.loadingMore")
                                    } else {
                                        t("common.more")
                                    },
                                    controls::Btn::Ghost,
                                    self.loading,
                                    cx.listener(|this, _: &ClickEvent, _, cx| this.load_more(cx)),
                                )))
                            },
                        ),
                ),
            )
            .children(recap)
    }
}

/// outcome_badge is what came of the work, as its report says (OutcomeBadge).
pub fn outcome_badge(outcome: &str) -> AnyElement {
    let key = format!("work.outcome.{outcome}");
    let label = if agentbox_i18n::has(&key) {
        t(&key)
    } else {
        outcome.to_string()
    };
    let tone = match outcome {
        "done" => Tone::Success,
        "partial" | "blocked" => Tone::Warning,
        "failed" => Tone::Danger,
        _ => Tone::Plain,
    };
    tag(label, tone)
}

/// Tone is a Badge's variant (ui/badge.tsx).
#[derive(Clone, Copy)]
pub(super) enum Tone {
    Plain,
    Success,
    Warning,
    Danger,
    Info,
    Brand,
}

/// tag is Electron's Badge: a caption pill, its tone's tint, text and inset
/// ring (bg-emerald-400/10 text-emerald-300 ring-emerald-400/25 and so on).
pub(super) fn tag(text: impl Into<SharedString>, tone: Tone) -> AnyElement {
    let (bg, fg, ring) = match tone {
        Tone::Plain => (theme::surface(), theme::tertiary(), theme::line_strong()),
        Tone::Success => (
            theme::emerald(400).opacity(0.1),
            theme::emerald(300),
            theme::emerald(400).opacity(0.25),
        ),
        Tone::Warning => (
            theme::amber(400).opacity(0.1),
            theme::amber(200),
            theme::amber(400).opacity(0.25),
        ),
        Tone::Danger => (
            theme::rose(400).opacity(0.1),
            theme::rose(300),
            theme::rose(400).opacity(0.25),
        ),
        Tone::Info => (
            theme::sky(400).opacity(0.1),
            theme::sky(300),
            theme::sky(400).opacity(0.25),
        ),
        Tone::Brand => (
            theme::brand(500).opacity(0.15),
            theme::brand(300),
            theme::brand(400).opacity(0.3),
        ),
    };
    h_flex()
        .flex_none()
        .gap_1p5()
        .px_2()
        .py(px(1.))
        .rounded_full()
        .border_1()
        .border_color(ring)
        .bg(bg)
        .text_size(theme::text::CAPTION)
        .font_weight(FontWeight::MEDIUM)
        .text_color(fg)
        .child(crate::icons::glyph(text.into()))
        .into_any_element()
}

/// pr_chip is a pull request as a small pill (PrChip), its id by its number.
pub fn pr_chip(pr: &agentbox_api::PullRequest) -> AnyElement {
    super::pulls::pr_chip(("pr-chip", pr.number as u64), pr)
}

fn local(time: &str) -> Option<chrono::DateTime<chrono::Local>> {
    chrono::DateTime::parse_from_rfc3339(time)
        .ok()
        .map(|t| t.with_timezone(&chrono::Local))
}

/// day_of is the local day a time falls on, YYYY-MM-DD.
fn day_of(time: &str) -> String {
    local(time).map_or_else(String::new, |t| t.format("%Y-%m-%d").to_string())
}

/// day_label names a day: today, yesterday, or its date.
fn day_label(day: &str) -> String {
    let today = chrono::Local::now().date_naive();
    match chrono::NaiveDate::parse_from_str(day, "%Y-%m-%d") {
        Ok(d) => crate::dates::day_label(d, today),
        _ => day.to_string(),
    }
}

/// clock_time is WorkView's clockTime, toLocaleTimeString's 2-digit hour
/// and minute: 04:28 PM in English, 16:28 in Portuguese.
fn clock_time(time: &str) -> String {
    let fmt = if agentbox_i18n::language().starts_with("en") {
        "%I:%M %p"
    } else {
        "%H:%M"
    };
    local(time).map_or_else(String::new, |t| t.format(fmt).to_string())
}

/// enc escapes a query value.
fn enc(s: &str) -> String {
    s.bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                (b as char).to_string()
            }
            _ => format!("%{b:02X}"),
        })
        .collect()
}

impl AppView {
    /// open_work shows the Work page, of a project ("" for every one) on a filter.
    pub(super) fn open_work(
        &mut self,
        project: String,
        filter: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.settings = false;
        self.side.page = None;
        match &self.work {
            Some(w) => w.update(cx, |w, cx| w.open_on(project, filter, cx)),
            None => {
                let client = self.client.clone();
                let w = cx.new(|cx| WorkView::new(client, project, filter, window, cx));
                let sub = cx.subscribe_in(&w, window, |this, _, ev: &WorkEvent, window, cx| {
                    let WorkEvent::Open(item, queue) = ev;
                    this.open_review(item.clone(), queue.clone(), window, cx);
                });
                self._subs.push(sub);
                self.work = Some(w);
            }
        }
        cx.notify();
    }

    pub(super) fn render_work(&self, cx: &mut Context<Self>) -> AnyElement {
        let Some(w) = self.work.clone() else {
            return div().into_any_element();
        };
        let held = self.held_agents();
        let projects = self.projects.clone();
        w.update(cx, |w, cx| {
            w.set_held(held, cx);
            w.set_projects(projects, cx);
        });
        w.into_any_element()
    }

    /// open_review opens the review on an item, walking through queue.
    pub(super) fn open_review(
        &mut self,
        item: WorkItem,
        queue: Vec<WorkItem>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let client = self.client.clone();
        let review = cx.new(|cx| ReviewView::new(client, item, queue, window, cx));
        self._review_sub = Some(cx.subscribe_in(
            &review,
            window,
            |this, _, ev: &ReviewEvent, window, cx| match ev {
                ReviewEvent::Close => {
                    this.review = None;
                    cx.notify();
                }
                ReviewEvent::Changed => this.reload_review(cx),
                ReviewEvent::Chat(r#ref) => {
                    this.review = None;
                    this.work = None;
                    this.open_agent_ref(r#ref.clone(), window, cx);
                }
            },
        ));
        self.review = Some(review);
        cx.notify();
    }

    /// reload_review reads again what waits for review, for the sidebar's
    /// counts and the Work page.
    pub(super) fn reload_review(&mut self, cx: &mut Context<Self>) {
        self.load_review(cx);
        if let Some(w) = &self.work {
            w.update(cx, |w, cx| w.load(cx));
        }
    }
}
