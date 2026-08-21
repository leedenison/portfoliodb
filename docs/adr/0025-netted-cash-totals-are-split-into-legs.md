# A netted cash total is split into orthogonal legs

Brokers differ in how they report a trade's commission. Fidelity posts it as its
own row on its own date; IBKR and Schwab fold it into a single cash total
(`<TOTAL>`, `Amount`) while still reporting the commission separately
(`<COMMISSION>`, `Fees & Comm`). Converters split the netted total into a
consideration leg and a fee leg, so a trade group has the same shape whatever the
broker reported and a commission is a posting everywhere rather than only where a
broker happened to itemise it. A posting then represents one thing, which a
netted total does not.

Keeping the broker's total as a single posting and adding only the expense leg
was considered. It balances just as well and reconciles to the statement line by
line, but it leaves the cash posting meaning two things at once -- the
consideration and the charge -- which is the conflation this exists to remove,
and it makes the same trade look different depending on the broker.

## Consequences

The user's own cash postings still sum to the total the broker reported, so
nothing about the cash balance changes; only its attribution does. The
consideration leg is derived as `total + commission` rather than as
`quantity * unit_price`, because the broker's own arithmetic is what the two cash
legs have to add back up to, and a recomputed consideration would drift from the
statement by the rounding in a quoted unit price.

A converter with no commission to read cannot split anything, and its trades keep
a residual in `IMBALANCE` (see [0021](0021-converters-own-transaction-grouping.md)).
Deriving the fee as `|amount| - quantity * unit_price` was rejected for those:
it is only the commission when the price and the cash total are already in the
same currency and the price is exact, and where either fails the number is
rounding or FX error wearing a commission's name.
