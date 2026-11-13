//! The agentbox command-line tool this app ships, and the copy of it the
//! daemon runs from (desktop/src/main/cli.ts).
//!
//! A packaged app has the tool of its own version built in (build.rs,
//! AGENTBOX_BUNDLED_CLI): in the binary itself rather than a file beside it,
//! so every way the app is installed has it, and so does the bare binary or
//! .exe its own update downloads (update.rs). As it starts, the app keeps the
//! managed copy current from it: ~/.local/share/agentbox/bin/agentbox, or
//! %LOCALAPPDATA%\AgentBox\bin\agentbox.exe, with agentbox-linux beside it on
//! a Mac and on Windows, where the front end looks for the Linux tool it
//! installs in the VM or the WSL distro. The daemon is started from that
//! copy, so a daemon older than the app can be restarted into the app's
//! version (staledaemon.rs).
//!
//! The Electron app keeps the same copy the same way. So neither undoes the
//! other when both are installed, a copy is written only when it differs,
//! and never over the other app's copy unless it's an older version: the
//! newer app's tool stays, and both apps run its daemon (daemon_version).
//! Two apps of one version are two builds of the same tool, and whichever
//! wrote first keeps it. Who wrote the
//! copy is in .agentbox-owner beside it ("native" or "electron"; a copy with
//! none is the Electron app's, from before it wrote one), so an app that went
//! back a version itself still gets its own tool.
use std::io;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

/// The managed copy's file name.
pub const EXE: &str = if cfg!(windows) {
    "agentbox.exe"
} else {
    "agentbox"
};
/// The Linux tool's name beside it, on a Mac and on Windows.
pub const LINUX: &str = "agentbox-linux";

/// Who wrote the managed copy, beside it: OWNER holds ME when this app did.
pub const OWNER: &str = ".agentbox-owner";
const ME: &str = "native";

/// Bundled is the tool built into this app.
#[derive(Clone, Copy)]
pub struct Bundled {
    pub cli: &'static [u8],
    pub linux: Option<&'static [u8]>,
}

/// bundled is this app's own tool: None in a build from a checkout.
pub fn bundled() -> Option<Bundled> {
    #[cfg(bundled_cli)]
    {
        #[cfg(bundled_cli_linux)]
        let linux = Some(&include_bytes!(env!("AGENTBOX_BUNDLED_CLI_LINUX_PATH"))[..]);
        #[cfg(not(bundled_cli_linux))]
        let linux = None;
        Some(Bundled {
            cli: include_bytes!(env!("AGENTBOX_BUNDLED_CLI_PATH")),
            linux,
        })
    }
    #[cfg(not(bundled_cli))]
    None
}

/// managed_dir is where the managed copy is kept (cli.ts's managedDir).
pub fn managed_dir(env: impl Fn(&str) -> Option<String>) -> Option<PathBuf> {
    let set = |k: &str| env(k).filter(|v| !v.is_empty()).map(PathBuf::from);
    if cfg!(windows) {
        return set("LOCALAPPDATA")
            .or_else(|| set("USERPROFILE").map(|h| h.join("AppData").join("Local")))
            .map(|d| d.join("AgentBox").join("bin"));
    }
    set("XDG_DATA_HOME")
        .filter(|d| d.is_absolute())
        .or_else(|| set("HOME").map(|h| h.join(".local").join("share")))
        .map(|d| d.join("agentbox").join("bin"))
}

/// managed is the managed copy, where there is one.
pub fn managed() -> Option<PathBuf> {
    let bin = managed_dir(|k| std::env::var(k).ok())?.join(EXE);
    bin.is_file().then_some(bin)
}

/// Kept is what keeping a copy current did.
#[derive(Debug, PartialEq, Eq)]
pub enum Kept {
    /// It was this app's tool already.
    Same,
    /// It was missing, older or another build, and is this app's now.
    Written,
    /// It is the Electron app's, of this version (its) or a newer one:
    /// left alone.
    Left(String),
}

/// keep_current makes the managed copy in dir this app's tool, of version
/// ours, unless it is already, or the Electron app put it there and it
/// isn't older: version_of asks a copy its version. The Linux tool beside it
/// follows it.
pub fn keep_current(
    bundled: &Bundled,
    dir: &Path,
    ours: &str,
    version_of: impl Fn(&Path) -> Option<String>,
) -> io::Result<Kept> {
    let dst = dir.join(EXE);
    let mut kept = Kept::Same;
    if !same(&dst, bundled.cli) {
        let mine = std::fs::read_to_string(dir.join(OWNER)).is_ok_and(|o| o.trim() == ME);
        if !mine
            && dst.is_file()
            && let Some(theirs) = version_of(&dst)
            && !newer(&comparable(ours), &comparable(&theirs))
        {
            return Ok(Kept::Left(theirs));
        }
        write(bundled.cli, &dst)?;
        std::fs::write(dir.join(OWNER), ME)?;
        kept = Kept::Written;
    }
    if let Some(linux) = bundled.linux {
        let dst = dir.join(LINUX);
        if !same(&dst, linux) {
            write(linux, &dst)?;
            kept = Kept::Written;
        }
    }
    Ok(kept)
}

/// same is whether the file at path holds exactly these bytes.
fn same(path: &Path, bytes: &[u8]) -> bool {
    std::fs::metadata(path).is_ok_and(|m| m.len() == bytes.len() as u64)
        && std::fs::read(path).is_ok_and(|have| have == bytes)
}

/// write puts bytes at dst through a file beside it, so a reader never sees
/// half of it. A running .exe can't be replaced on Windows, but it can be
/// renamed: the old one is moved aside, and removed the next time it isn't
/// running (cli.ts's keepCurrent).
fn write(bytes: &[u8], dst: &Path) -> io::Result<()> {
    let dir = dst.parent().unwrap_or(Path::new("."));
    std::fs::create_dir_all(dir)?;
    let name = dst.file_name().unwrap_or_default().to_string_lossy();
    if let Ok(entries) = std::fs::read_dir(dir) {
        let old = format!("{name}.old-");
        for e in entries.flatten() {
            if e.file_name().to_string_lossy().starts_with(&old) {
                let _ = std::fs::remove_file(e.path()); // fails while it runs
            }
        }
    }
    // The Electron app writes <name>.new: another name, so the two starting
    // at once don't write the same file.
    let new = dir.join(format!("{name}.new-{}", std::process::id()));
    std::fs::write(&new, bytes)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt as _;
        std::fs::set_permissions(&new, std::fs::Permissions::from_mode(0o755))?;
    }
    if cfg!(windows) && dst.exists() {
        let ms = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map_or(0, |d| d.as_millis());
        let old = dir.join(format!("{name}.old-{ms}"));
        std::fs::rename(dst, &old)?;
        let _ = std::fs::remove_file(&old);
    }
    std::fs::rename(&new, dst).inspect_err(|_| {
        let _ = std::fs::remove_file(&new);
    })
}

/// version_of asks the tool at path its version (`agentbox --version`, which
/// answers "agentbox version 0.18.0"), given ten seconds.
pub fn version_of(path: &Path) -> Option<String> {
    use std::io::Read as _;
    use std::process::{Command, Stdio};
    let mut cmd = Command::new(path);
    cmd.arg("--version")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null());
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt as _;
        cmd.creation_flags(0x0800_0000); // CREATE_NO_WINDOW
    }
    let mut child = cmd.spawn().ok()?;
    let mut out = child.stdout.take()?;
    let reader = std::thread::spawn(move || {
        let mut s = String::new();
        out.read_to_string(&mut s).ok().map(|_| s)
    });
    let deadline = Instant::now() + Duration::from_secs(10);
    loop {
        match child.try_wait() {
            Ok(Some(status)) if status.success() => break,
            Ok(None) if Instant::now() < deadline => std::thread::sleep(Duration::from_millis(10)),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return None;
            }
        }
    }
    parse_version(&reader.join().ok().flatten()?)
}

/// parse_version reads `agentbox --version`'s answer.
fn parse_version(said: &str) -> Option<String> {
    said.trim()
        .strip_prefix("agentbox version ")
        .map(str::trim)
        .filter(|v| !v.is_empty() && !v.contains(char::is_whitespace))
        .map(str::to_string)
}

/// The version of the Electron app's managed copy, when it was left alone.
static LEFT: std::sync::OnceLock<Option<String>> = std::sync::OnceLock::new();

/// daemon_version is the version of the daemon this app starts: its own
/// (None for a build without one), or that of the tool the Electron app
/// keeps, when keep_managed_current left it.
pub fn daemon_version() -> Option<String> {
    LEFT.get()
        .cloned()
        .flatten()
        .or_else(|| crate::update::app_version().map(str::to_string))
}

/// keep_managed_current is what the app does as it starts, before it looks
/// for a daemon: the managed copy becomes this app's tool (keep_current). An
/// app with no tool of its own, or told which to use (AGENTBOX_BIN), leaves
/// it alone. It answers a line for the log.
pub fn keep_managed_current() -> Option<String> {
    let bundled = bundled()?;
    if std::env::var_os("AGENTBOX_BIN").is_some_and(|b| !b.is_empty()) {
        return None;
    }
    let dir = managed_dir(|k| std::env::var(k).ok())?;
    let at = dir.join(EXE);
    let at = at.display();
    let kept = keep_current(&bundled, &dir, crate::update::APP_VERSION, version_of);
    let _ = LEFT.set(match &kept {
        Ok(Kept::Left(v)) => Some(v.clone()),
        _ => None,
    });
    Some(match kept {
        Ok(Kept::Same) => format!("agentbox-native: {at} is this app's agentbox"),
        Ok(Kept::Written) => format!("agentbox-native: {at} is now this app's agentbox"),
        Ok(Kept::Left(v)) => {
            format!("agentbox-native: {at} is the AgentBox app's {v}: left as it is")
        }
        Err(e) => format!("agentbox-native: can't write {at}: {e}"),
    })
}

/// extract writes this app's tool into dir as it is built in, for packaging
/// to check (scripts/native-package.sh): `agentbox-native --extract-cli DIR`.
pub fn extract(dir: &Path) -> io::Result<()> {
    let b = bundled().ok_or_else(|| io::Error::other("this build has no agentbox built in"))?;
    write(b.cli, &dir.join(EXE))?;
    if let Some(linux) = b.linux {
        write(linux, &dir.join(LINUX))?;
    }
    Ok(())
}

/// comparable spells a version the way a release does: the app's build may
/// spell a nightly 0.18.0.20261106.179.
pub fn comparable(v: &str) -> String {
    let v = v.trim_start_matches('v');
    let n: Vec<&str> = v.split('.').collect();
    if !v.contains('-') && n.len() == 5 {
        format!("{}.{}.{}-nightly.{}.{}", n[0], n[1], n[2], n[3], n[4])
    } else {
        v.to_string()
    }
}

/// newer is whether version a is past b (nightly.ts isUpgrade): their
/// numbers compared in order, a prerelease before its release, and two
/// prereleases by their own parts, numbers as numbers (nightly.20261106.99
/// comes before nightly.20261106.179).
pub fn newer(a: &str, b: &str) -> bool {
    use std::cmp::Ordering;
    fn parts(v: &str) -> (Vec<u64>, &str) {
        let v = v.trim_start_matches('v');
        let (core, pre) = v.split_once('-').unwrap_or((v, ""));
        (
            core.split('.').map(|n| n.parse().unwrap_or(0)).collect(),
            pre,
        )
    }
    fn pre_cmp(a: &str, b: &str) -> Ordering {
        let (mut a, mut b) = (a.split('.'), b.split('.'));
        loop {
            let ord = match (a.next(), b.next()) {
                (None, None) => return Ordering::Equal,
                (None, Some(_)) => Ordering::Less,
                (Some(_), None) => Ordering::Greater,
                (Some(x), Some(y)) => match (x.parse::<u64>(), y.parse::<u64>()) {
                    (Ok(x), Ok(y)) => x.cmp(&y),
                    _ => x.cmp(y),
                },
            };
            if ord != Ordering::Equal {
                return ord;
            }
        }
    }
    if a.is_empty() || b.is_empty() {
        return false;
    }
    let ((ca, pa), (cb, pb)) = (parts(a), parts(b));
    match ca.cmp(&cb) {
        Ordering::Greater => true,
        Ordering::Less => false,
        Ordering::Equal => match (pa.is_empty(), pb.is_empty()) {
            (true, false) => true,
            (false, false) => pre_cmp(pa, pb) == Ordering::Greater,
            _ => false,
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const OLD: &str = "0.18.0-nightly.20261104.168";
    const OURS: &str = "0.18.0-nightly.20261106.179";
    const NEW: &str = "0.18.0-nightly.20261112.187";

    fn tool(bytes: &'static [u8]) -> Bundled {
        Bundled {
            cli: bytes,
            linux: None,
        }
    }

    /// says answers a copy's version from its contents: "agentbox <version>".
    fn says(path: &Path) -> Option<String> {
        let s = std::fs::read_to_string(path).ok()?;
        s.strip_prefix("agentbox ").map(str::to_string)
    }

    fn dir(name: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("abn-cli-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        d
    }

    fn files(d: &Path) -> Vec<String> {
        let mut names: Vec<String> = std::fs::read_dir(d)
            .unwrap()
            .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
            .collect();
        names.sort();
        names
    }

    #[test]
    fn a_missing_copy_is_written_and_can_run() {
        let d = dir("missing");
        let ours = tool(b"agentbox 0.18.0-nightly.20261106.179");
        assert_eq!(keep_current(&ours, &d, OURS, says).unwrap(), Kept::Written);
        assert_eq!(std::fs::read(d.join(EXE)).unwrap(), ours.cli);
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt as _;
            let mode = std::fs::metadata(d.join(EXE)).unwrap().permissions().mode();
            assert_eq!(mode & 0o111, 0o111);
        }
        // Nothing left beside it but who wrote it.
        assert_eq!(files(&d), vec![OWNER.to_string(), EXE.to_string()]);
        assert_eq!(std::fs::read_to_string(d.join(OWNER)).unwrap(), "native");
        std::fs::remove_dir_all(d).unwrap();
    }

    #[test]
    fn the_same_copy_is_not_written_again() {
        let d = dir("same");
        let ours = tool(b"agentbox 0.18.0-nightly.20261106.179");
        keep_current(&ours, &d, OURS, says).unwrap();
        let before = std::fs::metadata(d.join(EXE)).unwrap().modified().unwrap();
        // Its version is never asked: nothing differs.
        let never = |_: &Path| -> Option<String> { panic!("asked a copy that is the same") };
        assert_eq!(keep_current(&ours, &d, OURS, never).unwrap(), Kept::Same);
        let after = std::fs::metadata(d.join(EXE)).unwrap().modified().unwrap();
        assert_eq!(before, after);
        std::fs::remove_dir_all(d).unwrap();
    }

    #[test]
    fn an_older_copy_is_replaced_and_a_newer_one_wins() {
        let d = dir("versions");
        std::fs::create_dir_all(&d).unwrap();
        let ours = tool(b"agentbox 0.18.0-nightly.20261106.179");
        // The Electron app's, older: replaced.
        std::fs::write(d.join(EXE), format!("agentbox {OLD}")).unwrap();
        assert_eq!(keep_current(&ours, &d, OURS, says).unwrap(), Kept::Written);
        assert_eq!(std::fs::read(d.join(EXE)).unwrap(), ours.cli);
        // The Electron app's, newer: it stays, however often this app starts.
        std::fs::write(d.join(EXE), format!("agentbox {NEW}")).unwrap();
        std::fs::write(d.join(OWNER), "electron").unwrap();
        for _ in 0..2 {
            assert_eq!(
                keep_current(&ours, &d, OURS, says).unwrap(),
                Kept::Left(NEW.into())
            );
        }
        assert_eq!(
            std::fs::read_to_string(d.join(EXE)).unwrap(),
            format!("agentbox {NEW}")
        );
        // And the newer app, starting next, has nothing to write: no
        // ping-pong between the two.
        let theirs = tool(b"agentbox 0.18.0-nightly.20261112.187");
        assert_eq!(keep_current(&theirs, &d, NEW, says).unwrap(), Kept::Same);
        std::fs::remove_dir_all(d).unwrap();
    }

    #[test]
    fn a_newer_copy_this_app_wrote_itself_is_replaced() {
        // This app went back a version: the copy is its own, not the
        // Electron app's, so it gets the tool of the version it is now.
        let d = dir("own");
        let was = tool(b"agentbox 0.18.0-nightly.20261112.187");
        keep_current(&was, &d, NEW, says).unwrap();
        let ours = tool(b"agentbox 0.18.0-nightly.20261106.179");
        assert_eq!(keep_current(&ours, &d, OURS, says).unwrap(), Kept::Written);
        assert_eq!(std::fs::read(d.join(EXE)).unwrap(), ours.cli);
        // The Electron app wrote over it since: its newer one stays.
        std::fs::write(d.join(EXE), format!("agentbox {NEW}")).unwrap();
        std::fs::write(d.join(OWNER), "electron\n").unwrap();
        assert_eq!(
            keep_current(&ours, &d, OURS, says).unwrap(),
            Kept::Left(NEW.into())
        );
        // And this app's own, older than the Electron app's it replaces,
        // is this app's from then on.
        std::fs::write(d.join(EXE), format!("agentbox {OLD}")).unwrap();
        keep_current(&ours, &d, OURS, says).unwrap();
        assert_eq!(std::fs::read_to_string(d.join(OWNER)).unwrap(), "native");
        std::fs::remove_dir_all(d).unwrap();
    }

    #[test]
    fn the_electron_apps_build_of_the_same_version_stays() {
        // Two builds of one version differ in their bytes: were each app
        // to write its own, they would take turns for good.
        let d = dir("same-version");
        std::fs::create_dir_all(&d).unwrap();
        let ours = tool(b"agentbox 0.18.0-nightly.20261106.179");
        std::fs::write(d.join(EXE), format!("agentbox {OURS}\n")).unwrap();
        let theirs = |_: &Path| Some(OURS.to_string());
        assert_eq!(
            keep_current(&ours, &d, OURS, theirs).unwrap(),
            Kept::Left(OURS.into())
        );
        // This app's own other build of it (a rebuilt nightly) is replaced.
        std::fs::write(d.join(OWNER), "native").unwrap();
        assert_eq!(
            keep_current(&ours, &d, OURS, theirs).unwrap(),
            Kept::Written
        );
        std::fs::remove_dir_all(d).unwrap();
    }

    #[test]
    fn a_copy_that_does_not_say_its_version_is_replaced() {
        let d = dir("unknown");
        std::fs::create_dir_all(&d).unwrap();
        let ours = tool(b"agentbox 0.18.0-nightly.20261106.179");
        // Broken, or half written.
        std::fs::write(d.join(EXE), "garbage").unwrap();
        assert_eq!(keep_current(&ours, &d, OURS, says).unwrap(), Kept::Written);
        // A build from a checkout.
        std::fs::remove_file(d.join(OWNER)).unwrap();
        std::fs::write(d.join(EXE), "agentbox dev").unwrap();
        assert_eq!(keep_current(&ours, &d, OURS, says).unwrap(), Kept::Written);
        std::fs::remove_dir_all(d).unwrap();
    }

    #[test]
    fn the_linux_tool_follows_the_managed_copy() {
        let d = dir("linux");
        let ours = Bundled {
            cli: b"agentbox 0.18.0-nightly.20261106.179",
            linux: Some(b"linux 179"),
        };
        assert_eq!(keep_current(&ours, &d, OURS, says).unwrap(), Kept::Written);
        assert_eq!(std::fs::read(d.join(LINUX)).unwrap(), b"linux 179");
        // Only the Linux one differs: it alone is written.
        std::fs::write(d.join(LINUX), "linux 168").unwrap();
        assert_eq!(keep_current(&ours, &d, OURS, says).unwrap(), Kept::Written);
        assert_eq!(std::fs::read(d.join(LINUX)).unwrap(), b"linux 179");
        assert_eq!(keep_current(&ours, &d, OURS, says).unwrap(), Kept::Same);
        // A newer front end keeps its own Linux tool: both stay.
        std::fs::write(d.join(EXE), format!("agentbox {NEW}")).unwrap();
        std::fs::write(d.join(LINUX), "linux 187").unwrap();
        std::fs::write(d.join(OWNER), "electron").unwrap();
        assert_eq!(
            keep_current(&ours, &d, OURS, says).unwrap(),
            Kept::Left(NEW.into())
        );
        assert_eq!(std::fs::read(d.join(LINUX)).unwrap(), b"linux 187");
        std::fs::remove_dir_all(d).unwrap();
    }

    #[test]
    fn the_managed_copy_is_where_the_electron_app_keeps_it() {
        let env = |pairs: &'static [(&'static str, &'static str)]| {
            move |k: &str| {
                pairs
                    .iter()
                    .find(|(n, _)| *n == k)
                    .map(|(_, v)| v.to_string())
            }
        };
        if cfg!(windows) {
            assert_eq!(
                managed_dir(env(&[("LOCALAPPDATA", "L")])),
                Some(PathBuf::from("L").join("AgentBox").join("bin"))
            );
            return;
        }
        assert_eq!(
            managed_dir(env(&[("HOME", "/home/u")])),
            Some("/home/u/.local/share/agentbox/bin".into())
        );
        assert_eq!(
            managed_dir(env(&[("HOME", "/home/u"), ("XDG_DATA_HOME", "/data")])),
            Some("/data/agentbox/bin".into())
        );
        // A relative XDG_DATA_HOME is ignored, as paths.ts does.
        assert_eq!(
            managed_dir(env(&[("HOME", "/home/u"), ("XDG_DATA_HOME", "data")])),
            Some("/home/u/.local/share/agentbox/bin".into())
        );
        assert_eq!(managed_dir(env(&[])), None);
    }

    #[test]
    fn the_tools_answer_is_its_version() {
        assert_eq!(
            parse_version("agentbox version 0.18.0-nightly.20261106.179\n"),
            Some(OURS.into())
        );
        assert_eq!(parse_version("agentbox version dev\n"), Some("dev".into()));
        assert_eq!(parse_version("something else"), None);
        assert_eq!(parse_version("agentbox version "), None);
    }

    #[test]
    fn newer_compares_numbers_then_prereleases() {
        assert!(newer("0.17.0", "0.16.3"));
        assert!(newer("0.17.0", "0.17.0-nightly.20261030.156"));
        assert!(newer(
            "0.17.0-nightly.20261031.1",
            "0.17.0-nightly.20261030.156"
        ));
        // The run number is a number: 179 is past 99.
        assert!(newer(
            "0.18.0-nightly.20261106.179",
            "0.18.0-nightly.20261106.99"
        ));
        assert!(!newer(
            "0.18.0-nightly.20261106.99",
            "0.18.0-nightly.20261106.179"
        ));
        assert!(!newer("0.16.3", "0.17.0"));
        assert!(!newer("0.17.0", "0.17.0"));
        assert!(!newer("", "0.17.0"));
        // "dev" has no numbers: never newer than a release.
        assert!(!newer("dev", "0.17.0"));
        assert_eq!(comparable("0.18.0.20261106.179"), OURS);
        assert_eq!(comparable("v0.18.0"), "0.18.0");
    }
}
