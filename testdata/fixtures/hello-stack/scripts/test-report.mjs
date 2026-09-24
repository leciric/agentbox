// Turns test-results/junit.xml into an HTML report in test-report/.
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";

const xml = readFileSync("test-results/junit.xml", "utf8");
const cases = [...xml.matchAll(/<testcase name="([^"]*)"[^>]*?(\/>|>([\s\S]*?)<\/testcase>)/g)].map((m) => ({
  name: m[1],
  failed: /<failure/.test(m[3] ?? ""),
}));
const passed = cases.filter((c) => !c.failed).length;
const rows = cases.map((c) => `<tr><td>${c.failed ? "❌" : "✅"}</td><td>${c.name}</td></tr>`).join("\n");

mkdirSync("test-report", { recursive: true });
writeFileSync(
  "test-report/index.html",
  `<!doctype html>
<meta charset="utf-8">
<title>hello-stack tests</title>
<body style="font: 16px system-ui, sans-serif; margin: 2rem; color: #18181b">
<h1>hello-stack tests</h1>
<p>${passed} passed, ${cases.length - passed} failed</p>
<table cellpadding="6">${rows}</table>
</body>
`,
);
console.log(`wrote test-report/index.html (${passed}/${cases.length} passed)`);
