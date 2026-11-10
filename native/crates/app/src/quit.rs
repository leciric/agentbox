//! Quitting, by every route: the window's close button, Ctrl+Q (Cmd+Q on a
//! Mac), the tray's Quit and an update's restart. Each one ends in
//! `cx.quit()`, and the process ends with it:
//!
//! - closing the last window quits (GPUI keeps running with none, so a
//!   closed window left an invisible process behind), unless the tray's
//!   "keep running" minimises it instead (app/alerts.rs);
//! - once GPUI's loop returns, main exits the process at once ([`exit`]):
//!   what is still running then (the I/O runtime, the tray's GTK thread,
//!   a webview, a video's decoder) must not hold it open;
//! - a quit that hasn't ended the process within [`GRACE`] (a quit handler,
//!   or the platform's loop, that never returns) ends it anyway.
use std::time::Duration;

use gpui::{App, KeyBinding, actions};

actions!(agentbox, [Quit]);

/// GRACE is how long a quit may take before the process ends regardless.
/// GPUI gives its quit handlers 200 ms (SHUTDOWN_TIMEOUT).
pub const GRACE: Duration = Duration::from_secs(3);

/// QUIT_KEY is the shortcut ("secondary-q" needs an OS GPUI's parse isn't given).
const QUIT_KEY: &str = if cfg!(target_os = "macos") {
    "cmd-q"
} else {
    "ctrl-q"
};

pub fn init(cx: &mut App) {
    // Electron's menu role quit: Cmd+Q on a Mac, Ctrl+Q elsewhere.
    cx.bind_keys([KeyBinding::new(QUIT_KEY, Quit, None)]);
    cx.on_action(|_: &Quit, cx| quit(cx, "the quit shortcut"));
    cx.on_window_closed(|cx, _| {
        if cx.windows().is_empty() {
            quit(cx, "its last window closed");
        }
    })
    .detach();
    cx.on_app_quit(|_| {
        eprintln!("agentbox-native: quit handlers running");
        async {}
    })
    .detach();
}

/// quit quits the app, saying why in the log.
pub fn quit(cx: &mut App, why: &str) {
    eprintln!("agentbox-native: quitting: {why}");
    watchdog(GRACE);
    cx.quit();
}

/// watchdog ends the process after grace, should quitting not have by then.
fn watchdog(grace: Duration) {
    std::thread::Builder::new()
        .name("quit-watchdog".into())
        .spawn(move || {
            std::thread::sleep(grace);
            eprintln!("agentbox-native: quitting took over {grace:?}, exiting");
            exit();
        })
        .ok();
}

/// exit ends the process now, with no destructors: the threads and children
/// that are still running go with it (a child is told by its pipe closing).
pub fn exit() -> ! {
    use std::io::Write as _;
    let _ = std::io::stderr().flush();
    std::process::exit(0)
}
