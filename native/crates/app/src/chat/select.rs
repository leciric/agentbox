//! Selecting a chat's text and copying it, as Chromium lets you in the
//! Electron app's chat: drag across messages, then Ctrl+C or the message's
//! menu.
//!
//! The selection is gpui-component's window-wide one (gpui-base's
//! TextSelection, mounted by Root): every TextView in the window takes part,
//! in the order they are painted, so a drag carries on from one message into
//! the next and a copy reads them top to bottom. Your own messages are
//! TextViews too, of `literal_markdown`, so a drag across them keeps that
//! order: gpui-base's plain SelectableText takes a fixed place in it instead.

/// literal_markdown is Markdown that a TextView draws as `text` reads, the
/// way a `white-space: pre-wrap` bubble shows it (UserMessage): every ASCII
/// punctuation mark escaped, so nothing turns into a heading, a list, a link
/// or code; every line ending a hard break, a blank line included (a line of
/// just the break's backslash isn't blank), so the text stays one paragraph:
/// a TextView copies paragraphs a single line ending apart; and the spaces at
/// either end of a line kept as character references, which Markdown would
/// drop or read as an indented code block. Blank lines before and after it
/// are left out.
pub fn literal_markdown(text: &str) -> String {
    let text = text.replace("\r\n", "\n");
    let mut out = String::with_capacity(text.len() + text.len() / 2);
    for (i, line) in text.trim_matches('\n').split('\n').enumerate() {
        if i > 0 {
            out.push_str("\\\n");
        }
        let body = line.trim_matches([' ', '\t']);
        let lead = line.len() - line.trim_start_matches([' ', '\t']).len();
        let (lead, trail) = if body.is_empty() {
            (line, "")
        } else {
            (&line[..lead], &line[lead + body.len()..])
        };
        push_spaces(&mut out, lead);
        for c in body.chars() {
            if c.is_ascii_punctuation() {
                out.push('\\');
            }
            out.push(c);
        }
        push_spaces(&mut out, trail);
    }
    out
}

fn push_spaces(out: &mut String, s: &str) {
    for c in s.chars() {
        out.push_str(if c == '\t' { "&#9;" } else { "&#32;" });
    }
}

/// copy_text is what a message's Copy puts on the clipboard: what is
/// selected, or the whole message when nothing is (MessageMenu's copy).
pub fn copy_text(selected: &str, message: &str) -> String {
    if selected.trim().is_empty() {
        message.to_string()
    } else {
        selected.to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use markdown::mdast::Node;

    /// The parse options gpui-base's TextView reads Markdown with
    /// (markdown_ext.rs's parse_options): GFM, and math.
    fn parse(md: &str) -> Node {
        let mut options = markdown::ParseOptions::gfm();
        options.constructs.math_text = true;
        options.constructs.math_flow = true;
        markdown::to_mdast(md, &options).unwrap()
    }

    /// shown is the text a parsed document draws, or what in it isn't plain
    /// text: paragraphs a blank line apart, a hard break a line ending.
    fn shown(node: &Node, out: &mut String) -> Result<(), String> {
        match node {
            Node::Root(r) => {
                for (i, c) in r.children.iter().enumerate() {
                    if i > 0 {
                        out.push_str("\n\n");
                    }
                    shown(c, out)?;
                }
            }
            Node::Paragraph(p) => {
                for c in &p.children {
                    shown(c, out)?;
                }
            }
            Node::Text(t) => out.push_str(&t.value),
            Node::Break(_) => out.push('\n'),
            other => return Err(format!("{other:?}")),
        }
        Ok(())
    }

    fn draws(text: &str) -> String {
        let mut out = String::new();
        if let Err(e) = shown(&parse(&literal_markdown(text)), &mut out) {
            panic!("{text:?} isn't drawn as plain text: {e}");
        }
        out
    }

    #[test]
    fn markup_is_drawn_as_written() {
        for text in [
            "hello world",
            "# not a heading\n- not a list\n1. nor this\n> nor a quote",
            "**bold** _em_ `code` ~~gone~~ [link](http://x.y) <b>html</b> &amp; $x$ $$y$$",
            "see https://example.com/a_b?c=d, www.foo.com and me@mail.com",
            "```\nfenced\n```",
            "| a | b |\n|---|---|\n| 1 | 2 |",
            "a back\\slash, and one at the end\\",
            "---\n***\n===",
            "- [ ] a task",
            "<!-- a comment -->",
            "[^1]",
            "🦊 ünïcode",
        ] {
            assert_eq!(draws(text), text);
        }
    }

    #[test]
    fn lines_and_their_spaces_stay() {
        assert_eq!(draws("one\ntwo\nthree"), "one\ntwo\nthree");
        // Four spaces would be an indented code block, a tab too.
        assert_eq!(draws("    indented\n\ttabbed"), "    indented\n\ttabbed");
        assert_eq!(draws("trailing  \nspaces   "), "trailing  \nspaces   ");
        assert_eq!(draws("  \n  x"), "  \n  x");
        assert_eq!(draws("crlf\r\nline"), "crlf\nline");
    }

    #[test]
    fn blank_lines_stay_in_one_paragraph() {
        assert_eq!(draws("a\n\nb"), "a\n\nb");
        assert_eq!(draws("a\n\n\n\nb\n"), "a\n\n\n\nb");
        assert_eq!(draws("\n\nlead and trail\n\n"), "lead and trail");
        assert_eq!(literal_markdown(""), "");
        // One paragraph, its blank lines two breaks in a row.
        let Node::Root(root) = parse(&literal_markdown("a\n\nb")) else {
            panic!("not a document");
        };
        assert_eq!(root.children.len(), 1);
    }

    #[test]
    fn copy_takes_the_selection_over_the_message() {
        assert_eq!(copy_text("part", "the whole message"), "part");
        assert_eq!(copy_text("", "the whole message"), "the whole message");
        assert_eq!(copy_text(" \n", "the whole message"), "the whole message");
    }
}

/// A drag across a message of yours and a reply, as the timeline draws them
/// (TextViews in a column, under gpui-component's Root), and Ctrl+C.
#[cfg(test)]
mod drag {
    use gpui::{
        AppContext as _, Context, FocusHandle, InteractiveElement as _, IntoElement, Modifiers,
        MouseButton, ParentElement as _, Render, Styled as _, TestAppContext, VisualTestContext,
        Window, div, point, px,
    };
    use gpui_component::Root;
    use gpui_component::text::TextView;

    use super::literal_markdown;

    const YOURS: &str = "Run `cargo test`, then *this*:\n  - not a list\n\n# nor a heading";
    const REPLY: &str = "Done:\n\n- **one**\n- two";

    /// The chat: focusable, as ChatView is, its two messages 40px apart.
    struct Chat {
        focus: FocusHandle,
    }

    impl Render for Chat {
        fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
            div()
                .track_focus(&self.focus)
                .size_full()
                .child(
                    div()
                        .h(px(160.))
                        .child(TextView::markdown("yours", literal_markdown(YOURS))),
                )
                .child(div().h(px(40.)))
                .child(div().h(px(160.)).child(TextView::markdown("reply", REPLY)))
        }
    }

    fn chat(cx: &mut TestAppContext) -> &mut VisualTestContext {
        cx.update(gpui_component::init);
        let (_, cx) = cx.add_window_view(|window, cx| {
            let chat = cx.new(|cx| Chat {
                focus: cx.focus_handle(),
            });
            Root::new(chat, window, cx)
        });
        cx.run_until_parked();
        cx.update(|window, cx| {
            let _ = window.draw(cx);
        });
        cx
    }

    fn drag(cx: &mut VisualTestContext, from: (f32, f32), to: (f32, f32)) {
        let (from, to) = (point(px(from.0), px(from.1)), point(px(to.0), px(to.1)));
        cx.simulate_mouse_down(from, MouseButton::Left, Modifiers::default());
        cx.simulate_mouse_move(to, Some(MouseButton::Left), Modifiers::default());
        cx.simulate_mouse_up(to, MouseButton::Left, Modifiers::default());
        cx.update(|window, cx| {
            let _ = window.draw(cx);
        });
    }

    fn copied(cx: &mut VisualTestContext) -> String {
        cx.simulate_keystrokes(if cfg!(target_os = "macos") {
            "cmd-c"
        } else {
            "ctrl-c"
        });
        cx.read_from_clipboard()
            .and_then(|c| c.text())
            .unwrap_or_default()
    }

    #[gpui::test]
    fn your_message_copies_as_written(cx: &mut TestAppContext) {
        let cx = chat(cx);
        drag(cx, (1., 1.), (600., 150.));
        assert_eq!(copied(cx), YOURS);
    }

    #[gpui::test]
    fn a_drag_across_messages_copies_both_in_order(cx: &mut TestAppContext) {
        let cx = chat(cx);
        drag(cx, (1., 1.), (600., 350.));
        // TextViews a blank line apart, a reply's blocks a line ending.
        assert_eq!(copied(cx), format!("{YOURS}\n\nDone:\none\ntwo"));
    }
}
