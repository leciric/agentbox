//! The code editors on this computer that can open an agent's worktree: the
//! agent's "Open in …" (its header, its ⋯ and its row's menu) and Settings →
//! Code editor, as the Electron app finds them (desktop/src/main/editors.ts).
//! The worktree is on this computer at the path the daemon gives (a Mac's
//! and Linux's are shared with the VM at the same path; Windows reads WSL's
//! through \\wsl.localhost), so the editor runs here, on the agent's files.
//!
//! An editor is found by its command on PATH, in the folders installers put
//! one that a desktop session's PATH may lack, as a Flatpak (exported by its
//! app ID), or on a Mac as an app in Applications, which an app started from
//! the Dock needs: its PATH is /usr/bin:/bin:/usr/sbin:/sbin. Each is given
//! the folder as its one argument.
use std::path::{Path, PathBuf};

/// Kind is an editor the app knows how to find.
struct Kind {
    id: &'static str,
    name: &'static str,
    /// On PATH (on Windows, with PATHEXT's extensions).
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
        commands: &["code"],
        flatpaks: &["com.visualstudio.code"],
        apps: &["Visual Studio Code"],
    },
    Kind {
        id: "cursor",
        name: "Cursor",
        commands: &["cursor"],
        flatpaks: &[],
        apps: &["Cursor"],
    },
    Kind {
        id: "windsurf",
        name: "Windsurf",
        commands: &["windsurf"],
        flatpaks: &[],
        apps: &["Windsurf"],
    },
    Kind {
        id: "zed",
        name: "Zed",
        commands: &["zed", "zeditor", "zedit"],
        flatpaks: &["dev.zed.Zed"],
        apps: &["Zed"],
    },
    Kind {
        id: "vscodium",
        name: "VSCodium",
        commands: &["codium"],
        flatpaks: &["com.vscodium.codium"],
        apps: &["VSCodium"],
    },
    Kind {
        id: "vscode-insiders",
        name: "VS Code Insiders",
        commands: &["code-insiders"],
        flatpaks: &[],
        apps: &["Visual Studio Code - Insiders"],
    },
    Kind {
        id: "sublime",
        name: "Sublime Text",
        commands: &["subl"],
        flatpaks: &["com.sublimetext.three"],
        apps: &["Sublime Text"],
    },
    Kind {
        id: "idea",
        name: "IntelliJ IDEA",
        commands: &["idea", "intellij-idea-ultimate", "intellij-idea-community"],
        flatpaks: &[
            "com.jetbrains.IntelliJ-IDEA-Ultimate",
            "com.jetbrains.IntelliJ-IDEA-Community",
        ],
        apps: &[
            "IntelliJ IDEA",
            "IntelliJ IDEA Ultimate",
            "IntelliJ IDEA CE",
        ],
    },
    Kind {
        id: "goland",
        name: "GoLand",
        commands: &["goland"],
        flatpaks: &["com.jetbrains.GoLand"],
        apps: &["GoLand"],
    },
    Kind {
        id: "webstorm",
        name: "WebStorm",
        commands: &["webstorm"],
        flatpaks: &["com.jetbrains.WebStorm"],
        apps: &["WebStorm"],
    },
    Kind {
        id: "pycharm",
        name: "PyCharm",
        commands: &["pycharm", "pycharm-professional", "pycharm-community"],
        flatpaks: &[
            "com.jetbrains.PyCharm-Professional",
            "com.jetbrains.PyCharm-Community",
        ],
        apps: &[
            "PyCharm",
            "PyCharm Professional Edition",
            "PyCharm CE",
            "PyCharm Community Edition",
        ],
    },
    Kind {
        id: "rustrover",
        name: "RustRover",
        commands: &["rustrover"],
        flatpaks: &[],
        apps: &["RustRover"],
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
    /// Windows' $PATHEXT.
    pub pathext: String,
    /// Whether an executable file, or a Mac app's folder, is there.
    pub exists: &'a dyn Fn(&str) -> bool,
}

/// Editor is one found, and the command that opens a folder in it: the
/// folder goes after args.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Editor {
    pub id: &'static str,
    pub name: &'static str,
    pub command: String,
    pub args: Vec<String>,
}

fn join(os: Os, dir: &str, name: &str) -> String {
    let sep = if os == Os::Windows { '\\' } else { '/' };
    format!("{}{sep}{name}", dir.trim_end_matches(sep))
}

/// search_dirs are the folders a command is looked for in: PATH's, then
/// those installers use that a session's PATH may lack.
fn search_dirs(host: &Host) -> Vec<String> {
    let sep = if host.os == Os::Windows { ';' } else { ':' };
    let mut dirs: Vec<String> = host
        .path
        .split(sep)
        .filter(|d| !d.is_empty())
        .map(String::from)
        .collect();
    if host.os != Os::Windows {
        let home = &host.home;
        dirs.extend([
            format!("{home}/.local/bin"),
            "/usr/local/bin".into(),
            "/usr/bin".into(),
            "/snap/bin".into(),
        ]);
        if host.os == Os::Mac {
            dirs.extend([
                "/opt/homebrew/bin".into(),
                format!("{home}/Library/Application Support/JetBrains/Toolbox/scripts"),
            ]);
        } else {
            dirs.push(format!("{home}/.local/share/JetBrains/Toolbox/scripts"));
        }
    }
    let mut seen = std::collections::HashSet::new();
    dirs.retain(|d| seen.insert(d.clone()));
    dirs
}

/// find lists the editors on host, each once, in KINDS' order.
pub fn find(host: &Host) -> Vec<Editor> {
    let dirs = search_dirs(host);
    let exts: Vec<String> = if host.os == Os::Windows {
        let pathext = if host.pathext.is_empty() {
            ".COM;.EXE;.BAT;.CMD"
        } else {
            &host.pathext
        };
        pathext
            .split(';')
            .filter(|e| !e.is_empty())
            .map(str::to_lowercase)
            .collect()
    } else {
        vec![String::new()]
    };
    let on_path = |name: &str| {
        dirs.iter().find_map(|dir| {
            exts.iter()
                .map(|ext| join(host.os, dir, &format!("{name}{ext}")))
                .find(|p| (host.exists)(p))
        })
    };
    let mut found = Vec::new();
    for kind in KINDS {
        let mut open: Option<(String, Vec<String>)> = None;
        if host.os == Os::Mac {
            // The app first: it's there whether or not its command was installed.
            open = kind.apps.iter().find_map(|app| {
                [
                    format!("/Applications/{app}.app"),
                    format!("{}/Applications/{app}.app", host.home),
                ]
                .into_iter()
                .find(|p| (host.exists)(p))
                .map(|at| ("open".to_string(), vec!["-a".to_string(), at]))
            });
        }
        if open.is_none() {
            open = kind
                .commands
                .iter()
                .find_map(|c| on_path(c))
                .map(|p| (p, Vec::new()));
        }
        if open.is_none() && host.os == Os::Linux {
            open = kind.flatpaks.iter().find_map(|id| {
                [
                    "/var/lib/flatpak/exports/bin".to_string(),
                    format!("{}/.local/share/flatpak/exports/bin", host.home),
                ]
                .into_iter()
                .map(|dir| format!("{dir}/{id}"))
                .find(|p| (host.exists)(p))
                .map(|p| (p, Vec::new()))
            });
        }
        if let Some((command, args)) = open {
            found.push(Editor {
                id: kind.id,
                name: kind.name,
                command,
                args,
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
    let home = if os == Os::Windows {
        var("USERPROFILE")
    } else {
        var("HOME")
    };
    find(&Host {
        os,
        home,
        path: var("PATH"),
        pathext: var("PATHEXT"),
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

/// host_path is where this computer sees a path the daemon gives: the same
/// path, except on Windows, where the daemon's are the WSL distro's
/// (desktop/src/main/wslpaths.ts).
pub fn host_path(path: &str) -> PathBuf {
    if !cfg!(windows) {
        return PathBuf::from(path);
    }
    let distro = std::env::var("AGENTBOX_WSL_DISTRO").unwrap_or_else(|_| "AgentBox".into());
    PathBuf::from(windows_path(path, &distro))
}

fn windows_path(path: &str, distro: &str) -> String {
    if let Some(rest) = path.strip_prefix("/mnt/") {
        let mut parts = rest.splitn(2, '/');
        let drive = parts.next().unwrap_or("");
        if drive.len() == 1 && drive.chars().all(|c| c.is_ascii_lowercase()) {
            let rest = parts.next().unwrap_or("");
            return format!("{}:\\{}", drive.to_uppercase(), rest.replace('/', "\\"));
        }
    }
    format!("\\\\wsl.localhost\\{distro}{}", path.replace('/', "\\"))
}

/// Failed is why a worktree didn't open.
#[derive(Debug)]
pub enum Failed {
    /// No editor is found here.
    None,
    /// The worktree isn't a folder on this computer.
    Folder,
    /// The editor's command didn't start.
    Start(&'static str, std::io::Error),
}

/// open opens a worktree (the daemon's path) in the editor chosen, and
/// answers its name.
pub fn open(worktree: &str) -> Result<&'static str, Failed> {
    let editor = chosen().ok_or(Failed::None)?;
    let folder = host_path(worktree);
    if !folder.is_dir() {
        return Err(Failed::Folder);
    }
    launch(&editor, &folder).map_err(|e| Failed::Start(editor.name, e))?;
    Ok(editor.name)
}

/// launch starts the editor on its own, apart from this app, and reaps it
/// when it exits (an editor's command usually hands the folder to the
/// editor already running and exits at once).
fn launch(editor: &Editor, folder: &Path) -> std::io::Result<()> {
    let mut cmd = std::process::Command::new(&editor.command);
    cmd.args(&editor.args)
        .arg(folder)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null());
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt as _;
        cmd.process_group(0);
    }
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt as _;
        const DETACHED_PROCESS: u32 = 0x0000_0008;
        const CREATE_NEW_PROCESS_GROUP: u32 = 0x0000_0200;
        cmd.creation_flags(DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP);
    }
    let mut child = cmd.spawn()?;
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
                Os::Linux => "/home/ana",
                Os::Mac => "/Users/ana",
                Os::Windows => "C:\\Users\\ana",
            }
            .into(),
            path: path.into(),
            pathext: if os == Os::Windows {
                ".COM;.EXE;.BAT;.CMD".into()
            } else {
                String::new()
            },
            exists: files,
        }
    }

    fn ids(found: &[Editor]) -> Vec<(&str, &str, Vec<&str>)> {
        found
            .iter()
            .map(|e| {
                (
                    e.id,
                    e.command.as_str(),
                    e.args.iter().map(String::as_str).collect(),
                )
            })
            .collect()
    }

    #[test]
    fn linux_editors_on_path_in_order_each_once() {
        let files =
            |p: &str| ["/usr/bin/cursor", "/usr/bin/code", "/usr/local/bin/code"].contains(&p);
        let found = find(&host(Os::Linux, "/usr/bin:/bin", &files));
        assert_eq!(
            ids(&found),
            vec![
                ("vscode", "/usr/bin/code", vec![]),
                ("cursor", "/usr/bin/cursor", vec![])
            ]
        );
    }

    #[test]
    fn linux_finds_editors_off_path_and_flatpaks() {
        let files = |p: &str| {
            [
                "/home/ana/.local/bin/zed",
                "/var/lib/flatpak/exports/bin/com.visualstudio.code",
                "/home/ana/.local/share/JetBrains/Toolbox/scripts/goland",
            ]
            .contains(&p)
        };
        let found = find(&host(Os::Linux, "/usr/bin", &files));
        assert_eq!(
            ids(&found),
            vec![
                (
                    "vscode",
                    "/var/lib/flatpak/exports/bin/com.visualstudio.code",
                    vec![]
                ),
                ("zed", "/home/ana/.local/bin/zed", vec![]),
                (
                    "goland",
                    "/home/ana/.local/share/JetBrains/Toolbox/scripts/goland",
                    vec![]
                ),
            ]
        );
    }

    #[test]
    fn mac_opens_an_app_with_open_a_whatever_path_has() {
        let files = |p: &str| {
            [
                "/Applications/Visual Studio Code.app",
                "/Users/ana/Applications/Zed.app",
                "/opt/homebrew/bin/subl",
            ]
            .contains(&p)
        };
        let found = find(&host(Os::Mac, "/usr/bin:/bin", &files));
        assert_eq!(
            ids(&found),
            vec![
                (
                    "vscode",
                    "open",
                    vec!["-a", "/Applications/Visual Studio Code.app"]
                ),
                ("zed", "open", vec!["-a", "/Users/ana/Applications/Zed.app"]),
                ("sublime", "/opt/homebrew/bin/subl", vec![]),
            ]
        );
    }

    #[test]
    fn windows_finds_a_command_with_pathext() {
        let dir = "C:\\Users\\ana\\AppData\\Local\\Programs\\Microsoft VS Code\\bin";
        let code = format!("{dir}\\code.cmd");
        let files = |p: &str| p == code;
        let found = find(&host(Os::Windows, &format!("C:\\Windows;{dir}"), &files));
        assert_eq!(ids(&found), vec![("vscode", code.as_str(), vec![])]);
    }

    #[test]
    fn the_one_picked_while_installed_else_the_first() {
        let none = |_: &str| false;
        assert!(find(&host(Os::Linux, "/usr/bin", &none)).is_empty());
        assert_eq!(pick(&[], "vscode"), None);
        let files = |p: &str| ["/usr/bin/code", "/usr/bin/zed"].contains(&p);
        let found = find(&host(Os::Linux, "/usr/bin", &files));
        assert_eq!(pick(&found, "zed").map(|e| e.id), Some("zed"));
        assert_eq!(pick(&found, "").map(|e| e.id), Some("vscode"));
        assert_eq!(pick(&found, "cursor").map(|e| e.id), Some("vscode"));
    }

    #[test]
    fn windows_paths_are_the_distros() {
        assert_eq!(
            windows_path(
                "/home/ana/.local/share/agentbox/worktrees/p/agent-01",
                "AgentBox"
            ),
            "\\\\wsl.localhost\\AgentBox\\home\\ana\\.local\\share\\agentbox\\worktrees\\p\\agent-01"
        );
        assert_eq!(
            windows_path("/mnt/c/Users/ana/p", "AgentBox"),
            "C:\\Users\\ana\\p"
        );
    }
}
