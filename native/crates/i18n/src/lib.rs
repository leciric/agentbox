//! The app's text in the user's language, from the desktop app's catalogs
//! (desktop/src/shared/i18n, compiled in by build.rs): en-US has every key,
//! another language lacking one shows en-US's. Messages use the catalogs'
//! ICU subset (format.ts): {name}, {n, plural, …} with #, {v, select, …},
//! and <tag>…</tag>, whose text is kept.
use std::sync::atomic::{AtomicUsize, Ordering};

mod catalogs {
    include!(concat!(env!("OUT_DIR"), "/catalogs.rs"));
}

/// The languages the app speaks, as desktop/src/shared/i18n/languages.ts lists them.
pub const LANGUAGES: &[(&str, &str)] =
    &[("en-US", "English (US)"), ("pt-BR", "Português (Brasil)")];

fn catalog(i: usize) -> &'static [(&'static str, &'static str)] {
    match i {
        1 => catalogs::PT_BR,
        _ => catalogs::EN_US,
    }
}

static CURRENT: AtomicUsize = AtomicUsize::new(0);

/// set_language switches to a BCP 47 tag's language: the tag itself, else one
/// with its primary language (pt-PT gets pt-BR), else en-US.
pub fn set_language(tag: &str) {
    let lower = tag.to_lowercase();
    let primary = lower.split('-').next().unwrap_or("");
    let i = LANGUAGES
        .iter()
        .position(|(t, _)| t.to_lowercase() == lower)
        .or_else(|| {
            LANGUAGES
                .iter()
                .position(|(t, _)| t.split('-').next().unwrap_or("").to_lowercase() == primary)
        })
        .unwrap_or(0);
    CURRENT.store(i, Ordering::Relaxed);
}

pub fn language() -> &'static str {
    LANGUAGES[CURRENT.load(Ordering::Relaxed)].0
}

fn lookup(i: usize, key: &str) -> Option<&'static str> {
    let c = catalog(i);
    c.binary_search_by(|(k, _)| (*k).cmp(key))
        .ok()
        .map(|ix| c[ix].1)
}

/// message is a key's message, unformatted: the language's, else en-US's,
/// else the key itself, so a missing one shows on screen.
pub fn message(key: &str) -> &str {
    lookup(CURRENT.load(Ordering::Relaxed), key)
        .or_else(|| lookup(0, key))
        .unwrap_or(key)
}

/// has says whether en-US has a key.
pub fn has(key: &str) -> bool {
    lookup(0, key).is_some()
}

/// A value for a placeholder.
pub enum Value<'a> {
    Str(&'a str),
    Num(f64),
}

impl<'a> From<&'a str> for Value<'a> {
    fn from(s: &'a str) -> Self {
        Value::Str(s)
    }
}
impl<'a> From<&'a String> for Value<'a> {
    fn from(s: &'a String) -> Self {
        Value::Str(s)
    }
}
impl From<usize> for Value<'_> {
    fn from(n: usize) -> Self {
        Value::Num(n as f64)
    }
}
impl From<i64> for Value<'_> {
    fn from(n: i64) -> Self {
        Value::Num(n as f64)
    }
}

/// t is a key's message with no values.
pub fn t(key: &str) -> String {
    tv(key, &[])
}

/// tv is a key's message with its placeholders filled.
pub fn tv(key: &str, values: &[(&str, Value)]) -> String {
    format(message(key), values, language())
}

/// plural_category is the CLDR plural category of n in a language: en has one
/// for exactly 1, pt for 0 and 1 (whole numbers).
fn plural_category(lang: &str, n: f64) -> &'static str {
    let whole = n.fract() == 0.0;
    match lang.split('-').next() {
        Some("pt") if whole && (n == 0.0 || n == 1.0) => "one",
        Some("pt") => "other",
        _ if whole && n == 1.0 => "one",
        _ => "other",
    }
}

fn number(n: f64) -> String {
    if n.fract() == 0.0 {
        format!("{}", n as i64)
    } else {
        format!("{n}")
    }
}

/// format fills a message in.
pub fn format(msg: &str, values: &[(&str, Value)], lang: &str) -> String {
    let mut out = String::new();
    fmt_into(msg, values, lang, None, &mut out);
    out
}

fn fmt_into(msg: &str, values: &[(&str, Value)], lang: &str, hash: Option<f64>, out: &mut String) {
    let b = msg.as_bytes();
    let mut i = 0;
    while i < b.len() {
        match b[i] {
            b'{' => {
                let Some(end) = matching(msg, i) else {
                    out.push_str(&msg[i..]);
                    return;
                };
                arg(&msg[i + 1..end], values, lang, out);
                i = end + 1;
            }
            b'#' if hash.is_some() => {
                out.push_str(&number(hash.unwrap()));
                i += 1;
            }
            b'<' => {
                // <tag> and </tag>: their text stays, the tags go.
                match msg[i..].find('>') {
                    Some(j)
                        if msg[i + 1..i + j]
                            .chars()
                            .all(|c| c.is_alphanumeric() || c == '/' || c == '-')
                            && j > 1 =>
                    {
                        i += j + 1
                    }
                    _ => {
                        out.push('<');
                        i += 1;
                    }
                }
            }
            _ => {
                let c = msg[i..].chars().next().unwrap();
                out.push(c);
                i += c.len_utf8();
            }
        }
    }
}

/// matching is the index of the } that closes the { at open.
fn matching(s: &str, open: usize) -> Option<usize> {
    let mut depth = 0;
    for (i, c) in s[open..].char_indices() {
        match c {
            '{' => depth += 1,
            '}' => {
                depth -= 1;
                if depth == 0 {
                    return Some(open + i);
                }
            }
            _ => {}
        }
    }
    None
}

fn arg(body: &str, values: &[(&str, Value)], lang: &str, out: &mut String) {
    let mut parts = body.splitn(3, ',');
    let name = parts.next().unwrap_or("").trim();
    let kind = parts.next().map(str::trim);
    let value = values.iter().find(|(k, _)| *k == name).map(|(_, v)| v);
    let Some(value) = value else {
        // A placeholder with no value is left as it is.
        out.push('{');
        out.push_str(body);
        out.push('}');
        return;
    };
    match (kind, parts.next()) {
        (Some("plural"), Some(branches)) => {
            let n = match value {
                Value::Num(n) => *n,
                Value::Str(s) => s.parse().unwrap_or(0.0),
            };
            let exact = format!("={}", number(n));
            let pick = branch(branches, &exact)
                .or_else(|| branch(branches, plural_category(lang, n)))
                .or_else(|| branch(branches, "other"));
            if let Some(b) = pick {
                fmt_into(b, values, lang, Some(n), out);
            }
        }
        (Some("select"), Some(branches)) => {
            let v = match value {
                Value::Str(s) => s.to_string(),
                Value::Num(n) => number(*n),
            };
            if let Some(b) = branch(branches, &v).or_else(|| branch(branches, "other")) {
                fmt_into(b, values, lang, None, out);
            }
        }
        _ => match value {
            Value::Str(s) => out.push_str(s),
            Value::Num(n) => out.push_str(&number(*n)),
        },
    }
}

/// branch finds `selector {text}` among a plural's or select's branches.
fn branch<'a>(branches: &'a str, selector: &str) -> Option<&'a str> {
    let mut rest = branches;
    loop {
        rest = rest.trim_start();
        let open = rest.find('{')?;
        let sel = rest[..open].trim();
        let close = matching(rest, open)?;
        if sel == selector {
            return Some(&rest[open + 1..close]);
        }
        rest = &rest[close + 1..];
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reads_both_catalogs() {
        assert!(catalogs::EN_US.len() > 3000);
        assert!(catalogs::PT_BR.len() > 3000);
        assert_eq!(lookup(0, "common.cancel"), Some("Cancel"));
        assert!(lookup(1, "common.cancel").is_some_and(|m| m != "Cancel"));
    }

    #[test]
    fn formats_the_icu_subset() {
        let m = "{count, plural, =0 {none} one {# agent} other {# agents}} in <b>{name}</b>";
        assert_eq!(
            format(
                m,
                &[("count", 0usize.into()), ("name", "demo".into())],
                "en-US"
            ),
            "none in demo"
        );
        assert_eq!(
            format(
                m,
                &[("count", 1usize.into()), ("name", "demo".into())],
                "en-US"
            ),
            "1 agent in demo"
        );
        assert_eq!(
            format(
                m,
                &[("count", 3usize.into()), ("name", "demo".into())],
                "en-US"
            ),
            "3 agents in demo"
        );
        let s = "{state, select, paused {Resume} other {Start}} {missing}";
        assert_eq!(
            format(s, &[("state", "paused".into())], "en-US"),
            "Resume {missing}"
        );
        // Portuguese counts 0 as one.
        assert_eq!(
            format(
                "{n, plural, one {# dia} other {# dias}}",
                &[("n", 0usize.into())],
                "pt-BR"
            ),
            "0 dia"
        );
    }

    #[test]
    fn falls_back_to_en_us_and_then_the_key() {
        set_language("pt-PT");
        assert_eq!(language(), "pt-BR");
        assert_eq!(message("no.such.key"), "no.such.key");
        set_language("en-US");
    }
}
