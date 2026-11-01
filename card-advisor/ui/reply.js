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

/** finishReply swaps in the final result: a recommend message is never streamed, so its text only arrives here. */
export const finishReply = (_reply, result) => ({ text: result.text, conversation: result.conversation, picks: result.picks });
