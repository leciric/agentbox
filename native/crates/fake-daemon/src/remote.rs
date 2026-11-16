//! Use AgentBox in another app (internal/daemon's remote MCP routes), kept in
//! memory: GET /v1/remote/mcp lists two pairings to start with, POST
//! /v1/remote/mcp/pairings makes one (with a token when asked), POST
//! …/pairings/{id}/code gives one a new code and DELETE …/pairings/{id}
//! revokes it. The hub is always there (`available`), at hub.example.com.
use std::sync::Mutex;
use std::time::Duration;

use agentbox_api::*;
use bytes::Bytes;
use http_body_util::{BodyExt, Full};
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, json, not_found, now};

const URL: &str = "https://hub.example.com/mcp";

static PAIRINGS: Mutex<Option<Vec<MCPPairing>>> = Mutex::new(None);

fn ago(d: Duration) -> String {
    humantime::format_rfc3339_millis(crate::clock() - d).to_string()
}

/// seed is what a new fake starts with: one app in use, one never used.
fn seed(fake: &Fake) -> Vec<MCPPairing> {
    vec![
        MCPPairing {
            id: "mcp-1".into(),
            label: "Claude".into(),
            client: "claude".into(),
            access: "chat".into(),
            created_at: ago(Duration::from_secs(3 * 86400)),
            last_used_at: ago(Duration::from_secs(240)),
            apps: vec!["Claude".into()],
            ..Default::default()
        },
        MCPPairing {
            id: "mcp-2".into(),
            label: "Poke on my phone".into(),
            client: "poke".into(),
            access: "read".into(),
            projects: vec![fake.opts.project.clone()],
            created_at: ago(Duration::from_secs(3600)),
            ..Default::default()
        },
    ]
}

fn secret(pairing: MCPPairing, code: &str, token: bool) -> MCPPairingSecret {
    MCPPairingSecret {
        token: if token {
            format!("abx_m_{}_4f9c2d7a1b8e6035c4d2f1a09b7e8c6d", pairing.id)
        } else {
            String::new()
        },
        pairing,
        url: URL.into(),
        code: code.into(),
        code_expires_at: humantime::format_rfc3339_millis(
            crate::clock() + Duration::from_secs(600),
        )
        .to_string(),
    }
}

pub async fn route(
    fake: &Fake,
    req: Request<Incoming>,
) -> Result<Response<Body>, Box<Request<Incoming>>> {
    let path = req.uri().path().to_string();
    let Some(rest) = path.strip_prefix("/v1/remote/mcp") else {
        return Err(Box::new(req));
    };
    let parts: Vec<&str> = rest.split('/').filter(|p| !p.is_empty()).collect();
    let method = req.method().clone();
    let body = match (&method, parts.as_slice()) {
        (&Method::POST, ["pairings"]) => req
            .into_body()
            .collect()
            .await
            .map(|b| b.to_bytes())
            .unwrap_or_default(),
        _ => Default::default(),
    };
    let mut kept = PAIRINGS.lock().unwrap();
    let pairings = kept.get_or_insert_with(|| seed(fake));
    Ok(match (method, parts.as_slice()) {
        (Method::GET, []) => json(
            StatusCode::OK,
            &MCPPairings {
                available: true,
                hub: "hub.example.com".into(),
                url: URL.into(),
                pairings: pairings.clone(),
                ..Default::default()
            },
        ),
        (Method::POST, ["pairings"]) => {
            let ask: MCPPairRequest = serde_json::from_slice(&body).unwrap_or_default();
            let n = pairings
                .iter()
                .filter_map(|p| p.id.strip_prefix("mcp-")?.parse::<u32>().ok())
                .max()
                .unwrap_or(0)
                + 1;
            let pairing = MCPPairing {
                id: format!("mcp-{n}"),
                label: ask.label,
                client: ask.client,
                access: ask.access,
                projects: ask.projects,
                created_at: now(),
                ..Default::default()
            };
            pairings.insert(0, pairing.clone());
            json(StatusCode::OK, &secret(pairing, "BCDF-GHJK", ask.token))
        }
        (Method::POST, ["pairings", id, "code"]) => match pairings.iter().find(|p| p.id == *id) {
            Some(p) => json(StatusCode::OK, &secret(p.clone(), "LMNP-QRST", false)),
            None => not_found(&path),
        },
        (Method::DELETE, ["pairings", id]) => {
            let before = pairings.len();
            pairings.retain(|p| p.id != *id);
            if pairings.len() == before {
                not_found(&path)
            } else {
                Response::builder()
                    .status(StatusCode::NO_CONTENT)
                    .body(Full::new(Bytes::new()).boxed())
                    .unwrap()
            }
        }
        _ => not_found(&path),
    })
}
