---
status: open
title: General alerting system for data problems needing review
milestone: M21
---

A single mechanism for raising, listing, and resolving alerts about data that needs
a human to look at it, replacing the one-off table and admin card each such problem
has today.

## Motivation

Every data problem the system can detect but not fix has grown its own machinery:
`validation_errors` and `identification_errors` hang off an ingestion job and are
surfaced on the uploads page; `unhandled_corporate_events` has its own table, its own
`resolved` flag, its own count RPC and its own admin card. The imbalance report
(0039) wants the same thing again.

Repeating it per problem means the resolution workflow, deduplication, and "is
anything wrong?" summary are re-implemented every time and drift apart. It also
means there is no answer to "what needs my attention", only a set of places to go
and look.

## Two audiences, not one

Alerts divide by who can act on them, and the split is the existing one between the
key user concepts and the key admin user concepts (docs/spec/information-architecture.md):

- **Admin alerts** concern shared reference data and system health -- an unhandled
  corporate event, a price fetch failing repeatedly, a plugin erroring. Nobody but
  an admin can resolve these, and one alert covers all users.
- **User alerts** concern a user's own data -- a large `Imbalance` balance under one
  broker, an unmatched transfer. These are per-user, and the user is the only one
  who can say whether the underlying data is right.

A user must not see admin alerts, and an admin looking at system health must not
have to wade through per-user data-quality noise.

## Design sketch

- One `alerts` table: scope (admin / user), `user_id` where the scope is user, a
  type, a severity, a human-readable detail, structured `data`, the subject it
  concerns (instrument, tx group, ingestion job), and a resolution state.
- Deduplication on (scope, subject, type) while unresolved, following the partial
  unique index `unhandled_corporate_events` already uses.
- Alerts are raised by whatever detects the problem and are never raised
  speculatively: an alert nobody acts on trains people to ignore all of them.
- Resolution is explicit and records who resolved it. An alert whose cause has gone
  away should also be able to close itself, so that fixing the data is enough and
  the list does not accumulate stale entries.
- A count per scope for the navigation badge, following the existing admin page
  pattern, and a list view with filtering by type and severity.

## Migration

`unhandled_corporate_events` is the closest existing thing and should move onto this
mechanism rather than sit alongside it. `validation_errors` and
`identification_errors` are per-upload and already have a natural home on the uploads
page; whether they also become alerts is a judgement call worth making explicitly
rather than by default.
