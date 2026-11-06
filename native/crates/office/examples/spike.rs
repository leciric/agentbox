//! The spike: one of #668's models (Kenney's character) on a floor, drawn by
//! wgpu offscreen on a thread of its own and painted by GPUI as an image,
//! turning while you hold the window clicked-on (a click toggles it).
//!
//! OFFICE_SPIKE_BENCH=5 turns for 5 s, stands still 5 s, prints frame times
//! and CPU use for both, and quits.

use agentbox_office::{assets, glb, gpu};
use glam::{Mat4, Vec3};
use gpui::{
    App, AppContext as _, Context, IntoElement, ParentElement as _, Render, RenderImage,
    Styled as _, Window, WindowOptions, div, img, px, size,
};
use std::sync::Arc;
use std::time::{Duration, Instant};

struct Req {
    w: u32,
    h: u32,
    angle: f32,
}

struct Out {
    frame: gpu::Frame,
    render: Duration,
}

fn cpu_secs() -> f64 {
    let s = std::fs::read_to_string("/proc/self/stat").unwrap_or_default();
    let f: Vec<&str> = s
        .rsplit(')')
        .next()
        .unwrap_or("")
        .split_whitespace()
        .collect();
    let t = |i: usize| f.get(i).and_then(|v| v.parse::<f64>().ok()).unwrap_or(0.);
    (t(11) + t(12)) / 100.
}

fn render_thread(rx: std::sync::mpsc::Receiver<Req>, tx: async_channel::Sender<Out>) {
    let mut g = gpu::Gpu::new().expect("gpu");
    eprintln!("adapter: {}", g.adapter_name);
    let model = glb::load(assets::CHARACTER, &assets::external).expect("character");
    let tex: Vec<usize> = model
        .images
        .iter()
        .map(|i| g.add_texture(i.width, i.height, &i.rgba))
        .collect();
    let meshes: Vec<gpu::MeshId> = model
        .meshes
        .iter()
        .map(|m| {
            let mut m = m.clone();
            m.texture = m.texture.map(|t| tex[t]);
            g.add_mesh(&m)
        })
        .collect();
    let v = |x: f32, z: f32| gpu::Vertex {
        pos: [x, 0., z],
        normal: [0., 1., 0.],
        uv: [0., 0.],
        color: [0.93, 0.90, 0.85, 1.],
    };
    let floor = g.add_mesh(&gpu::MeshData {
        vertices: vec![v(-1., -1.), v(1., -1.), v(1., 1.), v(-1., 1.)],
        indices: vec![0, 2, 1, 0, 3, 2],
        texture: None,
        transparent: false,
    });
    let h = model.max.y - model.min.y;
    let scale = 1.95 / h;
    while let Ok(mut req) = rx.recv() {
        while let Ok(r) = rx.try_recv() {
            req = r;
        }
        let t0 = Instant::now();
        let aspect = req.w as f32 / req.h as f32;
        let eye = Vec3::new(0., 5.5, 9.);
        let cam = gpu::Camera {
            view_proj: Mat4::perspective_rh(24f32.to_radians(), aspect, 0.1, 100.)
                * Mat4::look_at_rh(eye, Vec3::new(0., 0.9, 0.), Vec3::Y),
            eye,
        };
        let mut draws = vec![(
            floor,
            vec![gpu::Instance::new(
                Mat4::from_scale(Vec3::new(4., 1., 4.)),
                [1.; 4],
            )],
        )];
        // A ring of them, for something like the room's load.
        let mut ring = Vec::new();
        for i in 0..12 {
            let a = i as f32 / 12. * std::f32::consts::TAU;
            ring.push(gpu::Instance::new(
                Mat4::from_translation(Vec3::new(a.cos() * 2.8, 0., a.sin() * 2.8 - 1.))
                    * Mat4::from_rotation_y(req.angle + a)
                    * Mat4::from_scale(Vec3::splat(scale * 0.6)),
                [1.; 4],
            ));
        }
        ring.push(gpu::Instance::new(
            Mat4::from_rotation_y(req.angle) * Mat4::from_scale(Vec3::splat(scale)),
            [1.; 4],
        ));
        for m in &meshes {
            draws.push((*m, ring.clone()));
        }
        let frame = g
            .render(req.w, req.h, &cam, [0.17, 0.16, 0.2], &draws)
            .expect("render");
        if tx
            .send_blocking(Out {
                frame,
                render: t0.elapsed(),
            })
            .is_err()
        {
            return;
        }
    }
}

struct Spike {
    req: std::sync::mpsc::Sender<Req>,
    frame: Option<Arc<RenderImage>>,
    stale: Vec<Arc<RenderImage>>,
    spinning: bool,
    in_flight: bool,
    start: Instant,
    size: (u32, u32),
    // Bench.
    renders: Vec<f64>,
    shown: Vec<Instant>,
    painted: Vec<Instant>,
    last_painted: usize,
}

impl Spike {
    fn ask(&mut self) {
        if self.in_flight || self.size.0 == 0 {
            return;
        }
        self.in_flight = true;
        let _ = self.req.send(Req {
            w: self.size.0,
            h: self.size.1,
            angle: self.start.elapsed().as_secs_f32() * 1.2,
        });
    }
}

impl Render for Spike {
    fn render(&mut self, window: &mut Window, _cx: &mut Context<Self>) -> impl IntoElement {
        for old in self.stale.drain(..) {
            let _ = window.drop_image(old);
        }
        let vp = window.viewport_size();
        let s = window.scale_factor();
        let want = (
            (f32::from(vp.width) * s) as u32,
            (f32::from(vp.height) * s) as u32,
        );
        if want != self.size {
            self.size = want;
            self.ask();
        }
        if let Some(f) = &self.frame {
            let id = Arc::as_ptr(f) as usize;
            if id != self.last_painted {
                self.last_painted = id;
                self.painted.push(Instant::now());
            }
        }
        div()
            .size_full()
            .bg(gpui::black())
            .children(self.frame.clone().map(|f| img(f).size_full()))
    }
}

fn main() {
    let bench: Option<u64> = std::env::var("OFFICE_SPIKE_BENCH")
        .ok()
        .and_then(|v| v.parse().ok());
    gpui_platform::application().run(move |cx: &mut App| {
        let options = WindowOptions { window_bounds: Some(gpui::WindowBounds::Windowed(gpui::Bounds::centered(None, size(px(1440.), px(900.)), cx))), ..Default::default() };
        cx.open_window(options, |_, cx| {
            cx.new(|cx| {
                let (req_tx, req_rx) = std::sync::mpsc::channel();
                let (out_tx, out_rx) = async_channel::bounded::<Out>(2);
                std::thread::spawn(move || render_thread(req_rx, out_tx));
                cx.spawn(async move |this, cx| {
                    while let Ok(out) = out_rx.recv().await {
                        let ok = this.update(cx, |s: &mut Spike, cx| {
                            s.in_flight = false;
                            let f = out.frame;
                            let rgba = image::RgbaImage::from_raw(f.width, f.height, f.bgra).expect("frame");
                            let img = Arc::new(RenderImage::new(vec![image::Frame::new(rgba)]));
                            if let Some(old) = s.frame.replace(img) {
                                s.stale.push(old);
                            }
                            s.renders.push(out.render.as_secs_f64() * 1000.);
                            s.shown.push(Instant::now());
                            if s.spinning {
                                s.ask();
                            }
                            cx.notify();
                        });
                        if ok.is_err() {
                            return;
                        }
                    }
                })
                .detach();
                if let Some(secs) = bench {
                    cx.spawn(async move |this, cx| {
                        cx.background_executor().timer(Duration::from_secs(2)).await;
                        let _ = this.update(cx, |s: &mut Spike, _| {
                            s.renders.clear();
                            s.shown.clear();
                            s.painted.clear();
                        });
                        let c0 = cpu_secs();
                        cx.background_executor().timer(Duration::from_secs(secs)).await;
                        let c1 = cpu_secs();
                        let _ = this.update(cx, |s: &mut Spike, _| {
                            s.spinning = false;
                            let mut r = s.renders.clone();
                            r.sort_by(f64::total_cmp);
                            let mut gaps: Vec<f64> = s.shown.windows(2).map(|w| (w[1] - w[0]).as_secs_f64() * 1000.).collect();
                            gaps.sort_by(f64::total_cmp);
                            let p = |v: &[f64], q: f64| v.get(((v.len() as f64 - 1.) * q) as usize).copied().unwrap_or(0.);
                            println!(
                                "moving {}x{}: {} frames in {secs}s = {:.1} fps; render+readback p50 {:.1} ms p95 {:.1} ms; frame gap p50 {:.1} ms p95 {:.1} ms; cpu {:.0}% of one core",
                                s.size.0,
                                s.size.1,
                                s.shown.len(),
                                s.shown.len() as f64 / secs as f64,
                                p(&r, 0.5),
                                p(&r, 0.95),
                                p(&gaps, 0.5),
                                p(&gaps, 0.95),
                                (c1 - c0) / secs as f64 * 100.
                            );
                            let mut pg: Vec<f64> = s.painted.windows(2).map(|w| (w[1] - w[0]).as_secs_f64() * 1000.).collect();
                            pg.sort_by(f64::total_cmp);
                            println!("painted by GPUI: {} frames = {:.1} fps; gap p50 {:.1} ms p95 {:.1} ms p99 {:.1} ms", s.painted.len(), s.painted.len() as f64 / secs as f64, p(&pg, 0.5), p(&pg, 0.95), p(&pg, 0.99));
                            s.shown.clear();
                            s.painted.clear();
                        });
                        cx.background_executor().timer(Duration::from_secs(1)).await;
                        let c2 = cpu_secs();
                        cx.background_executor().timer(Duration::from_secs(secs)).await;
                        let c3 = cpu_secs();
                        let _ = this.update(cx, |s: &mut Spike, cx| {
                            println!("still: {} frames received, {} painted in {secs}s; cpu {:.1}% of one core", s.shown.len(), s.painted.len(), (c3 - c2) / secs as f64 * 100.);
                            cx.quit();
                        });
                    })
                    .detach();
                }
                Spike { req: req_tx, frame: None, stale: Vec::new(), spinning: true, in_flight: false, start: Instant::now(), size: (0, 0), renders: Vec::new(), shown: Vec::new(), painted: Vec::new(), last_painted: 0 }
            })
        })
        .expect("window");
        cx.activate(true);
    });
}
