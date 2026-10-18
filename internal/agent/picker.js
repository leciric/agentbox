// The element picker (pick.go): an overlay that outlines the element under
// the pointer and, on a click, describes that element for the daemon to
// collect from globalThis.__agentboxPicker.result. It runs in an isolated
// world of the page, so the page's scripts never see it, and draws in a
// closed shadow root, so the page's styles never reach it. Escape, or the
// daemon calling cancel(), takes it away with nothing picked.
(hint) => {
  globalThis.__agentboxPicker?.cancel();
  const doc = document;
  const host = doc.createElement('agentbox-picker');
  const shadow = host.attachShadow({ mode: 'closed' });
  shadow.innerHTML = `<style>
    :host { all: initial; position: fixed; inset: 0; z-index: 2147483647; pointer-events: none; }
    .box { position: fixed; display: none; box-sizing: border-box; border: 2px solid #8b7cf6; border-radius: 3px;
      background: rgb(139 124 246 / 0.14); box-shadow: 0 0 0 1px rgb(255 255 255 / 0.5); }
    .tag { position: absolute; left: -2px; bottom: 100%; margin-bottom: 4px; max-width: 420px; overflow: hidden;
      white-space: nowrap; text-overflow: ellipsis; padding: 2px 6px; border-radius: 4px; background: #6d5ce8; color: #fff;
      font: 600 11px/16px ui-monospace, SFMono-Regular, Menlo, monospace; }
    .tag.below { bottom: auto; top: 100%; margin: 4px 0 0; }
    .hint { position: fixed; left: 50%; top: 12px; transform: translateX(-50%); padding: 6px 12px; border-radius: 999px;
      background: rgb(20 18 32 / 0.88); color: #fff; font: 500 12.5px/18px system-ui, sans-serif;
      box-shadow: 0 4px 16px rgb(0 0 0 / 0.3); }
  </style><div class="box"><div class="tag"></div></div><div class="hint"></div>`;
  const box = shadow.querySelector('.box');
  const tag = shadow.querySelector('.tag');
  shadow.querySelector('.hint').textContent = hint;
  doc.documentElement.append(host);

  const picker = { result: undefined, cancel: () => finish(null) };
  globalThis.__agentboxPicker = picker;
  let current = null;

  const targetOf = (event) => {
    const el = event.composedPath().find((n) => n instanceof Element);
    return el && el !== host ? el : null;
  };
  const draw = () => {
    if (!current || !current.isConnected) {
      box.style.display = 'none';
      return;
    }
    const r = current.getBoundingClientRect();
    Object.assign(box.style, { display: 'block', left: `${r.left}px`, top: `${r.top}px`, width: `${r.width}px`, height: `${r.height}px` });
    tag.textContent = `${label(current)}  ${Math.round(r.width)}×${Math.round(r.height)}`;
    tag.classList.toggle('below', r.top < 24);
  };
  const move = (event) => {
    const el = targetOf(event);
    if (el && el !== current) {
      current = el;
      draw();
    }
  };
  // Nothing the pointer does reaches the page while picking: a click picks.
  const swallow = (event) => {
    event.preventDefault();
    event.stopImmediatePropagation();
  };
  const click = (event) => {
    swallow(event);
    const el = targetOf(event) ?? current;
    if (event.button === 0 && el) finish(describe(el));
  };
  const key = (event) => {
    if (event.key !== 'Escape') return;
    swallow(event);
    finish(null);
  };
  const listeners = [
    ['pointermove', move],
    ['mouseover', move],
    ['pointerdown', swallow],
    ['mousedown', swallow],
    ['pointerup', swallow],
    ['mouseup', swallow],
    ['dblclick', swallow],
    ['auxclick', swallow],
    ['contextmenu', swallow],
    ['click', click],
    ['keydown', key],
    ['scroll', draw],
    ['resize', draw],
  ];
  for (const [type, fn] of listeners) addEventListener(type, fn, { capture: true });

  function finish(result) {
    if (picker.done) return;
    picker.done = true;
    for (const [type, fn] of listeners) removeEventListener(type, fn, { capture: true });
    host.remove();
    // The crop is taken once the overlay is off the screen: after a frame.
    const done = () => (picker.result = result);
    if (result) requestAnimationFrame(() => setTimeout(done, 30));
    else done();
  }

  // label is how the overlay names an element: its tag, id and first classes.
  function label(el) {
    let s = el.localName;
    if (el.id) s += `#${el.id}`;
    for (const c of [...el.classList].slice(0, 2)) s += `.${c}`;
    return s;
  }

  function describe(el) {
    const r = el.getBoundingClientRect();
    return {
      url: location.href,
      title: doc.title,
      selector: selectorOf(el),
      tag: el.localName,
      html: htmlOf(el),
      text: (el.innerText ?? el.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 300),
      styles: stylesOf(el),
      box: { x: Math.round(r.left), y: Math.round(r.top), width: Math.round(r.width), height: Math.round(r.height) },
      viewport: { width: innerWidth, height: innerHeight },
      scroll: { x: scrollX, y: scrollY },
    };
  }

  // selectorOf is a CSS selector that finds el alone in its document (or
  // shadow root): an id when it has a unique one, else a path of tags, test
  // ids, a couple of classes and :nth-of-type, as short as stays unique.
  function selectorOf(el) {
    const root = el.getRootNode();
    const unique = (sel) => {
      try {
        return root.querySelectorAll(sel).length === 1;
      } catch {
        return false;
      }
    };
    const parts = [];
    for (let e = el; e && e.nodeType === 1 && e !== doc.documentElement; e = e.parentElement) {
      if (e.id && unique(`#${CSS.escape(e.id)}`)) {
        parts.unshift(`#${CSS.escape(e.id)}`);
        break;
      }
      let part = e.localName;
      const testId = e.getAttribute('data-testid');
      if (testId) part += `[data-testid="${CSS.escape(testId)}"]`;
      else
        for (const c of [...e.classList].filter((c) => c.length <= 32).slice(0, 2)) part += `.${CSS.escape(c)}`;
      const parent = e.parentElement;
      if (parent) {
        const same = [...parent.children].filter((c) => c.localName === e.localName);
        if (same.length > 1) part += `:nth-of-type(${same.indexOf(e) + 1})`;
      }
      parts.unshift(part);
      if (unique(parts.join(' > '))) break;
    }
    return parts.join(' > ');
  }

  // htmlOf is el's outer HTML, cut down to what a model needs to recognise
  // it: long text and attribute values shortened, SVG paths dropped, and the
  // whole at most 3,000 characters.
  function htmlOf(el) {
    const clone = el.cloneNode(true);
    const walker = doc.createTreeWalker(clone, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    for (let n = walker.currentNode; n; n = walker.nextNode()) {
      if (n.nodeType === 3) {
        const text = n.nodeValue.replace(/\s+/g, ' ');
        n.nodeValue = text.length > 120 ? `${text.slice(0, 120)}…` : text;
        continue;
      }
      for (const a of [...n.attributes]) {
        if (n.namespaceURI === 'http://www.w3.org/2000/svg' && a.name === 'd') n.setAttribute('d', '…');
        else if (a.value.length > 160) n.setAttribute(a.name, `${a.value.slice(0, 160)}…`);
      }
    }
    const html = clone.outerHTML;
    return html.length > 3000 ? `${html.slice(0, 3000)}…` : html;
  }

  // stylesOf is el's computed styles that say how it looks and sits, leaving
  // out what is at its default.
  function stylesOf(el) {
    const css = getComputedStyle(el);
    const layout = ['gap', 'flex-direction', 'align-items', 'justify-content', 'grid-template-columns'];
    const out = [];
    for (const name of [
      'display', 'position', 'width', 'height', 'margin', 'padding', 'gap',
      'flex-direction', 'align-items', 'justify-content', 'grid-template-columns',
      'color', 'background-color', 'background-image', 'font-family', 'font-size', 'font-weight',
      'line-height', 'letter-spacing', 'text-align', 'text-transform', 'border', 'border-radius',
      'box-shadow', 'opacity', 'transform', 'overflow', 'cursor', 'z-index',
    ]) {
      const value = css.getPropertyValue(name).trim();
      if (!value || /^(none|normal|auto|0px|visible|static|start|rgba\(0, 0, 0, 0\)|0px none .*)$/.test(value)) continue;
      if (name === 'opacity' && value === '1') continue;
      if (layout.includes(name) && !/flex|grid/.test(css.display)) continue;
      out.push({ name, value });
    }
    return out;
  }
};
