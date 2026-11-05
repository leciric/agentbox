//! The v2 app's palettes (desktop/src/renderer/styles.css), as GPUI colours:
//! dark (AgentBox's own, the default), light and Dracula. Which one is worn
//! is the daemon's appearance setting (GET /v1/theme), applied by `apply`;
//! every colour function reads it, so a window refresh repaints in it.
use std::sync::atomic::{AtomicU8, AtomicU32, Ordering};

use gpui::{Hsla, rgb};

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Look {
    Dark,
    Light,
    Dracula,
}

static LOOK: AtomicU8 = AtomicU8::new(0);
/// ACCENT is a followed theme's accent (0xRRGGBB), or NO_ACCENT for the look's own.
static ACCENT: AtomicU32 = AtomicU32::new(NO_ACCENT);
const NO_ACCENT: u32 = u32::MAX;

pub fn look() -> Look {
    match LOOK.load(Ordering::Relaxed) {
        1 => Look::Light,
        2 => Look::Dracula,
        _ => Look::Dark,
    }
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

/// apply wears what the daemon's theme status says, and reports whether
/// anything changed (so the caller refreshes the window).
pub fn apply(theme: &agentbox_api::Theme) -> bool {
    let next = look_of(theme);
    let accent = if theme.appearance == "follow" && theme.available {
        parse_hex(&theme.accent).unwrap_or(NO_ACCENT)
    } else {
        NO_ACCENT
    };
    let code = match next {
        Look::Dark => 0,
        Look::Light => 1,
        Look::Dracula => 2,
    };
    let changed = LOOK.swap(code, Ordering::Relaxed) != code;
    ACCENT.swap(accent, Ordering::Relaxed) != accent || changed
}

fn parse_hex(s: &str) -> Option<u32> {
    let s = s.trim().trim_start_matches('#');
    (s.len() == 6).then(|| u32::from_str_radix(s, 16).ok())?
}

/// pick is a colour in the look worn now: dark, light, Dracula.
fn pick(dark: u32, light: u32, dracula: u32) -> Hsla {
    rgb(match look() {
        Look::Dark => dark,
        Look::Light => light,
        Look::Dracula => dracula,
    })
    .into()
}

pub fn bg() -> Hsla {
    pick(0x0b0b0d, 0xf4f4f6, 0x282a36)
}
pub fn panel() -> Hsla {
    pick(0x111114, 0xffffff, 0x21222c)
}
pub fn raised() -> Hsla {
    pick(0x1a1a1f, 0xebebee, 0x343746)
}
pub fn hover() -> Hsla {
    pick(0x222228, 0xe2e2e6, 0x44475a)
}
pub fn border() -> Hsla {
    pick(0x26262c, 0xdadade, 0x3c3f52)
}
pub fn fg() -> Hsla {
    pick(0xededf0, 0x18181b, 0xf8f8f2)
}
pub fn muted() -> Hsla {
    pick(0x9a9aa3, 0x52525b, 0xadb5cb)
}
pub fn faint() -> Hsla {
    pick(0x5c5c66, 0x8e8e97, 0x6272a4)
}
pub fn accent() -> Hsla {
    match ACCENT.load(Ordering::Relaxed) {
        NO_ACCENT => pick(0x7c8cff, 0x7c3aed, 0xbd93f9),
        own => rgb(own).into(),
    }
}
pub fn running() -> Hsla {
    pick(0x34d399, 0x047857, 0x50fa7b)
}
pub fn starting() -> Hsla {
    pick(0xfbbf24, 0xb45309, 0xf1fa8c)
}
pub fn error() -> Hsla {
    pick(0xf87171, 0xbe123c, 0xff5555)
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

/// MONO is a monospaced font this platform has, for code laid out in
/// columns (a diff): GPUI doesn't resolve the generic "monospace" on Linux.
pub const MONO: &str = if cfg!(target_os = "macos") {
    "Menlo"
} else if cfg!(windows) {
    "Consolas"
} else {
    "DejaVu Sans Mono"
};

/// mono is the font for monospaced text anywhere in the app: [MONO].
pub fn mono() -> &'static str {
    MONO
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
}
