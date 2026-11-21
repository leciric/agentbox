//! The code editors on this computer that can open an agent's worktree inside
//! its machine, over SSH: the agent's "Open in …" (its header, its ⋯ and its
//! row's menu) and Settings → Code editor, as the Electron app finds them
//! (desktop/src/main/editors.ts). The worktree isn't on this computer: it's
//! on the VM's disk, in the agent's machine. `agentbox ssh-config` lets ssh
//! reach that machine as <instance>.agentbox (internal/sshconfig), so an
//! editor that works over SSH opens it there: VS Code and the editors made
//! from it through their Remote-SSH URL, and Zed with ssh://. Editors with
//! no cheap way in over SSH (Sublime Text; JetBrains' IDEs, which need
//! Gateway) aren't offered, and Windows has none yet: its ssh config and
//! ProxyCommand would be Windows' (internal/hostwsl).
//!
//! An editor is found by its command on PATH, in the folders installers put
//! one that a desktop session's PATH may lack, as a Flatpak (exported by its
//! app ID), or on a Mac as an app in Applications, which an app started from
//! the Dock needs: its PATH is /usr/bin:/bin:/usr/sbin:/sbin.
use std::path::Path;

/// Remote is how an editor opens a folder over SSH: its URL scheme's
/// vscode-remote URL, or Zed's ssh:// argument to its command.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Remote {
    Scheme(&'static str),
    Zed,
}

/// Kind is an editor the app knows how to find.
struct Kind {
    id: &'static str,
    name: &'static str,
    remote: Remote,
    /// On PATH.
    commands: &'static [&'static str],
    /// Linux: Flatpak app IDs.
    flatpaks: &'static [&'static str],
    /// Mac: app bundles, without .app.
    apps: &'static [&'static str],
}

/// KINDS, in the order the first one found is taken when none is picked.
const KINDS: &[Kind] = &[
    Kind {
        id: "vscode",
        name: "VS Code",
        remote: Remote::Scheme("vscode"),
        commands: &["code"],
        flatpaks: &["com.visualstudio.code"],
        apps: &["Visual Studio Code"],
    },
    Kind {
        id: "cursor",
        name: "Cursor",
        remote: Remote::Scheme("cursor"),
        commands: &["cursor"],
        flatpaks: &[],
        apps: &["Cursor"],
    },
    Kind {
        id: "windsurf",
        name: "Windsurf",
        remote: Remote::Scheme("windsurf"),
        commands: &["windsurf"],
        flatpaks: &[],
        apps: &["Windsurf"],
    },
    Kind {
        id: "zed",
        name: "Zed",
        remote: Remote::Zed,
        commands: &["zed", "zeditor", "zedit"],
        flatpaks: &["dev.zed.Zed"],
        apps: &["Zed"],
    },
    Kind {
        id: "vscodium",
        name: "VSCodium",
        remote: Remote::Scheme("vscodium"),
        commands: &["codium"],
        flatpaks: &["com.vscodium.codium"],
        apps: &["VSCodium"],
    },
    Kind {
        id: "vscode-insiders",
        name: "VS Code Insiders",
        remote: Remote::Scheme("vscode-insiders"),
        commands: &["code-insiders"],
        flatpaks: &[],
        apps: &["Visual Studio Code - Insiders"],
    },
];

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Os {
    Linux,
    Mac,
    Windows,
}

/// Host is what finding editors reads of this computer, so tests can fake it.
pub struct Host<'a> {
    pub os: Os,
    pub home: String,
    /// $PATH.
    pub path: String,
    /// Whether an executable file, or a Mac app's folder, is there.
    pub exists: &'a dyn Fn(&str) -> bool,
}

/// Editor is one found: how it opens a folder over SSH, and its command
/// (Zed's, which is given the ssh:// argument; on a Mac the one in its app).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Editor {
    pub id: &'static str,
    pub name: &'static str,
    pub remote: Remote,
    pub command: String,
}

/// search_dirs are the folders a command is looked for in: PATH's, then
/// those installers use that a session's PATH may lack.
fn search_dirs(host: &Host) -> Vec<String> {
    let mut dirs: Vec<String> = host
        .path
        .split(':')
        .filter(|d| !d.is_empty())
        .map(String::from)
        .collect();
    dirs.extend([
        format!("{}/.local/bin", host.home),
        "/usr/local/bin".into(),
        "/usr/bin".into(),
        "/snap/bin".into(),
    ]);
    if host.os == Os::Mac {
        dirs.push("/opt/homebrew/bin".into());
    }
    let mut seen = std::collections::HashSet::new();
    dirs.retain(|d| seen.insert(d.clone()));
    dirs
}

/// find lists the editors on host, each once, in KINDS' order.
pub fn find(host: &Host) -> Vec<Editor> {
    if host.os == Os::Windows {
        return Vec::new();
    }
    let dirs = search_dirs(host);
    let on_path = |name: &str| {
        dirs.iter()
            .map(|dir| format!("{}/{name}", dir.trim_end_matches('/')))
            .find(|p| (host.exists)(p))
    };
    let mut found = Vec::new();
    for kind in KINDS {
        let mut command: Option<String> = None;
        if host.os == Os::Mac {
            // The app first: it's there whether or not its command was
            // installed. Its URL is the app's; Zed's command is in it.
            command = kind.apps.iter().find_map(|app| {
                [
                    format!("/Applications/{app}.app"),
                    format!("{}/Applications/{app}.app", host.home),
                ]
                .into_iter()
                .find(|p| (host.exists)(p))
                .map(|at| match kind.remote {
                    Remote::Zed => format!("{at}/Contents/MacOS/cli"),
                    Remote::Scheme(_) => at,
                })
            });
        }
        if command.is_none() {
            command = kind.commands.iter().find_map(|c| on_path(c));
        }
        if command.is_none() && host.os == Os::Linux {
            command = kind.flatpaks.iter().find_map(|id| {
                [
                    "/var/lib/flatpak/exports/bin".to_string(),
                    format!("{}/.local/share/flatpak/exports/bin", host.home),
                ]
                .into_iter()
                .map(|dir| format!("{dir}/{id}"))
                .find(|p| (host.exists)(p))
            });
        }
        if let Some(command) = command {
            found.push(Editor {
                id: kind.id,
                name: kind.name,
                remote: kind.remote,
                command,
            });
        }
    }
    found
}

/// pick is the editor to open: the one picked, while it's installed, or
/// else the first found.
pub fn pick<'a>(found: &'a [Editor], picked: &str) -> Option<&'a Editor> {
    found.iter().find(|e| e.id == picked).or(found.first())
}

fn exists(path: &str) -> bool {
    let Ok(meta) = std::fs::metadata(path) else {
        return false;
    };
    if path.ends_with(".app") {
        return meta.is_dir();
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt as _;
        meta.is_file() && meta.permissions().mode() & 0o111 != 0
    }
    #[cfg(not(unix))]
    {
        meta.is_file()
    }
}

/// on_this_computer lists the editors here.
pub fn on_this_computer() -> Vec<Editor> {
    let os = if cfg!(windows) {
        Os::Windows
    } else if cfg!(target_os = "macos") {
        Os::Mac
    } else {
        Os::Linux
    };
    let var = |k: &str| std::env::var(k).unwrap_or_default();
    find(&Host {
        os,
        home: var("HOME"),
        path: var("PATH"),
        exists: &exists,
    })
}

/// FOUND is what on_this_computer last found, and when: the agent's header
/// asks as it draws, so a look round the disk is kept for a while, and an
/// editor installed meanwhile shows up within FRESH.
static FOUND: std::sync::Mutex<Option<(std::time::Instant, Vec<Editor>)>> =
    std::sync::Mutex::new(None);
const FRESH: std::time::Duration = std::time::Duration::from_secs(30);

/// found is the editors here, looked for again once FRESH has passed.
pub fn found() -> Vec<Editor> {
    let mut guard = FOUND.lock().unwrap_or_else(|e| e.into_inner());
    match guard.as_ref() {
        Some((at, found)) if at.elapsed() < FRESH => found.clone(),
        _ => {
            let found = on_this_computer();
            *guard = Some((std::time::Instant::now(), found.clone()));
            found
        }
    }
}

/// chosen is the editor "Open in …" opens here, None when none is found.
pub fn chosen() -> Option<Editor> {
    let found = found();
    pick(&found, &crate::prefs::editor()).cloned()
}

/// SshSetup is what `agentbox ssh-config --json` answers: the user ssh logs
/// in as, and what makes an instance's name its host name.
#[derive(Debug, PartialEq, Eq)]
pub struct SshSetup {
    pub user: String,
    pub host_suffix: String,
}

/// Target is how an editor opens an agent's worktree: a URL for the system
/// to open, or Zed's command with its ssh:// argument.
#[derive(Debug, PartialEq, Eq)]
pub enum Target {
    Url(String),
    Command(String, Vec<String>),
}

/// url_path is a path in a URL: each part percent-encoded, as JavaScript's
/// encodeURIComponent does (the Electron app's), its slashes kept.
fn url_path(path: &str) -> String {
    let mut out = String::new();
    for b in path.bytes() {
        if b.is_ascii_alphanumeric() || b"/-_.!~*'()".contains(&b) {
            out.push(b as char);
        } else {
            out.push_str(&format!("%{b:02X}"));
        }
    }
    out
}

/// remote_target is where editor opens the worktree of the agent whose
/// machine is instance: the path in the machine, on the host ssh knows it by.
pub fn remote_target(editor: &Editor, setup: &SshSetup, instance: &str, worktree: &str) -> Target {
    let host = format!("{instance}{}", setup.host_suffix);
    let path = url_path(worktree);
    match editor.remote {
        Remote::Zed => Target::Command(
            editor.command.clone(),
            vec![format!("ssh://{}@{host}{path}", url_path(&setup.user))],
        ),
        Remote::Scheme(scheme) => {
            Target::Url(format!("{scheme}://vscode-remote/ssh-remote+{host}{path}"))
        }
    }
}

/// Failed is why a worktree didn't open.
#[derive(Debug)]
pub enum Failed {
    /// No editor is found here.
    None,
    /// `agentbox ssh-config` didn't run, or said why it couldn't.
    Ssh(String),
    /// The editor's command didn't start.
    Start(&'static str, std::io::Error),
}

/// ssh_setup runs `agentbox ssh-config --json` (cli is the agentbox
/// command), which makes sure ssh can reach agents' machines (the key,
/// AgentBox's config and its Include line) and says how.
pub fn ssh_setup(cli: Option<&Path>) -> Result<SshSetup, String> {
    let cli = cli.ok_or("the agentbox command isn't installed")?;
    let out = std::process::Command::new(cli)
        .args(["ssh-config", "--json"])
        .stdin(std::process::Stdio::null())
        .output()
        .map_err(|e| format!("{}: {e}", cli.display()))?;
    if !out.status.success() {
        let why = String::from_utf8_lossy(&out.stderr).trim().to_string();
        return Err(if why.is_empty() {
            format!("agentbox ssh-config: {}", out.status)
        } else {
            why
        });
    }
    let v: serde_json::Value = serde_json::from_slice(&out.stdout)
        .map_err(|e| format!("agentbox ssh-config's answer: {e}"))?;
    let field = |k: &str| v.get(k).and_then(|x| x.as_str()).unwrap_or("").to_string();
    let setup = SshSetup {
        user: field("user"),
        host_suffix: field("hostSuffix"),
    };
    if setup.user.is_empty() || setup.host_suffix.is_empty() {
        return Err(format!(
            "agentbox ssh-config said {}",
            String::from_utf8_lossy(&out.stdout).trim()
        ));
    }
    Ok(setup)
}

/// target is how the editor chosen opens an agent's worktree (its path in
/// the machine whose instance this is), with ssh set up for it, and the
/// editor's name.
pub fn target(
    cli: Option<&Path>,
    instance: &str,
    worktree: &str,
) -> Result<(&'static str, Target), Failed> {
    let editor = chosen().ok_or(Failed::None)?;
    let setup = ssh_setup(cli).map_err(Failed::Ssh)?;
    Ok((
        editor.name,
        remote_target(&editor, &setup, instance, worktree),
    ))
}

/// launch starts a command on its own, apart from this app, and reaps it
/// when it exits.
pub fn launch(name: &'static str, command: &str, args: &[String]) -> Result<(), Failed> {
    let mut cmd = std::process::Command::new(command);
    cmd.args(args)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null());
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt as _;
        cmd.process_group(0);
    }
    let mut child = cmd.spawn().map_err(|e| Failed::Start(name, e))?;
    std::thread::spawn(move || child.wait());
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn host<'a>(os: Os, path: &str, files: &'a dyn Fn(&str) -> bool) -> Host<'a> {
        Host {
            os,
            home: match os {
                Os::Mac => "/Users/ana",
                _ => "/home/ana",
            }
            .into(),
            path: path.into(),
            exists: files,
        }
    }

    fn ids(found: &[Editor]) -> Vec<(&str, &str)> {
        found.iter().map(|e| (e.id, e.command.as_str())).collect()
    }

    #[test]
    fn linux_editors_on_path_in_order_each_once() {
        let files =
            |p: &str| ["/usr/bin/cursor", "/usr/bin/code", "/usr/local/bin/code"].contains(&p);
        let found = find(&host(Os::Linux, "/usr/bin:/bin", &files));
        assert_eq!(
            ids(&found),
            vec![("vscode", "/usr/bin/code"), ("cursor", "/usr/bin/cursor")]
        );
    }

    #[test]
    fn linux_finds_editors_off_path_and_flatpaks() {
        let files = |p: &str| {
            [
                "/home/ana/.local/bin/zed",
                "/var/lib/flatpak/exports/bin/com.visualstudio.code",
            ]
            .contains(&p)
        };
        let found = find(&host(Os::Linux, "/usr/bin", &files));
        assert_eq!(
            ids(&found),
            vec![
                (
                    "vscode",
                    "/var/lib/flatpak/exports/bin/com.visualstudio.code"
                ),
                ("zed", "/home/ana/.local/bin/zed"),
            ]
        );
    }

    #[test]
    fn editors_with_no_cheap_way_in_over_ssh_are_not_offered() {
        let files = |p: &str| ["/usr/bin/subl", "/usr/bin/idea", "/usr/bin/goland"].contains(&p);
        assert!(find(&host(Os::Linux, "/usr/bin", &files)).is_empty());
    }

    #[test]
    fn mac_finds_apps_whatever_path_has_and_zeds_command_inside_it() {
        let files = |p: &str| {
            [
                "/Applications/Visual Studio Code.app",
                "/Users/ana/Applications/Zed.app",
            ]
            .contains(&p)
        };
        let found = find(&host(Os::Mac, "/usr/bin:/bin", &files));
        assert_eq!(
            ids(&found),
            vec![
                ("vscode", "/Applications/Visual Studio Code.app"),
                ("zed", "/Users/ana/Applications/Zed.app/Contents/MacOS/cli"),
            ]
        );
    }

    #[test]
    fn windows_has_none_yet() {
        let any = |_: &str| true;
        assert!(find(&host(Os::Windows, "C:\\bin", &any)).is_empty());
    }

    #[test]
    fn the_one_picked_while_installed_else_the_first() {
        assert_eq!(pick(&[], "vscode"), None);
        let files = |p: &str| ["/usr/bin/code", "/usr/bin/zed"].contains(&p);
        let found = find(&host(Os::Linux, "/usr/bin", &files));
        assert_eq!(pick(&found, "zed").map(|e| e.id), Some("zed"));
        assert_eq!(pick(&found, "").map(|e| e.id), Some("vscode"));
        assert_eq!(pick(&found, "cursor").map(|e| e.id), Some("vscode"));
    }

    #[test]
    fn the_worktree_opens_inside_the_agents_machine() {
        let files = |p: &str| {
            [
                "/usr/bin/code",
                "/usr/bin/cursor",
                "/usr/bin/zed",
                "/usr/bin/codium",
            ]
            .contains(&p)
        };
        let found = find(&host(Os::Linux, "/usr/bin", &files));
        let setup = SshSetup {
            user: "ana".into(),
            host_suffix: ".agentbox".into(),
        };
        let wt = "/home/ana/.local/share/agentbox/worktrees/pawly/agent-01";
        let at = |id: &str| {
            remote_target(
                found.iter().find(|e| e.id == id).unwrap(),
                &setup,
                "ab-pawly-agent-01",
                wt,
            )
        };
        assert_eq!(
            at("vscode"),
            Target::Url(format!(
                "vscode://vscode-remote/ssh-remote+ab-pawly-agent-01.agentbox{wt}"
            ))
        );
        assert_eq!(
            at("cursor"),
            Target::Url(format!(
                "cursor://vscode-remote/ssh-remote+ab-pawly-agent-01.agentbox{wt}"
            ))
        );
        assert_eq!(
            at("vscodium"),
            Target::Url(format!(
                "vscodium://vscode-remote/ssh-remote+ab-pawly-agent-01.agentbox{wt}"
            ))
        );
        assert_eq!(
            at("zed"),
            Target::Command(
                "/usr/bin/zed".into(),
                vec![format!("ssh://ana@ab-pawly-agent-01.agentbox{wt}")]
            )
        );
        // A path with a space stays one path, as the Electron app encodes it.
        assert_eq!(
            remote_target(&found[0], &setup, "ab-p-a", "/home/ana/my work/a"),
            Target::Url(
                "vscode://vscode-remote/ssh-remote+ab-p-a.agentbox/home/ana/my%20work/a".into()
            )
        );
    }
}
