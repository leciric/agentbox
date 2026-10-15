#!/usr/bin/env node
// For development: a daemon run by hand on Linux (`agentbox daemon` with
// AGENTBOX_FRONT_END=host) doesn't open its phone port itself, the VM's
// supervisor does (internal/hostvm/chv/lan.go). This does the supervisor's
// part: while phones are on (`agentbox phone on`), it listens on their port,
// passes each request to the daemon's socket under /v1/lan/net, and reports
// the port (PUT /v1/lan/host), so `agentbox phone pair` shows a QR code that
// works.
//
// --web also lets Expo's web build (npm run web, another origin) reach it:
// it answers CORS for any origin and drops the browser's Origin and
// Sec-Fetch-* headers, which the daemon otherwise refuses a write over. Only
// for development on your own machine: a phone app never needs it.
//
// --port <n> listens on n only, without reporting it, beside a daemon that
// opens its phone port itself (on a Mac, or in host mode): for --web, then
// pair with the QR code's link on port n instead.
//
//   node scripts/lan-forward.mjs [--web] [--port <n>] [--socket <path>]
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';

const args = process.argv.slice(2);
const web = args.includes('--web');
const socketArg = args.indexOf('--socket');
const socket =
  (socketArg >= 0 && args[socketArg + 1]) ||
  process.env.AGENTBOX_SOCKET ||
  path.join(process.env.XDG_DATA_HOME || path.join(os.homedir(), '.local', 'share'), 'agentbox', 'run', 'agentbox.sock');

function daemon(method, p, body) {
  return new Promise((resolve, reject) => {
    const data = body === undefined ? undefined : JSON.stringify(body);
    const req = http.request({ socketPath: socket, method, path: p, headers: data ? { 'Content-Type': 'application/json' } : {} }, (res) => {
      let text = '';
      res.on('data', (c) => (text += c));
      res.on('end', () => (res.statusCode < 400 ? resolve(text ? JSON.parse(text) : undefined) : reject(new Error(`${res.statusCode} ${text}`))));
    });
    req.on('error', reject);
    req.end(data);
  });
}

function addresses() {
  return Object.values(os.networkInterfaces())
    .flat()
    .filter((a) => a && a.family === 'IPv4' && !a.internal)
    .map((a) => a.address);
}

function forward(req, res) {
  if (web) {
    res.setHeader('Access-Control-Allow-Origin', req.headers.origin || '*');
    // react-native-sse adds Cache-Control and X-Requested-With to the stream's.
    res.setHeader('Access-Control-Allow-Headers', 'Authorization, Content-Type, Cache-Control, X-Requested-With, Last-Event-ID');
    res.setHeader('Access-Control-Allow-Methods', 'GET, POST, PUT, PATCH, DELETE');
    if (req.method === 'OPTIONS') {
      res.writeHead(204).end();
      return;
    }
  }
  const headers = { ...req.headers, 'x-forwarded-for': req.socket.remoteAddress ?? '', 'x-forwarded-host': req.headers.host ?? '', 'x-forwarded-proto': 'http' };
  if (web) for (const h of ['origin', 'sec-fetch-site', 'sec-fetch-mode', 'sec-fetch-dest', 'referer']) delete headers[h];
  const up = http.request({ socketPath: socket, method: req.method, path: `/v1/lan/net${req.url}`, headers }, (upRes) => {
    res.writeHead(upRes.statusCode ?? 502, upRes.headers);
    upRes.pipe(res);
  });
  up.on('error', (err) => {
    if (!res.headersSent) res.writeHead(502, { 'Content-Type': 'text/plain' });
    res.end(`AgentBox's daemon doesn't answer: ${err.message}`);
  });
  req.pipe(up);
}

let server;
let port = 0;
async function tick() {
  let st;
  try {
    st = await daemon('GET', '/v1/lan');
  } catch {
    return;
  }
  if (!st.enabled) {
    if (server) {
      server.close();
      server = undefined;
      console.log(`phones: port ${port} closed`);
      await daemon('PUT', '/v1/lan/host', { port }).catch(() => {});
    }
    return;
  }
  if (server && port !== st.port) {
    server.close();
    server = undefined;
  }
  let error = '';
  if (!server) {
    port = st.port;
    server = http.createServer(forward);
    await new Promise((resolve) => {
      server.once('error', (err) => {
        error = `port ${port} can't be opened: ${err.message}`;
        server = undefined;
        resolve();
      });
      server.listen(port, '0.0.0.0', resolve);
    });
    if (server) console.log(`phones: port ${port} open, passed on to ${socket}${web ? ' (with CORS for Expo web)' : ''}`);
  }
  await daemon('PUT', '/v1/lan/host', { port, listening: !!server, error, addresses: addresses() }).catch(() => {});
}

const portArg = args.indexOf('--port');
if (portArg >= 0) {
  port = Number(args[portArg + 1]);
  http.createServer(forward).listen(port, '0.0.0.0', () => console.log(`port ${port} passed on to ${socket}${web ? ' (with CORS for Expo web)' : ''}`));
} else {
  console.log(`following ${socket}`);
  await tick();
  setInterval(tick, 3000);
}
