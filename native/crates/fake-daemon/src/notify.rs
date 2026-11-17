//! Notifications on demand, for the app's notices and the tray: POST
//! /fake/notify {kind?, project?, agent?, title?, text?} keeps a question as
//! the daemon does and sends its `notification` event; GET /v1/notifications
//! and POST /v1/notifications/seen answer from what was kept, as
//! internal/daemon/notifications.go does (newest first, unseen=1 leaving the
//! seen ones out, a `notification.seen` event with the request).
use std::collections::HashMap;
use std::sync::Mutex;

use agentbox_api::*;
use http_body_util::BodyExt;
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, json, now};

static KEPT: Mutex<Vec<Notification>> = Mutex::new(Vec::new());

#[derive(serde::Deserialize, Default)]
#[serde(default)]
struct Ask {
    kind: String,
    project: String,
    agent: String,
    title: String,
    text: String,
}

pub async fn route(
    fake: &Fake,
    req: Request<Incoming>,
    query: &HashMap<String, String>,
) -> Result<Response<Body>, Box<Request<Incoming>>> {
    let path = req.uri().path().to_string();
    match (req.method().clone(), path.as_str()) {
        (Method::POST, "/fake/notify") => {
            let body = req.into_body().collect().await.map(|b| b.to_bytes());
            let ask: Ask = body
                .ok()
                .and_then(|b| serde_json::from_slice(&b).ok())
                .unwrap_or_default();
            let mut kept = KEPT.lock().unwrap();
            let project = if ask.project.is_empty() {
                fake.opts.project.clone()
            } else {
                ask.project
            };
            let agent = if ask.agent.is_empty() {
                "agent-02".to_string()
            } else {
                ask.agent
            };
            let n = Notification {
                id: format!("fake-n{}", kept.len() + 1),
                kind: if ask.kind.is_empty() {
                    NOTIFY_QUESTION.into()
                } else {
                    ask.kind
                },
                r#ref: format!("{project}/{agent}"),
                project,
                agent,
                title: ask.title,
                text: if ask.text.is_empty() {
                    "Which port should the dev server use?".into()
                } else {
                    ask.text
                },
                at: now(),
                ..Default::default()
            };
            kept.insert(0, n.clone());
            fake.emit(EVENT_NOTIFICATION, &n);
            Ok(json(StatusCode::OK, &n))
        }
        (Method::GET, "/v1/notifications") => {
            let unseen = query.get("unseen").is_some_and(|v| v == "1" || v == "true");
            let limit = query
                .get("limit")
                .and_then(|l| l.parse().ok())
                .unwrap_or(50usize);
            let all: Vec<Notification> = KEPT
                .lock()
                .unwrap()
                .iter()
                .filter(|n| n.kind != NOTIFY_MEDIA && !(unseen && n.seen))
                .cloned()
                .collect();
            let page = NotificationPage {
                total: all.len() as i64,
                items: all.into_iter().take(limit).collect(),
                ..Default::default()
            };
            Ok(json(StatusCode::OK, &page))
        }
        (Method::POST, "/v1/notifications/seen") => {
            let body = req.into_body().collect().await.map(|b| b.to_bytes());
            let see: SeeNotificationsRequest = body
                .ok()
                .and_then(|b| serde_json::from_slice(&b).ok())
                .unwrap_or_default();
            let mut seen = 0;
            for n in KEPT.lock().unwrap().iter_mut() {
                let named = see.all
                    || see.ids.contains(&n.id)
                    || (!see.kind.is_empty() && see.kind == n.kind);
                if named && !n.seen {
                    n.seen = true;
                    seen += 1;
                }
            }
            fake.emit(EVENT_NOTIFICATIONS_SEEN, &see);
            Ok(json(StatusCode::OK, &SeeNotificationsResult { seen }))
        }
        _ => Err(Box::new(req)),
    }
}
