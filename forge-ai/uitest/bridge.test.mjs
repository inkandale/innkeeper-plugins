import assert from "node:assert/strict";
import { test } from "node:test";

// bridge.js listens on the window and posts to parent at import, so stand both in before loading it.
const listeners = [];
globalThis.addEventListener = (type, fn) => type === "message" && listeners.push(fn);
globalThis.parent = { postMessage: () => {} };
const deliver = (source, data) => listeners.forEach((fn) => fn({ source, data }));
const { ask, onNotice } = await import("../ui/bridge.js");

test("a notice from the page reaches the panel by name", () => {
  const heard = [];
  onNotice((name) => heard.push(name));
  deliver(parent, { notice: "context" });
  deliver({}, { notice: "context" });
  assert.deepEqual(heard, ["context"]);
});

test("a reply is not a notice", async () => {
  const heard = [];
  onNotice((name) => heard.push(name));
  const { id, promise } = ask({ call: "host.context" });
  deliver(parent, { id, result: { library: 1 } });
  assert.deepEqual(await promise, { library: 1 });
  assert.deepEqual(heard, []);
});
