---
status: superseded by ADR-0072
---

# A posting names a listing

Superseded by
[0072](0072-a-posting-names-a-security-and-a-line.md), which has a posting name
both a security and a nullable line rather than always a line.

The argument 0072 relies on and does not restate: a group balances on an exact
sum per `weight_commodity` ([0024](0024-group-balance-is-checked-on-weight.md)),
and the weight is computed once at ingest and stored
([0029](0029-posting-weight-is-stored.md)), before resolution has run. So two
legs of one group stated at two grains are two commodities and the group grows a
residual nothing put there, and the grain cannot be deferred until the line is
known. Whatever a posting is weighed in, every posting has to be weighed in the
same kind of thing.
