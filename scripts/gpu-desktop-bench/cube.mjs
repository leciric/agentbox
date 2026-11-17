// node cube.mjs <port> <secs>: prints, as JSON, the renderer and fps of the cube.html page
// open in the Chromium whose DevTools listen on <port>. Used by bench.sh.
const [port, secs] = [process.argv[2], +(process.argv[3] || 5)];
const sleep = ms => new Promise(r => setTimeout(r, ms));
let list;
for (let i = 0; i < 100; i++) { try { list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json(); if (list.some(t => t.type === 'page' && t.url.includes('cube'))) break } catch {} await sleep(100) }
const page = list.find(t => t.type === 'page' && t.url.includes('cube'));
const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise(r => ws.onopen = r);
let id = 0; const pending = {};
ws.onmessage = e => { const m = JSON.parse(e.data); if (pending[m.id]) { pending[m.id](m); delete pending[m.id] } };
const ev = expr => new Promise(r => { pending[++id] = r; ws.send(JSON.stringify({ id, method: 'Runtime.evaluate', params: { expression: expr, awaitPromise: true, returnByValue: true } })) }).then(m => m.result.result.value);
await sleep(2000);
const renderer = await ev('window.renderer');
const r = await ev(`measure(${secs})`);
console.log(JSON.stringify({ renderer, ...r }));
process.exit(0);
