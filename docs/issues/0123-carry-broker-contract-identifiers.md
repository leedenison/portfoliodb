---
status: open
title: Carry broker contract identifiers through ingestion
milestone: M16
---

IBKR names every contract by a CONID that survives corporate actions: the same
`678940680` appears on the trade that opened an NVDA option position, on the
transfer that split it three days before the ex-date, and on the trade that
closed it afterwards. The QFX carries one security record per CONID describing it
in current terms, so the OCC symbol in the file is rendered from a current-state
security master rather than stated per transaction.

The converter drops it. What reaches the archive is that current symbol
attributed to the trade date, so the resolver rebases for a split the symbol
already reflects and looks up a strike that never existed -- a $790 put becomes a
$79 put in the file and a $7.90 put in the query.

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
