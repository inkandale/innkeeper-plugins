// A reply is the assistant's answer as it streams in: the text so far and the
// conversation it belongs to.
export const newReply = (conversation) => ({ text: "", conversation });

/** applyEvent folds one streamed event into the reply. */
export function applyEvent(reply, event) {
  if (event?.reset) return { ...reply, text: "" };
  if (typeof event?.delta === "string") return { ...reply, text: reply.text + event.delta };
  // Kept at once so Retry after a first-turn failure stays in the same conversation.
  if (Number.isInteger(event?.conversation)) return { ...reply, conversation: event.conversation };
  return reply;
}

/** finishReply swaps in the final result: a reply's blocks are never streamed, so they only arrive here. */
export const finishReply = (_reply, result) => ({ text: "", conversation: result.conversation, blocks: result.blocks ?? [] });

/** proposeParams is Send to Review's request for a proposal block; one saved before snapshots sends none, and Review falls back to its revision. */
export const proposeParams = (p) => ({ revision: p.revision, ...(p.snapshot ? { snapshot: p.snapshot } : {}), proposal: p.fields });
