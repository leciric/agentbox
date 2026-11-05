//! The Office (#668), behind Settings → General → Office: a campus of
//! projects' rooms drawn natively (crates/office: wgpu, offscreen, on a thread
//! of its own), with a desk and a robot for every agent. Walk up to one and
//! open its chat; the board says who needs you. This is the page around the
//! drawing: the data it reads (every project's agents, fleets, questions and
//! leads), the frames it paints, and what goes over them (name tags, the top
//! bar, the tray, the desk's card, the help, the chat in place).

use agentbox_api::{Agent, Client, Fleet, Project, ProjectChat, Question, ReviewItem, ReviewPage};
use agentbox_i18n::{t, tv};
use agentbox_office::desks::{AreaLabel, BoardColumn, BoardEntry, BoardScreen, LineTone, SceneInput, Screen, ScreenLine};
use agentbox_office::engine::{Engine, In, Label, LabelKind, Out};
use agentbox_office::model::{self, AgentSt, DeskTone, LeadTone, OfficeInputs, OfficeModel, PrLook, Row};
use agentbox_office::sim::Note;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};
use std::cell::Cell;
use std::collections::{HashMap, HashSet};
use std::rc::Rc;
use std::sync::Arc;
use std::time::Duration;

use crate::app::agent_status::{St, agent_st};
use crate::theme;

/// PREF is the Office's flag (#668's agentbox.ui.office): off by default.
pub const PREF: &str = "ui.office";

pub fn enabled() -> bool {
    crate::prefs::get_bool(PREF, false)
}

/// What the Office asks of the app.
#[derive(Clone, Debug)]
pub enum OfficeEvent {
    /// Out of the Office (Back, or Escape over the campus).
    Leave,
    /// Open an agent's chat ("lead" for the project's) in place.
    Open { project: String, who: String },
    /// A pull request, in the browser.
    OpenUrl(String),
}

/// Where the Office's view of things stands (#668's onView).
#[derive(Clone, Debug, Default, PartialEq)]
struct ViewAt {
    project: Option<String>,
    floor: i64,
    floors: usize,
    overview: bool,
}

pub struct OfficeView {
    client: Client,
    focus: FocusHandle,
    engine: Engine,
    adapter: Option<(String, bool)>,
    failed: Option<String>,
    notice_closed: bool,
    frame: Option<Arc<RenderImage>>,
    painted: usize,
    stale: Vec<Arc<RenderImage>>,
    labels: Vec<Label>,
    bounds: Rc<Cell<Option<Bounds<Pixels>>>>,
    sent: Option<(f32, f32, f32)>,
    last_mouse: Option<Point<Pixels>>,
    // The data.
    projects: Vec<Project>,
    agents: Vec<Agent>,
    review: Vec<ReviewItem>,
    questions: Vec<(String, Vec<Question>)>,
    leads: HashMap<String, ProjectChat>,
    fleets: HashMap<String, Fleet>,
    model: OfficeModel,
    loaded: bool,
    reload: Option<Task<()>>,
    // What it says.
    near: Option<String>,
    hover: Option<String>,
    at: ViewAt,
    hint: bool,
    help: bool,
    tray: bool,
    /// The chat open in place, over the room's right side (the app's own).
    chat: Option<AnyView>,
    shown: bool,
    _task: Task<()>,
    _blur: Subscription,
}

impl EventEmitter<OfficeEvent> for OfficeView {}

const HINT_SEEN: &str = "office.hintSeen";

impl OfficeView {
    pub fn new(client: Client, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus = cx.focus_handle();
        let (tx, rx) = async_channel::unbounded::<Out>();
        let task = cx.spawn(async move |this, cx| {
            while let Ok(out) = rx.recv().await {
                if this.update(cx, |this, cx| this.take(out, cx)).is_err() {
                    return;
                }
            }
        });
        let blur = cx.on_blur(&focus, window, |this, _, _| this.engine.send(In::Blur));
        // The Office's thread starts once and stays while the app runs:
        // coming back is instant, where you left it.
        let (engine, _visit) = Engine::start(None, move |out| {
            let _ = tx.send_blocking(out);
        });
        let mut this = Self {
            client,
            focus,
            engine,
            adapter: None,
            failed: None,
            notice_closed: false,
            frame: None,
            painted: 0,
            stale: Vec::new(),
            labels: Vec::new(),
            bounds: Rc::default(),
            sent: None,
            last_mouse: None,
            projects: Vec::new(),
            agents: Vec::new(),
            review: Vec::new(),
            questions: Vec::new(),
            leads: HashMap::new(),
            fleets: HashMap::new(),
            model: OfficeModel::default(),
            loaded: false,
            reload: None,
            near: None,
            hover: None,
            at: ViewAt { floor: 1, floors: 1, overview: true, ..Default::default() },
            hint: !crate::prefs::get_bool(HINT_SEEN, false),
            help: false,
            tray: false,
            chat: None,
            shown: true,
            _task: task,
            _blur: blur,
        };
        this.focus.focus(window, cx);
        this.load(cx);
        this
    }

    fn send(&self, m: In) {
        self.engine.send(m);
    }

    /// show is the Office coming back into view (or going out of it): out of
    /// view it draws nothing.
    pub fn show(&mut self, on: bool, window: &mut Window, cx: &mut Context<Self>) {
        self.shown = on;
        self.send(In::Shown(on));
        if on {
            self.focus.focus(window, cx);
            self.load(cx);
        }
    }

    /// poke is the daemon saying something changed: read it all again soon,
    /// once however many come at once.
    pub fn poke(&mut self, cx: &mut Context<Self>) {
        if !self.shown || self.reload.is_some() {
            return;
        }
        self.reload = Some(cx.spawn(async move |this, cx| {
            cx.background_executor().timer(Duration::from_millis(400)).await;
            this.update(cx, |this, cx| {
                this.reload = None;
                this.load(cx);
            })
            .ok();
        }));
    }

    /// load reads every project's agents and what the Office says of them.
    fn load(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let projects = client.get::<Vec<Project>>("/v1/projects").await.unwrap_or_default();
            let agents = client.get::<Vec<Agent>>("/v1/agents").await.unwrap_or_default();
            let review = client.get::<ReviewPage>("/v1/review?limit=200").await.map(|p| p.items).unwrap_or_default();
            let mut questions = Vec::new();
            let mut leads = HashMap::new();
            let mut fleets = HashMap::new();
            for p in &projects {
                if let Ok(q) = client.get::<Vec<Question>>(&format!("/v1/projects/{}/questions", p.name)).await {
                    questions.push((p.name.clone(), q));
                }
                if let Ok(l) = client.get::<ProjectChat>(&format!("/v1/projects/{}/lead", p.name)).await {
                    leads.insert(p.name.clone(), l);
                }
                if agents.iter().any(|a| a.project == p.name)
                    && let Ok(f) = client.get::<Fleet>(&format!("/v1/projects/{}/fleet", p.name)).await
                {
                    fleets.insert(p.name.clone(), f);
                }
            }
            this.update(cx, |this, cx| {
                this.projects = projects;
                this.agents = agents;
                this.review = review;
                this.questions = questions;
                this.leads = leads;
                this.fleets = fleets;
                this.loaded = true;
                this.rebuild();
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// rebuild works the model out again and hands it to the Office.
    fn rebuild(&mut self) {
        let asking: HashSet<&str> = self.questions.iter().flat_map(|(_, q)| q.iter().map(|q| q.r#ref.as_str())).collect();
        let review: HashSet<&str> = self.review.iter().map(|r| r.r#ref.as_str()).collect();
        let rows: Vec<Row> = self
            .agents
            .iter()
            .filter(|a| a.project != agentbox_api::HOME_PROJECT)
            .map(|a| {
                let st = match agent_st(a, asking.contains(a.r#ref.as_str()), review.contains(a.r#ref.as_str())) {
                    St::Needs => AgentSt::Needs,
                    St::Error => AgentSt::Error,
                    St::Review => AgentSt::Review,
                    St::Working => AgentSt::Working,
                    St::Done => AgentSt::Done,
                };
                Row { agent: a.clone(), st }
            })
            .collect();
        let projects: Vec<model::ProjectRef> = self
            .projects
            .iter()
            .filter(|p| p.name != agentbox_api::HOME_PROJECT)
            .map(|p| model::ProjectRef { name: p.name.clone(), display_name: p.display_name.clone() })
            .collect();
        self.model = model::office_model(&OfficeInputs { projects: &projects, rows: &rows, fleets: &self.fleets, questions: &self.questions, leads: &self.leads, review: &self.review });
        let input = SceneInput { model: self.model.clone(), screens: self.screens(), board: self.board(), you: t("native.office.you"), area_labels: self.area_labels() };
        self.send(In::Apply(Box::new(input)));
    }

    fn screens(&self) -> HashMap<String, Screen> {
        let mut out = HashMap::new();
        for area in &self.model.areas {
            let lead_tone = match area.lead.tone {
                LeadTone::Needs => DeskTone::Needs,
                LeadTone::Working => DeskTone::Working,
                LeadTone::Idle => DeskTone::Idle,
            };
            out.insert(
                model::lead_ref(&area.project),
                Screen {
                    tone: lead_tone,
                    head: t(&format!("native.office.lead.{}", lead_word(area.lead.tone))),
                    title: tv("native.office.lead.title", &[("project", area.label.as_str().into())]),
                    lines: match &area.lead.line {
                        Some(l) => vec![ScreenLine { text: l.clone(), tone: LineTone::Plain }],
                        None => vec![ScreenLine { text: t("native.office.lead.quiet"), tone: LineTone::Muted }],
                    },
                },
            );
            for d in &area.desks {
                let mut lines = Vec::new();
                if let Some(q) = &d.question {
                    lines.push(ScreenLine { text: q.clone(), tone: if d.tone == DeskTone::Error { LineTone::Bad } else { LineTone::Warn } });
                }
                if let Some(pr) = &d.pr {
                    lines.push(pr_line(pr));
                }
                if let Some(c) = &d.changes {
                    lines.push(ScreenLine { text: changes_text(c.files, c.insertions, c.deletions), tone: LineTone::Muted });
                }
                lines.push(ScreenLine { text: d.branch.clone(), tone: LineTone::Code });
                out.insert(d.id.clone(), Screen { tone: d.tone, head: tone_label(d.tone), title: d.title.clone(), lines });
            }
        }
        out
    }

    fn board(&self) -> BoardScreen {
        let label = |project: &str, r: &str| -> String {
            let Some(a) = self.model.areas.iter().find(|a| a.project == project) else { return project.to_string() };
            let sizes = agentbox_office::layout::floors_of(a.desks.len());
            if r.ends_with("/lead") || sizes.len() < 2 {
                return a.label.clone();
            }
            let Some(mut at) = a.desks.iter().position(|d| d.id == r) else { return a.label.clone() };
            let mut floor = 0;
            while floor < sizes.len() - 1 && at >= sizes[floor] {
                at -= sizes[floor];
                floor += 1;
            }
            tv("native.office.floor", &[("name", a.label.as_str().into()), ("floor", ((floor + 1) as i64).into())])
        };
        let sub = |i: &model::BoardItem| -> String {
            if i.r#ref.ends_with("/lead") {
                t("native.office.board.leadWaiting")
            } else if let Some(text) = &i.text {
                format!("{} · {text}", label(&i.project, &i.r#ref))
            } else if let Some(pr) = &i.pr {
                let failing = if pr.checks == model::PrChecks::Failing { format!(" · {}", t("native.office.checks.failing")) } else { String::new() };
                format!("{} · #{} {}{failing}", label(&i.project, &i.r#ref), pr.number, t(&format!("native.office.pr.{}", pr_state(pr))))
            } else {
                label(&i.project, &i.r#ref)
            }
        };
        let col = |items: &[model::BoardItem], head: &str, empty: &str| BoardColumn {
            head: t(head),
            empty: t(empty),
            items: items.iter().map(|i| BoardEntry { r#ref: i.r#ref.clone(), title: i.title.clone(), sub: sub(i), tone: i.tone, failing: i.pr.as_ref().is_some_and(|p| p.checks == model::PrChecks::Failing) }).collect(),
        };
        BoardScreen {
            title: t("native.office.board.title"),
            needs: col(&self.model.board.needs, "native.office.board.needs", "native.office.board.needsEmpty"),
            review: col(&self.model.board.review, "native.office.board.review", "native.office.board.reviewEmpty"),
        }
    }

    fn area_labels(&self) -> HashMap<String, AreaLabel> {
        let mut out = HashMap::new();
        for a in &self.model.areas {
            let n = |tone: DeskTone| a.desks.iter().filter(|d| d.tone == tone).count() as i64;
            let needs = n(DeskTone::Needs) + n(DeskTone::Error) + i64::from(a.lead.tone == LeadTone::Needs);
            let parts: Vec<String> = [
                (needs > 0).then(|| tv("native.office.count.needs", &[("count", needs.into())])),
                (n(DeskTone::Working) > 0).then(|| tv("native.office.count.working", &[("count", n(DeskTone::Working).into())])),
                (n(DeskTone::Finished) > 0).then(|| tv("native.office.count.review", &[("count", n(DeskTone::Finished).into())])),
            ]
            .into_iter()
            .flatten()
            .collect();
            let sub = if parts.is_empty() { tv("native.office.lead.agents", &[("count", (a.desks.len() as i64).into())]) } else { parts.join(" · ") };
            out.insert(a.project.clone(), AreaLabel { name: a.label.clone(), sub, count: needs });
        }
        out
    }

    /// take is what the Office's thread sent.
    fn take(&mut self, out: Out, cx: &mut Context<Self>) {
        match out {
            Out::Adapter { name, software } => self.adapter = Some((name, software)),
            Out::Failed(e) => self.failed = Some(e),
            Out::Frame { frame, labels, render_ms } => {
                crate::bench::sample("office-render", Duration::from_secs_f32(render_ms / 1000.));
                if let Some(rgba) = image::RgbaImage::from_raw(frame.width, frame.height, frame.bgra) {
                    let img = Arc::new(RenderImage::new(vec![image::Frame::new(rgba)]));
                    if let Some(old) = self.frame.replace(img) {
                        self.stale.push(old);
                    }
                }
                self.labels = labels;
            }
            Out::Notes(notes) => {
                for n in notes {
                    self.note(n, cx);
                }
            }
        }
        cx.notify();
    }

    fn note(&mut self, n: Note, cx: &mut Context<Self>) {
        match n {
            Note::Near(r) => {
                self.near = r;
                // The card covers the room's right side: a close-up keeps
                // what it looks at clear of it.
                self.cover();
            }
            Note::View { project, floor, floors, overview } => self.at = ViewAt { project, floor, floors, overview },
            Note::Hover(r) => self.hover = r,
            Note::Activate(r) => self.activate(&r, cx),
            Note::Moved => {
                if self.hint {
                    self.hint = false;
                    crate::prefs::set_bool(HINT_SEEN, true);
                }
            }
            Note::Next => self.next(),
            Note::Leave => cx.emit(OfficeEvent::Leave),
            // Setting off yourself closes the chat that was open in place.
            Note::UserMove => {
                if self.chat.is_some() {
                    self.chat = None;
                    self.cover();
                }
            }
        }
    }

    fn cover(&self) {
        let w = self.bounds.get().map(|b| f32::from(b.size.width)).unwrap_or(1440.);
        let c = if self.chat.is_some() {
            chat_width(w)
        } else if self.near.is_some() {
            336. + 24.
        } else {
            0.
        };
        self.send(In::Cover(c as f64));
    }

    /// set_chat is the app having opened (or closed) a chat in place.
    pub fn set_chat(&mut self, chat: Option<AnyView>, cx: &mut Context<Self>) {
        self.chat = chat;
        self.cover();
        cx.notify();
    }

    pub fn has_chat(&self) -> bool {
        self.chat.is_some()
    }

    fn activate(&mut self, r: &str, cx: &mut Context<Self>) {
        let Some((project, who)) = r.split_once('/') else { return };
        cx.emit(OfficeEvent::Open { project: project.to_string(), who: who.to_string() });
    }

    /// next walks to the next desk that needs you (N), in the board's order.
    fn next(&mut self) {
        let needs = &self.model.board.needs;
        if needs.is_empty() {
            return;
        }
        let at = self.near.as_ref().and_then(|n| needs.iter().position(|i| &i.r#ref == n));
        let i = at.map_or(0, |i| (i + 1) % needs.len());
        self.send(In::WalkTo(needs[i].r#ref.clone()));
    }

    fn local(&self, p: Point<Pixels>) -> (f64, f64) {
        let o = self.bounds.get().map(|b| b.origin).unwrap_or_default();
        (f64::from(f32::from(p.x - o.x)), f64::from(f32::from(p.y - o.y)))
    }

    fn key_name(k: &Keystroke) -> Option<&'static str> {
        if k.modifiers.control || k.modifiers.alt || k.modifiers.platform {
            return None;
        }
        Some(match k.key.as_str() {
            "w" => "w",
            "a" => "a",
            "s" => "s",
            "d" => "d",
            "up" => "up",
            "down" => "down",
            "left" => "left",
            "right" => "right",
            "escape" => "escape",
            "n" => "n",
            "e" => "e",
            "enter" => "enter",
            _ => return None,
        })
    }
}

fn lead_word(t: LeadTone) -> &'static str {
    match t {
        LeadTone::Working => "working",
        LeadTone::Needs => "needs",
        LeadTone::Idle => "idle",
    }
}

fn tone_word(t: DeskTone) -> &'static str {
    match t {
        DeskTone::Needs => "needs",
        DeskTone::Error => "error",
        DeskTone::Finished => "finished",
        DeskTone::Working => "working",
        DeskTone::Idle => "idle",
        DeskTone::Stopped => "stopped",
    }
}

fn tone_label(t: DeskTone) -> String {
    agentbox_i18n::t(&format!("native.office.tone.{}", tone_word(t)))
}

fn pr_state(pr: &PrLook) -> &'static str {
    match pr.state {
        model::PrState::Open => "open",
        model::PrState::Draft => "draft",
        model::PrState::Merged => "merged",
        model::PrState::Closed => "closed",
    }
}

fn pr_line(pr: &PrLook) -> ScreenLine {
    let checks = match pr.checks {
        model::PrChecks::None => String::new(),
        model::PrChecks::Passing => format!(" · {}", t("native.office.checks.passing")),
        model::PrChecks::Failing => format!(" · {}", t("native.office.checks.failing")),
        model::PrChecks::Pending => format!(" · {}", t("native.office.checks.pending")),
    };
    ScreenLine {
        text: tv("native.office.pr.line", &[("number", pr.number.into()), ("state", t(&format!("native.office.pr.{}", pr_state(pr))).into()), ("checks", checks.into())]),
        tone: if pr.checks == model::PrChecks::Failing {
            LineTone::Bad
        } else if pr.state == model::PrState::Merged {
            LineTone::Code
        } else if pr.checks == model::PrChecks::Passing {
            LineTone::Good
        } else {
            LineTone::Muted
        },
    }
}

fn changes_text(files: i64, insertions: i64, deletions: i64) -> String {
    tv("native.office.changes", &[("files", files.into()), ("insertions", insertions.into()), ("deletions", deletions.into())])
}

/// tone_color is a tone's colour (textures.ts toneColor).
fn tone_color(t: DeskTone) -> Hsla {
    rgb(match t {
        DeskTone::Needs => 0xfbbf24,
        DeskTone::Error => 0xf87171,
        DeskTone::Finished => 0x34d399,
        DeskTone::Working => 0x60a5fa,
        DeskTone::Idle => 0xa5b4c8,
        DeskTone::Stopped => 0x6b7280,
    })
    .into()
}

fn hex(s: &str) -> Hsla {
    rgb(u32::from_str_radix(s.trim_start_matches('#'), 16).unwrap_or(0xffffff)).into()
}

/// chat_width is how wide the chat open in place is: most of a narrow
/// window, under half a wide one.
fn chat_width(w: f32) -> f32 {
    (w * 0.42).clamp(380., 640.).min(w - 40.)
}

/// The fix for drawing without a GPU, for this OS.
fn software_fix() -> String {
    t(if cfg!(target_os = "windows") {
        "native.office.software.windows"
    } else if cfg!(target_os = "macos") {
        "native.office.software.mac"
    } else {
        "native.office.software.linux"
    })
}

/// A card's look over the room: the app's panel colour, raised.
fn card() -> Div {
    div().bg(theme::panel()).border_1().border_color(theme::border()).rounded_lg().shadow_lg().text_color(theme::fg())
}

fn button(id: impl Into<ElementId>, label: impl IntoElement) -> Stateful<Div> {
    h_flex().id(id).gap_1().px_2().h_7().items_center().rounded_md().text_sm().cursor_pointer().hover(|el| el.bg(theme::hover())).child(label)
}

fn tip(el: Stateful<Div>, s: String) -> Stateful<Div> {
    el.tooltip(move |window, cx| gpui_component::tooltip::Tooltip::new(s.clone()).build(window, cx))
}

impl Render for OfficeView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        for old in self.stale.drain(..) {
            let _ = window.drop_image(old);
        }
        // A new frame on its way to the screen: the next may be drawn.
        if let Some(f) = &self.frame {
            let id = Arc::as_ptr(f) as usize;
            if id != self.painted {
                self.painted = id;
                self.send(In::Painted);
            }
        }
        let bounds = self.bounds.clone();
        let scale = window.scale_factor();
        if let Some(b) = bounds.get() {
            let size = (f32::from(b.size.width), f32::from(b.size.height), scale);
            if self.sent != Some(size) {
                self.sent = Some(size);
                self.send(In::Size { w: size.0, h: size.1, scale });
                self.cover();
            }
        }
        let w = bounds.get().map(|b| f32::from(b.size.width)).unwrap_or(1440.);
        let near_desk = self.near.as_ref().and_then(|n| self.model.areas.iter().flat_map(|a| a.desks.iter()).find(|d| &d.id == n)).cloned();
        let near_lead = self.near.as_ref().filter(|n| n.ends_with("/lead")).and_then(|n| self.model.areas.iter().find(|a| model::lead_ref(&a.project) == *n)).cloned();
        let needs = self.model.board.needs.len();
        let working = self.model.areas.iter().flat_map(|a| a.desks.iter()).filter(|d| d.tone == DeskTone::Working).count();
        let to_review = self.model.board.review.len();
        let empty = self.loaded && self.model.areas.is_empty();
        let room_name = self.at.project.as_ref().and_then(|p| self.model.areas.iter().find(|a| &a.project == p)).map(|a| a.label.clone());

        div()
            .id("office")
            .track_focus(&self.focus)
            .key_context("Office")
            .relative()
            .size_full()
            .overflow_hidden()
            .bg(rgb(0xdbe7ef))
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                if let Some(k) = Self::key_name(&ev.keystroke) {
                    this.send(In::Key { key: k.into(), down: true, repeat: ev.is_held });
                    cx.stop_propagation();
                }
            }))
            .on_key_up(cx.listener(|this, ev: &KeyUpEvent, _, _| {
                if let Some(k) = Self::key_name(&ev.keystroke) {
                    this.send(In::Key { key: k.into(), down: false, repeat: false });
                }
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, ev: &MouseDownEvent, window, cx| {
                    this.focus.focus(window, cx);
                    let (x, y) = this.local(ev.position);
                    this.last_mouse = Some(ev.position);
                    this.send(In::PointerDown { x, y });
                }),
            )
            .on_mouse_move(cx.listener(|this, ev: &MouseMoveEvent, _, _| {
                let dx = this.last_mouse.map(|p| f64::from(f32::from(ev.position.x - p.x))).unwrap_or(0.);
                this.last_mouse = Some(ev.position);
                let (x, y) = this.local(ev.position);
                this.send(In::PointerMove { x, y, dx });
            }))
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(|this, ev: &MouseUpEvent, _, _| {
                    let (x, y) = this.local(ev.position);
                    this.send(In::PointerUp { x, y });
                }),
            )
            .on_hover(cx.listener(|this, over: &bool, _, _| {
                if !*over {
                    this.send(In::PointerLeave);
                }
            }))
            .on_scroll_wheel(cx.listener(|this, ev: &ScrollWheelEvent, _, _| {
                let d = ev.delta.pixel_delta(px(33.));
                let (x, y) = this.local(ev.position);
                // As a browser's deltaY: positive is outwards.
                this.send(In::Wheel { x, y, delta: -f64::from(f32::from(d.y)) });
            }))
            .when(self.hover.is_some(), |el| el.cursor_pointer())
            .children(self.frame.clone().map(|f| img(f).absolute().inset_0().size_full()))
            .child(
                canvas(
                    move |b, window, _| {
                        if bounds.get() != Some(b) {
                            bounds.set(Some(b));
                            window.refresh();
                        }
                    },
                    |_, _, _, _| {},
                )
                .absolute()
                .size_full(),
            )
            .children(self.labels.iter().map(label_el))
            .child(self.top_bar(room_name, cx))
            .when(self.at.project.is_some() || !self.at.overview, |el| {
                el.child(div().absolute().top_3().right_3().child(card().child(button("office-overview", t("native.office.overview")).on_click(cx.listener(|this, _: &ClickEvent, _, _| this.send(In::Overview))))))
            })
            .children(self.notice(cx))
            .child(self.tray_el(needs, working, to_review, cx))
            .children(self.hint_el())
            .child(self.corner(near_desk, near_lead, w, cx))
            .when(empty, |el| {
                el.child(
                    div().absolute().inset_0().flex().items_center().justify_center().child(
                        card()
                            .p_5()
                            .max_w(px(420.))
                            .child(div().font_weight(FontWeight::SEMIBOLD).child(t("native.office.empty.title")))
                            .child(div().mt_1().text_sm().text_color(theme::muted()).child(t("native.office.empty.body"))),
                    ),
                )
            })
            .when_some(self.failed.clone(), |el, e| {
                el.child(
                    div().absolute().inset_0().flex().items_center().justify_center().bg(theme::bg()).child(
                        card()
                            .p_5()
                            .max_w(px(480.))
                            .child(div().child(t("native.office.unsupported")))
                            .child(div().mt_2().text_xs().text_color(theme::muted()).font_family(theme::mono()).child(e)),
                    ),
                )
            })
            .when_some(self.chat.clone(), |el, chat| {
                el.child(
                    v_flex()
                        .absolute()
                        .top_0()
                        .right_0()
                        .bottom_0()
                        .w(px(chat_width(w)))
                        .bg(theme::bg())
                        .border_l_1()
                        .border_color(theme::border())
                        .shadow_lg()
                        // The chat's own keys and clicks stay its own.
                        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                        .on_key_down(|_, _, cx| cx.stop_propagation())
                        .on_scroll_wheel(|_, _, cx| cx.stop_propagation())
                        .child(
                            h_flex()
                                .h_9()
                                .px_2()
                                .justify_end()
                                .border_b_1()
                                .border_color(theme::border())
                                .child(tip(button("office-chat-close", crate::icons::of("✕")), t("native.office.chat.close")).on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.set_chat(None, cx)))),
                        )
                        .child(div().flex_1().min_h_0().child(chat)),
                )
            })
    }
}

/// label_el is a name tag, a bubble or an area's pill, over the room.
fn label_el(l: &Label) -> AnyElement {
    let at = |w: f32, h: f32| div().absolute().left(px(l.x - w / 2.)).top(px(l.y - h / 2.));
    match l.kind {
        LabelKind::Bubble => at(26., 26.)
            .size(px(26.))
            .rounded_full()
            .bg(hex(&l.color))
            .border_2()
            .border_color(gpui::white())
            .shadow_md()
            .flex()
            .items_center()
            .justify_center()
            .text_sm()
            .font_weight(FontWeight::BOLD)
            .text_color(gpui::black())
            .child(l.text.clone())
            .into_any_element(),
        LabelKind::Area => at(200., 24.)
            .w(px(200.))
            .flex()
            .justify_center()
            .child(h_flex().gap_1p5().px_2p5().py_0p5().rounded_full().bg(rgba(0xffffffe6)).shadow_md().text_xs().text_color(rgb(0x334155)).child(div().size_2().rounded_full().bg(hex(&l.color))).child(l.text.clone()))
            .into_any_element(),
        LabelKind::Tag | LabelKind::You => at(160., 18.)
            .w(px(160.))
            .flex()
            .justify_center()
            .child(
                h_flex()
                    .gap_1()
                    .px_2()
                    .rounded_full()
                    .bg(if l.kind == LabelKind::You { rgba(0x7c3aedee) } else { rgba(0x0f172acc) })
                    .text_xs()
                    .text_color(gpui::white())
                    .when(l.kind == LabelKind::Tag, |el| el.child(div().size_1p5().rounded_full().bg(hex(&l.color))))
                    .child(div().max_w(px(140.)).truncate().child(l.text.clone())),
            )
            .into_any_element(),
    }
}

impl OfficeView {
    fn top_bar(&self, room: Option<String>, cx: &mut Context<Self>) -> impl IntoElement {
        let at = self.at.clone();
        div().absolute().top_3().left_3().child(
            card().child(
                h_flex()
                    .p_1()
                    .gap_0p5()
                    .items_center()
                    .child(tip(button("office-back", h_flex().gap_1().child(crate::icons::of("←")).child(t("native.office.leave"))), t("native.office.leaveHint")).on_click(cx.listener(|_, _: &ClickEvent, _, cx| cx.emit(OfficeEvent::Leave))))
                    .child(div().mx_0p5().h_5().w(px(1.)).bg(theme::border()))
                    .child(match &at.project {
                        Some(_) => button("office-campus", div().font_weight(FontWeight::SEMIBOLD).child(t("native.office.title"))).on_click(cx.listener(|this, _: &ClickEvent, _, _| this.send(In::Campus))).into_any_element(),
                        None => div().px_2().text_sm().font_weight(FontWeight::SEMIBOLD).child(t("native.office.title")).into_any_element(),
                    })
                    .when_some(room, |el, name| {
                        let label = if at.floors > 1 { tv("native.office.floor", &[("name", name.as_str().into()), ("floor", at.floor.into())]) } else { name };
                        el.child(div().text_color(theme::faint()).child(crate::icons::of("›"))).child(div().px_1().text_sm().max_w(px(260.)).truncate().child(label))
                    })
                    .when(at.project.is_some() && at.floors > 1, |el| {
                        el.child(h_flex().ml_1().gap_0p5().p_0p5().rounded_md().bg(theme::raised()).children((1..=at.floors as i64).map(|f| {
                            div()
                                .id(("office-floor", f as usize))
                                .h_6()
                                .min_w_6()
                                .px_1p5()
                                .rounded_md()
                                .flex()
                                .items_center()
                                .justify_center()
                                .text_xs()
                                .font_weight(FontWeight::SEMIBOLD)
                                .cursor_pointer()
                                .when(f == at.floor, |el| el.bg(theme::hover()))
                                .when(f != at.floor, |el| el.text_color(theme::muted()))
                                .child(f.to_string())
                                .on_click(cx.listener(move |this, _: &ClickEvent, _, _| this.send(In::Floor(f))))
                        })))
                    }),
            ),
        )
    }

    /// notice says the Office is drawn without a GPU, and how to fix that.
    fn notice(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let (name, software) = self.adapter.clone()?;
        if !software || self.notice_closed {
            return None;
        }
        Some(
            div()
                .absolute()
                .top_16()
                .left_0()
                .right_0()
                .flex()
                .justify_center()
                .child(
                    card().max_w(px(520.)).p_3().child(
                        h_flex()
                            .gap_2()
                            .items_start()
                            .child(div().text_color(theme::starting()).child(crate::icons::of("⚠")))
                            .child(
                                v_flex()
                                    .flex_1()
                                    .min_w_0()
                                    .gap_1()
                                    .child(div().text_sm().font_weight(FontWeight::SEMIBOLD).child(t("native.office.software.title")))
                                    .child(div().text_sm().text_color(theme::muted()).child(tv("native.office.software.body", &[("adapter", name.as_str().into())])))
                                    .child(div().text_sm().text_color(theme::muted()).child(software_fix())),
                            )
                            .child(button("office-notice-close", t("native.office.software.dismiss")).on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.notice_closed = true;
                                cx.notify();
                            }))),
                    ),
                )
                .into_any_element(),
        )
    }

    /// tray_el is the counts at the bottom left, and who needs you when it's open.
    fn tray_el(&self, needs: usize, working: usize, review: usize, cx: &mut Context<Self>) -> impl IntoElement {
        let dot = |c: u32| div().size_2().rounded_full().bg(rgb(c));
        let open = self.tray;
        let rows: Vec<model::BoardItem> = if open { self.model.board.needs.iter().chain(self.model.board.review.iter()).cloned().collect() } else { Vec::new() };
        div().absolute().bottom_3().left_3().child(
            card().max_w(px(380.)).child(
                v_flex()
                    .when(open, |el| {
                        el.child(v_flex().p_1().children(rows.into_iter().take(12).enumerate().map(|(i, item)| {
                            let r = item.r#ref.clone();
                            h_flex()
                                .id(("office-tray-row", i))
                                .gap_2()
                                .px_2()
                                .py_1()
                                .rounded_md()
                                .cursor_pointer()
                                .hover(|el| el.bg(theme::hover()))
                                .child(div().size_2().rounded_full().bg(tone_color(item.tone)))
                                .child(div().flex_1().min_w_0().text_sm().truncate().child(item.title.clone()))
                                .child(div().text_xs().text_color(theme::muted()).child(item.project.clone()))
                                .on_click(cx.listener(move |this, _: &ClickEvent, _, _| this.send(In::WalkTo(r.clone()))))
                        })))
                    })
                    .child(
                        h_flex()
                            .id("office-tray")
                            .gap_3()
                            .px_3()
                            .h_8()
                            .items_center()
                            .text_xs()
                            .cursor_pointer()
                            .child(h_flex().gap_1p5().items_center().child(dot(0xf59e0b)).child(div().font_weight(FontWeight::SEMIBOLD).child(needs.to_string())).child(t("native.office.legend.needs")))
                            .child(h_flex().gap_1p5().items_center().child(dot(0x60a5fa)).child(div().font_weight(FontWeight::SEMIBOLD).child(working.to_string())).child(t("native.office.legend.working")))
                            .child(h_flex().gap_1p5().items_center().child(dot(0x10b981)).child(div().font_weight(FontWeight::SEMIBOLD).child(review.to_string())).child(t("native.office.tray.review")))
                            .child(div().text_color(theme::faint()).child(crate::icons::of(if open { "▾" } else { "▸" })))
                            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.tray = !this.tray;
                                cx.notify();
                            })),
                    ),
            ),
        )
    }

    fn hint_el(&self) -> Option<AnyElement> {
        let title = |r: &str| self.model.areas.iter().flat_map(|a| a.desks.iter()).find(|d| d.id == r).map(|d| d.title.clone());
        let text = match &self.hover {
            Some(h) if Some(h) == self.near.as_ref() => title(h).map(|t| tv("native.office.clickToOpen", &[("title", t.as_str().into())])),
            Some(h) => title(h).map(|t| tv("native.office.clickToWalk", &[("title", t.as_str().into())])),
            None if self.hint => Some(if self.at.project.is_none() { t("native.office.hint.campus") } else { t("native.office.hint.short") }),
            None => None,
        }?;
        Some(div().absolute().bottom_3().left_0().right_0().flex().justify_center().child(div().px_3().py_1().rounded_full().bg(rgba(0x0f172acc)).text_xs().text_color(gpui::white()).child(text)).into_any_element())
    }

    /// corner is the bottom right: the desk's card when you're at one, and the help.
    fn corner(&self, desk: Option<model::DeskModel>, lead: Option<model::AreaModel>, w: f32, cx: &mut Context<Self>) -> impl IntoElement {
        let right = if self.chat.is_some() { chat_width(w) + 12. } else { 12. };
        let no_chat = self.chat.is_none();
        v_flex()
            .absolute()
            .bottom_3()
            .right(px(right))
            .items_end()
            .gap_2()
            .when(self.help, |el| el.child(self.help_card()))
            .when_some(desk.filter(|_| no_chat), |el, d| el.child(self.desk_card(d, cx)))
            .when_some(lead.filter(|_| no_chat), |el, a| {
                let p = a.project.clone();
                el.child(
                    card()
                        .w(px(336.))
                        .p_3()
                        .child(div().text_xs().font_weight(FontWeight::SEMIBOLD).text_color(theme::muted()).child(t(&format!("native.office.lead.{}", lead_word(a.lead.tone))).to_uppercase()))
                        .child(div().mt_1().font_weight(FontWeight::SEMIBOLD).child(tv("native.office.lead.title", &[("project", a.label.as_str().into())])))
                        .child(div().mt_1().text_sm().text_color(theme::muted()).line_clamp(3).child(a.lead.line.clone().unwrap_or_else(|| t("native.office.lead.quiet"))))
                        .child(h_flex().mt_3().child(button("office-open-lead", t("native.office.openProjectChat")).bg(theme::accent()).text_color(gpui::white()).on_click(cx.listener(move |_, _: &ClickEvent, _, cx| cx.emit(OfficeEvent::Open { project: p.clone(), who: "lead".into() }))))),
                )
            })
            .child(card().child(tip(button("office-help", "?"), t("native.office.help")).on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                this.help = !this.help;
                cx.notify();
            }))))
    }

    fn desk_card(&self, d: model::DeskModel, cx: &mut Context<Self>) -> impl IntoElement {
        let (project, who) = (d.project.clone(), d.name.clone());
        let pr = d.pr.clone().filter(|p| !p.url.is_empty());
        card()
            .w(px(336.))
            .p_3()
            .child(
                h_flex()
                    .gap_2()
                    .text_xs()
                    .font_weight(FontWeight::SEMIBOLD)
                    .child(div().size_2().rounded_full().bg(tone_color(d.tone)))
                    .child(div().text_color(theme::muted()).child(tone_label(d.tone).to_uppercase()))
                    .child(div().ml_auto().truncate().text_color(theme::faint()).child(d.name.clone())),
            )
            .child(div().mt_1p5().font_weight(FontWeight::SEMIBOLD).line_clamp(2).child(d.title.clone()))
            .child(div().mt_1().text_xs().text_color(theme::muted()).font_family(theme::mono()).truncate().child(d.branch.clone()))
            .when_some(d.question.clone(), |el, q| el.child(div().mt_2().px_2p5().py_2().rounded_lg().bg(rgba(0xfbbf2422)).text_sm().line_clamp(4).child(q)))
            .when_some(d.pr.clone(), |el, pr| el.child(div().mt_1p5().text_sm().text_color(theme::muted()).child(pr_line(&pr).text)))
            .when_some(d.changes.clone(), |el, c| el.child(div().mt_1().text_xs().text_color(theme::muted()).child(changes_text(c.files, c.insertions, c.deletions))))
            .child(
                h_flex()
                    .mt_3()
                    .gap_2()
                    .child(button("office-open-chat", t("native.office.openChat")).bg(theme::accent()).text_color(gpui::white()).on_click(cx.listener(move |_, _: &ClickEvent, _, cx| cx.emit(OfficeEvent::Open { project: project.clone(), who: who.clone() }))))
                    .when_some(pr, |el, pr| {
                        let url = pr.url.clone();
                        el.child(button("office-open-pr", tv("native.office.openPr", &[("number", pr.number.into())])).border_1().border_color(theme::border()).on_click(cx.listener(move |_, _: &ClickEvent, _, cx| cx.emit(OfficeEvent::OpenUrl(url.clone())))))
                    }),
            )
    }

    fn help_card(&self) -> impl IntoElement {
        let key = |k: String, what: &str| h_flex().gap_3().child(div().w(px(84.)).text_xs().font_weight(FontWeight::SEMIBOLD).text_color(theme::muted()).child(k)).child(div().text_sm().child(t(what)));
        let legend = [("!", 0xf59e0b, "native.office.legend.needs"), ("⋯", 0x60a5fa, "native.office.legend.working"), ("✓", 0x10b981, "native.office.legend.finished"), ("×", 0xef4444, "native.office.legend.error"), ("z", 0x94a3b8, "native.office.legend.stopped"), ("○", 0xcbd5e1, "native.office.legend.idle")];
        card()
            .w(px(336.))
            .p_3()
            .child(
                v_flex()
                    .gap_1p5()
                    .child(key(t("native.office.keys.click"), "native.office.keys.walk"))
                    .child(key("W A S D".into(), "native.office.keys.walkKeys"))
                    .child(key(t("native.office.keys.drag"), "native.office.keys.turn"))
                    .child(key(t("native.office.keys.scroll"), "native.office.keys.zoom"))
                    .child(key("E".into(), "native.office.keys.open"))
                    .child(key("N".into(), "native.office.keys.next"))
                    .child(key("Esc".into(), "native.office.keys.back")),
            )
            .child(div().mt_3().pt_3().border_t_1().border_color(theme::border()).child(h_flex().flex_wrap().gap_x_3().gap_y_1p5().children(legend.into_iter().map(|(g, c, k)| {
                h_flex()
                    .gap_1p5()
                    .w(px(140.))
                    .child(div().size_4().rounded_full().bg(rgb(c)).flex().items_center().justify_center().text_size(px(10.)).font_weight(FontWeight::BOLD).text_color(gpui::black()).child(g))
                    .child(div().text_xs().child(t(k)))
            }))))
    }
}
