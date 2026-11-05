//! What the Office shows, worked out from what the app already reads (#668's
//! model.ts): every agent with its state as the sidebar sees it, its pull
//! request from its project's fleet, the questions waiting on the user, each
//! lead's chat and the agents ready to review. Nothing here knows about the
//! renderer: the scene draws an `OfficeModel`, and the app builds one, so this
//! is the part with tests.

use std::collections::HashMap;
use std::sync::LazyLock;

use agentbox_api as t;
use fancy_regex::Regex;

/// DeskTone is an agent's state at a glance, the colour of its beacon: it needs
/// you (a question, a permission, a credential), stopped on an error, finished
/// (ready for you to review), working, idle with its machine up, or stopped.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DeskTone {
    Needs,
    Error,
    Finished,
    Working,
    Idle,
    Stopped,
}

/// The order a list of desks reads in: what needs you first.
pub const TONE_ORDER: [DeskTone; 6] = [DeskTone::Needs, DeskTone::Error, DeskTone::Finished, DeskTone::Working, DeskTone::Idle, DeskTone::Stopped];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PrState {
    Open,
    Draft,
    Merged,
    Closed,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PrChecks {
    Passing,
    Failing,
    Pending,
    None,
}

#[derive(Clone, Debug, PartialEq)]
pub struct PrLook {
    pub number: i64,
    pub state: PrState,
    pub checks: PrChecks,
    pub url: String,
    pub title: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Changes {
    pub files: i64,
    pub insertions: i64,
    pub deletions: i64,
}

#[derive(Clone, Debug, PartialEq)]
pub struct DeskModel {
    /// The agent's ref, project/name: a desk is always the same agent's.
    pub id: String,
    pub project: String,
    pub name: String,
    pub title: String,
    pub branch: String,
    pub ai: String,
    pub tone: DeskTone,
    pub pr: Option<PrLook>,
    pub changes: Option<Changes>,
    /// What it asks you, in its words, when it does.
    pub question: Option<String>,
    /// Since when it's been in this state, roughly: its last activity.
    pub last_active: Option<String>,
}

/// A project's lead is its front desk: it has no machine, only its chat.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum LeadTone {
    Working,
    Needs,
    Idle,
}

#[derive(Clone, Debug, PartialEq)]
pub struct LeadModel {
    pub tone: LeadTone,
    pub line: Option<String>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct AreaModel {
    pub project: String,
    pub label: String,
    /// The project's colour, a hue in degrees, the same every time.
    pub hue: f64,
    pub lead: LeadModel,
    pub desks: Vec<DeskModel>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct BoardItem {
    /// The desk it's about (an agent's ref), or a project's lead (project/lead).
    pub r#ref: String,
    pub project: String,
    pub title: String,
    pub text: Option<String>,
    pub tone: DeskTone,
    pub pr: Option<PrLook>,
}

#[derive(Clone, Debug, PartialEq, Default)]
pub struct Board {
    pub needs: Vec<BoardItem>,
    pub review: Vec<BoardItem>,
}

#[derive(Clone, Debug, PartialEq, Default)]
pub struct OfficeModel {
    pub areas: Vec<AreaModel>,
    pub board: Board,
}

/// The sidebar's state for an agent (the app's leadFirst.ts `AgentSt`).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum AgentSt {
    Needs,
    Error,
    Review,
    Working,
    Done,
}

/// A sidebar row (leadFirst.ts `Row`, less its pull request number, which the
/// Office doesn't read): an agent with its state. A plain struct because the
/// native app's own row type carries more than the model wants.
#[derive(Clone, Debug)]
pub struct Row {
    pub agent: t::Agent,
    pub st: AgentSt,
}

/// A project as the model reads it (TS: `Pick<T.Project, 'name' | 'displayName'>`).
#[derive(Clone, Debug)]
pub struct ProjectRef {
    pub name: String,
    pub display_name: String,
}

pub struct OfficeInputs<'a> {
    pub projects: &'a [ProjectRef],
    pub rows: &'a [Row],
    /// Each project's fleet, for the agents' pull requests and changes.
    pub fleets: &'a HashMap<String, t::Fleet>,
    /// Each project's questions (pending ones are what an agent asks you), in
    /// the order to read them (TS: a `Map`, which keeps its insertion order; a
    /// `HashMap` would not, and the first question for a ref wins).
    pub questions: &'a [(String, Vec<t::Question>)],
    /// Each project's lead chat: its state ('running', 'waiting', 'ready'...)
    /// and last line.
    pub leads: &'a HashMap<String, t::ProjectChat>,
    /// The agents ready to review, with their pull requests.
    pub review: &'a [t::ReviewItem],
}

/// leadRef is the ref a lead's board item and front desk go by.
pub fn lead_ref(project: &str) -> String {
    format!("{project}/lead")
}

/// hueOf is a project's colour: its name hashed onto the colour wheel, skipping
/// the muddy yellow-greens so every area reads as a colour of its own.
pub fn hue_of(name: &str) -> f64 {
    let mut h: u32 = 0;
    for ch in name.chars() {
        // JS `charCodeAt(0)` of a code point: its first UTF-16 unit.
        let unit = ch.encode_utf16(&mut [0u16; 2])[0] as u32;
        h = h.wrapping_mul(31).wrapping_add(unit);
    }
    const HUES: [f64; 10] = [8., 28., 200., 220., 250., 275., 300., 330., 170., 150.];
    HUES[h as usize % HUES.len()]
}

/// deskTone is a sidebar state (AgentSt) as a desk shows it: done splits into
/// idle, its machine still up, and stopped.
pub fn desk_tone(agent: &t::Agent, st: AgentSt) -> DeskTone {
    match st {
        AgentSt::Needs => DeskTone::Needs,
        AgentSt::Error => DeskTone::Error,
        AgentSt::Review => DeskTone::Finished,
        AgentSt::Working => DeskTone::Working,
        AgentSt::Done => {
            if agent.state == "running" {
                DeskTone::Idle
            } else {
                DeskTone::Stopped
            }
        }
    }
}

pub fn pr_look(pr: Option<&t::PullRequest>) -> Option<PrLook> {
    let pr = pr?;
    if pr.number == 0 {
        return None;
    }
    let raw = pr.state.to_lowercase();
    let state = if pr.draft && raw == "open" {
        PrState::Draft
    } else if raw == "merged" {
        PrState::Merged
    } else if raw == "closed" {
        PrState::Closed
    } else {
        PrState::Open
    };
    let checks = match pr.checks.as_str() {
        "passing" => PrChecks::Passing,
        "failing" => PrChecks::Failing,
        "pending" => PrChecks::Pending,
        _ => PrChecks::None,
    };
    Some(PrLook { number: pr.number, state, checks, url: pr.url.clone(), title: pr.title.clone() })
}

/// leadTone is a lead's chat state as its front desk shows it: waiting on a
/// permission is the user's to give.
pub fn lead_tone(chat: Option<&str>) -> LeadTone {
    match chat {
        Some("waiting") => LeadTone::Needs,
        Some("running") | Some("starting") => LeadTone::Working,
        _ => LeadTone::Idle,
    }
}

/// By age, then ref. (TS answers 1 for two equal refs, which no sort order
/// can honour; two agents never share a ref, so this says Equal.)
fn by_age(a: &Row, b: &Row) -> std::cmp::Ordering {
    use std::cmp::Ordering::*;
    match a.agent.created_at.cmp(&b.agent.created_at) {
        Equal => a.agent.r#ref.cmp(&b.agent.r#ref),
        o => o,
    }
}

fn tone_rank(t: DeskTone) -> usize {
    TONE_ORDER.iter().position(|x| *x == t).unwrap_or(0)
}

/// officeModel puts it together. Areas go in the projects' order, desks in
/// the order their agents were made, so a desk stays where it was as others
/// come and go; the board lists what needs you, then what waits for review.
pub fn office_model(inp: &OfficeInputs) -> OfficeModel {
    let mut review_by_ref: HashMap<&str, &t::ReviewItem> = HashMap::new();
    for r in inp.review {
        review_by_ref.insert(&r.r#ref, r);
    }
    let mut asks: HashMap<&str, &str> = HashMap::new();
    for (_, list) in inp.questions {
        // The ones waiting on you, as the sidebar counts them (agentStatus.ts's
        // isAsking): a credential, or a question the lead passed on.
        for q in list {
            if (q.status == "escalated" || (q.status == "pending" && !q.kind.is_empty())) && !asks.contains_key(q.r#ref.as_str()) {
                asks.insert(&q.r#ref, &q.question);
            }
        }
    }

    let mut rows_by: HashMap<&str, Vec<&Row>> = HashMap::new();
    let mut row_keys: Vec<&str> = Vec::new();
    for r in inp.rows {
        let list = rows_by.entry(r.agent.project.as_str()).or_default();
        if list.is_empty() {
            row_keys.push(&r.agent.project);
        }
        list.push(r);
    }

    let mut areas: Vec<AreaModel> = Vec::new();
    let mut needs: Vec<BoardItem> = Vec::new();
    let mut review: Vec<BoardItem> = Vec::new();
    // A project the projects list doesn't have yet still gets its agents' area.
    let mut projects: Vec<ProjectRef> = inp.projects.to_vec();
    for n in row_keys {
        if !inp.projects.iter().any(|p| p.name == n) {
            projects.push(ProjectRef { name: n.to_string(), display_name: String::new() });
        }
    }
    for p in &projects {
        let label = if p.display_name.is_empty() { p.name.clone() } else { p.display_name.clone() };
        let fleet = inp.fleets.get(&p.name);
        let mut fleet_by: HashMap<&str, &t::FleetAgent> = HashMap::new();
        for a in fleet.map(|f| f.agents.as_slice()).unwrap_or_default() {
            fleet_by.insert(&a.r#ref, a);
        }
        let mut mine: Vec<&Row> = rows_by.get(p.name.as_str()).cloned().unwrap_or_default();
        mine.sort_by(|a, b| by_age(a, b));
        let desks: Vec<DeskModel> = mine
            .iter()
            .map(|r| {
                let a = &r.agent;
                let f = fleet_by.get(a.r#ref.as_str()).copied();
                let tone = desk_tone(a, r.st);
                // (The API leaves an empty note out, as TS has it undefined.)
                let note = (!a.outcome_note.is_empty()).then(|| a.outcome_note.clone());
                let question = match tone {
                    DeskTone::Needs => asks.get(a.r#ref.as_str()).map(|s| s.to_string()).or_else(|| if a.outcome == "asking" { note } else { None }),
                    DeskTone::Error => note,
                    _ => None,
                };
                DeskModel {
                    id: a.r#ref.clone(),
                    project: p.name.clone(),
                    name: a.name.clone(),
                    title: if a.title.is_empty() { a.name.clone() } else { a.title.clone() },
                    branch: a.branch.clone(),
                    ai: a.ai.clone(),
                    tone,
                    // The review queue's pull request first: it's the one a click on the
                    // board opens.
                    pr: pr_look(review_by_ref.get(a.r#ref.as_str()).and_then(|r| r.pr.as_ref()).or_else(|| f.and_then(|f| f.pr.as_ref()))),
                    changes: f.and_then(|f| (f.changes.files > 0 || f.changes.insertions > 0).then_some(Changes { files: f.changes.files, insertions: f.changes.insertions, deletions: f.changes.deletions })),
                    question,
                    last_active: f.and_then(|f| f.last_active.clone()),
                }
            })
            .collect();
        let lead_chat = inp.leads.get(&p.name);
        let lead = LeadModel { tone: lead_tone(lead_chat.map(|c| c.chat.as_str())), line: lead_chat.and_then(|c| c.last.as_ref()).map(|l| l.text.clone()) };
        // A project with no agents and a quiet lead has no area: an empty rug
        // helps no one, and its chat is a click away in the sidebar.
        if desks.is_empty() && lead.tone == LeadTone::Idle {
            continue;
        }
        let lead_needs = lead.tone == LeadTone::Needs;
        areas.push(AreaModel { project: p.name.clone(), label: label.clone(), hue: hue_of(&p.name), lead, desks: desks.clone() });

        if lead_needs {
            needs.push(BoardItem { r#ref: lead_ref(&p.name), project: p.name.clone(), title: label.clone(), text: None, tone: DeskTone::Needs, pr: None });
        }
        for d in &desks {
            if d.tone == DeskTone::Needs || d.tone == DeskTone::Error {
                needs.push(BoardItem { r#ref: d.id.clone(), project: p.name.clone(), title: d.title.clone(), text: d.question.clone(), tone: d.tone, pr: d.pr.clone() });
            // Titled as the review queue has it, which a click on it opens.
            } else if d.tone == DeskTone::Finished || (d.pr.as_ref().is_some_and(|pr| pr.state == PrState::Open) && d.tone != DeskTone::Working) {
                let title = review_by_ref.get(d.id.as_str()).map(|r| r.title.as_str()).filter(|s| !s.is_empty()).unwrap_or(&d.title).to_string();
                review.push(BoardItem { r#ref: d.id.clone(), project: p.name.clone(), title, text: None, tone: d.tone, pr: d.pr.clone() });
            }
        }
    }
    needs.sort_by_key(|i| tone_rank(i.tone));
    // Two agents with the same title are told apart by their names.
    let mut seen: HashMap<String, usize> = HashMap::new();
    for a in &areas {
        for d in &a.desks {
            *seen.entry(d.title.clone()).or_default() += 1;
        }
    }
    for a in &mut areas {
        for d in &mut a.desks {
            if seen.get(&d.title).copied().unwrap_or(0) > 1 {
                d.title = format!("{} ({})", d.title, d.name);
            }
        }
    }
    for i in needs.iter_mut().chain(review.iter_mut()) {
        let d = if i.r#ref.ends_with("/lead") { None } else { areas.iter().find(|a| a.project == i.project).and_then(|a| a.desks.iter().find(|x| x.id == i.r#ref)) };
        if let Some(d) = d {
            i.title = d.title.clone();
        }
    }
    OfficeModel { areas, board: Board { needs, review } }
}

/// deskOf finds a desk by its agent's ref.
pub fn desk_of<'a>(model: &'a OfficeModel, r#ref: &str) -> Option<&'a DeskModel> {
    model.areas.iter().flat_map(|a| a.desks.iter()).find(|d| d.id == r#ref)
}

/// A JSON value with its objects' keys in the order JS reads them, which is
/// what `JSON.parse` and `Object.entries` give (serde_json's `Value` sorts
/// keys, or keeps them in file order only with a feature that would change
/// every crate in the workspace).
enum Json {
    Null,
    Bool(bool),
    Num(f64),
    Str(String),
    Arr(Vec<Json>),
    Obj(Vec<(String, Json)>),
}

impl<'de> serde::Deserialize<'de> for Json {
    fn deserialize<D: serde::Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        struct V;
        impl<'de> serde::de::Visitor<'de> for V {
            type Value = Json;
            fn expecting(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
                f.write_str("JSON")
            }
            fn visit_unit<E>(self) -> Result<Json, E> {
                Ok(Json::Null)
            }
            fn visit_bool<E>(self, v: bool) -> Result<Json, E> {
                Ok(Json::Bool(v))
            }
            fn visit_i64<E>(self, v: i64) -> Result<Json, E> {
                Ok(Json::Num(v as f64))
            }
            fn visit_u64<E>(self, v: u64) -> Result<Json, E> {
                Ok(Json::Num(v as f64))
            }
            fn visit_f64<E>(self, v: f64) -> Result<Json, E> {
                Ok(Json::Num(v))
            }
            fn visit_str<E>(self, v: &str) -> Result<Json, E> {
                Ok(Json::Str(v.to_string()))
            }
            fn visit_seq<A: serde::de::SeqAccess<'de>>(self, mut a: A) -> Result<Json, A::Error> {
                let mut v = Vec::new();
                while let Some(x) = a.next_element()? {
                    v.push(x);
                }
                Ok(Json::Arr(v))
            }
            fn visit_map<A: serde::de::MapAccess<'de>>(self, mut a: A) -> Result<Json, A::Error> {
                let mut v: Vec<(String, Json)> = Vec::new();
                while let Some((k, x)) = a.next_entry::<String, Json>()? {
                    // A repeated key keeps its first place and its last value.
                    match v.iter_mut().find(|(n, _)| *n == k) {
                        Some(slot) => slot.1 = x,
                        None => v.push((k, x)),
                    }
                }
                // Integer-like keys come first, ascending, as JS orders them.
                let index = |k: &str| k.parse::<u32>().ok().filter(|n| *n != u32::MAX && n.to_string() == k);
                v.sort_by_key(|(k, _)| index(k).map_or((1, 0), |n| (0, n)));
                Ok(Json::Obj(v))
            }
        }
        d.deserialize_any(V)
    }
}

/// `JSON.stringify` of a value.
fn stringify(v: &Json) -> String {
    match v {
        Json::Null => "null".into(),
        Json::Bool(b) => b.to_string(),
        Json::Num(n) if !n.is_finite() => "null".into(),
        Json::Num(n) if n.fract() == 0. && n.abs() < 1e21 => format!("{}", *n as i128),
        Json::Num(n) => format!("{n}"),
        Json::Str(s) => serde_json::to_string(s).unwrap_or_default(),
        Json::Arr(a) => format!("[{}]", a.iter().map(stringify).collect::<Vec<_>>().join(",")),
        Json::Obj(o) => format!("{{{}}}", o.iter().map(|(k, x)| format!("{}:{}", serde_json::to_string(k).unwrap_or_default(), stringify(x))).collect::<Vec<_>>().join(",")),
    }
}

/// JS's `\s`, and what `String.prototype.trim` strips.
const WS_ITEMS: &str = r"\t\n\x0B\x0C\r \u{a0}\u{1680}\u{2000}-\u{200a}\u{2028}\u{2029}\u{202f}\u{205f}\u{3000}\u{feff}";

fn is_js_space(c: char) -> bool {
    matches!(c, '\t' | '\n' | '\u{b}' | '\u{c}' | '\r' | ' ' | '\u{a0}' | '\u{1680}' | '\u{2000}'..='\u{200a}' | '\u{2028}' | '\u{2029}' | '\u{202f}' | '\u{205f}' | '\u{3000}' | '\u{feff}')
}

/// readable is a question as a person reads it: JSON, which some agents ask
/// in, becomes its fields, "key: value", one after another.
pub fn readable(text: &str) -> String {
    let t = text.trim_matches(is_js_space);
    if !(t.starts_with('{') || t.starts_with('[')) {
        return text.to_string();
    }
    // (Not JSON after all: as it is.)
    let Ok(v) = serde_json::from_str::<Json>(t) else { return text.to_string() };
    let show = |x: &Json| match x {
        Json::Str(s) => s.clone(),
        x => stringify(x),
    };
    match &v {
        Json::Arr(a) => a.iter().take(6).map(show).collect::<Vec<_>>().join(" · "),
        Json::Obj(o) => o.iter().take(6).map(|(k, x)| format!("{k}: {}", show(x))).collect::<Vec<_>>().join(" · "),
        _ => text.to_string(),
    }
}

fn re(p: String) -> Regex {
    Regex::new(&p).expect("a fixed pattern")
}

/// The pieces of `plain`, the JS regexes with `\s`, `\d` and `.` spelled out so
/// they match what JS does.
struct PlainRes([Regex; 7]);

static PLAIN: LazyLock<PlainRes> = LazyLock::new(|| {
    let ws = format!("[{WS_ITEMS}]");
    let dot = r"[^\n\r\u{2028}\u{2029}]";
    PlainRes([
        re("(?i)```[a-z]*\n?".into()),
        re(r"\[([^\]]+)\]\([^)]+\)".into()),
        re(format!(r"(\*\*|__)({dot}+?)\1")),
        re(format!(r"(^|[{WS_ITEMS}(])[*_]([^*_{WS_ITEMS}][^*_]*?)[*_](?=[{WS_ITEMS}).,!?:;]|$)")),
        re("`([^`]+)`".into()),
        re(format!(r"(?m)^{ws}{{0,3}}(#{{1,6}}|[-*+]|[0-9]+\.){ws}+")),
        re(format!(r"{ws}*\n+{ws}*")),
    ])
});

/// plain is a chat's markdown as one line of text: emphasis, code marks,
/// links and headings' marks gone, their words kept.
pub fn plain(text: &str) -> String {
    let r = &PLAIN.0;
    let s = r[0].replace_all(text, "");
    let s = r[1].replace_all(&s, "$1");
    let s = r[2].replace_all(&s, "$2");
    let s = r[3].replace_all(&s, "$1$2");
    let s = r[4].replace_all(&s, "$1");
    let s = r[5].replace_all(&s, "");
    let s = r[6].replace_all(&s, " ");
    s.trim_matches(is_js_space).to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn agent(r: &str, f: impl FnOnce(&mut t::Agent)) -> t::Agent {
        let (project, name) = r.split_once('/').unwrap();
        let mut a = t::Agent {
            r#ref: r.into(),
            project: project.into(),
            name: name.into(),
            title: format!("Task {r}"),
            branch: format!("agentbox/{name}"),
            ai: "claude".into(),
            state: "running".into(),
            created_at: "2026-11-01T10:00:00Z".into(),
            ..Default::default()
        };
        f(&mut a);
        a
    }
    fn a(r: &str) -> t::Agent {
        agent(r, |_| {})
    }
    fn row(a: t::Agent, st: AgentSt) -> Row {
        Row { agent: a, st }
    }
    fn pr(f: impl FnOnce(&mut t::PullRequest)) -> t::PullRequest {
        let mut p = t::PullRequest { number: 7, title: "feat: x".into(), state: "OPEN".into(), checks: "passing".into(), url: "https://github.com/o/r/pull/7".into(), ..Default::default() };
        f(&mut p);
        p
    }
    fn projects() -> Vec<ProjectRef> {
        vec![ProjectRef { name: "web".into(), display_name: "Web app".into() }, ProjectRef { name: "api".into(), display_name: String::new() }]
    }

    /// Everything the model reads, owned, so a test fills in what it needs.
    #[derive(Default)]
    struct In {
        rows: Vec<Row>,
        fleets: HashMap<String, t::Fleet>,
        questions: Vec<(String, Vec<t::Question>)>,
        leads: HashMap<String, t::ProjectChat>,
        review: Vec<t::ReviewItem>,
    }
    impl In {
        fn model(&self) -> OfficeModel {
            office_model(&OfficeInputs { projects: &projects(), rows: &self.rows, fleets: &self.fleets, questions: &self.questions, leads: &self.leads, review: &self.review })
        }
    }
    fn lead(chat: &str, text: Option<&str>) -> t::ProjectChat {
        t::ProjectChat { chat: chat.into(), last: text.map(|t| t::ChatLine { kind: "assistant".into(), text: t.into(), at: String::new() }), ..Default::default() }
    }

    #[test]
    fn a_sidebar_state_is_a_desk_tone_done_split_by_the_machine() {
        assert_eq!(desk_tone(&a("p/a"), AgentSt::Needs), DeskTone::Needs);
        assert_eq!(desk_tone(&a("p/a"), AgentSt::Error), DeskTone::Error);
        assert_eq!(desk_tone(&a("p/a"), AgentSt::Review), DeskTone::Finished);
        assert_eq!(desk_tone(&a("p/a"), AgentSt::Working), DeskTone::Working);
        assert_eq!(desk_tone(&a("p/a"), AgentSt::Done), DeskTone::Idle);
        assert_eq!(desk_tone(&agent("p/a", |x| x.state = "stopped".into()), AgentSt::Done), DeskTone::Stopped);
    }

    #[test]
    fn a_pull_request_reads_as_open_draft_merged_or_closed_with_its_checks() {
        assert_eq!(pr_look(None), None);
        assert_eq!(
            pr_look(Some(&pr(|_| {}))),
            Some(PrLook { number: 7, state: PrState::Open, checks: PrChecks::Passing, url: "https://github.com/o/r/pull/7".into(), title: "feat: x".into() })
        );
        assert_eq!(pr_look(Some(&pr(|p| p.draft = true))).unwrap().state, PrState::Draft);
        assert_eq!(pr_look(Some(&pr(|p| {
            p.state = "MERGED".into();
            p.draft = true;
        })))
        .unwrap()
        .state, PrState::Merged);
        assert_eq!(pr_look(Some(&pr(|p| p.state = "closed".into()))).unwrap().state, PrState::Closed);
        assert_eq!(pr_look(Some(&pr(|p| p.checks = "failing".into()))).unwrap().checks, PrChecks::Failing);
        assert_eq!(pr_look(Some(&pr(|p| p.checks = String::new()))).unwrap().checks, PrChecks::None);
    }

    #[test]
    fn a_lead_waiting_on_a_permission_needs_you() {
        assert_eq!(lead_tone(Some("waiting")), LeadTone::Needs);
        assert_eq!(lead_tone(Some("running")), LeadTone::Working);
        assert_eq!(lead_tone(Some("ready")), LeadTone::Idle);
        assert_eq!(lead_tone(None), LeadTone::Idle);
    }

    #[test]
    fn a_project_keeps_its_colour() {
        assert_eq!(hue_of("web"), hue_of("web"));
        assert!(hue_of("web") >= 0. && hue_of("web") < 360.);
    }

    #[test]
    fn areas_go_in_the_projects_order_desks_in_the_order_agents_were_made() {
        let m = In {
            rows: vec![
                row(agent("api/agent-3", |x| x.created_at = "2026-11-03T00:00:00Z".into()), AgentSt::Working),
                row(agent("web/agent-2", |x| x.created_at = "2026-11-02T00:00:00Z".into()), AgentSt::Working),
                row(agent("web/agent-1", |x| x.created_at = "2026-11-01T00:00:00Z".into()), AgentSt::Done),
            ],
            ..Default::default()
        }
        .model();
        assert_eq!(m.areas.iter().map(|a| a.project.as_str()).collect::<Vec<_>>(), ["web", "api"]);
        assert_eq!(m.areas[0].label, "Web app");
        assert_eq!(m.areas[1].label, "api");
        assert_eq!(m.areas[0].desks.iter().map(|d| d.id.as_str()).collect::<Vec<_>>(), ["web/agent-1", "web/agent-2"]);
    }

    #[test]
    fn a_project_with_nothing_going_on_has_no_area_one_the_list_lacks_still_does() {
        let m = In { rows: vec![row(a("new/agent-1"), AgentSt::Working)], ..Default::default() }.model();
        assert_eq!(m.areas.iter().map(|a| a.project.as_str()).collect::<Vec<_>>(), ["new"]);
        let quiet = In { leads: HashMap::from([("api".into(), lead("ready", Some("Hi")))]), ..Default::default() }.model();
        assert!(quiet.areas.is_empty());
        let thinking = In { leads: HashMap::from([("api".into(), lead("running", Some("Hi")))]), ..Default::default() }.model();
        assert_eq!(thinking.areas.iter().map(|a| a.project.as_str()).collect::<Vec<_>>(), ["api"]);
        assert_eq!(thinking.areas[0].lead.line.as_deref(), Some("Hi"));
    }

    fn question(id: &str, agent: &str, r: &str, q: &str, status: &str) -> t::Question {
        t::Question { id: id.into(), project: "web".into(), agent: agent.into(), r#ref: r.into(), question: q.into(), status: status.into(), ..Default::default() }
    }

    #[test]
    fn the_board_lists_who_needs_you_then_what_waits_for_review() {
        let asking = a("web/agent-1");
        let broken = agent("web/agent-2", |x| {
            x.outcome = "failed".into();
            x.outcome_note = "WebSocket closed".into();
        });
        let done = a("web/agent-3");
        let pr_open = a("web/agent-4");
        let busy = a("web/agent-5");
        // (TS: `{ ref, pr, changes }` cast to a Fleet, the rest absent.)
        let fa = |r: &str, p: t::PullRequest, changes: Option<t::AgentChanges>| t::FleetAgent { r#ref: r.into(), pr: Some(p), changes: changes.unwrap_or_default(), ..Default::default() };
        let m = In {
            rows: vec![row(asking, AgentSt::Needs), row(broken, AgentSt::Error), row(done, AgentSt::Review), row(pr_open, AgentSt::Done), row(busy, AgentSt::Working)],
            questions: vec![(
                "web".into(),
                vec![question("q1", "agent-1", "web/agent-1", "Paginate?", "escalated"), question("q2", "agent-2", "web/agent-2", "For the lead only", "pending")],
            )],
            fleets: HashMap::from([(
                "web".into(),
                t::Fleet {
                    project: "web".into(),
                    agents: vec![
                        fa("web/agent-4", pr(|p| p.number = 9), Some(t::AgentChanges { files: 2, insertions: 10, deletions: 1, dirty: false })),
                        fa("web/agent-5", pr(|p| p.number = 10), None),
                    ],
                    ..Default::default()
                },
            )]),
            review: vec![t::ReviewItem { project: "web".into(), agent: "agent-3".into(), r#ref: "web/agent-3".into(), pr: Some(pr(|p| {
                p.number = 8;
                p.checks = "failing".into();
            })), ..Default::default() }],
            leads: HashMap::from([("web".into(), lead("waiting", None))]),
        }
        .model();
        assert_eq!(
            m.board.needs.iter().map(|i| (i.r#ref.as_str(), i.tone, i.text.as_deref())).collect::<Vec<_>>(),
            [("web/lead", DeskTone::Needs, None), ("web/agent-1", DeskTone::Needs, Some("Paginate?")), ("web/agent-2", DeskTone::Error, Some("WebSocket closed"))]
        );
        // A working agent's open PR isn't waiting for you yet.
        assert_eq!(m.board.review.iter().map(|i| (i.r#ref.as_str(), i.pr.as_ref().map(|p| p.number))).collect::<Vec<_>>(), [("web/agent-3", Some(8)), ("web/agent-4", Some(9))]);
        let d = desk_of(&m, "web/agent-4");
        assert_eq!(d.and_then(|d| d.changes), Some(Changes { files: 2, insertions: 10, deletions: 1 }));
        assert_eq!(desk_of(&m, "web/agent-3").and_then(|d| d.pr.as_ref()).map(|p| p.checks), Some(PrChecks::Failing));
        assert!(desk_of(&m, "nope/agent-1").is_none());
    }

    #[test]
    fn an_agent_asking_in_its_own_words_with_no_question_filed_is_quoted() {
        let m = In {
            rows: vec![row(
                agent("web/agent-1", |x| {
                    x.outcome = "asking".into();
                    x.outcome_note = "Launch or stop?".into();
                }),
                AgentSt::Needs,
            )],
            ..Default::default()
        }
        .model();
        assert_eq!(m.board.needs[0].text.as_deref(), Some("Launch or stop?"));
    }

    #[test]
    fn agents_with_the_same_title_are_told_apart_by_name() {
        let titled = |r: &str, title: &str| agent(r, |x| x.title = title.into());
        let m = In {
            rows: vec![row(titled("web/agent-1", "Fix it"), AgentSt::Needs), row(titled("web/agent-2", "Fix it"), AgentSt::Working), row(titled("web/agent-3", "Other"), AgentSt::Working)],
            ..Default::default()
        }
        .model();
        assert_eq!(m.areas[0].desks.iter().map(|d| d.title.as_str()).collect::<Vec<_>>(), ["Fix it (agent-1)", "Fix it (agent-2)", "Other"]);
        assert_eq!(m.board.needs[0].title, "Fix it (agent-1)");
    }

    #[test]
    fn a_question_asked_in_json_reads_as_its_fields() {
        assert_eq!(readable("Paginate?"), "Paginate?");
        assert_eq!(readable(r#"{"state":"running","limit":3}"#), "state: running · limit: 3");
        assert_eq!(readable(r#"["a", 2]"#), "a · 2");
        assert_eq!(readable("{not json"), "{not json");
        // JS orders integer-like keys first, and prints numbers as it does.
        assert_eq!(readable(r#"{"b":1.0,"2":[1,"x"],"a":null,"1":{"k":true}}"#), r#"1: {"k":true} · 2: [1,"x"] · b: 1 · a: null"#);
    }

    #[test]
    fn chat_markdown_reads_as_plain_text_on_a_card() {
        assert_eq!(plain("agent-12 finished the **New project** button: PR #318 is green"), "agent-12 finished the New project button: PR #318 is green");
        assert_eq!(plain("`git push -u origin x` fails, see [the log](https://x.y/z)"), "git push -u origin x fails, see the log");
        assert_eq!(plain("## Done\n- one *small* thing\n- snake_case_name stays"), "Done one small thing snake_case_name stays");
    }
}
