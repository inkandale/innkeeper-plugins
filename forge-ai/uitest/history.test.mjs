import assert from "node:assert/strict";
import { test } from "node:test";
import { groupConversations } from "../ui/history.js";

const now = new Date(2026, 10, 2, 15, 0);
const daysAgo = (days) => new Date(2026, 10, 2 - days, 12).toISOString();
const list = [
  { id: 1, title: "A slow-burn rival", updated_at: daysAgo(0) },
  { id: 2, title: "Pirates", updated_at: daysAgo(1) },
  { id: 3, title: "Rival gangs", updated_at: daysAgo(3) },
  { id: 4, title: "Old chat", updated_at: daysAgo(30) },
];
const ids = (groups) => groups.map((g) => [g.label, g.items.map((c) => c.id)]);

test("groups conversations by age, newest first", () => {
  assert.deepEqual(ids(groupConversations(list, "", now)), [["Today", [1]], ["Yesterday", [2]], ["This week", [3]], ["Older", [4]]]);
});

test("the filter matches titles in any case and drops empty groups", () => {
  assert.deepEqual(ids(groupConversations(list, " RIVAL ", now)), [["Today", [1]], ["This week", [3]]]);
  assert.deepEqual(groupConversations(list, "zzz", now), []);
});
