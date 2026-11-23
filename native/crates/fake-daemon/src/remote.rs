//! Use AgentBox in another app (internal/daemon's remote MCP routes), kept in
//! memory: GET /v1/remote/mcp lists three pairings to start with, PATCH
//! turns the tunnel on or off for apps (running at once, as a quick one),
//! POST /v1/remote/mcp/pairings makes one (through the hub with a code, and
//! a token when asked; on this computer or through the tunnel with a token),
//! POST …/pairings/{id}/code gives one a new code or token and DELETE
//! …/pairings/{id} revokes it. The hub is there (`available`), at
//! hub.example.com, unless `FAKE_HUB=none`: then there's none, no pairing
//! through it, and pairing with no route is on this computer, as the daemon
//! does. The local port is at 127.0.0.1:7782.
use std::sync::Mutex;
use std::time::Duration;

use agentbox_api::*;
use bytes::Bytes;
use http_body_util::{BodyExt, Full};
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, bad_request, json, not_found, now};

const URL: &str = "https://hub.example.com/mcp";
const LOCAL: &str = "http://127.0.0.1:7782/mcp";
const TUNNEL: &str = "https://quiet-river-1234.trycloudflare.com/mcp";

static PAIRINGS: Mutex<Option<Vec<MCPPairing>>> = Mutex::new(None);
static TUNNEL_ON: Mutex<bool> = Mutex::new(false);

// no_hub is FAKE_HUB=none: this computer isn't connected to a hub.
fn no_hub() -> bool {
    std::env::var("FAKE_HUB").is_ok_and(|v| v == "none")
}

fn list(pairings: &[MCPPairing]) -> MCPPairings {
    let on = *TUNNEL_ON.lock().unwrap();
    let hub = !no_hub();
    MCPPairings {
        available: hub,
        reason: if hub { "" } else { "no-hub" }.into(),
        hub: if hub { "hub.example.com" } else { "" }.into(),
        url: if hub { URL } else { "" }.into(),
        local: MCPLocal {
            port: 7782,
            url: LOCAL.into(),
            listening: pairings.iter().any(|p| p.route == "local"),
            ..Default::default()
        },
        tunnel: MCPTunnel {
            enabled: on,
            mode: "quick".into(),
            state: if on { "running" } else { "off" }.into(),
            url: if on { TUNNEL.into() } else { String::new() },
            ..Default::default()
        },
        pairings: pairings
            .iter()
            .filter(|p| hub || p.route != "hub")
            .cloned()
            .collect(),
        ..Default::default()
    }
}

fn ago(d: Duration) -> String {
    humantime::format_rfc3339_millis(crate::clock() - d).to_string()
}

/// seed is what a new fake starts with: two apps in use, one never used.
fn seed(fake: &Fake) -> Vec<MCPPairing> {
    vec![
        MCPPairing {
            id: "mcp-3".into(),
            label: "Claude Code".into(),
            route: "local".into(),
            client: "claude-code".into(),
            access: "full".into(),
            created_at: ago(Duration::from_secs(86400)),
            last_used_at: ago(Duration::from_secs(60)),
            ..Default::default()
        },
        MCPPairing {
            id: "mcp-1".into(),
            label: "Claude".into(),
            route: "hub".into(),
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
            route: "hub".into(),
            client: "poke".into(),
            access: "read".into(),
            projects: vec![fake.opts.project.clone()],
            created_at: ago(Duration::from_secs(3600)),
            ..Default::default()
        },
    ]
}

fn secret(pairing: MCPPairing, code: &str, token: bool) -> MCPPairingSecret {
    if pairing.route != "hub" {
        return MCPPairingSecret {
            token: format!(
                "abx_m_{}_{code}_4f9c2d7a1b8e6035c4d2f1a09b7e8c6d",
                pairing.id
            ),
            url: if pairing.route == "local" {
                LOCAL
            } else {
                TUNNEL
            }
            .into(),
            pairing,
            ..Default::default()
        };
    }
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
        (&Method::POST, ["pairings"]) | (&Method::PATCH, []) => req
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
        (Method::GET, []) => json(StatusCode::OK, &list(pairings)),
        (Method::PATCH, []) => {
            let ask: UpdateMCPRequest = serde_json::from_slice(&body).unwrap_or_default();
            if let Some(on) = ask.tunnel {
                *TUNNEL_ON.lock().unwrap() = on;
            }
            json(StatusCode::OK, &list(pairings))
        }
        (Method::POST, ["pairings"]) => {
            let ask: MCPPairRequest = serde_json::from_slice(&body).unwrap_or_default();
            let route = match ask.route.as_str() {
                "" if no_hub() => "local".to_string(),
                "" => "hub".to_string(),
                _ => ask.route,
            };
            if route == "hub" && no_hub() {
                return Ok(bad_request("this computer isn't connected to a hub".into()));
            }
            if route == "tunnel" && !*TUNNEL_ON.lock().unwrap() {
                return Ok(bad_request(
                    "let other apps reach AgentBox through its tunnel first".into(),
                ));
            }
            let n = pairings
                .iter()
                .filter_map(|p| p.id.strip_prefix("mcp-")?.parse::<u32>().ok())
                .max()
                .unwrap_or(0)
                + 1;
            let pairing = MCPPairing {
                id: format!("mcp-{n}"),
                label: ask.label,
                route,
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
