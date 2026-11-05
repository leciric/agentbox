//! AgentBox's native desktop app (POC): a thin client of the daemon (D15/D19)
//! drawn with GPUI. It asks the daemon over its socket and shows what comes
//! back; there is no AgentBox logic here.
//!
//!   agentbox-native [--socket PATH]
//!
//! AGENTBOX_NATIVE_BENCH=1 prints timings to stderr (bench.rs).
mod app;
mod bench;
mod chat;
#[cfg(feature = "video")]
mod device;
mod diff;
mod files;
mod highlight;
mod media;
mod mermaid;
mod prefs;
#[cfg(target_os = "linux")]
mod snapshot;
mod terminal;
mod theme;
mod toasts;
#[cfg(feature = "video")]
mod video;
mod viewers;
mod vnc;
mod webview;

use gpui::{App, AppContext as _, Bounds, WindowBounds, WindowOptions, px, size};
use gpui_component::Root;

fn main() {
    bench::start();
    let mut socket = agentbox_api::client::default_socket();
    let mut play = None;
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        match a.as_str() {
            "--socket" => socket = args.next().expect("--socket PATH").into(),
            "--play" => play = Some(std::path::PathBuf::from(args.next().expect("--play FILE"))),
            "--help" | "-h" => {
                println!("agentbox-native [--socket PATH]");
                return;
            }
            _ => {}
        }
    }
    let client = agentbox_api::Client::new(socket);

    gpui_platform::application().run(move |cx: &mut App| {
        gpui_component::init(cx);
        gpui_component::Theme::change(gpui_component::ThemeMode::Dark, None, cx);
        webview::init(cx);
        let bounds = Bounds::centered(None, size(px(1400.), px(900.)), cx);
        let options = WindowOptions {
            window_bounds: Some(WindowBounds::Windowed(bounds)),
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
