// The panel's only door to the page: one postMessage request per call, matched to its reply by id.
let nextId = 1;
const pending = new Map();
let noticeHandler;

addEventListener("message", (e) => {
  if (e.source !== parent) return;
  const { id, notice, event, result, error } = e.data ?? {};
  if (id === undefined && typeof notice === "string") return noticeHandler?.(notice);
  const call = pending.get(id);
  if (!call) return;
  if (event !== undefined) return call.onEvent?.(event);
  pending.delete(id);
  if (error) call.reject(new Error(error));
  else call.resolve(result);
});

/** ask posts a request and returns its id with a promise for the reply. */
export function ask(message, onEvent) {
  const id = nextId++;
  const promise = new Promise((resolve, reject) => pending.set(id, { resolve, reject, onEvent }));
  parent.postMessage({ id, ...message }, "*");
  return { id, promise };
}

/** onNotice calls fn with the name of each notice the page sends unasked, such as "context". */
export const onNotice = (fn) => (noticeHandler = fn);

export const callPlugin = (method, params, onEvent) => ask({ call: "ui.call", method, params }, onEvent);
export const abort = (id) => parent.postMessage({ id, call: "ui.abort" }, "*");
