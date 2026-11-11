//! The nightly treatment of the sidebar's header, as Electron's `nightly-sky`
//! and `nightly-badge` (desktop/src/renderer/styles.css, lib/nightly.ts): a
//! few stars over a dusk wash behind the logo and title, fading out before
//! the header ends, and a moon badge with a "Nightly <version>" tooltip.
//! Stars are pale on dark looks and indigo on light, and some twinkle unless
//! motion is reduced.

use std::time::Duration;

use gpui::*;

use crate::{motion, theme};

/// is_nightly is nightly.ts's isNightly (and internal/update's IsNightly):
/// <release>-nightly.<YYYYMMDD>.<run>, with an optional leading v. The app's
/// own build may spell it 0.18.0.20261106.179 (no -nightly), which counts too.
pub fn is_nightly(version: Option<&str>) -> bool {
    let Some(v) = version else { return false };
    let v = v.trim().trim_start_matches('v');
    let digits = |s: &str| !s.is_empty() && s.bytes().all(|b| b.is_ascii_digit());
    let parts: Vec<&str> = v.split(['.', '-']).collect();
    match parts.as_slice() {
        [a, b, c, n, date, run] => {
            [a, b, c, date, run].iter().all(|s| digits(s)) && *n == "nightly" && date.len() == 8
        }
        [a, b, c, date, run] => [a, b, c, date, run].iter().all(|s| digits(s)) && date.len() == 8,
        _ => false,
    }
}

/// STARS are the sky's stars, as the CSS places them: x and y in percent of
/// the header, radius in px, and whether it twinkles (the ::after layer).
const STARS: [(f32, f32, f32, bool); 10] = [
    (12., 22., 1., false),
    (27., 74., 1., false),
    (44., 18., 1.5, false),
    (58., 58., 1., false),
    (71., 28., 1., false),
    (83., 66., 1.5, false),
    (93., 16., 1., false),
    (36., 42., 1.5, true),
    (66., 12., 1., true),
    (97., 48., 1.5, true),
];

/// star_color is --ab-star: pale on dark and Dracula, indigo on light.
fn star_color() -> Hsla {
    match theme::look() {
        theme::Look::Light => rgba(0x4f46e5bf).into(),
        _ => rgba(0xffffffb3).into(),
    }
}

/// night is --ab-night, the dusk wash's top.
fn night() -> Hsla {
    let a = if theme::look() == theme::Look::Light {
        0.14
    } else {
        0.16
    };
    let mut c: Hsla = rgba(0x6366f1ff).into();
    c.a = a;
    c
}

/// sky is the header's backdrop: absolutely placed under its children, `h` px
/// tall. The CSS masks both layers with a gradient (black to 45%, then
/// fading), so each star's alpha follows that fade.
pub fn sky(h: f32) -> impl IntoElement {
    let fade = move |y_pct: f32| -> f32 {
        let y = y_pct / 100.;
        if y <= 0.45 {
            1.
        } else {
            ((1. - y) / 0.55).clamp(0., 1.)
        }
    };
    let wash = div().absolute().inset_0().bg(linear_gradient(
        180.,
        linear_color_stop(night(), 0.),
        linear_color_stop(night().opacity(0.), 1.),
    ));
    let mut layer = div().absolute().inset_0().child(wash);
    for (i, (x, y, r, twinkle)) in STARS.into_iter().enumerate() {
        let size = r * 2.;
        let dot = div()
            .absolute()
            .left(relative(x / 100.))
            .top(px(h * y / 100. - r))
            .size(px(size))
            .rounded_full()
            .bg(star_color().opacity(fade(y)));
        layer = layer.child(if twinkle {
            // nightly-twinkle: 5 s ease-in-out alternate between 25% and 100%.
            // A slow fade: a few frames a second draw it as well as thirty.
            motion::looped_at(
                dot,
                SharedString::from(format!("nightly-star-{i}")),
                Duration::from_millis(10_000),
                0.5,
                8.,
                |el, t| {
                    let half = if t < 0.5 { t * 2. } else { (1. - t) * 2. };
                    el.opacity(0.25 + 0.75 * motion::ease_in_out(half))
                },
            )
        } else {
            dot.into_any_element()
        });
    }
    layer
}

/// badge is the moon beside the name: --ab-brand-300 on a 14% brand tint with
/// a 1px ring (brand-deep on a lighter tint on light looks).
pub fn badge(tip: String) -> impl IntoElement {
    let light = theme::look() == theme::Look::Light;
    let (fg, bg, ring) = if light {
        (
            theme::brand_deep(),
            mix_white(theme::brand_mid(), 0.12),
            theme::brand_mid().opacity(0.45),
        )
    } else {
        (
            theme::brand(300),
            theme::brand_mid().opacity(0.14),
            theme::brand_mid().opacity(0.32),
        )
    };
    let tip: SharedString = tip.into();
    div()
        .id("nightly-badge")
        .flex_none()
        .flex()
        .items_center()
        .p(px(4.))
        .rounded_full()
        .bg(bg)
        .border_1()
        .border_color(ring)
        .text_color(fg)
        .tooltip(move |w, cx| gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx))
        .child(crate::icons::ic("moon-star").size(px(12.)))
}

fn mix_white(c: Hsla, amount: f32) -> Hsla {
    let a = c.to_rgb();
    let m = |x: f32| x * amount + (1. - amount);
    Rgba {
        r: m(a.r),
        g: m(a.g),
        b: m(a.b),
        a: 1.,
    }
    .into()
}

#[cfg(test)]
mod tests {
    use super::is_nightly;

    #[test]
    fn detects_nightlies_like_nightly_ts() {
        assert!(is_nightly(Some("0.17.0-nightly.20261030.156")));
        assert!(is_nightly(Some("v0.17.0-nightly.20261030.156")));
        assert!(is_nightly(Some("0.18.0.20261106.179")));
        assert!(!is_nightly(Some("0.17.0")));
        assert!(!is_nightly(Some("0.17.0-rc.1")));
        assert!(!is_nightly(Some("0.17.0-nightly.2026.1")));
        assert!(!is_nightly(None));
    }
}
