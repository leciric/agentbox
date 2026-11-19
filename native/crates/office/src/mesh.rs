//! Building the Office's meshes in code, as #668 does with three.js: its
//! geometries (box, rounded box, cylinder, sphere, plane, a rounded slab), its
//! colours (sRGB hex, lit in linear), and baking — every static part of a room
//! merged into a mesh per texture, so a room is a few draws.

use crate::glb::Model;
use crate::gpu::{MeshData, Vertex};
use glam::{Mat3, Mat4, Quat, Vec2, Vec3};
use std::f32::consts::{FRAC_PI_2, PI, TAU};

/// A geometry: positions, normals and UVs (colour comes from its material).
#[derive(Clone, Debug, Default)]
pub struct Geo {
    pub pos: Vec<Vec3>,
    pub normal: Vec<Vec3>,
    pub uv: Vec<Vec2>,
    pub indices: Vec<u32>,
}

impl Geo {
    fn push(&mut self, p: Vec3, n: Vec3, uv: Vec2) -> u32 {
        self.pos.push(p);
        self.normal.push(n);
        self.uv.push(uv);
        self.pos.len() as u32 - 1
    }

    /// merge appends another geometry, moved by `t`.
    pub fn merge(&mut self, other: &Geo, t: Mat4) {
        let base = self.pos.len() as u32;
        let nm = Mat3::from_mat4(t).inverse().transpose();
        for i in 0..other.pos.len() {
            self.pos.push(t.transform_point3(other.pos[i]));
            self.normal.push((nm * other.normal[i]).normalize_or_zero());
            self.uv.push(other.uv[i]);
        }
        self.indices.extend(other.indices.iter().map(|i| i + base));
    }

    /// translated moves it (three's geometry.translate).
    pub fn translated(mut self, x: f32, y: f32, z: f32) -> Self {
        for p in &mut self.pos {
            *p += Vec3::new(x, y, z);
        }
        self
    }

    /// transformed applies a matrix to it.
    pub fn transformed(self, t: Mat4) -> Self {
        let mut g = Geo::default();
        g.merge(&self, t);
        g
    }

    pub fn bounds(&self) -> (Vec3, Vec3) {
        let mut lo = Vec3::splat(f32::MAX);
        let mut hi = Vec3::splat(f32::MIN);
        for p in &self.pos {
            lo = lo.min(*p);
            hi = hi.max(*p);
        }
        (lo, hi)
    }
}

/// cuboid is three's BoxGeometry: w×h×d about the origin.
pub fn cuboid(w: f32, h: f32, d: f32) -> Geo {
    rbox_n(w, h, d, 0., 0)
}

/// rbox is three's RoundedBoxGeometry(w, h, d, 3, r), its radius kept
/// within the box as #668's helper does.
pub fn rbox(w: f32, h: f32, d: f32, r: f32) -> Geo {
    let r = r
        .min(w / 2. - 1e-3)
        .min(h / 2. - 1e-3)
        .min(d / 2. - 1e-3)
        .max(0.);
    rbox_n(w, h, d, r, 3)
}

fn rbox_n(w: f32, h: f32, d: f32, r: f32, seg: usize) -> Geo {
    let half = Vec3::new(w / 2., h / 2., d / 2.);
    let inner = half - Vec3::splat(r);
    // Along an edge: the corner's arc, densely, then straight across.
    let axis = |hl: f32| -> Vec<f32> {
        if r <= 0. || seg == 0 {
            return vec![-hl, hl];
        }
        let mut v = Vec::new();
        for i in 0..=seg {
            let th = FRAC_PI_2 * (1. - i as f32 / seg as f32);
            v.push(-hl + r * (1. - th.sin()));
        }
        for i in (0..=seg).rev() {
            let th = FRAC_PI_2 * (1. - i as f32 / seg as f32);
            v.push(hl - r * (1. - th.sin()));
        }
        v
    };
    let mut g = Geo::default();
    // Each face: (normal axis, u axis, v axis, sign).
    let faces: [(usize, usize, usize, f32); 6] = [
        (0, 2, 1, 1.),
        (0, 2, 1, -1.),
        (1, 0, 2, 1.),
        (1, 0, 2, -1.),
        (2, 0, 1, 1.),
        (2, 0, 1, -1.),
    ];
    for (n, ua, va, s) in faces {
        let us = axis(half[ua]);
        let vs = axis(half[va]);
        let base = g.pos.len() as u32;
        for (j, &v) in vs.iter().enumerate() {
            for (i, &u) in us.iter().enumerate() {
                let mut p = Vec3::ZERO;
                p[n] = s * half[n];
                p[ua] = u;
                p[va] = v;
                let c = p.clamp(-inner, inner);
                let mut normal = (p - c).normalize_or_zero();
                if normal == Vec3::ZERO {
                    normal[n] = s;
                }
                let pos = if r > 0. { c + normal * r } else { p };
                if r <= 0. {
                    normal = Vec3::ZERO;
                    normal[n] = s;
                }
                let uv = Vec2::new(
                    i as f32 / (us.len() - 1) as f32,
                    j as f32 / (vs.len() - 1) as f32,
                );
                // Faces read from outside: flip u on the far sides.
                let uv = if (s < 0.) ^ (n == 1) {
                    Vec2::new(1. - uv.x, uv.y)
                } else {
                    uv
                };
                g.push(pos, normal, uv);
            }
        }
        let cols = us.len() as u32;
        for j in 0..vs.len() as u32 - 1 {
            for i in 0..cols - 1 {
                let a = base + j * cols + i;
                let b = a + 1;
                let c = a + cols;
                let dd = c + 1;
                // Wind outwards whichever way the face's axes go.
                let flip = {
                    let e1 = g.pos[b as usize] - g.pos[a as usize];
                    let e2 = g.pos[c as usize] - g.pos[a as usize];
                    let mut out = Vec3::ZERO;
                    out[n] = s;
                    e1.cross(e2).dot(out) < 0.
                };
                if flip {
                    g.indices.extend([a, c, b, b, c, dd]);
                } else {
                    g.indices.extend([a, b, c, b, dd, c]);
                }
            }
        }
    }
    g
}

/// cyl is three's CylinderGeometry(top, bottom, h, seg): about the origin,
/// up y, with caps (a cone when top is 0).
pub fn cyl(top: f32, bottom: f32, h: f32, seg: usize) -> Geo {
    let mut g = Geo::default();
    let slope = (bottom - top) / h;
    let base = g.pos.len() as u32;
    for i in 0..=seg {
        let a = i as f32 / seg as f32 * TAU;
        let (s, c) = a.sin_cos();
        let n = Vec3::new(s, slope, c).normalize();
        g.push(
            Vec3::new(top * s, h / 2., top * c),
            n,
            Vec2::new(i as f32 / seg as f32, 1.),
        );
        g.push(
            Vec3::new(bottom * s, -h / 2., bottom * c),
            n,
            Vec2::new(i as f32 / seg as f32, 0.),
        );
    }
    for i in 0..seg as u32 {
        let a = base + i * 2;
        g.indices.extend([a, a + 1, a + 2, a + 2, a + 1, a + 3]);
    }
    for (y, rr, up) in [(h / 2., top, 1.), (-h / 2., bottom, -1.)] {
        if rr <= 0. {
            continue;
        }
        let c = g.push(Vec3::new(0., y, 0.), Vec3::Y * up, Vec2::splat(0.5));
        for i in 0..=seg {
            let a = i as f32 / seg as f32 * TAU;
            let (s, co) = a.sin_cos();
            g.push(
                Vec3::new(rr * s, y, rr * co),
                Vec3::Y * up,
                Vec2::new(0.5 + s / 2., 0.5 + co / 2.),
            );
        }
        for i in 0..seg as u32 {
            if up > 0. {
                g.indices.extend([c, c + 1 + i, c + 2 + i]);
            } else {
                g.indices.extend([c, c + 2 + i, c + 1 + i]);
            }
        }
    }
    g
}

/// sphere is three's SphereGeometry(r, ws, hs).
pub fn sphere(r: f32, ws: usize, hs: usize) -> Geo {
    let mut g = Geo::default();
    for j in 0..=hs {
        let v = j as f32 / hs as f32;
        let th = v * PI;
        for i in 0..=ws {
            let u = i as f32 / ws as f32;
            let ph = u * TAU;
            let n = Vec3::new(-ph.cos() * th.sin(), th.cos(), ph.sin() * th.sin());
            g.push(n * r, n, Vec2::new(u, 1. - v));
        }
    }
    let row = ws as u32 + 1;
    for j in 0..hs as u32 {
        for i in 0..ws as u32 {
            let a = j * row + i;
            let b = a + row;
            if j != 0 {
                g.indices.extend([a, b, a + 1]);
            }
            if j != hs as u32 - 1 {
                g.indices.extend([a + 1, b, b + 1]);
            }
        }
    }
    g
}

/// plane is three's PlaneGeometry(w, h): in x and y, facing +z.
pub fn plane(w: f32, h: f32) -> Geo {
    let mut g = Geo::default();
    for (x, y, u, v) in [
        (-0.5, 0.5, 0., 1.),
        (0.5, 0.5, 1., 1.),
        (-0.5, -0.5, 0., 0.),
        (0.5, -0.5, 1., 0.),
    ] {
        g.push(Vec3::new(x * w, y * h, 0.), Vec3::Z, Vec2::new(u, v));
    }
    g.indices.extend([0, 2, 1, 2, 3, 1]);
    g
}

/// floor_plane is a plane lying on the floor, facing up.
pub fn floor_plane(w: f32, d: f32) -> Geo {
    plane(w, d).transformed(Mat4::from_rotation_x(-FRAC_PI_2))
}

/// slab is a rounded rectangle w×d (corners of radius r) extruded h up from
/// y = 0: a rug, a mat, a pill of floor.
pub fn slab(w: f32, d: f32, r: f32, h: f32) -> Geo {
    let rr = r.min(w / 2.).min(d / 2.).max(0.);
    let mut outline: Vec<Vec2> = Vec::new();
    let corners = [
        (w / 2. - rr, d / 2. - rr, 0.),
        (-w / 2. + rr, d / 2. - rr, FRAC_PI_2),
        (-w / 2. + rr, -d / 2. + rr, PI),
        (w / 2. - rr, -d / 2. + rr, 1.5 * PI),
    ];
    for (cx, cz, a0) in corners {
        let n = if rr > 0. { 6 } else { 0 };
        for i in 0..=n {
            let a = a0
                + if n > 0 {
                    i as f32 / n as f32 * FRAC_PI_2
                } else {
                    0.
                };
            outline.push(Vec2::new(cx + rr * a.cos(), cz + rr * a.sin()));
        }
    }
    let mut g = Geo::default();
    // Top and bottom: fans from the centre (the outline is convex).
    for (y, up) in [(h, 1.), (0., -1.)] {
        let c = g.push(Vec3::new(0., y, 0.), Vec3::Y * up, Vec2::splat(0.5));
        for p in &outline {
            g.push(
                Vec3::new(p.x, y, p.y),
                Vec3::Y * up,
                Vec2::new(0.5 + p.x / w, 0.5 + p.y / d),
            );
        }
        let n = outline.len() as u32;
        for i in 0..n {
            let a = c + 1 + i;
            let b = c + 1 + (i + 1) % n;
            if up > 0. {
                g.indices.extend([c, b, a]);
            } else {
                g.indices.extend([c, a, b]);
            }
        }
    }
    if h > 0. {
        let n = outline.len();
        for i in 0..n {
            let a = outline[i];
            let b = outline[(i + 1) % n];
            let e = (b - a).normalize_or_zero();
            let nn = Vec3::new(e.y, 0., -e.x);
            let base = g.pos.len() as u32;
            g.push(Vec3::new(a.x, 0., a.y), nn, Vec2::ZERO);
            g.push(Vec3::new(b.x, 0., b.y), nn, Vec2::X);
            g.push(Vec3::new(a.x, h, a.y), nn, Vec2::Y);
            g.push(Vec3::new(b.x, h, b.y), nn, Vec2::ONE);
            g.indices
                .extend([base, base + 2, base + 1, base + 1, base + 2, base + 3]);
        }
    }
    g
}

/// torus is three's TorusGeometry(r, tube, radial, tubular), in x and y.
pub fn torus(r: f32, tube: f32, radial: usize, tubular: usize, arc: f32) -> Geo {
    let mut g = Geo::default();
    for j in 0..=radial {
        for i in 0..=tubular {
            let u = i as f32 / tubular as f32 * arc;
            let v = j as f32 / radial as f32 * TAU;
            let c = Vec3::new(r * u.cos(), r * u.sin(), 0.);
            let p = Vec3::new(
                (r + tube * v.cos()) * u.cos(),
                (r + tube * v.cos()) * u.sin(),
                tube * v.sin(),
            );
            g.push(
                p,
                (p - c).normalize_or_zero(),
                Vec2::new(i as f32 / tubular as f32, j as f32 / radial as f32),
            );
        }
    }
    let row = tubular as u32 + 1;
    for j in 1..=radial as u32 {
        for i in 1..=tubular as u32 {
            let a = row * j + i - 1;
            let b = row * (j - 1) + i - 1;
            let c = row * (j - 1) + i;
            let d = row * j + i;
            g.indices.extend([a, b, d, b, c, d]);
        }
    }
    g
}

/// srgb turns a colour as written (#rrggbb, sRGB) into linear, as three's
/// colour management does.
pub fn hex(s: &str) -> [f32; 3] {
    let v = u32::from_str_radix(s.trim_start_matches('#'), 16).unwrap_or(0xff00ff);
    let c = |x: u32| {
        let f = x as f32 / 255.;
        if f <= 0.04045 {
            f / 12.92
        } else {
            ((f + 0.055) / 1.055).powf(2.4)
        }
    };
    [c(v >> 16 & 255), c(v >> 8 & 255), c(v & 255)]
}

/// hsl is three's Color.setHSL, h s l in 0..1: three reads them in its
/// working colour space, linear, so they stay as they are (unlike a hex).
pub fn hsl(h: f32, s: f32, l: f32) -> [f32; 3] {
    let h = h.rem_euclid(1.);
    let q = if l <= 0.5 {
        l * (1. + s)
    } else {
        l + s - l * s
    };
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

/// A material: a colour (linear), its opacity, a texture by its key in the
/// scene's textures, the UVs' repeat, and whether it's lit by itself.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Mat {
    pub color: [f32; 3],
    pub opacity: f32,
    pub texture: Option<usize>,
    pub repeat: [f32; 2],
    pub emissive: bool,
}

impl Mat {
    pub fn new(color: [f32; 3]) -> Self {
        Self {
            color,
            opacity: 1.,
            texture: None,
            repeat: [1., 1.],
            emissive: false,
        }
    }
    pub fn hex(s: &str) -> Self {
        Self::new(hex(s))
    }
    pub fn tex(mut self, t: usize, repeat: [f32; 2]) -> Self {
        self.texture = Some(t);
        self.repeat = repeat;
        self
    }
    pub fn opacity(mut self, o: f32) -> Self {
        self.opacity = o;
        self
    }
    pub fn glow(mut self) -> Self {
        self.emissive = true;
        self
    }
    /// The vertex colour the shader reads: alpha is opacity, plus 2 for
    /// something lit by itself.
    pub fn rgba(&self) -> [f32; 4] {
        [
            self.color[0],
            self.color[1],
            self.color[2],
            self.opacity + if self.emissive { 2. } else { 0. },
        ]
    }
}

/// trs is a transform as three's object: position, rotation (Euler XYZ, as
/// three's default order) and scale.
pub fn trs(x: f32, y: f32, z: f32, rx: f32, ry: f32, rz: f32, s: Vec3) -> Mat4 {
    Mat4::from_scale_rotation_translation(
        s,
        Quat::from_euler(glam::EulerRot::XYZ, rx, ry, rz),
        Vec3::new(x, y, z),
    )
}

/// at is a transform that only moves.
pub fn at(x: f32, y: f32, z: f32) -> Mat4 {
    Mat4::from_translation(Vec3::new(x, y, z))
}

/// Baker gathers parts into one mesh per texture (and its transparent parts
/// apart, drawn after), as #668's bake does.
#[derive(Default)]
pub struct Baker {
    pub batches: Vec<(Option<usize>, bool, MeshData)>,
}

impl Baker {
    fn batch(&mut self, texture: Option<usize>, clear: bool) -> &mut MeshData {
        let i = match self
            .batches
            .iter()
            .position(|(t, c, _)| *t == texture && *c == clear)
        {
            Some(i) => i,
            None => {
                self.batches.push((
                    texture,
                    clear,
                    MeshData {
                        texture,
                        transparent: clear,
                        ..Default::default()
                    },
                ));
                self.batches.len() - 1
            }
        };
        &mut self.batches[i].2
    }

    /// put adds a geometry in a material, moved by t.
    pub fn put(&mut self, g: &Geo, m: &Mat, t: Mat4) {
        let clear = m.opacity < 0.999;
        let out = self.batch(m.texture, clear);
        let base = out.vertices.len() as u32;
        let nm = Mat3::from_mat4(t).inverse().transpose();
        let rgba = m.rgba();
        for i in 0..g.pos.len() {
            out.vertices.push(Vertex {
                pos: t.transform_point3(g.pos[i]).into(),
                normal: (nm * g.normal[i]).normalize_or_zero().into(),
                uv: [g.uv[i].x * m.repeat[0], g.uv[i].y * m.repeat[1]],
                color: rgba,
            });
        }
        out.indices.extend(g.indices.iter().map(|i| i + base));
    }

    /// put_model adds a Kenney model; `textures` maps its images to the
    /// scene's texture keys.
    pub fn put_model(&mut self, model: &Model, textures: &[usize], t: Mat4) {
        let nm = Mat3::from_mat4(t).inverse().transpose();
        for m in &model.meshes {
            let tex = m.texture.map(|i| textures[i]);
            let out = self.batch(tex, false);
            let base = out.vertices.len() as u32;
            for v in &m.vertices {
                out.vertices.push(Vertex {
                    pos: t.transform_point3(Vec3::from(v.pos)).into(),
                    normal: (nm * Vec3::from(v.normal)).normalize_or_zero().into(),
                    ..*v
                });
            }
            out.indices.extend(m.indices.iter().map(|i| i + base));
        }
    }

    pub fn is_empty(&self) -> bool {
        self.batches.iter().all(|(_, _, m)| m.indices.is_empty())
    }

    pub fn finish(self) -> Vec<MeshData> {
        self.batches
            .into_iter()
            .map(|(_, _, m)| m)
            .filter(|m| !m.indices.is_empty())
            .collect()
    }
}

/// rbox_seg is three's RoundedBoxGeometry(w, h, d, segments, r) with its
/// own number of segments per corner (figures.rs's heads use eight).
pub fn rbox_seg(w: f32, h: f32, d: f32, segments: usize, r: f32) -> Geo {
    let r = r
        .min(w / 2. - 1e-3)
        .min(h / 2. - 1e-3)
        .min(d / 2. - 1e-3)
        .max(0.);
    rbox_n(w, h, d, r, segments)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_rounded_box_keeps_its_size() {
        let (lo, hi) = rbox(2., 1., 0.5, 0.1).bounds();
        assert!((hi - lo - Vec3::new(2., 1., 0.5)).abs().max_element() < 1e-4);
    }

    #[test]
    fn faces_wind_outwards() {
        for g in [
            cuboid(1., 2., 3.),
            rbox(1., 1., 1., 0.2),
            cyl(0.5, 0.5, 1., 12),
            sphere(1., 12, 8),
            slab(2., 1., 0.3, 0.1),
        ] {
            for t in g.indices.chunks(3) {
                let [a, b, c] = [t[0], t[1], t[2]].map(|i| g.pos[i as usize]);
                let n = (b - a).cross(c - a);
                if n.length() < 1e-7 {
                    continue;
                }
                let avg =
                    (g.normal[t[0] as usize] + g.normal[t[1] as usize] + g.normal[t[2] as usize])
                        .normalize_or_zero();
                assert!(n.normalize().dot(avg) > 0., "a face winds inwards");
            }
        }
    }

    #[test]
    fn colours_are_linear() {
        assert_eq!(hex("#ffffff"), [1., 1., 1.]);
        assert!((hex("#808080")[0] - 0.2158).abs() < 1e-3);
        let c = hsl(0., 1., 0.5);
        assert!((c[0] - 1.).abs() < 1e-5 && c[1].abs() < 1e-5);
    }
}
