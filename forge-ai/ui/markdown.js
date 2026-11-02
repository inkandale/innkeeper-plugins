// blocks reads the small Markdown the model writes. It returns data, never
// HTML: the panel builds text nodes from it, so card text can't add markup.
const spansOf = (text) => {
  const clean = text.replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1");
  const spans = [];
  // Only a closed pair is emphasis; the split keeps unclosed asterisks inside plain parts.
  for (const part of clean.split(/(\*\*[^*]+\*\*|\*[^*]+\*)/)) {
    if (!part) continue;
    if (/^\*\*[^*]+\*\*$/.test(part)) spans.push({ text: part.slice(2, -2), bold: true });
    else if (/^\*[^*]+\*$/.test(part)) spans.push({ text: part.slice(1, -1), italic: true });
    else spans.push({ text: part });
  }
  return spans;
};

// A GFM pipe table: rows open and close with a pipe, and a rule of dashes follows the header.
const isRow = (line) => /^\s*\|.*\|\s*$/.test(line);
const isRule = (line) => /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$/.test(line);
const cellsOf = (line) =>
  line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((c) => spansOf(c.trim()));

function table(lines) {
  const columns = cellsOf(lines[0]);
  const rows = lines.slice(2).map((l) => {
    const cells = cellsOf(l);
    return columns.map((_, i) => cells[i] ?? []);
  });
  return { type: "table", columns, rows };
}

const textBlock = (lines) =>
  lines.every((l) => /^[-*] /.test(l)) ? { type: "ul", items: lines.map((l) => spansOf(l.slice(2))) } : { type: "p", spans: spansOf(lines.join("\n")) };

// Models told to use table blocks still write pipe tables, so those become tables too.
export function blocks(markdown) {
  const out = [];
  for (const chunk of markdown.split(/\n{2,}/)) {
    if (!chunk.trim()) continue;
    const lines = chunk.trim().split("\n");
    const at = lines.findIndex((l, i) => isRow(l) && isRule(lines[i + 1] ?? ""));
    if (at < 0) {
      out.push(textBlock(lines));
      continue;
    }
    let end = at + 2;
    while (end < lines.length && isRow(lines[end])) end++;
    if (at > 0) out.push(textBlock(lines.slice(0, at)));
    out.push(table(lines.slice(at, end)));
    if (end < lines.length) out.push(textBlock(lines.slice(end)));
  }
  return out;
}
