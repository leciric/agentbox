//! Agents asking the user, on demand, the way the daemon tells it, for the
//! app's prompts and their tests (native/crates/fake-daemon/tests/asks.rs):
//!
//! - POST /fake/permission {project?, agent, title?, command?}: the agent's AI
//!   tool asks permission (ACP session/request_permission, chat.go). Its chat
//!   gets the tool call and the request, its session goes `waiting`, and its
//!   project's chat gets the card (ChatAgentPermission), as
//!   internal/chat/leadpermission.go puts it there. Answering it on the
//!   agent's route (main.rs) settles both and the session runs again.
//! - POST /fake/request {project?, agent, kind, secretName?, connector?, url?,
//!   ports?, question?}: request_credential / request_connector, a Question
//!   with a Kind that only the user answers, from a card. With no kind, an
//!   `agentbox ask` the lead escalated to the user. Either way the daemon's
//!   `question` event, and a `notification` (notifyQuestion).
//! - GET /v1/projects/{p}/questions (?all=1 for the settled too) and POST
//!   …/questions/{id}/credential answer from what was kept; GET/PUT
//!   …/connectors, POST …/connectors/{name}/connect, PUT …/secrets/{name} and
//!   POST /v1/auth/github are there for the cards' answers.
use std::collections::HashMap;
use std::sync::Mutex;

use agentbox_api::*;
use http_body_util::BodyExt;
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, json, now};

/// The questions asked, oldest first: one decision the lead passed on, from
/// the start, so a project shows someone asking.
static QUESTIONS: Mutex<Vec<Question>> = Mutex::new(Vec::new());
static CONNECTORS: Mutex<Vec<Connector>> = Mutex::new(Vec::new());
static ACCOUNTS: Mutex<Vec<GitHubAccount>> = Mutex::new(Vec::new());

fn seed(project: &str) {
    let mut qs = QUESTIONS.lock().unwrap();
    if qs.is_empty() {
        qs.push(Question {
            id: "q1".into(),
            project: project.into(),
            agent: "agent-03".into(),
            r#ref: format!("{project}/agent-03"),
            question: "Should the reminders page paginate?".into(),
            status: "escalated".into(),
            escalation: "It's a product decision.".into(),
            created_at: now(),
            ..Default::default()
        });
    }
}

/// github_accounts are the accounts GET /v1/auth lists: one to give, and
/// any logged in from a card.
pub fn github_accounts() -> Vec<GitHubAccount> {
    let mut a = ACCOUNTS.lock().unwrap();
    if a.is_empty() {
        a.push(GitHubAccount {
            name: "personal".into(),
            default: true,
            saved_at: now(),
            login: "octocat".into(),
        });
    }
    a.clone()
}

#[derive(serde::Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct Ask {
    project: String,
    agent: String,
    title: String,
    command: String,
    kind: String,
    secret_name: String,
    connector: String,
    url: String,
    ports: Vec<i64>,
    question: String,
}

async fn body<T: serde::de::DeserializeOwned + Default>(req: Request<Incoming>) -> T {
    req.into_body()
        .collect()
        .await
        .ok()
        .and_then(|b| serde_json::from_slice(&b.to_bytes()).ok())
        .unwrap_or_default()
}

fn rand_id() -> String {
    format!(
        "{:x}",
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_nanos()
            % 0xffff_ffff
    )
}

pub async fn route(
    fake: &Fake,
    req: Request<Incoming>,
    query: &HashMap<String, String>,
) -> Result<Response<Body>, Box<Request<Incoming>>> {
    let path = req.uri().path().to_string();
    let parts: Vec<&str> = path.trim_matches('/').split('/').collect();
    let method = req.method().clone();
    seed(&fake.opts.project);
    match (&method, parts.as_slice()) {
        (&Method::POST, ["fake", "permission"]) => {
            let ask: Ask = body(req).await;
            let project = or(ask.project, &fake.opts.project);
            let agent = or(ask.agent, "agent-02");
            let item = permission(fake, &project, &agent, &ask.title, &ask.command);
            Ok(json(StatusCode::OK, &serde_json::json!({ "item": item })))
        }
        (&Method::POST, ["fake", "request"]) => {
            let ask: Ask = body(req).await;
            let q = request(fake, ask);
            Ok(json(StatusCode::OK, &q))
        }
        (&Method::GET, ["v1", "projects", p, "questions"]) if fake.is_fake(p) => {
            let all = query.get("all").is_some_and(|v| !v.is_empty());
            let qs: Vec<Question> = QUESTIONS
                .lock()
                .unwrap()
                .iter()
                .filter(|q| q.project == *p)
                .filter(|q| all || q.status == "pending" || q.status == "escalated")
                .cloned()
                .collect();
            Ok(json(StatusCode::OK, &qs))
        }
        (&Method::POST, ["v1", "projects", p, "questions", id, "credential"])
            if fake.is_fake(p) =>
        {
            let (p, id) = (p.to_string(), id.to_string());
            let a: AnswerCredentialRequest = body(req).await;
            Ok(answer(fake, &p, &id, a))
        }
        (&Method::GET, ["v1", "projects", p, "connectors"]) if fake.is_fake(p) => {
            let list: Vec<Connector> = CONNECTORS
                .lock()
                .unwrap()
                .iter()
                .filter(|c| c.project == *p)
                .cloned()
                .collect();
            Ok(json(StatusCode::OK, &list))
        }
        (&Method::PUT, ["v1", "projects", p, "connectors", name]) if fake.is_fake(p) => {
            let (p, name) = (p.to_string(), name.to_string());
            let r: SetConnectorRequest = body(req).await;
            let c = Connector {
                name: name.clone(),
                scope: "project".into(),
                project: p.clone(),
                url: r.url,
                auth: if r.auth.is_empty() {
                    CONNECTOR_O_AUTH.into()
                } else {
                    r.auth
                },
                secret: r.secret,
                header: r.header,
                enabled: true,
                status: CONNECTOR_DISCONNECTED.into(),
                ..Default::default()
            };
            let mut list = CONNECTORS.lock().unwrap();
            list.retain(|x| !(x.project == p && x.name == name));
            list.push(c.clone());
            Ok(json(StatusCode::OK, &c))
        }
        // Signing in: connecting at once, connected a second later, as if
        // the user finished in the browser.
        (&Method::POST, ["v1", "projects", p, "connectors", name, "connect"])
            if fake.is_fake(p) =>
        {
            let (p, name) = (p.to_string(), name.to_string());
            let mut c = Connector::default();
            for x in CONNECTORS.lock().unwrap().iter_mut() {
                if x.project == p && x.name == name {
                    x.status = CONNECTOR_CONNECTING.into();
                    c = x.clone();
                }
            }
            tokio::spawn(async move {
                tokio::time::sleep(std::time::Duration::from_secs(1)).await;
                for x in CONNECTORS.lock().unwrap().iter_mut() {
                    if x.project == p && x.name == name {
                        x.status = CONNECTOR_CONNECTED.into();
                        x.connected_at = Some(now());
                    }
                }
            });
            Ok(json(
                StatusCode::OK,
                &ConnectResult {
                    authorization_url: "about:blank".into(),
                    connector: c,
                    ..Default::default()
                },
            ))
        }
        (&Method::PUT, ["v1", "projects", p, "secrets", name]) if fake.is_fake(p) => {
            let name = name.to_string();
            Ok(json(
                StatusCode::OK,
                &serde_json::json!({ "name": name, "setAt": now() }),
            ))
        }
        (&Method::POST, ["v1", "auth", "github"]) if fake.opts.upstream.is_none() => {
            let r: GitHubTokenRequest = body(req).await;
            github_accounts();
            ACCOUNTS.lock().unwrap().push(GitHubAccount {
                name: r.account.clone(),
                saved_at: now(),
                login: r.account,
                ..Default::default()
            });
            Ok(json(
                StatusCode::OK,
                &serde_json::json!({ "user": "octocat" }),
            ))
        }
        _ => Err(Box::new(req)),
    }
}

fn or(s: String, default: &str) -> String {
    if s.is_empty() { default.into() } else { s }
}

/// permission puts a permission request in an agent's chat and the card in
/// its project's, and says the agent waits on it. It returns the request's
/// item id.
fn permission(fake: &Fake, project: &str, agent: &str, title: &str, command: &str) -> String {
    let r#ref = format!("{project}/{agent}");
    let id = rand_id();
    let call = format!("call-{id}");
    let item = format!("perm-{id}");
    let command = or(command.into(), "npm publish --dry-run");
    let title = or(title.into(), &format!("Run `{command}`"));
    let tool = ChatTool {
        call_id: call.clone(),
        title: title.clone(),
        kind: "execute".into(),
        status: "pending".into(),
        command: command.clone(),
        ..Default::default()
    };
    let opt = |id: &str, name: &str, kind: &str| ChatPermissionOption {
        id: id.into(),
        name: name.into(),
        kind: kind.into(),
        ..Default::default()
    };
    let perm = ChatPermission {
        call_id: call.clone(),
        title,
        options: vec![
            opt("allow", "Allow", "allow_once"),
            opt("always", "Always allow", "allow_always"),
            opt("deny", "Deny", "reject_once"),
        ],
        ..Default::default()
    };
    let turn = fake
        .thread(&r#ref)
        .items
        .iter()
        .rev()
        .find(|i| i.kind == "user")
        .map(|i| i.id.clone())
        .unwrap_or_default();
    let mk = |id: &str, kind: &str| ChatItem {
        id: id.into(),
        turn: turn.clone(),
        kind: kind.into(),
        created_at: now(),
        updated_at: now(),
        ..Default::default()
    };
    let tool_item = ChatItem {
        tool: Some(tool.clone()),
        ..mk(&format!("tool-{id}"), "tool")
    };
    let perm_item = ChatItem {
        permission: Some(perm.clone()),
        ..mk(&item, "permission")
    };
    for it in [tool_item, perm_item] {
        fake.chat_event(&r#ref, |ev, _, items| {
            items.push(it.clone());
            ev.item = Some(it);
        });
    }
    fake.chat_event(&r#ref, |ev, s, _| {
        s.state = CHAT_WAITING.into();
        if s.turn_started_at.is_none() {
            s.turn_started_at = Some(now());
        }
        ev.session = Some(s.clone());
    });
    set_chat(fake, &r#ref, CHAT_WAITING);
    // The card in the project's chat.
    let (title_of, ai) = fake
        .fleet
        .lock()
        .unwrap()
        .iter()
        .find(|a| a.r#ref == r#ref)
        .map(|a| (a.title.clone(), a.ai.clone()))
        .unwrap_or_default();
    let mut card_tool = tool;
    card_tool.output = String::new();
    let card = ChatAgentPermission {
        agent: agent.into(),
        r#ref: r#ref.clone(),
        title: title_of,
        ai,
        item: item.clone(),
        permission: perm.clone(),
        tool: Some(card_tool),
    };
    let lead = format!("{project}/lead");
    let card_item = ChatItem {
        id: format!("ap-{id}"),
        kind: "notice".into(),
        text: format!("{agent} asks permission: {}", perm.title),
        agent_permission: Some(card),
        created_at: now(),
        updated_at: now(),
        ..Default::default()
    };
    fake.chat_event(&lead, |ev, _, items| {
        let mut it = card_item;
        it.turn = items.last().map(|i| i.turn.clone()).unwrap_or_default();
        items.push(it.clone());
        ev.item = Some(it);
    });
    item
}

/// permission_answered settles an agent's request wherever it shows: the
/// card in its project's chat follows it, and the session runs again.
pub fn permission_answered(fake: &Fake, project: &str, agent: &str, item: &str, outcome: &str) {
    let r#ref = format!("{project}/{agent}");
    // Another request made here still waiting keeps it waiting. (The
    // fixtures' own request, w-perm, is only there to be drawn: the
    // fixture's session never waited on it.)
    let still = fake.thread(&r#ref).items.iter().any(|i| {
        i.id != item
            && i.id.starts_with("perm-")
            && i.permission.as_ref().is_some_and(|p| p.outcome.is_empty())
    });
    if !still {
        fake.chat_event(&r#ref, |ev, s, _| {
            s.state = CHAT_RUNNING.into();
            ev.session = Some(s.clone());
        });
        set_chat(fake, &r#ref, CHAT_RUNNING);
    }
    let lead = format!("{project}/lead");
    fake.chat_event(&lead, |ev, _, items| {
        if let Some(it) = items.iter_mut().find(|i| {
            i.agent_permission
                .as_ref()
                .is_some_and(|ap| ap.r#ref == r#ref && ap.item == item)
        }) {
            if let Some(ap) = it.agent_permission.as_mut() {
                ap.permission.outcome = outcome.into();
            }
            it.updated_at = now();
            ev.item = Some(it.clone());
        }
    });
}

fn set_chat(fake: &Fake, r#ref: &str, chat: &str) {
    if let Some(a) = fake
        .fleet
        .lock()
        .unwrap()
        .iter_mut()
        .find(|a| a.r#ref == r#ref)
    {
        a.chat = chat.into();
    }
}

/// request keeps a question asked of the user, and tells it as the daemon
/// does: the question, then the notification.
fn request(fake: &Fake, ask: Ask) -> Question {
    let project = or(ask.project, &fake.opts.project);
    let agent = or(ask.agent, "agent-02");
    let decision = ask.kind.is_empty();
    let question = if !ask.question.is_empty() {
        ask.question
    } else if decision {
        "Which port should the dev server use?".into()
    } else {
        "To open a pull request with the fix.".into()
    };
    let q = Question {
        id: format!("q-{}", rand_id()),
        r#ref: format!("{project}/{agent}"),
        project,
        agent,
        kind: ask.kind,
        secret_name: ask.secret_name,
        connector: ask.connector,
        url: ask.url,
        ports: ask.ports,
        question,
        status: if decision { "escalated" } else { "pending" }.into(),
        created_at: now(),
        ..Default::default()
    };
    QUESTIONS.lock().unwrap().push(q.clone());
    fake.emit(EVENT_QUESTION, &q);
    let title = fake
        .fleet
        .lock()
        .unwrap()
        .iter()
        .find(|a| a.r#ref == q.r#ref)
        .map(|a| a.title.clone())
        .unwrap_or_default();
    crate::notify::keep(
        fake,
        Notification {
            kind: NOTIFY_QUESTION.into(),
            project: q.project.clone(),
            agent: q.agent.clone(),
            r#ref: q.r#ref.clone(),
            title,
            text: q.question.clone(),
            question: q.id.clone(),
            at: now(),
            ..Default::default()
        },
    );
    q
}

/// answer settles a credential request (questions.go's answerCredential):
/// answered with what was given, or refused.
fn answer(fake: &Fake, project: &str, id: &str, a: AnswerCredentialRequest) -> Response<Body> {
    let mut qs = QUESTIONS.lock().unwrap();
    let Some(q) = qs.iter_mut().find(|q| q.id == id && q.project == project) else {
        return json(
            StatusCode::NOT_FOUND,
            &serde_json::json!({ "error": format!("question {id}: not found") }),
        );
    };
    if q.kind.is_empty() {
        return json(
            StatusCode::BAD_REQUEST,
            &serde_json::json!({ "error": "not a credential request" }),
        );
    }
    if q.status != "pending" && q.status != "escalated" {
        return json(
            StatusCode::CONFLICT,
            &serde_json::json!({ "error": format!("question {id} is {}", q.status) }),
        );
    }
    q.status = "answered".into();
    q.answered_by = "user".into();
    q.answered_at = Some(now());
    q.answer = if a.refuse {
        format!("refused: {}", a.reason)
    } else if !a.github_account.is_empty() {
        format!("GitHub account {}", a.github_account)
    } else if !a.connector.is_empty() {
        format!("connector {}", a.connector)
    } else if a.allow {
        "ports allowed".into()
    } else {
        format!("${} is set", q.secret_name)
    };
    let q = q.clone();
    drop(qs);
    fake.emit(EVENT_QUESTION, &q);
    json(StatusCode::OK, &q)
}
