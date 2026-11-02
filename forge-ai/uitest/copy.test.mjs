import assert from "node:assert/strict";
import { test } from "node:test";
import { askCopy } from "../ui/copy.js";

test("without cards.edit the copy never offers a change", () => {
  const copy = askCopy(["cards.push"]);
  assert.doesNotMatch(copy.hint + copy.placeholder, /change/);
});

test("with cards.edit the copy offers a change to the open card", () => {
  const copy = askCopy(["cards.edit"]);
  assert.match(copy.hint, /Or open a card and ask for a change\./);
  assert.match(copy.placeholder, /or for a change to the open card/);
});
