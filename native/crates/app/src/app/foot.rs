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
use crate::{media, theme, toasts};

#[derive(Clone, Copy, PartialEq)]
pub enum FootPage {
    Media,
    Jobs,
}

/// A card's picture as made: its width and height, and the image.
type Thumb = (u32, u32, std::sync::Arc<RenderImage>);

/// What the pages read.
#[derive(Default)]
pub struct Foot {
    media: Vec<MediaItem>,
    next: String,
    project: String,
    /// The agent picked within project (by name), "" for all of them.
    agent: String,
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
    /// The Media page's kind chip ("" for everything, "page" for Hatch's
    /// pages) and search (MediaView.tsx's filter.kind and query).
    kind: String,
    q: String,
    search: Option<Entity<gpui_component::input::InputState>>,
    _search: Option<Subscription>,
    /// GET /v1/media/counts of what the filters keep, and of everything
    /// (the project picker's).
    counts: Option<agentbox_api::MediaCounts>,
    everything: Option<agentbox_api::MediaCounts>,
    /// Every project's live Hatch pages, with their project (usePagesOf('')).
    pages: Vec<(String, agentbox_api::Artifact)>,
    scroll: ScrollHandle,
    /// The recordings' frames at 0.5 s (BGRA), by media id.
    stills: HashMap<String, std::sync::Arc<image::RgbaImage>>,
    /// The cards' pictures, resized ahead for the card they fill (as
    /// Chromium filters a picture it shrinks), by media id, with the size
    /// each was made at; and those being made.
    thumbs: std::cell::RefCell<HashMap<String, Thumb>>,
    making: std::cell::RefCell<std::collections::HashSet<(String, u32, u32)>>,
}

/// Picture is what a card's thumbnail is made from.
#[derive(Clone)]
enum Picture {
    /// A file on disk (a screenshot), drawn object-cover object-top.
    File(PathBuf),
    /// A decoded frame (a recording's still), drawn object-cover.
    Frame(std::sync::Arc<image::RgbaImage>),
}

/// fill is a picture resized to cover w×h device pixels and cut to it,
/// from its top (a screenshot) or its middle (a still); BGRA, as GPUI
/// draws a frame.
fn fill(picture: Picture, w: u32, h: u32) -> Option<image::RgbaImage> {
    let (src, top) = match picture {
        Picture::File(p) => {
            let mut img = image::load_from_memory(&std::fs::read(p).ok()?)
                .ok()?
                .to_rgba8();
            for px in img.pixels_mut() {
                px.0.swap(0, 2);
            }
            (img, true)
        }
        Picture::Frame(f) => ((*f).clone(), false),
    };
    let (sw, sh) = (src.width() as f32, src.height() as f32);
    let k = (w as f32 / sw).max(h as f32 / sh);
    let (nw, nh) = (
        ((sw * k).round() as u32).max(w),
        ((sh * k).round() as u32).max(h),
    );
    let big = image::imageops::resize(&src, nw, nh, image::imageops::FilterType::Triangle);
    let x = (nw - w) / 2;
    let y = if top { 0 } else { (nh - h) / 2 };
    Some(image::imageops::crop_imm(&big, x, y, w, h).to_image())
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
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.settings = false;
        self.work = None;
        // The viewer is the page's it was opened on: it stayed over Jobs.
        self.side.foot.viewing = None;
        self.side.page = Some(page);
        if page == FootPage::Media && self.side.foot.search.is_none() {
            let search = cx.new(|cx| {
                // Its placeholder is drawn under it (media_search), in faint.
                gpui_component::input::InputState::new(window, cx)
            });
            let sub = cx.subscribe(
                &search,
                |this: &mut Self, s, ev: &gpui_component::input::InputEvent, cx| {
                    if !matches!(ev, gpui_component::input::InputEvent::Change) {
                        return;
                    }
                    this.side.foot.q = s.read(cx).value().trim().to_string();
                    this.load_all_media(false, cx);
                },
            );
            self.side.foot.search = Some(search);
            self.side.foot._search = Some(sub);
        }
        match page {
            FootPage::Media => {
                self.load_all_media(false, cx);
                self.load_media_pages(cx);
            }
            FootPage::Jobs => self.load_jobs(cx),
        }
        cx.notify();
    }

    fn load_all_media(&mut self, more: bool, cx: &mut Context<Self>) {
        let f = &self.side.foot;
        let mut q = vec![format!("limit={PAGE}")];
        if !f.project.is_empty() {
            q.push(format!("project={}", f.project));
            if !f.agent.is_empty() {
                q.push(format!("agent={}", enc(&f.agent)));
            }
        }
        if f.unseen {
            q.push("unseen=1".into());
        }
        if f.favorites {
            q.push("favorite=1".into());
        }
        // The scope the counts are of: ?project= (media_list.go).
        let mut scope = Vec::new();
        if !f.project.is_empty() {
            scope.push(format!("project={}", enc(&f.project)));
            if !f.agent.is_empty() {
                scope.push(format!("agent={}", enc(&f.agent)));
            }
        }
        if !f.kind.is_empty() && f.kind != PAGE_KIND {
            q.push(format!("only={}", f.kind));
            scope.push(format!("only={}", f.kind));
        }
        if !f.q.is_empty() {
            q.push(format!("q={}", enc(&f.q)));
            scope.push(format!("q={}", enc(&f.q)));
        }
        if more && !f.next.is_empty() {
            q.push(format!("cursor={}", f.next));
        }
        if !more {
            self.load_media_counts(scope.join("&"), cx);
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

    /// load_media_counts reads the filters' counts, and everything's for
    /// the project picker (useMediaCounts).
    fn load_media_counts(&mut self, scope: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let counts = client
                .get::<agentbox_api::MediaCounts>(&format!("/v1/media/counts?{scope}"))
                .await
                .ok();
            let everything = client
                .get::<agentbox_api::MediaCounts>("/v1/media/counts?")
                .await
                .ok();
            this.update(cx, |this, cx| {
                this.side.foot.counts = counts;
                this.side.foot.everything = everything;
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// load_media_pages reads every project's Hatch pages (usePagesOf('')):
    /// the live ones, none for a project without Hatch.
    fn load_media_pages(&mut self, cx: &mut Context<Self>) {
        let names: Vec<String> = self
            .projects
            .iter()
            .map(|p| p.name.clone())
            .filter(|n| n != agentbox_api::HOME_PROJECT)
            .collect();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let mut pages = Vec::new();
            for name in names {
                let Ok(list) = client
                    .get::<agentbox_api::Artifacts>(&format!(
                        "/v1/projects/{}/artifacts",
                        enc(&name)
                    ))
                    .await
                else {
                    continue;
                };
                if list.connector.is_empty() {
                    continue;
                }
                let now = chrono::Utc::now().timestamp_millis();
                pages.extend(
                    list.artifacts
                        .into_iter()
                        .filter(|a| {
                            !a.expired
                                && (a.permanent
                                    || a.expires_at.as_deref().is_none_or(|e| media::ms(e) > now))
                        })
                        .map(|a| (name.clone(), a)),
                );
            }
            this.update(cx, |this, cx| {
                this.side.foot.pages = pages;
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
            let still = m.kind == "recording";
            if (media::look(m) != media::Look::Picture && !still)
                || self.side.foot.files.contains_key(&m.id)
            {
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
                            if still {
                                this.make_still(id.clone(), p.clone(), cx);
                            }
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

    /// make_still draws a recording's frame at 0.5 s for its card, on a
    /// thread (MediaParts.tsx VideoStill).
    #[cfg(feature = "video")]
    fn make_still(&mut self, id: String, path: PathBuf, cx: &mut Context<Self>) {
        if self.side.foot.stills.contains_key(&id) {
            return;
        }
        let (tx, rx) = tokio::sync::oneshot::channel();
        std::thread::spawn(move || {
            // lib/videoStills.ts draws it 480 wide.
            let _ = tx.send(crate::video::frame_at(&path, 480, 0.5).ok());
        });
        cx.spawn(async move |this, cx| {
            if let Ok(Some(frame)) = rx.await {
                this.update(cx, |this, cx| {
                    this.side.foot.stills.insert(id, std::sync::Arc::new(frame));
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    #[cfg(not(feature = "video"))]
    fn make_still(&mut self, _: String, _: PathBuf, _: &mut Context<Self>) {}

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
                                    .active(|s| s.bg(theme::surface_vivid()))
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
                                    .active(|s| s.opacity(0.7))
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

    /// render_media_page is media/MediaView.tsx with `heading`, across every
    /// project: its title, the project picker and search, the kind chips,
    /// then the media and Hatch's pages by day in a grid of cards.
    fn render_media_page(&self, cx: &mut Context<Self>) -> AnyElement {
        let f = &self.side.foot;
        let with_media = f.kind != PAGE_KIND;
        // pagesIn: the project's (or every one's), never under Favorites.
        let scope_pages: Vec<&(String, agentbox_api::Artifact)> = f
            .pages
            .iter()
            .filter(|(p, a)| {
                f.project.is_empty()
                    || (*p == f.project
                        && (f.agent.is_empty() || a.agents.contains(&format!("{p}/{}", f.agent))))
            })
            .collect();
        let shows_pages = (f.kind.is_empty() || f.kind == PAGE_KIND) && !f.favorites && !f.unseen;
        let words: Vec<String> =
            f.q.to_lowercase()
                .split_whitespace()
                .map(String::from)
                .collect();
        let mut pages: Vec<&(String, agentbox_api::Artifact)> = if shows_pages {
            scope_pages
                .iter()
                .copied()
                .filter(|(p, a)| {
                    let hay = format!(
                        "{} {} {}",
                        a.title,
                        a.agent.split('/').nth(1).unwrap_or(""),
                        if f.project.is_empty() { p.as_str() } else { "" }
                    )
                    .to_lowercase();
                    words.iter().all(|w| hay.contains(w.as_str()))
                })
                .collect()
        } else {
            Vec::new()
        };
        pages.sort_by(|a, b| b.1.updated_at.cmp(&a.1.updated_at));
        let items: Vec<&MediaItem> = if with_media {
            f.media
                .iter()
                .filter(|m| (!f.unseen || m.unseen) && (!f.favorites || m.favorite))
                .collect()
        } else {
            Vec::new()
        };
        // interleave: a page older than the oldest item waits for the rest.
        let complete = !with_media || f.next.is_empty();
        let oldest = items.last().map(|m| m.created_at.clone());
        let mut entries: Vec<Entry> = items.iter().map(|m| Entry::Item(m)).collect();
        entries.extend(
            pages
                .iter()
                .filter(|(_, a)| complete || oldest.as_ref().is_none_or(|o| a.updated_at >= *o))
                .map(|(p, a)| Entry::Page(p, a)),
        );
        entries.sort_by_key(|e| std::cmp::Reverse(media::ms(e.at())));

        let counts = f.counts.clone().unwrap_or_default();
        let total = counts.total + scope_pages.len() as i64;
        let filtered = !f.kind.is_empty() || f.unseen || f.favorites || !f.q.is_empty();
        let project_name = |name: &str| {
            self.projects
                .iter()
                .find(|p| p.name == name)
                .map_or_else(|| name.to_string(), super::sidebar_actions::label_of)
        };

        // The project picker: every project's count, as the daemon has them.
        let everything = f.everything.clone().unwrap_or_default();
        let mut choices = vec![Choice::new(
            "",
            format!("{} ({})", t("shell.allMedia.allProjects"), everything.total),
        )];
        choices.extend(everything.projects.iter().map(|p| {
            Choice::new(
                p.project.clone(),
                format!("{} ({})", project_name(&p.project), p.count),
            )
        }));
        let view = cx.entity().downgrade();
        let pick: controls::Pick<str> = Rc::new(move |v: &str, _, cx| {
            let v = v.to_string();
            view.update(cx, |this, cx| {
                this.side.foot.project = v;
                this.side.foot.agent.clear();
                this.load_all_media(false, cx);
            })
            .ok();
        });

        // The agent picker, once a project is picked: its agents' counts,
        // and the one picked even when it has none (MediaView.tsx's).
        let agent_choices = (!f.project.is_empty()).then(|| {
            let mut choices = vec![Choice::new("", t("project.media.allAgents"))];
            if !f.agent.is_empty() && !counts.agents.iter().any(|a| a.name == f.agent) {
                choices.push(Choice::new(f.agent.clone(), format!("{} (0)", f.agent)));
            }
            choices.extend(counts.agents.iter().map(|a| {
                let mut label = if a.title.is_empty() {
                    a.name.clone()
                } else {
                    format!("{} · {}", a.name, a.title)
                };
                if a.gone {
                    label = format!("{label} {}", t("project.media.removed"));
                }
                Choice::new(a.name.clone(), format!("{label} ({})", a.count))
            }));
            choices
        });
        let view = cx.entity().downgrade();
        let pick_agent: controls::Pick<str> = Rc::new(move |v: &str, _, cx| {
            let v = v.to_string();
            view.update(cx, |this, cx| {
                this.side.foot.agent = v;
                this.load_all_media(false, cx);
            })
            .ok();
        });

        // The kind chips: screenshots and recordings always, the rest when
        // there are some.
        let mut chips: Vec<AnyElement> = vec![
            filter_chip(
                "media-kind-all",
                None,
                t("project.media.everything"),
                total,
                f.kind.is_empty(),
            )
            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                this.side.foot.kind.clear();
                this.load_all_media(false, cx);
            }))
            .into_any_element(),
        ];
        for (kind, icon) in KINDS {
            let n = counts.kinds.get(*kind).copied().unwrap_or(0);
            if !(*kind == "screenshot" || *kind == "recording" || n > 0 || f.kind == *kind) {
                continue;
            }
            let k = kind.to_string();
            chips.push(
                filter_chip(
                    SharedString::from(format!("media-kind-{kind}")),
                    Some(Glyph::Icon(icon)),
                    t(&format!("agent.media.kind.{kind}")),
                    n,
                    f.kind == *kind,
                )
                .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                    let f = &mut this.side.foot;
                    f.kind = if f.kind == k {
                        String::new()
                    } else {
                        k.clone()
                    };
                    this.load_all_media(false, cx);
                }))
                .into_any_element(),
            );
        }
        if !scope_pages.is_empty() || f.kind == PAGE_KIND {
            chips.push(
                filter_chip(
                    "media-kind-page",
                    Some(Glyph::Page),
                    t("media.view.pages"),
                    scope_pages.len() as i64,
                    f.kind == PAGE_KIND,
                )
                .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                    let f = &mut this.side.foot;
                    f.kind = if f.kind == PAGE_KIND {
                        String::new()
                    } else {
                        PAGE_KIND.into()
                    };
                    f.favorites = false;
                    this.load_all_media(false, cx);
                }))
                .into_any_element(),
            );
        }
        chips.push(
            div()
                .mx_1()
                .h_4()
                .w(px(1.))
                .bg(theme::line())
                .into_any_element(),
        );
        chips.push(
            filter_chip(
                "media-unseen",
                Some(Glyph::Dot),
                t("shell.allMedia.unseen"),
                counts.unseen,
                f.unseen,
            )
            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                this.side.foot.unseen = !this.side.foot.unseen;
                this.load_all_media(false, cx);
            }))
            .into_any_element(),
        );
        chips.push(
            filter_chip(
                "media-fav",
                Some(Glyph::Icon("star")),
                t("shell.allMedia.favorites"),
                counts.favorites,
                f.favorites,
            )
            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                let f = &mut this.side.foot;
                f.favorites = !f.favorites;
                if f.kind == PAGE_KIND {
                    f.kind.clear();
                }
                this.load_all_media(false, cx);
            }))
            .into_any_element(),
        );
        if counts.bytes > 0 {
            chips.push(
                div()
                    .pl_1p5()
                    .text_size(theme::text::CAPTION)
                    .font_features(theme::tabular_features())
                    .text_color(theme::subtle())
                    .child(human_bytes(counts.bytes))
                    .into_any_element(),
            );
        }

        // The header: border-b border-line px-8 pt-6 pb-2.5, gap-2.
        let header = v_flex()
            .flex_none()
            .gap_2()
            .px_8()
            .pt_6()
            .pb_2p5()
            .border_b_1()
            .border_color(theme::line())
            .child(
                v_flex()
                    .child(
                        div()
                            .text_size(theme::text::DISPLAY)
                            .line_height(px(30.))
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::title())
                            .child(t("shell.nav.media")),
                    )
                    .child(
                        div()
                            .mt_0p5()
                            .text_size(theme::text::UI)
                            // 19.5 in Electron, which puts what's under it on
                            // half pixels that its text rounds down onto.
                            .line_height(px(20.))
                            .text_color(theme::muted())
                            .child(t("media.view.subtitle")),
                    ),
            )
            .child(
                h_flex()
                    .flex_wrap()
                    .gap_2()
                    .child(div().w(px(224.)).child(controls::field_select(
                        "media-project",
                        choices,
                        &f.project,
                        false,
                        pick,
                        Anchor::TopLeft,
                        None,
                    )))
                    // w-60
                    .when_some(agent_choices, |el, choices| {
                        el.child(div().w(px(240.)).child(controls::field_select(
                            "media-agent",
                            choices,
                            &f.agent,
                            false,
                            pick_agent,
                            Anchor::TopLeft,
                            None,
                        )))
                    })
                    .when_some(f.search.as_ref().filter(|_| total > 0), |el, search| {
                        el.child(media_search(
                            search,
                            f.q.is_empty() && search.read(cx).value().is_empty(),
                        ))
                    }),
            )
            .child(h_flex().flex_wrap().gap_1().children(chips));

        // The body: overflow-y-auto p-4 md:px-8, the days gap-5.
        let gutter = f.scroll.max_offset().y > px(0.5);
        let main = controls::viewport().width
            - px(if controls::viewport().width >= px(1024.) {
                SIDEBAR
            } else {
                0.
            });
        let inner =
            (main - px(64.) - if gutter { controls::SCROLLBAR } else { px(0.) }).max(px(240.));
        // grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3
        let cols = (((inner + px(12.)) / px(252.)).floor() as u16).max(1);
        let col_w = (inner - px(12.) * (cols as f32 - 1.)) / cols as f32;
        let now = chrono::Local::now().date_naive();
        let mut days: Vec<(String, Vec<&Entry>)> = Vec::new();
        for e in &entries {
            let day = media_day(e.at(), now);
            match days.last_mut() {
                Some((d, list)) if *d == day => list.push(e),
                _ => days.push((day, vec![e])),
            }
        }
        let body: AnyElement =
            if f.loaded && total == 0 && !filtered {
                crate::motion::enter(
                    v_flex()
                        .py_12()
                        .items_center()
                        .gap_2()
                        .child(
                            crate::icons::ic("images")
                                .size(px(24.))
                                .text_color(theme::faint()),
                        )
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
                    "media-empty",
                    0,
                )
                .into_any_element()
            } else if !f.loaded && entries.is_empty() {
                // Six 4:3 tiles until the first page arrives.
                div()
                    .grid()
                    .grid_cols(cols)
                    .gap_3()
                    .children((0..6).map(|i| {
                        div()
                            .rounded_xl()
                            .overflow_hidden()
                            .child(crate::motion::skeleton(
                                format!("media-skel-{i}"),
                                col_w,
                                col_w * 0.75,
                            ))
                    }))
                    .into_any_element()
            } else if f.loaded && entries.is_empty() {
                v_flex()
                    .py_12()
                    .items_center()
                    .gap_2()
                    .text_size(theme::text::UI)
                    .text_color(theme::subtle())
                    .child(t("agent.mediaTab.noMatch"))
                    .into_any_element()
            } else {
                v_flex()
                    .gap_5()
                    .children(days.into_iter().map(|(day, list)| {
                        let n = list.len();
                        v_flex()
                            .gap_2p5()
                            .child(
                                h_flex()
                                    .items_baseline()
                                    .gap_2()
                                    .px_1()
                                    .text_size(theme::text::UI)
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::secondary())
                                    .child(day)
                                    .child(
                                        div()
                                            .text_size(theme::text::CAPTION)
                                            .font_weight(FontWeight::NORMAL)
                                            .text_color(theme::subtle())
                                            .child(n.to_string()),
                                    ),
                            )
                            .child(div().grid().grid_cols(cols).gap_3().children(
                                list.into_iter().map(|e| match e {
                                    // Proof that has just come in slides in.
                                    Entry::Item(m) => crate::motion::arrive(
                                        div().child(self.media_card(m, col_w, &project_name, cx)),
                                        "media-cards",
                                        &m.id,
                                    ),
                                    Entry::Page(p, a) => page_card(
                                        p,
                                        a,
                                        col_w,
                                        if f.project.is_empty() {
                                            format!(
                                                "{} · {}",
                                                project_name(p),
                                                self.agent_label(&a.agent)
                                            )
                                        } else {
                                            self.agent_label(&a.agent)
                                        },
                                    ),
                                }),
                            ))
                    }))
                    .when(!f.next.is_empty(), |el| {
                        el.child(h_flex().justify_center().child(controls::button(
                            "media-more",
                            t("common.more"),
                            Btn::Ghost,
                            false,
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.load_all_media(true, cx)
                            }),
                        )))
                    })
                    .into_any_element()
            };
        v_flex()
            .size_full()
            .child(header)
            .child(
                div()
                    .relative()
                    .flex_1()
                    .min_h_0()
                    .child(
                        div()
                            .id("media-page")
                            .size_full()
                            .overflow_y_scroll()
                            .track_scroll(&f.scroll)
                            .when(gutter, |el| el.pr(controls::SCROLLBAR))
                            .child(div().px_8().py_4().child(body)),
                    )
                    .child(controls::scrollbar(&f.scroll, gutter)),
            )
            .into_any_element()
    }

    /// thumb_for is a card's picture made for its size, `w`×`h` logical
    /// pixels: made on a thread the first time (or at a new size), the last
    /// one made shown meanwhile.
    fn thumb_for(
        &self,
        id: &str,
        picture: Picture,
        w: Pixels,
        h: Pixels,
        cx: &mut Context<Self>,
    ) -> Option<std::sync::Arc<RenderImage>> {
        let scale = controls::scale();
        let (w, h) = (
            (f32::from(w) * scale).round() as u32,
            (f32::from(h) * scale).round() as u32,
        );
        let f = &self.side.foot;
        let had = f.thumbs.borrow().get(id).cloned();
        if let Some((tw, th, img)) = &had
            && (*tw, *th) == (w, h)
        {
            return Some(img.clone());
        }
        let key = (id.to_string(), w, h);
        if w > 0 && h > 0 && f.making.borrow_mut().insert(key.clone()) {
            let (tx, rx) = tokio::sync::oneshot::channel();
            std::thread::spawn(move || {
                let _ = tx.send(fill(picture, w, h));
            });
            cx.spawn(async move |this, cx| {
                let made = rx.await.ok().flatten();
                this.update(cx, |this, cx| {
                    let f = &mut this.side.foot;
                    f.making.borrow_mut().remove(&key);
                    if let Some(made) = made {
                        let image =
                            std::sync::Arc::new(RenderImage::new(vec![image::Frame::new(made)]));
                        f.thumbs.borrow_mut().insert(key.0, (w, h, image));
                        cx.notify();
                    }
                })
                .ok();
            })
            .detach();
        }
        had.map(|(_, _, img)| img)
    }

    /// agent_label is useAgentLabel: an agent by its title, else its name.
    fn agent_label(&self, r#ref: &str) -> String {
        let name = r#ref.split('/').nth(1).unwrap_or(r#ref);
        self.fleet
            .iter()
            .find(|a| a.r#ref == r#ref)
            .map(|a| a.title.clone())
            .filter(|t| !t.is_empty())
            // Or as its media name it, for an agent the fleet doesn't list.
            .or_else(|| {
                self.side
                    .foot
                    .media
                    .iter()
                    .find(|m| m.agent == r#ref && !m.agent_title.is_empty())
                    .map(|m| m.agent_title.clone())
            })
            .unwrap_or_else(|| name.to_string())
    }

    /// media_card is MediaParts.tsx's MediaCard: its thumbnail under its
    /// kind and whose it is, its name, who kept it, when and how big.
    fn media_card(
        &self,
        m: &MediaItem,
        width: Pixels,
        project_name: &dyn Fn(&str) -> String,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let f = &self.side.foot;
        let item = m.clone();
        let file = f
            .files
            .get(&m.id)
            .filter(|p| !p.as_os_str().is_empty())
            .cloned();
        // label: project · agent (MediaView.tsx), the agent's title too in
        // one project.
        let who = if !m.agent_title.is_empty() && !f.project.is_empty() {
            format!("{} · {}", m.agent_name, m.agent_title)
        } else {
            m.agent_name.clone()
        };
        let label = if f.project.is_empty() {
            format!(
                "{} · {who}",
                project_name(m.agent.split('/').next().unwrap_or(""))
            )
        } else {
            who
        };
        let label = if m.agent_gone {
            tv("project.media.agentRemoved", &[("label", label.into())])
        } else {
            label
        };
        let (icon, variant) = kind_of(&m.kind);
        // aspect-[16/10] inside the card's 1px border.
        let inner_w = width - px(2.);
        let thumb_h = inner_w * 10. / 16.;
        let light = theme::look() == theme::Look::Light;
        let thumb = div()
            .relative()
            .w_full()
            .h(thumb_h)
            .overflow_hidden()
            .bg(theme::well())
            .child(match m.kind.as_str() {
                "screenshot" => match file.filter(|p| is_image(p)) {
                    Some(p) => {
                        match self.thumb_for(&m.id, Picture::File(p.clone()), inner_w, thumb_h, cx)
                        {
                            Some(t) => img(t).size_full().into_any_element(),
                            None => img(p)
                                .size_full()
                                .object_fit(ObjectFit::Cover)
                                .into_any_element(),
                        }
                    }
                    None => icon_tile("camera"),
                },
                "recording" => div()
                    .size_full()
                    .when_some(
                        f.stills.get(&m.id).cloned().and_then(|still| {
                            self.thumb_for(&m.id, Picture::Frame(still), inner_w, thumb_h, cx)
                        }),
                        |el, still| el.child(img(still).size_full()),
                    )
                    .child(
                        div()
                            .absolute()
                            .inset_0()
                            .flex()
                            .items_center()
                            .justify_center()
                            .child(
                                div()
                                    .size_10()
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .rounded_full()
                                    .bg(theme::media_shade(0.6))
                                    .border_1()
                                    .border_color(theme::line_heavy())
                                    .child(
                                        div().ml_0p5().child(
                                            crate::icons::ic("play")
                                                .size(px(16.))
                                                .text_color(theme::on_media()),
                                        ),
                                    ),
                            ),
                    )
                    .into_any_element(),
                "note" => div()
                    .size_full()
                    .p_3()
                    .pt_10()
                    .bg(gpui::linear_gradient(
                        135.,
                        gpui::linear_color_stop(theme::amber(300).opacity(0.07), 0.),
                        gpui::linear_color_stop(theme::amber(300).opacity(0.), 1.),
                    ))
                    .text_size(theme::text::UI)
                    .line_height(relative(1.625))
                    .text_color(theme::tertiary())
                    .child(div().line_clamp(6).child(m.text.clone()))
                    .into_any_element(),
                "report" => v_flex()
                    .size_full()
                    .items_center()
                    .justify_center()
                    .gap_2()
                    .bg(gpui::linear_gradient(
                        135.,
                        gpui::linear_color_stop(theme::sky(400).opacity(0.08), 0.),
                        gpui::linear_color_stop(theme::sky(400).opacity(0.), 1.),
                    ))
                    .child(
                        crate::icons::ic("file-chart-column")
                            .size(px(32.))
                            .text_color(theme::sky(300).opacity(0.8)),
                    )
                    .into_any_element(),
                _ => icon_tile(icon),
            })
            // The kind and whose, in one row along the top.
            .child(
                h_flex()
                    .absolute()
                    .top_2()
                    .left_2()
                    .right_2()
                    .items_start()
                    .justify_between()
                    .gap_2()
                    .child(
                        ui_badge(variant, Some(icon), noun(&m.kind))
                            .bg(theme::media_shade(0.6))
                            .when(light, |el| el.text_color(theme::on_media())),
                    )
                    .child(
                        div()
                            .min_w_0()
                            .px(px(7.))
                            .py(px(1.))
                            .rounded_full()
                            .border_1()
                            .border_color(theme::line_strong())
                            .bg(theme::media_shade(0.7))
                            .text_size(theme::text::CAPTION)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::on_media().opacity(0.9))
                            .truncate()
                            .child(label),
                    ),
            )
            .when(m.kind == "recording" && m.meta.duration > 0., |el| {
                el.child(
                    div()
                        .absolute()
                        .bottom_2()
                        .right_2()
                        .px_1p5()
                        .py_0p5()
                        .rounded(theme::radius::MD)
                        .bg(theme::media_shade(0.7))
                        .font_family(theme::MONO)
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::on_media())
                        .child(clock(m.meta.duration)),
                )
            });
        let source = if m.source == "agent" {
            ("bot", t("agent.mediaTab.sourceAgent"))
        } else {
            ("user", t("agent.mediaTab.sourceYou"))
        };
        div()
            .relative()
            .child(
                v_flex()
                    .id(SharedString::from(format!("media-{}", m.id)))
                    .w_full()
                    .overflow_hidden()
                    .rounded(theme::radius::XL)
                    .border_1()
                    .border_color(theme::line())
                    .bg(theme::surface_faint())
                    .cursor_pointer()
                    // MediaCard's hover: the border and ground a step up, and
                    // it lifts off the page (its shadow; GPUI can't move it).
                    .hover(|el| {
                        el.border_color(theme::line_vivid())
                            .bg(theme::surface())
                            .shadow(theme::shadow_pop())
                    })
                    .active(|el| el.bg(theme::surface_raised()))
                    .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                        let list = this.side.foot.media.clone();
                        this.open_media(item.clone(), list, String::new(), window, cx)
                    }))
                    .child(thumb)
                    .child(
                        v_flex()
                            .px_3()
                            .py_2p5()
                            .child(
                                div()
                                    .truncate()
                                    .text_size(theme::text::UI)
                                    // Electron's 19.5 lays the line under it on a half
                                    // pixel, which its text then rounds up from.
                                    .line_height(px(19.))
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .child(m.name.clone()),
                            )
                            .child(
                                h_flex()
                                    .mt_1()
                                    .gap_1p5()
                                    .text_size(theme::text::CAPTION)
                                    .text_color(theme::subtle())
                                    .child(crate::icons::ic(source.0).size(px(12.)))
                                    .child(source.1)
                                    .child("·")
                                    .child(time_ago(&m.created_at))
                                    .when(m.size > 0, |el| {
                                        el.child("·").child(human_bytes(m.size))
                                    }),
                            ),
                    ),
            )
            .when(m.favorite, |el| {
                el.child(
                    div()
                        .absolute()
                        .bottom(px(6.))
                        .right(px(6.))
                        .size_7()
                        .flex()
                        .items_center()
                        .justify_center()
                        .text_color(theme::amber(400))
                        .child(crate::icons::ic("star").size(px(16.))),
                )
            })
            .when(m.unseen, |el| {
                el.child(
                    div()
                        .absolute()
                        .top(px(-6.))
                        .right(px(-6.))
                        .px_1p5()
                        .rounded_full()
                        .bg(theme::brand(500))
                        .text_size(theme::text::CAPTION)
                        .font_weight(FontWeight::SEMIBOLD)
                        .text_color(gpui::white())
                        .child(t("shell.allMedia.new")),
                )
            })
            .into_any_element()
    }

    /// render_jobs is JobsView.tsx: the recent jobs beside the picked one's
    /// log, two Cards in a 1fr / 1.1fr grid.
    fn render_jobs(&self, cx: &mut Context<Self>) -> AnyElement {
        let f = &self.side.foot;
        if f.loaded && f.jobs.is_empty() {
            return crate::motion::enter(
                v_flex()
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
                    ),
                "jobs-empty",
                0,
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
                .active(|s| s.bg(theme::surface_raised()))
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
                        .whitespace_nowrap()
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
                .relative()
                // A log is for pasting somewhere: its Copy, in the corner.
                .when(f.job.is_some() && !f.log.is_empty(), |el| {
                    el.child(div().absolute().bottom(px(12.)).right(px(16.)).child(
                        super::controls::copy_button(
                            "job-log-copy",
                            f.log.clone(),
                            t("common.copy"),
                        ),
                    ))
                })
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

/// PAGE_KIND is the chip of Hatch's pages (lib/mediaFilter.ts pageKind).
const PAGE_KIND: &str = "page";
/// The sidebar's width, which the Media grid is laid out beside.
const SIDEBAR: f32 = 288.;

/// KINDS is lib/media.ts's mediaKinds after "Everything": each kind and
/// its icon.
const KINDS: &[(&str, &str)] = &[
    ("screenshot", "camera"),
    ("recording", "video"),
    ("report", "file-chart-column"),
    ("log", "scroll-text"),
    ("note", "sticky-note"),
    ("file", "file"),
];

/// kind_of is a kind's icon and its badge's variant (MediaParts.tsx
/// kindVariant).
fn kind_of(kind: &str) -> (&'static str, Variant) {
    let icon = KINDS
        .iter()
        .find(|(k, _)| *k == kind)
        .map_or("file", |(_, i)| *i);
    let variant = match kind {
        "screenshot" => Variant::Brand,
        "recording" => Variant::Danger,
        "report" => Variant::Info,
        "note" => Variant::Warning,
        _ => Variant::Default,
    };
    (icon, variant)
}

/// noun is one of a kind (lib/media.ts noun(kind, 1)): "screenshot".
fn noun(kind: &str) -> String {
    let key = format!("agent.media.noun.{kind}");
    if agentbox_i18n::has(&key) {
        tv(&key, &[("count", 1i64.into())])
    } else {
        kind.to_string()
    }
}

/// Entry is one card of the Media page: an item, or a project's page.
enum Entry<'a> {
    Item(&'a MediaItem),
    Page(&'a str, &'a agentbox_api::Artifact),
}

impl Entry<'_> {
    fn at(&self) -> &str {
        match self {
            Entry::Item(m) => &m.created_at,
            Entry::Page(_, a) => &a.updated_at,
        }
    }
}

/// media_day is lib/notifications.ts dayLabel: Today, Yesterday, or the
/// day written out ("Thursday, November 5").
fn media_day(at: &str, today: chrono::NaiveDate) -> String {
    use chrono::Datelike as _;
    let Some(d) = crate::dates::local(at).map(|d| d.date_naive()) else {
        return String::new();
    };
    match (today - d).num_days() {
        0 => t("shell.notice.today"),
        1 => t("shell.notice.yesterday"),
        _ => format!(
            "{}, {}",
            crate::dates::weekday(d.weekday().num_days_from_sunday(), true),
            crate::dates::date(d, false)
        ),
    }
}

/// Glyph is what goes before a FilterChip's word.
enum Glyph {
    Icon(&'static str),
    /// PageIcon, the page with its corner folded.
    Page,
    /// The Unseen chip's brand dot.
    Dot,
}

/// filter_chip is MediaParts.tsx's FilterChip: h-7 gap-1.5 rounded-lg
/// px-2.5 text-ui, lit while it's on, its count in tabular figures.
fn filter_chip(
    id: impl Into<ElementId>,
    glyph: Option<Glyph>,
    label: String,
    count: i64,
    on: bool,
) -> Stateful<Div> {
    h_flex()
        .id(id)
        .flex_none()
        .h_7()
        .gap_1p5()
        .px_2p5()
        .rounded_lg()
        .text_size(theme::text::UI)
        .cursor_pointer()
        .map(|el| {
            if on {
                el.bg(theme::surface_strong())
                    .text_color(theme::title())
                    .shadow(vec![BoxShadow {
                        color: gpui::white().opacity(0.06),
                        offset: point(px(0.), px(1.)),
                        blur_radius: px(0.),
                        spread_radius: px(0.),
                        inset: true,
                    }])
            } else {
                el.text_color(theme::muted())
                    .hover(|s| s.bg(theme::surface()).text_color(theme::primary()))
            }
        })
        .map(|el| match glyph {
            Some(Glyph::Icon(i)) => el.child(crate::icons::ic(i).size(px(14.))),
            Some(Glyph::Page) => el.child(page_icon(px(14.))),
            Some(Glyph::Dot) => el.child(div().size_1p5().rounded_full().bg(theme::brand(400))),
            None => el,
        })
        .child(label)
        .child(
            div()
                .font_features(theme::tabular_features())
                .text_color(theme::subtle())
                .child(count.to_string()),
        )
}

/// page_icon is PageThumb.tsx's PageIcon, in the text's colour.
fn page_icon(size: Pixels) -> AnyElement {
    const SVG: &str = r#"<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="black" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M4 1.75h5.25L12.5 5v8.25a1 1 0 0 1-1 1h-7.5a1 1 0 0 1-1-1V2.75a1 1 0 0 1 1-1Z"/><path d="M9 1.75V5.25h3.5M5.5 8.5h5M5.5 11h3.25"/></svg>"#;
    svg_icon(SVG, size)
}

/// svg_icon draws an inline SVG as a mask in the current text colour, as
/// the Lucide icons are drawn (gpui's svg()), from a file kept once made.
fn svg_icon(src: &'static str, size: Pixels) -> AnyElement {
    let path = media::cache_dir().join(format!("icon-{:x}.svg", fnv(src)));
    if !path.exists() {
        let _ = std::fs::create_dir_all(media::cache_dir());
        let _ = std::fs::write(&path, src);
    }
    gpui::svg()
        .external_path(path.to_string_lossy().to_string())
        .flex_none()
        .size(size)
        .text_color(theme::subtle())
        .into_any_element()
}

fn fnv(s: &str) -> u64 {
    s.bytes().fold(0xcbf29ce484222325, |h, b| {
        (h ^ b as u64).wrapping_mul(0x100000001b3)
    })
}

/// media_search is MediaParts.tsx's MediaSearch: w-64, h-7, Search at
/// left-2.5, the words at pl-8, text-ui.
fn media_search(search: &Entity<gpui_component::input::InputState>, empty: bool) -> AnyElement {
    use gpui_component::Sizable as _;
    h_flex()
        .w(px(256.))
        .min_w_0()
        .h_7()
        .pl(px(9.))
        .pr_3()
        .gap(px(9.))
        .rounded_lg()
        .border_1()
        .border_color(theme::line_strong())
        .bg(theme::sunken())
        .text_size(theme::text::UI)
        .child(
            crate::icons::ic("search")
                .size(px(14.))
                .flex_none()
                .text_color(theme::subtle()),
        )
        .child(
            div()
                .relative()
                .flex_1()
                .min_w_0()
                // placeholder:text-faint, which gpui-component's own (muted) isn't.
                .when(empty, |el| {
                    el.child(
                        div()
                            .absolute()
                            .inset_0()
                            .flex()
                            .items_center()
                            .text_color(theme::faint())
                            .truncate()
                            .child(t("agent.mediaTab.searchPlaceholder")),
                    )
                })
                .child(
                    gpui_component::input::Input::new(search)
                        .appearance(false)
                        .with_size(gpui_component::Size::Size(px(13. / 0.875)))
                        .px_0()
                        .cleanable(true),
                ),
        )
        .into_any_element()
}

/// page_card is MediaView.tsx's PageCard: the page's first screen (its
/// title tile here: the snapshot is the Electron main process's), its
/// title, who published it and when. It opens in the browser.
fn page_card(
    project: &str,
    page: &agentbox_api::Artifact,
    width: Pixels,
    label: String,
) -> AnyElement {
    let _ = project;
    let url = page.url.clone();
    let hue = tile_hue(&page.id);
    v_flex()
        .id(SharedString::from(format!("media-page-{}", page.id)))
        .w_full()
        .overflow_hidden()
        .rounded(theme::radius::XL)
        .border_1()
        .border_color(theme::line())
        .bg(theme::surface_faint())
        .cursor_pointer()
        .hover(|el| el.border_color(theme::line_vivid()).bg(theme::surface()))
        .on_click(move |_, _, cx| cx.open_url(&url))
        .child(
            div()
                .relative()
                .w_full()
                .h(width * 10. / 16.)
                .overflow_hidden()
                .border_b_1()
                .border_color(theme::line())
                .bg(gpui::linear_gradient(
                    160.,
                    gpui::linear_color_stop(hsl(hue, 0.7, 0.97), 0.),
                    gpui::linear_color_stop(hsl((hue + 40.) % 360., 0.6, 0.93), 1.),
                ))
                .child(div().w_full().h(relative(0.14)).bg(hsl(hue, 0.65, 0.55)))
                .child(
                    div()
                        .px(relative(0.08))
                        .pt(relative(0.07))
                        .line_clamp(2)
                        .font_weight(FontWeight::SEMIBOLD)
                        .text_size(px(20.))
                        .line_height(relative(1.15))
                        .text_color(hsl(hue, 0.45, 0.22))
                        .child(page.title.clone()),
                )
                .child(
                    div().absolute().top_2().left_2().child(
                        h_flex()
                            .gap_1p5()
                            .px(px(7.))
                            .py(px(1.))
                            .rounded_full()
                            .border_1()
                            .border_color(theme::sky(400).opacity(0.25))
                            .bg(theme::media_shade(0.6))
                            .text_size(theme::text::CAPTION)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(if theme::look() == theme::Look::Light {
                                theme::on_media()
                            } else {
                                theme::sky(300)
                            })
                            .child(page_icon(px(12.)))
                            .child(t("media.view.page")),
                    ),
                ),
        )
        .child(
            v_flex()
                .gap_1()
                .px_3()
                .py_2p5()
                .child(
                    div()
                        .truncate()
                        .text_size(theme::text::UI)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::primary())
                        .child(page.title.clone()),
                )
                .child(
                    h_flex()
                        .min_w_0()
                        .gap_1p5()
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::subtle())
                        .child(div().min_w_0().truncate().child(label))
                        .child("·")
                        .child(
                            div()
                                .flex_none()
                                .font_features(theme::tabular_features())
                                .child(time_ago(&page.updated_at)),
                        ),
                ),
        )
        .into_any_element()
}

/// tile_hue is PageThumb.tsx's tileHue: a page's own hue from its id.
fn tile_hue(id: &str) -> f32 {
    let mut h: u32 = 0;
    for c in id.encode_utf16() {
        h = h.wrapping_mul(31).wrapping_add(c as u32);
    }
    (h % 360) as f32
}

fn hsl(h: f32, s: f32, l: f32) -> Hsla {
    gpui::hsla(h / 360., s, l, 1.)
}

/// icon_tile is MediaParts.tsx's IconTile: the kind's icon, size-8, faint.
fn icon_tile(icon: &str) -> AnyElement {
    div()
        .size_full()
        .flex()
        .items_center()
        .justify_center()
        .text_color(theme::faint())
        .child(crate::icons::ic(icon).size(px(32.)))
        .into_any_element()
}

/// is_image is whether a screenshot's file is a picture the card draws.
fn is_image(p: &std::path::Path) -> bool {
    p.extension()
        .and_then(|e| e.to_str())
        .is_some_and(|e| ["png", "jpg", "jpeg", "gif", "webp"].contains(&e.to_lowercase().as_str()))
}

/// clock is lib/media.ts's: seconds as m:ss.
fn clock(seconds: f64) -> String {
    let s = seconds.max(0.).round() as i64;
    format!("{}:{:02}", s / 60, s % 60)
}

/// human_bytes is utils.ts's humanBytes: 512 B, 152.0 KiB, 2.9 MiB.
fn human_bytes(n: i64) -> String {
    let units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let (mut n, mut i) = (n as f64, 0);
    while n >= 1024. && i < units.len() - 1 {
        n /= 1024.;
        i += 1;
    }
    if i == 0 {
        format!("{n} {}", units[i])
    } else {
        format!("{n:.1} {}", units[i])
    }
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
    Brand,
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
        Variant::Brand => (
            theme::brand(300),
            theme::brand(500).opacity(0.15),
            theme::brand(400).opacity(0.3),
        ),
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
        .when_some(icon, |el, i| {
            el.child(crate::icons::icon(i, crate::icons::ic(i).size(px(12.))))
        })
        .child(text.into())
}

/// status_badge is state.tsx's JobStatusBadge.
pub(super) fn status_badge(status: &str) -> AnyElement {
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
    } else if ms >= 86_400_000 {
        // Days and hours for one left running: not "2884m 5s".
        format!("{}d {}h", ms / 86_400_000, ms % 86_400_000 / 3_600_000)
    } else if ms >= 3_600_000 {
        format!("{}h {}m", ms / 3_600_000, ms % 3_600_000 / 60_000)
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
