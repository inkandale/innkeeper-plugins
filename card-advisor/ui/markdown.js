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

export function blocks(markdown) {
  return markdown
    .split(/\n{2,}/)
    .filter((b) => b.trim())
    .map((b) => {
      const lines = b.trim().split("\n");
      if (lines.every((l) => /^[-*] /.test(l))) return { type: "ul", items: lines.map((l) => spansOf(l.slice(2))) };
      return { type: "p", spans: spansOf(b.trim()) };
    });
}
