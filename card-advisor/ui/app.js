import { abort, ask, callPlugin } from "./bridge.js";
import { blocks } from "./markdown.js";
import { applyEvent, finishReply, newReply } from "./reply.js";

const el = (id) => document.getElementById(id);
const state = { conversation: 0, library: 0, grants: [], running: null, stopped: false };

// Each offer shows only when the host granted it.
const offers = [
  ["Push", "push", "cards.push"],
  ["Add to collection", "add-to-collection", "cards.collect"],
];

function button(label, onClick) {
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = label;
  b.addEventListener("click", onClick);
  return b;
}

function addSpans(node, spans) {
  for (const s of spans) {
    const span = document.createElement(s.bold ? "strong" : s.italic ? "em" : "span");
    span.textContent = s.text;
    node.append(span);
  }
}

function appendBlocks(parent, markdown) {
  for (const block of blocks(markdown)) {
    if (block.type === "p") {
      const p = document.createElement("p");
      addSpans(p, block.spans);
      parent.append(p);
      continue;
    }
    const ul = document.createElement("ul");
    for (const item of block.items) {
      const li = document.createElement("li");
      addSpans(li, item);
      ul.append(li);
    }
    parent.append(ul);
  }
}

// Scrolls to the end only if the reader was already there.
function keepAtBottom(change) {
  const log = el("log");
  const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  change();
  if (atBottom) log.scrollTop = log.scrollHeight;
}

function showPick(parent, pick) {
  const box = document.createElement("div");
  box.className = "pick";
  const name = document.createElement("strong");
  name.textContent = pick.name;
  const reason = document.createElement("p");
  reason.textContent = pick.reason;
  box.append(name, reason, button("Open", () => ask({ call: "host.openCard", card: pick.card_id })));
  for (const [label, request, grant] of offers) {
    if (state.grants.includes(grant)) box.append(button(label, () => ask({ call: "host.request", request, card: pick.card_id })));
  }
  parent.append(box);
}

function showMessage(role, content, picks = []) {
  const box = document.createElement("div");
  box.className = `message ${role}`;
  appendBlocks(box, content);
  for (const pick of picks) showPick(box, pick);
  keepAtBottom(() => el("log").append(box));
  return box;
}

function showNotice(text, retry) {
  const box = document.createElement("div");
  box.className = "message notice";
  box.textContent = text;
  if (retry) {
    box.append(" ", button("Retry", () => {
      box.remove();
      retry();
    }));
  }
  keepAtBottom(() => el("log").append(box));
}

function showHint() {
  const hint = document.createElement("p");
  hint.className = "hint";
  hint.id = "hint";
  hint.textContent = "Describe what you feel like, such as a slow-burn rival, not too dark.";
  el("log").replaceChildren(hint);
}

function setBusy(busy) {
  el("send").hidden = busy;
  el("stop").hidden = !busy;
  el("log").setAttribute("aria-busy", String(busy));
  for (const id of ["history", "new"]) el(id).disabled = busy;
  syncDelete();
}

function applyContext(context) {
  if (!context.library) throw new Error("Open a library first.");
  state.library = context.library;
  state.grants = context.grants ?? [];
  for (const [token, value] of Object.entries(context.theme ?? {})) document.documentElement.style.setProperty(token, value);
}

const refreshContext = async () => applyContext(await ask({ call: "host.context" }).promise);

/** send runs one turn. A retry resends the same text without repeating the user's message. */
async function send(text, isRetry = false) {
  if (state.running) return;
  const log = el("log");
  document.getElementById("hint")?.remove();
  if (!isRetry) showMessage("user", text);
  const live = document.createElement("div");
  live.className = "message assistant streaming";
  state.stopped = false;
  state.running = true;
  setBusy(true);
  let reply = newReply(state.conversation);
  try {
    await refreshContext();
    keepAtBottom(() => log.append(live));
    const call = callPlugin(
      "send",
      { conversation: state.conversation, text, library: state.library, all_libraries: el("all").checked },
      (event) => {
        reply = applyEvent(reply, event);
        state.conversation = reply.conversation;
        keepAtBottom(() => (live.textContent = reply.text));
      },
    );
    state.running = call.id;
    const result = await call.promise;
    reply = finishReply(reply, result);
    state.conversation = reply.conversation;
    live.remove();
    showMessage("assistant", reply.text, reply.picks);
    await loadHistory().catch(() => {});
  } catch (e) {
    live.remove();
    showNotice(state.stopped ? "Stopped." : e.message, () => send(text, true));
  } finally {
    state.running = null;
    setBusy(false);
    el("text").focus();
  }
}

async function loadHistory() {
  const list = (await callPlugin("list_conversations").promise) ?? [];
  const select = el("history");
  select.replaceChildren(new Option("Past conversations", "0"), ...list.map((c) => new Option(c.title, String(c.id))));
  select.value = String(state.conversation);
  syncDelete();
}

async function open(id) {
  state.conversation = id;
  el("log").replaceChildren();
  if (!id) return showHint();
  const messages = await callPlugin("get_conversation", { id }).promise;
  for (const m of messages) showMessage(m.role, m.content, m.picks ?? []);
}

const guarded = (action) => () => action().catch((e) => showNotice(e.message));

function resetDelete() {
  el("delete").textContent = "Delete";
  el("delete").classList.remove("armed");
}

function syncDelete() {
  el("delete").disabled = state.running !== null || el("history").value === "0";
  resetDelete();
}

// A two-step button: a sandboxed frame without allow-modals can't show window.confirm.
async function deleteSelected() {
  const id = Number(el("history").value);
  if (!el("delete").classList.contains("armed")) {
    el("delete").textContent = "Really delete?";
    el("delete").classList.add("armed");
    return;
  }
  resetDelete();
  await callPlugin("delete_conversation", { id }).promise;
  if (id === state.conversation) await open(0);
  await loadHistory();
}

function onSubmit(e) {
  e.preventDefault();
  const text = el("text").value.trim();
  if (!text || state.running) return;
  el("text").value = "";
  void send(text);
}

function onKeydown(e) {
  if (e.key !== "Enter" || e.shiftKey || e.isComposing) return;
  e.preventDefault();
  el("ask").requestSubmit();
}

function onStop() {
  if (typeof state.running !== "number") return;
  state.stopped = true;
  abort(state.running);
}

el("ask").addEventListener("submit", onSubmit);
el("text").addEventListener("keydown", onKeydown);
el("stop").addEventListener("click", onStop);
el("new").addEventListener("click", guarded(async () => { await open(0); await loadHistory(); }));
el("history").addEventListener("change", guarded(async (e) => { await open(Number(e.target.value)); syncDelete(); }));
el("delete").addEventListener("click", guarded(deleteSelected));
el("delete").addEventListener("blur", resetDelete);

try {
  await refreshContext();
  await loadHistory();
  showHint();
} catch (e) {
  showNotice(`The advisor couldn't start: ${e.message}`);
}
