---
status: closed
title: Sanity check tx group pairings against fetched prices
dependencies: [0064, 0066]
---

Closed without implementing. Kept as a record so the idea is not raised again
without the objection that killed it.

## The idea

Once prices were known for an instrument and date, check each tx group's cash leg
against `quantity * close` for its security leg, and alert when they disagreed by
more than a tolerance. The appeal was that converters pair a trade with its cash row
using only what the broker's transaction log contains, so a mispairing is silent.

## Why not

**The tolerance cannot be made meaningful.** `eod_prices` holds the closing price,
while a trade executes intraday. The band would have to absorb the whole intraday
range -- percentage points on an ordinary day, more on a volatile one -- before it
absorbed anything about pairing. A check that loose flags nothing worth flagging and
misfires on days a price moved, which is the worst combination: noise that trains
people to ignore the alert.

**The useful half is already done, and done better.** The Fidelity converter now
checks the cash leg against `quantity * unit_price` from the trade row (0064).
`unit_price` is the execution price, so that comparison has no intraday problem at
all and runs at conversion time rather than whenever prices catch up. It holds to
0.16% across the sample exports, which is tight enough to reject a swapped cash row.
A close-based check would only catch mispairings so gross that the converter has
already rejected them.

What remains uncovered is a broker misreporting its own `unit_price`, and no
external price series can distinguish that from ordinary intraday movement.
