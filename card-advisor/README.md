# Card advisor

A chat in the corner of every page. Describe the mood you want and it
suggests cards from your library, with a reason for each pick.

## Set up

1. In the AI plugin, add a connection with a chat model that supports tools.
   The advisor looks up cards through tools, so a model without them can't
   answer.
2. Set up Card advisor and pick that connection.
3. Library access is off by default. Turn it on for Card advisor, or it
   can't see your cards.
4. Optional: turn on Offer push and Offer add to collection to let the
   advisor propose those actions. Each one still waits for your click.

Search by meaning needs the Similar cards plugin set up. Without it, the
advisor falls back to text search.

Conversations are kept per instance. Delete one from the chat's history
list.
