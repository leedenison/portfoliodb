---
status: open
title: Attribute cash dividends to the security that paid them
milestone: M18
---

A cash dividend resolves to the cash instrument and carries no record of which
holding generated it. The spec says an income transaction should carry the
description of the generating security (see spec/portfoliodb-spec.md), and
Fidelity supplies it: the CSV has a `Source investment` column, and the
underlying JSON carries `linkedAssetName` together with a real `linkedAssetIsin`.

The Fidelity converters now describe a cash posting by its currency and hint at
the currency, so the payer is not merely unused -- it is dropped. Before 0065 the
conversion script put `Source investment` in the description, which is one of the
options below taken by accident rather than by decision, and the client never
carried it at all. Whichever option is chosen has to put the payer somewhere
deliberately.

The decision to make is how income is modelled. The dividend increases **cash**,
so the paying security must not become the transaction's instrument or the payout
lands in that security's holding. Options include describing the transaction by
the payer while still resolving it to cash, or recording the payer as a separate
identifier hint.

Also settle whether the identifier is worth capturing for other purposes: knowing
which holding produced income is needed for per-instrument yield, and the ISIN
identifies it exactly without a description lookup.
