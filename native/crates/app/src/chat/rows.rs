//! The timeline's rows (lib/chat.ts timelineRows): a conversation's items
//! grouped into turns, each user message heading its turn, the work between
//! messages folded into one row, a settled turn's work behind its "Worked
//! for …", what it changed, and the checkpoint it can be rolled back to.
//! Rows point into the items by index: they are rebuilt whenever the items
//! change, so an index never outlives the items it was taken from.
use std::collections::{HashMap, HashSet};
use std::hash::{Hash, Hasher};

use agentbox_api::{ChatDiff, ChatItem, ChatSession, Checkpoint};
use agentbox_i18n::{t, tv};

#[derive(Clone, Debug, PartialEq)]
pub enum Row {
    User(usize),
    Woken(usize),
    Background(Vec<String>),
    Assistant {
        ix: usize,
        last: bool,
    },
    Work {
        items: Vec<usize>,
        live: bool,
    },
    Fold {
        turn: String,
        label: String,
        open: bool,
        empty: bool,
    },
    Working {
        since: String,
        stalled: Option<String>,
    },
    Thinking,
    Note(usize),
    Subagent {
        ix: usize,
        children: Vec<usize>,
    },
    Compaction(usize),
    Changes(Vec<ChangedFile>),
    Turn(Checkpoint),
}

#[derive(Clone, Debug, PartialEq)]
pub struct ChangedFile {
    pub path: String,
    pub added: usize,
    pub removed: usize,
}

/// A row and what tells it apart: its key, the same across rebuilds, and a
/// signature that changes when what it shows does.
pub struct Keyed {
    pub key: String,
    pub sig: u64,
    pub row: Row,
}

pub fn is_work(it: &ChatItem) -> bool {
    it.kind == "tool"
        || it.kind == "thought"
        || (it.kind == "permission"
            && it
                .permission
                .as_ref()
                .is_some_and(|p| !p.outcome.is_empty()))
}

fn is_note(it: &ChatItem) -> bool {
    matches!(it.kind.as_str(), "notice" | "error" | "compaction" | "goal")
}

/// is_silent is AgentBox's prose to a project's chat, addressed to the model.
pub fn is_silent(it: &ChatItem) -> bool {
    it.hidden && it.finished.is_empty() && it.group.is_empty()
}

pub fn is_active(it: &ChatItem) -> bool {
    (it.kind == "thought" && it.streaming)
        || (it.kind == "tool"
            && it
                .tool
                .as_ref()
                .is_some_and(|t| t.status == "pending" || t.status == "in_progress"))
        || (it.kind == "subagent" && it.subagent.as_ref().is_some_and(|s| s.state == "running"))
}

/// folds: what a settled turn puts behind its fold, the work, never what was said.
fn folds(it: &ChatItem) -> bool {
    it.kind != "assistant" && !is_note(it) && it.kind != "aside"
}

pub fn millis(time: &str) -> Option<i64> {
    chrono::DateTime::parse_from_rfc3339(time)
        .ok()
        .map(|d| d.timestamp_millis())
}

pub fn format_duration(seconds: f64) -> String {
    let s = seconds.max(0.).round() as i64;
    if s < 60 {
        tv("chat.duration.seconds", &[("s", s.into())])
    } else if s < 3600 {
        tv(
            "chat.duration.minutes",
            &[("m", (s / 60).into()), ("s", (s % 60).into())],
        )
    } else {
        tv(
            "chat.duration.hours",
            &[("h", (s / 3600).into()), ("m", ((s % 3600) / 60).into())],
        )
    }
}

fn fold_label(user: &ChatItem) -> String {
    let Some(r) = &user.result else {
        return String::new();
    };
    let took = match (millis(&r.ended_at), millis(&user.created_at)) {
        (Some(a), Some(b)) => (a - b) as f64 / 1000.,
        _ => 0.,
    };
    let time = format_duration(took);
    let key = match r.state.as_str() {
        "cancelled" => "chat.fold.stopped",
        "failed" => "chat.fold.failed",
        _ => "chat.fold.worked",
    };
    tv(key, &[("time", time.into())])
}

/// line_diff is the lines two texts differ by, as a unified diff's lines
/// (DiffView): ' ' kept, '-' gone, '+' new. Long texts are compared by
/// their ends only, which is where an edit's change usually is.
pub fn line_diff(old: &str, new: &str) -> Vec<(char, String)> {
    let a: Vec<&str> = old.lines().collect();
    let b: Vec<&str> = new.lines().collect();
    let pre = a.iter().zip(&b).take_while(|(x, y)| x == y).count();
    let suf = a[pre..]
        .iter()
        .rev()
        .zip(b[pre..].iter().rev())
        .take_while(|(x, y)| x == y)
        .count();
    let (ma, mb) = (&a[pre..a.len() - suf], &b[pre..b.len() - suf]);
    let mut out: Vec<(char, String)> = Vec::new();
    let ctx = 3;
    for l in &a[pre.saturating_sub(ctx)..pre] {
        out.push((' ', l.to_string()));
    }
    if ma.len() * mb.len() > 250_000 {
        out.extend(ma.iter().map(|l| ('-', l.to_string())));
        out.extend(mb.iter().map(|l| ('+', l.to_string())));
    } else {
        // Longest common subsequence, then walked from the start.
        let (n, m) = (ma.len(), mb.len());
        let mut dp = vec![0u32; (n + 1) * (m + 1)];
        for i in (0..n).rev() {
            for j in (0..m).rev() {
                dp[i * (m + 1) + j] = if ma[i] == mb[j] {
                    dp[(i + 1) * (m + 1) + j + 1] + 1
                } else {
                    dp[(i + 1) * (m + 1) + j].max(dp[i * (m + 1) + j + 1])
                };
            }
        }
        let (mut i, mut j) = (0, 0);
        while i < n || j < m {
            if i < n && j < m && ma[i] == mb[j] {
                out.push((' ', ma[i].to_string()));
                i += 1;
                j += 1;
            } else if j < m && (i == n || dp[i * (m + 1) + j + 1] >= dp[(i + 1) * (m + 1) + j]) {
                out.push(('+', mb[j].to_string()));
                j += 1;
            } else {
                out.push(('-', ma[i].to_string()));
                i += 1;
            }
        }
    }
    let end = b.len() - suf;
    for l in &b[end..(end + ctx).min(b.len())] {
        out.push((' ', l.to_string()));
    }
    out
}

pub fn line_counts(d: &ChatDiff) -> (usize, usize) {
    let lines = line_diff(&d.old_text, &d.new_text);
    (
        lines.iter().filter(|l| l.0 == '+').count(),
        lines.iter().filter(|l| l.0 == '-').count(),
    )
}

/// changed_files is what a turn's tool calls changed, file by file.
fn changed_files<'a>(items: impl Iterator<Item = &'a ChatItem>) -> Vec<ChangedFile> {
    let mut out: Vec<ChangedFile> = Vec::new();
    for it in items {
        let Some(tool) = &it.tool else { continue };
        for d in &tool.diffs {
            let (added, removed) = line_counts(d);
            match out.iter_mut().find(|f| f.path == d.path) {
                Some(f) => {
                    f.added += added;
                    f.removed += removed;
                }
                None => out.push(ChangedFile {
                    path: d.path.clone(),
                    added,
                    removed,
                }),
            }
        }
    }
    out
}

struct Turn {
    id: String,
    user: Option<usize>,
    items: Vec<usize>,
}

fn turns_of(items: &[ChatItem], top: &[usize]) -> Vec<Turn> {
    let mut turns: Vec<Turn> = Vec::new();
    let mut by_id: HashMap<String, usize> = HashMap::new();
    for &ix in top {
        let it = &items[ix];
        if it.kind == "user" {
            by_id.insert(it.id.clone(), turns.len());
            turns.push(Turn {
                id: it.id.clone(),
                user: Some(ix),
                items: Vec::new(),
            });
            continue;
        }
        let at = match by_id.get(&it.turn) {
            Some(&at) => at,
            None => {
                let id = if it.turn.is_empty() {
                    format!("loose:{}", it.id)
                } else {
                    it.turn.clone()
                };
                by_id.insert(id.clone(), turns.len());
                turns.push(Turn {
                    id,
                    user: None,
                    items: Vec::new(),
                });
                turns.len() - 1
            }
        };
        turns[at].items.push(ix);
    }
    turns
}

/// rows builds the timeline. open_turns are the settled turns opened past
/// their fold; checkpoints are the agent's, by the user message whose turn
/// each ends.
pub fn rows(
    items: &[ChatItem],
    session: &ChatSession,
    open_turns: &HashSet<String>,
    checkpoints: &HashMap<String, Checkpoint>,
) -> Vec<Keyed> {
    let mut out: Vec<Keyed> = Vec::new();
    let mut top: Vec<usize> = Vec::new();
    let mut children: HashMap<&str, Vec<usize>> = HashMap::new();
    for (ix, it) in items.iter().enumerate() {
        if it.parent.is_empty() {
            top.push(ix);
        } else {
            children.entry(it.parent.as_str()).or_default().push(ix);
        }
    }
    let push = |out: &mut Vec<Keyed>, key: String, row: Row| {
        let sig = signature(items, &row);
        out.push(Keyed { key, sig, row });
    };
    for turn in turns_of(items, &top) {
        let user = turn.user.map(|ix| &items[ix]);
        let running = user.is_some_and(|u| u.result.is_none());
        let settled = user.is_some_and(|u| u.result.is_some());
        let body: Vec<usize> = turn
            .items
            .iter()
            .copied()
            .filter(|&ix| {
                let it = &items[ix];
                !is_silent(it)
                    && (it.kind == "assistant"
                        || it.kind == "subagent"
                        || is_work(it)
                        || is_note(it)
                        || it.kind == "aside")
            })
            .collect();
        let last_answer = body
            .iter()
            .rev()
            .find(|&&ix| items[ix].kind == "assistant")
            .copied();
        let hidden = if settled {
            body.iter().filter(|&&ix| folds(&items[ix])).count()
        } else {
            0
        };
        let open = hidden == 0 || open_turns.contains(&turn.id);
        if let (Some(u), Some(uix)) = (user, turn.user) {
            if u.woken {
                push(&mut out, u.id.clone(), Row::Woken(uix));
            } else if !is_silent(u) {
                push(&mut out, u.id.clone(), Row::User(uix));
            }
            if running {
                push(
                    &mut out,
                    format!("working:{}", turn.id),
                    Row::Working {
                        since: u.created_at.clone(),
                        stalled: session.stalled_since.clone(),
                    },
                );
            }
            let cancelled = u.result.as_ref().is_some_and(|r| r.state == "cancelled");
            if hidden > 0 || (cancelled && last_answer.is_none()) {
                push(
                    &mut out,
                    format!("fold:{}", turn.id),
                    Row::Fold {
                        turn: turn.id.clone(),
                        label: fold_label(u),
                        open,
                        empty: hidden == 0,
                    },
                );
            }
        }
        let mut group: Vec<usize> = Vec::new();
        let end_group = |out: &mut Vec<Keyed>, group: &mut Vec<usize>, live: bool| {
            if let Some(&first) = group.first() {
                let row = Row::Work {
                    items: std::mem::take(group),
                    live,
                };
                let sig = signature(items, &row);
                out.push(Keyed {
                    key: format!("work:{}", items[first].id),
                    sig,
                    row,
                });
            }
        };
        for &ix in &body {
            let it = &items[ix];
            if !open && folds(it) {
                continue;
            }
            if is_work(it) {
                group.push(ix);
                continue;
            }
            end_group(&mut out, &mut group, false);
            let row = match it.kind.as_str() {
                "aside" => Row::User(ix),
                "subagent" => Row::Subagent {
                    ix,
                    children: children.get(it.id.as_str()).cloned().unwrap_or_default(),
                },
                "compaction" => Row::Compaction(ix),
                "assistant" => Row::Assistant {
                    ix,
                    last: settled && Some(ix) == last_answer,
                },
                _ => Row::Note(ix),
            };
            push(&mut out, it.id.clone(), row);
        }
        end_group(&mut out, &mut group, running);
        if running {
            let last = body.last().map(|&ix| &items[ix]);
            if !last.is_some_and(|l| (l.kind == "assistant" && l.streaming) || is_active(l)) {
                push(&mut out, format!("thinking:{}", turn.id), Row::Thinking);
            }
        }
        if settled {
            let nested = turn
                .items
                .iter()
                .filter(|&&ix| items[ix].kind == "subagent")
                .flat_map(|&ix| {
                    children
                        .get(items[ix].id.as_str())
                        .cloned()
                        .unwrap_or_default()
                });
            let all: Vec<usize> = turn.items.iter().copied().chain(nested).collect();
            let files = changed_files(all.iter().map(|&ix| &items[ix]));
            if !files.is_empty() {
                push(
                    &mut out,
                    format!("changes:{}", turn.id),
                    Row::Changes(files),
                );
            }
            if let Some(cp) = user.and_then(|u| checkpoints.get(&u.id)) {
                push(&mut out, format!("turn:{}", turn.id), Row::Turn(cp.clone()));
            }
        }
    }
    if !session.background.is_empty() && session.turn_started_at.is_none() {
        push(
            &mut out,
            "background".into(),
            Row::Background(session.background.clone()),
        );
    }
    out
}

fn signature(items: &[ChatItem], row: &Row) -> u64 {
    let mut h = std::collections::hash_map::DefaultHasher::new();
    let item = |h: &mut std::collections::hash_map::DefaultHasher, ix: usize| {
        let it = &items[ix];
        it.id.hash(h);
        it.updated_at.hash(h);
        it.text.len().hash(h);
        it.streaming.hash(h);
        it.delivery.hash(h);
        it.tool
            .as_ref()
            .map(|t| (&t.status, t.output.len()))
            .hash(h);
        it.subagent.as_ref().map(|s| &s.state).hash(h);
        it.compaction
            .as_ref()
            .map(|c| (&c.state, c.waiting))
            .hash(h);
        it.agent_permission
            .as_ref()
            .map(|p| &p.permission.outcome)
            .hash(h);
        it.result.as_ref().map(|r| &r.state).hash(h);
    };
    match row {
        Row::User(ix) | Row::Woken(ix) | Row::Note(ix) | Row::Compaction(ix) => item(&mut h, *ix),
        Row::Assistant { ix, last } => {
            item(&mut h, *ix);
            last.hash(&mut h);
        }
        Row::Work { items: ixs, live } => {
            live.hash(&mut h);
            for &ix in ixs {
                item(&mut h, ix);
            }
        }
        Row::Subagent { ix, children } => {
            item(&mut h, *ix);
            for &c in children {
                item(&mut h, c);
            }
        }
        Row::Fold {
            label, open, empty, ..
        } => (label, open, empty).hash(&mut h),
        Row::Working { since, stalled } => (since, stalled).hash(&mut h),
        Row::Background(tasks) => tasks.hash(&mut h),
        Row::Thinking => {}
        Row::Changes(files) => {
            for f in files {
                (&f.path, f.added, f.removed).hash(&mut h);
            }
        }
        Row::Turn(cp) => (&cp.id, cp.number).hash(&mut h),
    }
    h.finish()
}

/// work_summary is what a folded group of work says it did (workSummary).
pub fn work_summary(items: &[&ChatItem]) -> String {
    let (mut commands, mut reads, mut searches, mut fetches, mut others) = (0, 0, 0, 0, 0);
    let (mut thoughts, mut answers) = (0, 0);
    let mut edited: HashSet<String> = HashSet::new();
    for it in items {
        if it.kind == "thought" {
            thoughts += 1;
        } else if it.kind == "permission" {
            answers += 1;
        } else if let Some(tool) = &it.tool {
            match tool.kind.as_str() {
                "execute" => commands += 1,
                "read" => reads += 1,
                "edit" | "delete" | "move" => {
                    if tool.paths.is_empty() {
                        edited.insert(tool.title.clone());
                    }
                    edited.extend(tool.paths.iter().cloned());
                }
                "search" => searches += 1,
                "fetch" => fetches += 1,
                _ => others += 1,
            }
        }
    }
    let part = |key: &str, n: usize| (n > 0).then(|| tv(key, &[("count", n.into())]));
    let mut parts: Vec<String> = [
        part("chat.work.ran", commands),
        part("chat.work.edited", edited.len()),
        part("chat.work.read", reads),
        part("chat.work.searched", searches),
        part("chat.work.fetched", fetches),
        part("chat.work.used", others),
    ]
    .into_iter()
    .flatten()
    .collect();
    if parts.is_empty() {
        parts = [
            part("chat.work.thought", thoughts),
            part("chat.work.answered", answers),
        ]
        .into_iter()
        .flatten()
        .collect();
    }
    let text = match parts.len() {
        0 => String::new(),
        1 => parts.remove(0),
        _ => {
            let last = parts.pop().unwrap_or_default();
            tv(
                "chat.work.join",
                &[
                    ("head", parts.join(&t("chat.work.separator")).into()),
                    ("last", last.into()),
                ],
            )
        }
    };
    let mut c = text.chars();
    match c.next() {
        Some(f) => f.to_uppercase().chain(c).collect(),
        None => text,
    }
}

fn file_name(path: &str) -> &str {
    path.rsplit('/').find(|s| !s.is_empty()).unwrap_or(path)
}

/// entry_label is what a work row says of a finished item (entryLabel).
pub fn entry_label(it: &ChatItem) -> String {
    if it.kind == "thought" {
        return t("chat.work.thoughtDone");
    }
    if let (true, Some(p)) = (it.kind == "permission", &it.permission) {
        let kind = p
            .options
            .iter()
            .find(|o| o.id == p.outcome)
            .map(|o| o.kind.as_str())
            .unwrap_or("");
        let outcome = if p.outcome == "cancelled" {
            "cancelled"
        } else if kind.starts_with("reject") {
            "denied"
        } else {
            "allowed"
        };
        return tv(
            "chat.work.permission",
            &[("outcome", outcome.into()), ("title", (&p.title).into())],
        );
    }
    let Some(tool) = &it.tool else {
        return t("chat.work.toolCall");
    };
    if tool.kind == "execute" && !tool.command.is_empty() {
        return tool.command.clone();
    }
    if !tool.title.is_empty() {
        return tool.title.clone();
    }
    if !tool.name.is_empty() {
        return tool.name.clone();
    }
    t("chat.work.toolCall")
}

/// live_label is what a work row says while its item happens (liveLabel).
pub fn live_label(it: &ChatItem) -> String {
    if it.kind == "thought" {
        return t("chat.work.thinking");
    }
    let Some(tool) = it.tool.as_ref().filter(|_| is_active(it)) else {
        return entry_label(it);
    };
    let path = tool.paths.first().map(|p| file_name(p).to_string());
    let what = |s: &str| {
        if s.is_empty() {
            tool.title.clone()
        } else {
            s.to_string()
        }
    };
    match (tool.kind.as_str(), path) {
        ("execute", _) => tv("chat.work.running", &[("what", what(&tool.command).into())]),
        ("edit", Some(p)) => tv("chat.work.editing", &[("name", p.into())]),
        ("read", Some(p)) => tv("chat.work.reading", &[("name", p.into())]),
        ("search", _) => tv("chat.work.searching", &[("what", (&tool.title).into())]),
        ("fetch", _) => tv("chat.work.fetching", &[("what", (&tool.title).into())]),
        _ if !tool.title.is_empty() => tool.title.clone(),
        _ if !tool.name.is_empty() => tool.name.clone(),
        _ => t("chat.work.working"),
    }
}

/// page_url is the Hatch page a finished publish_page or update_page call
/// made or changed: the link in its answer (lib/pages.ts pageIdOf).
pub fn page_url(it: &ChatItem) -> Option<String> {
    let tool = it.tool.as_ref()?;
    if tool.status != "completed" {
        return None;
    }
    let call = [&tool.name, &tool.title].iter().any(|s| {
        let s = s.trim().to_lowercase();
        ["publish_page", "update_page"].iter().any(|p| {
            s.ends_with(p) && !s[..s.len() - p.len()].ends_with(|c: char| c.is_ascii_alphanumeric())
        })
    });
    if !call {
        return None;
    }
    let out = &tool.output;
    let mut from = 0;
    while let Some(at) = out[from..].find("http") {
        let start = from + at;
        let end = out[start..]
            .find(|c: char| c.is_whitespace() || "\"'<>()\\".contains(c))
            .map_or(out.len(), |e| start + e);
        let url = &out[start..end];
        if (url.starts_with("http://") || url.starts_with("https://")) && url.contains("/p/") {
            return Some(url.trim_end_matches([',', '.']).to_string());
        }
        from = end;
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    use agentbox_api::{ChatTool, ChatTurnResult};

    fn item(id: &str, kind: &str, turn: &str) -> ChatItem {
        ChatItem {
            id: id.into(),
            kind: kind.into(),
            turn: turn.into(),
            created_at: "2026-11-05T10:00:00Z".into(),
            ..Default::default()
        }
    }

    fn tool(id: &str, turn: &str, kind: &str, status: &str) -> ChatItem {
        ChatItem {
            tool: Some(ChatTool {
                kind: kind.into(),
                status: status.into(),
                title: format!("{kind} thing"),
                ..Default::default()
            }),
            ..item(id, "tool", turn)
        }
    }

    fn kinds(rows: &[Keyed]) -> Vec<String> {
        rows.iter()
            .map(|r| match &r.row {
                Row::User(_) => "user".into(),
                Row::Assistant { last, .. } => format!("assistant{}", if *last { "!" } else { "" }),
                Row::Work { items, live } => {
                    format!("work{}{}", items.len(), if *live { "~" } else { "" })
                }
                Row::Fold { open, .. } => format!("fold{}", if *open { "+" } else { "" }),
                Row::Working { .. } => "working".into(),
                Row::Thinking => "thinking".into(),
                Row::Changes(_) => "changes".into(),
                Row::Turn(_) => "turn".into(),
                Row::Note(_) => "note".into(),
                other => format!("{other:?}"),
            })
            .collect()
    }

    #[test]
    fn a_settled_turn_folds_its_work_and_ends_with_its_checkpoint() {
        let mut u = item("u1", "user", "");
        u.result = Some(ChatTurnResult {
            state: "completed".into(),
            ended_at: "2026-11-05T10:01:05Z".into(),
            ..Default::default()
        });
        let items = vec![
            u,
            tool("t1", "u1", "execute", "completed"),
            tool("t2", "u1", "read", "completed"),
            item("a1", "assistant", "u1"),
        ];
        let cps = HashMap::from([(
            "u1".to_string(),
            Checkpoint {
                id: "cp1".into(),
                number: 1,
                ..Default::default()
            },
        )]);
        let shut = rows(&items, &ChatSession::default(), &HashSet::new(), &cps);
        assert_eq!(kinds(&shut), ["user", "fold", "assistant!", "turn"]);
        let Row::Fold { label, .. } = &shut[1].row else {
            panic!()
        };
        assert!(label.contains("1m 5s"), "{label}");
        let open = rows(
            &items,
            &ChatSession::default(),
            &HashSet::from(["u1".to_string()]),
            &cps,
        );
        assert_eq!(
            kinds(&open),
            ["user", "fold+", "work2", "assistant!", "turn"]
        );
    }

    #[test]
    fn a_running_turn_works_live_and_thinks_between_calls() {
        let items = vec![
            item("u1", "user", ""),
            tool("t1", "u1", "execute", "completed"),
            tool("t2", "u1", "edit", "in_progress"),
        ];
        let r = rows(
            &items,
            &ChatSession::default(),
            &HashSet::new(),
            &HashMap::new(),
        );
        assert_eq!(kinds(&r), ["user", "working", "work2~"]);
        let items = vec![
            item("u1", "user", ""),
            tool("t1", "u1", "execute", "completed"),
        ];
        let r = rows(
            &items,
            &ChatSession::default(),
            &HashSet::new(),
            &HashMap::new(),
        );
        assert_eq!(kinds(&r), ["user", "working", "work1~", "thinking"]);
    }

    #[test]
    fn signatures_change_with_what_a_row_shows() {
        let mut items = vec![item("u1", "user", ""), item("a1", "assistant", "u1")];
        let a = rows(
            &items,
            &ChatSession::default(),
            &HashSet::new(),
            &HashMap::new(),
        );
        items[1].text.push_str("more");
        let b = rows(
            &items,
            &ChatSession::default(),
            &HashSet::new(),
            &HashMap::new(),
        );
        assert_eq!(a[0].sig, b[0].sig);
        let sig = |r: &[Keyed]| r.iter().find(|k| k.key == "a1").map(|k| k.sig);
        assert_ne!(sig(&a), sig(&b));
    }

    #[test]
    fn work_is_summed_up_and_diffs_counted() {
        let a = tool("t1", "u", "execute", "completed");
        let b = tool("t2", "u", "read", "completed");
        assert_eq!(
            work_summary(&[&a, &a, &b]),
            "Ran 2 commands and read 1 file"
        );
        let d = ChatDiff {
            old_text: "a\nb\nc\n".into(),
            new_text: "a\nB\nc\nd\n".into(),
            ..Default::default()
        };
        assert_eq!(line_counts(&d), (2, 1));
    }

    #[test]
    fn a_page_call_links_its_page() {
        let mut it = tool("t", "u", "other", "completed");
        if let Some(t) = it.tool.as_mut() {
            t.name = "mcp__hatch__publish_page".into();
            t.output = "Published: https://usehatch.dev/p/O4CQ (private)".into();
        }
        assert_eq!(
            page_url(&it).as_deref(),
            Some("https://usehatch.dev/p/O4CQ")
        );
    }
}
