//! What Files shows of a file that isn't plain text (components/files/):
//! a CSV or TSV as a table (DataGrid.tsx, lib/delimited.ts), a SQLite
//! database a table at a time from the daemon (SqliteView.tsx: …/files/sqlite),
//! and a PDF page by page (PdfView.tsx), drawn by hayro, a PDF renderer in
//! pure Rust, so nothing more needs installing.
use std::sync::Arc;

use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use crate::theme;

/// The most rows a table lays out.
const MAX_ROWS: usize = 2000;

/// parse_delimited splits a CSV or TSV into rows (lib/delimited.ts): quoted
/// fields may hold the separator, newlines and doubled quotes. None when a
/// quote is never closed.
pub fn parse_delimited(text: &str, sep: char) -> Option<Vec<Vec<String>>> {
    let mut rows = Vec::new();
    let mut row = Vec::new();
    let mut field = String::new();
    let mut quoted = false;
    let mut chars = text.chars().peekable();
    while let Some(c) = chars.next() {
        if quoted {
            if c == '"' {
                if chars.peek() == Some(&'"') {
                    field.push('"');
                    chars.next();
                } else {
                    quoted = false;
                }
            } else {
                field.push(c);
            }
            continue;
        }
        match c {
            '"' if field.is_empty() => quoted = true,
            c if c == sep => row.push(std::mem::take(&mut field)),
            '\r' => {}
            '\n' => {
                row.push(std::mem::take(&mut field));
                rows.push(std::mem::take(&mut row));
            }
            c => field.push(c),
        }
    }
    if quoted {
        return None;
    }
    if !field.is_empty() || !row.is_empty() {
        row.push(field);
        rows.push(row);
    }
    Some(rows)
}

/// is_number is whether a cell reads as a number, right-aligned.
fn is_number(s: &str) -> bool {
    let s = s
        .trim()
        .trim_start_matches(['+', '-'])
        .trim_start_matches(['$', '€', '£']);
    let s = s.trim_end_matches('%');
    !s.is_empty()
        && s.chars().next().is_some_and(|c| c.is_ascii_digit())
        && s.chars()
            .all(|c| c.is_ascii_digit() || c == ',' || c == '.')
}

/// grid is a table: its header row, then its rows, each column as wide as
/// it needs up to a point.
pub fn grid(id: &str, header: &[String], rows: &[Vec<String>]) -> AnyElement {
    let cols = header
        .len()
        .max(rows.iter().map(Vec::len).max().unwrap_or(0));
    let width = |c: usize| {
        let longest = std::iter::once(header.get(c).map_or(0, |h| h.chars().count()))
            .chain(
                rows.iter()
                    .take(200)
                    .map(|r| r.get(c).map_or(0, |v| v.chars().count())),
            )
            .max()
            .unwrap_or(0);
        (longest as f32 * 7.5 + 26.).clamp(56., 280.)
    };
    let widths: Vec<f32> = (0..cols).map(width).collect();
    let cell = |text: String, w: f32, head: bool| {
        let num = !head && is_number(&text);
        div()
            .w(px(w))
            .flex_none()
            .px_2()
            .py_1()
            .truncate()
            .border_r_1()
            .border_color(theme::border())
            .when(num, |el| el.text_right().font_family(theme::mono()))
            .when(head, |el| {
                el.font_weight(FontWeight::SEMIBOLD).text_color(theme::fg())
            })
            .child(text)
    };
    div()
        .id(SharedString::from(id.to_string()))
        .size_full()
        .overflow_scroll()
        .child(
            v_flex()
                .flex_none()
                .text_xs()
                .child(
                    h_flex()
                        .bg(theme::raised())
                        .border_b_1()
                        .border_color(theme::border())
                        .children((0..cols).map(|c| {
                            cell(header.get(c).cloned().unwrap_or_default(), widths[c], true)
                        })),
                )
                .children(rows.iter().take(MAX_ROWS).enumerate().map(|(i, r)| {
                    h_flex()
                        .text_color(theme::muted())
                        .when(i % 2 == 1, |el| el.bg(theme::bg().opacity(0.5)))
                        .border_b_1()
                        .border_color(theme::border().opacity(0.5))
                        .children(
                            (0..cols).map(|c| {
                                cell(r.get(c).cloned().unwrap_or_default(), widths[c], false)
                            }),
                        )
                })),
        )
        .into_any_element()
}

/// csv is a delimited file as a table, or None to show its text.
pub fn csv(path: &str, text: &str) -> Option<Result<AnyElement, String>> {
    let lower = path.to_lowercase();
    let sep = if lower.ends_with(".csv") {
        ','
    } else if lower.ends_with(".tsv") {
        '\t'
    } else {
        return None;
    };
    if text.trim().is_empty() {
        return Some(Err(t("files.csv.empty")));
    }
    Some(match parse_delimited(text, sep) {
        None => Err(t("files.csv.notTable")),
        Some(mut rows) if !rows.is_empty() => {
            let header = rows.remove(0);
            Ok(grid("csv-grid", &header, &rows))
        }
        Some(_) => Err(t("files.csv.empty")),
    })
}

/// cell_text is a SQLite value as the grid shows it.
pub fn cell_text(v: &serde_json::Value) -> String {
    match v {
        serde_json::Value::Null => "NULL".into(),
        serde_json::Value::String(s) => s.clone(),
        other => other.to_string(),
    }
}

/// sqlite_range is "1–100 of 240".
pub fn sqlite_range(offset: i64, shown: usize, total: i64) -> String {
    if shown == 0 {
        return t("files.sqlite.noRows");
    }
    let (from, to) = (offset + 1, offset + shown as i64);
    if total >= 0 {
        tv(
            "files.sqlite.range",
            &[
                ("from", from.into()),
                ("to", to.into()),
                ("total", total.into()),
            ],
        )
    } else {
        tv(
            "files.sqlite.rangeOpen",
            &[("from", from.into()), ("to", to.into())],
        )
    }
}

/// A PDF's pages, drawn.
pub struct PdfPages {
    pub pages: Vec<Arc<RenderImage>>,
    pub sizes: Vec<(u32, u32)>,
}

/// render_pdf draws every page (at most 50) at a scale for a sharp column,
/// on a thread of its own: hayro's types stay there.
pub fn render_pdf(bytes: Vec<u8>) -> Result<PdfPages, String> {
    use hayro::hayro_interpret::InterpreterSettings;
    use hayro::hayro_syntax::Pdf;
    use hayro::{PixmapSettings, RenderCache, RenderSettings, render};
    let pdf = Pdf::new(bytes).map_err(|e| format!("{e:?}"))?;
    let cache = RenderCache::new();
    let interp = InterpreterSettings::default();
    let settings = RenderSettings::default();
    let mut out = PdfPages {
        pages: Vec::new(),
        sizes: Vec::new(),
    };
    for page in pdf.pages().iter().take(50) {
        let pixmap = render(
            page,
            &cache,
            &interp,
            &settings,
            &PixmapSettings {
                x_scale: 1.5,
                y_scale: 1.5,
                bg_color: hayro::vello_cpu::color::palette::css::WHITE,
            },
        );
        let (w, h) = (pixmap.width() as u32, pixmap.height() as u32);
        // Opaque on white, so premultiplied is plain RGBA; GPUI wants BGRA.
        let mut px = pixmap.data_as_u8_slice().to_vec();
        for p in px.as_chunks_mut::<4>().0 {
            p.swap(0, 2);
        }
        let img = image::RgbaImage::from_raw(w, h, px).ok_or("page size")?;
        out.pages
            .push(Arc::new(RenderImage::new(vec![image::Frame::new(img)])));
        out.sizes.push((w, h));
    }
    if out.pages.is_empty() {
        return Err("no pages".into());
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::{is_number, parse_delimited};

    #[test]
    fn delimited_text_keeps_quoted_separators() {
        let rows = parse_delimited("a,b\n\"x, y\",\"say \"\"hi\"\"\"\n", ',').unwrap();
        assert_eq!(rows, vec![vec!["a", "b"], vec!["x, y", "say \"hi\""]]);
        assert!(parse_delimited("a,\"open\n", ',').is_none());
        assert!(is_number("$48,160") && is_number("-2.1%") && !is_number("Q1"));
    }
}
