import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { after, before, test } from "node:test";

const port = 3999;
let server;

before(async () => {
  server = spawn(process.execPath, ["server.mjs"], { env: { ...process.env, PORT: String(port) }, stdio: ["ignore", "pipe", "inherit"] });
  await new Promise((resolve) => server.stdout.once("data", resolve));
});
after(() => server.kill());

const get = (path, options) => fetch(`http://127.0.0.1:${port}${path}`, options);

test("the home route says hello", async () => {
  const body = await (await get("/")).json();
  assert.equal(body.message, "hello");
});

test("the login page asks for a name", async () => {
  const html = await (await get("/login")).text();
  assert.match(html, /Not signed in/);
});

test("signing in keeps the name in a cookie", async () => {
  const res = await get("/login?user=alice", { redirect: "manual" });
  assert.equal(res.status, 303);
  assert.match(res.headers.get("set-cookie"), /user=alice/);
});
