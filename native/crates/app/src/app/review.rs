//! The review (desktop/src/renderer/components/ReadyToReview.tsx's
//! ReviewCarousel, ReviewProof.tsx, ReviewCompare.tsx): one agent's work,
//! over the whole window, built around the decision. The header says what it
//! is and where its pull request stands; the body is its proof — before/after
//! pairs side by side or flipped, recordings played with their chapters,
//! screenshots — or its diff, or the details; the footer is the verdicts.
//! Opened on work that waits, it walks on through the rest that waits.
//!
//! Keys, as the Electron app's: ← → the proof, N/P the agents, D diff, I
//! details, R mark reviewed, C request changes, M twice merges, Q a pair's
//! mode, Space plays (or flips), Esc backs out.
use std::collections::HashMap;
use std::path::PathBuf;

use agentbox_api::{
    Client, MediaItem, MergePullRequestRequest, MergeWhenGreen, MergeWhenGreenRequest,
    ProjectPullRequests, PullRequest, RequestChangesRequest, ReviewRequest, Settings, WorkItem,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{InputEvent, Textarea, TextareaState};
use gpui_component::text::TextView;
use gpui_component::{h_flex, v_flex};

use super::controls::{self, Btn};
use super::merge;
use super::work::{outcome_badge, pr_chip};
use crate::diff::DiffView;
use crate::media::{self, Shown};
use crate::toasts::{self, Kind, Toast};
#[cfg(feature = "video")]
use crate::video::VideoPlayer;
use crate::{prefs, theme};

pub enum ReviewEvent {
    Close,
    /// Open the agent's chat (its ref).
    Chat(String),
    /// A verdict or a merge changed what waits.
    Changed,
}

#[derive(Clone, Copy, PartialEq)]
enum Pane {
    Proof,
    Diff,
    Details,
}

/// How a before/after pair of screenshots is compared.
#[derive(Clone, Copy, PartialEq)]
enum Compare {
    Side,
    Slider,
    Flip,
}

impl Compare {
    fn name(self) -> &'static str {
        match self {
            Compare::Side => "side",
            Compare::Slider => "slider",
            Compare::Flip => "flip",
        }
    }

    fn from(name: &str) -> Self {
        match name {
            "slider" => Compare::Slider,
            "flip" => Compare::Flip,
            _ => Compare::Side,
        }
    }

    /// next is Q's cycle (nextMode).
    fn next(self) -> Self {
        match self {
            Compare::Side => Compare::Slider,
            Compare::Slider => Compare::Flip,
            Compare::Flip => Compare::Side,
        }
    }
}

/// Where a pair's mode and highlight are remembered (lib/compare.ts).
const COMPARE_KEY: &str = "review.compare";

/// What highlighting found between a pair's screenshots (lib/compare.ts).
#[derive(Clone)]
enum Difference {
    Working,
    Ready {
        overlay: PathBuf,
        changed: u64,
        total: u64,
    },
    Sizes(String, String),
    Failed(String),
}

/// pixel_diff is pixelDiff: a pixel changed when a channel moved by more
/// than 24; the overlay has those in solid magenta and the 16px cells around
/// them tinted. None when nothing changed.
fn pixel_diff(a: &image::RgbaImage, b: &image::RgbaImage) -> (Option<image::RgbaImage>, u64) {
    const CELL: u32 = 16;
    let (w, h) = a.dimensions();
    let cols = w.div_ceil(CELL);
    let mut cells = vec![false; (cols * h.div_ceil(CELL)) as usize];
    let mut hit = vec![false; (w * h) as usize];
    let mut changed = 0u64;
    for (p, (x, y)) in a.pixels().zip(b.pixels()).enumerate() {
        let d = (0..4)
            .map(|c| (x[c] as i32 - y[c] as i32).abs())
            .max()
            .unwrap_or(0);
        if d <= 24 {
            continue;
        }
        hit[p] = true;
        changed += 1;
        let (px, py) = (p as u32 % w, p as u32 / w);
        cells[((py / CELL) * cols + px / CELL) as usize] = true;
    }
    if changed == 0 {
        return (None, 0);
    }
    let overlay = image::RgbaImage::from_fn(w, h, |x, y| {
        let p = (y * w + x) as usize;
        if hit[p] {
            image::Rgba([255, 0, 170, 230])
        } else if cells[((y / CELL) * cols + x / CELL) as usize] {
            image::Rgba([255, 0, 170, 60])
        } else {
            image::Rgba([0, 0, 0, 0])
        }
    });
    (Some(overlay), changed)
}

fn difference_of(before: PathBuf, after: PathBuf, out: PathBuf) -> Difference {
    let open = |p: &PathBuf| image::open(p).map(|i| i.to_rgba8());
    let (a, b) = match (open(&before), open(&after)) {
        (Ok(a), Ok(b)) => (a, b),
        (Err(e), _) | (_, Err(e)) => return Difference::Failed(e.to_string()),
    };
    if a.dimensions() != b.dimensions() {
        let size = |i: &image::RgbaImage| format!("{}×{}", i.width(), i.height());
        return Difference::Sizes(size(&a), size(&b));
    }
    let total = a.width() as u64 * a.height() as u64;
    let (overlay, changed) = pixel_diff(&a, &b);
    let overlay = overlay.unwrap_or_else(|| image::RgbaImage::new(1, 1));
    match overlay.save_with_format(&out, image::ImageFormat::Png) {
        Ok(()) => Difference::Ready {
            overlay: out,
            changed,
            total,
        },
        Err(e) => Difference::Failed(e.to_string()),
    }
}

/// The zoom steps (lib/zoom.ts zoomStep) and how far in it goes.
const ZOOM_STEP: f32 = 1.25;
const ZOOM_MAX: f32 = 8.;

pub struct ReviewView {
    client: Client,
    focus: FocusHandle,
    item: WorkItem,
    /// The walk: what waits, the item on screen among it. Empty when the
    /// item is only looked at.
    queue: Vec<WorkItem>,
    /// What was shown before, for Back.
    trail: Vec<WorkItem>,
    media: Option<Result<Vec<MediaItem>, String>>,
    gallery: Vec<Shown>,
    at: usize,
    /// Fetched files by media id, and the ones that couldn't be.
    files: HashMap<String, PathBuf>,
    failed: HashMap<String, String>,
    #[cfg(feature = "video")]
    players: HashMap<String, Entity<VideoPlayer>>,
    compare: Compare,
    flipped: bool,
    /// Slider: where the divider is (0..1) and the box it's dragged in.
    slider: f32,
    slider_box: std::rc::Rc<std::cell::Cell<Bounds<Pixels>>>,
    /// Highlight changes is on, and what it found for each pair.
    highlight: bool,
    differences: HashMap<String, Difference>,
    /// How far what's on stage is zoomed (1 fits it), and whether it's
    /// open large over the whole review.
    zoom: f32,
    /// The zoom as the players read it, and the scroll every zoomed picture
    /// pans by: a pair's halves, side by side, pan together.
    zoom_shared: std::rc::Rc<std::cell::Cell<f32>>,
    pan: ScrollHandle,
    expanded: bool,
    /// The agent's Hatch pages, part of its proof.
    pages: Option<Vec<agentbox_api::Artifact>>,
    pane: Option<Pane>,
    diff: Option<Result<Entity<DiffView>, String>>,
    /// The diff's notes as Request changes lists them.
    notes_list: Option<Entity<crate::diff::NotesList>>,
    /// The project's pull requests, for the methods its repository allows
    /// and whether its account may merge.
    pulls: Option<ProjectPullRequests>,
    /// The method Merge uses, picked from its ▾.
    method: Option<String>,
    /// Settings' "Resolved — merge when CI is green" is on.
    green: bool,
    report_open: bool,
    changing: bool,
    changes: Entity<TextareaState>,
    busy: bool,
    error: Option<SharedString>,
    /// M was pressed once: a second press merges.
    armed: bool,
    _sub: Subscription,
}

impl EventEmitter<ReviewEvent> for ReviewView {}

impl ReviewView {
    pub fn new(
        client: Client,
        item: WorkItem,
        queue: Vec<WorkItem>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        let changes = cx.new(|cx| {
            TextareaState::new(window, cx)
                .auto_grow(2, 8)
                .placeholder(t("review.changesPlaceholder"))
        });
        // Ctrl or ⌘ with Enter sends them (Enter alone is a new line).
        let sub = cx.subscribe_in(&changes, window, |this, _, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter {
                secondary: true, ..
            } = ev
            {
                this.send_changes(window, cx);
            }
        });
        let focus = cx.focus_handle();
        focus.focus(window, cx);
        let mut this = Self {
            client,
            focus,
            item: item.clone(),
            queue,
            trail: Vec::new(),
            media: None,
            gallery: Vec::new(),
            at: 0,
            files: HashMap::new(),
            failed: HashMap::new(),
            #[cfg(feature = "video")]
            players: HashMap::new(),
            compare: Compare::from(
                prefs::get(COMPARE_KEY)
                    .as_ref()
                    .and_then(|v| v.get("mode"))
                    .and_then(|m| m.as_str())
                    .unwrap_or("side"),
            ),
            flipped: false,
            slider: 0.5,
            slider_box: Default::default(),
            highlight: prefs::get(COMPARE_KEY)
                .as_ref()
                .and_then(|v| v.get("highlight"))
                .and_then(|m| m.as_bool())
                .unwrap_or(false),
            differences: HashMap::new(),
            zoom: 1.,
            zoom_shared: std::rc::Rc::new(std::cell::Cell::new(1.)),
            pan: ScrollHandle::new(),
            expanded: false,
            pages: None,
            pane: None,
            diff: None,
            notes_list: None,
            pulls: None,
            method: None,
            green: false,
            report_open: false,
            changing: false,
            changes,
            busy: false,
            error: None,
            armed: false,
            _sub: sub,
        };
        this.show(item, cx);
        let client = this.client.clone();
        cx.spawn(async move |this, cx| {
            if let Ok(s) = client.get::<Settings>("/v1/settings").await {
                this.update(cx, |this, cx| {
                    this.green = s.merge_when_green;
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
        this
    }

    /// show puts an agent's work on screen and reads its proof.
    fn show(&mut self, item: WorkItem, cx: &mut Context<Self>) {
        self.item = item;
        self.media = None;
        self.gallery.clear();
        self.at = 0;
        #[cfg(feature = "video")]
        self.players.clear();
        self.pane = None;
        self.diff = None;
        self.notes_list = None;
        self.report_open = false;
        self.changing = false;
        self.error = None;
        self.armed = false;
        self.flipped = false;
        self.set_zoom_quiet(1.);
        self.expanded = false;
        self.pages = None;
        let i = &self.item.item;
        {
            let client = self.client.clone();
            let path = format!("/v1/projects/{}/artifacts", i.project);
            let r#ref = i.r#ref.clone();
            cx.spawn(async move |this, cx| {
                let list = client
                    .get::<agentbox_api::Artifacts>(&path)
                    .await
                    .unwrap_or_default();
                this.update(cx, |this, cx| {
                    if this.item.item.r#ref != r#ref {
                        return;
                    }
                    this.pages = Some(media::live_pages(&list, &r#ref));
                    this.rebuild();
                    cx.notify();
                })
                .ok();
            })
            .detach();
        }
        if self.pulls.as_ref().is_none_or(|p| p.project != i.project) {
            self.pulls = None;
            self.method = None;
            let client = self.client.clone();
            let path = format!("/v1/projects/{}/pulls", i.project);
            cx.spawn(async move |this, cx| {
                if let Ok(p) = client.get::<ProjectPullRequests>(&path).await {
                    this.update(cx, |this, cx| {
                        this.pulls = Some(p);
                        cx.notify();
                    })
                    .ok();
                }
            })
            .detach();
        }
        // A past agent's media is the project's under its name: its own
        // routes went with its machine.
        let path = if self.item.past {
            format!("/v1/projects/{}/media?agent={}", i.project, i.agent)
        } else {
            format!("/v1/agents/{}/{}/media", i.project, i.agent)
        };
        let client = self.client.clone();
        let r#ref = i.r#ref.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<Vec<MediaItem>>(&path).await;
            this.update(cx, |this, cx| {
                if this.item.item.r#ref != r#ref {
                    return;
                }
                match res {
                    Ok(m) => {
                        this.media = Some(Ok(m));
                        this.rebuild();
                        this.fetch_files(cx);
                    }
                    Err(e) => this.media = Some(Err(e.to_string())),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// rebuild is the gallery from the media and the pages read so far.
    fn rebuild(&mut self) {
        let mut g = match &self.media {
            Some(Ok(m)) => media::gallery(m),
            _ => Vec::new(),
        };
        g.extend(self.pages.iter().flatten().cloned().map(Shown::Page));
        self.at = self.at.min(g.len().saturating_sub(1));
        self.gallery = g;
    }

    /// set_compare picks how pairs are compared, remembered for next time.
    fn set_compare(&mut self, compare: Compare, highlight: bool, cx: &mut Context<Self>) {
        self.compare = compare;
        self.highlight = highlight;
        prefs::set(
            COMPARE_KEY,
            serde_json::json!({ "mode": compare.name(), "highlight": highlight }),
        );
        cx.notify();
    }

    /// ensure_difference works out the pair on stage's highlight when it's
    /// on, both its pictures are here, and it isn't known yet.
    fn ensure_difference(&mut self, cx: &mut Context<Self>) {
        if !self.highlight || self.pane() != Pane::Proof {
            return;
        }
        let Some(Shown::Pair(p)) = self.gallery.get(self.at) else {
            return;
        };
        if p.before.kind != "screenshot" {
            return;
        }
        let key = format!("{}|{}", p.before.id, p.after.id);
        if self.differences.contains_key(&key) {
            return;
        }
        let (Some(a), Some(b)) = (
            self.files.get(&p.before.id).cloned(),
            self.files.get(&p.after.id).cloned(),
        ) else {
            return;
        };
        self.differences.insert(key.clone(), Difference::Working);
        let out = std::env::temp_dir().join(format!(
            "agentbox-highlight-{}-{}.png",
            p.before.id, p.after.id
        ));
        let task = cx
            .background_executor()
            .spawn(async move { difference_of(a, b, out) });
        cx.spawn(async move |this, cx| {
            let d = task.await;
            this.update(cx, |this, cx| {
                this.differences.insert(key, d);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// set_zoom_quiet sets the zoom without a redraw (a new item on stage).
    fn set_zoom_quiet(&mut self, zoom: f32) {
        self.zoom = zoom;
        self.zoom_shared.set(zoom);
        self.pan.set_offset(point(px(0.), px(0.)));
    }

    fn set_zoom(&mut self, zoom: f32, cx: &mut Context<Self>) {
        let from = self.zoom;
        self.zoom = zoom.clamp(1., ZOOM_MAX);
        crate::viewers::rezoom(&self.pan, from, self.zoom);
        self.zoom_shared.set(self.zoom);
        if self.zoom <= 1.0001 {
            self.pan.set_offset(point(px(0.), px(0.)));
        }
        cx.notify();
    }

    /// fetch_files reads every picture and recording of the proof into the cache.
    fn fetch_files(&mut self, cx: &mut Context<Self>) {
        let mut wanted: Vec<MediaItem> = Vec::new();
        for s in &self.gallery {
            match s {
                Shown::Pair(p) => wanted.extend([p.before.clone(), p.after.clone()]),
                Shown::Recording(t) => wanted.push(t.media.clone()),
                Shown::Screenshot(m) => wanted.push(m.clone()),
                Shown::Page(_) => {}
            }
        }
        for m in wanted {
            if self.files.contains_key(&m.id) {
                continue;
            }
            let client = self.client.clone();
            cx.spawn(async move |this, cx| {
                let id = m.id.clone();
                let res = media::fetch(client, m).await;
                this.update(cx, |this, cx| {
                    match res {
                        Ok(p) => {
                            this.files.insert(id, p);
                        }
                        Err(e) => {
                            this.failed.insert(id, e.to_string());
                        }
                    }
                    this.sync_players(cx);
                    cx.notify();
                })
                .ok();
            })
            .detach();
        }
    }

    /// sync_players keeps a player for each recording on stage, and none for
    /// what has left it.
    #[cfg(feature = "video")]
    fn sync_players(&mut self, cx: &mut Context<Self>) {
        let on_stage: Vec<(MediaItem, Vec<f64>)> = match self.gallery.get(self.at) {
            Some(Shown::Recording(t)) if self.pane() == Pane::Proof => {
                vec![(t.media.clone(), t.chapters.iter().map(|c| c.at).collect())]
            }
            Some(Shown::Pair(p)) if p.before.kind == "recording" && self.pane() == Pane::Proof => {
                vec![
                    (p.before.clone(), Vec::new()),
                    (p.after.clone(), Vec::new()),
                ]
            }
            _ => Vec::new(),
        };
        self.players
            .retain(|id, _| on_stage.iter().any(|(m, _)| &m.id == id));
        for (m, marks) in on_stage {
            if self.players.contains_key(&m.id) {
                continue;
            }
            if let Some(path) = self.files.get(&m.id).cloned() {
                let player = cx.new(|cx| {
                    let mut p = VideoPlayer::new(path, cx);
                    p.set_marks(marks, cx);
                    p.set_zoom(self.zoom_shared.clone(), self.pan.clone());
                    p
                });
                self.players.insert(m.id.clone(), player);
            }
        }
    }

    #[cfg(not(feature = "video"))]
    fn sync_players(&mut self, _cx: &mut Context<Self>) {}

    /// pane is the one on screen: the one picked, else the proof when there
    /// is any, else the details (lib/review.ts reviewPane).
    fn pane(&self) -> Pane {
        if let Some(p) = self.pane {
            return p;
        }
        match &self.media {
            Some(Ok(_)) if self.gallery.is_empty() => Pane::Details,
            _ => Pane::Proof,
        }
    }

    fn set_pane(&mut self, pane: Pane, cx: &mut Context<Self>) {
        // Pressed again, a pane goes back to the proof.
        let pane = if self.pane() == pane && pane != Pane::Proof {
            Pane::Proof
        } else {
            pane
        };
        self.pane = Some(pane);
        if pane == Pane::Diff && self.diff.is_none() {
            self.load_diff(cx);
        }
        self.sync_players(cx);
        cx.notify();
    }

    fn load_diff(&mut self, cx: &mut Context<Self>) {
        let i = &self.item.item;
        let path = if self.item.past {
            format!("/v1/archived-agents/{}/{}/diff", i.project, i.agent)
        } else {
            format!("/v1/agents/{}/{}/diff", i.project, i.agent)
        };
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(agentbox_api::client::Method::GET, &path, None)
                .await;
            this.update(cx, |this, cx| {
                this.diff = Some(match res {
                    Ok(b) => {
                        let text = String::from_utf8_lossy(&b).into_owned();
                        let notes = (!this.item.past).then(|| {
                            let i = &this.item.item;
                            (
                                this.client.clone(),
                                format!("/v1/agents/{}/{}/review-notes", i.project, i.agent),
                            )
                        });
                        Ok(cx.new(|cx| {
                            let d = DiffView::text("review-diff", &text, cx);
                            match notes {
                                Some((c, base)) => d.with_notes(c, base, cx),
                                None => d,
                            }
                        }))
                    }
                    Err(e) => Err(e.to_string()),
                });
                this.notes_list = match &this.diff {
                    Some(Ok(d)) if !this.item.past => {
                        let d = d.clone();
                        Some(cx.new(|cx| crate::diff::NotesList::new(d, cx)))
                    }
                    _ => None,
                };
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn step(&mut self, by: isize, cx: &mut Context<Self>) {
        let n = self.gallery.len();
        if n == 0 {
            return;
        }
        self.at = (self.at as isize + by).rem_euclid(n as isize) as usize;
        self.flipped = false;
        self.set_zoom_quiet(1.);
        self.sync_players(cx);
        cx.notify();
    }

    fn pick(&mut self, at: usize, cx: &mut Context<Self>) {
        self.at = at;
        self.flipped = false;
        self.sync_players(cx);
        cx.notify();
    }

    /// position is where the item is in the walk.
    fn position(&self) -> Option<(usize, usize)> {
        let ix = self
            .queue
            .iter()
            .position(|w| w.item.r#ref == self.item.item.r#ref)?;
        Some((ix + 1, self.queue.len()))
    }

    /// next is the one after this in the walk, going round to those before it.
    fn next(&self) -> Option<WorkItem> {
        let ix = self
            .queue
            .iter()
            .position(|w| w.item.r#ref == self.item.item.r#ref)?;
        let n = self.queue.len();
        (1..n).map(|d| &self.queue[(ix + d) % n]).next().cloned()
    }

    fn go_next(&mut self, cx: &mut Context<Self>) {
        if let Some(next) = self.next() {
            self.trail.push(self.item.clone());
            self.show(next, cx);
        }
    }

    fn go_back(&mut self, cx: &mut Context<Self>) {
        if let Some(prev) = self.trail.pop() {
            self.show(prev, cx);
        }
    }

    /// advance takes the item out of the walk, its verdict given, and goes on
    /// to the next; with none left the review closes.
    fn advance(&mut self, cx: &mut Context<Self>) {
        cx.emit(ReviewEvent::Changed);
        let next = self.next();
        let done = self.item.item.r#ref.clone();
        self.queue.retain(|w| w.item.r#ref != done);
        self.trail.retain(|w| w.item.r#ref != done);
        match next {
            Some(n) => self.show(n, cx),
            None => {
                toasts::info(cx, t("review.allDone"));
                cx.emit(ReviewEvent::Close)
            }
        }
    }

    fn awaiting(&self) -> bool {
        self.item.status == "review" && !self.item.past
    }

    fn mark_reviewed(&mut self, cx: &mut Context<Self>) {
        if self.busy || !self.awaiting() {
            return;
        }
        let i = &self.item.item;
        let path = format!("/v1/agents/{}/{}/review", i.project, i.agent);
        let req = ReviewRequest {
            verdict: "reviewed".into(),
            fingerprint: i.fingerprint.clone(),
        };
        self.send(path, serde_json::to_vec(&req).unwrap_or_default(), cx);
    }

    fn send_changes(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let text = self.changes.read(cx).value().trim().to_string();
        let drafts = self.drafts(cx);
        if self.busy || (text.is_empty() && drafts == 0) {
            return;
        }
        let notes_path = (!self.item.past).then(|| {
            let i = &self.item.item;
            format!("/v1/agents/{}/{}/review-notes", i.project, i.agent)
        });
        let i = &self.item.item;
        let path = format!("/v1/agents/{}/{}/request-changes", i.project, i.agent);
        let req = RequestChangesRequest {
            text,
            fingerprint: i.fingerprint.clone(),
            notes: Vec::new(),
        };
        self.changes.update(cx, |s, cx| s.set_value("", window, cx));
        self.focus.focus(window, cx);
        // The review goes on as soon as they're sent: the daemon may have
        // to start the agent's machine first. If they don't go, what was
        // written is in the toast, to copy.
        let agent = i.agent.clone();
        let text = req.text.clone();
        let client = self.client.clone();
        let mut req = req;
        Toast::new(
            Kind::Info,
            tv("review.changesSent", &[("agent", (&agent).into())]),
        )
        .show(cx);
        cx.spawn(async move |this, cx| {
            // The drafts on the diff's lines go with them.
            if let Some(np) = notes_path
                && let Ok(notes) = client.get::<Vec<agentbox_api::ReviewNote>>(&np).await
            {
                req.notes = notes
                    .into_iter()
                    .filter(|n| n.status == "draft")
                    .map(|n| n.id)
                    .collect();
            }
            let body = serde_json::to_vec(&req).unwrap_or_default();
            let res = client
                .raw(agentbox_api::client::Method::POST, &path, Some(body))
                .await;
            this.update(cx, |_, cx| {
                cx.emit(ReviewEvent::Changed);
                if let Err(e) = res {
                    let copy = text.clone();
                    Toast::new(
                        Kind::Error,
                        tv(
                            "review.changesFailed",
                            &[("error", (&e.to_string()).into())],
                        ),
                    )
                    .description(text)
                    .action(t("common.copy"), move |_, cx| {
                        cx.write_to_clipboard(ClipboardItem::new_string(copy.clone()))
                    })
                    .show(cx);
                }
            })
            .ok();
        })
        .detach();
        self.advance(cx);
    }

    /// drafts is how many line notes wait to go with Request changes.
    fn drafts(&self, cx: &App) -> usize {
        match &self.diff {
            Some(Ok(d)) => d.read(cx).drafts().len(),
            _ => 0,
        }
    }

    /// send posts a verdict and goes on when it's taken.
    fn send(&mut self, path: String, body: Vec<u8>, cx: &mut Context<Self>) {
        self.busy = true;
        self.error = None;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(agentbox_api::client::Method::POST, &path, Some(body))
                .await;
            this.update(cx, |this, cx| {
                this.busy = false;
                match res {
                    Ok(_) => this.advance(cx),
                    Err(e) => toasts::error(
                        cx,
                        tv("review.failed", &[("error", (&e.to_string()).into())]),
                    ),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// pr is the item's pull request.
    fn pr(&self) -> Option<&PullRequest> {
        self.item.item.pr.as_ref().filter(|p| p.number > 0)
    }

    /// merge_block is why Merge can't, if it can't (lib mergeBlockReason).
    fn merge_block(&self) -> Option<String> {
        let Some(pr) = self.pr() else {
            return Some(t("review.mergeBlocked.none"));
        };
        match pr.state.as_str() {
            "merged" => Some(t("review.mergeBlocked.merged")),
            "closed" => Some(t("review.mergeBlocked.closed")),
            _ => super::pulls::merge_block(pr, self.pulls.as_ref()),
        }
    }

    /// methods is what the repository allows; method the one Merge uses:
    /// picked here, else the project's remembered one.
    fn methods(&self) -> Vec<String> {
        merge::offered(self.pulls.as_ref().map_or(&[][..], |p| &p.merge_methods))
    }

    fn method(&self) -> String {
        let methods = self.methods();
        self.method
            .clone()
            .filter(|m| methods.contains(m))
            .unwrap_or_else(|| merge::default_method(&methods, &self.item.item.project))
    }

    /// merge_when_green resolves the work and has the daemon merge its pull
    /// request once its checks pass; the review goes on, and the toast can
    /// take it back.
    fn merge_when_green(&mut self, cx: &mut Context<Self>) {
        if self.busy || !self.awaiting() || self.merge_block().is_some() {
            return;
        }
        let Some(pr) = self.pr().cloned() else {
            return;
        };
        let i = self.item.item.clone();
        let req = MergeWhenGreenRequest {
            method: self.method(),
            agent: i.agent.clone(),
            fingerprint: i.fingerprint.clone(),
        };
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, MergeWhenGreen>(
                    &format!(
                        "/v1/projects/{}/pulls/{}/merge-when-green",
                        i.project, pr.number
                    ),
                    &req,
                )
                .await;
            this.update(cx, |_, cx| {
                cx.emit(ReviewEvent::Changed);
                match res {
                    Ok(mark) => {
                        let (client, project, number) =
                            (client.clone(), i.project.clone(), mark.number);
                        Toast::new(
                            Kind::Info,
                            tv("review.mergeWhenGreen.queued", &[("number", number.into())]),
                        )
                        .description(if i.title.is_empty() {
                            i.agent.clone()
                        } else {
                            i.title.clone()
                        })
                        .action(t("review.mergeWhenGreen.cancel"), move |_, cx| {
                            cancel_green(client.clone(), project.clone(), number, false, cx)
                        })
                        .show(cx);
                    }
                    Err(e) => toasts::error(
                        cx,
                        tv(
                            "review.mergeWhenGreen.failed",
                            &[("error", (&e.to_string()).into())],
                        ),
                    ),
                }
            })
            .ok();
        })
        .detach();
        self.advance(cx);
    }

    /// merge merges the pull request (squash: the daemon's default when the
    /// project hasn't said) and, merged, marks the work reviewed.
    fn merge(&mut self, cx: &mut Context<Self>) {
        if self.busy || self.merge_block().is_some() || !self.awaiting() {
            return;
        }
        let Some(pr) = self.pr().cloned() else {
            return;
        };
        let i = self.item.item.clone();
        let client = self.client.clone();
        let method = self.method();
        self.busy = true;
        self.armed = false;
        self.error = None;
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, PullRequest>(
                    &format!("/v1/projects/{}/pulls/{}/merge", i.project, pr.number),
                    &MergePullRequestRequest {
                        method: method.clone(),
                        confirmed: false,
                    },
                )
                .await;
            if res.is_ok() {
                let _ = client
                    .raw(
                        agentbox_api::client::Method::POST,
                        &format!("/v1/agents/{}/{}/review", i.project, i.agent),
                        serde_json::to_vec(&ReviewRequest {
                            verdict: "reviewed".into(),
                            fingerprint: i.fingerprint.clone(),
                        })
                        .ok(),
                    )
                    .await;
            }
            this.update(cx, |this, cx| {
                this.busy = false;
                match res {
                    Ok(merged) => {
                        let base = if merged.base_branch.is_empty() {
                            pr.base_branch.clone()
                        } else {
                            merged.base_branch.clone()
                        };
                        Toast::new(
                            Kind::Success,
                            tv(
                                "review.merged",
                                &[
                                    ("number", merged.number.into()),
                                    ("agent", (&i.agent).into()),
                                    ("base", (&base).into()),
                                ],
                            ),
                        )
                        .description(merged.title.clone())
                        .show(cx);
                        this.advance(cx)
                    }
                    Err(e) => {
                        this.error = Some(
                            tv(
                                "memory.pulls.mergeFailed",
                                &[("error", (&e.to_string()).into())],
                            )
                            .into(),
                        )
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn open_changes(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if !self.awaiting() {
            return;
        }
        self.changing = true;
        // Its notes are the diff's: read them if the diff isn't yet.
        if self.diff.is_none() {
            self.load_diff(cx);
        }
        let f = self.changes.read(cx).focus_handle(cx);
        f.focus(window, cx);
        cx.notify();
    }

    fn close_changes(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.changing = false;
        self.focus.focus(window, cx);
        cx.notify();
    }

    /// play plays or pauses what's on stage, or flips a pair.
    fn play(&mut self, cx: &mut Context<Self>) {
        match self.gallery.get(self.at) {
            Some(Shown::Pair(p)) if p.before.kind == "screenshot" => {
                self.flipped = !self.flipped;
                cx.notify();
            }
            _ =>
            {
                #[cfg(feature = "video")]
                for p in self.players.values() {
                    p.update(cx, |p, cx| p.toggle(cx));
                }
            }
        }
    }

    fn on_key(&mut self, ev: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        let typing = self.changes.read(cx).focus_handle(cx).is_focused(window);
        let k = &ev.keystroke;
        if k.key == "escape" {
            if let Some(Ok(d)) = &self.diff
                && d.read(cx).notes_open()
            {
                d.update(cx, |d, cx| d.close_note(window, cx));
                cx.stop_propagation();
                return;
            }
            if self.expanded {
                self.expanded = false;
                cx.notify();
            } else if self.changing {
                self.close_changes(window, cx);
            } else if self.pane() != Pane::Proof && self.pane.is_some() {
                self.set_pane(Pane::Proof, cx);
            } else {
                cx.emit(ReviewEvent::Close);
            }
            cx.stop_propagation();
            return;
        }
        let noting = matches!(&self.diff, Some(Ok(d)) if d.read(cx).notes_open());
        if typing || noting {
            return;
        }
        if k.modifiers.control || k.modifiers.alt || k.modifiers.platform {
            return;
        }
        if k.key != "m" {
            self.armed = false;
        }
        match k.key.as_str() {
            "left" if self.pane() == Pane::Proof => self.step(-1, cx),
            "right" if self.pane() == Pane::Proof => self.step(1, cx),
            "space" if self.pane() == Pane::Proof => self.play(cx),
            "n" => self.go_next(cx),
            "p" => self.go_back(cx),
            "d" => self.set_pane(Pane::Diff, cx),
            "i" => self.set_pane(Pane::Details, cx),
            "r" => self.mark_reviewed(cx),
            "c" => self.open_changes(window, cx),
            "q" => self.set_compare(self.compare.next(), self.highlight, cx),
            "h" => self.set_compare(self.compare, !self.highlight, cx),
            "f" if self.pane() == Pane::Proof && !self.gallery.is_empty() => {
                self.expanded = !self.expanded;
                cx.notify();
            }
            "+" | "=" if self.pane() == Pane::Proof => self.set_zoom(self.zoom * ZOOM_STEP, cx),
            "-" if self.pane() == Pane::Proof => self.set_zoom(self.zoom / ZOOM_STEP, cx),
            "0" if self.pane() == Pane::Proof => self.set_zoom(1., cx),
            "m" => {
                if self.armed {
                    self.merge(cx);
                } else if self.awaiting() && self.merge_block().is_none() {
                    self.armed = true;
                    cx.notify();
                }
            }
            _ => return,
        }
        cx.stop_propagation();
    }
}

/// hint is a key beside a button's label.
fn hint(k: &str) -> AnyElement {
    div()
        .flex_none()
        .px_1()
        .rounded_sm()
        .border_1()
        .border_color(theme::line())
        .text_xs()
        .text_color(theme::faint())
        .child(k.to_string())
        .into_any_element()
}

/// tab is one of the panes under the header, underlined when it's on screen.
fn tab(id: &str, on: bool, children: Vec<AnyElement>) -> Stateful<Div> {
    h_flex()
        .id(SharedString::from(format!("review-tab-{id}")))
        .h_10()
        .gap_1p5()
        .text_sm()
        .font_weight(FontWeight::MEDIUM)
        .cursor_pointer()
        .border_b_1()
        .border_color(if on {
            theme::accent()
        } else {
            transparent_black()
        })
        .text_color(if on { theme::primary() } else { theme::faint() })
        .hover(|s| s.text_color(theme::primary()))
        .children(children)
}

impl ReviewView {
    fn render_header(&self, cx: &mut Context<Self>) -> AnyElement {
        let i = &self.item.item;
        let title = if i.title.is_empty() {
            i.agent.clone()
        } else {
            i.title.clone()
        };
        let summary = i
            .summary
            .lines()
            .find(|l| !l.trim().is_empty())
            .unwrap_or("")
            .to_string();
        let position = self.position().filter(|(_, n)| *n > 1);
        let can_back = !self.trail.is_empty();
        let next = self.next();
        v_flex()
            .px_5()
            .pt_4()
            .pb_3()
            .gap_1p5()
            .child(
                h_flex()
                    .gap_3()
                    .child(
                        div()
                            .min_w_0()
                            .truncate()
                            .text_xl()
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::primary())
                            .when(i.title.is_empty(), |el| {
                                el.font_family(crate::theme::mono())
                            })
                            .child(title),
                    )
                    .when(!i.verdict.is_empty(), |el| {
                        let when = i
                            .reviewed_at
                            .as_deref()
                            .map(super::settings::defaults::time_ago)
                            .unwrap_or_default();
                        el.child(controls::badge(
                            tv(
                                match i.verdict.as_str() {
                                    "changes" => "review.verdict.changes",
                                    "dismissed" => "review.verdict.dismissed",
                                    _ => "review.verdict.reviewed",
                                },
                                &[("when", when.into())],
                            ),
                            match i.verdict.as_str() {
                                "changes" => theme::starting(),
                                "dismissed" => theme::muted(),
                                _ => theme::running(),
                            },
                        ))
                    })
                    .child(div().flex_1())
                    .when_some(position, |el, (n, count)| {
                        el.child(controls::button(
                            "review-back",
                            "‹",
                            Btn::Ghost,
                            !can_back,
                            cx.listener(|this, _: &ClickEvent, _, cx| this.go_back(cx)),
                        ))
                        .child(
                            div()
                                .text_xs()
                                .font_family(crate::theme::mono())
                                .text_color(theme::faint())
                                .child(tv(
                                    "review.position",
                                    &[("n", n.into()), ("count", count.into())],
                                )),
                        )
                        .child(controls::button(
                            "review-next",
                            "›",
                            Btn::Ghost,
                            next.is_none(),
                            cx.listener(|this, _: &ClickEvent, _, cx| this.go_next(cx)),
                        ))
                    })
                    .map(|el| {
                        // A past agent's chat opens read-only (PastAgentView).
                        let r#ref = if self.item.past {
                            format!("past:{}", i.r#ref)
                        } else {
                            i.r#ref.clone()
                        };
                        el.child(controls::button(
                            "review-chat",
                            t("review.chat"),
                            Btn::Ghost,
                            false,
                            cx.listener(move |_, _: &ClickEvent, _, cx| {
                                cx.emit(ReviewEvent::Chat(r#ref.clone()))
                            }),
                        ))
                    })
                    .child(controls::button(
                        "review-close",
                        "✕",
                        Btn::Ghost,
                        false,
                        cx.listener(|_, _: &ClickEvent, _, cx| cx.emit(ReviewEvent::Close)),
                    )),
            )
            // Where its pull request stands, and why Merge waits if it does.
            .child(
                h_flex()
                    .gap_2()
                    .text_xs()
                    .text_color(theme::muted())
                    .when_some(self.pr(), |el, pr| el.child(pr_chip(pr)))
                    .when(!i.branch.is_empty(), |el| {
                        el.child(
                            div()
                                .min_w_0()
                                .truncate()
                                .font_family(crate::theme::mono())
                                .child(crate::icons::lead("git-branch", i.branch.to_string())),
                        )
                    })
                    .when_some(self.merge_block().filter(|_| self.awaiting()), |el, why| {
                        el.child(div().text_color(theme::faint()).child(why))
                    }),
            )
            .when(self.pane() != Pane::Details, |el| {
                el.child(
                    div()
                        .min_w_0()
                        .truncate()
                        .text_sm()
                        .text_color(if summary.is_empty() {
                            theme::faint()
                        } else {
                            theme::muted()
                        })
                        .child(if summary.is_empty() {
                            t("review.noSummary")
                        } else {
                            summary
                        }),
                )
            })
            .into_any_element()
    }

    fn render_tabs(&self, cx: &mut Context<Self>) -> AnyElement {
        let pane = self.pane();
        let i = &self.item.item;
        let count = self.gallery.len();
        h_flex()
            .px_5()
            .gap_5()
            .border_b_1()
            .border_color(theme::line())
            .child(
                tab(
                    "proof",
                    pane == Pane::Proof,
                    vec![
                        div().child(t("review.media")).into_any_element(),
                        div()
                            .text_color(theme::faint())
                            .when(count > 0, |el| el.child(count.to_string()))
                            .into_any_element(),
                    ],
                )
                .on_click(
                    cx.listener(|this, _: &ClickEvent, _, cx| this.set_pane(Pane::Proof, cx)),
                ),
            )
            .child(
                tab(
                    "diff",
                    pane == Pane::Diff,
                    vec![
                        div().child(t("review.diff")).into_any_element(),
                        div()
                            .font_family(crate::theme::mono())
                            .text_color(theme::faint())
                            .when_some(i.changes.as_ref().filter(|c| c.files > 0), |el, c| {
                                el.child(format!("+{} −{}", c.insertions, c.deletions))
                            })
                            .into_any_element(),
                        hint("D"),
                    ],
                )
                .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.set_pane(Pane::Diff, cx))),
            )
            .child(
                tab(
                    "details",
                    pane == Pane::Details,
                    vec![
                        div().child(t("review.details")).into_any_element(),
                        hint("I"),
                    ],
                )
                .on_click(
                    cx.listener(|this, _: &ClickEvent, _, cx| this.set_pane(Pane::Details, cx)),
                ),
            )
            .child(div().flex_1())
            .when_some(self.pr().map(|p| p.url.clone()), |el, url| {
                el.child(controls::button(
                    "review-github",
                    t("review.github"),
                    Btn::Ghost,
                    false,
                    move |_, _, cx| cx.open_url(&url),
                ))
            })
            .into_any_element()
    }

    /// render_report is the agent's final report: its outcome and the start
    /// of its summary, the whole of it and what it left open a click away.
    fn render_report(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let r = self.item.report.as_ref()?;
        let open = self.report_open;
        let remaining = r.remaining_issues.clone();
        Some(
            v_flex()
                .px_5()
                .py_2p5()
                .gap_1p5()
                .border_b_1()
                .border_color(theme::line())
                .bg(theme::surface_raised().opacity(0.3))
                .child(
                    h_flex()
                        .items_start()
                        .gap_2p5()
                        .child(
                            div()
                                .flex_none()
                                .text_xs()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::muted())
                                .child(t("review.report")),
                        )
                        .child(outcome_badge(&r.status))
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .text_sm()
                                .text_color(theme::muted())
                                .when(!open, |el| el.truncate())
                                .child(r.summary.clone()),
                        )
                        .child(
                            div()
                                .id("review-report-toggle")
                                .flex_none()
                                .px_1p5()
                                .rounded_md()
                                .text_xs()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::faint())
                                .cursor_pointer()
                                .hover(|s| {
                                    s.bg(theme::surface_strong()).text_color(theme::primary())
                                })
                                .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                    this.report_open = !this.report_open;
                                    cx.notify();
                                }))
                                .child(if open {
                                    t("review.reportLess")
                                } else if remaining.is_empty() {
                                    t("review.reportMore")
                                } else {
                                    tv(
                                        "review.reportMoreOpen",
                                        &[("count", remaining.len().into())],
                                    )
                                }),
                        ),
                )
                .when(open && !remaining.is_empty(), |el| {
                    el.child(
                        v_flex()
                            .pl(px(72.))
                            .gap_0p5()
                            .text_xs()
                            .child(
                                div()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::starting())
                                    .child(t("review.remaining")),
                            )
                            .children(remaining.into_iter().map(|r| {
                                div().text_color(theme::muted()).child(format!("•  {r}"))
                            })),
                    )
                })
                .into_any_element(),
        )
    }

    /// picture is a fetched screenshot, contained in its box.
    fn picture(&self, m: &MediaItem) -> AnyElement {
        match (self.files.get(&m.id), self.failed.get(&m.id)) {
            (Some(path), _) => img(path.clone())
                .size_full()
                .object_fit(ObjectFit::Contain)
                .into_any_element(),
            (None, Some(e)) => div()
                .p_4()
                .text_sm()
                .text_color(theme::error())
                .child(e.clone())
                .into_any_element(),
            _ => div()
                .text_sm()
                .text_color(theme::faint())
                .child(t("common.loading"))
                .into_any_element(),
        }
    }

    /// video is a recording's player, or what stands in for one.
    fn video(&self, m: &MediaItem) -> AnyElement {
        #[cfg(feature = "video")]
        if let Some(p) = self.players.get(&m.id) {
            return p.clone().into_any_element();
        }
        if let Some(e) = self.failed.get(&m.id) {
            return div()
                .p_4()
                .text_sm()
                .text_color(theme::error())
                .child(e.clone())
                .into_any_element();
        }
        let text = if cfg!(feature = "video") {
            t("common.loading")
        } else {
            t("native.video.unavailable")
        };
        div()
            .size_full()
            .flex()
            .items_center()
            .justify_center()
            .rounded_lg()
            .bg(theme::pitch())
            .text_sm()
            .text_color(theme::faint())
            .child(text)
            .into_any_element()
    }

    fn render_stage(&self, cx: &mut Context<Self>) -> AnyElement {
        let Some(shown) = self.gallery.get(self.at) else {
            return div().into_any_element();
        };
        let frame = |el: AnyElement| {
            div()
                .flex_1()
                .min_w_0()
                .min_h_0()
                .flex()
                .items_center()
                .justify_center()
                .rounded_lg()
                .bg(theme::ink())
                .overflow_hidden()
                .child(el)
        };
        let label = |s: String| {
            div()
                .flex_none()
                .text_xs()
                .font_weight(FontWeight::MEDIUM)
                .text_color(theme::muted())
                .child(s)
        };
        match shown {
            Shown::Screenshot(m) => frame(self.zoomed(&m.id, self.picture(m)))
                .id("review-shot")
                .size_full()
                .when(!self.expanded && self.zoom <= 1., |el| {
                    el.cursor_pointer()
                        .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.expanded = true;
                            cx.notify();
                        }))
                })
                .into_any_element(),
            Shown::Recording(take) => h_flex()
                .size_full()
                .gap_4()
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .child(self.video(&take.media)),
                )
                // What it checked: its chapters, a click going to each.
                .child(
                    v_flex()
                        .id("review-chapters")
                        .w(px(240.))
                        .flex_none()
                        .h_full()
                        .overflow_y_scroll()
                        .gap_1()
                        .child(label(t("review.checked")))
                        .when(take.chapters.is_empty(), |el| {
                            el.child(
                                div()
                                    .text_xs()
                                    .text_color(theme::faint())
                                    .child(t("review.noChapters")),
                            )
                        })
                        .children(take.chapters.iter().enumerate().map(|(n, c)| {
                            let at = c.at;
                            let id = take.media.id.clone();
                            v_flex()
                                .id(SharedString::from(format!("chapter-{n}")))
                                .px_2()
                                .py_1p5()
                                .rounded_md()
                                .cursor_pointer()
                                .hover(|s| s.bg(theme::surface_strong()))
                                .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    #[cfg(feature = "video")]
                                    if let Some(p) = this.players.get(&id) {
                                        p.update(cx, |p, cx| p.seek(at, cx));
                                    }
                                    let _ = (this, &id, at, &cx);
                                }))
                                .child(
                                    h_flex()
                                        .gap_2()
                                        .text_sm()
                                        .text_color(theme::primary())
                                        .child(
                                            div()
                                                .flex_none()
                                                .font_family(crate::theme::mono())
                                                .text_xs()
                                                .text_color(theme::faint())
                                                .child(clock(at)),
                                        )
                                        .child(div().min_w_0().child(c.title.clone())),
                                )
                                .when_some(c.detail.clone(), |el, d| {
                                    el.child(div().text_xs().text_color(theme::muted()).child(d))
                                })
                        })),
                )
                .into_any_element(),
            Shown::Pair(p) if p.before.kind == "recording" => h_flex()
                .size_full()
                .gap_3()
                .child(
                    v_flex()
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .gap_1()
                        .child(label(t("review.compare.before")))
                        .child(div().flex_1().min_h_0().child(self.video(&p.before))),
                )
                .child(
                    v_flex()
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .gap_1()
                        .child(label(t("review.compare.after")))
                        .child(div().flex_1().min_h_0().child(self.video(&p.after))),
                )
                .into_any_element(),
            Shown::Pair(p) => self.render_pair(p, cx),
            Shown::Page(page) => crate::viewers::page_stage(&self.item.item.project, page, true),
        }
    }

    /// fitted is a picture contained in a box of size, centred, at sizes in
    /// pixels: the slider's layers are absolute, where relative heights
    /// don't reach.
    fn fitted(&self, m: &MediaItem, size: Size<Pixels>, overlay: Option<&PathBuf>) -> AnyElement {
        let Some(path) = self.files.get(&m.id) else {
            return self.picture(m);
        };
        let (iw, ih) = image::image_dimensions(path).unwrap_or((16, 10));
        let k = (f32::from(size.width) / iw.max(1) as f32)
            .min(f32::from(size.height) / ih.max(1) as f32)
            * self.zoom;
        let (w, h) = (px(iw as f32 * k), px(ih as f32 * k));
        div()
            .size_full()
            .flex()
            .items_center()
            .justify_center()
            .child(
                div()
                    .relative()
                    .flex_none()
                    .w(w)
                    .h(h)
                    .child(img(path.clone()).w(w).h(h))
                    .when_some(overlay.cloned(), |el, o| {
                        el.child(img(o).absolute().top_0().left_0().w(w).h(h))
                    }),
            )
            .into_any_element()
    }

    /// zoomed is a picture at the stage's zoom: past fit it scrolls to pan.
    fn zoomed(&self, id: &str, el: AnyElement) -> AnyElement {
        // Its scroll is there at fit too, so the first zoom knows the
        // view's size and keeps its middle.
        div()
            .id(SharedString::from(format!("zoom-{id}")))
            .size_full()
            .overflow_scroll()
            .track_scroll(&self.pan)
            .when(self.zoom > 1.0001, |el| el.cursor_grab())
            .map(|el| crate::viewers::drag_pans(el, &self.pan))
            .child(
                div()
                    .flex_none()
                    .w(relative(self.zoom))
                    .h(relative(self.zoom))
                    .child(el),
            )
            .into_any_element()
    }

    /// shot is a pair's screenshot zoomed, with the highlight over it when
    /// it's the after.
    fn shot(&self, m: &MediaItem, overlay: Option<&PathBuf>) -> AnyElement {
        let el = div()
            .relative()
            .size_full()
            .child(self.picture(m))
            .when_some(overlay.cloned(), |el, o| {
                el.child(
                    img(o)
                        .absolute()
                        .top_0()
                        .left_0()
                        .size_full()
                        .object_fit(ObjectFit::Contain),
                )
            })
            .into_any_element();
        self.zoomed(&m.id, el)
    }

    /// render_pair is two screenshots compared (ReviewCompare): side by
    /// side, one over the other with a divider to drag, or flipped; the
    /// pixels that changed highlighted over the after.
    fn render_pair(&self, p: &media::Pair, cx: &mut Context<Self>) -> AnyElement {
        let frame = |el: AnyElement| {
            div()
                .flex_1()
                .min_w_0()
                .min_h_0()
                .flex()
                .items_center()
                .justify_center()
                .rounded_lg()
                .bg(theme::ink())
                .overflow_hidden()
                .child(el)
        };
        let label = |s: String| {
            div()
                .flex_none()
                .text_xs()
                .font_weight(FontWeight::MEDIUM)
                .text_color(theme::muted())
                .child(s)
        };
        let me = cx.entity().downgrade();
        let modes = controls::segmented(
            "review-compare",
            vec![
                ("side".into(), t("review.compare.side")),
                ("slider".into(), t("review.compare.slider")),
                ("flip".into(), t("review.compare.flip")),
            ],
            Some(self.compare.name()),
            false,
            std::rc::Rc::new(move |v: &str, _, cx| {
                let c = Compare::from(v);
                me.update(cx, |this, cx| this.set_compare(c, this.highlight, cx))
                    .ok();
            }),
        );
        let key = format!("{}|{}", p.before.id, p.after.id);
        let diff = self.differences.get(&key).filter(|_| self.highlight);
        let overlay = match diff {
            Some(Difference::Ready {
                overlay, changed, ..
            }) if *changed > 0 => Some(overlay),
            _ => None,
        };
        let note = diff.map(|d| match d {
            Difference::Working => t("review.compare.working"),
            Difference::Sizes(a, b) => tv(
                "review.compare.sizes",
                &[("before", a.into()), ("after", b.into())],
            ),
            Difference::Failed(e) => tv("review.compare.failed", &[("error", e.into())]),
            Difference::Ready { changed: 0, .. } => t("review.compare.unchanged"),
            Difference::Ready { changed, total, .. } => {
                let pc = *changed as f64 / (*total).max(1) as f64 * 100.;
                tv(
                    "review.compare.changed",
                    &[(
                        "percent",
                        if pc < 0.1 {
                            "<0.1".to_string()
                        } else {
                            format!("{pc:.1}")
                        }
                        .into(),
                    )],
                )
            }
        });
        let body = match self.compare {
            Compare::Side => h_flex()
                .flex_1()
                .min_h_0()
                .gap_3()
                .child(
                    v_flex()
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .gap_1()
                        .child(label(t("review.compare.before")))
                        .child(frame(self.shot(&p.before, None))),
                )
                .child(
                    v_flex()
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .gap_1()
                        .child(label(t("review.compare.after")))
                        .child(frame(self.shot(&p.after, overlay))),
                )
                .into_any_element(),
            Compare::Slider => {
                let frac = self.slider.clamp(0.001, 0.999);
                // The pictures at the box's size, as measured (relative
                // heights don't reach through the absolute layers).
                let size = self.slider_box.get().size;
                let bounds = self.slider_box.clone();
                let read = self.slider_box.clone();
                let at = move |pos: Point<Pixels>| {
                    let b = read.get();
                    if b.size.width <= px(0.) {
                        return None;
                    }
                    Some(((pos.x - b.origin.x) / b.size.width).clamp(0., 1.))
                };
                let at2 = at.clone();
                div()
                    .id("review-slider")
                    .flex_1()
                    .min_h_0()
                    .relative()
                    .rounded_lg()
                    .overflow_hidden()
                    .bg(theme::ink())
                    .cursor_col_resize()
                    .child({
                        let me = cx.entity().downgrade();
                        canvas(
                            move |b, window, _| {
                                // Measured: the pictures take its size next frame.
                                if bounds.get().size != b.size {
                                    let me = me.clone();
                                    window.on_next_frame(move |_, cx| {
                                        me.update(cx, |_, cx| cx.notify()).ok();
                                    });
                                }
                                bounds.set(b)
                            },
                            |_, _, _, _| {},
                        )
                        .absolute()
                        .size_full()
                    })
                    .child(
                        div()
                            .absolute()
                            .top_0()
                            .left_0()
                            .w(size.width)
                            .h(size.height)
                            .child(self.fitted(&p.after, size, overlay)),
                    )
                    .child(
                        div()
                            .absolute()
                            .top_0()
                            .left_0()
                            .h(size.height)
                            .w(size.width * frac)
                            .overflow_hidden()
                            .child(
                                div()
                                    .w(size.width)
                                    .h(size.height)
                                    .child(self.fitted(&p.before, size, None)),
                            ),
                    )
                    .child(
                        div()
                            .absolute()
                            .top_0()
                            .h_full()
                            .left(relative(frac))
                            .w(px(2.))
                            .bg(theme::on_media())
                            .shadow_md(),
                    )
                    .child(
                        div()
                            .absolute()
                            .top_2()
                            .left_2()
                            .px_1p5()
                            .rounded_sm()
                            .bg(theme::media_shade(0.6))
                            .text_xs()
                            .text_color(theme::on_media())
                            .child(t("review.compare.before")),
                    )
                    .child(
                        div()
                            .absolute()
                            .top_2()
                            .right_2()
                            .px_1p5()
                            .rounded_sm()
                            .bg(theme::media_shade(0.6))
                            .text_xs()
                            .text_color(theme::on_media())
                            .child(t("review.compare.after")),
                    )
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |this, ev: &MouseDownEvent, _, cx| {
                            if let Some(f) = at(ev.position) {
                                this.slider = f;
                                cx.notify();
                            }
                        }),
                    )
                    .on_mouse_move(cx.listener(move |this, ev: &MouseMoveEvent, _, cx| {
                        if ev.pressed_button == Some(MouseButton::Left)
                            && let Some(f) = at2(ev.position)
                        {
                            this.slider = f;
                            cx.notify();
                        }
                    }))
                    .into_any_element()
            }
            Compare::Flip => {
                let (side, m, o) = if self.flipped {
                    (t("review.compare.before"), &p.before, None)
                } else {
                    (t("review.compare.after"), &p.after, overlay)
                };
                v_flex()
                    .id("review-flip")
                    .flex_1()
                    .min_h_0()
                    .gap_1()
                    .cursor_pointer()
                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                        this.flipped = !this.flipped;
                        cx.notify();
                    }))
                    .child(label(side))
                    .child(frame(self.shot(m, o)))
                    .into_any_element()
            }
        };
        let hint_key = match self.compare {
            Compare::Side => "review.compare.sideHint",
            Compare::Slider => "review.compare.sliderHint",
            Compare::Flip => "review.compare.flipHint",
        };
        let on = self.highlight;
        v_flex()
            .size_full()
            .gap_2()
            .child(
                h_flex()
                    .flex_wrap()
                    .gap_2()
                    .child(label(t("review.compare.modes")))
                    .child(modes)
                    .child(hint("Q"))
                    .child(
                        div()
                            .id("review-highlight")
                            .px_2()
                            .py_0p5()
                            .rounded_md()
                            .text_xs()
                            .font_weight(FontWeight::MEDIUM)
                            .cursor_pointer()
                            .when(on, |el| {
                                el.bg(theme::tint(theme::dracula_hue(theme::dracula::PINK), 0.2))
                                    .text_color(theme::primary())
                            })
                            .when(!on, |el| {
                                el.text_color(theme::muted())
                                    .hover(|s| s.bg(theme::surface_strong()))
                            })
                            .tooltip(|w, cx| {
                                gpui_component::tooltip::Tooltip::new(t(
                                    "review.compare.highlightTitle",
                                ))
                                .build(w, cx)
                            })
                            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.set_compare(this.compare, !this.highlight, cx)
                            }))
                            .child(t("review.compare.highlight")),
                    )
                    .child(hint("H"))
                    .when_some(note, |el, n| {
                        el.child(
                            div()
                                .min_w_0()
                                .truncate()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(n),
                        )
                    })
                    .child(div().flex_1())
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .truncate()
                            .child(tv(hint_key, &[("mod", "Ctrl".into())])),
                    ),
            )
            .child(body)
            .into_any_element()
    }

    /// render_strip is every piece of proof in cards, the one on stage lit,
    /// with where it is and its name above.
    fn render_strip(&self, cx: &mut Context<Self>) -> AnyElement {
        let n = self.gallery.len();
        let name = self
            .gallery
            .get(self.at)
            .map(Shown::name)
            .unwrap_or_default();
        v_flex()
            .flex_none()
            .gap_2()
            .child(
                h_flex()
                    .gap_2()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(controls::button(
                        "proof-prev",
                        "←",
                        Btn::Ghost,
                        n < 2,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.step(-1, cx)),
                    ))
                    .child(div().font_family(crate::theme::mono()).child(tv(
                        "review.position",
                        &[("n", (self.at + 1).into()), ("count", n.into())],
                    )))
                    .child(controls::button(
                        "proof-next",
                        "→",
                        Btn::Ghost,
                        n < 2,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.step(1, cx)),
                    ))
                    .child(
                        div()
                            .min_w_0()
                            .truncate()
                            .text_color(theme::primary())
                            .child(name),
                    )
                    .child(div().flex_1())
                    .children(self.zoom_controls(cx)),
            )
            .child(
                h_flex()
                    .id("proof-strip")
                    .gap_2()
                    .overflow_x_scroll()
                    .children(self.gallery.iter().enumerate().map(|(ix, s)| {
                        let on = ix == self.at;
                        let kind = match s {
                            Shown::Pair(_) => "arrow-left-right",
                            Shown::Recording(_) => "film",
                            Shown::Screenshot(_) => "image",
                            Shown::Page(_) => "globe",
                        };
                        let thumb = s.thumb().and_then(|m| self.files.get(&m.id)).cloned();
                        v_flex()
                            .id(SharedString::from(format!("proof-{ix}")))
                            .flex_none()
                            .w(px(132.))
                            .gap_1()
                            .p_1()
                            .rounded_md()
                            .cursor_pointer()
                            .border_b_2()
                            .border_color(if on {
                                theme::accent()
                            } else {
                                transparent_black()
                            })
                            .hover(|el| el.bg(theme::surface_strong()))
                            .on_click(
                                cx.listener(move |this, _: &ClickEvent, _, cx| this.pick(ix, cx)),
                            )
                            .child(
                                div()
                                    .relative()
                                    .w_full()
                                    .h(px(70.))
                                    .rounded_sm()
                                    .overflow_hidden()
                                    .bg(theme::pitch())
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .text_color(theme::faint())
                                    .map(|el| match thumb {
                                        Some(p) => el
                                            .child(img(p).size_full().object_fit(ObjectFit::Cover)),
                                        None => el.child(crate::icons::of(kind)),
                                    })
                                    .child(
                                        div()
                                            .absolute()
                                            .bottom_1()
                                            .left_1()
                                            .px_1()
                                            .rounded_sm()
                                            .bg(theme::media_shade(0.6))
                                            .text_xs()
                                            .text_color(theme::on_media())
                                            .child(crate::icons::of(kind)),
                                    ),
                            )
                            .child(
                                div()
                                    .truncate()
                                    .text_xs()
                                    .text_color(if on { theme::primary() } else { theme::faint() })
                                    .child(s.name()),
                            )
                    })),
            )
            .into_any_element()
    }

    /// zoom_controls are − the zoom · Fit + and Expand (StageBar,
    /// ZoomControls), for what can be zoomed.
    fn zoom_controls(&self, cx: &mut Context<Self>) -> Vec<AnyElement> {
        let zoomable = !matches!(self.gallery.get(self.at), None | Some(Shown::Page(_)));
        let mut out = Vec::new();
        if zoomable {
            out.push(controls::button(
                "zoom-out",
                "−",
                Btn::Ghost,
                self.zoom <= 1.,
                cx.listener(|this, _: &ClickEvent, _, cx| this.set_zoom(this.zoom / ZOOM_STEP, cx)),
            ));
            out.push(
                div()
                    .id("zoom-fit")
                    .px_1p5()
                    .rounded_md()
                    .font_family(crate::theme::mono())
                    .text_xs()
                    .text_color(theme::muted())
                    .cursor_pointer()
                    .hover(|s| s.text_color(theme::primary()))
                    .tooltip(|w, cx| {
                        gpui_component::tooltip::Tooltip::new(t("review.zoom.fitTitle"))
                            .build(w, cx)
                    })
                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.set_zoom(1., cx)))
                    .child(format!(
                        "{}% · {}",
                        (self.zoom * 100.).round(),
                        t("review.zoom.fit")
                    ))
                    .into_any_element(),
            );
            out.push(controls::button(
                "zoom-in",
                "+",
                Btn::Ghost,
                self.zoom >= ZOOM_MAX,
                cx.listener(|this, _: &ClickEvent, _, cx| this.set_zoom(this.zoom * ZOOM_STEP, cx)),
            ));
        }
        if let Some(Shown::Page(p)) = self.gallery.get(self.at)
            && !p.url.is_empty()
        {
            let url = p.url.clone();
            out.push(controls::button(
                "page-open",
                t("review.openPage"),
                Btn::Ghost,
                false,
                move |_, _, cx| cx.open_url(&url),
            ));
        }
        out.push(if self.expanded {
            controls::button(
                "review-collapse",
                format!("✕ {}", t("review.collapse")),
                Btn::Ghost,
                false,
                cx.listener(|this, _: &ClickEvent, _, cx| {
                    this.expanded = false;
                    cx.notify();
                }),
            )
        } else {
            controls::button(
                "review-expand",
                t("review.expand"),
                Btn::Ghost,
                self.gallery.is_empty(),
                cx.listener(|this, _: &ClickEvent, _, cx| {
                    this.expanded = true;
                    cx.notify();
                }),
            )
        });
        out
    }

    /// render_expanded is the piece on stage large over the whole review,
    /// Esc or Back closing it.
    fn render_expanded(&self, cx: &mut Context<Self>) -> AnyElement {
        let name = self
            .gallery
            .get(self.at)
            .map(Shown::name)
            .unwrap_or_default();
        v_flex()
            .id("review-expanded")
            .absolute()
            .inset_0()
            .bg(theme::stage())
            .on_scroll(cx.listener(Self::on_wheel))
            .child(
                h_flex()
                    .px_4()
                    .py_3()
                    .gap_2()
                    .text_sm()
                    .text_color(theme::on_media())
                    .child(div().font_family(crate::theme::mono()).text_xs().child(tv(
                        "review.position",
                        &[
                            ("n", (self.at + 1).into()),
                            ("count", self.gallery.len().into()),
                        ],
                    )))
                    .child(div().min_w_0().truncate().child(name))
                    .child(div().flex_1())
                    .children(self.zoom_controls(cx)),
            )
            .child(div().flex_1().min_h_0().px_4().child(self.render_stage(cx)))
            .child(
                div()
                    .px_4()
                    .py_2()
                    .text_center()
                    .text_xs()
                    .text_color(theme::on_media().opacity(0.4))
                    .child(tv("review.zoom.hint", &[("mod", "Ctrl".into())])),
            )
            .into_any_element()
    }

    /// on_wheel zooms with Ctrl (or ⌘) held, as the Electron app does.
    fn on_wheel(&mut self, ev: &ScrollEvent, _: &mut Window, cx: &mut Context<Self>) {
        if !(ev.modifiers.control || ev.modifiers.platform) {
            return;
        }
        let dy = match ev.delta {
            ScrollDelta::Pixels(p) => f32::from(p.y),
            ScrollDelta::Lines(l) => l.y * 20.,
        };
        if dy > 0. {
            self.set_zoom(self.zoom * ZOOM_STEP, cx);
        } else if dy < 0. {
            self.set_zoom(self.zoom / ZOOM_STEP, cx);
        }
        cx.stop_propagation();
    }

    fn render_proof(&self, cx: &mut Context<Self>) -> AnyElement {
        match &self.media {
            None => div()
                .p_6()
                .text_sm()
                .text_color(theme::faint())
                .child(t("common.loading"))
                .into_any_element(),
            Some(Err(e)) => div()
                .p_6()
                .text_sm()
                .text_color(theme::error())
                .child(e.clone())
                .into_any_element(),
            Some(Ok(_)) if self.gallery.is_empty() => v_flex()
                .p_6()
                .gap_1()
                .child(
                    div()
                        .text_color(theme::primary())
                        .child(t("review.noProof")),
                )
                .child(
                    div()
                        .text_sm()
                        .text_color(theme::muted())
                        .child(t("review.noProofBody")),
                )
                .into_any_element(),
            Some(Ok(_)) => v_flex()
                .size_full()
                .p_4()
                .gap_3()
                .child(
                    v_flex()
                        .id("review-stage")
                        .flex_1()
                        .min_h_0()
                        .overflow_hidden()
                        .on_scroll(cx.listener(Self::on_wheel))
                        .when(!self.expanded, |el| el.child(self.render_stage(cx))),
                )
                .child(self.render_strip(cx))
                .into_any_element(),
        }
    }

    fn render_diff(&self) -> AnyElement {
        match &self.diff {
            None => div()
                .p_6()
                .text_sm()
                .text_color(theme::faint())
                .child(t("common.loading"))
                .into_any_element(),
            Some(Err(e)) => div()
                .p_5()
                .text_sm()
                .text_color(theme::error())
                .child(tv("review.diffUnavailable", &[("error", e.into())]))
                .into_any_element(),
            Some(Ok(view)) => div().size_full().child(view.clone()).into_any_element(),
        }
    }

    fn render_details(&self) -> AnyElement {
        let i = &self.item.item;
        div()
            .id("review-details")
            .size_full()
            .overflow_y_scroll()
            .child(
                v_flex()
                    .w_full()
                    .max_w(px(900.))
                    .mx_auto()
                    .p_5()
                    .gap_6()
                    .child(
                        div()
                            .text_sm()
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::muted())
                            .child(i.project.clone()),
                    )
                    .when(!self.item.task.is_empty(), |el| {
                        el.child(
                            div()
                                .text_sm()
                                .text_color(theme::muted())
                                .child(format!("“{}”", self.item.task)),
                        )
                    })
                    .child(
                        v_flex()
                            .gap_2()
                            .child(
                                div()
                                    .text_xs()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::muted())
                                    .child(t("review.summary")),
                            )
                            .child(if i.summary.is_empty() {
                                div()
                                    .text_sm()
                                    .text_color(theme::faint())
                                    .child(t("review.noSummary"))
                                    .into_any_element()
                            } else {
                                div()
                                    .text_sm()
                                    .child(TextView::markdown(
                                        SharedString::from(format!("review-summary-{}", i.r#ref)),
                                        i.summary.clone(),
                                    ))
                                    .into_any_element()
                            }),
                    )
                    .when_some(self.pr(), |el, pr| {
                        el.child(
                            h_flex()
                                .gap_2()
                                .p_3()
                                .rounded_xl()
                                .border_1()
                                .border_color(theme::line())
                                .text_sm()
                                .child(div().flex_1().min_w_0().truncate().child(pr.title.clone()))
                                .child(pr_chip(pr)),
                        )
                    }),
            )
            .into_any_element()
    }

    fn render_changes(&self, cx: &mut Context<Self>) -> AnyElement {
        v_flex()
            .mx_4()
            .mb_3()
            .p_3()
            .gap_2()
            .rounded_xl()
            .border_1()
            .border_color(theme::line())
            .bg(theme::ink())
            .child(div().text_xs().text_color(theme::muted()).child(tv(
                "review.changesTo",
                &[("agent", (&self.item.item.agent).into())],
            )))
            .children(self.notes_list.clone())
            .child(Textarea::new(&self.changes))
            .child(
                h_flex()
                    .justify_end()
                    .gap_2()
                    .child(controls::button(
                        "changes-cancel",
                        t("common.cancel"),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.close_changes(window, cx)
                        }),
                    ))
                    .child(controls::button(
                        "changes-send",
                        t("review.changesSend"),
                        Btn::Primary,
                        self.busy,
                        cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.send_changes(window, cx)
                        }),
                    )),
            )
            .into_any_element()
    }

    /// render_green_note is the strip over the footer while the pull request
    /// waits to merge when green, and on what, or why it didn't
    /// (MergeWhenGreenNote).
    fn render_green_note(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let mark = self.item.item.merge_when_green.clone()?;
        let failed = !mark.error.is_empty();
        let text = if failed {
            tv(
                "review.mergeWhenGreen.notMerged",
                &[("error", (&mark.error).into())],
            )
        } else if let Some(wait) = wait_text(&mark) {
            tv(
                "review.mergeWhenGreen.waiting",
                &[("number", mark.number.into()), ("wait", (&wait).into())],
            )
        } else {
            tv(
                "review.mergeWhenGreen.pending",
                &[("number", mark.number.into())],
            )
        };
        let color = if failed {
            theme::error()
        } else {
            theme::starting()
        };
        let (client, project, number) = (
            self.client.clone(),
            self.item.item.project.clone(),
            mark.number,
        );
        Some(
            h_flex()
                .gap_2()
                .px_4()
                .py_2()
                .border_t_1()
                .border_color(theme::line())
                .text_xs()
                .text_color(color)
                .child(crate::icons::of(if failed { "⚠" } else { "◷" }))
                .child(div().flex_1().min_w_0().truncate().child(text))
                .child(controls::button(
                    "review-green-cancel",
                    t(if failed {
                        "review.mergeWhenGreen.dismiss"
                    } else {
                        "review.mergeWhenGreen.cancel"
                    }),
                    Btn::Ghost,
                    false,
                    cx.listener(move |_, _: &ClickEvent, _, cx| {
                        cancel_green(client.clone(), project.clone(), number, failed, cx);
                        cx.emit(ReviewEvent::Changed);
                    }),
                ))
                .into_any_element(),
        )
    }

    fn render_footer(&self, cx: &mut Context<Self>) -> AnyElement {
        let i = &self.item.item;
        let block = self.merge_block();
        let note = if let Some(e) = &self.error {
            Some((e.to_string(), theme::error()))
        } else if self.armed {
            self.pr().map(|pr| {
                (
                    tv("review.mergeArmed", &[("number", pr.number.into())]),
                    theme::primary(),
                )
            })
        } else {
            block.clone().map(|b| (b, theme::muted()))
        };
        h_flex()
            .flex_wrap()
            .gap_2()
            .px_4()
            .py_2p5()
            .border_t_1()
            .border_color(theme::line())
            .map(|el| {
                if !self.awaiting() {
                    let key = if self.item.past {
                        "review.lookOnly.past"
                    } else if !i.verdict.is_empty() {
                        "review.lookOnly.reviewed"
                    } else if !i.fingerprint.is_empty() {
                        "review.lookOnly.done"
                    } else {
                        "review.lookOnly.working"
                    };
                    return el
                        .child(div().flex_1())
                        .child(div().text_xs().text_color(theme::faint()).child(t(key)));
                }
                el.child(
                    h_flex()
                        .id("review-changes")
                        .gap_2()
                        .px_3()
                        .py(px(5.))
                        .rounded_md()
                        .bg(theme::surface_raised())
                        .border_1()
                        .border_color(theme::line())
                        .text_sm()
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::surface_strong()))
                        .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.open_changes(window, cx)
                        }))
                        .child(t("review.requestChanges"))
                        .child(hint("C")),
                )
                .child(
                    h_flex()
                        .id("review-done")
                        .gap_2()
                        .px_3()
                        .py(px(5.))
                        .rounded_md()
                        .bg(theme::surface_raised())
                        .border_1()
                        .border_color(theme::line())
                        .text_sm()
                        .when(self.busy, |el| el.opacity(0.5))
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::surface_strong()))
                        .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.mark_reviewed(cx)))
                        .child(format!("✓ {}", t("review.markReviewed")))
                        .child(hint("R")),
                )
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .text_right()
                        .text_xs()
                        .when_some(note, |el, (s, c)| el.text_color(c).child(s)),
                )
                .when(
                    self.green && self.pr().is_some_and(|p| p.state == "open"),
                    |el| {
                        el.child(controls::button(
                            "review-merge-when-green",
                            format!("◷ {}", t("review.mergeWhenGreen.short")),
                            Btn::Secondary,
                            block.is_some() || self.busy,
                            cx.listener(|this, _: &ClickEvent, _, cx| this.merge_when_green(cx)),
                        ))
                    },
                )
                .child(merge::split(
                    "review-merge",
                    match self.pr() {
                        Some(pr) => tv("review.mergeNumber", &[("number", pr.number.into())]),
                        None => t("review.noPullRequest"),
                    },
                    block.is_none().then(|| hint("M")),
                    &self.method(),
                    &self.methods(),
                    block.is_some() || self.busy,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.merge(cx)),
                    std::rc::Rc::new(cx.listener(|this, m: &str, _, cx| {
                        merge::remember(&this.item.item.project, m);
                        this.method = Some(m.to_string());
                        cx.notify();
                    })),
                    cx,
                ))
            })
            .into_any_element()
    }
}

impl Render for ReviewView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.ensure_difference(cx);
        let pane = self.pane();
        let body = match pane {
            Pane::Proof => self.render_proof(cx),
            Pane::Diff => self.render_diff(),
            Pane::Details => self.render_details(),
        };
        div()
            .id("review")
            .track_focus(&self.focus)
            .absolute()
            .inset_0()
            .bg(theme::media_shade(0.6))
            .flex()
            .items_center()
            .justify_center()
            .on_key_down(cx.listener(Self::on_key))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|_, _, _, cx| cx.emit(ReviewEvent::Close)),
            )
            .child(
                v_flex()
                    .id("review-card")
                    .w(relative(0.94))
                    .max_w(px(1400.))
                    .h(relative(0.94))
                    .rounded_xl()
                    .bg(theme::card())
                    .border_1()
                    .border_color(theme::line())
                    .overflow_hidden()
                    .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                    .child(self.render_header(cx))
                    .child(self.render_tabs(cx))
                    .children(self.render_report(cx))
                    .child(div().flex_1().min_h_0().child(body))
                    .when(self.changing, |el| el.child(self.render_changes(cx)))
                    .children(self.render_green_note(cx))
                    .child(self.render_footer(cx))
                    .when(self.expanded && pane == Pane::Proof, |el| {
                        el.relative().child(self.render_expanded(cx))
                    }),
            )
    }
}

/// clock writes seconds as m:ss.
fn clock(secs: f64) -> String {
    let s = secs.max(0.).round() as u64;
    format!("{}:{:02}", s / 60, s % 60)
}

/// cancel_green takes a merge-when-green mark back, or dismisses a given-up
/// one (MergeWhenGreen.tsx cancelMergeWhenGreen); quiet says nothing when
/// it went.
fn cancel_green(client: Client, project: String, number: i64, quiet: bool, cx: &mut App) {
    cx.spawn(async move |cx| {
        let res = client
            .raw(
                agentbox_api::client::Method::DELETE,
                &format!("/v1/projects/{project}/pulls/{number}/merge-when-green"),
                None,
            )
            .await;
        cx.update(|cx| match res {
            Ok(_) if !quiet => toasts::info(
                cx,
                tv(
                    "review.mergeWhenGreen.cancelled",
                    &[("number", number.into())],
                ),
            ),
            Ok(_) => {}
            Err(e) => toasts::error(cx, e.to_string()),
        });
    })
    .detach();
}

/// wait_text is what a pending mark waits on, in words (waitText).
fn wait_text(mark: &MergeWhenGreen) -> Option<String> {
    let word = match mark.waiting.as_str() {
        "checks" | "failing" | "conflict" | "draft" | "retry" => {
            t(&format!("mergeWhenGreen.wait.{}", mark.waiting))
        }
        _ => return None,
    };
    Some(if !mark.detail.is_empty() && mark.waiting != "conflict" {
        format!("{word} ({})", mark.detail)
    } else {
        word
    })
}
