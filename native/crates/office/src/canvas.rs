//! Textures drawn in code, as #668 draws them on 2D canvases (terrazzo,
//! planks, bricks, prints, monitors' screens, signs, the board): here an SVG
//! built up shape by shape and rasterised by resvg, the renderer GPUI draws
//! its icons with (and the recap card uses, recap_card.rs).

use crate::glb::Image;
use resvg::{tiny_skia, usvg};
use std::fmt::Write as _;
use std::sync::{Arc, OnceLock};

fn fonts() -> Arc<usvg::fontdb::Database> {
    static DB: OnceLock<Arc<usvg::fontdb::Database>> = OnceLock::new();
    DB.get_or_init(|| {
        let mut db = usvg::fontdb::Database::new();
        db.load_system_fonts();
        // sans-serif is Arial to fontdb, which few Linux systems have.
        let have = |name: &str| db.faces().any(|f| f.families.iter().any(|(n, _)| n.eq_ignore_ascii_case(name)));
        if let Some(name) = ["Inter", "Inter Variable", "Segoe UI", "Helvetica Neue", "Noto Sans", "Cantarell", "DejaVu Sans", "Liberation Sans", "Arial"].into_iter().find(|n| have(n)) {
            db.set_sans_serif_family(name);
        }
        Arc::new(db)
    })
    .clone()
}

pub fn esc(s: &str) -> String {
    s.replace('&', "&amp;").replace('<', "&lt;").replace('>', "&gt;").replace('"', "&quot;")
}

/// Text's alignment, as canvas's textAlign.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Align {
    Left,
    Center,
    Right,
}

/// Svg is a canvas: w×h pixels, y down, drawn in order.
pub struct Svg {
    pub w: u32,
    pub h: u32,
    defs: String,
    body: String,
    ids: usize,
}

impl Svg {
    pub fn new(w: u32, h: u32) -> Self {
        Self { w, h, defs: String::new(), body: String::new(), ids: 0 }
    }

    /// raw appends SVG as it is (a path, a group…).
    pub fn raw(&mut self, s: &str) -> &mut Self {
        self.body.push_str(s);
        self
    }

    pub fn rect(&mut self, x: f32, y: f32, w: f32, h: f32, fill: &str) -> &mut Self {
        let _ = write!(self.body, r#"<rect x="{x}" y="{y}" width="{w}" height="{h}" fill="{fill}"/>"#);
        self
    }

    /// round_rect is a rectangle with corners of radius r (canvas's roundRect).
    pub fn round_rect(&mut self, x: f32, y: f32, w: f32, h: f32, r: f32, fill: &str) -> &mut Self {
        let _ = write!(self.body, r#"<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" fill="{fill}"/>"#);
        self
    }

    pub fn stroke_rect(&mut self, x: f32, y: f32, w: f32, h: f32, r: f32, stroke: &str, width: f32) -> &mut Self {
        let _ = write!(self.body, r#"<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" fill="none" stroke="{stroke}" stroke-width="{width}"/>"#);
        self
    }

    pub fn circle(&mut self, cx: f32, cy: f32, r: f32, fill: &str) -> &mut Self {
        let _ = write!(self.body, r#"<circle cx="{cx}" cy="{cy}" r="{r}" fill="{fill}"/>"#);
        self
    }

    pub fn ellipse(&mut self, cx: f32, cy: f32, rx: f32, ry: f32, rot_deg: f32, fill: &str) -> &mut Self {
        let _ = write!(self.body, r#"<ellipse cx="{cx}" cy="{cy}" rx="{rx}" ry="{ry}" transform="rotate({rot_deg} {cx} {cy})" fill="{fill}"/>"#);
        self
    }

    pub fn line(&mut self, x0: f32, y0: f32, x1: f32, y1: f32, stroke: &str, width: f32) -> &mut Self {
        let _ = write!(self.body, r#"<line x1="{x0}" y1="{y0}" x2="{x1}" y2="{y1}" stroke="{stroke}" stroke-width="{width}" stroke-linecap="round"/>"#);
        self
    }

    /// path fills an SVG path (d) with a colour.
    pub fn path(&mut self, d: &str, fill: &str) -> &mut Self {
        let _ = write!(self.body, r#"<path d="{d}" fill="{fill}"/>"#);
        self
    }

    /// linear is a gradient from (x0,y0) to (x1,y1) through stops (offset
    /// 0..1, colour), to fill with: returns its url(#id).
    pub fn linear(&mut self, x0: f32, y0: f32, x1: f32, y1: f32, stops: &[(f32, &str)]) -> String {
        self.ids += 1;
        let id = format!("g{}", self.ids);
        let _ = write!(self.defs, r#"<linearGradient id="{id}" gradientUnits="userSpaceOnUse" x1="{x0}" y1="{y0}" x2="{x1}" y2="{y1}">"#);
        for (o, c) in stops {
            let _ = write!(self.defs, r#"<stop offset="{o}" stop-color="{c}"/>"#);
        }
        self.defs.push_str("</linearGradient>");
        format!("url(#{id})")
    }

    /// radial is a radial gradient about (cx,cy) of radius r.
    pub fn radial(&mut self, cx: f32, cy: f32, r: f32, stops: &[(f32, &str)]) -> String {
        self.ids += 1;
        let id = format!("g{}", self.ids);
        let _ = write!(self.defs, r#"<radialGradient id="{id}" gradientUnits="userSpaceOnUse" cx="{cx}" cy="{cy}" r="{r}">"#);
        for (o, c) in stops {
            let _ = write!(self.defs, r#"<stop offset="{o}" stop-color="{c}"/>"#);
        }
        self.defs.push_str("</radialGradient>");
        format!("url(#{id})")
    }

    /// text sets one line at (x, baseline y).
    pub fn text(&mut self, x: f32, y: f32, size: f32, weight: u32, fill: &str, align: Align, s: &str) -> &mut Self {
        let anchor = match align {
            Align::Left => "start",
            Align::Center => "middle",
            Align::Right => "end",
        };
        let _ = write!(self.body, r#"<text x="{x}" y="{y}" font-family="sans-serif" font-size="{size}" font-weight="{weight}" fill="{fill}" text-anchor="{anchor}">{}</text>"#, esc(s));
        self
    }

    pub fn source(&self) -> String {
        format!(r#"<svg xmlns="http://www.w3.org/2000/svg" width="{}" height="{}" viewBox="0 0 {} {}"><defs>{}</defs>{}</svg>"#, self.w, self.h, self.w, self.h, self.defs, self.body)
    }

    /// render rasterises it into RGBA8 (straight alpha, sRGB).
    pub fn render(&self) -> Image {
        let opts = usvg::Options { fontdb: fonts(), ..Default::default() };
        let mut pixmap = tiny_skia::Pixmap::new(self.w.max(1), self.h.max(1)).expect("pixmap");
        if let Ok(tree) = usvg::Tree::from_str(&self.source(), &opts) {
            resvg::render(&tree, tiny_skia::Transform::default(), &mut pixmap.as_mut());
        }
        // tiny-skia's pixels are premultiplied.
        let mut rgba = pixmap.take();
        for p in rgba.chunks_mut(4) {
            let a = p[3] as u32;
            if a > 0 && a < 255 {
                for c in &mut p[..3] {
                    *c = ((*c as u32 * 255 + a / 2) / a).min(255) as u8;
                }
            }
        }
        Image { width: self.w.max(1), height: self.h.max(1), rgba }
    }
}

/// measure is how wide a line of text is at a size and weight, in pixels.
pub fn measure(s: &str, size: f32, weight: u32) -> f32 {
    if s.is_empty() {
        return 0.;
    }
    let svg = format!(r#"<svg xmlns="http://www.w3.org/2000/svg" width="8000" height="400"><text x="0" y="200" font-family="sans-serif" font-size="{size}" font-weight="{weight}">{}</text></svg>"#, esc(s));
    let opts = usvg::Options { fontdb: fonts(), ..Default::default() };
    usvg::Tree::from_str(&svg, &opts).map(|t| t.root().abs_bounding_box().width()).unwrap_or(s.chars().count() as f32 * size * 0.55)
}

/// A small seeded random, for speckles and the like (stable frame to frame).
pub struct Rng(u32);

impl Rng {
    pub fn new(seed: u32) -> Self {
        Self(seed.max(1))
    }
    /// next is in [0, 1).
    pub fn next(&mut self) -> f32 {
        // mulberry32, as #668's rng.
        self.0 = self.0.wrapping_add(0x6d2b79f5);
        let mut t = self.0;
        t = (t ^ (t >> 15)).wrapping_mul(t | 1);
        t ^= t.wrapping_add((t ^ (t >> 7)).wrapping_mul(t | 61));
        ((t ^ (t >> 14)) as f64 / 4294967296.) as f32
    }
}
