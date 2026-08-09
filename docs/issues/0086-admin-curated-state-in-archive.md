---
status: closed
title: Carry admin-curated state in the system archive
milestone: M14
dependencies: [0078]
---

Export the decisions an admin made by hand: fetch blocks, resolved unhandled
events, and plugin configuration.

## Motivation

Each of these is a human judgement stored as a row, and none of them is
exported. They are tier 1 in
adr/0032-archive-preserves-inputs-not-derived-state.md.

- **`price_fetch_blocks` and `corporate_event_fetch_blocks`** record permanently
  blocked (instrument, plugin) pairs with a `reason`. Losing them means the
  fetcher resumes querying providers that were deliberately stopped, and the
  reason someone wrote down is gone.
- **`unhandled_corporate_events.resolved`** is the record that an admin looked
  at a reverse split or a merger and made a call. The flag is the only trace of
  it; a rebuild re-surfaces every event for review.
- **`plugin_config`** holds which plugins are enabled, their precedence and
  their config including API keys. Precedence is an ordering someone chose and
  the constraint is unique per category, so it is not reconstructible by
  guessing.

## Design

Parts of the system archive. Fetch blocks and unhandled event resolutions
reference instruments, so they restore after the instrument part.

`plugin_config` is exported **in full, including API keys**, so that a rebuild
needs no manual re-entry. The consequence is that an archive containing it is a
secret and cannot be kept where the current exports are kept. That is why it is
off by default in the export menu (0079): including it must be a deliberate
choice, and the UI should say what it means.

Closed. The system archive carries all three, and the export menu has no
disabled rows left.

Three things landed differently from the description. The `reason` on a fetch
block is free text, not a typed vocabulary: the column, the Go model and the
API message are all strings, its only producer is `ErrPermanent.Reason`, and it
is read by a person deciding whether to unblock. The two fetch block tables
travel in one part rather than two, named by plugin category, because they are
the same statement about two fetchers. And the unhandled event part carries
whole rows rather than only the `resolved` flag ADR 0032 lists as tier 1:
those rows are created only by a fetch detecting something it could not apply,
and an import writes events from the file rather than fetching them, so nothing
would re-create the row a bare resolution needed to attach to.
