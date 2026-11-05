//! A video player for the recordings agents make (H.264 in MP4): FFmpeg
//! demuxes and decodes on a thread of its own, scales each frame to BGRA, and
//! GPUI shows each one as an image when its time comes.
use std::path::PathBuf;
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
            _task: None,
        };
        this.play(cx);
        this
    }

    pub fn play(&mut self, cx: &mut Context<Self>) {
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
                if let Err(e) = decode(&path, &tx) {
                    let _ = tx.blocking_send(Err(e));
                }
            })
            .expect("decode thread");
        self.playing = true;
        self.shown = 0;
        self._task = Some(cx.spawn(async move |this, cx| {
            let start = Instant::now();
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
                let due = start + Duration::from_secs_f64(frame.pts.max(0.));
                let now = Instant::now();
                if due > now {
                    cx.background_executor().timer(due - now).await;
                } else {
                    bench::sample("video-frame-late", now - due);
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
                if ok.is_err() {
                    return;
                }
            }
            this.update(cx, |this, cx| {
                this.playing = false;
                bench::report();
                cx.notify();
            })
            .ok();
        }));
    }
}

/// probe reads the file's duration in seconds.
fn probe(path: &PathBuf) -> Result<f64> {
    ff::init()?;
    let input = ff::format::input(path).with_context(|| format!("opening {}", path.display()))?;
    Ok(input.duration() as f64 / f64::from(ff::ffi::AV_TIME_BASE))
}

fn decode(path: &PathBuf, tx: &tokio::sync::mpsc::Sender<Result<Decoded>>) -> Result<()> {
    let mut input = ff::format::input(path)?;
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
            let pts = frame.timestamp().unwrap_or(0) as f64 * base;
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
        let label = format!(
            "{:>5.1}s / {:.1}s · {} frames",
            self.position, self.duration, self.shown
        );
        v_flex()
            .size_full()
            .bg(theme::bg())
            .p_4()
            .gap_3()
            .child(
                div()
                    .flex_1()
                    .min_h_0()
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_lg()
                    .bg(gpui::black())
                    .overflow_hidden()
                    .when_some(self.frame.clone(), |el, f| {
                        el.child(img(f).size_full().object_fit(ObjectFit::Contain))
                    }),
            )
            .child(
                h_flex()
                    .gap_3()
                    .text_sm()
                    .text_color(theme::muted())
                    .child(
                        div()
                            .id("replay")
                            .px_3()
                            .py_1()
                            .rounded_md()
                            .bg(theme::raised())
                            .cursor_pointer()
                            .text_color(theme::fg())
                            .child(if self.playing {
                                "Playing"
                            } else {
                                "Play again"
                            })
                            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                if !this.playing {
                                    this.play(cx);
                                }
                            })),
                    )
                    .child(label)
                    .when_some(self.error.clone(), |el, e| {
                        el.child(div().text_color(theme::error()).child(e))
                    }),
            )
    }
}
