//! A device's screen, live (components/device/DeviceStreamView.tsx): the
//! daemon's device stream (internal/devicestream), H.264 decoded by FFmpeg
//! as the review's recordings are, and with Take control the pointer as a
//! finger, the wheel, the keyboard, and the device's own buttons.
//!
//! The wire format, version 1: binary messages from the server, 0x01 a
//! config (JSON, with the avcC as base64), 0x02 a frame (flags, an 8-byte
//! timestamp, an access unit in AVCC form), 0x03 a status ({"error"}); JSON
//! text messages back.
use std::sync::{Arc, Mutex};

use agentbox_api::Client;
use agentbox_api::stream::{In, Out};
use anyhow::{Result, anyhow};
use base64::Engine as _;
use ffmpeg_next as ff;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};
use tokio::sync::mpsc;

use crate::theme;

/// The widest a frame is scaled to before it's shown.
const MAX_WIDTH: u32 = 1080;

pub struct DeviceView {
    image: Option<Arc<RenderImage>>,
    stale: Vec<Arc<RenderImage>>,
    size: (u32, u32),
    status: Option<SharedString>,
    input: Option<mpsc::UnboundedSender<Out>>,
    shown: Arc<Mutex<Option<Bounds<Pixels>>>>,
    down: bool,
    focus: FocusHandle,
    pub control: bool,
    _task: Task<()>,
}

/// Parsed is one server message.
enum Parsed {
    Config(Vec<u8>),
    Frame(Vec<u8>),
    Status(String),
}

fn parse(data: &[u8]) -> Option<Parsed> {
    match *data.first()? {
        0x01 => {
            let v: serde_json::Value = serde_json::from_slice(&data[1..]).ok()?;
            let d = v.get("description")?.as_str()?;
            base64::engine::general_purpose::STANDARD
                .decode(d)
                .ok()
                .map(Parsed::Config)
        }
        0x02 if data.len() >= 10 => Some(Parsed::Frame(data[10..].to_vec())),
        0x03 => {
            let v: serde_json::Value = serde_json::from_slice(&data[1..]).ok()?;
            Some(Parsed::Status(
                v.get("error")?.as_str().unwrap_or_default().to_string(),
            ))
        }
        _ => None,
    }
}

/// Decoder turns access units into frames: one per config.
struct Decoder {
    dec: ff::decoder::Video,
    scaler: Option<(ff::software::scaling::Context, u32, u32, u32, u32)>,
}

impl Decoder {
    fn new(avcc: &[u8]) -> Result<Self> {
        let codec = ff::decoder::find(ff::codec::Id::H264).ok_or_else(|| anyhow!("no H.264"))?;
        let mut ctx = ff::codec::context::Context::new_with_codec(codec);
        // SAFETY: extradata is FFmpeg's to free, so it's allocated by FFmpeg,
        // with the padding its readers may read past the end into.
        unsafe {
            let p = ctx.as_mut_ptr();
            let pad = ff::ffi::AV_INPUT_BUFFER_PADDING_SIZE as usize;
            let buf = ff::ffi::av_mallocz(avcc.len() + pad) as *mut u8;
            std::ptr::copy_nonoverlapping(avcc.as_ptr(), buf, avcc.len());
            (*p).extradata = buf;
            (*p).extradata_size = avcc.len() as i32;
        }
        Ok(Self {
            dec: ctx.decoder().video()?,
            scaler: None,
        })
    }

    fn decode(&mut self, au: &[u8]) -> Result<Option<image::RgbaImage>> {
        self.dec.send_packet(&ff::Packet::copy(au))?;
        let mut last = None;
        let mut frame = ff::frame::Video::empty();
        while self.dec.receive_frame(&mut frame).is_ok() {
            let (w, h) = (frame.width(), frame.height());
            let width = w.min(MAX_WIDTH);
            let height = (h as u64 * width as u64 / w.max(1) as u64) as u32 & !1;
            if self
                .scaler
                .as_ref()
                .is_none_or(|(_, sw, sh, _, _)| (*sw, *sh) != (w, h))
            {
                let s = ff::software::scaling::Context::get(
                    frame.format(),
                    w,
                    h,
                    ff::format::Pixel::BGRA,
                    width,
                    height,
                    ff::software::scaling::Flags::BILINEAR,
                )?;
                self.scaler = Some((s, w, h, width, height));
            }
            let (scaler, _, _, width, height) = self.scaler.as_mut().unwrap();
            let mut bgra = ff::frame::Video::empty();
            scaler.run(&frame, &mut bgra)?;
            let stride = bgra.stride(0);
            let data = bgra.data(0);
            let mut pixels = Vec::with_capacity((*width * *height * 4) as usize);
            for row in 0..*height as usize {
                pixels.extend_from_slice(&data[row * stride..row * stride + *width as usize * 4]);
            }
            last = image::RgbaImage::from_raw(*width, *height, pixels);
        }
        Ok(last)
    }
}

/// decode_loop runs on its own thread: messages in, frames (or why it
/// ended) out.
fn decode_loop(
    rx: std::sync::mpsc::Receiver<Vec<u8>>,
    tx: mpsc::UnboundedSender<Result<image::RgbaImage, String>>,
    keyframe: mpsc::UnboundedSender<Out>,
) {
    let mut dec: Option<Decoder> = None;
    let mut failed = false;
    while let Ok(msg) = rx.recv() {
        match parse(&msg) {
            Some(Parsed::Config(avcc)) => {
                failed = false;
                dec = match Decoder::new(&avcc) {
                    Ok(d) => Some(d),
                    Err(e) => {
                        let _ = tx.send(Err(e.to_string()));
                        None
                    }
                };
            }
            Some(Parsed::Frame(au)) if !failed => {
                let Some(d) = dec.as_mut() else { continue };
                match d.decode(&au) {
                    Ok(Some(img)) => {
                        if tx.send(Ok(img)).is_err() {
                            return;
                        }
                    }
                    Ok(None) => {}
                    // Nothing decodes until the keyframe asked for.
                    Err(_) => {
                        failed = true;
                        let _ = keyframe.send(Out::Text(r#"{"type":"keyframe"}"#.into()));
                    }
                }
            }
            Some(Parsed::Status(e)) => {
                let _ = tx.send(Err(e));
            }
            _ => {}
        }
    }
}

impl DeviceView {
    pub fn new(client: Client, path: String, control: bool, cx: &mut Context<Self>) -> Self {
        let task = cx.spawn(async move |this, cx| {
            let mut stream = match client.stream(&path).await {
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
            let (raw_tx, raw_rx) = std::sync::mpsc::channel::<Vec<u8>>();
            let (img_tx, mut img_rx) = mpsc::unbounded_channel();
            let keyframe = stream.tx.clone();
            std::thread::spawn(move || decode_loop(raw_rx, img_tx, keyframe));
            let input = stream.tx.clone();
            this.update(cx, |v, _| v.input = Some(input)).ok();
            client.spawn(async move {
                while let Some(m) = stream.rx.recv().await {
                    match m {
                        In::Bytes(b) => {
                            if raw_tx.send(b).is_err() {
                                return;
                            }
                        }
                        In::Closed(_) => return,
                    }
                }
            });
            while let Some(msg) = img_rx.recv().await {
                let mut last = msg;
                while let Ok(m) = img_rx.try_recv() {
                    last = m;
                }
                let ok = this.update(cx, |v, cx| {
                    match last {
                        Ok(img) => {
                            v.size = (img.width(), img.height());
                            let image = Arc::new(RenderImage::new(vec![image::Frame::new(img)]));
                            if let Some(old) = v.image.replace(image) {
                                v.stale.push(old);
                            }
                            v.status = None;
                        }
                        Err(e) => v.status = Some(e.into()),
                    }
                    cx.notify();
                });
                if ok.is_err() {
                    return;
                }
            }
        });
        Self {
            image: None,
            stale: Vec::new(),
            size: (0, 0),
            status: Some(agentbox_i18n::t("device.connecting").into()),
            input: None,
            shown: Arc::default(),
            down: false,
            focus: cx.focus_handle(),
            control,
            _task: task,
        }
    }

    /// send_text types text on the device (the Android tab's Send clipboard).
    pub fn send_text(&self, text: &str) {
        if !text.is_empty() {
            self.send(serde_json::json!({"type": "text", "text": text}));
        }
    }

    fn send(&self, v: serde_json::Value) {
        if let Some(tx) = &self.input {
            let _ = tx.send(Out::Text(v.to_string()));
        }
    }

    /// at is where p falls on the video, from 0 to 1 each way.
    fn at(&self, p: Point<Pixels>) -> Option<(f32, f32)> {
        let b = (*self.shown.lock().unwrap())?;
        let x = f32::from(p.x - b.origin.x) / f32::from(b.size.width);
        let y = f32::from(p.y - b.origin.y) / f32::from(b.size.height);
        (0. ..=1.).contains(&x).then_some(())?;
        (0. ..=1.).contains(&y).then_some(())?;
        Some((x, y))
    }

    fn touch(&mut self, action: &str, p: Point<Pixels>) {
        if !self.control {
            return;
        }
        let Some((x, y)) = self.at(p) else {
            if action == "up" && self.down {
                self.down = false;
                self.send(serde_json::json!({"type":"touch","action":"cancel","x":0,"y":0,"id":0}));
            }
            return;
        };
        match action {
            "down" => self.down = true,
            "move" if !self.down => return,
            "up" if !self.down => return,
            "up" => self.down = false,
            _ => {}
        }
        self.send(serde_json::json!({"type":"touch","action":action,"x":x,"y":y,"id":0}));
    }

    fn key(&self, k: &Keystroke) {
        if !self.control {
            return;
        }
        let named = match k.key.as_str() {
            "enter" => Some("Enter"),
            "backspace" => Some("Backspace"),
            "delete" => Some("Delete"),
            "tab" => Some("Tab"),
            "escape" => Some("Escape"),
            "left" => Some("ArrowLeft"),
            "right" => Some("ArrowRight"),
            "up" => Some("ArrowUp"),
            "down" => Some("ArrowDown"),
            "home" => Some("Home"),
            "end" => Some("End"),
            "pageup" => Some("PageUp"),
            "pagedown" => Some("PageDown"),
            _ => None,
        };
        if let Some(key) = named {
            for action in ["down", "up"] {
                self.send(serde_json::json!({"type":"key","action":action,"key":key}));
            }
        } else if let Some(text) = k.key_char.as_ref().filter(|_| !k.modifiers.control) {
            self.send(serde_json::json!({"type":"text","text":text}));
        }
    }
}

impl Render for DeviceView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        for old in self.stale.drain(..) {
            let _ = window.drop_image(old);
        }
        let shown = self.shown.clone();
        let (w, h) = self.size;
        let control = self.control;
        let screen = div()
            .id("device")
            .track_focus(&self.focus)
            .relative()
            .flex_1()
            .min_h_0()
            .w_full()
            .overflow_hidden()
            .flex()
            .items_center()
            .justify_center()
            .when(control, |el| el.cursor_pointer())
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, ev: &MouseDownEvent, window, cx| {
                    this.focus.focus(window, cx);
                    this.touch("down", ev.position);
                }),
            )
            .on_mouse_move(
                cx.listener(|this, ev: &MouseMoveEvent, _, _| this.touch("move", ev.position)),
            )
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(|this, ev: &MouseUpEvent, _, _| this.touch("up", ev.position)),
            )
            .on_scroll(cx.listener(|this, ev: &ScrollEvent, _, _| {
                if !this.control {
                    return;
                }
                if let Some((x, y)) = this.at(ev.position) {
                    let d = ev.delta.pixel_delta(px(20.));
                    let dy = -f32::from(d.y) / 40.;
                    let dx = -f32::from(d.x) / 40.;
                    this.send(serde_json::json!({"type":"wheel","x":x,"y":y,"dx":dx,"dy":dy}));
                }
            }))
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                this.key(&ev.keystroke);
                if this.control {
                    cx.stop_propagation();
                }
            }))
            .when_some(self.image.clone(), |el, image| {
                // Sized from where the last frame was fitted: an image fills
                // the box it's laid out in, whatever its own shape.
                let fit = (*self.shown.lock().unwrap()).map(|b| b.size);
                el.child(
                    div()
                        .absolute()
                        .inset_0()
                        .flex()
                        .items_center()
                        .justify_center()
                        .child(match fit {
                            Some(sz) => img(image).w(sz.width).h(sz.height).into_any_element(),
                            None => img(image)
                                .size_full()
                                .object_fit(ObjectFit::Contain)
                                .into_any_element(),
                        })
                        .child(
                            canvas(
                                move |b, window, _| {
                                    if w > 0 && h > 0 {
                                        let scale = (f32::from(b.size.width) / w as f32)
                                            .min(f32::from(b.size.height) / h as f32);
                                        let size = size(px(w as f32) * scale, px(h as f32) * scale);
                                        let origin = b.origin
                                            + point(
                                                (b.size.width - size.width) / 2.,
                                                (b.size.height - size.height) / 2.,
                                            );
                                        let next = Some(Bounds { origin, size });
                                        let mut was = shown.lock().unwrap();
                                        if was.map(|b| b.size) != next.map(|b| b.size) {
                                            window.refresh();
                                        }
                                        *was = next;
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
            });
        let button =
            |id: &'static str, glyph: &'static str, key: &'static str, which: &'static str| {
                let tip: SharedString = agentbox_i18n::t(key).into();
                div()
                    .id(id)
                    .px_3()
                    .py_1()
                    .rounded_md()
                    .text_sm()
                    .text_color(theme::muted())
                    .when(control, |el| {
                        el.cursor_pointer()
                            .hover(|s| s.bg(theme::hover()).text_color(theme::fg()))
                            .on_click(cx.listener(move |this, _: &ClickEvent, _, _| {
                                this.send(serde_json::json!({"type":"button","button":which}))
                            }))
                    })
                    .when(!control, |el| el.opacity(0.4))
                    .tooltip(move |w, cx| {
                        gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx)
                    })
                    .child(glyph)
            };
        v_flex().size_full().gap_2().child(screen).child(
            h_flex()
                .flex_none()
                .justify_center()
                .gap_2()
                .child(button("device-back", "◁", "device.button.back", "back"))
                .child(button("device-home", "○", "device.button.home", "home"))
                .child(button(
                    "device-recents",
                    "□",
                    "device.button.recents",
                    "recents",
                ))
                .child(button(
                    "device-rotate",
                    "⟳",
                    "device.button.rotate",
                    "rotate",
                )),
        )
    }
}
