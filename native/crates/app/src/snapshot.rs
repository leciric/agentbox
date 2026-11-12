//! ```html preview blocks where a child webview can't be made: on Linux when
//! the GPUI window is a Wayland one (wry makes child webviews on X11 only), or
//! when making one failed. The page is laid out by WebKitGTK in an offscreen
//! window as wide as its block, snapshotted once it has loaded, and the block
//! shows that picture.
//!
//! It is a still: the page's own height up to MAX_HEIGHT, nothing to click,
//! hover or animate. The page gets what a child webview's gets (webview.rs's
//! PRELUDE: the Content-Security-Policy and the app's CSS variables), an
//! ephemeral context, and no navigation away from it.
//!
//! GTK is started and pumped from GPUI's loop by webview::init; every call
//! here is on that same (main) thread.
use std::cell::{Cell, RefCell};
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

use gpui::*;
use gtk::prelude::*;
use webkit2gtk::{
    LoadEvent, NavigationPolicyDecision, PolicyDecisionExt, PolicyDecisionType, SnapshotOptions,
    SnapshotRegion, URIRequestExt, WebContext, WebView, WebViewExt,
};

/// The viewport's height, the X11 path's block height: a page shorter than
/// it is shown this tall, one taller up to MAX_HEIGHT.
const VIEWPORT: f32 = 260.;
const MAX_HEIGHT: f32 = 720.;
/// How long after load-finished the picture is taken, for scripts that draw
/// on the next frame or a short timer.
const SETTLE: Duration = Duration::from_millis(200);

thread_local! {
    static PAGES: RefCell<HashMap<String, Page>> = RefCell::new(HashMap::new());
    static CONTEXT: WebContext = WebContext::new_ephemeral();
    /// Set when a picture is ready, so the pump redraws the windows.
    static DIRTY: Cell<bool> = const { Cell::new(false) };
}

/// A width in device pixels and the scale it was laid out at.
type Size = (i32, f32);

struct Page {
    html: String,
    window: gtk::OffscreenWindow,
    view: WebView,
    /// The size the block wants, the one being rendered, the one shown.
    want: Size,
    busy: Option<Size>,
    image: Option<(Arc<RenderImage>, f32)>,
    failed: bool,
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

/// take_dirty is whether a picture (or a page's HTML) came in since it was
/// last asked.
pub fn take_dirty() -> bool {
    DIRTY.with(|d| d.replace(false)) | FETCHED.swap(false, std::sync::atomic::Ordering::Relaxed)
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
        Some(h) if !h.is_empty() => html_snapshot(id, h).into_any_element(),
        _ => div().into_any_element(),
    }
}

/// html_snapshot is a block showing `html` as a picture, as tall as the page
/// (from its last picture; VIEWPORT until there is one).
pub fn html_snapshot(id: impl Into<String>, html: impl Into<String>) -> impl IntoElement {
    let id = id.into();
    let html = html.into();
    let height = PAGES
        .with_borrow(|p| {
            p.get(&id)
                .and_then(|p| p.image.as_ref())
                .map(|(img, scale)| img.size(0).height.0 as f32 / scale)
        })
        .unwrap_or(VIEWPORT)
        .clamp(1., MAX_HEIGHT);
    canvas(
        |_, _, _| {},
        move |bounds, _, window, _| {
            let scale = window.scale_factor();
            let width = (f32::from(bounds.size.width) * scale).round() as i32;
            if width <= 0 {
                return;
            }
            let image = PAGES.with_borrow_mut(|pages| {
                if !pages.contains_key(&id) && pages.len() >= MAX_PAGES {
                    trim(pages);
                }
                let page = pages
                    .entry(id.clone())
                    .or_insert_with(|| Page::new(&id, html.clone()));
                page.want = (width, scale);
                if page.busy.is_none() && !page.failed && page.shown() != Some(page.want) {
                    page.start();
                }
                page.image.clone()
            });
            if let Some((image, at)) = image {
                let size = image.size(0);
                let shown = Bounds::new(
                    bounds.origin,
                    gpui::size(px(size.width.0 as f32 / at), px(size.height.0 as f32 / at)),
                );
                let _ = window.paint_image(bounds, shown, Corners::default(), image, 0, false);
            }
        },
    )
    .w_full()
    .h(px(height))
}

/// Pages kept at once: each holds a WebKit view (and its web process's share).
const MAX_PAGES: usize = 48;

/// trim drops the pages not rendering; those still shown are made again.
fn trim(pages: &mut HashMap<String, Page>) {
    pages.retain(|_, p| {
        if p.busy.is_some() {
            return true;
        }
        // SAFETY: nothing else holds the window; its view goes with it.
        unsafe { p.window.destroy() };
        false
    });
}

impl Page {
    fn new(id: &str, html: String) -> Page {
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
        let key = id.to_string();
        view.connect_load_changed(move |_, event| {
            if event == LoadEvent::Finished {
                let key = key.clone();
                gtk::glib::timeout_add_local_once(SETTLE, move || picture(&key));
            }
        });
        let key = id.to_string();
        view.connect_load_failed(move |_, _, _, _| {
            PAGES.with_borrow_mut(|p| {
                if let Some(p) = p.get_mut(&key) {
                    p.failed = true;
                    p.busy = None;
                }
            });
            DIRTY.with(|d| d.set(true));
            false
        });
        let window = gtk::OffscreenWindow::new();
        window.add(&view);
        window.show_all();
        Page {
            html,
            window,
            view,
            want: (0, 1.),
            busy: None,
            image: None,
            failed: false,
        }
    }

    fn shown(&self) -> Option<Size> {
        self.image
            .as_ref()
            .map(|(img, scale)| (img.size(0).width.0, *scale))
    }

    /// start lays the page out at `want` and loads it; picture() follows.
    fn start(&mut self) {
        let (width, scale) = self.want;
        self.busy = Some(self.want);
        let height = (VIEWPORT * scale).round() as i32;
        self.view.set_size_request(width, height);
        self.window.resize(width, height);
        self.view.set_zoom_level(scale as f64);
        self.view
            .load_html(&format!("{}{}", crate::webview::PRELUDE, self.html), None);
    }
}

/// picture snapshots the page `key` once it has loaded and settled.
fn picture(key: &str) {
    let Some(view) = PAGES.with_borrow(|p| p.get(key).map(|p| p.view.clone())) else {
        return;
    };
    let key = key.to_string();
    view.snapshot(
        SnapshotRegion::FullDocument,
        SnapshotOptions::NONE,
        None::<&gtk::gio::Cancellable>,
        move |result| {
            let image = result
                .map_err(|e| e.to_string())
                .and_then(|s| to_image(s).ok_or_else(|| "not an image surface".into()));
            PAGES.with_borrow_mut(|pages| {
                let Some(page) = pages.get_mut(&key) else {
                    return;
                };
                let at = page.busy.take();
                match image {
                    Ok(image) => {
                        page.image = Some((image, at.map_or(1., |(_, s)| s)));
                        // The block was resized while this one rendered.
                        if page.shown() != Some(page.want) {
                            page.start();
                        }
                    }
                    Err(e) => {
                        eprintln!("html preview: snapshot: {e}");
                        page.failed = true;
                    }
                }
            });
            DIRTY.with(|d| d.set(true));
        },
    );
}

/// to_image copies a snapshot (cairo ARGB32: BGRA bytes on little-endian, as
/// GPUI's RenderImage takes them) into a RenderImage.
fn to_image(surface: gtk::cairo::Surface) -> Option<Arc<RenderImage>> {
    let surface = gtk::cairo::ImageSurface::try_from(surface).ok()?;
    let (w, h, stride) = (
        surface.width() as usize,
        surface.height() as usize,
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
