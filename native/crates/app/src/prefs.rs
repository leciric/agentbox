//! What this app keeps on this computer only, as the Electron app keeps its
//! display settings in localStorage (lib/groupIdle.ts and its neighbours):
//! a small JSON file in the user's config folder. Everything the daemon can
//! keep, it keeps; this is only how this window behaves.
use std::path::PathBuf;
use std::sync::Mutex;

use serde_json::{Map, Value};

static PREFS: Mutex<Option<Map<String, Value>>> = Mutex::new(None);

fn path() -> Option<PathBuf> {
    if let Ok(p) = std::env::var("AGENTBOX_NATIVE_PREFS") {
        return Some(p.into());
    }
    let dir = if cfg!(windows) {
        PathBuf::from(std::env::var_os("APPDATA")?).join("AgentBox")
    } else if cfg!(target_os = "macos") {
        PathBuf::from(std::env::var_os("HOME")?).join("Library/Application Support/AgentBox")
    } else if let Some(x) = std::env::var_os("XDG_CONFIG_HOME") {
        PathBuf::from(x).join("agentbox")
    } else {
        PathBuf::from(std::env::var_os("HOME")?).join(".config/agentbox")
    };
    Some(dir.join("native.json"))
}

fn with<R>(f: impl FnOnce(&mut Map<String, Value>) -> R) -> R {
    let mut guard = PREFS.lock().unwrap_or_else(|e| e.into_inner());
    let map = guard.get_or_insert_with(|| {
        path()
            .and_then(|p| std::fs::read(p).ok())
            .and_then(|b| serde_json::from_slice(&b).ok())
            .unwrap_or_default()
    });
    f(map)
}

pub fn get_bool(key: &str, default: bool) -> bool {
    with(|m| m.get(key).and_then(Value::as_bool).unwrap_or(default))
}

/// set_bool keeps a choice, for this session at least if the file can't be
/// written.
pub fn set_bool(key: &str, on: bool) {
    set(key, Value::Bool(on));
}

pub fn get(key: &str) -> Option<Value> {
    with(|m| m.get(key).cloned())
}

/// set keeps any value, as set_bool does.
pub fn set(key: &str, value: Value) {
    let bytes = with(|m| {
        m.insert(key.into(), value);
        serde_json::to_vec_pretty(m).unwrap_or_default()
    });
    if let Some(p) = path() {
        if let Some(dir) = p.parent() {
            let _ = std::fs::create_dir_all(dir);
        }
        let _ = std::fs::write(p, bytes);
    }
}

/// GROUP_IDLE is whether the sidebar puts idle projects in a dock of their
/// own and sorts the rest by activity (settings.entry.group-idle), off by
/// default as in the Electron app.
pub const GROUP_IDLE: &str = "groupIdle";

pub fn group_idle() -> bool {
    get_bool(GROUP_IDLE, false)
}

/// The notices, by kind, and how each is given (lib/notifyPrefs.ts): a
/// system notification and a sound, every one on by default.
pub const NOTIFY_KINDS: [&str; 4] = ["review", "question", "error", "session"];
pub const NOTIFY_CHANNELS: [&str; 2] = ["system", "sound"];
/// NOTIFICATIONS keeps them as Electron's localStorage does: {kinds:
/// {review: {system, sound}, …}, dndUntil: milliseconds since 1970}.
pub const NOTIFICATIONS: &str = "notifications";
/// DND_FOREVER is "until turned off", as notifyPrefs.ts's forever.
pub const DND_FOREVER: i64 = 9_007_199_254_740_991;
/// REVIEW_VOLUME is the chime's loudness, 0–100, 60 by default.
pub const REVIEW_VOLUME: &str = "reviewVolume";

fn notifications() -> Map<String, Value> {
    get(NOTIFICATIONS)
        .and_then(|v| v.as_object().cloned())
        .unwrap_or_default()
}

pub fn notify_on(kind: &str, channel: &str) -> bool {
    notifications()
        .get("kinds")
        .and_then(|k| k.get(kind))
        .and_then(|k| k.get(channel))
        .and_then(Value::as_bool)
        .unwrap_or(true)
}

pub fn set_notify(kind: &str, channel: &str, on: bool) {
    let mut n = notifications();
    let kinds = n
        .entry("kinds")
        .or_insert_with(|| Value::Object(Map::new()));
    if let Some(kinds) = kinds.as_object_mut() {
        let k = kinds
            .entry(kind)
            .or_insert_with(|| Value::Object(Map::new()));
        if let Some(k) = k.as_object_mut() {
            k.insert(channel.into(), Value::Bool(on));
        }
    }
    set(NOTIFICATIONS, Value::Object(n));
}

/// dnd_until is when Do not disturb ends, in milliseconds; 0 when it's off.
pub fn dnd_until() -> i64 {
    notifications()
        .get("dndUntil")
        .and_then(Value::as_i64)
        .unwrap_or(0)
}

pub fn set_dnd_until(ms: i64) {
    let mut n = notifications();
    n.insert("dndUntil".into(), Value::from(ms.max(0)));
    set(NOTIFICATIONS, Value::Object(n));
}

pub fn review_volume() -> i64 {
    get(REVIEW_VOLUME)
        .and_then(|v| v.as_i64())
        .unwrap_or(60)
        .clamp(0, 100)
}

/// may_notify is notifyPrefs.ts's mayNotify: whether a kind may use a
/// channel now, which Do not disturb overrides.
pub fn may_notify(kind: &str, channel: &str) -> bool {
    dnd_until() <= chrono::Local::now().timestamp_millis() && notify_on(kind, channel)
}

/// TRAY_KEEP_RUNNING is the tray's "Keep running when the window is closed"
/// (Electron's tray.json keepRunning), off by default.
pub const TRAY_KEEP_RUNNING: &str = "trayKeepRunning";

pub fn tray_keep_running() -> bool {
    get_bool(TRAY_KEEP_RUNNING, false)
}

/// TRAY_KEEP_RUNNING_TOLD is whether the user was told, the first time
/// closing the window left the app running, that it did (alerts.rs).
pub const TRAY_KEEP_RUNNING_TOLD: &str = "trayKeepRunningTold";

/// WINDOW is the window's place and size and whether it was maximised
/// (Electron's window.json): {x, y, width, height, maximized}.
pub const WINDOW: &str = "window";
