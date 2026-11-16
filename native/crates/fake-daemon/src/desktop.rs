//! The fake agents' desktop: this machine's own VNC server (VNC_ADDR, else
//! 127.0.0.1:5900, the Xvnc an AgentBox agent runs), answered for as the real
//! daemon answers for an agent's (internal/daemon/browser.go,
//! internal/agent/browser.go). Its status says whether the VNC server is up,
//! the view refuses with the daemon's error while it's down, and starting or
//! stopping it runs FAKE_DESKTOP_START / FAKE_DESKTOP_STOP (`sh -c`; in an
//! agent, `agentbox browser start` and `agentbox browser stop`), as starting
//! and stopping an agent's machine do: so the app can be tried against a real
//! VNC server going away and coming back. Every fake agent shares the one
//! server.
use std::time::Duration;

use agentbox_api::*;
use tokio::net::TcpStream;

pub fn addr() -> String {
    std::env::var("VNC_ADDR").unwrap_or_else(|_| "127.0.0.1:5900".into())
}

/// connect reaches the VNC server, if it's up.
pub async fn connect() -> Option<TcpStream> {
    tokio::time::timeout(Duration::from_millis(500), TcpStream::connect(addr()))
        .await
        .ok()?
        .ok()
}

/// listening is whether something listens on the VNC server's port, read
/// from /proc/net/tcp{,6} rather than by connecting: TigerVNC counts a
/// connection closed before its handshake ends as a failed login, and after a
/// few refuses that address for a while ("Too many security failures"). The
/// real daemon asks the X server instead (browser.sh's display_answering).
fn listening() -> bool {
    let Some(port) = addr()
        .rsplit_once(':')
        .and_then(|(_, p)| p.parse::<u16>().ok())
    else {
        return false;
    };
    let port = format!(":{port:04X}");
    ["/proc/net/tcp", "/proc/net/tcp6"].iter().any(|f| {
        std::fs::read_to_string(f).is_ok_and(|t| {
            t.lines().skip(1).any(|l| {
                let mut cols = l.split_whitespace().skip(1);
                let local = cols.next().unwrap_or("");
                let state = cols.nth(1).unwrap_or("");
                local.ends_with(&port) && state == "0A"
            })
        })
    })
}

/// status is GET /v1/agents/<p>/<a>/browser.
pub async fn status() -> BrowserStatus {
    let display = listening();
    BrowserStatus {
        display,
        running: display,
        version: if display {
            "Chromium 131".into()
        } else {
            String::new()
        },
        pages: if display {
            vec![BrowserPage {
                id: "1".into(),
                url: "http://localhost:5173/settings".into(),
                title: "AgentBox · Settings".into(),
            }]
        } else {
            vec![]
        },
    }
}

/// refusal is the daemon's answer to a view of a desktop that isn't running
/// (Manager.DialVNC; writeError's 400).
pub fn refusal(r#ref: &str) -> String {
    format!("{ref}'s desktop isn't running: start it first")
}

/// start runs FAKE_DESKTOP_START, if set, and waits for the server to answer.
pub async fn start() {
    run("FAKE_DESKTOP_START", true).await
}

/// stop runs FAKE_DESKTOP_STOP, if set, and waits for the server to go.
pub async fn stop() {
    run("FAKE_DESKTOP_STOP", false).await
}

async fn run(var: &str, up: bool) {
    let Ok(cmd) = std::env::var(var) else { return };
    match tokio::process::Command::new("sh")
        .args(["-c", &cmd])
        .status()
        .await
    {
        Ok(s) if !s.success() => eprintln!("{var}: {s}"),
        Err(e) => eprintln!("{var}: {e}"),
        _ => {}
    }
    for _ in 0..40 {
        if listening() == up {
            return;
        }
        tokio::time::sleep(Duration::from_millis(250)).await;
    }
}
