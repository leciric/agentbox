//! You and the camera (#668's scene.ts: step, camStep, fitDistance, focusOf,
//! the keys, the pointer and the wheel): the keys and the mouse walk you, a
//! click on a desk walks you to it, a click on the desk you're at opens it;
//! the camera stands over the campus, a room's overview, follows you, and
//! comes close at a desk you walked to. No drawing here: the scene asks where
//! the camera is and what's where; the view hands in the input.

use crate::layout::{self, Campus, Rect};
use crate::walk::{P, path, path_length, slide, turn};
use glam::{Mat4, Vec2, Vec3, Vec4};

// How far you keep from things: Kenney's chibi is broad, its head and arms
// well over a metre across.
pub const RADIUS: f64 = 0.45;
// Metres a second: walking (the keys, a short click), and running, for a
// long walk or one you asked for by name. The figure's walk and run play at
// these, feet planted (player.rs).
pub const SPEED: f64 = 3.0;
pub const RUN: f64 = 6.0;
// How quickly you get going and come to a stop, metres a second a second.
const ACCEL: f64 = 9.;
const BRAKE: f64 = 8.;
// Turning on the keys: changing direction takes this much more than setting
// off, so a turn is a short curve rather than a skid or a snap.
const TURN_ACCEL: f64 = 20.;
// How quickly you turn to face where the keys point (radians a second, at
// most), easing in at the end.
const TURN_RATE: f64 = 14.;
const TURN_MAX: f64 = 4. * std::f64::consts::PI;
// Following you, the camera looks this many seconds ahead of you, so more of
// where you're going is in view than of where you've been.
const LOOK_AHEAD: f64 = 0.3;
pub const FOV: f64 = 24.;
// The overview's 3/4 view: from the front right, over the open corner.
const HOME_YAW: f64 = 0.4;
// Over the campus, more from the front: its rows of rooms read left to right.
const CAMPUS_YAW: f64 = 0.3;
const OVERVIEW_PITCH: f64 = 0.62;
// Following you, the camera looks down this steeply.
const FOLLOW_PITCH: f64 = 0.7;
const FOLLOW_FAR: f64 = 26.;
const FOLLOW_NEAR: f64 = 10.5;
// The desk's card (bottom right), as #668's: a close-up keeps what it looks
// at clear of it.
const CARD: f64 = 336. + 24.;

/// What the Office tells the page (#668's OfficeSceneOptions callbacks).
#[derive(Clone, Debug, PartialEq)]
pub enum Note {
    /// What you're standing at: a desk's ref, project/lead, "board", or nothing.
    Near(Option<String>),
    /// Which room you're in (project, floor of floors), or the campus, and
    /// whether at its overview.
    View {
        project: Option<String>,
        floor: i64,
        floors: usize,
        overview: bool,
    },
    /// What the pointer is over.
    Hover(Option<String>),
    /// A click on what you're standing at, or E: open it.
    Activate(String),
    /// You moved for the first time (the controls hint can go).
    Moved,
    /// N: on to the next desk that needs you.
    Next,
    /// Escape over the campus, with nothing left to step back from.
    Leave,
    /// You set off yourself, by the keys or a click on the floor.
    UserMove,
}

/// Something a click can land on: a desk's figure, a counter, a board, a
/// room's slab ("room:3") or a bubble over an agent.
#[derive(Clone, Debug)]
pub struct Hit {
    pub r#ref: String,
    pub min: Vec3,
    pub max: Vec3,
    pub bubble: bool,
    /// Its room (shown only in its room's view, or over the campus), or -1 for the campus's own.
    pub room: i64,
    pub campus_only: bool,
}

#[derive(Clone, Copy, Debug, Default)]
struct Eased {
    tx: f64,
    ty: f64,
    tz: f64,
    dist: f64,
    pitch: f64,
    yaw: f64,
    init: bool,
}

#[derive(Clone, Copy, Debug)]
struct Focus {
    x: f64,
    y: f64,
    z: f64,
    dist: f64,
    pitch: f64,
    yaw: f64,
}

#[derive(Clone, Debug, Default)]
struct Aim {
    r#ref: Option<String>,
    floor: Option<P>,
    bubble: bool,
}

struct Pointer {
    x: f64,
    y: f64,
    dragged: bool,
    aim: Aim,
    cam: String,
}

/// Where you were when you left the Office, so coming back from an agent's
/// chat puts you back at its desk rather than at the door.
#[derive(Clone, Copy, Debug)]
pub struct Visit {
    pub pos: P,
    pub facing: f64,
    pub follow: f64,
    pub yaw: f64,
    pub dist: f64,
    pub scope: Option<usize>,
}

pub struct Sim {
    pub lay: Campus,
    /// Each room's wall height (decor.rs), for framing it.
    pub walls: Vec<f64>,
    /// What clicks land on (the scene keeps it current).
    pub hits: Vec<Hit>,
    /// The room you're in, and what the overview frames: a room, or the
    /// whole campus (None). The Office opens on the campus.
    pub cur: usize,
    pub scope: Option<usize>,
    boxes: Vec<Rect>,
    bounds: Rect,
    pub pos: P,
    pub facing: f64,
    pub speed: f64,
    pace: f64,
    key_dir: Option<P>,
    /// How you're moving on the keys (metres a second, each way): eased
    /// towards where they point, so you set off, curve and stop.
    vel: P,
    /// Shift is held: the keys run.
    run: bool,
    /// Where the camera looks ahead of you, eased.
    look: P,
    route: Vec<P>,
    route_goal: Option<String>,
    came_to: Option<String>,
    placed: bool,
    moved: bool,
    // The camera.
    yaw: f64,
    follow_k: f64,
    pub close_k: f64,
    dist: f64,
    zoom_z: f64,
    follow_target: f64,
    eased: Eased,
    pub near: Option<String>,
    pub focus_ref: Option<String>,
    zoom_focus: Option<P>,
    pub still_for: f64,
    held: bool,
    turned: bool,
    campus_yaw: f64,
    shown_for: Option<Option<usize>>,
    fit_cache: (String, f64, f64, f64),
    inset: f64,
    cover: f64,
    pub motion: bool,
    // Input.
    keys: std::collections::HashSet<String>,
    pointer: Option<Pointer>,
    pub hover: Option<String>,
    /// The canvas's size in logical pixels.
    pub size: Vec2,
    told_view: String,
    pub notes: Vec<Note>,
    /// Something changed that a frame should show.
    pub dirty: bool,
    pub camera: Camera,
}

/// Where the camera is: its eye, what it looks at, and the matrices.
#[derive(Clone, Copy, Debug)]
pub struct Camera {
    pub eye: Vec3,
    pub target: Vec3,
    pub view: Mat4,
    pub proj: Mat4,
}

impl Camera {
    fn new(eye: Vec3, target: Vec3, aspect: f32) -> Self {
        let view = Mat4::look_at_rh(eye, target, Vec3::Y);
        let proj = Mat4::perspective_rh((FOV as f32).to_radians(), aspect.max(0.01), 0.1, 900.);
        Self {
            eye,
            target,
            view,
            proj,
        }
    }

    pub fn view_proj(&self) -> Mat4 {
        self.proj * self.view
    }

    /// project is a point in normalised device coordinates (x, y in -1..1,
    /// y up), and whether it's in front of the camera.
    pub fn project(&self, p: Vec3) -> (Vec2, bool) {
        let c = self.view_proj() * Vec4::new(p.x, p.y, p.z, 1.);
        if c.w <= 1e-6 {
            return (Vec2::new(c.x, c.y), false);
        }
        (Vec2::new(c.x / c.w, c.y / c.w), c.z / c.w <= 1.)
    }

    /// ray is the line from the eye through a point in NDC.
    pub fn ray(&self, ndc: Vec2) -> (Vec3, Vec3) {
        let inv = self.view_proj().inverse();
        let a = inv.project_point3(Vec3::new(ndc.x, ndc.y, 0.));
        let b = inv.project_point3(Vec3::new(ndc.x, ndc.y, 0.5));
        (a, (b - a).normalize())
    }
}

fn floor_hit(o: Vec3, d: Vec3) -> Option<P> {
    if d.y.abs() < 1e-6 {
        return None;
    }
    let t = -o.y / d.y;
    (t > 0.).then_some(P {
        x: (o.x + d.x * t) as f64,
        z: (o.z + d.z * t) as f64,
    })
}

fn ray_box(o: Vec3, d: Vec3, min: Vec3, max: Vec3) -> Option<f32> {
    let inv = d.recip();
    let t0 = (min - o) * inv;
    let t1 = (max - o) * inv;
    let lo = t0.min(t1).max_element();
    let hi = t0.max(t1).min_element();
    (hi >= lo.max(0.)).then_some(lo.max(0.))
}

fn is_board(r: &Option<String>) -> bool {
    r.as_deref().is_some_and(board)
}

fn board(r: &str) -> bool {
    r.starts_with("board:")
}

impl Sim {
    pub fn new() -> Self {
        let lay = layout::layout(&[]);
        let bounds = lay.room;
        Self {
            lay,
            walls: Vec::new(),
            hits: Vec::new(),
            cur: 0,
            scope: None,
            boxes: Vec::new(),
            bounds,
            pos: P { x: 0., z: 0. },
            facing: std::f64::consts::PI,
            speed: 0.,
            pace: 0.,
            key_dir: None,
            vel: P { x: 0., z: 0. },
            run: false,
            look: P { x: 0., z: 0. },
            route: Vec::new(),
            route_goal: None,
            came_to: None,
            placed: false,
            moved: false,
            yaw: HOME_YAW,
            follow_k: 0.,
            close_k: 0.,
            dist: 13.,
            zoom_z: 0.,
            follow_target: 0.,
            eased: Eased {
                dist: 20.,
                pitch: 0.9,
                ..Default::default()
            },
            near: None,
            focus_ref: None,
            zoom_focus: None,
            still_for: 0.,
            held: false,
            turned: false,
            campus_yaw: CAMPUS_YAW,
            shown_for: None,
            fit_cache: (String::new(), 20., 0., 0.),
            inset: 0.,
            cover: 0.,
            motion: true,
            keys: Default::default(),
            pointer: None,
            hover: None,
            size: Vec2::new(1440., 900.),
            told_view: String::new(),
            notes: Vec::new(),
            dirty: true,
            camera: Camera::new(Vec3::new(0., 10., 10.), Vec3::ZERO, 1.6),
        }
    }

    fn room_bounds(&self, i: usize) -> Rect {
        let b = self.lay.rooms[i].r#box;
        Rect {
            x0: b.x0 + RADIUS,
            z0: b.z0 + RADIUS,
            x1: b.x1 - RADIUS,
            z1: b.z1 - RADIUS,
        }
    }

    fn aspect(&self) -> f64 {
        (self.size.x / self.size.y.max(1.)) as f64
    }

    /// set_layout takes a new campus (the model's areas changed). `visit` is
    /// where you were last time, the first time it's laid out.
    pub fn set_layout(&mut self, lay: Campus, walls: Vec<f64>, visit: Option<Visit>) {
        let n = lay.rooms.len();
        self.lay = lay;
        self.walls = walls;
        self.boxes = layout::walkable(&self.lay, RADIUS);
        if n == 0 {
            return;
        }
        self.cur = (if self.placed {
            layout::room_at(&self.lay, self.pos).unwrap_or(self.cur)
        } else {
            0
        })
        .min(n - 1);
        if let Some(s) = self.scope {
            self.scope = Some(s.min(n - 1));
        }
        self.bounds = self.room_bounds(self.cur);
        self.fit_cache.0.clear();
        if let (false, Some(v)) = (self.placed, visit) {
            self.pos = v.pos;
            self.cur = layout::room_at(&self.lay, self.pos).unwrap_or(0);
            self.scope = v.scope.map(|s| s.min(n - 1));
            self.bounds = self.room_bounds(self.cur);
            self.show_scope();
            self.facing = v.facing;
            self.came_to = layout::nearest(&self.lay, self.pos);
            self.zoom_to(v.follow / 2.);
            self.follow_k = v.follow;
            self.yaw = v.yaw;
            self.dist = v.dist;
            self.zoom_z = if v.follow < 1. {
                v.follow / 2.
            } else {
                0.5 + ((FOLLOW_FAR - v.dist) / (FOLLOW_FAR - FOLLOW_NEAR)) * 0.5
            };
            self.moved = true;
            self.placed = true;
            let back = path(&self.boxes, &self.bounds, self.pos, self.pos);
            if let Some(b) = back.last() {
                self.pos = *b;
            }
            self.still_for = 1.;
            // Back where you were, close up already: no fade in from afar.
            self.focus_ref = layout::nearest(&self.lay, self.pos);
            // Back from a chat a step out, gliding in to the desk again.
            if self.focus_ref.is_some() {
                self.close_k = 0.;
            }
        } else if !self.placed {
            // In the first room, the camera over the whole campus.
            self.cur = 0;
            self.scope = None;
            self.yaw = CAMPUS_YAW;
            self.bounds = self.room_bounds(0);
            self.pos = P {
                x: self.lay.spawn.x,
                z: self.lay.spawn.z,
            };
            self.facing = self.lay.spawn.face;
            self.placed = true;
            self.show_scope();
        } else {
            // Where you stood may be a desk now: step to the nearest free floor.
            let r = path(&self.boxes, &self.bounds, self.pos, self.pos);
            if let Some(b) = r.last() {
                self.pos = *b;
            }
            if let Some(g) = self.route_goal.clone() {
                self.walk_to(&g);
            }
        }
        self.tell_view();
        self.poke();
    }

    /// visit is where you are, kept for coming back.
    pub fn visit(&self) -> Visit {
        Visit {
            pos: self.pos,
            facing: self.facing,
            follow: self.follow_target,
            yaw: self.yaw,
            dist: self.dist,
            scope: self.scope,
        }
    }

    pub fn poke(&mut self) {
        self.dirty = true;
    }

    fn zoom_to(&mut self, z: f64) {
        self.zoom_z = z.clamp(0., 1.);
        self.follow_target = (self.zoom_z * 2.).min(1.);
        self.dist = if self.zoom_z <= 0.5 {
            FOLLOW_FAR
        } else {
            FOLLOW_FAR - (self.zoom_z - 0.5) * 2. * (FOLLOW_FAR - FOLLOW_NEAR)
        };
        self.tell_view();
    }

    fn home_yaw(&self) -> f64 {
        if self.scope.is_none() {
            CAMPUS_YAW
        } else {
            HOME_YAW
        }
    }

    pub fn scope_box(&self) -> Rect {
        match self.scope {
            None => self.lay.room,
            Some(s) => self.lay.rooms[s].r#box,
        }
    }

    fn scope_wall(&self) -> f64 {
        match self.scope {
            None => self.walls.iter().copied().fold(0., f64::max),
            Some(s) => self.walls.get(s).copied().unwrap_or(3.),
        }
    }

    /// shown is how far a room's things are moved from where you walk in it:
    /// over the campus a floor past the first stands on its building.
    pub fn shown(&self, room: usize) -> Vec3 {
        match (self.scope, self.lay.rooms.get(room)) {
            (None, Some(r)) => Vec3::new(r.shown.dx as f32, r.shown.dy as f32, r.shown.dz as f32),
            _ => Vec3::ZERO,
        }
    }

    /// in_scope is whether a room's things are drawn: in its own view, or
    /// over the campus.
    pub fn in_scope(&self, room: i64) -> bool {
        room < 0 || self.scope.is_none_or(|s| s as i64 == room)
    }

    fn show_scope(&mut self) {
        // Moving a floor onto its building and back, the camera moves with
        // it, so the glide in or out starts where it was on screen.
        if self.shown_for == Some(None) && self.scope.is_some() {
            self.campus_yaw = self.eased.yaw;
        }
        if let Some(prev) = self.shown_for
            && prev != self.scope
        {
            let s = match (prev, self.scope) {
                (None, Some(i)) => self.lay.rooms.get(i).map(|r| r.shown),
                (Some(i), None) => self.lay.rooms.get(i).map(|r| r.shown),
                _ => None,
            };
            let sign = if prev.is_none() { -1. } else { 1. };
            if let (Some(s), true) = (s, self.eased.init) {
                self.eased.tx += sign * s.dx;
                self.eased.ty += sign * s.dy;
                self.eased.tz += sign * s.dz;
            }
        }
        self.shown_for = Some(self.scope);
        self.dirty = true;
    }

    fn tell_view(&mut self) {
        let rm = self.scope.and_then(|s| self.lay.rooms.get(s));
        let note = Note::View {
            project: rm.map(|r| r.project.clone()),
            floor: rm.map(|r| r.floor).unwrap_or(1),
            floors: rm.map(|r| r.floors).unwrap_or(1),
            overview: self.follow_target == 0.,
        };
        let k = format!("{note:?}");
        if k != self.told_view {
            self.told_view = k;
            self.notes.push(note);
        }
    }

    fn started(&mut self) {
        self.held = false;
        self.turned = false;
        // Moving, you're in your room's view, not the campus's.
        if self.scope != Some(self.cur) {
            self.scope = Some(self.cur);
            self.fit_cache.0.clear();
            self.yaw = HOME_YAW;
            self.show_scope();
        }
        self.zoom_focus = None;
        if self.moved {
            return;
        }
        self.moved = true;
        self.notes.push(Note::Moved);
    }

    /// into goes into a room: you come in at its door if you were in
    /// another, and the camera frames it.
    fn into(&mut self, i: usize) {
        if i != self.cur {
            self.cur = i;
            let s0 = self.lay.rooms[i].spawn;
            self.pos = P { x: s0.x, z: s0.z };
            self.facing = s0.face;
            self.route.clear();
            self.bounds = self.room_bounds(i);
            self.speed = 0.;
        }
        if self.scope != Some(i) {
            self.scope = Some(i);
            self.fit_cache.0.clear();
            self.yaw = HOME_YAW;
            self.show_scope();
        }
    }

    /// enter shows a room from its overview, you at its door.
    pub fn enter(&mut self, i: usize) {
        if i >= self.lay.rooms.len() {
            return;
        }
        self.into(i);
        self.zoom_to(0.);
        self.zoom_focus = None;
        self.turned = false;
        self.held = true;
        self.yaw = HOME_YAW;
        self.tell_view();
        self.poke();
    }

    /// walk_to walks you to a desk, a lead's counter or a board ("board" is
    /// the board of the room you're in).
    pub fn walk_to(&mut self, ref0: &str) {
        if self.lay.rooms.is_empty() {
            return;
        }
        let r = if ref0 == "board" {
            layout::board_ref(&self.lay.rooms[self.cur])
        } else {
            ref0.to_string()
        };
        let (Some(ri), Some(spot)) = (
            layout::room_of(&self.lay, &r),
            layout::spot_of(&self.lay, &r),
        ) else {
            return;
        };
        // In another room: there already, at it, the camera gliding in.
        let other = ri != self.cur;
        self.into(ri);
        if other {
            self.pos = P {
                x: spot.x,
                z: spot.z,
            };
            self.facing = spot.face;
        }
        self.route = path(
            &self.boxes,
            &self.bounds,
            self.pos,
            P {
                x: spot.x,
                z: spot.z,
            },
        );
        self.route_goal = Some(r.clone());
        self.came_to = Some(r);
        self.zoom_to(self.zoom_z.max(0.5));
        self.started();
        self.tell_view();
        self.poke();
    }

    fn walk_to_point(&mut self, p: P) {
        // On another room's floor: into it, from its door.
        if let Some(ri) = layout::room_at(&self.lay, p)
            && ri != self.cur
        {
            self.into(ri);
        }
        self.route = path(&self.boxes, &self.bounds, self.pos, p);
        self.route_goal = None;
        self.came_to = None;
        self.notes.push(Note::UserMove);
        // From the overview a click on the floor comes halfway down, not all
        // the way behind you.
        if self.zoom_z == 0. {
            self.zoom_to(0.24);
        }
        self.started();
        self.poke();
    }

    /// overview steps back: to the room's overview, or from there to the campus.
    pub fn overview(&mut self) {
        if self.scope.is_some() && self.follow_target == 0. && self.near.is_none() {
            return self.campus();
        }
        self.zoom_to(0.);
        self.zoom_focus = None;
        self.turned = false;
        self.yaw = HOME_YAW;
        self.held = true;
        self.tell_view();
        self.poke();
    }

    /// campus stands back over every room.
    pub fn campus(&mut self) {
        self.scope = None;
        let yaw = self.campus_yaw;
        self.fit_cache.0.clear();
        self.show_scope();
        self.zoom_to(0.);
        self.zoom_focus = None;
        self.turned = false;
        self.yaw = yaw;
        self.held = true;
        self.tell_view();
        self.poke();
    }

    /// floor goes to another floor of the project whose room you're in.
    pub fn floor(&mut self, f: i64) {
        let Some(here) = self.lay.rooms.get(self.cur) else {
            return;
        };
        let p = here.project.clone();
        if let Some(i) = self
            .lay
            .rooms
            .iter()
            .position(|r| r.project == p && r.floor == f)
            && Some(i) != self.scope
        {
            self.enter(i);
        }
    }

    pub fn set_inset(&mut self, left: f64) {
        self.inset = left;
        self.fit_cache.0.clear();
        self.poke();
    }

    pub fn set_cover(&mut self, right: f64) {
        if right != self.cover {
            self.cover = right;
            self.poke();
        }
    }

    // ---- The frame ----

    /// step moves you and the camera by dt seconds; true while either is
    /// still moving (the view keeps asking for frames until it's false).
    pub fn step(&mut self, dt: f64) -> bool {
        let mut moving = false;
        // The keys walk you, relative to where the camera looks.
        let k = |s: &Self, a: &str, b: &str| s.keys.contains(a) || s.keys.contains(b);
        let mut kx = 0.;
        let mut kz = 0.;
        if k(self, "w", "up") {
            kz -= 1.;
        }
        if k(self, "s", "down") {
            kz += 1.;
        }
        if k(self, "a", "left") {
            kx -= 1.;
        }
        if k(self, "d", "right") {
            kx += 1.;
        }
        let mut vx = 0.;
        let mut vz = 0.;
        let mut stepped = 0.;
        let motion = self.motion;
        // Speeds ease up and down: you set off and come to a stop, not jump to a pace.
        let toward = |v: f64, want: f64| {
            if motion {
                if want > v {
                    want.min(v + ACCEL * dt)
                } else {
                    want.max(v - BRAKE * 1.6 * dt)
                }
            } else {
                want
            }
        };
        if kx != 0. || kz != 0. {
            self.route.clear();
            self.route_goal = None;
            if self.came_to.is_some() || self.pace == 0. {
                self.notes.push(Note::UserMove);
            }
            self.came_to = None;
            // Down from the overview gently, a little behind you.
            if self.zoom_z < 0.35 {
                self.zoom_to(0.35);
            }
            self.started();
            let len = f64::hypot(kx, kz);
            let (s, c) = self.eased.yaw.sin_cos();
            let d = P {
                x: (kx * c + kz * s) / len,
                z: (-kx * s + kz * c) / len,
            };
            // Set off from a walk to a desk at the pace you had.
            if self.key_dir.is_none() && self.pace > 0. {
                self.vel = P {
                    x: self.facing.sin() * self.pace,
                    z: self.facing.cos() * self.pace,
                };
            }
            let top = if self.run { RUN } else { SPEED };
            self.vel = accelerate(
                self.vel,
                P {
                    x: d.x * top,
                    z: d.z * top,
                },
                dt,
                motion,
            );
            self.pace = self.vel.x.hypot(self.vel.z);
            self.key_dir = Some(d);
            vx = self.vel.x;
            vz = self.vel.z;
        } else if let Some(goal) = self.route.first().copied() {
            let dx = goal.x - self.pos.x;
            let dz = goal.z - self.pos.z;
            let dist = dx.hypot(dz);
            let left = dist
                + if self.route.len() > 1 {
                    path_length(self.route[0], &self.route[1..])
                } else {
                    0.
                };
            // A long walk, or one you asked for by name, is a run; a short
            // one a walk. Slowing in time to stop where it ends.
            let top = if self.route_goal.is_some() || left > 7. || self.run {
                RUN
            } else {
                SPEED
            };
            self.pace = toward(self.pace, top.min((2. * BRAKE * left).sqrt().max(0.9)));
            let step_len = self.pace * dt;
            if dist <= step_len {
                self.pos = goal;
                self.route.remove(0);
                // Past a corner you keep walking; at the end you've stopped.
                stepped = dist;
                if self.route.is_empty() {
                    self.pace = 0.;
                    if let Some(g) = &self.route_goal
                        && !motion
                        && let Some(spot) = layout::spot_of(&self.lay, g)
                    {
                        self.facing = spot.face;
                    }
                }
            } else {
                vx = dx / dist * self.pace;
                vz = dz / dist * self.pace;
            }
        } else if self.key_dir.is_some() && self.pace > 0.05 {
            // The keys let go: a few steps' stop, the way you were going.
            self.vel = accelerate(self.vel, P { x: 0., z: 0. }, dt, motion);
            self.pace = self.vel.x.hypot(self.vel.z);
            vx = self.vel.x;
            vz = self.vel.z;
        }
        if self.route.is_empty() && kx == 0. && kz == 0. && self.pace <= 0.05 {
            self.pace = 0.;
            self.key_dir = None;
            self.vel = P { x: 0., z: 0. };
        }
        if vx != 0. || vz != 0. {
            let next = if self.key_dir.is_some() && self.route.is_empty() {
                slide(
                    &self.boxes,
                    &self.bounds,
                    self.pos,
                    P {
                        x: vx * dt,
                        z: vz * dt,
                    },
                )
            } else {
                P {
                    x: self.pos.x + vx * dt,
                    z: self.pos.z + vz * dt,
                }
            };
            let moved1 = (next.x - self.pos.x).hypot(next.z - self.pos.z);
            // Up against something, the way into it stops: you slide on along
            // it at what's left, and don't carry the push round a corner.
            if self.key_dir.is_some() && dt > 0. {
                if (next.x - self.pos.x).abs() < (vx * dt).abs() * 0.5 {
                    self.vel.x = (next.x - self.pos.x) / dt;
                }
                if (next.z - self.pos.z).abs() < (vz * dt).abs() * 0.5 {
                    self.vel.z = (next.z - self.pos.z) / dt;
                }
            }
            self.pos = next;
            self.speed = moved1 / dt.max(1e-3);
            // You turn to where the keys point (the way you're going follows
            // in a curve), or along a walk to a desk.
            let want = match (self.route.is_empty(), self.key_dir) {
                (true, Some(d)) if kx != 0. || kz != 0. => d.x.atan2(d.z),
                _ => vx.atan2(vz),
            };
            self.facing = if motion {
                ease_turn(self.facing, want, dt)
            } else {
                turn(self.facing, want, std::f64::consts::PI)
            };
            moving = true;
            self.still_for = 0.;
        } else if stepped > 0. && !self.route.is_empty() {
            self.speed = stepped / dt.max(1e-3);
            moving = true;
            self.still_for = 0.;
        } else {
            self.speed = 0.;
            self.still_for += dt;
            // Arrived: turn to face what you walked to.
            if let Some(g) = &self.route_goal
                && let Some(spot) = layout::spot_of(&self.lay, g)
            {
                let f = turn(
                    self.facing,
                    spot.face,
                    if motion {
                        dt * 8.
                    } else {
                        std::f64::consts::PI
                    },
                );
                if (f - self.facing).abs() > 1e-3 {
                    moving = true;
                }
                self.facing = f;
            }
        }
        // What you're at, once you've stopped. Standing back for the
        // overview, you're at nothing; walking to a desk by name, its card
        // is already up on the way; over the campus, at nothing.
        let at = if self.held || self.scope.is_none() {
            None
        } else if self.still_for > 0.15 {
            layout::nearest(&self.lay, self.pos)
        } else if self.route_goal.is_some() && !self.route.is_empty() {
            self.route_goal.clone()
        } else {
            None
        };
        if at != self.near {
            self.near = at;
            self.notes.push(Note::Near(
                self.near
                    .as_ref()
                    .map(|n| if board(n) { "board".into() } else { n.clone() }),
            ));
            self.dirty = true;
        }
        let cam = self.cam_step(dt);
        cam || moving
    }

    /// focus_of is what the close-up looks at, standing at ref: a desk's
    /// monitor, a counter's, or the board, and from how far.
    fn focus_of(&self, r: &str, aspect: f64) -> Option<Focus> {
        let tan = (FOV / 2.).to_radians().tan();
        if board(r) {
            // From high over the first row, so no desk is in the way, the
            // whole board in view.
            let rm = &self.lay.rooms[layout::room_of(&self.lay, r).unwrap_or(self.cur)];
            let bh = (rm.board.w * 0.52).min(2.6);
            let free = 1.;
            let pitch = 0.4;
            // Wide enough for you, standing at its left end, too.
            let dist = ((rm.board.w + 3.6) / (free * 2. * tan * aspect))
                .max(((bh + 0.9) * f64::cos(pitch)) / (0.7 * 2. * tan));
            let vw = 2. * dist * tan * aspect;
            return Some(Focus {
                x: rm.board.x + (vw * (1. - free)) / 2.,
                y: 0.45 + bh / 2.,
                z: rm.oz + 0.1,
                dist,
                pitch,
                yaw: 0.,
            });
        }
        for a in &self.lay.areas {
            let ar = &a.area;
            // The angle you're at: within a little of the room's own angle,
            // you stand beside the agent for that view, never between.
            if let Some(l) = &ar.lead
                && r == format!("{}/lead", ar.project)
            {
                return Some(self.aside(
                    Focus {
                        x: l.x - 0.4,
                        y: 1.1,
                        z: l.z - 0.2,
                        dist: 17.,
                        pitch: 0.7,
                        yaw: self.yaw.clamp(0.1, 0.75),
                    },
                    aspect,
                ));
            }
            // One angle for every desk, a little from the right as the
            // room's view is: N from desk to desk doesn't swing the room round.
            if let Some(d) = ar.desks.iter().find(|d| d.id == r) {
                return Some(self.aside(
                    Focus {
                        x: d.x + d.side as f64 * 0.35,
                        y: 0.9,
                        z: d.z - 0.3,
                        dist: 22.,
                        pitch: 0.85,
                        yaw: self.yaw.clamp(0.1, 0.75),
                    },
                    aspect,
                ));
            }
        }
        None
    }

    /// aside moves a close-up's centre right, so what it looks at sits in the
    /// middle of the part of the view the desk's card (or chat) leaves.
    fn aside(&self, f: Focus, aspect: f64) -> Focus {
        let w = (self.size.x as f64).max(1.);
        let share = (if self.cover > 0. { self.cover } else { CARD } / w).min(0.6);
        let vw = 2. * f.dist * (FOV / 2.).to_radians().tan() * aspect;
        let shift = vw * share / 2.;
        Focus {
            x: f.x + f.yaw.cos() * shift,
            z: f.z - f.yaw.sin() * shift,
            ..f
        }
    }

    /// fit_distance is how far back the overview stands for the whole room
    /// (or campus), its floor and the tops of its walls, to be in view:
    /// found by trying, since a tilted view of a box has no neat formula.
    fn fit_distance(&mut self, bx: Rect, h: f64, aspect: f64, pitch: f64) -> (f64, f64, f64) {
        // Over the campus, as it's turned: after a drag every building's in view.
        let yaw0 = if self.scope.is_none() {
            self.yaw
        } else {
            self.home_yaw()
        };
        let key = format!(
            "{:?}:{:.2}:{:.2}:{:.2}:{:.2}:{h}:{aspect:.3}:{}:{yaw0}",
            self.scope, bx.x0, bx.z0, bx.x1, bx.z1, self.inset
        );
        if self.fit_cache.0 == key {
            return (self.fit_cache.1, self.fit_cache.2, self.fit_cache.3);
        }
        // The share of the width an overlay covers: the room sits in the
        // middle of what's left.
        let cover = (self.inset / (self.size.x as f64).max(1.)).min(0.45);
        // The window the diorama fills, in clip space: nearly all of it, the
        // top bar's and the legend's clear parts over its corners and edges.
        let win = if self.scope.is_none() {
            (-0.94 + 2. * cover, 0.9, -0.74, 0.78)
        } else {
            (-0.98 + 2. * cover, 0.99, -0.82, 0.9)
        };
        // The diorama's outline: the slab's underside, the walls' tops where
        // there are walls (back and left), the floor's open corner; over the
        // campus every room's, each floor where the campus shows it.
        let outlines: Vec<(Rect, f64)> = if self.scope.is_none() {
            self.lay
                .rooms
                .iter()
                .map(|r| {
                    (
                        Rect {
                            x0: r.r#box.x0 + r.shown.dx,
                            z0: r.r#box.z0 + r.shown.dz,
                            x1: r.r#box.x1 + r.shown.dx,
                            z1: r.r#box.z1 + r.shown.dz,
                        },
                        r.shown.dy,
                    )
                })
                .collect()
        } else {
            vec![(bx, 0.)]
        };
        let mut corners: Vec<Vec3> = Vec::new();
        for (b, y) in &outlines {
            for x in [b.x0 - 0.3, b.x1 + 0.3] {
                for z in [b.z0 - 0.3, b.z1 + 0.3] {
                    corners.push(Vec3::new(x as f32, (y - 0.72) as f32, z as f32));
                }
            }
            corners.push(Vec3::new(
                (b.x0 - 0.3) as f32,
                (y + h) as f32,
                (b.z0 - 0.3) as f32,
            ));
            corners.push(Vec3::new(
                (b.x1 + 0.1) as f32,
                (y + h) as f32,
                (b.z0 - 0.3) as f32,
            ));
            corners.push(Vec3::new(
                (b.x0 - 0.3) as f32,
                (y + h) as f32,
                (b.z1 + 0.1) as f32,
            ));
        }
        let right = (yaw0.cos(), -yaw0.sin());
        let back = (-yaw0.sin(), -yaw0.cos());
        let bounds = |t: (f64, f64), d: f64| {
            let eye = Vec3::new(
                (t.0 + yaw0.sin() * pitch.cos() * d) as f32,
                (pitch.sin() * d) as f32,
                (t.1 + yaw0.cos() * pitch.cos() * d) as f32,
            );
            let cam = Camera::new(eye, Vec3::new(t.0 as f32, 0., t.1 as f32), aspect as f32);
            let mut b = (f64::MAX, f64::MIN, f64::MAX, f64::MIN, false);
            for c in &corners {
                let (v, front) = cam.project(*c);
                if !front {
                    b.4 = true;
                }
                b.0 = b.0.min(v.x as f64);
                b.1 = b.1.max(v.x as f64);
                b.2 = b.2.min(v.y as f64);
                b.3 = b.3.max(v.y as f64);
            }
            b
        };
        // Closer while the outline fits the window's size; then the outline
        // is centred in the window, by moving where the camera looks, and again.
        let mut t = ((bx.x0 + bx.x1) / 2., (bx.z0 + bx.z1) / 2.);
        let mut dist = 20.;
        let tan = (FOV / 2.).to_radians().tan();
        for _ in 0..3 {
            let (mut lo, mut hi) = (3., 300.);
            for _ in 0..26 {
                let mid = (lo + hi) / 2.;
                let b = bounds(t, mid);
                if !b.4 && b.1 - b.0 < win.1 - win.0 && b.3 - b.2 < win.3 - win.2 {
                    hi = mid;
                } else {
                    lo = mid;
                }
            }
            dist = hi;
            let b = bounds(t, dist);
            let dx = (b.0 + b.1) / 2. - (win.0 + win.1) / 2.;
            let dy = (b.2 + b.3) / 2. - (win.2 + win.3) / 2.;
            // Clip space to the floor: a unit across is half the view's
            // width there; up the screen is back across the floor, foreshortened.
            let vh = dist * tan;
            t.0 += right.0 * dx * vh * aspect;
            t.1 += right.1 * dx * vh * aspect;
            let k = dy * vh / pitch.sin().max(0.2);
            t.0 += back.0 * k;
            t.1 += back.1 * k;
        }
        self.fit_cache = (key, dist, t.0, t.1);
        (dist, t.0, t.1)
    }

    /// cam_step eases the camera to where it should be; true while it's easing.
    fn cam_step(&mut self, dt: f64) -> bool {
        let aspect = self.aspect();
        let motion = self.motion;
        // From the overview down to following you is a long way: taken
        // gently. Standing back is quicker than coming down to you.
        let k = if motion {
            1. - (-dt
                * if self.follow_target < self.follow_k {
                    6.5
                } else {
                    4.2
                })
            .exp()
        } else {
            1.
        };
        self.follow_k += (self.follow_target - self.follow_k) * k;
        if (self.follow_target - self.follow_k).abs() < 1e-3 {
            self.follow_k = self.follow_target;
        }
        // Close up at what you're at, and gliding into it over a walk's last
        // few metres; only at what you walked to by name.
        let mut close_target = if self.near.is_some()
            && self.near == self.came_to
            && self.still_for > 0.15
            && !self.held
            && !self.turned
        {
            1.
        } else {
            0.
        };
        if close_target == 0. && self.route_goal.is_some() && !self.route.is_empty() {
            let rem = (self.route[0].x - self.pos.x).hypot(self.route[0].z - self.pos.z)
                + if self.route.len() > 1 {
                    path_length(self.route[0], &self.route[1..])
                } else {
                    0.
                };
            if rem < 8. {
                close_target = 1. - rem / 8.;
            }
        }
        if close_target > 0. {
            self.focus_ref = self.near.clone().or_else(|| self.route_goal.clone());
        }
        self.close_k +=
            (close_target - self.close_k) * if motion { 1. - (-dt * 6.5).exp() } else { 1. };
        if (close_target - self.close_k).abs() < 1e-3 {
            self.close_k = close_target;
        }
        // The overview fits the room, from the front and above.
        let o_pitch = OVERVIEW_PITCH;
        let (o_dist, o_x, o_z) =
            self.fit_distance(self.scope_box(), self.scope_wall(), aspect, o_pitch);
        let c = self.close_k;
        let focus = self
            .focus_ref
            .as_deref()
            .and_then(|r| self.focus_of(r, aspect));
        let f = self.follow_k;
        // Following you, or the spot the wheel zoomed in on; kept over the
        // room near its walls, so the view isn't half sky.
        let sb = self.scope.map(|s| self.lay.rooms[s].r#box);
        let clamp_to = |v: f64, a: f64, b: f64, m: f64| {
            if b - a < 2. * m {
                (a + b) / 2.
            } else {
                v.clamp(a + m, b - m)
            }
        };
        // A little ahead of you, the way you're going.
        let ahead = if motion && self.zoom_focus.is_none() {
            let (vx, vz) = if self.key_dir.is_some() {
                (self.vel.x, self.vel.z)
            } else if self.speed > 0. {
                (
                    self.facing.sin() * self.speed,
                    self.facing.cos() * self.speed,
                )
            } else {
                (0., 0.)
            };
            P {
                x: vx * LOOK_AHEAD,
                z: vz * LOOK_AHEAD,
            }
        } else {
            P { x: 0., z: 0. }
        };
        let kl = if motion { 1. - (-dt * 2.5).exp() } else { 1. };
        self.look.x += (ahead.x - self.look.x) * kl;
        self.look.z += (ahead.z - self.look.z) * kl;
        let fx0 = self
            .zoom_focus
            .map(|z| z.x)
            .unwrap_or(self.pos.x + self.look.x);
        let fz0 = self
            .zoom_focus
            .map(|z| z.z)
            .unwrap_or(self.pos.z + self.look.z);
        let (fx, fz) = match sb {
            Some(b) => (
                clamp_to(fx0, b.x0, b.x1, 5.),
                clamp_to(fz0, b.z0, b.z1, 3.5),
            ),
            None => (fx0, fz0),
        };
        let mut tx = o_x + (fx - o_x) * f;
        let mut tz = o_z + (fz - o_z) * f;
        let mut ty = 0.9 * f;
        let mut dist = o_dist + (self.dist - o_dist) * f;
        let mut pitch = o_pitch + (FOLLOW_PITCH - o_pitch) * f;
        let mut yaw = self.yaw;
        if let (Some(fo), true) = (focus, c > 0.) {
            tx += (fo.x - tx) * c;
            ty += (fo.y - ty) * c;
            tz += (fo.z - tz) * c;
            dist += (fo.dist - dist) * c;
            pitch += (fo.pitch - pitch) * c;
            yaw += (fo.yaw - yaw) * c;
        }
        let e0 = self.eased;
        if !self.eased.init {
            self.eased = Eased {
                tx,
                ty,
                tz,
                dist,
                pitch,
                yaw,
                init: true,
            };
        } else {
            // Standing back for the overview settles quickly; following you,
            // gently; at the board quicker still.
            let rate = if is_board(&self.focus_ref) && self.close_k > 0.5 {
                14.
            } else if self.held {
                10.
            } else {
                8.
            };
            let e = if motion { 1. - (-dt * rate).exp() } else { 1. };
            let ey = if motion { 1. - (-dt * 10.).exp() } else { 1. };
            let s = &mut self.eased;
            s.tx += (tx - s.tx) * e;
            s.ty += (ty - s.ty) * e;
            s.tz += (tz - s.tz) * e;
            s.dist += (dist - s.dist) * e;
            s.pitch += (pitch - s.pitch) * e;
            s.yaw += (yaw - s.yaw) * ey;
        }
        let s = self.eased;
        let cp = s.pitch.cos();
        let eye = Vec3::new(
            (s.tx + s.yaw.sin() * cp * s.dist) as f32,
            (s.ty + s.pitch.sin() * s.dist) as f32,
            (s.tz + s.yaw.cos() * cp * s.dist) as f32,
        );
        self.camera = Camera::new(
            eye,
            Vec3::new(s.tx as f32, s.ty as f32, s.tz as f32),
            aspect as f32,
        );
        // Still easing while it moves more than a hair a frame.
        let moved = (s.tx - e0.tx).abs()
            + (s.tz - e0.tz).abs()
            + (s.ty - e0.ty).abs()
            + (s.dist - e0.dist).abs() * 0.1
            + (s.pitch - e0.pitch).abs() * 10.
            + (s.yaw - e0.yaw).abs() * 10.;
        !e0.init
            || moved > 1e-4
            || self.follow_k != self.follow_target
            || self.close_k != close_target
    }

    // ---- Input ----

    fn ndc(&self, x: f64, y: f64) -> Vec2 {
        Vec2::new(
            (x / self.size.x as f64 * 2. - 1.) as f32,
            (-(y / self.size.y as f64) * 2. + 1.) as f32,
        )
    }

    /// floor_at is the floor under a point of the view, whatever stands on it.
    pub fn floor_at(&self, x: f64, y: f64) -> Option<P> {
        let (o, d) = self.camera.ray(self.ndc(x, y));
        floor_hit(o, d)
    }

    fn pick(&self, x: f64, y: f64) -> Aim {
        let (o, d) = self.camera.ray(self.ndc(x, y));
        // Only what's shown takes a click.
        let mut best: Option<(f32, &Hit)> = None;
        for h in &self.hits {
            if !self.in_scope(h.room) || (h.campus_only && self.scope.is_some()) {
                continue;
            }
            let off = if h.room >= 0 {
                self.shown(h.room as usize)
            } else {
                Vec3::ZERO
            };
            if let Some(t) = ray_box(o, d, h.min + off, h.max + off)
                && best.is_none_or(|(bt, _)| t < bt)
            {
                best = Some((t, h));
            }
        }
        if let Some((_, h)) = best {
            return Aim {
                r#ref: Some(h.r#ref.clone()),
                floor: None,
                bubble: h.bubble,
            };
        }
        Aim {
            r#ref: None,
            floor: floor_hit(o, d),
            bubble: false,
        }
    }

    fn cam_at(&self) -> String {
        let e = self.eased;
        format!(
            "{:.2}:{:.2}:{:.2}:{:.3}:{:.3}",
            e.tx, e.tz, e.dist, e.pitch, e.yaw
        )
    }

    pub fn pointer_down(&mut self, x: f64, y: f64) {
        // What's under the pointer as it's pressed is what the click is for,
        // even if the camera moves on before it's released.
        self.pointer = Some(Pointer {
            x,
            y,
            dragged: false,
            aim: self.pick(x, y),
            cam: self.cam_at(),
        });
        self.poke();
    }

    /// pointer_move is the pointer moving, by (mx) since the last move;
    /// true while dragging (the room turns).
    pub fn pointer_move(&mut self, x: f64, y: f64, mx: f64) -> bool {
        if let Some(p) = &mut self.pointer {
            if !p.dragged && (x - p.x).hypot(y - p.y) > 5. {
                p.dragged = true;
            }
            if p.dragged {
                // Dragging turns the room, within limits; turning steps out of
                // a close-up, so the turn shows at once.
                if self.close_k > 0.3 && !self.held && !self.turned {
                    self.turned = true;
                    self.yaw = self.eased.yaw;
                }
                // Over the campus a little either way.
                let (lo, hi) = if self.scope.is_none() {
                    (0.35, 0.35)
                } else {
                    (0.7, 0.5)
                };
                self.yaw =
                    (self.yaw - mx * 0.0035).clamp(self.home_yaw() - lo, self.home_yaw() + hi);
                self.poke();
                return true;
            }
        }
        let over = self.pick(x, y).r#ref;
        if over != self.hover {
            self.hover = over;
            let shown = self
                .hover
                .clone()
                .filter(|h| !h.starts_with("room:"))
                .map(|h| if board(&h) { "board".into() } else { h });
            self.notes.push(Note::Hover(shown));
            self.poke();
        }
        false
    }

    pub fn pointer_leave(&mut self) {
        if self.hover.is_some() {
            self.hover = None;
            self.notes.push(Note::Hover(None));
            self.poke();
        }
    }

    /// pointer_up is a release: a click (not a drag) walks or opens.
    pub fn pointer_up(&mut self, x: f64, y: f64) {
        let Some(p) = self.pointer.take() else { return };
        if p.dragged {
            return;
        }
        let aim = if p.cam == self.cam_at() {
            p.aim
        } else {
            self.pick(x, y)
        };
        // On the way to a board, a click on what's in front of it waits.
        if aim.r#ref.is_some()
            && is_board(&self.route_goal)
            && (!self.route.is_empty() || self.close_k < 0.95)
        {
            return;
        }
        // Over the campus, a click anywhere on a room goes into it (a "!"
        // over a room's agent goes to that agent).
        if self.scope.is_none() {
            if let (Some(r), true) = (&aim.r#ref, aim.bubble) {
                let r = r.clone();
                return self.walk_to(&r);
            }
            let ri = match (&aim.r#ref, aim.floor) {
                (Some(r), _) if r.starts_with("room:") => r[5..].parse().ok(),
                (Some(r), _) => layout::room_of(&self.lay, r),
                (None, Some(f)) => layout::room_at_margin(&self.lay, f, 0.4),
                _ => None,
            };
            if let Some(ri) = ri {
                self.enter(ri);
            }
            return;
        }
        if let Some(r) = aim.r#ref {
            if r.starts_with("room:") {
                return;
            }
            if Some(&r) == self.near.as_ref() && self.route.is_empty() {
                if !board(&r) {
                    self.notes.push(Note::Activate(r));
                }
            } else {
                self.walk_to(&r);
            }
        } else if let Some(f) = aim.floor {
            self.walk_to_point(f);
        }
    }

    /// wheel zooms in steps from the overview down to following you, and
    /// then closer; `d` is the wheel's delta in pixels (positive: outwards).
    pub fn wheel(&mut self, x: f64, y: f64, d: f64) {
        let n = (if d.abs() >= 50. {
            d.signum() * (d.abs() / 120.).max(1.)
        } else {
            d / 100.
        })
        .clamp(-3., 3.);
        // Over the campus, a turn inwards goes into the room under the
        // pointer; at a room's overview, a turn outwards stands back to the campus.
        let over = self.floor_at(x, y);
        let hit = if self.scope.is_none() {
            Some(self.pick(x, y))
        } else {
            None
        };
        let over_room = match hit.as_ref().and_then(|h| h.r#ref.clone()) {
            Some(r) if r.starts_with("room:") => r[5..].parse().ok(),
            Some(r) => layout::room_of(&self.lay, &r),
            None => over.and_then(|o| layout::room_at_margin(&self.lay, o, 0.4)),
        };
        if self.scope.is_none() && n < 0. {
            if let Some(r) = over_room {
                self.enter(r);
            }
        } else if self.scope.is_none() && n > 0. {
            let yaw = self.yaw;
            self.campus();
            self.yaw = yaw;
        } else if self.scope.is_some() && n > 0. && self.zoom_z == 0. {
            self.campus();
        } else if self.close_k > 0.5 && n > 0. {
            // At a desk, a turn of the wheel outwards stands back from it.
            self.held = true;
            self.zoom_to(0.5);
        } else {
            // Zooming keeps the floor under the pointer where it is on
            // screen: the camera's target moves towards it as it comes in.
            if let (Some(under), true) = (over, self.close_k < 0.5) {
                let sb = self.scope_box();
                let before = (self.eased.tx, self.eased.tz, self.eased.dist);
                let z1 = (self.zoom_z - n * 0.07).clamp(0., 1.);
                let f1 = (z1 * 2.).min(1.);
                let (o_dist, cx, cz) =
                    self.fit_distance(sb, self.scope_wall(), self.aspect(), OVERVIEW_PITCH);
                let fd1 = if z1 <= 0.5 {
                    FOLLOW_FAR
                } else {
                    FOLLOW_FAR - (z1 - 0.5) * 2. * (FOLLOW_FAR - FOLLOW_NEAR)
                };
                let d1 = o_dist + (fd1 - o_dist) * f1;
                let k = d1 / before.2.max(0.01);
                let mut tx = under.x + (before.0 - under.x) * k;
                let mut tz = under.z + (before.1 - under.z) * k;
                let at = self.ndc(x, y);
                let p1 = OVERVIEW_PITCH + (FOLLOW_PITCH - OVERVIEW_PITCH) * f1;
                for _ in 0..3 {
                    let cp = p1.cos();
                    let yaw = self.eased.yaw;
                    let eye = Vec3::new(
                        (tx + yaw.sin() * cp * d1) as f32,
                        (0.9 * f1 + p1.sin() * d1) as f32,
                        (tz + yaw.cos() * cp * d1) as f32,
                    );
                    let probe = Camera::new(
                        eye,
                        Vec3::new(tx as f32, (0.9 * f1) as f32, tz as f32),
                        self.aspect() as f32,
                    );
                    let (o, dd) = probe.ray(at);
                    let Some(g) = floor_hit(o, dd) else { break };
                    tx += under.x - g.x;
                    tz += under.z - g.z;
                }
                // ...and the focus that, blended with the overview's centre, gives it.
                if f1 > 0.02 {
                    self.zoom_focus = Some(P {
                        x: (cx + (tx - cx) / f1).clamp(sb.x0, sb.x1),
                        z: (cz + (tz - cz) / f1).clamp(sb.z0, sb.z1),
                    });
                }
            }
            self.zoom_to(self.zoom_z - n * 0.07);
            if self.zoom_z == 0. {
                self.zoom_focus = None;
            }
        }
        self.poke();
    }

    /// key_down takes a key by name: w a s d, up down left right, escape, n,
    /// e, enter. True when the Office used it.
    pub fn key_down(&mut self, k: &str, repeat: bool) -> bool {
        match k {
            "w" | "a" | "s" | "d" | "up" | "down" | "left" | "right" => {
                self.keys.insert(k.to_string());
                self.poke();
                true
            }
            "shift" => {
                self.run = true;
                self.poke();
                false
            }
            "escape" if repeat => true,
            "escape" if self.near.is_some() || self.follow_target > 0. || self.scope.is_some() => {
                // Escape stands back a step: out of a close-up, then to the
                // room's overview, then to the campus.
                if self.close_k > 0.3 {
                    self.held = true;
                    self.turned = false;
                    self.yaw = HOME_YAW;
                    self.zoom_to(0.);
                    self.tell_view();
                    self.poke();
                } else if self.near.is_none() && self.scope.is_some() {
                    self.campus();
                } else {
                    self.overview();
                }
                true
            }
            "escape" => {
                self.notes.push(Note::Leave);
                true
            }
            "n" if !repeat => {
                self.notes.push(Note::Next);
                true
            }
            "e" | "enter" if !repeat => match self.near.clone() {
                Some(n) => {
                    if !board(&n) {
                        self.notes.push(Note::Activate(n));
                    }
                    true
                }
                None => false,
            },
            _ => false,
        }
    }

    pub fn key_up(&mut self, k: &str) {
        if k == "shift" {
            self.run = false;
            self.poke();
        }
        if self.keys.remove(k) {
            self.poke();
        }
    }

    /// blur lets go of every key (the window lost focus).
    pub fn blur(&mut self) {
        self.keys.clear();
        self.run = false;
    }

    /// busy is whether a key is held or a walk is under way: frames are wanted.
    pub fn busy(&self) -> bool {
        !self.keys.is_empty()
            || !self.route.is_empty()
            || self.pace > 0.
            || self.look.x.abs() + self.look.z.abs() > 0.01
    }

    /// moving_pos is where you are, in the world (your room's shown offset added).
    pub fn you(&self) -> Vec3 {
        let off = self.shown(self.cur);
        Vec3::new(self.pos.x as f32, 0., self.pos.z as f32) + off
    }
}

/// accelerate eases a velocity towards the one wanted by dt seconds: setting
/// off at ACCEL, stopping harder, and changing direction harder still, so
/// you curve into a turn and a reversal is a quick stop and start.
fn accelerate(v: P, want: P, dt: f64, motion: bool) -> P {
    if !motion {
        return want;
    }
    let (dx, dz) = (want.x - v.x, want.z - v.z);
    let gap = dx.hypot(dz);
    if gap < 1e-9 {
        return want;
    }
    let speed = v.x.hypot(v.z);
    let top = want.x.hypot(want.z);
    let rate = if top < 1e-9 {
        BRAKE * 1.6
    } else if speed < 1e-6 {
        ACCEL
    } else {
        // How far the way you're going is from the way wanted: none, ACCEL;
        // square or back, TURN_ACCEL.
        let cos = (v.x * want.x + v.z * want.z) / (speed * top);
        if cos > 0.97 && top >= speed {
            ACCEL
        } else if cos > 0.97 {
            BRAKE * 1.6
        } else {
            TURN_ACCEL
        }
    };
    let k = (rate * dt / gap).min(1.);
    P {
        x: v.x + dx * k,
        z: v.z + dz * k,
    }
}

/// ease_turn turns from one heading towards another by dt seconds: quickly,
/// at most TURN_MAX a second, slowing as it gets there.
fn ease_turn(from: f64, to: f64, dt: f64) -> f64 {
    let d = turn(0., to - from, std::f64::consts::PI * 2.);
    let step = d * (1. - (-dt * TURN_RATE).exp());
    from + step.clamp(-TURN_MAX * dt, TURN_MAX * dt)
}

impl Default for Sim {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_camera_projects_what_it_looks_at_to_the_middle() {
        let c = Camera::new(Vec3::new(5., 8., 10.), Vec3::new(1., 0., 2.), 1.6);
        let (p, front) = c.project(Vec3::new(1., 0., 2.));
        assert!(front && p.length() < 1e-4);
        let (o, d) = c.ray(Vec2::ZERO);
        let hit = floor_hit(o, d).unwrap();
        assert!((hit.x - 1.).abs() < 1e-3 && (hit.z - 2.).abs() < 1e-3);
    }

    fn len(v: P) -> f64 {
        v.x.hypot(v.z)
    }

    #[test]
    fn you_set_off_curve_and_stop_rather_than_jump() {
        let dt = 1. / 60.;
        let zero = P { x: 0., z: 0. };
        let ahead = P { x: 0., z: SPEED };
        // Setting off takes a few frames, not one.
        let mut v = accelerate(zero, ahead, dt, true);
        assert!(len(v) > 0. && len(v) < SPEED / 4.);
        for _ in 0..30 {
            v = accelerate(v, ahead, dt, true);
        }
        assert_eq!(v, ahead);
        // Turning square, the way you go swings round through a curve: part
        // way, you're going both ways at once, never stopped.
        let right = P { x: SPEED, z: 0. };
        let mut mid = v;
        for _ in 0..6 {
            mid = accelerate(mid, right, dt, true);
        }
        assert!(mid.x > 0.5 && mid.z > 0.5, "{mid:?}");
        // Letting go stops you within a few steps' length.
        let (mut v, mut went) = (ahead, 0.);
        while len(v) > 0. {
            v = accelerate(v, zero, dt, true);
            went += len(v) * dt;
        }
        assert!(went > 0.1 && went < 0.6, "{went}");
        // Reduced motion: at once.
        assert_eq!(accelerate(zero, ahead, dt, false), ahead);
    }

    #[test]
    fn you_turn_to_face_the_way_smoothly() {
        let dt = 1. / 60.;
        let mut f: f64 = 0.;
        let to = std::f64::consts::PI * 0.9;
        let first = ease_turn(f, to, dt);
        assert!(first > 0. && first <= TURN_MAX * dt + 1e-9);
        for _ in 0..30 {
            f = ease_turn(f, to, dt);
        }
        assert!((f - to).abs() < 0.05, "{f}");
        // The short way round: from just left of behind to just right.
        let g = ease_turn(3., -3., dt);
        assert!(g > 3., "{g}");
    }

    #[test]
    fn shift_runs() {
        let pace_after = |run: bool| {
            let mut s = Sim::new();
            // Out in the open, nothing in the way.
            s.bounds = Rect {
                x0: -50.,
                z0: -50.,
                x1: 50.,
                z1: 50.,
            };
            s.boxes.clear();
            if run {
                s.key_down("shift", false);
            }
            s.key_down("w", false);
            for _ in 0..90 {
                s.step(1. / 60.);
            }
            s.pace
        };
        let (walk, run) = (pace_after(false), pace_after(true));
        assert!((walk - SPEED).abs() < 0.1, "{walk}");
        assert!(run > SPEED * 1.5, "{run}");
        // Let go of Shift (or the window), and you walk again.
        let mut s = Sim::new();
        s.key_down("shift", false);
        s.key_up("shift");
        assert!(!s.run);
        s.key_down("shift", false);
        s.blur();
        assert!(!s.run);
    }

    #[test]
    fn a_ray_hits_a_box_in_its_way() {
        assert!(
            ray_box(
                Vec3::new(0., 0., -5.),
                Vec3::Z,
                Vec3::splat(-1.),
                Vec3::splat(1.)
            )
            .is_some()
        );
        assert!(
            ray_box(
                Vec3::new(3., 0., -5.),
                Vec3::Z,
                Vec3::splat(-1.),
                Vec3::splat(1.)
            )
            .is_none()
        );
    }
}
