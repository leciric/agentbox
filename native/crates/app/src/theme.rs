//! The app's design system, as desktop/src/renderer/styles.css writes it down
//! for the Electron app: every colour the app paints is a role here (text from
//! title to ghost, surfaces, lines, wells, the brand's ramp and the status
//! hues), each with its value in AgentBox's dark, light and Dracula looks, and
//! the type scale, radii, shadows and fonts beside them. Nothing outside this
//! module writes a colour down.
//!
//! Which look is worn is the daemon's appearance setting (GET /v1/theme),
//! applied by `apply`; a followed host theme or Dracula also brings an accent
//! (and a window colour), from which the brand's ramp is derived exactly as
//! lib/theme.ts's `palette` derives it. Every role function reads the palette
//! worn now, so a window refresh repaints in it.
// The palette is styles.css's whole: a role nothing draws in yet stays, so
// the next screen asks for it by name instead of writing a colour.
#![allow(dead_code)]

use std::sync::RwLock;

use gpui::{App, BoxShadow, Hsla, Pixels, SharedString, Window, point, px, rgba};

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Look {
    Dark,
    Light,
    Dracula,
}

/// Palette is one look's colours, each 0xRRGGBBAA. The names are styles.css's
/// roles without their `--ab-` prefix.
#[derive(Clone, Copy, PartialEq, Debug)]
pub struct Palette {
    pub look: Look,
    pub brand: [u32; 7], // 100 … 700
    pub on_brand: u32,
    pub ink: u32,
    pub wash_near: u32,
    pub wash_far: u32,
    pub selection: u32,
    pub brand_mid: u32,
    pub brand_deep: u32,
    pub brand_far: u32,

    pub title: u32,
    pub primary: u32,
    pub secondary: u32,
    pub tertiary: u32,
    pub muted: u32,
    pub subtle: u32,
    pub faint: u32,
    pub ghost: u32,

    pub surface_faint: u32,
    pub surface: u32,
    pub surface_raised: u32,
    pub surface_strong: u32,
    pub surface_vivid: u32,

    pub line_faint: u32,
    pub line: u32,
    pub line_strong: u32,
    pub line_vivid: u32,
    pub line_heavy: u32,

    pub rail: u32,
    pub sunken: u32,
    pub well: u32,
    pub overlay: u32,
    pub modal: u32,
    pub scrim: u32,
    pub gloss: u32,

    pub chat: u32,
    pub composer: u32,
    pub stage: u32,
    pub attached_warning: u32,
    pub attached_danger: u32,
    pub attached_neutral: u32,
    pub panel_top: u32,
    pub panel_bottom: u32,
    pub panel_edge: u32,
    pub shadow_deep: u32,
    pub shadow_soft: u32,

    pub inverted: u32,
    pub inverted_hover: u32,
    pub on_inverted: u32,
    pub on_bright: u32,

    pub emerald: [u32; 3], // 200, 300, 400
    pub amber: [u32; 5],   // 50, 100, 200, 300, 400
    pub rose: [u32; 4],    // 100 … 400
    pub sky: [u32; 4],     // 50, 200, 300, 400
    pub violet: [u32; 2],  // 300, 400
}

const fn op(rgb: u32) -> u32 {
    (rgb << 8) | 0xff
}
const fn a(rgb: u32, alpha: f32) -> u32 {
    (rgb << 8) | ((alpha * 255.0 + 0.5) as u32 & 0xff)
}

/// DARK is AgentBox's own look, and the default.
pub const DARK: Palette = Palette {
    look: Look::Dark,
    brand: [
        op(0xede9fe),
        op(0xddd6fe),
        op(0xc4b5fd),
        op(0xa78bfa),
        op(0x8455f0),
        op(0x7c3aed),
        op(0x6d28d9),
    ],
    on_brand: op(0xffffff),
    ink: op(0x07070b),
    wash_near: a(0x8b5cf6, 0.13),
    wash_far: a(0x38bdf8, 0.07),
    selection: a(0x8b5cf6, 0.35),
    brand_mid: op(0x6366f1),
    brand_deep: op(0x4f46e5),
    brand_far: op(0x38bdf8),

    title: op(0xfafafa),
    primary: op(0xf4f4f5),
    secondary: op(0xe4e4e7),
    tertiary: op(0xd4d4d8),
    muted: op(0xa1a1aa),
    subtle: op(0x8e8e97),
    faint: op(0x82828b),
    ghost: op(0x3f3f46),

    surface_faint: a(0xffffff, 0.03),
    surface: a(0xffffff, 0.05),
    surface_raised: a(0xffffff, 0.065),
    surface_strong: a(0xffffff, 0.09),
    surface_vivid: a(0xffffff, 0.14),

    line_faint: a(0xffffff, 0.05),
    line: a(0xffffff, 0.065),
    line_strong: a(0xffffff, 0.1),
    line_vivid: a(0xffffff, 0.15),
    line_heavy: a(0xffffff, 0.25),

    rail: a(0x000000, 0.25),
    sunken: a(0x000000, 0.3),
    well: a(0x000000, 0.42),
    overlay: a(0x18181b, 0.95),
    modal: a(0x09090b, 0.95),
    scrim: a(0x000000, 0.62),
    gloss: a(0xffffff, 0.25),

    chat: op(0x09090d),
    composer: op(0x131318),
    stage: op(0x0a0a0f),
    attached_warning: op(0x1d1810),
    attached_danger: op(0x1f1014),
    attached_neutral: op(0x111116),
    panel_top: a(0xffffff, 0.035),
    panel_bottom: a(0xffffff, 0.015),
    panel_edge: a(0xffffff, 0.04),
    shadow_deep: a(0x000000, 0.9),
    shadow_soft: a(0x000000, 0.4),

    inverted: op(0xf4f4f5),
    inverted_hover: op(0xffffff),
    on_inverted: op(0x18181b),
    on_bright: op(0x09090b),

    emerald: [op(0xa7f3d0), op(0x6ee7b7), op(0x34d399)],
    amber: [
        op(0xfffbeb),
        op(0xfef3c7),
        op(0xfde68a),
        op(0xfcd34d),
        op(0xfbbf24),
    ],
    rose: [op(0xffe4e6), op(0xfecdd3), op(0xfda4af), op(0xfb7185)],
    sky: [op(0xf0f9ff), op(0xbae6fd), op(0x7dd3fc), op(0x38bdf8)],
    violet: [op(0xc4b5fd), op(0xa78bfa)],
};

/// LIGHT is styles.css's light block: the text ramp turned over and every
/// status hue darkened to stay 4.5:1 on a pale window.
pub const LIGHT: Palette = Palette {
    look: Look::Light,
    brand: [
        op(0x4c1d95),
        op(0x5b21b6),
        op(0x6d28d9),
        op(0x7c3aed),
        op(0x8455f0),
        op(0x6d28d9),
        op(0x5b21b6),
    ],
    ink: op(0xf4f4f6),
    wash_near: a(0x8b5cf6, 0.10),
    wash_far: a(0x38bdf8, 0.08),
    selection: a(0x8b5cf6, 0.28),

    title: op(0x09090b),
    primary: op(0x18181b),
    secondary: op(0x27272a),
    tertiary: op(0x3f3f46),
    muted: op(0x52525b),
    subtle: op(0x5c5c65),
    faint: op(0x66666f),
    ghost: op(0xa1a1aa),

    surface_faint: a(0x09090b, 0.035),
    surface: a(0x09090b, 0.05),
    surface_raised: a(0x09090b, 0.07),
    surface_strong: a(0x09090b, 0.1),
    surface_vivid: a(0x09090b, 0.16),

    line_faint: a(0x09090b, 0.08),
    line: a(0x09090b, 0.11),
    line_strong: a(0x09090b, 0.16),
    line_vivid: a(0x09090b, 0.24),
    line_heavy: a(0x09090b, 0.38),

    rail: a(0xffffff, 0.58),
    sunken: a(0xffffff, 0.85),
    well: a(0xffffff, 0.7),
    overlay: a(0xffffff, 0.97),
    modal: a(0xffffff, 0.98),
    scrim: a(0x09090b, 0.4),
    gloss: a(0xffffff, 0.6),

    chat: op(0xfafafb),
    composer: op(0xffffff),
    attached_warning: op(0xfef6e7),
    attached_danger: op(0xfdeef0),
    attached_neutral: op(0xf4f4f6),
    panel_top: a(0xffffff, 0.85),
    panel_bottom: a(0xffffff, 0.6),
    panel_edge: a(0xffffff, 0.9),
    shadow_deep: a(0x09090b, 0.24),
    shadow_soft: a(0x09090b, 0.12),

    inverted: op(0x27272a),
    inverted_hover: op(0x18181b),
    on_inverted: op(0xfafafa),

    emerald: [op(0x065f46), op(0x047857), op(0x047857)],
    amber: [
        op(0x451a03),
        op(0x78350f),
        op(0x92400e),
        op(0xb45309),
        op(0xb45309),
    ],
    rose: [op(0x881337), op(0x9f1239), op(0xbe123c), op(0xbe123c)],
    sky: [op(0x082f49), op(0x075985), op(0x0369a1), op(0x0369a1)],
    violet: [op(0x6d28d9), op(0x7c3aed)],
    ..DARK
};

/// DRACULA is AgentBox's dark with Dracula's backgrounds, text and hues; its
/// accent and window come through `apply` like a followed theme's.
pub const DRACULA: Palette = Palette {
    look: Look::Dracula,
    chat: op(0x282a36),
    composer: op(0x343746),
    stage: op(0x21222c),
    attached_neutral: op(0x21222c),
    overlay: a(0x21222c, 0.97),
    modal: a(0x21222c, 0.97),

    title: op(0xffffff),
    primary: op(0xf8f8f2),
    secondary: op(0xecece6),
    tertiary: op(0xd8d8d2),
    muted: op(0xadb5cb),
    subtle: op(0x99a3cc),
    faint: op(0x8c97c2),
    ghost: op(0x44475a),

    inverted: op(0xf8f8f2),
    on_inverted: op(0x282a36),

    emerald: [op(0x8afba5), op(0x50fa7b), op(0x50fa7b)],
    amber: [
        op(0xfffbeb),
        op(0xfef3c7),
        op(0xf1fa8c),
        op(0xf1fa8c),
        op(0xffb86c),
    ],
    rose: [op(0xffe4e6), op(0xff9a9a), op(0xff6e6e), op(0xff5555)],
    sky: [op(0xf0f9ff), op(0xa4ffff), op(0x8be9fd), op(0x8be9fd)],
    violet: [op(0xd6acff), op(0xbd93f9)],
    ..DARK
};

/// Dracula's other hues, which its menus wear a hue per place.
pub mod dracula {
    pub const PURPLE: u32 = 0xbd93f9;
    pub const PINK: u32 = 0xff79c6;
    pub const GREEN: u32 = 0x50fa7b;
    pub const CYAN: u32 = 0x8be9fd;
    pub const ORANGE: u32 = 0xffb86c;
    pub const YELLOW: u32 = 0xf1fa8c;
    pub const RED: u32 = 0xff5555;
}

static WORN: RwLock<Palette> = RwLock::new(DARK);

fn worn() -> Palette {
    *WORN.read().unwrap_or_else(|e| e.into_inner())
}

fn c(v: u32) -> Hsla {
    rgba(v).into()
}

pub fn look() -> Look {
    worn().look
}

/// look_of is the look a theme status asks for, as lib/theme.ts's lookOf:
/// Dracula when picked, light when picked or when following a light theme,
/// dark otherwise — following with nothing to follow included.
pub fn look_of(theme: &agentbox_api::Theme) -> Look {
    match theme.appearance.as_str() {
        "dracula" => Look::Dracula,
        "light" => Look::Light,
        "follow" if theme.available && theme.mode == "light" => Look::Light,
        _ => Look::Dark,
    }
}

/// palette_of is the palette a theme status resolves to: the look's own, with
/// the brand's ramp, the window and the washes derived from the theme's accent
/// when it is worn (lib/theme.ts's palette, step for step).
pub fn palette_of(theme: &agentbox_api::Theme) -> Palette {
    let base = match look_of(theme) {
        Look::Dark => DARK,
        Look::Light => LIGHT,
        Look::Dracula => DRACULA,
    };
    let worn = theme.appearance == "dracula" || (theme.appearance == "follow" && theme.available);
    let Some(accent) = worn.then(|| parse_hex(&theme.accent)).flatten() else {
        return base;
    };
    let accent = Rgb::from(accent);
    let light = theme.mode == "light";
    let ground = parse_hex(&theme.background)
        .map(|b| Rgb::from(b).mix(if light { WHITE } else { BLACK }, 0.35));
    let behind = ground.unwrap_or(if light {
        Rgb(0xf4, 0xf4, 0xf6)
    } else {
        Rgb(0x07, 0x07, 0x0b)
    });
    let text = if light { BLACK } else { WHITE };
    let shade = |amount: f32| op(accent.mix(text, amount).readable(behind, text).hex());
    let on_white = WHITE.contrast(accent);
    Palette {
        brand: [
            shade(0.7),
            shade(0.5),
            shade(0.35),
            shade(0.18),
            op(accent.hex()),
            op(accent.mix(BLACK, 0.18).hex()),
            op(accent.mix(BLACK, 0.32).hex()),
        ],
        on_brand: if on_white >= 4.5 || on_white >= NEAR_BLACK.contrast(accent) {
            op(0xffffff)
        } else {
            op(NEAR_BLACK.hex())
        },
        ink: ground.map(|g| op(g.hex())).unwrap_or(base.ink),
        wash_near: a(accent.hex(), if light { 0.12 } else { 0.16 }),
        wash_far: a(
            accent.mix(if light { BLACK } else { WHITE }, 0.3).hex(),
            if light { 0.08 } else { 0.07 },
        ),
        selection: a(accent.hex(), if light { 0.28 } else { 0.35 }),
        brand_mid: op(accent.mix(BLACK, 0.28).hex()),
        brand_deep: op(accent.mix(BLACK, 0.42).hex()),
        brand_far: op(accent.mix(WHITE, 0.4).hex()),
        ..base
    }
}

/// apply wears what the daemon's theme status says, and reports whether
/// anything changed (so the caller refreshes the window and the widgets).
pub fn apply(theme: &agentbox_api::Theme) -> bool {
    let next = palette_of(theme);
    let mut w = WORN.write().unwrap_or_else(|e| e.into_inner());
    let changed = *w != next;
    *w = next;
    changed
}

fn parse_hex(s: &str) -> Option<u32> {
    let s = s.trim().trim_start_matches('#');
    (s.len() == 6).then(|| u32::from_str_radix(s, 16).ok())?
}

#[derive(Clone, Copy, PartialEq, Debug)]
struct Rgb(u8, u8, u8);
const WHITE: Rgb = Rgb(255, 255, 255);
const BLACK: Rgb = Rgb(0, 0, 0);
const NEAR_BLACK: Rgb = Rgb(9, 9, 11);

impl From<u32> for Rgb {
    fn from(v: u32) -> Self {
        Rgb((v >> 16) as u8, (v >> 8) as u8, v as u8)
    }
}

impl Rgb {
    fn hex(self) -> u32 {
        (self.0 as u32) << 16 | (self.1 as u32) << 8 | self.2 as u32
    }
    fn mix(self, to: Rgb, amount: f32) -> Rgb {
        let m = |f: u8, t: u8| {
            (f as f32 + (t as f32 - f as f32) * amount)
                .round()
                .clamp(0., 255.) as u8
        };
        Rgb(m(self.0, to.0), m(self.1, to.1), m(self.2, to.2))
    }
    fn luminance(self) -> f32 {
        let l = |v: u8| {
            let s = v as f32 / 255.;
            if s <= 0.03928 {
                s / 12.92
            } else {
                ((s + 0.055) / 1.055).powf(2.4)
            }
        };
        0.2126 * l(self.0) + 0.7152 * l(self.1) + 0.0722 * l(self.2)
    }
    fn contrast(self, other: Rgb) -> f32 {
        let (x, y) = (self.luminance(), other.luminance());
        (x.max(y) + 0.05) / (x.min(y) + 0.05)
    }
    fn readable(self, background: Rgb, toward: Rgb) -> Rgb {
        let mut amount = 0.;
        while amount < 1. {
            let next = self.mix(toward, amount);
            if next.contrast(background) >= 4.5 {
                return next;
            }
            amount += 0.05;
        }
        toward
    }
}

// ── Text, loudest to quietest ─────────────────────────────────────────────
pub fn title() -> Hsla {
    c(worn().title)
}
pub fn primary() -> Hsla {
    c(worn().primary)
}
pub fn secondary() -> Hsla {
    c(worn().secondary)
}
pub fn tertiary() -> Hsla {
    c(worn().tertiary)
}
pub fn muted() -> Hsla {
    c(worn().muted)
}
pub fn subtle() -> Hsla {
    c(worn().subtle)
}
pub fn faint() -> Hsla {
    c(worn().faint)
}
/// ghost is for what isn't read: a grip, a chevron, a divider's dash.
pub fn ghost() -> Hsla {
    c(worn().ghost)
}

// ── Surfaces and lines (translucent, over whatever is behind) ─────────────
pub fn surface_faint() -> Hsla {
    c(worn().surface_faint)
}
pub fn surface() -> Hsla {
    c(worn().surface)
}
pub fn surface_raised() -> Hsla {
    c(worn().surface_raised)
}
pub fn surface_strong() -> Hsla {
    c(worn().surface_strong)
}
pub fn surface_vivid() -> Hsla {
    c(worn().surface_vivid)
}
pub fn line_faint() -> Hsla {
    c(worn().line_faint)
}
pub fn line() -> Hsla {
    c(worn().line)
}
pub fn line_strong() -> Hsla {
    c(worn().line_strong)
}
pub fn line_vivid() -> Hsla {
    c(worn().line_vivid)
}
pub fn line_heavy() -> Hsla {
    c(worn().line_heavy)
}

// ── Grounds ───────────────────────────────────────────────────────────────
/// ink is the window itself, under its two washes.
pub fn ink() -> Hsla {
    c(worn().ink)
}
pub fn wash_near() -> Hsla {
    c(worn().wash_near)
}
pub fn wash_far() -> Hsla {
    c(worn().wash_far)
}
pub fn selection() -> Hsla {
    c(worn().selection)
}
pub fn rail() -> Hsla {
    c(worn().rail)
}
pub fn sunken() -> Hsla {
    c(worn().sunken)
}
pub fn well() -> Hsla {
    c(worn().well)
}
/// overlay is a menu's, a popover's or a toast's ground.
pub fn overlay() -> Hsla {
    c(worn().overlay)
}
/// modal is a dialog's ground.
pub fn modal() -> Hsla {
    c(worn().modal)
}
pub fn scrim() -> Hsla {
    c(worn().scrim)
}
pub fn gloss() -> Hsla {
    c(worn().gloss)
}
pub fn chat() -> Hsla {
    c(worn().chat)
}
pub fn composer() -> Hsla {
    c(worn().composer)
}
/// stage frames another machine's screen, and stays dark in every look.
pub fn stage() -> Hsla {
    c(worn().stage)
}
pub fn attached_warning() -> Hsla {
    c(worn().attached_warning)
}
pub fn attached_danger() -> Hsla {
    c(worn().attached_danger)
}
pub fn attached_neutral() -> Hsla {
    c(worn().attached_neutral)
}
pub fn panel_top() -> Hsla {
    c(worn().panel_top)
}
pub fn panel_bottom() -> Hsla {
    c(worn().panel_bottom)
}
/// card is an opaque raised ground: a dialog's, a popover's or a floating
/// panel's body, the overlay flattened onto the window.
pub fn card() -> Hsla {
    solid(overlay())
}
pub fn inverted() -> Hsla {
    c(worn().inverted)
}
pub fn inverted_hover() -> Hsla {
    c(worn().inverted_hover)
}
pub fn on_inverted() -> Hsla {
    c(worn().on_inverted)
}
pub fn on_bright() -> Hsla {
    c(worn().on_bright)
}

/// hex is a colour as #rrggbb, for what takes colours as strings (an SVG).
pub fn hex(color: Hsla) -> String {
    let c = color.to_rgb();
    let b = |v: f32| (v.clamp(0., 1.) * 255.).round() as u8;
    format!("#{:02x}{:02x}{:02x}", b(c.r), b(c.g), b(c.b))
}

/// paper and pitch are the literal ends, for what must not turn with the look:
/// a QR code's modules, a web page's or a recording's backdrop.
pub fn paper() -> Hsla {
    gpui::white()
}
pub fn pitch() -> Hsla {
    gpui::black()
}
/// on_media is text and marks drawn over a recording or a screenshot.
pub fn on_media() -> Hsla {
    gpui::white()
}
/// media_shade darkens a recording under controls or a caption laid on it.
pub fn media_shade(alpha: f32) -> Hsla {
    gpui::black().opacity(alpha)
}
/// merged is GitHub's merged purple, as a PR's chip wears it.
pub fn merged() -> Hsla {
    violet(400)
}
/// label_grey is a label GitHub sent no colour for.
pub fn label_grey() -> Hsla {
    c(op(0x8b949e))
}
/// hashed is a hue picked from a name (a skill's tag): a tint and its text.
pub fn hashed(hue: f32) -> (Hsla, Hsla) {
    if look() == Look::Light {
        (
            gpui::hsla(hue, 0.7, 0.5, 0.14),
            gpui::hsla(hue, 0.75, 0.32, 1.),
        )
    } else {
        (
            gpui::hsla(hue, 0.8, 0.6, 0.22),
            gpui::hsla(hue, 0.9, 0.78, 1.),
        )
    }
}

// ── The brand ─────────────────────────────────────────────────────────────
/// brand is a shade of the accent, 100 (palest, text on a brand tint) to 700
/// (a pressed brand button); 500 is a primary button's fill.
pub fn brand(shade: u16) -> Hsla {
    let i = (shade / 100).clamp(1, 7) as usize - 1;
    c(worn().brand[i])
}
pub fn on_brand() -> Hsla {
    c(worn().on_brand)
}
pub fn brand_mid() -> Hsla {
    c(worn().brand_mid)
}
pub fn brand_deep() -> Hsla {
    c(worn().brand_deep)
}
pub fn brand_far() -> Hsla {
    c(worn().brand_far)
}
/// tint is a colour at an alpha: a wash of a hue behind its own text.
pub fn tint(color: Hsla, alpha: f32) -> Hsla {
    color.opacity(alpha)
}
/// accent is the brand as text and dots: brand-400.
pub fn accent() -> Hsla {
    brand(400)
}
/// claude is Claude's own orange, for its usage meter.
pub fn claude() -> Hsla {
    c(op(0xd97757))
}
pub fn dracula_hue(rgb: u32) -> Hsla {
    c(op(rgb))
}

// ── Status hues (move with legibility, not with the theme) ────────────────
pub fn emerald(shade: u16) -> Hsla {
    let p = worn();
    c(match shade {
        200 => p.emerald[0],
        300 => p.emerald[1],
        _ => p.emerald[2],
    })
}
pub fn amber(shade: u16) -> Hsla {
    let p = worn();
    c(match shade {
        50 => p.amber[0],
        100 => p.amber[1],
        200 => p.amber[2],
        300 => p.amber[3],
        _ => p.amber[4],
    })
}
pub fn rose(shade: u16) -> Hsla {
    let p = worn();
    c(match shade {
        100 => p.rose[0],
        200 => p.rose[1],
        300 => p.rose[2],
        _ => p.rose[3],
    })
}
pub fn sky(shade: u16) -> Hsla {
    let p = worn();
    c(match shade {
        50 => p.sky[0],
        200 => p.sky[1],
        300 => p.sky[2],
        _ => p.sky[3],
    })
}
pub fn violet(shade: u16) -> Hsla {
    let p = worn();
    c(if shade == 300 {
        p.violet[0]
    } else {
        p.violet[1]
    })
}
/// destructive is a deleting confirm's fill and its hover and press: Tailwind's
/// rose 600, 700 and 800, as Electron's destructive button (white on it).
pub fn destructive(step: usize) -> Hsla {
    c(op([0xe11d48, 0xbe123c, 0x9f1239][step.min(2)]))
}
/// running is a working agent's green.
pub fn running() -> Hsla {
    emerald(400)
}
/// starting is amber: starting, paused, waiting on you.
pub fn starting() -> Hsla {
    amber(400)
}
/// error is rose: failed, missing, destructive.
pub fn error() -> Hsla {
    rose(400)
}

/// state_color is an agent's dot: running, paused, missing or asleep.
pub fn state_color(state: &str) -> Hsla {
    match state {
        "running" => running(),
        "paused" | "starting" => starting(),
        "missing" | "error" => error(),
        _ => faint(),
    }
}

/// linear_panel is Electron's `.panel` ground: a faint lift at its top
/// easing toward its bottom.
pub fn linear_panel() -> gpui::Background {
    gpui::linear_gradient(
        180.,
        gpui::linear_color_stop(panel_top(), 0.),
        gpui::linear_color_stop(panel_bottom(), 1.),
    )
}

// ── Elevation ─────────────────────────────────────────────────────────────
/// shadow_card is a resting card's lift: a hairline of light along its top
/// edge (the `.panel` inset) and a soft drop.
pub fn shadow_card() -> Vec<BoxShadow> {
    vec![BoxShadow {
        color: c(worn().shadow_soft).opacity(0.35),
        offset: point(px(0.), px(1.)),
        blur_radius: px(2.),
        spread_radius: px(0.),
        inset: false,
    }]
}
/// shadow_pop is a menu's or popover's: close and soft.
pub fn shadow_pop() -> Vec<BoxShadow> {
    vec![
        BoxShadow {
            color: c(worn().shadow_soft),
            offset: point(px(0.), px(8.)),
            blur_radius: px(24.),
            spread_radius: px(-4.),
            inset: false,
        },
        BoxShadow {
            color: c(worn().shadow_soft).opacity(0.5),
            offset: point(px(0.), px(1.)),
            blur_radius: px(3.),
            spread_radius: px(0.),
            inset: false,
        },
    ]
}
/// shadow_composer is the composer's: a long soft drop under it, and the
/// brand's ring around it while it has the focus.
pub fn shadow_composer(focused: bool) -> Vec<BoxShadow> {
    let mut v = vec![BoxShadow {
        color: c(worn().shadow_deep),
        offset: point(px(0.), px(24.)),
        blur_radius: px(60.),
        spread_radius: px(-28.),
        inset: false,
    }];
    if focused {
        v.push(BoxShadow {
            color: brand(400).opacity(0.6),
            offset: point(px(0.), px(0.)),
            blur_radius: px(0.),
            spread_radius: px(2.),
            inset: false,
        });
    }
    v
}
/// shadow_drawer is a drawer's, cast over what it slides across:
/// Electron's -20px 0 60px -20px shadow-deep.
pub fn shadow_drawer() -> Vec<BoxShadow> {
    vec![BoxShadow {
        color: c(worn().shadow_deep),
        offset: point(px(-20.), px(0.)),
        blur_radius: px(60.),
        spread_radius: px(-20.),
        inset: false,
    }]
}
/// shadow_modal is a dialog's: deep, so it reads above the scrim.
pub fn shadow_modal() -> Vec<BoxShadow> {
    vec![BoxShadow {
        color: c(worn().shadow_deep).opacity(0.6),
        offset: point(px(0.), px(24.)),
        blur_radius: px(64.),
        spread_radius: px(-12.),
        inset: false,
    }]
}
/// glow is a ring of a hue around something that wants a look: the focus
/// ring's colour at a soft spread.
pub fn glow(color: Hsla, strength: f32) -> Vec<BoxShadow> {
    vec![BoxShadow {
        color: color.opacity(0.45 * strength),
        offset: point(px(0.), px(0.)),
        blur_radius: px(12. * strength),
        spread_radius: px(1.),
        inset: false,
    }]
}

/// kbd is a keyboard shortcut's chip, as Electron's <kbd>.
pub fn kbd(keys: impl Into<SharedString>) -> gpui::Div {
    use gpui::{ParentElement, Styled, div};
    div()
        .flex_none()
        .px_1p5()
        .rounded(radius::MD)
        .bg(surface_raised())
        .font_family(MONO_FACE)
        .text_size(px(10.5))
        .text_color(subtle())
        .child(keys.into())
}

/// logo is AgentBox's mark, as Electron's Logo: a box on the brand's
/// gradient, with a gloss across its top and a glow of the brand beneath.
pub fn logo(size: Pixels) -> gpui::Div {
    use gpui::{ParentElement, Styled, div, linear_color_stop, linear_gradient};
    let r = size * (10. / 32.);
    div()
        .relative()
        .flex_none()
        .size(size)
        .rounded(r)
        .flex()
        .items_center()
        .justify_center()
        .bg(linear_gradient(
            135.,
            linear_color_stop(brand(400), 0.),
            linear_color_stop(brand_far(), 1.),
        ))
        .shadow(vec![BoxShadow {
            color: brand(500).opacity(0.7),
            offset: point(px(0.), px(8.)),
            blur_radius: px(24.),
            spread_radius: px(-8.),
            inset: false,
        }])
        .child(
            div()
                .absolute()
                .inset(px(1.))
                .rounded(r - px(1.))
                .bg(linear_gradient(
                    180.,
                    linear_color_stop(gloss(), 0.),
                    linear_color_stop(gpui::transparent_white(), 0.6),
                )),
        )
        .child(
            crate::icons::ic("box")
                .size(size * (18. / 32.))
                .text_color(gpui::white()),
        )
}

// ── Radii and type ────────────────────────────────────────────────────────
/// The radii Electron's Tailwind uses: rounded (4), -md (6), -lg (8), -xl (12),
/// -2xl (16).
pub mod radius {
    use gpui::{Pixels, px};
    pub const SM: Pixels = px(4.);
    pub const MD: Pixels = px(6.);
    pub const LG: Pixels = px(8.);
    pub const XL: Pixels = px(12.);
    pub const XXL: Pixels = px(16.);
}

/// The type scale, five sizes and nothing between: caption (labels,
/// timestamps, badges), ui (dense controls), body (reading text, inputs),
/// heading (a panel's or dialog's title), display (a page's title).
pub mod text {
    use gpui::{Pixels, px};
    pub const CAPTION: Pixels = px(12.);
    pub const UI: Pixels = px(13.);
    pub const BODY: Pixels = px(14.);
    pub const HEADING: Pixels = px(16.);
    pub const DISPLAY: Pixels = px(20.);
}

// ── Fonts ─────────────────────────────────────────────────────────────────
/// SANS is the app's face, Inter, as Electron bundles it; MONO_FACE is
/// JetBrains Mono. Both are embedded (assets/fonts, Latin subsets, OFL) and
/// registered by `init`, so every platform draws the same letters.
pub const SANS: &str = "Inter";

/// balanced_width is the width that splits `text` (Inter at `size`) into
/// lines of about equal length within `max`, as CSS's text-wrap: balance
/// (Tailwind's text-balance) does: `max` when it fits on one line.
pub fn balanced_width(window: &gpui::Window, text: &str, size: Pixels, max: Pixels) -> Pixels {
    let mut font = gpui::font(SANS);
    font.features = sans_features();
    let run = gpui::TextRun {
        len: text.len(),
        font,
        color: gpui::black(),
        background_color: None,
        underline: None,
        strikethrough: None,
    };
    let w = f32::from(window.text_system().layout_line(text, size, &[run], None).width);
    let max_f = f32::from(max);
    if w <= max_f {
        return max;
    }
    let lines = (w / max_f).ceil();
    px((w / lines).ceil() + f32::from(size) * 0.5).min(max)
}

/// sans_features are the Inter alternates Electron's body turns on
/// (styles.css: font-feature-settings "cv11", "ss01"): the single-storey a
/// and the open digits.
pub fn sans_features() -> gpui::FontFeatures {
    gpui::FontFeatures(std::sync::Arc::new(vec![("cv11".into(), 1), ("ss01".into(), 1)]))
}
pub const MONO_FACE: &str = "JetBrains Mono";

static FONTS: &[&[u8]] = &[
    include_bytes!("../assets/fonts/Inter-Regular.ttf"),
    include_bytes!("../assets/fonts/Inter-Medium.ttf"),
    include_bytes!("../assets/fonts/Inter-SemiBold.ttf"),
    include_bytes!("../assets/fonts/Inter-Bold.ttf"),
    include_bytes!("../assets/fonts/JetBrainsMono-Regular.ttf"),
    include_bytes!("../assets/fonts/JetBrainsMono-Medium.ttf"),
    include_bytes!("../assets/fonts/JetBrainsMono-Bold.ttf"),
];

/// MONO is the monospaced font for code laid out in columns.
pub const MONO: &str = MONO_FACE;

/// mono is the font for monospaced text anywhere in the app: [MONO].
pub fn mono() -> &'static str {
    MONO
}

/// init registers the fonts and dresses gpui-component's widgets (inputs,
/// menus, tooltips, buttons, scrollbars) in the palette worn now.
pub fn init(cx: &mut App) {
    let fonts = FONTS
        .iter()
        .map(|f| std::borrow::Cow::Borrowed(*f))
        .collect();
    if let Err(e) = cx.text_system().add_fonts(fonts) {
        eprintln!("agentbox-native: fonts: {e}");
    }
    sync_widgets(cx);
}

/// sync_widgets copies the worn palette into gpui-component's theme, so its
/// widgets wear the same roles as everything drawn here; call it after
/// `apply` reports a change.
pub fn sync_widgets(cx: &mut App) {
    use gpui_component::{Theme, ThemeMode};
    let mode = if look() == Look::Light {
        ThemeMode::Light
    } else {
        ThemeMode::Dark
    };
    Theme::change(mode, None, cx);
    let t = Theme::global_mut(cx);
    t.font_family = SharedString::from(SANS);
    // gpui-component makes this the window's rem: 16px, as a browser's, so
    // text_sm() and friends are the sizes Electron's classes are.
    t.font_size = px(16.);
    t.mono_font_family = SharedString::from(MONO_FACE);
    t.mono_font_size = text::UI;
    t.radius = radius::MD;
    t.radius_lg = radius::LG;
    t.shadow = true;
    let col = &mut t.colors;
    col.background = ink();
    col.foreground = primary();
    col.border = line_strong();
    col.input = line_strong();
    col.ring = brand(400);
    col.selection = selection();
    col.caret = brand(400);
    col.muted = surface();
    col.muted_foreground = muted();
    col.accent = surface_strong();
    col.accent_foreground = title();
    col.primary = brand(500);
    col.primary_hover = brand(600);
    col.primary_active = brand(700);
    col.primary_foreground = on_brand();
    col.secondary = surface_raised();
    col.secondary_hover = surface_strong();
    col.secondary_active = surface_vivid();
    col.secondary_foreground = secondary();
    col.danger = rose(400);
    col.danger_foreground = on_bright();
    col.popover = solid(overlay());
    col.popover_foreground = primary();
    col.list = gpui::transparent_black();
    col.list_hover = surface_strong();
    col.list_active = tint(brand(400), 0.14);
    col.list_active_border = tint(brand(400), 0.35);
    col.scrollbar = gpui::transparent_black();
    col.scrollbar_thumb = line_vivid();
    col.scrollbar_thumb_hover = line_heavy();
    col.link = brand(300);
    col.link_hover = brand(200);
    col.success = emerald(400);
    col.warning = amber(400);
    col.info = sky(400);
}

/// solid flattens a translucent ground onto the window, for widgets that
/// paint without a backdrop to blend against.
fn solid(color: Hsla) -> Hsla {
    let base = ink().to_rgb();
    let top = color.to_rgb();
    let m = |b: f32, t: f32| b + (t - b) * top.a;
    gpui::Rgba {
        r: m(base.r, top.r),
        g: m(base.g, top.g),
        b: m(base.b, top.b),
        a: 1.,
    }
    .into()
}

/// window_washes is the window's ground: ink with Electron's two washes in its
/// top corners (the body's radial gradients), drawn as soft blurred glows.
pub fn window_washes(window: &Window) -> impl gpui::IntoElement {
    use gpui::{ParentElement, Styled, div};
    let w = window.viewport_size().width;
    let glow = |color: Hsla, size: Pixels| {
        div()
            .absolute()
            .size(size)
            .rounded_full()
            .shadow(vec![BoxShadow {
                color,
                offset: point(px(0.), px(0.)),
                blur_radius: size * 0.6,
                spread_radius: size * 0.1,
                inset: false,
            }])
    };
    div()
        .absolute()
        .inset_0()
        .overflow_hidden()
        .child(
            glow(wash_near(), px(520.))
                .left(px(-260.) - w * 0.08)
                .top(px(-380.)),
        )
        .child(
            glow(wash_far(), px(420.))
                .left(w * 1.08 - px(210.))
                .top(px(-320.)),
        )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn looks_follow_the_setting() {
        let mut th = agentbox_api::Theme {
            appearance: "follow".into(),
            ..Default::default()
        };
        assert_eq!(look_of(&th), Look::Dark, "nothing to follow is dark");
        th.available = true;
        th.mode = "light".into();
        assert_eq!(look_of(&th), Look::Light);
        th.appearance = "dark".into();
        assert_eq!(look_of(&th), Look::Dark);
        th.appearance = "dracula".into();
        assert_eq!(look_of(&th), Look::Dracula);
        assert_eq!(parse_hex("#7aa2f7"), Some(0x7aa2f7));
        assert_eq!(parse_hex("nope"), None);
    }

    #[test]
    fn own_looks_are_styles_css() {
        let dark = palette_of(&agentbox_api::Theme::default());
        assert_eq!(dark, DARK);
        assert_eq!(DARK.brand[4], 0x8455f0ff);
        assert_eq!(DARK.line, 0xffffff11, "6.5% white");
        assert_eq!(LIGHT.primary, 0x18181bff);
        assert_eq!(LIGHT.chat, 0xfafafbff);
        assert_eq!(DRACULA.chat, 0x282a36ff);
        assert_eq!(
            DRACULA.ink, DARK.ink,
            "Dracula's window comes from its theme"
        );
    }

    /// A followed theme's ramp is lib/theme.ts's: its 500 is the accent, its
    /// text shades reach 4.5:1 on the window, and the window is the theme's
    /// background a third of the way to black.
    #[test]
    fn a_followed_accent_derives_the_ramp() {
        let th = agentbox_api::Theme {
            appearance: "follow".into(),
            available: true,
            mode: "dark".into(),
            accent: "#7aa2f7".into(),
            background: "#1a1b26".into(),
            ..Default::default()
        };
        let p = palette_of(&th);
        assert_eq!(p.brand[4] >> 8, 0x7aa2f7);
        let ink = Rgb::from(p.ink >> 8);
        assert_eq!(ink, Rgb::from(0x1a1b26).mix(BLACK, 0.35));
        for s in &p.brand[..4] {
            assert!(Rgb::from(s >> 8).contrast(ink) >= 4.5);
        }
        assert_eq!(
            p.on_brand,
            op(NEAR_BLACK.hex()),
            "white doesn't read on a pale blue"
        );
        assert_eq!(p.title, DARK.title, "the text ramp is the look's own");
    }
}
