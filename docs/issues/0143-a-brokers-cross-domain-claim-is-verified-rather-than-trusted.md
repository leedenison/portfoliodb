---
status: open
title: A broker's cross-domain claim is verified rather than trusted
milestone: M24
dependencies: [0140, 0142]
---

A broker naming its own contract identifier alongside a global one is
authoritative, because no identifier plugin can be asked what a contract number
means. Chaining through it is a different claim: if the contract identifier is
never reassigned, then the two global identifiers it named at different times
denote one security -- and that conclusion lies wholly inside the identifier
plugins' domain, arriving through a channel nobody authenticated.

Such a derived claim is neither merged nor discarded. It is a hypothesis with
somewhere to look: two instruments that may be one, and an identifier plugin
able to settle it. Discarding it wastes the only part of an untrusted message
that was useful, and merging on it spreads a possible error into shared data
with no way back.

It parks on the same surface a contradiction is recorded on (0141), and is
resolved by an identifier plugin confirming or refuting it rather than by a
person, where one can be asked.

Verification is not the only way out. The promotion sweep in 0142 reaches the
same place from the other side: once enough users hold the broker's mapping and
none contradicts it, the association becomes system-owned and may mediate a chain
on its own, so the hypothesis is answered without anyone being asked. Both routes
end with the claim eligible; neither leaves it acted on while it is still one
user's word.

See adr/0062-a-user-mediated-claim-is-a-lead-not-a-write.md.
