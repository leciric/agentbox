//! Compiles the desktop app's catalogs (desktop/src/shared/i18n/*.ts) into
//! the native app, so both apps speak from the same catalogs and translators
//! keep one copy. Each is a TypeScript object of 'key': 'message' lines.
use std::fmt::Write as _;
use std::path::PathBuf;

const CATALOGS: &[(&str, &str)] = &[("EN_US", "en-US.ts"), ("PT_BR", "pt-BR.ts")];

fn main() {
    let dir = PathBuf::from(std::env::var("CARGO_MANIFEST_DIR").unwrap())
        .join("../../../desktop/src/shared/i18n");
    let mut out = String::new();
    for (name, file) in CATALOGS {
        let path = dir.join(file);
        println!("cargo:rerun-if-changed={}", path.display());
        let src =
            std::fs::read_to_string(&path).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
        let mut entries = parse(&src).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
        entries.sort();
        entries.dedup_by(|a, b| a.0 == b.0);
        writeln!(out, "pub(crate) static {name}: &[(&str, &str)] = &[").unwrap();
        for (k, v) in entries {
            writeln!(out, "    ({k:?}, {v:?}),").unwrap();
        }
        writeln!(out, "];").unwrap();
    }
    std::fs::write(
        PathBuf::from(std::env::var("OUT_DIR").unwrap()).join("catalogs.rs"),
        out,
    )
    .unwrap();
}

/// parse reads every `'key': 'message',` (or "message") line of a catalog.
fn parse(src: &str) -> Result<Vec<(String, String)>, String> {
    let mut out = Vec::new();
    for (n, line) in src.lines().enumerate() {
        let t = line.trim_start();
        if !t.starts_with('\'') {
            continue;
        }
        let (key, rest) = string(t).ok_or(format!("line {}: a key", n + 1))?;
        let rest = rest
            .trim_start()
            .strip_prefix(':')
            .ok_or(format!("line {}: no ':'", n + 1))?
            .trim_start();
        let (value, _) =
            string(rest).ok_or(format!("line {}: a message on the key's line", n + 1))?;
        out.push((key, value));
    }
    if out.is_empty() {
        return Err("no messages".into());
    }
    Ok(out)
}

/// string reads a quoted JavaScript string at the start of s.
fn string(s: &str) -> Option<(String, &str)> {
    let mut chars = s.char_indices();
    let (_, quote) = chars.next().filter(|(_, c)| *c == '\'' || *c == '"')?;
    let mut out = String::new();
    while let Some((i, c)) = chars.next() {
        match c {
            '\\' => match chars.next()?.1 {
                'n' => out.push('\n'),
                't' => out.push('\t'),
                c => out.push(c),
            },
            c if c == quote => return Some((out, &s[i + 1..])),
            c => out.push(c),
        }
    }
    None
}
