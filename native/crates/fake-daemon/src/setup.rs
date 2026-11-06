//! An installation that isn't ready yet, to see the native app's setup
//! screens against a daemon that is:
//!
//!   FAKE_FIRST_RUN=1   GET /v1/settings says firstRun until a PATCH says
//!                      firstRunDone (settings.go).
//!   FAKE_IMAGE=building|failed   the base image builds, or failed to, with
//!                      a job and its log (GET /v1/image, /v1/jobs/{id}[/log]).
//!   FAKE_MACHINE=ok    GET /v1/setup has the machine's steps done (Incus,
//!                      user mapping, storage), the rest as the daemon says.
//!   FAKE_DISK=low|full GET /v1/disk is short of space (diskguard.go).
//!
//! It also answers what the top bar's meters read that the real daemon here
//! can't (no Incus): GET /v1/usage's host, and a Claude reading of the
//! account's limits from a few minutes ago, its windows still open.
use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, SystemTime};

use agentbox_api::*;
use http_body_util::BodyExt;
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, json};

static FIRST_RUN_DONE: AtomicBool = AtomicBool::new(false);
const IMAGE_JOB: &str = "fake-image-build";

fn on(name: &str) -> Option<String> {
    std::env::var(name).ok().filter(|v| !v.is_empty())
}

/// at is a time this far from now, as the daemon writes them.
fn at(secs: i64) -> String {
    let now = SystemTime::now();
    let t = if secs >= 0 {
        now + Duration::from_secs(secs as u64)
    } else {
        now - Duration::from_secs((-secs) as u64)
    };
    humantime::format_rfc3339_seconds(t).to_string()
}

fn image_log() -> String {
    [
        "Downloading image-0.42 part 1 of 3",
        "Downloading image-0.42 part 2 of 3",
        "Joining the parts and checking them",
        "Importing the base image into Incus",
        "Installing Claude Code 2.1.4",
    ]
    .join("\n")
}

pub async fn route(
    fake: &Fake,
    req: Request<Incoming>,
    _query: &HashMap<String, String>,
) -> Result<Response<Body>, Box<Request<Incoming>>> {
    let path = req.uri().path().to_string();
    let method = req.method().clone();
    let image = on("FAKE_IMAGE");
    match (&method, path.as_str()) {
        (&Method::GET, "/v1/settings") if on("FAKE_FIRST_RUN").is_some() => {
            let Some(up) = &fake.opts.upstream else {
                return Err(Box::new(req));
            };
            let res = crate::proxy(up, req).await;
            let Ok(res) = res else {
                return Ok(json(StatusCode::BAD_GATEWAY, &ApiError::default()));
            };
            let body = res.into_body().collect().await.map(|b| b.to_bytes());
            let mut s: Settings = body
                .ok()
                .and_then(|b| serde_json::from_slice(&b).ok())
                .unwrap_or_default();
            s.first_run = !FIRST_RUN_DONE.load(Ordering::Relaxed);
            Ok(json(StatusCode::OK, &s))
        }
        (&Method::GET, "/v1/setup") if on("FAKE_MACHINE").is_some() => {
            let Some(up) = &fake.opts.upstream else {
                return Err(Box::new(req));
            };
            let Ok(res) = crate::proxy(up, req).await else {
                return Ok(json(StatusCode::BAD_GATEWAY, &ApiError::default()));
            };
            let body = res.into_body().collect().await.map(|b| b.to_bytes());
            let mut s: SetupStatus = body
                .ok()
                .and_then(|b| serde_json::from_slice(&b).ok())
                .unwrap_or_default();
            for c in &mut s.checks {
                if matches!(c.id.as_str(), "incus" | "host" | "storage") {
                    c.status = "ok".into();
                    c.detail = String::new();
                    c.fix = String::new();
                }
                if c.id == "image" && image.is_some() {
                    c.status = "building".into();
                    c.detail = "Importing the base image into Incus".into();
                    c.job = IMAGE_JOB.into();
                }
            }
            Ok(json(StatusCode::OK, &s))
        }
        (&Method::PATCH, "/v1/settings") if on("FAKE_FIRST_RUN").is_some() => {
            let (parts, body) = req.into_parts();
            let body = body
                .collect()
                .await
                .map(|b| b.to_bytes())
                .unwrap_or_default();
            let r: UpdateSettingsRequest = serde_json::from_slice(&body).unwrap_or_default();
            if r.first_run_done == Some(true) {
                FIRST_RUN_DONE.store(true, Ordering::Relaxed);
            }
            // The rest is the real daemon's, without firstRunDone.
            let Some(up) = &fake.opts.upstream else {
                return Ok(json(StatusCode::OK, &Settings::default()));
            };
            let mut v: serde_json::Value = serde_json::from_slice(&body).unwrap_or_default();
            if let Some(o) = v.as_object_mut() {
                o.remove("firstRunDone");
            }
            let bytes = serde_json::to_vec(&v).unwrap_or_default();
            let mut parts = parts;
            parts.headers.remove(hyper::header::CONTENT_LENGTH);
            let req = Request::from_parts(parts, http_body_util::Full::new(bytes.into()));
            let res = crate::proxy_full(up, req).await;
            let body = match res {
                Ok(r) => r.into_body().collect().await.map(|b| b.to_bytes()).ok(),
                Err(_) => None,
            };
            let mut s: Settings = body
                .and_then(|b| serde_json::from_slice(&b).ok())
                .unwrap_or_default();
            s.first_run = !FIRST_RUN_DONE.load(Ordering::Relaxed);
            Ok(json(StatusCode::OK, &s))
        }
        (&Method::GET, "/v1/image") if image.is_some() => {
            let failed = image.as_deref() == Some("failed");
            Ok(json(
                StatusCode::OK,
                &ImageStatus {
                    ready: false,
                    build: if failed { "failed" } else { "building" }.into(),
                    job: IMAGE_JOB.into(),
                    step: if failed {
                        String::new()
                    } else {
                        "Importing the base image into Incus".into()
                    },
                    error: if failed {
                        "incus image import: no space left on device".into()
                    } else {
                        String::new()
                    },
                    waiting: if failed { 0 } else { 2 },
                    ..Default::default()
                },
            ))
        }
        (&Method::GET, p) if p == format!("/v1/jobs/{IMAGE_JOB}") && image.is_some() => {
            let failed = image.as_deref() == Some("failed");
            Ok(json(
                StatusCode::OK,
                &Job {
                    id: IMAGE_JOB.into(),
                    kind: "image-build".into(),
                    target: "base".into(),
                    status: if failed { "failed" } else { JOB_RUNNING }.into(),
                    error: if failed {
                        "incus image import: no space left on device".into()
                    } else {
                        String::new()
                    },
                    created_at: at(-240),
                    ..Default::default()
                },
            ))
        }
        (&Method::GET, p) if p == format!("/v1/jobs/{IMAGE_JOB}/log") && image.is_some() => {
            Ok(Response::builder()
                .header("content-type", "text/plain")
                .body(http_body_util::Full::new(bytes::Bytes::from(image_log())).boxed())
                .unwrap())
        }
        (&Method::POST, "/v1/image/build") if image.is_some() => Ok(json(
            StatusCode::OK,
            &Job {
                id: IMAGE_JOB.into(),
                kind: "image-build".into(),
                status: JOB_RUNNING.into(),
                ..Default::default()
            },
        )),
        (&Method::GET, "/v1/disk") if on("FAKE_DISK").is_some() => {
            let full = on("FAKE_DISK").as_deref() == Some("full");
            let gib = 1024 * 1024 * 1024;
            let free = if full { 6 * gib } else { 21 * gib };
            let level = if full { "full" } else { "low" };
            Ok(json(
                StatusCode::OK,
                &DiskGuard {
                    level: level.into(),
                    disks: vec![DiskGuardDisk {
                        label: "Worktrees, media, AgentBox's data, Incus".into(),
                        path: "/home/lint/.local/share/agentbox".into(),
                        free,
                        total: 340 * gib,
                        floor: 16 * gib,
                        level: level.into(),
                        advice: "Destroy agents you're done with. `agentbox package-cache --clear` empties the shared caches.".into(),
                    }],
                    paused: if full {
                        vec!["demo/agent-02".into(), "demo/agent-05".into()]
                    } else {
                        Vec::new()
                    },
                    since: at(-600),
                    ..Default::default()
                },
            ))
        }
        (&Method::GET, "/v1/usage") => {
            let gib = 1024 * 1024 * 1024;
            Ok(json(
                StatusCode::OK,
                &Usage {
                    host: HostUsage {
                        cpu: 37.0,
                        cores: 16,
                        mem_used: 21 * gib,
                        mem_total: 31 * gib,
                        pool_used: 118 * gib,
                        pool_total: 340 * gib,
                        ..Default::default()
                    },
                    ..Default::default()
                },
            ))
        }
        (&Method::GET, "/v1/limits") if req.uri().query().is_none() => Ok(json(
            StatusCode::OK,
            &vec![
                ClaudeLimit {
                    account: "default".into(),
                    default: true,
                    at: at(-180),
                    status: "allowed".into(),
                    windows: vec![
                        ClaudeLimitWindow {
                            name: "five_hour".into(),
                            label: "5-hour".into(),
                            utilization: 0.21,
                            resets_at: at(2 * 3600 + 58 * 60 + 30),
                        },
                        ClaudeLimitWindow {
                            name: "seven_day".into(),
                            label: "Weekly".into(),
                            utilization: 0.46,
                            resets_at: at(3 * 86400),
                        },
                    ],
                    ..Default::default()
                },
                ClaudeLimit {
                    account: "work".into(),
                    at: at(-3600),
                    status: "allowed".into(),
                    windows: vec![
                        ClaudeLimitWindow {
                            name: "five_hour".into(),
                            label: "5-hour".into(),
                            utilization: 0.72,
                            resets_at: at(40 * 60),
                        },
                        ClaudeLimitWindow {
                            name: "seven_day".into(),
                            label: "Weekly".into(),
                            utilization: 0.88,
                            resets_at: at(86400),
                        },
                    ],
                    ..Default::default()
                },
            ],
        )),
        _ => Err(Box::new(req)),
    }
}
