//! ```html preview blocks: each in a real webview (wry: WebKitGTK on Linux,
//! WKWebView on macOS, WebView2 on Windows), a child window of the GPUI
//! window, kept over the block's place in the layout every frame.
//!
//! A child window draws over everything, so a webview is only shown while its
//! block is wholly inside the area it scrolls in; otherwise it is hidden and
//! the block shows what GPUI drew underneath. Pages are offline, as in the
//! Electron app's frame: a Content-Security-Policy allows nothing from the
//! network, and navigating away is refused.
//!
//! On Linux child webviews work on X11 only (wry can't make them on
//! Wayland): elsewhere snapshot.rs shows the page as a picture.
use std::cell::RefCell;
use std::collections::HashMap;
use std::time::{Duration, Instant};

use gpui::prelude::FluentBuilder as _;
use gpui::*;

thread_local! {
    static VIEWS: RefCell<HashMap<String, Placed>> = RefCell::new(HashMap::new());
}

struct Placed {
    view: wry::WebView,
    /// The frame it was last placed in; one not placed in a frame is hidden.
    frame: u64,
    shown: bool,
    bounds: Option<Bounds<Pixels>>,
}

impl Drop for Placed {
    fn drop(&mut self) {
        cut_ipc(&self.view);
    }
}

/// cut_ipc disconnects the message handler wry gave a page with an ipc
/// handler, before the view is dropped. That handler, on the view's user
/// content manager, holds the WebKitWebView it belongs to: the cycle outlived
/// wry's destroy(), so a dropped preview kept its page, its web process and
/// its network process (an ephemeral context each) until the app quit.
#[cfg(target_os = "linux")]
fn cut_ipc(view: &wry::WebView) {
    use gtk::glib::gobject_ffi;
    use gtk::glib::object::ObjectType as _;
    use gtk::glib::prelude::ObjectExt as _;
    use gtk::glib::translate::IntoGlib as _;
    use webkit2gtk::WebViewExt as _;
    use wry::WebViewExtUnix as _;
    let Some(manager) = view.webview().user_content_manager() else {
        return;
    };
    // SAFETY: the manager is alive (held here); the name is a C string; with
    // G_SIGNAL_MATCH_ID only the signal's id is matched, the other arguments
    // are ignored.
    unsafe {
        let signal = gobject_ffi::g_signal_lookup(
            c"script-message-received".as_ptr(),
            manager.type_().into_glib(),
        );
        if signal != 0 {
            gobject_ffi::g_signal_handlers_disconnect_matched(
                manager.as_ptr().cast(),
                gobject_ffi::G_SIGNAL_MATCH_ID,
                signal,
                0,
                std::ptr::null_mut(),
                std::ptr::null_mut(),
                std::ptr::null_mut(),
            );
        }
    }
}

#[cfg(not(target_os = "linux"))]
fn cut_ipc(_: &wry::WebView) {}

thread_local! {
    /// Where webviews may show this frame: the scrolling area they're in.
    static CLIP: std::cell::Cell<Option<Bounds<Pixels>>> = const { std::cell::Cell::new(None) };
}

/// set_clip says where the blocks drawn next are visible.
pub fn set_clip(bounds: Option<Bounds<Pixels>>) {
    CLIP.with(|c| c.set(bounds));
}

/// Set when a webview can't be made here (Wayland, no GTK): previews then
/// show their source.
static UNAVAILABLE: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

/// Set once GTK started: snapshot.rs can render pages offscreen.
static GTK: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

#[cfg(target_os = "linux")]
pub fn gtk_ready() -> bool {
    GTK.load(std::sync::atomic::Ordering::Relaxed)
}

pub fn available() -> bool {
    !UNAVAILABLE.load(std::sync::atomic::Ordering::Relaxed)
}

/// Wheel deltas from pages, in pixels: what a page can't scroll itself
/// scrolls the chat.
static WHEEL: std::sync::Mutex<f32> = std::sync::Mutex::new(0.);

/// MEASURE is a page's height in CSS pixels, as a script's value: the
/// scrolling element's scrollHeight while the page overflows its viewport,
/// and otherwise its content's (lib/rich.ts's bootstrap), as scrollHeight is
/// never less than the viewport and a page shorter than its block would keep
/// the block's height.
pub(crate) const MEASURE: &str = "(() => { const s = document.scrollingElement || document.documentElement, R = document.documentElement, b = document.body; \
return Math.ceil(s.scrollHeight > s.clientHeight + 1 ? s.scrollHeight : Math.max(R.getBoundingClientRect().height, b ? b.getBoundingClientRect().bottom : 0)); })()";

/// The script every page in the chat gets. It tells the app the page's
/// height as it loads and whenever it changes ("h" and MEASURE), and when it
/// scrolled ("s", for its still: see STILLS); a wheel
/// scrolls the page, or what in it scrolls, while that can go further that
/// way, and otherwise goes to the app, which scrolls the chat with it.
fn preview_script() -> String {
    format!(
        "(() => {{ const post = m => window.ipc.postMessage(m); let last = -1;\
const tell = () => {{ const h = {MEASURE}; if (h !== last) {{ last = h; post('h' + h); }} }};\
const ro = new ResizeObserver(tell); ro.observe(document.documentElement);\
addEventListener('DOMContentLoaded', () => {{ document.body && ro.observe(document.body); tell(); }}); addEventListener('load', tell);\
addEventListener('scroll', () => post('s'), {{ capture: true, passive: true }});\
const room = (el, dy) => {{ for (; el; el = el.parentElement) {{ const root = el === document.scrollingElement;\
if (!root) {{ const o = getComputedStyle(el).overflowY; if (o !== 'auto' && o !== 'scroll') continue; }}\
if (el.scrollHeight - el.clientHeight < 1) continue; if (dy < 0 ? el.scrollTop > 0 : el.scrollTop + el.clientHeight < el.scrollHeight - 1) return true; }} return false; }};\
addEventListener('wheel', e => {{ const dy = e.deltaMode === 1 ? e.deltaY * 40 : e.deltaMode === 2 ? e.deltaY * innerHeight : e.deltaY;\
if (e.ctrlKey || room(e.target instanceof Element ? e.target : document.scrollingElement, dy)) return;\
e.preventDefault(); post(String(dy)); }}, {{ passive: false }}); }})();"
    )
}

/// The heights pages reported, by id, in CSS pixels: a block drawn again (a
/// chat scrolled back to, a page made again after it was dropped) is the
/// height it was, not FRAME_DEFAULT first.
static HEIGHTS: std::sync::Mutex<Option<HashMap<String, f32>>> = std::sync::Mutex::new(None);
/// Set when a page's height changed, so the pump redraws the windows.
static RESIZED: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

/// A page's block in the chat: at least FRAME_MIN, FRAME_DEFAULT until the
/// page tells its height, and at most FRAME_VIEWPORT of the window
/// (lib/rich.ts's), and FRAME_CLIP of the chat's view: its webview is shown
/// live only while the block is wholly in view, which a block about as tall
/// as the view hardly ever is.
const FRAME_MIN: f32 = 60.;
const FRAME_DEFAULT: f32 = 160.;
const FRAME_VIEWPORT: f32 = 0.7;
const FRAME_CLIP: f32 = 0.8;

/// set_height records the page `id`'s height, as it reported it.
pub(crate) fn set_height(id: &str, h: f32) {
    if !h.is_finite() || h < 0. {
        return;
    }
    let mut heights = HEIGHTS.lock().unwrap();
    let heights = heights.get_or_insert_with(HashMap::new);
    if heights.get(id) == Some(&h) {
        return;
    }
    if heights.len() > 256 {
        heights.clear();
    }
    heights.insert(id.to_string(), h);
    RESIZED.store(true, std::sync::atomic::Ordering::Relaxed);
}

/// height is the page `id`'s height as it last reported it.
pub(crate) fn height(id: &str) -> Option<f32> {
    HEIGHTS
        .lock()
        .unwrap()
        .as_ref()
        .and_then(|h| h.get(id).copied())
}

/// frame_max is the most a page's block takes of `window`.
pub fn frame_max(window: &Window) -> Pixels {
    let mut max = window.viewport_size().height * FRAME_VIEWPORT;
    if let Some(clip) = CLIP.with(|c| c.get()) {
        max = max.min(clip.size.height * FRAME_CLIP);
    }
    max.max(px(FRAME_MIN))
}

/// frame_height is the page `id`'s block's height: the page's own, within
/// FRAME_MIN and `max`.
pub fn frame_height(id: &str, max: Pixels) -> Pixels {
    px(height(id).unwrap_or(FRAME_DEFAULT).ceil()).clamp(px(FRAME_MIN), max.max(px(FRAME_MIN)))
}

/// placeholder is where a page will be, `height` tall, shimmering while it
/// loads (Electron's skeleton).
pub fn placeholder(id: &str, height: Pixels) -> impl IntoElement {
    div()
        .w_full()
        .h(height)
        .flex()
        .child(crate::motion::skeleton(
            SharedString::from(format!("{id}-loading")),
            relative(1.),
            height,
        ))
}

/// made is whether the page `id` has a webview (or a picture) already.
pub fn made(id: &str) -> bool {
    #[cfg(target_os = "linux")]
    if crate::snapshot::made(id) {
        return true;
    }
    VIEWS.with_borrow(|v| v.contains_key(id))
}

/// When the pump next redraws the windows for a page waiting to be made.
static WAKE: std::sync::Mutex<Option<std::time::Instant>> = std::sync::Mutex::new(None);

/// wake_at has the windows redrawn at `at` (or sooner).
pub fn wake_at(at: std::time::Instant) {
    let mut wake = WAKE.lock().unwrap();
    if wake.is_none_or(|w| at < w) {
        *wake = Some(at);
    }
}

fn take_wake() -> bool {
    let mut wake = WAKE.lock().unwrap();
    if wake.is_some_and(|w| std::time::Instant::now() >= w) {
        *wake = None;
        return true;
    }
    false
}

/// framed is a page as a block (or the page open large) loads it: PRELUDE at
/// the start of its head, wherever the page starts it, so a doctype stays
/// first and the page out of quirks mode, and a fragment gets one
/// (lib/rich.ts injectHead).
pub fn framed(html: &str) -> String {
    let lower = html.to_ascii_lowercase();
    for tag in ["<head", "<html", "<!doctype"] {
        let mut from = 0;
        while let Some(i) = lower[from..].find(tag) {
            let at = from + i;
            from = at + tag.len();
            // A whole tag's name: `<header>` isn't `<head>`.
            let next = lower[from..].chars().next();
            if tag != "<!doctype" && next.is_some_and(|c| c.is_ascii_alphanumeric() || c == '-') {
                continue;
            }
            let Some(end) = lower[from..].find('>') else {
                break;
            };
            let end = from + end + 1;
            return format!("{}{PRELUDE}{}", &html[..end], &html[end..]);
        }
    }
    format!("<!doctype html>{PRELUDE}{html}")
}

thread_local! {
    /// The list the webviews in view belong to, which a page's wheel scrolls.
    static SCROLLER: RefCell<Option<ListState>> = const { RefCell::new(None) };
}

/// set_scroller names the list the next frame's webviews scroll.
pub fn set_scroller(list: ListState) {
    SCROLLER.with_borrow_mut(|s| *s = Some(list));
}

static FRAME: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);

/// Set while a block is open large over the window (large.rs): the chat's
/// webviews and the panel's pages, which would draw over it, hide.
static COVERED: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

/// set_covered says whether something is open over the window's webviews.
pub fn set_covered(on: bool) {
    COVERED.store(on, std::sync::atomic::Ordering::Relaxed);
}

fn covered() -> bool {
    COVERED.load(std::sync::atomic::Ordering::Relaxed)
}

/// The app's palette as the CSS variables the Electron app's previews use.
pub(crate) const PRELUDE: &str = r#"<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; font-src data:">
<style>:root{color-scheme:dark;--foreground:#ededf0;--muted-foreground:#9a9aa3;--border:#26262c;--background:#1a1a1f;--chart-1:#7c8cff;--chart-2:#34d399;--chart-3:#fbbf24;--chart-4:#f87171;--chart-5:#a78bfa;--chart-6:#38bdf8}html,body{margin:0;background:var(--background);color:var(--foreground);font:14px system-ui,sans-serif}</style>"#;

/// init starts GTK (on Linux) and pumps its events from GPUI's loop, and
/// hides webviews whose block wasn't drawn in the last frame.
pub fn init(cx: &mut App) {
    #[cfg(target_os = "linux")]
    // WebKitGTK's DMA-BUF renderer draws nothing without a GPU it can share
    // buffers with (software rendering, VMs); the shared-memory path works.
    for var in [
        "WEBKIT_DISABLE_DMABUF_RENDERER",
        "WEBKIT_DISABLE_COMPOSITING_MODE",
    ] {
        if std::env::var_os(var).is_none() {
            // SAFETY: set before any other thread reads the environment.
            unsafe { std::env::set_var(var, "1") };
        }
    }
    #[cfg(target_os = "linux")]
    if let Err(e) = gtk::init() {
        eprintln!("webviews are off: gtk: {e}");
        UNAVAILABLE.store(true, std::sync::atomic::Ordering::Relaxed);
        return;
    }
    GTK.store(
        cfg!(target_os = "linux"),
        std::sync::atomic::Ordering::Relaxed,
    );
    cx.spawn(async move |cx| {
        let mut ticks = 0u32;
        loop {
            cx.background_executor()
                .timer(Duration::from_millis(16))
                .await;
            // Once a second, also while no frame is drawn: webviews left
            // behind (another chat open, the panel closed) are let go.
            ticks = ticks.wrapping_add(1);
            if ticks.is_multiple_of(60) {
                prune();
            }
            #[cfg(target_os = "linux")]
            while gtk::events_pending() {
                gtk::main_iteration_do(false);
            }
            // Pictures let go leave the windows' atlases (on a software
            // renderer, memory like any other).
            #[cfg(target_os = "linux")]
            {
                let dropped = crate::snapshot::take_dropped();
                if !dropped.is_empty() {
                    cx.update(|cx| {
                        for image in dropped {
                            cx.drop_image(image, None);
                        }
                    });
                }
            }
            #[cfg(target_os = "linux")]
            let dirty = crate::snapshot::take_dirty();
            #[cfg(not(target_os = "linux"))]
            let dirty = false;
            // A page told its height, or one waiting to settle is due.
            let resized = RESIZED.swap(false, std::sync::atomic::Ordering::Relaxed);
            if dirty | resized | take_wake() {
                cx.update(|cx| cx.refresh_windows());
            }
            let commands = std::mem::take(&mut *LARGE_COMMANDS.lock().unwrap());
            if !commands.is_empty() {
                cx.update(|cx| {
                    for c in commands {
                        crate::large::page_command(&c, cx);
                    }
                });
            }
            let dy = std::mem::take(&mut *WHEEL.lock().unwrap());
            if dy != 0. {
                SCROLLER.with_borrow(|s| {
                    if let Some(list) = s {
                        list.scroll_by(px(dy));
                    }
                });
                cx.update(|cx| cx.refresh_windows());
            }
        }
    })
    .detach();
}

/// begin goes before the blocks that may hold webviews, and end after them:
/// both are painted every frame the window draws, so a webview whose block
/// wasn't painted between them (scrolled out of a virtual list, gone) is
/// hidden by end.
pub fn begin() -> impl IntoElement {
    canvas(
        |_, _, _| {},
        |_, _, _, _| {
            FRAME.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
        },
    )
    .absolute()
    .size_0()
}

pub fn end() -> impl IntoElement {
    canvas(
        |_, _, _| {},
        |_, _, _, _| {
            let now = FRAME.load(std::sync::atomic::Ordering::Relaxed);
            VIEWS.with_borrow_mut(|views| {
                for p in views.values_mut() {
                    if p.shown && p.frame != now {
                        let _ = p.view.set_visible(false);
                        p.shown = false;
                    }
                }
                prune_previews(views, now);
            });
            flush();
        },
    )
    .absolute()
    .size_0()
}

/// html_preview is a block that shows `html` in a webview, as tall as the
/// page up to `max`, past which the page scrolls inside it. The webview is
/// made once per page, parked out of view until the page has told its height
/// (the block shimmers until then), and kept as the chat scrolls and draws
/// it again.
pub fn html_preview(id: impl Into<String>, html: impl Into<String>, max: Pixels) -> AnyElement {
    let id = id.into();
    let html = html.into();
    let height = frame_height(&id, max);
    let ready = height_known(&id);
    let pictured = still(&id).is_some();
    div()
        .relative()
        .w_full()
        .h(height)
        .when(!ready && !pictured, |el| el.child(placeholder(&id, height)))
        .child(
            canvas(
                |_, _, _| {},
                move |bounds, _, window, _| {
                    if let Some(image) = still(&id) {
                        let _ =
                            window.paint_image(bounds, bounds, Corners::default(), image, 0, false);
                    }
                    let clip = CLIP
                        .with(|c| c.get())
                        .unwrap_or(window.content_mask().bounds);
                    let inside = !covered()
                        && clip.contains(&bounds.origin)
                        && clip.contains(&bounds.bottom_right().map(|v| v - px(1.)));
                    place(&id, &html, bounds, inside, ready, window);
                },
            )
            .absolute()
            .inset_0(),
        )
        .into_any_element()
}

/// The pages whose webview has loaded and told its height since it was made.
static LOADED: std::sync::Mutex<Option<std::collections::HashSet<String>>> =
    std::sync::Mutex::new(None);

fn loaded(id: &str, on: bool) {
    let mut set = LOADED.lock().unwrap();
    let set = set.get_or_insert_with(Default::default);
    let changed = if on {
        set.insert(id.to_string())
    } else {
        set.remove(id)
    };
    if changed {
        RESIZED.store(true, std::sync::atomic::Ordering::Relaxed);
    }
}

/// height_known is whether the page `id`'s webview has loaded and told its
/// height: until then it is hidden, as it would show blank, then jump.
fn height_known(id: &str) -> bool {
    LOADED
        .lock()
        .unwrap()
        .as_ref()
        .is_some_and(|s| s.contains(id))
}

/// heard is what the page `id` posted: its height ("h" and a number), or a
/// wheel it didn't scroll itself.
fn heard(id: &str, body: &str) {
    if let Some(h) = body.strip_prefix('h') {
        if let Ok(h) = h.parse::<f32>() {
            set_height(id, h);
            loaded(id, true);
            still_soon(id);
        }
    } else if body == "s" {
        still_soon(id);
    } else if let Ok(dy) = body.parse::<f32>() {
        *WHEEL.lock().unwrap() += dy;
    }
}

thread_local! {
    /// The last picture of each page's webview (Linux: WebKitGTK's snapshot
    /// of what it shows), drawn in the block under it. A child window can't
    /// be clipped by the chat, so it shows only while its block is wholly in
    /// view; the rest of the time, the block partly scrolled out, the block
    /// shows this, clipped as the chat clips anything, rather than nothing.
    static STILLS: RefCell<HashMap<String, std::sync::Arc<RenderImage>>> = RefCell::new(HashMap::new());
    /// The still each page is due, by number: the last one asked for wins.
    static STILL_DUE: RefCell<HashMap<String, u64>> = RefCell::new(HashMap::new());
}

/// still_soon has the page `id`'s still taken again once it has held still
/// for a moment: it loaded, changed height or scrolled.
fn still_soon(id: &str) {
    #[cfg(target_os = "linux")]
    {
        let n = STILL_DUE.with_borrow_mut(|due| {
            let n = due.entry(id.to_string()).or_insert(0);
            *n += 1;
            *n
        });
        let id = id.to_string();
        gtk::glib::timeout_add_local_once(Duration::from_millis(150), move || {
            if STILL_DUE.with_borrow(|due| due.get(&id) == Some(&n)) {
                take_still(&id);
            }
        });
    }
    #[cfg(not(target_os = "linux"))]
    let _ = id;
}

/// take_still snapshots what the page `id`'s webview shows, as its still.
#[cfg(target_os = "linux")]
fn take_still(id: &str) {
    use webkit2gtk::WebViewExt as _;
    use wry::WebViewExtUnix as _;
    let Some(view) = VIEWS.with_borrow(|v| v.get(id).map(|p| p.view.webview())) else {
        return;
    };
    let id = id.to_string();
    view.snapshot(
        webkit2gtk::SnapshotRegion::Visible,
        webkit2gtk::SnapshotOptions::NONE,
        None::<&gtk::gio::Cancellable>,
        move |result| {
            let Some(image) = result.ok().and_then(crate::snapshot::to_image) else {
                return;
            };
            // Its webview was dropped meanwhile.
            if !VIEWS.with_borrow(|v| v.contains_key(&id)) {
                return;
            }
            let old = STILLS.with_borrow_mut(|s| s.insert(id, image));
            if let Some(old) = old {
                crate::snapshot::release(old);
            }
            RESIZED.store(true, std::sync::atomic::Ordering::Relaxed);
        },
    );
}

/// drop_still lets go of the page `id`'s still, its webview gone.
fn drop_still(id: &str) {
    STILL_DUE.with_borrow_mut(|due| due.remove(id));
    #[cfg(target_os = "linux")]
    if let Some(old) = STILLS.with_borrow_mut(|s| s.remove(id)) {
        crate::snapshot::release(old);
    }
}

fn still(id: &str) -> Option<std::sync::Arc<RenderImage>> {
    STILLS.with_borrow(|s| s.get(id).cloned())
}

/// Parent is the GPUI window as wry takes it. GPUI's X11 backend hands out an
/// XCB handle and wry wants Xlib's: the window is the same X resource either
/// way, so it is passed on as an Xlib one.
struct Parent<'a>(&'a Window);

impl raw_window_handle::HasWindowHandle for Parent<'_> {
    fn window_handle(
        &self,
    ) -> Result<raw_window_handle::WindowHandle<'_>, raw_window_handle::HandleError> {
        let handle = raw_window_handle::HasWindowHandle::window_handle(self.0)?;
        #[cfg(target_os = "linux")]
        if let raw_window_handle::RawWindowHandle::Xcb(xcb) = handle.as_raw() {
            let xlib = raw_window_handle::XlibWindowHandle::new(xcb.window.get().into());
            // SAFETY: the X window outlives the webview, a child of it.
            return Ok(unsafe { raw_window_handle::WindowHandle::borrow_raw(xlib.into()) });
        }
        Ok(handle)
    }
}

/// flush sends what wry asked of X (mapping, moving the child window): wry
/// calls Xlib on GTK's connection, which buffers it until GTK flushes.
fn flush() {
    #[cfg(target_os = "linux")]
    if let Some(d) = gtk::gdk::Display::default() {
        d.flush();
    }
}

fn rect(b: Bounds<Pixels>) -> wry::Rect {
    wry::Rect {
        position: wry::dpi::LogicalPosition::new(
            f32::from(b.origin.x) as f64,
            f32::from(b.origin.y) as f64,
        )
        .into(),
        size: wry::dpi::LogicalSize::new(
            f32::from(b.size.width) as f64,
            f32::from(b.size.height) as f64,
        )
        .into(),
    }
}

/// parked is where a page loads before it has told its height: as wide and
/// tall as its block, just off the window's top left, where X clips it from
/// view. Shown there, not hidden, as GTK lays out no hidden widget and the
/// page would be measured at a width it won't have.
fn parked(b: Bounds<Pixels>) -> Bounds<Pixels> {
    Bounds::new(
        point(-b.size.width - px(64.), -b.size.height - px(64.)),
        b.size,
    )
}

/// place keeps the page `id`'s webview over its block: there and shown while
/// the block is wholly in view and the page `ready`, parked while it is not
/// ready, hidden otherwise. It is made the first time.
fn place(
    id: &str,
    html: &str,
    bounds: Bounds<Pixels>,
    inside: bool,
    ready: bool,
    window: &mut Window,
) {
    let frame = FRAME.load(std::sync::atomic::Ordering::Relaxed);
    let (at, show) = if ready {
        (bounds, inside)
    } else {
        (parked(bounds), true)
    };
    VIEWS.with_borrow_mut(|views| {
        if !views.contains_key(id) {
            loaded(id, false);
            let (key, on_load) = (id.to_string(), id.to_string());
            let built = wry::WebViewBuilder::new()
                .with_html(framed(html))
                .with_bounds(rect(at))
                .with_visible(show)
                .with_incognito(true)
                .with_navigation_handler(|url| url == "about:blank" || url.starts_with("data:"))
                .with_initialization_script(preview_script())
                .with_ipc_handler(move |req| heard(&key, req.body()))
                // A page that never tells its height (its script stopped)
                // shows once loaded, at the height it was or FRAME_DEFAULT.
                .with_on_page_load_handler(move |event, _| {
                    if matches!(event, wry::PageLoadEvent::Finished) {
                        loaded(&on_load, true);
                        still_soon(&on_load);
                    }
                })
                .build_as_child(&Parent(window));
            match built {
                Ok(view) => {
                    // wry maps its X11 container at creation whatever
                    // with_visible says: unmap it when it starts hidden.
                    if !show {
                        let _ = view.set_visible(false);
                    }
                    views.insert(
                        id.to_string(),
                        Placed {
                            view,
                            frame,
                            shown: show,
                            bounds: Some(at),
                        },
                    );
                }
                Err(e) => {
                    eprintln!("webviews are off: {e}");
                    UNAVAILABLE.store(true, std::sync::atomic::Ordering::Relaxed);
                }
            }
            return;
        }
        let p = views.get_mut(id).unwrap();
        p.frame = frame;
        let moved = p.bounds != Some(at);
        if moved {
            let _ = p.view.set_bounds(rect(at));
            p.bounds = Some(at);
        }
        if p.shown != show {
            let _ = p.view.set_visible(show);
            p.shown = show;
        }
        if moved || show {
            fit(&p.view, at);
        }
    });
    flush();
}

/// fit holds a chat page's webview at its block's size. wry's set_bounds
/// resizes the X window it made for the page and allocates the page once,
/// but GDK knows that window as a foreign one it never asked to hear about:
/// it keeps the size it was made at, and GTK's next layout (a show, the
/// page changing) gives the page that size again, so a block grown from
/// FRAME_DEFAULT showed the page's first 160 px and black below. Asking for
/// the window's structure events lets GDK follow its size; and should GTK
/// still have laid the page out at another size, it is laid out again here.
fn fit(view: &wry::WebView, at: Bounds<Pixels>) {
    #[cfg(target_os = "linux")]
    {
        use gtk::prelude::*;
        use wry::WebViewExtUnix as _;
        let (w, h) = (
            (f32::from(at.size.width).round() as i32).max(1),
            (f32::from(at.size.height).round() as i32).max(1),
        );
        let page = view.webview();
        let Some(top) = page.toplevel() else {
            return;
        };
        if let Some(window) = top.window() {
            let events = window.events();
            if !events.contains(gtk::gdk::EventMask::STRUCTURE_MASK) {
                window.set_events(events | gtk::gdk::EventMask::STRUCTURE_MASK);
            }
        }
        if page.allocated_width() != w || page.allocated_height() != h {
            top.size_allocate(&gtk::Allocation::new(0, 0, w, h));
        }
    }
    #[cfg(not(target_os = "linux"))]
    let _ = (view, at);
}

/// A page of an agent's machine (the panel's Browser): the webview, the URL
/// it was last sent to, the frame it was placed in (0 hidden), and since when
/// it is hidden.
type Page = (wry::WebView, String, u64, Option<Instant>);

thread_local! {
    /// The pages, by id.
    static PAGES: RefCell<HashMap<String, Page>> = RefCell::new(HashMap::new());
}

static PAGE_FRAME: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);

/// pages_begin and pages_end go first and last in the window: a page not
/// placed between them this frame (its view closed, something drawn over
/// it) is hidden.
pub fn pages_begin() -> impl IntoElement {
    canvas(
        |_, _, _| {},
        |_, _, _, _| {
            PAGE_FRAME.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
        },
    )
    .absolute()
    .size_0()
}

pub fn pages_end() -> impl IntoElement {
    canvas(
        |_, _, _| {},
        |_, _, _, _| {
            let now = PAGE_FRAME.load(std::sync::atomic::Ordering::Relaxed);
            PAGES.with_borrow_mut(|pages| {
                for (view, _, frame, hidden) in pages.values_mut() {
                    if *frame != now && *frame != 0 {
                        let _ = view.set_visible(false);
                        *frame = 0;
                        *hidden = Some(Instant::now());
                    }
                }
            });
            LARGE.with_borrow_mut(|large| {
                if let Some(p) = large.as_mut()
                    && p.frame != now
                    && p.shown
                {
                    let _ = p.view.set_visible(false);
                    p.shown = false;
                }
            });
            flush();
        },
    )
    .absolute()
    .size_0()
}

static CLIENT: std::sync::OnceLock<agentbox_api::Client> = std::sync::OnceLock::new();

/// set_client is the daemon pages' `agentbox:` URLs are read from.
pub fn set_client(client: agentbox_api::Client) {
    let _ = CLIENT.set(client);
}

/// client is the daemon's client, once main set it.
#[cfg(target_os = "linux")]
pub fn client() -> Option<agentbox_api::Client> {
    CLIENT.get().cloned()
}

/// daemon_url is a path of the daemon's API as a page loads it: the
/// Electron app's agentbox-media:// protocol, which relays the socket. wry
/// serves custom schemes as http://<scheme>.localhost on Windows.
pub fn daemon_url(path: &str) -> String {
    if cfg!(windows) {
        format!("http://agentbox.localhost{path}")
    } else {
        format!("agentbox://localhost{path}")
    }
}

/// with_daemon serves `agentbox:` from the daemon's socket, its status,
/// headers (a Hatch page's Content-Security-Policy) and body as they are.
fn with_daemon(b: wry::WebViewBuilder<'_>) -> wry::WebViewBuilder<'_> {
    b.with_asynchronous_custom_protocol("agentbox".into(), |_, req, responder| {
        let path = req
            .uri()
            .path_and_query()
            .map(|p| p.as_str().to_string())
            .unwrap_or_default();
        // A folder report's files, as /media/<id>/<path> (media::page_path).
        let path = crate::media::media_route(&path);
        let Some(client) = CLIENT.get().cloned() else {
            responder.respond(
                wry::http::Response::builder()
                    .status(503)
                    .body(Vec::new())
                    .unwrap(),
            );
            return;
        };
        client.clone().spawn(async move {
            let res = match client.fetch(&path).await {
                Ok((status, headers, body)) => {
                    let mut r = wry::http::Response::builder().status(status);
                    for (k, v) in headers {
                        if k != "content-length" && k != "transfer-encoding" {
                            r = r.header(k, v);
                        }
                    }
                    r.body(body.to_vec())
                }
                Err(e) => wry::http::Response::builder()
                    .status(502)
                    .header("content-type", "text/plain; charset=utf-8")
                    .body(e.to_string().into_bytes()),
            };
            if let Ok(r) = res {
                responder.respond(r);
            }
        });
    })
}

/// page is a live web page filling its box: `url`, loaded again whenever it
/// (or the reload key in it, after a #) changes. Unlike a preview it goes
/// to the network and follows links: it is a page of the agent's machine
/// through the daemon's preview proxy, the Electron app's iframe.
pub fn page(id: impl Into<String>, url: impl Into<String>, shown: bool) -> impl IntoElement {
    let id = id.into();
    let url = url.into();
    canvas(
        |_, _, _| {},
        move |bounds, _, window, _| {
            if shown && !covered() {
                place_page(&id, &url, bounds, window)
            }
        },
    )
    .size_full()
}

fn place_page(id: &str, url: &str, bounds: Bounds<Pixels>, window: &mut Window) {
    if !available() {
        return;
    }
    let frame = PAGE_FRAME.load(std::sync::atomic::Ordering::Relaxed);
    PAGES.with_borrow_mut(|pages| {
        if let Some((view, loaded, at, hidden)) = pages.get_mut(id) {
            if loaded != url {
                let _ = view.load_url(url.split('#').next().unwrap_or(url));
                *loaded = url.to_string();
            }
            let _ = view.set_bounds(rect(bounds));
            if *at == 0 {
                let _ = view.set_visible(true);
            }
            *at = frame;
            *hidden = None;
            return;
        }
        let built = with_daemon(wry::WebViewBuilder::new())
            .with_url(url.split('#').next().unwrap_or(url))
            .with_bounds(rect(bounds))
            .with_incognito(true)
            .build_as_child(&Parent(window));
        match built {
            Ok(view) => {
                pages.insert(id.to_string(), (view, url.to_string(), frame, None));
            }
            Err(e) => {
                eprintln!("webviews are off: {e}");
                UNAVAILABLE.store(true, std::sync::atomic::Ordering::Relaxed);
            }
        }
    });
    flush();
}

thread_local! {
    /// The ```html preview page open large (large.rs): a webview of its own,
    /// made as it opens and dropped as it closes.
    static LARGE: RefCell<Option<LargePage>> = const { RefCell::new(None) };
}

struct LargePage {
    id: String,
    view: wry::WebView,
    zoom: f32,
    /// The frame it was last placed in (PAGE_FRAME's), and whether it shows.
    frame: u64,
    shown: bool,
}

impl Drop for LargePage {
    fn drop(&mut self) {
        cut_ipc(&self.view);
    }
}

/// What the page open large asked of the app: its keys and Ctrl+wheel,
/// which GPUI doesn't hear while the page has the keyboard.
static LARGE_COMMANDS: std::sync::Mutex<Vec<String>> = std::sync::Mutex::new(Vec::new());

/// The script the page open large gets: Escape closes it, and Ctrl (or ⌘)
/// with plus, minus, 0 or the wheel zooms it (a touchpad's pixels added up
/// to a notch); every other key and wheel is the page's own.
const LARGE_SCRIPT: &str = "(() => { const post = c => window.ipc.postMessage(c); let wheel = 0;\
addEventListener('keydown', e => { const mod = e.ctrlKey || e.metaKey; const c = e.key === 'Escape' ? 'close' : !mod ? '' : (e.key === '=' || e.key === '+') ? 'in' : (e.key === '-' || e.key === '_') ? 'out' : e.key === '0' ? 'reset' : ''; if (c) { e.preventDefault(); post(c); } }, true);\
addEventListener('wheel', e => { if (!(e.ctrlKey || e.metaKey)) return; e.preventDefault(); wheel += e.deltaMode === 1 ? e.deltaY * 40 : e.deltaY; if (Math.abs(wheel) >= 40) { post(wheel < 0 ? 'in' : 'out'); wheel = 0; } }, { passive: false }); })();";

/// large_page is the page open large, filling its box at `zoom`.
pub fn large_page(id: String, html: String, zoom: f32) -> impl IntoElement {
    canvas(
        |_, _, _| {},
        move |bounds, _, window, _| place_large(&id, &html, bounds, zoom, window),
    )
    .size_full()
}

fn place_large(id: &str, html: &str, bounds: Bounds<Pixels>, zoom: f32, window: &mut Window) {
    if !available() {
        return;
    }
    let frame = PAGE_FRAME.load(std::sync::atomic::Ordering::Relaxed);
    LARGE.with_borrow_mut(|slot| {
        if slot.as_ref().is_some_and(|p| p.id != id) {
            *slot = None;
        }
        if let Some(p) = slot.as_mut() {
            let _ = p.view.set_bounds(rect(bounds));
            if p.zoom != zoom {
                let _ = p.view.zoom(zoom as f64);
                p.zoom = zoom;
            }
            if !p.shown {
                let _ = p.view.set_visible(true);
                p.shown = true;
            }
            p.frame = frame;
            return;
        }
        let built = wry::WebViewBuilder::new()
            .with_html(framed(html))
            .with_bounds(rect(bounds))
            .with_incognito(true)
            .with_navigation_handler(|url| url == "about:blank" || url.starts_with("data:"))
            .with_initialization_script(LARGE_SCRIPT)
            .with_ipc_handler(|req| LARGE_COMMANDS.lock().unwrap().push(req.body().clone()))
            .build_as_child(&Parent(window));
        match built {
            Ok(view) => {
                if zoom != 1. {
                    let _ = view.zoom(zoom as f64);
                }
                *slot = Some(LargePage {
                    id: id.to_string(),
                    view,
                    zoom,
                    frame,
                    shown: true,
                });
            }
            Err(e) => eprintln!("the page open large: {e}"),
        }
    });
    flush();
}

/// close_large drops the page open large, giving the keyboard back to the
/// window first.
pub fn close_large() {
    LARGE.with_borrow_mut(|slot| {
        if let Some(p) = slot.take() {
            let _ = p.view.focus_parent();
        }
    });
    flush();
}

/// The chat's previews kept off screen, besides those on it: each webview is
/// a WebKit web process and network process of its own (about 27 MB
/// together, measured with WebKitGTK 2.54), and every preview a long chat had
/// shown used to be kept until the app quit.
const KEEP_HIDDEN: usize = 3;

/// How long a webview not drawn is kept (a preview scrolled away, a page whose
/// panel closed) before it goes; it is made again when drawn again.
const HIDDEN_FOR: Duration = Duration::from_secs(30);

thread_local! {
    /// When each preview was last drawn, by id.
    static DRAWN: RefCell<HashMap<String, Instant>> = RefCell::new(HashMap::new());
}

/// prune lets go of the webviews not drawn lately: the chat's previews and the
/// panel's pages.
fn prune() {
    let now = FRAME.load(std::sync::atomic::Ordering::Relaxed);
    VIEWS.with_borrow_mut(|views| prune_previews(views, now));
    PAGES.with_borrow_mut(|pages| {
        pages.retain(|_, (_, _, _, hidden)| hidden.is_none_or(|at| at.elapsed() < HIDDEN_FOR))
    });
}

/// prune_previews keeps the previews drawn in frame `now`, and of the others
/// the KEEP_HIDDEN drawn last, for HIDDEN_FOR.
fn prune_previews(views: &mut HashMap<String, Placed>, now: u64) {
    let t = Instant::now();
    DRAWN.with_borrow_mut(|drawn| {
        for (id, p) in views.iter() {
            if p.frame == now {
                drawn.insert(id.clone(), t);
            }
        }
        let gone = {
            let frames: HashMap<&str, u64> =
                views.iter().map(|(id, p)| (id.as_str(), p.frame)).collect();
            stale_ids(&frames, drawn, now, t)
        };
        for id in gone {
            views.remove(&id);
            loaded(&id, false);
            drop_still(&id);
        }
        drawn.retain(|id, _| views.contains_key(id));
    });
}

/// stale_ids is the previews to let go of at `t`: off screen (not drawn in
/// frame `now`) and either past the KEEP_HIDDEN drawn last or not drawn for
/// HIDDEN_FOR.
fn stale_ids(
    frames: &HashMap<&str, u64>,
    drawn: &HashMap<String, Instant>,
    now: u64,
    t: Instant,
) -> Vec<String> {
    let mut hidden: Vec<(Instant, &str)> = frames
        .iter()
        .filter(|(_, frame)| **frame != now)
        .map(|(id, _)| (drawn.get(*id).copied().unwrap_or(t), *id))
        .collect();
    // Drawn longest ago first.
    hidden.sort();
    let over = hidden.len().saturating_sub(KEEP_HIDDEN);
    hidden
        .into_iter()
        .enumerate()
        .filter(|(i, (at, _))| *i < over || t.duration_since(*at) >= HIDDEN_FOR)
        .map(|(_, (_, id))| id.to_string())
        .collect()
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;
    use std::time::{Duration, Instant};

    use super::{PRELUDE, framed, stale_ids};

    #[test]
    fn a_page_keeps_its_doctype_first() {
        // A fragment gets a doctype, so it isn't laid out in quirks mode.
        assert_eq!(
            framed("<b>hi</b>"),
            format!("<!doctype html>{PRELUDE}<b>hi</b>")
        );
        // The prelude goes at the start of the page's head, or its html, or
        // after its doctype.
        let page =
            "<!DOCTYPE html><html lang=en><head><title>t</title></head><body>x</body></html>";
        assert_eq!(
            framed(page),
            format!(
                "<!DOCTYPE html><html lang=en><head>{PRELUDE}<title>t</title></head><body>x</body></html>"
            )
        );
        assert_eq!(
            framed("<!doctype html><html><body>x"),
            format!("<!doctype html><html>{PRELUDE}<body>x")
        );
        assert_eq!(
            framed("<!doctype html><p>x"),
            format!("<!doctype html>{PRELUDE}<p>x")
        );
        // A <header> isn't the head.
        assert_eq!(
            framed("<header>top</header>"),
            format!("<!doctype html>{PRELUDE}<header>top</header>")
        );
    }

    #[test]
    fn previews_off_screen_go_past_three_and_after_a_while() {
        let t = Instant::now() + Duration::from_secs(100);
        let ago = |s: u64| t - Duration::from_secs(s);
        // a and b on screen (frame 9), c..g off it.
        let frames: HashMap<&str, u64> = [
            ("a", 9),
            ("b", 9),
            ("c", 8),
            ("d", 7),
            ("e", 6),
            ("f", 5),
            ("g", 4),
        ]
        .into_iter()
        .collect();
        let drawn: HashMap<String, Instant> = [
            ("c", 1),
            ("d", 2),
            ("e", 3),
            ("f", 4),
            ("g", 5),
            ("a", 0),
            ("b", 0),
        ]
        .into_iter()
        .map(|(id, s)| (id.to_string(), ago(s)))
        .collect();
        let mut gone = stale_ids(&frames, &drawn, 9, t);
        gone.sort();
        assert_eq!(gone, ["f", "g"]);
        // Long unseen, any off-screen preview goes; those on screen never do.
        let drawn: HashMap<String, Instant> =
            frames.keys().map(|id| (id.to_string(), ago(31))).collect();
        let mut gone = stale_ids(&frames, &drawn, 9, t);
        gone.sort();
        assert_eq!(gone, ["c", "d", "e", "f", "g"]);
        // A preview just made, not yet in `drawn`, stays.
        let frames: HashMap<&str, u64> = [("new", 8)].into_iter().collect();
        assert!(stale_ids(&frames, &HashMap::new(), 9, t).is_empty());
    }
}
