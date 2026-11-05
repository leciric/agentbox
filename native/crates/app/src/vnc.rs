//! An agent's desktop: its VNC server through the daemon
//! (GET /v1/agents/<p>/<a>/browser/view, RFB bytes in WebSocket frames, as
//! noVNC reads it in the Electron app). vnc-rs speaks RFB on the I/O runtime
//! into a framebuffer, and GPUI shows it as an image, sending the pointer and
//! keys back.
use std::sync::{Arc, Mutex};
use std::time::Duration;

use agentbox_api::Client;
use agentbox_api::stream::{In, Out};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
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

pub struct VncView {
    image: Option<Arc<RenderImage>>,
    stale: Vec<Arc<RenderImage>>,
    size: (u32, u32),
    status: Option<SharedString>,
    input: Option<mpsc::UnboundedSender<X11Event>>,
    /// Where the image was drawn, for mapping the pointer onto the screen.
    shown: Arc<Mutex<Option<Bounds<Pixels>>>>,
    buttons: u8,
    focus: FocusHandle,
    /// Whether the pointer and keys go to the remote screen: off, it is only
    /// watched (the panel's "Take control" switch).
    pub control: bool,
    _task: Task<()>,
}

impl VncView {
    pub fn new(client: Client, path: String, cx: &mut Context<Self>) -> Self {
        let fb = Arc::new(Mutex::new(Framebuffer::default()));
        let (input_tx, input_rx) = mpsc::unbounded_channel();
        let (frame_tx, mut frame_rx) = mpsc::unbounded_channel::<Result<(), String>>();
        let task = cx.spawn({
            let fb = fb.clone();
            async move |this, cx| {
                let stream = match client.stream(&path).await {
                    Ok(s) => s,
                    Err(e) => {
                        this.update(cx, |v, cx| {
                            v.status = Some(e.to_string().into());
                            cx.notify();
                        })
                        .ok();
                        return;
                    }
                };
                client.spawn(run(stream, fb.clone(), input_rx, frame_tx));
                this.update(cx, |v, cx| {
                    v.status = Some(agentbox_i18n::t("device.connecting").into());
                    cx.notify();
                })
                .ok();
                while let Some(msg) = frame_rx.recv().await {
                    // Skip to the newest frame.
                    let mut last = msg;
                    while let Ok(m) = frame_rx.try_recv() {
                        last = m;
                    }
                    let frame = match last {
                        Ok(()) => {
                            let mut fb = fb.lock().unwrap();
                            fb.dirty = false;
                            let mut px = fb.pixels.clone();
                            for a in px.iter_mut().skip(3).step_by(4) {
                                *a = 255;
                            }
                            image::RgbaImage::from_raw(fb.width, fb.height, px)
                                .map(|i| (i, fb.width, fb.height))
                        }
                        Err(e) => {
                            this.update(cx, |v, cx| {
                                v.status = Some(e.into());
                                cx.notify();
                            })
                            .ok();
                            return;
                        }
                    };
                    let Some((img, w, h)) = frame else { continue };
                    let image = Arc::new(RenderImage::new(vec![image::Frame::new(img)]));
                    let ok = this.update(cx, |v, cx| {
                        if let Some(old) = v.image.replace(image) {
                            v.stale.push(old);
                        }
                        v.size = (w, h);
                        v.status = None;
                        cx.notify();
                    });
                    if ok.is_err() {
                        return;
                    }
                }
            }
        });
        Self {
            image: None,
            stale: Vec::new(),
            size: (0, 0),
            status: Some(agentbox_i18n::t("device.connecting").into()),
            input: Some(input_tx),
            shown: Arc::default(),
            buttons: 0,
            focus: cx.focus_handle(),
            control: true,
            _task: task,
        }
    }

    pub fn focus_handle(&self) -> &FocusHandle {
        &self.focus
    }

    /// connected is whether a frame of the remote screen has arrived.
    pub fn connected(&self) -> bool {
        self.image.is_some()
    }

    /// to_remote maps a window point onto the remote screen.
    fn to_remote(&self, p: Point<Pixels>) -> Option<(u16, u16)> {
        let b = (*self.shown.lock().unwrap())?;
        let (w, h) = self.size;
        if w == 0 || !b.contains(&p) {
            return None;
        }
        let x = (p.x - b.origin.x) / b.size.width * w as f32;
        let y = (p.y - b.origin.y) / b.size.height * h as f32;
        Some((
            x.clamp(0., w as f32 - 1.) as u16,
            y.clamp(0., h as f32 - 1.) as u16,
        ))
    }

    fn pointer(&mut self, p: Point<Pixels>, buttons: u8) {
        self.buttons = buttons;
        if !self.control {
            return;
        }
        if let (Some((x, y)), Some(tx)) = (self.to_remote(p), &self.input) {
            let _ = tx.send(X11Event::PointerEvent(ClientMouseEvent {
                position_x: x,
                position_y: y,
                bottons: buttons,
            }));
        }
    }

    fn key(&self, ks: &Keystroke, down: bool) {
        if !self.control {
            return;
        }
        let Some(tx) = &self.input else { return };
        let send = |k: u32, d: bool| {
            let _ = tx.send(X11Event::KeyEvent(ClientKeyEvent {
                keycode: k,
                down: d,
            }));
        };
        if down {
            for (on, k) in [
                (ks.modifiers.control, 0xffe3),
                (ks.modifiers.alt, 0xffe9),
                (ks.modifiers.shift, 0xffe1),
            ] {
                if on {
                    send(k, true);
                }
            }
        }
        if let Some(sym) = keysym(ks) {
            send(sym, down);
        }
        if !down {
            for (on, k) in [
                (ks.modifiers.control, 0xffe3),
                (ks.modifiers.alt, 0xffe9),
                (ks.modifiers.shift, 0xffe1),
            ] {
                if on {
                    send(k, false);
                }
            }
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
        "home" => 0xff50,
        "left" => 0xff51,
        "up" => 0xff52,
        "right" => 0xff53,
        "down" => 0xff54,
        "pageup" => 0xff55,
        "pagedown" => 0xff56,
        "end" => 0xff57,
        "space" => 0x20,
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

/// run speaks RFB over the stream until either side ends it.
async fn run(
    mut stream: agentbox_api::stream::Stream,
    fb: Arc<Mutex<Framebuffer>>,
    mut input: mpsc::UnboundedReceiver<X11Event>,
    frames: mpsc::UnboundedSender<Result<(), String>>,
) {
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
    tokio::spawn(async move {
        while let Some(In::Bytes(b)) = stream.rx.recv().await {
            if wr.write_all(&b).await.is_err() {
                break;
            }
        }
    });
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
    let vnc = match started.await {
        Ok(v) => v,
        Err(e) => {
            let _ = frames.send(Err(format!("VNC: {e}")));
            return;
        }
    };
    let mut tick = tokio::time::interval(Duration::from_millis(16));
    loop {
        tokio::select! {
            ev = vnc.recv_event() => {
                let ev = match ev {
                    Ok(ev) => ev,
                    Err(e) => {
                        let _ = frames.send(Err(format!("the desktop closed: {e}")));
                        return;
                    }
                };
                let mut f = fb.lock().unwrap();
                match ev {
                    VncEvent::SetResolution(s) => f.resize(s.width as u32, s.height as u32),
                    VncEvent::DesktopUpdate(u) => {
                        if let Some(l) = u.layout
                            && (l.width as u32, l.height as u32) != (f.width, f.height) {
                                f.resize(l.width as u32, l.height as u32);
                            }
                    }
                    VncEvent::RawImage(r, data) => f.blit(&r, &data),
                    VncEvent::Copy(dst, src) => f.copy(&dst, &src),
                    VncEvent::Error(e) => {
                        let _ = frames.send(Err(e));
                        return;
                    }
                    _ => {}
                }
            }
            Some(ev) = input.recv() => {
                if vnc.input(ev).await.is_err() {
                    return;
                }
            }
            _ = tick.tick() => {
                let dirty = std::mem::take(&mut fb.lock().unwrap().dirty);
                if dirty && frames.send(Ok(())).is_err() {
                    let _ = vnc.close().await;
                    return;
                }
                if vnc.input(X11Event::Refresh).await.is_err() {
                    return;
                }
            }
        }
    }
}

impl Render for VncView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        for old in self.stale.drain(..) {
            let _ = window.drop_image(old);
        }
        let shown = self.shown.clone();
        let (w, h) = self.size;
        div()
            .id("vnc")
            .track_focus(&self.focus)
            .size_full()
            .bg(gpui::black())
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
                    this.pointer(ev.position, 1);
                }),
            )
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(|this, ev: &MouseUpEvent, _, _| this.pointer(ev.position, 0)),
            )
            .on_mouse_down(
                MouseButton::Right,
                cx.listener(|this, ev: &MouseDownEvent, _, _| this.pointer(ev.position, 4)),
            )
            .on_mouse_up(
                MouseButton::Right,
                cx.listener(|this, ev: &MouseUpEvent, _, _| this.pointer(ev.position, 0)),
            )
            .on_scroll(cx.listener(|this, ev: &ScrollEvent, _, _| {
                let dy = ev.delta.pixel_delta(px(20.)).y;
                let button = if dy > px(0.) { 8 } else { 16 };
                let p = ev.position;
                this.pointer(p, button);
                this.pointer(p, 0);
            }))
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                this.key(&ev.keystroke, true);
                if this.control {
                    cx.stop_propagation();
                }
            }))
            .on_key_up(cx.listener(|this, ev: &KeyUpEvent, _, _| this.key(&ev.keystroke, false)))
            .when_some(self.image.clone(), |el, image| {
                el.child(
                    div()
                        .relative()
                        .size_full()
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
            .when_some(self.status.clone(), |el, s| {
                el.child(div().text_sm().text_color(theme::muted()).child(s))
            })
    }
}
