//! AgentBox's native desktop app (POC): a thin client of the daemon (D15/D19)
//! drawn with GPUI. It asks the daemon over its socket and shows what comes
//! back; there is no AgentBox logic here.
//!
//!   agentbox-native [--socket PATH]
//!
//! AGENTBOX_NATIVE_BENCH=1 prints timings to stderr (bench.rs).
// A release build on Windows is a GUI program, so starting it from Explorer
// doesn't open a console window beside it too (stderr goes nowhere there).
#![cfg_attr(all(windows, not(debug_assertions)), windows_subsystem = "windows")]
mod app;
mod bench;
mod chat;
mod dates;
#[cfg(feature = "video")]
mod device;
mod diff;
mod files;
mod highlight;
mod hostpower;
mod icons;
mod media;
mod mermaid;
mod motion;
mod nightly;
mod notices;
mod office;
mod prefs;
mod quit;
#[cfg(target_os = "linux")]
mod snapshot;
mod spot;
mod terminal;
mod theme;
mod toasts;
mod tray;
mod update;
#[cfg(feature = "video")]
mod video;
mod viewers;
mod vnc;
mod webview;

use gpui::{App, AppContext as _, WindowOptions, px, size};
use gpui_component::Root;

fn main() {
    // Restarted into by an update: the old version goes first (update.rs).
    let waited = update::wait_for_previous();
    bench::start();
    // Text as light as Chromium's: GPUI's greyscale text defaults to gamma
    // 1.8 with an enhanced contrast of 1, a touch heavier than Skia's
    // (measured against the Electron app, native/scripts/parity.sh). A value
    // the person set wins.
    for (var, value) in [
        ("ZED_FONTS_GAMMA", "1.2"),
        ("ZED_FONTS_GRAYSCALE_ENHANCED_CONTRAST", "0.1"),
    ] {
        if std::env::var_os(var).is_none() {
            // SAFETY: before any thread starts.
            unsafe { std::env::set_var(var, value) };
        }
    }
    let mut socket = agentbox_api::client::default_socket();
    let mut play = None;
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        match a.as_str() {
            "--socket" => socket = args.next().expect("--socket PATH").into(),
            "--play" => play = Some(std::path::PathBuf::from(args.next().expect("--play FILE"))),
            "--help" | "-h" => {
                println!("agentbox-native [--socket PATH] [--version]");
                return;
            }
            // The version this build was stamped with (update.rs,
            // AGENTBOX_VERSION): native-package.sh checks it, so a nightly
            // can't ship a build that doesn't know its own version.
            "--version" | "-V" => {
                println!("agentbox-native {}", update::APP_VERSION);
                return;
            }
            _ => {}
        }
    }
    // Which build is running, for the logs: after an update's restart, the
    // new version's line follows the old one's.
    eprintln!(
        "agentbox-native {} (pid {}, {})",
        update::APP_VERSION,
        std::process::id(),
        std::env::current_exe()
            .map(|p| p.display().to_string())
            .unwrap_or_default()
    );
    if let Some(waited) = waited {
        eprintln!(
            "agentbox-native: restarted by an update, after the old version exited ({waited:.1?})"
        );
    }
    let client = agentbox_api::Client::new(socket);
    webview::set_client(client.clone());

    gpui_platform::application()
        .with_assets(icons::Assets)
        .run(move |cx: &mut App| {
            gpui_component::init(cx);
            // Greyscale antialiasing, as Electron draws its text (Chromium
            // on Linux, -webkit-font-smoothing: antialiased): no colour
            // fringes, the same weight.
            cx.set_text_rendering_mode(gpui::TextRenderingMode::Grayscale);
            theme::init(cx);
            motion::init(cx);
            webview::init(cx);
            quit::init(cx);
            // Where it was last time, on a display there is now (alerts.rs).
            let options = WindowOptions {
                window_bounds: Some(app::initial_bounds(cx)),
                window_min_size: Some(size(px(480.), px(480.))),
                app_id: Some("agentbox-native".into()),
                ..Default::default()
            };
            #[cfg(feature = "video")]
            if let Some(path) = play {
                // agentbox-native --play FILE: one recording in a window of its own.
                cx.open_window(options, |window, cx| {
                    window.set_window_title("Recording");
                    let view = cx.new(|cx| video::VideoPlayer::new(path, cx));
                    cx.new(|cx| Root::new(view, window, cx))
                })
                .expect("open the window");
                cx.activate(true);
                return;
            }
            #[cfg(not(feature = "video"))]
            let _ = play;
            cx.open_window(options, |window, cx| {
                window.set_window_title("AgentBox");
                let view = cx.new(|cx| app::AppView::new(client.clone(), window, cx));
                cx.new(|cx| Root::new(view, window, cx))
            })
            .expect("open the window");
            cx.activate(true);
        });
    // GPUI's loop has returned: the app has quit (quit.rs).
    quit::exit();
}

#[cfg(test)]
mod tests {
    /// Every key the app asks for by name is in the catalogs (a typo would
    /// show the key on screen).
    #[::core::prelude::v1::test]
    fn every_key_is_in_the_catalogs() {
        fn sources(dir: &std::path::Path, out: &mut Vec<String>) {
            for f in std::fs::read_dir(dir).unwrap() {
                let path = f.unwrap().path();
                if path.is_dir() {
                    sources(&path, out);
                } else if path.extension().is_some_and(|e| e == "rs") {
                    out.push(std::fs::read_to_string(path).unwrap());
                }
            }
        }
        let mut all = Vec::new();
        sources(
            &std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("src"),
            &mut all,
        );
        let mut checked = 0;
        for src in all {
            for call in ["t(\"", "tv(\""] {
                let mut parts = src.split(call);
                let mut before = parts.next().unwrap_or("");
                for part in parts {
                    // t( itself, not the end of expect( or format(.
                    let word = before
                        .chars()
                        .last()
                        .is_some_and(|c| c.is_alphanumeric() || c == '_');
                    before = part;
                    if word {
                        continue;
                    }
                    let key = &part[..part.find('"').unwrap()];
                    if key.contains('{') {
                        continue;
                    }
                    assert!(
                        agentbox_i18n::has(key),
                        "{key} isn't in desktop/src/shared/i18n/en-US.ts"
                    );
                    checked += 1;
                }
            }
        }
        assert!(checked > 5);
    }
}
