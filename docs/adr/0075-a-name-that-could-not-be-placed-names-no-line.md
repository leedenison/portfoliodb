# A name that could not be placed names no line

A listing-grain identifier -- a ticker under its venue, a SEDOL, a composite FIGI
-- names one currency line of a security. A result may supply one without
supplying a currency, and then nothing says which line it names.

`instrument_listing_identifiers` and `provider_listing_identifiers` carry the
security and a **nullable** listing, under the composite foreign key
[0072](0072-a-posting-names-a-security-and-a-line.md) gives a posting:

    FOREIGN KEY (instrument_id, listing_id)
      REFERENCES instrument_listings (instrument_id, id) MATCH SIMPLE

A null line means nobody could place the name. It is the same first-class state a
posting has, said the same way, and it is the only one: the currency-unknown
listing row is deleted and `instrument_listings.currency` becomes `NOT NULL`.

## The null listing had one job left

[0068](0068-a-listing-is-a-currency-of-a-security.md) gave a security whose
currency is unknown a listing with a null currency, to distinguish "exactly one
line, and it is this currency" from "how many lines this security has is
unknown". Absence says the second thing just as well: a security with no listing
rows has no line anyone has named, and one with a listing has that line.

What the row was actually load-bearing for was somewhere to file a name nobody
could place, both identifier tables having required a listing. Of the eight
columns that name a line, the other six cannot hold an unplaced anything: prices,
coverage, fetch blocks and dividends are barred by 0068's own rule that an unknown
listing is not priceable and not event-bearing, a derivative's underlying is barred
by [0074](0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md), and
`listing_venues` is derived from the listing-grain identifiers by trigger. So one
sentinel row existed to serve two tables, and the ~30 reads that had to step around
it paid for it.

Two representations of one unknown is the worse cost. A reader of
`instrument_listings` could not tell a line from a placeholder without checking a
column, and a caller that stated no currency was handed the placeholder as though
it were a line -- which is how a posting came to be written on one, against 0072.
Under a nullable column the same question has one answer everywhere: the line is
null exactly when nothing named it.

## Considered options

**Naming a line by `(instrument_id, currency)` in the two identifier tables.** A
null currency would say the same thing, and it is how an archive names a listing
already ([0069](0069-a-listing-is-named-by-a-security-identifier-and-a-currency.md)).
Rejected because listing uniqueness is on the currency *family* and a foreign key
cannot be hung off an expression index, so it needs a materialised family column on
`instrument_listings` and on both referencing tables; because every read that joins
those tables would join on two columns and then look up the listing id it used to
read off the row; and because the state it could express that a nullable line cannot
-- a name filed against a currency whose line does not exist -- never arises, a
stated currency being exactly what mints a line.

**Keeping the null listing.** It is the status quo, and it costs a rule for
completing it in place, a rule for merging two of them, a rule for splitting one
that turns out to be several, and a filter on every read that must not see it.

## Consequences

A security may hold no listings, so `EnsureInstrument` returns no line for a
caller that stated no currency. That is what a caller stating no currency has
named, and it removes the fault by construction rather than by a guard.

An unplaced name is claimed when the security acquires exactly one line, in the
fill-in that gives a posting its line. It carries no currency to match on, so a
security with several lines leaves it unplaced rather than picking one -- the same
refusal made everywhere else in this model.

A merge no longer has an unknown listing to reconcile: it unions the listing sets
by currency family, and the names that name no line travel with the security. The
split in [0071](0071-listings-merge-by-currency-and-an-unknown-one-splits.md) has
nothing left to split.

An archive carries the unplaced names on the security, beside the security-grain
ones, because grain alone no longer says where a name belongs: a listing-grain name
in a file is on a line or on none, and the file has to say which.
