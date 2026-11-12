//! Syntax colours for code outside Markdown — the diff's lines and the file
//! viewer's — the way chat's code blocks get theirs: syntect with two-face's
//! syntaxes (the set gpui-component's `syntect` backend uses), each TextMate
//! scope named as gpui-component names it and coloured by its HighlightTheme.
//! A text is highlighted whole, so a construct spanning lines reads right,
//! then cut into its lines; the answer is kept for the text, as the views
//! redraw often and the text rarely changes.
use std::cell::RefCell;
use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::ops::Range;
use std::rc::Rc;
use std::sync::LazyLock;

use gpui::{AnyElement, HighlightStyle, IntoElement, StyledText};
use syntect::parsing::{ParseState, Scope, ScopeStack, SyntaxSet};

/// One line's coloured spans, by byte within the line.
pub type Runs = Vec<(Range<usize>, HighlightStyle)>;

/// The most texts kept highlighted at once.
const KEPT: usize = 64;
/// Past this, a text is shown plain: highlighting it would stall a frame.
const MAX_BYTES: usize = 256 * 1024;

static SYNTAXES: LazyLock<SyntaxSet> = LazyLock::new(two_face::syntax::extra_newlines);

thread_local! {
    static CACHE: RefCell<HashMap<u64, Rc<Vec<Runs>>>> = RefCell::new(HashMap::new());
}

/// lang_of is the syntax a file's name says (its extension, or a name like
/// Makefile), when syntect knows one.
pub fn lang_of(path: &str) -> Option<&'static str> {
    let name = path.rsplit('/').next().unwrap_or(path);
    let set = &*SYNTAXES;
    let ext = name.rsplit_once('.').map(|(_, e)| e.to_lowercase());
    let found = ext
        .as_deref()
        .and_then(|e| set.find_syntax_by_extension(e))
        .or_else(|| set.find_syntax_by_extension(name))
        .or_else(|| set.find_syntax_by_extension(&name.to_lowercase()))?;
    (found.name != "Plain Text").then_some(found.name.as_str())
}

/// lang_of_fence is the syntax a fenced block's language names (go, rust,
/// ts…), when syntect knows one.
pub fn lang_of_fence(token: &str) -> Option<&'static str> {
    let set = &*SYNTAXES;
    let found = set
        .find_syntax_by_token(token)
        .or_else(|| set.find_syntax_by_extension(&token.to_lowercase()))?;
    (found.name != "Plain Text").then_some(found.name.as_str())
}

/// The palettes Electron's Shiki themes paint with (lib/highlight.ts):
/// github-dark-default, github-light-default and Dracula, by the semantic
/// names below, as 0xRRGGBB.
fn palette(name: &str) -> Option<u32> {
    use crate::theme::Look;
    let look = crate::theme::look();
    let pick = |dark: u32, light: u32, dracula: u32| match look {
        Look::Dark => dark,
        Look::Light => light,
        Look::Dracula => dracula,
    };
    Some(match name {
        "keyword" | "preproc" | "operator" => pick(0xff7b72, 0xcf222e, 0xff79c6),
        "number" | "boolean" | "constant" | "variable.special" | "attribute" | "property"
        | "string.escape" => pick(0x79c0ff, 0x0550ae, 0xbd93f9),
        "string" | "string.regex" | "text.literal" | "link_uri" => {
            pick(0xa5d6ff, 0x0a3069, 0xf1fa8c)
        }
        "comment" | "comment.doc" => pick(0x8b949e, 0x6e7781, 0x6272a4),
        "function" => pick(0xd2a8ff, 0x8250df, 0x50fa7b),
        "type" => pick(0xffa657, 0x953800, 0x8be9fd),
        "variable.parameter" => pick(0xffa657, 0x953800, 0xffb86c),
        "tag" => pick(0x7ee787, 0x116329, 0xff79c6),
        "title" => pick(0x1f6feb, 0x0550ae, 0xbd93f9),
        _ => return None,
    })
}

/// code_fg is the palette's plain text, for what no scope colours.
pub fn code_fg() -> gpui::Hsla {
    use crate::theme::Look;
    gpui::rgb(match crate::theme::look() {
        Look::Dark => 0xe6edf3,
        Look::Light => 0x1f2328,
        Look::Dracula => 0xf8f8f2,
    })
    .into()
}

fn style_of(name: &str) -> Option<HighlightStyle> {
    let colour = palette(name)?;
    Some(HighlightStyle {
        color: Some(gpui::rgb(colour).into()),
        font_style: (name == "comment" || name == "comment.doc").then_some(gpui::FontStyle::Normal),
        ..Default::default()
    })
}

/// line_runs colours the lines of one text in a syntax (lang_of's), each
/// line's spans relative to it.
pub fn line_runs(lang: &'static str, lines: &[String]) -> Rc<Vec<Runs>> {
    let mut h = std::collections::hash_map::DefaultHasher::new();
    lang.hash(&mut h);
    (crate::theme::look() as u8).hash(&mut h);
    lines.hash(&mut h);
    let key = h.finish();
    if let Some(hit) = CACHE.with_borrow(|c| c.get(&key).cloned()) {
        return hit;
    }
    let size: usize = lines.iter().map(|l| l.len() + 1).sum();
    let out = Rc::new(if size > MAX_BYTES {
        vec![Vec::new(); lines.len()]
    } else {
        colour(lang, lines)
    });
    CACHE.with_borrow_mut(|c| {
        if c.len() >= KEPT {
            c.clear();
        }
        c.insert(key, out.clone());
    });
    out
}

/// colour parses the lines in order, a line syntect can't parse keeping the
/// scopes it started with.
fn colour(lang: &str, lines: &[String]) -> Vec<Runs> {
    let set = &*SYNTAXES;
    let Some(syntax) = set.find_syntax_by_name(lang) else {
        return vec![Vec::new(); lines.len()];
    };
    let mut parser = ParseState::new(syntax);
    let mut scopes = ScopeStack::new();
    let mut names = HashMap::<Scope, Option<&'static str>>::new();
    let mut out = Vec::with_capacity(lines.len());
    for line in lines {
        // The line's tokens with the scopes over each, then their colours.
        let mut tokens: Vec<(Range<usize>, ScopeStack)> = Vec::new();
        let with_end = format!("{line}\n");
        if let Ok(ops) = parser.parse_line(&with_end, set) {
            let mut cursor = 0;
            for (at, op) in ops {
                let at = at.min(line.len());
                if at > cursor {
                    tokens.push((cursor..at, scopes.clone()));
                }
                let _ = scopes.apply(&op);
                cursor = at;
            }
            if line.len() > cursor {
                tokens.push((cursor..line.len(), scopes.clone()));
            }
        }
        let innermost = |i: usize| {
            tokens
                .get(i)
                .and_then(|(_, s)| s.scopes.last())
                .map(|s| s.build_string())
                .unwrap_or_default()
        };
        let mut runs: Runs = Vec::new();
        for (i, (range, stack)) in tokens.iter().enumerate() {
            // Shiki's Go grammar takes the package of a qualified type
            // (`http` in `http.Request`) as part of the type; syntect's
            // calls it a variable.
            let qualifier = innermost(i) == "variable.other.go"
                && innermost(i + 1) == "punctuation.accessor.dot.go"
                && innermost(i + 2) == "storage.type.go";
            // The innermost scope that names something wins.
            let style = if qualifier {
                style_of("type")
            } else {
                stack
                    .scopes
                    .iter()
                    .rev()
                    .find_map(|s| *names.entry(*s).or_insert_with(|| semantic_name(*s)))
                    .and_then(style_of)
            };
            if let Some(style) = style {
                match runs.last_mut() {
                    Some((last, s)) if last.end == range.start && *s == style => {
                        last.end = range.end
                    }
                    _ => runs.push((range.clone(), style)),
                }
            }
        }
        out.push(runs);
    }
    out
}

/// semantic_name is the theme's name for a TextMate scope, as gpui-component's
/// syntect backend names it, or None to let an enclosing scope decide.
fn semantic_name(scope: Scope) -> Option<&'static str> {
    let scope = scope.build_string();
    let is = |prefix: &str| {
        scope == prefix
            || scope
                .strip_prefix(prefix)
                .is_some_and(|rest| rest.starts_with('.'))
    };
    Some(
        if is("punctuation.definition.string")
            || is("punctuation.definition.comment")
            || is("invalid")
            || is("meta")
            || is("source")
            || is("text")
        {
            return None;
        } else if is("comment.block.documentation") || is("comment.line.documentation") {
            "comment.doc"
        } else if is("comment") {
            "comment"
        } else if is("constant.character.escape") {
            "string.escape"
        } else if is("string.regexp") {
            "string.regex"
        } else if is("string") {
            "string"
        } else if is("constant.numeric") {
            "number"
        } else if is("constant.language.boolean") {
            "boolean"
        } else if is("constant") || is("support.constant") || is("entity.name.constant") {
            "constant"
        } else if scope == "storage.type.go" {
            // syntect's Go names a named type (`Server`, `http.Request`)
            // storage.type, Shiki's entity.name.type.
            "type"
        } else if is("support.type.builtin.go") {
            // Shiki's Go grammar has `error`, `int` and `string` as
            // storage.type, which the themes paint as keywords.
            "keyword"
        } else if is("keyword.operator") {
            "operator"
        } else if is("keyword.control.import") || is("meta.preprocessor") {
            "preproc"
        } else if is("keyword") || is("storage.modifier") {
            "keyword"
        } else if is("storage.type") {
            // `fn`, `class` and `let` are declarations, `int` and `bool` types.
            let decl = [
                "class",
                "enum",
                "function",
                "impl",
                "interface",
                "module",
                "namespace",
                "struct",
                "trait",
                "type",
                "let",
                "var",
                "const",
            ];
            if decl.iter().any(|w| scope.contains(w)) {
                "keyword"
            } else {
                "type"
            }
        } else if is("entity.name.function")
            || is("support.function")
            || is("variable.function")
            || is("meta.function-call")
        {
            "function"
        } else if is("entity.name.type")
            || is("entity.name.class")
            || is("entity.name.struct")
            || is("entity.name.enum")
            || is("entity.name.trait")
            || is("entity.name.interface")
            || is("entity.other.inherited-class")
            || is("support.type")
            || is("support.class")
        {
            "type"
        } else if is("entity.name.tag") {
            "tag"
        } else if is("entity.other.attribute-name") {
            "attribute"
        } else if is("entity.name.section") || is("markup.heading") {
            "title"
        } else if is("variable.language") {
            "variable.special"
        } else if is("variable.other.member")
            || is("variable.other.property")
            || is("support.variable.property")
            || is("meta.mapping.key")
        {
            "property"
        } else if is("variable.parameter") {
            "variable.parameter"
        } else if is("variable") {
            "variable"
        } else if is("markup.bold") {
            "emphasis.strong"
        } else if is("markup.italic") {
            "emphasis"
        } else if is("markup.underline.link") {
            "link_uri"
        } else if is("markup.raw") {
            "text.literal"
        } else if is("punctuation.section") || is("punctuation.definition.block") {
            "punctuation.bracket"
        } else if is("punctuation") {
            "punctuation"
        } else {
            return None;
        },
    )
}

/// shifted is a code line drawn `dx` right and `dy` down of its box: Chromium
/// sets text at fractional positions (a pre's 0.9rem of padding is 14.4px)
/// where GPUI's layout takes whole pixels, and a glyph half a pixel over
/// antialiases differently. `height` is the box's; the line is painted with
/// `line_height`.
pub fn shifted(
    text: String,
    runs: Option<&Runs>,
    dx: gpui::Pixels,
    dy: gpui::Pixels,
    height: gpui::Pixels,
    line_height: gpui::Pixels,
) -> AnyElement {
    use gpui::{Styled as _, point, px};
    let runs = runs.cloned().unwrap_or_default();
    let text: gpui::SharedString = text.into();
    gpui::canvas(
        |_, _, _| {},
        move |bounds, _, window, cx| {
            let style = window.text_style();
            let size = style.font_size.to_pixels(window.rem_size());
            let mut out = Vec::new();
            let mut at = 0;
            for (range, h) in &runs {
                let range = range.start.min(text.len())..range.end.min(text.len());
                if range.start > at {
                    out.push(style.to_run(range.start - at));
                }
                if range.end > range.start {
                    out.push(style.clone().highlight(*h).to_run(range.end - range.start));
                }
                at = at.max(range.end);
            }
            if text.len() > at {
                out.push(style.to_run(text.len() - at));
            }
            let line = window
                .text_system()
                .shape_line(text.clone(), size, &out, None);
            let _ = line.paint(
                bounds.origin + point(dx, dy),
                line_height,
                gpui::TextAlign::Left,
                None,
                window,
                cx,
            );
        },
    )
    .w_full()
    .h(height)
    .min_w(px(0.))
    .into_any_element()
}

/// styled is a line's text with its colours.
pub fn styled(text: String, runs: Option<&Runs>) -> AnyElement {
    match runs {
        Some(r) if !r.is_empty() => StyledText::new(text)
            .with_highlights(r.iter().cloned())
            .into_any_element(),
        _ => text.into_any_element(),
    }
}

#[cfg(test)]
mod tests {
    use super::{lang_of, line_runs};

    #[test]
    fn languages_come_from_the_file_name() {
        assert_eq!(lang_of("native/src/main.rs"), Some("Rust"));
        assert!(lang_of("desktop/App.tsx").is_some());
        assert_eq!(lang_of("a/b.YML"), Some("YAML"));
        assert!(lang_of("internal/x.go").is_some());
        assert_eq!(lang_of("image.png"), None);
    }

    #[test]
    fn rust_gets_colours_on_every_line() {
        let lines = vec![
            "fn main() {".to_string(),
            "    let x = \"hi\";".to_string(),
            "}".to_string(),
        ];
        let runs = line_runs("Rust", &lines);
        assert_eq!(runs.len(), 3);
        assert!(
            runs[0].iter().any(|(r, _)| *r == (0..2)),
            "fn: {:?}",
            runs[0]
        );
        assert!(!runs[1].is_empty());
    }
}
