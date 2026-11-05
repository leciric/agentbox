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
//! On Linux only X11 is supported (wry can't make child webviews on Wayland).
use std::cell::RefCell;
use std::collections::HashMap;
use std::time::Duration;

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

pub fn available() -> bool {
    !UNAVAILABLE.load(std::sync::atomic::Ordering::Relaxed)
}

static FRAME: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);

/// The app's palette as the CSS variables the Electron app's previews use.
const PRELUDE: &str = r#"<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; font-src data:">
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
    cx.spawn(async move |cx| {
        loop {
            cx.background_executor()
                .timer(Duration::from_millis(16))
                .await;
            #[cfg(target_os = "linux")]
            while gtk::events_pending() {
                gtk::main_iteration_do(false);
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
            let inside = clip.contains(&bounds.origin)
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
