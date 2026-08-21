---
status: open
title: Listing-level metadata does not propagate through a security-level identifier
milestone: M24
---

Currency and exchange are facts about a listing. An ISIN spans every listing of
a security while a MIC_TICKER names one of them, so a currency learned for one
line must not be asserted of another through the identifier they share. This is
**0137** seen from the identity side.

Two places treat a listing-scoped identifier as though the domain were
decoration. `consistentWith` and `confirmedFields` both key comparison on
`Identifier.Type` alone, so `MIC_TICKER/XNAS/AAPL` and `MIC_TICKER/XLON/AAPL`
compare equal: two identifier plugins naming one symbol on different venues are
recorded as agreeing, and a stated ticker on the wrong venue counts as
confirmed. Comparison should be on the whole triple.

`fillBlanks` guards the venue it adopts -- it refuses one outside the country
the winner named -- and fills the currency from any consistent result with no
equivalent check, which is the propagation this issue is about.

Security-level metadata is unaffected: name, asset class, CIK and SIC code are
facts about the security and travel across every identifier corroborated as
denoting it.

See adr/0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md.
