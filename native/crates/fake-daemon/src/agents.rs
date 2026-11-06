//! What the real daemon can only answer about an agent with its machine
//! (Incus): its worktree's files (internal/daemon/filetree.go), its lifecycle
//! (start, stop, pause, resume, retire, destroy, rename, fork), snapshots and
//! checkpoints, its desktop's status and recordings, its timeline, its disk,
//! and what the machines use. Each answers in the Go handler's JSON, and an
//! action changes the fixtures and says so with an agent event, as the
//! daemon does. The worktree is this machine's checkout (--worktree, else
//! the current directory), read-only, with a few changes made up on top.
use std::collections::{BTreeMap, HashMap};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use agentbox_api::*;
use base64::Engine as _;
use bytes::Bytes;
use http_body_util::{BodyExt, Full};
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, bad_request, json, not_found, now, rand_id};

/// Folders a checkout's .gitignore leaves out, never listed.
pub const IGNORED: &[&str] = &[".git", "target", "node_modules", "bin", "dist", ".cache"];

/// The files the fixtures' worktrees changed since their base, and how.
const CHANGES: &[(&str, &str)] = &[
    ("README.md", FILE_MODIFIED),
    ("internal/daemon/filetree.go", FILE_MODIFIED),
    ("native/crates/app/src/files.rs", FILE_ADDED),
    ("docs/notes-from-agent.md", FILE_UNTRACKED),
];

/// The most of a file read sends unless asked for all (filetree.go's limit).
const READ_LIMIT: usize = 256 * 1024;

#[derive(Default)]
pub struct Machines {
    pub root: PathBuf,
    snapshots: HashMap<String, Vec<Snapshot>>,
    recording: HashMap<String, RecordingStatus>,
    /// Captures taken here, by agent name.
    pub extra_media: HashMap<String, Vec<MediaItem>>,
    /// Flips each time the worktree "changes", for EventFiles.
    touched: bool,
}

impl Machines {
    pub fn new(root: PathBuf) -> Self {
        Self {
            root,
            ..Default::default()
        }
    }

    fn snapshots_of(&mut self, r#ref: &str) -> &mut Vec<Snapshot> {
        self.snapshots.entry(r#ref.into()).or_insert_with(|| {
            ["before-refactor", "tests-green"]
                .iter()
                .enumerate()
                .map(|(i, n)| Snapshot {
                    name: n.to_string(),
                    created_at: ago(Duration::from_secs((3 - i as u64) * 3600)),
                    head: sha(i as u64 + 3),
                })
                .collect()
        })
    }
}

/// sha is a commit id made up from n.
fn sha(n: u64) -> String {
    let a = n.wrapping_mul(0x9E37_79B9_7F4A_7C15);
    format!(
        "{a:016x}{:016x}{:08x}",
        a.rotate_left(17) ^ n,
        (a >> 7) as u32
    )
}

fn ago(d: Duration) -> String {
    humantime::format_rfc3339_millis(std::time::SystemTime::now() - d).to_string()
}

fn no_content() -> Response<Body> {
    Response::builder()
        .status(StatusCode::NO_CONTENT)
        .body(Full::new(Bytes::new()).boxed())
        .unwrap()
}

/// as_agent is a fleet entry as GET /v1/agents/<p>/<a> answers it.
fn as_agent(a: &FleetAgent) -> Agent {
    serde_json::from_value(serde_json::to_value(a).unwrap()).unwrap_or_default()
}

/// fleet_extras fills in what the fleet says beyond the agent: its changes,
/// address, worktree, last activity and whether retiring it is safe.
pub fn fleet_extras(a: &mut FleetAgent, root: &Path, i: usize) {
    if let Some((files, ins, del)) = crate::review::Review::changes(&a.name) {
        a.changes = AgentChanges {
            files,
            insertions: ins,
            deletions: del,
            dirty: i.is_multiple_of(2),
        };
    }
    a.ip = format!("10.8.8.{}", 11 + i);
    a.worktree = root.to_string_lossy().into_owned();
    a.github_account = "leciric".into();
    a.claude_account = "default".into();
    a.last_active = Some(ago(Duration::from_secs(60 * (3 + 17 * i as u64))));
    a.retire = if i.is_multiple_of(2) {
        RetireAdvice {
            safe: true,
            branch: a.branch.clone(),
            ..Default::default()
        }
    } else {
        RetireAdvice {
            safe: false,
            reason: "2 commits on its branch aren't pushed".into(),
            branch: a.branch.clone(),
        }
    };
}

/// list is a folder's entries, folders first, each group by name (FileList).
fn list(root: &Path, path: &str, extra: &[&str], ignored: bool) -> Option<FileList> {
    if path == "samples" {
        return Some(FileList {
            path: path.into(),
            entries: crate::panel::SAMPLES
                .iter()
                .map(|n| FileEntry {
                    name: n.to_string(),
                    path: format!("samples/{n}"),
                    size: crate::panel::sample(n).map_or(0, |c| c.size),
                    ..Default::default()
                })
                .collect(),
            truncated: false,
        });
    }
    let dir = root.join(path);
    let mut entries: Vec<FileEntry> = std::fs::read_dir(&dir)
        .ok()?
        .filter_map(Result::ok)
        .filter_map(|e| {
            let name = e.file_name().to_string_lossy().into_owned();
            let hidden = IGNORED.contains(&name.as_str());
            if hidden && !ignored {
                return None;
            }
            let meta = e.metadata().ok()?;
            Some(FileEntry {
                path: join(path, &name),
                name,
                ignored: hidden,
                dir: meta.is_dir(),
                size: if meta.is_dir() { 0 } else { meta.len() as i64 },
                symlink: e.file_type().is_ok_and(|t| t.is_symlink()),
            })
        })
        .collect();
    // The made-up new files in this folder.
    for f in extra {
        let (parent, name) = f.rsplit_once('/').unwrap_or(("", f));
        if parent == path && !entries.iter().any(|e| e.name == name) {
            entries.push(FileEntry {
                name: name.into(),
                path: f.to_string(),
                size: 120,
                ..Default::default()
            });
        }
    }
    if path.is_empty() {
        entries.push(FileEntry {
            name: "samples".into(),
            path: "samples".into(),
            dir: true,
            ..Default::default()
        });
    }
    entries.sort_by(|a, b| b.dir.cmp(&a.dir).then(a.name.cmp(&b.name)));
    let truncated = entries.len() > 2000;
    entries.truncate(2000);
    Some(FileList {
        path: path.into(),
        entries,
        truncated,
    })
}

fn join(dir: &str, name: &str) -> String {
    if dir.is_empty() {
        name.into()
    } else {
        format!("{dir}/{name}")
    }
}

/// base_of makes up what a changed file was at its base: a few of its lines
/// otherwise, and one that is gone.
fn base_of(text: &str) -> String {
    text.lines()
        .enumerate()
        .filter(|(i, _)| i % 11 != 4)
        .map(|(i, l)| {
            if i % 17 == 2 {
                format!("{l} // was")
            } else {
                l.to_string()
            }
        })
        .collect::<Vec<_>>()
        .join("\n")
}

const NOTES: &str = "# Notes from the agent\n\nThe tree follows the disk: a folder listed is watched,\nand what moves is read again.\n";

fn read(
    root: &Path,
    path: &str,
    full: bool,
    with_base: bool,
    touched: bool,
) -> Option<FileContent> {
    if let Some(name) = path.strip_prefix("samples/") {
        return crate::panel::sample(name);
    }
    let change = CHANGES.iter().find(|(p, _)| *p == path).map(|(_, c)| *c);
    let bytes = if path == "docs/notes-from-agent.md" {
        let mut n = NOTES.to_string();
        if touched {
            n.push_str("\nTouched again.\n");
        }
        n.into_bytes()
    } else {
        std::fs::read(root.join(path)).ok()?
    };
    let size = bytes.len() as i64;
    let mut c = FileContent {
        path: path.into(),
        size,
        ..Default::default()
    };
    let lower = path.to_lowercase();
    let image = [
        ("png", "image/png"),
        ("jpg", "image/jpeg"),
        ("svg", "image/svg+xml"),
        ("gif", "image/gif"),
    ]
    .iter()
    .find(|(e, _)| lower.ends_with(&format!(".{e}")))
    .map(|(_, m)| *m);
    if let Some(mime) = image {
        if bytes.len() > 8 * 1024 * 1024 {
            c.too_large = true;
        } else {
            c.data = base64::engine::general_purpose::STANDARD.encode(&bytes);
        }
        c.image = mime.into();
        return Some(c);
    }
    if bytes.contains(&0) {
        c.binary = true;
        c.sqlite = bytes.starts_with(b"SQLite format 3");
        c.pdf = bytes.starts_with(b"%PDF");
        return Some(c);
    }
    let text = String::from_utf8_lossy(&bytes).into_owned();
    if !full && text.len() > READ_LIMIT {
        let mut end = READ_LIMIT;
        while !text.is_char_boundary(end) {
            end -= 1;
        }
        c.text = text[..end].to_string();
        c.truncated = true;
    } else {
        c.text = text.clone();
    }
    if let Some(ch) = change {
        c.status = ch.into();
        if with_base {
            c.base = "a1b2c3d".into();
            match ch {
                FILE_MODIFIED => c.base_text = base_of(&text),
                FILE_ADDED | FILE_UNTRACKED => c.base_missing = true,
                _ => {}
            }
        }
    }
    Some(c)
}

fn timeline() -> Timeline {
    let at = |mins_ago: u64| ago(Duration::from_secs(mins_ago * 60));
    let step = |kind: &str, title: &str, start: u64, end: u64| TimelineStep {
        kind: kind.into(),
        title: title.into(),
        start: at(start),
        end: at(end),
        ..Default::default()
    };
    let mut steps = vec![
        step(
            "task",
            "Build the agent page and its panel at parity with v2",
            52,
            52,
        ),
        TimelineStep {
            paths: vec![
                "desktop/src/renderer/components/v2/RightPanel.tsx".into(),
                "desktop/src/renderer/components/v2/AgentList.tsx".into(),
            ],
            calls: 6,
            ..step("read", "", 51, 47)
        },
        step("plan", "", 47, 46),
        TimelineStep {
            paths: vec![
                "native/crates/app/src/app/panel.rs".into(),
                "native/crates/app/src/files.rs".into(),
                "native/crates/app/src/app/agent.rs".into(),
            ],
            added: 2140,
            removed: 96,
            ..step("edit", "", 46, 21)
        },
        TimelineStep {
            status: "failed".into(),
            lines: vec!["error[E0592]: duplicate definitions with name `start_rename`".into()],
            ..step("test", "", 21, 19)
        },
        TimelineStep {
            status: "passed".into(),
            lines: vec!["test result: ok. 64 passed; 0 failed".into()],
            ..step("test", "", 18, 15)
        },
        TimelineStep {
            lines: vec!["cargo build --release -p agentbox-native -p fake-daemon".into()],
            ..step("run", "Built the release", 15, 12)
        },
        step("desktop", "Checked the panel on the desktop", 12, 8),
        step("pr", "", 6, 6),
    ];
    steps.retain(|s| !s.start.is_empty());
    Timeline {
        start: at(52),
        end: at(6),
        working: true,
        steps,
    }
}

async fn body_of(req: Request<Incoming>) -> Bytes {
    req.into_body()
        .collect()
        .await
        .map(|b| b.to_bytes())
        .unwrap_or_default()
}

/// set_state changes an agent's state and says so, as the daemon does.
fn set_state(fake: &Fake, r#ref: &str, state: &str) -> Option<Agent> {
    let agent = {
        let mut fleet = fake.fleet.lock().unwrap();
        let a = fleet.iter_mut().find(|a| a.r#ref == r#ref)?;
        a.state = state.into();
        if state != "running" {
            a.busy = false;
        }
        as_agent(a)
    };
    fake.emit(
        EVENT_AGENT,
        &AgentChange {
            r#ref: r#ref.into(),
            state: state.into(),
            ..Default::default()
        },
    );
    Some(agent)
}

/// route answers the requests about the fake project's agents this module
/// keeps, and gives back any other.
pub async fn route(
    fake: &Arc<Fake>,
    req: Request<Incoming>,
    query: &HashMap<String, String>,
) -> Result<Response<Body>, Box<Request<Incoming>>> {
    let path = req.uri().path().to_string();
    let parts: Vec<String> = path
        .trim_matches('/')
        .split('/')
        .map(crate::decode)
        .collect();
    let parts: Vec<&str> = parts.iter().map(String::as_str).collect();
    let method = req.method().clone();
    let in_fleet = |p: &str, a: &str| {
        fake.is_fake(p) && fake.fleet.lock().unwrap().iter().any(|x| x.name == a)
    };
    let q = |k: &str| query.get(k).map(|v| crate::decode(v)).unwrap_or_default();
    let past = |p: &str, a: &str| fake.is_fake(p) && crate::review::Review::is_agent(a);
    let res = match (&method, parts.as_slice()) {
        // What the machines use: only the fake's agents have any.
        (&Method::GET, ["v1", "usage", "cpu"]) => {
            let fleet = fake.fleet.lock().unwrap().clone();
            let agents = fleet
                .iter()
                .enumerate()
                .filter(|(_, a)| a.state == "running")
                .map(|(i, a)| CPUUsageAgent {
                    r#ref: a.r#ref.clone(),
                    title: a.title.clone(),
                    state: a.state.clone(),
                    cpu: if a.busy {
                        85. + (rand_id() % 60) as f64
                    } else {
                        2.5 + i as f64
                    },
                })
                .collect();
            json(
                StatusCode::OK,
                &CPUUsage {
                    host_cpu: 140.,
                    host_cores: 8,
                    other_cpu: 12.,
                    agents,
                },
            )
        }
        (&Method::GET, ["v1", "usage", "memory"]) => {
            let fleet = fake.fleet.lock().unwrap().clone();
            let agents: Vec<MemoryUsageAgent> = fleet
                .iter()
                .enumerate()
                .filter(|(_, a)| a.state == "running")
                .map(|(i, a)| MemoryUsageAgent {
                    r#ref: a.r#ref.clone(),
                    title: a.title.clone(),
                    state: a.state.clone(),
                    memory: (700 + 450 * i as i64) * 1024 * 1024,
                    swap: 0,
                })
                .collect();
            json(
                StatusCode::OK,
                &MemoryUsage {
                    host_total: 16 * 1024 * 1024 * 1024,
                    agents_used: agents.iter().map(|a| a.memory).sum(),
                    agents,
                    ..Default::default()
                },
            )
        }
        (&Method::GET, ["v1", "projects", p, "agent-events"]) if fake.is_fake(p) => {
            json(StatusCode::OK, &fake.review.lock().unwrap().events(p))
        }
        (&Method::GET, ["v1", "archived-agents"]) if fake.is_fake(&q("project")) => json(
            StatusCode::OK,
            &fake.review.lock().unwrap().past(&q("project")),
        ),
        // The project's Evidence: every agent's media.
        (&Method::GET, ["v1", "projects", p, "media"])
            if fake.is_fake(p) && q("agent").is_empty() =>
        {
            let mut m = fake.review.lock().unwrap().project_media(p);
            for (agent, extra) in &fake.machines.lock().unwrap().extra_media {
                m.extend(extra.iter().cloned().map(|mut i| {
                    i.agent_name = agent.clone();
                    i
                }));
            }
            json(StatusCode::OK, &m)
        }
        (&Method::GET, ["v1", "agents", p, a, "media"]) if in_fleet(p, a) => {
            let mut m = fake.review.lock().unwrap().media(p, a);
            if let Some(extra) = fake.machines.lock().unwrap().extra_media.get(*a) {
                m.extend(extra.iter().cloned());
            }
            json(StatusCode::OK, &m)
        }
        (
            &Method::POST,
            [
                "v1",
                "agents",
                p,
                a,
                action @ ("start" | "stop" | "pause" | "resume"),
            ],
        ) if in_fleet(p, a) => {
            let state = match *action {
                "start" | "resume" => "running",
                "stop" => "stopped",
                _ => "paused",
            };
            // The machine's desktop goes with it, and comes back a moment
            // after it starts (Manager.EnsureBrowser).
            match *action {
                "stop" => crate::desktop::stop().await,
                "start" => {
                    tokio::spawn(async {
                        tokio::time::sleep(Duration::from_secs(2)).await;
                        crate::desktop::start().await;
                    });
                }
                _ => {}
            }
            match set_state(fake, &format!("{p}/{a}"), state) {
                Some(agent) => json(StatusCode::OK, &agent),
                None => not_found(&path),
            }
        }
        (&Method::PATCH, ["v1", "agents", p, a]) if in_fleet(p, a) => {
            let body = body_of(req).await;
            let up: UpdateAgentRequest = serde_json::from_slice(&body).unwrap_or_default();
            let agent = {
                let mut fleet = fake.fleet.lock().unwrap();
                let x = fleet.iter_mut().find(|x| x.name == *a).unwrap();
                if let Some(t) = up.title {
                    x.title = t;
                }
                as_agent(x)
            };
            return Ok(json(StatusCode::OK, &agent));
        }
        (&Method::DELETE, ["v1", "agents", p, a]) if in_fleet(p, a) => {
            let r#ref = format!("{p}/{a}");
            fake.fleet.lock().unwrap().retain(|x| x.r#ref != r#ref);
            fake.emit(
                EVENT_AGENT,
                &AgentChange {
                    r#ref,
                    removed: true,
                    ..Default::default()
                },
            );
            no_content()
        }
        (&Method::POST, ["v1", "projects", p, "retire"]) if fake.is_fake(p) => {
            let body = body_of(req).await;
            let asked: RetireRequest = serde_json::from_slice(&body).unwrap_or_default();
            let mut res = RetireResult {
                how: asked.how.clone(),
                ..Default::default()
            };
            for name in &asked.agents {
                let found = fake
                    .fleet
                    .lock()
                    .unwrap()
                    .iter()
                    .find(|x| &x.name == name)
                    .cloned();
                let Some(x) = found else { continue };
                let entry = RetiredAgent {
                    name: x.name.clone(),
                    title: x.title.clone(),
                    branch: x.branch.clone(),
                    reason: String::new(),
                };
                if x.state == "stopped" {
                    res.skipped.push(RetiredAgent {
                        reason: "it is already stopped".into(),
                        ..entry
                    });
                } else {
                    set_state(fake, &x.r#ref, "stopped");
                    res.retired.push(entry);
                }
            }
            return Ok(json(StatusCode::OK, &res));
        }
        (&Method::GET, ["v1", "agents", p, a, "disk"]) if in_fleet(p, a) => json(
            StatusCode::OK,
            &AgentDisk {
                machine: Some(5_400_000_000 + a.len() as i64 * 1_000_000),
                worktree: Some(812_000_000),
                measured_at: now(),
            },
        ),
        (&Method::GET, ["v1", "agents", p, a, "timeline"]) if in_fleet(p, a) => {
            json(StatusCode::OK, &timeline())
        }
        (&Method::GET, ["v1", "agents", p, a, "checkpoints"]) if in_fleet(p, a) => json(
            StatusCode::OK,
            &crate::chat::checkpoints(&format!("{p}/{a}"), fake.opts.history),
        ),
        (&Method::POST, ["v1", "agents", p, a, "rollback"]) if in_fleet(p, a) => {
            let body = body_of(req).await;
            let asked: RollbackRequest = serde_json::from_slice(&body).unwrap_or_default();
            if !crate::chat::checkpoints(&format!("{p}/{a}"), fake.opts.history)
                .iter()
                .any(|c| c.id == asked.checkpoint)
            {
                return Ok(bad_request(format!("no checkpoint {}", asked.checkpoint)));
            }
            return Ok(json(
                StatusCode::OK,
                &RollbackResult {
                    saved: Checkpoint {
                        id: format!("cp-saved-{}", rand_id() % 100_000),
                        kind: "rollback".into(),
                        created_at: now(),
                        ..Default::default()
                    },
                },
            ));
        }
        (&Method::GET, ["v1", "agents", p, a, "snapshots"]) if in_fleet(p, a) => {
            let r#ref = format!("{p}/{a}");
            let list = fake.machines.lock().unwrap().snapshots_of(&r#ref).clone();
            json(StatusCode::OK, &list)
        }
        (&Method::POST, ["v1", "agents", p, a, "snapshots"]) if in_fleet(p, a) => {
            let r#ref = format!("{p}/{a}");
            let body = body_of(req).await;
            let asked: SnapshotRequest = serde_json::from_slice(&body).unwrap_or_default();
            let name = if asked.name.is_empty() {
                format!(
                    "snap-{}",
                    humantime::format_rfc3339_seconds(std::time::SystemTime::now())
                )
                .replace([':', 'T'], "-")
                .trim_end_matches('Z')
                .to_string()
            } else {
                asked.name
            };
            let snap = Snapshot {
                name,
                created_at: now(),
                head: sha(rand_id()),
            };
            fake.machines
                .lock()
                .unwrap()
                .snapshots_of(&r#ref)
                .push(snap.clone());
            return Ok(json(StatusCode::OK, &snap));
        }
        (&Method::DELETE, ["v1", "agents", p, a, "snapshots", name]) if in_fleet(p, a) => {
            let r#ref = format!("{p}/{a}");
            fake.machines
                .lock()
                .unwrap()
                .snapshots_of(&r#ref)
                .retain(|s| s.name != *name);
            no_content()
        }
        (&Method::POST, ["v1", "agents", p, a, "restore"]) if in_fleet(p, a) => json(
            StatusCode::ACCEPTED,
            &Job {
                id: format!("job-{}", rand_id() % 100_000),
                kind: "restore".into(),
                target: format!("{p}/{a}"),
                status: JOB_SUCCEEDED.into(),
                created_at: now(),
                finished_at: Some(now()),
                ..Default::default()
            },
        ),
        // A fork is a new agent, made at once.
        (&Method::POST, ["v1", "agents", p, a, "fork"]) if in_fleet(p, a) => {
            let body = body_of(req).await;
            let asked: ForkRequest = serde_json::from_slice(&body).unwrap_or_default();
            let agent = {
                let mut fleet = fake.fleet.lock().unwrap();
                let from = fleet.iter().find(|x| x.name == *a).cloned().unwrap();
                let next = (1..)
                    .map(|n| format!("agent-{n:02}"))
                    .find(|n| !fleet.iter().any(|x| &x.name == n))
                    .unwrap();
                let name = if asked.name.is_empty() {
                    next
                } else {
                    asked.name
                };
                let mut x = from.clone();
                x.r#ref = format!("{p}/{name}");
                x.title = if asked.title.is_empty() {
                    format!("{} (fork)", from.title)
                } else {
                    asked.title
                };
                x.branch = format!("agentbox/{name}");
                x.name = name;
                x.state = "running".into();
                x.busy = false;
                x.pr = None;
                x.created_at = now();
                fleet.push(x.clone());
                as_agent(&x)
            };
            fake.emit(
                EVENT_AGENT,
                &AgentChange {
                    r#ref: agent.r#ref.clone(),
                    state: agent.state.clone(),
                    ..Default::default()
                },
            );
            json(
                StatusCode::ACCEPTED,
                &Job {
                    id: format!("job-{}", rand_id() % 100_000),
                    kind: "fork".into(),
                    target: format!("{p}/{a}"),
                    status: JOB_SUCCEEDED.into(),
                    result: serde_json::to_value(&agent).unwrap(),
                    created_at: now(),
                    finished_at: Some(now()),
                    ..Default::default()
                },
            )
        }
        // Its desktop: this machine's VNC server (desktop.rs), up or not.
        (&Method::GET, ["v1", "agents", p, a, "browser"]) if in_fleet(p, a) => {
            json(StatusCode::OK, &crate::desktop::status().await)
        }
        (&Method::POST, ["v1", "agents", p, a, "browser", action @ ("start" | "stop")])
            if in_fleet(p, a) =>
        {
            if *action == "start" {
                crate::desktop::start().await;
            } else {
                crate::desktop::stop().await;
            }
            json(StatusCode::OK, &crate::desktop::status().await)
        }
        (&Method::GET, ["v1", "agents", p, a, "media", "record"]) if in_fleet(p, a) => {
            let s = fake
                .machines
                .lock()
                .unwrap()
                .recording
                .get(&format!("{p}/{a}"))
                .cloned()
                .unwrap_or_default();
            json(StatusCode::OK, &s)
        }
        (&Method::POST, ["v1", "agents", p, a, "media", "record", "start"]) if in_fleet(p, a) => {
            let body = body_of(req).await;
            let asked: RecordRequest = serde_json::from_slice(&body).unwrap_or_default();
            let s = RecordingStatus {
                recording: true,
                target: "display".into(),
                input: "desktop".into(),
                name: asked.name,
                source: "user".into(),
                started_at: Some(now()),
                limit_seconds: 600,
            };
            fake.machines
                .lock()
                .unwrap()
                .recording
                .insert(format!("{p}/{a}"), s.clone());
            return Ok(json(StatusCode::OK, &s));
        }
        // A capture is one of the fixtures' files, under a new id: the
        // media file route serves an id by its stem.
        (&Method::POST, ["v1", "agents", p, a, "media", kind @ "screenshot"])
        | (&Method::POST, ["v1", "agents", p, a, "media", kind @ "record", "stop"])
            if in_fleet(p, a) =>
        {
            let shot = *kind == "screenshot";
            let started = {
                let mut m = fake.machines.lock().unwrap();
                m.recording
                    .remove(&format!("{p}/{a}"))
                    .and_then(|s| s.started_at)
            };
            let secs = started
                .and_then(|s| humantime::parse_rfc3339_weak(s.trim_end_matches('Z')).ok())
                .and_then(|t| std::time::SystemTime::now().duration_since(t).ok())
                .map_or(9., |d| d.as_secs_f64());
            let item = MediaItem {
                id: format!(
                    "{a}-{}-{}",
                    rand_id() % 1_000_000,
                    if shot { "home" } else { "walkthrough" }
                ),
                agent: a.to_string(),
                kind: if shot { "screenshot" } else { "recording" }.into(),
                name: if shot { "screen" } else { "screen recording" }.into(),
                file: if shot { "home.png" } else { "walkthrough.mp4" }.into(),
                mime: if shot { "image/png" } else { "video/mp4" }.into(),
                source: "user".into(),
                meta: MediaMeta {
                    width: 1440,
                    height: 900,
                    duration: if shot { 0. } else { secs.min(9.) },
                    target: "display".into(),
                    ..Default::default()
                },
                created_at: now(),
                ..Default::default()
            };
            fake.machines
                .lock()
                .unwrap()
                .extra_media
                .entry(a.to_string())
                .or_default()
                .push(item.clone());
            fake.emit(EVENT_MEDIA, &item);
            json(StatusCode::OK, &item)
        }
        // The worktree's files.
        (&Method::GET, ["v1", "agents" | "archived-agents", p, a, "files", "list"])
            if in_fleet(p, a) || past(p, a) =>
        {
            let m = fake.machines.lock().unwrap();
            let extra: Vec<&str> = CHANGES
                .iter()
                .filter(|(_, c)| *c != FILE_MODIFIED)
                .map(|(p, _)| *p)
                .collect();
            match list(&m.root, &q("path"), &extra, !q("ignored").is_empty()) {
                Some(l) => json(StatusCode::OK, &l),
                None => not_found(&path),
            }
        }
        (&Method::GET, ["v1", "agents" | "archived-agents", p, a, "files", "status"])
            if in_fleet(p, a) || past(p, a) =>
        {
            let touched = fake.machines.lock().unwrap().touched;
            let mut changes: BTreeMap<String, String> = CHANGES
                .iter()
                .map(|(p, c)| (p.to_string(), c.to_string()))
                .collect();
            if touched {
                changes.insert(
                    "native/crates/fake-daemon/src/agents.rs".into(),
                    FILE_MODIFIED.into(),
                );
            }
            json(
                StatusCode::OK,
                &FileStatus {
                    base: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678".into(),
                    changes,
                    truncated: false,
                },
            )
        }
        (&Method::GET, ["v1", "agents" | "archived-agents", p, a, "files", "read"])
            if in_fleet(p, a) || past(p, a) =>
        {
            let m = fake.machines.lock().unwrap();
            let file = q("path");
            let mut c = read(
                &m.root,
                &file,
                query.contains_key("full"),
                query.contains_key("base"),
                m.touched,
            );
            if m.touched
                && file == "native/crates/fake-daemon/src/agents.rs"
                && let Some(c) = &mut c
            {
                c.status = FILE_MODIFIED.into();
                c.base = "a1b2c3d".into();
                c.base_text = base_of(&c.text);
            }
            match c {
                Some(c) => json(StatusCode::OK, &c),
                None => not_found(&path),
            }
        }
        _ => return Err(Box::new(req)),
    };
    Ok(res)
}

/// touch makes the worktree "change" every while, as an agent at work
/// does, and says so as the daemon's file watch would.
pub async fn touch(fake: Arc<Fake>) {
    loop {
        tokio::time::sleep(Duration::from_secs(20)).await;
        let first = fake.fleet.lock().unwrap().first().map(|a| a.r#ref.clone());
        let Some(r#ref) = first else { continue };
        fake.machines.lock().unwrap().touched ^= true;
        fake.emit(
            EVENT_FILES,
            &FilesChange {
                r#ref,
                dirs: vec!["docs".into(), "native/crates/fake-daemon/src".into()],
                files: vec![
                    "docs/notes-from-agent.md".into(),
                    "native/crates/fake-daemon/src/agents.rs".into(),
                ],
            },
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_folder_lists_folders_first_without_the_ignored() {
        let root = Path::new(env!("CARGO_MANIFEST_DIR"));
        let l = list(root, "", &["notes.md"], false).unwrap();
        assert!(l.entries[0].dir && l.entries.iter().any(|e| e.name == "src" && e.dir));
        assert!(l.entries.iter().any(|e| e.name == "Cargo.toml" && !e.dir));
        assert!(l.entries.iter().any(|e| e.name == "notes.md"));
        assert!(!l.entries.iter().any(|e| e.name == "target"));
        let c = read(root, "Cargo.toml", false, true, false).unwrap();
        assert!(c.text.contains("[package]"));
        assert!(base_of("a\nb\nc\nd\ne\nf").lines().count() < 6);
    }
}
