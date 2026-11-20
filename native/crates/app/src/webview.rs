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

/// Wheel deltas from pages, in pixels: a page doesn't scroll, the chat does.
static WHEEL: std::sync::Mutex<f32> = std::sync::Mutex::new(0.);

/// The script every page gets: its wheel goes to the app.
const FORWARD_WHEEL: &str = "addEventListener('wheel', e => { e.preventDefault(); window.ipc.postMessage(String(e.deltaY)); }, { passive: false });";

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
            #[cfg(target_os = "linux")]
            if crate::snapshot::take_dirty() {
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

/// html_preview is a block of `height` that shows `html` in a webview.
pub fn html_preview(
    id: impl Into<String>,
    html: impl Into<String>,
    height: Pixels,
) -> impl IntoElement {
    let id = id.into();
    let html = html.into();
    canvas(
        |_, _, _| {},
        move |bounds, _, window, _| {
            let clip = CLIP
                .with(|c| c.get())
                .unwrap_or(window.content_mask().bounds);
            let inside = !covered()
                && clip.contains(&bounds.origin)
                && clip.contains(&bounds.bottom_right().map(|v| v - px(1.)));
            place(&id, &html, bounds, inside, window);
        },
    )
    .w_full()
    .h(height)
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

fn place(id: &str, html: &str, bounds: Bounds<Pixels>, inside: bool, window: &mut Window) {
    let frame = FRAME.load(std::sync::atomic::Ordering::Relaxed);
    VIEWS.with_borrow_mut(|views| {
        if !views.contains_key(id) {
            let built = wry::WebViewBuilder::new()
                .with_html(format!("{PRELUDE}{html}"))
                .with_bounds(rect(bounds))
                .with_visible(inside)
                .with_incognito(true)
                .with_navigation_handler(|url| url == "about:blank" || url.starts_with("data:"))
                .with_initialization_script(FORWARD_WHEEL)
                .with_ipc_handler(|req| {
                    if let Ok(dy) = req.body().parse::<f32>() {
                        *WHEEL.lock().unwrap() += dy;
                    }
                })
                .build_as_child(&Parent(window));
            match built {
                Ok(view) => {
                    // wry maps its X11 container at creation whatever
                    // with_visible says: unmap it when it starts hidden.
                    if !inside {
                        let _ = view.set_visible(false);
                    }
                    views.insert(
                        id.to_string(),
                        Placed {
                            view,
                            frame,
                            shown: inside,
                            bounds: Some(bounds),
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
        if p.bounds != Some(bounds) {
            let _ = p.view.set_bounds(rect(bounds));
            p.bounds = Some(bounds);
        }
        if p.shown != inside {
            let _ = p.view.set_visible(inside);
            p.shown = inside;
        }
    });
    flush();
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
            .with_html(format!("{PRELUDE}{html}"))
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
    // Not super::*: gpui's own `test` attribute would come with it.
    use std::collections::HashMap;
    use std::time::{Duration, Instant};

    use super::stale_ids;

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
