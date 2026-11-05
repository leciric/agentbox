//! Reaching the daemon on Windows, as the Electron app does
//! (desktop/src/main/relay.ts). The daemon's unix socket is inside AgentBox's
//! WSL distro, which Windows can't open (WSL 2 carries files and TCP across,
//! not AF_UNIX), so the app starts `agentbox.exe relay`
//! (internal/hostwsl/relay.go): a named pipe with a random name only this
//! Windows user may open, carried over one wsl.exe into the distro and on to
//! the socket. The relay's first line on stdout says where it listens,
//! `listening \\.\pipe\agentbox-…` (hostwsl.ReadSocketLine reads it the same
//! way). Every request, event stream and WebSocket then connects to that pipe
//! the way it connects to the socket on Linux.
//!
//! The relay is a child of the app and serves until its stdin closes, which
//! the app's exit does. When no daemon answers in the distro, the relay
//! itself answers 503 with `x-agentbox-relay: not-listening`.
use anyhow::{Result, anyhow};

/// The header the relay marks its own answers with.
pub const RELAY_HEADER: &str = "x-agentbox-relay";
pub const RELAY_NOT_LISTENING: &str = "not-listening";

/// parse_listening reads the relay's first line: where it listens.
pub fn parse_listening(line: &str) -> Result<String> {
    line.trim()
        .strip_prefix("listening ")
        .map(|p| p.trim().to_string())
        .filter(|p| !p.is_empty())
        .ok_or_else(|| anyhow!("the relay said {:?}", line.trim()))
}

/// is_pipe is a path that names a Windows named pipe, which a --socket of
/// one is: connected to directly, with no relay.
pub fn is_pipe(path: &str) -> bool {
    let p = path.replace('/', "\\");
    p.starts_with("\\\\.\\pipe\\") || p.starts_with("\\\\?\\pipe\\")
}

/// refused is an answer from the relay rather than the daemon.
pub fn refused(status: u16, header: Option<&str>) -> bool {
    status == 503 && header == Some(RELAY_NOT_LISTENING)
}

/// last_error is the last line the relay said on stderr, without its
/// "error: ", for when it exits before listening.
pub fn last_error(stderr: &str) -> Option<String> {
    stderr
        .lines()
        .map(str::trim)
        .rfind(|l| !l.is_empty())
        .map(|l| l.strip_prefix("error: ").unwrap_or(l).to_string())
}

/// agentbox_bin is the agentbox.exe to run the relay with, as the Electron
/// app's agentboxBin picks it: AGENTBOX_BIN, else the copy the Electron app
/// keeps in %LOCALAPPDATA%\AgentBox\bin, else one beside this app, else
/// whichever PATH has.
pub fn agentbox_bin(
    env: impl Fn(&str) -> Option<String>,
    exe_dir: Option<&std::path::Path>,
    exists: impl Fn(&std::path::Path) -> bool,
) -> std::path::PathBuf {
    use std::path::PathBuf;
    if let Some(b) = env("AGENTBOX_BIN").filter(|b| !b.is_empty()) {
        return b.into();
    }
    let managed = env("LOCALAPPDATA")
        .filter(|d| !d.is_empty())
        .map(PathBuf::from)
        .or_else(|| env("USERPROFILE").map(|h| PathBuf::from(h).join("AppData").join("Local")))
        .map(|d| d.join("AgentBox").join("bin").join("agentbox.exe"));
    let beside = exe_dir.map(|d| d.join("agentbox.exe"));
    [managed, beside]
        .into_iter()
        .flatten()
        .find(|p| exists(p))
        .unwrap_or_else(|| "agentbox.exe".into())
}

#[cfg(windows)]
pub(crate) use win::open;

#[cfg(windows)]
mod win {
    use std::io::{BufRead as _, BufReader, Read as _};
    use std::path::Path;
    use std::process::{Child, ChildStdin, Command, Stdio};
    use std::sync::{Arc, Mutex};
    use std::time::Duration;

    use anyhow::{Context as _, Result, anyhow};
    use tokio::net::windows::named_pipe::{ClientOptions, NamedPipeClient};

    /// The running relay: its pipe, and its stdin, whose closing ends it.
    struct Running {
        pipe: String,
        child: Child,
        _stdin: ChildStdin,
    }

    static RELAY: Mutex<Option<Running>> = Mutex::new(None);
    /// One start at a time.
    static STARTING: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());

    const ERROR_FILE_NOT_FOUND: i32 = 2;
    const ERROR_PIPE_BUSY: i32 = 231;
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;

    /// running is the pipe of a relay still running.
    fn running() -> Option<String> {
        let mut r = RELAY.lock().unwrap();
        let alive = r
            .as_mut()
            .is_some_and(|r| matches!(r.child.try_wait(), Ok(None)));
        if !alive {
            *r = None;
        }
        r.as_ref().map(|r| r.pipe.clone())
    }

    /// pipe starts the relay unless it runs, and returns its pipe.
    async fn pipe() -> Result<String> {
        if let Some(p) = running() {
            return Ok(p);
        }
        let _one = STARTING.lock().await;
        if let Some(p) = running() {
            return Ok(p);
        }
        let started = tokio::task::spawn_blocking(start).await??;
        let pipe = started.pipe.clone();
        *RELAY.lock().unwrap() = Some(started);
        Ok(pipe)
    }

    /// forget drops a relay whose pipe is gone, so the next open starts one.
    fn forget(pipe: &str) {
        let mut r = RELAY.lock().unwrap();
        if r.as_ref().is_some_and(|r| r.pipe == pipe)
            && let Some(mut old) = r.take()
        {
            let _ = old.child.kill();
        }
    }

    /// start runs `agentbox.exe relay` and reads where it listens. Its
    /// stdout and stderr are drained for as long as it runs, so it never
    /// blocks writing to them.
    fn start() -> Result<Running> {
        let bin = super::agentbox_bin(
            |k| std::env::var(k).ok(),
            std::env::current_exe()
                .ok()
                .as_deref()
                .and_then(Path::parent),
            |p| p.is_file(),
        );
        use std::os::windows::process::CommandExt as _;
        let mut child = Command::new(&bin)
            .arg("relay")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .creation_flags(CREATE_NO_WINDOW)
            .spawn()
            .with_context(|| format!("couldn't run {} relay", bin.display()))?;
        let stdin = child.stdin.take().expect("piped stdin");
        let mut stdout = BufReader::new(child.stdout.take().expect("piped stdout"));
        let mut stderr = child.stderr.take().expect("piped stderr");
        let said = Arc::new(Mutex::new(String::new()));
        let tail = said.clone();
        std::thread::spawn(move || {
            let mut buf = [0u8; 4096];
            while let Ok(n) = stderr.read(&mut buf) {
                if n == 0 {
                    break;
                }
                let mut t = tail.lock().unwrap();
                t.push_str(&String::from_utf8_lossy(&buf[..n]));
                if t.len() > 4000 {
                    let cut = t.len() - 2000;
                    let cut = (cut..t.len()).find(|i| t.is_char_boundary(*i)).unwrap_or(0);
                    t.drain(..cut);
                }
            }
        });
        let mut line = String::new();
        let read = stdout.read_line(&mut line);
        if matches!(read, Ok(0) | Err(_)) {
            let code = child.wait().ok().and_then(|s| s.code());
            // Give the stderr reader a moment to take the last of it.
            std::thread::sleep(Duration::from_millis(50));
            let last = super::last_error(&said.lock().unwrap());
            let code = code.map_or("signal".into(), |c| c.to_string());
            return Err(anyhow!(
                "the relay to AgentBox's WSL distro exited ({code}){}",
                last.map(|l| format!(": {l}")).unwrap_or_default()
            ));
        }
        let pipe = match super::parse_listening(&line) {
            Ok(p) => p,
            Err(e) => {
                let _ = child.kill();
                return Err(e);
            }
        };
        std::thread::spawn(move || {
            let _ = std::io::copy(&mut stdout, &mut std::io::sink());
        });
        Ok(Running {
            pipe,
            child,
            _stdin: stdin,
        })
    }

    /// open connects to the daemon: to the pipe a --socket names, or through
    /// the relay, started on first use and again should it have ended.
    pub(crate) async fn open(socket: &Path) -> Result<NamedPipeClient> {
        let given = socket.to_string_lossy();
        if super::is_pipe(&given) {
            return connect(&given).await;
        }
        for _ in 0..2 {
            let pipe = pipe().await?;
            match connect(&pipe).await {
                Err(e)
                    if e.downcast_ref::<std::io::Error>()
                        .and_then(|e| e.raw_os_error())
                        == Some(ERROR_FILE_NOT_FOUND) =>
                {
                    forget(&pipe);
                }
                r => return r,
            }
        }
        Err(anyhow!(
            "the relay to AgentBox's WSL distro isn't listening"
        ))
    }

    /// connect opens a pipe, waiting while each of its instances is busy.
    async fn connect(pipe: &str) -> Result<NamedPipeClient> {
        for _ in 0..100 {
            match ClientOptions::new().open(pipe) {
                Ok(c) => return Ok(c),
                Err(e) if e.raw_os_error() == Some(ERROR_PIPE_BUSY) => {
                    tokio::time::sleep(Duration::from_millis(50)).await;
                }
                Err(e) => {
                    return Err(anyhow::Error::new(e).context(format!("connecting to {pipe}")));
                }
            }
        }
        Err(anyhow!(
            "connecting to {pipe}: every instance of the pipe is busy"
        ))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::{Path, PathBuf};

    #[test]
    fn the_relay_says_where_it_listens() {
        let p = r"\\.\pipe\agentbox-relay-3f2a";
        assert_eq!(parse_listening(&format!("listening {p}\r\n")).unwrap(), p);
        assert_eq!(parse_listening(&format!("listening {p}\n")).unwrap(), p);
        let e = parse_listening("error: no distro\n")
            .unwrap_err()
            .to_string();
        assert_eq!(e, r#"the relay said "error: no distro""#);
        assert!(parse_listening("listening \n").is_err());
        assert!(is_pipe(p));
        assert!(is_pipe("//./pipe/x"));
        assert!(!is_pipe(
            r"C:\Users\u\.local\share\agentbox\run\agentbox.sock"
        ));
    }

    #[test]
    fn a_refusal_is_the_relays_own() {
        assert!(refused(503, Some("not-listening")));
        assert!(!refused(503, None));
        assert!(!refused(200, Some("not-listening")));
        let said = "starting\nerror: the distro AgentBox isn't installed\n\n";
        assert_eq!(
            last_error(said).as_deref(),
            Some("the distro AgentBox isn't installed")
        );
        assert_eq!(last_error(""), None);
    }

    #[test]
    fn the_relay_runs_from_the_managed_copy_first() {
        let env = |vars: &'static [(&'static str, &'static str)]| {
            move |k: &str| {
                vars.iter()
                    .find(|(n, _)| *n == k)
                    .map(|(_, v)| v.to_string())
            }
        };
        let managed = PathBuf::from("L")
            .join("AgentBox")
            .join("bin")
            .join("agentbox.exe");
        let beside = Path::new("app").join("agentbox.exe");
        let all = |_: &Path| true;
        let none = |_: &Path| false;
        assert_eq!(
            agentbox_bin(
                env(&[("AGENTBOX_BIN", "x.exe"), ("LOCALAPPDATA", "L")]),
                None,
                all
            ),
            PathBuf::from("x.exe")
        );
        assert_eq!(
            agentbox_bin(env(&[("LOCALAPPDATA", "L")]), Some(Path::new("app")), all),
            managed
        );
        let only_beside = |p: &Path| p == Path::new("app").join("agentbox.exe");
        assert_eq!(
            agentbox_bin(
                env(&[("LOCALAPPDATA", "L")]),
                Some(Path::new("app")),
                only_beside
            ),
            beside
        );
        assert_eq!(
            agentbox_bin(env(&[]), Some(Path::new("app")), none),
            PathBuf::from("agentbox.exe")
        );
    }
}
