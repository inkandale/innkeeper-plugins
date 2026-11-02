import { abort, ask, callPlugin } from "./bridge.js";
import { groupConversations } from "./history.js";
import { blocks as markdownBlocks } from "./markdown.js";
import { applyEvent, finishReply, newReply } from "./reply.js";

const el = (id) => document.getElementById(id);
const state = { conversation: 0, conversations: [], library: 0, grants: [], draft: null, running: null, stopped: false, ready: false };

// Runs a click action; a rejected host call becomes a notice instead of an unhandled rejection.
const guarded = (action) => (...args) => action(...args).catch((e) => showNotice(e.message));

// Each offer shows only when the host granted it.
const offers = [
  ["Push", "push", "cards.push"],
  ["Add to collection", "add-to-collection", "cards.collect"],
];

// The Forge's names for the fields a proposal can change.
const fieldLabels = {
  name: "Name",
  description: "Description",
  personality: "Personality",
  scenario: "Scenario",
  first_mes: "First message",
  mes_example: "Example dialogue",
  system_prompt: "System prompt",
  post_history_instructions: "Post-history instructions",
  creator_notes: "Creator notes",
};

// A proposed field longer than this many lines starts collapsed.
const clampLines = 4;

function node(tag, className = "", text) {
  const n = document.createElement(tag);
  if (className) n.className = className;
  if (text !== undefined) n.textContent = text;
  return n;
}

function button(label, onClick, className = "") {
  const b = node("button", className, label);
  b.type = "button";
  b.addEventListener("click", onClick);
  return b;
}

const openCard = (card) => guarded(() => ask({ call: "host.openCard", card }).promise);
const request = (name, card, params) => guarded(() => ask({ call: "host.request", request: name, card, params }).promise);

function addSpans(parent, spans) {
  for (const s of spans) parent.append(node(s.bold ? "strong" : s.italic ? "em" : "span", "", s.text));
}

// table lays out a header and rows, filling each cell with fill(cell, value).
function table(columns, rows, fill) {
  const t = node("table");
  const head = t.createTHead().insertRow();
  for (const c of columns) fill(head.appendChild(node("th")), c);
  const body = t.createTBody();
  for (const r of rows) {
    const tr = body.insertRow();
    for (const c of r) fill(tr.insertCell(), c);
  }
  const wrap = node("div", "table");
  wrap.append(t);
  return wrap;
}

function appendMarkdown(parent, markdown) {
  for (const b of markdownBlocks(markdown)) {
    if (b.type === "table") parent.append(table(b.columns, b.rows, addSpans));
    else if (b.type === "ul") {
      const ul = node("ul");
      for (const item of b.items) addSpans(ul.appendChild(node("li")), item);
      parent.append(ul);
    } else addSpans(parent.appendChild(node("p")), b.spans);
  }
}

function showPicks(parent, picks) {
  for (const pick of picks) {
    const box = node("div", "pick");
    box.append(node("strong", "", pick.name), node("p", "", pick.reason), button("Open", openCard(pick.card_id)));
    for (const [label, name, grant] of offers) if (state.grants.includes(grant)) box.append(button(label, request(name, pick.card_id)));
    parent.append(box);
  }
}

function fillCell(td, cell) {
  if (typeof cell === "string") td.textContent = cell;
  else td.append(button(cell.name, openCard(cell.card_id), "link"));
}

function showProposal(parent, proposal) {
  // Edits reach the card only through Review, which needs cards.edit.
  if (!state.grants.includes("cards.edit")) return;
  const box = node("div", "proposal");
  box.append(node("strong", "", proposal.summary));
  for (const [key, text] of Object.entries(proposal.fields)) {
    const field = node("div", "field");
    const body = node("div", "field-text", text);
    field.append(node("div", "field-label", fieldLabels[key] ?? key), body);
    if (text.split("\n").length > clampLines || text.length > clampLines * 80) {
      body.classList.add("clamped");
      const more = button("Show all", () => (more.textContent = body.classList.toggle("clamped") ? "Show all" : "Show less"), "link");
      field.append(more);
    }
    box.append(field);
  }
  box.append(button("Send to Review", request("propose", proposal.card, { revision: proposal.revision, proposal: proposal.fields }), "primary"));
  parent.append(box);
}

const shows = {
  text: (parent, b) => appendMarkdown(parent, b.markdown),
  picks: (parent, b) => showPicks(parent, b.picks),
  table: (parent, b) => parent.append(table(b.columns, b.rows, fillCell)),
  proposal: showProposal,
};

// Scrolls to the end only if the reader was already there.
function keepAtBottom(change) {
  const log = el("log");
  const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  change();
  if (atBottom) log.scrollTop = log.scrollHeight;
}

function showMessage(role, content, blocks = []) {
  const box = node("div", `message ${role}`);
  if (role === "user") box.textContent = content;
  else {
    box.append(node("div", "who", "AI Assistant"));
    if (content) appendMarkdown(box, content);
    for (const b of blocks) if (Object.hasOwn(shows, b.type)) shows[b.type](box, b);
  }
  keepAtBottom(() => el("log").append(box));
  return box;
}

function showNotice(text, retry) {
  const box = node("div", "message notice", text);
  if (retry) {
    const b = button("Retry", () => {
      if (state.running) return;
      box.remove();
      retry();
    }, "retry");
    box.append(" ", b);
  }
  keepAtBottom(() => el("log").append(box));
}

function showHint() {
  const hint = node("p", "hint", "Describe the card you want, such as a slow-burn rival, not too dark. Or open a card and ask for a change.");
  hint.id = "hint";
  el("log").replaceChildren(hint);
}

function setBusy(busy) {
  el("send").hidden = busy;
  el("stop").hidden = !busy;
  el("log").setAttribute("aria-busy", String(busy));
  for (const id of ["history", "new"]) el(id).disabled = busy;
  if (!el("drawer").hidden) renderHistory();
}

function applyContext(context) {
  if (!context.library) throw new Error("Open a library first.");
  state.library = context.library;
  state.grants = context.grants ?? [];
  state.draft = context.draft ?? null;
  for (const [token, value] of Object.entries(context.theme ?? {})) document.documentElement.style.setProperty(token, value);
  const name = state.draft && typeof state.draft.data?.name === "string" && state.draft.data.name ? state.draft.data.name : "this card";
  el("editing").textContent = state.draft ? `Editing ${name}` : "";
  el("editing").hidden = !state.draft;
}

const refreshContext = async () => applyContext(await ask({ call: "host.context" }).promise);

/** send runs one turn. A retry resends the same text without repeating the owner's message. */
async function send(text, isRetry = false) {
  if (state.running) return;
  const log = el("log");
  document.getElementById("hint")?.remove();
  // An older failure's Retry would resend stale text once a newer turn exists.
  if (!isRetry) for (const stale of log.querySelectorAll("button.retry")) stale.remove();
  if (!isRetry) showMessage("user", text);
  const live = node("div", "message assistant streaming");
  state.stopped = false;
  state.running = true;
  setBusy(true);
  let reply = newReply(state.conversation);
  try {
    await refreshContext();
    if (state.stopped) throw new Error("Stopped.");
    keepAtBottom(() => log.append(live));
    const params = { conversation: state.conversation, text, library: state.library, all_libraries: el("all").checked };
    if (state.draft) params.draft = state.draft;
    const call = callPlugin("send", params, (event) => {
      reply = applyEvent(reply, event);
      state.conversation = reply.conversation;
      keepAtBottom(() => (live.textContent = reply.text));
    });
    state.running = call.id;
    reply = finishReply(reply, await call.promise);
    state.conversation = reply.conversation;
    live.remove();
    showMessage("assistant", "", reply.blocks);
  } catch (e) {
    live.remove();
    showNotice(state.stopped ? "Stopped." : e.message, () => send(text, true));
  } finally {
    // After any turn, so a first turn that failed or was stopped still lists its new conversation.
    if (state.conversation) await loadHistory().catch(() => {});
    state.running = null;
    setBusy(false);
    el("text").focus();
  }
}

// Chats often start with the same words, so the time tells them apart.
const when = (c) => new Date(c.updated_at).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });

function historyRow(c) {
  const row = node("div", c.id === state.conversation ? "row current" : "row");
  const open = button(c.title, guarded(async () => {
    toggleHistory(false);
    await openConversation(c.id);
  }), "title");
  // A two-step button: a sandboxed frame without allow-modals can't show window.confirm.
  const del = button("Delete", guarded(async () => {
    if (!del.classList.contains("armed")) {
      del.classList.add("armed");
      del.textContent = "Really delete?";
      return;
    }
    await callPlugin("delete_conversation", { id: c.id }).promise;
    if (c.id === state.conversation) await openConversation(0);
    await loadHistory();
  }), "delete");
  del.addEventListener("blur", () => {
    del.classList.remove("armed");
    del.textContent = "Delete";
  });
  del.disabled = state.running !== null;
  row.append(open, node("small", "", when(c)), del);
  return row;
}

function renderHistory() {
  const list = el("conversations");
  const groups = groupConversations(state.conversations, el("filter").value);
  list.replaceChildren();
  if (!groups.length) list.append(node("p", "hint", state.conversations.length ? "No conversation matches." : "No conversations yet."));
  for (const g of groups) {
    list.append(node("h3", "", g.label));
    for (const c of g.items) list.append(historyRow(c));
  }
}

async function loadHistory() {
  state.conversations = (await callPlugin("list_conversations").promise) ?? [];
  renderHistory();
}

function toggleHistory(open = el("drawer").hidden) {
  el("drawer").hidden = !open;
  el("history").setAttribute("aria-expanded", String(open));
  if (open) {
    renderHistory();
    el("filter").focus();
  }
}

async function openConversation(id) {
  state.conversation = id;
  el("log").replaceChildren();
  if (!id) return showHint();
  const messages = await callPlugin("get_conversation", { id }).promise;
  for (const m of messages) showMessage(m.role, m.content, m.blocks ?? []);
}

// Not a <form>: the sandbox lacks allow-forms, so the browser blocks submission before any submit event.
function submit() {
  if (!state.ready) return;
  const text = el("text").value.trim();
  if (!text || state.running) return;
  el("text").value = "";
  void send(text);
}

function onKeydown(e) {
  if (e.key !== "Enter" || e.shiftKey || e.isComposing) return;
  e.preventDefault();
  submit();
}

function onStop() {
  if (!state.running) return;
  state.stopped = true;
  if (typeof state.running === "number") abort(state.running);
}

el("send").addEventListener("click", submit);
el("text").addEventListener("keydown", onKeydown);
// The owner may have opened another card since the last turn, which the Editing line should show.
el("text").addEventListener("focus", () => void refreshContext().catch(() => {}));
el("stop").addEventListener("click", onStop);
el("new").addEventListener("click", guarded(async () => {
  toggleHistory(false);
  await openConversation(0);
  await loadHistory();
}));
el("history").addEventListener("click", () => toggleHistory());
el("filter").addEventListener("input", renderHistory);
el("drawer").addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  toggleHistory(false);
  el("history").focus();
});

try {
  await refreshContext();
  await loadHistory();
  showHint();
} catch (e) {
  showNotice(`The AI Assistant couldn't start: ${e.message}`);
}
// Send stays off until here so showHint can't wipe a message sent mid-startup; a failed start still lets send retry the context.
state.ready = true;
el("send").disabled = false;
