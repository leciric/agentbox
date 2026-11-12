//! The Office's people, as rounded toys: soft plastic shading, chunky shapes,
//! big eyes, squash and stretch (#668's figures.ts, ported).
//!
//! Every agent is Boxie, the AgentBox character, chibi: its head as big as
//! the rest of it, the logo's cube rounded off in its violet-to-indigo
//! gradient with white piping, a face screen with big eyes that change with
//! its mood, and a tiny body with stubby limbs. One rig and one set of poses:
//! typing, waving, celebrating, an error, asleep. Variety is in the details:
//! the agent's colour (its body), something to wear (a hat, headphones, a
//! bow…), and a badge on its chest for its AI tool, all from a stable hash.
//!
//! You are a chibi person: a big head, a stubby body, big eyes, a hoodie
//! ([`walker`], until Kenney's character, player.rs, has loaded).
//!
//! A figure is a [`Rig`]: three's group tree flattened. Its static parts are
//! baked once into meshes in each node's frame (figures.ts's `bake`: one mesh
//! per material, here per texture, by `Baker`); posing a figure only moves
//! nodes (position, rotation, scale, visibility), so the scene uploads a
//! rig's meshes once and draws each visible node's at `rig.world(root)`.
//!
//! Textures use the image's own convention (row 0 at the top is v = 0), as
//! glTF's do, not three's flipped canvas.

use crate::canvas::Svg;
use crate::gpu::MeshData;
use crate::kit::Kit;
use crate::mesh::{self, Baker, Geo, Mat, at, trs};
use crate::model::DeskTone;
use crate::rig::{NodeId, Rig};
use glam::{EulerRot, Mat4, Quat, Vec2, Vec3};
use std::f32::consts::{FRAC_PI_2, PI, TAU};

/// What a figure is, as the scene drives it.
pub trait Figure {
    fn rig(&self) -> &Rig;
    /// pose poses the figure for its state at time t (seconds). look is
    /// where you stand, in the figure's own frame (x, z), when you're near
    /// enough to be looked at.
    fn pose(&mut self, tone: DeskTone, t: f32, motion: bool, look: Option<(f32, f32)>);
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Tool {
    Claude,
    Codex,
    OpenCode,
    Cursor,
    Gemini,
    Copilot,
}

impl Tool {
    pub const ALL: [Tool; 6] = [
        Tool::Claude,
        Tool::Codex,
        Tool::OpenCode,
        Tool::Cursor,
        Tool::Gemini,
        Tool::Copilot,
    ];
    pub fn name(self) -> &'static str {
        match self {
            Tool::Claude => "claude",
            Tool::Codex => "codex",
            Tool::OpenCode => "opencode",
            Tool::Cursor => "cursor",
            Tool::Gemini => "gemini",
            Tool::Copilot => "copilot",
        }
    }
}

/// tool_of is an agent's AI tool from its name (Claude Code when unknown).
pub fn tool_of(ai: Option<&str>) -> Tool {
    match ai {
        Some("codex") => Tool::Codex,
        Some("opencode") => Tool::OpenCode,
        Some("cursor") => Tool::Cursor,
        Some("gemini") => Tool::Gemini,
        Some("copilot") => Tool::Copilot,
        _ => Tool::Claude,
    }
}

/// hash is a stable number for a string: an agent is the same figure every
/// time. (JS's: each code point's first UTF-16 unit.)
pub fn hash(s: &str) -> u32 {
    let mut h: u32 = 2166136261;
    for ch in s.chars() {
        let c = ch as u32;
        let unit = if c > 0xFFFF {
            0xD800 + ((c - 0x10000) >> 10)
        } else {
            c
        };
        h = (h ^ unit).wrapping_mul(16777619);
    }
    h
}

/// Xorshift32 over a seed, as figures.ts's rng: numbers in [0, 1) in steps
/// of 1/100000 (f64, so floor(c * 8) picks what JS picks).
pub struct Rng(u32);

impl Rng {
    pub fn new(seed: u32) -> Self {
        Self(if seed == 0 { 1 } else { seed })
    }
    #[allow(clippy::should_implement_trait)]
    pub fn next(&mut self) -> f64 {
        let mut x = self.0;
        x ^= x << 13;
        x ^= x >> 17;
        x ^= x << 5;
        self.0 = x;
        (x % 100000) as f64 / 100000.
    }
}

/// The accents, bright against every mascot's own colours.
/// Soft, toy-like, and apart from each other at a glance: each one a Boxie's
/// head and body, so they read apart from across the room.
pub const ACCENTS: [&str; 8] = [
    "#ff7aa2", "#45cbb8", "#ffc94d", "#8b7cf6", "#5aaef0", "#ff9150", "#86c95e", "#e077c6",
];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Accessory {
    Beanie,
    Headphones,
    Bow,
    Cap,
    Scarf,
    Crown,
    Sprout,
    None,
    Headset,
}

impl Accessory {
    /// The ones an agent can draw from its hash, in figures.ts's order.
    pub const ALL: [Accessory; 8] = [
        Accessory::Beanie,
        Accessory::Headphones,
        Accessory::Bow,
        Accessory::Cap,
        Accessory::Scarf,
        Accessory::Crown,
        Accessory::Sprout,
        Accessory::None,
    ];
}

/// The logo's gradient, its hue turned by these many degrees, for an agent's
/// head: from cyan to rose about the logo's violet, so every head is still
/// the logo's cube and yet they read apart across the room. (Leads keep the
/// logo's own.)
pub const HEAD_SHIFTS: [f32; 8] = [-78., 20., 84., -40., 42., -60., 64., -20.];

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Accent {
    pub color: &'static str,
    pub accessory: Accessory,
    pub tint: f64,
    pub shift: f32,
}

/// accent_of is an agent's accent, from its ref: a colour and a thing to wear.
/// variant, when given, is the agent's place in the room: then no two agents
/// wear the same thing in the same colour until sixty-four of them do.
pub fn accent_of(r#ref: &str, variant: Option<u32>) -> Accent {
    let mut r = Rng::new(hash(r#ref));
    let a = r.next();
    let c = r.next();
    let tint = r.next() * 2. - 1.;
    let h = r.next();
    match variant {
        None => Accent {
            color: ACCENTS[(c * ACCENTS.len() as f64).floor() as usize],
            accessory: Accessory::ALL[(a * Accessory::ALL.len() as f64).floor() as usize],
            tint,
            shift: HEAD_SHIFTS[(h * HEAD_SHIFTS.len() as f64).floor() as usize],
        },
        // Sixty-four pairings of a thing and a colour before any repeats.
        Some(v) => {
            let v = v as usize;
            Accent {
                color: ACCENTS[(v / Accessory::ALL.len() + v * 3) % ACCENTS.len()],
                accessory: Accessory::ALL[v % Accessory::ALL.len()],
                tint,
                shift: HEAD_SHIFTS[v % HEAD_SHIFTS.len()],
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Colours. three's Color holds linear components, and getHSL/setHSL work on
// them as they are, so the ramps below do too.

fn to_hsl(c: [f32; 3]) -> (f32, f32, f32) {
    let [r, g, b] = c;
    let max = r.max(g).max(b);
    let min = r.min(g).min(b);
    let l = (min + max) / 2.;
    if min == max {
        return (0., 0., l);
    }
    let d = max - min;
    let s = if l <= 0.5 {
        d / (max + min)
    } else {
        d / (2. - max - min)
    };
    let h = if max == r {
        (g - b) / d + if g < b { 6. } else { 0. }
    } else if max == g {
        (b - r) / d + 2.
    } else {
        (r - g) / d + 4.
    };
    (h / 6., s, l)
}

fn from_hsl(h: f32, s: f32, l: f32) -> [f32; 3] {
    let h = h.rem_euclid(1.);
    let (s, l) = (s.clamp(0., 1.), l.clamp(0., 1.));
    if s == 0. {
        return [l, l, l];
    }
    let p = if l <= 0.5 {
        l * (1. + s)
    } else {
        l + s - l * s
    };
    let q = 2. * l - p;
    let f = |t: f32| {
        let t = t.rem_euclid(1.);
        if t < 1. / 6. {
            q + (p - q) * 6. * t
        } else if t < 0.5 {
            p
        } else if t < 2. / 3. {
            q + (p - q) * 6. * (2. / 3. - t)
        } else {
            q
        }
    };
    [f(h + 1. / 3.), f(h), f(h - 1. / 3.)]
}

fn lerp3(a: [f32; 3], b: [f32; 3], k: f32) -> [f32; 3] {
    [
        a[0] + (b[0] - a[0]) * k,
        a[1] + (b[1] - a[1]) * k,
        a[2] + (b[2] - a[2]) * k,
    ]
}

/// Three colours (linear), light to dark.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Ramp {
    pub light: [f32; 3],
    pub mid: [f32; 3],
    pub dark: [f32; 3],
}

/// The logo's colours: violet to indigo, white lines. The leads wear them.
pub fn logo() -> Ramp {
    Ramp {
        light: mesh::hex("#a78bfa"),
        mid: mesh::hex("#7c3aed"),
        dark: mesh::hex("#4f46e5"),
    }
}

/// ramp_of is the logo's gradient in an agent's accent: lighter at the top
/// left, turning a little towards blue and deeper at the bottom right, as
/// the logo's violet turns indigo.
pub fn ramp_of(accent: &str) -> Ramp {
    let mid = mesh::hex(accent);
    let (h, s, l) = to_hsl(mid);
    Ramp {
        light: from_hsl(h + 0.01, (s * 0.95).min(1.), (l + 0.14).min(0.86)),
        mid,
        dark: from_hsl(h + 0.05, (s * 0.9).min(1.), (l - 0.16).max(0.3)),
    }
}

/// turned is a ramp with its hue turned by deg degrees.
pub fn turned(r: &Ramp, deg: f32) -> Ramp {
    let t = |c: [f32; 3]| {
        let (h, s, l) = to_hsl(c);
        // A little softer than the logo's own, so a room of them stays calm.
        from_hsl(
            (h + deg / 360. + 1.) % 1.,
            s * if deg != 0. { 0.9 } else { 1. },
            l,
        )
    };
    // The gradient a little stronger than the logo's own: it shows across a room.
    let lighten = |c: [f32; 3], dl: f32| {
        let (h, s, l) = to_hsl(c);
        from_hsl(h, s, (l + dl).clamp(0.2, 0.88))
    };
    let on = deg != 0.;
    Ramp {
        light: lighten(t(r.light), if on { 0.08 } else { 0. }),
        mid: t(r.mid),
        dark: lighten(t(r.dark), if on { -0.1 } else { 0. }),
    }
}

/// gradient is a geometry's vertex colours: the ramp's light at its top
/// left to its dark at the bottom right.
fn gradient(g: &Geo, size: f32, ramp: &Ramp) -> Vec<[f32; 3]> {
    g.pos
        .iter()
        .map(|p| {
            let u = (0.5 + (p.x - p.y + p.z * 0.3) / (2.4 * size)).clamp(0., 1.);
            if u < 0.55 {
                lerp3(ramp.light, ramp.mid, u / 0.55)
            } else {
                lerp3(ramp.mid, ramp.dark, (u - 0.55) / 0.45)
            }
        })
        .collect()
}

/// put_colored is Baker::put with a colour for every vertex (times the
/// material's), as three's vertexColors.
pub fn put_colored(b: &mut Baker, g: &Geo, colors: &[[f32; 3]], m: &Mat, t: Mat4) {
    let clear = m.opacity < 0.999;
    let i = match b
        .batches
        .iter()
        .position(|(tx, c, _)| *tx == m.texture && *c == clear)
    {
        Some(i) => i,
        None => {
            b.batches.push((
                m.texture,
                clear,
                MeshData {
                    texture: m.texture,
                    transparent: clear,
                    ..Default::default()
                },
            ));
            b.batches.len() - 1
        }
    };
    let out = &mut b.batches[i].2;
    let base = out.vertices.len() as u32;
    let nm = glam::Mat3::from_mat4(t).inverse().transpose();
    let rgba = m.rgba();
    #[allow(clippy::needless_range_loop)] // positions, normals, UVs and colours, side by side
    for k in 0..g.pos.len() {
        out.vertices.push(crate::gpu::Vertex {
            pos: t.transform_point3(g.pos[k]).into(),
            normal: (nm * g.normal[k]).normalize_or_zero().into(),
            uv: [g.uv[k].x * m.repeat[0], g.uv[k].y * m.repeat[1]],
            color: [
                colors[k][0] * rgba[0],
                colors[k][1] * rgba[1],
                colors[k][2] * rgba[2],
                rgba[3],
            ],
        });
    }
    out.indices.extend(g.indices.iter().map(|i| i + base));
}

// ---------------------------------------------------------------------------
// The geometries three has and mesh.rs doesn't.

fn push(g: &mut Geo, p: Vec3, n: Vec3, uv: Vec2) -> u32 {
    g.pos.push(p);
    g.normal.push(n);
    g.uv.push(uv);
    g.pos.len() as u32 - 1
}

/// lathe turns a profile — (radius, y, normal's radial part, normal's y) from
/// the top down — about y, as three's SphereGeometry does (and its capsule).
fn lathe(
    profile: &[(f32, f32, f32, f32)],
    seg: usize,
    phi_start: f32,
    phi_len: f32,
    skip_first: bool,
    skip_last: bool,
) -> Geo {
    let mut g = Geo::default();
    let hs = profile.len() - 1;
    for (j, &(rad, y, nr, ny)) in profile.iter().enumerate() {
        for i in 0..=seg {
            let u = i as f32 / seg as f32;
            let ph = phi_start + u * phi_len;
            let d = Vec3::new(-ph.cos(), 0., ph.sin());
            push(
                &mut g,
                Vec3::new(d.x * rad, y, d.z * rad),
                Vec3::new(d.x * nr, ny, d.z * nr),
                Vec2::new(u, 1. - j as f32 / hs as f32),
            );
        }
    }
    let row = seg as u32 + 1;
    for j in 0..hs as u32 {
        for i in 0..seg as u32 {
            let a = j * row + i;
            let b = a + row;
            if !(j == 0 && skip_first) {
                g.indices.extend([a, b, a + 1]);
            }
            if !(j == hs as u32 - 1 && skip_last) {
                g.indices.extend([a + 1, b, b + 1]);
            }
        }
    }
    g
}

/// sphere_part is SphereGeometry(r, ws, hs, 0, 2π, 0, theta_len): a cap.
fn sphere_cap(r: f32, ws: usize, hs: usize, theta_len: f32) -> Geo {
    let profile: Vec<_> = (0..=hs)
        .map(|j| {
            let th = j as f32 / hs as f32 * theta_len;
            (r * th.sin(), r * th.cos(), th.sin(), th.cos())
        })
        .collect();
    lathe(&profile, ws, 0., TAU, true, theta_len >= PI - 1e-6)
}

/// capsule is CapsuleGeometry(r, length, cap_seg, radial): a cylinder of
/// `length` between two hemispheres.
fn capsule(r: f32, length: f32, cap_seg: usize, radial: usize) -> Geo {
    let mut profile = Vec::new();
    for j in 0..=cap_seg {
        let th = j as f32 / cap_seg as f32 * FRAC_PI_2;
        profile.push((r * th.sin(), length / 2. + r * th.cos(), th.sin(), th.cos()));
    }
    for j in 0..=cap_seg {
        let th = FRAC_PI_2 + j as f32 / cap_seg as f32 * FRAC_PI_2;
        profile.push((
            r * th.sin(),
            -length / 2. + r * th.cos(),
            th.sin(),
            th.cos(),
        ));
    }
    lathe(&profile, radial, 0., TAU, true, true)
}

/// cyl_open is a cylinder with no caps.
fn cyl_open(top: f32, bottom: f32, h: f32, seg: usize) -> Geo {
    let mut g = mesh::cyl(top, bottom, h, seg);
    let n = 2 * (seg + 1);
    g.pos.truncate(n);
    g.normal.truncate(n);
    g.uv.truncate(n);
    g.indices.truncate(seg * 6);
    g
}

/// circle is CircleGeometry(r, seg), facing +z (its image's top is v = 0).
fn circle(r: f32, seg: usize) -> Geo {
    let mut g = Geo::default();
    push(&mut g, Vec3::ZERO, Vec3::Z, Vec2::splat(0.5));
    for i in 0..=seg {
        let a = i as f32 / seg as f32 * TAU;
        let (s, c) = a.sin_cos();
        push(
            &mut g,
            Vec3::new(r * c, r * s, 0.),
            Vec3::Z,
            Vec2::new((c + 1.) / 2., 1. - (s + 1.) / 2.),
        );
    }
    for i in 0..seg as u32 {
        g.indices.extend([0, i + 1, i + 2]);
    }
    g
}

/// ring is RingGeometry(inner, outer, seg).
fn ring(inner: f32, outer: f32, seg: usize) -> Geo {
    let mut g = Geo::default();
    for i in 0..=seg {
        let a = i as f32 / seg as f32 * TAU;
        let (s, c) = a.sin_cos();
        push(
            &mut g,
            Vec3::new(inner * c, inner * s, 0.),
            Vec3::Z,
            Vec2::ZERO,
        );
        push(
            &mut g,
            Vec3::new(outer * c, outer * s, 0.),
            Vec3::Z,
            Vec2::ONE,
        );
    }
    for i in 0..seg as u32 {
        let (a, b, c, d) = (i * 2, i * 2 + 1, i * 2 + 2, i * 2 + 3);
        g.indices.extend([a, b, c, b, d, c]);
    }
    g
}

/// tube is a round pipe of radius t along an arc of a circle (centre c, radius
/// r) from direction e towards direction d (both unit), as figures.ts's
/// TubeGeometry along a Catmull-Rom curve through five points of that arc.
fn tube_arc(c: Vec3, r: f32, e: Vec3, d: Vec3, t: f32) -> Geo {
    const N: usize = 8;
    const R: usize = 6;
    let mut g = Geo::default();
    let plane_n = e.cross(d).normalize_or_zero();
    for i in 0..=N {
        let dir = e.lerp(d, i as f32 / N as f32).normalize();
        let p = c + dir * r;
        for k in 0..=R {
            let a = k as f32 / R as f32 * TAU;
            let n = dir * a.cos() + plane_n * a.sin();
            push(
                &mut g,
                p + n * t,
                n,
                Vec2::new(i as f32 / N as f32, k as f32 / R as f32),
            );
        }
    }
    // Wind outwards: whichever way the frame turns.
    let tangent = d - e;
    let right_handed = dir_frame_sign(e, plane_n, tangent) > 0.;
    let row = R as u32 + 1;
    for i in 0..N as u32 {
        for k in 0..R as u32 {
            let a = i * row + k;
            let (b, cc, dd) = (a + 1, a + row, a + row + 1);
            if !right_handed {
                g.indices.extend([a, cc, b, b, cc, dd]);
            } else {
                g.indices.extend([a, b, cc, b, dd, cc]);
            }
        }
    }
    g
}

fn dir_frame_sign(a: Vec3, b: Vec3, t: Vec3) -> f32 {
    a.cross(b).dot(t)
}

/// piping is a white frame on a rounded box's edges (w by h by d, its
/// corners of radius r): twelve straight runs, along the middles of the
/// rounded edges, joined round each corner by arcs over its curve, as the
/// logo's line-art cube is drawn in one stroke. Merged: one draw.
fn piping(w: f32, h: f32, d: f32, r: f32, t: f32) -> Geo {
    let mut out = Geo::default();
    let (cx, cy, cz) = (w / 2. - r, h / 2. - r, d / 2. - r);
    let m = r * std::f32::consts::FRAC_1_SQRT_2;
    let mut run = |len: f32, axis: usize, x: f32, y: f32, z: f32| {
        let g = cyl_open(t, t, len, 8);
        let rot = match axis {
            0 => Mat4::from_rotation_z(FRAC_PI_2),
            2 => Mat4::from_rotation_x(FRAC_PI_2),
            _ => Mat4::IDENTITY,
        };
        out.merge(&g, at(x, y, z) * rot);
    };
    for sy in [-1., 1.] {
        for sz in [-1., 1.] {
            run(2. * cx, 0, 0., sy * (cy + m), sz * (cz + m));
        }
    }
    for sx in [-1., 1.] {
        for sz in [-1., 1.] {
            run(2. * cy, 1, sx * (cx + m), 0., sz * (cz + m));
        }
    }
    for sx in [-1., 1.] {
        for sy in [-1., 1.] {
            run(2. * cz, 2, sx * (cx + m), sy * (cy + m), 0.);
        }
    }
    for sx in [-1., 1.] {
        for sy in [-1., 1.] {
            for sz in [-1., 1.] {
                let c = Vec3::new(sx * cx, sy * cy, sz * cz);
                let diag = Vec3::new(sx, sy, sz).normalize();
                for e in [
                    Vec3::new(0., sy, sz),
                    Vec3::new(sx, 0., sz),
                    Vec3::new(sx, sy, 0.),
                ] {
                    out.merge(&tube_arc(c, r, e.normalize(), diag, t), Mat4::IDENTITY);
                }
                out.merge(
                    &mesh::sphere(t, 8, 6),
                    at(c.x + diag.x * r, c.y + diag.y * r, c.z + diag.z * r),
                );
            }
        }
    }
    out
}

// ---------------------------------------------------------------------------
// Textures: the logo's line-art cube and the tools' badges, drawn once.

/// logo_mark is the logo's white line-art cube, on nothing: for the top of a
/// head.
fn logo_mark(kit: &mut Kit) -> usize {
    kit.texture("figures:logo-mark", || {
        let mut s = Svg::new(128, 128);
        s.raw(r#"<path d="M64 16L106 40L106 88L64 112L22 88L22 40Z M22 40L64 64L106 40 M64 64L64 112" fill="none" stroke="rgba(255,255,255,0.92)" stroke-width="9" stroke-linejoin="round" stroke-linecap="round"/>"#);
        s.render()
    })
}

/// The badge's look for each tool: a colour and a glyph.
fn badge_texture(kit: &mut Kit, tool: Tool) -> usize {
    let (bg, fg) = match tool {
        Tool::Claude => ("#d97757", "#ffffff"),
        Tool::Codex => ("#111318", "#ffffff"),
        Tool::OpenCode => ("#f2f2f2", "#111318"),
        Tool::Cursor => ("#2b2f37", "#ffffff"),
        Tool::Gemini => ("#5b7cf6", "#ffffff"),
        Tool::Copilot => ("#6e40c9", "#ffffff"),
    };
    kit.texture(&format!("figures:badge:{}", tool.name()), || {
        let mut s = Svg::new(128, 128);
        s.circle(64., 64., 62., "#ffffff");
        s.circle(64., 64., 54., bg);
        // The glyphs (✳ >_ ▣ ◆ ✦) drawn as shapes: no font needs them.
        let stroke = |d: &str| format!(r#"<path d="{d}" fill="none" stroke="{fg}" stroke-width="9" stroke-linecap="round" stroke-linejoin="round"/>"#);
        match tool {
            Tool::Claude => {
                for a in [0f32, 45., 90., 135.] {
                    let (sn, cs) = a.to_radians().sin_cos();
                    s.raw(&stroke(&format!("M{} {}L{} {}", 64. - cs * 28., 66. - sn * 28., 64. + cs * 28., 66. + sn * 28.)));
                }
            }
            Tool::Codex => {
                s.raw(&stroke("M38 46L62 64L38 82"));
                s.raw(&stroke("M68 86L92 86"));
            }
            Tool::OpenCode => {
                s.raw(&format!(r#"<rect x="35" y="37" width="58" height="58" rx="7" fill="none" stroke="{fg}" stroke-width="8"/><rect x="51" y="53" width="26" height="26" rx="3" fill="{fg}"/>"#));
            }
            Tool::Cursor => {
                s.path("M64 30L98 66L64 102L30 66Z", fg);
            }
            Tool::Gemini => {
                s.path("M64 28Q64 66 102 66Q64 66 64 104Q64 66 26 66Q64 66 64 28Z", fg);
            }
            Tool::Copilot => {
                // A visor: two eyes in a rounded face.
                s.raw(&format!(r#"<rect x="32" y="42" width="64" height="46" rx="20" fill="none" stroke="{fg}" stroke-width="8"/><circle cx="50" cy="65" r="6" fill="{fg}"/><circle cx="78" cy="65" r="6" fill="{fg}"/>"#));
            }
        }
        s.render()
    })
}

// ---------------------------------------------------------------------------
// Easing.

/// ease moves v towards target at rate per second, at any frame rate.
pub fn ease(v: f32, target: f32, rate: f32, dt: f32) -> f32 {
    v + (target - v) * (1. - (-rate * dt).exp())
}
/// bump rises and falls once, smoothly, over [0, 1].
pub fn bump(u: f32) -> f32 {
    if u <= 0. || u >= 1. {
        0.
    } else {
        (PI * u).sin().powi(2)
    }
}

/// A mesh's parts into a node, with Baker.
fn bake(rig: &mut Rig, node: NodeId, f: impl FnOnce(&mut Baker)) {
    let mut b = Baker::default();
    f(&mut b);
    rig.nodes[node].meshes.extend(b.finish());
}

fn toon(c: &str) -> Mat {
    Mat::hex(c)
}
/// MeshBasicMaterial: lit by nothing.
fn basic(c: &str) -> Mat {
    Mat::hex(c).glow()
}

// ---------------------------------------------------------------------------
// Boxie

/// The six states' colours (the status light's).
fn tone_color(t: DeskTone) -> &'static str {
    match t {
        DeskTone::Needs => "#fbbf24",
        DeskTone::Error => "#f87171",
        DeskTone::Finished => "#34d399",
        DeskTone::Working => "#60a5fa",
        DeskTone::Idle => "#cbd5e1",
        DeskTone::Stopped => "#4b5563",
    }
}
const TONES: [DeskTone; 6] = [
    DeskTone::Needs,
    DeskTone::Error,
    DeskTone::Finished,
    DeskTone::Working,
    DeskTone::Idle,
    DeskTone::Stopped,
];
fn tone_index(t: DeskTone) -> usize {
    TONES.iter().position(|&x| x == t).unwrap_or(0)
}

#[derive(Clone, Copy)]
struct Pose {
    lean: f32,
    yaw: f32,
    roll: f32,
    squash: f32,
    lift: f32,
    lax: f32,
    laz: f32,
    rax: f32,
    raz: f32,
    eye_open: f32,
    look_x: f32,
    look_y: f32,
}

impl Pose {
    fn rest() -> Self {
        Self {
            lean: 0.,
            yaw: 0.,
            roll: 0.,
            squash: 1.,
            lift: 0.,
            lax: 0.,
            laz: -0.25,
            rax: 0.,
            raz: 0.25,
            eye_open: 1.,
            look_x: 0.,
            look_y: 0.,
        }
    }
    fn fields(&mut self) -> [&mut f32; 12] {
        [
            &mut self.lean,
            &mut self.yaw,
            &mut self.roll,
            &mut self.squash,
            &mut self.lift,
            &mut self.lax,
            &mut self.laz,
            &mut self.rax,
            &mut self.raz,
            &mut self.eye_open,
            &mut self.look_x,
            &mut self.look_y,
        ]
    }
}

#[derive(Clone, Copy, PartialEq)]
enum Mood {
    Open,
    Happy,
    Cross,
    Sleep,
}

struct Eye {
    open: NodeId,
    pupil: NodeId,
    happy: NodeId,
    cross: NodeId,
    sleep: NodeId,
}

/// A mascot: an agent as its tool's character, seated, facing +z, its
/// origin on the floor under its seat.
pub struct Mascot {
    pub rig: Rig,
    pub tool: Tool,
    pub accent: Accent,
    pub lead: bool,
    r#ref: String,
    body: NodeId,
    arms: [NodeId; 2],
    eyes: [Eye; 2],
    cheeks: [NodeId; 2],
    mouth: NodeId,
    pip: NodeId,
    pips: [NodeId; 6],
    // Boxie's measures.
    height: f32,
    eye_size: f32,
    hover: f32,
    // Its life.
    cur: Pose,
    last_t: Option<f32>,
    blink_at: f32,
    glance: (f32, f32, f32),
    phase: f32,
    life: Rng,
}

const HEAD: f32 = 0.46;

/// mascot builds an agent's figure from its ref (a stable hash gives it its
/// colour and what it wears), its AI tool, whether it's the lead, and its
/// place in the room (variant).
pub fn mascot(
    r#ref: &str,
    ai: Option<&str>,
    lead: bool,
    variant: Option<u32>,
    kit: &mut Kit,
) -> Mascot {
    let tool = tool_of(ai);
    let acc = accent_of(r#ref, variant);
    let mut life = Rng::new(hash(&format!("{}:life", r#ref)));
    let mut rig = Rig::new();
    // Bigger than life: they're who you came to see.
    // Big for their desks, as a toy's people are.
    rig.nodes[0].scale = Vec3::splat(if lead { 2.65 } else { 2.5 });
    let seat = rig.add("seat", Some(0), Vec3::new(0., 0.49, 0.));
    let body = rig.add("body", Some(seat), Vec3::ZERO);
    let s = HEAD;
    let height = 0.34 + s * 0.92 + 0.02;
    let eye_gap = 0.088;
    let eye_size = 0.062;

    // The body a shade under the head, all one colour, as a toy is.
    let ramp = if lead {
        logo()
    } else {
        turned(&logo(), acc.shift)
    };
    let coat = Mat::new(lerp3(ramp.mid, ramp.light, 0.15));
    let shoe = toon("#ffffff");
    let badge_tex = badge_texture(kit, tool);
    bake(&mut rig, body, |b| {
        // The body: a soft little bean, legs sticking forward off the seat.
        b.put(
            &capsule(0.17, 0.12, 8, 18),
            &coat,
            trs(0., 0.22, 0., 0., 0., 0., Vec3::new(1.05, 1., 0.9)),
        );
        for sx in [-1., 1.] {
            b.put(
                &capsule(0.055, 0.1, 6, 12),
                &coat,
                trs(sx * 0.07, 0.07, 0.11, FRAC_PI_2, 0., 0., Vec3::ONE),
            );
            b.put(
                &mesh::sphere(0.065, 14, 10),
                &shoe,
                at(sx * 0.07, 0.06, 0.2),
            );
        }
        // Its AI tool's badge, on its chest: what tells a Claude Code agent
        // from a Codex one.
        b.put(
            &circle(0.045, 24),
            &Mat::new([1., 1., 1.]).tex(badge_tex, [1., 1.]),
            trs(0.06, 0.24, 0.14, -0.15, 0., 0., Vec3::ONE),
        );
    });
    // Stubby arms with round hands (pivots, raised by rotation.z, reaching
    // forward by rotation.x).
    let mut arms = [0; 2];
    for (i, sx) in [-1f32, 1.].into_iter().enumerate() {
        let p = rig.add(
            if i == 0 { "arm-l" } else { "arm-r" },
            Some(body),
            Vec3::new(sx * 0.16, 0.27, 0.01),
        );
        bake(&mut rig, p, |b| {
            b.put(&capsule(0.045, 0.07, 6, 12), &coat, at(0., -0.06, 0.));
            b.put(&mesh::sphere(0.052, 14, 10), &shoe, at(0., -0.13, 0.));
        });
        arms[i] = p;
    }

    // The head: the logo's cube, rounded, in its gradient, with white piping
    // along its edges as the logo's line art (one unbroken frame round the
    // rounded cube); on top, the logo itself, its white line-art cube seen
    // from above; on the front a dark, glossy screen.
    let head = rig.add("head", Some(body), Vec3::new(0., 0.34 + s * 0.46, 0.));
    let mark_tex = logo_mark(kit);
    bake(&mut rig, head, |b| {
        let g = mesh::rbox_seg(s, s * 0.92, s * 0.9, 8, 0.16);
        let colors = gradient(&g, s, &ramp);
        put_colored(b, &g, &colors, &toon("#ffffff"), Mat4::IDENTITY);
        b.put(
            &piping(s, s * 0.92, s * 0.9, 0.16, 0.032),
            &toon("#ffffff"),
            Mat4::IDENTITY,
        );
        let mut mark = mesh::plane(s * 0.5, s * 0.5);
        for uv in &mut mark.uv {
            uv.y = 1. - uv.y;
        }
        b.put(
            &mark,
            &basic("#ffffff").tex(mark_tex, [1., 1.]),
            trs(0., s * 0.92 / 2. + 0.003, 0., -FRAC_PI_2, 0., 0., Vec3::ONE),
        );
        b.put(
            &mesh::rbox_seg(s * 0.82, s * 0.64, 0.03, 4, 0.07),
            &Mat::hex("#1a1530"),
            at(0., -0.01, s * 0.45),
        );
    });
    let face = rig.add("face", Some(head), Vec3::new(0., -0.005, s * 0.45 + 0.017));

    // The eyes: big ones, white with pupils that look about and a glint. Each
    // has its open shape, a happy arc, a cross for an error, and closed for
    // asleep. On a dark face screen the moods are drawn in light.
    let ink = basic("#16131f");
    let white = basic("#ffffff");
    let e = eye_size;
    let mut eyes = Vec::new();
    for sx in [-1f32, 1.] {
        let pivot = rig.add("eye", Some(face), Vec3::new(sx * eye_gap, 0., 0.));
        let open = rig.add("eye-open", Some(pivot), Vec3::ZERO);
        bake(&mut rig, open, |b| {
            let sy = Vec3::new(1., 1.25, 1.);
            b.put(&circle(e, 24), &white, trs(0., 0., 0., 0., 0., 0., sy));
            b.put(
                &ring(e, e * 1.18, 24),
                &ink,
                trs(0., 0., 0., 0., 0., 0., sy),
            );
        });
        let pupil = rig.add("pupil", Some(open), Vec3::new(0., 0., 0.002));
        bake(&mut rig, pupil, |b| {
            b.put(
                &circle(e * 0.58, 20),
                &ink,
                trs(0., 0., 0., 0., 0., 0., Vec3::new(1., 1.2, 1.)),
            );
            b.put(&circle(e * 0.2, 12), &white, at(e * 0.2, e * 0.28, 0.002));
        });
        let happy = rig.add("eye-happy", Some(pivot), Vec3::ZERO);
        bake(&mut rig, happy, |b| {
            b.put(
                &mesh::torus(e * 0.75, e * 0.22, 6, 16, PI),
                &white,
                Mat4::IDENTITY,
            )
        });
        let cross = rig.add("eye-cross", Some(pivot), Vec3::ZERO);
        bake(&mut rig, cross, |b| {
            for a in [0.78, -0.78] {
                b.put(
                    &mesh::cuboid(e * 1.6, e * 0.35, 0.01),
                    &white,
                    trs(0., 0., 0., 0., 0., a, Vec3::ONE),
                );
            }
        });
        let sleep = rig.add("eye-sleep", Some(pivot), Vec3::ZERO);
        bake(&mut rig, sleep, |b| {
            b.put(
                &mesh::torus(e * 0.7, e * 0.18, 6, 16, PI),
                &white,
                trs(0., 0., 0., 0., 0., PI, Vec3::ONE),
            )
        });
        eyes.push(Eye {
            open,
            pupil,
            happy,
            cross,
            sleep,
        });
    }
    // A blush, for when it's pleased or waving.
    let blush = basic("#ff7fa8").opacity(0.6);
    let mut cheeks = [0; 2];
    for (i, sx) in [-1f32, 1.].into_iter().enumerate() {
        let c = rig.add(
            "cheek",
            Some(face),
            Vec3::new(sx * (eye_gap + e * 1.1), -e * 1.5, 0.001),
        );
        bake(&mut rig, c, |b| {
            b.put(&circle(e * 0.55, 14), &blush, Mat4::IDENTITY)
        });
        cheeks[i] = c;
    }
    // Its mouth: open when it calls you, a grin when pleased.
    let mouth = rig.add("mouth", Some(face), Vec3::new(0., -e * 1.55, 0.));
    rig.nodes[mouth].rot.z = PI;
    bake(&mut rig, mouth, |b| {
        b.put(
            &mesh::torus(e * 0.45, e * 0.13, 6, 16, PI),
            &white,
            Mat4::IDENTITY,
        )
    });

    // What it wears stands out against it: another of the accents.
    let accessory = if lead {
        Accessory::Headset
    } else {
        acc.accessory
    };
    let at_i = ACCENTS.iter().position(|&c| c == acc.color).unwrap_or(0);
    let top = rig.add("top", Some(body), Vec3::new(0., height, 0.));
    wear(
        &mut rig,
        top,
        accessory,
        ACCENTS[(at_i + 3) % ACCENTS.len()],
    );

    // The status light: a little glowing pip over its head (one per state,
    // the current one shown).
    let pip = rig.add("pip", Some(body), Vec3::new(0., height + 0.22, 0.));
    let mut pips = [0; 6];
    for (i, &t) in TONES.iter().enumerate() {
        let n = rig.add("pip-light", Some(pip), Vec3::ZERO);
        bake(&mut rig, n, |b| {
            b.put(
                &mesh::sphere(0.035, 14, 10),
                &basic(tone_color(t)),
                Mat4::IDENTITY,
            )
        });
        rig.nodes[n].visible = false;
        pips[i] = n;
    }

    let blink_at = 1. + life.next() as f32 * 3.;
    let phase = life.next() as f32 * 20.;
    Mascot {
        rig,
        tool,
        accent: acc,
        lead,
        r#ref: r#ref.to_string(),
        body,
        arms,
        eyes: [eyes.remove(0), eyes.remove(0)],
        cheeks,
        mouth,
        pip,
        pips,
        height,
        eye_size,
        hover: 0.,
        cur: Pose::rest(),
        last_t: None,
        blink_at,
        glance: (0., 0., 0.),
        phase,
        life,
    }
}

/// wear puts the agent's accent on its head (or round its neck).
fn wear(rig: &mut Rig, top: NodeId, accessory: Accessory, color: &str) {
    let fabric = toon(color);
    let mut parts: Vec<(Geo, Mat, Mat4)> = Vec::new();
    match accessory {
        Accessory::Beanie => {
            parts.push((
                sphere_cap(0.15, 20, 10, FRAC_PI_2),
                fabric,
                trs(0., 0., 0., 0., 0., 0., Vec3::new(1.2, 0.8, 1.1)),
            ));
            parts.push((
                mesh::sphere(0.045, 12, 10),
                toon("#ffffff"),
                at(0., 0.13, 0.),
            ));
        }
        Accessory::Cap => {
            parts.push((
                sphere_cap(0.15, 20, 10, FRAC_PI_2),
                fabric,
                trs(0., 0., 0., 0., 0., 0., Vec3::new(1.15, 0.6, 1.1)),
            ));
            parts.push((
                mesh::rbox_seg(0.22, 0.02, 0.13, 2, 0.008),
                fabric,
                at(0., 0.01, 0.17),
            ));
        }
        Accessory::Headphones | Accessory::Headset => {
            parts.push((
                mesh::torus(0.22, 0.02, 8, 28, PI),
                fabric,
                at(0., -0.12, 0.),
            ));
            for sx in [-1., 1.] {
                parts.push((
                    mesh::rbox_seg(0.07, 0.13, 0.12, 2, 0.03),
                    fabric,
                    at(sx * 0.23, -0.14, 0.),
                ));
            }
            if accessory == Accessory::Headset {
                parts.push((
                    mesh::cyl(0.008, 0.008, 0.2, 6),
                    toon("#333333"),
                    trs(0.2, -0.22, 0.1, FRAC_PI_2, 0., -0.6, Vec3::ONE),
                ));
            }
        }
        Accessory::Bow => {
            for s in [-1f32, 1.] {
                parts.push((
                    mesh::cyl(0., 0.06, 0.1, 12),
                    fabric,
                    trs(s * 0.06, 0.03, 0., 0., 0., s * FRAC_PI_2, Vec3::ONE),
                ));
            }
            parts.push((mesh::sphere(0.03, 10, 8), fabric, at(0., 0.03, 0.)));
            rig.nodes[top].pos.x = 0.1;
        }
        Accessory::Crown => {
            let gold = toon("#ffcf3f");
            parts.push((cyl_open(0.1, 0.11, 0.06, 12), gold, Mat4::IDENTITY));
            for i in 0..5 {
                let a = i as f32 / 5. * TAU;
                parts.push((
                    mesh::cyl(0., 0.025, 0.06, 6),
                    gold,
                    at(a.sin() * 0.1, 0.05, a.cos() * 0.1),
                ));
            }
            parts.push((mesh::sphere(0.02, 8, 6), fabric, at(0., 0.02, 0.105)));
        }
        Accessory::Sprout => {
            let leaf = toon("#5cc46b");
            parts.push((mesh::cyl(0.008, 0.01, 0.1, 6), leaf, at(0., 0.05, 0.)));
            for s in [-1f32, 1.] {
                parts.push((
                    mesh::sphere(0.045, 12, 8),
                    leaf,
                    trs(s * 0.045, 0.1, 0., 0., 0., s * 0.5, Vec3::new(1., 0.4, 0.6)),
                ));
            }
        }
        Accessory::Scarf => {
            rig.nodes[top].pos.y = 0.16;
            parts.push((
                mesh::torus(0.19, 0.035, 10, 28, TAU),
                fabric,
                trs(0., 0., 0., FRAC_PI_2, 0., 0., Vec3::new(1.15, 0.85, 1.)),
            ));
            parts.push((
                mesh::rbox_seg(0.06, 0.14, 0.03, 2, 0.01),
                fabric,
                trs(0.1, -0.08, 0.2, 0., 0., 0.2, Vec3::ONE),
            ));
        }
        Accessory::None => {}
    }
    rig.bake(top, &parts);
}

impl Mascot {
    /// pose poses it for its state at time t (seconds); motion is off for
    /// still frames. look is where you stand, in its frame (x, z), when
    /// you're near enough to be looked at.
    pub fn pose(&mut self, tone: DeskTone, t0: f32, motion: bool, look: Option<(f32, f32)>) {
        let dt = match self.last_t {
            None => 1.,
            Some(l) => (t0 - l).clamp(0., 0.1),
        };
        self.last_t = Some(t0);
        let t = if motion { t0 + self.phase } else { self.phase };
        let mut tg = Pose::rest();
        let mut mood = Mood::Open;
        let looking = look.is_some_and(|(x, z)| {
            matches!(tone, DeskTone::Needs | DeskTone::Idle | DeskTone::Finished)
                && x.hypot(z) < 4.5
        });
        match tone {
            DeskTone::Working => {
                // Typing: arms forward, tapping in turn, a little squash with
                // each tap, eyes down at the keys.
                let tap = |o: f32| bump(((t * 3.4 + o) % 1.) * 1.7);
                tg.lax = -1.05 + tap(0.) * 0.5;
                tg.rax = -1.05 + tap(0.5) * 0.5;
                tg.laz = -0.15;
                tg.raz = 0.15;
                tg.lean = 0.03;
                tg.squash = 1. - (tap(0.) + tap(0.5)) * 0.025;
                tg.look_y = -0.4;
                tg.yaw = (t * 0.37).sin() * 0.1;
            }
            DeskTone::Needs => {
                // Waving at you, hopping: stretched in the air, squashed landing.
                let hop = (t * 3.6).sin().abs();
                tg.raz = 2.6 + (t * 8.).sin() * 0.6;
                tg.rax = -0.2;
                tg.laz = -0.5;
                tg.lift = hop * 0.06;
                tg.squash = 1. + (hop - 0.35) * 0.12;
                tg.roll = (t * 4.).sin() * 0.08;
                tg.eye_open = 1.15;
            }
            DeskTone::Finished => {
                // A happy jump every few seconds, arms up; content between.
                let j = (t % 3.6) / 0.9;
                let up = bump(j);
                tg.laz = -0.5 - up * 2.1;
                tg.raz = 0.5 + up * 2.1;
                tg.lift = up * 0.12;
                tg.squash = if j > 0. && j < 1. {
                    1. + up * 0.15 - if j > 0.85 { 0.12 } else { 0. }
                } else {
                    1. + (t * 2.).sin() * 0.01
                };
                tg.lean = -0.06;
                mood = Mood::Happy;
            }
            DeskTone::Error => {
                // Arms up to its head, shaking it now and then; squashed, glum.
                let shake = bump((t % 3.2) / 0.8) * (t * 18.).sin() * 0.2;
                tg.lax = -2.6;
                tg.rax = -2.6;
                tg.laz = -0.4;
                tg.raz = 0.4;
                tg.yaw = shake;
                tg.lean = 0.15;
                tg.squash = 0.93;
                mood = Mood::Cross;
            }
            DeskTone::Idle => {
                // At rest: breathing, looking about now and then.
                let mut rr = Rng::new(hash(&format!(
                    "{}:{}",
                    self.r#ref,
                    (t / 3.2).floor() as i64
                )));
                tg.yaw = (rr.next() as f32 - 0.5) * 0.9;
                tg.squash = 1. + (t * 1.8).sin() * 0.015;
            }
            DeskTone::Stopped => {
                tg.lean = 0.1;
                tg.roll = 0.1;
                tg.squash = 0.97 + (t * 0.8).sin() * 0.02;
                tg.laz = -0.1;
                tg.raz = 0.1;
                mood = Mood::Sleep;
            }
        }
        if looking && let Some((lx, lz)) = look {
            // A turn of the body towards you, the eyes doing the rest, so its
            // face stays to the camera.
            // The eyes look at you; the body stays to the room, its face to the camera.
            let toward = lx.atan2(lz);
            tg.yaw = (toward * 0.2).clamp(-0.15, 0.15);
            tg.look_x = toward.clamp(-1., 1.);
            tg.lean = tg.lean.min(-0.04);
        }
        if motion && t0 > self.blink_at {
            self.blink_at = t0 + 2.2 + self.life.next() as f32 * 3.5;
        }
        let blinking =
            motion && mood == Mood::Open && self.blink_at - t0 > 0. && self.blink_at - t0 < 0.13;
        if motion && t0 > self.glance.0 {
            let until = t0 + 0.8 + self.life.next() as f32 * 2.5;
            let x = (self.life.next() as f32 - 0.5) * 1.4;
            let y = (self.life.next() as f32 - 0.5) * 0.8;
            self.glance = (until, x, y);
        }
        if motion && tone != DeskTone::Working && !looking {
            tg.look_x += self.glance.1;
            tg.look_y += self.glance.2;
        }
        let rates = [10., 10., 10., 16., 16., 10., 10., 10., 10., 10., 10., 10.];
        let targets = tg.fields().map(|f| *f);
        for ((cur, target), rate) in self.cur.fields().into_iter().zip(targets).zip(rates) {
            *cur = if motion {
                ease(*cur, target, rate, dt)
            } else {
                target
            };
        }
        let cur = self.cur;

        // Squash and stretch keep its volume: thinner as it stretches.
        let sq = cur.squash;
        let wide = 1. / sq.sqrt();
        let body = &mut self.rig.nodes[self.body];
        body.scale = Vec3::new(wide, sq, wide);
        body.pos.y = self.hover
            + cur.lift
            + if motion && self.hover != 0. {
                (t * 2.2).sin() * 0.015
            } else {
                0.
            };
        // (three's Euler order YXZ, as the XYZ the rig keeps.)
        let (x, y, z) =
            Quat::from_euler(EulerRot::YXZ, cur.yaw, cur.lean, cur.roll).to_euler(EulerRot::XYZ);
        body.rot = Vec3::new(x, y, z);
        self.rig.nodes[self.arms[0]].rot = Vec3::new(cur.lax, 0., cur.laz);
        self.rig.nodes[self.arms[1]].rot = Vec3::new(cur.rax, 0., cur.raz);

        let open = if blinking { 0.12 } else { cur.eye_open };
        let es = self.eye_size;
        for e in &self.eyes {
            self.rig.nodes[e.open].visible = mood == Mood::Open;
            self.rig.nodes[e.happy].visible = mood == Mood::Happy;
            self.rig.nodes[e.cross].visible = mood == Mood::Cross;
            self.rig.nodes[e.sleep].visible = mood == Mood::Sleep;
            self.rig.nodes[e.open].scale = Vec3::new(1., open.max(0.1), 1.);
            self.rig.nodes[e.pupil].pos =
                Vec3::new(cur.look_x * es * 0.35, cur.look_y * es * 0.3, 0.002);
        }
        let pleased = mood == Mood::Happy || tone == DeskTone::Needs;
        for c in self.cheeks {
            self.rig.nodes[c].visible = pleased;
        }
        let m = &mut self.rig.nodes[self.mouth];
        m.visible = pleased;
        m.scale = Vec3::new(
            if tone == DeskTone::Needs { 0.7 } else { 1.4 },
            if tone == DeskTone::Needs { 1.8 } else { 1. },
            1.,
        );
        for (i, p) in self.pips.into_iter().enumerate() {
            self.rig.nodes[p].visible = i == tone_index(tone) && tone != DeskTone::Stopped;
        }
        let pip = &mut self.rig.nodes[self.pip];
        pip.pos.y = self.height + 0.22 + if motion { (t * 2.4).sin() * 0.025 } else { 0. };
        pip.scale = Vec3::splat(if tone == DeskTone::Needs && motion {
            1. + (t0 * 6.).sin().abs() * 0.5
        } else {
            1.
        });
    }
}

impl Figure for Mascot {
    fn rig(&self) -> &Rig {
        &self.rig
    }
    fn pose(&mut self, tone: DeskTone, t: f32, motion: bool, look: Option<(f32, f32)>) {
        Mascot::pose(self, tone, t, motion, look)
    }
}

// ---------------------------------------------------------------------------
// Walker

/// walker is you: a chibi in a hoodie, standing, facing +z until you turn.
/// (The stand-in for the player, player.rs, until Kenney's has loaded.)
pub struct Walker {
    pub rig: Rig,
    squash: NodeId,
    legs: [NodeId; 2],
    torso: NodeId,
    head: NodeId,
    arms: [(NodeId, f32); 2],
    eyes: [(NodeId, NodeId); 2],
    gait: f32,
    last_t: Option<f32>,
    k: f32,
    head_lag: f32,
    blink_at: f32,
}

pub fn walker(_kit: &mut Kit) -> Walker {
    let hoodie = toon("#7c5cf0");
    let hood = toon("#5b3fd0");
    let pants = toon("#2c3550");
    let skin = toon("#ffd2b3");
    let hair = toon("#3b2a22");
    let shoe = toon("#ffffff");
    let ink = basic("#16131f");
    let white = basic("#ffffff");
    let blush = basic("#ff8fa3").opacity(0.55);
    let mut rig = Rig::new();
    // Squash and stretch happen on this, its pivot at the feet.
    let squash = rig.add("squash", Some(0), Vec3::ZERO);
    let mut legs = [0; 2];
    for (i, sx) in [-1f32, 1.].into_iter().enumerate() {
        let p = rig.add("leg", Some(squash), Vec3::new(sx * 0.1, 0.3, 0.));
        rig.bake(
            p,
            &[
                (capsule(0.075, 0.12, 6, 12), pants, at(0., -0.13, 0.)),
                (
                    mesh::rbox_seg(0.15, 0.1, 0.22, 3, 0.045),
                    shoe,
                    at(0., -0.25, 0.03),
                ),
            ],
        );
        legs[i] = p;
    }
    // A stubby body, a hoodie with its hood behind.
    let torso = rig.add("torso", Some(squash), Vec3::new(0., 0.3, 0.));
    rig.bake(
        torso,
        &[
            (
                capsule(0.19, 0.14, 8, 16),
                hoodie,
                trs(0., 0.17, 0., 0., 0., 0., Vec3::new(1.08, 1., 0.92)),
            ),
            (
                mesh::sphere(0.15, 16, 12),
                hood,
                trs(0., 0.3, -0.12, 0., 0., 0., Vec3::new(1.1, 0.7, 0.6)),
            ),
            (
                mesh::rbox_seg(0.18, 0.08, 0.04, 2, 0.02),
                hood,
                at(0., 0.1, 0.17),
            ),
        ],
    );
    // The big head, with a hair cap and tuft, big eyes, brows, a smile.
    let head = rig.add("head", Some(torso), Vec3::new(0., 0.62, 0.));
    let mut parts = vec![
        (
            mesh::sphere(0.27, 32, 24),
            skin,
            trs(0., 0., 0., 0., 0., 0., Vec3::new(1.06, 0.98, 1.)),
        ),
        (
            sphere_cap(0.285, 32, 16, PI * 0.52),
            hair,
            trs(0., 0.02, -0.02, -0.4, 0., 0., Vec3::ONE),
        ),
        (
            mesh::cyl(0., 0.07, 0.16, 10),
            hair,
            trs(0.04, 0.28, 0.02, 0., 0., -0.5, Vec3::ONE),
        ),
        (
            mesh::sphere(0.12, 16, 10),
            hair,
            trs(0.08, 0.15, 0.18, 0., 0., -0.3, Vec3::new(1.5, 0.5, 0.6)),
        ),
    ];
    for sx in [-1f32, 1.] {
        parts.push((
            mesh::sphere(0.06, 12, 10),
            skin,
            trs(sx * 0.285, -0.02, 0., 0., 0., 0., Vec3::new(0.5, 1., 0.8)),
        ));
        parts.push((
            circle(0.035, 16),
            blush,
            trs(sx * 0.17, -0.09, 0.225, 0., sx * 0.5, 0., Vec3::ONE),
        ));
    }
    parts.push((
        mesh::torus(0.035, 0.009, 6, 16, PI),
        ink,
        trs(0., -0.1, 0.262, 0., 0., PI, Vec3::ONE),
    ));
    rig.bake(head, &parts);
    let mut eyes = [(0, 0); 2];
    for (i, sx) in [-1f32, 1.].into_iter().enumerate() {
        let e = rig.add("eye", Some(head), Vec3::new(sx * 0.1, 0., 0.258));
        rig.nodes[e].rot.y = sx * 0.2;
        // Big black eyes with two catchlights, the toy look's.
        let sclera = rig.add("sclera", Some(e), Vec3::ZERO);
        rig.nodes[sclera].scale.y = 1.35;
        rig.bake(sclera, &[(circle(0.05, 24), ink, Mat4::IDENTITY)]);
        let pupil = rig.add("pupil", Some(e), Vec3::new(0., 0., 0.002));
        rig.bake(
            pupil,
            &[
                (circle(0.017, 12), white, at(0.016, 0.026, 0.001)),
                (circle(0.008, 8), white, at(-0.014, -0.02, 0.001)),
            ],
        );
        rig.bake(
            e,
            &[(
                mesh::rbox_seg(0.075, 0.016, 0.01, 2, 0.006),
                hair,
                trs(0., 0.1, 0., 0., 0., sx * -0.15, Vec3::ONE),
            )],
        );
        eyes[i] = (sclera, pupil);
    }
    // Short arms with round hands.
    let mut arms = [(0, 0.); 2];
    for (i, sx) in [-1f32, 1.].into_iter().enumerate() {
        let sh = rig.add("arm", Some(torso), Vec3::new(sx * 0.2, 0.26, 0.));
        rig.nodes[sh].rot.z = sx * 0.35;
        rig.bake(
            sh,
            &[
                (capsule(0.06, 0.12, 6, 12), hoodie, at(0., -0.1, 0.)),
                (mesh::sphere(0.06, 14, 10), skin, at(0., -0.2, 0.)),
            ],
        );
        arms[i] = (sh, sx);
    }
    Walker {
        rig,
        squash,
        legs,
        torso,
        head,
        arms,
        eyes,
        gait: 0.,
        last_t: None,
        k: 0.,
        head_lag: 0.,
        blink_at: 2.,
    }
}

impl Walker {
    /// stride poses it walking at `speed` (m/s) at time t.
    pub fn stride(&mut self, speed: f32, t: f32, motion: bool) {
        let dt = match self.last_t {
            None => 0.,
            Some(l) => (t - l).clamp(0., 0.1),
        };
        self.last_t = Some(t);
        self.k = if motion {
            ease(self.k, (speed / 2.6).min(1.), 8., dt)
        } else {
            0.
        };
        let k = self.k;
        self.gait += dt * (5. + speed * 2.6);
        let gait = self.gait;
        let s = gait.sin();
        self.rig.nodes[self.legs[0]].rot.x = s * 0.7 * k;
        self.rig.nodes[self.legs[1]].rot.x = -s * 0.7 * k;
        for (n, sx) in self.arms {
            self.rig.nodes[n].rot = Vec3::new(
                if sx == -1. { s * 0.9 * k } else { -s * 0.9 * k },
                0.,
                sx * (0.35 + 0.15 * k),
            );
        }
        // Squash on each footfall, stretch between: volume kept.
        let st = if motion {
            1. + (gait * 2.).cos() * 0.07 * k + (t * 2.4).sin() * 0.015 * (1. - k)
        } else {
            1.
        };
        let wide = 1. / st.sqrt();
        self.rig.nodes[self.squash].scale = Vec3::new(wide, st, wide);
        self.rig.nodes[self.squash].pos.y = (gait * 2.).cos().max(0.) * 0.05 * k;
        self.rig.nodes[self.torso].rot = Vec3::new(0.06 * k, s * 0.1 * k, 0.);
        // The big head lags the body a little, and looks about at rest.
        self.head_lag = ease(self.head_lag, -s * 0.12 * k, 10., dt);
        let lag = self.head_lag;
        self.rig.nodes[self.head].rot = Vec3::new(
            if motion {
                (t * 0.7).sin() * 0.04 * (1. - k)
            } else {
                0.
            },
            (if motion {
                (t * 0.33).sin() * 0.35 * (1. - k)
            } else {
                0.
            }) + lag,
            lag * 0.5,
        );
        if motion && t > self.blink_at {
            self.blink_at = t + 2.4 + (t * 7.3) % 3.;
        }
        let blink = motion && self.blink_at - t > 0. && self.blink_at - t < 0.12;
        for (sclera, pupil) in self.eyes {
            self.rig.nodes[sclera].scale.y = if blink { 0.12 } else { 1.35 };
            self.rig.nodes[pupil].visible = !blink;
        }
    }

    /// fade is how much of it shows (1 solid, under 0.2 gone).
    pub fn fade(&mut self, opacity: f32) {
        for n in &mut self.rig.nodes {
            n.opacity = opacity;
        }
        self.rig.nodes[0].visible = opacity > 0.2;
    }
}

impl Figure for Walker {
    fn rig(&self) -> &Rig {
        &self.rig
    }
    fn pose(&mut self, _tone: DeskTone, _t: f32, _motion: bool, _look: Option<(f32, f32)>) {}
}

#[cfg(test)]
mod tests {
    use super::*;

    fn finite(rig: &Rig) -> usize {
        let mut n = 0;
        for node in &rig.nodes {
            for m in &node.meshes {
                assert!(!m.indices.is_empty());
                assert!(m.indices.iter().all(|&i| (i as usize) < m.vertices.len()));
                for v in &m.vertices {
                    assert!(
                        v.pos
                            .iter()
                            .chain(&v.normal)
                            .chain(&v.uv)
                            .chain(&v.color)
                            .all(|x| x.is_finite())
                    );
                }
                n += 1;
            }
        }
        n
    }

    fn transforms(rig: &Rig) -> Vec<[f32; 16]> {
        rig.world(Mat4::IDENTITY)
            .iter()
            .map(|(m, v)| {
                let mut a = m.to_cols_array();
                if !v {
                    a[0] = f32::NAN;
                }
                a
            })
            .collect()
    }

    #[test]
    fn the_hash_is_javascripts() {
        // From figures.ts itself (node).
        assert_eq!(hash("a"), 3826002220);
        assert_eq!(hash("demo/agent-02"), 3184793693);
        assert_eq!(hash("agent-740"), 2279251398);
        assert_eq!(hash("héllo🙂"), 774582598);
        assert_eq!(hash(&"x".repeat(40)), 2226015845);
    }

    #[test]
    fn accents_are_javascripts() {
        let a = accent_of("a", None);
        assert_eq!(
            (a.color, a.accessory, a.shift),
            ("#8b7cf6", Accessory::None, 64.)
        );
        assert!((a.tint - 0.06485999999999992).abs() < 1e-12);
        let a = accent_of("demo/agent-02", None);
        assert_eq!(
            (a.color, a.accessory, a.shift),
            ("#5aaef0", Accessory::Headphones, 84.)
        );
        assert!((a.tint + 0.98542).abs() < 1e-12);
        let a = accent_of("agent-740", None);
        assert_eq!(
            (a.color, a.accessory, a.shift),
            ("#ff9150", Accessory::Beanie, -40.)
        );
        let a = accent_of("héllo🙂", None);
        assert_eq!(
            (a.color, a.accessory, a.shift),
            ("#e077c6", Accessory::Headphones, -60.)
        );
        let a = accent_of(&"x".repeat(40), None);
        assert_eq!(
            (a.color, a.accessory, a.shift),
            ("#8b7cf6", Accessory::None, -20.)
        );
        // By place in the room.
        let a = accent_of("a", Some(5));
        assert_eq!(
            (a.color, a.accessory, a.shift),
            ("#e077c6", Accessory::Crown, -60.)
        );
        let a = accent_of("a", Some(13));
        assert_eq!(
            (a.color, a.accessory, a.shift),
            ("#ff7aa2", Accessory::Crown, -60.)
        );
    }

    fn close(a: [f32; 3], b: [f32; 3]) {
        for i in 0..3 {
            assert!((a[i] - b[i]).abs() < 2e-4, "{a:?} vs {b:?}");
        }
    }

    #[test]
    fn ramps_are_javascripts() {
        let r = ramp_of("#ff7aa2");
        close(r.light, [0.986865, 0.487752, 0.561106]);
        close(r.mid, [1., 0.194618, 0.361307]);
        close(r.dark, [0.830887, 0.116961, 0.043731]);
        let r = ramp_of("#45cbb8");
        close(r.light, [0.104058, 0.832655, 0.716635]);
        close(r.dark, [0.078935, 0.485357, 0.521065]);
        let t = turned(&logo(), 20.);
        close(t.light, [0.695642, 0.437005, 0.937151]);
        close(t.mid, [0.467228, 0.08254, 0.806645]);
        close(t.dark, [0.251336, 0.074311, 0.570473]);
        let t = turned(&logo(), -78.);
        close(t.light, [0.437005, 0.937151, 0.879029]);
        close(t.dark, [0.074311, 0.570473, 0.433262]);
    }

    #[test]
    fn every_tool_and_accessory_builds_a_finite_mascot() {
        let mut kit = Kit::default();
        for tool in Tool::ALL {
            let m = mascot("agent-1", Some(tool.name()), false, None, &mut kit);
            assert!(finite(&m.rig) > 8);
        }
        for (i, acc) in Accessory::ALL.into_iter().enumerate() {
            let m = mascot("agent-2", None, false, Some(i as u32), &mut kit);
            assert_eq!(m.accent.accessory, acc);
            let top = m.rig.find("top").unwrap();
            assert_eq!(m.rig.nodes[top].meshes.is_empty(), acc == Accessory::None);
            finite(&m.rig);
        }
        let lead = mascot("lead", Some("claude"), true, None, &mut kit);
        let top = lead.rig.find("top").unwrap();
        assert!(!lead.rig.nodes[top].meshes.is_empty());
        finite(&lead.rig);
        // The badges and the logo are textures the kit holds.
        assert_eq!(kit.textures.len(), Tool::ALL.len() + 1);
    }

    #[test]
    fn every_tone_poses_it_differently() {
        let mut kit = Kit::default();
        let tones = [
            DeskTone::Needs,
            DeskTone::Error,
            DeskTone::Finished,
            DeskTone::Working,
            DeskTone::Idle,
            DeskTone::Stopped,
        ];
        let mut seen = Vec::new();
        for tone in tones {
            let mut m = mascot("agent-3", None, false, Some(2), &mut kit);
            let rest = transforms(&m.rig);
            m.pose(tone, 1.3, false, None);
            let posed = transforms(&m.rig);
            assert!(
                posed
                    .iter()
                    .flatten()
                    .filter(|x| x.is_finite())
                    .all(|x| x.abs() < 100.)
            );
            let moved = rest.iter().zip(&posed).any(|(a, b)| {
                a.iter()
                    .zip(b)
                    .any(|(x, y)| (x.is_nan() != y.is_nan()) || (x - y).abs() > 1e-4)
            });
            assert!(moved, "{tone:?} left it as it was");
            assert!(
                !seen.contains(&format!("{posed:?}")),
                "{tone:?} posed like another"
            );
            seen.push(format!("{posed:?}"));
        }
    }

    #[test]
    fn motion_eases_and_looks() {
        let mut kit = Kit::default();
        let mut m = mascot("agent-4", Some("codex"), false, None, &mut kit);
        m.pose(DeskTone::Idle, 0., true, None);
        let a = transforms(&m.rig);
        for i in 1..=20 {
            m.pose(DeskTone::Needs, i as f32 * 0.05, true, Some((1.5, 2.)));
        }
        let b = transforms(&m.rig);
        assert_ne!(format!("{a:?}"), format!("{b:?}"));
        assert!(
            b.iter()
                .flatten()
                .filter(|x| x.is_finite())
                .all(|x| x.is_finite())
        );
    }

    #[test]
    fn the_walker_strides() {
        let mut kit = Kit::default();
        let mut w = walker(&mut kit);
        assert!(finite(&w.rig) > 5);
        for i in 0..40 {
            w.stride(0., i as f32 * 0.05, true);
        }
        let idle = transforms(&w.rig);
        for i in 40..80 {
            w.stride(2.6, i as f32 * 0.05, true);
        }
        let walking = transforms(&w.rig);
        assert_ne!(format!("{idle:?}"), format!("{walking:?}"));
        w.fade(0.1);
        assert!(!w.rig.world(Mat4::IDENTITY)[1].1);
    }

    #[test]
    fn the_piping_winds_outwards() {
        let g = piping(0.46, 0.42, 0.41, 0.16, 0.032);
        let mut bad = 0;
        let mut total = 0;
        for t in g.indices.chunks(3) {
            let [a, b, c] = [t[0], t[1], t[2]].map(|i| g.pos[i as usize]);
            let n = (b - a).cross(c - a);
            if n.length() < 1e-9 {
                continue;
            }
            let avg = (g.normal[t[0] as usize] + g.normal[t[1] as usize] + g.normal[t[2] as usize])
                .normalize_or_zero();
            total += 1;
            if n.normalize().dot(avg) <= 0. {
                bad += 1;
            }
        }
        assert!(total > 100 && bad == 0, "{bad} of {total} wind inwards");
    }
}
