---
status: open
title: Attribute cash dividends to the security that paid them
---

A cash dividend currently resolves to the cash instrument with the description
"Cash", losing which holding generated it. The spec says an income transaction
should carry the description of the generating security
(see spec/portfoliodb-spec.md), and Fidelity supplies it: the CSV has a
`Source investment` column, and the underlying JSON carries `linkedAssetName`
together with a real `linkedAssetIsin`.

The decision to make is how income is modelled. The dividend increases **cash**,
so the paying security must not become the transaction's instrument or the payout
lands in that security's holding. Options include describing the transaction by
the payer while still resolving it to cash, or recording the payer as a separate
identifier hint.

Also settle whether the identifier is worth capturing for other purposes: knowing
which holding produced income is needed for per-instrument yield, and the ISIN
identifies it exactly without a description lookup.
