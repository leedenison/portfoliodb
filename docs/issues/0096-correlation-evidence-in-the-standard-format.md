---
status: closed
title: Carry correlation evidence in the standard format
milestone: M15
dependencies: [0092]
---

Give a posting a broker-neutral description of why it might belong with another
posting, and make each converter synthesise its source's data into it.

## Motivation

adr/0041-server-owns-transaction-grouping.md moves the grouping decision to the
server. The server cannot make it on what the standard format carries today: the
converter's answer arrives as `group_ref`, which is opaque, upload-scoped and not
stored, and the evidence behind it never leaves the converter at all.

## Design

The shape is settled in adr/0048-correlations-declare-their-own-semantics.md. A
posting carries repeated correlations, each stating an identifier and what may be
done with it:

```
message Correlation {
  string label = 1;             // which series; the comparability partition
  string token = 2;             // compared by MATCH_EXACT
  optional int64 ordinal = 3;   // compared by MATCH_ORDINAL
  Scope scope = 4;              // exactly one
  repeated Match match = 5;     // at least one
  optional int64 ordinal_span = 6;
}
```

`Scope` is `FILE`, `ACCOUNT` or `BROKER`, and `BROKER` means this user's data for
this broker. `Match` is a set, because an identifier can be honestly comparable
both ways. `token` and `ordinal` are separate fields: an IBKR `FITID` reads
`20251015U10000018371888432`, which is not an integer but plainly carries one, and
only the converter knows how to take it.

Per converter:

- **Fidelity CSV and the extension's Fidelity JSON** already parse `referenceId` as
  a number and use its distance when grouping. That becomes one correlation with an
  empty `label`, the reference as `token`, the parsed number as `ordinal`, scope
  `FILE`, and match `{EXACT, ORDINAL}`. `DEPOSIT_REF_SPAN` becomes its
  `ordinal_span`.
- **OFX** emits the `FITID` as `token` with scope `ACCOUNT` -- the OFX spec makes it
  unique within the account, not the institution -- match `{EXACT}`, and no ordinal.
  It must **not** emit a correlation for the `unreferenced` legs whose group the
  parser synthesises, since that group is inferred rather than transcribed.

`broker_ref` and `counterparty_account` are stored already and overlap this: the
first is the equality half, the second a pointer of the same kind. Decide here
whether they fold into `Correlation` or stay alongside it.

## Scope

The evidence has to be stored on `txs` and to travel in the user archive, or a
rebuild from an archive would have nothing to group on. It lands on the same flat
`Posting` the archive already carries in both directions after 0084, so there is no
structural change to the format here -- only fields. Converters populate it while
still emitting `group_ref`; nothing reads it until 0097.

**A converter may only transcribe, never infer.** A token may be synthesised from
the source's own structure -- nesting, containment, an explicit order column -- and
never from amounts, dates or proximity. Nothing on the wire tells the two apart, so
this is the discipline that keeps 0098 reachable rather than preserving the
converter rules under a new name.

**Source-asserted groupings wait for 0097.** 0097 validates the engine by requiring
it to reproduce `group_ref` exactly in shadow. A converter asserting a grouping
through an exact-match correlation during that window would have the engine
trivially reproduce what it was told, and the validation would prove nothing. Land
the field here; populate it for transcribed groupings only once the engine is
proven.
