---
status: open
title: Sanity check tx group pairings against fetched prices
dependencies: [0064, 0066]
---

Once prices are known for an instrument and date, check each tx group's cash leg
against `quantity * price` for its security leg, and raise a user alert when they
disagree by more than a tolerance.

## Motivation

Converters pair a trade with its cash row using only what the broker's transaction
log contains (see 0064: amount for sells, reference proximity for buys). Those rules
are measured against sample exports and hold on them, but a mispairing is silent --
two trades settling the same day in one account can swap cash rows and nothing in
the file says so. The consequence is a cash balance that is wrong in both directions
at once, which no total will reveal.

The check that would catch it needs a price, and the converter has none: the broker
log carries `unit_price` at best, which is the number being validated. The server
has prices, but not usually at upload time -- an instrument may be unidentified, and
the fetcher runs afterwards. So this cannot be an ingestion-time validation. It has
to run later, when the price data has caught up.

## Design

- For each tx group with a security leg and a cash leg, compare the cash leg against
  `quantity * close` for the security leg's instrument on the group's date.
- Use the split-adjusted pair, `split_adjusted_quantity * split_adjusted_close`, so
  both sides are denominated in the same share count (docs/spec/bitemporality.md).
- Tolerance has to absorb the real reasons a cash leg differs from the gross
  consideration: commissions and duties posted separately, the difference between an
  execution price and the day's close, and FX where the settlement currency is not
  the instrument's. A fixed epsilon will not do; a relative band with an absolute
  floor is the starting point, tuned against the sample exports so it is quiet on
  known-good data before it is trusted on unknown data.
- Skip rather than alert when there is no price for the date, when the instrument is
  unidentified, or when the group has no security leg. An absent price is not
  evidence of a bad pairing.
- Raise a **user** alert (0066), not an admin one: it concerns one user's
  transactions and only they can confirm what the broker actually did.

## When it runs

After prices arrive, not at upload. Re-running must be idempotent and must not
re-alert on a group already reviewed and dismissed. A group whose price later
arrives, or changes, should be re-checked -- which suggests keying the check off
price availability rather than sweeping everything on a timer.

## Note

The same check is a regression test for converter pairing rules: running it across
the sample exports says whether a new broker's rules are sound before that broker's
data is trusted.
