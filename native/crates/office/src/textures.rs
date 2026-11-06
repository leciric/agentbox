//! What the Office paints on canvases (#668's textures.ts): the monitors, the
//! wall board, the name tags, bubbles and signs. The words come from the app,
//! translated; this only lays them out. Every function returns the image as a
//! canvas has it (top row first); `flip` makes one a texture for a plane.

use crate::canvas::{Align, Svg, esc, measure};
use crate::glb::Image;
use crate::model::DeskTone;
use std::fmt::Write as _;

/// Each tone's colour: the beacon, the monitor's bar, the board's dot.
pub fn tone_color(t: DeskTone) -> &'static str {
    match t {
        DeskTone::Needs => "#fbbf24",
        DeskTone::Error => "#f87171",
        DeskTone::Finished => "#34d399",
        DeskTone::Working => "#60a5fa",
        DeskTone::Idle => "#a5b4c8",
        DeskTone::Stopped => "#6b7280",
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub enum LineTone {
    Muted,
    Good,
    Bad,
    Warn,
    Code,
    #[default]
    Plain,
}

fn line_color(t: LineTone) -> &'static str {
    match t {
        LineTone::Muted => "#94a3b8",
        LineTone::Good => "#6ee7b7",
        LineTone::Bad => "#fca5a5",
        LineTone::Warn => "#fcd34d",
        LineTone::Code => "#c4b5fd",
        LineTone::Plain => "#e2e8f0",
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct ScreenLine {
    pub text: String,
    pub tone: LineTone,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Screen {
    pub tone: DeskTone,
    /// The bar across the top: the state in words.
    pub head: String,
    pub title: String,
    pub lines: Vec<ScreenLine>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct BoardEntry {
    pub r#ref: String,
    pub title: String,
    pub sub: String,
    pub tone: DeskTone,
    pub failing: bool,
}

/// A column lists what fits; the rest is "+n", the board's list has them all.
#[derive(Clone, Debug, PartialEq, Default)]
pub struct BoardColumn {
    pub head: String,
    pub empty: String,
    pub items: Vec<BoardEntry>,
}

/// Where draw_board put each row, in the canvas's pixels, for a click on it.
#[derive(Clone, Debug, PartialEq)]
pub struct BoardRow {
    pub r#ref: String,
    pub x: f32,
    pub y: f32,
    pub w: f32,
    pub h: f32,
}

#[derive(Clone, Debug, PartialEq, Default)]
pub struct BoardScreen {
    pub title: String,
    pub needs: BoardColumn,
    pub review: BoardColumn,
}

/// flip turns an image upside down: three's canvas textures have their top at
/// v = 1, the renderer samples v = 0 at the top.
pub fn flip(img: Image) -> Image {
    let row = img.width as usize * 4;
    let mut rgba = Vec::with_capacity(img.rgba.len());
    for r in (0..img.height as usize).rev() {
        rgba.extend_from_slice(&img.rgba[r * row..(r + 1) * row]);
    }
    Image { rgba, ..img }
}

// ---- Text, as a canvas's font sets it ----

#[derive(Clone, Copy)]
struct Ink {
    size: f32,
    weight: u32,
    mono: bool,
}

impl Ink {
    fn sans(weight: u32, size: f32) -> Self {
        Self {
            size,
            weight,
            mono: false,
        }
    }
    fn mono(weight: u32, size: f32) -> Self {
        Self {
            size,
            weight,
            mono: true,
        }
    }
    fn width(&self, s: &str) -> f32 {
        if self.mono {
            s.chars().count() as f32 * self.size * 0.6
        } else {
            measure(s, self.size, self.weight)
        }
    }
    /// fit cuts text with an ellipsis to fit width.
    fn fit(&self, text: &str, width: f32) -> String {
        if self.width(text) <= width {
            return text.to_string();
        }
        let chars: Vec<char> = text.chars().collect();
        // The longest prefix that fits with its ellipsis.
        let (mut lo, mut hi) = (1usize, chars.len());
        while lo < hi {
            let mid = (lo + hi).div_ceil(2);
            let s: String = chars[..mid].iter().collect();
            if self.width(&format!("{s}…")) <= width {
                lo = mid;
            } else {
                hi = mid - 1;
            }
        }
        let s: String = chars[..lo.min(chars.len())].iter().collect();
        format!("{}…", s.trim_end())
    }
    /// wrap breaks text into at most max lines that fit width, the last one
    /// cut with an ellipsis.
    fn wrap(&self, text: &str, width: f32, max: usize) -> Vec<String> {
        let words: Vec<&str> = text.split_whitespace().collect();
        let mut lines: Vec<String> = Vec::new();
        let mut cur = String::new();
        for w in &words {
            let next = if cur.is_empty() {
                w.to_string()
            } else {
                format!("{cur} {w}")
            };
            if self.width(&next) <= width || cur.is_empty() {
                cur = next;
            } else {
                lines.push(std::mem::take(&mut cur));
                cur = w.to_string();
                if lines.len() == max {
                    break;
                }
            }
        }
        if lines.len() < max && !cur.is_empty() {
            lines.push(cur);
        }
        if lines.len() == max
            && max > 0
            && words.join(" ").chars().count() > lines.join(" ").chars().count()
        {
            let last = format!("{}…", lines[max - 1]);
            lines[max - 1] = self.fit(&last, width);
        }
        lines.iter().map(|l| self.fit(l, width)).collect()
    }
    fn draw(&self, s: &mut Svg, x: f32, y: f32, fill: &str, align: Align, text: &str) {
        if self.mono {
            let anchor = match align {
                Align::Left => "start",
                Align::Center => "middle",
                Align::Right => "end",
            };
            s.raw(&format!(r#"<text x="{x}" y="{y}" font-family="monospace" font-size="{}" font-weight="{}" fill="{fill}" text-anchor="{anchor}">{}</text>"#, self.size, self.weight, esc(text)));
        } else {
            s.text(x, y, self.size, self.weight, fill, align, text);
        }
    }
}

/// wrap is the text in at most max lines that fit width, in a font.
pub fn wrap(text: &str, width: f32, max: usize, size: f32, weight: u32) -> Vec<String> {
    Ink::sans(weight, size).wrap(text, width, max)
}

fn rrect(s: &mut Svg, x: f32, y: f32, w: f32, h: f32, r: f32, fill: &str, alpha: f32) {
    s.raw(&format!(r#"<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" fill="{fill}" fill-opacity="{alpha}"/>"#));
}

/// css_hsl is a CSS hsl() colour (hue degrees, s and l in percent) as #rrggbb.
pub fn css_hsl(h: f32, s: f32, l: f32) -> String {
    let (h, s, l) = (h.rem_euclid(360.) / 360., s / 100., l / 100.);
    let c = crate::mesh::hsl(h, s, l);
    let b = |v: f32| (v.clamp(0., 1.) * 255.).round() as u8;
    format!("#{:02x}{:02x}{:02x}", b(c[0]), b(c[1]), b(c[2]))
}

// ---- The drawings ----

pub const SCREEN_W: u32 = 512;
pub const SCREEN_H: u32 = 320;

/// draw_screen paints a monitor: the state's bar, the task's title, and its
/// lines (branch, pull request, what it's doing). A stopped agent's is off.
/// t blinks a working agent's cursor.
pub fn draw_screen(sc: &Screen, t: f32) -> Image {
    let (w, h) = (SCREEN_W as f32, SCREEN_H as f32);
    let mut s = Svg::new(SCREEN_W, SCREEN_H);
    let off = sc.tone == DeskTone::Stopped;
    let g = s.linear(
        0.,
        0.,
        0.,
        h,
        &[
            (0., if off { "#0b0d12" } else { "#111827" }),
            (1., if off { "#07080b" } else { "#0b1220" }),
        ],
    );
    s.rect(0., 0., w, h, &g);
    if off {
        let i = Ink::sans(600, 34.);
        i.draw(
            &mut s,
            w / 2.,
            h / 2. + 6.,
            "#1f2937",
            Align::Center,
            &i.fit(&sc.head, w),
        );
        let i = Ink::sans(500, 26.);
        i.draw(
            &mut s,
            w / 2.,
            h / 2. + 46.,
            "#273244",
            Align::Center,
            &i.fit(&sc.title, w - 80.),
        );
        return s.render();
    }
    s.rect(0., 0., w, 54., tone_color(sc.tone));
    let i = Ink::sans(700, 30.);
    i.draw(
        &mut s,
        22.,
        38.,
        "#0b1220",
        Align::Left,
        &i.fit(&sc.head, w - 40.),
    );
    // A working agent's cursor blinks in the bar.
    if sc.tone == DeskTone::Working && (t * 2.).floor() as i64 % 2 == 0 {
        s.rect(w - 40., 16., 16., 24., "#0b1220");
    }
    let i = Ink::sans(700, 38.);
    let title = i.wrap(&sc.title, w - 40., 2);
    for (k, l) in title.iter().enumerate() {
        i.draw(
            &mut s,
            20.,
            102. + k as f32 * 44.,
            "#f8fafc",
            Align::Left,
            l,
        );
    }
    let mut y = 102. + title.len() as f32 * 44. + 12.;
    for line in &sc.lines {
        if y > h - 16. {
            break;
        }
        let i = if line.tone == LineTone::Code {
            Ink::mono(500, 26.)
        } else {
            Ink::sans(600, 29.)
        };
        let max = if matches!(line.tone, LineTone::Plain | LineTone::Warn) {
            3
        } else {
            1
        };
        for l in i.wrap(&line.text, w - 40., max) {
            if y > h - 12. {
                break;
            }
            i.draw(&mut s, 20., y, line_color(line.tone), Align::Left, &l);
            y += 35.;
        }
        y += 4.;
    }
    s.render()
}

/// draw_board paints the wall board, w by h: what needs you on the left, what
/// waits for your review on the right, as sticky notes on a whiteboard. It
/// also says where each row is, in pixels.
pub fn draw_board(b: &BoardScreen, w: u32, h: u32) -> (Image, Vec<BoardRow>) {
    let (wf, hf) = (w as f32, h as f32);
    let mut s = Svg::new(w, h);
    let mut rows: Vec<BoardRow> = Vec::new();
    s.rect(0., 0., wf, hf, "#fbfaf6");
    let i = Ink::sans(700, 34.);
    i.draw(&mut s, wf / 2., 46., "#2b3340", Align::Center, &b.title);
    let col_w = (wf - 90.) / 2.;
    let mut column =
        |s: &mut Svg, c: &BoardColumn, kind: &str, x: f32, tab: &str, note: &str, edge: &str| {
            s.round_rect(x, 70., col_w, 46., 12., tab);
            let i = Ink::sans(700, 26.);
            i.draw(
                s,
                x + 16.,
                101.,
                "#1f2630",
                Align::Left,
                &i.fit(&format!("{} · {}", c.head, c.items.len()), col_w - 30.),
            );
            let mut y = 130.;
            if c.items.is_empty() {
                let i = Ink::sans(500, 24.);
                for l in i.wrap(&c.empty, col_w - 20., 2) {
                    i.draw(s, x + 6., y + 34., "#8a8f98", Align::Left, &l);
                    y += 32.;
                }
                return;
            }
            let row_h = 64.;
            let fits = (((hf - y - 10.) / (row_h + 9.)).floor() as i64).max(1);
            let shown = if c.items.len() as i64 > fits {
                fits - 1
            } else {
                fits
            } as usize;
            for (k, it) in c.items.iter().take(shown).enumerate() {
                rows.push(BoardRow {
                    r#ref: it.r#ref.clone(),
                    x,
                    y,
                    w: col_w,
                    h: row_h,
                });
                // A sticky note, a little askew, with a soft shadow.
                let rot = ((k % 3) as f32 - 1.) * 0.008;
                s.raw(&format!(
                    r#"<g transform="translate({} {}) rotate({})">"#,
                    x + col_w / 2.,
                    y + row_h / 2.,
                    rot.to_degrees()
                ));
                rrect(
                    s,
                    -col_w / 2. + 3.,
                    -row_h / 2. + 4.,
                    col_w,
                    row_h,
                    8.,
                    "#3c2814",
                    0.12,
                );
                s.round_rect(-col_w / 2., -row_h / 2., col_w, row_h, 8., note);
                s.round_rect(
                    -col_w / 2.,
                    -row_h / 2.,
                    8.,
                    row_h,
                    4.,
                    if it.tone == DeskTone::Error || it.failing {
                        "#e0675f"
                    } else {
                        edge
                    },
                );
                s.raw("</g>");
                let i = Ink::sans(700, 24.);
                i.draw(
                    s,
                    x + 22.,
                    y + 28.,
                    "#26221d",
                    Align::Left,
                    &i.fit(&it.title, col_w - 36.),
                );
                let i = Ink::sans(500, 19.);
                i.draw(
                    s,
                    x + 22.,
                    y + 52.,
                    "#6b6258",
                    Align::Left,
                    &i.fit(&it.sub, col_w - 36.),
                );
                y += row_h + 9.;
            }
            if c.items.len() > shown {
                // The "+n" is a row too: a click on it goes to the full list.
                rows.push(BoardRow {
                    r#ref: format!("more:{kind}:{shown}"),
                    x,
                    y,
                    w: 120.,
                    h: 44.,
                });
                Ink::sans(700, 26.).draw(
                    s,
                    x + 8.,
                    y + 30.,
                    "#4a4f58",
                    Align::Left,
                    &format!("+{}", c.items.len() - shown),
                );
            }
        };
    column(
        &mut s, &b.needs, "needs", 30., "#ffd66b", "#fff3c2", "#f2b731",
    );
    column(
        &mut s,
        &b.review,
        "review",
        60. + col_w,
        "#a8e6c3",
        "#e2f7ea",
        "#3fbf7f",
    );
    (s.render(), rows)
}

/// draw_tag paints a name tag: the text on a dark pill with a coloured dot.
pub fn draw_tag(w: u32, h: u32, text: &str, dot: Option<&str>, big: bool) -> Image {
    let (wf, hf) = (w as f32, h as f32);
    let mut s = Svg::new(w, h);
    let i = Ink::sans(if big { 700 } else { 600 }, if big { 50. } else { 38. });
    let label = i.fit(text, wf - if dot.is_some() { 110. } else { 70. });
    let tw = i.width(&label);
    let pw = tw + if dot.is_some() { 92. } else { 56. };
    let x = (wf - pw) / 2.;
    rrect(
        &mut s,
        x,
        6.,
        pw,
        hf - 12.,
        (hf - 12.) / 2.,
        "#0a0e18",
        0.82,
    );
    let mut tx = x + 28.;
    if let Some(d) = dot {
        s.circle(x + 36., hf / 2., if big { 13. } else { 11. }, d);
        tx = x + 60.;
    }
    i.draw(
        &mut s,
        tx,
        hf / 2. + if big { 18. } else { 13. },
        "#f8fafc",
        Align::Left,
        &label,
    );
    s.render()
}

/// draw_sign paints an area's sign: its project's name in its colour.
pub fn draw_sign(w: u32, h: u32, text: &str, hue: f32) -> Image {
    let (wf, hf) = (w as f32, h as f32);
    let mut s = Svg::new(w, h);
    s.round_rect(4., 4., wf - 8., hf - 8., 26., &css_hsl(hue, 55., 22.));
    s.stroke_rect(7., 7., wf - 14., hf - 14., 23., &css_hsl(hue, 70., 62.), 6.);
    let i = Ink::sans(800, 64.);
    i.draw(
        &mut s,
        wf / 2.,
        hf / 2. + 22.,
        &css_hsl(hue, 90., 88.),
        Align::Center,
        &i.fit(text, wf - 60.),
    );
    s.render()
}

pub const BUBBLE_W: u32 = 128;
pub const BUBBLE_H: u32 = 150;

/// draw_bubble paints a speech bubble's sign: ! for a question, × an error,
/// ✓ finished, z asleep.
pub fn draw_bubble(glyph: &str, fill: &str) -> Image {
    let (w, h) = (BUBBLE_W as f32, BUBBLE_H as f32);
    let mut s = Svg::new(BUBBLE_W, BUBBLE_H);
    s.circle(w / 2., h / 2. - 8., w / 2. - 12., fill);
    s.path(
        &format!(
            "M{} {}L{} {}L{} {}Z",
            w / 2. - 16.,
            h - 40.,
            w / 2.,
            h - 6.,
            w / 2. + 16.,
            h - 40.
        ),
        fill,
    );
    Ink::sans(900, w * 0.55).draw(
        &mut s,
        w / 2.,
        h / 2. + w * 0.18,
        "#111827",
        Align::Center,
        glyph,
    );
    s.render()
}

/// The glyph and colour a bubble over a desk wears for its tone (bubbleOf).
pub fn bubble_of(t: DeskTone) -> Option<(&'static str, &'static str)> {
    match t {
        DeskTone::Needs => Some(("!", "#fbbf24")),
        DeskTone::Working => Some(("⋯", "#60a5fa")),
        DeskTone::Error => Some(("×", "#f87171")),
        DeskTone::Finished => Some(("✓", "#34d399")),
        DeskTone::Stopped => Some(("z", "#94a3b8")),
        DeskTone::Idle => None,
    }
}

/// draw_plate paints the front of a lead's counter: its word over its
/// project's name.
pub fn draw_plate(word: &str, label: &str) -> Image {
    let mut s = Svg::new(512, 160);
    s.raw(&format!(r##"<text x="256" y="46" font-family="sans-serif" font-size="26" font-weight="700" fill="#ffffff" fill-opacity="0.85" text-anchor="middle">{}</text>"##, esc(&word.to_uppercase())));
    let i = Ink::sans(800, 64.);
    let mut label: Vec<char> = label.chars().collect();
    while i.width(&label.iter().collect::<String>()) > 480. && label.len() > 3 {
        label.truncate(label.len() - 2);
        label.push('…');
    }
    i.draw(
        &mut s,
        256.,
        118.,
        "#ffffff",
        Align::Center,
        &label.iter().collect::<String>(),
    );
    s.render()
}

/// draw_contact is a soft dark blot, the shadow where things meet the floor.
pub fn draw_contact() -> Image {
    let mut s = Svg::new(128, 128);
    // Canvas's inner circle of radius 4 is the first stop's flat colour.
    let k = 4. / 62.;
    s.ids_radial_blot(64., 64., 62., k);
    s.render()
}

trait Blot {
    fn ids_radial_blot(&mut self, cx: f32, cy: f32, r: f32, k: f32);
}
impl Blot for Svg {
    fn ids_radial_blot(&mut self, cx: f32, cy: f32, r: f32, k: f32) {
        let stops = [
            (0., "#140e08", 0.55),
            (k, "#140e08", 0.55),
            (k + 0.55 * (1. - k), "#140e08", 0.28),
            (1., "#140e08", 0.),
        ];
        let mut st = String::new();
        for (o, c, a) in stops {
            let _ = write!(
                st,
                r#"<stop offset="{o}" stop-color="{c}" stop-opacity="{a}"/>"#
            );
        }
        self.raw(&format!(r##"<defs><radialGradient id="blot" gradientUnits="userSpaceOnUse" cx="{cx}" cy="{cy}" r="{r}">{st}</radialGradient></defs><rect width="128" height="128" fill="url(#blot)"/>"##));
    }
}

/// draw_mark is the white arrow over the agent whose card is up.
pub fn draw_mark() -> Image {
    let mut s = Svg::new(96, 96);
    s.raw(r##"<path d="M18 26L78 26L48 74Z" fill="#ffffff" stroke="#7c3aed" stroke-width="8" stroke-linejoin="round"/>"##);
    s.render()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sc(tone: DeskTone) -> Screen {
        Screen {
            tone,
            head: "Working".into(),
            title: "Add pagination to the tasks list and keep the cursor".into(),
            lines: vec![
                ScreenLine {
                    text: "agentbox/feat-pages".into(),
                    tone: LineTone::Code,
                },
                ScreenLine {
                    text: "Running the tests for the list view and its empty state now".into(),
                    tone: LineTone::Plain,
                },
            ],
        }
    }

    #[test]
    fn a_screen_is_512_by_320_and_a_stopped_one_is_dark() {
        let on = draw_screen(&sc(DeskTone::Working), 0.);
        assert_eq!(
            (on.width, on.height, on.rgba.len()),
            (512, 320, 512 * 320 * 4)
        );
        // The bar's colour at the top left.
        assert_eq!(&on.rgba[..3], &[0x60, 0xa5, 0xfa]);
        let off = draw_screen(&sc(DeskTone::Stopped), 0.);
        assert!(off.rgba[0] < 0x20);
    }

    fn entry(i: usize) -> BoardEntry {
        BoardEntry {
            r#ref: format!("p/agent-{i}"),
            title: format!("Task number {i}"),
            sub: "p · asks".into(),
            tone: DeskTone::Needs,
            failing: false,
        }
    }

    #[test]
    fn a_board_says_where_its_rows_are_and_folds_the_rest_into_a_count() {
        let b = BoardScreen {
            title: "Board".into(),
            needs: BoardColumn {
                head: "Needs you".into(),
                empty: "Nobody".into(),
                items: (0..9).map(entry).collect(),
            },
            review: BoardColumn {
                head: "Review".into(),
                empty: "Nothing to review yet".into(),
                items: vec![],
            },
        };
        let (img, rows) = draw_board(&b, 1024, 532);
        assert_eq!((img.width, img.height), (1024, 532));
        assert!(rows.iter().any(|r| r.r#ref.starts_with("more:needs:")));
        assert!(rows.iter().filter(|r| r.r#ref.starts_with("p/")).count() < 9);
        assert!(
            rows.iter()
                .all(|r| r.x >= 0. && r.x + r.w <= 1024. && r.y + r.h <= 532.)
        );
    }

    #[test]
    fn tags_bubbles_signs_and_the_blot_have_their_sizes_and_transparency() {
        let t = draw_tag(640, 96, "Fix the login", Some("#fbbf24"), false);
        assert_eq!((t.width, t.height), (640, 96));
        assert_eq!(t.rgba[3], 0);
        let b = draw_bubble("!", "#fbbf24");
        assert_eq!((b.width, b.height), (128, 150));
        let s = draw_sign(512, 160, "agentbox", 260.);
        assert_eq!(s.height, 160);
        let c = draw_contact();
        let mid = (64 * 128 + 64) * 4 + 3;
        assert!(
            c.rgba[mid] > 100 && c.rgba[3] == 0,
            "{} {}",
            c.rgba[mid],
            c.rgba[3]
        );
        let p = draw_plate(
            "Lead",
            "A very long project name that cannot possibly fit on the counter's front",
        );
        assert_eq!(p.width, 512);
    }

    #[test]
    fn long_text_is_cut_with_an_ellipsis() {
        let l = wrap(
            "one two three four five six seven eight nine ten eleven twelve",
            120.,
            2,
            20.,
            600,
        );
        assert_eq!(l.len(), 2);
        assert!(l[1].ends_with('…'));
    }
}
