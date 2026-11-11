//! The right-hand panel's other views, for the fake's agents: what needs a
//! machine, Android, GitHub or the scheduler the real daemon (with no Incus
//! here) can't answer. The preview proxy (GET /v1/preview, and a page on its
//! port for every agent's port), the element picker, the Android emulator's
//! status, schedules and their runs, the project's issues, an agent's own
//! secrets and connectors, a past agent (its record, continue, delete),
//! token counts, the worktree's file list for Find, and sample CSV, SQLite
//! and PDF files under samples/ for the viewers. Shapes as the Go handlers
//! write them.
use std::collections::HashMap;
use std::sync::{Mutex, OnceLock};
use std::time::Duration;

use agentbox_api::*;
use bytes::Bytes;
use http_body_util::{BodyExt, Full};
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, json, not_found, now, rand_id};

/// The preview proxy's port: a page for any host.
pub const PREVIEW_PORT: u16 = 7781;

#[derive(Default)]
struct State {
    android: HashMap<String, AndroidStatus>,
    schedules: Vec<Schedule>,
    runs: Vec<ScheduleRun>,
    secrets: HashMap<String, Vec<Secret>>,
    connectors: HashMap<String, Vec<Connector>>,
    deleted_past: Vec<String>,
    handed: HashMap<i64, String>,
    seeded: bool,
}

fn state() -> &'static Mutex<State> {
    static S: OnceLock<Mutex<State>> = OnceLock::new();
    S.get_or_init(Default::default)
}

fn ago(mins: u64) -> String {
    humantime::format_rfc3339_millis(crate::clock() - Duration::from_secs(mins * 60)).to_string()
}

fn ahead(mins: u64) -> String {
    humantime::format_rfc3339_millis(crate::clock() + Duration::from_secs(mins * 60)).to_string()
}

fn seed(s: &mut State, project: &str) {
    if s.seeded {
        return;
    }
    s.seeded = true;
    let run =
        |id: &str, sched: &str, title: &str, outcome: &str, agent: &str, mins: u64| ScheduleRun {
            id: id.into(),
            schedule: sched.into(),
            title: title.into(),
            trigger: if id.ends_with('m') { "manual" } else { "time" }.into(),
            ran_at: ago(mins),
            outcome: outcome.into(),
            agent: agent.into(),
            ..Default::default()
        };
    s.runs = vec![
        run(
            "r3m",
            "s1",
            "Morning dependency audit",
            "agent",
            "agent-04",
            50,
        ),
        run("r2", "s2", "Review open pull requests", "sent", "", 60 * 14),
        run(
            "r1",
            "s1",
            "Morning dependency audit",
            "failed",
            "",
            60 * 24,
        ),
    ];
    s.runs[2].error = "precheck failed: exit status 1".into();
    s.schedules = vec![
        Schedule {
            id: "s1".into(),
            project: project.into(),
            title: "Morning dependency audit".into(),
            task: "Check every dependency for updates and known vulnerabilities; open a pull request with the safe bumps.".into(),
            kind: "cron".into(),
            cron: "0 9 * * 1-5".into(),
            timezone: "UTC".into(),
            target: "agent".into(),
            session: "fresh".into(),
            next_run: Some(ahead(60 * 15)),
            last_run: Some(s.runs[0].clone()),
            runs: 2,
            created_by: "user".into(),
            created_at: ago(60 * 24 * 6),
            updated_at: ago(60 * 24 * 6),
            ..Default::default()
        },
        Schedule {
            id: "s2".into(),
            project: project.into(),
            title: "Review open pull requests".into(),
            task: "Go through the open pull requests, summarize what each needs, and nudge the agents whose checks fail.".into(),
            kind: "cron".into(),
            cron: "0 18 * * *".into(),
            timezone: "UTC".into(),
            target: "lead".into(),
            precheck: "gh pr list --state open --limit 1 | grep -q .".into(),
            next_run: Some(ahead(60 * 3)),
            last_run: Some(s.runs[1].clone()),
            runs: 1,
            created_by: "lead".into(),
            created_at: ago(60 * 24 * 3),
            updated_at: ago(60 * 24 * 3),
            ..Default::default()
        },
        Schedule {
            id: "s3".into(),
            project: project.into(),
            title: "Fix main when CI fails".into(),
            task: "CI failed on main: find why and fix it.".into(),
            kind: "event".into(),
            event: "ci_failed".into(),
            timezone: "UTC".into(),
            target: "agent".into(),
            paused: true,
            created_by: "user".into(),
            created_at: ago(60 * 24),
            updated_at: ago(60 * 24),
            ..Default::default()
        },
    ];
}

async fn body_json<T: serde::de::DeserializeOwned + Default>(req: Request<Incoming>) -> T {
    let b = req
        .into_body()
        .collect()
        .await
        .map(|b| b.to_bytes())
        .unwrap_or_default();
    serde_json::from_slice(&b).unwrap_or_default()
}

fn no_content() -> Response<Body> {
    Response::builder()
        .status(StatusCode::NO_CONTENT)
        .body(Full::new(Bytes::new()).boxed())
        .unwrap()
}

/// apply makes a schedule what a request says, as schedules.go does.
fn apply(s: &mut Schedule, r: &ScheduleRequest) {
    let set = |f: &mut String, v: &Option<String>| {
        if let Some(v) = v {
            *f = v.clone();
        }
    };
    set(&mut s.title, &r.title);
    set(&mut s.task, &r.task);
    set(&mut s.timezone, &r.timezone);
    set(&mut s.target, &r.target);
    set(&mut s.session, &r.session);
    set(&mut s.model, &r.model);
    set(&mut s.effort, &r.effort);
    set(&mut s.precheck, &r.precheck);
    if let Some(g) = r.grace_minutes {
        s.grace_minutes = g;
    }
    if let Some(p) = r.paused {
        s.paused = p;
    }
    let timing = r.at.is_some() || r.every.is_some() || r.cron.is_some() || r.on.is_some();
    if timing {
        s.at = None;
        s.every_minutes = 0;
        s.cron.clear();
        s.event.clear();
        s.event_label.clear();
    }
    if let Some(at) = r.at.clone().filter(|a| !a.is_empty()) {
        s.kind = "once".into();
        s.next_run = Some(at.clone());
        s.at = Some(at);
    } else if let Some(every) = &r.every {
        let n: i64 = every.trim_end_matches(['m', 'h', 'd']).parse().unwrap_or(1);
        s.kind = "every".into();
        s.every_minutes = match every.chars().last() {
            Some('h') => n * 60,
            Some('d') => n * 1440,
            _ => n,
        };
        s.next_run = Some(ahead(s.every_minutes as u64));
    } else if let Some(cron) = &r.cron {
        s.kind = "cron".into();
        s.cron = cron.clone();
        s.next_run = Some(ahead(60 * 9));
    } else if let Some(on) = &r.on {
        s.kind = "event".into();
        s.event = on.clone();
        s.event_label = r.label.clone().unwrap_or_default();
        s.next_run = None;
    }
    if s.title.is_empty() {
        s.title = s.task.chars().take(40).collect();
    }
    s.updated_at = now();
}

fn issues(project: &str, s: &State, pick_up: bool) -> ProjectIssues {
    let label = |name: &str, color: &str| Label {
        name: name.into(),
        color: color.into(),
        ..Default::default()
    };
    let issue =
        |n: i64, title: &str, labels: Vec<Label>, author: &str, comments: i64, mins: u64| {
            let handed = s.handed.get(&n).map(|agent| IssueHandoff {
                agent: agent.clone(),
                agent_title: title.into(),
                status: "running".into(),
                at: ago(1),
                ..Default::default()
            });
            Issue {
                number: n,
                title: title.into(),
                url: format!("https://github.com/leciric/agentbox/issues/{n}"),
                author: author.into(),
                labels,
                comments,
                created_at: Some(ago(mins + 600)),
                updated_at: Some(ago(mins)),
                handed,
                ..Default::default()
            }
        };
    let mut list = vec![
        issue(
            712,
            "Terminal loses its scrollback after the window is resized",
            vec![label("bug", "d73a4a")],
            "leciric",
            3,
            42,
        ),
        issue(
            708,
            "Show the agent's CPU history on its info card",
            vec![label("enhancement", "a2eeef")],
            "marina-dev",
            1,
            60 * 5,
        ),
        issue(
            701,
            "Import a project's schedules from another machine",
            vec![label("enhancement", "a2eeef"), label("agentbox", "0e8a16")],
            "leciric",
            0,
            60 * 26,
        ),
        issue(
            695,
            "Windows: the relay port stays bound after quitting",
            vec![label("bug", "d73a4a"), label("windows", "1d76db")],
            "tkachenko",
            7,
            60 * 50,
        ),
    ];
    list[3].handed = Some(IssueHandoff {
        agent: "agent-03".into(),
        agent_title: "Free the relay port on quit".into(),
        status: "running".into(),
        at: ago(60 * 20),
        pull_request: Some(IssuePullRequest {
            number: 611,
            url: "https://github.com/leciric/agentbox/pull/611".into(),
            state: "open".into(),
        }),
        ..Default::default()
    });
    ProjectIssues {
        project: project.into(),
        github: "leciric/agentbox".into(),
        github_account: "personal".into(),
        issues: list,
        pick_up_issues: pick_up,
        label: "agentbox".into(),
        fetched_at: Some(ago(2)),
        ..Default::default()
    }
}

fn connectors_of(r#ref: &str) -> Vec<Connector> {
    let (p, a) = r#ref.split_once('/').unwrap_or_default();
    let c = |name: &str, url: &str, auth: &str, status: &str, scope: &str| Connector {
        name: name.into(),
        scope: scope.into(),
        project: p.into(),
        agent: if scope == "agent" {
            a.into()
        } else {
            "".into()
        },
        url: url.into(),
        auth: auth.into(),
        enabled: true,
        status: status.into(),
        connected_at: (status == CONNECTOR_CONNECTED).then(|| ago(60 * 30)),
        updated_at: ago(60 * 30),
        ..Default::default()
    };
    vec![
        c(
            "linear",
            "https://mcp.linear.app/mcp",
            CONNECTOR_O_AUTH,
            CONNECTOR_CONNECTED,
            "agent",
        ),
        c(
            "sentry",
            "https://mcp.sentry.dev/mcp",
            CONNECTOR_O_AUTH,
            CONNECTOR_NONE,
            "agent",
        ),
    ]
}

/// The samples the viewers are shown with, beside the worktree's own files.
pub const SAMPLES: [&str; 3] = ["sales.csv", "app.db", "report.pdf"];

const CSV: &str = "region,quarter,orders,revenue,growth\nNorth,Q1,1204,\"$48,160\",4.2%\nNorth,Q2,1388,\"$55,520\",15.3%\nSouth,Q1,980,\"$39,200\",-2.1%\nSouth,Q2,1102,\"$44,080\",12.4%\nEast,Q1,1530,\"$61,200\",8.8%\nEast,Q2,1611,\"$64,440\",5.3%\n\"West, coast\",Q1,870,\"$34,800\",1.0%\n\"West, coast\",Q2,1012,\"$40,480\",16.3%\n";

/// pdf is a two-page PDF, written out with its cross-reference table.
pub fn pdf() -> Vec<u8> {
    let page = |text: &str, n: usize| {
        let stream = format!(
            "BT /F1 28 Tf 72 720 Td ({text}) Tj ET\nBT /F1 14 Tf 72 680 Td (Page {n} of the fake daemon's sample report.) Tj ET\n0.49 0.55 1 rg 72 400 468 200 re f\n0.2 0.83 0.6 rg 72 340 300 40 re f"
        );
        stream
    };
    let s1 = page("Quarterly report", 1);
    let s2 = page("Appendix", 2);
    let objs = [
        "<< /Type /Catalog /Pages 2 0 R >>".to_string(),
        "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>".to_string(),
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 5 0 R /Resources << /Font << /F1 7 0 R >> >> >>".to_string(),
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 6 0 R /Resources << /Font << /F1 7 0 R >> >> >>".to_string(),
        format!("<< /Length {} >>\nstream\n{s1}\nendstream", s1.len() + 1),
        format!("<< /Length {} >>\nstream\n{s2}\nendstream", s2.len() + 1),
        "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>".to_string(),
    ];
    let mut out = b"%PDF-1.4\n".to_vec();
    let mut offsets = Vec::new();
    for (i, o) in objs.iter().enumerate() {
        offsets.push(out.len());
        out.extend(format!("{} 0 obj\n{o}\nendobj\n", i + 1).bytes());
    }
    let xref = out.len();
    out.extend(format!("xref\n0 {}\n0000000000 65535 f \n", objs.len() + 1).bytes());
    for o in offsets {
        out.extend(format!("{o:010} 00000 n \n").bytes());
    }
    out.extend(
        format!(
            "trailer\n<< /Size {} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n",
            objs.len() + 1
        )
        .bytes(),
    );
    out
}

/// sample is a sample file's content, as …/files/read sends it.
pub fn sample(name: &str) -> Option<FileContent> {
    let path = format!("samples/{name}");
    Some(match name {
        "sales.csv" => FileContent {
            path,
            size: CSV.len() as i64,
            text: CSV.into(),
            ..Default::default()
        },
        "app.db" => FileContent {
            path,
            size: 49152,
            binary: true,
            sqlite: true,
            ..Default::default()
        },
        "report.pdf" => {
            use base64::Engine as _;
            let b = pdf();
            FileContent {
                path,
                size: b.len() as i64,
                binary: true,
                pdf: true,
                data: base64::engine::general_purpose::STANDARD.encode(&b),
                ..Default::default()
            }
        }
        _ => return None,
    })
}

fn sqlite(table: &str, offset: i64, limit: i64) -> SQLiteQuery {
    let tables = vec![
        SQLiteTable {
            name: "agents".into(),
            view: false,
        },
        SQLiteTable {
            name: "events".into(),
            view: false,
        },
        SQLiteTable {
            name: "recent_events".into(),
            view: true,
        },
    ];
    let table = if table.is_empty() { "agents" } else { table };
    let (columns, total): (Vec<&str>, i64) = match table {
        "agents" => (vec!["id", "name", "state", "cpu", "created_at"], 6),
        _ => (vec!["id", "agent", "kind", "summary", "at"], 240),
    };
    let limit = if limit <= 0 { 100 } else { limit };
    let rows = (offset..(offset + limit).min(total))
        .map(|i| match table {
            "agents" => vec![
                serde_json::json!(i + 1),
                serde_json::json!(format!("agent-{:02}", i + 1)),
                serde_json::json!(["running", "stopped", "paused"][(i % 3) as usize]),
                serde_json::json!(2.5 + i as f64 * 1.25),
                serde_json::json!(ago(60 * (i as u64 + 1))),
            ],
            _ => vec![
                serde_json::json!(i + 1),
                serde_json::json!(format!("agent-{:02}", i % 6 + 1)),
                serde_json::json!(["report", "pr_opened", "checks_failed"][(i % 3) as usize]),
                serde_json::json!(if i % 7 == 0 {
                    serde_json::Value::Null
                } else {
                    serde_json::json!(format!("Step {i} of the work, reported"))
                }),
                serde_json::json!(ago(i as u64 * 3)),
            ],
        })
        .collect();
    SQLiteQuery {
        path: "samples/app.db".into(),
        tables,
        table: table.into(),
        columns: columns.into_iter().map(String::from).collect(),
        rows,
        offset,
        total,
    }
}

/// walk lists a checkout's files for Find, as git ls-files would: the
/// ignored folders left out.
fn walk(root: &std::path::Path, dir: &str, out: &mut Vec<String>) {
    if out.len() >= 20000 {
        return;
    }
    let Ok(rd) = std::fs::read_dir(root.join(dir)) else {
        return;
    };
    let mut entries: Vec<_> = rd.filter_map(Result::ok).collect();
    entries.sort_by_key(|e| e.file_name());
    for e in entries {
        let name = e.file_name().to_string_lossy().into_owned();
        if crate::agents::IGNORED.contains(&name.as_str()) || name.starts_with('.') {
            continue;
        }
        let path = if dir.is_empty() {
            name
        } else {
            format!("{dir}/{name}")
        };
        if e.file_type().is_ok_and(|t| t.is_dir()) {
            walk(root, &path, out);
        } else {
            out.push(path);
        }
    }
}

/// route answers what it knows; anything else goes back to the caller.
pub async fn route(
    fake: &Fake,
    req: Request<Incoming>,
    query: &HashMap<String, String>,
    pick_up: impl std::future::Future<Output = bool>,
) -> Result<Response<Body>, Box<Request<Incoming>>> {
    let path = req.uri().path().to_string();
    let parts: Vec<String> = path
        .trim_matches('/')
        .split('/')
        .map(crate::decode)
        .collect();
    let parts: Vec<&str> = parts.iter().map(String::as_str).collect();
    let method = req.method().clone();
    let q = |k: &str| query.get(k).map(|v| crate::decode(v)).unwrap_or_default();
    let in_fleet = |p: &str, a: &str| {
        fake.is_fake(p) && fake.fleet.lock().unwrap().iter().any(|x| x.name == a)
    };
    let past = |p: &str, a: &str| {
        fake.is_fake(p)
            && crate::review::Review::is_agent(a)
            && !state()
                .lock()
                .unwrap()
                .deleted_past
                .contains(&format!("{p}/{a}"))
    };
    let res = match (&method, parts.as_slice()) {
        (&Method::GET, ["v1", "preview"]) => json(
            StatusCode::OK,
            &PreviewInfo {
                addr: format!("127.0.0.1:{PREVIEW_PORT}"),
            },
        ),
        (&Method::POST, ["v1", "agents", p, a, "browser", "pick"]) if in_fleet(p, a) => {
            tokio::time::sleep(Duration::from_millis(1500)).await;
            json(
                StatusCode::OK,
                &ElementPickResult {
                    element: Some(ElementPick {
                        url: "http://localhost:5173/settings".into(),
                        title: "AgentBox · Settings".into(),
                        selector: "main > section.card > button.primary".into(),
                        tag: "button".into(),
                        html: "<button class=\"primary\">Save changes</button>".into(),
                        text: "Save changes".into(),
                        styles: vec![
                            ElementStyle {
                                name: "background-color".into(),
                                value: "rgb(124, 140, 255)".into(),
                            },
                            ElementStyle {
                                name: "border-radius".into(),
                                value: "8px".into(),
                            },
                        ],
                        r#box: ElementBox {
                            x: 312.,
                            y: 480.,
                            width: 132.,
                            height: 36.,
                        },
                        viewport: ElementSize {
                            width: 1280.,
                            height: 800.,
                        },
                        image: None,
                    }),
                },
            )
        }
        (&Method::POST, ["v1", "agents", p, a, "browser", "pick", "cancel"]) if in_fleet(p, a) => {
            no_content()
        }
        (&Method::GET, ["v1", "agents", p, a, "android", "stream"]) if in_fleet(p, a) => {
            return Ok(android_stream(req));
        }
        (&Method::GET, ["v1", "agents", p, a, "android"]) if in_fleet(p, a) => {
            let mut s = state().lock().unwrap();
            let booted = *a == "agent-02";
            let st = s
                .android
                .entry(format!("{p}/{a}"))
                .or_insert_with(|| AndroidStatus {
                    available: true,
                    sdk: "/opt/android-sdk".into(),
                    images: vec!["system-images;android-35;google_apis;x86_64".into()],
                    // One agent's emulator is up already, as if it started it.
                    running: booted,
                    booted,
                    device: if booted {
                        "emulator-5554 · Pixel 8".into()
                    } else {
                        String::new()
                    },
                    image: if booted {
                        "system-images;android-35;google_apis;x86_64".into()
                    } else {
                        String::new()
                    },
                    ..Default::default()
                });
            json(StatusCode::OK, &*st)
        }
        (&Method::POST, ["v1", "agents", p, a, "android", action @ ("start" | "stop")])
            if in_fleet(p, a) =>
        {
            let key = format!("{p}/{a}");
            if *action == "start" {
                state()
                    .lock()
                    .unwrap()
                    .android
                    .entry(key.clone())
                    .or_default()
                    .running = true;
                // Booting takes a while, as the emulator's does.
                tokio::time::sleep(Duration::from_secs(4)).await;
            }
            let mut s = state().lock().unwrap();
            let st = s.android.entry(key).or_default();
            st.available = true;
            st.images = vec!["system-images;android-35;google_apis;x86_64".into()];
            if *action == "start" {
                st.running = true;
                st.booted = true;
                st.device = "emulator-5554 · Pixel 8".into();
                st.image = "system-images;android-35;google_apis;x86_64".into();
            } else {
                st.running = false;
                st.booted = false;
                st.device.clear();
                st.image.clear();
            }
            json(StatusCode::OK, &*st)
        }
        (&Method::POST, ["v1", "agents", p, a, "media", "logs"]) if in_fleet(p, a) => json(
            StatusCode::OK,
            &MediaItem {
                id: format!("logs-{}", rand_id() % 100000),
                kind: "logs".into(),
                name: "logcat".into(),
                agent_name: a.to_string(),
                created_at: now(),
                ..Default::default()
            },
        ),
        (&Method::GET, ["v1", "projects", p, "schedules"]) if fake.is_fake(p) => {
            let mut s = state().lock().unwrap();
            seed(&mut s, p);
            json(StatusCode::OK, &s.schedules)
        }
        (&Method::GET, ["v1", "projects", p, "schedules", "runs"]) if fake.is_fake(p) => {
            let mut s = state().lock().unwrap();
            seed(&mut s, p);
            json(StatusCode::OK, &s.runs)
        }
        (&Method::POST, ["v1", "projects", p, "schedules"]) if fake.is_fake(p) => {
            let p = p.to_string();
            let r: ScheduleRequest = body_json(req).await;
            let mut sc = Schedule {
                id: format!("s{}", rand_id() % 100000),
                project: p.clone(),
                timezone: "UTC".into(),
                target: "lead".into(),
                created_by: "user".into(),
                created_at: now(),
                ..Default::default()
            };
            apply(&mut sc, &r);
            let mut s = state().lock().unwrap();
            seed(&mut s, &p);
            s.schedules.push(sc.clone());
            json(StatusCode::OK, &sc)
        }
        (&Method::PATCH, ["v1", "projects", p, "schedules", id]) if fake.is_fake(p) => {
            let (p, id) = (p.to_string(), id.to_string());
            let r: ScheduleRequest = body_json(req).await;
            let mut s = state().lock().unwrap();
            seed(&mut s, &p);
            match s.schedules.iter_mut().find(|x| x.id == id) {
                Some(sc) => {
                    apply(sc, &r);
                    json(StatusCode::OK, &*sc)
                }
                None => not_found(&path),
            }
        }
        (&Method::DELETE, ["v1", "projects", p, "schedules", id]) if fake.is_fake(p) => {
            let mut s = state().lock().unwrap();
            s.schedules.retain(|x| x.id != *id);
            no_content()
        }
        (&Method::POST, ["v1", "projects", p, "schedules", id, "run"]) if fake.is_fake(p) => {
            let mut s = state().lock().unwrap();
            seed(&mut s, p);
            let Some(sc) = s.schedules.iter().find(|x| x.id == *id).cloned() else {
                return Ok(not_found(&path));
            };
            let agent = (sc.target == "agent").then(|| "agent-05".to_string());
            let run = ScheduleRun {
                id: format!("r{}m", rand_id() % 100000),
                schedule: sc.id.clone(),
                title: sc.title.clone(),
                trigger: "manual".into(),
                ran_at: now(),
                outcome: if agent.is_some() { "agent" } else { "sent" }.into(),
                agent: agent.unwrap_or_default(),
                ..Default::default()
            };
            s.runs.insert(0, run.clone());
            if let Some(x) = s.schedules.iter_mut().find(|x| x.id == *id) {
                x.runs += 1;
                x.last_run = Some(run.clone());
            }
            json(StatusCode::OK, &run)
        }
        (&Method::GET, ["v1", "projects", p, "issues"]) if fake.is_fake(p) => {
            let p = p.to_string();
            let on = pick_up.await;
            json(StatusCode::OK, &issues(&p, &state().lock().unwrap(), on))
        }
        (&Method::POST, ["v1", "agents"]) => {
            let r: serde_json::Value = body_json(req).await;
            let Some(n) = r.get("issue").and_then(|v| v.as_i64()) else {
                return Ok(json(
                    StatusCode::BAD_REQUEST,
                    &ApiError {
                        error: "the fake daemon only hands issues to agents".into(),
                        ..Default::default()
                    },
                ));
            };
            state().lock().unwrap().handed.insert(n, "agent-07".into());
            json(
                StatusCode::ACCEPTED,
                &Job {
                    id: format!("job-{}", rand_id() % 100000),
                    kind: "create".into(),
                    status: "running".into(),
                    created_at: now(),
                    ..Default::default()
                },
            )
        }
        (&Method::GET, ["v1", "agents", p, a, "secrets"]) if in_fleet(p, a) => {
            let key = format!("{p}/{a}");
            let mut s = state().lock().unwrap();
            let list = s.secrets.entry(key).or_insert_with(|| {
                vec![
                    Secret {
                        name: "NPM_TOKEN".into(),
                        scope: "project".into(),
                        project: p.to_string(),
                        updated_at: ago(60 * 26),
                        ..Default::default()
                    },
                    Secret {
                        name: "STRIPE_TEST_KEY".into(),
                        scope: "agent".into(),
                        project: p.to_string(),
                        agent: a.to_string(),
                        updated_at: ago(60 * 3),
                        ..Default::default()
                    },
                ]
            });
            json(StatusCode::OK, &*list)
        }
        (&Method::PUT, ["v1", "agents", p, a, "secrets", name]) if in_fleet(p, a) => {
            let (key, name, p, a) = (
                format!("{p}/{a}"),
                name.to_string(),
                p.to_string(),
                a.to_string(),
            );
            let _: SetSecretRequest = body_json(req).await;
            let secret = Secret {
                name: name.clone(),
                scope: "agent".into(),
                project: p,
                agent: a,
                updated_at: now(),
                ..Default::default()
            };
            let mut s = state().lock().unwrap();
            let list = s.secrets.entry(key).or_default();
            list.retain(|x| x.name != name);
            list.push(secret.clone());
            json(StatusCode::OK, &secret)
        }
        (&Method::DELETE, ["v1", "agents", p, a, "secrets", name]) if in_fleet(p, a) => {
            let mut s = state().lock().unwrap();
            if let Some(list) = s.secrets.get_mut(&format!("{p}/{a}")) {
                list.retain(|x| x.name != *name);
            }
            no_content()
        }
        (&Method::GET, ["v1", "agents", p, a, "connectors"]) if in_fleet(p, a) => {
            let key = format!("{p}/{a}");
            let mut s = state().lock().unwrap();
            let list = s
                .connectors
                .entry(key.clone())
                .or_insert_with(|| connectors_of(&key));
            json(StatusCode::OK, &*list)
        }
        (&Method::DELETE, ["v1", "agents", p, a, "connectors", name]) if in_fleet(p, a) => {
            let mut s = state().lock().unwrap();
            if let Some(list) = s.connectors.get_mut(&format!("{p}/{a}")) {
                list.retain(|x| x.name != *name);
            }
            no_content()
        }
        (
            &Method::POST,
            [
                "v1",
                "agents",
                p,
                a,
                "connectors",
                name,
                action @ ("connect" | "disconnect"),
            ],
        ) if in_fleet(p, a) => {
            let key = format!("{p}/{a}");
            let mut s = state().lock().unwrap();
            let list = s
                .connectors
                .entry(key.clone())
                .or_insert_with(|| connectors_of(&key));
            let Some(c) = list.iter_mut().find(|x| x.name == *name) else {
                return Ok(not_found(&path));
            };
            if *action == "connect" {
                c.status = CONNECTOR_CONNECTED.into();
                c.connected_at = Some(now());
            } else {
                c.status = CONNECTOR_NONE.into();
                c.connected_at = None;
            }
            let c = c.clone();
            if *action == "connect" {
                json(
                    StatusCode::OK,
                    &ConnectResult {
                        connector: c,
                        ..Default::default()
                    },
                )
            } else {
                json(StatusCode::OK, &c)
            }
        }
        (&Method::GET, ["v1", "archived-agents", p, a]) if past(p, a) => {
            let list = fake.review.lock().unwrap().past(p);
            match list.into_iter().find(|x| x.name == *a) {
                Some(mut x) => {
                    x.tokens = 1_840_000;
                    x.cost_usd = 3.12;
                    json(StatusCode::OK, &x)
                }
                None => not_found(&path),
            }
        }
        (&Method::DELETE, ["v1", "archived-agents", p, a]) if past(p, a) => {
            state()
                .lock()
                .unwrap()
                .deleted_past
                .push(format!("{p}/{a}"));
            no_content()
        }
        (&Method::POST, ["v1", "archived-agents", p, a, "continue"]) if past(p, a) => json(
            StatusCode::ACCEPTED,
            &Job {
                id: format!("job-{}", rand_id() % 100000),
                kind: "continue".into(),
                target: format!("{p}/{a}"),
                status: "running".into(),
                created_at: now(),
                ..Default::default()
            },
        ),
        // An agent's own spend; the project's ledger is the real daemon's.
        (&Method::GET, ["v1", "tokens"])
            if fake.is_fake(&q("project")) && !q("agent").is_empty() =>
        {
            let agent = q("agent");
            let total = 2_400_000 + (agent.len() as i64 * 13_000);
            json(
                StatusCode::OK,
                &TokenReport {
                    until: now(),
                    total,
                    input: total / 10,
                    output: total / 20,
                    cache_read: total * 8 / 10,
                    cost_usd: total as f64 / 1_000_000. * 1.9,
                    avg_tps: 61.4,
                    agents: vec![AgentTokens {
                        project: q("project"),
                        agent: agent.clone(),
                        r#ref: format!("{}/{agent}", q("project")),
                        ai: "claude".into(),
                        exists: true,
                        turns: 38,
                        total,
                        cost_usd: total as f64 / 1_000_000. * 1.9,
                        avg_tps: 61.4,
                        ..Default::default()
                    }],
                    ..Default::default()
                },
            )
        }
        (&Method::GET, ["v1", "agents" | "archived-agents", p, a, "files"])
            if in_fleet(p, a) || past(p, a) =>
        {
            let root = fake.machines.lock().unwrap().root.clone();
            let mut files = Vec::new();
            walk(&root, "", &mut files);
            files.extend(SAMPLES.iter().map(|s| format!("samples/{s}")));
            files.sort();
            json(
                StatusCode::OK,
                &WorktreeFiles {
                    truncated: files.len() >= 20000,
                    files,
                },
            )
        }
        (&Method::GET, ["v1", "agents" | "archived-agents", p, a, "files", "sqlite"])
            if in_fleet(p, a) || past(p, a) =>
        {
            json(
                StatusCode::OK,
                &sqlite(
                    &q("table"),
                    q("offset").parse().unwrap_or(0),
                    q("limit").parse().unwrap_or(0),
                ),
            )
        }
        _ => return Err(Box::new(req)),
    };
    Ok(res)
}

/// serve_preview answers the preview proxy's port: one page, naming the host
/// it was asked for (port.agent.project.localhost).
pub async fn serve_preview() {
    let Ok(listener) = tokio::net::TcpListener::bind(("0.0.0.0", PREVIEW_PORT)).await else {
        eprintln!("preview port {PREVIEW_PORT} is taken");
        return;
    };
    loop {
        let Ok((mut stream, _)) = listener.accept().await else {
            continue;
        };
        tokio::spawn(async move {
            use tokio::io::{AsyncReadExt as _, AsyncWriteExt as _};
            let mut buf = vec![0u8; 8192];
            let n = stream.read(&mut buf).await.unwrap_or(0);
            let head = String::from_utf8_lossy(&buf[..n]);
            let host = head
                .lines()
                .find_map(|l| {
                    l.strip_prefix("Host: ")
                        .or_else(|| l.strip_prefix("host: "))
                })
                .unwrap_or("localhost")
                .to_string();
            let target = head.split_whitespace().nth(1).unwrap_or("/").to_string();
            let body = format!(
                "<!doctype html><html><head><title>Invoice due dates</title><style>body{{font:15px system-ui,sans-serif;margin:0;background:#f7f7fb;color:#1a1a1f}}header{{background:#7c8cff;color:white;padding:18px 28px;font-weight:600;font-size:18px}}main{{padding:28px}}.card{{background:white;border-radius:12px;padding:20px;box-shadow:0 2px 10px #0001;max-width:560px}}table{{border-collapse:collapse;width:100%}}td,th{{text-align:left;padding:8px;border-bottom:1px solid #eee}}button{{background:#7c8cff;color:white;border:0;border-radius:8px;padding:9px 16px;font-weight:600;margin-top:14px}}code{{color:#666}}</style></head><body><header>Invoices · dev server</header><main><div class=card><h2 style=margin-top:0>Due this week</h2><table><tr><th>Client</th><th>Due</th><th>Amount</th></tr><tr><td>Acme Corp</td><td>Nov 7</td><td>$4,200</td></tr><tr><td>Globex</td><td>Nov 9</td><td>$1,180</td></tr><tr><td>Initech</td><td>Nov 10</td><td>$760</td></tr></table><button>Send reminders</button><p><code>{host}{target}</code></p></div></main></body></html>"
            );
            let res = format!(
                "HTTP/1.1 200 OK\r\ncontent-type: text/html; charset=utf-8\r\ncontent-length: {}\r\nconnection: close\r\n\r\n{body}",
                body.len()
            );
            let _ = stream.write_all(res.as_bytes()).await;
        });
    }
}

/// nal_units splits an Annex B stream at its start codes.
fn nal_units(buf: &[u8]) -> Vec<(usize, usize)> {
    let mut starts = Vec::new();
    let mut i = 0;
    while i + 3 <= buf.len() {
        if buf[i] == 0 && buf[i + 1] == 0 && buf[i + 2] == 1 {
            starts.push(i + 3);
            i += 3;
        } else {
            i += 1;
        }
    }
    starts
        .iter()
        .enumerate()
        .map(|(k, &s)| {
            let mut end = starts.get(k + 1).map_or(buf.len(), |n| n - 3);
            // A four-byte start code's leading zero belongs to it.
            while end > s && buf[end - 1] == 0 && starts.get(k + 1).is_some() {
                end -= 1;
            }
            (s, end)
        })
        .collect()
}

/// android_stream is the device stream (internal/devicestream's wire
/// format) of a made-up phone: ffmpeg's test pattern at a phone's size,
/// encoded by libx264 with an access unit delimiter before each frame.
pub fn android_stream(req: Request<Incoming>) -> Response<Body> {
    use futures_util::{SinkExt as _, StreamExt as _};
    use tokio::io::AsyncReadExt as _;
    use tokio_tungstenite::tungstenite::{Message, handshake::derive_accept_key, protocol::Role};
    let Some(key) = req.headers().get("sec-websocket-key").cloned() else {
        return not_found("android/stream");
    };
    tokio::spawn(async move {
        let Ok(upgraded) = hyper::upgrade::on(req).await else {
            return;
        };
        let ws = tokio_tungstenite::WebSocketStream::from_raw_socket(
            hyper_util::rt::TokioIo::new(upgraded),
            Role::Server,
            None,
        )
        .await;
        let (mut sink, mut source) = ws.split();
        let Ok(mut child) = tokio::process::Command::new("ffmpeg")
            .args([
                "-loglevel",
                "error",
                "-re",
                "-f",
                "lavfi",
                "-i",
                "testsrc2=size=360x780:rate=15",
                "-c:v",
                "libx264",
                "-preset",
                "ultrafast",
                "-tune",
                "zerolatency",
                "-profile:v",
                "baseline",
                "-pix_fmt",
                "yuv420p",
                "-x264-params",
                "aud=1:keyint=60",
                "-f",
                "h264",
                "-",
            ])
            .stdout(std::process::Stdio::piped())
            .stdin(std::process::Stdio::null())
            .kill_on_drop(true)
            .spawn()
        else {
            let _ = sink
                .send(Message::Binary(
                    [&[3u8][..], br#"{"error":"no ffmpeg"}"#].concat().into(),
                ))
                .await;
            return;
        };
        let mut out = child.stdout.take().unwrap();
        let up = async {
            let mut buf: Vec<u8> = Vec::new();
            let mut chunk = vec![0u8; 64 * 1024];
            let (mut sps, mut pps): (Vec<u8>, Vec<u8>) = (Vec::new(), Vec::new());
            let mut configured = false;
            while let Ok(n) = out.read(&mut chunk).await {
                if n == 0 {
                    break;
                }
                buf.extend_from_slice(&chunk[..n]);
                // Every complete access unit: from one delimiter to the next.
                let units = nal_units(&buf);
                let auds: Vec<usize> = units
                    .iter()
                    .enumerate()
                    .filter(|(_, (s, e))| e > s && buf[*s] & 0x1f == 9)
                    .map(|(i, _)| i)
                    .collect();
                if auds.len() < 2 {
                    continue;
                }
                let last = auds[auds.len() - 1];
                let mut frames: Vec<(bool, Vec<u8>)> = Vec::new();
                let mut au = Vec::new();
                let mut key = false;
                for (i, &(s, e)) in units.iter().enumerate().take(last) {
                    if i < auds[0] || e <= s {
                        continue;
                    }
                    let nal = &buf[s..e];
                    match nal[0] & 0x1f {
                        9 => {
                            if !au.is_empty() {
                                frames.push((key, std::mem::take(&mut au)));
                            }
                            key = false;
                            continue;
                        }
                        7 => sps = nal.to_vec(),
                        8 => pps = nal.to_vec(),
                        5 => key = true,
                        _ => {}
                    }
                    au.extend_from_slice(&(nal.len() as u32).to_be_bytes());
                    au.extend_from_slice(nal);
                }
                if !au.is_empty() {
                    frames.push((key, au));
                }
                let cut = units[last].0 - 3;
                buf.drain(..cut);
                for (key, data) in frames {
                    if !configured && key && sps.len() > 3 && !pps.is_empty() {
                        let mut avcc = vec![1, sps[1], sps[2], sps[3], 0xff, 0xe1];
                        avcc.extend_from_slice(&(sps.len() as u16).to_be_bytes());
                        avcc.extend_from_slice(&sps);
                        avcc.push(1);
                        avcc.extend_from_slice(&(pps.len() as u16).to_be_bytes());
                        avcc.extend_from_slice(&pps);
                        use base64::Engine as _;
                        let config = serde_json::json!({
                            "v": 1, "platform": "android",
                            "codec": format!("avc1.{:02x}{:02x}{:02x}", sps[1], sps[2], sps[3]),
                            "width": 360, "height": 780,
                            "description": base64::engine::general_purpose::STANDARD.encode(&avcc),
                        });
                        let msg = [&[1u8][..], config.to_string().as_bytes()].concat();
                        if sink.send(Message::Binary(msg.into())).await.is_err() {
                            return;
                        }
                        configured = true;
                    }
                    if !configured {
                        continue;
                    }
                    let mut msg = vec![2u8, key as u8];
                    msg.extend_from_slice(&(rand_id() / 1000).to_be_bytes());
                    msg.extend_from_slice(&data);
                    if sink.send(Message::Binary(msg.into())).await.is_err() {
                        return;
                    }
                }
            }
        };
        let down = async { while let Some(Ok(_)) = source.next().await {} };
        tokio::select! {
            _ = up => {}
            _ = down => {}
        }
        let _ = child.kill().await;
    });
    Response::builder()
        .status(StatusCode::SWITCHING_PROTOCOLS)
        .header("connection", "Upgrade")
        .header("upgrade", "websocket")
        .header("sec-websocket-accept", derive_accept_key(key.as_bytes()))
        .body(Full::new(Bytes::new()).boxed())
        .unwrap()
}
