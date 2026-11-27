//! Shared projects (#813, #821, #822), with FAKE_SHARE=1 and no upstream:
//! this AgentBox ("Leo's AgentBox") shares its project with two paired ones,
//! Ana (every right, her agents may run on her own PC) and Bob (chat only),
//! and Ana's AgentBox shares "webshop" with it, which the sidebar lists under
//! Remote as r1~webshop. Who is where (GET /v1/here) has Ana typing in the
//! project's chat and Bob on agent-02's desktop; the project's lead chat
//! has a message of Ana's, and webshop's conversation is Ana's and yours.
//! The fleet's remote agents put one on Bob's PC and, in webshop, one on
//! this PC. Settings → Share's changes are kept in memory; PUT /v1/here is
//! taken and forgotten.
use std::sync::Mutex;
use std::time::Duration;

use agentbox_api::*;
use bytes::Bytes;
use http_body_util::{BodyExt, Full};
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, json, not_found};

/// REMOTE is the project Ana's AgentBox shares, as this one names it.
pub const REMOTE: &str = "r1~webshop";
const OWNER: &str = "Ana";

static STATUS: Mutex<Option<ShareStatus>> = Mutex::new(None);

/// on is FAKE_SHARE=1 on a fake with no upstream.
pub fn on(fake: &Fake) -> bool {
    fake.opts.upstream.is_none() && std::env::var_os("FAKE_SHARE").is_some()
}

fn ago(d: Duration) -> String {
    humantime::format_rfc3339_millis(crate::clock() - d).to_string()
}

fn mins(n: u64) -> Duration {
    Duration::from_secs(n * 60)
}

fn seed(fake: &Fake) -> ShareStatus {
    ShareStatus {
        name: "Leo's AgentBox".into(),
        slots: 2,
        ready: true,
        peers: vec![
            SharePeer {
                id: "peer-ana".into(),
                name: "Ana's AgentBox".into(),
                paired: ago(mins(9 * 24 * 60)),
                last_seen: ago(mins(1)),
                last_addr: "192.168.1.24".into(),
                all_projects: false,
                projects: vec![fake.opts.project.clone()],
                slots: -1,
                running: 1,
                access: "full".into(),
                own_machine: true,
                machine: true,
                sync: true,
                usage: SharePeerUsage {
                    tokens: 1_840_000,
                    cost_usd: 4.2,
                    agent_minutes: 96,
                },
            },
            SharePeer {
                id: "peer-bob".into(),
                name: "Bob's laptop".into(),
                paired: ago(mins(2 * 24 * 60)),
                last_seen: ago(mins(40)),
                all_projects: true,
                slots: 0,
                access: "chat".into(),
                own_machine: false,
                usage: SharePeerUsage {
                    tokens: 52_000,
                    agent_minutes: 0,
                    ..Default::default()
                },
                ..Default::default()
            },
        ],
        remotes: vec![ShareRemote {
            id: "r1".into(),
            name: "Ana's AgentBox".into(),
            url: "https://192.168.1.24:7780".into(),
            paired: ago(mins(9 * 24 * 60)),
            projects: 1,
            machine: true,
            ..Default::default()
        }],
        ..Default::default()
    }
}

/// projects are the projects other AgentBoxes share with this one.
pub fn projects(fake: &Fake) -> Vec<Project> {
    if !on(fake) {
        return Vec::new();
    }
    vec![Project {
        name: REMOTE.into(),
        display_name: "webshop".into(),
        remote: Some(ProjectRemote {
            id: "r1".into(),
            owner: OWNER.into(),
            access: String::new(),
        }),
        ..Default::default()
    }]
}

/// remote_agents are a project's agents its lead runs on other computers.
pub fn remote_agents(fake: &Fake, project: &str) -> Vec<RemoteAgent> {
    if !on(fake) {
        return Vec::new();
    }
    if project == REMOTE {
        return vec![RemoteAgent {
            machine: "Leo's AgentBox".into(),
            machine_id: "self".into(),
            name: "agent-7".into(),
            title: "Checkout coupons".into(),
            status: "working".into(),
            summary: "Adding the coupon field to the checkout form".into(),
            updated_at: ago(mins(3)),
            shared: true,
            here: true,
            ..Default::default()
        }];
    }
    if project != fake.opts.project {
        return Vec::new();
    }
    vec![
        RemoteAgent {
            machine: "Bob's laptop".into(),
            machine_id: "peer-bob".into(),
            name: "agent-14".into(),
            title: "Dark mode toggle".into(),
            status: "asking".into(),
            summary: "Asks whether the toggle belongs in Settings or the top bar".into(),
            updated_at: ago(mins(6)),
            shared: true,
            ..Default::default()
        },
        RemoteAgent {
            machine: "Ana's AgentBox".into(),
            machine_id: "peer-ana".into(),
            name: "agent-9".into(),
            title: "Flaky login test".into(),
            status: "done".into(),
            summary: "Fixed the race in the login test".into(),
            pr_url: "https://github.com/example/demo/pull/212".into(),
            updated_at: ago(mins(52)),
            shared: true,
            ..Default::default()
        },
    ]
}

fn person(
    id: &str,
    name: &str,
    project: &str,
    session: &str,
    agent: &str,
    tab: &str,
    typing: bool,
) -> PersonHere {
    PersonHere {
        id: id.into(),
        name: name.into(),
        project: project.into(),
        session: session.into(),
        agent: agent.into(),
        tab: tab.into(),
        typing,
        since: ago(mins(4)),
    }
}

fn here(fake: &Fake) -> Vec<ProjectHere> {
    let p = fake.opts.project.as_str();
    vec![
        ProjectHere {
            project: p.into(),
            people: vec![
                person("peer-ana", "Ana", p, "main", "", "", true),
                person("peer-bob", "Bob", p, "", "agent-02", "desktop", false),
            ],
        },
        ProjectHere {
            project: REMOTE.into(),
            people: vec![
                // This app's own user, here through the owner: never shown.
                person("", "Leo", REMOTE, "main", "", "", false),
                person("owner", OWNER, REMOTE, "main", "", "", false),
            ],
        },
    ]
}

fn said(id: &str, kind: &str, text: &str, author: Option<&str>, at: Duration) -> ChatItem {
    ChatItem {
        id: id.into(),
        turn: format!("t-{id}"),
        kind: kind.into(),
        text: text.into(),
        author: author.map(|name| Person {
            id: format!("p-{}", name.to_lowercase()),
            name: name.into(),
        }),
        created_at: ago(at),
        updated_at: ago(at),
        ..Default::default()
    }
}

/// guest_message is a message of Ana's in this project's lead chat.
pub fn guest_message(fake: &Fake) -> Option<ChatItem> {
    on(fake).then(|| {
        said(
            "share-ana-1",
            "user",
            "Ana here: can the lead have an agent look at the flaky login test? I'll review it tonight.",
            Some("Ana"),
            mins(2),
        )
    })
}

/// remote_lead is webshop's conversation, as its owner's daemon keeps it:
/// Ana's messages (authored) and yours (no author: this app's user).
pub fn remote_lead() -> Vec<ChatItem> {
    vec![
        said(
            "w1",
            "user",
            "Leo, the checkout needs a coupon field before Friday. Can you take it?",
            Some(OWNER),
            mins(30),
        ),
        said(
            "w2",
            "assistant",
            "I can start an agent on it. Ana's slots for you are 2, one is free.",
            None,
            mins(29),
        ),
        said(
            "w3",
            "user",
            "Yes please, run it on my PC so it doesn't use Ana's accounts.",
            None,
            mins(5),
        ),
        said(
            "w4",
            "assistant",
            "Started **agent-7** on Leo's PC: it's adding the coupon field to the checkout form.",
            None,
            mins(4),
        ),
    ]
}

fn activity() -> Vec<ShareActivity> {
    let a = |id: i64, m: u64, kind: &str, agent: &str, detail: &str, project: &str| ShareActivity {
        id,
        at: ago(mins(m)),
        kind: kind.into(),
        project: project.into(),
        agent: agent.into(),
        detail: detail.into(),
    };
    vec![
        a(9, 2, "message", "", "", "demo"),
        a(8, 7, "open-desktop", "agent-02", "", "demo"),
        a(7, 15, "create-agent", "agent-9", "chat", "demo"),
        a(6, 31, "prompt", "agent-02", "", "demo"),
        a(5, 58, "answer", "agent-02", "", "demo"),
        a(4, 95, "merge", "", "207", "demo"),
        a(3, 180, "review", "agent-04", "", "demo"),
        a(2, 300, "stop-agent", "agent-04", "", "demo"),
        a(1, 400, "open-terminal", "agent-01", "", "demo"),
    ]
}

fn no_content() -> Response<Body> {
    Response::builder()
        .status(StatusCode::NO_CONTENT)
        .body(Full::new(Bytes::new()).boxed())
        .unwrap()
}

pub async fn route(
    fake: &Fake,
    req: Request<Incoming>,
) -> Result<Response<Body>, Box<Request<Incoming>>> {
    if !on(fake) {
        return Err(Box::new(req));
    }
    let path = req.uri().path().to_string();
    let parts: Vec<String> = path
        .trim_matches('/')
        .split('/')
        .map(|p| p.replace("%7E", "~").replace("%7e", "~"))
        .collect();
    let parts: Vec<&str> = parts.iter().map(String::as_str).collect();
    let method = req.method().clone();
    let ours = matches!(
        parts.as_slice(),
        ["v1", "share", ..] | ["v1", "here", ..] | ["v1", "projects", _, "run-on"]
    ) || matches!(parts.as_slice(), ["v1", "projects", REMOTE, "fleet"]);
    if !ours {
        return Err(Box::new(req));
    }
    let body = req
        .into_body()
        .collect()
        .await
        .map(|b| b.to_bytes())
        .unwrap_or_default();
    let mut kept = STATUS.lock().unwrap();
    let st = kept.get_or_insert_with(|| seed(fake));
    Ok(match (method, parts.as_slice()) {
        (Method::GET, ["v1", "share"]) => json(StatusCode::OK, &*st),
        (Method::PATCH, ["v1", "share"]) => {
            let ask: UpdateShareRequest = serde_json::from_slice(&body).unwrap_or_default();
            if let Some(n) = ask.name {
                st.name = n;
            }
            if let Some(n) = ask.slots {
                st.slots = n;
            }
            json(StatusCode::OK, &*st)
        }
        (Method::POST, ["v1", "share", "pairings"]) => json(
            StatusCode::OK,
            &SharePairing {
                link: "https://192.168.1.31:7780/#share=Xq7v2LmP9cTn4KdR8wYb3HsJ".into(),
                expires: humantime::format_rfc3339_millis(crate::clock() + mins(15)).to_string(),
                ..Default::default()
            },
        ),
        (Method::PATCH, ["v1", "share", "peers", id]) => {
            let ask: UpdateSharePeerRequest = serde_json::from_slice(&body).unwrap_or_default();
            match st.peers.iter_mut().find(|p| p.id == *id) {
                Some(p) => {
                    if let Some(v) = ask.all_projects {
                        p.all_projects = v;
                    }
                    if let Some(v) = ask.projects {
                        p.projects = v;
                    }
                    if let Some(v) = ask.slots {
                        p.slots = v;
                    }
                    if let Some(v) = ask.access {
                        p.access = v;
                    }
                    if let Some(v) = ask.own_machine {
                        p.own_machine = v;
                    }
                    json(StatusCode::OK, &*p)
                }
                None => not_found(&path),
            }
        }
        (Method::DELETE, ["v1", "share", "peers", id]) => {
            st.peers.retain(|p| p.id != *id);
            no_content()
        }
        (Method::GET, ["v1", "share", "peers", id, "activity"]) => {
            if *id == "peer-ana" {
                json(StatusCode::OK, &activity())
            } else {
                json(StatusCode::OK, &Vec::<ShareActivity>::new())
            }
        }
        (Method::POST, ["v1", "share", "remotes"]) => {
            let r = ShareRemote {
                id: format!("r{}", st.remotes.len() + 1),
                name: "Sam's AgentBox".into(),
                url: "https://10.0.0.7:7780".into(),
                paired: ago(Duration::ZERO),
                machine_error: "connecting".into(),
                ..Default::default()
            };
            st.remotes.push(r.clone());
            json(StatusCode::OK, &r)
        }
        (Method::DELETE, ["v1", "share", "remotes", id]) => {
            st.remotes.retain(|r| r.id != *id);
            no_content()
        }
        (Method::GET, ["v1", "here"]) => json(StatusCode::OK, &here(fake)),
        (Method::PUT, ["v1", "here"]) | (Method::DELETE, ["v1", "here", _]) => no_content(),
        (Method::GET, ["v1", "projects", _, "run-on"]) => json(
            StatusCode::OK,
            &ShareRunOn {
                owner: OWNER.into(),
                here: true,
                ..Default::default()
            },
        ),
        (Method::GET, ["v1", "projects", p, "fleet"]) => json(
            StatusCode::OK,
            &Fleet {
                project: p.to_string(),
                remote: remote_agents(fake, p),
                ..Default::default()
            },
        ),
        _ => not_found(&path),
    })
}
