//! The Office's thread: the renderer, the scene and you (sim.rs) live on a
//! thread of their own, so GPUI's never waits on a frame. The view sends it
//! input and the data; it sends back frames, what happened (notes) and where
//! the labels go. While nothing moves it sleeps on its channel: no frames,
//! no CPU (#668 drew its robots' idle life a few times a second; the native
//! Office draws only while something changes), unless a working agent is in
//! view: it types, and the monitor you're at blinks, at #668's idle pace.

use crate::desks::{Built, SceneInput};
use crate::gpu::{self, Gpu, Instance, MeshId};
use crate::kit::{Draw, Kit};
use crate::layout;
use crate::player::Player;
use crate::sim::{Note, Sim, Visit};
use glam::{Mat4, Vec2, Vec3};
use std::collections::HashMap;
use std::sync::mpsc::{Receiver, RecvTimeoutError, Sender, channel};
use std::time::{Duration, Instant};

/// What the view tells the Office.
#[derive(Debug)]
pub enum In {
    /// The view's size in logical pixels and its scale (pixels per point).
    Size {
        w: f32,
        h: f32,
        scale: f32,
    },
    /// The data, as the view read it (a new model, screens, the board).
    Apply(Box<SceneInput>),
    Key {
        key: String,
        down: bool,
        repeat: bool,
    },
    PointerDown {
        x: f64,
        y: f64,
    },
    PointerMove {
        x: f64,
        y: f64,
        dx: f64,
    },
    PointerUp {
        x: f64,
        y: f64,
    },
    PointerLeave,
    Wheel {
        x: f64,
        y: f64,
        delta: f64,
    },
    WalkTo(String),
    Overview,
    Campus,
    Floor(i64),
    /// How much of the right edge a panel covers (the desk's card, the chat).
    Cover(f64),
    Blur,
    /// The view is shown (again) or hidden: hidden, nothing is drawn.
    Shown(bool),
    /// The display's refresh interval (GPUI's, 60 Hz when it doesn't say).
    Refresh(Duration),
    /// A frame was painted, at this moment (a display refresh): the next may come.
    Painted(Instant),
}

/// A label the view draws over the room: an agent's name tag, a bubble
/// ("!" when it needs you), an area's pill.
#[derive(Clone, Debug, PartialEq)]
pub struct Label {
    pub r#ref: String,
    pub kind: LabelKind,
    pub text: String,
    /// #rrggbb.
    pub color: String,
    /// Where, in the view's logical pixels.
    pub x: f32,
    pub y: f32,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum LabelKind {
    Tag,
    Bubble,
    You,
    Area,
}

/// What the Office tells the view.
pub enum Out {
    /// Which adapter draws, and whether it's the CPU (lavapipe, WARP).
    Adapter {
        name: String,
        software: bool,
    },
    /// No adapter at all: the Office can't be drawn here.
    Failed(String),
    Frame {
        frame: gpu::Frame,
        labels: Vec<Label>,
        render_ms: f32,
        /// The moment it shows: the display refresh it was drawn for. The
        /// view holds it until then, so what moved by a frame's time is on
        /// screen for that time.
        at: Instant,
    },
    Notes(Vec<Note>),
}

pub struct Engine {
    tx: Sender<In>,
}

impl Engine {
    /// start runs the Office's thread; `out` is called on it with each thing
    /// to tell the view (send it on to the view's own executor).
    pub fn start(
        visit: Option<Visit>,
        out: impl FnMut(Out) + Send + 'static,
    ) -> (Engine, std::sync::Arc<std::sync::Mutex<Option<Visit>>>) {
        let (tx, rx) = channel();
        let kept = std::sync::Arc::new(std::sync::Mutex::new(visit));
        let k2 = kept.clone();
        std::thread::Builder::new()
            .name("office".into())
            .spawn(move || run(rx, out, k2))
            .expect("start the Office's thread");
        (Engine { tx }, kept)
    }

    pub fn send(&self, m: In) {
        let _ = self.tx.send(m);
    }
}

struct Stage {
    gpu: Gpu,
    kit: Kit,
    /// Kit textures → GPU textures, and kit meshes → GPU meshes, uploaded so far.
    tex: Vec<usize>,
    meshes: Vec<MeshId>,
    built: Option<Built>,
    player: Option<Player>,
    player_mesh: Vec<MeshId>,
    sim: Sim,
    input: Option<SceneInput>,
    layout_key: String,
    scale: f32,
    start: Instant,
    /// Where you were last time, for the first layout.
    sim_visit: Option<Visit>,
    /// Draws besides the scene's this frame (you).
    extra: Vec<(MeshId, Instance)>,
}

impl Stage {
    fn upload(&mut self) {
        while self.tex.len() < self.kit.textures.len() {
            let i = &self.kit.textures[self.tex.len()];
            let id = self.gpu.add_texture(i.width, i.height, &i.rgba);
            self.tex.push(id);
        }
        for k in std::mem::take(&mut self.kit.changed) {
            if k < self.tex.len() {
                let i = &self.kit.textures[k];
                self.gpu
                    .replace_texture(self.tex[k], i.width, i.height, &i.rgba);
            }
        }
        while self.meshes.len() < self.kit.meshes.len() {
            let mut m = self.kit.meshes[self.meshes.len()].clone();
            m.texture = m.texture.map(|t| self.tex[t]);
            let id = self.gpu.add_mesh(&m);
            self.meshes.push(id);
        }
    }

    fn apply(&mut self, input: SceneInput) {
        let key = input
            .model
            .areas
            .iter()
            .map(|a| {
                format!(
                    "{}:{}:{}:{}",
                    a.project,
                    a.label,
                    a.hue,
                    a.desks
                        .iter()
                        .map(|d| d.id.as_str())
                        .collect::<Vec<_>>()
                        .join(",")
                )
            })
            .collect::<Vec<_>>()
            .join("|");
        if key != self.layout_key || self.built.is_none() {
            self.layout_key = key;
            let lay = layout::layout(&input.model.areas);
            let walls = lay
                .rooms
                .iter()
                .map(|r| crate::decor::wall_height(&r.local) as f64)
                .collect();
            // A new campus: the meshes start again (the textures stay, a
            // few hundred kB of signs and screens, until the Office closes).
            self.kit.meshes.clear();
            self.meshes.clear();
            self.gpu.clear_meshes();
            self.player_mesh.clear();
            let built = crate::desks::build(&input, &lay, &mut self.kit);
            self.sim.hits = built.hits.clone();
            let visit = self.sim_visit.take();
            self.sim.set_layout(lay, walls, visit);
            self.built = Some(built);
        } else if let Some(b) = &mut self.built {
            b.update(&input, &mut self.kit);
        }
        self.input = Some(input);
        self.sim.poke();
    }

    fn frame(&mut self, w: u32, h: u32, at: Instant) -> anyhow::Result<(gpu::Frame, Vec<Label>)> {
        let t = at.saturating_duration_since(self.start).as_secs_f32();
        let mut draws: Vec<Draw> = Vec::new();
        if let Some(b) = &mut self.built {
            b.draws(&self.sim, t, self.sim.motion, &mut self.kit, &mut draws);
        }
        // The textures and meshes first: you're drawn with Kenney's colormap.
        self.upload();
        // You: Kenney's figure, posed on the CPU, where you stand (only in a
        // room's view: over the campus you're nowhere).
        if let (Some(p), true) = (&mut self.player, self.sim.scope.is_some()) {
            p.yaw = self.sim.facing as f32;
            p.stride(self.sim.speed as f32, t, self.sim.motion);
            if self.player_mesh.len() != p.meshes.len() {
                self.player_mesh = p
                    .meshes
                    .iter()
                    .map(|m| {
                        let mut m = m.clone();
                        m.texture = m.texture.map(|t| self.tex.get(t).copied().unwrap_or(0));
                        self.gpu.add_mesh(&m)
                    })
                    .collect();
            } else {
                for (id, m) in self.player_mesh.iter().zip(&p.meshes) {
                    self.gpu.update_mesh(*id, &m.vertices);
                }
            }
            if p.visible() {
                let at = Mat4::from_translation(self.sim.you())
                    * Mat4::from_rotation_y(self.sim.facing as f32)
                    * p.matrix();
                self.extra = self
                    .player_mesh
                    .iter()
                    .map(|id| (*id, Instance::new(at, [1., 1., 1., p.opacity()])))
                    .collect();
            }
        }
        // Group by mesh: one instanced draw each.
        let mut by: HashMap<usize, Vec<Instance>> = HashMap::new();
        let mut order: Vec<usize> = Vec::new();
        for d in &draws {
            let e = by.entry(d.mesh.0).or_insert_with(|| {
                order.push(d.mesh.0);
                Vec::new()
            });
            e.push(Instance::new(d.at, d.tint));
        }
        let mut list: Vec<(MeshId, Vec<Instance>)> = order
            .into_iter()
            .filter_map(|k| Some((*self.meshes.get(k)?, by.remove(&k)?)))
            .collect();
        for (id, i) in self.extra.drain(..) {
            list.push((id, vec![i]));
        }
        let cam = gpu::Camera {
            view_proj: self.sim.camera.view_proj(),
            eye: self.sim.camera.eye,
        };
        let frame = self.gpu.render(w, h, &cam, SKY, &list)?;
        let labels = self.labels();
        Ok((frame, labels))
    }

    fn labels(&self) -> Vec<Label> {
        let mut out = Vec::new();
        let size = self.sim.size;
        let to_px = |p: Vec3| {
            let (n, front) = self.sim.camera.project(p);
            (front && n.x.abs() <= 1.1 && n.y.abs() <= 1.1)
                .then(|| Vec2::new((n.x + 1.) / 2. * size.x, (1. - n.y) / 2. * size.y))
        };
        if let Some(b) = &self.built {
            for l in b.labels(&self.sim) {
                if !l.visible {
                    continue;
                }
                if let Some(p) = to_px(l.world) {
                    out.push(Label {
                        r#ref: l.r#ref,
                        kind: l.kind,
                        text: l.text,
                        color: l.color,
                        x: p.x,
                        y: p.y,
                    });
                }
            }
        }
        if let Some(input) = &self.input
            && let Some(p) = to_px(self.sim.you() + Vec3::new(0., 2.25, 0.))
            && self.sim.scope.is_some()
        {
            out.push(Label {
                r#ref: "you".into(),
                kind: LabelKind::You,
                text: input.you.clone(),
                color: "#a78bfa".into(),
                x: p.x,
                y: p.y,
            });
        }
        out
    }
}

// The sky behind the campus, #668's pale blue (linear).
const SKY: [f64; 3] = [0.73, 0.82, 0.88];

fn run(
    rx: Receiver<In>,
    mut out: impl FnMut(Out),
    kept: std::sync::Arc<std::sync::Mutex<Option<Visit>>>,
) {
    let gpu = match Gpu::new() {
        Ok(g) => g,
        Err(e) => {
            out(Out::Failed(format!("{e:#}")));
            return;
        }
    };
    out(Out::Adapter {
        name: gpu.adapter_name.clone(),
        software: gpu.software,
    });
    let mut kit = Kit::new();
    let player = Player::new(&mut kit).ok();
    let mut st = Stage {
        gpu,
        kit,
        tex: Vec::new(),
        meshes: Vec::new(),
        built: None,
        player,
        player_mesh: Vec::new(),
        sim: Sim::new(),
        input: None,
        layout_key: String::new(),
        scale: 1.,
        start: Instant::now(),
        sim_visit: kept.lock().ok().and_then(|v| *v),
        extra: Vec::new(),
    };
    let mut shown = true;
    // A frame is in flight until the view says it's painted (or skipped)
    // it. Two at most: the next is drawn while the view shows the last
    // (GPUI's upload and frame overlap this thread's), and a slow GPU (or
    // CPU) never queues more up.
    let mut in_flight = 0u32;
    let mut busy = true;
    // The next frame of the working agents' life, while nothing else moves;
    // and when you last did something (or something last moved).
    let mut life: Option<Instant> = None;
    let mut active = Instant::now();
    let mut pace = Pace::new();
    loop {
        // Wait for something to do: while busy, only until the next frame's due.
        let wait = if busy && shown && in_flight < AHEAD {
            Some(Duration::from_millis(0))
        } else {
            life.map(|at| at.saturating_duration_since(Instant::now()))
        };
        let first = match wait {
            Some(d) => match rx.recv_timeout(d) {
                Ok(m) => Some(m),
                Err(RecvTimeoutError::Timeout) => {
                    // Time for the next frame of their life.
                    if life.take().is_some() {
                        st.sim.poke();
                    }
                    None
                }
                Err(RecvTimeoutError::Disconnected) => break,
            },
            None => match rx.recv() {
                Ok(m) => Some(m),
                Err(_) => break,
            },
        };
        let mut msgs: Vec<In> = first.into_iter().collect();
        msgs.extend(rx.try_iter());
        let mut size = None;
        for m in msgs {
            if !matches!(
                m,
                In::Painted(_)
                    | In::Refresh(_)
                    | In::Apply(_)
                    | In::Shown(_)
                    | In::Size { .. }
                    | In::Cover(_)
                    | In::Blur
            ) {
                active = Instant::now();
            }
            match m {
                In::Size { w, h, scale } => size = Some((w, h, scale)),
                In::Apply(i) => st.apply(*i),
                In::Key {
                    key,
                    down: true,
                    repeat,
                } => {
                    st.sim.key_down(&key, repeat);
                }
                In::Key {
                    key, down: false, ..
                } => st.sim.key_up(&key),
                In::PointerDown { x, y } => st.sim.pointer_down(x, y),
                In::PointerMove { x, y, dx } => {
                    st.sim.pointer_move(x, y, dx);
                }
                In::PointerUp { x, y } => st.sim.pointer_up(x, y),
                In::PointerLeave => st.sim.pointer_leave(),
                In::Wheel { x, y, delta } => st.sim.wheel(x, y, delta),
                In::WalkTo(r) => st.sim.walk_to(&r),
                In::Overview => st.sim.overview(),
                In::Campus => st.sim.campus(),
                In::Floor(f) => st.sim.floor(f),
                In::Cover(c) => st.sim.set_cover(c),
                In::Blur => st.sim.blur(),
                In::Shown(s) => {
                    shown = s;
                    st.sim.poke();
                }
                In::Refresh(d) => {
                    pace.period = d.clamp(Duration::from_millis(4), Duration::from_millis(50))
                }
                In::Painted(at) => {
                    in_flight = in_flight.saturating_sub(1);
                    pace.vsync = Some(at);
                }
            }
        }
        if let Some((w, h, scale)) = size {
            st.sim.size = Vec2::new(w.max(1.), h.max(1.));
            st.scale = scale;
            st.sim.poke();
        }
        if let Ok(mut k) = kept.lock() {
            *k = Some(st.sim.visit());
        }
        if in_flight >= AHEAD && shown && st.built.is_some() {
            // Waiting for the view to paint: the clock runs on, so walking
            // keeps its pace however slowly frames come.
            continue;
        }
        if !shown || st.built.is_none() {
            busy = false;
            pace.rest();
            continue;
        }
        let (now, dt) = pace.next(Instant::now());
        let moving = st.sim.step(dt);
        let notes = std::mem::take(&mut st.sim.notes);
        if !notes.is_empty() {
            out(Out::Notes(notes));
        }
        busy = moving || st.sim.busy();
        if busy {
            active = now;
        }
        // At rest, a working agent in view still types and the monitor
        // you're at blinks its cursor: drawn as #668 drew its idle life,
        // 8 frames a second, 2 after a minute untouched (on a grid, so the
        // half-second blink lands on a frame). Nobody working, no frames.
        life = (!busy && st.sim.motion && st.built.as_ref().is_some_and(|b| b.working_in_view()))
            .then(|| life_frame(now, active, st.start));
        if !(busy || st.sim.dirty) {
            pace.rest();
            continue;
        }
        st.sim.dirty = false;
        let w = (st.sim.size.x * st.scale).round() as u32;
        let h = (st.sim.size.y * st.scale).round() as u32;
        let t0 = Instant::now();
        match st.frame(w, h, now) {
            Ok((frame, labels)) => {
                in_flight += 1;
                pace.drew(t0.elapsed());
                out(Out::Frame {
                    frame,
                    labels,
                    render_ms: t0.elapsed().as_secs_f32() * 1000.,
                    at: now,
                });
            }
            Err(e) => {
                out(Out::Failed(format!("{e:#}")));
                return;
            }
        }
    }
}

/// Pace times the frames to the display. Each frame is drawn for a display
/// refresh: the first one it can be ready by (the time a frame takes, its
/// slowest lately, from the last refresh one was painted at), and the sim
/// steps to exactly that moment. The view shows it then and not before. So
/// every frame moves things by the time it's on screen for: a steady 60 fps
/// where a frame takes under a refresh, a steady 30 where it takes under two
/// (lavapipe), never a mix of 1- and 2-refresh frames each moved as if it
/// lasted as long as it took to draw (judder).
struct Pace {
    period: Duration,
    /// A refresh a frame was painted at: where the display's refreshes fall.
    vsync: Option<Instant>,
    /// What the last frame was drawn for; None at rest.
    last: Option<Instant>,
    /// How long a frame takes, its slowest lately (decaying).
    cost: Duration,
}

/// life_frame is when the next frame of the idle life is due, after `now`:
/// every 125 ms for a minute after `active`, then every 500 ms, on a grid
/// from `start` (the clock frames are drawn by).
fn life_frame(now: Instant, active: Instant, start: Instant) -> Instant {
    let every = if now.saturating_duration_since(active) < Duration::from_secs(60) {
        0.125
    } else {
        0.5
    };
    let t = now.saturating_duration_since(start).as_secs_f64();
    start + Duration::from_secs_f64(((t / every).floor() + 1.) * every)
}

/// How many frames may be on their way to the screen at once.
const AHEAD: u32 = 2;

/// Besides drawing: sending it, GPUI's upload and its own frame.
const MARGIN: Duration = Duration::from_millis(4);

impl Pace {
    fn new() -> Self {
        Self {
            period: Duration::from_micros(16_667),
            vsync: None,
            last: None,
            cost: Duration::ZERO,
        }
    }

    fn rest(&mut self) {
        self.last = None;
    }

    fn drew(&mut self, took: Duration) {
        // A one-off stall (a new campus built) doesn't slow what follows.
        let took = took.min(self.period * 3);
        self.cost = took.max(self.cost.mul_f64(0.9));
    }

    /// next is the moment the next frame shows and how far the sim steps to it.
    fn next(&mut self, now: Instant) -> (Instant, f64) {
        let p = self.period;
        let mut ready = now + self.cost + MARGIN;
        // Frames drawn back to back (one ahead) are as far apart as one
        // takes, in whole refreshes: a steady cadence, not as they come.
        if let Some(l) = self.last {
            let k = (self.cost.as_secs_f64() / p.as_secs_f64()).ceil().max(1.);
            ready = ready.max(l + p.mul_f64(k) - p / 2);
        }
        // The first refresh at or after `ready`, on the display's grid.
        let mut at = match self.vsync {
            Some(v) if v <= ready => {
                let n = (ready - v).as_secs_f64() / p.as_secs_f64();
                v + p.mul_f64(n.ceil())
            }
            Some(v) => v,
            None => ready,
        };
        let dt = match self.last {
            // Never twice the same moment, never backwards.
            Some(l) if at <= l => {
                at = l + p;
                p.as_secs_f64()
            }
            Some(l) => (at - l).as_secs_f64(),
            // Coming back from rest: one refresh's step, not the whole pause.
            None => p.as_secs_f64(),
        };
        self.last = Some(at);
        // As #668: a long pause (a stall) is one 50 ms step.
        (at, dt.clamp(1. / 240., 0.05))
    }
}

#[cfg(test)]
mod pace_tests {
    use super::*;

    #[test]
    fn life_is_eight_a_second_then_two_on_a_grid() {
        let start = Instant::now();
        let at = |s: f64| start + Duration::from_secs_f64(s);
        // Just touched: the next eighth of a second.
        let next = life_frame(at(10.03), at(10.), start);
        assert!((next - at(10.125)).as_secs_f64().abs() < 1e-6);
        // A minute untouched: the next half second, where the cursor blinks.
        let next = life_frame(at(80.03), at(10.), start);
        assert!((next - at(80.5)).as_secs_f64().abs() < 1e-6);
        // Never now: a frame drawn on the grid waits for the next.
        let next = life_frame(at(80.5), at(10.), start);
        assert!((next - at(81.)).as_secs_f64().abs() < 1e-6);
    }

    #[test]
    fn frames_land_on_refreshes_and_step_by_them() {
        let mut p = Pace::new();
        p.period = Duration::from_millis(16);
        let t0 = Instant::now();
        // A slow frame (lavapipe): 25 ms to draw.
        p.drew(Duration::from_millis(25));
        p.vsync = Some(t0);
        let (a, _) = p.next(t0 + Duration::from_millis(1));
        assert_eq!(a, t0 + Duration::from_millis(32), "two refreshes on");
        // Painted then, the next is drawn for two more, and steps 32 ms.
        p.vsync = Some(a);
        let (b, dt) = p.next(a + Duration::from_millis(1));
        assert_eq!(b, a + Duration::from_millis(32));
        assert!((dt - 0.032).abs() < 1e-9);
    }

    #[test]
    fn a_fast_frame_is_every_refresh() {
        let mut p = Pace::new();
        p.period = Duration::from_millis(16);
        p.drew(Duration::from_millis(3));
        let t0 = Instant::now();
        p.vsync = Some(t0);
        let (a, _) = p.next(t0);
        assert_eq!(a, t0 + Duration::from_millis(16));
        p.vsync = Some(a);
        let (b, dt) = p.next(a);
        assert_eq!(b - a, Duration::from_millis(16));
        assert!((dt - 0.016).abs() < 1e-9);
    }

    #[test]
    fn from_rest_one_refresh_step() {
        let mut p = Pace::new();
        let (_, dt) = p.next(Instant::now());
        assert!((dt - p.period.as_secs_f64()).abs() < 1e-9);
    }
}
