//! Work (desktop/src/renderer/components/WorkView.tsx): every piece of work
//! agents did, live and past, newest first, grouped by day, of one project
//! or every one (GET /v1/work). Filters (to review, by pull request) and a
//! search narrow it; a row opens the review on that agent, walking on through
//! the others waiting when it waits too.
use std::collections::HashMap;
use std::rc::Rc;
use std::time::{Duration, Instant};

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

/// How many pieces of work a page reads: the first shows at once, and the
/// next is read as the list is scrolled within READ_AHEAD lines of the end of
/// what is loaded.
const PAGE: usize = 50;
const READ_AHEAD: usize = 12;
/// The most a request may ask for (the daemon's reviewPageMax).
const PAGE_MAX: usize = 200;
/// How long after a fresh list arrives its rows still slide in.
const ARRIVE: Duration = Duration::from_millis(500);

/// Line is a line of the page as the list draws it: only the ones in view
/// are built and laid out (gpui's `list`), however much work is loaded.
#[derive(Clone, Debug, PartialEq)]
enum Line {
    /// The title, the filters and the search, which scroll with the list.
    Head,
    /// The list's card when it has no rows: loading, failed or empty.
    Body,
    /// A day's heading; the first one opens the card.
    Day { day: String, first: bool },
    /// A piece of work, `ix` in `shown`; `first` under its day's heading.
    Work { ix: usize, first: bool },
    /// The card's closing edge, and "More" while there is more.
    Foot,
}

/// lines is the page's lines for `shown`, newest first, under their days.
fn lines(shown: &[WorkItem], blank: bool) -> Vec<Line> {
    if blank || shown.is_empty() {
        return vec![Line::Head, Line::Body];
    }
    let mut out = vec![Line::Head];
    let mut last = String::new();
    for (ix, w) in shown.iter().enumerate() {
        let day = day_of(&w.at);
        let first = day != last;
        if first {
            out.push(Line::Day {
                day: day.clone(),
                first: ix == 0,
            });
            last = day;
        }
        out.push(Line::Work { ix, first });
    }
    out.push(Line::Foot);
    out
}

/// What a read of the list is for.
enum Read {
    /// The filters changed: the first page, from the top.
    Fresh,
    /// The page after `cursor`, added to what is shown.
    More(String),
    /// Something changed (a verdict, a merge): as much as is loaded, read
    /// again in place, where the list is scrolled to.
    Again(usize),
}

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
    /// The work listed (the page's, without what `held` keeps out of "to
    /// review") and the lines drawn for it, which `list` scrolls.
    shown: Vec<WorkItem>,
    lines: Vec<Line>,
    list: ListState,
    /// Which read the list is waiting for: an answer to an earlier one (the
    /// filters changed meanwhile) is dropped.
    reading: u64,
    /// When a fresh list arrived, while its rows slide in.
    arrived: Option<Instant>,
    /// AGENTBOX_NATIVE_WORK_BENCH: how much work to load before scrolling
    /// through it, timing the frames (bench.rs).
    bench: Option<usize>,
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
        let list = ListState::new(2, ListAlignment::Top, px(600.));
        // Scrolled near the end of what is loaded: read the page after it.
        let view = cx.entity().downgrade();
        list.set_scroll_handler(move |ev, _, cx| {
            let end = ev.visible_range.end;
            view.update(cx, |this, cx| {
                if end + READ_AHEAD >= this.lines.len() {
                    this.load_more(cx);
                }
            })
            .ok();
        });
        let mut this = Self {
            client,
            projects: Vec::new(),
            project,
            filter,
            search,
            q: String::new(),
            page: None,
            shown: Vec::new(),
            lines: vec![Line::Head, Line::Body],
            list,
            reading: 0,
            arrived: None,
            bench: std::env::var("AGENTBOX_NATIVE_WORK_BENCH")
                .ok()
                .map(|n| n.parse().unwrap_or(500)),
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
            // "To review" lists only what isn't held.
            self.sync(true);
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

    /// load reads the list as the filters have it now, from its top.
    pub fn load(&mut self, cx: &mut Context<Self>) {
        self.fetch(Read::Fresh, cx);
    }

    /// reload reads again what is loaded, where the list is: something
    /// changed that a row may show.
    pub fn reload(&mut self, cx: &mut Context<Self>) {
        match self.page.as_ref().map(|p| p.items.len()) {
            Some(n) if n > 0 => self.fetch(Read::Again(n), cx),
            _ => self.load(cx),
        }
    }

    /// load_more reads the page after the ones shown, adding it to them.
    fn load_more(&mut self, cx: &mut Context<Self>) {
        if let Some(next) = self.page.as_ref().map(|p| p.next.clone())
            && !next.is_empty()
            && !self.loading
        {
            self.fetch(Read::More(next), cx);
        }
    }

    /// path is the list's request for `limit` pieces of work after `cursor`.
    fn path(&self, limit: usize, cursor: Option<&str>) -> String {
        let mut query = vec![format!("limit={limit}")];
        if let Some(c) = cursor {
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
        format!("{base}?{}", query.join("&"))
    }

    fn fetch(&mut self, read: Read, cx: &mut Context<Self>) {
        let client = self.client.clone();
        self.loading = true;
        self.reading += 1;
        let reading = self.reading;
        // A read in place asks for as much as is loaded, as many at once as
        // the daemon gives, each request after the one before it.
        let (want, first) = match &read {
            Read::Fresh => (PAGE, self.path(PAGE, None)),
            Read::More(cursor) => (PAGE, self.path(PAGE, Some(cursor))),
            Read::Again(n) => (*n, self.path((*n).min(PAGE_MAX), None)),
        };
        let this = cx.entity().downgrade();
        cx.spawn(async move |_, cx| {
            let mut res = client.get::<WorkPage>(&first).await;
            while let Ok(p) = &mut res
                && p.items.len() < want
                && !p.next.is_empty()
            {
                let Ok(path) = this.read_with(cx, |this, _| {
                    this.path((want - p.items.len()).min(PAGE_MAX), Some(&p.next))
                }) else {
                    return;
                };
                match client.get::<WorkPage>(&path).await {
                    Ok(mut more) => {
                        p.items.append(&mut more.items);
                        p.next = more.next;
                    }
                    Err(e) => res = Err(e),
                }
            }
            this.update(cx, |this, cx| {
                if this.reading != reading {
                    return; // the filters changed since: theirs is on its way
                }
                this.loading = false;
                match res {
                    Ok(mut p) => {
                        let more = matches!(read, Read::More(_));
                        if more && let Some(old) = this.page.take() {
                            let mut items = old.items;
                            items.append(&mut p.items);
                            p.items = items;
                            p.counts = old.counts;
                            p.total = old.total;
                        }
                        this.page = Some(p);
                        this.error = None;
                        if matches!(read, Read::Fresh) {
                            this.arrived = Some(Instant::now());
                            crate::bench::mark("work-loaded");
                        }
                        this.sync(!matches!(read, Read::Fresh));
                        // The bench loads its fill before it scrolls.
                        if this.bench.is_some_and(|n| this.shown.len() < n) {
                            this.load_more(cx);
                        }
                    }
                    Err(e) => {
                        this.error = Some(e.to_string().into());
                        this.sync(false);
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// sync makes the lines the list draws those of what is loaded now. With
    /// `keep` the list stays where it is scrolled to, and lines added at its
    /// end leave the ones before them as they were measured.
    fn sync(&mut self, keep: bool) {
        self.shown = self.items();
        let blank = self.error.is_some() || self.page.is_none();
        let new = lines(&self.shown, blank);
        let old = std::mem::replace(&mut self.lines, new);
        let (was, now) = (old.len(), self.lines.len());
        if keep && was > 1 && now >= was && old[..was - 1] == self.lines[..was - 1] {
            self.list.splice(was - 1..was, now - (was - 1));
        } else if keep {
            let top = self.list.logical_scroll_top();
            self.list.reset(now);
            self.list.scroll_to(top);
        } else {
            self.list.reset(now);
        }
    }

    /// scroll_bench scrolls the list 24 px a frame for 600 frames, timing the
    /// frames, then reports (bench.rs), once the bench's fill is loaded.
    fn scroll_bench(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        fn tick(
            view: Entity<WorkView>,
            left: usize,
            last: Instant,
            window: &mut Window,
            cx: &mut App,
        ) {
            let now = Instant::now();
            crate::bench::sample("work-scroll-frame-interval", now - last);
            if left == 0 {
                crate::bench::report();
                return;
            }
            view.update(cx, |this, cx| {
                this.list.scroll_by(px(24.));
                cx.notify();
            });
            window.on_next_frame(move |window, cx| tick(view, left - 1, now, window, cx));
        }
        self.bench = None;
        eprintln!("bench work-filled {} loaded", self.shown.len());
        let view = cx.entity();
        let start = Instant::now();
        window.on_next_frame(move |window, cx| tick(view, 600, start, window, cx));
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
            self.shown
                .iter()
                .filter(|i| self.status(i) == "review")
                .cloned()
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

    fn render_filters(&self, view: Entity<Self>) -> AnyElement {
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
                    .on_click({
                        let view = view.clone();
                        move |_: &ClickEvent, _, cx| {
                            view.update(cx, |this, cx| {
                                this.filter = f.clone();
                                this.load(cx);
                            })
                        }
                    })
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

    fn render_row(&self, w: &WorkItem, view: Entity<Self>) -> AnyElement {
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
            .on_click(move |_: &ClickEvent, _, cx| {
                view.update(cx, |this, cx| this.open(open.clone(), cx))
            })
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

impl WorkView {
    /// column is the page's column, as wide as max-w-6xl inside px-8, which
    /// every line stands in.
    fn column(inner: impl IntoElement) -> Div {
        div()
            .w_full()
            .max_w(px(1152.)) // max-w-6xl
            .mx_auto()
            .px_8()
            .child(inner)
    }

    /// card is a slice of the list's panel: its sides, with its top edge on
    /// the line that opens it. The panel's ground eases from top to bottom
    /// over the whole card, which a list of any length doesn't show: each
    /// slice is its top's.
    fn card(open: bool) -> Div {
        div()
            .min_w_0()
            .border_l_1()
            .border_r_1()
            .border_color(theme::line())
            .bg(theme::panel_top())
            .overflow_hidden()
            .when(open, |el| el.border_t_1().rounded_t(theme::radius::XXL))
    }

    /// render_line draws line `ix` of the page (Line).
    fn render_line(&self, ix: usize, view: Entity<Self>, cx: &App) -> AnyElement {
        let Some(line) = self.lines.get(ix) else {
            return div().into_any_element();
        };
        match line {
            Line::Head => Self::column(self.render_head(view, cx))
                .pt_7()
                .pb_3()
                .into_any_element(),
            Line::Body => Self::column(
                Self::card(true)
                    .border_b_1()
                    .rounded_b(theme::radius::XXL)
                    .shadow(theme::shadow_card())
                    .map(|el| match (&self.error, &self.page) {
                        (Some(e), _) => {
                            el.child(div().p_4().child(controls::error_line(e.clone())))
                        }
                        (None, None) => el.children(
                            [0.33f32, 0.5, 0.25, 0.42, 0.3].into_iter().enumerate().map(
                                |(n, width)| {
                                    h_flex()
                                        .gap_3()
                                        .px_4()
                                        .py_3()
                                        .when(n > 0, |el| {
                                            el.border_t_1().border_color(theme::line_faint())
                                        })
                                        .child(crate::motion::skeleton(
                                            ElementId::Name(format!("work-skel-a-{n}").into()),
                                            relative(width),
                                            px(14.),
                                        ))
                                        .child(div().flex_1())
                                        .child(crate::motion::skeleton(
                                            ElementId::Name(format!("work-skel-b-{n}").into()),
                                            px(56.),
                                            px(12.),
                                        ))
                                },
                            ),
                        ),
                        (None, Some(_)) => el.child(
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
                                .child(if self.q.is_empty() && self.filter.is_empty() {
                                    t("work.empty")
                                } else {
                                    t("work.noMatch")
                                }),
                        ),
                    }),
            )
            .pb_7()
            .into_any_element(),
            Line::Day { day, first } => Self::column(
                Self::card(*first).child(
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
                        .child(day_label(day).to_uppercase()),
                ),
            )
            .into_any_element(),
            Line::Work { ix: n, first } => {
                let Some(w) = self.shown.get(*n) else {
                    return div().into_any_element();
                };
                crate::bench::mark("work-rows");
                // ul divide-y: a line between rows only.
                let row = div()
                    .when(!*first, |el| {
                        el.border_t_1().border_color(theme::line_faint())
                    })
                    .child(self.render_row(w, view));
                // A fresh list's rows slide in; one scrolled into view later
                // is simply there.
                let row = if self.arrived.is_some_and(|at| at.elapsed() < ARRIVE) {
                    crate::motion::enter(
                        row,
                        SharedString::from(format!("work-in-{}", w.item.r#ref)),
                        *n,
                    )
                    .into_any_element()
                } else {
                    row.into_any_element()
                };
                Self::column(Self::card(false).child(row)).into_any_element()
            }
            Line::Foot => {
                let more = self.page.as_ref().is_some_and(|p| !p.next.is_empty());
                Self::column(
                    v_flex()
                        .gap_3()
                        .child(
                            Self::card(false)
                                .h(theme::radius::XXL)
                                .border_b_1()
                                .rounded_b(theme::radius::XXL),
                        )
                        .when(more, |el| {
                            el.child(h_flex().justify_center().child(controls::button(
                                "work-more",
                                if self.loading {
                                    t("agent.mediaTab.loadingMore")
                                } else {
                                    t("common.more")
                                },
                                controls::Btn::Ghost,
                                self.loading,
                                move |_: &ClickEvent, _, cx| {
                                    view.update(cx, |this, cx| this.load_more(cx))
                                },
                            )))
                        }),
                )
                .pb_7()
                .into_any_element()
            }
        }
    }

    /// render_head is the page's title, its project picker, the filters and
    /// the search.
    fn render_head(&self, view: Entity<Self>, cx: &App) -> AnyElement {
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
        let me = view.downgrade();
        let pick: controls::Pick<str> = Rc::new(move |v: &str, _, cx| {
            let v = v.to_string();
            me.update(cx, |this, cx| {
                this.project = v;
                this.load(cx);
            })
            .ok();
        });
        v_flex()
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
                                        gpui::linear_color_stop(theme::brand(600).opacity(0.1), 1.),
                                    ))
                                    .cursor_pointer()
                                    .hover(|el| {
                                        el.border_color(theme::brand(400).opacity(0.5))
                                            .bg(theme::brand(500).opacity(0.25))
                                    })
                                    .text_size(theme::text::UI)
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .on_click({
                                        let view = view.clone();
                                        move |_: &ClickEvent, window, cx| {
                                            view.update(cx, |this, cx| this.open_recap(window, cx))
                                        }
                                    })
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
                    .child(self.render_filters(view.clone()))
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
                                    .when(self.search.read(cx).value().is_empty(), |el| {
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
                                    })
                                    .child(
                                        Input::new(&self.search)
                                            .appearance(false)
                                            .with_size(gpui_component::Size::Size(px(14. / 0.875)))
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
            .into_any_element()
    }
}

impl Render for WorkView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        if self.bench.is_some_and(|n| {
            self.shown.len() >= n || self.page.as_ref().is_some_and(|p| p.next.is_empty())
        }) && !self.loading
        {
            self.scroll_bench(window, cx);
        }
        // The rows that slid in are redrawn plain once they have arrived.
        if let Some(at) = self.arrived {
            if at.elapsed() >= ARRIVE + Duration::from_millis(300) {
                self.arrived = None;
            } else {
                cx.spawn(async move |this, cx| {
                    cx.background_executor()
                        .timer(ARRIVE + Duration::from_millis(350))
                        .await;
                    this.update(cx, |_, cx| cx.notify()).ok();
                })
                .detach();
            }
        }
        let recap = self.recap.as_ref().map(|(v, _)| v.clone());
        let view = cx.entity();
        div()
            .relative()
            .size_full()
            .child(
                list(self.list.clone(), move |ix, _, cx| {
                    let cx: &App = cx;
                    view.read(cx).render_line(ix, view.clone(), cx)
                })
                .size_full(),
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
            w.update(cx, |w, cx| w.reload(cx));
        }
    }
}

#[cfg(test)]
mod line_tests {
    use super::{Line, lines};
    use agentbox_api::WorkItem;

    fn work(at: &str) -> WorkItem {
        WorkItem {
            at: at.into(),
            ..Default::default()
        }
    }

    #[test]
    fn work_is_listed_under_its_days_between_the_head_and_the_foot() {
        // Midday, so the day is the same in any time zone the test runs in.
        let shown = [
            work("2026-10-23T12:30:00Z"),
            work("2026-10-23T12:00:00Z"),
            work("2026-10-21T12:00:00Z"),
        ];
        let got = lines(&shown, false);
        let day = |d: &str, first| Line::Day {
            day: d.into(),
            first,
        };
        assert_eq!(
            got,
            vec![
                Line::Head,
                day("2026-10-23", true),
                Line::Work { ix: 0, first: true },
                Line::Work {
                    ix: 1,
                    first: false
                },
                day("2026-10-21", false),
                Line::Work { ix: 2, first: true },
                Line::Foot,
            ]
        );
    }

    #[test]
    fn a_page_with_nothing_to_list_is_its_head_and_one_card() {
        assert_eq!(lines(&[], false), vec![Line::Head, Line::Body]);
        // Still loading, or failed, whatever was listed before.
        assert_eq!(
            lines(&[work("2026-10-23T12:00:00Z")], true),
            vec![Line::Head, Line::Body]
        );
    }

    #[test]
    fn a_page_read_after_another_only_adds_lines_before_the_foot() {
        // What sync relies on to keep the lines above as they were measured.
        let mut shown = vec![work("2026-10-23T12:30:00Z"), work("2026-10-23T12:00:00Z")];
        let before = lines(&shown, false);
        shown.push(work("2026-10-23T11:00:00Z"));
        shown.push(work("2026-10-20T12:00:00Z"));
        let after = lines(&shown, false);
        assert_eq!(before[..before.len() - 1], after[..before.len() - 1]);
        assert_eq!(after.len(), before.len() + 3);
    }
}
