#!/usr/bin/env python3
"""Writes a downloads page for the R2 bucket (scripts/r2-publish.sh).

    r2-page.py <releases.json> <notes dir> [tag]

The page shows every release in releases.json (the newest stable and the
newest nightly) as a card, its assets grouped by platform, and leads with a
download button for the visitor's OS taken from tag's release (the stable one
when no tag is given). <notes dir>/<tag>.html, when there, is the release's
notes as GitHub renders them. It looks like agentbox.linting.dev (zinc,
violet, Inter where installed, light and dark) and is one file that requests
nothing else: downloads are absolute URLs, so the same page works at the
bucket's root and in releases/<tag>/.
"""

import html
import json
import re
import sys
from datetime import datetime
from pathlib import Path

LOGO = (
    '<svg viewBox="0 0 512 512" aria-hidden="true"><defs>'
    '<linearGradient id="lg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#a78bfa"/>'
    '<stop offset=".55" stop-color="#7c3aed"/><stop offset="1" stop-color="#4f46e5"/></linearGradient></defs>'
    '<rect x="24" y="24" width="464" height="464" rx="112" fill="url(#lg)"/>'
    '<g transform="translate(106 106) scale(12.5)" fill="none" stroke="#fff" stroke-width="1.9" '
    'stroke-linecap="round" stroke-linejoin="round"><path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 '
    '4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z"/><path d="m3.3 7 8.7 5 8.7-5"/>'
    '<path d="M12 22V12"/></g></svg>'
)
DOWNLOAD = (
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" '
    'stroke-linejoin="round" aria-hidden="true"><path d="M12 3v12m0 0-4-4m4 4 4-4M4 17v2a2 2 0 0 0 2 2h12a2 '
    '2 0 0 0 2-2v-2"/></svg>'
)
THEME = (
    '<svg class="moon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" '
    'stroke-linejoin="round" aria-hidden="true"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z"/></svg>'
    '<svg class="sun" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" '
    'aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 '
    '1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>'
)

# What each asset is, by its name: (pattern, platform, label, detail, OS the
# button offers it to), listed in this order. Anything unmatched
# (latest-linux.yml, blockmaps) isn't listed.
KINDS = [
    (r"\.AppImage$", "linux", "AppImage", "Any distribution; updates itself", "linux"),
    (r"\.deb$", "linux", "Debian and Ubuntu", ".deb package", None),
    (r"\.pacman$", "linux", "Arch Linux", ".pacman package", None),
    (r"-mac-arm64\.dmg$", "mac", "Apple Silicon", ".dmg disk image", "mac"),
    (r"-mac-arm64\.zip$", "mac", "Apple Silicon", ".zip archive", None),
    (r"-mac-x64\.dmg$", "mac", "Intel", ".dmg disk image", None),
    (r"-mac-x64\.zip$", "mac", "Intel", ".zip archive", None),
    (r"-setup\.exe$", "windows", "Installer", "x64 setup", "windows"),
    (r"-portable\.exe$", "windows", "Portable", "x64, runs without installing", None),
    (r"-linux-amd64$", "cli", "Linux", "x86_64", None),
    (r"-linux-arm64$", "cli", "Linux", "arm64", None),
    (r"-darwin-arm64$", "cli", "macOS", "Apple Silicon", None),
    (r"-darwin-amd64$", "cli", "macOS", "Intel", None),
]
PLATFORMS = [("linux", "Linux"), ("mac", "macOS"), ("windows", "Windows"), ("cli", "Command line")]
OS_NAMES = {"linux": "Linux", "mac": "macOS", "windows": "Windows"}


def esc(s):
    return html.escape(str(s), quote=True)


def size(n):
    mb = n / 1048576
    if mb >= 100:
        return f"{mb:.0f} MB"
    if mb >= 1:
        return f"{mb:.1f} MB"
    return f"{max(n / 1024, 1):.0f} kB"


def day(iso):
    try:
        d = datetime.fromisoformat(iso.replace("Z", "+00:00"))
    except (AttributeError, ValueError):
        return ""
    return f"{d.day} {d:%B %Y}"


def kind(name):
    for i, (pattern, platform, label, detail, os_) in enumerate(KINDS):
        if re.search(pattern, name):
            return platform, label, detail, os_, i
    return None


def card(rel, notes_dir, focus):
    tag = rel["tag_name"]
    version = tag.removeprefix("v")
    nightly = bool(rel.get("prerelease"))
    groups = {p: [] for p, _ in PLATFORMS}
    sums = []
    for a in rel.get("assets", []):
        if a["name"].startswith("SHA256SUMS"):
            sums.append(a)
            continue
        k = kind(a["name"])
        if k:
            groups[k[0]].append((k, a))
    out = [f'<section class="card{" focus" if focus else ""}" id="{esc(tag)}">']
    out.append('<div class="card-head"><div>')
    out.append(
        f'<h2>AgentBox {esc(version)} <span class="badge {"nightly" if nightly else "stable"}">'
        f'{"Nightly" if nightly else "Stable"}</span></h2>'
    )
    blurb = "Built from the next release as it stands: for trying what’s coming, and it may break." if nightly else "The latest release."
    out.append(f'<p class="muted">Published {esc(day(rel.get("published_at", "")))}. {blurb}</p></div>')
    if sums:
        out.append('<div class="sums">' + " ".join(
            f'<a href="{esc(a["browser_download_url"])}">{esc(a["name"])}</a>' for a in sums) + "</div>")
    out.append('</div><div class="groups">')
    for platform, title in PLATFORMS:
        if not groups[platform]:
            continue
        out.append(f'<div class="group"><h3>{title}</h3><ul>')
        for (_, label, detail, _, _), a in sorted(groups[platform], key=lambda g: g[0][4]):
            out.append(
                f'<li><a href="{esc(a["browser_download_url"])}" title="{esc(a["name"])}">'
                f'<span class="what"><span class="label">{esc(label)}</span><span class="detail">{esc(detail)}</span></span>'
                f'<span class="size">{size(a["size"])}</span>{DOWNLOAD}</a></li>'
            )
        out.append("</ul></div>")
    out.append("</div>")
    notes = Path(notes_dir, f"{tag}.html")
    if notes.is_file() and notes.read_text().strip():
        out.append(f'<details class="notes"{"" if nightly else " open"}><summary>Release notes</summary>'
                   f'<div class="markdown">{notes.read_text()}</div></details>')
    out.append("</section>")
    return "\n".join(out)


def page(releases, notes_dir, focus_tag=None):
    stable = [r for r in releases if not r.get("prerelease")]
    nightly = [r for r in releases if r.get("prerelease")]
    ordered = stable[:1] + nightly[:1]
    focus = next((r for r in ordered if r["tag_name"] == focus_tag), ordered[0] if ordered else None)
    if focus is None:
        sys.exit("releases.json lists no release")
    # The button's choices, one per OS, from the focused release; Linux is the
    # one shown before (or without) the script.
    choices = {}
    for a in focus.get("assets", []):
        k = kind(a["name"])
        if k and k[3]:
            choices[k[3]] = {"url": a["browser_download_url"], "what": f"{k[1]} · {size(a['size'])}"}
    first = choices.get("linux") or next(iter(choices.values()), None)
    version = focus["tag_name"].removeprefix("v")
    title = f"AgentBox {version}"
    button = ""
    if first:
        button = (
            f'<a class="primary" id="download" href="{esc(first["url"])}">{DOWNLOAD}'
            f'<span id="download-label">Download for {OS_NAMES["linux" if "linux" in choices else next(iter(choices))]}</span></a>'
            f'<p class="muted small" id="download-what">{esc(first["what"])}</p>'
        )
    cards = "\n".join(card(r, notes_dir, r is focus and len(ordered) > 1) for r in ordered)
    return TEMPLATE.format(
        title=esc(title),
        logo=LOGO,
        theme=THEME,
        version=esc(version),
        kind="nightly" if focus.get("prerelease") else "stable",
        kind_label="Nightly" if focus.get("prerelease") else "Stable",
        published=esc(day(focus.get("published_at", ""))),
        button=button,
        cards=cards,
        choices=json.dumps(choices).replace("</", "<\\/"),
    )


TEMPLATE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>{title} · Download</title>
<script>try{{var t=localStorage.getItem("theme");if(t)document.documentElement.dataset.theme=t}}catch(e){{}}</script>
<style>
:root{{--bg:#fff;--fg:#52525b;--strong:#09090b;--muted:#71717a;--line:#e4e4e7;--card:#fff;--soft:#fafafa;--hover:#f4f4f5;--accent:#7c3aed;--accent-hover:#8b5cf6;--accent-fg:#6d28d9;--accent-soft:#f5f3ff;--code:#f4f4f5;color-scheme:light}}
@media (prefers-color-scheme:dark){{:root:not([data-theme=light]){{--bg:#09090b;--fg:#a1a1aa;--strong:#fff;--muted:#71717a;--line:#27272a;--card:#18181b;--soft:#18181b;--hover:#27272a;--accent-fg:#a78bfa;--accent-soft:#2e1065;--code:#27272a;color-scheme:dark}}}}
:root[data-theme=dark]{{--bg:#09090b;--fg:#a1a1aa;--strong:#fff;--muted:#71717a;--line:#27272a;--card:#18181b;--soft:#18181b;--hover:#27272a;--accent-fg:#a78bfa;--accent-soft:#2e1065;--code:#27272a;color-scheme:dark}}
*{{box-sizing:border-box}}
body{{margin:0;background:var(--bg);color:var(--fg);font:15px/1.6 Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;-webkit-font-smoothing:antialiased}}
a{{color:inherit;text-decoration:none}}
svg{{display:block}}
.wrap{{max-width:64rem;margin:0 auto;padding:0 1.5rem}}
header{{border-bottom:1px solid var(--line)}}
header .wrap{{display:flex;align-items:center;height:4rem;gap:.625rem}}
.brand{{display:flex;align-items:center;gap:.625rem;font-weight:600;color:var(--strong)}}
.brand svg{{width:1.75rem;height:1.75rem}}
.toggle{{margin-left:auto;display:grid;place-items:center;width:2.25rem;height:2.25rem;border:0;border-radius:.375rem;background:none;color:var(--muted);cursor:pointer}}
.toggle:hover{{background:var(--hover);color:var(--strong)}}
.toggle svg{{width:1.25rem;height:1.25rem}}
.sun{{display:none}}
:root[data-theme=dark] .sun{{display:block}} :root[data-theme=dark] .moon{{display:none}}
@media (prefers-color-scheme:dark){{:root:not([data-theme]) .sun{{display:block}} :root:not([data-theme]) .moon{{display:none}}}}
.hero{{text-align:center;padding:4rem 0 3rem}}
.hero h1{{margin:1rem 0 0;font-size:2.75rem;line-height:1.1;font-weight:600;letter-spacing:-.025em;color:var(--strong)}}
.hero .lead{{margin:1rem auto 0;max-width:36rem;font-size:1.125rem}}
.primary{{display:inline-flex;align-items:center;gap:.5rem;margin-top:2rem;height:2.75rem;padding:0 1.25rem;border-radius:.5rem;background:var(--accent);color:#fff;font-weight:500;box-shadow:0 1px 2px rgb(0 0 0/.08)}}
.primary:hover{{background:var(--accent-hover)}}
.primary svg{{width:1rem;height:1rem}}
.muted{{color:var(--muted)}}
.small{{font-size:.8125rem;margin:.5rem 0 0}}
.badge{{display:inline-block;vertical-align:middle;padding:.125rem .625rem;border-radius:9999px;font-size:.75rem;font-weight:500;line-height:1.25rem;border:1px solid var(--line);color:var(--fg)}}
.badge.nightly{{background:var(--accent-soft);color:var(--accent-fg);border-color:transparent}}
.cards{{display:grid;gap:1.5rem;padding-bottom:4rem}}
.card{{border:1px solid var(--line);border-radius:.75rem;background:var(--card);padding:1.5rem}}
.card.focus{{box-shadow:0 0 0 1px var(--accent)}}
.card-head{{display:flex;flex-wrap:wrap;justify-content:space-between;gap:.5rem 1.5rem;align-items:flex-start}}
.card h2{{margin:0;font-size:1.25rem;font-weight:600;color:var(--strong);display:flex;align-items:center;gap:.625rem}}
.card-head p{{margin:.25rem 0 0;font-size:.875rem}}
.sums{{display:flex;gap:1rem;font-size:.8125rem;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}}
.sums a{{color:var(--muted);border-bottom:1px dashed var(--line)}}
.sums a:hover{{color:var(--strong)}}
.groups{{display:grid;grid-template-columns:repeat(auto-fit,minmax(13.5rem,1fr));gap:1.25rem;margin-top:1.5rem}}
.group h3{{margin:0 0 .5rem;font-size:.8125rem;font-weight:600;color:var(--muted)}}
.group ul{{list-style:none;margin:0;padding:0;display:grid;gap:.375rem}}
.group a{{display:flex;align-items:center;gap:.75rem;padding:.5rem .75rem;border:1px solid var(--line);border-radius:.5rem;background:var(--soft)}}
.group a:hover{{border-color:var(--accent);background:var(--hover)}}
.group a svg{{width:1rem;height:1rem;color:var(--muted);flex:none}}
.group a:hover svg{{color:var(--accent-fg)}}
.what{{display:flex;flex-direction:column;min-width:0;flex:1;line-height:1.3}}
.label{{font-weight:500;color:var(--strong);font-size:.875rem}}
.detail{{font-size:.75rem;color:var(--muted)}}
.size{{font-size:.75rem;color:var(--muted);font-variant-numeric:tabular-nums;white-space:nowrap}}
.notes{{margin-top:1.5rem;border-top:1px solid var(--line);padding-top:1rem}}
.notes summary{{cursor:pointer;font-weight:500;color:var(--strong);font-size:.875rem}}
.markdown{{font-size:.875rem;overflow-wrap:anywhere}}
.markdown h1,.markdown h2,.markdown h3{{color:var(--strong);font-size:1rem;margin:1.25rem 0 .5rem}}
.markdown a{{color:var(--accent-fg)}}
.markdown code{{background:var(--code);padding:.1rem .3rem;border-radius:.25rem;font-size:.8125rem}}
.markdown pre{{background:var(--code);padding:.75rem;border-radius:.5rem;overflow:auto}}
.markdown pre code{{background:none;padding:0}}
.markdown ul{{padding-left:1.25rem}}
footer{{border-top:1px solid var(--line);padding:2rem 0;font-size:.8125rem;color:var(--muted)}}
footer a:hover{{color:var(--strong)}}
@media (max-width:640px){{.hero h1{{font-size:2rem}}.hero{{padding:2.5rem 0 2rem}}}}
</style>
</head>
<body>
<header><div class="wrap">
<a class="brand" href="https://agentbox.linting.dev">{logo}AgentBox</a>
<button class="toggle" id="theme" type="button" aria-label="Switch theme">{theme}</button>
</div></header>
<main class="wrap">
<div class="hero">
<span class="badge {kind}">{kind_label} · {version}</span>
<h1>Download AgentBox {version}</h1>
<p class="lead">Published {published}. Every platform, and the command-line tool on its own, is below.</p>
{button}
</div>
<div class="cards">
{cards}
</div>
</main>
<footer><div class="wrap">AgentBox · <a href="https://agentbox.linting.dev">agentbox.linting.dev</a> · check a download against its release’s SHA256SUMS</div></footer>
<script>
(function(){{
  var c={choices},ua=navigator.userAgent,os=/Mac/.test(ua)?"mac":/Win/.test(ua)?"windows":"linux",n={{linux:"Linux",mac:"macOS",windows:"Windows"}};
  var a=document.getElementById("download");
  if(a&&c[os]){{a.href=c[os].url;document.getElementById("download-label").textContent="Download for "+n[os];document.getElementById("download-what").textContent=c[os].what}}
  var r=document.documentElement;
  document.getElementById("theme").onclick=function(){{
    var dark=r.dataset.theme?r.dataset.theme==="dark":matchMedia("(prefers-color-scheme: dark)").matches;
    r.dataset.theme=dark?"light":"dark";try{{localStorage.setItem("theme",r.dataset.theme)}}catch(e){{}}
  }};
}})();
</script>
</body>
</html>
"""

if __name__ == "__main__":
    if len(sys.argv) not in (3, 4):
        sys.exit(__doc__)
    releases = json.loads(Path(sys.argv[1]).read_text())
    sys.stdout.write(page(releases, sys.argv[2], sys.argv[3] if len(sys.argv) == 4 else None))
