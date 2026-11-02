const day = (d) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
const labels = ["Today", "Yesterday", "This week", "Older"];

/**
 * groupConversations keeps the conversations whose title contains query, in any case, and sorts them into Today,
 * Yesterday, This week, and Older, keeping their order and dropping empty groups.
 */
export function groupConversations(list, query, now = new Date()) {
  const q = query.trim().toLowerCase();
  const groups = labels.map((label) => ({ label, items: [] }));
  for (const c of list) {
    if (q && !c.title.toLowerCase().includes(q)) continue;
    // Rounded, since a day with a clock change isn't 24 hours.
    const days = Math.round((day(now) - day(new Date(c.updated_at))) / 86_400_000);
    groups[days <= 0 ? 0 : days === 1 ? 1 : days < 7 ? 2 : 3].items.push(c);
  }
  return groups.filter((g) => g.items.length);
}
