# A venue set is what we know, not what exists

A listing's venues are derived from its `MIC_TICKER` identifiers into
`listing_venues` ([0068](0068-a-listing-is-a-currency-of-a-security.md)), and a
provider names a venue only when it happens to. The set is therefore **open**: it
holds the venues we have been told about, not the venues a line is admitted to. A
source naming a venue we do not hold has told us something new rather than
contradicted us.

The distinction that decides how any one comparison reads is which question it
asks.

**"Where is this line quoted?" is permissive.** A line admitted to no venue is
ordinary -- a composite identifier names a market and stores no MIC -- and a line
admitted to several is one line, its venues differing by a spread rather than by
anything a provider holds separate data for. Carrying any one of them is carrying
the line, so `PluginAcceptsListing` accepts a plugin covering any venue in the
set and accepts a line with none, the price-gap CLI quotes a line at whichever of
its venues its quote source knows, and a surface showing venues shows all of them
rather than choosing.

**"Are these the same line?" is strict, and the currency is what it asks about.**
A line is a currency of a security ([0068](0068-a-listing-is-a-currency-of-a-security.md)),
so two identifier plugins stating currencies of one family have described one
line however many venues they name between them, and two stating different
families have described two. The venue stands in where no currency was stated,
and only there: an answer naming a market or a venue and nothing else has still
said where it trades, and adopting another plugin's names onto it is how a London
ticker comes to sit on a New York instrument. So `Venue.Agrees` decides admission
in exactly the case the currencies leave open, `Venue.Permits` decides whether
`fillBlanks` may restate a market as a venue inside it, and `consistentWith`
excludes a result failing either. These compare two answers rather than an answer
against the store, so the open-world rule does not reach them: neither side is a
partial record.

## Consequences

**A stated venue is never a hint difference.** `CompareHints` compared a hint's
MIC against a single MIC read off the security, and reported a mismatch to the
price importer as a refusal and to the corporate-event importer as a review item.
There is nothing left to report: a stated venue either is among the ones we hold,
which is agreement, or is not, which is news. The comparison is deleted rather
than widened to a membership test, a test that can only pass being no test.
Currency remains the hint check with content, and is the one that guards an
import -- a line is a currency, so a file stating the wrong one states the wrong
line.

**Refusing to answer is about currency, not venue.** `ListingForVenue` returns
nothing when a MIC matches two lines. The LSE lists both the GBP and the USD line
of one ETC, so a bare MIC does not narrow to a line and settling it by picking
one would attach a holding to a currency nobody stated. The ambiguity refused
there is the currency's.

**A MIC the reference table does not carry is dropped and the ticker kept.**
`recompute_listing_venues` joins `exchanges`, which is both the foreign key and
the filter. Losing a venue we cannot describe costs nothing when the set was
never complete.

**Nothing derives a security-wide venue.** A security is admitted to no venue;
its lines are. This is why `instruments.exchange_mic` and the `exchange` label
derived from it are retired rather than recomputed
(issue [0155](../issues/0155-an-instruments-own-currency-and-exchange-are-retired.md)),
and why exchange reference data reaches the API on each line's venues rather than
on the security.

**A listing-grain domain is a venue restated, and does not contradict on its own
account.** `contradicts` read two `MIC_TICKER`s under different domains as two
listings. Every identifier plugin sets that domain from the venue it put on the
answer, so the test said what the venue comparison already said -- and said it on
a field 0068 does not key a line on, in a spelling no country normalisation
reaches, a composite's `US` against a venue's `XLON`. It is gone: a difference in
the value under one subject is the whole of what an identifier can contradict
(issue [0159](../issues/0159-merge-admission-tells-two-lines-apart-by-currency.md)).

**A subject is what dedupes a merged identifier, not a type.** `flattenClaims`
kept one row per identifier type, which for a ticker meant one venue -- dropping
the second whether it arrived from a second result or from one result naming
both, so a winner's own answer was trimmed on the way to the store. The venue set
is derived from those rows, so a venue went with each one lost. It keys on the
subject, the type and its normalised domain, which is what `sameSubject` already
meant by one thing.
