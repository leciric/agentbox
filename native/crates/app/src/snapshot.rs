//! ```html preview blocks where a child webview can't be made: on Linux when
//! the GPUI window is a Wayland one (wry makes child webviews on X11 only), or
//! when making one failed. The page is laid out by WebKitGTK in an offscreen
//! window as wide as its block, measured once it has loaded (webview.rs's
//! MEASURE), laid out again as tall as it is, and snapshotted whole; the
//! block shows that picture.
//!
//! It is a still: nothing to click, hover or animate. In the chat the block
//! is as tall as the page up to webview::frame_max, the picture scrolling
//! inside it past that (the wheel goes on to the chat at either end), as a
//! child webview's page does. The page gets what a child webview's gets
//! (webview::framed: the Content-Security-Policy and the app's CSS
//! variables), an ephemeral context, and no navigation away from it.
//!
//! Memory: each WebKit view is a web process of its own, some 95 MB. A page
//! holds a view only while it is pictured: at most POOL views lay pages out
//! at once, taking the waiting pages in turn, and each is destroyed (its
//! process with it) as soon as no page waits. What a page keeps is its
//! picture, and those are kept up to CACHE_BYTES, the least recently drawn
//! let go first and pictured again if they are drawn again.
//!
//! GTK is started and pumped from GPUI's loop by webview::init; every call
//! here is on that same (main) thread.
use std::cell::{Cell, RefCell};
use std::collections::{HashMap, VecDeque};
use std::sync::Arc;
use std::time::{Duration, Instant};

use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gtk::prelude::*;
use javascriptcore::ValueExt as _;
use webkit2gtk::{
    LoadEvent, NavigationPolicyDecision, PolicyDecisionExt, PolicyDecisionType, SnapshotOptions,
    SnapshotRegion, URIRequestExt, WebContext, WebView, WebViewExt,
};

/// The viewport's height a page is first laid out at, before it is measured.
const VIEWPORT: f32 = 260.;
/// The tallest picture taken, in device pixels (a texture's limit, and some
/// 45 MB at 1400 wide): a page taller is pictured down to here.
const CAPTURE_MAX: f32 = 8192.;
/// How long after load-finished the page is measured, for scripts that draw
/// on the next frame or a short timer; and how long after it is laid out at
/// its height it is pictured.
const SETTLE: Duration = Duration::from_millis(200);
const RELAYOUT: Duration = Duration::from_millis(120);
/// The longest a page may hold a view: one not pictured by then (its load
/// hung) shows its source.
const JOB_TIMEOUT: Duration = Duration::from_secs(15);
/// The WebKit views alive at once, each a web process.
const POOL: usize = 4;
/// The bytes of pictures kept. A picture drawn within IN_VIEW is on screen
/// and kept whatever the total.
const CACHE_BYTES: usize = 96 << 20;
const IN_VIEW: Duration = Duration::from_secs(2);
/// The pages remembered (their HTML, height and scroll), past which those
/// drawn longest ago are forgotten.
const MAX_PAGES: usize = 256;

thread_local! {
    static PAGES: RefCell<HashMap<String, Page>> = RefCell::new(HashMap::new());
    /// The views, by slot; a slot is empty once its view is destroyed.
    static VIEWS: RefCell<Vec<Option<Renderer>>> = const { RefCell::new(Vec::new()) };
    /// The pages waiting for a view, first come first served.
    static QUEUE: RefCell<VecDeque<String>> = const { RefCell::new(VecDeque::new()) };
    static CONTEXT: WebContext = WebContext::new_ephemeral();
    /// Set when a picture is ready, so the pump redraws the windows.
    static DIRTY: Cell<bool> = const { Cell::new(false) };
    /// Pictures let go, for the pump to take out of the windows' atlases too.
    static DROPPED: RefCell<Vec<Arc<RenderImage>>> = const { RefCell::new(Vec::new()) };
    static JOBS: Cell<u64> = const { Cell::new(0) };
}

/// A width in device pixels and the scale it was laid out at.
type Size = (i32, f32);

struct Page {
    html: String,
    /// The size the block wants, and the one queued or being pictured.
    want: Size,
    busy: Option<Size>,
    /// The picture, and the scale it was taken at.
    image: Option<(Arc<RenderImage>, f32)>,
    failed: bool,
    /// How far the picture is scrolled inside its block, in pixels.
    scroll: f32,
    /// When its block was last drawn.
    drawn: Instant,
}

impl Page {
    fn shown(&self) -> Option<Size> {
        self.image
            .as_ref()
            .map(|(img, scale)| (img.size(0).width.0, *scale))
    }
}

/// A WebKit view in an offscreen window, laying out one page at a time.
struct Renderer {
    window: gtk::OffscreenWindow,
    view: WebView,
    /// The page it lays out now and that job's number, which its timers and
    /// callbacks check, so one left from a job before does nothing.
    job: Option<(String, u64)>,
}

/// wanted says whether previews in this window are drawn here rather than in
/// a child webview: GTK is up and the window isn't X11's, or child webviews
/// failed.
pub fn wanted(window: &Window) -> bool {
    if !crate::webview::gtk_ready() {
        return false;
    }
    if !crate::webview::available() {
        return true;
    }
    use raw_window_handle::{HasWindowHandle, RawWindowHandle};
    !matches!(
        HasWindowHandle::window_handle(window).map(|h| h.as_raw()),
        Ok(RawWindowHandle::Xlib(_) | RawWindowHandle::Xcb(_))
    )
}

/// failed is a page WebKit couldn't load or picture: its block shows the source.
pub fn failed(id: &str) -> bool {
    PAGES.with_borrow(|p| p.get(id).is_some_and(|p| p.failed))
}

/// made is whether the page `id` is known here already.
pub fn made(id: &str) -> bool {
    PAGES.with_borrow(|p| p.contains_key(id))
}

/// take_dirty is whether a picture (or a page's HTML) came in since it was
/// last asked.
pub fn take_dirty() -> bool {
    DIRTY.with(|d| d.replace(false)) | FETCHED.swap(false, std::sync::atomic::Ordering::Relaxed)
}

/// take_dropped is the pictures let go since it was last asked, for the
/// windows to drop from their atlases (App::drop_image).
pub fn take_dropped() -> Vec<Arc<RenderImage>> {
    DROPPED.take()
}

/// Daemon pages' HTML read so far, by path (None while it is on its way).
static HTML: std::sync::Mutex<Option<HashMap<String, Option<String>>>> =
    std::sync::Mutex::new(None);
static FETCHED: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

/// page_snapshot is a daemon page (a Hatch page's HTML, …/artifacts/{id}/page)
/// as a picture, where no child webview can show it live: its HTML read once
/// from the daemon, then pictured as a preview is.
pub fn page_snapshot(
    id: impl Into<String>,
    path: &str,
    client: agentbox_api::Client,
) -> AnyElement {
    let html = {
        let mut cache = HTML.lock().unwrap();
        let cache = cache.get_or_insert_with(HashMap::new);
        match cache.get(path) {
            Some(h) => h.clone(),
            None => {
                cache.insert(path.to_string(), None);
                let path = path.to_string();
                client.clone().spawn(async move {
                    let text = match client.fetch(&path).await {
                        Ok((200, _, body)) => String::from_utf8_lossy(&body).into_owned(),
                        _ => String::new(),
                    };
                    HTML.lock()
                        .unwrap()
                        .get_or_insert_with(HashMap::new)
                        .insert(path, Some(text));
                    FETCHED.store(true, std::sync::atomic::Ordering::Relaxed);
                });
                None
            }
        }
    };
    match html {
        Some(h) if !h.is_empty() => html_snapshot(id, h, None),
        _ => div().into_any_element(),
    }
}

/// html_snapshot is a block showing `html` as a picture, as tall as the page
/// (as it last measured; FRAME_DEFAULT until it has). Within `max`, past which
/// the picture scrolls inside the block; without one (a page open large, a
/// Hatch page, whose box scrolls) it is the whole page.
pub fn html_snapshot(
    id: impl Into<String>,
    html: impl Into<String>,
    max: Option<Pixels>,
) -> AnyElement {
    let id = id.into();
    let html = html.into();
    let pictured = PAGES.with_borrow(|p| {
        p.get(&id)
            .and_then(|p| p.image.as_ref())
            .map(|(img, scale)| img.size(0).height.0 as f32 / scale)
    });
    let height = match max {
        Some(max) => crate::webview::frame_height(&id, max),
        None => px(pictured
            .or_else(|| crate::webview::height(&id))
            .unwrap_or(VIEWPORT)
            .max(1.)),
    };
    let key = id.clone();
    div()
        .relative()
        .w_full()
        .h(height)
        .when(pictured.is_none(), |el| {
            el.child(crate::webview::placeholder(&id, height))
        })
        .child(
            canvas(
                |bounds, window, _| window.insert_hitbox(bounds, HitboxBehavior::Normal),
                move |bounds, hitbox, window, _| paint(&key, &html, bounds, hitbox, window),
            )
            .absolute()
            .inset_0(),
        )
        .into_any_element()
}

/// paint asks for the page pictured at the block's width (again when that
/// changed, or its picture was let go) and draws the picture, scrolled; a
/// wheel over it scrolls it while it can go further that way, and the chat
/// otherwise.
fn paint(id: &str, html: &str, bounds: Bounds<Pixels>, hitbox: Hitbox, window: &mut Window) {
    let scale = window.scale_factor();
    let width = (f32::from(bounds.size.width) * scale).round() as i32;
    if width <= 0 {
        return;
    }
    let (shown, wanted) = PAGES.with_borrow_mut(|pages| {
        if !pages.contains_key(id) && pages.len() >= MAX_PAGES {
            forget(pages);
        }
        let page = pages.entry(id.to_string()).or_insert_with(|| Page {
            html: html.to_string(),
            want: (0, 1.),
            busy: None,
            image: None,
            failed: false,
            scroll: 0.,
            drawn: Instant::now(),
        });
        page.want = (width, scale);
        page.drawn = Instant::now();
        let wanted = page.busy.is_none() && !page.failed && page.shown() != Some(page.want);
        if wanted {
            page.busy = Some(page.want);
        }
        let shown = page.image.clone().map(|(image, at)| {
            let tall = image.size(0).height.0 as f32 / at;
            let room = (tall - f32::from(bounds.size.height)).max(0.);
            page.scroll = page.scroll.clamp(0., room);
            (image, at, room, page.scroll)
        });
        (shown, wanted)
    });
    if wanted {
        QUEUE.with_borrow_mut(|q| q.push_back(id.to_string()));
        pump();
    }
    let Some((image, at, room, scroll)) = shown else {
        return;
    };
    let size = image.size(0);
    let picture = Bounds::new(
        point(bounds.origin.x, bounds.origin.y - px(scroll)),
        gpui::size(px(size.width.0 as f32 / at), px(size.height.0 as f32 / at)),
    );
    let _ = window.paint_image(bounds, picture, Corners::default(), image, 0, false);
    if room <= 0. {
        return;
    }
    crate::app::controls::paint_scrollbar(
        bounds,
        bounds.right() - crate::app::controls::SCROLLBAR,
        px(room),
        px(scroll),
        true,
        window,
    );
    let id = id.to_string();
    window.on_mouse_event(move |e: &ScrollEvent, phase, window, cx| {
        if phase != DispatchPhase::Bubble || !hitbox.should_handle_scroll(window) {
            return;
        }
        let dy = f32::from(e.delta.pixel_delta(px(20.)).y);
        let moved = PAGES.with_borrow_mut(|pages| {
            let Some(p) = pages.get_mut(&id) else {
                return false;
            };
            let was = p.scroll;
            p.scroll = (was - dy).clamp(0., room);
            p.scroll != was
        });
        // At either end the wheel goes on to the chat (the list scrolls
        // after its items have had it).
        if moved {
            cx.stop_propagation();
            window.refresh();
        }
    });
}

/// bytes is what a picture holds.
fn bytes(image: &RenderImage) -> usize {
    let s = image.size(0);
    s.width.0.max(0) as usize * s.height.0.max(0) as usize * 4
}

/// let_go keeps the pictures within CACHE_BYTES, letting go of those drawn
/// longest ago first; never one drawn within IN_VIEW, which is on screen.
fn let_go(pages: &mut HashMap<String, Page>) {
    let mut total: usize = pages
        .values()
        .filter_map(|p| p.image.as_ref())
        .map(|(i, _)| bytes(i))
        .sum();
    if total <= CACHE_BYTES {
        return;
    }
    let now = Instant::now();
    let mut idle: Vec<(Instant, String)> = pages
        .iter()
        .filter(|(_, p)| p.image.is_some() && now.duration_since(p.drawn) > IN_VIEW)
        .map(|(id, p)| (p.drawn, id.clone()))
        .collect();
    idle.sort();
    for (_, id) in idle {
        if total <= CACHE_BYTES {
            break;
        }
        if let Some((image, _)) = pages.get_mut(&id).and_then(|p| p.image.take()) {
            total -= bytes(&image);
            DROPPED.with_borrow_mut(|d| d.push(image));
        }
    }
}

/// forget drops the quarter of the pages drawn longest ago that aren't being
/// pictured; one drawn again is pictured again.
fn forget(pages: &mut HashMap<String, Page>) {
    let mut old: Vec<(Instant, String)> = pages
        .iter()
        .filter(|(_, p)| p.busy.is_none())
        .map(|(id, p)| (p.drawn, id.clone()))
        .collect();
    old.sort();
    for (_, id) in old.into_iter().take(MAX_PAGES / 4) {
        if let Some((image, _)) = pages.remove(&id).and_then(|p| p.image) {
            DROPPED.with_borrow_mut(|d| d.push(image));
        }
    }
}

impl Renderer {
    /// new is a view for the slot `slot`: offline, no navigation away, and
    /// its load's end measuring the page it lays out.
    fn new(slot: usize) -> Renderer {
        let view = CONTEXT.with(WebView::with_context);
        view.connect_decide_policy(|_, decision, kind| {
            if kind != PolicyDecisionType::NavigationAction {
                return false;
            }
            let uri = decision
                .downcast_ref::<NavigationPolicyDecision>()
                .and_then(|d| {
                    use webkit2gtk::NavigationPolicyDecisionExt as _;
                    d.navigation_action()
                })
                .and_then(|a| a.request())
                .and_then(|r| r.uri())
                .unwrap_or_default();
            if uri == "about:blank" || uri.starts_with("data:") {
                return false;
            }
            decision.ignore();
            true
        });
        view.connect_load_changed(move |_, event| {
            if event == LoadEvent::Finished
                && let Some(n) = job_of(slot)
            {
                gtk::glib::timeout_add_local_once(SETTLE, move || measure(slot, n));
            }
        });
        view.connect_load_failed(move |_, _, _, _| {
            if let Some(n) = job_of(slot) {
                done(slot, n, None);
            }
            false
        });
        let window = gtk::OffscreenWindow::new();
        window.add(&view);
        window.show_all();
        Renderer {
            window,
            view,
            job: None,
        }
    }

    fn resize(&self, width: i32, height: i32) {
        self.view.set_size_request(width, height);
        self.window.resize(width, height);
    }
}

/// job_of is the number of the job the slot's view is on.
fn job_of(slot: usize) -> Option<u64> {
    VIEWS.with_borrow(|v| v.get(slot)?.as_ref()?.job.as_ref().map(|(_, n)| *n))
}

/// on runs `f` on the slot's view while it is still on job `n`.
fn on<R>(slot: usize, n: u64, f: impl FnOnce(&Renderer, &str) -> R) -> Option<R> {
    VIEWS.with_borrow(|v| {
        let r = v.get(slot)?.as_ref()?;
        let (id, at) = r.job.as_ref()?;
        (*at == n).then(|| f(r, id))
    })
}

/// pump hands the waiting pages to the views: an idle one, or a new one
/// while there are fewer than POOL.
fn pump() {
    loop {
        let Some(id) = QUEUE.with_borrow_mut(|q| q.pop_front()) else {
            return;
        };
        // Forgotten while it waited, or no longer wanting a picture.
        let Some((html, size)) = PAGES.with_borrow(|p| {
            let p = p.get(&id)?;
            Some((p.html.clone(), p.busy?))
        }) else {
            continue;
        };
        let Some(slot) = free_slot() else {
            QUEUE.with_borrow_mut(|q| q.push_front(id));
            return;
        };
        start(slot, id, &html, size);
    }
}

/// free_slot is a view with no page, made if there are fewer than POOL.
fn free_slot() -> Option<usize> {
    let (idle, empty, live) = VIEWS.with_borrow(|v| {
        (
            v.iter()
                .position(|r| r.as_ref().is_some_and(|r| r.job.is_none())),
            v.iter().position(Option::is_none),
            v.iter().flatten().count(),
        )
    });
    if idle.is_some() {
        return idle;
    }
    if live >= POOL {
        return None;
    }
    let slot = empty.unwrap_or_else(|| VIEWS.with_borrow(Vec::len));
    let made = Renderer::new(slot);
    VIEWS.with_borrow_mut(|v| {
        if slot == v.len() {
            v.push(Some(made));
        } else {
            v[slot] = Some(made);
        }
    });
    Some(slot)
}

/// start lays the page `id` out on the slot's view at `size` (as tall as it
/// last measured) and loads it; measure() and picture() follow.
fn start(slot: usize, id: String, html: &str, (width, scale): Size) {
    let n = JOBS.with(|j| j.replace(j.get() + 1) + 1);
    let tall = crate::webview::height(&id).unwrap_or(VIEWPORT);
    let view = VIEWS.with_borrow_mut(|v| {
        let r = v[slot].as_mut()?;
        r.job = Some((id, n));
        r.resize(width, device_height(tall, scale));
        r.view.set_zoom_level(scale as f64);
        Some(r.view.clone())
    });
    if let Some(view) = view {
        view.load_html(&crate::webview::framed(html), None);
        gtk::glib::timeout_add_local_once(JOB_TIMEOUT, move || {
            if job_of(slot) == Some(n) {
                eprintln!("html preview: not pictured in {JOB_TIMEOUT:?}");
                done(slot, n, None);
            }
        });
    }
}

/// device_height is `css` pixels at `scale`, within what may be pictured.
fn device_height(css: f32, scale: f32) -> i32 {
    (css.max(1.) * scale).ceil().min(CAPTURE_MAX) as i32
}

/// measure asks the page on the slot's view its height once it has loaded
/// and settled, lays it out that tall, so nothing of it is past the viewport,
/// and has it pictured once that has been drawn.
fn measure(slot: usize, n: u64) {
    let Some(view) = on(slot, n, |r, _| r.view.clone()) else {
        return;
    };
    // evaluate_javascript, which replaces it, is WebKitGTK 2.40's; this
    // builds for 2.38, where run_javascript isn't deprecated yet.
    #[allow(deprecated)]
    view.run_javascript(
        crate::webview::MEASURE,
        None::<&gtk::gio::Cancellable>,
        move |result| {
            let tall = result
                .ok()
                .and_then(|r| r.js_value())
                .map(|v| v.to_double() as f32)
                .filter(|h| h.is_finite() && *h > 0.);
            let busy = |id: &str| PAGES.with_borrow(|p| p.get(id).and_then(|p| p.busy));
            let relaid = on(slot, n, |r, id| {
                let tall = tall?;
                crate::webview::set_height(id, tall);
                let (width, scale) = busy(id)?;
                let height = device_height(tall, scale);
                if (r.window.allocated_height() - height).abs() <= 1 {
                    return None;
                }
                r.resize(width, height);
                Some(())
            })
            .flatten();
            if relaid.is_some() {
                gtk::glib::timeout_add_local_once(RELAYOUT, move || picture(slot, n));
            } else {
                picture(slot, n);
            }
        },
    );
}

/// picture snapshots the page on the slot's view.
fn picture(slot: usize, n: u64) {
    let Some(view) = on(slot, n, |r, _| r.view.clone()) else {
        return;
    };
    view.snapshot(
        SnapshotRegion::FullDocument,
        SnapshotOptions::NONE,
        None::<&gtk::gio::Cancellable>,
        move |result| {
            let image = result
                .map_err(|e| e.to_string())
                .and_then(|s| to_image(s).ok_or_else(|| "not an image surface".into()));
            if let Err(e) = &image {
                eprintln!("html preview: snapshot: {e}");
            }
            done(slot, n, image.ok());
        },
    );
}

/// done ends the slot's job `n`: its page keeps the picture (None: it shows
/// its source), and is pictured again if its block was resized meanwhile.
/// The view goes on to the next page waiting, or is destroyed.
fn done(slot: usize, n: u64, image: Option<Arc<RenderImage>>) {
    let Some(id) = VIEWS.with_borrow_mut(|v| {
        let r = v.get_mut(slot)?.as_mut()?;
        if r.job.as_ref().is_some_and(|(_, at)| *at == n) {
            r.job.take().map(|(id, _)| id)
        } else {
            None
        }
    }) else {
        return;
    };
    let again = PAGES.with_borrow_mut(|pages| {
        let page = pages.get_mut(&id)?;
        let at = page.busy.take();
        match image {
            Some(image) => {
                let old = page
                    .image
                    .replace((image, at.map_or(1., |(_, s)| s)))
                    .map(|(i, _)| i);
                DROPPED.with_borrow_mut(|d| d.extend(old));
                // The block was resized while this one was pictured.
                let again = page.shown() != Some(page.want);
                if again {
                    page.busy = Some(page.want);
                }
                let_go(pages);
                again.then_some(())
            }
            None => {
                page.failed = true;
                None
            }
        }
    });
    if again.is_some() {
        QUEUE.with_borrow_mut(|q| q.push_back(id));
    }
    DIRTY.with(|d| d.set(true));
    // Out of the view's own callback: the next page, then the views left
    // idle destroyed, and their web processes with them.
    gtk::glib::idle_add_local_once(|| {
        pump();
        let idle: Vec<Renderer> = VIEWS.with_borrow_mut(|v| {
            v.iter_mut()
                .filter(|r| matches!(r, Some(r) if r.job.is_none()))
                .filter_map(Option::take)
                .collect()
        });
        for r in idle {
            // SAFETY: nothing else holds the window; its view goes with it.
            unsafe { r.window.destroy() };
        }
    });
}

/// to_image copies a snapshot (cairo ARGB32: BGRA bytes on little-endian, as
/// GPUI's RenderImage takes them) into a RenderImage.
fn to_image(surface: gtk::cairo::Surface) -> Option<Arc<RenderImage>> {
    let surface = gtk::cairo::ImageSurface::try_from(surface).ok()?;
    // A page taller than CAPTURE_MAX is pictured down to there.
    let (w, h, stride) = (
        surface.width() as usize,
        (surface.height() as usize).min(CAPTURE_MAX as usize),
        surface.stride() as usize,
    );
    if w == 0 || h == 0 {
        return None;
    }
    let mut bytes = Vec::with_capacity(w * h * 4);
    surface.flush();
    surface
        .with_data(|d| {
            for row in d.chunks(stride).take(h) {
                bytes.extend_from_slice(&row[..w * 4]);
            }
        })
        .ok()?;
    let frame = image::RgbaImage::from_raw(w as u32, h as u32, bytes)?;
    Some(Arc::new(RenderImage::new(vec![image::Frame::new(frame)])))
}
