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
mod theme;

use gpui::{App, AppContext as _, Bounds, WindowBounds, WindowOptions, px, size};
use gpui_component::Root;

fn main() {
    bench::start();
    let mut socket = agentbox_api::client::default_socket();
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        match a.as_str() {
            "--socket" => socket = args.next().expect("--socket PATH").into(),
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
        let bounds = Bounds::centered(None, size(px(1400.), px(900.)), cx);
        let options = WindowOptions {
            window_bounds: Some(WindowBounds::Windowed(bounds)),
            app_id: Some("agentbox-native".into()),
            ..Default::default()
        };
        cx.open_window(options, |window, cx| {
            window.set_window_title("AgentBox");
            let view = cx.new(|cx| app::AppView::new(client.clone(), window, cx));
            cx.new(|cx| Root::new(view, window, cx))
        })
        .expect("open the window");
        cx.activate(true);
    });
}
