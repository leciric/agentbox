//! An agent's desktop: its VNC server through the daemon
//! (GET /v1/agents/<p>/<a>/browser/view, RFB bytes in WebSocket frames, as
//! noVNC reads it in the Electron app). vnc-rs speaks RFB on the I/O runtime
//! into a framebuffer, and GPUI shows it as an image, sending the pointer and
//! keys back. Like the Electron app's VncSession it connects again by itself
//! whenever the connection ends or can't open (the desktop not started yet,
//! the agent's machine restarting), and says why meanwhile.
use std::sync::{Arc, Mutex};
use std::time::Duration;

use agentbox_api::Client;
use agentbox_api::stream::{In, Out};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};
use tokio::io::{AsyncReadExt as _, AsyncWriteExt as _};
use tokio::sync::mpsc;
use vnc::{
    ClientKeyEvent, ClientMouseEvent, PixelFormat, VncConnector, VncEncoding, VncEvent, X11Event,
};

use crate::theme;

/// The remote screen, BGRA.
#[derive(Default)]
struct Framebuffer {
    width: u32,
    height: u32,
    pixels: Vec<u8>,
    dirty: bool,
}

impl Framebuffer {
    fn resize(&mut self, w: u32, h: u32) {
        self.width = w;
        self.height = h;
        self.pixels = vec![0; (w * h * 4) as usize];
        self.dirty = true;
    }

    fn blit(&mut self, r: &vnc::Rect, data: &[u8]) {
        let (x, y, w, h) = (
            r.x as usize,
            r.y as usize,
            r.width as usize,
            r.height as usize,
        );
        let stride = self.width as usize * 4;
        for row in 0..h {
            let dst = (y + row) * stride + x * 4;
            let src = row * w * 4;
            if dst + w * 4 <= self.pixels.len() && src + w * 4 <= data.len() {
                self.pixels[dst..dst + w * 4].copy_from_slice(&data[src..src + w * 4]);
            }
        }
        self.dirty = true;
    }

    fn copy(&mut self, dst: &vnc::Rect, src: &vnc::Rect) {
        let stride = self.width as usize * 4;
        let (w, h) = (src.width as usize * 4, src.height as usize);
        let rows: Vec<Vec<u8>> = (0..h)
            .map(|r| {
                let s = (src.y as usize + r) * stride + src.x as usize * 4;
                self.pixels
                    .get(s..s + w)
                    .map(<[u8]>::to_vec)
                    .unwrap_or_default()
            })
            .collect();
        for (r, row) in rows.iter().enumerate() {
            let d = (dst.y as usize + r) * stride + dst.x as usize * 4;
            if d + row.len() <= self.pixels.len() {
                self.pixels[d..d + row.len()].copy_from_slice(row);
            }
        }
        self.dirty = true;
    }
}

/// Where the remote screen is drawn in the view, as noVNC's scaleViewport
/// draws it in the Electron app: scaled uniformly to fit, never stretched,
/// centred, letterboxed; and the size its picture is made at.
#[derive(Clone, Copy, Debug, PartialEq)]
struct Fit {
    /// In window pixels, on whole device pixels: a picture drawn between two
    /// of them is blurred across both.
    bounds: Bounds<Pixels>,
    /// The picture's size, in device pixels: the remote screen's own when it
    /// is shown at full size or larger, else exactly the size it's drawn at,
    /// averaged down (shrink) rather than left to the GPU, whose sampling
    /// drops whole rows and columns of thin text.
    image: (u32, u32),
}

/// fit places a remote screen of `screen` pixels in `area`, on a display of
/// `scale_factor` device pixels to the window pixel: None while either is
/// empty.
fn fit(area: Bounds<Pixels>, screen: (u32, u32), scale_factor: f32) -> Option<Fit> {
    let (w, h) = screen;
    let sf = if scale_factor > 0. { scale_factor } else { 1. };
    let (aw, ah) = (
        f32::from(area.size.width) * sf,
        f32::from(area.size.height) * sf,
    );
    if w == 0 || h == 0 || aw < 1. || ah < 1. {
        return None;
    }
    let s = (aw / w as f32).min(ah / h as f32);
    let (dw, dh) = (
        (w as f32 * s).round().clamp(1., aw.floor().max(1.)),
        (h as f32 * s).round().clamp(1., ah.floor().max(1.)),
    );
    let (ox, oy) = (
        (f32::from(area.origin.x) * sf + (aw - dw) / 2.).round(),
        (f32::from(area.origin.y) * sf + (ah - dh) / 2.).round(),
    );
    Some(Fit {
        bounds: Bounds {
            origin: point(px(ox / sf), px(oy / sf)),
            size: size(px(dw / sf), px(dh / sf)),
        },
        image: if (dw as u32) < w {
            (dw as u32, dh as u32)
        } else {
            (w, h)
        },
    })
}

/// remote_point maps a window point onto a remote screen of `screen` pixels
/// drawn at `shown`: the pixel under it, clamped to the screen's edges so a
/// drag that leaves the picture still lands on its edge.
fn remote_point(shown: Bounds<Pixels>, screen: (u32, u32), p: Point<Pixels>) -> Option<(u16, u16)> {
    let (w, h) = screen;
    if w == 0 || h == 0 || shown.size.width <= px(0.) || shown.size.height <= px(0.) {
        return None;
    }
    let x = ((p.x - shown.origin.x) / shown.size.width * w as f32).floor();
    let y = ((p.y - shown.origin.y) / shown.size.height * h as f32).floor();
    Some((
        x.clamp(0., (w - 1) as f32) as u16,
        y.clamp(0., (h - 1) as f32) as u16,
    ))
}

/// shrink averages a BGRA picture of sw×sh down to dw×dh, each pixel the mean
/// of the ones it covers (a box filter, in two passes), opaque. Not larger:
/// a picture already as small passes through, only made opaque.
fn shrink(src: &[u8], sw: u32, sh: u32, dw: u32, dh: u32) -> Vec<u8> {
    let (sw, sh) = (sw as usize, sh as usize);
    let (dw, dh) = (
        dw.clamp(1, sw as u32) as usize,
        dh.clamp(1, sh as u32) as usize,
    );
    if src.len() < sw * sh * 4 {
        return vec![0; dw * dh * 4];
    }
    // The source columns (or rows) each output one covers: [from, to).
    let spans = |n: usize, d: usize| -> Vec<(usize, usize)> {
        (0..d)
            .map(|i| {
                let from = i * n / d;
                (from, ((i + 1) * n / d).max(from + 1))
            })
            .collect()
    };
    let cols = spans(sw, dw);
    let rows = spans(sh, dh);
    // Across: sh rows of dw pixels, as sums with their counts.
    let mut across = vec![0u32; sh * dw * 3];
    for y in 0..sh {
        let line = &src[y * sw * 4..(y + 1) * sw * 4];
        for (x, &(from, to)) in cols.iter().enumerate() {
            let (mut b, mut g, mut r) = (0u32, 0u32, 0u32);
            for p in line[from * 4..to * 4].as_chunks::<4>().0 {
                b += p[0] as u32;
                g += p[1] as u32;
                r += p[2] as u32;
            }
            let n = (to - from) as u32;
            let o = (y * dw + x) * 3;
            across[o] = (b + n / 2) / n;
            across[o + 1] = (g + n / 2) / n;
            across[o + 2] = (r + n / 2) / n;
        }
    }
    // Down.
    let mut out = vec![255u8; dw * dh * 4];
    for (y, &(from, to)) in rows.iter().enumerate() {
        let n = (to - from) as u32;
        for x in 0..dw {
            let (mut b, mut g, mut r) = (0u32, 0u32, 0u32);
            for sy in from..to {
                let o = (sy * dw + x) * 3;
                b += across[o];
                g += across[o + 1];
                r += across[o + 2];
            }
            let o = (y * dw + x) * 4;
            out[o] = ((b + n / 2) / n) as u8;
            out[o + 1] = ((g + n / 2) / n) as u8;
            out[o + 2] = ((r + n / 2) / n) as u8;
        }
    }
    out
}

/// picture makes the image of a remote screen's pixels (BGRA, sw×sh) at
/// `want` (Fit::image), opaque.
fn picture(
    mut px: Vec<u8>,
    sw: u32,
    sh: u32,
    want: Option<(u32, u32)>,
) -> Option<Arc<RenderImage>> {
    let (w, h, px) = match want {
        // Wanted for this screen's shape: not for the one before it resized.
        Some((dw, dh))
            if 0 < dw
                && dw < sw
                && 0 < dh
                && dh <= sh
                && (dw as u64 * sh as u64).abs_diff(dh as u64 * sw as u64) <= sw.max(sh) as u64 =>
        {
            (dw, dh, shrink(&px, sw, sh, dw, dh))
        }
        _ => {
            for a in px.iter_mut().skip(3).step_by(4) {
                *a = 255;
            }
            (sw, sh, px)
        }
    };
    let img = image::RgbaImage::from_raw(w, h, px)?;
    Some(Arc::new(RenderImage::new(vec![image::Frame::new(img)])))
}

/// What the view's connection is doing.
#[derive(Clone, PartialEq)]
enum Link {
    /// Opening the stream and the VNC handshake.
    Connecting,
    /// Frames are arriving.
    Live,
    /// It ended or never opened, and why: tried again by itself.
    Down(SharedString),
}

/// How long to wait before trying again, after `failures` in a row: every 2
/// seconds like the Electron app (vncSession.ts), then less often, since a
/// VNC server counts a connection dropped in its handshake as a failed login.
fn backoff(failures: u32) -> Duration {
    Duration::from_secs(if failures <= 5 { 2 } else { 5 })
}

pub struct VncView {
    image: Option<Arc<RenderImage>>,
    /// Whether the view has drawn since the last image was made: a new image
    /// is made only then, so a view that isn't on screen (the panel on
    /// another tab) holds one frame rather than piling them up.
    drawn: bool,
    /// A frame arrived while the last one was still undrawn.
    pending: bool,
    /// Asks the frame loop for the newest frame (render, after `pending`):
    /// the current connection's.
    frames: Option<mpsc::UnboundedSender<Result<(), String>>>,
    /// The remote screen's size, in its pixels.
    size: (u32, u32),
    /// The size the image was made at (Fit::image), in device pixels.
    image_size: (u32, u32),
    /// The size the view draws the screen at now, and so wants its image
    /// made at (Fit::image): set as it's laid out, read as a frame is made.
    want: Arc<Mutex<Option<(u32, u32)>>>,
    link: Link,
    /// Failures since the last frame, for the backoff.
    failures: u32,
    /// Set by retry: the wait before the next attempt ends now.
    retry_now: bool,
    input: Option<mpsc::UnboundedSender<X11Event>>,
    /// Where the image was drawn, for mapping the pointer onto the screen.
    shown: Arc<Mutex<Option<Bounds<Pixels>>>>,
    buttons: u8,
    /// The scroll not yet sent as a wheel click.
    scrolled: f32,
    /// The modifiers the remote screen has been told are down.
    mods: Modifiers,
    /// The keysym each key held down was sent as, so its release is the same
    /// one (a shifted key's key_char is gone by the time it's released).
    held: Vec<(String, u32)>,
    focus: FocusHandle,
    /// Whether the pointer and keys go to the remote screen: off, it is only
    /// watched (the panel's "Take control" switch).
    pub control: bool,
    _task: Task<()>,
    _live: crate::bench::Live,
}

impl VncView {
    pub fn new(client: Client, path: String, cx: &mut Context<Self>) -> Self {
        // One task for the view's life: connect, show frames until the
        // connection ends, say why, wait, connect again.
        let task = cx.spawn(async move |this, cx| {
            loop {
                let reason = session(&this, &client, &path, cx).await;
                let Ok(wait) = this.update(cx, |v, cx| {
                    v.input = None;
                    v.frames = None;
                    v.failures += 1;
                    v.link = Link::Down(reason.into());
                    v.retry_now = false;
                    cx.notify();
                    backoff(v.failures)
                }) else {
                    return;
                };
                let mut waited = Duration::ZERO;
                while waited < wait {
                    let step = Duration::from_millis(250);
                    cx.background_executor().timer(step).await;
                    waited += step;
                    match this.update(cx, |v, _| std::mem::take(&mut v.retry_now)) {
                        Ok(true) => break,
                        Ok(false) => {}
                        Err(_) => return,
                    }
                }
            }
        });
        Self {
            image: None,
            drawn: true,
            pending: false,
            frames: None,
            size: (0, 0),
            image_size: (0, 0),
            want: Arc::default(),
            link: Link::Connecting,
            failures: 0,
            retry_now: false,
            input: None,
            shown: Arc::default(),
            buttons: 0,
            scrolled: 0.,
            mods: Modifiers::default(),
            held: Vec::new(),
            focus: cx.focus_handle(),
            control: true,
            _task: task,
            _live: crate::bench::Live::new("vnc"),
        }
    }

    pub fn focus_handle(&self) -> &FocusHandle {
        &self.focus
    }

    /// connected is whether the remote screen is showing, live.
    pub fn connected(&self) -> bool {
        self.link == Link::Live && self.image.is_some()
    }

    /// retry ends the wait before the next attempt, when it's down: the
    /// desktop was just started.
    pub fn retry(&mut self) {
        if matches!(self.link, Link::Down(_)) {
            self.failures = 0;
            self.retry_now = true;
        }
    }

    /// set_control gives the remote screen the pointer and keys, or takes
    /// them back, letting go of whatever was held down.
    pub fn set_control(&mut self, control: bool) {
        if !control {
            self.release_all();
        }
        self.control = control;
    }

    /// to_remote maps a window point onto the remote screen.
    fn to_remote(&self, p: Point<Pixels>) -> Option<(u16, u16)> {
        let b = (*self.shown.lock().unwrap())?;
        remote_point(b, self.size, p)
    }

    fn inside(&self, p: Point<Pixels>) -> bool {
        self.shown.lock().unwrap().is_some_and(|b| b.contains(&p))
    }

    fn send(&self, ev: X11Event) {
        if let Some(tx) = &self.input {
            let _ = tx.send(ev);
        }
    }

    fn pointer(&mut self, p: Point<Pixels>, buttons: u8) {
        self.buttons = buttons;
        if !self.control {
            return;
        }
        if let Some((x, y)) = self.to_remote(p) {
            self.send(X11Event::PointerEvent(ClientMouseEvent {
                position_x: x,
                position_y: y,
                bottons: buttons,
            }));
        }
    }

    fn press(&mut self, p: Point<Pixels>, mask: u8, down: bool) {
        let buttons = if down {
            self.buttons | mask
        } else {
            self.buttons & !mask
        };
        self.pointer(p, buttons);
    }

    /// wheel sends a scroll as wheel clicks (buttons 4 to 7), a click per 20
    /// pixels, so a touchpad's many small scrolls don't each make one.
    fn wheel(&mut self, p: Point<Pixels>, delta: Point<Pixels>) {
        if !self.control {
            return;
        }
        self.scrolled += f32::from(delta.y);
        let held = self.buttons;
        while self.scrolled.abs() >= 20. {
            let up = self.scrolled > 0.;
            self.scrolled -= if up { 20. } else { -20. };
            self.pointer(p, held | if up { 8 } else { 16 });
            self.pointer(p, held);
        }
        let dx = f32::from(delta.x);
        if dx.abs() >= 20. {
            self.pointer(p, held | if dx > 0. { 32 } else { 64 });
            self.pointer(p, held);
        }
    }

    fn key(&mut self, ks: &Keystroke, down: bool) {
        if !self.control {
            return;
        }
        if down {
            let Some(sym) = keysym(ks) else { return };
            self.held.retain(|(k, _)| *k != ks.key);
            self.held.push((ks.key.clone(), sym));
            self.send(X11Event::KeyEvent(ClientKeyEvent {
                keycode: sym,
                down: true,
            }));
        } else {
            let sym = match self.held.iter().position(|(k, _)| *k == ks.key) {
                Some(i) => self.held.remove(i).1,
                None => match keysym(ks) {
                    Some(s) => s,
                    None => return,
                },
            };
            self.send(X11Event::KeyEvent(ClientKeyEvent {
                keycode: sym,
                down: false,
            }));
        }
    }

    /// modifiers tells the remote screen which modifiers are down, as they
    /// change, so ctrl+click and shift+drag work as well as ctrl+c.
    fn modifiers(&mut self, m: Modifiers) {
        if !self.control {
            return;
        }
        for (was, now, sym) in [
            (self.mods.control, m.control, 0xffe3),
            (self.mods.alt, m.alt, 0xffe9),
            (self.mods.shift, m.shift, 0xffe1),
            (self.mods.platform, m.platform, 0xffeb),
        ] {
            if was != now {
                self.send(X11Event::KeyEvent(ClientKeyEvent {
                    keycode: sym,
                    down: now,
                }));
            }
        }
        self.mods = m;
    }

    /// release_all lets go of every key, modifier and button held down.
    fn release_all(&mut self) {
        for (_, sym) in std::mem::take(&mut self.held) {
            self.send(X11Event::KeyEvent(ClientKeyEvent {
                keycode: sym,
                down: false,
            }));
        }
        self.modifiers(Modifiers::default());
        let shown = *self.shown.lock().unwrap();
        if self.buttons != 0
            && let Some(b) = shown
        {
            self.pointer(b.center(), 0);
        }
    }
}

/// keysym is the X11 keysym for a GPUI key.
fn keysym(ks: &Keystroke) -> Option<u32> {
    Some(match ks.key.as_str() {
        "enter" => 0xff0d,
        "backspace" => 0xff08,
        "tab" => 0xff09,
        "escape" => 0xff1b,
        "delete" => 0xffff,
        "insert" => 0xff63,
        "home" => 0xff50,
        "left" => 0xff51,
        "up" => 0xff52,
        "right" => 0xff53,
        "down" => 0xff54,
        "pageup" => 0xff55,
        "pagedown" => 0xff56,
        "end" => 0xff57,
        "space" => 0x20,
        // Modifiers go as they change (VncView::modifiers).
        "shift" | "control" | "alt" | "platform" | "function" => return None,
        k if k.len() > 1 && k.starts_with('f') && k[1..].parse::<u32>().is_ok() => {
            0xffbe + k[1..].parse::<u32>().unwrap() - 1
        }
        _ => {
            // Latin-1 keysyms are their code points; others are 0x01000000 + it.
            let c = ks
                .key_char
                .as_ref()
                .and_then(|s| s.chars().next())
                .or_else(|| ks.key.chars().next())?;
            let c = c as u32;
            if c < 0x100 { c } else { 0x0100_0000 + c }
        }
    })
}

/// reason is what a failed stream says, without the HTTP status the daemon
/// answered with ("400 Bad Request: x's desktop isn't running" is x's...).
fn reason(e: &anyhow::Error) -> String {
    let s = format!("{e:#}");
    match s.split_once(": ") {
        Some((status, rest))
            if status.len() > 4
                && status.as_bytes()[..3].iter().all(u8::is_ascii_digit)
                && status.as_bytes()[3] == b' ' =>
        {
            rest.to_string()
        }
        _ => s,
    }
}

/// session is one connection, from opening the stream until it ends: it
/// says why it ended.
async fn session(
    this: &WeakEntity<VncView>,
    client: &Client,
    path: &str,
    cx: &mut AsyncApp,
) -> String {
    let fb = Arc::new(Mutex::new(Framebuffer::default()));
    let (input_tx, input_rx) = mpsc::unbounded_channel();
    let (frame_tx, mut frame_rx) = mpsc::unbounded_channel::<Result<(), String>>();
    if this
        .update(cx, |v, cx| {
            v.link = Link::Connecting;
            v.input = Some(input_tx);
            v.frames = Some(frame_tx.clone());
            // What was held down went with the last connection.
            v.held.clear();
            v.mods = Modifiers::default();
            v.buttons = 0;
            cx.notify();
        })
        .is_err()
    {
        return String::new();
    }
    let stream = match client.stream(path).await {
        Ok(s) => s,
        Err(e) => return reason(&e),
    };
    client.spawn({
        let fb = fb.clone();
        async move {
            let why = run(stream, fb, input_rx, &frame_tx).await;
            let _ = frame_tx.send(Err(why));
        }
    });
    while let Some(msg) = frame_rx.recv().await {
        // Skip to the newest frame.
        let mut last = msg;
        while let Ok(m) = frame_rx.try_recv() {
            if last.is_ok() {
                last = m;
            }
        }
        if let Err(e) = last {
            return e;
        }
        // Not drawn since the last image: note it and wait for the view to
        // draw (render asks again) rather than make another.
        let waiting = this
            .update(cx, |v, _| {
                let waiting = !v.drawn && v.image.is_some() && v.link == Link::Live;
                v.pending |= waiting;
                waiting
            })
            .unwrap_or(true);
        if waiting {
            continue;
        }
        let Ok(want) = this.update(cx, |v, _| *v.want.lock().unwrap()) else {
            return String::new();
        };
        let (px, w, h) = {
            let mut fb = fb.lock().unwrap();
            fb.dirty = false;
            (fb.pixels.clone(), fb.width, fb.height)
        };
        // Made off the UI thread: shrinking a large screen takes a few
        // milliseconds.
        let made = cx
            .background_executor()
            .spawn(async move { picture(px, w, h, want) })
            .await;
        let Some(image) = made else { continue };
        let made_at = image.size(0);
        let ok = this.update(cx, |v, cx| {
            // The frame it replaces leaves every window's atlas now, drawn
            // or not: one texture per desktop at a time.
            if let Some(old) = v.image.replace(image) {
                cx.drop_image(old, None);
            }
            v.drawn = false;
            v.pending = false;
            v.size = (w, h);
            v.image_size = (made_at.width.0 as u32, made_at.height.0 as u32);
            v.link = Link::Live;
            v.failures = 0;
            cx.notify();
        });
        if ok.is_err() {
            return String::new();
        }
    }
    agentbox_i18n::t("native.vnc.closed")
}

/// run speaks RFB over the stream until either side ends it, and says why
/// it ended.
async fn run(
    mut stream: agentbox_api::stream::Stream,
    fb: Arc<Mutex<Framebuffer>>,
    mut input: mpsc::UnboundedReceiver<X11Event>,
    frames: &mpsc::UnboundedSender<Result<(), String>>,
) -> String {
    // vnc-rs wants a byte stream: bridge one end of a pipe to the WebSocket.
    let (ours, theirs) = tokio::io::duplex(1 << 20);
    let (mut rd, mut wr) = tokio::io::split(theirs);
    let tx = stream.tx.clone();
    tokio::spawn(async move {
        let mut buf = vec![0u8; 64 * 1024];
        while let Ok(n) = rd.read(&mut buf).await {
            if n == 0 || tx.send(Out::Bytes(buf[..n].to_vec())).is_err() {
                break;
            }
        }
    });
    // Why the WebSocket closed, for when that's what ended it.
    let closed = Arc::new(Mutex::new(None::<String>));
    tokio::spawn({
        let closed = closed.clone();
        async move {
            loop {
                match stream.rx.recv().await {
                    Some(In::Bytes(b)) => {
                        if wr.write_all(&b).await.is_err() {
                            break;
                        }
                    }
                    Some(In::Closed(why)) => {
                        *closed.lock().unwrap() = Some(why);
                        break;
                    }
                    None => break,
                }
            }
            // Ends vnc-rs's reading too.
            let _ = wr.shutdown().await;
        }
    });
    let ended = |e: String| {
        let why = closed.lock().unwrap().clone().filter(|w| !w.is_empty());
        agentbox_i18n::tv("native.vnc.ended", &[("reason", why.unwrap_or(e).into())])
    };
    let started = async {
        VncConnector::new(ours)
            .set_auth_method(async { Ok(String::new()) })
            .add_encoding(VncEncoding::Tight)
            .add_encoding(VncEncoding::Zrle)
            .add_encoding(VncEncoding::CopyRect)
            .add_encoding(VncEncoding::Raw)
            .add_encoding(VncEncoding::DesktopSizePseudo)
            .allow_shared(true)
            .set_pixel_format(PixelFormat::bgra())
            .build()?
            .try_start()
            .await?
            .finish()
    };
    let started = tokio::time::timeout(Duration::from_secs(15), started).await;
    let vnc = match started {
        Ok(Ok(v)) => v,
        Ok(Err(e)) => return ended(e.to_string()),
        Err(_) => return agentbox_i18n::t("native.vnc.noAnswer"),
    };
    let mut tick = tokio::time::interval(Duration::from_millis(16));
    loop {
        tokio::select! {
            ev = vnc.recv_event() => {
                let ev = match ev {
                    Ok(ev) => ev,
                    Err(e) => return ended(e.to_string()),
                };
                // A new size: the whole screen again, not only what changes.
                let mut resized = false;
                {
                    let mut f = fb.lock().unwrap();
                    match ev {
                        VncEvent::SetResolution(s) => {
                            resized = (s.width as u32, s.height as u32) != (f.width, f.height);
                            f.resize(s.width as u32, s.height as u32);
                        }
                        VncEvent::DesktopUpdate(u) => {
                            if let Some(l) = u.layout
                                && (l.width as u32, l.height as u32) != (f.width, f.height) {
                                    f.resize(l.width as u32, l.height as u32);
                                    resized = true;
                                }
                        }
                        VncEvent::RawImage(r, data) => f.blit(&r, &data),
                        VncEvent::Copy(dst, src) => f.copy(&dst, &src),
                        VncEvent::Error(e) => return ended(e),
                        _ => {}
                    }
                }
                if resized && vnc.input(X11Event::FullRefresh).await.is_err() {
                    return ended(String::new());
                }
            }
            Some(ev) = input.recv() => {
                if let Err(e) = vnc.input(ev).await {
                    return ended(e.to_string());
                }
            }
            _ = tick.tick() => {
                let dirty = std::mem::take(&mut fb.lock().unwrap().dirty);
                if dirty && frames.send(Ok(())).is_err() {
                    // The view is gone.
                    let _ = vnc.close().await;
                    return String::new();
                }
                if let Err(e) = vnc.input(X11Event::Refresh).await {
                    return ended(e.to_string());
                }
            }
        }
    }
}

impl Render for VncView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.drawn = true;
        // A frame waited for this draw: send for it.
        if std::mem::take(&mut self.pending)
            && let Some(f) = &self.frames
        {
            f.send(Ok(())).ok();
        }
        let shown = self.shown.clone();
        let (w, h) = self.size;
        let live = self.link == Link::Live;
        div()
            .id("vnc")
            .track_focus(&self.focus)
            .relative()
            .size_full()
            .bg(theme::pitch())
            .flex()
            .items_center()
            .justify_center()
            .on_mouse_move(cx.listener(|this, ev: &MouseMoveEvent, _, _| {
                let b = this.buttons;
                this.pointer(ev.position, b);
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, ev: &MouseDownEvent, window, cx| {
                    this.focus.focus(window, cx);
                    if this.inside(ev.position) {
                        this.press(ev.position, 1, true);
                    }
                }),
            )
            .on_mouse_down(
                MouseButton::Middle,
                cx.listener(|this, ev: &MouseDownEvent, _, _| {
                    if this.inside(ev.position) {
                        this.press(ev.position, 2, true);
                    }
                }),
            )
            .on_mouse_down(
                MouseButton::Right,
                cx.listener(|this, ev: &MouseDownEvent, _, _| {
                    if this.inside(ev.position) {
                        this.press(ev.position, 4, true);
                    }
                }),
            )
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(|this, ev: &MouseUpEvent, _, _| this.press(ev.position, 1, false)),
            )
            .on_mouse_up(
                MouseButton::Middle,
                cx.listener(|this, ev: &MouseUpEvent, _, _| this.press(ev.position, 2, false)),
            )
            .on_mouse_up(
                MouseButton::Right,
                cx.listener(|this, ev: &MouseUpEvent, _, _| this.press(ev.position, 4, false)),
            )
            // A drag let go of outside the view lets go on the screen too.
            .on_mouse_up_out(
                MouseButton::Left,
                cx.listener(|this, ev: &MouseUpEvent, _, _| {
                    if this.buttons & 1 != 0 {
                        this.press(ev.position, 1, false);
                    }
                }),
            )
            .on_scroll(cx.listener(|this, ev: &ScrollEvent, _, _| {
                if this.inside(ev.position) {
                    this.wheel(ev.position, ev.delta.pixel_delta(px(20.)));
                }
            }))
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                this.key(&ev.keystroke, true);
                if this.control {
                    cx.stop_propagation();
                }
            }))
            .on_key_up(cx.listener(|this, ev: &KeyUpEvent, _, _| this.key(&ev.keystroke, false)))
            .on_modifiers_changed(
                cx.listener(|this, ev: &ModifiersChangedEvent, _, _| this.modifiers(ev.modifiers)),
            )
            .when_some(self.image.clone(), |el, image| {
                let want = self.want.clone();
                let frames = self.frames.clone();
                let made_at = self.image_size;
                el.child(
                    div()
                        .absolute()
                        .inset_0()
                        // The last frame stays while it reconnects, dimmed.
                        .when(!live, |el| el.opacity(0.35))
                        .child(
                            // Laid out and drawn here, rather than by img(),
                            // so where it's drawn, the size its picture is
                            // made at and where the pointer lands all come
                            // from one fit, of this frame's bounds.
                            canvas(
                                move |b, window, _| {
                                    let f = fit(b, (w, h), window.scale_factor());
                                    *shown.lock().unwrap() = f.map(|f| f.bounds);
                                    let f = f?;
                                    // Drawn at another size than its picture
                                    // was made at (the window or the panel
                                    // resized): ask for it again at this one.
                                    let mut want = want.lock().unwrap();
                                    if *want != Some(f.image) {
                                        *want = Some(f.image);
                                        if f.image != made_at
                                            && let Some(tx) = &frames
                                        {
                                            tx.send(Ok(())).ok();
                                        }
                                    }
                                    Some(f)
                                },
                                move |_, f, window, _| {
                                    if let Some(f) = f {
                                        window
                                            .paint_image(
                                                f.bounds,
                                                f.bounds,
                                                Corners::default(),
                                                image,
                                                0,
                                                false,
                                            )
                                            .ok();
                                    }
                                },
                            )
                            .size_full(),
                        ),
                )
            })
            .when(!live, |el| el.child(self.render_status(cx)))
    }
}

impl VncView {
    /// render_status says, over the screen, that it's connecting, or why it
    /// can't show the desktop and that it's trying again.
    fn render_status(&self, cx: &mut Context<Self>) -> AnyElement {
        let body = match &self.link {
            Link::Down(reason) => v_flex()
                .items_center()
                .gap_1p5()
                .max_w(px(420.))
                .child(
                    h_flex()
                        .gap_1p5()
                        .text_sm()
                        .text_color(theme::primary())
                        .child(crate::icons::of("⚠"))
                        .child(agentbox_i18n::t("native.vnc.cantShow")),
                )
                .when(!reason.is_empty(), |el| {
                    el.child(
                        div()
                            .id("vnc-reason")
                            .text_xs()
                            .text_center()
                            .text_color(theme::muted())
                            .child(reason.clone()),
                    )
                })
                .child(
                    h_flex()
                        .gap_2()
                        .items_center()
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::faint())
                                .child(agentbox_i18n::t("native.vnc.retrying")),
                        )
                        .child(
                            div()
                                .id("vnc-retry")
                                .px_2()
                                .py_0p5()
                                .rounded_md()
                                .border_1()
                                .border_color(theme::line())
                                .text_xs()
                                .text_color(theme::primary())
                                .cursor_pointer()
                                .hover(|s| s.bg(theme::surface_strong()))
                                .active(|s| s.bg(theme::surface_vivid()))
                                .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                    this.retry();
                                    cx.notify();
                                }))
                                .child(agentbox_i18n::t("native.vnc.retryNow")),
                        ),
                )
                .into_any_element(),
            _ => div()
                .text_sm()
                .text_color(theme::muted())
                .child(agentbox_i18n::t("agent.browser.connecting"))
                .into_any_element(),
        };
        div()
            .absolute()
            .inset_0()
            .flex()
            .items_center()
            .justify_center()
            .px_4()
            .child(body)
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    // Not super::*: gpui's own `test` attribute would come with it.
    use super::{Fit, backoff, fit, keysym, picture, reason, remote_point, shrink};
    use gpui::{Bounds, Keystroke, point, px, size};
    use std::time::Duration;

    fn ks(key: &str, ch: Option<&str>) -> Keystroke {
        Keystroke {
            key: key.into(),
            key_char: ch.map(Into::into),
            ..Default::default()
        }
    }

    #[test]
    fn the_daemons_refusal_reads_without_its_status() {
        let e = anyhow::anyhow!(
            "400 Bad Request: demo/agent-02's desktop isn't running: start it first"
        );
        assert_eq!(
            reason(&e),
            "demo/agent-02's desktop isn't running: start it first"
        );
        let e = anyhow::anyhow!("connecting to /t/x.sock: Connection refused (os error 111)");
        assert_eq!(
            reason(&e),
            "connecting to /t/x.sock: Connection refused (os error 111)"
        );
    }

    #[test]
    fn keys_map_to_keysyms() {
        assert_eq!(keysym(&ks("a", Some("A"))), Some(0x41));
        assert_eq!(keysym(&ks("a", None)), Some(0x61));
        assert_eq!(keysym(&ks("enter", None)), Some(0xff0d));
        assert_eq!(keysym(&ks("f5", None)), Some(0xffc2));
        assert_eq!(keysym(&ks("é", Some("é"))), Some(0xe9));
        assert_eq!(keysym(&ks("€", Some("€"))), Some(0x0100_20ac));
        // Modifiers go as they change, not as keys.
        assert_eq!(keysym(&ks("shift", None)), None);
    }

    #[test]
    fn it_tries_again_every_two_seconds_then_less_often() {
        assert_eq!(backoff(1), Duration::from_secs(2));
        assert_eq!(backoff(5), Duration::from_secs(2));
        assert_eq!(backoff(6), Duration::from_secs(5));
    }

    fn area(x: f32, y: f32, w: f32, h: f32) -> Bounds<gpui::Pixels> {
        Bounds {
            origin: point(px(x), px(y)),
            size: size(px(w), px(h)),
        }
    }

    #[test]
    fn the_screen_fits_keeping_its_shape_centred() {
        // Wider than the screen's shape: bars left and right.
        let f = fit(area(10., 20., 800., 400.), (1280, 1024), 1.).unwrap();
        assert_eq!(f.bounds, area(160., 20., 500., 400.));
        assert_eq!(f.image, (500, 400));
        // Taller: bars above and below.
        let f = fit(area(0., 0., 536., 800.), (1440, 900), 1.).unwrap();
        assert_eq!(f.bounds, area(0., 233., 536., 335.));
        // The panel's 16:10 box shows a 16:10 screen edge to edge.
        let f = fit(area(0., 0., 536., 335.), (1440, 900), 1.).unwrap();
        assert_eq!(f.bounds, area(0., 0., 536., 335.));
        assert_eq!(f.image, (536, 335));
    }

    #[test]
    fn it_sits_on_whole_device_pixels() {
        // 301 - 250 leaves an odd margin: rounded, not split in halves.
        let f = fit(area(0., 0., 301., 200.), (1280, 1024), 1.).unwrap();
        assert_eq!(f.bounds, area(26., 0., 250., 200.));
        // At 2x a window pixel is two device pixels: halves are whole.
        let f = fit(area(0., 0., 400.25, 250.), (1280, 800), 2.).unwrap();
        assert_eq!(f.bounds, area(0., 0., 400., 250.));
        assert_eq!(f.image, (800, 500));
        let f = fit(area(0., 0., 400.75, 250.), (1280, 800), 2.).unwrap();
        assert_eq!(f.bounds.origin.x, px(0.5));
    }

    #[test]
    fn a_screen_shown_larger_is_scaled_up_from_its_own_size() {
        let f = fit(area(0., 0., 1920., 1080.), (1024, 768), 1.).unwrap();
        assert_eq!(f.bounds, area(240., 0., 1440., 1080.));
        assert_eq!(f.image, (1024, 768));
        // Exactly its size: its own pixels, one for one.
        let f = fit(area(0., 0., 1024., 768.), (1024, 768), 1.).unwrap();
        assert_eq!(f.bounds, area(0., 0., 1024., 768.));
        assert_eq!(f.image, (1024, 768));
    }

    #[test]
    fn nothing_fits_in_nothing() {
        assert_eq!(fit(area(0., 0., 0., 300.), (1280, 800), 1.), None::<Fit>);
        assert_eq!(fit(area(0., 0., 300., 300.), (0, 0), 1.), None);
    }

    #[test]
    fn the_pointer_lands_on_the_pixel_under_it() {
        let shown = area(160., 20., 500., 400.); // 1280x1024 at 0.390625
        assert_eq!(
            remote_point(shown, (1280, 1024), point(px(160.), px(20.))),
            Some((0, 0))
        );
        assert_eq!(
            remote_point(shown, (1280, 1024), point(px(410.), px(220.))),
            Some((640, 512))
        );
        assert_eq!(
            remote_point(shown, (1280, 1024), point(px(659.9), px(419.9))),
            Some((1279, 1023))
        );
        // Off the picture (the bars, or a drag past the edge): its edge.
        assert_eq!(
            remote_point(shown, (1280, 1024), point(px(0.), px(500.))),
            Some((0, 1023))
        );
        assert_eq!(
            remote_point(shown, (1280, 1024), point(px(900.), px(-5.))),
            Some((1279, 0))
        );
        // Scaled up 2x: each remote pixel is two window pixels.
        let shown = area(0., 0., 2048., 1536.);
        assert_eq!(
            remote_point(shown, (1024, 768), point(px(3.), px(5.))),
            Some((1, 2))
        );
        assert_eq!(remote_point(shown, (0, 0), point(px(3.), px(5.))), None);
    }

    #[test]
    fn pointer_and_picture_agree_after_any_resize() {
        // Wherever the fit puts it, its corners map to the screen's.
        for (aw, ah) in [
            (340., 212.5),
            (536., 335.),
            (1200., 300.),
            (300., 1200.),
            (1913., 1041.),
        ] {
            for sf in [1., 1.25, 2.] {
                let f = fit(area(37., 11., aw, ah), (1440, 900), sf).unwrap();
                let b = f.bounds;
                assert_eq!(remote_point(b, (1440, 900), b.origin), Some((0, 0)));
                let end = b.origin + point(b.size.width - px(0.01), b.size.height - px(0.01));
                assert_eq!(remote_point(b, (1440, 900), end), Some((1439, 899)));
                // Never out of the area, never stretched past a pixel.
                assert!(b.origin.x >= px(37. - 0.5) && b.origin.y >= px(11. - 0.5));
                // Its shape off by at most the rounding of a device pixel.
                let (dw, dh) = (f.image.0 as f32, f.image.1 as f32);
                assert!(
                    (dw / dh - 1.6).abs() <= 2. / dh,
                    "{aw}x{ah}@{sf}: {dw}x{dh}"
                );
            }
        }
    }

    #[test]
    fn shrinking_averages_what_each_pixel_covers() {
        // 4x2 of black and white columns, halved: grey, opaque.
        let mut src = Vec::new();
        for _ in 0..2 {
            for x in 0..4 {
                let v = if x % 2 == 0 { 0 } else { 255 };
                src.extend_from_slice(&[v, v, v, 0]);
            }
        }
        assert_eq!(
            shrink(&src, 4, 2, 2, 1),
            vec![128, 128, 128, 255, 128, 128, 128, 255]
        );
        // Its channels stay apart.
        let src = [10, 20, 30, 0, 30, 40, 50, 0];
        assert_eq!(shrink(&src, 2, 1, 1, 1), vec![20, 30, 40, 255]);
        // An uneven ratio covers every source pixel once.
        let src: Vec<u8> = (0..7u8).flat_map(|i| [i * 30, 0, 0, 0]).collect();
        let out = shrink(&src, 7, 1, 3, 1);
        assert_eq!(out.len(), 12);
        assert_eq!([out[0], out[4], out[8]], [15, 75, 150]);
    }

    #[test]
    fn the_picture_is_made_at_the_size_it_is_drawn() {
        let px = vec![200u8; 8 * 4 * 4];
        let i = picture(px.clone(), 8, 4, Some((4, 2))).unwrap();
        assert_eq!((i.size(0).width.0, i.size(0).height.0), (4, 2));
        // Not larger than the screen, nor for another shape of it.
        let i = picture(px.clone(), 8, 4, Some((16, 8))).unwrap();
        assert_eq!((i.size(0).width.0, i.size(0).height.0), (8, 4));
        let i = picture(px.clone(), 8, 4, Some((4, 4))).unwrap();
        assert_eq!((i.size(0).width.0, i.size(0).height.0), (8, 4));
        let i = picture(px, 8, 4, None).unwrap();
        assert_eq!((i.size(0).width.0, i.size(0).height.0), (8, 4));
    }
}
