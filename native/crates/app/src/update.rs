//! Updating the app in place, as the Electron app does
//! (desktop/src/main/appupdate.ts, updater.ts): the update button downloads
//! the new release's native build for this OS and architecture, checks it
//! against the release's SHA256SUMS, puts it where this copy of the app is,
//! and offers to restart into it. The daemon picks the release
//! (GET /v1/update/release, for its channel); this only fetches its files.
//!
//! # The release's native files
//!
//! Packaging must name them as [`asset_name`] does, and list each in the
//! release's `SHA256SUMS` (or `SHA256SUMS-mac`), `sha256sum`'s format:
//!
//! | install                       | asset                                          |
//! |-------------------------------|------------------------------------------------|
//! | Linux binary                  | `agentbox-native-<version>-linux-<arch>`        |
//! | Linux AppImage (`$APPIMAGE`)  | `agentbox-native-<version>-linux-<arch>.AppImage` |
//! | Windows `.exe`                | `agentbox-native-<version>-windows-<arch>.exe`  |
//! | Mac `.app` bundle             | `agentbox-native-<version>-mac-<arch>.app.tar.gz` |
//! | Mac binary outside a bundle   | `agentbox-native-<version>-mac-<arch>`          |
//!
//! `<version>` has no leading `v` (0.17.0, 0.17.0-nightly.20261030.156) and
//! `<arch>` is Node's (`x64`, `arm64`), as the Electron app's files have. The
//! Mac archive holds one `*.app` at its top whose `Contents/MacOS/` has a
//! binary of the running one's name. The nightly packages them
//! (scripts/native-package.sh) beside its user-facing files: the AppImage is
//! the Linux install that updates itself, the bare binary is the tarball's,
//! and a .deb install (under /usr) opens the release page instead. Until a release has these, the update
//! fails with "has no build for this machine" and offers the release page.
//!
//! # Putting it in place
//!
//! The download goes beside the app (same file system, so a rename moves it
//! in). Linux and a Mac binary: renamed over the running binary, which keeps
//! running from its old inode; an AppImage is the file `$APPIMAGE` names.
//! Windows can't replace a running .exe but can rename it: it goes aside as
//! `<exe>.old-<ms>`, removed at the next start ([`clean_up`]). A Mac bundle is
//! unpacked with `tar`, checked with `codesign --verify`, and swapped in by
//! renaming the old one to `.<App>.app.old`. Nothing needs a password: an app
//! in a folder the user can't write to opens the release page instead.
use std::collections::HashMap;
use std::io::{Read as _, Write as _};
use std::path::{Path, PathBuf};

use sha2::{Digest as _, Sha256};

/// APP_VERSION is this build's version: AGENTBOX_VERSION at build time, which
/// release packaging sets; the crate's 0.0.0 otherwise (a version unknown).
pub const APP_VERSION: &str = match option_env!("AGENTBOX_VERSION") {
    Some(v) => v,
    None => env!("CARGO_PKG_VERSION"),
};

/// app_version is APP_VERSION when one was set at build time.
pub fn app_version() -> Option<&'static str> {
    (APP_VERSION != "0.0.0" && !APP_VERSION.is_empty()).then_some(APP_VERSION)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    /// A plain executable: Linux, Windows, or a Mac one outside a bundle.
    Binary,
    AppImage,
    /// A Mac .app bundle (path is the bundle).
    MacApp,
}

/// Install is where this copy of the app is.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Install {
    pub kind: Kind,
    pub path: PathBuf,
    /// The running binary's file name (a Mac bundle's Contents/MacOS/ one).
    pub exe_name: String,
}

/// Support is whether this copy updates itself, or why it opens the release
/// page instead (shell.update.releasePage.<reason>).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Support {
    InPlace(Install),
    ReleasePage(&'static str),
}

/// Machine is what detect looks at, passed in for the tests.
pub struct Machine<'a> {
    pub os: &'a str,
    pub exe: PathBuf,
    pub appimage: Option<PathBuf>,
    pub writable: &'a dyn Fn(&Path) -> bool,
    pub exists: &'a dyn Fn(&Path) -> bool,
}

impl Machine<'_> {
    /// this is the running app.
    pub fn this() -> Machine<'static> {
        Machine {
            os: std::env::consts::OS,
            exe: std::env::current_exe()
                .and_then(|p| p.canonicalize())
                .unwrap_or_default(),
            appimage: std::env::var_os("APPIMAGE")
                .filter(|v| !v.is_empty())
                .map(PathBuf::from),
            writable: &writable,
            exists: &|p| p.exists(),
        }
    }
}

/// writable is whether a file can be made in dir.
fn writable(dir: &Path) -> bool {
    let probe = dir.join(format!(".agentbox-write-test-{}", std::process::id()));
    let ok = std::fs::File::create(&probe).is_ok();
    let _ = std::fs::remove_file(&probe);
    ok
}

pub fn detect(m: &Machine) -> Support {
    let Some(name) = m.exe.file_name().map(|n| n.to_string_lossy().into_owned()) else {
        return Support::ReleasePage("unknown");
    };
    // Cargo's target folder (it marks it with CACHEDIR.TAG): a build of the
    // repository, which `git pull` updates.
    if m.exe.ancestors().any(|a| {
        a.file_name().is_some_and(|n| n == "target") && (m.exists)(&a.join("CACHEDIR.TAG"))
    }) {
        return Support::ReleasePage("dev");
    }
    let yes = |kind, path: PathBuf, dir: &Path| {
        if (m.writable)(dir) {
            Support::InPlace(Install {
                kind,
                path,
                exe_name: name.clone(),
            })
        } else {
            Support::ReleasePage("notWritable")
        }
    };
    let dir = m.exe.parent().unwrap_or(Path::new("/"));
    match m.os {
        "linux" => {
            if let Some(image) = &m.appimage {
                return yes(Kind::AppImage, image.clone(), image.parent().unwrap_or(dir));
            }
            // Where a package manager puts what it installs.
            if m.exe.starts_with("/usr") || m.exe.starts_with("/opt") {
                return Support::ReleasePage("linuxPackage");
            }
            yes(Kind::Binary, m.exe.clone(), dir)
        }
        "macos" => {
            // …/AgentBox.app/Contents/MacOS/AgentBox
            let bundle = m.exe.ancestors().nth(3).map(Path::to_path_buf);
            let in_bundle = m
                .exe
                .parent()
                .is_some_and(|p| p.ends_with("Contents/MacOS"));
            match bundle {
                Some(app) if in_bundle && app.extension().is_some_and(|e| e == "app") => {
                    if app.starts_with("/Volumes") {
                        return Support::ReleasePage("macDiskImage");
                    }
                    if app.to_string_lossy().contains("/AppTranslocation/") {
                        return Support::ReleasePage("macTranslocated");
                    }
                    let parent = app.parent().unwrap_or(Path::new("/")).to_path_buf();
                    yes(Kind::MacApp, app, &parent)
                }
                _ => yes(Kind::Binary, m.exe.clone(), dir),
            }
        }
        "windows" => yes(Kind::Binary, m.exe.clone(), dir),
        _ => Support::ReleasePage("unknown"),
    }
}

/// arch is Node's name for this architecture, which the release's files use.
pub fn arch() -> &'static str {
    match std::env::consts::ARCH {
        "x86_64" => "x64",
        "aarch64" => "arm64",
        a => a,
    }
}

/// asset_name is the release's native file for an install (see the module's
/// table): os is Rust's (std::env::consts::OS), arch Node's.
pub fn asset_name(kind: Kind, version: &str, os: &str, arch: &str) -> String {
    let version = version.trim_start_matches('v');
    let os = match os {
        "macos" => "mac",
        o => o,
    };
    let ext = match (kind, os) {
        (Kind::AppImage, _) => ".AppImage",
        (Kind::MacApp, _) => ".app.tar.gz",
        (Kind::Binary, "windows") => ".exe",
        (Kind::Binary, _) => "",
    };
    format!("agentbox-native-{version}-{os}-{arch}{ext}")
}

/// parse_sums reads a SHA256SUMS: "<hex>  <name>", or "<hex> *<name>".
pub fn parse_sums(text: &str) -> HashMap<String, String> {
    text.lines()
        .filter_map(|l| {
            let l = l.trim();
            let (hex, rest) = l.split_at_checked(64)?;
            let name = rest.strip_prefix(' ')?;
            let name = name.strip_prefix([' ', '*'])?;
            (hex.bytes().all(|b| b.is_ascii_hexdigit()) && !name.is_empty())
                .then(|| (name.to_string(), hex.to_ascii_lowercase()))
        })
        .collect()
}

/// Failure is why an update stopped: code is shell.update.error.<code>.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Failure {
    pub code: &'static str,
    pub detail: String,
    /// The release's page, when one is known.
    pub url: Option<String>,
}

impl Failure {
    pub fn new(code: &'static str, detail: impl Into<String>) -> Self {
        Self {
            code,
            detail: detail.into(),
            url: None,
        }
    }
    fn at(mut self, url: &str) -> Self {
        if !url.is_empty() {
            self.url = Some(url.to_string());
        }
        self
    }
}

/// Plan is what to download, worked out from the release.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Plan {
    pub version: String,
    pub page: String,
    pub name: String,
    pub url: String,
    pub size: u64,
    /// SHA256SUMS files' URLs, in the order to look in.
    pub sums: Vec<String>,
    pub install: Install,
}

/// The sums files a release has: SHA256SUMS for everything a nightly has and
/// a release's Linux and Windows files, SHA256SUMS-mac for its Mac files.
const SUMS_FILES: [&str; 2] = ["SHA256SUMS", "SHA256SUMS-mac"];

/// is_prerelease is a version with a -suffix (a nightly).
fn is_prerelease(v: &str) -> bool {
    v.contains('-')
}

/// plan picks the release's file for install (updater.ts's start, after the
/// daemon has answered): current is this app's version, offered the version
/// the daemon's update check offers.
pub fn plan(
    release: &agentbox_api::UpdateRelease,
    install: &Install,
    current: &str,
    offered: Option<&str>,
) -> Result<Plan, Failure> {
    let version = release.version.trim_start_matches('v').to_string();
    let current = current.trim_start_matches('v');
    if version == current {
        return Err(Failure::new("upToDate", version).at(&release.url));
    }
    // A nightly goes back to a stable release only when the daemon offers
    // it (after the user switched to the stable channel).
    if is_prerelease(current) && !is_prerelease(&version) && offered != Some(version.as_str()) {
        return Err(Failure::new("upToDate", current).at(&release.url));
    }
    if release.assets.is_empty() {
        return Err(Failure::new(
            "noAssets",
            format!("the release list has no files for {version}"),
        )
        .at(&release.url));
    }
    let name = asset_name(install.kind, &version, std::env::consts::OS, arch());
    let Some(asset) = release.assets.iter().find(|a| a.name == name) else {
        return Err(
            Failure::new("noBuild", format!("{version} has no native build ({name})"))
                .at(&release.url),
        );
    };
    let sums: Vec<String> = SUMS_FILES
        .iter()
        .filter_map(|f| release.assets.iter().find(|a| a.name == *f))
        .map(|a| a.url.clone())
        .collect();
    if sums.is_empty() {
        return Err(Failure::new("noChecksum", "").at(&release.url));
    }
    Ok(Plan {
        version,
        page: release.url.clone(),
        name,
        url: asset.url.clone(),
        size: asset.size.max(0) as u64,
        sums,
        install: install.clone(),
    })
}

/// Phase is how far an update got (shared/appupdate.ts's AppUpdateProgress).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Phase {
    Preparing,
    Download {
        received: u64,
        total: u64,
    },
    Verify,
    Install,
    /// Installed: restarting starts this binary.
    Ready {
        version: String,
        relaunch: PathBuf,
    },
}

fn agent() -> ureq::Agent {
    let tls = ureq::tls::TlsConfig::builder();
    #[cfg(not(target_os = "linux"))]
    let tls = tls
        .provider(ureq::tls::TlsProvider::NativeTls)
        .root_certs(ureq::tls::RootCerts::PlatformVerifier);
    ureq::Agent::config_builder()
        .tls_config(tls.build())
        .timeout_connect(Some(std::time::Duration::from_secs(30)))
        .build()
        .into()
}

fn get(agent: &ureq::Agent, url: &str) -> Result<ureq::http::Response<ureq::Body>, String> {
    agent.get(url).call().map_err(|e| format!("{url}: {e}"))
}

/// expected_sum is name's SHA256 as the release's sums files list it.
fn expected_sum(agent: &ureq::Agent, plan: &Plan) -> Result<String, Failure> {
    for url in &plan.sums {
        let text = get(agent, url)
            .and_then(|mut r| r.body_mut().read_to_string().map_err(|e| e.to_string()))
            .map_err(|e| Failure::new("download", e))?;
        if let Some(sum) = parse_sums(&text).get(&plan.name) {
            return Ok(sum.clone());
        }
    }
    Err(Failure::new("noChecksum", ""))
}

/// download_path is where the download goes: beside the app.
fn download_path(install: &Install, name: &str) -> PathBuf {
    let dir = install.path.parent().unwrap_or(Path::new("."));
    let base = install
        .path
        .file_name()
        .unwrap_or_default()
        .to_string_lossy();
    match install.kind {
        Kind::MacApp => dir.join(format!(".{base}.update-{name}")),
        _ => dir.join(format!(".{base}.update")),
    }
}

/// download writes url to dest, returning its SHA256: no more than size bytes
/// when the release says how big it is.
fn download(
    agent: &ureq::Agent,
    url: &str,
    dest: &Path,
    size: u64,
    progress: &dyn Fn(Phase),
) -> Result<String, Failure> {
    let mut res = get(agent, url).map_err(|e| Failure::new("download", e))?;
    let total = if size > 0 {
        size
    } else {
        res.headers()
            .get("content-length")
            .and_then(|v| v.to_str().ok()?.parse().ok())
            .unwrap_or(0)
    };
    let mut reader = res.body_mut().with_config().limit(u64::MAX).reader();
    let mut file = std::fs::File::create(dest)
        .map_err(|e| Failure::new("download", format!("{}: {e}", dest.display())))?;
    let mut hash = Sha256::new();
    let mut buf = vec![0u8; 256 << 10];
    let (mut received, mut reported) = (0u64, 0u64);
    let step = (total / 100).max(256 << 10);
    let result = loop {
        let n = match reader.read(&mut buf) {
            Ok(0) => break Ok(()),
            Ok(n) => n,
            Err(e) => break Err(Failure::new("download", e.to_string())),
        };
        received += n as u64;
        if size > 0 && received > size {
            break Err(Failure::new(
                "checksum",
                format!("the download is larger than the {size} bytes the release lists"),
            ));
        }
        hash.update(&buf[..n]);
        if let Err(e) = file.write_all(&buf[..n]) {
            break Err(Failure::new("download", e.to_string()));
        }
        if received - reported >= step || received == total {
            reported = received;
            progress(Phase::Download { received, total });
        }
    };
    drop(file);
    if let Err(e) = result {
        let _ = std::fs::remove_file(dest);
        return Err(e);
    }
    Ok(format!("{:x}", hash.finalize()))
}

/// apply downloads the plan's file, checks it and puts it in place, saying
/// how far it got; it blocks, so it runs on a thread of its own. It returns
/// the binary to restart into.
pub fn apply(plan: &Plan, progress: &dyn Fn(Phase)) -> Result<PathBuf, Failure> {
    let agent = agent();
    let page = plan.page.clone();
    let run = || -> Result<PathBuf, Failure> {
        let want = expected_sum(&agent, plan)?;
        let dest = download_path(&plan.install, &plan.name);
        progress(Phase::Download {
            received: 0,
            total: plan.size,
        });
        let got = download(&agent, &plan.url, &dest, plan.size, progress)?;
        progress(Phase::Verify);
        if got != want {
            let _ = std::fs::remove_file(&dest);
            return Err(Failure::new(
                "checksum",
                format!(
                    "{}'s SHA256 is {got}, and SHA256SUMS says {want}",
                    plan.name
                ),
            ));
        }
        progress(Phase::Install);
        install(&plan.install, &dest, &plan.version)
    };
    run().map_err(|f| f.at(&page))
}

/// install puts the checked download in place of the install, returning the
/// binary to restart into.
pub fn install(install: &Install, download: &Path, version: &str) -> Result<PathBuf, Failure> {
    let failed = |e: std::io::Error| Failure::new("install", e.to_string());
    match install.kind {
        Kind::Binary | Kind::AppImage => {
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt as _;
                std::fs::set_permissions(download, std::fs::Permissions::from_mode(0o755))
                    .map_err(failed)?;
            }
            swap(download, &install.path).map_err(|e| {
                let _ = std::fs::remove_file(download);
                failed(e)
            })?;
            Ok(install.path.clone())
        }
        Kind::MacApp => {
            let staging = mac_staging(&install.path);
            let fresh = unpack_mac_app(download, &install.path, &install.exe_name, version);
            let _ = std::fs::remove_file(download);
            let fresh = fresh.inspect_err(|_| {
                let _ = std::fs::remove_dir_all(&staging);
            })?;
            let old = mac_old(&install.path);
            let _ = std::fs::remove_dir_all(&old);
            std::fs::rename(&install.path, &old).map_err(failed)?;
            if let Err(e) = std::fs::rename(&fresh, &install.path) {
                let _ = std::fs::rename(&old, &install.path);
                return Err(failed(e));
            }
            let _ = std::fs::remove_dir_all(&staging);
            Ok(install.path.join("Contents/MacOS").join(&install.exe_name))
        }
    }
}

/// swap moves new over path. Windows won't replace a running .exe, but
/// renames it: it goes aside first, and back should the new one not move in.
fn swap(new: &Path, path: &Path) -> std::io::Result<()> {
    if cfg!(windows) && path.exists() {
        let millis = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_millis();
        let old = PathBuf::from(format!("{}.old-{millis}", path.display()));
        std::fs::rename(path, &old)?;
        return std::fs::rename(new, path).inspect_err(|_| {
            let _ = std::fs::rename(&old, path);
        });
    }
    std::fs::rename(new, path)
}

fn sibling(app: &Path, suffix: &str) -> PathBuf {
    let name = app.file_name().unwrap_or_default().to_string_lossy();
    app.with_file_name(format!(".{name}.{suffix}"))
}
fn mac_staging(app: &Path) -> PathBuf {
    sibling(app, "update")
}
fn mac_old(app: &Path) -> PathBuf {
    sibling(app, "old")
}

/// unpack_mac_app unpacks the release's .app.tar.gz beside the app and checks
/// what's in it: one .app with a binary of this one's name, whose signature
/// verifies (on a Mac).
fn unpack_mac_app(
    archive: &Path,
    app: &Path,
    exe_name: &str,
    version: &str,
) -> Result<PathBuf, Failure> {
    let staging = mac_staging(app);
    let _ = std::fs::remove_dir_all(&staging);
    std::fs::create_dir_all(&staging).map_err(|e| Failure::new("install", e.to_string()))?;
    let out = std::process::Command::new("tar")
        .arg("-xzf")
        .arg(archive)
        .arg("-C")
        .arg(&staging)
        .output()
        .map_err(|e| Failure::new("install", format!("tar: {e}")))?;
    if !out.status.success() {
        return Err(Failure::new(
            "install",
            format!("unpacking: {}", String::from_utf8_lossy(&out.stderr).trim()),
        ));
    }
    let fresh = std::fs::read_dir(&staging)
        .ok()
        .and_then(|d| {
            d.flatten()
                .map(|e| e.path())
                .find(|p| p.extension().is_some_and(|e| e == "app"))
        })
        .ok_or_else(|| Failure::new("signature", "the archive has no app in it"))?;
    if !fresh.join("Contents/MacOS").join(exe_name).is_file() {
        return Err(Failure::new(
            "signature",
            format!("the new app has no Contents/MacOS/{exe_name}"),
        ));
    }
    if cfg!(target_os = "macos") {
        let out = std::process::Command::new("codesign")
            .args(["--verify", "--deep", "--strict"])
            .arg(&fresh)
            .output()
            .map_err(|e| Failure::new("signature", format!("codesign: {e}")))?;
        if !out.status.success() {
            return Err(Failure::new(
                "signature",
                format!(
                    "{version}'s signature doesn't verify: {}",
                    String::from_utf8_lossy(&out.stderr).trim()
                ),
            ));
        }
    }
    Ok(fresh)
}

/// clean_up removes what an earlier update left beside the app: a Windows
/// .exe renamed aside (still locked while that one ran), an old Mac bundle,
/// a download cut short.
pub fn clean_up(support: &Support) {
    let Support::InPlace(install) = support else {
        return;
    };
    let Some(dir) = install.path.parent() else {
        return;
    };
    let name = install
        .path
        .file_name()
        .unwrap_or_default()
        .to_string_lossy()
        .into_owned();
    let Ok(entries) = std::fs::read_dir(dir) else {
        return;
    };
    for e in entries.flatten() {
        let n = e.file_name().to_string_lossy().into_owned();
        let leftover = n.starts_with(&format!("{name}.old-"))
            || n == format!(".{name}.old")
            || n.starts_with(&format!(".{name}.update"));
        if leftover {
            let p = e.path();
            let _ = if p.is_dir() {
                std::fs::remove_dir_all(&p)
            } else {
                std::fs::remove_file(&p)
            };
        }
    }
}

/// RESTART_LOCK is the variable naming the file the old app holds locked
/// until it has exited, which the new one waits for ([`wait_for_previous`]).
const RESTART_LOCK: &str = "AGENTBOX_NATIVE_RESTART_LOCK";

/// WAIT_FOR_PREVIOUS is how long the new app waits for the old one to go;
/// quitting ends it within quit.rs's GRACE.
const WAIT_FOR_PREVIOUS: std::time::Duration = std::time::Duration::from_secs(15);

/// The lock this app holds for the one it restarts into: taken once, and
/// let go of only by exiting (the system drops it with the process).
static HELD: std::sync::Mutex<Option<(PathBuf, std::fs::File)>> = std::sync::Mutex::new(None);

fn hold_restart_lock() -> std::io::Result<PathBuf> {
    let mut held = HELD.lock().unwrap_or_else(|e| e.into_inner());
    if let Some((path, _)) = held.as_ref() {
        return Ok(path.clone());
    }
    let path = std::env::temp_dir().join(format!(
        "agentbox-native-restart-{}.lock",
        std::process::id()
    ));
    let file = std::fs::OpenOptions::new()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(&path)?;
    file.lock()?;
    *held = Some((path.clone(), file));
    Ok(path)
}

/// restart starts the new binary with this one's arguments, on its own;
/// the caller quits. The new one waits until this one has exited before it
/// opens a window ([`wait_for_previous`]): two copies never run at once.
pub fn restart(relaunch: &Path) -> std::io::Result<()> {
    let lock = hold_restart_lock()?;
    let mut cmd = std::process::Command::new(relaunch);
    cmd.args(std::env::args_os().skip(1))
        .env(RESTART_LOCK, &lock)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null());
    // stderr is this one's: the new version's lines follow the old one's in
    // the same log (main.rs says which version starts, and after what).
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
    cmd.spawn().map(drop)
}

/// wait_for_previous is the first thing a restarted app does: it waits for
/// the app that started it to exit (its lock to go), for up to
/// WAIT_FOR_PREVIOUS. It says how long it waited, None when this app wasn't
/// restarted into. Call it before any thread starts: it unsets the variable.
pub fn wait_for_previous() -> Option<std::time::Duration> {
    let path = std::env::var_os(RESTART_LOCK)?;
    // SAFETY: main calls this before any other thread starts.
    unsafe { std::env::remove_var(RESTART_LOCK) };
    Some(wait_for_lock(Path::new(&path), WAIT_FOR_PREVIOUS))
}

fn wait_for_lock(path: &Path, limit: std::time::Duration) -> std::time::Duration {
    let start = std::time::Instant::now();
    let Ok(file) = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(path)
    else {
        return start.elapsed();
    };
    while start.elapsed() < limit {
        match file.try_lock() {
            Err(std::fs::TryLockError::WouldBlock) => {
                std::thread::sleep(std::time::Duration::from_millis(25))
            }
            _ => break,
        }
    }
    drop(file);
    let _ = std::fs::remove_file(path);
    start.elapsed()
}

#[cfg(test)]
mod tests {
    use super::*;
    use agentbox_api::{ReleaseAsset, UpdateRelease};

    fn machine<'a>(
        os: &'a str,
        exe: &str,
        writable: &'a dyn Fn(&Path) -> bool,
        exists: &'a dyn Fn(&Path) -> bool,
    ) -> Machine<'a> {
        Machine {
            os,
            exe: exe.into(),
            appimage: None,
            writable,
            exists,
        }
    }

    #[test]
    fn where_the_app_is_says_how_it_updates() {
        let yes = |_: &Path| true;
        let no = |_: &Path| false;
        let kind = |s: Support| match s {
            Support::InPlace(i) => Ok((i.kind, i.path)),
            Support::ReleasePage(r) => Err(r),
        };
        let bin = machine("linux", "/home/u/bin/agentbox-native", &yes, &no);
        assert_eq!(
            kind(detect(&bin)),
            Ok((Kind::Binary, "/home/u/bin/agentbox-native".into()))
        );
        let pkg = machine("linux", "/usr/bin/agentbox-native", &yes, &no);
        assert_eq!(kind(detect(&pkg)), Err("linuxPackage"));
        let mut image = machine("linux", "/tmp/.mount_x/usr/bin/agentbox-native", &yes, &no);
        image.appimage = Some("/home/u/AgentBox.AppImage".into());
        assert_eq!(
            kind(detect(&image)),
            Ok((Kind::AppImage, "/home/u/AgentBox.AppImage".into()))
        );
        let ro = machine("linux", "/home/u/bin/agentbox-native", &no, &no);
        assert_eq!(kind(detect(&ro)), Err("notWritable"));
        let tag = |p: &Path| p.ends_with("target/CACHEDIR.TAG");
        let dev = machine(
            "linux",
            "/src/native/target/release/agentbox-native",
            &yes,
            &tag,
        );
        assert_eq!(kind(detect(&dev)), Err("dev"));
        let app = "/Applications/AgentBox.app/Contents/MacOS/AgentBox";
        assert_eq!(
            kind(detect(&machine("macos", app, &yes, &no))),
            Ok((Kind::MacApp, "/Applications/AgentBox.app".into()))
        );
        let dmg = "/Volumes/AgentBox/AgentBox.app/Contents/MacOS/AgentBox";
        assert_eq!(
            kind(detect(&machine("macos", dmg, &yes, &no))),
            Err("macDiskImage")
        );
        let win = r"C:\Users\u\AgentBox\agentbox-native.exe";
        assert!(matches!(
            kind(detect(&machine("windows", win, &yes, &no))),
            Ok((Kind::Binary, _))
        ));
    }

    #[test]
    fn asset_names_follow_the_table() {
        assert_eq!(
            asset_name(Kind::Binary, "v0.17.0", "linux", "x64"),
            "agentbox-native-0.17.0-linux-x64"
        );
        assert_eq!(
            asset_name(Kind::Binary, "0.17.0", "windows", "arm64"),
            "agentbox-native-0.17.0-windows-arm64.exe"
        );
        assert_eq!(
            asset_name(Kind::AppImage, "0.17.0", "linux", "x64"),
            "agentbox-native-0.17.0-linux-x64.AppImage"
        );
        assert_eq!(
            asset_name(Kind::MacApp, "0.17.0", "macos", "arm64"),
            "agentbox-native-0.17.0-mac-arm64.app.tar.gz"
        );
    }

    #[test]
    fn sums_are_read_in_both_modes() {
        let a = "a".repeat(64);
        let b = "B".repeat(64);
        let s = parse_sums(&format!("{a}  one\r\n{b} *two\nnot a line\n{a} three\n"));
        assert_eq!(s.get("one"), Some(&a));
        assert_eq!(s.get("two"), Some(&"b".repeat(64)));
        assert_eq!(s.len(), 2);
    }

    fn release(version: &str, names: &[&str]) -> UpdateRelease {
        UpdateRelease {
            version: version.into(),
            url: "https://example.com/r".into(),
            assets: names
                .iter()
                .map(|n| ReleaseAsset {
                    name: n.to_string(),
                    url: format!("https://example.com/{n}"),
                    size: 3,
                })
                .collect(),
        }
    }

    /// The nightly's native jobs package the files the update asks for
    /// (scripts/native-package.sh), under the names asset_name gives them.
    /// Each native build of the nightly is stamped with its version: without
    /// it the app takes the daemon's for its own and offers again the update
    /// it has just installed.
    #[test]
    fn the_nightly_stamps_each_native_build_with_its_version() {
        let flow = include_str!("../../../../.github/workflows/nightly.yml");
        let jobs: Vec<&str> = flow.split("\n  native-").skip(1).collect();
        assert_eq!(jobs.len(), 3, "native-linux, native-windows, native-macos");
        for job in jobs {
            let job = job.split("\n  publish:").next().unwrap();
            assert!(job.contains("cargo build --release"), "{job}");
            assert!(
                job.contains("AGENTBOX_VERSION: ${{ needs.resolve.outputs.version }}"),
                "native-{} builds without AGENTBOX_VERSION",
                job.lines().next().unwrap_or_default()
            );
        }
    }

    /// A restarted app waits for its lock while the old one holds it, and no
    /// longer.
    #[test]
    fn a_restart_waits_for_the_old_app_to_let_go() {
        let dir = std::env::temp_dir().join(format!("restart-lock-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("held.lock");
        let held = std::fs::File::create(&path).unwrap();
        held.lock().unwrap();
        let release = std::thread::spawn(move || {
            std::thread::sleep(std::time::Duration::from_millis(300));
            drop(held);
        });
        let waited = wait_for_lock(&path, std::time::Duration::from_secs(10));
        release.join().unwrap();
        assert!(
            waited >= std::time::Duration::from_millis(250),
            "{waited:?}"
        );
        assert!(waited < std::time::Duration::from_secs(5), "{waited:?}");
        assert!(!path.exists(), "the lock file is removed");
        // Nobody holding it, or no file at all: no wait.
        std::fs::File::create(&path).unwrap();
        assert!(
            wait_for_lock(&path, std::time::Duration::from_secs(10))
                < std::time::Duration::from_secs(1)
        );
        assert!(
            wait_for_lock(&path, std::time::Duration::from_secs(10))
                < std::time::Duration::from_secs(1)
        );
        // Taken twice by one app (a restart tried again), it's the same lock.
        assert_eq!(hold_restart_lock().unwrap(), hold_restart_lock().unwrap());
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn release_files_match_the_packaging() {
        let script = include_str!("../../../../scripts/native-package.sh");
        let flow = include_str!("../../../../.github/workflows/nightly.yml");
        let v = "0.18.0-nightly.1";
        // The script builds each from $version, with Node's arch.
        for (kind, os, arch, line) in [
            (
                Kind::Binary,
                "linux",
                "x64",
                "\"$out/agentbox-native-$version-linux-$node\"",
            ),
            (
                Kind::AppImage,
                "linux",
                "x64",
                "\"$out/agentbox-native-$version-linux-$node.AppImage\"",
            ),
            (
                Kind::Binary,
                "windows",
                "x64",
                "\"$out/agentbox-native-$version-windows-x64.exe\"",
            ),
            (
                Kind::MacApp,
                "macos",
                "arm64",
                "\"$out/agentbox-native-$version-mac-$arch.app.tar.gz\"",
            ),
        ] {
            assert!(script.contains(line), "native-package.sh lacks {line}");
            let want = asset_name(kind, v, os, arch);
            let built = line
                .trim_matches('"')
                .trim_start_matches("$out/")
                .replace("$version", v)
                .replace("$node", arch)
                .replace("$arch", arch);
            assert_eq!(built, want);
        }
        // SHA256SUMS is taken over agentbox-*, which these start with, and the
        // Mac's bundle is not left to SHA256SUMS-mac (the publish job drops it).
        assert!(flow.contains("sha256sum AgentBox-* agentbox-* >SHA256SUMS"));
        assert!(asset_name(Kind::Binary, v, "linux", "x64").starts_with("agentbox-"));
        // Not the Electron app's, install.sh's or the download button's.
        for n in [
            asset_name(Kind::Binary, v, "linux", "x64"),
            asset_name(Kind::Binary, v, "windows", "x64"),
            asset_name(Kind::MacApp, v, "macos", "arm64"),
        ] {
            assert!(!n.starts_with("AgentBox-"), "{n}");
        }
    }

    #[test]
    fn a_release_without_a_native_build_offers_its_page() {
        let install = Install {
            kind: Kind::Binary,
            path: "/x/agentbox-native".into(),
            exe_name: "agentbox-native".into(),
        };
        let r = release("0.18.0", &["AgentBox-0.18.0-x86_64.AppImage", "SHA256SUMS"]);
        let f = plan(&r, &install, "0.17.0", None).unwrap_err();
        assert_eq!(f.code, "noBuild");
        assert_eq!(f.url.as_deref(), Some("https://example.com/r"));
        assert_eq!(
            plan(&r, &install, "0.18.0", None).unwrap_err().code,
            "upToDate"
        );
        // A nightly isn't moved back to a release the daemon doesn't offer.
        let n = "0.18.0-nightly.1";
        assert_eq!(plan(&r, &install, n, None).unwrap_err().code, "upToDate");
        let name = asset_name(Kind::Binary, "0.18.0", std::env::consts::OS, arch());
        let r = release("0.18.0", &[&name]);
        assert_eq!(
            plan(&r, &install, "0.17.0", None).unwrap_err().code,
            "noChecksum"
        );
        let r = release("0.18.0", &[&name, "SHA256SUMS"]);
        let p = plan(&r, &install, n, Some("0.18.0")).unwrap();
        assert_eq!(p.url, format!("https://example.com/{name}"));
        assert_eq!(p.sums, ["https://example.com/SHA256SUMS"]);
    }

    /// serve answers each path of files over HTTP on a port of its own.
    fn serve(files: Vec<(String, Vec<u8>)>) -> String {
        use std::io::{BufRead as _, BufReader};
        let ln = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = ln.local_addr().unwrap();
        std::thread::spawn(move || {
            for c in ln.incoming().flatten() {
                let mut r = BufReader::new(c.try_clone().unwrap());
                let mut line = String::new();
                r.read_line(&mut line).unwrap();
                let path = line.split(' ').nth(1).unwrap_or("").to_string();
                loop {
                    let mut h = String::new();
                    if r.read_line(&mut h).unwrap_or(0) <= 2 {
                        break;
                    }
                }
                let mut c = c;
                match files.iter().find(|(p, _)| *p == path) {
                    Some((_, body)) => {
                        let _ = write!(
                            c,
                            "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                            body.len()
                        );
                        let _ = c.write_all(body);
                    }
                    None => {
                        let _ = c.write_all(b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
                    }
                }
            }
        });
        format!("http://{addr}")
    }

    fn sha(b: &[u8]) -> String {
        format!("{:x}", Sha256::digest(b))
    }

    #[test]
    fn an_update_replaces_the_binary_after_checking_it() {
        let dir = std::env::temp_dir().join(format!("abx-update-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let exe = dir.join("agentbox-native");
        std::fs::write(&exe, b"old").unwrap();
        let new = b"#!/bin/sh\necho new\n".to_vec();
        let base = serve(vec![
            ("/bin".into(), new.clone()),
            ("/bad".into(), b"tampered".to_vec()),
            (
                "/SHA256SUMS".into(),
                format!("{}  app\n{}  bad\n", sha(&new), sha(&new)).into_bytes(),
            ),
        ]);
        let install = Install {
            kind: Kind::Binary,
            path: exe.clone(),
            exe_name: "agentbox-native".into(),
        };
        let mut p = Plan {
            version: "0.18.0".into(),
            page: "https://example.com/r".into(),
            name: "app".into(),
            url: format!("{base}/bin"),
            size: new.len() as u64,
            sums: vec![format!("{base}/SHA256SUMS")],
            install,
        };
        let seen = std::cell::RefCell::new(Vec::new());
        let progress = |ph: Phase| seen.borrow_mut().push(ph);
        let relaunch = apply(&p, &progress).unwrap();
        assert_eq!(relaunch, exe);
        assert_eq!(std::fs::read(&exe).unwrap(), new);
        assert!(seen.borrow().contains(&Phase::Verify));
        assert!(seen.borrow().contains(&Phase::Download {
            received: new.len() as u64,
            total: new.len() as u64
        }));
        // A download that doesn't match SHA256SUMS is thrown away.
        std::fs::write(&exe, b"old").unwrap();
        p.name = "bad".into();
        p.url = format!("{base}/bad");
        p.size = 0;
        let f = apply(&p, &|_| {}).unwrap_err();
        assert_eq!(f.code, "checksum");
        assert_eq!(f.url.as_deref(), Some("https://example.com/r"));
        assert_eq!(std::fs::read(&exe).unwrap(), b"old");
        assert!(!download_path(&p.install, "bad").exists());
        // One SHA256SUMS doesn't list.
        p.name = "missing".into();
        assert_eq!(apply(&p, &|_| {}).unwrap_err().code, "noChecksum");
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn a_mac_bundle_is_swapped_whole() {
        let dir = std::env::temp_dir().join(format!("abx-macupdate-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let app = dir.join("AgentBox.app");
        std::fs::create_dir_all(app.join("Contents/MacOS")).unwrap();
        std::fs::write(app.join("Contents/MacOS/AgentBox"), b"old").unwrap();
        let src = dir.join("src/AgentBox.app/Contents/MacOS");
        std::fs::create_dir_all(&src).unwrap();
        std::fs::write(src.join("AgentBox"), b"new").unwrap();
        let archive = dir.join("new.tar.gz");
        let ok = std::process::Command::new("tar")
            .arg("-czf")
            .arg(&archive)
            .arg("-C")
            .arg(dir.join("src"))
            .arg("AgentBox.app")
            .status()
            .unwrap();
        assert!(ok.success());
        let install = Install {
            kind: Kind::MacApp,
            path: app.clone(),
            exe_name: "AgentBox".into(),
        };
        let relaunch = super::install(&install, &archive, "0.18.0").unwrap();
        assert_eq!(relaunch, app.join("Contents/MacOS/AgentBox"));
        assert_eq!(std::fs::read(&relaunch).unwrap(), b"new");
        assert!(mac_old(&app).exists());
        clean_up(&Support::InPlace(install));
        assert!(!mac_old(&app).exists());
        assert!(app.exists());
        let _ = std::fs::remove_dir_all(&dir);
    }
}
