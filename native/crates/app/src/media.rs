//! An agent's proof for the review (desktop/src/renderer/lib/review.ts and
//! shared/pairs.ts): its media sorted into before/after pairs, recordings in
//! chapters, and screenshots, and each file fetched from the daemon
//! (GET /v1/media/{id}/file) into a cache to show from disk.
use std::path::PathBuf;

use agentbox_api::{Client, MediaItem};
use anyhow::Result;

/// cache_dir is where fetched media is kept: the XDG cache (LOCALAPPDATA on
/// Windows, ~/Library/Caches on a Mac).
pub fn cache_dir() -> PathBuf {
    let home = || {
        std::env::var_os("HOME")
            .map(PathBuf::from)
            .unwrap_or_default()
    };
    let base = if cfg!(target_os = "windows") {
        std::env::var_os("LOCALAPPDATA")
            .map(PathBuf::from)
            .unwrap_or_else(std::env::temp_dir)
    } else if cfg!(target_os = "macos") {
        home().join("Library/Caches")
    } else {
        std::env::var_os("XDG_CACHE_HOME")
            .map(PathBuf::from)
            .filter(|p| p.is_absolute())
            .unwrap_or_else(|| home().join(".cache"))
    };
    base.join("agentbox-native").join("media")
}

/// cached is where an item's file goes: its id (the daemon's, unique and
/// never reused for other bytes) and its file's extension.
fn cached(item: &MediaItem) -> PathBuf {
    let id: String = item
        .id
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '-' || c == '_' {
                c
            } else {
                '_'
            }
        })
        .collect();
    let ext = item
        .file
        .rsplit_once('.')
        .map(|(_, e)| e.to_string())
        .or_else(|| item.mime.split('/').nth(1).map(str::to_string))
        .unwrap_or_else(|| "bin".into());
    cache_dir().join(format!("{id}.{ext}"))
}

/// fetch is an item's file on disk, read from the daemon the first time.
pub async fn fetch(client: Client, item: MediaItem) -> Result<PathBuf> {
    let path = cached(&item);
    if path.exists() {
        return Ok(path);
    }
    let bytes = client
        .raw(
            agentbox_api::client::Method::GET,
            &format!("/v1/media/{}/file", item.id),
            None,
        )
        .await?;
    std::fs::create_dir_all(cache_dir())?;
    // Written whole, then moved into place: a half-written file is never read.
    let part = path.with_extension("part");
    std::fs::write(&part, &bytes)?;
    std::fs::rename(&part, &path)?;
    Ok(path)
}

/// Look is how an item is shown, by its kind and file, as the Electron
/// app's viewer does (MediaParts.tsx's ViewerBody and Thumbnail).
#[derive(Clone, Debug, PartialEq)]
pub enum Look {
    /// A picture: a screenshot, or a file that is an image.
    Picture,
    /// A recording, in the player.
    Video,
    /// A report that is a folder: its entry page (an HTML report), framed.
    Page(String),
    /// A note: its text, which the item carries.
    Note,
    /// Anything else, read as text: a log, a one-file report, a file.
    Text,
}

/// The picture formats the app draws (GPUI's): what a file must be to be
/// shown as a picture rather than read as text.
const PICTURES: [&str; 7] = ["png", "jpg", "jpeg", "gif", "webp", "bmp", "svg"];

/// is_picture says a file is an image the app can draw, by its MIME type or
/// else its extension.
pub fn is_picture(m: &MediaItem) -> bool {
    let ext = m
        .file
        .rsplit_once('.')
        .map(|(_, e)| e.to_ascii_lowercase())
        .unwrap_or_default();
    match m.mime.split(';').next().unwrap_or("").trim() {
        "" => PICTURES.contains(&ext.as_str()),
        mime => mime
            .strip_prefix("image/")
            .is_some_and(|t| PICTURES.contains(&t) || t == "svg+xml"),
    }
}

/// look is how an item is shown.
pub fn look(m: &MediaItem) -> Look {
    match m.kind.as_str() {
        "screenshot" => Look::Picture,
        "recording" => Look::Video,
        "note" => Look::Note,
        "report" if !m.meta.entry.is_empty() => Look::Page(m.meta.entry.clone()),
        _ if !m.file.is_empty() && is_picture(m) => Look::Picture,
        _ => Look::Text,
    }
}

/// page_path is where a folder report's page is read from by a webview:
/// agentbox://localhost/media/<id>/<path>, which webview.rs serves from
/// GET /v1/media/<id>/file?path=<path>, so the page's relative links (its
/// stylesheets, scripts and pictures) are read from the same folder, as
/// the Electron app's agentbox-media://media/<id>/<path> protocol does.
pub fn page_path(m: &MediaItem, entry: &str) -> String {
    let enc = |s: &str| -> String {
        s.bytes()
            .map(|b| match b {
                b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                    (b as char).to_string()
                }
                _ => format!("%{b:02X}"),
            })
            .collect()
    };
    let path: Vec<String> = entry.split('/').map(enc).collect();
    format!("/media/{}/{}", enc(&m.id), path.join("/"))
}

/// media_route turns a page's /media/<id>/<path> into the daemon's path for
/// that file of the item; any other path is the daemon's own.
pub fn media_route(path: &str) -> String {
    let Some(rest) = path.strip_prefix("/media/") else {
        return path.to_string();
    };
    let rest = rest.split(['?', '#']).next().unwrap_or(rest);
    match rest.split_once('/') {
        Some((id, sub)) if !sub.is_empty() => {
            // The path is sent on as a query value: decoded, then encoded
            // again as one.
            let sub = percent_decode(sub);
            let q: String = sub
                .bytes()
                .map(|b| match b {
                    b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' | b'/' => {
                        (b as char).to_string()
                    }
                    _ => format!("%{b:02X}"),
                })
                .collect();
            format!("/v1/media/{id}/file?path={q}")
        }
        _ => format!("/v1/media/{}/file", rest.trim_end_matches('/')),
    }
}

fn percent_decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        if b[i] == b'%'
            && i + 2 < b.len()
            && b[i + 1].is_ascii_hexdigit()
            && b[i + 2].is_ascii_hexdigit()
            && let Ok(v) = u8::from_str_radix(&s[i + 1..i + 3], 16)
        {
            out.push(v);
            i += 3;
            continue;
        }
        out.push(b[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// ms is an RFC 3339 time in milliseconds since the epoch.
pub fn ms(time: &str) -> i64 {
    chrono::DateTime::parse_from_rfc3339(time).map_or(0, |t| t.timestamp_millis())
}

/// A pair is the before and the after of one thing.
#[derive(Clone)]
pub struct Pair {
    pub name: String,
    pub before: MediaItem,
    pub after: MediaItem,
}

/// A chapter is something an agent checked while it recorded, at seconds
/// into the recording: a note it saved, or a screenshot it took.
#[derive(Clone)]
pub struct Chapter {
    pub at: f64,
    pub title: String,
    pub detail: Option<String>,
}

/// A take is one recording and its chapters.
#[derive(Clone)]
pub struct Take {
    pub media: MediaItem,
    pub chapters: Vec<Chapter>,
}

/// One piece of proof the review steps through. A gallery is a handful of
/// these, so the pair's size doesn't matter.
#[derive(Clone)]
#[allow(clippy::large_enum_variant)]
pub enum Shown {
    Pair(Pair),
    Recording(Take),
    Screenshot(MediaItem),
    /// A Hatch page the agent published (lib/pages.ts pagesFor).
    Page(agentbox_api::Artifact),
}

impl Shown {
    pub fn name(&self) -> String {
        match self {
            Shown::Pair(p) => readable(&p.name),
            Shown::Recording(t) => readable(&t.media.name),
            Shown::Screenshot(m) => readable(&m.name),
            Shown::Page(p) => p.title.clone(),
        }
    }

    /// thumb is the picture its card shows: a screenshot, a pair's after.
    pub fn thumb(&self) -> Option<&MediaItem> {
        match self {
            Shown::Pair(p) if p.after.kind == "screenshot" => Some(&p.after),
            Shown::Screenshot(m) => Some(m),
            _ => None,
        }
    }
}

/// live_pages is a project's pages Hatch still has that an agent made or
/// changed (lib/pages.ts pagesFor, isExpired), none without a connector.
pub fn live_pages(list: &agentbox_api::Artifacts, agent_ref: &str) -> Vec<agentbox_api::Artifact> {
    if list.connector.is_empty() {
        return Vec::new();
    }
    let now = chrono::Utc::now().timestamp_millis();
    list.artifacts
        .iter()
        .filter(|a| {
            !a.expired
                && (a.permanent || a.expires_at.as_deref().is_none_or(|e| ms(e) > now))
                && a.agents.iter().any(|r| r == agent_ref)
        })
        .cloned()
        .collect()
}

/// readable turns a media name like "settings-at-1024" into words.
pub fn readable(name: &str) -> String {
    let stem = name.rsplit_once('.').map_or(name, |(s, e)| {
        if ["png", "jpg", "jpeg", "webp", "mp4", "webm"].contains(&e.to_lowercase().as_str()) {
            s
        } else {
            name
        }
    });
    let words = stem.replace(['-', '_'], " ");
    let mut c = words.chars();
    match c.next() {
        Some(f) => f.to_uppercase().chain(c).collect(),
        None => String::new(),
    }
}

/// side_of is which half of which pair an item is: its meta first, then its
/// name ("settings-before", "after_settings").
fn side_of(m: &MediaItem) -> Option<(String, bool)> {
    let side = m.meta.side.to_lowercase();
    if !m.meta.pair.is_empty() && (side == "before" || side == "after") {
        return Some((m.meta.pair.clone(), side == "after"));
    }
    let name = readable_stem(m.name.trim());
    let lower = name.to_lowercase();
    for (side, after) in [("before", false), ("after", true)] {
        for sep in ['-', '_', ' '] {
            if let Some(rest) = lower.strip_prefix(&format!("{side}{sep}")) {
                return Some((name[name.len() - rest.len()..].to_string(), after));
            }
            if let Some(rest) = lower.strip_suffix(&format!("{sep}{side}")) {
                return Some((name[..rest.len()].to_string(), after));
            }
        }
    }
    None
}

fn readable_stem(name: &str) -> &str {
    for ext in [".png", ".jpg", ".jpeg", ".webp", ".mp4", ".webm"] {
        if name.to_lowercase().ends_with(ext) {
            return &name[..name.len() - ext.len()];
        }
    }
    name
}

/// gallery is every piece of proof in the order the review shows it: the
/// pairs in the order they were taken, the other recordings latest first,
/// then the screenshots oldest first (lib/review.ts proofOf and gallery).
pub fn gallery(media: &[MediaItem]) -> Vec<Shown> {
    let mut by_time: Vec<&MediaItem> = media.iter().collect();
    by_time.sort_by_key(|m| ms(&m.created_at));
    let notes: Vec<&MediaItem> = by_time
        .iter()
        .copied()
        .filter(|m| m.kind == "note")
        .collect();
    let shots: Vec<&MediaItem> = by_time
        .iter()
        .copied()
        .filter(|m| m.kind == "screenshot")
        .collect();
    // Pairs: a half's latest take; a half without its other half is the rest.
    let mut groups: Vec<(String, String, Option<&MediaItem>, Option<&MediaItem>)> = Vec::new();
    for m in by_time
        .iter()
        .copied()
        .filter(|m| m.kind == "screenshot" || m.kind == "recording")
    {
        let Some((pair, after)) = side_of(m) else {
            continue;
        };
        let key = format!(
            "{}:{}",
            m.kind,
            pair.trim().to_lowercase().replace(['_', ' '], "-")
        );
        let ix = match groups.iter().position(|g| g.0 == key) {
            Some(ix) => ix,
            None => {
                groups.push((key, pair, None, None));
                groups.len() - 1
            }
        };
        let slot = if after {
            &mut groups[ix].3
        } else {
            &mut groups[ix].2
        };
        if slot.is_none_or(|had| ms(&m.created_at) >= ms(&had.created_at)) {
            *slot = Some(m);
        }
    }
    let mut pairs: Vec<Pair> = groups
        .into_iter()
        .filter_map(|(_, name, b, a)| {
            Some(Pair {
                name,
                before: b?.clone(),
                after: a?.clone(),
            })
        })
        .collect();
    pairs.sort_by_key(|p| ms(&p.before.created_at).min(ms(&p.after.created_at)));
    let paired: Vec<&str> = pairs
        .iter()
        .flat_map(|p| [p.before.id.as_str(), p.after.id.as_str()])
        .collect();
    let rest: Vec<&MediaItem> = by_time
        .iter()
        .copied()
        .filter(|m| {
            (m.kind == "screenshot" || m.kind == "recording") && !paired.contains(&m.id.as_str())
        })
        .collect();
    let mut out: Vec<Shown> = pairs.into_iter().map(Shown::Pair).collect();
    out.extend(
        rest.iter()
            .rev()
            .filter(|m| m.kind == "recording")
            .map(|r| {
                Shown::Recording(Take {
                    media: (*r).clone(),
                    chapters: chapters(r, &notes, &shots),
                })
            }),
    );
    out.extend(
        rest.iter()
            .filter(|m| m.kind == "screenshot")
            .map(|m| Shown::Screenshot((*m).clone())),
    );
    out
}

/// chapters are what a recording checked: the notes saved while it
/// recorded, or with none the screenshots taken, at seconds into its video
/// (a trimmed recording's cuts moving them earlier: lib/recording.ts videoAt).
fn chapters(rec: &MediaItem, notes: &[&MediaItem], shots: &[&MediaItem]) -> Vec<Chapter> {
    let start = ms(&rec.created_at);
    let recorded = if rec.meta.recorded > 0. {
        rec.meta.recorded
    } else {
        rec.meta.duration
    };
    let end = start + (recorded * 1000.) as i64;
    let during = |m: &&&MediaItem| (start..=end).contains(&ms(&m.created_at));
    let at = |m: &MediaItem| {
        let wall = (ms(&m.created_at) - start) as f64 / 1000.;
        let mut v = wall;
        for c in &rec.meta.cuts {
            if wall >= c.to {
                v -= c.to - c.from;
            } else if wall > c.from {
                v -= wall - c.from;
            }
        }
        v
    };
    let marks: Vec<&&MediaItem> = notes.iter().filter(during).collect();
    if !marks.is_empty() {
        return marks
            .into_iter()
            .map(|n| {
                let text = n.text.trim();
                let detail = if text.is_empty() || text == n.name {
                    None
                } else {
                    Some(
                        text.strip_prefix(n.name.as_str())
                            .unwrap_or(text)
                            .trim()
                            .to_string(),
                    )
                    .filter(|d| !d.is_empty())
                };
                Chapter {
                    at: at(n),
                    title: n.name.clone(),
                    detail,
                }
            })
            .collect();
    }
    shots
        .iter()
        .filter(during)
        .map(|s| Chapter {
            at: at(s),
            title: readable(&s.name),
            detail: None,
        })
        .collect()
}

#[cfg(test)]
mod tests {
    #[test]
    fn each_kind_is_shown_as_the_electron_viewer_shows_it() {
        let mut m = item("m1", "log", "build", "2026-11-06T10:00:00Z");
        m.file = "build.log".into();
        assert_eq!(look(&m), Look::Text);
        m.kind = "file".into();
        m.file = "diagram.png".into();
        m.mime = "image/png".into();
        assert_eq!(look(&m), Look::Picture);
        m.mime = String::new();
        m.file = "photo.JPG".into();
        assert_eq!(look(&m), Look::Picture);
        m.file = "notes.txt".into();
        m.mime = "text/plain".into();
        assert_eq!(look(&m), Look::Text);
        m.kind = "report".into();
        m.file = "playwright-report".into();
        m.meta.entry = "index.html".into();
        assert_eq!(look(&m), Look::Page("index.html".into()));
        m.kind = "note".into();
        assert_eq!(look(&m), Look::Note);
    }

    #[test]
    fn a_report_page_reads_its_folder_through_the_file_route() {
        let m = item("m 1", "report", "r", "2026-11-06T10:00:00Z");
        assert_eq!(
            page_path(&m, "data/index.html"),
            "/media/m%201/data/index.html"
        );
        assert_eq!(
            media_route("/media/m%201/data/index.html"),
            "/v1/media/m%201/file?path=data/index.html"
        );
        assert_eq!(
            media_route("/media/abc/a%20b.css?v=1"),
            "/v1/media/abc/file?path=a%20b.css"
        );
        assert_eq!(media_route("/media/abc"), "/v1/media/abc/file");
        assert_eq!(
            media_route("/v1/projects/p/artifacts/x/page"),
            "/v1/projects/p/artifacts/x/page"
        );
    }

    use super::*;
    use agentbox_api::MediaMeta;

    fn item(id: &str, kind: &str, name: &str, at: &str) -> MediaItem {
        MediaItem {
            id: id.into(),
            kind: kind.into(),
            name: name.into(),
            created_at: format!("2026-11-05T08:00:{at}Z"),
            ..Default::default()
        }
    }

    #[test]
    fn sorts_proof_into_pairs_recordings_and_screenshots() {
        let mut rec = item("r", "recording", "walk", "10");
        rec.meta = MediaMeta {
            duration: 20.,
            ..Default::default()
        };
        let media = vec![
            item("b", "screenshot", "settings-before", "01"),
            rec,
            item("n", "note", "Opens General", "15"),
            item("a", "screenshot", "after_settings", "40"),
            item("s", "screenshot", "home", "45"),
        ];
        let g = gallery(&media);
        assert_eq!(g.len(), 3);
        assert!(matches!(&g[0], Shown::Pair(p) if p.before.id == "b" && p.after.id == "a"));
        match &g[1] {
            Shown::Recording(t) => {
                assert_eq!(t.chapters.len(), 1);
                assert_eq!(t.chapters[0].at, 5.);
            }
            _ => panic!("a recording second"),
        }
        assert!(matches!(&g[2], Shown::Screenshot(m) if m.id == "s"));
        assert_eq!(readable("settings-at-1024.png"), "Settings at 1024");
    }
}
