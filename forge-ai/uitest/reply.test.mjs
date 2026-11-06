import assert from "node:assert/strict";
import { test } from "node:test";
import { applyEvent, finishReply, newReply, proposeParams } from "../ui/reply.js";

test("deltas append to the reply", () => {
  const reply = [{ delta: "Hel" }, { delta: "lo" }].reduce(applyEvent, newReply(0));
  assert.equal(reply.text, "Hello");
});

test("reset clears the text but keeps the conversation", () => {
  const reply = [{ conversation: 7 }, { delta: "draft" }, { reset: true }, { delta: "final" }].reduce(applyEvent, newReply(0));
  assert.deepEqual(reply, { text: "final", conversation: 7 });
});

test("an unknown event changes nothing", () => {
  const start = newReply(3);
  assert.deepEqual(applyEvent(start, { other: 1 }), start);
});

test("the result replaces streamed text with its blocks", () => {
  const streamed = applyEvent(newReply(0), { delta: "thinking aloud" });
  const blocks = [{ type: "picks", picks: [{ card_id: 1, name: "A", reason: "r" }] }];
  assert.deepEqual(finishReply(streamed, { conversation: 5, blocks }), { text: "", conversation: 5, blocks });
});

test("Send to Review names the snapshot the proposal was written against", () => {
  const proposal = { type: "proposal", summary: "s", fields: { name: "Bob" }, card: 7, revision: 11, snapshot: 5 };
  assert.deepEqual(proposeParams(proposal), { revision: 11, snapshot: 5, proposal: { name: "Bob" } });
});

test("a proposal saved without a snapshot sends none, so Review reads its revision", () => {
  const proposal = { type: "proposal", summary: "s", fields: { name: "Bob" }, card: 7, revision: 11 };
  assert.deepEqual(proposeParams(proposal), { revision: 11, proposal: { name: "Bob" } });
});
