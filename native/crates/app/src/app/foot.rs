//! The sidebar foot's pages across every project: Media (media/MediaView.tsx,
//! every agent's screenshots, recordings, reports and notes, newest first,
//! by project, unseen or favorites, each opened in the app's own viewer) and Jobs (JobsView.tsx: what the daemon
//! ran, each with its log, a running one cancelled and rolled back).
use std::collections::HashMap;
use std::path::PathBuf;
use std::rc::Rc;

use agentbox_api::client::Method;
use agentbox_api::{Job, MediaItem, MediaPage};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{self, Btn, Choice};
use super::settings::defaults::time_ago;
use super::sidebar::ago;
use crate::{media, theme, toasts};

#[derive(Clone, Copy, PartialEq)]
pub enum FootPage {
    Media,
    Jobs,
}

/// What the pages read.
#[derive(Default)]
pub struct Foot {
    media: Vec<MediaItem>,
    next: String,
    project: String,
    unseen: bool,
    favorites: bool,
    /// Screenshots fetched to disk, by media id.
    files: HashMap<String, PathBuf>,
    loaded: bool,
    jobs: Vec<Job>,
    job: Option<String>,
    log: String,
    /// The item open in the viewer, over the page.
    viewing: Option<Viewing>,
}

/// ViewerClick is what a button over the viewer does.
type ViewerClick = Box<dyn Fn(&mut AppView, &mut Window, &mut Context<AppView>)>;

/// Viewing is an item shown in the app (MediaParts.tsx MediaViewer): a
/// screenshot as itself, a recording in the player, a report or a note read.
struct Viewing {
    item: MediaItem,
    /// The list it was opened from, which the arrows step through: the
    /// Media page's, or an agent's or project's (the panel's), and whose
    /// that is: "" for the Media page, else the panel's (media_for).
    list: Vec<MediaItem>,
    source: String,
    path: Option<PathBuf>,
    text: Option<String>,
    error: Option<String>,
    #[cfg(feature = "video")]
    player: Option<Entity<crate::video::VideoPlayer>>,
    focus: FocusHandle,
    /// What it is part of, as the review shows it (media::gallery): the
    /// other half of its before/after pair, or its recording's chapters.
    pair: Option<media::Pair>,
    other: Option<PathBuf>,
    chapters: Vec<media::Chapter>,
    /// The review's zoom (1 fits) and the scroll it pans by, shared by a
    /// pair's halves and the player.
    zoom: f32,
    zoom_shared: std::rc::Rc<std::cell::Cell<f32>>,
    pan: ScrollHandle,
}

/// The zoom steps and the most it zooms in, as the review's (lib/zoom.ts).
const ZOOM_STEP: f32 = 1.25;
const ZOOM_MAX: f32 = 8.;

const PAGE: usize = 40;

impl AppView {
    pub(super) fn open_foot_page(
        &mut self,
        page: FootPage,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.settings = false;
        self.work = None;
        self.side.page = Some(page);
        match page {
            FootPage::Media => self.load_all_media(false, cx),
            FootPage::Jobs => self.load_jobs(cx),
        }
        cx.notify();
    }

    fn load_all_media(&mut self, more: bool, cx: &mut Context<Self>) {
        let f = &self.side.foot;
        let mut q = vec![format!("limit={PAGE}")];
        if !f.project.is_empty() {
            q.push(format!("project={}", f.project));
        }
        if f.unseen {
            q.push("unseen=1".into());
        }
        if f.favorites {
            q.push("favorite=1".into());
        }
        if more && !f.next.is_empty() {
            q.push(format!("cursor={}", f.next));
        }
        let path = format!("/v1/media?{}", q.join("&"));
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<MediaPage>(&path).await;
            this.update(cx, |this, cx| {
                let f = &mut this.side.foot;
                f.loaded = true;
                match res {
                    Ok(p) => {
                        if !more {
                            f.media.clear();
                        }
                        f.media.extend(p.items);
                        f.next = p.next;
                        let list = f.media.clone();
                        this.sync_media_list("", list);
                        this.fetch_thumbs(cx);
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// fetch_thumbs brings the pictures' files to disk to draw them: the
    /// screenshots, and files that are images. One that couldn't be read is
    /// asked for again the next time the page loads.
    fn fetch_thumbs(&mut self, cx: &mut Context<Self>) {
        for m in &self.side.foot.media {
            if media::look(m) != media::Look::Picture || self.side.foot.files.contains_key(&m.id) {
                continue;
            }
            self.side.foot.files.insert(m.id.clone(), PathBuf::new());
            let (client, item) = (self.client.clone(), m.clone());
            cx.spawn(async move |this, cx| {
                let id = item.id.clone();
                let res = media::fetch(client, item).await;
                this.update(cx, |this, cx| {
                    match res {
                        Ok(p) => {
                            this.side.foot.files.insert(id, p);
                        }
                        Err(_) => {
                            this.side.foot.files.remove(&id);
                        }
                    }
                    cx.notify();
                })
                .ok();
            })
            .detach();
        }
    }

    /// open_media shows an item in the viewer over the page; its file comes
    /// from the daemon's cache on disk.
    pub(super) fn open_media(
        &mut self,
        item: MediaItem,
        list: Vec<MediaItem>,
        source: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let focus = cx.focus_handle();
        focus.focus(window, cx);
        let text = (!item.text.is_empty()).then(|| item.text.clone());
        // Its place among its agent's media, as the review groups them.
        let same: Vec<MediaItem> = list
            .iter()
            .filter(|m| m.agent == item.agent)
            .cloned()
            .collect();
        let (mut pair, mut chapters) = (None, Vec::new());
        for s in media::gallery(&same) {
            match s {
                media::Shown::Pair(p) if p.before.id == item.id || p.after.id == item.id => {
                    pair = Some(p)
                }
                media::Shown::Recording(t) if t.media.id == item.id => chapters = t.chapters,
                _ => {}
            }
        }
        let other = pair.as_ref().map(|p| {
            if p.before.id == item.id {
                p.after.clone()
            } else {
                p.before.clone()
            }
        });
        let zoom_shared = std::rc::Rc::new(std::cell::Cell::new(1.));
        let pan = ScrollHandle::new();
        self.side.foot.viewing = Some(Viewing {
            item: item.clone(),
            list,
            source,
            path: None,
            text,
            error: None,
            #[cfg(feature = "video")]
            player: None,
            focus,
            pair,
            other: None,
            chapters: chapters.clone(),
            zoom: 1.,
            zoom_shared: zoom_shared.clone(),
            pan: pan.clone(),
        });
        cx.notify();
        let client = self.client.clone();
        let id = item.id.clone();
        if let Some(o) = other {
            let (client, id) = (client.clone(), id.clone());
            cx.spawn(async move |this, cx| {
                let res = media::fetch(client, o).await;
                this.update(cx, |this, cx| {
                    if let Some(v) = this.side.foot.viewing.as_mut().filter(|v| v.item.id == id) {
                        v.other = res.ok();
                        cx.notify();
                    }
                })
                .ok();
            })
            .detach();
        }
        #[cfg(feature = "video")]
        let marks: Vec<f64> = chapters.iter().map(|c| c.at).collect();
        cx.spawn(async move |this, cx| {
            let res = media::fetch(client, item.clone()).await;
            // A log, a report, a file that isn't a picture and a note
            // without its text are read as text (a folder report's page too,
            // for where no webview can frame it), as the Electron viewer
            // does; up to a megabyte of it.
            let as_text = match media::look(&item) {
                media::Look::Picture | media::Look::Video => false,
                media::Look::Page(_) => !crate::webview::available(),
                _ => true,
            };
            let text = match &res {
                Ok(p) if item.text.is_empty() && as_text => Some(read_text(p)),
                _ => None,
            };
            this.update(cx, |this, cx| {
                let Some(v) = this.side.foot.viewing.as_mut().filter(|v| v.item.id == id) else {
                    return;
                };
                match res {
                    Ok(p) => {
                        #[cfg(feature = "video")]
                        if v.item.kind == "recording" {
                            let path = p.clone();
                            v.player = Some(cx.new(|cx| {
                                let mut pl = crate::video::VideoPlayer::new(path, cx);
                                pl.set_marks(marks.clone(), cx);
                                pl.set_zoom(zoom_shared.clone(), pan.clone());
                                pl.play(cx);
                                pl
                            }));
                        }
                        v.path = Some(p);
                        match text {
                            Some(Ok(t)) => v.text = Some(t),
                            Some(Err(e)) => v.error = Some(e.to_string()),
                            None => {}
                        }
                    }
                    Err(e) => v.error = Some(e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// step_media opens the item before or after in the page's list.
    fn step_media(&mut self, by: isize, window: &mut Window, cx: &mut Context<Self>) {
        let Some(v) = &self.side.foot.viewing else {
            return;
        };
        let (list, source) = (v.list.clone(), v.source.clone());
        let Some(i) = list.iter().position(|m| m.id == v.item.id) else {
            return;
        };
        let j = i as isize + by;
        if j < 0 || j as usize >= list.len() {
            return;
        }
        let next = list[j as usize].clone();
        self.open_media(next, list, source, window, cx);
    }

    /// seek_media goes to a chapter of the recording on view.
    fn seek_media(&mut self, at: f64, cx: &mut Context<Self>) {
        #[cfg(feature = "video")]
        if let Some(p) = self
            .side
            .foot
            .viewing
            .as_ref()
            .and_then(|v| v.player.clone())
        {
            p.update(cx, |p, cx| p.seek(at, cx));
        }
        #[cfg(not(feature = "video"))]
        let _ = (at, cx);
    }

    /// zoom_media zooms what the viewer shows (1 fits it).
    fn zoom_media(&mut self, zoom: f32, cx: &mut Context<Self>) {
        let Some(v) = self.side.foot.viewing.as_mut() else {
            return;
        };
        let from = v.zoom;
        v.zoom = zoom.clamp(1., ZOOM_MAX);
        crate::viewers::rezoom(&v.pan, from, v.zoom);
        v.zoom_shared.set(v.zoom);
        if v.zoom <= 1.0001 {
            v.pan.set_offset(point(px(0.), px(0.)));
        }
        cx.notify();
    }

    /// zoomed is a picture at the viewer's zoom: past fit it scrolls, and a
    /// drag pans it; a pair's halves pan together.
    fn media_zoomed(v: &Viewing, id: &str, el: AnyElement) -> AnyElement {
        div()
            .id(SharedString::from(format!("media-zoom-{id}")))
            .size_full()
            .overflow_scroll()
            .track_scroll(&v.pan)
            .when(v.zoom > 1.0001, |el| el.cursor_grab())
            .map(|el| crate::viewers::drag_pans(el, &v.pan))
            .child(
                div()
                    .flex_none()
                    .w(relative(v.zoom))
                    .h(relative(v.zoom))
                    .child(el),
            )
            .into_any_element()
    }

    /// sync_media_list gives the viewer the list it came from as it is now,
    /// once that list (the Media page's, or the panel's for `source`) is read
    /// again, so its arrows don't step to items gone or past new ones.
    pub(super) fn sync_media_list(&mut self, source: &str, list: Vec<MediaItem>) {
        if let Some(v) = self.side.foot.viewing.as_mut()
            && v.source == source
        {
            v.list = list;
        }
    }

    /// media_open says the media viewer is open, over what's shown.
    pub(super) fn media_open(&self) -> bool {
        self.side.foot.viewing.is_some()
    }

    pub(super) fn close_media(&mut self, cx: &mut Context<Self>) {
        self.side.foot.viewing = None;
        cx.notify();
    }

    /// render_viewer is the open item over the page, with its name, where
    /// it's from and when, the arrows to the next, and its own viewer.
    pub(super) fn render_media_viewer(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let v = self.side.foot.viewing.as_ref()?;
        let m = &v.item;
        let list = &v.list;
        let look = media::look(m);
        let at = list.iter().position(|x| x.id == m.id);
        let first = at.is_none_or(|i| i == 0);
        let last = at.is_none_or(|i| i + 1 >= list.len());
        let who = if m.agent_title.is_empty() {
            m.agent_name.clone()
        } else {
            m.agent_title.clone()
        };
        let from = t(if m.source == "agent" {
            "agent.mediaTab.fromAgent"
        } else {
            "agent.mediaTab.fromYou"
        });
        let sub = [who, from, crate::dates::date_time_of(&m.created_at)]
            .into_iter()
            .filter(|s| !s.is_empty())
            .collect::<Vec<_>>()
            .join(" · ");
        let icon = |id: &'static str,
                    label: &'static str,
                    tip: String,
                    disabled: bool,
                    click: ViewerClick| {
            div()
                .id(id)
                .px_2()
                .py_1()
                .rounded_md()
                .text_color(if disabled {
                    theme::faint()
                } else {
                    theme::muted()
                })
                .when(!disabled, |el| {
                    el.cursor_pointer()
                        .hover(|s| s.bg(theme::surface_strong()).text_color(theme::primary()))
                        .on_click(
                            cx.listener(move |this, _: &ClickEvent, w, cx| click(this, w, cx)),
                        )
                })
                .tooltip(move |w, cx| {
                    gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx)
                })
                .child(crate::icons::of(label))
        };
        let body: AnyElement = if let Some(e) = v.error.as_ref().filter(|_| v.text.is_none()) {
            div()
                .text_color(theme::error())
                .child(e.clone())
                .into_any_element()
        } else if look == media::Look::Picture {
            let picture = |p: &PathBuf| {
                img(p.clone())
                    .size_full()
                    .object_fit(ObjectFit::Contain)
                    .into_any_element()
            };
            match (&v.path, &v.pair, &v.other) {
                // Part of a before/after pair: both halves side by side, as
                // the review compares them, zoomed and panned together.
                (Some(p), Some(pair), Some(o)) => {
                    let mine_before = pair.before.id == m.id;
                    let (b, a) = if mine_before { (p, o) } else { (o, p) };
                    let half = |label: String, id: &str, path: &PathBuf| {
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .h_full()
                            .gap_1()
                            .child(
                                div()
                                    .text_xs()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::muted())
                                    .child(label),
                            )
                            .child(
                                div()
                                    .flex_1()
                                    .min_h_0()
                                    .rounded_lg()
                                    .overflow_hidden()
                                    .child(Self::media_zoomed(v, id, picture(path))),
                            )
                    };
                    h_flex()
                        .size_full()
                        .gap_3()
                        .child(half(t("review.compare.before"), "before", b))
                        .child(half(t("review.compare.after"), "after", a))
                        .into_any_element()
                }
                (Some(p), _, _) => Self::media_zoomed(v, &m.id, picture(p)),
                _ => div()
                    .text_color(theme::faint())
                    .child(t("common.loading"))
                    .into_any_element(),
            }
        } else if let (media::Look::Page(entry), true) = (look.clone(), crate::webview::available())
        {
            // A folder report (an HTML test report): its page, framed as
            // the Electron viewer's iframe, reading the folder's files.
            div()
                .size_full()
                .rounded_lg()
                .overflow_hidden()
                .bg(gpui::white())
                .child(crate::webview::page(
                    format!("media-page-{}", m.id),
                    crate::webview::daemon_url(&media::page_path(m, &entry)),
                    true,
                ))
                .into_any_element()
        } else if look == media::Look::Video {
            // Its chapters beside it, each a jump in the recording.
            h_flex()
                .size_full()
                .gap_4()
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .child(self.viewer_player(v)),
                )
                .when(!v.chapters.is_empty(), |el| {
                    el.child(
                        v_flex()
                            .id("media-chapters")
                            .w(px(260.))
                            .flex_none()
                            .h_full()
                            .overflow_y_scroll()
                            .gap_1()
                            .child(
                                div()
                                    .text_xs()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::muted())
                                    .child(t("review.checked")),
                            )
                            .children(v.chapters.iter().enumerate().map(|(n, c)| {
                                let at = c.at;
                                v_flex()
                                    .id(SharedString::from(format!("media-chapter-{n}")))
                                    .px_2()
                                    .py_1p5()
                                    .rounded_md()
                                    .cursor_pointer()
                                    .hover(|s| s.bg(theme::surface_strong()))
                                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        this.seek_media(at, cx)
                                    }))
                                    .child(
                                        h_flex()
                                            .gap_2()
                                            .text_sm()
                                            .child(
                                                div()
                                                    .flex_1()
                                                    .min_w_0()
                                                    .text_color(theme::primary())
                                                    .child(c.title.clone()),
                                            )
                                            .child(
                                                div()
                                                    .flex_none()
                                                    .text_xs()
                                                    .font_family(theme::mono())
                                                    .text_color(theme::faint())
                                                    .child(super::panel::clock(at)),
                                            ),
                                    )
                                    .when_some(c.detail.clone(), |el, d| {
                                        el.child(
                                            div()
                                                .text_xs()
                                                .text_color(theme::faint())
                                                .line_clamp(3)
                                                .child(d),
                                        )
                                    })
                            })),
                    )
                })
                .into_any_element()
        } else {
            match &v.text {
                Some(text) => div()
                    .id("media-viewer-text")
                    .size_full()
                    .overflow_y_scroll()
                    .p_6()
                    .rounded_lg()
                    .bg(theme::card())
                    .text_sm()
                    .map(|el| {
                        // A note or a Markdown report reads as prose; a log or
                        // any other file as it is, in monospace (ViewerBody's <pre>).
                        let prose = m.kind == "note"
                            || (m.kind == "report" && m.file.to_ascii_lowercase().ends_with(".md"));
                        if prose {
                            el.child(gpui_component::text::TextView::markdown(
                                SharedString::from(format!("media-text-{}", m.id)),
                                text.clone(),
                            ))
                        } else {
                            el.font_family(theme::mono())
                                .text_xs()
                                .text_color(theme::muted())
                                .child(text.clone())
                        }
                    })
                    .into_any_element(),
                None => div()
                    .text_color(theme::faint())
                    .child(t("common.loading"))
                    .into_any_element(),
            }
        };
        let path = v.path.clone();
        let zoomable = m.kind == "screenshot" || m.kind == "recording";
        Some(
            v_flex()
                .id("media-viewer")
                .absolute()
                .inset_0()
                .bg(theme::ink())
                .track_focus(&v.focus)
                .on_key_down(cx.listener(move |this, e: &KeyDownEvent, window, cx| {
                    let z = this.side.foot.viewing.as_ref().map_or(1., |v| v.zoom);
                    match e.keystroke.key.as_str() {
                        "escape" => this.close_media(cx),
                        "left" => this.step_media(-1, window, cx),
                        "right" => this.step_media(1, window, cx),
                        "+" | "=" if zoomable => this.zoom_media(z * ZOOM_STEP, cx),
                        "-" if zoomable => this.zoom_media(z / ZOOM_STEP, cx),
                        "0" if zoomable => this.zoom_media(1., cx),
                        _ => return,
                    }
                    cx.stop_propagation();
                }))
                // Ctrl (or ⌘) and the wheel zoom, as the review's.
                .on_scroll(cx.listener(move |this, ev: &ScrollEvent, _, cx| {
                    if !zoomable || !(ev.modifiers.control || ev.modifiers.platform) {
                        return;
                    }
                    let z = this.side.foot.viewing.as_ref().map_or(1., |v| v.zoom);
                    let dy = ev.delta.pixel_delta(px(16.)).y;
                    if dy > px(0.) {
                        this.zoom_media(z * ZOOM_STEP, cx);
                    } else if dy < px(0.) {
                        this.zoom_media(z / ZOOM_STEP, cx);
                    }
                    cx.stop_propagation();
                }))
                .child(
                    h_flex()
                        .px_4()
                        .py_2()
                        .gap_2()
                        .border_b_1()
                        .border_color(theme::line())
                        .child(
                            v_flex()
                                .flex_1()
                                .min_w_0()
                                .child(
                                    div()
                                        .truncate()
                                        .text_sm()
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(theme::primary())
                                        .child(media::readable(&m.name)),
                                )
                                .child(
                                    div()
                                        .truncate()
                                        .text_xs()
                                        .text_color(theme::faint())
                                        .child(sub),
                                ),
                        )
                        .when(zoomable, |el| {
                            let z = v.zoom;
                            el.child(icon(
                                "media-zoom-out",
                                "zoom-out",
                                t("review.zoom.out"),
                                z <= 1.,
                                Box::new(move |this, _, cx| this.zoom_media(z / ZOOM_STEP, cx)),
                            ))
                            .child(
                                div()
                                    .id("media-zoom-fit")
                                    .px_1()
                                    .text_xs()
                                    .font_family(theme::mono())
                                    .text_color(theme::muted())
                                    .cursor_pointer()
                                    .hover(|s| s.text_color(theme::primary()))
                                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                        this.zoom_media(1., cx)
                                    }))
                                    .child(format!(
                                        "{}% · {}",
                                        (z * 100.).round(),
                                        t("review.zoom.fit")
                                    )),
                            )
                            .child(icon(
                                "media-zoom-in",
                                "zoom-in",
                                t("review.zoom.in"),
                                z >= ZOOM_MAX,
                                Box::new(move |this, _, cx| this.zoom_media(z * ZOOM_STEP, cx)),
                            ))
                        })
                        .child(icon(
                            "media-prev",
                            "‹",
                            t("agent.mediaTab.previous"),
                            first,
                            Box::new(|this, w, cx| this.step_media(-1, w, cx)),
                        ))
                        .child(icon(
                            "media-next",
                            "›",
                            t("common.next"),
                            last,
                            Box::new(|this, w, cx| this.step_media(1, w, cx)),
                        ))
                        .child(icon(
                            "media-system",
                            "↗",
                            t("agent.mediaTab.openDefault"),
                            path.is_none(),
                            Box::new(move |_, _, cx| {
                                if let Some(p) = &path {
                                    cx.open_with_system(p);
                                }
                            }),
                        ))
                        .child(icon(
                            "media-close",
                            "✕",
                            t("common.close"),
                            false,
                            Box::new(|this, _, cx| this.close_media(cx)),
                        )),
                )
                .child(
                    div()
                        .flex_1()
                        .min_h_0()
                        .p_4()
                        .flex()
                        .items_center()
                        .justify_center()
                        .child(body),
                )
                .into_any_element(),
        )
    }

    #[cfg(feature = "video")]
    fn viewer_player(&self, v: &Viewing) -> AnyElement {
        match &v.player {
            Some(p) => div().size_full().child(p.clone()).into_any_element(),
            None => div()
                .text_color(theme::faint())
                .child(t("common.loading"))
                .into_any_element(),
        }
    }

    #[cfg(not(feature = "video"))]
    fn viewer_player(&self, _v: &Viewing) -> AnyElement {
        div()
            .text_color(theme::faint())
            .child(t("native.video.unavailable"))
            .into_any_element()
    }

    fn load_jobs(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let jobs = client.get::<Vec<Job>>("/v1/jobs").await.unwrap_or_default();
            this.update(cx, |this, cx| {
                this.side.foot.jobs = jobs;
                this.side.foot.loaded = true;
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn pick_job(&mut self, id: String, cx: &mut Context<Self>) {
        self.side.foot.job = Some(id.clone());
        self.side.foot.log.clear();
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let log = client
                .raw(Method::GET, &format!("/v1/jobs/{id}/log"), None)
                .await
                .map(|b| String::from_utf8_lossy(&b).into_owned())
                .unwrap_or_else(|e| e.to_string());
            this.update(cx, |this, cx| {
                if this.side.foot.job.as_deref() == Some(id.as_str()) {
                    this.side.foot.log = log;
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn cancel_job(&mut self, id: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(Method::POST, &format!("/v1/jobs/{id}/cancel"), None)
                .await;
            this.update(cx, |this, cx| {
                if let Err(e) = res {
                    toasts::error(cx, e.to_string());
                }
                this.load_jobs(cx);
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn render_foot_page(&self, cx: &mut Context<Self>) -> AnyElement {
        match self.side.page {
            Some(FootPage::Jobs) => self.render_jobs(cx),
            _ => div()
                .relative()
                .size_full()
                .child(self.render_media_page(cx))
                .into_any_element(),
        }
    }

    fn render_media_page(&self, cx: &mut Context<Self>) -> AnyElement {
        let f = &self.side.foot;
        let mut choices = vec![Choice::new("", t("shell.allMedia.allProjects"))];
        choices.extend(
            self.projects
                .iter()
                .map(|p| Choice::new(p.name.clone(), super::sidebar_actions::label_of(p))),
        );
        let view = cx.entity().downgrade();
        let chip = |id: &'static str, label: String, on: bool| {
            div()
                .id(id)
                .px_2p5()
                .py_1()
                .rounded_full()
                .border_1()
                .border_color(if on { theme::muted() } else { theme::line() })
                .when(on, |el| el.bg(theme::surface_strong()))
                .text_xs()
                .text_color(if on { theme::primary() } else { theme::muted() })
                .cursor_pointer()
                .child(label)
        };
        let cards = f.media.iter().map(|m| {
            let item = m.clone();
            let file = f
                .files
                .get(&m.id)
                .filter(|p| !p.as_os_str().is_empty())
                .cloned();
            let glyph = match m.kind.as_str() {
                "recording" => "film",
                "report" | "log" => "file-text",
                "note" => "sticky-note",
                "file" => "file",
                _ => "image",
            };
            let who = if m.agent_title.is_empty() {
                m.agent_name.clone()
            } else {
                m.agent_title.clone()
            };
            v_flex()
                .id(SharedString::from(format!("media-{}", m.id)))
                .w(px(220.))
                .rounded_lg()
                .overflow_hidden()
                .border_1()
                .border_color(theme::line())
                .bg(theme::surface_raised())
                .cursor_pointer()
                .hover(|el| el.border_color(theme::muted()))
                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                    let list = this.side.foot.media.clone();
                    this.open_media(item.clone(), list, String::new(), window, cx)
                }))
                .child(
                    div()
                        .h(px(124.))
                        .bg(theme::card())
                        .flex()
                        .items_center()
                        .justify_center()
                        .text_2xl()
                        .text_color(theme::faint())
                        .map(|el| match file {
                            Some(p) => el.child(img(p).size_full().object_fit(ObjectFit::Cover)),
                            None => el.child(crate::icons::of(glyph)),
                        }),
                )
                .child(
                    v_flex()
                        .p_2()
                        .gap_0p5()
                        .child(
                            h_flex()
                                .gap_1()
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .truncate()
                                        .text_sm()
                                        .text_color(theme::primary())
                                        .child(media::readable(&m.name)),
                                )
                                .when(m.unseen, |el| {
                                    el.child(controls::badge(
                                        t("shell.allMedia.new"),
                                        theme::accent(),
                                    ))
                                })
                                .when(m.favorite, |el| el.child(crate::icons::of("★"))),
                        )
                        .child(
                            div()
                                .text_xs()
                                .truncate()
                                .text_color(theme::faint())
                                .child(format!("{who} · {}", ago(&m.created_at))),
                        ),
                )
        });
        let empty = f.loaded && f.media.is_empty();
        v_flex()
            .id("media-page")
            .size_full()
            .overflow_y_scroll()
            .p_6()
            .gap_4()
            .child(
                v_flex()
                    .gap_1()
                    .child(
                        div()
                            .text_xl()
                            .font_weight(FontWeight::SEMIBOLD)
                            .child(t("shell.nav.media")),
                    )
                    .child(
                        div()
                            .text_sm()
                            .text_color(theme::muted())
                            .child(t("media.view.subtitle")),
                    ),
            )
            .child(
                h_flex()
                    .gap_2()
                    .flex_wrap()
                    .child(div().w(px(220.)).child(controls::select_at(
                        "media-project",
                        choices,
                        &f.project,
                        false,
                        Rc::new(move |v: &str, _, cx| {
                            let v = v.to_string();
                            view.update(cx, |this, cx| {
                                this.side.foot.project = v;
                                this.load_all_media(false, cx);
                            })
                            .ok();
                        }),
                        Anchor::TopLeft,
                    )))
                    .child(
                        chip("media-unseen", t("shell.allMedia.unseen"), f.unseen).on_click(
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.side.foot.unseen = !this.side.foot.unseen;
                                this.load_all_media(false, cx);
                            }),
                        ),
                    )
                    .child(
                        chip("media-fav", t("shell.allMedia.favorites"), f.favorites).on_click(
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.side.foot.favorites = !this.side.foot.favorites;
                                this.load_all_media(false, cx);
                            }),
                        ),
                    ),
            )
            .when(empty, |el| {
                el.child(
                    v_flex()
                        .py_12()
                        .items_center()
                        .gap_2()
                        .child(
                            div()
                                .text_color(theme::primary())
                                .child(t("project.media.emptyTitle")),
                        )
                        .child(
                            div()
                                .max_w(px(420.))
                                .text_sm()
                                .text_center()
                                .text_color(theme::muted())
                                .child(t("shell.allMedia.emptyBody")),
                        ),
                )
            })
            .child(h_flex().flex_wrap().gap_3().children(cards))
            .when(!f.next.is_empty(), |el| {
                el.child(h_flex().child(controls::button(
                    "media-more",
                    t("common.more"),
                    Btn::Secondary,
                    false,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.load_all_media(true, cx)),
                )))
            })
            .into_any_element()
    }

    /// render_jobs is JobsView.tsx: the recent jobs beside the picked one's
    /// log, two Cards in a 1fr / 1.1fr grid.
    fn render_jobs(&self, cx: &mut Context<Self>) -> AnyElement {
        let f = &self.side.foot;
        if f.loaded && f.jobs.is_empty() {
            return v_flex()
                .size_full()
                .items_center()
                .justify_center()
                .gap_2()
                .child(
                    div()
                        .text_color(theme::primary())
                        .child(t("shell.jobs.emptyTitle")),
                )
                .child(
                    div()
                        .max_w(px(420.))
                        .text_sm()
                        .text_center()
                        .text_color(theme::muted())
                        .child(t("shell.jobs.emptyBody")),
                )
                .into_any_element();
        }
        let current = f
            .job
            .as_ref()
            .and_then(|id| f.jobs.iter().find(|j| &j.id == id));
        // The rows: flex items-center gap-3 rounded-xl px-3 py-2.5 text-ui.
        let rows = f.jobs.iter().map(|j| {
            let id = j.id.clone();
            let on = f.job.as_deref() == Some(j.id.as_str());
            h_flex()
                .id(SharedString::from(format!("job-{}", j.id)))
                .px_3()
                .py_2p5()
                .gap_3()
                .rounded(theme::radius::XL)
                .text_size(theme::text::UI)
                .cursor_pointer()
                .when(on, |el| el.bg(theme::surface_raised()))
                .hover(|el| el.bg(theme::surface()))
                .on_click(
                    cx.listener(move |this, _: &ClickEvent, _, cx| this.pick_job(id.clone(), cx)),
                )
                .child(status_badge(&j.status))
                .child(
                    div()
                        .flex_none()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::secondary())
                        .child(j.kind.clone()),
                )
                .child(
                    div()
                        .min_w_0()
                        .truncate()
                        .text_color(theme::subtle())
                        .child(j.target.clone()),
                )
                .child(div().flex_1())
                .child(
                    div()
                        .flex_none()
                        .text_size(px(12.))
                        .line_height(px(16.))
                        .text_color(theme::subtle())
                        .child(time_ago(&j.created_at)),
                )
                .child(
                    div()
                        .flex_none()
                        .w(px(56.))
                        .text_right()
                        .text_size(theme::text::CAPTION)
                        .font_family(theme::MONO)
                        .text_color(theme::faint())
                        .child(duration(&j.created_at, j.finished_at.as_deref())),
                )
        });
        let action = current.map(|j| {
            let id = j.id.clone();
            if j.status == "running" {
                controls::button(
                    "job-cancel",
                    t("shell.jobs.cancelRollback"),
                    Btn::Secondary,
                    false,
                    cx.listener(move |this, _: &ClickEvent, _, cx| this.cancel_job(id.clone(), cx)),
                )
            } else {
                status_badge(&j.status)
            }
        });
        h_flex()
            .size_full()
            .overflow_hidden()
            .p_6()
            .gap_4()
            .items_stretch()
            .child(
                card(Some("list-checks"), t("shell.jobs.recent"), None, None)
                    .flex_grow(1.)
                    .flex_basis(px(0.))
                    .child(
                        div()
                            .id("jobs-list")
                            .flex_1()
                            .min_h_0()
                            .overflow_y_scroll()
                            .px_2()
                            .pt_3()
                            .pb_2()
                            .child(v_flex().gap_1().children(rows)),
                    ),
            )
            .child(
                card(
                    None,
                    match current {
                        Some(j) => format!("{} {}", j.kind, j.target),
                        None => t("shell.jobs.log"),
                    },
                    current.map(|j| {
                        tv(
                            "shell.jobs.jobId",
                            &[
                                ("id", (&j.id).into()),
                                (
                                    "duration",
                                    duration(&j.created_at, j.finished_at.as_deref()).into(),
                                ),
                            ],
                        )
                    }),
                    action,
                )
                .flex_basis(px(0.))
                .flex_grow(1.1)
                .child(
                    div()
                        .id("job-log")
                        .flex_1()
                        .min_h_0()
                        .overflow_y_scroll()
                        .px_5()
                        .pt_3()
                        .pb_5()
                        .map(|el| {
                            if f.job.is_some() {
                                el.text_xs()
                                    .font_family(theme::MONO)
                                    .text_color(theme::muted())
                                    .child(f.log.clone())
                            } else {
                                el.text_size(px(14.))
                                    .line_height(px(20.))
                                    .text_color(theme::subtle())
                                    .child(t("shell.jobs.pick"))
                            }
                        }),
                ),
            )
            .into_any_element()
    }
}

/// card is ui/card.tsx's Card: the `.panel` ground, rounded-2xl, a header
/// (an icon in a ringed tile, the title, a description, an action) over
/// the body the caller adds.
fn card(
    icon: Option<&'static str>,
    title: String,
    description: Option<String>,
    action: Option<AnyElement>,
) -> Stateful<Div> {
    v_flex()
        .id(SharedString::from(format!("card-{title}")))
        .min_w_0()
        .min_h_0()
        .overflow_hidden()
        .rounded(theme::radius::XXL)
        .border_1()
        .border_color(theme::line())
        .bg(theme::linear_panel())
        .shadow(theme::shadow_card())
        .child(
            h_flex()
                .flex_none()
                .gap_3()
                .px_5()
                .pt_4()
                .pb_1()
                .when_some(icon, |el, icon| {
                    el.child(
                        div()
                            .flex_none()
                            .size_7()
                            .flex()
                            .items_center()
                            .justify_center()
                            .rounded(theme::radius::LG)
                            .bg(theme::surface())
                            .border_1()
                            .border_color(theme::line())
                            .text_color(theme::muted())
                            .child(crate::icons::ic(icon).size(px(16.))),
                    )
                })
                .child(
                    v_flex()
                        .min_w_0()
                        .child(
                            div()
                                .text_size(px(14.))
                                .line_height(px(20.))
                                .font_weight(FontWeight::SEMIBOLD)
                                .text_color(theme::primary())
                                .child(title),
                        )
                        .when_some(description, |el, d| {
                            el.child(
                                div()
                                    .text_size(px(12.))
                                    .line_height(px(16.))
                                    .text_color(theme::subtle())
                                    .child(d),
                            )
                        }),
                )
                .when_some(action, |el, a| {
                    el.child(div().flex_1()).child(h_flex().gap_1().child(a))
                }),
        )
}

/// Variant is ui/badge.tsx's: the pill's tint, text and ring.
#[derive(Clone, Copy, PartialEq)]
pub(super) enum Variant {
    Default,
    Success,
    Warning,
    Danger,
    Info,
}

/// ui_badge is ui/badge.tsx's Badge: inline-flex gap-1.5 rounded-full px-2
/// py-0.5 text-caption font-medium ring-1 ring-inset, its svg size-3.
pub(super) fn ui_badge(variant: Variant, icon: Option<&str>, text: impl Into<SharedString>) -> Div {
    let (fg, bg, ring) = match variant {
        Variant::Default => (theme::tertiary(), theme::surface(), theme::line_strong()),
        Variant::Success => {
            let e = theme::emerald(400);
            (theme::emerald(300), e.opacity(0.1), e.opacity(0.25))
        }
        Variant::Warning => {
            let a = theme::amber(400);
            (theme::amber(200), a.opacity(0.1), a.opacity(0.25))
        }
        Variant::Danger => (
            theme::rose(300),
            theme::rose(400).opacity(0.1),
            theme::rose(400).opacity(0.25),
        ),
        Variant::Info => {
            let s = theme::sky(400);
            (theme::sky(300), s.opacity(0.1), s.opacity(0.25))
        }
    };
    // The ring is inset in Electron (it takes no room), a border here: the
    // padding gives up its pixel.
    h_flex()
        .flex_none()
        .gap_1p5()
        .px(px(7.))
        .py(px(1.))
        .rounded_full()
        .border_1()
        .border_color(ring)
        .bg(bg)
        .text_size(theme::text::CAPTION)
        .font_weight(FontWeight::MEDIUM)
        .text_color(fg)
        .when_some(icon, |el, i| el.child(crate::icons::ic(i).size(px(12.))))
        .child(text.into())
}

/// status_badge is state.tsx's JobStatusBadge.
fn status_badge(status: &str) -> AnyElement {
    let (variant, key) = match status {
        "running" => (Variant::Info, Some("agent.state.running")),
        "succeeded" => (Variant::Success, Some("agent.job.succeeded")),
        "failed" => (Variant::Danger, Some("agent.job.failed")),
        "cancelled" => (Variant::Warning, Some("agent.job.cancelled")),
        _ => (Variant::Default, None),
    };
    ui_badge(
        variant,
        (status == "running").then_some("loader-circle"),
        key.map_or_else(|| status.to_string(), t),
    )
    .into_any_element()
}

/// duration is utils.ts's: 4.2s under a minute, else 3m 5s.
fn duration(from: &str, to: Option<&str>) -> String {
    let start = media::ms(from);
    let end = to
        .map(media::ms)
        .unwrap_or_else(|| chrono::Utc::now().timestamp_millis());
    if start == 0 || end < start {
        return String::new();
    }
    let ms = end - start;
    if ms < 60_000 {
        format!("{:.1}s", ms as f64 / 1000.)
    } else {
        format!(
            "{}m {}s",
            ms / 60_000,
            ((ms % 60_000) as f64 / 1000.).round() as i64
        )
    }
}

/// read_text is a file read as text for the viewer: up to a megabyte, as
/// the Electron viewer reads (useMediaText), whatever its encoding.
fn read_text(p: &std::path::Path) -> std::io::Result<String> {
    use std::io::Read as _;
    let mut bytes = Vec::new();
    std::fs::File::open(p)?
        .take(1 << 20)
        .read_to_end(&mut bytes)?;
    Ok(String::from_utf8_lossy(&bytes).into_owned())
}
