# UI concept visibility and navigation model

Several domain concepts are deliberately kept out of the user's way. The epoch
start date, initialization transactions, and holdings checkpoints are surfaced
only when absolutely necessary — they are bookkeeping mechanics a user should not
have to reason about to read their holdings. "All Holdings" is presented as a
default portfolio that every user has and that cannot be renamed or deleted, so
there is always a well-defined unfiltered view. Trade notifications (automated
single-transaction uploads) are shown separately from manual uploads because
users conceive of the two as distinct, even though the system treats them
similarly.

Global navigation is split into a top bar and a left sidebar to mirror the
information architecture: the top bar holds the portfolio selector and
account-level actions, while the left sidebar holds the primary working views,
all scoped to the selected portfolio. Uploads are reached from the top-bar user
menu rather than the portfolio-scoped sidebar because a single upload can span
transactions in multiple portfolios, so it is not meaningfully scoped to one.
