// The page of `agentbox machines serve`. The server filters, groups and pages
// (GET /api/items, /api/facets); this keeps the filters in the URL, renders
// pages of cards as they scroll into view, and reuses each card's element
// across refreshes so its thumbnail never reloads. The sidebar's Machines
// section lists the machines (GET /api/machines), and opens one's desktop
// live (#machine=<name>) with the app's VNC client, vnc.js.

const PAGE = 120;
const $ = (id) => document.getElementById(id);

const state = {
  q: "", kind: "", when: "", from: "", to: "", group: "day", source: "",
  repo: "", branch: "", session: "",
};
let items = [];     // loaded so far, in order
let total = 0;
let loading = null; // the page request in flight
let open = new Set(JSON.parse(localStorage.getItem("open") || "[]")); // expanded tree nodes
const cards = new Map(); // id -> card element

// ---- URL state ----

function readURL() {
  const p = new URLSearchParams(location.search);
  for (const k of Object.keys(state)) if (p.has(k)) state[k] = p.get(k);
}
function writeURL() {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(state)) if (v && !(k === "group" && v === "day")) p.set(k, v);
  const s = p.toString();
  history.replaceState(null, "", (s ? "?" + s : location.pathname) + location.hash);
}

function query(extra = {}) {
  const p = new URLSearchParams();
  for (const k of ["q", "kind", "group", "source", "repo", "branch", "session"]) if (state[k]) p.set(k, state[k]);
  const [from, to] = dateRange();
  if (from) p.set("from", from);
  if (to) p.set("to", to);
  for (const [k, v] of Object.entries(extra)) p.set(k, v);
  return p.toString();
}

function ymd(d) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}
function dateRange() {
  const now = new Date();
  switch (state.when) {
    case "today": return [ymd(now), ""];
    case "7": case "30": {
      const d = new Date(now); d.setDate(d.getDate() - Number(state.when) + 1);
      return [ymd(d), ""];
    }
    case "custom": return [state.from, state.to];
  }
  return ["", ""];
}

// ---- Fetching ----

async function getJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}

// reload rereads everything shown: the tree and as many items as are loaded
// (at least a page), keeping the scroll position.
async function reload({ keep = false } = {}) {
  const n = keep ? Math.max(items.length, PAGE) : PAGE;
  const [facets, page] = await Promise.all([
    getJSON("/api/facets?" + query()),
    getJSON("/api/items?" + query({ offset: 0, limit: n })),
  ]);
  items = page.items || [];
  total = page.total;
  renderTree(facets);
  renderGrid();
  if (lb.index >= 0) lb.sync();
}

async function loadMore() {
  if (loading || items.length >= total) return;
  loading = getJSON("/api/items?" + query({ offset: items.length, limit: PAGE }));
  try {
    const page = await loading;
    items = items.concat(page.items || []);
    total = page.total;
    renderGrid();
  } finally {
    loading = null;
  }
}

// ---- Tree ----

function node({ cls, name, count, current, chev, onClick, title, src }) {
  const b = document.createElement("button");
  b.className = "node " + (cls || "");
  if (current) b.setAttribute("aria-current", "true");
  if (title) b.title = title;
  const c = document.createElement("span");
  c.className = "chev" + (chev === "open" ? " open" : "");
  c.textContent = chev ? "▶" : "";
  const n = document.createElement("span");
  n.className = "name";
  n.textContent = name;
  b.append(c, n);
  if (src) {
    const s = document.createElement("span");
    s.className = "src"; s.textContent = src;
    b.append(s);
  }
  const k = document.createElement("span");
  k.className = "count"; k.textContent = count;
  b.append(k);
  b.addEventListener("click", onClick);
  return b;
}

function select(repo, branch, session) {
  Object.assign(state, { repo, branch, session });
  if (repo) open.add(repo);
  if (branch) open.add(repo + "\0" + branch);
  saveOpen();
  apply();
}
function toggle(key) {
  open.has(key) ? open.delete(key) : open.add(key);
  saveOpen();
}
function saveOpen() { localStorage.setItem("open", JSON.stringify([...open].slice(-200))); }

function renderTree(f) {
  const t = $("tree");
  const out = [];
  out.push(node({ name: "All media", count: f.total, current: !state.repo, onClick: () => select("", "", "") }));
  const lbl = document.createElement("div");
  lbl.className = "label"; lbl.textContent = "Repositories";
  out.push(lbl);
  $("source").hidden = !f.sources.agentbox && !state.source;
  for (const r of f.repos || []) {
    const rOpen = open.has(r.repo);
    out.push(node({
      name: r.repo, count: r.count, chev: rOpen ? "open" : "closed",
      current: state.repo === r.repo && !state.branch,
      onClick: (e) => {
        if (state.repo === r.repo && !state.branch || e.target.classList.contains("chev")) { toggle(r.repo); renderTree(f); return; }
        select(r.repo, "", "");
      },
    }));
    if (!rOpen) continue;
    for (const b of r.branches) {
      const key = r.repo + "\0" + b.branch;
      const bOpen = open.has(key);
      out.push(node({
        cls: "branch", name: b.branch, count: b.count, title: b.worktree || b.branch,
        chev: b.sessions.length > 1 || b.sessions[0]?.session !== "(no session)" ? (bOpen ? "open" : "closed") : "",
        current: state.repo === r.repo && state.branch === b.branch && !state.session,
        onClick: (e) => {
          if (state.branch === b.branch && state.repo === r.repo && !state.session || e.target.classList.contains("chev")) { toggle(key); renderTree(f); return; }
          select(r.repo, b.branch, "");
        },
      }));
      if (!bOpen) continue;
      for (const s of b.sessions) {
        out.push(node({
          cls: "session", name: s.label || s.session, count: s.count, title: s.session,
          current: state.repo === r.repo && state.branch === b.branch && state.session === s.session,
          onClick: () => select(r.repo, b.branch, s.session),
        }));
      }
    }
  }
  t.replaceChildren(...out);
}

// ---- Grid ----

function fmtDuration(ms) {
  const s = Math.round(ms / 1000);
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}
function fmtBytes(n) {
  if (n < 1024) return n + " B";
  if (n < 1 << 20) return (n / 1024).toFixed(0) + " KB";
  if (n < 1 << 30) return (n / (1 << 20)).toFixed(1) + " MB";
  return (n / (1 << 30)).toFixed(2) + " GB";
}
function dayLabel(d) {
  const today = new Date(); today.setHours(0, 0, 0, 0);
  const day = new Date(d); day.setHours(0, 0, 0, 0);
  const diff = Math.round((today - day) / 864e5);
  if (diff === 0) return "Today";
  if (diff === 1) return "Yesterday";
  return d.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short", year: day.getFullYear() === today.getFullYear() ? undefined : "numeric" });
}
function timeLabel(d) {
  if (state.group === "day") return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  return d.toLocaleDateString(undefined, { day: "numeric", month: "short" }) + " " + d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

function groupOf(it) {
  const d = new Date(it.created);
  switch (state.group) {
    case "repo": return { key: it.repo, title: it.repo };
    case "branch": return { key: it.repo + "\0" + it.branch, title: it.branch, sub: it.repo };
    case "session": return { key: it.repo + "\0" + it.branch + "\0" + it.session, title: it.sessionLabel || it.session, sub: `${it.repo} › ${it.branch}` };
  }
  return { key: ymd(d), title: dayLabel(d) };
}

function card(it, i) {
  let el = cards.get(it.id);
  if (!el || el.dataset.sig !== sig(it)) {
    el = document.createElement("div");
    el.className = "card";
    el.tabIndex = 0;
    el.dataset.id = it.id;
    el.dataset.sig = sig(it);
    const th = document.createElement("div");
    th.className = "thumb";
    const img = document.createElement("img");
    img.loading = "lazy";
    img.decoding = "async";
    img.alt = it.caption || it.kind;
    img.src = `/api/items/${encodeURIComponent(it.id)}/thumb`;
    img.addEventListener("error", () => fallbackThumb(th, img, it), { once: true });
    th.append(img);
    if (it.kind === "recording") {
      const b = document.createElement("span");
      b.className = "badge";
      b.innerHTML = '<svg viewBox="0 0 10 10"><path d="M2 1l7 4-7 4z"/></svg>';
      b.append(it.durationMs ? fmtDuration(it.durationMs) : "video");
      th.append(b);
    }
    if (it.source === "agentbox") {
      const t = document.createElement("span");
      t.className = "tag"; t.textContent = "AgentBox";
      th.append(t);
    }
    const cap = document.createElement("div");
    cap.className = "cap" + (it.caption ? "" : " none");
    cap.textContent = it.caption || (it.kind === "recording" ? "Recording" : "Screenshot");
    const meta = document.createElement("div");
    meta.className = "meta";
    const where = document.createElement("span");
    where.textContent = state.group === "branch" || state.group === "session" ? it.sessionLabel || it.session : it.branch;
    where.title = `${it.repo} › ${it.branch} › ${it.sessionLabel || it.session}`;
    const when = document.createElement("span");
    when.className = "when";
    when.textContent = timeLabel(new Date(it.created));
    meta.append(where, when);
    el.append(th, cap, meta);
    cards.set(it.id, el);
  }
  el.dataset.index = i;
  return el;
}
// sig changes when what a card shows does, so it's rebuilt then.
const sig = (it) => [it.caption, it.branch, it.sessionLabel, it.durationMs, state.group].join("\x01");

// A recording without a poster frame (no ffmpeg on this machine) shows its
// own first frame; an image Go couldn't thumbnail shows itself.
function fallbackThumb(th, img, it) {
  const src = `/api/items/${encodeURIComponent(it.id)}/file`;
  if (it.kind === "recording") {
    const v = document.createElement("video");
    v.muted = true; v.preload = "metadata"; v.playsInline = true;
    v.src = src + "#t=0.5";
    img.replaceWith(v);
  } else {
    img.src = src;
  }
}

function renderGrid() {
  const grid = $("grid");
  const out = [];
  let cur = null, section = null;
  items.forEach((it, i) => {
    const g = groupOf(it);
    if (!cur || g.key !== cur) {
      cur = g.key;
      const h = document.createElement("div");
      h.className = "group-head";
      h.textContent = g.title;
      if (g.sub) {
        const s = document.createElement("span");
        s.className = "sub"; s.textContent = g.sub;
        h.append(s);
      }
      section = document.createElement("div");
      section.className = "grid";
      out.push(h, section);
    }
    section.append(card(it, i));
  });
  grid.replaceChildren(...out);
  // Forget cards no longer shown, so the map can't grow forever.
  const shown = new Set(items.map((it) => it.id));
  for (const id of cards.keys()) if (!shown.has(id)) cards.delete(id);

  $("empty").hidden = total > 0;
  const filtered = state.q || state.kind || state.when || state.repo || state.source;
  $("status").textContent = total === 0
    ? (filtered ? "Nothing matches these filters." : "")
    : `${total.toLocaleString()} item${total === 1 ? "" : "s"}` + (items.length < total ? ` · showing ${items.length.toLocaleString()}` : "");
  if (total === 0 && filtered) $("empty").hidden = true;
}

new IntersectionObserver((es) => { if (es.some((e) => e.isIntersecting)) loadMore(); }, { root: $("main"), rootMargin: "1200px" })
  .observe($("more"));

$("grid").addEventListener("click", (e) => {
  const c = e.target.closest(".card");
  if (c) lb.show(Number(c.dataset.index));
});
$("grid").addEventListener("keydown", (e) => {
  const c = e.target.closest(".card");
  if (c && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); lb.show(Number(c.dataset.index)); }
});

// ---- Lightbox ----

const lb = {
  index: -1,
  id: "",
  async show(i) {
    if (i < 0) return;
    if (i >= items.length) {
      if (items.length < total) await loadMore();
      if (i >= items.length) return;
    }
    this.index = i;
    const it = items[i];
    this.id = it.id;
    $("lb").hidden = false;
    document.body.style.overflow = "hidden";
    const file = `/api/items/${encodeURIComponent(it.id)}/file`;
    const m = $("lb-media");
    let el;
    if (it.kind === "recording") {
      el = document.createElement("video");
      el.controls = true; el.autoplay = true; el.preload = "auto"; el.playsInline = true;
      el.src = file;
    } else {
      el = document.createElement("img");
      el.src = file;
      el.alt = it.caption || "Screenshot";
      el.addEventListener("click", () => el.classList.toggle("actual"));
    }
    m.replaceChildren(el);
    $("lb-caption").textContent = it.caption || (it.kind === "recording" ? "Recording" : "Screenshot");
    $("lb-pos").textContent = `${(i + 1).toLocaleString()} of ${total.toLocaleString()}`;
    const rows = [
      ["Repository", it.repo],
      ["Branch", it.branch],
      ["Worktree", it.worktree, true],
      [it.source === "agentbox" ? "Agent" : "Session", it.sessionLabel && it.sessionLabel !== it.session ? `${it.sessionLabel}` : it.session],
      ["Tool", it.tool],
      ["Taken", new Date(it.created).toLocaleString()],
      ["Size", [it.width && it.height ? `${it.width}×${it.height}` : "", it.durationMs ? fmtDuration(it.durationMs) : "", fmtBytes(it.bytes)].filter(Boolean).join(" · ")],
      ["File", it.path, true],
    ];
    const dl = $("lb-meta");
    dl.replaceChildren();
    for (const [k, v, code] of rows) {
      if (!v) continue;
      const dt = document.createElement("dt"); dt.textContent = k;
      const dd = document.createElement("dd");
      if (code) { const c = document.createElement("code"); c.textContent = v; dd.append(c); } else dd.textContent = v;
      dl.append(dt, dd);
    }
    $("lb-copy").disabled = !it.path;
    $("lb-download").href = file + "?download=1";
    $("lb-open").href = file;
    $("lb-prev").disabled = i === 0;
    $("lb-next").disabled = i >= total - 1;
    cards.get(it.id)?.scrollIntoView({ block: "nearest" });
  },
  close() {
    $("lb").hidden = true;
    $("lb-media").replaceChildren();
    document.body.style.overflow = "";
    const c = cards.get(this.id);
    this.index = -1;
    c?.focus({ preventScroll: true });
  },
  // sync follows the item shown after a refresh moved or removed it.
  sync() {
    const i = items.findIndex((it) => it.id === this.id);
    if (i >= 0) { this.index = i; $("lb-pos").textContent = `${(i + 1).toLocaleString()} of ${total.toLocaleString()}`; }
    else if (items.length) this.show(Math.min(this.index, items.length - 1));
    else this.close();
  },
  current() { return items[this.index]; },
};

$("lb-prev").onclick = () => lb.show(lb.index - 1);
$("lb-next").onclick = () => lb.show(lb.index + 1);
$("lb-close").onclick = () => lb.close();
$("lb-stage").addEventListener("click", (e) => { if (e.target === e.currentTarget) lb.close(); });
$("lb-copy").onclick = copyPath;
$("lb-delete").onclick = remove;

async function copyPath() {
  const it = lb.current();
  if (!it?.path) return;
  try {
    await navigator.clipboard.writeText(it.path);
    toast("Path copied");
  } catch {
    toast("Couldn't copy: " + it.path);
  }
}

async function remove() {
  const it = lb.current();
  if (!it) return;
  const d = $("confirm");
  $("confirm-text").textContent = `${it.caption || it.kind} — ${it.repo} › ${it.branch}. ` +
    (it.source === "agentbox" ? "It is deleted from the agent's media in AgentBox too." : "The file is deleted from disk.");
  d.returnValue = "";
  d.showModal();
  await new Promise((r) => d.addEventListener("close", r, { once: true }));
  if (d.returnValue !== "ok") return;
  const r = await fetch(`/api/items/${encodeURIComponent(it.id)}`, { method: "DELETE", headers: { "X-Machines": "1" } });
  if (!r.ok) { toast("Couldn't delete: " + (await r.text())); return; }
  toast("Deleted");
  const at = lb.index;
  items.splice(at, 1);
  total--;
  cards.delete(it.id);
  renderGrid();
  if (items.length) lb.show(Math.min(at, items.length - 1)); else lb.close();
  reload({ keep: true });
}

function toast(text) {
  const t = $("toast");
  t.textContent = text;
  t.classList.add("show");
  clearTimeout(toast.t);
  toast.t = setTimeout(() => t.classList.remove("show"), 1800);
}

document.addEventListener("keydown", (e) => {
  if ($("confirm").open) return;
  if (!$("mv").hidden) {
    // In control, every key is the machine's.
    if (mv.control) return;
    if (e.key === "Escape" && !document.fullscreenElement) { e.preventDefault(); mv.close(); }
    else if (e.key === "f" && !e.ctrlKey && !e.metaKey) { e.preventDefault(); mv.fullscreen(); }
    return;
  }
  const typing = e.target.matches?.("input, select, textarea");
  if (!$("lb").hidden) {
    if (e.key === "Escape") { e.preventDefault(); lb.close(); }
    else if (e.key === "ArrowLeft" && !(e.target instanceof HTMLVideoElement)) { e.preventDefault(); lb.show(lb.index - 1); }
    else if (e.key === "ArrowRight" && !(e.target instanceof HTMLVideoElement)) { e.preventDefault(); lb.show(lb.index + 1); }
    else if (e.key === "Delete" || e.key === "Backspace") { e.preventDefault(); remove(); }
    else if (e.key === "c" && !e.ctrlKey && !e.metaKey) copyPath();
    else if (e.key === "d" && !e.ctrlKey && !e.metaKey) $("lb-download").click();
    else if (e.key === " " && !(e.target instanceof HTMLVideoElement)) {
      const v = $("lb-media").querySelector("video");
      if (v) { e.preventDefault(); v.paused ? v.play() : v.pause(); }
    }
    return;
  }
  if (e.key === "/" && !typing) { e.preventDefault(); $("q").focus(); }
});

// ---- Controls ----

function apply() {
  writeURL();
  $("main").scrollTop = 0;
  reload().catch((e) => toast(String(e.message || e)));
}

function syncControls() {
  $("q").value = state.q;
  for (const b of $("kind").children) b.setAttribute("aria-pressed", String(b.dataset.v === state.kind));
  $("when").value = state.when;
  $("range").hidden = state.when !== "custom";
  $("from").value = state.from;
  $("to").value = state.to;
  $("group").value = state.group || "day";
  $("source").value = state.source;
}

let qTimer;
$("q").addEventListener("input", () => {
  clearTimeout(qTimer);
  qTimer = setTimeout(() => { state.q = $("q").value.trim(); apply(); }, 150);
});
$("q").addEventListener("keydown", (e) => { if (e.key === "Escape") { $("q").value = ""; $("q").dispatchEvent(new Event("input")); $("q").blur(); } });
$("kind").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.kind = b.dataset.v;
  syncControls(); apply();
});
$("when").addEventListener("change", () => { state.when = $("when").value; syncControls(); apply(); });
$("from").addEventListener("change", () => { state.from = $("from").value; apply(); });
$("to").addEventListener("change", () => { state.to = $("to").value; apply(); });
$("group").addEventListener("change", () => { state.group = $("group").value; apply(); });
$("source").addEventListener("change", () => { state.source = $("source").value; apply(); });

const zoom = localStorage.getItem("zoom");
if (zoom) $("zoom").value = zoom;
const setZoom = () => document.documentElement.style.setProperty("--card", $("zoom").value + "px");
setZoom();
$("zoom").addEventListener("input", () => { setZoom(); localStorage.setItem("zoom", $("zoom").value); });

$("theme").addEventListener("click", () => {
  const dark = getComputedStyle(document.documentElement).colorScheme === "dark";
  const next = dark ? "light" : "dark";
  document.documentElement.dataset.theme = next;
  localStorage.setItem("theme", next);
});

// ---- Machines ----

let machines = [];
let machinesError = "";

function machineTitle(m) {
  const base = m.worktree.split("/").filter(Boolean).pop() || m.name;
  if (m.repo && m.branch) return `${m.repo.split("/").pop()} · ${m.branch}`;
  return m.branch ? `${base} · ${m.branch}` : base;
}
function fmtUptime(started) {
  const s = Math.max(0, Math.floor((Date.now() - new Date(started)) / 1000));
  if (s < 60) return "up " + s + "s";
  const m = Math.floor(s / 60);
  if (m < 60) return "up " + m + "m";
  const h = Math.floor(m / 60);
  if (h < 24) return `up ${h}h ${m % 60}m`;
  return `up ${Math.floor(h / 24)}d ${h % 24}h`;
}
function machineSub(m) {
  if (m.busy) return m.busy[0].toUpperCase() + m.busy.slice(1) + "…";
  if (m.error) return m.error;
  if (!m.running) return "Stopped";
  const parts = [];
  if (m.started) parts.push(fmtUptime(m.started));
  if (m.memory) parts.push(m.limit ? `${m.memory} of ${m.limit}` : m.memory);
  return parts.join(" · ") || "Running";
}
function dotClass(m) { return "dot" + (m.busy ? " busy" : m.running ? " on" : ""); }

async function loadMachines() {
  try {
    const r = await getJSON("/api/machines");
    machines = r.machines || [];
    machinesError = r.error || "";
  } catch (e) {
    machinesError = String(e.message || e);
  }
  renderMachines();
  mv.sync();
}

function renderMachines() {
  const out = [];
  const lbl = document.createElement("div");
  lbl.className = "label"; lbl.textContent = "Machines";
  out.push(lbl);
  if (machinesError || !machines.length) {
    const p = document.createElement("div");
    p.className = "none" + (machinesError ? " err" : "");
    p.textContent = machinesError || "None yet: an AI tool's machine_start makes one for its worktree.";
    out.push(p);
  }
  for (const m of machines) {
    const row = document.createElement("div");
    row.className = "machine";
    if (mv.name === m.name) row.setAttribute("aria-current", "true");
    const dot = document.createElement("span");
    dot.className = dotClass(m);
    const open = document.createElement("button");
    open.className = "open";
    open.title = m.running ? `Watch ${m.worktree} live` : m.worktree;
    const t = document.createElement("span");
    t.className = "title"; t.textContent = machineTitle(m);
    const sub = document.createElement("span");
    sub.className = "sub" + (m.error && !m.busy ? " err" : ""); sub.textContent = machineSub(m);
    open.append(t, sub);
    open.addEventListener("click", () => { location.hash = "machine=" + encodeURIComponent(m.name); });
    row.append(dot, open, powerButton(m, "power"));
    out.push(row);
  }
  $("machines").replaceChildren(...out);
}

function powerButton(m, cls) {
  const b = document.createElement("button");
  b.className = cls;
  setPower(b, m);
  return b;
}
function setPower(b, m) {
  b.textContent = m.running ? "Stop" : "Start";
  b.disabled = !!m.busy;
  b.onclick = (e) => { e.stopPropagation(); power(m, m.running ? "stop" : "start"); };
}

async function power(m, op) {
  const r = await fetch(`/api/machines/${encodeURIComponent(m.name)}/${op}`, { method: "POST", headers: { "X-Machines": "1" } });
  if (!r.ok) toast(await r.text());
  loadMachines();
}

// mv is the live view of one machine, opened by #machine=<name>.
const mv = {
  name: "",
  session: null,
  control: false,
  connected: false,
  open(name) {
    if (this.name === name) return;
    this.close({ keepHash: true });
    this.name = name;
    $("mv").hidden = false;
    document.body.style.overflow = "hidden";
    this.setControl(false);
    this.sync();
    renderMachines();
  },
  machine() { return machines.find((m) => m.name === this.name); },
  // sync follows the machine: connects when it runs, says why not otherwise.
  sync() {
    if (!this.name) return;
    const m = this.machine();
    $("mv-name").textContent = m ? machineTitle(m) : this.name;
    $("mv-sub").textContent = m ? m.worktree : "";
    $("mv-dot").className = m ? dotClass(m) : "dot";
    $("mv-power").hidden = !m;
    if (m) setPower($("mv-power"), m);
    const msg = $("mv-msg");
    if (m && m.running) {
      msg.hidden = true;
      if (!this.session) this.connect();
    } else {
      this.disconnect();
      msg.hidden = false;
      msg.textContent = !m ? (machinesError || "There's no machine called " + this.name + ".")
        : m.busy ? m.busy[0].toUpperCase() + m.busy.slice(1) + "…"
        : m.error || "This machine is stopped. Start it to watch its desktop.";
    }
    this.state();
  },
  async connect() {
    const name = this.name;
    this.session = {}; // connecting
    let view;
    try {
      view = await import("./vnc.js");
    } catch (e) {
      this.session = null;
      toast("Couldn't load the viewer: " + (e.message || e));
      return;
    }
    if (this.name !== name || !this.session) return;
    this.session = view.open($("mv-view"), name, (connected) => {
      this.connected = connected;
      this.state();
      if (connected) this.session?.setControl(this.control);
    });
  },
  disconnect() {
    this.session?.close?.();
    this.session = null;
    this.connected = false;
    $("mv-view").replaceChildren();
  },
  state() {
    const m = this.machine();
    $("mv-state").textContent = !m || !m.running ? "" : this.connected ? (this.control ? "Live · you're in control" : "Live · view only") : "Connecting…";
  },
  setControl(on) {
    this.control = on;
    $("mv-control").setAttribute("aria-pressed", String(on));
    $("mv-control").textContent = on ? "Release control" : "Take control";
    $("mv-paste").disabled = !on;
    $("mv-cover").hidden = on;
    this.session?.setControl?.(on);
    this.state();
  },
  fullscreen() {
    if (document.fullscreenElement) document.exitFullscreen();
    else $("mv-screen").requestFullscreen?.().catch(() => {});
  },
  close({ keepHash = false } = {}) {
    if (!this.name) return;
    this.disconnect();
    this.name = "";
    this.setControl(false);
    $("mv").hidden = true;
    document.body.style.overflow = "";
    if (document.fullscreenElement) document.exitFullscreen();
    if (!keepHash && location.hash) history.replaceState(null, "", location.pathname + location.search);
    renderMachines();
  },
};

$("mv-control").addEventListener("click", () => mv.setControl(!mv.control));
$("mv-paste").addEventListener("click", async () => {
  try {
    await mv.session?.paste?.();
    toast("Sent your clipboard: paste it on the machine");
  } catch {
    toast("The browser didn't let the page read your clipboard");
  }
});
$("mv-full").addEventListener("click", () => mv.fullscreen());
$("mv-close").addEventListener("click", () => mv.close());

function followHash() {
  const name = new URLSearchParams(location.hash.slice(1)).get("machine");
  if (name) mv.open(name);
  else mv.close({ keepHash: true });
}
window.addEventListener("hashchange", followHash);

// Machines are polled: their uptime and memory change all the time, and
// starting one is the runtime's, not the page's, to report.
setInterval(() => { if (!document.hidden) loadMachines(); }, 3000);
document.addEventListener("visibilitychange", () => { if (!document.hidden) loadMachines(); });

// ---- Live updates ----

function listen() {
  const es = new EventSource("/api/events");
  let first = true;
  es.addEventListener("hello", () => {
    // After a reconnect (the server restarted), catch up.
    if (!first) reload({ keep: true }).catch(() => {});
    first = false;
  });
  let t;
  es.addEventListener("change", () => {
    clearTimeout(t);
    t = setTimeout(() => reload({ keep: true }).catch(() => {}), 150);
  });
}

readURL();
syncControls();
reload().catch((e) => toast(String(e.message || e)));
listen();
followHash();
loadMachines();
