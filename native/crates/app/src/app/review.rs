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
    Client, MediaItem, MergePullRequestRequest, PullRequest, RequestChangesRequest, ReviewRequest,
    WorkItem,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{InputEvent, Textarea, TextareaState};
use gpui_component::text::TextView;
use gpui_component::{h_flex, v_flex};

use super::controls::{self, Btn};
use super::work::{outcome_badge, pr_chip};
use crate::media::{self, Shown};
use crate::theme;
#[cfg(feature = "video")]
use crate::video::VideoPlayer;

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
    Flip,
}

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
    pane: Option<Pane>,
    diff: Option<Result<String, String>>,
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
            compare: Compare::Side,
            flipped: false,
            pane: None,
            diff: None,
            report_open: false,
            changing: false,
            changes,
            busy: false,
            error: None,
            armed: false,
            _sub: sub,
        };
        this.show(item, cx);
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
        self.report_open = false;
        self.changing = false;
        self.error = None;
        self.armed = false;
        self.flipped = false;
        let i = &self.item.item;
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
                        this.gallery = media::gallery(&m);
                        this.media = Some(Ok(m));
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

    /// fetch_files reads every picture and recording of the proof into the cache.
    fn fetch_files(&mut self, cx: &mut Context<Self>) {
        let mut wanted: Vec<MediaItem> = Vec::new();
        for s in &self.gallery {
            match s {
                Shown::Pair(p) => wanted.extend([p.before.clone(), p.after.clone()]),
                Shown::Recording(t) => wanted.push(t.media.clone()),
                Shown::Screenshot(m) => wanted.push(m.clone()),
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
                this.diff = Some(
                    res.map(|b| String::from_utf8_lossy(&b).into_owned())
                        .map_err(|e| e.to_string()),
                );
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
            None => cx.emit(ReviewEvent::Close),
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
        if self.busy || text.is_empty() {
            return;
        }
        let i = &self.item.item;
        let path = format!("/v1/agents/{}/{}/request-changes", i.project, i.agent);
        let req = RequestChangesRequest {
            text,
            fingerprint: i.fingerprint.clone(),
            notes: Vec::new(),
        };
        self.changes.update(cx, |s, cx| s.set_value("", window, cx));
        self.focus.focus(window, cx);
        self.send(path, serde_json::to_vec(&req).unwrap_or_default(), cx);
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
                    Err(e) => {
                        this.error =
                            Some(tv("review.failed", &[("error", (&e.to_string()).into())]).into())
                    }
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
        Some(match pr.state.as_str() {
            "merged" => t("review.mergeBlocked.merged"),
            "closed" => t("review.mergeBlocked.closed"),
            _ if pr.draft => t("review.mergeBlocked.draft"),
            _ if pr.checks == "failure" => t("review.mergeBlocked.failing"),
            _ if pr.conflict => tv(
                "review.mergeBlocked.conflict",
                &[("base", (&pr.base_branch).into())],
            ),
            _ => return None,
        })
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
        self.busy = true;
        self.armed = false;
        self.error = None;
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, PullRequest>(
                    &format!("/v1/projects/{}/pulls/{}/merge", i.project, pr.number),
                    &MergePullRequestRequest {
                        method: "squash".into(),
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
                    Ok(_) => this.advance(cx),
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
            if self.changing {
                self.close_changes(window, cx);
            } else if self.pane() != Pane::Proof && self.pane.is_some() {
                self.set_pane(Pane::Proof, cx);
            } else {
                cx.emit(ReviewEvent::Close);
            }
            cx.stop_propagation();
            return;
        }
        if typing {
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
            "q" => {
                self.compare = match self.compare {
                    Compare::Side => Compare::Flip,
                    Compare::Flip => Compare::Side,
                };
                cx.notify();
            }
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
        .border_color(theme::border())
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
        .text_color(if on { theme::fg() } else { theme::faint() })
        .hover(|s| s.text_color(theme::fg()))
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
                            .text_color(theme::fg())
                            .when(i.title.is_empty(), |el| el.font_family("monospace"))
                            .child(title),
                    )
                    .when(!i.verdict.is_empty(), |el| {
                        el.child(controls::badge(
                            t(match i.verdict.as_str() {
                                "changes" => "work.verdict.changes",
                                "dismissed" => "work.verdict.dismissed",
                                _ => "work.verdict.reviewed",
                            }),
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
                                .font_family("monospace")
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
                    .when(!self.item.past, |el| {
                        let r#ref = i.r#ref.clone();
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
                                .font_family("monospace")
                                .child(format!("⎇ {}", i.branch)),
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
            .border_color(theme::border())
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
                            .font_family("monospace")
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
                .border_color(theme::border())
                .bg(theme::raised().opacity(0.3))
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
                                .hover(|s| s.bg(theme::hover()).text_color(theme::fg()))
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
            .bg(gpui::black())
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
                .bg(theme::bg())
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
            Shown::Screenshot(m) => frame(self.picture(m)).size_full().into_any_element(),
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
                                .hover(|s| s.bg(theme::hover()))
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
                                        .text_color(theme::fg())
                                        .child(
                                            div()
                                                .flex_none()
                                                .font_family("monospace")
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
            Shown::Pair(p) => {
                let modes = controls::segmented(
                    "review-compare",
                    vec![
                        ("side".into(), t("review.compare.side")),
                        ("flip".into(), t("review.compare.flip")),
                    ],
                    Some(match self.compare {
                        Compare::Side => "side",
                        Compare::Flip => "flip",
                    }),
                    false,
                    {
                        let me = cx.entity().downgrade();
                        std::rc::Rc::new(move |v: &str, _, cx| {
                            let flip = v == "flip";
                            me.update(cx, |this, cx| {
                                this.compare = if flip { Compare::Flip } else { Compare::Side };
                                cx.notify();
                            })
                            .ok();
                        })
                    },
                );
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
                                .child(frame(self.picture(&p.before))),
                        )
                        .child(
                            v_flex()
                                .flex_1()
                                .min_w_0()
                                .h_full()
                                .gap_1()
                                .child(label(t("review.compare.after")))
                                .child(frame(self.picture(&p.after))),
                        )
                        .into_any_element(),
                    Compare::Flip => {
                        let (side, m) = if self.flipped {
                            (t("review.compare.before"), &p.before)
                        } else {
                            (t("review.compare.after"), &p.after)
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
                            .child(frame(self.picture(m)))
                            .into_any_element()
                    }
                };
                v_flex()
                    .size_full()
                    .gap_2()
                    .child(
                        h_flex()
                            .gap_2()
                            .child(label(t("review.compare.modes")))
                            .child(modes)
                            .child(hint("Q")),
                    )
                    .child(body)
                    .into_any_element()
            }
        }
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
                    .child(div().font_family("monospace").child(tv(
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
                            .text_color(theme::fg())
                            .child(name),
                    ),
            )
            .child(
                h_flex()
                    .id("proof-strip")
                    .gap_2()
                    .overflow_x_scroll()
                    .children(self.gallery.iter().enumerate().map(|(ix, s)| {
                        let on = ix == self.at;
                        let kind = match s {
                            Shown::Pair(_) => "⇆",
                            Shown::Recording(_) => "▶",
                            Shown::Screenshot(_) => "▣",
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
                            .hover(|el| el.bg(theme::hover()))
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
                                    .bg(gpui::black())
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .text_color(theme::faint())
                                    .map(|el| match thumb {
                                        Some(p) => el
                                            .child(img(p).size_full().object_fit(ObjectFit::Cover)),
                                        None => el.child(kind),
                                    })
                                    .child(
                                        div()
                                            .absolute()
                                            .bottom_1()
                                            .left_1()
                                            .px_1()
                                            .rounded_sm()
                                            .bg(gpui::black().opacity(0.6))
                                            .text_xs()
                                            .text_color(gpui::white())
                                            .child(kind),
                                    ),
                            )
                            .child(
                                div()
                                    .truncate()
                                    .text_xs()
                                    .text_color(if on { theme::fg() } else { theme::faint() })
                                    .child(s.name()),
                            )
                    })),
            )
            .into_any_element()
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
                .child(div().text_color(theme::fg()).child(t("review.noProof")))
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
                        .flex_1()
                        .min_h_0()
                        .overflow_hidden()
                        .child(self.render_stage(cx)),
                )
                .child(self.render_strip(cx))
                .into_any_element(),
        }
    }

    fn render_diff(&self) -> AnyElement {
        let body = match &self.diff {
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
            Some(Ok(text)) => v_flex()
                .p_4()
                .font_family("monospace")
                .text_xs()
                .children(text.lines().map(|l| {
                    let file = l.starts_with("diff --git");
                    let (fg, bg) = if file {
                        (theme::fg(), theme::raised())
                    } else if l.starts_with("+++")
                        || l.starts_with("---")
                        || l.starts_with("index ")
                    {
                        (theme::faint(), transparent_black())
                    } else if l.starts_with('+') {
                        (theme::running(), theme::running().opacity(0.08))
                    } else if l.starts_with('-') {
                        (theme::error(), theme::error().opacity(0.08))
                    } else if l.starts_with("@@") {
                        (theme::accent(), transparent_black())
                    } else {
                        (theme::muted(), transparent_black())
                    };
                    div()
                        .px_2()
                        .when(file, |el| {
                            el.mt_3()
                                .py_1()
                                .rounded_sm()
                                .font_weight(FontWeight::SEMIBOLD)
                        })
                        .text_color(fg)
                        .bg(bg)
                        .whitespace_nowrap()
                        .child(if l.is_empty() {
                            " ".to_string()
                        } else {
                            l.replace('\t', "    ")
                        })
                }))
                .into_any_element(),
        };
        div()
            .id("review-diff")
            .size_full()
            .overflow_y_scroll()
            .bg(theme::bg())
            .child(body)
            .into_any_element()
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
                                .border_color(theme::border())
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
            .border_color(theme::border())
            .bg(theme::bg())
            .child(div().text_xs().text_color(theme::muted()).child(tv(
                "review.changesTo",
                &[("agent", (&self.item.item.agent).into())],
            )))
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

    fn render_footer(&self, cx: &mut Context<Self>) -> AnyElement {
        let i = &self.item.item;
        let block = self.merge_block();
        let note = if let Some(e) = &self.error {
            Some((e.to_string(), theme::error()))
        } else if self.armed {
            self.pr().map(|pr| {
                (
                    tv("review.mergeArmed", &[("number", pr.number.into())]),
                    theme::fg(),
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
            .border_color(theme::border())
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
                        .bg(theme::raised())
                        .border_1()
                        .border_color(theme::border())
                        .text_sm()
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::hover()))
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
                        .bg(theme::raised())
                        .border_1()
                        .border_color(theme::border())
                        .text_sm()
                        .when(self.busy, |el| el.opacity(0.5))
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::hover()))
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
                .child(
                    h_flex()
                        .id("review-merge")
                        .gap_2()
                        .px_3()
                        .py(px(5.))
                        .rounded_md()
                        .bg(theme::accent())
                        .text_sm()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(gpui::white())
                        .when(block.is_some() || self.busy, |el| el.opacity(0.45))
                        .when(block.is_none() && !self.busy, |el| {
                            el.cursor_pointer()
                                .hover(|s| s.opacity(0.9))
                                .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.merge(cx)))
                        })
                        .child(match self.pr() {
                            Some(pr) => tv("review.mergeNumber", &[("number", pr.number.into())]),
                            None => t("review.noPullRequest"),
                        })
                        .when(block.is_none(), |el| el.child(hint("M"))),
                )
            })
            .into_any_element()
    }
}

impl Render for ReviewView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
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
            .bg(gpui::black().opacity(0.6))
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
                    .bg(theme::panel())
                    .border_1()
                    .border_color(theme::border())
                    .overflow_hidden()
                    .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                    .child(self.render_header(cx))
                    .child(self.render_tabs(cx))
                    .children(self.render_report(cx))
                    .child(div().flex_1().min_h_0().child(body))
                    .when(self.changing, |el| el.child(self.render_changes(cx)))
                    .child(self.render_footer(cx)),
            )
    }
}

/// clock writes seconds as m:ss.
fn clock(secs: f64) -> String {
    let s = secs.max(0.).round() as u64;
    format!("{}:{:02}", s / 60, s % 60)
}
