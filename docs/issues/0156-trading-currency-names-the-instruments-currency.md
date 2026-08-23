---
status: open
title: Trading currency names the instrument's currency
milestone: M25
dependencies: []
---

`trading_currency` is defined as the instrument's own currency, and the OFX
parser writes the account's default into it whenever a record states none of its
own.

## Motivation

OFX's `CURRENCY` element means the amounts in this record are expressed in
`CURSYM`, `CURRATE` being the rate back to the account default. Where the
element is present `CURSYM` is the currency `UNITPRICE` is quoted in, so it
names the line the security trades on. Where it is absent the figures are in
`CURDEF`, which is a fact about the account and says nothing about the line.

`client/lib/ofx/parser.ts` collapses the two into one field, so a reader cannot
tell which it was handed. `HintsFromTx` passes `trading_currency` to the
identifier plugins as `Hints.Currency`, and a currency hint completes an
identity under adr/0068, so a record with no `CURRENCY` element hands a plugin
the account's currency as the security's.

## Scope

`tradingCurrency` comes from an explicit `CURSYM` alone; `settlementCurrency`
keeps the `CURDEF` fallback, which is correct -- with no element the figures
really are in the account default. Weight is unaffected, `settleCurrency`
preferring settlement.

Fidelity already states it correctly: `trading_currency` on cash rows only,
where the instrument is its own currency.
