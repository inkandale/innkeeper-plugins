/** askCopy is the intro hint and input placeholder, which offer a change to the card only with cards.edit. */
export function askCopy(grants) {
  const edit = grants.includes("cards.edit");
  return {
    hint: `Describe the card you want, such as a slow-burn rival, not too dark.${edit ? " Or open a card and ask for a change." : ""}`,
    placeholder: edit ? "Ask for cards, or for a change to the open card" : "Ask for cards",
  };
}
