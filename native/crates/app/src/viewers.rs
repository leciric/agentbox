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
            .border_color(theme::line())
            .when(num, |el| el.text_right().font_family(theme::mono()))
            .when(head, |el| {
                el.font_weight(FontWeight::SEMIBOLD)
                    .text_color(theme::primary())
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
                        .bg(theme::surface_raised())
                        .border_b_1()
                        .border_color(theme::line())
                        .children((0..cols).map(|c| {
                            cell(header.get(c).cloned().unwrap_or_default(), widths[c], true)
                        })),
                )
                .children(rows.iter().take(MAX_ROWS).enumerate().map(|(i, r)| {
                    h_flex()
                        .text_color(theme::muted())
                        .when(i % 2 == 1, |el| el.bg(theme::ink().opacity(0.5)))
                        .border_b_1()
                        .border_color(theme::line().opacity(0.5))
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

/// page_stage is a Hatch page on a stage, framed as the Electron app's
/// PageFrame frames it: its HTML from the daemon (…/artifacts/{id}/page,
/// with the page's own Content-Security-Policy) in a webview; the stage's
/// bar has Open in browser. Where a child webview can't be made (Wayland)
/// it is the page pictured offscreen, and without GTK the page's card. `shown` is false while something is drawn over it.
pub fn page_stage(project: &str, page: &agentbox_api::Artifact, shown: bool) -> AnyElement {
    let url = page.url.clone();
    let open = (!url.is_empty()).then(|| {
        crate::app::controls::button(
            SharedString::from(format!("page-open-{}", page.id)),
            t("review.openPage"),
            crate::app::controls::Btn::Secondary,
            false,
            move |_, _, cx| cx.open_url(&url),
        )
    });
    let path = format!(
        "/v1/projects/{}/artifacts/{}/page",
        crate::files::encode(project).replace('/', "%2F"),
        crate::files::encode(&page.id).replace('/', "%2F"),
    );
    // No child webviews here (Wayland): the page as WebKit pictures it
    // offscreen, a still.
    #[cfg(target_os = "linux")]
    if !crate::webview::available()
        && crate::webview::gtk_ready()
        && let Some(client) = crate::webview::client()
    {
        return div()
            .id(SharedString::from(format!("page-still-{}", page.id)))
            .size_full()
            .rounded_lg()
            .overflow_y_scroll()
            .bg(theme::paper())
            .child(crate::snapshot::page_snapshot(
                format!("hatch-{}", page.id),
                &path,
                client,
            ))
            .into_any_element();
    }
    if !crate::webview::available() {
        return v_flex()
            .size_full()
            .items_center()
            .justify_center()
            .gap_3()
            .rounded_lg()
            .bg(theme::ink())
            .child(
                div()
                    .text_3xl()
                    .text_color(theme::faint())
                    .child(crate::icons::ic("globe")),
            )
            .child(
                div()
                    .text_lg()
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(theme::primary())
                    .child(page.title.clone()),
            )
            .child(
                div()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(crate::dates::date_time_of(&page.updated_at)),
            )
            .children(open)
            .into_any_element();
    }
    div()
        .size_full()
        .rounded_lg()
        .overflow_hidden()
        .border_1()
        .border_color(theme::line())
        .bg(theme::paper())
        .child(crate::webview::page(
            format!("hatch-{}", page.id),
            crate::webview::daemon_url(&path),
            shown,
        ))
        .into_any_element()
}

/// drag_pans has a drag move what a zoomed view shows, as the Electron
/// review's (lib/zoom.ts panBy), through the scroll it pans by.
pub fn drag_pans(el: Stateful<Div>, pan: &ScrollHandle) -> Stateful<Div> {
    let from: std::rc::Rc<std::cell::Cell<Option<Point<Pixels>>>> = std::rc::Rc::default();
    let (down, up) = (from.clone(), from.clone());
    let pan = pan.clone();
    el.on_mouse_down(MouseButton::Left, move |ev, _, _| {
        down.set(Some(ev.position))
    })
    .on_mouse_up(MouseButton::Left, move |_, _, _| up.set(None))
    .on_mouse_move(move |ev, window, _| {
        let Some(at) = from.get() else { return };
        if ev.pressed_button != Some(MouseButton::Left) {
            from.set(None);
            return;
        }
        let max = pan.max_offset();
        let o = pan.offset() + (ev.position - at);
        pan.set_offset(point(o.x.clamp(-max.x, px(0.)), o.y.clamp(-max.y, px(0.))));
        from.set(Some(ev.position));
        window.refresh();
    })
}

/// rezoom keeps what is at the middle of a zoomed view there as its zoom
/// goes from `from` to `to` (lib/zoom.ts keeps the view's middle point).
pub fn rezoom(pan: &ScrollHandle, from: f32, to: f32) {
    let size = pan.bounds().size;
    if size.width <= px(0.) || from <= 0. {
        return;
    }
    let (w, h) = (size.width, size.height);
    let o = pan.offset();
    let k = to / from;
    let x = w / 2. - (w / 2. - o.x) * k;
    let y = h / 2. - (h / 2. - o.y) * k;
    let (mx, my) = (w * (to - 1.).max(0.), h * (to - 1.).max(0.));
    pan.set_offset(point(x.clamp(-mx, px(0.)), y.clamp(-my, px(0.))));
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
