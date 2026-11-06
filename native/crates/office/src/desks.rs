//! The Office's desks, counters, boards and campus (#668's scene.ts `build`,
//! `addDesk`, `update` and what `render` animates): everything of the campus
//! that isn't a room's diorama (decor.rs) or you (player.rs). `build` makes
//! the meshes once and registers them with the kit; `update` redraws what the
//! data changed (a monitor, the board); `draws` says what to draw this frame,
//! where, with the agents posed and the things in the camera's way stepped
//! aside; `labels` says where the name tags, bubbles and pills go, which the
//! app draws over the frame (#668 drew them as sprites and HTML chips).
//!
//! Desks, counters and boards are in the campus's coordinates as layout gives
//! them (where you walk); over the campus each room's things are moved by
//! `sim.shown(room)`.

use crate::decor::{self, diorama};
use crate::engine::LabelKind;
use crate::figures::{Mascot, hash, mascot};
use crate::glb::Image;
use crate::kit::{Draw, Kit, MeshKey, Piece};
use crate::layout::{self, COUNTER, Campus, DESK, FLOOR_LIFT, SEAT};
use crate::mesh::{
    Baker, Geo, Mat, at, cuboid, cyl, hex, hsl, plane, rbox, rbox_seg, slab, sphere, torus, trs,
};
use crate::model::{DeskTone, LeadTone, OfficeModel, PrChecks, PrState};
use crate::sim::{Hit, Sim};
use crate::textures::{
    self, BoardRow, BoardScreen, Screen, bubble_of, draw_board, draw_contact, draw_plate,
    draw_screen, flip, tone_color,
};
use glam::{Mat4, Vec3};
use std::collections::HashMap;
use std::f32::consts::{FRAC_PI_2, PI, TAU};

/// The word on a lead's counter, over its project's name.
pub const LEAD_WORD: &str = "Lead";

/// An area's label from far off: its name and, in words, its counts.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct AreaLabel {
    pub name: String,
    pub sub: String,
    pub count: i64,
}

/// What the scene draws: the model, each desk's monitor (by agent ref, and
/// each lead's, project/lead), the board, and your name.
#[derive(Clone, Debug, Default)]
pub struct SceneInput {
    pub model: OfficeModel,
    pub screens: HashMap<String, Screen>,
    pub board: BoardScreen,
    pub you: String,
    pub area_labels: HashMap<String, AreaLabel>,
}

/// A label the app draws over the frame, at a point in the world.
#[derive(Clone, Debug, PartialEq)]
pub struct SceneLabel {
    pub r#ref: String,
    pub kind: LabelKind,
    pub text: String,
    /// #rrggbb.
    pub color: String,
    pub world: Vec3,
    pub visible: bool,
}

/// Something static to draw: a mesh in its room's frame (campus coordinates),
/// moved by the room's `shown` offset over the campus. Room -1 is the
/// campus's own (drawn over the campus only).
#[derive(Clone, Copy, Debug)]
pub struct Static {
    pub room: i64,
    pub mesh: MeshKey,
    pub at: Mat4,
}

/// A wall the camera can be behind, faded when it is (decor's side walls).
struct SideWall {
    room: i64,
    left: bool,
    mesh: MeshKey,
    at: Mat4,
    opacity: f32,
}

/// A room's board: its texture (redrawn when its content changes) and where
/// its rows are in it, for a click.
pub struct BoardObj {
    pub room: usize,
    pub tex: usize,
    pub rows: Vec<BoardRow>,
    pub titles: HashMap<String, String>,
    /// Its size in pixels, and where its face is (centre, width, height).
    pub px: (u32, u32),
    pub center: Vec3,
    pub size: (f32, f32),
    key: String,
}

/// A potted plant, stepping aside as the desks do.
pub struct PlantObj {
    pub room: usize,
    /// Where it is in its room's frame.
    pub x: f32,
    pub z: f32,
    pub visible: bool,
    meshes: Vec<MeshKey>,
    scale: f32,
    rot: f32,
}

/// What a frame decided about a desk, for the labels.
#[derive(Clone, Copy, Debug, Default)]
#[allow(dead_code)]
struct Frame {
    visible: bool,
    root: Vec3,
    k: f32,
    bubble: Option<(Vec3, f32)>,
    mark: bool,
}

/// One desk, or a lead's counter: its meshes, its agent and its state.
pub struct DeskObj {
    pub r#ref: String,
    pub room: usize,
    pub lead: bool,
    pub side: i32,
    /// Where it is, as laid out (x, 0, z).
    pub at: Vec3,
    pub tone: DeskTone,
    pub title: String,
    pub pr: Option<(PrState, PrChecks)>,
    pub figure: Mascot,
    fixed: Vec<MeshKey>,
    glow: MeshKey,
    fig_keys: Vec<Vec<MeshKey>>,
    base: f32,
    k: f32,
    rx: f32,
    cz: f32,
    top: f32,
    screen_tex: usize,
    screen: Option<Screen>,
    blink: Option<i64>,
    frame: Frame,
}

/// Meshes every desk shares.
struct Shared {
    ring: MeshKey,
    pole: MeshKey,
    cloth: MeshKey,
}

/// Everything built for a campus.
pub struct Built {
    pub statics: Vec<Static>,
    pub desks: Vec<DeskObj>,
    pub boards: Vec<BoardObj>,
    pub plants: Vec<PlantObj>,
    /// What clicks land on, for `sim.hits`.
    pub hits: Vec<Hit>,
    /// Where the arrow over the agent whose card is up goes, if one is.
    pub at_mark: Option<Vec3>,
    pub you: String,
    lay: Campus,
    side_walls: Vec<SideWall>,
    shared: Shared,
    area_labels: HashMap<String, AreaLabel>,
    hues: HashMap<String, f64>,
    names: HashMap<String, String>,
}

// ---- Geometry three has that mesh.rs doesn't ----

/// ring_geo is three's RingGeometry(inner, outer, seg): flat in x and y.
pub fn ring_geo(inner: f32, outer: f32, seg: usize) -> Geo {
    let mut g = Geo::default();
    for i in 0..=seg {
        let a = i as f32 / seg as f32 * TAU;
        let (s, c) = a.sin_cos();
        for r in [inner, outer] {
            g.pos.push(Vec3::new(r * c, r * s, 0.));
            g.normal.push(Vec3::Z);
            g.uv.push(glam::Vec2::new(
                (c * r / outer + 1.) / 2.,
                (s * r / outer + 1.) / 2.,
            ));
        }
    }
    for i in 0..seg as u32 {
        let a = i * 2;
        g.indices.extend([a, a + 1, a + 2, a + 1, a + 3, a + 2]);
    }
    g
}

/// circle_geo is three's CircleGeometry(r, seg): a fan in x and y facing +z.
pub fn circle_geo(r: f32, seg: usize) -> Geo {
    let mut g = Geo::default();
    g.pos.push(Vec3::ZERO);
    g.normal.push(Vec3::Z);
    g.uv.push(glam::Vec2::splat(0.5));
    for i in 0..=seg {
        let a = i as f32 / seg as f32 * TAU;
        let (s, c) = a.sin_cos();
        g.pos.push(Vec3::new(r * c, r * s, 0.));
        g.normal.push(Vec3::Z);
        g.uv.push(glam::Vec2::new((c + 1.) / 2., (s + 1.) / 2.));
    }
    for i in 0..seg as u32 {
        g.indices.extend([0, i + 1, i + 2]);
    }
    g
}

/// lathe is three's LatheGeometry: a profile (radius, height) turned about y.
pub fn lathe(profile: &[(f32, f32)], seg: usize) -> Geo {
    let mut g = Geo::default();
    let n = profile.len();
    let normal_at = |i: usize| -> (f32, f32) {
        let seg_n = |a: usize, b: usize| {
            let (dx, dy) = (profile[b].0 - profile[a].0, profile[b].1 - profile[a].1);
            let l = dx.hypot(dy).max(1e-9);
            (dy / l, -dx / l)
        };
        let (mut x, mut y) = (0., 0.);
        if i > 0 {
            let s = seg_n(i - 1, i);
            x += s.0;
            y += s.1;
        }
        if i + 1 < n {
            let s = seg_n(i, i + 1);
            x += s.0;
            y += s.1;
        }
        let l = x.hypot(y).max(1e-9);
        (x / l, y / l)
    };
    for s in 0..=seg {
        let a = s as f32 / seg as f32 * TAU;
        let (sa, ca) = a.sin_cos();
        for (i, &(r, y)) in profile.iter().enumerate() {
            let (nx, ny) = normal_at(i);
            g.pos.push(Vec3::new(r * sa, y, r * ca));
            g.normal.push(Vec3::new(nx * sa, ny, nx * ca));
            g.uv.push(glam::Vec2::new(
                s as f32 / seg as f32,
                i as f32 / (n - 1) as f32,
            ));
        }
    }
    for s in 0..seg as u32 {
        for i in 0..n as u32 - 1 {
            let a = s * n as u32 + i;
            let b = a + n as u32;
            g.indices.extend([a, b, a + 1, a + 1, b, b + 1]);
        }
    }
    g
}

/// The unit geometries three's `geo` has, scaled per mesh.
struct Geos {
    top: Geo,
    soft: Geo,
    thin: Geo,
    bx: Geo,
    cy: Geo,
    sp: Geo,
    pl: Geo,
    circ: Geo,
    mug: Geo,
    handle: Geo,
}

impl Geos {
    fn new() -> Self {
        Self {
            top: rbox(1., 1., 1., 0.04),
            soft: rbox_seg(1., 1., 1., 4, 0.3),
            thin: rbox_seg(1., 1., 1., 2, 0.02),
            bx: cuboid(1., 1., 1.),
            cy: cyl(1., 1., 1., 20),
            sp: sphere(1., 18, 12),
            pl: plane(1., 1.),
            circ: circle_geo(1., 24),
            mug: lathe(
                &[
                    (0., 0.),
                    (0.038, 0.),
                    (0.042, 0.01),
                    (0.043, 0.1),
                    (0.036, 0.1),
                    (0.036, 0.02),
                    (0., 0.02),
                ],
                24,
            ),
            handle: torus(0.026, 0.007, 8, 16, PI),
        }
    }
}

/// s is three's mesh(): where and how scaled.
fn s(x: f32, y: f32, z: f32, sx: f32, sy: f32, sz: f32) -> Mat4 {
    trs(x, y, z, 0., 0., 0., Vec3::new(sx, sy, sz))
}

fn rot(x: f32, y: f32, z: f32, sx: f32, sy: f32, sz: f32, rx: f32, ry: f32, rz: f32) -> Mat4 {
    trs(x, y, z, rx, ry, rz, Vec3::new(sx, sy, sz))
}

fn std(c: &str) -> Mat {
    Mat::hex(c)
}

const CHAIRS: [&str; 5] = ["#e9b44c", "#4aa3a2", "#e07a5f", "#8fb39a", "#7c8bd6"];
const FRAME: &str = "#2f4a3a";

fn register(kit: &mut Kit, b: Baker) -> Vec<MeshKey> {
    b.finish().into_iter().map(|m| kit.mesh(m)).collect()
}

fn lin(hexc: &str) -> [f32; 3] {
    hex(hexc)
}

fn tint(c: &str, a: f32) -> [f32; 4] {
    let l = lin(c);
    [l[0], l[1], l[2], a]
}

fn bounds_of(rig: &crate::rig::Rig) -> (Vec3, Vec3) {
    let mut lo = Vec3::splat(f32::MAX);
    let mut hi = Vec3::splat(f32::MIN);
    for (n, (w, _)) in rig.nodes.iter().zip(rig.world(Mat4::IDENTITY)) {
        for m in &n.meshes {
            for v in &m.vertices {
                let p = w.transform_point3(Vec3::from(v.pos));
                lo = lo.min(p);
                hi = hi.max(p);
            }
        }
    }
    (lo, hi)
}

fn lead_tone(t: LeadTone) -> DeskTone {
    match t {
        LeadTone::Needs => DeskTone::Needs,
        LeadTone::Working => DeskTone::Working,
        LeadTone::Idle => DeskTone::Idle,
    }
}

fn pr_color(state: PrState, checks: PrChecks) -> &'static str {
    if checks == PrChecks::Failing {
        "#ef4444"
    } else if state == PrState::Merged {
        "#a78bfa"
    } else if state == PrState::Draft {
        "#9ca3af"
    } else {
        "#22c55e"
    }
}

// ---- Building ----

/// build makes the campus's static meshes, desks, boards and hits.
pub fn build(input: &SceneInput, lay: &Campus, kit: &mut Kit) -> Built {
    let g = Geos::new();
    let mut statics: Vec<Static> = Vec::new();
    let mut hits: Vec<Hit> = Vec::new();
    let mut side_walls: Vec<SideWall> = Vec::new();
    let mut boards: Vec<BoardObj> = Vec::new();
    let model = &input.model;
    let area_of = |p: &str| model.areas.iter().find(|a| a.project == p);
    let add = |statics: &mut Vec<Static>, kit: &mut Kit, room: i64, b: Baker, at: Mat4| {
        for k in register(kit, b) {
            statics.push(Static { room, mesh: k, at });
        }
    };

    // Each room: its diorama (decor.rs) at its place, its board.
    let contact_tex = kit.texture("desk-contact", || flip(draw_contact()));
    for (ri, rm) in lay.rooms.iter().enumerate() {
        let am = area_of(&rm.project);
        let label = am.map_or("AgentBox".to_string(), |a| a.label.clone());
        let sign = if rm.floors > 1 {
            format!("{label} {}", rm.floor)
        } else {
            label
        };
        let dio = diorama(&rm.local, &sign, am.map(|a| a.hue), kit);
        let origin = at(rm.ox as f32, 0., rm.oz as f32);
        add(&mut statics, kit, ri as i64, dio.solid, origin);
        for (side, b) in dio.side_walls {
            for k in register(kit, b) {
                side_walls.push(SideWall {
                    room: ri as i64,
                    left: side == decor::Side::Left,
                    mesh: k,
                    at: origin,
                    opacity: 1.,
                });
            }
        }

        // The board: a whiteboard in a dark green frame, a ledge below it.
        let bw = rm.board.w as f32;
        let bh = (bw * 0.52).min(2.6);
        let (px_w, px_h) = (1024u32, (1024. * bh / bw).round() as u32);
        let tex = kit.add(flip(Image {
            width: px_w,
            height: px_h,
            rgba: [0xfb, 0xfa, 0xf6, 255].repeat((px_w * px_h) as usize),
        }));
        let (bx, oz) = (rm.board.x as f32, rm.oz as f32);
        let mut b = Baker::default();
        let cy = 0.45 + bh / 2.;
        b.put(
            &g.top,
            &std(FRAME),
            s(bx, cy, oz + 0.04, bw + 0.16, bh + 0.16, 0.06),
        );
        b.put(
            &g.pl,
            &Mat::new([1., 1., 1.]).tex(tex, [1., 1.]).glow(),
            s(bx, cy, oz + 0.04 + 0.045, bw, bh, 1.),
        );
        b.put(
            &g.top,
            &std(FRAME),
            s(bx, 0.42, oz + 0.1, bw * 0.6, 0.04, 0.14),
        );
        add(&mut statics, kit, ri as i64, b, Mat4::IDENTITY);
        let bref = layout::board_ref(rm);
        hits.push(Hit {
            r#ref: bref,
            min: Vec3::new(bx - bw / 2., 0., oz),
            max: Vec3::new(bx + bw / 2., 3., oz + 0.6),
            bubble: false,
            room: ri as i64,
            campus_only: false,
        });
        boards.push(BoardObj {
            room: ri,
            tex,
            rows: Vec::new(),
            titles: HashMap::new(),
            px: (px_w, px_h),
            center: Vec3::new(bx, cy, oz + 0.085),
            size: (bw, bh),
            key: String::new(),
        });
    }

    // The campus's ground: a soft green under every building (the campus only).
    {
        let mut b = Baker::default();
        for rm in lay.rooms.iter().filter(|r| r.floor == 1) {
            let r = rm.r#box;
            let (cx, cz) = (
                ((r.x0 + r.x1) / 2.) as f32 + 0.6,
                ((r.z0 + r.z1) / 2.) as f32,
            );
            let (w, d) = ((r.x1 - r.x0) as f32, (r.z1 - r.z0) as f32);
            b.put(
                &g.top,
                &std("#dfeadb"),
                s(cx, -1.32, cz, w + 7., 0.24, d + 6.),
            );
            b.put(
                &g.top,
                &std("#c9d9c4"),
                s(cx, -1.5, cz, w + 7.6, 0.2, d + 6.6),
            );
        }
        add(&mut statics, kit, -1, b, Mat4::IDENTITY);
    }
    // A project on floors: a building. A plinth under its first floor, its
    // floors over it on posts, a glass lift up its side.
    {
        let mut b = Baker::default();
        let mut glass = Baker::default();
        let frame = std(FRAME);
        for f in lay.rooms.iter().filter(|r| r.floors > 1 && r.floor == 1) {
            let hue = area_of(&f.project).map_or(260., |a| a.hue) as f32;
            let r = f.r#box;
            let (w, d) = ((r.x1 - r.x0) as f32, (r.z1 - r.z0) as f32);
            b.put(
                &g.top,
                &Mat::new(hsl(hue / 360., 0.45, 0.66)),
                s(
                    ((r.x0 + r.x1) / 2.) as f32,
                    -1.0,
                    ((r.z0 + r.z1) / 2.) as f32,
                    w + 3.2,
                    0.2,
                    d + 3.2,
                ),
            );
            let ups: Vec<_> = lay
                .rooms
                .iter()
                .filter(|o| o.project == f.project && o.floor > 1)
                .collect();
            let top = ups.iter().map(|o| o.shown.dy as f32).fold(0., f32::max);
            for up in &ups {
                let sb = (
                    up.r#box.x0 + up.shown.dx,
                    up.r#box.z0 + up.shown.dz,
                    up.r#box.x1 + up.shown.dx,
                    up.r#box.z1 + up.shown.dz,
                );
                let below = up.shown.dy as f32 - FLOOR_LIFT as f32;
                let h = FLOOR_LIFT as f32 - 0.75;
                for (px, pz) in [
                    (sb.0 + 0.35, sb.3 - 0.35),
                    (sb.2 - 0.35, sb.3 - 0.35),
                    (sb.2 - 0.35, sb.1 + 0.35),
                ] {
                    b.put(
                        &g.top,
                        &frame,
                        s(px as f32, below + h / 2., pz as f32, 0.42, h, 0.42),
                    );
                }
            }
            // The lift: beside the building, from its plinth to its top.
            let lx = (r.x0 - 0.85) as f32;
            let lz = ups
                .iter()
                .map(|o| o.r#box.z0 + o.shown.dz)
                .fold(f64::MIN, f64::max) as f32
                + 1.6;
            let lh = top + 4.2;
            glass.put(
                &g.bx,
                &Mat::hex("#9fd3e8").opacity(0.55),
                s(lx, -0.9 + lh / 2., lz, 1.4, lh, 1.8),
            );
            for y in std::iter::once(0.).chain(ups.iter().map(|o| o.shown.dy as f32)) {
                b.put(&g.top, &frame, s(lx, y - 0.05, lz, 1.5, 0.1, 1.9));
            }
            for dx in [-0.7, 0.7] {
                for dz in [-0.9, 0.9] {
                    b.put(
                        &g.top,
                        &frame,
                        s(lx + dx, -0.9 + lh / 2., lz + dz, 0.14, lh, 0.14),
                    );
                }
            }
            b.put(
                &g.top,
                &std("#f4c66a"),
                s(lx, top / 2. + 0.9, lz, 1.1, 1.8, 1.4),
            );
        }
        add(&mut statics, kit, -1, b, Mat4::IDENTITY);
        add(&mut statics, kit, -1, glass, Mat4::IDENTITY);
    }

    // Each area: its pods' rugs, its desks, its lead's counter.
    let shared = {
        let ring = {
            let mut b = Baker::default();
            b.put(
                &ring_geo(0.85, 1., 48),
                &Mat::new([1., 1., 1.]).opacity(0.99).glow(),
                Mat4::from_rotation_x(-FRAC_PI_2),
            );
            register(kit, b)[0]
        };
        let pole = {
            let mut b = Baker::default();
            b.put(&g.cy, &std("#9aa3b2"), s(0., 0.45, 0., 0.014, 0.9, 0.014));
            register(kit, b)[0]
        };
        let cloth = {
            let mut b = Baker::default();
            b.put(&plane(0.4, 0.26), &Mat::new([1., 1., 1.]), Mat4::IDENTITY);
            register(kit, b)[0]
        };
        Shared { ring, pole, cloth }
    };
    let mut desks: Vec<DeskObj> = Vec::new();
    let mut per_room: HashMap<usize, u32> = HashMap::new();
    let plate_tex: HashMap<String, usize> = HashMap::new();
    let mut plate_tex = plate_tex;
    for place in &lay.areas {
        let Some(area) = area_of(&place.project) else {
            continue;
        };
        let floor = lay.rooms.get(place.room).map_or(1, |r| r.floor);
        let mut rugs = Baker::default();
        for (pi, pod) in place.pods.iter().enumerate() {
            let shade =
                [0., 30., -30., 15., -15.][(pi as i64 + 2 * floor - 2).rem_euclid(5) as usize];
            let hh = ((area.hue as f32 + shade + 360.) % 360.) / 360.;
            let rug = Mat::new(hsl(hh, 0.8, if pi % 2 == 1 { 0.5 } else { 0.55 }));
            let edge = Mat::new(hsl(hh, 0.6, 0.45));
            let (pw, pd) = ((pod.x1 - pod.x0) as f32, (pod.z1 - pod.z0) as f32);
            let (cx, cz) = (
                ((pod.x0 + pod.x1) / 2.) as f32,
                ((pod.z0 + pod.z1) / 2.) as f32,
            );
            rugs.put(&slab(pw, pd, 0.5, 0.02), &edge, at(cx, 0., cz));
            rugs.put(
                &slab(pw - 0.16, pd - 0.16, 0.42, 0.024),
                &rug,
                at(cx, 0., cz),
            );
        }
        add(&mut statics, kit, place.room as i64, rugs, Mat4::IDENTITY);

        for d in &place.desks {
            let variant = {
                let n = per_room.entry(place.room).or_insert(0);
                let v = *n + place.room as u32 * 3;
                *n += 1;
                v
            };
            let ai = area
                .desks
                .iter()
                .find(|x| x.id == d.id)
                .map(|x| x.ai.clone());
            desks.push(add_desk(
                &g,
                kit,
                &mut hits,
                &d.id,
                place.room,
                d.x as f32,
                d.z as f32,
                false,
                d.side,
                ai.as_deref(),
                Some(variant),
                contact_tex,
                &shared,
            ));
        }
        let Some(lead) = &place.lead else { continue };
        let (lx, lz) = (lead.x as f32, lead.z as f32);
        let mut b = Baker::default();
        b.put(
            &g.top,
            &std("#dfa86e"),
            s(
                lx,
                1.0,
                lz,
                COUNTER.w as f32 + 0.1,
                0.07,
                COUNTER.d as f32 + 0.1,
            ),
        );
        b.put(
            &g.soft,
            &Mat::new(hsl(area.hue as f32 / 360., 0.5, 0.62)),
            s(
                lx,
                0.5,
                lz + 0.05,
                COUNTER.w as f32,
                0.96,
                COUNTER.d as f32 - 0.1,
            ),
        );
        // Its front says whose it is: the project's lead, its front desk.
        let plate = *plate_tex
            .entry(place.project.clone())
            .or_insert_with(|| kit.add(flip(draw_plate(LEAD_WORD, &area.label))));
        b.put(
            &g.pl,
            &Mat::new([1., 1., 1.]).tex(plate, [1., 1.]).opacity(0.995),
            s(
                lx,
                0.52,
                lz + 0.05 + (COUNTER.d as f32 - 0.1) / 2. + 0.006,
                COUNTER.w as f32 * 0.86,
                COUNTER.w as f32 * 0.86 * (160. / 512.),
                1.,
            ),
        );
        add(&mut statics, kit, place.room as i64, b, Mat4::IDENTITY);
        desks.push(add_desk(
            &g,
            kit,
            &mut hits,
            &format!("{}/lead", area.project),
            place.room,
            lx,
            lz,
            true,
            -1,
            Some("claude"),
            None,
            contact_tex,
            &shared,
        ));
    }

    // Potted plants, Kenney's.
    let plants = lay
        .plants
        .iter()
        .enumerate()
        .map(|(i, p)| {
            let room = layout::room_at_margin(lay, *p, 0.5).unwrap_or(0);
            let meshes = plant_meshes(&g, kit);
            PlantObj {
                room,
                x: p.x as f32,
                z: p.z as f32,
                visible: true,
                meshes,
                scale: 0.9 + (i % 3) as f32 * 0.14,
                rot: i as f32 * 1.3,
            }
        })
        .collect();

    // Each room's slab, over the campus: a click on its floor goes into it.
    for (ri, rm) in lay.rooms.iter().enumerate() {
        let r = rm.r#box;
        hits.push(Hit {
            r#ref: format!("room:{ri}"),
            min: Vec3::new(r.x0 as f32, -0.65, r.z0 as f32),
            max: Vec3::new(r.x1 as f32, 0.25, r.z1 as f32),
            bubble: false,
            room: ri as i64,
            campus_only: true,
        });
    }

    let mut built = Built {
        statics,
        desks,
        boards,
        plants,
        hits,
        at_mark: None,
        you: String::new(),
        lay: lay.clone(),
        side_walls,
        shared,
        area_labels: HashMap::new(),
        hues: HashMap::new(),
        names: HashMap::new(),
    };
    built.update(input, kit);
    built
}

/// plant_meshes registers a potted plant: Kenney's, or a pot and a bush.
fn plant_meshes(g: &Geos, kit: &mut Kit) -> Vec<MeshKey> {
    let ms: Option<Vec<_>> = kit
        .piece(Piece::PottedPlant)
        .map(|p| p.model.meshes.clone());
    match ms {
        Some(ms) => ms.into_iter().map(|m| kit.mesh(m)).collect(),
        None => {
            let mut b = Baker::default();
            b.put(
                &lathe(
                    &[
                        (0., 0.),
                        (0.15, 0.),
                        (0.19, 0.36),
                        (0.215, 0.37),
                        (0.215, 0.42),
                        (0.19, 0.42),
                        (0.18, 0.38),
                        (0., 0.38),
                    ],
                    28,
                ),
                &std("#d27c55"),
                Mat4::IDENTITY,
            );
            b.put(&g.sp, &std("#3f8f55"), s(0., 0.7, 0., 0.3, 0.3, 0.3));
            register(kit, b)
        }
    }
}

#[allow(clippy::too_many_arguments)]
fn add_desk(
    g: &Geos,
    kit: &mut Kit,
    hits: &mut Vec<Hit>,
    r#ref: &str,
    room: usize,
    x: f32,
    z: f32,
    lead: bool,
    side: i32,
    ai: Option<&str>,
    variant: Option<u32>,
    contact: usize,
    _shared: &Shared,
) -> DeskObj {
    let n = hash(r#ref);
    let sd = side as f32;
    let rx = if lead { 0. } else { sd * 0.48 };
    let out = -sd;
    let cz = if lead { -0.75 } else { -(SEAT as f32) };
    let dh = DESK.h as f32;
    let top = if lead { 1.03 } else { dh + 0.025 };
    let mut b = Baker::default();
    let metal = std("#d8c7b0");
    let plastic = std("#1f2430");
    let ceramic = std("#fbfaf7");
    if !lead {
        // An oak top on two metal side frames, with a modesty panel.
        b.put(
            &g.top,
            &std("#dfa86e"),
            s(0., dh, 0., DESK.w as f32, 0.05, DESK.d as f32),
        );
        for sx in [-1., 1.] {
            b.put(
                &g.top,
                &metal,
                s(sx * 0.66, dh / 2., 0., 0.05, dh - 0.02, 0.08),
            );
            b.put(
                &g.top,
                &metal,
                s(sx * 0.66, 0.02, 0., 0.07, 0.04, DESK.d as f32 - 0.04),
            );
            b.put(
                &g.top,
                &metal,
                s(sx * 0.66, dh - 0.04, 0., 0.06, 0.03, DESK.d as f32 - 0.06),
            );
        }
        b.put(&g.top, &metal, s(0., 0.48, 0.3, 1.28, 0.32, 0.02));
        // A mug, a pen cup, a sticky note on the monitor: some of each, by desk.
        if !n.is_multiple_of(3) {
            b.put(
                &g.mug,
                &ceramic,
                rot(
                    0.02 * out,
                    dh + 0.025,
                    0.25,
                    1.,
                    1.,
                    1.,
                    0.,
                    (n % 7) as f32 * 0.8,
                    0.,
                ),
            );
            b.put(
                &g.handle,
                &ceramic,
                rot(
                    (0.02 + 0.045) * out,
                    dh + 0.075,
                    0.25,
                    1.,
                    1.,
                    1.,
                    0.,
                    FRAC_PI_2,
                    0.,
                ),
            );
        }
        if n.is_multiple_of(2) {
            b.put(
                &g.cy,
                &metal,
                s((-0.72 + 0.1) * out, dh + 0.07, -0.25, 0.035, 0.1, 0.035),
            );
            for (dx, c) in [(-0.012, "#ef6f6c"), (0.012, "#4f86f7")] {
                b.put(
                    &g.cy,
                    &std(c),
                    rot(
                        (-0.62 + dx) * out,
                        dh + 0.14,
                        -0.25,
                        0.006,
                        0.14,
                        0.006,
                        0.,
                        0.,
                        dx * 8.,
                    ),
                );
            }
        }
        // A little plant where a bench's two desks meet (Kenney's).
        if side > 0 {
            let p = [Piece::PlantSmall1, Piece::PlantSmall2, Piece::PlantSmall3][(n % 3) as usize];
            if let Some(l) = kit.piece(p) {
                b.put_model(
                    &l.model,
                    &[],
                    at(-0.72, dh + 0.025, -0.26) * Mat4::from_scale(Vec3::splat(1.3)),
                );
            }
        }
    }
    // An office chair under the agent, in one of the room's pastels.
    let seat = std(CHAIRS[n as usize % CHAIRS.len()]);
    b.put(&g.soft, &seat, s(rx, 0.45, cz, 0.5, 0.08, 0.48));
    b.put(
        &g.soft,
        &seat,
        rot(rx, 0.8, cz - 0.25, 0.48, 0.5, 0.07, 0.12, 0., 0.),
    );
    b.put(&g.top, &metal, s(rx, 0.6, cz - 0.27, 0.05, 0.22, 0.03));
    b.put(&g.cy, &metal, s(rx, 0.26, cz, 0.028, 0.34, 0.028));
    for k in 0..5 {
        let a = k as f32 / 5. * TAU + 0.3;
        b.put(
            &g.top,
            &plastic,
            rot(
                rx + a.sin() * 0.13,
                0.07,
                cz + a.cos() * 0.13,
                0.05,
                0.04,
                0.27,
                0.,
                a,
                0.,
            ),
        );
        b.put(
            &g.sp,
            &plastic,
            s(
                rx + a.sin() * 0.26,
                0.03,
                cz + a.cos() * 0.26,
                0.03,
                0.03,
                0.03,
            ),
        );
    }
    // The laptop before the agent, its lid's back glowing in its state's colour.
    let mut glow = Baker::default();
    let glow_mat = Mat::new([1., 1., 1.]).glow();
    let lx = if lead { 0.4 } else { rx };
    b.put(
        &g.thin,
        &std("#d3d7de"),
        s(lx, top + 0.012, -0.16, 0.4, 0.022, 0.27),
    );
    b.put(
        &g.pl,
        &std("#262b35"),
        rot(lx, top + 0.024, -0.13, 0.34, 0.13, 1., -FRAC_PI_2, 0., 0.),
    );
    let lid = rot(lx, top + 0.022, -0.035, 1., 1., 1., -0.75, 0., 0.);
    b.put(
        &g.thin,
        &std("#d3d7de"),
        lid * s(0., 0.1, 0., 0.4, 0.2, 0.012),
    );
    glow.put(&g.circ, &glow_mat, lid * s(0., 0.1, 0.0075, 0.03, 0.03, 1.));
    // The monitor across the desk from the agent, turned to it.
    let mon = rot(
        if lead { -0.9 } else { rx - sd * 0.55 },
        top,
        if lead { -0.05 } else { 0.2 },
        1.,
        1.,
        1.,
        0.,
        if lead { 2.45 } else { sd * (PI - 0.59) },
        0.,
    );
    let mw = if lead { 0.86 } else { 0.76 };
    let mh = mw * (320. / 512.);
    let my = 0.07 + mh / 2. + 0.06;
    let bezel = std("#141922");
    b.put(
        &g.thin,
        &bezel,
        mon * s(0., my, 0., mw + 0.05, mh + 0.05, 0.035),
    );
    b.put(
        &g.thin,
        &bezel,
        mon * s(0., my, -0.03, mw * 0.6, mh * 0.6, 0.04),
    );
    b.put(&g.top, &metal, mon * s(0., 0.09, -0.04, 0.05, 0.16, 0.03));
    b.put(&g.cy, &metal, mon * s(0., 0.006, -0.03, 0.13, 0.012, 0.09));
    let screen_tex = kit.add(flip(Image {
        width: textures::SCREEN_W,
        height: textures::SCREEN_H,
        rgba: [0x11, 0x18, 0x27, 255].repeat((textures::SCREEN_W * textures::SCREEN_H) as usize),
    }));
    b.put(
        &g.pl,
        &Mat::new([1., 1., 1.]).tex(screen_tex, [1., 1.]).glow(),
        mon * s(0., my, 0.0185, mw, mh, 1.),
    );
    glow.put(
        &g.circ,
        &glow_mat,
        mon * rot(0., my + 0.02, -0.052, 0.06, 0.06, 1., 0., PI, 0.),
    );
    if !lead && n % 4 == 1 {
        let note = [std("#ffe27a"), std("#ffb3c9"), std("#a6ecff")][(n % 3) as usize];
        b.put(
            &g.pl,
            &note,
            mon * rot(
                mw / 2. - 0.02,
                my + mh / 2. - 0.02,
                0.019,
                0.08,
                0.08,
                1.,
                0.,
                0.,
                0.12,
            ),
        );
    }
    // A soft contact shadow under it all.
    b.put(
        &g.pl,
        &Mat::new([1., 1., 1.]).tex(contact, [1., 1.]).opacity(0.995),
        rot(
            if lead { 0. } else { sd * 0.1 },
            0.03,
            if lead { -0.3 } else { -0.2 },
            if lead { 3.0 } else { 2.1 },
            if lead { 2.2 } else { 1.9 },
            1.,
            -FRAC_PI_2,
            0.,
            0.,
        ),
    );
    let fixed = register(kit, b);
    let glow_key = register(kit, glow)[0];

    // The agent, its tool's mascot, on its chair.
    let mut fig = mascot(r#ref, ai, lead, if lead { None } else { variant }, kit);
    let base = fig.rig.nodes[0].scale.x;
    fig.rig.nodes[0].pos = Vec3::new(rx, 0.49 * (1. - base), cz + 0.1);
    let fig_keys: Vec<Vec<MeshKey>> = fig
        .rig
        .nodes
        .iter()
        .map(|nd| nd.meshes.iter().map(|m| kit.mesh(m.clone())).collect())
        .collect();
    let (lo, hi) = bounds_of(&fig.rig);

    let o = Vec3::new(x, 0., z);
    // What a click picks: the desk and the robot, as one box; and the robot's
    // whole figure, big head and all.
    let (c, sz) = (
        Vec3::new(
            if lead { 0. } else { sd * 0.1 },
            0.7,
            if lead { -0.3 } else { -0.25 },
        ),
        Vec3::new(
            if lead { COUNTER.w as f32 } else { 1.5 },
            1.4,
            if lead { 1.45 } else { 1.25 },
        ),
    );
    hits.push(Hit {
        r#ref: r#ref.to_string(),
        min: o + c - sz / 2.,
        max: o + c + sz / 2.,
        bubble: false,
        room: room as i64,
        campus_only: false,
    });
    let (fc, fs) = ((lo + hi) / 2., (hi - lo) * Vec3::new(0.9, 1., 0.9));
    hits.push(Hit {
        r#ref: r#ref.to_string(),
        min: o + fc - fs / 2.,
        max: o + fc + fs / 2.,
        bubble: false,
        room: room as i64,
        campus_only: false,
    });
    let bc = Vec3::new(rx, 2.05, cz + 0.05);
    hits.push(Hit {
        r#ref: r#ref.to_string(),
        min: o + bc - Vec3::new(0.2, 0.235, 0.2),
        max: o + bc + Vec3::new(0.2, 0.235, 0.2),
        bubble: true,
        room: room as i64,
        campus_only: false,
    });

    DeskObj {
        r#ref: r#ref.to_string(),
        room,
        lead,
        side,
        at: o,
        tone: DeskTone::Idle,
        title: String::new(),
        pr: None,
        figure: fig,
        fixed,
        glow: glow_key,
        fig_keys,
        base,
        k: base,
        rx,
        cz,
        top,
        screen_tex,
        screen: None,
        blink: None,
        frame: Frame::default(),
    }
}

// ---- Keeping up with the data ----

impl Built {
    /// update brings every desk and board up to date with the data: its tone,
    /// its monitor, its title, its flag, each board's notes.
    pub fn update(&mut self, input: &SceneInput, kit: &mut Kit) {
        self.you = input.you.clone();
        self.area_labels = input.area_labels.clone();
        self.hues = input
            .model
            .areas
            .iter()
            .map(|a| (a.project.clone(), a.hue))
            .collect();
        self.names = input
            .model
            .areas
            .iter()
            .map(|a| (a.project.clone(), a.label.clone()))
            .collect();
        for d in &mut self.desks {
            let project = d.r#ref.split('/').next().unwrap_or("");
            let area = input.model.areas.iter().find(|a| a.project == project);
            let desk = if d.lead {
                None
            } else {
                area.and_then(|a| a.desks.iter().find(|x| x.id == d.r#ref))
            };
            d.tone = if d.lead {
                area.map_or(DeskTone::Idle, |a| lead_tone(a.lead.tone))
            } else {
                desk.map_or(DeskTone::Idle, |x| x.tone)
            };
            d.title = if d.lead {
                area.map_or(project.to_string(), |a| a.label.clone())
            } else {
                desk.map_or(d.r#ref.clone(), |x| x.title.clone())
            };
            d.pr = desk
                .and_then(|x| x.pr.as_ref())
                .map(|p| (p.state, p.checks));
            if let Some(sc) = input.screens.get(&d.r#ref)
                && d.screen.as_ref() != Some(sc)
            {
                d.screen = Some(sc.clone());
                kit.replace(d.screen_tex, flip(draw_screen(sc, 0.)));
            }
        }
        // Each room's board: what waits in its project, every floor's.
        for b in &mut self.boards {
            let rm = &self.lay.rooms[b.room];
            let mine = |r: &str| r.split('/').next() == Some(rm.project.as_str());
            let mut bs = input.board.clone();
            bs.needs.items.retain(|i| mine(&i.r#ref));
            bs.review.items.retain(|i| mine(&i.r#ref));
            let key = format!("{bs:?}");
            if key != b.key {
                b.key = key;
                let (img, rows) = draw_board(&bs, b.px.0, b.px.1);
                b.rows = rows;
                b.titles = bs
                    .needs
                    .items
                    .iter()
                    .chain(&bs.review.items)
                    .map(|i| (i.r#ref.clone(), i.title.clone()))
                    .collect();
                kit.replace(b.tex, flip(img));
            }
        }
    }

    /// walls is each room's wall height, for `sim.set_layout`.
    pub fn walls(&self) -> Vec<f64> {
        self.lay
            .rooms
            .iter()
            .map(|r| decor::wall_height(&r.local) as f64)
            .collect()
    }

    pub fn desk(&self, r#ref: &str) -> Option<&DeskObj> {
        self.desks.iter().find(|d| d.r#ref == r#ref)
    }

    // ---- The frame ----

    /// draws says what to draw this frame: the rooms in scope, each moved to
    /// where the campus shows it; the agents posed (looking at you when you're
    /// near); beacons' rings pulsing; what's in the camera's way stepped aside.
    /// It also blinks the cursor of the monitor you're at, which is why it
    /// takes the kit.
    pub fn draws(&mut self, sim: &Sim, t: f32, motion: bool, kit: &mut Kit, out: &mut Vec<Draw>) {
        let campus = sim.scope.is_none();
        let crowded = campus;
        let eye = sim.camera.eye;
        let size = sim.size;
        let close = sim.close_k as f32;
        let follow = sim.visit().follow as f32;
        let white = [1.; 4];
        let shown = |room: i64| {
            if room >= 0 {
                sim.shown(room as usize)
            } else {
                Vec3::ZERO
            }
        };
        let in_scope = |room: i64| if room < 0 { campus } else { sim.in_scope(room) };

        for st in &self.statics {
            if in_scope(st.room) {
                out.push(Draw {
                    mesh: st.mesh,
                    at: Mat4::from_translation(shown(st.room)) * st.at,
                    tint: white,
                });
            }
        }
        for w in &mut self.side_walls {
            // A side wall the camera is behind fades away.
            let r = &self.lay.room;
            let behind = if w.left {
                eye.x < 0.5
            } else {
                eye.x > r.x1 as f32 - 0.5
            };
            let o = if behind { 0.08 } else { 1. };
            w.opacity += (o - w.opacity) * if motion { 0.2 } else { 1. };
            if in_scope(w.room) {
                out.push(Draw {
                    mesh: w.mesh,
                    at: Mat4::from_translation(shown(w.room)) * w.at,
                    tint: [1., 1., 1., w.opacity],
                });
            }
        }

        // The camera's yaw, from where it looks.
        let target = sim.camera.target;
        let yaw = (eye.x - target.x).atan2(eye.z - target.z);
        let to_px = |p: Vec3| {
            let (n, _) = sim.camera.project(p);
            ((n.x + 1.) / 2. * size.x, (1. - n.y) / 2. * size.y)
        };

        // The board you're at: how far it is from the camera.
        let focus = sim.focus_ref.clone();
        let board_dist = match &focus {
            Some(f) if f.starts_with("board:") && close > 0.3 => layout::room_of(&self.lay, f)
                .and_then(|ri| self.lay.rooms.get(ri))
                .map_or(0., |rm| {
                    eye.distance(Vec3::new(
                        rm.board.x as f32,
                        1.5,
                        rm.board.spot.z as f32 - 2.1,
                    ))
                }),
            _ => 0.,
        };
        // In a close-up: where on screen the agent you're at and you are.
        let look_at: Option<(f32, Vec<(f32, f32)>)> = if let Some(fd) = focus
            .as_ref()
            .and_then(|f| self.desks.iter().find(|d| &d.r#ref == f))
            .filter(|_| close > 0.3)
        {
            let off = shown(fd.room as i64);
            let v = fd.at + off + Vec3::new(fd.rx, 0.49 * (1. - fd.k), fd.cz + 0.1) + Vec3::Y;
            Some((
                v.distance(eye),
                vec![
                    to_px(v),
                    to_px(Vec3::new(sim.pos.x as f32, 0.7, sim.pos.z as f32)),
                ],
            ))
        } else if !campus && follow > 0.5 {
            let v = Vec3::new(sim.pos.x as f32, 0.8, sim.pos.z as f32);
            Some((v.distance(eye), vec![to_px(v)]))
        } else {
            None
        };

        let near = sim.near.clone();
        let hover = sim.hover.clone();
        self.at_mark = None;
        let sh = &self.shared;
        for d in &mut self.desks {
            let off = shown(d.room as i64);
            let root = Vec3::new(
                d.at.x + off.x,
                off.y
                    + if hover.as_deref() == Some(d.r#ref.as_str()) {
                        0.02
                    } else {
                        0.
                    },
                d.at.z + off.z,
            );
            // Where you are, from the robot: near enough, it looks at you.
            let (fx, fz) = (d.at.x + d.rx, d.at.z + d.cz + 0.1);
            let (dx, dz) = (sim.pos.x as f32 - fx, sim.pos.z as f32 - fz);
            d.figure.pose(
                d.tone,
                t,
                motion,
                (dx * dx + dz * dz < 25.).then_some((dx, dz)),
            );
            // From afar the characters grow a little, toy-like, always seated.
            let fig_at = root + Vec3::new(d.rx, 0.49 * (1. - d.k), d.cz + 0.1);
            let far = fig_at.distance(eye);
            let k = d.base
                * (1.
                    + ((if crowded { 0.35 } else { 0.15 }) as f32)
                        .min(((far - 12.) / 36.).max(0.))
                        * (1. - follow));
            d.k = k;
            d.figure.rig.nodes[0].scale = Vec3::splat(k);
            d.figure.rig.nodes[0].pos = Vec3::new(d.rx, 0.49 * (1. - k), d.cz + 0.1);

            let is_focus = focus.as_deref() == Some(d.r#ref.as_str());
            let ahead = match (&look_at, is_focus) {
                (Some((dist, spots)), false) => {
                    let p = fig_at + Vec3::Y;
                    let (sx, sy) = to_px(p);
                    p.distance(eye) < dist - 0.8
                        && spots
                            .iter()
                            .any(|&(px, py)| (sx - px).abs() < 190. && (sy - py).abs() < 230.)
                }
                _ => false,
            };
            let in_way = close > 0.3 && !is_focus && (root.distance(eye) < 4.2 || ahead);
            let board_way = board_dist > 0. && root.distance(eye) < board_dist - 1.5;
            let visible = in_scope(d.room as i64) && !(board_way && close > 0.9);

            let glyph = bubble_of(d.tone);
            let loud_tone = matches!(d.tone, DeskTone::Needs | DeskTone::Error);
            let grow_max: f32 = if d.tone == DeskTone::Needs {
                if crowded { 5. } else { 2.3 }
            } else if loud_tone {
                if crowded { 3. } else { 1.9 }
            } else if crowded {
                1.
            } else {
                1.5
            };
            let grow = grow_max.min(((far - 8.) / 12.).max(1.));
            let mut bubble_visible =
                visible && glyph.is_some() && !in_way && !(close > 0.3 && !is_focus && far < 8.);
            if crowded && !loud_tone && far > 30. {
                bubble_visible = false;
            }
            let lift = if d.lead { 0.3 } else { 0.12 };
            let bob = if motion && d.tone == DeskTone::Needs {
                (t * 3.5).sin().abs() * if crowded { 0.12 * grow } else { 0.06 }
            } else {
                0.
            };
            let side = if crowded { 0. } else { 0.27 * k };
            let by = if crowded {
                0.49 + 1.18 * k + lift * 0.5 + (grow - 1.) * 0.2
            } else {
                0.49 + 0.86 * k + 0.12 * grow
            } + bob;
            let bpos =
                root + Vec3::new(d.rx + yaw.cos() * side, by, d.cz + 0.05 - yaw.sin() * side);
            let is_near = near.as_deref() == Some(d.r#ref.as_str());
            if is_near && visible {
                // The arrow over the agent whose card is up.
                let wob = if motion { (t * 3.).sin().abs() } else { 0. };
                let p = if d.lead {
                    root + Vec3::new(d.rx, 0.49 + 0.95 * k + wob * 0.06, d.cz + 0.1 + 0.35)
                } else {
                    root + Vec3::new(
                        d.rx,
                        by + if glyph.is_some() && bubble_visible {
                            0.42 * grow
                        } else {
                            0.
                        } + 0.05
                            + wob * 0.08,
                        d.cz + 0.1,
                    )
                };
                if close > 0.2 || follow > 0.3 {
                    self.at_mark = Some(p);
                }
            }
            d.frame = Frame {
                visible,
                root,
                k,
                bubble: bubble_visible.then_some((bpos, grow)),
                mark: is_near,
            };

            // The monitor you're at blinks its cursor.
            if is_near
                && d.tone == DeskTone::Working
                && let Some(sc) = &d.screen
            {
                let blink = (t * 2.).floor() as i64;
                if d.blink != Some(blink) {
                    d.blink = Some(blink);
                    kit.replace(d.screen_tex, flip(draw_screen(sc, t)));
                }
            }
            if !visible {
                continue;
            }

            let m_root = Mat4::from_translation(root);
            for f in &d.fixed {
                out.push(Draw {
                    mesh: *f,
                    at: m_root,
                    tint: white,
                });
            }
            // The glow in its state's colour (dark when stopped).
            let gl = lin(tone_color(d.tone));
            let gi = if d.tone == DeskTone::Stopped { 0.4 } else { 1. };
            out.push(Draw {
                mesh: d.glow,
                at: m_root,
                tint: [gl[0] * gi, gl[1] * gi, gl[2] * gi, 1.],
            });
            // The agent.
            let world = d.figure.rig.world(m_root);
            for (i, (m, vis)) in world.iter().enumerate() {
                if !vis {
                    continue;
                }
                let op = d.figure.rig.nodes[i].opacity;
                for key in &d.fig_keys[i] {
                    out.push(Draw {
                        mesh: *key,
                        at: *m,
                        tint: [1., 1., 1., op],
                    });
                }
            }
            // A ring: the desk you're at wears a soft white one, a loud desk its own colour.
            let chosen = is_near && !loud_tone;
            if chosen || loud_tone {
                let p = if motion { (t * 0.8) % 1. } else { 0.5 };
                let sc = (0.9 + p * 0.5) * grow.min(1.8);
                let c = if chosen {
                    [1., 1., 1.]
                } else {
                    lin(tone_color(d.tone))
                };
                out.push(Draw {
                    mesh: sh.ring,
                    at: m_root * at(0., 0.025, -0.1) * Mat4::from_scale(Vec3::splat(sc)),
                    tint: [c[0], c[1], c[2], 0.55 * (1. - p)],
                });
            }
            // The pull request's flag, at the desk's back corner.
            if let Some((state, checks)) = d.pr
                && state != PrState::Closed
            {
                let flag = m_root
                    * at(
                        if d.lead { 1.05 } else { -d.side as f32 * 0.6 },
                        d.top,
                        -0.28,
                    );
                out.push(Draw {
                    mesh: sh.pole,
                    at: flag,
                    tint: white,
                });
                let wave = if motion {
                    (t * 3. + d.rx).sin() * 0.25
                } else {
                    0.
                };
                out.push(Draw {
                    mesh: sh.cloth,
                    at: flag * at(0.2, 0.75, 0.) * Mat4::from_rotation_y(wave),
                    tint: tint(pr_color(state, checks), 1.),
                });
            }
        }

        // The potted plants step aside as the desks do.
        for p in &mut self.plants {
            let mut hide = false;
            if let Some((dist, spots)) = &look_at {
                let v = Vec3::new(p.x, 0.9, p.z);
                let (sx, sy) = to_px(v);
                hide = v.distance(eye) < dist - 0.8
                    && spots
                        .iter()
                        .any(|&(px, py)| (sx - px).abs() < 170. && (sy - py).abs() < 220.);
            }
            p.visible = in_scope(p.room as i64) && !hide;
            if p.visible {
                let m = Mat4::from_translation(Vec3::new(p.x, 0., p.z) + shown(p.room as i64))
                    * Mat4::from_rotation_y(p.rot)
                    * Mat4::from_scale(Vec3::splat(p.scale));
                for k in &p.meshes {
                    out.push(Draw {
                        mesh: *k,
                        at: m,
                        tint: white,
                    });
                }
            }
        }
    }

    /// labels says where the name tags, bubbles and rooms' pills go, in the
    /// world; the app draws them over the frame.
    pub fn labels(&self, sim: &Sim) -> Vec<SceneLabel> {
        let mut out = Vec::new();
        let campus = sim.scope.is_none();
        let eye = sim.camera.eye;
        let close = sim.close_k as f32;
        let follow = sim.visit().follow as f32;
        let small_room = sim
            .scope
            .and_then(|s| self.lay.rooms.get(s))
            .is_some_and(|r| r.desks.len() <= 8);
        let focus = sim.focus_ref.as_deref();
        for d in &self.desks {
            let f = &d.frame;
            let color = tone_color(d.tone).to_string();
            // Tags: in a room of up to eight, everyone's; in a bigger one the
            // loud ones, the rest as you come in. None over the campus, none for
            // the desk you're at (its card says it).
            let tag = f.root + Vec3::new(d.rx, 0.5, 0.55);
            let dist = tag.distance(eye);
            let loud = matches!(d.tone, DeskTone::Needs | DeskTone::Error)
                && (d.tone == DeskTone::Needs || follow > 0.2 || close > 0.3);
            let want = !campus
                && f.visible
                && !(focus == Some(d.r#ref.as_str()) && close > 0.3)
                && dist > 2.6
                && (loud || small_room || follow > 0.4)
                && (loud || small_room || (close < 0.3 && dist < 17.));
            out.push(SceneLabel {
                r#ref: d.r#ref.clone(),
                kind: LabelKind::Tag,
                text: d.title.clone(),
                color: color.clone(),
                world: tag,
                visible: want,
            });
            if let Some((g, c)) = bubble_of(d.tone) {
                out.push(SceneLabel {
                    r#ref: d.r#ref.clone(),
                    kind: LabelKind::Bubble,
                    text: g.to_string(),
                    color: c.to_string(),
                    world: f
                        .bubble
                        .map_or(f.root + Vec3::new(d.rx, 2.05, d.cz + 0.05), |b| b.0),
                    visible: f.bubble.is_some(),
                });
            }
        }
        // Each room's pill, over the campus: its project's name, at its room's front.
        for rm in &self.lay.rooms {
            let Some(base) = self
                .area_labels
                .get(&rm.project)
                .map(|l| l.name.clone())
                .or_else(|| self.names.get(&rm.project).cloned())
            else {
                continue;
            };
            if !self.hues.contains_key(&rm.project) {
                continue;
            }
            let (sx, sy, sz) = (rm.shown.dx as f32, rm.shown.dy as f32, rm.shown.dz as f32);
            let world = if rm.floors > 1 {
                let right = self
                    .lay
                    .rooms
                    .iter()
                    .filter(|o| o.project == rm.project)
                    .map(|o| (o.r#box.x1 + o.shown.dx) as f32)
                    .fold(f32::MIN, f32::max);
                Vec3::new(right + 2.6, 0.6 + sy, rm.r#box.z1 as f32 - 1.2 + sz)
            } else {
                Vec3::new(
                    (rm.r#box.x1 - 5f64.min((rm.r#box.x1 - rm.r#box.x0) / 3.)) as f32 + sx,
                    -0.4 + sy,
                    rm.r#box.z1 as f32 + 1.0 + sz,
                )
            };
            // Not red, which the failed use: a red project's pill is coral.
            let hue = self.hues[&rm.project] as f32;
            let ph = if !(18. ..=342.).contains(&hue) {
                18.
            } else {
                hue
            };
            let name = if rm.floors > 1 {
                format!("{} {}", base, rm.floor)
            } else {
                base
            };
            out.push(SceneLabel {
                r#ref: rm.key.clone(),
                kind: LabelKind::Area,
                text: name,
                color: textures::css_hsl(ph, 62., 50.),
                world,
                visible: campus,
            });
        }
        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{AreaModel, DeskModel, LeadModel};

    fn area(project: &str, n: usize) -> AreaModel {
        let tones = [
            DeskTone::Working,
            DeskTone::Needs,
            DeskTone::Finished,
            DeskTone::Error,
            DeskTone::Idle,
            DeskTone::Stopped,
        ];
        AreaModel {
            project: project.into(),
            label: project.into(),
            hue: 200.,
            lead: LeadModel {
                tone: LeadTone::Working,
                line: None,
            },
            desks: (0..n)
                .map(|i| DeskModel {
                    id: format!("{project}/agent-{}", i + 1),
                    project: project.into(),
                    name: format!("agent-{}", i + 1),
                    title: format!("Task {}", i + 1),
                    branch: String::new(),
                    ai: ["claude", "codex", "cursor"][i % 3].into(),
                    tone: tones[i % 6],
                    pr: None,
                    changes: None,
                    question: None,
                    last_active: None,
                })
                .collect(),
        }
    }

    fn input(sizes: &[usize]) -> SceneInput {
        let areas: Vec<AreaModel> = sizes
            .iter()
            .enumerate()
            .map(|(i, &n)| area(&format!("p{i}"), n))
            .collect();
        let mut screens = HashMap::new();
        for a in &areas {
            for d in &a.desks {
                screens.insert(
                    d.id.clone(),
                    Screen {
                        tone: d.tone,
                        head: "Working".into(),
                        title: d.title.clone(),
                        lines: vec![],
                    },
                );
            }
        }
        SceneInput {
            model: OfficeModel {
                areas,
                ..Default::default()
            },
            screens,
            you: "You".into(),
            ..Default::default()
        }
    }

    fn check(sizes: &[usize]) {
        let inp = input(sizes);
        let lay = layout::layout(&inp.model.areas);
        let mut kit = Kit::new();
        let mut built = build(&inp, &lay, &mut kit);
        let total: usize = sizes.iter().sum();
        let leads = lay.areas.iter().filter(|a| a.lead.is_some()).count();
        assert_eq!(built.desks.len(), total + leads);
        for d in &built.desks {
            assert!(
                built.hits.iter().any(|h| h.r#ref == d.r#ref && !h.bubble),
                "{} has a hit",
                d.r#ref
            );
        }
        assert_eq!(built.boards.len(), lay.rooms.len());
        for h in &built.hits {
            assert!(
                h.min.is_finite() && h.max.is_finite() && h.min.cmple(h.max).all(),
                "{}",
                h.r#ref
            );
        }
        for m in &kit.meshes {
            assert!(
                m.vertices
                    .iter()
                    .all(|v| v.pos.iter().chain(&v.normal).all(|x| x.is_finite()))
            );
            assert!(m.indices.iter().all(|&i| (i as usize) < m.vertices.len()));
        }
        // Draws for the campus, then a room.
        let mut sim = Sim::new();
        let walls = built.walls();
        sim.set_layout(lay.clone(), walls, None);
        for _ in 0..30 {
            sim.step(0.05);
        }
        let mut out = Vec::new();
        built.draws(&sim, 1., true, &mut kit, &mut out);
        assert!(!out.is_empty());
        assert!(
            out.iter()
                .all(|d| d.at.to_cols_array().iter().all(|x| x.is_finite())
                    && d.mesh.0 < kit.meshes.len())
        );
        let campus_n = out.len();
        let labels = built.labels(&sim);
        assert!(
            labels
                .iter()
                .any(|l| l.kind == LabelKind::Area && l.visible)
        );
        assert!(labels.iter().all(|l| l.world.is_finite()));
        let desk = built
            .desks
            .iter()
            .find(|d| !d.lead)
            .map(|d| d.r#ref.clone());
        if let Some(r) = desk {
            sim.walk_to(&r);
            for _ in 0..200 {
                sim.step(0.05);
            }
            out.clear();
            built.draws(&sim, 2., true, &mut kit, &mut out);
            assert!(!out.is_empty(), "{campus_n}");
            let labels = built.labels(&sim);
            assert!(
                !labels
                    .iter()
                    .any(|l| l.kind == LabelKind::Area && l.visible)
            );
        }
    }

    #[test]
    fn one_agent() {
        check(&[1]);
    }

    #[test]
    fn three_agents_in_two_projects() {
        check(&[2, 1]);
    }

    #[test]
    fn twelve_agents_in_three_projects() {
        check(&[5, 4, 3]);
    }

    #[test]
    fn a_big_project_gets_floors() {
        check(&[12]);
    }

    #[test]
    fn a_changed_screen_is_redrawn_in_place() {
        let mut inp = input(&[2]);
        let lay = layout::layout(&inp.model.areas);
        let mut kit = Kit::new();
        let mut built = build(&inp, &lay, &mut kit);
        kit.changed.clear();
        built.update(&inp, &mut kit);
        assert!(kit.changed.is_empty());
        inp.screens.get_mut("p0/agent-1").unwrap().title = "Something else".into();
        built.update(&inp, &mut kit);
        assert_eq!(kit.changed.len(), 1);
    }
}
