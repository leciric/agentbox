//! The things the Office is furnished with besides desks (#668's props.ts;
//! layout.rs places them as Props): trees, standing tables, a printer and water
//! cooler, phone booths, bean bags, low shelves, rolling whiteboards, lockers,
//! the commons' kitchen and island, a ping-pong table, arcade cabinets, server
//! racks. Rounded and toy-like, in the diorama's palette; every part goes into
//! the diorama's Baker, so they cost a few draws, not one each.
//!
//! The kitchen, fridge and floor lamp are Kenney's pieces, which are always
//! bundled: props.ts's hand-built fallbacks for when they aren't loaded are not
//! ported.

use crate::canvas::{Align, Svg};
use crate::decor::{canvas_tex, m, stroke, textured};
use crate::kit::{Kit, Piece};
use crate::layout::{PROP_SCALE, Prop, PropKind};
use crate::mesh::{Baker, Geo, Mat, cyl, rbox, slab, sphere, torus, trs};
use glam::{Mat4, Vec3};
use std::f32::consts::{FRAC_PI_2, TAU};

const OAK: &str = "#e1a86d";
const CREAM: &str = "#f3ece1";
const STEEL: &str = "#cfc6ba";
const DARK: &str = "#2f3440";
const LEAVES: [&str; 3] = ["#4f9a52", "#6db35a", "#3f8a4a"];
const POTS: [&str; 4] = ["#e9dccb", "#d9825b", "#f4efe6", "#9fb8a8"];

fn ball(r: f32) -> Geo {
    sphere(r, 16, 12)
}

/// A prop's own frame: placed on the floor, turned, a little bigger than life.
struct P<'a> {
    b: &'a mut Baker,
    kit: &'a Kit,
    at: Mat4,
}

impl P<'_> {
    fn put(&mut self, g: Geo, mat: &Mat, x: f32, y: f32, z: f32) {
        self.b
            .put(&g, mat, self.at * trs(x, y, z, 0., 0., 0., Vec3::ONE));
    }
    fn putr(&mut self, g: Geo, mat: &Mat, x: f32, y: f32, z: f32, r: [f32; 3]) {
        self.b
            .put(&g, mat, self.at * trs(x, y, z, r[0], r[1], r[2], Vec3::ONE));
    }
    fn puts(&mut self, g: Geo, mat: &Mat, x: f32, y: f32, z: f32, s: [f32; 3]) {
        self.b
            .put(&g, mat, self.at * trs(x, y, z, 0., 0., 0., Vec3::from(s)));
    }
    /// A piece of Kenney's kit, placed in the prop's own frame.
    fn piece(&mut self, name: Piece, x: f32, y: f32, z: f32, rot: f32) -> bool {
        match self.kit.piece(name) {
            Some(q) => {
                self.b.put_model(
                    &q.model,
                    &[],
                    self.at * trs(x, y, z, 0., rot, 0., Vec3::ONE),
                );
                true
            }
            None => false,
        }
    }
    fn size(&self, name: Piece) -> Option<(f32, f32)> {
        self.kit.piece(name).map(|q| (q.w, q.h))
    }
    /// A little potted plant, for shelves and tables.
    fn sprig(&mut self, x: f32, y: f32, z: f32, i: usize) {
        self.put(cyl(0.07, 0.055, 0.12, 20), &m(POTS[i % 4]), x, y + 0.06, z);
        for j in 0..3usize {
            let dz = ((j * 7) % 3) as f32 - 1.;
            self.puts(
                ball(0.07 + (j % 2) as f32 * 0.02),
                &m(LEAVES[(i + j) % 3]),
                x + (j as f32 - 1.) * 0.05,
                y + 0.18 + (j % 2) as f32 * 0.05,
                z + dz * 0.03,
                [1., 0.85, 1.],
            );
        }
    }
}

/// furnish adds every prop to b.
pub fn furnish(b: &mut Baker, props: &[Prop], kit: &mut Kit) {
    let tex = textures(kit);
    let kit: &Kit = kit;
    for (i, p) in props.iter().enumerate() {
        let at = trs(
            p.x as f32,
            0.,
            p.z as f32,
            0.,
            p.rot as f32,
            0.,
            Vec3::splat(PROP_SCALE as f32),
        );
        build(
            &mut P {
                b: &mut *b,
                kit,
                at,
            },
            p.kind,
            &tex,
            i,
        );
    }
}

fn build(p: &mut P, kind: PropKind, t: &Tex, i: usize) {
    let glass = m("#bfe1ee").opacity(0.42);
    match kind {
        // A tall potted tree: a round pot, a slim trunk, a crown of soft leaves.
        PropKind::Tree => {
            p.put(cyl(0.3, 0.24, 0.5, 24), &m(POTS[i % 4]), 0., 0.25, 0.);
            p.put(cyl(0.27, 0.27, 0.02, 20), &m("#6b4a32"), 0., 0.5, 0.);
            p.put(cyl(0.035, 0.05, 1.1, 8), &m("#7a5236"), 0., 1.0, 0.);
            let crown = [
                [0., 1.75, 0., 0.42],
                [0.24, 1.5, 0.1, 0.3],
                [-0.22, 1.55, -0.08, 0.32],
                [0.05, 1.45, -0.24, 0.28],
                [-0.06, 2.05, 0.06, 0.26],
            ];
            for (j, [x, y, z, r]) in crown.iter().enumerate() {
                p.put(ball(*r), &m(LEAVES[(i + j) % 3]), *x, *y, *z);
            }
        }
        // A standing table with a laptop open on it and a coffee.
        PropKind::Standing => {
            p.put(rbox(1.4, 0.05, 0.72, 0.02), &m(OAK), 0., 1.05, 0.);
            for x in [-0.55, 0.55] {
                p.put(rbox(0.05, 1.02, 0.05, 0.015), &m(STEEL), x, 0.52, 0.);
                p.put(rbox(0.06, 0.03, 0.6, 0.015), &m(STEEL), x, 0.02, 0.);
            }
            let alu = m("#d5d9e0");
            p.put(rbox(0.36, 0.02, 0.25, 0.008), &alu, -0.2, 1.085, 0.06);
            let lid = trs(-0.2, 1.2, -0.07, -0.25, 0., 0., Vec3::ONE);
            p.b.put(&rbox(0.36, 0.24, 0.015, 0.008), &alu, p.at * lid);
            // The screen, a child of the lid.
            p.b.put(
                &crate::mesh::plane(0.32, 0.2),
                &textured(t.code, [1., 1.]).glow(),
                p.at * lid * trs(0., 0., 0.009, 0., 0., 0., Vec3::ONE),
            );
            p.put(cyl(0.04, 0.035, 0.1, 14), &m("#fbfaf7"), 0.35, 1.125, 0.1);
            p.sprig(0.5, 1.075, -0.18, 2);
        }
        // A printer on its cabinet, a water cooler beside it.
        PropKind::Printer => {
            p.put(
                rbox(0.66, 0.55, 0.55, 0.04),
                &m("#d9d4cc"),
                -0.45,
                0.275,
                0.,
            );
            p.put(rbox(0.6, 0.32, 0.5, 0.05), &m("#f2f0ec"), -0.45, 0.71, 0.);
            p.put(rbox(0.5, 0.03, 0.24, 0.01), &m("#ffffff"), -0.45, 0.88, 0.1);
            p.put(rbox(0.16, 0.05, 0.02, 0.01), &m(DARK), -0.32, 0.8, 0.255);
            p.put(ball(0.012), &m("#45e08a").glow(), -0.2, 0.8, 0.26);
            p.put(rbox(0.38, 0.95, 0.38, 0.06), &m("#f4f2ee"), 0.45, 0.475, 0.);
            p.put(
                cyl(0.15, 0.15, 0.42, 24),
                &m("#9ad0f0").opacity(0.75),
                0.45,
                1.16,
                0.,
            );
            p.put(
                rbox(0.12, 0.05, 0.05, 0.015),
                &m("#5aaef0"),
                0.45,
                0.72,
                0.2,
            );
            p.put(cyl(0.025, 0.02, 0.06, 10), &m("#ffffff"), 0.32, 0.98, 0.12);
        }
        // A low oak sideboard, plants and books on it.
        PropKind::Shelf => {
            p.put(rbox(1.8, 0.7, 0.42, 0.04), &m(OAK), 0., 0.37, 0.);
            for x in [-0.6, 0., 0.6] {
                p.put(rbox(0.55, 0.52, 0.02, 0.01), &m("#d39a62"), x, 0.38, 0.215);
            }
            for x in [-0.6, 0., 0.6] {
                p.put(rbox(0.12, 0.02, 0.02, 0.008), &m(DARK), x, 0.55, 0.23);
            }
            for x in [-0.8, 0.8] {
                p.put(cyl(0.02, 0.02, 0.04, 8), &m(DARK), x, 0.02, 0.15);
            }
            p.sprig(-0.6, 0.72, 0., i);
            p.sprig(0.62, 0.72, 0.02, i + 1);
            let books = ["#c4622d", "#2f5a4c", "#e9b44c", "#7c8bd6"];
            for bk in 0..4usize {
                p.put(
                    rbox(0.05, 0.24 + (bk % 2) as f32 * 0.04, 0.18, 0.008),
                    &m(books[(bk + i) % 4]),
                    -0.05 + bk as f32 * 0.06,
                    0.84 + (bk % 2) as f32 * 0.02,
                    0.,
                );
            }
            p.putr(
                rbox(0.26, 0.2, 0.02, 0.01),
                &m("#ffffff"),
                0.25,
                0.83,
                -0.1,
                [-0.15, 0., 0.],
            );
        }
        // A phone booth: felt walls, a glass door, a little ledge and a lamp.
        PropKind::Booth => {
            let felt = m("#a9c4a4");
            p.put(rbox(1.3, 0.08, 1.3, 0.03), &m("#e6dccd"), 0., 0.04, 0.);
            p.put(rbox(1.3, 2.2, 0.08, 0.03), &felt, 0., 1.14, -0.61);
            for x in [-0.61, 0.61] {
                p.put(rbox(0.08, 2.2, 1.3, 0.03), &felt, x, 1.14, 0.);
            }
            p.put(rbox(1.36, 0.1, 1.36, 0.04), &m("#8fb08a"), 0., 2.28, 0.);
            p.put(crate::mesh::plane(1.14, 2.1), &glass, 0., 1.12, 0.62);
            p.put(rbox(0.04, 2.15, 0.05, 0.015), &m(DARK), 0.55, 1.12, 0.62);
            p.put(rbox(0.04, 2.15, 0.05, 0.015), &m(DARK), -0.55, 1.12, 0.62);
            p.put(rbox(1.1, 0.04, 0.4, 0.015), &m(OAK), 0., 1.0, -0.38);
            p.put(
                cyl(0.11, 0.14, 0.12, 18),
                &m("#fff1d6").glow(),
                0.3,
                2.12,
                -0.2,
            );
        }
        // Bean bags round a little table, on a round rug.
        PropKind::Beanbags => {
            p.put(cyl(1.1, 1.1, 0.02, 40), &m("#f0d9bf"), 0., 0.032, 0.);
            let cols = ["#ffc94d", "#ff7aa2", "#45cbb8"];
            for j in 0..3usize {
                let a = (j as f32 / 3.) * TAU + 0.5;
                let c = m(cols[(j + i) % 3]);
                p.puts(
                    ball(0.45),
                    &c,
                    a.sin() * 0.7,
                    0.24,
                    a.cos() * 0.62,
                    [1., 0.58, 1.],
                );
                p.b.put(
                    &ball(0.3),
                    &c,
                    p.at * trs(
                        a.sin() * 0.95,
                        0.44,
                        a.cos() * 0.85,
                        0.,
                        a,
                        0.,
                        Vec3::new(1., 0.8, 0.8),
                    ),
                );
            }
            p.put(cyl(0.22, 0.22, 0.04, 24), &m(OAK), 0., 0.42, 0.);
            p.put(cyl(0.04, 0.06, 0.4, 10), &m(STEEL), 0., 0.2, 0.);
            p.sprig(0.05, 0.44, 0., i);
        }
        // A rolling whiteboard, mid-discussion.
        PropKind::Whiteboard => {
            let frame = m("#bfc4cc");
            p.put(
                rbox(1.56, 0.98, 0.04, 0.02),
                &textured(t.scribble, [1., 1.]),
                0.,
                1.35,
                0.,
            );
            p.put(rbox(1.62, 1.04, 0.03, 0.02), &frame, 0., 1.35, -0.02);
            for x in [-0.78, 0.78] {
                p.put(rbox(0.04, 1.75, 0.04, 0.012), &frame, x, 0.9, 0.);
                p.put(rbox(0.05, 0.04, 0.48, 0.012), &frame, x, 0.06, 0.);
                for z in [-0.2, 0.2] {
                    p.put(sphere(0.035, 16, 12), &m(DARK), x, 0.035, z);
                }
            }
            p.put(rbox(0.6, 0.03, 0.08, 0.01), &frame, 0., 0.84, 0.05);
            for (j, c) in ["#ef4444", "#3b82f6", "#22c55e"].iter().enumerate() {
                p.putr(
                    cyl(0.012, 0.012, 0.12, 8),
                    &m(c),
                    -0.12 + j as f32 * 0.1,
                    0.87,
                    0.05,
                    [0., 0., FRAC_PI_2],
                );
            }
        }
        // A row of lockers in pastels, against the wall.
        PropKind::Lockers => {
            let cols = ["#bfe3d6", "#f6d7a7", "#c9d3f5", "#f5c6d0"];
            for j in 0..4usize {
                let x = -0.75 + j as f32 * 0.5;
                p.put(
                    rbox(0.48, 1.8, 0.46, 0.04),
                    &m(cols[(j + i) % 4]),
                    x,
                    0.92,
                    0.,
                );
                for v in 0..3 {
                    p.put(
                        rbox(0.22, 0.015, 0.01, 0.005),
                        &m("#8d8a86"),
                        x,
                        1.6 - v as f32 * 0.04,
                        0.232,
                    );
                }
                p.put(rbox(0.03, 0.12, 0.03, 0.01), &m(STEEL), x + 0.16, 1.0, 0.24);
            }
            p.put(rbox(2.02, 0.06, 0.48, 0.02), &m("#e9e2d6"), 0., 0.03, 0.);
            p.sprig(-0.6, 1.82, 0., i);
            p.put(rbox(0.36, 0.22, 0.28, 0.04), &m("#c08b5c"), 0.45, 1.93, 0.);
        }
        // The kitchen counter, Kenney's: a drawer unit, the sink and a cabinet,
        // the microwave and coffee machine on top, wall cabinets above, tiles
        // behind.
        PropKind::Kitchen => {
            let (cw, ch) = p.size(Piece::KitchenCabinet).unwrap_or((0., 0.9));
            if cw > 0. {
                p.piece(Piece::KitchenCabinetDrawer, -cw, 0., 0., 0.);
                p.piece(Piece::KitchenSink, 0., 0., 0., 0.);
                p.piece(Piece::KitchenCabinet, cw, 0., 0., 0.);
                p.piece(Piece::KitchenMicrowave, -cw, ch, -0.05, 0.);
                p.piece(Piece::KitchenCoffeeMachine, cw - 0.1, ch, -0.05, 0.);
                p.piece(Piece::KitchenCabinetUpperDouble, -cw / 2., 1.55, -0.18, 0.);
                p.piece(Piece::KitchenCabinetUpperDouble, cw / 2., 1.55, -0.18, 0.);
                p.put(
                    rbox(cw * 3., 0.55, 0.02, 0.01),
                    &m("#d6e8e1"),
                    0.,
                    ch + 0.32,
                    -0.31,
                );
                p.put(
                    cyl(0.09, 0.11, 0.2, 18),
                    &m("#ff7aa2"),
                    cw * 0.55,
                    ch + 0.1,
                    0.05,
                );
            }
        }
        // A rounded retro fridge in mint, Kenney's.
        PropKind::Fridge => {
            p.piece(Piece::KitchenFridge, 0., 0., 0., 0.);
        }
        // The kitchen island and its three stools.
        PropKind::Island => {
            p.put(rbox(2.2, 0.9, 0.78, 0.04), &m("#9fb8a8"), 0., 0.45, -0.38);
            p.put(rbox(2.36, 0.05, 0.92, 0.02), &m(OAK), 0., 0.925, -0.32);
            let steel = m(STEEL);
            for j in 0..3usize {
                let x = -0.75 + j as f32 * 0.75;
                p.put(
                    cyl(0.19, 0.17, 0.07, 22),
                    &m(["#ffc94d", "#ff7aa2", "#45cbb8"][j]),
                    x,
                    0.7,
                    0.42,
                );
                p.put(cyl(0.025, 0.03, 0.66, 8), &steel, x, 0.34, 0.42);
                p.putr(
                    torus(0.13, 0.012, 6, 20, TAU),
                    &steel,
                    x,
                    0.26,
                    0.42,
                    [FRAC_PI_2, 0., 0.],
                );
                p.put(cyl(0.16, 0.18, 0.02, 20), &steel, x, 0.01, 0.42);
            }
            p.put(
                cyl(0.035, 0.03, 0.09, 12),
                &m("#fbfaf7"),
                -0.5,
                0.995,
                -0.15,
            );
            p.put(
                cyl(0.035, 0.03, 0.09, 12),
                &m("#ffc94d"),
                0.35,
                0.995,
                -0.25,
            );
            p.sprig(0.85, 0.95, -0.4, 1);
        }
        // A ping-pong table, its net, two paddles and a ball.
        PropKind::Pingpong => {
            p.put(rbox(2.74, 0.05, 1.525, 0.015), &m("#2f8f6a"), 0., 0.76, 0.);
            let line = m("#ffffff");
            for z in [-0.74, 0.74] {
                p.put(rbox(2.72, 0.006, 0.025, 0.002), &line, 0., 0.787, z);
            }
            for x in [-1.355, 1.355] {
                p.put(rbox(0.025, 0.006, 1.5, 0.002), &line, x, 0.787, 0.);
            }
            p.put(rbox(2.72, 0.006, 0.012, 0.002), &line, 0., 0.787, 0.);
            for x in [-1.15, 1.15] {
                for z in [-0.6, 0.6] {
                    p.put(rbox(0.06, 0.74, 0.06, 0.015), &m(DARK), x, 0.37, z);
                }
            }
            p.put(
                rbox(0.015, 0.15, 1.68, 0.005),
                &m("#ffffff").opacity(0.75),
                0.,
                0.86,
                0.,
            );
            for z in [-0.84, 0.84] {
                p.put(rbox(0.03, 0.17, 0.03, 0.01), &m(DARK), 0., 0.86, z);
            }
            p.put(cyl(0.08, 0.08, 0.012, 20), &m("#e04848"), -0.8, 0.792, 0.3);
            p.put(rbox(0.03, 0.012, 0.1, 0.005), &m(OAK), -0.8, 0.792, 0.42);
            p.put(cyl(0.08, 0.08, 0.012, 20), &m("#2b2f3a"), 0.9, 0.792, -0.35);
            p.put(rbox(0.03, 0.012, 0.1, 0.005), &m(OAK), 0.9, 0.792, -0.47);
            p.put(ball(0.022), &m("#ffffff"), 0.3, 0.81, 0.1);
        }
        // An arcade cabinet: a glowing game, a joystick and buttons, a lit marquee.
        PropKind::Arcade => {
            let body = m(if i % 2 == 1 { "#7c6cf0" } else { "#45cbb8" });
            p.put(rbox(0.68, 1.1, 0.62, 0.05), &body, 0., 0.55, -0.02);
            p.put(rbox(0.68, 0.72, 0.36, 0.05), &body, 0., 1.45, -0.15);
            p.putr(
                rbox(0.66, 0.08, 0.34, 0.03),
                &body,
                0.,
                1.12,
                0.12,
                [0.35, 0., 0.],
            );
            p.putr(
                crate::mesh::plane(0.5, 0.42),
                &textured(t.game, [1., 1.]).glow(),
                0.,
                1.45,
                0.031,
                [-0.12, 0., 0.],
            );
            p.put(rbox(0.68, 0.18, 0.4, 0.04), &m(DARK), 0., 1.9, -0.12);
            p.put(
                crate::mesh::plane(0.56, 0.12),
                &textured(t.marquee, [1., 1.]).glow(),
                0.,
                1.9,
                0.081,
            );
            p.put(cyl(0.012, 0.012, 0.1, 8), &m(DARK), -0.14, 1.2, 0.16);
            p.put(ball(0.035), &m("#e04848"), -0.14, 1.26, 0.16);
            for (j, c) in ["#ffc94d", "#5aaef0", "#ff7aa2"].iter().enumerate() {
                p.putr(
                    cyl(0.025, 0.025, 0.02, 12),
                    &m(c),
                    0.04 + j as f32 * 0.08,
                    1.17,
                    0.15,
                    [0.35, 0., 0.],
                );
            }
        }
        // Three potted plants of different heights on a low plinth.
        PropKind::Plants => {
            p.put(rbox(1.5, 0.12, 0.9, 0.04), &m("#e6dccd"), 0., 0.06, 0.);
            let pots = [
                (-0.45, 0.2, 0.42, 0.62),
                (0.15, 0.17, 0.3, 0.38),
                (0.5, 0.14, 0.55, 0.3),
            ];
            for (j, (x, r, h, lr)) in pots.iter().enumerate() {
                let zj = (((j * 5) % 3) as f32 - 1.) * 0.12;
                p.put(
                    cyl(*r, r * 0.82, *h, 22),
                    &m(POTS[(i + j) % 4]),
                    *x,
                    0.12 + h / 2.,
                    zj,
                );
                for q in 0..3usize {
                    let qf = q as f32;
                    p.put(
                        ball(lr * (0.7 + qf * 0.12)),
                        &m(LEAVES[(i + j + q) % 3]),
                        x + (qf - 1.) * lr * 0.35,
                        0.12 + h + lr * (0.6 + qf * 0.25),
                        zj + (q % 2) as f32 * 0.08,
                    );
                }
            }
        }
        // A fish tank on an oak cabinet: blue water, weed, a few bright fish.
        PropKind::Aquarium => {
            p.put(rbox(1.5, 0.72, 0.55, 0.04), &m(OAK), 0., 0.36, 0.);
            p.put(
                rbox(1.42, 0.62, 0.48, 0.02),
                &m("#8fd6f2").opacity(0.55),
                0.,
                1.05,
                0.,
            );
            p.put(rbox(1.46, 0.05, 0.52, 0.02), &m(DARK), 0., 1.38, 0.);
            p.put(rbox(1.36, 0.06, 0.42, 0.02), &m("#e8d9b8"), 0., 0.77, 0.);
            for j in 0..5usize {
                p.put(
                    cyl(0.015, 0.025, 0.3 + (j % 3) as f32 * 0.08, 6),
                    &m("#3f8a4a"),
                    -0.55 + j as f32 * 0.27,
                    0.95 + (j % 3) as f32 * 0.04,
                    -0.1 + (j % 2) as f32 * 0.12,
                );
            }
            for (j, c) in ["#ff9150", "#ffc94d", "#ff7aa2", "#5aaef0"]
                .iter()
                .enumerate()
            {
                p.puts(
                    ball(0.045),
                    &m(c),
                    -0.4 + j as f32 * 0.28,
                    0.98 + ((j * 7) % 3) as f32 * 0.1,
                    0.05 - (j % 2) as f32 * 0.1,
                    [1.5, 0.9, 0.7],
                );
            }
        }
        // A vending machine, lit, its rows of drinks behind glass.
        PropKind::Vending => {
            p.put(rbox(0.92, 1.9, 0.78, 0.06), &m("#ff7aa2"), 0., 0.95, 0.);
            p.put(
                crate::mesh::plane(0.56, 1.2),
                &textured(t.drinks, [1., 1.]).glow(),
                -0.1,
                1.15,
                0.395,
            );
            p.put(rbox(0.18, 0.5, 0.03, 0.02), &m(DARK), 0.3, 1.25, 0.39);
            p.put(rbox(0.5, 0.16, 0.03, 0.02), &m(DARK), -0.1, 0.32, 0.39);
            p.put(rbox(0.92, 0.16, 0.6, 0.05), &m("#ffffff"), 0., 1.98, 0.05);
        }
        // The AgentBox cube, a sculpture on a plinth: its gradient, white edges.
        PropKind::Logo => {
            p.put(rbox(0.8, 0.5, 0.8, 0.06), &m(CREAM), 0., 0.25, 0.);
            p.putr(
                rbox(0.6, 0.6, 0.6, 0.12),
                &textured(t.logo, [1., 1.]),
                0.,
                0.95,
                0.,
                [0.35, 0.65, 0.2],
            );
        }
        // A soft mat under a corner's things, in one of the room's pastels.
        PropKind::Mat => {
            let cols = [
                ["#f3dcc6", "#e6c3a3"],
                ["#dcebd8", "#bfd8ba"],
                ["#e6e0f3", "#cdc3e6"],
                ["#f6e3c4", "#e8c99b"],
                ["#d9ebef", "#b9d6de"],
            ][i % 5];
            p.put(slab(4.3, 2.4, 0.9, 0.016), &m(cols[1]), 0., 0., 0.);
            p.put(slab(4.1, 2.2, 0.8, 0.02), &m(cols[0]), 0., 0., 0.);
        }
        // A two-seat couch, soft and low, facing the room.
        PropKind::Couch => {
            let cols = ["#9cb89a", "#e9b44c", "#c9d3f5", "#f5c6d0"];
            let fab = m(cols[i % 4]);
            let cush = m("#fbf6ee");
            p.put(rbox(1.8, 0.34, 0.75, 0.12), &fab, 0., 0.2, 0.02);
            p.put(rbox(1.8, 0.5, 0.2, 0.09), &fab, 0., 0.52, -0.3);
            for x in [-0.82, 0.82] {
                p.put(rbox(0.18, 0.46, 0.75, 0.08), &fab, x, 0.36, 0.02);
            }
            for x in [-0.38, 0.38] {
                p.put(rbox(0.7, 0.12, 0.6, 0.06), &cush, x, 0.42, 0.06);
            }
            p.putr(
                rbox(0.34, 0.3, 0.1, 0.08),
                &m(["#ffc94d", "#ff7aa2", "#45cbb8"][i % 3]),
                0.45,
                0.62,
                -0.16,
                [-0.25, 0.2, 0.],
            );
        }
        // A low table: a mug and a book on it.
        PropKind::Lowtable => {
            p.put(rbox(0.9, 0.05, 0.5, 0.15), &m(OAK), 0., 0.38, 0.);
            for (x, z) in [(-0.36, -0.18), (0.36, -0.18), (-0.36, 0.18), (0.36, 0.18)] {
                p.put(cyl(0.02, 0.018, 0.36, 8), &m(DARK), x, 0.18, z);
            }
            p.put(cyl(0.035, 0.03, 0.08, 12), &m("#fbfaf7"), 0.2, 0.45, 0.05);
            p.putr(
                rbox(0.24, 0.035, 0.17, 0.01),
                &m(["#2f5a4c", "#c4622d", "#7c8bd6"][i % 3]),
                -0.18,
                0.425,
                -0.02,
                [0., 0.3, 0.],
            );
        }
        // A server rack, its lights green and blue.
        PropKind::Rack => {
            p.put(rbox(0.6, 1.5, 0.6, 0.05), &m("#1e242c"), 0., 0.75, 0.);
            for j in 0..6usize {
                p.put(
                    rbox(0.32, 0.03, 0.02, 0.01),
                    &m(if j % 3 == 2 { "#5ab8ff" } else { "#45e08a" }).glow(),
                    0.,
                    0.35 + j as f32 * 0.18,
                    0.31,
                );
            }
        }
    }
}

struct Tex {
    code: usize,
    scribble: usize,
    game: usize,
    marquee: usize,
    drinks: usize,
    logo: usize,
}

fn textures(k: &mut Kit) -> Tex {
    // A laptop's screen: lines of code.
    let code = canvas_tex(k, "prop:code", 128, 80, |c| {
        c.rect(0., 0., 128., 80., "#1f2433");
        let cols = ["#a78bfa", "#5aaef0", "#86c95e", "#ffc94d", "#e6e8ee"];
        for y in 0..9usize {
            c.rect(
                8. + (y % 3) as f32 * 8.,
                6. + y as f32 * 8.,
                30. + ((y * 37) % 60) as f32,
                4.,
                cols[(y * 3) % cols.len()],
            );
        }
    });
    // A whiteboard mid-discussion: boxes, arrows, sticky notes.
    let scribble = canvas_tex(k, "prop:scribble", 256, 160, |c| {
        c.rect(0., 0., 256., 160., "#ffffff");
        c.stroke_rect(20., 24., 60., 34., 0., "#3b82f6", 3.);
        c.stroke_rect(120., 24., 60., 34., 0., "#3b82f6", 3.);
        stroke(c, "M80,41 L118,41 M110,35 L118,41 L110,47", "#3b82f6", 3.);
        // An ellipse, stroked.
        c.raw(r##"<ellipse cx="150" cy="105" rx="40" ry="22" fill="none" stroke="#ef4444" stroke-width="3"/>"##);
        stroke(c, "M24,100 C50,80 70,130 100,100", "#22c55e", 3.);
        for (x, y, col) in [
            (200., 20., "#fde68a"),
            (222., 52., "#fbcfe8"),
            (200., 84., "#bbf7d0"),
        ] {
            c.rect(x, y, 28., 26., col);
        }
    });
    // An arcade game: a little maze with a hero and a ghost.
    let game = canvas_tex(k, "prop:game", 128, 108, |c| {
        c.rect(0., 0., 128., 108., "#120f2a");
        c.stroke_rect(8., 8., 112., 92., 0., "#5aaef0", 4.);
        c.stroke_rect(32., 30., 64., 48., 0., "#5aaef0", 4.);
        let mut x = 18.;
        while x < 116. {
            c.rect(x, 18., 3., 3., "#ffc94d");
            c.rect(x, 88., 3., 3., "#ffc94d");
            x += 10.;
        }
        // The hero: a pie with its mouth open to the right.
        let (a0, a1) = (0.6f32, TAU - 0.6);
        c.path(
            &format!(
                "M24,50 L{},{} A9,9 0 1 1 {},{} Z",
                24. + 9. * a0.cos(),
                50. + 9. * a0.sin(),
                24. + 9. * a1.cos(),
                50. + 9. * a1.sin()
            ),
            "#ffc94d",
        );
        // The ghost.
        c.path("M100,52 A8,8 0 0 1 116,52 L116,62 L100,62 Z", "#ff7aa2");
    });
    let marquee = canvas_tex(k, "prop:marquee", 128, 28, |c| {
        let g = c.linear(0., 0., 128., 0., &[(0., "#ff7aa2"), (1., "#ffc94d")]);
        c.rect(0., 0., 128., 28., &g);
        c.text(64., 21., 18., 800, "#1f1a3a", Align::Center, "BOXIE");
    });
    // A vending machine's window: rows of cans and bottles.
    let drinks = canvas_tex(k, "prop:drinks", 96, 200, |c| {
        c.rect(0., 0., 96., 200., "#f4f6fb");
        let cols = ["#ef4444", "#3b82f6", "#22c55e", "#f59e0b", "#a78bfa"];
        for r in 0..5usize {
            c.rect(0., 36. + r as f32 * 38., 96., 3., "#c9ccd1");
            for j in 0..4usize {
                c.round_rect(
                    8. + j as f32 * 21.,
                    10. + r as f32 * 38.,
                    14.,
                    26.,
                    4.,
                    cols[(r + j) % cols.len()],
                );
            }
        }
    });
    // The logo's face: its violet-to-indigo gradient, a white cube drawn on it.
    let logo = canvas_tex(k, "prop:logo", 128, 128, |c: &mut Svg| {
        let g = c.linear(
            0.,
            0.,
            128.,
            128.,
            &[(0., "#a78bfa"), (0.55, "#7c3aed"), (1., "#4f46e5")],
        );
        c.rect(0., 0., 128., 128., &g);
        stroke(
            c,
            "M64,26 L98,45 L98,84 L64,103 L30,84 L30,45 Z M30,45 L64,64 L98,45 M64,64 L64,103",
            "#ffffff",
            6.,
        );
    });
    Tex {
        code,
        scribble,
        game,
        marquee,
        drinks,
        logo,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::layout::{AreaIn, room_layout};

    const ALL: [PropKind; 21] = [
        PropKind::Tree,
        PropKind::Standing,
        PropKind::Printer,
        PropKind::Shelf,
        PropKind::Booth,
        PropKind::Beanbags,
        PropKind::Whiteboard,
        PropKind::Lockers,
        PropKind::Kitchen,
        PropKind::Fridge,
        PropKind::Island,
        PropKind::Pingpong,
        PropKind::Arcade,
        PropKind::Rack,
        PropKind::Plants,
        PropKind::Aquarium,
        PropKind::Vending,
        PropKind::Logo,
        PropKind::Mat,
        PropKind::Couch,
        PropKind::Lowtable,
    ];

    fn finite(b: &Baker) -> bool {
        b.batches.iter().all(|(_, _, m)| {
            m.vertices.iter().all(|v| {
                v.pos
                    .iter()
                    .chain(&v.normal)
                    .chain(&v.uv)
                    .chain(&v.color)
                    .all(|x| x.is_finite())
            }) && m.indices.iter().all(|&i| (i as usize) < m.vertices.len())
        })
    }

    #[test]
    fn every_kind_makes_geometry() {
        let mut kit = Kit::new();
        for kind in ALL {
            for rot in [0., 1.2] {
                let mut b = Baker::default();
                furnish(
                    &mut b,
                    &[Prop {
                        kind,
                        x: 3.,
                        z: 2.,
                        rot,
                    }],
                    &mut kit,
                );
                assert!(!b.is_empty(), "{kind:?} made nothing");
                assert!(finite(&b), "{kind:?} made non-finite geometry");
            }
        }
    }

    #[test]
    fn rooms_props_all_build() {
        let mut kit = Kit::new();
        for n in [1, 4, 9] {
            let l = room_layout(&[AreaIn {
                project: format!("p{n}"),
                desks: (0..n).map(|i| format!("d{i}")).collect(),
                no_lead: false,
                floor: None,
            }]);
            let mut b = Baker::default();
            furnish(&mut b, &l.props, &mut kit);
            assert!(
                !l.props.is_empty() && !b.is_empty() && finite(&b),
                "{n} desks"
            );
        }
    }
}
