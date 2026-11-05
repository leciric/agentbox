//! An agent's finish in its lead's chat (components/chat/EvidenceCard.tsx):
//! the card with what it showed it checked — its recording's chapters (or its
//! summary), a Hatch page, its pull request, lines and checks, the first
//! screenshots — and Review. A finish the same agent made again later is a
//! line pointing at the newer card.
use std::collections::{HashMap, HashSet};
use std::path::PathBuf;

use agentbox_api::{Artifacts, MediaItem, ReviewItem, ReviewPage};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::{ChatAction, ChatView};
use crate::media::{self, Shown};
use crate::theme;

/// What a finished agent's card shows, as it's read.
#[derive(Default)]
pub struct Evidence {
    pub item: Option<ReviewItem>,
    pub gallery: Vec<Shown>,
    pub files: HashMap<String, PathBuf>,
}

/// Merged is whether a finish's pull request was merged: merged finishes
/// are folded away unless shown (lib/finished.ts visibleFinishes).
fn merged(e: Option<&Evidence>) -> bool {
    e.and_then(|e| e.item.as_ref())
        .and_then(|i| i.pr.as_ref())
        .is_some_and(|p| p.state == "merged")
}

#[derive(Default)]
pub struct Evidences {
    pub by_ref: HashMap<String, Evidence>,
    asked: HashSet<String>,
    /// The item each agent finished in last: only it gets the whole card.
    latest: HashMap<String, String>,
    scanned: usize,
    /// The items whose merged finishes are shown.
    show_merged: HashSet<String>,
    /// Each card's recording, playing muted and looped.
    #[cfg(feature = "video")]
    players: HashMap<String, Entity<crate::video::VideoPlayer>>,
}

impl ChatView {
    /// want_evidence reads, once each, what the finished agents' cards show.
    pub(super) fn want_evidence(&mut self, cx: &mut Context<Self>) {
        if self.evidence.scanned == self.items.len() {
            return;
        }
        self.evidence.scanned = self.items.len();
        let mut wanted = Vec::new();
        for it in &self.items {
            for r in &it.finished {
                self.evidence.latest.insert(r.clone(), it.id.clone());
                if self.evidence.asked.insert(r.clone()) {
                    wanted.push(r.clone());
                }
            }
        }
        for r in wanted {
            let Some((project, name)) = r.split_once('/') else {
                continue;
            };
            let (project, name) = (project.to_string(), name.to_string());
            let client = self.client.clone();
            let r2 = r.clone();
            cx.spawn(async move |this, cx| {
                let base = format!("/v1/projects/{project}/review?limit=200");
                let mut item = None;
                for q in [base.clone(), format!("{base}&reviewed=1")] {
                    if let Ok(p) = client.get::<ReviewPage>(&q).await
                        && let Some(i) = p.items.into_iter().find(|i| i.r#ref == r2)
                    {
                        item = Some(i);
                        break;
                    }
                }
                let list = client
                    .get::<Vec<MediaItem>>(&format!("/v1/agents/{project}/{name}/media"))
                    .await
                    .unwrap_or_default();
                let pages = client
                    .get::<Artifacts>(&format!("/v1/projects/{project}/artifacts"))
                    .await
                    .unwrap_or_default();
                let mut gallery = media::gallery(&list);
                gallery.extend(media::live_pages(&pages, &r2).into_iter().map(Shown::Page));
                let shots: Vec<MediaItem> = gallery
                    .iter()
                    .filter_map(Shown::thumb)
                    .take(3)
                    .cloned()
                    .collect();
                let mut files = HashMap::new();
                // The first recording plays on the card.
                let rec = gallery.iter().find_map(|s| match s {
                    Shown::Recording(t) => Some(t.media.clone()),
                    _ => None,
                });
                let rec_path = match rec.filter(|_| cfg!(feature = "video")) {
                    Some(m) => media::fetch(client.clone(), m).await.ok(),
                    None => None,
                };
                for m in shots {
                    if let Ok(p) = media::fetch(client.clone(), m.clone()).await {
                        files.insert(m.id.clone(), p);
                    }
                }
                this.update(cx, |this, cx| {
                    #[cfg(feature = "video")]
                    if let Some(path) = rec_path {
                        let player = cx.new(|cx| {
                            let mut p = crate::video::VideoPlayer::new(path, cx);
                            p.set_bare();
                            p
                        });
                        this.evidence.players.insert(r2.clone(), player);
                    }
                    #[cfg(not(feature = "video"))]
                    let _ = rec_path;
                    this.evidence.by_ref.insert(
                        r2,
                        Evidence {
                            item,
                            gallery,
                            files,
                        },
                    );
                    cx.notify();
                })
                .ok();
            })
            .detach();
        }
    }

    /// render_finished is an item's finishes: the card of each agent's last,
    /// a line for one it finished again later.
    pub(super) fn render_finished(
        &self,
        item_id: &str,
        refs: &[String],
        view: &Entity<Self>,
    ) -> AnyElement {
        // Merged finishes fold into a line that shows them again
        // (FinishedCards' merged toggle).
        let show = self.evidence.show_merged.contains(item_id);
        let merged_n = refs
            .iter()
            .filter(|r| merged(self.evidence.by_ref.get(*r)))
            .count();
        let hidden = if show { 0 } else { merged_n };
        let toggle = (hidden > 0 || (show && merged_n > 0)).then(|| {
            let (v, id) = (view.clone(), item_id.to_string());
            h_flex()
                .id(SharedString::from(format!("finished-merged-{item_id}")))
                .px_1()
                .gap_1p5()
                .text_xs()
                .text_color(theme::faint())
                .cursor_pointer()
                .hover(|s| s.text_color(theme::primary()))
                .child(crate::icons::ic("git-merge"))
                .child(if show {
                    t("evidence.hideMerged")
                } else {
                    tv("evidence.mergedHidden", &[("count", hidden.into())])
                })
                .on_click(move |_, _, cx| {
                    v.update(cx, |this, cx| {
                        if !this.evidence.show_merged.remove(&id) {
                            this.evidence.show_merged.insert(id.clone());
                        }
                        cx.notify();
                    })
                })
        });
        v_flex()
            .mb_4()
            .gap_3()
            .children(
                refs.iter()
                    .filter(|r| show || !merged(self.evidence.by_ref.get(*r)))
                    .map(|r| {
                        if self.evidence.latest.get(r).is_some_and(|l| l != item_id) {
                            let title = self
                                .evidence
                                .by_ref
                                .get(r)
                                .and_then(|e| e.item.as_ref())
                                .map(|i| i.title.clone())
                                .filter(|t| !t.is_empty())
                                .unwrap_or_else(|| r.split('/').nth(1).unwrap_or(r).to_string());
                            return h_flex()
                                .px_1()
                                .gap_1p5()
                                .text_sm()
                                .text_color(theme::faint())
                                .child(crate::icons::of("✓"))
                                .child(tv("evidence.finishedEarlier", &[("title", title.into())]))
                                .into_any_element();
                        }
                        self.render_evidence(item_id, r, view)
                    }),
            )
            .children(toggle)
            .into_any_element()
    }

    fn render_evidence(&self, item_id: &str, r: &str, view: &Entity<Self>) -> AnyElement {
        let name = r.split('/').nth(1).unwrap_or(r).to_string();
        let e = self.evidence.by_ref.get(r);
        let item = e.and_then(|e| e.item.as_ref());
        let take = e.and_then(|e| {
            e.gallery.iter().find_map(|s| match s {
                Shown::Recording(t) => Some(t.clone()),
                _ => None,
            })
        });
        let page = e.and_then(|e| {
            e.gallery.iter().find_map(|s| match s {
                Shown::Page(p) => Some(p.title.clone()),
                _ => None,
            })
        });
        let shots: Vec<(String, Option<PathBuf>)> = e
            .map(|e| {
                e.gallery
                    .iter()
                    .filter_map(Shown::thumb)
                    .map(|m| (m.id.clone(), e.files.get(&m.id).cloned()))
                    .collect()
            })
            .unwrap_or_default();
        let more = shots.len().saturating_sub(3);
        let title = item
            .map(|i| i.title.clone())
            .filter(|t| !t.is_empty())
            .unwrap_or_else(|| name.clone());
        let chapters = take
            .as_ref()
            .map(|t| t.chapters.clone())
            .unwrap_or_default();
        let summary = item
            .map(|i| i.summary.replace(['*', '_', '`', '#', '>'], ""))
            .unwrap_or_default();
        let pr = item.and_then(|i| i.pr.clone()).filter(|p| p.number > 0);
        let changes = item.and_then(|i| i.changes.clone()).filter(|c| c.files > 0);
        let review = item.cloned();
        let open = {
            let (v, review) = (view.clone(), review.clone());
            move |_: &ClickEvent, _: &mut Window, cx: &mut App| {
                if let Some(i) = review.clone() {
                    v.update(cx, |_, cx| cx.emit(ChatAction::OpenReview(Box::new(i))));
                }
            }
        };
        // Its recording plays on the card, muted and looped; a click
        // reviews it.
        #[cfg(feature = "video")]
        let video = self.evidence.players.get(r).cloned().map(|p| {
            div()
                .id(SharedString::from(format!("ev-video-{item_id}-{r}")))
                .w_full()
                .h(px(220.))
                .bg(theme::pitch())
                .overflow_hidden()
                .cursor_pointer()
                .on_click(open.clone())
                .child(p)
        });
        #[cfg(not(feature = "video"))]
        let video: Option<Div> = None;
        let merge_pr = pr.clone().filter(|p| p.state == "open");
        let block = merge_pr
            .as_ref()
            .and_then(|p| crate::app::pulls::merge_block(p, None));
        v_flex()
            .rounded_xl()
            .border_1()
            .border_color(theme::line())
            .bg(theme::card())
            .overflow_hidden()
            .children(video)
            .child(
                v_flex()
                    .p_3p5()
                    .gap_2p5()
                    .child(
                        h_flex()
                            .gap_2()
                            .child(crate::icons::ai_icon(
                                item.map(|i| i.ai.as_str()).unwrap_or(""),
                                px(16.),
                                theme::muted(),
                            ))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .truncate()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .child(title),
                            )
                            .child(
                                div()
                                    .flex_none()
                                    .text_xs()
                                    .text_color(theme::faint())
                                    .child(tv("evidence.finished", &[("agent", name.into())])),
                            ),
                    )
                    .when_some(take.as_ref(), |el, t| {
                        el.child(
                            div()
                                .id(SharedString::from(format!("ev-rec-{item_id}-{r}")))
                                .text_xs()
                                .text_color(theme::muted())
                                .cursor_pointer()
                                .hover(|s| s.text_color(theme::primary()))
                                .on_click(open.clone())
                                .child(crate::icons::lead("play", media::readable(&t.media.name))),
                        )
                    })
                    .map(|el| {
                        if !chapters.is_empty() {
                            el.child(v_flex().gap_1().text_sm().children(
                                chapters.iter().take(5).map(|c| {
                                    h_flex()
                                        .gap_1p5()
                                        .child(
                                            div()
                                                .text_color(theme::running())
                                                .child(crate::icons::of("✓")),
                                        )
                                        .child(
                                            div()
                                                .min_w_0()
                                                .truncate()
                                                .text_color(theme::primary())
                                                .child(c.title.clone()),
                                        )
                                }),
                            ))
                        } else if !summary.is_empty() {
                            el.child(
                                div()
                                    .text_sm()
                                    .text_color(theme::muted())
                                    .line_clamp(3)
                                    .child(summary),
                            )
                        } else {
                            el
                        }
                    })
                    .when_some(page, |el, p| {
                        el.child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .truncate()
                                .child(crate::icons::lead("globe", p)),
                        )
                    })
                    .child(
                        h_flex()
                            .flex_wrap()
                            .gap_2p5()
                            .text_xs()
                            .text_color(theme::faint())
                            .when_some(pr.as_ref(), |el, pr| el.child(crate::app::pr_chip_for(pr)))
                            .when_some(changes, |el, c| {
                                el.child(
                                    h_flex()
                                        .gap_1()
                                        .font_family(theme::mono())
                                        .child(
                                            div()
                                                .text_color(theme::running())
                                                .child(format!("+{}", c.insertions)),
                                        )
                                        .child(
                                            div()
                                                .text_color(theme::error())
                                                .child(format!("−{}", c.deletions)),
                                        ),
                                )
                            })
                            .when_some(
                                pr.as_ref().filter(|p| p.state == "open"),
                                |el, pr| match pr.checks.as_str() {
                                    "failing" => el.child(
                                        div()
                                            .text_color(theme::error())
                                            .child(t("v2.card.checksFailing")),
                                    ),
                                    "passing" => el.child(
                                        div()
                                            .text_color(theme::running())
                                            .child(t("v2.card.checksPass")),
                                    ),
                                    _ => el,
                                },
                            ),
                    ),
            )
            .child(
                h_flex()
                    .p_2p5()
                    .gap_2()
                    .border_t_1()
                    .border_color(theme::line())
                    .child(
                        h_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_1p5()
                            .children(shots.into_iter().take(3).enumerate().map(|(n, (_, p))| {
                                div()
                                    .id(SharedString::from(format!("ev-shot-{item_id}-{r}-{n}")))
                                    .w(px(80.))
                                    .h(px(48.))
                                    .flex_none()
                                    .rounded_md()
                                    .overflow_hidden()
                                    .border_1()
                                    .border_color(theme::line())
                                    .bg(theme::pitch())
                                    .cursor_pointer()
                                    .on_click(open.clone())
                                    .when_some(p, |el, p| {
                                        el.child(img(p).size_full().object_fit(ObjectFit::Cover))
                                    })
                            }))
                            .when(more > 0, |el| {
                                el.child(
                                    div()
                                        .px_2()
                                        .text_xs()
                                        .text_color(theme::faint())
                                        .child(tv("evidence.moreShots", &[("count", more.into())])),
                                )
                            }),
                    )
                    .when(review.is_some(), |el| {
                        el.child(crate::app::controls::button(
                            SharedString::from(format!("ev-review-{item_id}-{r}")),
                            t("evidence.review"),
                            crate::app::controls::Btn::Secondary,
                            false,
                            open.clone(),
                        ))
                    })
                    .when(pr.as_ref().is_some_and(|p| p.state == "merged"), |el| {
                        el.child(
                            h_flex()
                                .gap_1p5()
                                .px_2()
                                .text_sm()
                                .text_color(theme::accent())
                                .child(crate::icons::ic("git-merge"))
                                .child(t("evidence.merged")),
                        )
                    })
                    // Merge, asked first as in the Pull requests list.
                    .when_some(merge_pr, |el, p| {
                        let (v, project, number) = (
                            view.clone(),
                            item.map(|i| i.project.clone()).unwrap_or_default(),
                            p.number,
                        );
                        let tip = block.clone();
                        el.child(
                            div()
                                .id(SharedString::from(format!("ev-merge-wrap-{item_id}-{r}")))
                                .when_some(tip, |el, tip| {
                                    el.tooltip(move |w, cx| {
                                        gpui_component::tooltip::Tooltip::new(tip.clone())
                                            .build(w, cx)
                                    })
                                })
                                .child(crate::app::controls::button(
                                    SharedString::from(format!("ev-merge-{item_id}-{r}")),
                                    t("evidence.merge"),
                                    crate::app::controls::Btn::Primary,
                                    block.is_some(),
                                    move |_, _, cx| {
                                        let project = project.clone();
                                        v.update(cx, |_, cx| {
                                            cx.emit(ChatAction::Merge(project, number))
                                        })
                                    },
                                )),
                        )
                    })
                    .child(
                        div()
                            .id(SharedString::from(format!("ev-open-{item_id}-{r}")))
                            .text_xs()
                            .text_color(theme::accent())
                            .cursor_pointer()
                            .hover(|s| s.underline())
                            .on_click({
                                let (v, r) = (view.clone(), r.to_string());
                                move |_, _, cx| {
                                    v.update(cx, |_, cx| cx.emit(ChatAction::OpenAgent(r.clone())))
                                }
                            })
                            .child(t("chat.agentPermission.open")),
                    ),
            )
            .into_any_element()
    }
}
