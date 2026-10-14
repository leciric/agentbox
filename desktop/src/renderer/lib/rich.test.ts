// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { FRAME_MAX, FRAME_MIN, framedPage, frameHeight, injectHead, pageOf, richKind, svgDocument, svgSize, svgUrl, titleOf } from './rich.ts';

test('mermaid and svg blocks are drawn; html only when its fence asks for a preview', () => {
  assert.deepEqual(richKind('mermaid'), { kind: 'mermaid', drawn: true });
  assert.deepEqual(richKind('SVG'), { kind: 'svg', drawn: true });
  assert.deepEqual(richKind('html'), { kind: 'html', drawn: false });
  assert.deepEqual(richKind('html', 'preview'), { kind: 'html', drawn: true });
  assert.deepEqual(richKind('html', 'title="x" render'), { kind: 'html', drawn: true });
  assert.equal(richKind('ts'), null);
  assert.equal(richKind(undefined), null);
});

test('an svg gets its namespace, which an <img> needs, once', () => {
  assert.equal(svgDocument('<svg viewBox="0 0 1 1"></svg>'), '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"></svg>');
  const ok = '<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>';
  assert.equal(svgDocument(ok), ok);
  assert.ok(svgUrl('<svg/>').startsWith('data:image/svg+xml;charset=utf-8,%3Csvg%20xmlns'));
});

test('an svg is as big as it says, or as its viewBox', () => {
  assert.deepEqual(svgSize('<svg width="120" height="40px">'), { width: 120, height: 40 });
  assert.deepEqual(svgSize('<svg width="100%" viewBox="0 0 640 480" style="max-width: 640px">'), { width: 640, height: 480 });
  assert.equal(svgSize('<svg>'), null);
  assert.equal(svgSize('no svg'), null);
});

test('a frame takes the height its page reports, within bounds', () => {
  assert.equal(frameHeight(10), FRAME_MIN);
  assert.equal(frameHeight(200.2), 201);
  assert.equal(frameHeight(5000), FRAME_MAX);
  assert.equal(frameHeight('300'), null);
  assert.equal(frameHeight(Number.NaN), null);
});

test('the chat’s head goes inside the page’s head, after its doctype', () => {
  assert.equal(injectHead('<!DOCTYPE html><html><head><title>x</title></head></html>', 'X'), '<!DOCTYPE html><html><head>X<title>x</title></head></html>');
  assert.equal(injectHead('<!doctype html><html lang="en"><body>b</body></html>', 'X'), '<!doctype html><html lang="en">X<body>b</body></html>');
  assert.equal(injectHead('<!doctype html><p>a</p>', 'X'), '<!doctype html>X<p>a</p>');
  assert.equal(injectHead('<p>a</p>', 'X'), '<!doctype html>X<p>a</p>');
  const page = framedPage('<p>hi</p>', 'light');
  assert.ok(page.startsWith('<!doctype html><meta charset="utf-8"><meta name="color-scheme" content="light">'));
  assert.match(page, /agentboxRenderHeight/);
  assert.ok(page.endsWith('<p>hi</p>'));
});

test('a block’s title is its page’s title or heading, its diagram’s title, or what it is', () => {
  assert.equal(titleOf('html', '<title> Q3  report </title><h1>No</h1>', 'Page'), 'Q3 report');
  assert.equal(titleOf('html', '<h2>Sales <em>by</em> month</h2>', 'Page'), 'Sales by month');
  assert.equal(titleOf('mermaid', 'pie\n  title "Pets adopted"\n  "Dogs": 3', 'Diagram'), 'Pets adopted');
  assert.equal(titleOf('mermaid', 'graph TD\nA-->B', 'Diagram'), 'Diagram');
  assert.equal(titleOf('svg', '<svg/>', 'Picture'), 'Picture');
});

test('a picture published is a page holding it; a page is itself', () => {
  assert.equal(pageOf('html', '<p>x</p>', 'T'), '<p>x</p>');
  const page = pageOf('mermaid', 'graph TD', 'A <b> & "c"', '<svg viewBox="0 0 1 1"></svg>');
  assert.match(page, /<title>A &lt;b&gt; &amp; &quot;c&quot;<\/title>/);
  assert.match(page, /<body><svg xmlns="http:\/\/www.w3.org\/2000\/svg" viewBox="0 0 1 1"><\/svg><\/body>/);
  assert.match(pageOf('svg', '<svg><circle/></svg>', 'T'), /<circle\/>/);
});
