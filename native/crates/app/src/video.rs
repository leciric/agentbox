//! A video player for the recordings agents make (H.264 in MP4): FFmpeg
//! demuxes and decodes on a thread of its own, scales each frame to BGRA, and
//! GPUI shows each one as an image when its time comes.
use std::cell::Cell;
use std::path::PathBuf;
use std::rc::Rc;
use std::sync::Arc;
use std::time::{Duration, Instant};

use anyhow::{Context as _, Result, anyhow};
use ffmpeg_next as ff;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use crate::{bench, theme};

/// The widest a frame is decoded to: the player is never wider.
const MAX_WIDTH: u32 = 1440;

struct Decoded {
    pts: f64,
    image: image::RgbaImage,
}

pub struct VideoPlayer {
    path: PathBuf,
    frame: Option<Arc<RenderImage>>,
    /// Frames shown and replaced, whose textures go back to GPUI next render.
    stale: Vec<Arc<RenderImage>>,
    playing: bool,
    position: f64,
    duration: f64,
    shown: usize,
    error: Option<SharedString>,
    /// Points on the bar to mark (a review's chapters), in seconds.
    marks: Vec<f64>,
    /// Where the bar was last drawn, so a click on it seeks.
    bar: Rc<Cell<Bounds<Pixels>>>,
    _task: Option<Task<()>>,
}

impl VideoPlayer {
    pub fn new(path: PathBuf, cx: &mut Context<Self>) -> Self {
        let mut this = Self {
            path,
            frame: None,
            stale: Vec::new(),
            playing: false,
            position: 0.,
            duration: 0.,
            shown: 0,
            error: None,
            marks: Vec::new(),
            bar: Rc::default(),
            _task: None,
        };
        this.play(cx);
        this
    }

    /// set_marks puts marks on the bar at these seconds.
    pub fn set_marks(&mut self, marks: Vec<f64>, cx: &mut Context<Self>) {
        self.marks = marks;
        cx.notify();
    }

    pub fn play(&mut self, cx: &mut Context<Self>) {
        self.start(0., true, cx);
    }

    /// toggle pauses, or plays on from where it stopped (from the start once it ended).
    pub fn toggle(&mut self, cx: &mut Context<Self>) {
        if self.playing {
            self.pause(cx);
        } else if self.position >= self.duration - 0.05 {
            self.start(0., true, cx);
        } else {
            self.start(self.position, true, cx);
        }
    }

    pub fn pause(&mut self, cx: &mut Context<Self>) {
        // Dropping the task drops the frames' channel, which ends the decoder.
        self._task = None;
        self.playing = false;
        cx.notify();
    }

    /// seek goes to at seconds in, playing on if it was playing, else
    /// showing the frame there.
    pub fn seek(&mut self, at: f64, cx: &mut Context<Self>) {
        let at = at.clamp(0., self.duration.max(0.));
        self.position = at;
        let playing = self.playing;
        self.start(at, playing, cx);
    }

    /// start decodes from at seconds in, showing frames as their time comes,
    /// or only the first one when not playing.
    fn start(&mut self, at: f64, playing: bool, cx: &mut Context<Self>) {
        let (tx, mut rx) = tokio::sync::mpsc::channel::<Result<Decoded>>(8);
        let path = self.path.clone();
        match probe(&path) {
            Ok(d) => self.duration = d,
            Err(e) => {
                self.error = Some(e.to_string().into());
                return;
            }
        }
        std::thread::Builder::new()
            .name("video-decode".into())
            .spawn(move || {
                if let Err(e) = decode(&path, at, &tx) {
                    let _ = tx.blocking_send(Err(e));
                }
            })
            .expect("decode thread");
        self.playing = playing;
        self.shown = 0;
        self._task = Some(cx.spawn(async move |this, cx| {
            let start = Instant::now() - Duration::from_secs_f64(at);
            while let Some(next) = rx.recv().await {
                let frame = match next {
                    Ok(f) => f,
                    Err(e) => {
                        this.update(cx, |this, cx| {
                            this.error = Some(e.to_string().into());
                            cx.notify();
                        })
                        .ok();
                        break;
                    }
                };
                if playing {
                    let due = start + Duration::from_secs_f64(frame.pts.max(0.));
                    let now = Instant::now();
                    if due > now {
                        cx.background_executor().timer(due - now).await;
                    } else {
                        bench::sample("video-frame-late", now - due);
                    }
                }
                let image = Arc::new(RenderImage::new(vec![image::Frame::new(frame.image)]));
                let ok = this.update(cx, |this, cx| {
                    if let Some(old) = this.frame.replace(image) {
                        this.stale.push(old);
                    }
                    this.position = frame.pts;
                    this.shown += 1;
                    cx.notify();
                });
                if ok.is_err() || !playing {
                    return;
                }
            }
            this.update(cx, |this, cx| {
                this.playing = false;
                this.position = this.duration;
                bench::report();
                cx.notify();
            })
            .ok();
        }));
        cx.notify();
    }
}

/// probe reads the file's duration in seconds.
fn probe(path: &PathBuf) -> Result<f64> {
    ff::init()?;
    let input = ff::format::input(path).with_context(|| format!("opening {}", path.display()))?;
    Ok(input.duration() as f64 / f64::from(ff::ffi::AV_TIME_BASE))
}

/// decode sends the frames from at seconds in (from the keyframe before it,
/// decoded but not sent).
fn decode(path: &PathBuf, at: f64, tx: &tokio::sync::mpsc::Sender<Result<Decoded>>) -> Result<()> {
    let mut input = ff::format::input(path)?;
    if at > 0. {
        let ts = (at * f64::from(ff::ffi::AV_TIME_BASE)) as i64;
        input.seek(ts, ..ts)?;
    }
    let stream = input
        .streams()
        .best(ff::media::Type::Video)
        .ok_or_else(|| anyhow!("no video in {}", path.display()))?;
    let index = stream.index();
    let base = f64::from(stream.time_base());
    let mut decoder = ff::codec::context::Context::from_parameters(stream.parameters())?
        .decoder()
        .video()?;
    let (w, h) = (decoder.width(), decoder.height());
    let width = w.min(MAX_WIDTH);
    let height = (h as u64 * width as u64 / w.max(1) as u64) as u32 & !1;
    let mut scaler = ff::software::scaling::Context::get(
        decoder.format(),
        w,
        h,
        ff::format::Pixel::BGRA,
        width,
        height,
        ff::software::scaling::Flags::BILINEAR,
    )?;
    let mut send = |decoder: &mut ff::decoder::Video| -> Result<bool> {
        let mut frame = ff::frame::Video::empty();
        while decoder.receive_frame(&mut frame).is_ok() {
            let pts = frame.timestamp().unwrap_or(0) as f64 * base;
            if pts < at - 0.04 {
                continue;
            }
            let mut bgra = ff::frame::Video::empty();
            scaler.run(&frame, &mut bgra)?;
            let stride = bgra.stride(0);
            let data = bgra.data(0);
            let mut pixels = Vec::with_capacity((width * height * 4) as usize);
            for row in 0..height as usize {
                pixels.extend_from_slice(&data[row * stride..row * stride + width as usize * 4]);
            }
            let image = image::RgbaImage::from_raw(width, height, pixels)
                .ok_or_else(|| anyhow!("frame size"))?;
            if tx.blocking_send(Ok(Decoded { pts, image })).is_err() {
                return Ok(false); // the player is gone
            }
        }
        Ok(true)
    };
    for (s, packet) in input.packets() {
        if s.index() != index {
            continue;
        }
        decoder.send_packet(&packet)?;
        if !send(&mut decoder)? {
            return Ok(());
        }
    }
    decoder.send_eof()?;
    send(&mut decoder)?;
    Ok(())
}

impl Render for VideoPlayer {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        for old in self.stale.drain(..) {
            let _ = window.drop_image(old);
        }
        let label = format!("{} / {}", clock(self.position), clock(self.duration));
        let share = if self.duration > 0. {
            (self.position / self.duration).clamp(0., 1.) as f32
        } else {
            0.
        };
        let bar = self.bar.clone();
        let duration = self.duration;
        v_flex()
            .size_full()
            .gap_2()
            .child(
                div()
                    .id("video-frame")
                    .flex_1()
                    .min_h_0()
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_lg()
                    .bg(gpui::black())
                    .overflow_hidden()
                    .cursor_pointer()
                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.toggle(cx)))
                    .when_some(self.frame.clone(), |el, f| {
                        el.child(img(f).size_full().object_fit(ObjectFit::Contain))
                    }),
            )
            .child(
                h_flex()
                    .flex_none()
                    .gap_3()
                    .text_sm()
                    .text_color(theme::muted())
                    .child(
                        div()
                            .id("video-play")
                            .flex_none()
                            .px_3()
                            .py_1()
                            .rounded_md()
                            .bg(theme::raised())
                            .cursor_pointer()
                            .text_color(theme::fg())
                            .child(if self.playing {
                                agentbox_i18n::t("native.video.pause")
                            } else if self.position >= self.duration - 0.05 && self.shown > 0 {
                                agentbox_i18n::t("native.video.playAgain")
                            } else {
                                agentbox_i18n::t("native.video.play")
                            })
                            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.toggle(cx))),
                    )
                    // The bar: where it is, the marks, and a click seeks.
                    .child(
                        div()
                            .id("video-bar")
                            .relative()
                            .flex_1()
                            .h(px(18.))
                            .cursor_pointer()
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |this, ev: &MouseDownEvent, _, cx| {
                                    let b = this.bar.get();
                                    if b.size.width > px(0.) {
                                        let x = (ev.position.x - b.origin.x) / b.size.width;
                                        this.seek(f64::from(x.clamp(0., 1.)) * duration, cx);
                                    }
                                }),
                            )
                            .child(
                                canvas(move |bounds, _, _| bar.set(bounds), |_, _, _, _| {})
                                    .absolute()
                                    .inset_0(),
                            )
                            .child(
                                div()
                                    .absolute()
                                    .left_0()
                                    .right_0()
                                    .top(px(7.))
                                    .h(px(4.))
                                    .rounded_full()
                                    .bg(theme::raised())
                                    .child(
                                        div()
                                            .h_full()
                                            .rounded_full()
                                            .bg(theme::accent())
                                            .w(relative(share)),
                                    ),
                            )
                            .children(self.marks.iter().filter(|_| duration > 0.).map(|m| {
                                div()
                                    .absolute()
                                    .top(px(4.))
                                    .left(relative((m / duration).clamp(0., 1.) as f32))
                                    .w(px(2.))
                                    .h(px(10.))
                                    .bg(theme::fg().opacity(0.7))
                            })),
                    )
                    .child(div().flex_none().child(label))
                    .when_some(self.error.clone(), |el, e| {
                        el.child(div().text_color(theme::error()).child(e))
                    }),
            )
    }
}

/// clock writes seconds as m:ss.
fn clock(secs: f64) -> String {
    let s = secs.max(0.).round() as u64;
    format!("{}:{:02}", s / 60, s % 60)
}
