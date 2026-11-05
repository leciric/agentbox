//! The v2 app's dark palette (desktop/src/renderer/styles.css), as GPUI colours.
use gpui::{Hsla, rgb};

pub fn bg() -> Hsla {
    rgb(0x0b0b0d).into()
}
pub fn panel() -> Hsla {
    rgb(0x111114).into()
}
pub fn raised() -> Hsla {
    rgb(0x1a1a1f).into()
}
pub fn hover() -> Hsla {
    rgb(0x222228).into()
}
pub fn border() -> Hsla {
    rgb(0x26262c).into()
}
pub fn fg() -> Hsla {
    rgb(0xededf0).into()
}
pub fn muted() -> Hsla {
    rgb(0x9a9aa3).into()
}
pub fn faint() -> Hsla {
    rgb(0x5c5c66).into()
}
pub fn accent() -> Hsla {
    rgb(0x7c8cff).into()
}
pub fn running() -> Hsla {
    rgb(0x34d399).into()
}
pub fn starting() -> Hsla {
    rgb(0xfbbf24).into()
}
pub fn error() -> Hsla {
    rgb(0xf87171).into()
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
