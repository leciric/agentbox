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
    let bytes = with(|m| {
        m.insert(key.into(), Value::Bool(on));
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
