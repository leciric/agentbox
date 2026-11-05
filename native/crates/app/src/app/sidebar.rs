//! The v2 sidebar (desktop/src/renderer/components/v2/SidebarV2.tsx): Home,
//! a search, the user's sections, the other projects by what needs attention,
//! and docks for the idle and the hidden. Each project's row has its time, its
//! lead's last line or the question waiting, and counts of what's going on.
use std::collections::{HashMap, HashSet};
use std::time::SystemTime;

use agentbox_api::{
    Agent, ChatLine, HOME_PROJECT, Project, ProjectActivity, ProjectChat, Question, ReviewItem,
    ReviewPage, Section, SidebarLayout,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{InputEvent, InputState};
use gpui_component::{h_flex, tooltip::Tooltip, v_flex};

use super::AppView;
use super::sidebar_actions::Place;
use crate::theme;

/// Where this computer keeps the sidebar's own choices (prefs.rs).
pub(super) const PINS: &str = "v2.pins";
pub(super) const FILTER_ST: &str = "v2.filters.st";
pub(super) const FILTER_PROJECTS: &str = "v2.filters.projects";
const HIDDEN_OPEN: &str = "v2.hiddenOpen";
pub(super) const DRAFTS: &str = "drafts";

/// The states the filter offers, in the order they read (leadFirst.ts liveOrder).
pub(super) const STATES: [(&str, &str); 4] = [
    ("needs", "v2.st.needs"),
    ("error", "v2.st.error"),
    ("review", "v2.st.review"),
    ("working", "v2.st.working"),
];

fn strings(key: &str) -> Vec<String> {
    crate::prefs::get(key)
        .and_then(|v| serde_json::from_value(v).ok())
        .unwrap_or_default()
}

/// save_strings keeps a list of names in prefs.
pub(super) fn save_strings(key: &str, list: &[String]) {
    crate::prefs::set(key, serde_json::json!(list));
}

/// Shown is a project's icon as the sidebar draws it.
#[derive(Clone)]
pub enum Shown {
    None,
    Emoji(String),
    Image(std::sync::Arc<Image>),
}

/// shown_of reads an icon: its emoji, or its data: URL's picture.
pub(super) fn shown_of(icon: &agentbox_api::ProjectIcon) -> Shown {
    if !icon.emoji.is_empty() {
        return Shown::Emoji(icon.emoji.clone());
    }
    data_image(&icon.image).map_or(Shown::None, Shown::Image)
}

/// data_image decodes a base64 data: URL into a picture GPUI can draw.
pub(super) fn data_image(url: &str) -> Option<std::sync::Arc<Image>> {
    use base64::Engine as _;
    let rest = url.strip_prefix("data:")?;
    let (mime, data) = rest.split_once(";base64,")?;
    let format = ImageFormat::from_mime_type(mime)?;
    let bytes = base64::engine::general_purpose::STANDARD
        .decode(data.trim().as_bytes())
        .ok()?;
    Some(std::sync::Arc::new(Image::from_bytes(format, bytes)))
}

/// What the sidebar reads besides the projects.
pub struct Sidebar {
    pub(super) sections: Vec<Section>,
    pub(super) hidden: HashSet<String>,
    activity: HashMap<String, ProjectActivity>,
    pub(super) agents: Vec<Agent>,
    questions: HashMap<String, Vec<Question>>,
    /// Each project's lead's last line (GET /v1/projects/<p>/lead).
    lines: HashMap<String, ChatLine>,
    /// Every project's agents ready to review (GET /v1/review).
    review: Vec<ReviewItem>,
    pub(super) search: Entity<InputState>,
    idle_open: bool,
    hidden_open: bool,
    /// The pinned projects, in the user's order: this computer's, as the
    /// Electron app keeps them in localStorage (v2Projects.ts).
    pub(super) pins: Vec<String>,
    /// The filters' states (needs, error, review, working) and projects.
    pub(super) st: Vec<String>,
    pub(super) only: Vec<String>,
    pub(super) filter_open: bool,
    /// Each project's icon, by the icon_version it was read at.
    pub(super) icons: HashMap<String, (i64, Shown)>,
    /// The language the placeholders were written in.
    pub(super) lang: &'static str,
    /// What was typed and not sent in each chat, by its ref (lib/drafts.ts).
    pub(super) drafts: HashMap<String, String>,
    /// The dialog open over the window, one at a time.
    pub(super) dialog: Option<super::sidebar_dialogs::Dialog>,
    /// The search palette (Ctrl+K).
    pub(super) palette: Option<super::search::Palette>,
    /// The foot's page open in the middle: Media or Jobs.
    pub(super) page: Option<super::foot::FootPage>,
    pub(super) update: Option<agentbox_api::UpdateStatus>,
    pub(super) checking: bool,
    pub(super) foot: super::foot::Foot,
    /// The window's own focus, so Ctrl+K and the rest reach the app when
    /// nothing in it has the focus.
    pub(super) root_focus: FocusHandle,
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
            review: Vec::new(),
            search,
            idle_open: false,
            hidden_open: crate::prefs::get_bool(HIDDEN_OPEN, false),
            pins: strings(PINS),
            st: strings(FILTER_ST),
            only: strings(FILTER_PROJECTS),
            filter_open: false,
            icons: HashMap::new(),
            lang: agentbox_i18n::language(),
            drafts: crate::prefs::get(DRAFTS)
                .and_then(|v| serde_json::from_value(v).ok())
                .unwrap_or_default(),
            dialog: None,
            palette: None,
            page: None,
            update: None,
            checking: false,
            foot: Default::default(),
            root_focus: cx.focus_handle(),
            _sub: sub,
            _poll: poll,
        }
    }

    /// questions_of are a project's open questions.
    pub fn questions_of(&self, project: &str) -> &[Question] {
        self.questions.get(project).map_or(&[], Vec::as_slice)
    }

    /// review_of are the refs of a project's agents waiting for review.
    pub fn review_of(&self, project: &str) -> Vec<String> {
        self.review
            .iter()
            .filter(|i| i.project == project)
            .map(|i| i.r#ref.clone())
            .collect()
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
        self.load_review(cx);
        self.load_icons(cx);
        if self.side.update.is_none() {
            self.load_update(cx);
        }
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

    /// load_review reads what waits for review, every project's.
    pub(super) fn load_review(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            if let Ok(p) = client.get::<ReviewPage>("/v1/review?limit=200").await {
                this.update(cx, |this, cx| {
                    this.side.review = p.items;
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    /// held_agents are the agents whose state keeps their work out of "to
    /// review": one that asks you something, failed, or is at work again
    /// (lib/agentCounts.ts workStatus), by ref.
    pub(super) fn held_agents(&self) -> HashMap<String, &'static str> {
        let mut out = HashMap::new();
        for a in &self.side.agents {
            match a.chat.as_str() {
                "running" => out.insert(a.r#ref.clone(), "working"),
                "error" => out.insert(a.r#ref.clone(), "error"),
                _ => None,
            };
        }
        for q in self.side.questions.values().flatten() {
            if !q.r#ref.is_empty() {
                out.insert(q.r#ref.clone(), "needs");
            }
        }
        out
    }

    /// to_review is how many of a project's agents wait for review ("" for
    /// every project's), as the counts read it.
    pub(super) fn to_review(&self, project: &str) -> usize {
        let held = self.held_agents();
        self.side
            .review
            .iter()
            .filter(|i| project.is_empty() || i.project == project)
            .filter(|i| !held.contains_key(&i.r#ref))
            .count()
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

    /// st_counts are a project's agents in each of the filter's states:
    /// asking, stopped on an error, to review, working.
    pub(super) fn st_counts(&self, project: &str) -> [usize; 4] {
        let c = self.counts(project);
        [c.asks, c.failed, self.to_review(project), c.working]
    }

    /// passes is whether a project gets through the filters: one of the
    /// projects picked, with an agent in one of the states picked.
    fn passes(&self, p: &Project) -> bool {
        if !self.side.only.is_empty() && !self.side.only.contains(&p.name) {
            return false;
        }
        if self.side.st.is_empty() {
            return true;
        }
        let n = self.st_counts(&p.name);
        STATES
            .iter()
            .enumerate()
            .any(|(i, (st, _))| n[i] > 0 && self.side.st.iter().any(|s| s == st))
    }

    /// has_draft is whether a project's lead has an unsent message.
    fn has_draft(&self, project: &str, cx: &App) -> bool {
        let lead = |r: &str| {
            r.strip_prefix(project)
                .and_then(|r| r.strip_prefix('/'))
                .is_some_and(|rest| {
                    !self
                        .side
                        .agents
                        .iter()
                        .any(|a| a.project == project && a.name == rest)
                })
        };
        if let Some(chat) = &self.chat {
            let c = chat.read(cx);
            if lead(&c.agent) {
                return !c.draft_text(cx).trim().is_empty();
            }
        }
        self.side
            .drafts
            .iter()
            .any(|(r, text)| lead(r) && !text.trim().is_empty())
    }

    pub(super) fn render_sidebar(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let query = self.side.search.read(cx).value().trim().to_string();
        let filtering = !query.is_empty() || !self.side.st.is_empty() || !self.side.only.is_empty();
        let shown: Vec<&Project> = self
            .projects
            .iter()
            .filter(|p| self.matches(p, &query) && self.passes(p))
            .collect();
        let hidden = &self.side.hidden;
        // The pins first, in the user's order; a pin is never hidden.
        let pinned: Vec<&Project> = self
            .side
            .pins
            .iter()
            .filter_map(|n| shown.iter().copied().find(|p| &p.name == n))
            .collect();
        let has_pins = !pinned.is_empty();
        let mut placed: HashSet<String> = pinned.iter().map(|p| p.name.clone()).collect();
        // Sections keep the user's order; with group-idle on (Settings), the rest go by
        // attention, then activity, and the idle ones to a dock.
        let group_idle = crate::prefs::group_idle();
        let mut sections = Vec::new();
        for s in &self.side.sections {
            let mut ps: Vec<&Project> = shown
                .iter()
                .copied()
                .filter(|p| {
                    p.section == s.id && !hidden.contains(&p.name) && !placed.contains(&p.name)
                })
                .collect();
            ps.sort_by_key(|p| p.position);
            placed.extend(ps.iter().map(|p| p.name.clone()));
            sections.push((s, ps));
        }
        let (mut rest, mut idle, mut hid) = (Vec::new(), Vec::new(), Vec::new());
        for p in shown.iter().copied().filter(|p| !placed.contains(&p.name)) {
            if hidden.contains(&p.name) && self.project.as_deref() != Some(&p.name) {
                hid.push(p);
            } else if group_idle
                && self.counts(&p.name).running == 0
                && self.counts(&p.name).asks == 0
            {
                idle.push(p);
            } else {
                rest.push(p);
            }
        }
        if group_idle {
            rest.sort_by(|a, b| {
                let (ca, cb) = (self.counts(&a.name), self.counts(&b.name));
                (cb.asks + cb.failed, cb.working)
                    .cmp(&(ca.asks + ca.failed, ca.working))
                    .then_with(|| self.last(&b.name).cmp(&self.last(&a.name)))
            });
        } else {
            // Off (the default), a stable order that activity never moves.
            rest.sort_by_key(|p| (p.position, p.name.clone()));
        }
        idle.sort_by_key(|p| std::cmp::Reverse(self.last(&p.name)));
        // A search or a filter shows only the sections with a match.
        sections.retain(|(_, ps)| !filtering || !ps.is_empty());
        let nothing_else =
            rest.is_empty() && pinned.is_empty() && sections.iter().all(|(_, ps)| ps.is_empty());
        let idle_open = self.side.idle_open || nothing_else || !query.is_empty();
        let hidden_open = self.side.hidden_open || !query.is_empty();
        let nothing = !self.projects.is_empty() && shown.is_empty();
        let dragging = cx.has_active_drag();
        let no_rest = rest.is_empty();
        let in_section = !self.side.sections.is_empty();

        v_flex()
            .w(px(288.))
            .flex_none()
            .h_full()
            .bg(theme::panel())
            .border_r_1()
            .border_color(theme::border())
            .child(self.sidebar_head(cx))
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
                            .child(self.filter_bar(cx))
                            .when(self.projects.is_empty(), |el| {
                                el.child(
                                    div()
                                        .id("add-first")
                                        .mx_3()
                                        .p_3()
                                        .rounded_md()
                                        .border_1()
                                        .border_dashed()
                                        .border_color(theme::border())
                                        .text_sm()
                                        .text_color(theme::muted())
                                        .cursor_pointer()
                                        .hover(|el| el.text_color(theme::fg()))
                                        .on_click(cx.listener(
                                            |this, _: &ClickEvent, window, cx| {
                                                this.open_add_project(window, cx)
                                            },
                                        ))
                                        .child(format!("⊕  {}", t("shell.sidebar.addFirst"))),
                                )
                            })
                            .when(!pinned.is_empty(), |el| {
                                el.child(self.list_head(
                                    "head-pinned",
                                    t("v2.sidebar.pinned"),
                                    true,
                                    no_rest,
                                    cx,
                                ))
                                .children(
                                    pinned
                                        .into_iter()
                                        .map(|p| self.project_row(p, Place::Pinned, cx)),
                                )
                            })
                            .children(sections.into_iter().map(|(s, ps)| {
                                let open = !s.collapsed || filtering;
                                let empty = ps.is_empty();
                                let id = s.id.clone();
                                v_flex()
                                    .child(self.section_header(s, ps.len(), cx))
                                    .when(open, |el| {
                                        el.children(ps.into_iter().map(|p| {
                                            self.project_row(p, Place::Section(id.clone()), cx)
                                        }))
                                    })
                                    .when(open && empty, |el| {
                                        el.child(self.drop_box(
                                            SharedString::from(format!("drop-{}", s.id)),
                                            t("shell.sidebar.dropHere"),
                                            Some(s.id.clone()),
                                            cx,
                                        ))
                                    })
                            }))
                            .when(dragging && in_section, |el| {
                                el.child(self.drop_box(
                                    "drop-no-section".into(),
                                    t("v2.sidebar.dropNoSection"),
                                    None,
                                    cx,
                                ))
                            })
                            .when(!rest.is_empty(), |el| {
                                el.child(self.list_head(
                                    "head-projects",
                                    t("shell.sidebar.projects"),
                                    !has_pins,
                                    true,
                                    cx,
                                ))
                            })
                            .children(
                                rest.into_iter()
                                    .map(|p| self.project_row(p, Place::Rest, cx)),
                            )
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
                                    el.children(
                                        idle.into_iter()
                                            .map(|p| self.project_row(p, Place::Rest, cx)),
                                    )
                                })
                            })
                            .when(!hid.is_empty(), |el| {
                                let n = hid.len();
                                el.child(self.dock(
                                    "dock-hidden",
                                    format!("{} · {n}", t("v2.sidebar.hidden")),
                                    hidden_open,
                                    cx,
                                    |s| {
                                        s.hidden_open = !s.hidden_open;
                                        crate::prefs::set_bool(HIDDEN_OPEN, s.hidden_open);
                                    },
                                ))
                                .when(hidden_open, |el| {
                                    el.children(hid.into_iter().map(|p| {
                                        div().opacity(0.6).child(self.project_row(
                                            p,
                                            Place::Hidden,
                                            cx,
                                        ))
                                    }))
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
            .child(self.sidebar_foot(cx))
    }

    /// review_all is "Review N": the Work page on what waits, every project's.
    pub(super) fn review_all(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let n = self.to_review("");
        if n == 0 {
            return None;
        }
        Some(
            div()
                .id("review-all")
                .px_2()
                .py_0p5()
                .rounded_md()
                .border_1()
                .border_color(theme::border())
                .text_xs()
                .font_weight(FontWeight::MEDIUM)
                .text_color(theme::muted())
                .cursor_pointer()
                .hover(|el| el.bg(theme::raised()).text_color(theme::fg()))
                .child(tv("v2.review.all", &[("count", n.into())]))
                .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.open_work(String::new(), "review".into(), window, cx)
                }))
                .into_any_element(),
        )
    }

    fn last(&self, project: &str) -> String {
        self.side
            .activity
            .get(project)
            .and_then(|a| a.last_activity.clone())
            .unwrap_or_default()
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

    fn project_row(&self, p: &Project, place: Place, cx: &mut Context<Self>) -> AnyElement {
        let selected = self.project.as_deref() == Some(p.name.as_str());
        let name = p.name.clone();
        let display = if p.display_name.is_empty() {
            p.name.clone()
        } else {
            p.display_name.clone()
        };
        let draft = self.has_draft(&p.name, cx);
        let c = self.counts(&p.name);
        // Its chat's sessions: one working, one that said something unseen,
        // one waiting on a permission.
        let (s_working, s_unread, s_needs) = self.project_sessions(&p.name);
        let needs = (c.asks + c.failed).max(usize::from(s_needs));
        let to_review = self.to_review(&p.name);
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
        let row = self
            .row(&p.name, selected, cx)
            .items_start()
            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                this.select_project(name.clone(), window, cx)
            }))
            .child(
                div()
                    .relative()
                    .flex_none()
                    .child(self.project_icon(p, 24.))
                    .when(needs > 0 || s_unread, |el| {
                        el.child(
                            div()
                                .absolute()
                                .top(px(-2.))
                                .right(px(-2.))
                                .size_2()
                                .rounded_full()
                                .bg(if needs > 0 {
                                    theme::starting()
                                } else {
                                    theme::accent()
                                }),
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
                                    .child(display.clone()),
                            )
                            .when(draft, |el| {
                                let tip: SharedString = t("v2.sidebar.draft").into();
                                el.child(
                                    div()
                                        .id(SharedString::from(format!("draft-{}", p.name)))
                                        .text_xs()
                                        .text_color(theme::faint())
                                        .tooltip(move |w, cx| {
                                            Tooltip::new(tip.clone()).build(w, cx)
                                        })
                                        .child("✎"),
                                )
                            })
                            .when(c.working > 0 || s_working, |el| {
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
                    .when(!counts.is_empty() || to_review > 0, |el| {
                        let project = p.name.clone();
                        el.child(
                            h_flex()
                                .gap_2()
                                .text_xs()
                                .children(
                                    counts
                                        .into_iter()
                                        .map(|(s, c)| div().text_color(c).child(s)),
                                )
                                .when(to_review > 0, |el| {
                                    // "N to review" opens Work on what waits.
                                    el.child(div().flex_1()).child(
                                        div()
                                            .id(SharedString::from(format!("to-review-{project}")))
                                            .px_1p5()
                                            .rounded_full()
                                            .border_1()
                                            .border_color(theme::border())
                                            .text_color(theme::muted())
                                            .hover(|el| {
                                                el.bg(theme::hover()).text_color(theme::fg())
                                            })
                                            .child(tv(
                                                "v2.review.toReview",
                                                &[("count", to_review.into())],
                                            ))
                                            .on_click(cx.listener(
                                                move |this, _: &ClickEvent, window, cx| {
                                                    cx.stop_propagation();
                                                    this.open_work(
                                                        project.clone(),
                                                        "review".into(),
                                                        window,
                                                        cx,
                                                    )
                                                },
                                            )),
                                    )
                                }),
                        )
                    }),
            )
            .into_any_element();
        let row = self.row_dnd(p, &display, place, row, cx);
        v_flex()
            .child(row)
            .children(self.render_snoozed_rows(&p.name, cx))
            .into_any_element()
    }
}

/// ago is how long since an RFC 3339 time, as the sidebar writes it: now, 5m,
/// 3h, 2d.
pub(super) fn ago(time: &str) -> String {
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
