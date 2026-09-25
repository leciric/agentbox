import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { hostname } from "node:os";

const port = Number(process.env.PORT ?? 3000);

const escape = (s) => s.replace(/[&<>"]/g, (c) => `&#${c.charCodeAt(0)};`);

// A sign-in page for browser demos. The signed-in user is kept in a cookie.
function login(req, res) {
  const user = new URL(req.url, "http://localhost").searchParams.get("user");
  if (user !== null) {
    res.writeHead(303, { location: "/login", "set-cookie": `user=${encodeURIComponent(user)}; Path=/; SameSite=Lax` });
    return res.end();
  }
  const cookie = /(?:^|;\s*)user=([^;]*)/.exec(req.headers.cookie ?? "");
  const name = cookie ? decodeURIComponent(cookie[1]) : "";
  const title = name ? `Signed in as ${escape(name)}` : "Not signed in";
  res.setHeader("content-type", "text/html; charset=utf-8");
  res.end(`<!doctype html>
<title>${title}</title>
<body style="font: 22px system-ui, sans-serif; margin: 3rem">
<h1>${title}</h1>
<p>Served by ${hostname()}</p>
${name ? '<a href="/login?user=">Sign out</a>' : '<form><input name="user" placeholder="Your name" autofocus> <button>Sign in</button></form>'}
</body>
`);
}

createServer((req, res) => {
  if (req.url.startsWith("/login")) return login(req, res);
  res.setHeader("content-type", "application/json");
  res.end(
    JSON.stringify({
      host: hostname(),
      message: readFileSync(new URL("./message.txt", import.meta.url), "utf8").trim(),
    }) + "\n",
  );
}).listen(port, () => console.log(`listening on :${port}`));
