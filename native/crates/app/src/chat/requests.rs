//! The credential and connector requests an agent is blocked on
//! (request_credential, request_connector: a Question with a Kind), and what
//! it takes to answer them: the renderer's CredentialCard.tsx,
//! ConnectorRequestCard.tsx, RequestParts.tsx and lib/credentialRequests.ts.
//!
//! A project's chat lists every one waiting in the project, one row for the
//! requests that ask for the same thing (ProjectCredentialCards), each opening
//! to its card; an agent's chat shows the one that agent waits on, open
//! (ChatCredentialCard). Both sit over the composer, so a request is in view
//! whatever the timeline is scrolled to. The app hands each chat the
//! project's open requests (AppView::sync_chat_requests) as it reads them
//! (GET /v1/projects/{p}/questions, again on each `question` event and on
//! every reconnect), so one asked while you look elsewhere, or before the app
//! started, is there when you come.
//!
//! What is typed here goes to the daemon (POST …/questions/{id}/credential)
//! and from there into the agent's machine: the agent is told what happened,
//! never the value.
use std::collections::{HashMap, HashSet};
use std::time::Duration;

use agentbox_api::client::Method;
use agentbox_api::{
    AnswerCredentialRequest, AuthStatus, CONNECTOR_CONNECTED, CONNECTOR_CONNECTING,
    CONNECTOR_ERROR, CONNECTOR_NONE, CONNECTOR_SECRET, CREDENTIAL_IOS_PORTS, CREDENTIAL_SECRET,
    Client, ConnectResult, Connector, GitHubAccount, GitHubTokenRequest, QUESTION_CONNECTOR,
    Question, SetConnectorRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputState};
use gpui_component::{h_flex, v_flex};

use crate::app::controls::{Btn, button, button_busy};
use crate::app::{PRESETS, Preset};
use crate::theme;

/// is_open is whether a request still waits (isOpenRequest).
pub fn is_open(q: &Question) -> bool {
    q.status == "pending" || q.status == "escalated"
}

/// is_request is whether a question is a request only the user answers, from
/// a card: a credential or a connector, not a decision put in words.
pub fn is_request(q: &Question) -> bool {
    !q.kind.is_empty()
}

/// for_lead is what a project's chat lists: every request waiting in it.
pub fn for_lead(questions: &[Question]) -> Vec<Question> {
    let mut v: Vec<Question> = questions
        .iter()
        .filter(|q| is_request(q) && is_open(q))
        .cloned()
        .collect();
    v.sort_by(|a, b| a.created_at.cmp(&b.created_at));
    v
}

/// for_agent is what an agent's chat shows: the request it is blocked on,
/// the newest of its own still waiting (waitingCredential); an agent waits on
/// one call at a time.
pub fn for_agent(questions: &[Question], agent: &str) -> Vec<Question> {
    questions
        .iter()
        .filter(|q| q.agent == agent && is_request(q) && is_open(q))
        .max_by(|a, b| a.created_at.cmp(&b.created_at))
        .cloned()
        .into_iter()
        .collect()
}

/// A connector request as the card reads it (lib/connectors.ts's
/// connectorRequest): the name and server the agent gave, and the catalog's
/// entry only when the server is that entry's own.
#[derive(Clone, Debug, PartialEq)]
pub struct ConnectorAsk {
    pub name: String,
    pub url: String,
    pub host: String,
    pub preset: Option<&'static Preset>,
}

fn same_server(a: &str, b: &str) -> bool {
    a.trim_end_matches('/') == b.trim_end_matches('/')
}

/// host_of is a URL's host (with its port), or "" when it isn't one.
pub fn host_of(url: &str) -> String {
    let Some((scheme, rest)) = url.split_once("://") else {
        return String::new();
    };
    if scheme.is_empty() {
        return String::new();
    }
    let auth = rest.split(['/', '?', '#']).next().unwrap_or("");
    let host = auth.rsplit('@').next().unwrap_or("");
    host.to_lowercase()
}

/// connector_name makes a name from a server's address (connectorName):
/// https://mcp.acme.io/mcp is "acme"; one on this machine gets none.
pub fn connector_name(url: &str) -> String {
    let host = host_of(url);
    let host = if host.starts_with('[') {
        return String::new();
    } else {
        host.split(':').next().unwrap_or("").to_string()
    };
    if host.is_empty()
        || host == "localhost"
        || host.chars().all(|c| c.is_ascii_digit() || c == '.')
    {
        return String::new();
    }
    let mut parts: Vec<&str> = host
        .split('.')
        .filter(|p| !p.is_empty() && !["www", "mcp", "api"].contains(p))
        .collect();
    let n = parts.len();
    if n > 2
        && ["co", "com", "net", "org", "gov", "ac", "edu"].contains(&parts[n - 2])
        && parts[n - 1].len() == 2
    {
        parts.pop();
    }
    let name = if parts.len() > 1 {
        parts[parts.len() - 2]
    } else {
        parts.first().copied().unwrap_or("")
    };
    let name: String = name
        .to_lowercase()
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '_' || c == '-' {
                c
            } else {
                '-'
            }
        })
        .collect();
    name.trim_start_matches('-').chars().take(40).collect()
}

pub fn connector_ask(q: &Question) -> Option<ConnectorAsk> {
    if q.kind != QUESTION_CONNECTOR {
        return None;
    }
    let asked = q.connector.trim().to_lowercase();
    let given = q.url.trim();
    let preset = if given.is_empty() {
        PRESETS
            .iter()
            .find(|p| p.name == asked || same_server(p.url, &asked))
    } else {
        PRESETS.iter().find(|p| same_server(p.url, given))
    };
    let url = if given.is_empty() {
        preset.map(|p| p.url.to_string()).unwrap_or_default()
    } else {
        given.to_string()
    };
    let name = if !asked.is_empty() {
        asked
    } else if let Some(p) = preset {
        p.name.to_string()
    } else {
        connector_name(&url)
    };
    if name.is_empty() {
        return None;
    }
    Some(ConnectorAsk {
        host: host_of(&url),
        name,
        url,
        preset,
    })
}

/// target is what a request asks for, the same for two requests one answer
/// settles (requestTarget): any GitHub account, a secret by its name, a
/// connector by its name and server.
pub fn target(q: &Question) -> String {
    if q.kind == QUESTION_CONNECTOR {
        return match connector_ask(q) {
            Some(c) => format!("connector:{}@{}", c.name, c.url.trim_end_matches('/')),
            None => format!("id:{}", q.id),
        };
    }
    if q.kind == CREDENTIAL_SECRET {
        return if q.secret_name.is_empty() {
            format!("id:{}", q.id)
        } else {
            format!("secret:{}", q.secret_name)
        };
    }
    if q.kind == CREDENTIAL_IOS_PORTS {
        let ports: Vec<String> = q.ports.iter().map(i64::to_string).collect();
        return format!("ios:{}", ports.join(","));
    }
    q.kind.clone()
}

/// group puts the waiting requests that ask for the same thing together, in
/// the order the first of each was asked (groupRequests).
pub fn group(questions: &[Question]) -> Vec<(String, Vec<Question>)> {
    let mut sorted: Vec<&Question> = questions.iter().collect();
    sorted.sort_by(|a, b| a.created_at.cmp(&b.created_at));
    let mut out: Vec<(String, Vec<Question>)> = Vec::new();
    for q in sorted {
        let key = target(q);
        match out.iter_mut().find(|(k, _)| *k == key) {
            Some((_, g)) => g.push(q.clone()),
            None => out.push((key, vec![q.clone()])),
        }
    }
    out
}

/// list_of joins names the language's way: "a, b and c" (formatList).
fn list_of(names: &[String]) -> String {
    let and = if agentbox_i18n::language().starts_with("pt") {
        " e "
    } else if names.len() > 2 {
        ", and "
    } else {
        " and "
    };
    match names {
        [] => String::new(),
        [one] => one.clone(),
        [rest @ .., last] => format!("{}{and}{last}", rest.join(", ")),
    }
}

/// summary is who asks for what, in one line (requestSummary).
fn summary(group: &[Question]) -> String {
    let q = &group[0];
    let agents = list_of(&group.iter().map(|q| q.agent.clone()).collect::<Vec<_>>());
    let count = group.len();
    if q.kind == QUESTION_CONNECTOR {
        let name = connector_ask(q)
            .map(|c| c.preset.map_or(c.name.clone(), |p| p.label.to_string()))
            .unwrap_or_else(|| "?".into());
        return tv(
            "chat.request.connector",
            &[
                ("agents", agents.into()),
                ("count", count.into()),
                ("name", name.into()),
            ],
        );
    }
    if q.kind == CREDENTIAL_SECRET {
        return tv(
            "chat.request.secret",
            &[
                ("agents", agents.into()),
                ("count", count.into()),
                ("name", format!("${}", q.secret_name).into()),
            ],
        );
    }
    if q.kind == CREDENTIAL_IOS_PORTS {
        return tv(
            "chat.request.iosPorts",
            &[
                ("agents", agents.into()),
                ("count", count.into()),
                ("ports", ports_of(q).into()),
            ],
        );
    }
    tv(
        "chat.request.github",
        &[("agents", agents.into()), ("count", count.into())],
    )
}

fn ports_of(q: &Question) -> String {
    q.ports
        .iter()
        .map(i64::to_string)
        .collect::<Vec<_>>()
        .join(", ")
}

fn enc(s: &str) -> String {
    s.bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                (b as char).to_string()
            }
            _ => format!("%{b:02X}"),
        })
        .collect()
}

/// The fields of one group's card, made when it is first drawn.
struct Fields {
    value: Entity<InputState>,
    reason: Entity<InputState>,
    name: Entity<InputState>,
    token: Entity<InputState>,
}

pub struct Requests {
    client: Client,
    /// lead: a project's chat, a row a group; otherwise an agent's, its card.
    lead: bool,
    /// What the app handed in: the requests waiting, as last read.
    given: Vec<Question>,
    /// Answered from here, before the list read again says so.
    settled: HashSet<String>,
    /// The rows opened to their card (by group key).
    open: HashSet<String>,
    refusing: HashSet<String>,
    adding: HashSet<String>,
    busy: HashSet<String>,
    errors: HashMap<String, String>,
    /// A connector's browser sign-in under way, by group key: its page.
    signing_in: HashMap<String, String>,
    fields: HashMap<String, Fields>,
    accounts: Option<Vec<GitHubAccount>>,
    asked_accounts: bool,
    /// Each project's connectors, read when a connector request shows.
    connectors: HashMap<String, Vec<Connector>>,
    asked_connectors: HashSet<String>,
    poll: Option<Task<()>>,
}

impl Requests {
    pub fn new(client: Client, lead: bool) -> Self {
        Self {
            client,
            lead,
            given: Vec::new(),
            settled: HashSet::new(),
            open: HashSet::new(),
            refusing: HashSet::new(),
            adding: HashSet::new(),
            busy: HashSet::new(),
            errors: HashMap::new(),
            signing_in: HashMap::new(),
            fields: HashMap::new(),
            accounts: None,
            asked_accounts: false,
            connectors: HashMap::new(),
            asked_connectors: HashSet::new(),
            poll: None,
        }
    }

    pub fn given(&self) -> &[Question] {
        &self.given
    }

    /// set takes the requests waiting now. One gone from the list is
    /// settled: it leaves the settled set too, which only bridges the moment
    /// between an answer and the next read.
    pub fn set(&mut self, list: Vec<Question>, cx: &mut Context<Self>) {
        self.settled.retain(|id| list.iter().any(|q| &q.id == id));
        self.given = list;
        cx.notify();
    }

    /// shown is what is drawn: the requests still waiting.
    pub fn shown(&self) -> Vec<Question> {
        self.given
            .iter()
            .filter(|q| !self.settled.contains(&q.id))
            .cloned()
            .collect()
    }

    pub fn is_empty(&self) -> bool {
        self.shown().is_empty()
    }

    fn fields(&mut self, key: &str, q: &Question, window: &mut Window, cx: &mut Context<Self>) {
        if self.fields.contains_key(key) {
            return;
        }
        let token_label = connector_ask(q)
            .and_then(|c| c.preset)
            .and_then(|p| p.secret)
            .map(|_| t("project.connectors.figma.tokenLabel"))
            .unwrap_or_else(|| t("chat.credential.tokenPlaceholder"));
        let f = Fields {
            value: cx.new(|cx| {
                InputState::new(window, cx)
                    .masked(true)
                    .placeholder(t("chat.credential.value"))
            }),
            reason: cx.new(|cx| {
                InputState::new(window, cx).placeholder(t("chat.connector.whyPlaceholder"))
            }),
            name: cx.new(|cx| {
                InputState::new(window, cx).placeholder(t("chat.credential.namePlaceholder"))
            }),
            token: cx.new(|cx| {
                InputState::new(window, cx)
                    .masked(true)
                    .placeholder(token_label)
            }),
        };
        self.fields.insert(key.to_string(), f);
    }

    fn load_accounts(&mut self, cx: &mut Context<Self>) {
        if self.asked_accounts {
            return;
        }
        self.asked_accounts = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let auth = client.get::<AuthStatus>("/v1/auth").await;
            this.update(cx, |this, cx| {
                this.accounts = Some(auth.map(|a| a.github_accounts).unwrap_or_default());
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn load_connectors(&mut self, project: &str, force: bool, cx: &mut Context<Self>) {
        if !force && !self.asked_connectors.insert(project.to_string()) {
            return;
        }
        let client = self.client.clone();
        let project = project.to_string();
        cx.spawn(async move |this, cx| {
            let list = client
                .get::<Vec<Connector>>(&format!("/v1/projects/{}/connectors", enc(&project)))
                .await;
            this.update(cx, |this, cx| {
                if let Ok(list) = list {
                    this.connectors.insert(project, list);
                    this.signed_in(cx);
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// signed_in answers the requests whose browser sign-in finished: the
    /// sign-in is what answers the agent.
    fn signed_in(&mut self, cx: &mut Context<Self>) {
        let shown = self.shown();
        let done: Vec<(String, Vec<Question>, String)> = group(&shown)
            .into_iter()
            .filter(|(key, _)| self.signing_in.contains_key(key) && !self.busy.contains(key))
            .filter_map(|(key, g)| {
                let ask = connector_ask(&g[0])?;
                let connected = self
                    .existing(&g[0].project, &ask.name)
                    .is_some_and(|c| c.status == CONNECTOR_CONNECTED);
                connected.then_some((key, g, ask.name))
            })
            .collect();
        for (key, g, name) in done {
            self.signing_in.remove(&key);
            self.answer(
                key,
                g,
                AnswerCredentialRequest {
                    connector: name,
                    ..Default::default()
                },
                cx,
            );
        }
        if self.signing_in.is_empty() {
            self.poll = None;
        }
    }

    /// watch reads the connectors again every two seconds while a sign-in
    /// is under way, as Settings → Connectors does.
    fn watch(&mut self, project: String, cx: &mut Context<Self>) {
        self.poll = Some(cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor().timer(Duration::from_secs(2)).await;
                let go = this
                    .update(cx, |this, cx| {
                        if this.signing_in.is_empty() {
                            return false;
                        }
                        this.load_connectors(&project, true, cx);
                        true
                    })
                    .unwrap_or(false);
                if !go {
                    return;
                }
            }
        }));
    }

    fn existing(&self, project: &str, name: &str) -> Option<&Connector> {
        self.connectors
            .get(project)
            .and_then(|l| l.iter().find(|c| c.name == name))
    }

    /// answer gives every request of a group the same answer, one after
    /// another (useAnswerRequests); the ones that went through are settled
    /// here at once, before the event's read.
    fn answer(
        &mut self,
        key: String,
        group: Vec<Question>,
        req: AnswerCredentialRequest,
        cx: &mut Context<Self>,
    ) {
        if !self.busy.insert(key.clone()) {
            return;
        }
        self.errors.remove(&key);
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let mut done = Vec::new();
            let mut err = None;
            for q in &group {
                let path = format!(
                    "/v1/projects/{}/questions/{}/credential",
                    enc(&q.project),
                    enc(&q.id)
                );
                match client.post::<_, Question>(&path, &req).await {
                    Ok(_) => done.push(q.id.clone()),
                    Err(e) => {
                        err = Some(e.to_string());
                        break;
                    }
                }
            }
            this.update(cx, |this, cx| {
                this.busy.remove(&key);
                this.settled.extend(done);
                match err {
                    Some(e) => {
                        this.errors.insert(key.clone(), e);
                    }
                    None => {
                        this.open.remove(&key);
                        this.refusing.remove(&key);
                        this.adding.remove(&key);
                        this.fields.remove(&key);
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// add_connector adds the connector the agent asked for to the project
    /// (with its token, for a server that takes one) and signs in with the
    /// browser when it signs in that way; one that needs no sign-in is given
    /// at once (ConnectorAnswer's add).
    fn add_connector(&mut self, key: String, group: Vec<Question>, cx: &mut Context<Self>) {
        let Some(ask) = connector_ask(&group[0]) else {
            return;
        };
        let project = group[0].project.clone();
        let existing = self.existing(&project, &ask.name).cloned();
        let secret = ask.preset.and_then(|p| p.secret);
        let token = self
            .fields
            .get(&key)
            .map(|f| f.token.read(cx).value().trim().to_string())
            .unwrap_or_default();
        if existing.is_none() && secret.is_some() && token.is_empty() {
            return;
        }
        if !self.busy.insert(key.clone()) {
            return;
        }
        self.errors.remove(&key);
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res: anyhow::Result<Option<String>> = async {
                let base = format!("/v1/projects/{}", enc(&project));
                if existing.is_none() {
                    let mut req = SetConnectorRequest {
                        url: ask.url.clone(),
                        ..Default::default()
                    };
                    if let Some((name, header, _)) = secret {
                        let body = serde_json::to_vec(&serde_json::json!({ "value": token }))?;
                        client
                            .raw(
                                Method::PUT,
                                &format!("{base}/secrets/{}", enc(name)),
                                Some(body),
                            )
                            .await?;
                        req.auth = CONNECTOR_SECRET.into();
                        req.secret = name.into();
                        req.header = header.into();
                    }
                    client
                        .raw(
                            Method::PUT,
                            &format!("{base}/connectors/{}", enc(&ask.name)),
                            Some(serde_json::to_vec(&req)?),
                        )
                        .await?;
                }
                let plain = existing
                    .as_ref()
                    .is_some_and(|c| c.auth == CONNECTOR_SECRET || c.auth == CONNECTOR_NONE);
                if secret.is_some() || plain {
                    return Ok(None);
                }
                let r: ConnectResult = client
                    .post(
                        &format!("{base}/connectors/{}/connect", enc(&ask.name)),
                        &serde_json::json!({}),
                    )
                    .await?;
                Ok(Some(r.authorization_url))
            }
            .await;
            this.update(cx, |this, cx| {
                this.busy.remove(&key);
                match res {
                    Err(e) => {
                        this.errors.insert(key.clone(), e.to_string());
                    }
                    Ok(Some(url)) => {
                        cx.open_url(&url);
                        this.signing_in.insert(key.clone(), url);
                        this.load_connectors(&project, true, cx);
                        this.watch(project.clone(), cx);
                    }
                    Ok(None) => {
                        this.load_connectors(&project, true, cx);
                        this.answer(
                            key.clone(),
                            group.clone(),
                            AnswerCredentialRequest {
                                connector: ask.name.clone(),
                                ..Default::default()
                            },
                            cx,
                        );
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// log_in stores a GitHub token under a name, as `agentbox auth github`
    /// does, then gives that account (GitHubForm's login).
    fn log_in(&mut self, key: String, group: Vec<Question>, cx: &mut Context<Self>) {
        let Some(f) = self.fields.get(&key) else {
            return;
        };
        let name = f.name.read(cx).value().trim().to_string();
        let token = f.token.read(cx).value().trim().to_string();
        if name.is_empty() || token.is_empty() || !self.busy.insert(key.clone()) {
            return;
        }
        self.errors.remove(&key);
        cx.notify();
        let client = self.client.clone();
        let req = GitHubTokenRequest {
            token,
            account: name.clone(),
        };
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, serde_json::Value>("/v1/auth/github", &req)
                .await;
            this.update(cx, |this, cx| {
                this.busy.remove(&key);
                match res {
                    Err(e) => {
                        this.errors.insert(key.clone(), e.to_string());
                    }
                    Ok(_) => {
                        this.asked_accounts = false;
                        this.load_accounts(cx);
                        this.answer(
                            key.clone(),
                            group.clone(),
                            AnswerCredentialRequest {
                                github_account: name.clone(),
                                ..Default::default()
                            },
                            cx,
                        );
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn refuse(&mut self, key: String, group: Vec<Question>, cx: &mut Context<Self>) {
        let reason = self
            .fields
            .get(&key)
            .map(|f| f.reason.read(cx).value().trim().to_string())
            .unwrap_or_default();
        self.answer(
            key,
            group,
            AnswerCredentialRequest {
                refuse: true,
                reason,
                ..Default::default()
            },
            cx,
        );
    }

    fn give_secret(&mut self, key: String, group: Vec<Question>, cx: &mut Context<Self>) {
        let value = self
            .fields
            .get(&key)
            .map(|f| f.value.read(cx).value().to_string())
            .unwrap_or_default();
        if value.is_empty() {
            return;
        }
        self.answer(
            key,
            group,
            AnswerCredentialRequest {
                value,
                ..Default::default()
            },
            cx,
        );
    }

    /// waiting_label is the one amber thing on a request (WaitingLabel).
    fn waiting_label(extra: Option<String>) -> Div {
        h_flex()
            .min_w_0()
            .gap_1p5()
            .text_size(theme::text::CAPTION)
            .text_color(theme::subtle())
            .child(
                div()
                    .flex_none()
                    .size(px(6.))
                    .rounded_full()
                    .bg(theme::amber(400)),
            )
            .child(
                div()
                    .flex_none()
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::amber(300))
                    .child(t("chat.credential.waitingOnYou")),
            )
            .when_some(extra, |el, s| el.child(div().min_w_0().truncate().child(s)))
    }

    fn icon_of(q: &Question) -> &'static str {
        if q.kind == QUESTION_CONNECTOR {
            "plug"
        } else if q.kind == CREDENTIAL_IOS_PORTS {
            "smartphone"
        } else {
            "key-round"
        }
    }

    fn hint(text: String) -> Div {
        div()
            .text_size(theme::text::CAPTION)
            .line_height(px(18.))
            .text_color(theme::faint())
            .child(text)
    }

    /// card is a group's card (CredentialCard / ConnectorRequestCard): what
    /// is needed, why each agent asked, and the answer.
    fn card(&self, key: &str, group: &[Question], bare: bool, cx: &mut Context<Self>) -> Div {
        let q = &group[0];
        let ask = connector_ask(q);
        let headline = if q.kind == QUESTION_CONNECTOR {
            tv(
                "chat.connector.needs",
                &[(
                    "name",
                    ask.as_ref()
                        .map(|c| c.preset.map_or(c.name.clone(), |p| p.label.to_string()))
                        .unwrap_or_else(|| "?".into())
                        .into(),
                )],
            )
        } else if q.kind == CREDENTIAL_SECRET {
            tv(
                "chat.credential.needsSecret",
                &[("name", format!("${}", q.secret_name).into())],
            )
        } else if q.kind == CREDENTIAL_IOS_PORTS {
            tv(
                "chat.credential.needsIOSPorts",
                &[("ports", ports_of(q).into())],
            )
        } else {
            t("chat.credential.needsGitHub")
        };
        let reasons = if group.len() == 1 {
            v_flex().child(Self::reason(None, &q.question))
        } else {
            v_flex()
                .gap_1p5()
                .child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::subtle())
                        .child(tv(
                            "chat.request.oneAnswer",
                            &[("count", group.len().into())],
                        )),
                )
                .children(
                    group
                        .iter()
                        .map(|q| Self::reason(Some(&q.agent), &q.question)),
                )
        };
        v_flex()
            .mt_1p5()
            .min_w_0()
            .gap_2()
            .when(!bare, |el| {
                el.child(
                    h_flex()
                        .gap_1p5()
                        .text_size(theme::text::UI)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::secondary())
                        .child(
                            crate::icons::ic(Self::icon_of(q))
                                .size(px(14.))
                                .text_color(theme::subtle()),
                        )
                        .child(div().min_w_0().child(headline)),
                )
            })
            .when_some(ask.as_ref().filter(|a| !a.host.is_empty()), |el, a| {
                el.child(
                    h_flex()
                        .flex_wrap()
                        .gap_1()
                        .text_size(theme::text::UI)
                        .text_color(theme::secondary())
                        .child(tv("chat.connector.at", &[("host", a.host.clone().into())]))
                        .when(a.preset.is_none(), |el| {
                            el.child(
                                div()
                                    .text_color(theme::faint())
                                    .child(format!("· {}", t("chat.connector.unknownServer"))),
                            )
                        }),
                )
            })
            .child(reasons)
            .child(self.answer_form(key, group, cx))
    }

    fn reason(agent: Option<&str>, text: &str) -> Div {
        h_flex()
            .flex_wrap()
            .gap_1()
            .text_size(theme::text::UI)
            .line_height(px(19.5))
            .text_color(theme::tertiary())
            .when_some(agent, |el, a| {
                el.child(
                    div()
                        .font_family(theme::mono())
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::secondary())
                        .child(a.to_string()),
                )
            })
            .child(div().min_w_0().child(text.to_string()))
    }

    /// answer_form is CredentialAnswer / ConnectorAnswer: the form the kind
    /// takes, then Refuse, then what went wrong.
    fn answer_form(&self, key: &str, group: &[Question], cx: &mut Context<Self>) -> Div {
        let q = &group[0];
        let busy = self.busy.contains(key);
        let view = cx.entity().downgrade();
        let k = key.to_string();
        let g = group.to_vec();
        // act calls f on this view with the group's key and requests.
        let act = |f: fn(&mut Self, String, Vec<Question>, &mut Context<Self>)| {
            let (view, k, g) = (view.clone(), k.clone(), g.clone());
            move |_: &ClickEvent, _: &mut Window, cx: &mut App| {
                view.update(cx, |this, cx| f(this, k.clone(), g.clone(), cx))
                    .ok();
            }
        };
        let toggle = |set: fn(&mut Self) -> &mut HashSet<String>, on: bool| {
            let (view, k) = (view.clone(), k.clone());
            move |_: &ClickEvent, _: &mut Window, cx: &mut App| {
                view.update(cx, |this, cx| {
                    if on {
                        set(this).insert(k.clone());
                    } else {
                        set(this).remove(&k);
                    }
                    cx.notify();
                })
                .ok();
            }
        };
        let f = self.fields.get(key);
        let id = |s: &str| SharedString::from(format!("request-{s}-{key}"));
        let form: AnyElement = if q.kind == CREDENTIAL_SECRET {
            v_flex()
                .gap_1p5()
                .child(
                    h_flex()
                        .gap_1p5()
                        .child(
                            div()
                                .flex_none()
                                .w(px(150.))
                                .truncate()
                                .px_2()
                                .py_1()
                                .rounded_md()
                                .border_1()
                                .border_color(theme::line())
                                .font_family(theme::mono())
                                .text_size(theme::text::CAPTION)
                                .text_color(theme::subtle())
                                .child(q.secret_name.clone()),
                        )
                        .when_some(f, |el, f| {
                            el.child(div().flex_1().min_w_0().child(Input::new(&f.value)))
                        }),
                )
                .child(Self::hint(t("chat.credential.secretHint")))
                .child(h_flex().justify_end().child(button_busy(
                    id("give-secret"),
                    t("chat.connector.saveAndGive"),
                    Btn::Primary,
                    busy,
                    false,
                    act(Self::give_secret),
                )))
                .into_any_element()
        } else if q.kind == CREDENTIAL_IOS_PORTS {
            let allow = {
                let (view, k, g) = (view.clone(), k.clone(), g.clone());
                move |_: &ClickEvent, _: &mut Window, cx: &mut App| {
                    view.update(cx, |this, cx| {
                        this.answer(
                            k.clone(),
                            g.clone(),
                            AnswerCredentialRequest {
                                allow: true,
                                ..Default::default()
                            },
                            cx,
                        )
                    })
                    .ok();
                }
            };
            v_flex()
                .gap_1p5()
                .child(Self::hint(t("chat.credential.iosPortsHint")))
                .child(h_flex().justify_end().child(button_busy(
                    id("allow"),
                    t("chat.credential.allow"),
                    Btn::Primary,
                    busy,
                    false,
                    allow,
                )))
                .into_any_element()
        } else if q.kind == QUESTION_CONNECTOR {
            self.connector_form(key, group, busy, cx)
        } else {
            // GitHub: one of this machine's accounts, or a new one logged in.
            let accounts = self.accounts.clone().unwrap_or_default();
            let adding = self.adding.contains(key);
            v_flex()
                .gap_1p5()
                .children(accounts.into_iter().map(|acc| {
                    let (view, k, g) = (view.clone(), k.clone(), g.clone());
                    let name = acc.name.clone();
                    h_flex()
                        .id(SharedString::from(format!(
                            "request-account-{key}-{}",
                            acc.name
                        )))
                        .min_w_0()
                        .gap_2()
                        .px_2()
                        .py_1()
                        .rounded_md()
                        .border_1()
                        .border_color(theme::line())
                        .bg(theme::surface_faint())
                        .when(!busy, |el| {
                            el.cursor_pointer()
                                .hover(|el| el.bg(theme::surface_raised()))
                                .on_click(move |_, _, cx| {
                                    view.update(cx, |this, cx| {
                                        this.answer(
                                            k.clone(),
                                            g.clone(),
                                            AnswerCredentialRequest {
                                                github_account: name.clone(),
                                                ..Default::default()
                                            },
                                            cx,
                                        )
                                    })
                                    .ok();
                                })
                        })
                        .when(busy, |el| el.opacity(0.5))
                        .child(
                            div()
                                .truncate()
                                .font_family(theme::mono())
                                .text_size(theme::text::CAPTION)
                                .text_color(theme::secondary())
                                .child(acc.name.clone()),
                        )
                        .when(!acc.login.is_empty(), |el| {
                            el.child(
                                div()
                                    .truncate()
                                    .text_size(theme::text::CAPTION)
                                    .text_color(theme::subtle())
                                    .child(acc.login.clone()),
                            )
                        })
                        .child(
                            div()
                                .ml_auto()
                                .flex_none()
                                .text_size(theme::text::CAPTION)
                                .text_color(theme::faint())
                                .child(t("chat.credential.give")),
                        )
                }))
                .when(adding, |el| {
                    el.when_some(f, |el, f| {
                        el.child(
                            h_flex()
                                .gap_1p5()
                                .child(div().w(px(150.)).flex_none().child(Input::new(&f.name)))
                                .child(div().flex_1().min_w_0().child(Input::new(&f.token))),
                        )
                    })
                    .child(
                        h_flex()
                            .justify_end()
                            .gap_1p5()
                            .child(button(
                                id("add-cancel"),
                                t("common.cancel"),
                                Btn::Ghost,
                                false,
                                toggle(|s| &mut s.adding, false),
                            ))
                            .child(button_busy(
                                id("log-in"),
                                t("chat.credential.logInAndGive"),
                                Btn::Primary,
                                busy,
                                false,
                                act(Self::log_in),
                            )),
                    )
                })
                .when(!adding, |el| {
                    el.child(h_flex().child(button(
                        id("add"),
                        t("chat.credential.logInAnother"),
                        Btn::Ghost,
                        false,
                        toggle(|s| &mut s.adding, true),
                    )))
                })
                .child(Self::hint(t("chat.credential.githubHint")))
                .into_any_element()
        };
        let refusing = self.refusing.contains(key);
        v_flex()
            .min_w_0()
            .gap_1p5()
            .child(form)
            .when(refusing, |el| {
                el.child(
                    h_flex()
                        .gap_1p5()
                        .when_some(f, |el, f| {
                            el.child(div().flex_1().min_w_0().child(Input::new(&f.reason)))
                        })
                        .child(button_busy(
                            id("refuse-send"),
                            t("chat.connector.refuse"),
                            Btn::Danger,
                            busy,
                            false,
                            act(Self::refuse),
                        ))
                        .child(button(
                            id("refuse-cancel"),
                            t("common.cancel"),
                            Btn::Ghost,
                            false,
                            toggle(|s| &mut s.refusing, false),
                        )),
                )
            })
            .when(!refusing, |el| {
                el.child(h_flex().child(button(
                    id("refuse"),
                    t("chat.connector.refuse"),
                    Btn::Ghost,
                    false,
                    toggle(|s| &mut s.refusing, true),
                )))
            })
            .when_some(self.errors.get(key), |el, e| {
                el.child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::rose(300))
                        .child(e.clone()),
                )
            })
    }

    fn connector_form(
        &self,
        key: &str,
        group: &[Question],
        busy: bool,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let q = &group[0];
        let Some(ask) = connector_ask(q) else {
            return Self::hint(t("chat.connector.noUrl")).into_any_element();
        };
        let existing = self.existing(&q.project, &ask.name);
        let view = cx.entity().downgrade();
        let (k, g) = (key.to_string(), group.to_vec());
        let id = |s: &str| SharedString::from(format!("request-{s}-{key}"));
        let connected = existing.is_some_and(|c| c.status == CONNECTOR_CONNECTED);
        let waiting = self.signing_in.get(key).filter(|_| {
            existing.is_none_or(|c| c.status == CONNECTOR_CONNECTING || c.status.is_empty())
        });
        let needs_token = existing.is_none() && ask.preset.is_some_and(|p| p.secret.is_some());
        let el = if connected {
            let name = ask.name.clone();
            h_flex()
                .justify_end()
                .child(button_busy(
                    id("give-connector"),
                    tv("chat.connector.give", &[("name", ask.name.clone().into())]),
                    Btn::Primary,
                    busy,
                    false,
                    move |_, _, cx| {
                        view.update(cx, |this, cx| {
                            this.answer(
                                k.clone(),
                                g.clone(),
                                AnswerCredentialRequest {
                                    connector: name.clone(),
                                    ..Default::default()
                                },
                                cx,
                            )
                        })
                        .ok();
                    },
                ))
                .into_any_element()
        } else if let Some(url) = waiting {
            let url = url.clone();
            h_flex()
                .gap_1p5()
                .text_size(theme::text::CAPTION)
                .text_color(theme::subtle())
                .child(crate::icons::spinner())
                .child(t("chat.connector.waitingSignIn"))
                .child(div().ml_auto().child(button(
                    id("open-again"),
                    t("chat.connector.openAgain"),
                    Btn::Ghost,
                    false,
                    move |_, _, cx| cx.open_url(&url),
                )))
                .into_any_element()
        } else if ask.url.is_empty() && existing.is_none() {
            Self::hint(t("chat.connector.noUrl")).into_any_element()
        } else {
            let label = if needs_token {
                t("chat.connector.saveAndGive")
            } else if existing.is_some() {
                t("chat.connector.connect")
            } else {
                t("chat.connector.addAndConnect")
            };
            v_flex()
                .gap_1p5()
                .when(needs_token, |el| {
                    el.when_some(self.fields.get(key), |el, f| el.child(Input::new(&f.token)))
                        .child(Self::hint(t("project.connectors.figma.why")))
                })
                .when(!needs_token, |el| {
                    el.child(Self::hint(t("chat.connector.addedHint")))
                })
                .child(h_flex().justify_end().child(button_busy(
                    id("add-connector"),
                    label,
                    Btn::Primary,
                    busy,
                    false,
                    move |_, _, cx| {
                        view.update(cx, |this, cx| this.add_connector(k.clone(), g.clone(), cx))
                            .ok();
                    },
                )))
                .into_any_element()
        };
        v_flex()
            .gap_1p5()
            .child(el)
            .when_some(
                existing.filter(|c| c.status == CONNECTOR_ERROR && !c.error.is_empty()),
                |el, c| {
                    el.child(
                        div()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::rose(300))
                            .child(c.error.clone()),
                    )
                },
            )
            .into_any_element()
    }

    /// row is one line of a project's requests (RequestRow): a dot, who asks
    /// for what, and Answer, which opens the card under it.
    fn row(&self, key: &str, group: &[Question], cx: &mut Context<Self>) -> Div {
        let q = &group[0];
        let open = self.open.contains(key);
        let view = cx.entity().downgrade();
        let k = key.to_string();
        v_flex()
            .min_w_0()
            .rounded_xl()
            .border_1()
            .border_color(theme::amber(300).opacity(0.3))
            .bg(theme::card())
            .child(
                h_flex()
                    .min_w_0()
                    .gap_2()
                    .py_1p5()
                    .pr_1p5()
                    .pl_3()
                    .child(
                        div()
                            .flex_none()
                            .size(px(6.))
                            .rounded_full()
                            .bg(theme::amber(400)),
                    )
                    .child(
                        crate::icons::ic(Self::icon_of(q))
                            .size(px(14.))
                            .text_color(theme::subtle()),
                    )
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .truncate()
                            .text_size(theme::text::UI)
                            .text_color(theme::tertiary())
                            .child(summary(group)),
                    )
                    .child(button(
                        SharedString::from(format!("request-toggle-{key}")),
                        if open {
                            t("chat.request.hide")
                        } else {
                            t("chat.request.answer")
                        },
                        if open { Btn::Ghost } else { Btn::Primary },
                        false,
                        move |_, _, cx| {
                            view.update(cx, |this, cx| {
                                if !this.open.remove(&k) {
                                    this.open.insert(k.clone());
                                }
                                cx.notify();
                            })
                            .ok();
                        },
                    )),
            )
            .when(open, |el| {
                el.child(
                    div()
                        .border_t_1()
                        .border_color(theme::line_faint())
                        .px_3()
                        .pb_3()
                        .child(self.card(key, group, true, cx)),
                )
            })
    }
}

impl Render for Requests {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let shown = self.shown();
        if shown.is_empty() {
            return div().into_any_element();
        }
        let groups = group(&shown);
        for (key, g) in &groups {
            self.fields(key, &g[0], window, cx);
            if g[0].kind == QUESTION_CONNECTOR {
                let p = g[0].project.clone();
                self.load_connectors(&p, false, cx);
            } else if g[0].kind != CREDENTIAL_SECRET && g[0].kind != CREDENTIAL_IOS_PORTS {
                self.load_accounts(cx);
            }
        }
        if self.lead {
            v_flex()
                .id("project-requests")
                .mx(px(20.))
                .mb_2()
                .gap_2()
                .max_h(px(360.))
                .overflow_y_scroll()
                .children(groups.iter().map(|(key, g)| self.row(key, g, cx)))
                .into_any_element()
        } else {
            let (key, g) = &groups[0];
            v_flex()
                .id("agent-request")
                .mx(px(20.))
                .mb_2()
                .px(px(14.))
                .py(px(10.))
                .rounded_xl()
                .border_1()
                .border_color(theme::amber(300).opacity(0.3))
                .bg(theme::card())
                .shadow_md()
                .max_h(px(360.))
                .overflow_y_scroll()
                .child(Self::waiting_label(None))
                .child(self.card(key, g, false, cx))
                .into_any_element()
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn q(id: &str, agent: &str, kind: &str, status: &str, at: &str) -> Question {
        Question {
            id: id.into(),
            project: "demo".into(),
            agent: agent.into(),
            r#ref: format!("demo/{agent}"),
            kind: kind.into(),
            status: status.into(),
            created_at: at.into(),
            ..Default::default()
        }
    }

    #[::core::prelude::v1::test]
    fn a_project_lists_every_open_request_and_an_agent_its_newest() {
        let mut secret = q("q2", "agent-1", "secret", "pending", "2026-11-01T10:02:00Z");
        secret.secret_name = "OPENAI_API_KEY".into();
        let list = vec![
            q("q1", "agent-1", "github", "pending", "2026-11-01T10:01:00Z"),
            secret,
            q(
                "q3",
                "agent-2",
                "github",
                "escalated",
                "2026-11-01T10:00:00Z",
            ),
            // A decision is the lead's to pass on in words, not a card.
            q("q4", "agent-2", "", "escalated", "2026-11-01T10:03:00Z"),
            q(
                "q5",
                "agent-3",
                "github",
                "answered",
                "2026-11-01T10:04:00Z",
            ),
        ];
        let lead: Vec<_> = for_lead(&list).into_iter().map(|q| q.id).collect();
        assert_eq!(lead, ["q3", "q1", "q2"]);
        let mine: Vec<_> = for_agent(&list, "agent-1")
            .into_iter()
            .map(|q| q.id)
            .collect();
        assert_eq!(mine, ["q2"]);
        assert!(for_agent(&list, "agent-3").is_empty());
        // Two agents asking for a GitHub account are one row, one answer.
        let groups = group(&for_lead(&list));
        assert_eq!(groups.len(), 2);
        assert_eq!(groups[0].0, "github");
        assert_eq!(groups[0].1.len(), 2);
        assert_eq!(groups[1].0, "secret:OPENAI_API_KEY");
        assert!(summary(&groups[0].1).contains("agent-2 and agent-1"));
    }

    #[::core::prelude::v1::test]
    fn a_connector_request_names_its_server() {
        let mut c = q("c", "agent-1", QUESTION_CONNECTOR, "pending", "");
        c.connector = "notion".into();
        let a = connector_ask(&c).unwrap();
        assert_eq!(a.url, "https://mcp.notion.com/mcp");
        assert_eq!(a.host, "mcp.notion.com");
        assert!(a.preset.is_some());
        // Named like a preset but somewhere else: not that preset.
        c.url = "https://evil.example.com/mcp".into();
        let a = connector_ask(&c).unwrap();
        assert!(a.preset.is_none());
        assert_eq!(a.name, "notion");
        assert_eq!(a.host, "evil.example.com");
        c.connector = String::new();
        c.url = "https://mcp.acme.co.uk/mcp".into();
        assert_eq!(connector_ask(&c).unwrap().name, "acme");
        c.url = "http://localhost:3000/mcp".into();
        assert!(connector_ask(&c).is_none());
    }
}
