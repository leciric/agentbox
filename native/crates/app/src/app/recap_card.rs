//! The weekly recap's share card (lib/recapCard.ts): the week drawn at
//! 1200×630 (a post's link card) or 1080×1080, as the Electron app draws it
//! on a canvas, here as an SVG rasterised by resvg (the renderer GPUI draws
//! its icons with) into the PNG that is shown, copied and saved. Project
//! names, and agents' task titles, which can name one, are left off unless
//! the user turns them on.
//!
//! Text is set in Inter where it is installed and the system's sans-serif
//! otherwise; fit() measures it with the same fonts, so nothing overruns.
use std::fmt::Write as _;
use std::sync::{Arc, OnceLock};

use agentbox_api::Recap;
use agentbox_i18n::{t, tv};
use anyhow::{Result, anyhow};
use resvg::{tiny_skia, usvg};

use super::recap::{compact, duration, model_label};

const FONT: &str = "Inter, 'Inter Variable', 'Segoe UI', 'Helvetica Neue', sans-serif";
const TEXT: &str = "#f5f3ff";
const MUTED: &str = "#a8a3c7";
const SUBTLE: &str = "#6f6a92";
const ACCENT: &str = "#c4b5fd";
const AWAY: &str = "#8b5cf6";
const WITH_YOU: &str = "#c4b5fd";
const PANEL: &str = "rgba(255,255,255,0.045)";
const EDGE: &str = "rgba(255,255,255,0.09)";

/// The card's options: its shape, and whether names are on it.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub struct Opts {
    pub square: bool,
    pub names: bool,
}

pub fn size(square: bool) -> (u32, u32) {
    if square { (1080, 1080) } else { (1200, 630) }
}

/// file_name is what a saved card is called (lib/recap.ts cardFileName).
pub fn file_name(r: &Recap, square: bool) -> String {
    let day = crate::dates::local(&r.end)
        .map(|e| {
            (e - chrono::Duration::hours(12))
                .format("%Y-%m-%d")
                .to_string()
        })
        .unwrap_or_default();
    format!(
        "agentbox-week-{day}-{}.png",
        if square { "square" } else { "landscape" }
    )
}

fn fonts() -> Arc<usvg::fontdb::Database> {
    static DB: OnceLock<Arc<usvg::fontdb::Database>> = OnceLock::new();
    DB.get_or_init(|| {
        let mut db = usvg::fontdb::Database::new();
        db.load_system_fonts();
        // sans-serif is Arial to fontdb, which few Linux systems have: make
        // it the first of these that is installed.
        let have = |name: &str| {
            db.faces()
                .any(|f| f.families.iter().any(|(n, _)| n.eq_ignore_ascii_case(name)))
        };
        if let Some(name) = [
            "Inter",
            "Inter Variable",
            "Segoe UI",
            "Helvetica Neue",
            "Noto Sans",
            "Cantarell",
            "DejaVu Sans",
            "Liberation Sans",
            "Arial",
        ]
        .into_iter()
        .find(|n| have(n))
        {
            db.set_sans_serif_family(name);
        }
        Arc::new(db)
    })
    .clone()
}

fn options() -> usvg::Options<'static> {
    usvg::Options {
        fontdb: fonts(),
        ..Default::default()
    }
}

fn esc(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
}

/// measure is how wide a line of text is set at a size and weight.
fn measure(s: &str, size: f32, weight: u32, spacing: f32) -> f32 {
    if s.is_empty() {
        return 0.;
    }
    let svg = format!(
        r#"<svg xmlns="http://www.w3.org/2000/svg" width="6000" height="600"><text x="0" y="300" font-family="{FONT}" font-size="{size}" font-weight="{weight}" letter-spacing="{spacing}">{}</text></svg>"#,
        esc(s)
    );
    usvg::Tree::from_str(&svg, &options())
        .map(|t| t.root().abs_bounding_box().width())
        .unwrap_or(s.chars().count() as f32 * size * 0.56)
}

/// fit is s cut to width w with an ellipsis.
fn fit(s: &str, w: f32, size: f32, weight: u32) -> String {
    if measure(s, size, weight, 0.) <= w {
        return s.to_string();
    }
    let chars: Vec<char> = s.chars().collect();
    let (mut lo, mut hi) = (0, chars.len());
    while lo < hi {
        let mid = (lo + hi).div_ceil(2);
        let cut: String = chars[..mid].iter().collect();
        if measure(&format!("{cut}…"), size, weight, 0.) <= w {
            lo = mid;
        } else {
            hi = mid - 1;
        }
    }
    let cut: String = chars[..lo].iter().collect();
    format!("{}…", cut.trim_end())
}

struct Card {
    out: String,
    ids: usize,
}

#[derive(Clone, Copy)]
struct Box_ {
    x: f32,
    y: f32,
    w: f32,
    h: f32,
}

impl Card {
    fn id(&mut self) -> String {
        self.ids += 1;
        format!("g{}", self.ids)
    }

    fn linear(&mut self, x1: f32, y1: f32, x2: f32, y2: f32, stops: &[(f32, &str)]) -> String {
        let id = self.id();
        let _ = write!(
            self.out,
            r#"<defs><linearGradient id="{id}" gradientUnits="userSpaceOnUse" x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}">"#
        );
        for (at, c) in stops {
            let _ = write!(self.out, r#"<stop offset="{at}" stop-color="{c}"/>"#);
        }
        self.out.push_str("</linearGradient></defs>");
        format!("url(#{id})")
    }

    fn glow(&mut self, w: f32, h: f32, x: f32, y: f32, r: f32, color: &str) {
        let id = self.id();
        let _ = write!(
            self.out,
            r#"<defs><radialGradient id="{id}" gradientUnits="userSpaceOnUse" cx="{x}" cy="{y}" r="{r}"><stop offset="0" stop-color="{color}"/><stop offset="1" stop-color="rgba(0,0,0,0)"/></radialGradient></defs><rect width="{w}" height="{h}" fill="url(#{id})"/>"#
        );
    }

    fn rounded(&mut self, b: Box_, r: f32, fill: &str, stroke: Option<&str>) {
        let _ = write!(
            self.out,
            r#"<rect x="{}" y="{}" width="{}" height="{}" rx="{r}" fill="{fill}"{}/>"#,
            b.x,
            b.y,
            b.w.max(0.),
            b.h.max(0.),
            stroke
                .map(|s| format!(r#" stroke="{s}" stroke-width="1""#))
                .unwrap_or_default()
        );
    }

    #[allow(clippy::too_many_arguments)]
    fn text(
        &mut self,
        s: &str,
        x: f32,
        y: f32,
        size: f32,
        weight: u32,
        fill: &str,
        anchor: &str,
        spacing: f32,
    ) {
        let _ = write!(
            self.out,
            r#"<text x="{x}" y="{y}" font-family="{FONT}" font-size="{size}" font-weight="{weight}" fill="{fill}" text-anchor="{anchor}" letter-spacing="{spacing}">{}</text>"#,
            esc(s)
        );
    }

    fn background(&mut self, w: f32, h: f32) {
        let base = self.linear(0., 0., w, h, &[(0., "#0d0b1f"), (1., "#130d2c")]);
        let _ = write!(
            self.out,
            r#"<rect width="{w}" height="{h}" fill="{base}"/>"#
        );
        let m = w.max(h);
        self.glow(w, h, w * 0.92, h * 0.02, m * 0.62, "rgba(124,58,237,0.42)");
        self.glow(w, h, w * 0.02, h, m * 0.55, "rgba(79,70,229,0.30)");
        self.glow(w, h, w * 0.48, h * 0.42, m * 0.35, "rgba(167,139,250,0.07)");
        // A faint dot grid, the texture of a machine room.
        let id = self.id();
        let _ = write!(
            self.out,
            r#"<defs><pattern id="{id}" x="18" y="18" width="28" height="28" patternUnits="userSpaceOnUse"><rect width="1.5" height="1.5" fill="rgba(255,255,255,0.045)"/></pattern></defs><rect width="{w}" height="{h}" fill="url(#{id})"/>"#
        );
    }

    /// logo is AgentBox's mark (build/icon.svg) at x, y, size across.
    fn logo(&mut self, x: f32, y: f32, size: f32) {
        let g = self.linear(
            x,
            y,
            x + size,
            y + size,
            &[(0., "#a78bfa"), (0.55, "#7c3aed"), (1., "#4f46e5")],
        );
        self.rounded(
            Box_ {
                x,
                y,
                w: size,
                h: size,
            },
            size * 0.24,
            &g,
            None,
        );
        let s = size * 0.62 / 24.;
        let _ = write!(
            self.out,
            r##"<g transform="translate({} {}) scale({s})" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z"/><path d="m3.3 7 8.7 5 8.7-5"/><path d="M12 22V12"/></g>"##,
            x + size * 0.19,
            y + size * 0.19
        );
    }

    fn header(&mut self, b: Box_, range: &str) {
        self.logo(b.x, b.y, b.h);
        self.text(
            "AgentBox",
            b.x + b.h + 14.,
            b.y + b.h * 0.68,
            b.h * 0.5,
            650,
            TEXT,
            "start",
            -0.3,
        );
        self.text(
            range,
            b.x + b.w,
            b.y + b.h * 0.66,
            b.h * 0.42,
            500,
            MUTED,
            "end",
            0.,
        );
    }

    fn hero(&mut self, b: Box_, r: &Recap, size: f32) {
        self.text(
            &t("recap.card.eyebrow").to_uppercase(),
            b.x,
            b.y + 16.,
            size * 0.135,
            700,
            ACCENT,
            "start",
            2.4,
        );
        let big = duration(r.work_seconds);
        let top = b.y + 30. + size * 0.86;
        let g = self.linear(
            b.x,
            top - size,
            b.x,
            top,
            &[(0., "#ffffff"), (1., "#c4b5fd")],
        );
        let spacing = -size * 0.035;
        self.text(
            &big,
            b.x - size * 0.04,
            top,
            size,
            800,
            &g,
            "start",
            spacing,
        );
        let after = b.x + measure(&big, size, 800, spacing) + size * 0.12;
        let label = fit(
            &t("recap.card.agentWork"),
            b.x + b.w - after,
            size * 0.26,
            600,
        );
        self.text(
            &label,
            after,
            top - size * 0.06,
            size * 0.26,
            600,
            TEXT,
            "start",
            0.,
        );
        let line = if r.work_seconds > 0 {
            let share = (r.away_seconds as f64 / r.work_seconds as f64 * 100.).round() as i64;
            tv(
                "recap.card.away",
                &[
                    ("hours", duration(r.away_seconds).into()),
                    ("share", share.into()),
                ],
            )
        } else {
            t("recap.card.quiet")
        };
        self.moon(b.x + size * 0.11, top + size * 0.365, size * 0.1);
        let line = fit(&line, b.w - size * 0.34, size * 0.2, 500);
        self.text(
            &line,
            b.x + size * 0.3,
            top + size * 0.43,
            size * 0.2,
            500,
            ACCENT,
            "start",
            0.,
        );
    }

    /// moon is a crescent: the work done while the user slept.
    fn moon(&mut self, cx: f32, cy: f32, r: f32) {
        let (ox, oy, or) = (cx + r * 0.55, cy - r * 0.35, r * 0.85);
        let _ = write!(
            self.out,
            r##"<path fill="#fde68a" fill-rule="evenodd" d="M{} {cy}a{r} {r} 0 1 0 {} 0a{r} {r} 0 1 0 {} 0ZM{} {oy}a{or} {or} 0 1 0 {} 0a{or} {or} 0 1 0 {} 0Z"/>"##,
            cx - r,
            2. * r,
            -2. * r,
            ox - or,
            2. * or,
            -2. * or
        );
    }

    fn tiles(&mut self, b: Box_, r: &Recap, names: bool) {
        let gap = 14.;
        let w = (b.w - gap * 2.) / 3.;
        let h = (b.h - gap) / 2.;
        let s = h / 100.;
        for (i, (label, value, sub)) in tile_list(r, names).into_iter().enumerate() {
            let x = b.x + (i % 3) as f32 * (w + gap);
            let y = b.y + (i / 3) as f32 * (h + gap);
            self.rounded(Box_ { x, y, w, h }, 18., PANEL, Some(EDGE));
            let inner = w - 36.;
            self.text(
                &fit(&label, inner, 14. * s, 600),
                x + 18.,
                y + 28. * s,
                14. * s,
                600,
                MUTED,
                "start",
                0.,
            );
            self.text(
                &fit(&value, inner, 32. * s, 750),
                x + 18.,
                y + 66. * s,
                32. * s,
                750,
                TEXT,
                "start",
                -0.5,
            );
            if !sub.is_empty() {
                self.text(
                    &fit(&sub, inner, 14. * s, 500),
                    x + 18.,
                    y + 88. * s,
                    14. * s,
                    500,
                    SUBTLE,
                    "start",
                    0.,
                );
            }
        }
    }

    /// days is each day's work as a bar: the part done while the user was
    /// away at its foot, the rest above it, a 2px gap between.
    fn days(&mut self, b: Box_, r: &Recap) {
        self.text(
            &t("recap.eachDay"),
            b.x,
            b.y + 16.,
            15.,
            650,
            TEXT,
            "start",
            0.,
        );
        self.legend(b.x + b.w, b.y + 16.);
        let top = b.y + 44.;
        let foot = b.y + b.h - 30.;
        let most = r
            .days
            .iter()
            .map(|d| d.work_seconds)
            .max()
            .unwrap_or(0)
            .max(1) as f32;
        let n = r.days.len().max(1) as f32;
        let slot = b.w / n;
        let bar = (slot * 0.58).min(44.);
        for (i, d) in r.days.iter().enumerate() {
            let x = b.x + i as f32 * slot + (slot - bar) / 2.;
            let full = (foot - top) * d.work_seconds as f32 / most;
            if d.work_seconds == 0 {
                self.rounded(
                    Box_ {
                        x,
                        y: foot - 4.,
                        w: bar,
                        h: 4.,
                    },
                    2.,
                    "rgba(255,255,255,0.10)",
                    None,
                );
            } else {
                let low = full * d.away_seconds as f32 / d.work_seconds as f32;
                let high = full - low;
                let id = self.id();
                let (y, rt, rb) = (foot - full, 6f32.min(full / 2.), 2f32.min(full / 2.));
                let _ = write!(
                    self.out,
                    r#"<defs><clipPath id="{id}"><path d="M{x} {}Q{x} {y} {} {y}H{}Q{} {y} {} {}V{}Q{} {foot} {} {foot}H{}Q{x} {foot} {x} {}Z"/></clipPath></defs><g clip-path="url(#{id})"><rect x="{x}" y="{}" width="{bar}" height="{low}" fill="{AWAY}"/><rect x="{x}" y="{y}" width="{bar}" height="{}" fill="{WITH_YOU}"/></g>"#,
                    y + rt,
                    x + rt,
                    x + bar - rt,
                    x + bar,
                    x + bar,
                    y + rt,
                    foot - rb,
                    x + bar,
                    x + bar - rb,
                    x + rb,
                    foot - rb,
                    foot - low,
                    (high - if low > 0. && high > 0. { 2. } else { 0. }).max(0.),
                );
            }
            let name = chrono::NaiveDate::parse_from_str(&d.date, "%Y-%m-%d")
                .map(|n| {
                    use chrono::Datelike as _;
                    crate::dates::weekday(n.weekday().num_days_from_sunday(), false)
                })
                .unwrap_or_default();
            let last = i + 1 == r.days.len();
            self.text(
                &name,
                x + bar / 2.,
                b.y + b.h - 6.,
                13.,
                500,
                if last { TEXT } else { SUBTLE },
                "middle",
                0.,
            );
        }
    }

    fn legend(&mut self, right: f32, y: f32) {
        let mut x = right;
        for (label, color) in [(t("recap.withYou"), WITH_YOU), (t("recap.whileAway"), AWAY)] {
            let w = measure(&label, 12., 500, 0.);
            self.text(&label, x, y, 12., 500, MUTED, "end", 0.);
            x -= w + 8.;
            self.rounded(
                Box_ {
                    x: x - 9.,
                    y: y - 9.,
                    w: 9.,
                    h: 9.,
                },
                2.,
                color,
                None,
            );
            x -= 9. + 16.;
        }
    }

    /// models is the week's tokens by model, the top three and the rest.
    fn models(&mut self, b: Box_, r: &Recap) {
        self.text(
            &t("recap.models"),
            b.x,
            b.y + 16.,
            15.,
            650,
            TEXT,
            "start",
            0.,
        );
        self.text(
            &tv("recap.tokens", &[("tokens", compact(r.tokens).into())]),
            b.x + b.w,
            b.y + 16.,
            13.,
            500,
            MUTED,
            "end",
            0.,
        );
        let name = |m: &str| {
            if m.is_empty() {
                t("vm.tokens.unnamedModel")
            } else {
                model_label(m)
            }
        };
        let mut rows: Vec<(String, i64)> = r
            .models
            .iter()
            .take(3)
            .map(|m| (name(&m.model), m.tokens))
            .collect();
        let rest: i64 = r.models.iter().skip(3).map(|m| m.tokens).sum();
        if rest > 0 {
            rows.push((t("recap.otherModels"), rest));
        }
        if rows.is_empty() {
            self.text(
                &t("recap.noTokens"),
                b.x,
                b.y + 56.,
                14.,
                500,
                SUBTLE,
                "start",
                0.,
            );
            return;
        }
        let most = rows[0].1.max(1) as f32;
        let step = 40f32.min((b.h - 44.) / rows.len() as f32);
        let g = self.linear(b.x, 0., b.x + b.w, 0., &[(0., "#7c3aed"), (1., "#a78bfa")]);
        for (i, (label, tokens)) in rows.iter().enumerate() {
            let y = b.y + 48. + i as f32 * step;
            self.text(
                &fit(label, b.w * 0.62, 14., 600),
                b.x,
                y,
                14.,
                600,
                TEXT,
                "start",
                0.,
            );
            self.text(&compact(*tokens), b.x + b.w, y, 14., 600, MUTED, "end", 0.);
            self.rounded(
                Box_ {
                    x: b.x,
                    y: y + 7.,
                    w: b.w,
                    h: 5.,
                },
                2.5,
                "rgba(255,255,255,0.07)",
                None,
            );
            let w = (b.w * *tokens as f32 / most).max(5.);
            self.rounded(
                Box_ {
                    x: b.x,
                    y: y + 7.,
                    w,
                    h: 5.,
                },
                2.5,
                &g,
                None,
            );
        }
    }
}

/// tile_list is the six numbers, names only when they're on.
fn tile_list(r: &Recap, names: bool) -> Vec<(String, String, String)> {
    let ai = |a: &str| match a {
        "claude" => "Claude Code".to_string(),
        "codex" => "Codex".to_string(),
        "opencode" => "OpenCode".to_string(),
        "cursor" => "Cursor".to_string(),
        "gemini" => "Gemini CLI".to_string(),
        _ => t("recap.oneAgent"),
    };
    let longest = r.longest.as_ref();
    let busiest = r.busiest.as_ref();
    vec![
        (
            t("recap.agents"),
            compact(r.agents),
            tv("recap.projects", &[("count", r.projects.into())]),
        ),
        (
            t("recap.prs"),
            compact(r.prs_opened),
            tv("recap.merged", &[("count", r.prs_merged.into())]),
        ),
        (
            t("recap.lines"),
            compact(r.insertions + r.deletions),
            format!("+{}  −{}", compact(r.insertions), compact(r.deletions)),
        ),
        (
            t("recap.streak"),
            tv("recap.days", &[("count", r.streak.into())]),
            tv("recap.best", &[("count", r.best_streak.into())]),
        ),
        (
            t("recap.card.longest"),
            longest.map_or("—".into(), |l| duration(l.work_seconds)),
            match longest {
                None => String::new(),
                Some(l) if names => format!(
                    "{} · {}",
                    l.project,
                    if l.title.is_empty() {
                        &l.agent
                    } else {
                        &l.title
                    }
                ),
                Some(l) => ai(&l.ai),
            },
        ),
        match busiest {
            Some(b) if names => (
                t("recap.busiest"),
                b.project.clone(),
                duration(b.work_seconds),
            ),
            _ => (
                t("recap.busiest"),
                match busiest {
                    Some(b) if r.work_seconds > 0 => format!(
                        "{}%",
                        (b.work_seconds as f64 / r.work_seconds as f64 * 100.).round()
                    ),
                    _ => "—".into(),
                },
                if busiest.is_some() {
                    t("recap.ofTheWork")
                } else {
                    String::new()
                },
            ),
        },
    ]
}

/// svg is the card as an SVG, `range` the week's dates.
pub fn svg(r: &Recap, o: Opts, range: &str) -> String {
    let (w, h) = size(o.square);
    let (w, h) = (w as f32, h as f32);
    let mut c = Card {
        out: format!(
            r#"<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}">"#
        ),
        ids: 0,
    };
    c.background(w, h);
    if !o.square {
        c.header(
            Box_ {
                x: 60.,
                y: 52.,
                w: w - 120.,
                h: 44.,
            },
            range,
        );
        c.hero(
            Box_ {
                x: 60.,
                y: 134.,
                w: 560.,
                h: 200.,
            },
            r,
            112.,
        );
        c.tiles(
            Box_ {
                x: 60.,
                y: 352.,
                w: 560.,
                h: 218.,
            },
            r,
            o.names,
        );
        let right = Box_ {
            x: 652.,
            y: 134.,
            w: 488.,
            h: 436.,
        };
        c.rounded(right, 24., PANEL, Some(EDGE));
        c.days(
            Box_ {
                x: right.x + 28.,
                y: right.y + 26.,
                w: right.w - 56.,
                h: 200.,
            },
            r,
        );
        c.models(
            Box_ {
                x: right.x + 28.,
                y: right.y + 252.,
                w: right.w - 56.,
                h: 160.,
            },
            r,
        );
    } else {
        c.header(
            Box_ {
                x: 72.,
                y: 64.,
                w: w - 144.,
                h: 48.,
            },
            range,
        );
        c.hero(
            Box_ {
                x: 72.,
                y: 168.,
                w: w - 144.,
                h: 230.,
            },
            r,
            140.,
        );
        c.tiles(
            Box_ {
                x: 72.,
                y: 418.,
                w: w - 144.,
                h: 246.,
            },
            r,
            o.names,
        );
        let bottom = Box_ {
            x: 72.,
            y: 690.,
            w: w - 144.,
            h: 318.,
        };
        c.rounded(bottom, 28., PANEL, Some(EDGE));
        let half = (bottom.w - 32. * 3.) / 2.;
        c.days(
            Box_ {
                x: bottom.x + 32.,
                y: bottom.y + 30.,
                w: half,
                h: bottom.h - 60.,
            },
            r,
        );
        c.models(
            Box_ {
                x: bottom.x + 64. + half,
                y: bottom.y + 30.,
                w: half,
                h: bottom.h - 60.,
            },
            r,
        );
    }
    c.out.push_str("</svg>");
    c.out
}

/// png is the card drawn as PNG bytes.
pub fn png(r: &Recap, o: Opts, range: &str) -> Result<Vec<u8>> {
    let tree = usvg::Tree::from_str(&svg(r, o, range), &options())?;
    let (w, h) = size(o.square);
    let mut pixmap = tiny_skia::Pixmap::new(w, h).ok_or_else(|| anyhow!("card size"))?;
    resvg::render(&tree, tiny_skia::Transform::default(), &mut pixmap.as_mut());
    Ok(pixmap.encode_png()?)
}

#[cfg(test)]
mod tests {
    use super::{Opts, png, svg};

    #[test]
    fn a_card_draws_in_both_shapes() {
        let mut r = agentbox_api::Recap {
            work_seconds: 36_000,
            away_seconds: 12_000,
            agents: 12,
            tokens: 4_200_000,
            ..Default::default()
        };
        r.days = (1..=7)
            .map(|d| agentbox_api::RecapDay {
                date: format!("2026-11-0{d}"),
                work_seconds: d * 1000,
                away_seconds: d * 300,
                ..Default::default()
            })
            .collect();
        for square in [false, true] {
            let o = Opts {
                square,
                names: false,
            };
            assert!(svg(&r, o, "Oct 30 – Nov 5").contains("AgentBox"));
            let bytes = png(&r, o, "Oct 30 – Nov 5").unwrap();
            assert_eq!(&bytes[1..4], b"PNG");
        }
    }
}
