---
status: open
title: Carry broker contract identifiers through ingestion
milestone: M24
dependencies: [0140, 0143, 0145, 0175]
---

IBKR names every contract by a CONID that survives corporate actions: the same
`678940680` appears on the trade that opened an NVDA option position, on the
transfer that split it three days before the ex-date, and on the trade that
closed it afterwards. The QFX carries one security record per CONID describing it
in current terms, so the OCC symbol in the file is rendered from a current-state
security master rather than stated per transaction.

The converter drops it, and what reaches the archive is that current symbol
attributed to a trade that happened under a different one. Since 0125 and 0126 a
name carries its own validity interval and hints are no longer rebased, so the
stored symbol is correct as of the export it came from -- the failure moved
rather than went away. Two exports of different vintages now name one contract by
two OCC symbols and resolve to **two instruments**, unless PortfolioDB minted the
second name itself from a split it already knew about. The contract identifier
makes them one whether the split is known or not, which is the thing no amount of
interval machinery can do on its own.

For some contracts it is the only identity the file offers at all. The Eurex
options in the sample statements carry IBKR's own rendering in the TICKER field
and no OCC exists for them (0145), so without the contract identifier they are
broker-description-only for good.

`IdentifierType` has no broker-scoped contract identifier. Adding one means a new
enum member, a domain naming the broker that issued it the way `MIC_TICKER` names
a MIC, and extraction from the OFX/QFX `UNIQUEID` whose `UNIQUEIDTYPE` is
`CONID`. The identifier is opaque and carries no strike, expiry or ticker, so
nothing rebases it and nothing has to state what it is denominated in.

The member is broker-internal identifiers generally rather than CONID
specifically. A broker's own contract number is meaningless without the broker
that issued it, so the domain is what makes the value resolvable and is what
keeps two brokers' numbering from colliding; CONID is the first instance because
IBKR is the source already in the tree, not because the type is IBKR's.

Transactions are not the only place they arrive. A QFX `INVPOSLIST` names option
positions by CONID too, so 0088 cannot import an options position list without
this.

0122 asks how to resolve identity as of a date. A stable broker identifier is the
kind of identifier that question prefers, and it is the one already on offer from
the source PortfolioDB has most trouble with.

## Evidence

Checked against the IBKR exports in `local/`, which are gitignored, so this is
recorded here rather than being reproducible from the tree.

Contract identifiers are stable across re-downloads: 43 option and 17 stock
identifiers recur across the 2021-2024 exports and 30 option identifiers across
the 2025-2026 exports, with no disagreement anywhere, and no OCC symbol appears
under two of them. IBKR documents them as static per contract, which is
documentation rather than a formal commitment (see
adr/0061-transitivity-needs-a-non-reassigned-identifier.md).

The OCC symbol one of them renders as is not stable. In the January-November 2024
export, contract 678940680 is bought on 2024-04-08 at 60.584634 and 678941159
sold the same day at 109.000188, with NVDA around 870 before its split; a
TRANSFER on 2024-06-07 memoed `SPLIT 10 FOR 1` moves 18 units between them, which
is 2 contracts becoming 20; and the SECLIST renders 678940680 at strike 79. Those
prices are coherent only at strikes 790 and 910, so the whole file states the
April trades under the November name.

That is the only option split in the corpus, and the pre-split export ends a week
before the contract was first traded, so there is no side-by-side of one contract
identifier rendered either side of a corporate action. The contradiction within
the single file is the evidence instead.
