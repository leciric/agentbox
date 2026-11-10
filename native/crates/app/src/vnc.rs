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
    size: (u32, u32),
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
        let (w, h) = self.size;
        if w == 0 {
            return None;
        }
        // Clamped, so a drag that leaves the picture still lands on its edge.
        let x = (p.x - b.origin.x) / b.size.width * w as f32;
        let y = (p.y - b.origin.y) / b.size.height * h as f32;
        Some((
            x.clamp(0., w as f32 - 1.) as u16,
            y.clamp(0., h as f32 - 1.) as u16,
        ))
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
        let frame = {
            let mut fb = fb.lock().unwrap();
            fb.dirty = false;
            let mut px = fb.pixels.clone();
            for a in px.iter_mut().skip(3).step_by(4) {
                *a = 255;
            }
            image::RgbaImage::from_raw(fb.width, fb.height, px).map(|i| (i, fb.width, fb.height))
        };
        let Some((img, w, h)) = frame else { continue };
        let image = Arc::new(RenderImage::new(vec![image::Frame::new(img)]));
        let ok = this.update(cx, |v, cx| {
            // The frame it replaces leaves every window's atlas now, drawn
            // or not: one texture per desktop at a time.
            if let Some(old) = v.image.replace(image) {
                cx.drop_image(old, None);
            }
            v.drawn = false;
            v.pending = false;
            v.size = (w, h);
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
                el.child(
                    div()
                        .relative()
                        .size_full()
                        // The last frame stays while it reconnects, dimmed.
                        .when(!live, |el| el.opacity(0.35))
                        .child(img(image).size_full().object_fit(ObjectFit::Contain))
                        .child(
                            canvas(
                                move |b, _, _| {
                                    // The image is fitted, centred, in b: where it actually is.
                                    if w > 0 && h > 0 {
                                        let scale = (f32::from(b.size.width) / w as f32)
                                            .min(f32::from(b.size.height) / h as f32);
                                        let size = size(px(w as f32) * scale, px(h as f32) * scale);
                                        let origin = b.origin
                                            + point(
                                                (b.size.width - size.width) / 2.,
                                                (b.size.height - size.height) / 2.,
                                            );
                                        *shown.lock().unwrap() = Some(Bounds { origin, size });
                                    }
                                },
                                |_, _, _, _| {},
                            )
                            .absolute()
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
    use super::{backoff, keysym, reason};
    use gpui::Keystroke;
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
}
