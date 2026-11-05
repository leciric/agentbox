//! The Office's room as a diorama (#668's decor.ts): a cut-away slab floating
//! on a pale sky, its thickness showing, two warm walls and no ceiling, a
//! terrazzo floor with a wooden lobby, and the things that make it a place: a
//! big window, a moss wall, a brick wall with a framed sign, a planter dividing
//! the lobby, a glass meeting room, a server rack. Everything is rounded and
//! toy-like, in a pastel and warm-wood palette. Static, so each piece is merged
//! into a few meshes (a `Baker`) and costs a handful of draws.

use crate::canvas::{Align, Svg};
use crate::glb::Image;
use crate::kit::{Kit, Piece};
use crate::layout::{Layout, Rect, js_round};
use crate::mesh::{Baker, Geo, Mat, at, cyl, floor_plane, hex, plane, rbox, sphere, torus, trs};
use crate::props::furnish;
use glam::Vec3;
use std::f32::consts::{FRAC_PI_2, TAU};

pub const WALL: &str = "#ecd1ad";
pub const WALL_TOP: &str = "#c98f58";
pub const WALL_EDGE: &str = "#e7c9a4";
pub const SLAB: &str = "#e2b27f";
pub const SLAB_DARK: &str = "#c39262";
pub const TERRAZZO: &str = "#f3e1cb";
pub const WOOD: &str = "#e2b27c";
pub const FRAME: &str = "#2f4a3a";
pub const BRICK: &str = "#b8664a";
pub const MOSS: [&str; 4] = ["#3f7d3c", "#5c9b46", "#78b35a", "#2f6a33"];
pub const GLASS: &str = "#d5ecf2";

/// A side wall the camera can be behind, which the scene fades.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Side {
    Left,
    Right,
}

/// A room's static parts, in its own frame (x right, z towards the camera,
/// the floor at y = 0), as decor.ts's group before the scene moves it.
#[derive(Default)]
pub struct Diorama {
    /// Everything static: slab, floors, walls, features, furniture.
    pub solid: Baker,
    /// Walls #668 fades when the camera is behind them. #668 never registers
    /// any (its `sideWalls` stays empty: the diorama's two walls are the back
    /// and left ones, which the overview never looks through), so this is
    /// empty too; the scene fades what's here if a later room has them.
    pub side_walls: Vec<(Side, Baker)>,
    /// Where the meeting room's door is, on the floor.
    pub meeting: Vec3,
}

/// wall_height is the walls' height: a quarter of the room's depth or so, low
/// enough that the overview looks over them into the room, as a diorama.
pub fn wall_height(l: &Layout) -> f32 {
    (l.room.z1 as f32 * 0.24).clamp(3.4, 4.6)
}

// ---- helpers shared with props.rs ----

/// m is a plain colour.
pub fn m(c: &str) -> Mat {
    Mat::hex(c)
}

/// textured is a material showing a texture (white, so it keeps its colours).
pub fn textured(t: usize, repeat: [f32; 2]) -> Mat {
    Mat::new([1., 1., 1.]).tex(t, repeat)
}

/// flip turns an image upside down: three's canvas textures have their top at
/// v = 1, the renderer samples v = 0 at the top.
fn flip(img: Image) -> Image {
    let row = img.width as usize * 4;
    let mut rgba = Vec::with_capacity(img.rgba.len());
    for r in (0..img.height as usize).rev() {
        rgba.extend_from_slice(&img.rgba[r * row..(r + 1) * row]);
    }
    Image { rgba, ..img }
}

/// canvas_tex draws a w×h canvas texture (shared by its name) with `draw`.
pub fn canvas_tex(k: &mut Kit, name: &str, w: u32, h: u32, draw: impl FnOnce(&mut Svg)) -> usize {
    k.texture(name, || {
        let mut s = Svg::new(w, h);
        draw(&mut s);
        flip(s.render())
    })
}

/// ellipse_a is an ellipse with an alpha (canvas's globalAlpha), rotated in radians.
pub fn ellipse_a(s: &mut Svg, cx: f32, cy: f32, rx: f32, ry: f32, rot: f32, fill: &str, alpha: f32) {
    s.raw(&format!(r#"<ellipse cx="{cx}" cy="{cy}" rx="{rx}" ry="{ry}" transform="rotate({} {cx} {cy})" fill="{fill}" fill-opacity="{alpha}"/>"#, rot.to_degrees()));
}

/// stroke is a stroked path (canvas's stroke()).
pub fn stroke(s: &mut Svg, d: &str, color: &str, width: f32) {
    s.raw(&format!(r#"<path d="{d}" fill="none" stroke="{color}" stroke-width="{width}" stroke-linejoin="round"/>"#));
}

/// Lcg is #668's `seed * 16807 % 2147483647` random, in [0, 1).
pub struct Lcg(u64);

impl Lcg {
    pub fn new(seed: u64) -> Self {
        Self(seed)
    }
    pub fn next(&mut self) -> f32 {
        self.0 = self.0 * 16807 % 2147483647;
        (self.0 as f64 / 2147483647.) as f32
    }
}

/// hsl_lin is three's `Color.setHSL(h, s, l)`: the numbers are taken as linear,
/// as three's working colour space is (mesh::hsl reads them as sRGB instead).
fn hsl_lin(h: f32, s: f32, l: f32) -> [f32; 3] {
    let h = h.rem_euclid(1.);
    let q = if l <= 0.5 { l * (1. + s) } else { l + s - l * s };
    let p = 2. * l - q;
    let f = |t: f32| {
        let t = t.rem_euclid(1.);
        if t < 1. / 6. {
            p + (q - p) * 6. * t
        } else if t < 0.5 {
            q
        } else if t < 2. / 3. {
            p + (q - p) * 6. * (2. / 3. - t)
        } else {
            p
        }
    };
    [f(h + 1. / 3.), f(h), f(h - 1. / 3.)]
}

fn put(b: &mut Baker, g: Geo, mat: &Mat, x: f32, y: f32, z: f32) {
    b.put(&g, mat, at(x, y, z));
}

fn putr(b: &mut Baker, g: Geo, mat: &Mat, x: f32, y: f32, z: f32, rot: [f32; 3]) {
    b.put(&g, mat, trs(x, y, z, rot[0], rot[1], rot[2], Vec3::ONE));
}

fn puts(b: &mut Baker, g: Geo, mat: &Mat, x: f32, y: f32, z: f32, s: [f32; 3]) {
    b.put(&g, mat, trs(x, y, z, 0., 0., 0., Vec3::from(s)));
}

struct R {
    x0: f32,
    z0: f32,
    x1: f32,
    z1: f32,
}

fn r32(r: &Rect) -> R {
    R { x0: r.x0 as f32, z0: r.z0 as f32, x1: r.x1 as f32, z1: r.z1 as f32 }
}

// ---- textures ----

/// terrazzo: cream with chips of warm colour, the floor of the work area.
fn terrazzo(k: &mut Kit, finish: usize) -> usize {
    let base = ["#f3e1cb", "#ebe5dc", "#e2ece2"][finish.min(2)];
    canvas_tex(k, &format!("terrazzo{finish}"), 512, 512, |s| {
        s.rect(0., 0., 512., 512., base);
        let mut rnd = Lcg::new(11);
        // Few chips, soft: a calm floor, not a speckled one.
        let chips = ["#e3a27a", "#d98c70", "#a9c79a", "#cdb9a2", "#eec58c", "#bfae9f"];
        for _ in 0..850 {
            let c = chips[(rnd.next() * chips.len() as f32) as usize];
            let alpha = 0.55 + rnd.next() * 0.35;
            let r = 1.2 + rnd.next() * 2.8;
            let cx = rnd.next() * 512.;
            let cy = rnd.next() * 512.;
            let ry = r * (0.6 + rnd.next() * 0.5);
            let rot = rnd.next() * 3.;
            ellipse_a(s, cx, cy, r, ry, rot, c, alpha);
        }
    })
}

/// planks: long, warm boards for the lobby. The boards run along the room,
/// towards the camera's corner: #668 turns the texture a quarter, here it's
/// drawn turned (its long way down), and the floor repeats it bw/4 by bd/4.
fn planks(k: &mut Kit) -> usize {
    canvas_tex(k, "planks", 512, 512, |s| {
        let rows = 10;
        let h = 512. / rows as f32;
        let mut rnd = Lcg::new(5);
        for r in 0..rows {
            let mut x = -rnd.next() * 240.;
            while x < 512. {
                let w = 220. + rnd.next() * 200.;
                let (hh, ss, ll) = (31. + rnd.next() * 4., 40. + rnd.next() * 6., 69. + rnd.next() * 5.);
                let y = r as f32 * h;
                // x and y swapped.
                s.rect(y, x, h, w, &format!("hsl({hh:.2},{ss:.2}%,{ll:.2}%)"));
                s.rect(y, x, 3., w, "rgba(255,255,255,0.08)");
                s.rect(y, x, h, 2., "rgba(120,60,20,0.16)");
                x += w;
            }
            s.rect(r as f32 * h, 0., 1.5, 512., "rgba(120,60,20,0.2)");
        }
    })
}

/// print_texture is one of the framed prints: soft shapes in the palette.
fn print_texture(k: &mut Kit, i: usize) -> usize {
    canvas_tex(k, &format!("print{}", i % 3), 168, 216, |s| {
        let bg = ["#f6e7d2", "#e3efe8", "#efe3f4"][i % 3];
        s.rect(0., 0., 168., 216., bg);
        let cols = [["#ff9150", "#ffc94d", "#45cbb8"], ["#8b7cf6", "#5aaef0", "#ff7aa2"], ["#86c95e", "#ffc94d", "#e077c6"]][i % 3];
        match i % 3 {
            0 => {
                s.circle(84., 92., 46., cols[0]);
                s.rect(0., 150., 168., 66., cols[2]);
                s.rect(0., 150., 168., 10., cols[1]);
            }
            1 => {
                for q in 0..3 {
                    let q = q as f32;
                    s.round_rect(24. + q * 14., 30. + q * 52., 120. - q * 28., 40., 20., cols[q as usize]);
                }
            }
            _ => {
                s.path("M20,190 L84,60 L148,190 Z", cols[0]);
                s.circle(126., 52., 18., cols[1]);
            }
        }
    })
}

/// bricks: a feature wall's worth.
fn bricks(k: &mut Kit) -> usize {
    canvas_tex(k, "bricks", 256, 256, |s| {
        s.rect(0., 0., 256., 256., "#e8d6c4");
        let bh = 256. / 10.;
        let mut rnd = Lcg::new(3);
        for r in 0..10 {
            for c in -1..5 {
                let x = c as f32 * 64. + if r % 2 == 1 { 32. } else { 0. };
                let (h, sat, l) = (12. + rnd.next() * 8., 45. + rnd.next() * 12., 44. + rnd.next() * 9.);
                s.round_rect(x + 2., r as f32 * bh + 2., 60., bh - 4., 3., &format!("hsl({h:.2},{sat:.2}%,{l:.2}%)"));
            }
        }
    })
}

/// sky is the window's view: a pale gradient and a skyline.
fn sky(k: &mut Kit) -> usize {
    canvas_tex(k, "sky", 256, 256, |s| {
        let g = s.linear(0., 0., 0., 256., &[(0., "#bfe2f5"), (1., "#f4e6c8")]);
        s.rect(0., 0., 256., 256., &g);
        let mut x = 0;
        while x < 256 {
            let hgt = 60 + (x * 7) % 60;
            s.rect(x as f32, (200 - (x * 7) % 60) as f32, 18., hgt as f32, "#d9c2a0");
            x += 22;
        }
    })
}

fn sign_tex(k: &mut Kit, sign: &str) -> usize {
    canvas_tex(k, &format!("sign:{sign}"), 512, 256, |s| {
        s.rect(0., 0., 512., 256., "#1f2a3a");
        s.text(256., 140., 68., 700, "#f6efe3", Align::Center, sign);
        s.rect(196., 172., 120., 8., "#e8a273");
    })
}

fn screen_tex(k: &mut Kit) -> usize {
    canvas_tex(k, "meeting-screen", 256, 144, |c| {
        c.rect(0., 0., 256., 144., "#1f2433");
        let bars = [40., 62., 50., 84., 72., 104.];
        for (i, h) in bars.iter().enumerate() {
            c.rect(28. + i as f32 * 36., 122. - h, 22., *h, if i == bars.len() - 1 { "#a78bfa" } else { "#5aaef0" });
        }
        c.text(24., 24., 16., 700, "#e6e8ee", Align::Left, "Merged this week");
    })
}

/// diorama builds the room for a layout: slab, floors, walls and what's on
/// them, the lobby's planter and rack, the meeting room, and its furniture.
pub fn diorama(l: &Layout, sign: &str, hue: Option<f64>, kit: &mut Kit) -> Diorama {
    let mut b = Baker::default();
    let b = &mut b;
    let w = l.room.x1 as f32;
    let d = l.room.z1 as f32;
    let t = 0.62; // the slab's thickness
    let wall_h = wall_height(l);
    let wt = 0.22; // walls' thickness
    let finish = l.finish.clamp(0, 2) as usize;

    // The slab, lit on its edges, darker below.
    put(b, rbox(w + 0.5, t, d + 0.5, 0.08), &m(SLAB), w / 2., -t / 2. - 0.001, d / 2.);
    put(b, rbox(w + 0.3, 0.06, d + 0.3, 0.03), &m(SLAB_DARK), w / 2., -t - 0.02, d / 2.);

    // The floors: terrazzo throughout, planks under each of the commons.
    // Each room's finish: warm, stone or mint terrazzo.
    let tz = terrazzo(kit, finish);
    put(b, floor_plane(w, d), &textured(tz, [w / 3., d / 3.]), w / 2., 0.001, d / 2.);
    let pk = planks(kit);
    for (i, bx) in l.woods.iter().enumerate() {
        let bx = r32(bx);
        let (bw, bd) = (bx.x1 - bx.x0, bx.z1 - bx.z0);
        if bw <= 0. || bd <= 0. {
            continue;
        }
        put(b, floor_plane(bw, bd), &textured(pk, [bw / 4., bd / 4.]), bx.x0 + bw / 2., 0.002 + i as f32 * 0.0004, bx.z0 + bd / 2.);
    }

    // The two walls, back and left, with a lighter cap and a skirting.
    // Its walls a little of its project's colour, so rooms read apart.
    let wall_c = {
        let base = hex(WALL);
        match hue {
            None => base,
            Some(h) => {
                let tint = hsl_lin(h as f32 / 360., 0.5, 0.82);
                [0, 1, 2].map(|i| base[i] + (tint[i] - base[i]) * 0.3)
            }
        }
    };
    let wall = Mat::new(wall_c);
    let cap = m(WALL_TOP);
    let skirt = m(WALL_EDGE);
    put(b, rbox(w + wt, wall_h, wt, 0.03), &wall, w / 2. - wt / 2., wall_h / 2., -wt / 2.);
    put(b, rbox(wt, wall_h, d + wt, 0.03), &wall, -wt / 2., wall_h / 2., d / 2. - wt / 2.);
    put(b, rbox(w + wt + 0.04, 0.06, wt + 0.06, 0.02), &cap, w / 2. - wt / 2., wall_h + 0.02, -wt / 2.);
    put(b, rbox(wt + 0.06, 0.06, d + wt + 0.04, 0.02), &cap, -wt / 2., wall_h + 0.02, d / 2. - wt / 2.);
    put(b, rbox(w, 0.12, 0.04, 0.01), &skirt, w / 2., 0.06, 0.02);
    put(b, rbox(0.04, 0.12, d, 0.01), &skirt, 0.02, 0.06, d / 2.);

    let frame = m(FRAME);
    let dark = m("#3b3f46");

    // The meeting room: in the back left corner, glass on its two sides into
    // the room; or at the front left, glass at its back and side and a low
    // glass rail along the slab's open edge. A round table and chairs.
    let mr = r32(&l.meeting);
    let front = mr.z0 > 0.5;
    let glass = m("#bfe1ee").opacity(0.42);
    let gw = mr.x1 - mr.x0;
    let gd = mr.z1 - mr.z0;
    // The glass across it, its z: the front of a back room, the back of a front one.
    let gz = if front { mr.z0 } else { mr.z1 };
    b.put(&plane(gw, 2.2), &glass, at(mr.x0 + gw / 2., 1.1, gz));
    b.put(&plane(gd, 2.2), &glass, trs(mr.x1, 1.1, mr.z0 + gd / 2., 0., FRAC_PI_2, 0., Vec3::ONE));
    if front {
        b.put(&plane(gw, 0.9), &glass, at(mr.x0 + gw / 2., 0.45, mr.z1 - 0.02));
        put(b, rbox(gw, 0.05, 0.05, 0.02), &frame, mr.x0 + gw / 2., 0.9, mr.z1 - 0.02);
        put(b, rbox(0.06, 0.9, 0.06, 0.02), &frame, mr.x1, 0.45, mr.z1 - 0.02);
    }
    // Its frame: posts and rails, dark green.
    for x in [mr.x0 + 0.05, mr.x0 + gw / 2., mr.x1 - 0.6] {
        put(b, rbox(0.06, 2.2, 0.06, 0.02), &frame, x, 1.1, gz);
    }
    put(b, rbox(0.06, 2.2, 0.06, 0.02), &frame, mr.x1, 1.1, gz);
    put(b, rbox(0.06, 2.2, 0.06, 0.02), &frame, mr.x1, 1.1, mr.z0 + gd / 2.);
    put(b, rbox(gw, 0.06, 0.06, 0.02), &frame, mr.x0 + gw / 2., 2.2, gz);
    put(b, rbox(0.06, 0.06, gd, 0.02), &frame, mr.x1, 2.2, mr.z0 + gd / 2.);
    put(b, rbox(gw, 0.05, 0.05, 0.02), &frame, mr.x0 + gw / 2., 0.03, gz);
    put(b, rbox(0.05, 0.05, gd, 0.02), &frame, mr.x1, 0.03, mr.z0 + gd / 2.);
    let tcx = mr.x0 + gw * 0.55;
    let tcz = mr.z0 + gd * 0.55;
    put(b, cyl(0.55, 0.55, 0.06, 32), &m("#e1a86d"), tcx, 0.74, tcz);
    put(b, cyl(0.05, 0.08, 0.72, 12), &dark, tcx, 0.36, tcz);
    let seat_cols = ["#e9b44c", "#e07a5f", "#4aa3a2", "#e9b44c"];
    for (i, sc) in seat_cols.iter().enumerate() {
        let a = (i as f32 / 4.) * TAU + 0.4;
        let sx = tcx + a.sin() * 0.85;
        let sz = tcz + a.cos() * 0.85;
        let c = m(sc);
        put(b, rbox(0.38, 0.08, 0.38, 0.04), &c, sx, 0.46, sz);
        putr(b, rbox(0.36, 0.32, 0.06, 0.03), &c, sx + a.sin() * 0.17, 0.66, sz + a.cos() * 0.17, [0., a, 0.]);
        put(b, cyl(0.02, 0.02, 0.44, 8), &dark, sx, 0.22, sz);
    }
    // A sofa against its wall, sage green: the back wall, or the left one.
    let sage = m("#9cb89a");
    if front {
        put(b, rbox(0.7, 0.36, 1.6, 0.12), &sage, mr.x0 + 0.5, 0.22, mr.z0 + gd * 0.5);
        put(b, rbox(0.2, 0.5, 1.6, 0.08), &sage, mr.x0 + 0.18, 0.6, mr.z0 + gd * 0.5);
    } else {
        put(b, rbox(1.6, 0.36, 0.7, 0.12), &sage, mr.x0 + gw * 0.45, 0.22, mr.z0 + 0.5);
        put(b, rbox(1.6, 0.5, 0.2, 0.08), &sage, mr.x0 + gw * 0.45, 0.6, mr.z0 + 0.18);
    }

    // The left wall: a big gridded window and a moss wall.
    let win = (l.window.at as f32, l.window.w as f32);
    let sky_t = sky(kit);
    let sky_m = textured(sky_t, [1., 1.]).glow();
    b.put(&plane(win.1, 1.7), &sky_m, trs(0.012, 1.55, win.0, 0., FRAC_PI_2, 0., Vec3::ONE));
    let cols = 3.max(js_round(win.1 as f64 / 0.55) as i32);
    for i in 0..=cols {
        put(b, rbox(0.05, 1.8, 0.05, 0.015), &frame, 0.03, 1.55, win.0 - win.1 / 2. + (i as f32 * win.1) / cols as f32);
    }
    for y in [0.7, 1.55, 2.4] {
        put(b, rbox(0.05, 0.05, win.1 + 0.05, 0.015), &frame, 0.03, y, win.0);
    }
    put(b, rbox(0.16, 0.05, win.1 + 0.1, 0.02), &m("#f7e2c4"), 0.08, 0.66, win.0);
    // The left wall's feature, each room's own: a moss wall, a wall of books,
    // or a second window.
    let moss = (l.moss.at as f32, l.moss.w as f32);
    let v = l.finish;
    let moss_mats: Vec<Mat> = MOSS.iter().map(|c| m(c)).collect();
    let mut rnd = Lcg::new(17);
    if v == 1 {
        // Shelves of books, floor to above head height.
        put(b, rbox(0.34, 2.4, moss.1, 0.03), &m("#c08b5c"), 0.17, 1.2, moss.0);
        let spines: Vec<Mat> = ["#c4622d", "#2f5a4c", "#e9b44c", "#7c8bd6", "#f4efe6", "#a8502f", "#5aaef0"].iter().map(|c| m(c)).collect();
        for row in 0..5usize {
            put(b, rbox(0.32, 0.03, moss.1 - 0.06, 0.01), &m("#a87648"), 0.2, 0.1 + row as f32 * 0.46, moss.0);
            let n = ((moss.1 - 0.2) / 0.1).floor().max(0.) as usize;
            for i in 0..n {
                if (i * 7 + row * 3) % 9 == 4 {
                    continue;
                }
                let hgt = 0.26 + ((i * 5 + row) % 4) as f32 * 0.035;
                put(b, rbox(0.22, hgt, 0.08, 0.01), &spines[(i + row * 2) % spines.len()], 0.22, 0.12 + row as f32 * 0.46 + hgt / 2., moss.0 - moss.1 / 2. + 0.12 + i as f32 * 0.1);
            }
        }
    } else if v == 2 {
        // A second window, as tall as the first.
        b.put(&plane(moss.1, 1.7), &sky_m, trs(0.012, 1.55, moss.0, 0., FRAC_PI_2, 0., Vec3::ONE));
        let cols2 = 3.max(js_round(moss.1 as f64 / 0.55) as i32);
        for i in 0..=cols2 {
            put(b, rbox(0.05, 1.8, 0.05, 0.015), &frame, 0.03, 1.55, moss.0 - moss.1 / 2. + (i as f32 * moss.1) / cols2 as f32);
        }
        for y in [0.7, 1.55, 2.4] {
            put(b, rbox(0.05, 0.05, moss.1 + 0.05, 0.015), &frame, 0.03, y, moss.0);
        }
    }
    if v == 0 {
        put(b, rbox(0.08, 1.9, moss.1 + 0.12, 0.03), &frame, 0.04, 1.45, moss.0);
        let tuft = sphere(1., 10, 8);
        for i in 0..(moss.1 * 55.).round() as usize {
            let r = 0.05 + rnd.next() * 0.06;
            let y = 0.6 + rnd.next() * 1.7;
            let z = moss.0 - moss.1 / 2. + rnd.next() * moss.1;
            b.put(&tuft, &moss_mats[i % moss_mats.len()], trs(0.1, y, z, 0., 0., 0., Vec3::new(r * 0.6, r, r)));
        }
    }

    // The back wall: a brick feature wall with a framed sign.
    let br = (l.brick.at as f32, l.brick.w as f32);
    // Brick, or warm wooden slats, or a wall painted in the project's colour.
    if v == 1 {
        let slat = m("#c98f58");
        let slat2 = m("#d9a46c");
        let n = (br.1 / 0.16).floor().max(0.) as usize;
        for i in 0..n {
            put(b, rbox(0.11, wall_h - 0.1, 0.06, 0.02), if i % 2 == 1 { &slat } else { &slat2 }, br.0 - br.1 / 2. + 0.08 + i as f32 * 0.16, (wall_h - 0.1) / 2., 0.035);
        }
    } else if let (2, Some(h)) = (v, hue) {
        put(b, rbox(br.1, wall_h - 0.06, 0.05, 0.01), &Mat::new(hsl_lin(h as f32 / 360., 0.45, 0.62)), br.0, (wall_h - 0.06) / 2., 0.025);
    } else {
        let bt = bricks(kit);
        put(b, rbox(br.1, wall_h - 0.06, 0.05, 0.01), &textured(bt, [br.1 / 1.2, 2.4]), br.0, (wall_h - 0.06) / 2., 0.025);
    }
    let sg = sign_tex(kit, sign);
    put(b, rbox(1.5, 0.82, 0.06, 0.02), &m("#6b4128"), br.0, 1.95, 0.08);
    b.put(&plane(1.36, 0.68), &textured(sg, [1., 1.]).glow(), at(br.0, 1.95, 0.115));

    // The planter dividing the lobby: a long wooden box of round shrubs.
    let pl = r32(&l.planter);
    put(b, rbox(pl.x1 - pl.x0, 0.38, pl.z1 - pl.z0, 0.05), &m("#b57846"), (pl.x0 + pl.x1) / 2., 0.19, (pl.z0 + pl.z1) / 2.);
    let shrub = sphere(1., 14, 10);
    let greens = [m("#4f9a52"), m("#6db35a")];
    let along_z = pl.z1 - pl.z0 > pl.x1 - pl.x0;
    let len = if along_z { pl.z1 - pl.z0 } else { pl.x1 - pl.x0 };
    let n = 3.max((len / 0.36).floor() as i32);
    for i in 0..n {
        let r = 0.2 + ((i * 7) % 5) as f32 * 0.012;
        let a = 0.2 + (i as f32 * (len - 0.4)) / 1.max(n - 1) as f32;
        let (x, z) = if along_z { ((pl.x0 + pl.x1) / 2., pl.z0 + a) } else { (pl.x0 + a, (pl.z0 + pl.z1) / 2.) };
        puts(b, shrub.clone(), &greens[i as usize % 2], x, 0.5, z, [r, r * 0.85, r]);
    }

    // A server rack by the bookshelf, its lights to the room.
    let (rkx, rkz) = (l.rack.x as f32, l.rack.z as f32);
    put(b, rbox(0.6, 1.5, 0.6, 0.05), &m("#1e242c"), rkx, 0.75, rkz);
    let led = m("#45e08a").glow();
    let led_blue = m("#5ab8ff").glow();
    for i in 0..6 {
        putr(b, rbox(0.32, 0.03, 0.02, 0.01), if i % 3 == 2 { &led_blue } else { &led }, rkx + 0.31, 0.35 + i as f32 * 0.18, rkz, [0., FRAC_PI_2, 0.]);
    }

    // The lounge: a sectional sofa along the brick wall, facing the room, its
    // chaise on the left, cushions and a throw; a low table with mugs and a
    // book before it, a floor lamp at its end, all on a soft rug.
    let lg = r32(&l.lounge);
    let sf = r32(&l.sofa);
    let lcx = (lg.x0 + lg.x1) / 2.;
    put(b, rbox(lg.x1 - lg.x0, 0.02, lg.z1 - lg.z0, 0.01), &m("#d7e4cf"), lcx, 0.012, (lg.z0 + lg.z1) / 2.);
    let fabric = m("#9cb89a");
    let cushion = m("#b5cdb2");
    let sw = sf.x1 - sf.x0;
    let scx = (sf.x0 + sf.x1) / 2.;
    put(b, rbox(sw, 0.36, 0.95, 0.12), &fabric, scx, 0.22, sf.z0 + 0.48);
    put(b, rbox(sw, 0.62, 0.24, 0.1), &fabric, scx, 0.52, sf.z0 + 0.12);
    put(b, rbox(0.95, 0.36, 1.25, 0.12), &fabric, sf.x0 + 0.475, 0.22, sf.z1 + 0.6);
    put(b, rbox(0.22, 0.52, 2.2, 0.09), &fabric, sf.x0 + 0.11, 0.42, sf.z0 + 1.1);
    put(b, rbox(0.22, 0.52, 0.95, 0.09), &fabric, sf.x1 - 0.11, 0.42, sf.z0 + 0.48);
    let seats = 2.max(js_round(((sw - 0.5) / 1.0) as f64) as i32);
    for i in 0..seats {
        let x = sf.x0 + 0.3 + ((i as f32 + 0.5) * (sw - 0.55)) / seats as f32;
        put(b, rbox((sw - 0.6) / seats as f32 - 0.05, 0.14, 0.72, 0.07), &cushion, x, 0.46, sf.z0 + 0.55);
        putr(b, rbox((sw - 0.6) / seats as f32 - 0.08, 0.42, 0.16, 0.08), &cushion, x, 0.75, sf.z0 + 0.3, [-0.12, 0., 0.]);
    }
    put(b, rbox(0.68, 0.14, 1.0, 0.07), &cushion, sf.x0 + 0.5, 0.46, sf.z1 + 0.62);
    for (i, c) in ["#ffc94d", "#ff7aa2", "#5aaef0"].iter().enumerate() {
        let fi = i as f32;
        putr(b, rbox(0.34, 0.32, 0.12, 0.1), &m(c), sf.x0 + 0.7 + fi * (sw - 1.4) / 2., 0.72, sf.z0 + 0.42, [-0.2, (fi - 1.) * 0.3, (fi - 1.) * 0.12]);
    }
    putr(b, rbox(0.75, 0.03, 0.6, 0.02), &m("#e9b44c"), sf.x0 + 0.5, 0.545, sf.z1 + 0.9, [0., 0.2, 0.]);
    // The low table.
    let ltx = scx + 0.4;
    let ltz = lg.z0 + 2.35;
    put(b, rbox(1.5, 0.06, 0.75, 0.2), &m("#e1a86d"), ltx, 0.4, ltz);
    for (dx, dz) in [(-0.6, -0.25), (0.6, -0.25), (-0.6, 0.25), (0.6, 0.25)] {
        put(b, cyl(0.025, 0.02, 0.38, 8), &dark, ltx + dx, 0.19, ltz + dz);
    }
    put(b, cyl(0.04, 0.035, 0.09, 14), &m("#fbfaf7"), ltx + 0.35, 0.475, ltz + 0.1);
    put(b, cyl(0.04, 0.035, 0.09, 14), &m("#ff7aa2"), ltx + 0.15, 0.475, ltz - 0.15);
    putr(b, rbox(0.32, 0.04, 0.24, 0.01), &m("#2f5a4c"), ltx - 0.35, 0.45, ltz, [0., 0.3, 0.]);
    // A floor lamp at its right end, warm: Kenney's.
    if let Some(lamp) = kit.piece(Piece::LampRoundFloor) {
        b.put_model(&lamp.model, &[], trs(sf.x1 + 0.3, 0., sf.z1 + 0.9, 0., -FRAC_PI_2, 0., Vec3::ONE));
    }

    // A tall bookshelf against the left wall, at the front.
    let sh = r32(&l.shelf);
    let sh_z = (sh.z0 + sh.z1) / 2.;
    let sh_d = sh.z1 - sh.z0;
    put(b, rbox(0.38, 1.9, sh_d, 0.03), &m("#c08b5c"), 0.19, 0.95, sh_z);
    let books: Vec<Mat> = ["#c4622d", "#2f5a4c", "#e9b44c", "#7c8bd6", "#f4efe6", "#a8502f"].iter().map(|c| m(c)).collect();
    for row in 0..4usize {
        put(b, rbox(0.36, 0.03, sh_d - 0.08, 0.01), &m("#a87648"), 0.22, 0.2 + row as f32 * 0.45, sh_z);
        for i in 0..10usize {
            if (i + row * 3) % 7 == 3 {
                continue;
            }
            let h = 0.24 + ((i * 7 + row) % 4) as f32 * 0.03;
            put(b, rbox(0.24, h, 0.09, 0.01), &books[(i + row * 2) % books.len()], 0.26, 0.22 + row as f32 * 0.45 + h / 2., sh.z0 + 0.12 + i as f32 * ((sh_d - 0.24) / 9.));
        }
    }

    // On the work's back wall, either side of the board: framed prints.
    let prints = [print_texture(kit, 0), print_texture(kit, 1), print_texture(kit, 2)];
    let frame_m = m("#f4efe6");
    for (pi, x) in l.art.iter().enumerate() {
        let x = *x as f32;
        put(b, rbox(1.0, 1.25, 0.05, 0.02), &frame_m, x, 2.45, 0.03);
        b.put(&plane(0.84, 1.08), &textured(prints[pi % prints.len()], [1., 1.]), at(x, 2.45, 0.06));
    }
    // A clock over the meeting room, or where it'd be.
    {
        let cx = if front { 1.6 } else { (mr.x0 + mr.x1) / 2. };
        putr(b, cyl(0.32, 0.32, 0.06, 32), &m("#ffffff"), cx, 2.95, 0.04, [FRAC_PI_2, 0., 0.]);
        put(b, torus(0.32, 0.03, 8, 32, TAU), &m("#2f4a3a"), cx, 2.95, 0.06);
        put(b, rbox(0.03, 0.2, 0.01, 0.005), &m("#1f2a3a"), cx, 3.03, 0.08);
        put(b, rbox(0.15, 0.03, 0.01, 0.005), &m("#1f2a3a"), cx + 0.07, 2.95, 0.08);
    }
    // The meeting room's screen, a chart on it.
    {
        let tv = screen_tex(kit);
        let scr = textured(tv, [1., 1.]).glow();
        if front {
            // On the left wall, over the sofa, facing the table.
            let tv_z = mr.z0 + gd * 0.5;
            put(b, rbox(0.05, 0.88, 1.5, 0.02), &m("#15181f"), 0.04, 1.75, tv_z);
            b.put(&plane(1.42, 0.8), &scr, trs(0.07, 1.75, tv_z, 0., FRAC_PI_2, 0., Vec3::ONE));
        } else {
            let tv_x = mr.x0 + gw * 0.5;
            put(b, rbox(1.5, 0.88, 0.05, 0.02), &m("#15181f"), tv_x, 1.75, 0.04);
            b.put(&plane(1.42, 0.8), &scr, at(tv_x, 1.75, 0.07));
        }
    }

    // Everything else the room is furnished with.
    furnish(b, &l.props, kit);

    Diorama { solid: std::mem::take(b), side_walls: Vec::new(), meeting: Vec3::new(mr.x0 + gw / 2., 0., mr.z1) }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::layout::{AreaIn, room_layout};

    fn area(n: usize) -> AreaIn {
        AreaIn { project: format!("p{n}"), desks: (0..n).map(|i| format!("d{i}")).collect(), no_lead: false, floor: None }
    }

    #[test]
    fn a_diorama_is_finite_geometry() {
        let mut kit = Kit::new();
        for n in [1, 4, 9] {
            let l = room_layout(&[area(n)]);
            for hue in [None, Some(200.)] {
                let d = diorama(&l, "Sign", hue, &mut kit);
                assert!(!d.solid.is_empty(), "{n} desks: empty");
                let mut tris = 0;
                for (_, _, mesh) in &d.solid.batches {
                    tris += mesh.indices.len() / 3;
                    assert!(mesh.indices.iter().all(|&i| (i as usize) < mesh.vertices.len()));
                    for v in &mesh.vertices {
                        assert!(v.pos.iter().chain(&v.normal).chain(&v.uv).chain(&v.color).all(|x| x.is_finite()), "{n} desks: non-finite vertex");
                    }
                }
                assert!(tris > 1000, "{n} desks: {tris} triangles");
                assert!(d.meeting.is_finite());
            }
        }
    }

    #[test]
    fn walls_are_low_enough_to_look_over() {
        let l = room_layout(&[area(4)]);
        let h = wall_height(&l);
        assert!((3.4..=4.6).contains(&h));
    }
}
