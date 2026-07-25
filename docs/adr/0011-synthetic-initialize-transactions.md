# Synthetic INITIALIZE transactions from holding declarations

Users who lack transaction history back to portfolio inception can declare a
known holding quantity at a date; the system establishes the correct opening
balance with a synthetic INITIALIZE transaction. The user's holding declaration
is the source of truth and is stored as a first-class record that is never
discarded; the INITIALIZE transaction is **derived** from it and recomputed
whenever its inputs change. Storing the declaration rather than only the computed
balance means the opening balance can be recalculated correctly when the portfolio
start date moves or intervening transactions change.

INITIALIZE transactions are system-managed and not directly editable through the
normal transaction UI — users change them indirectly by editing the declaration.
Making derived data user-editable would let the declaration and its INITIALIZE
diverge. Each carries a `synthetic_purpose` field (`NULL` for real transactions,
`'INITIALIZE'` here) chosen so future synthetic kinds such as TRUE_UP fit the same
mechanism without a schema change. INITIALIZE rows are dated at midnight and must
sort before real transactions on the same day so balance tracking never sees a
transient negative from a same-day real sell preceding the synthetic buy.
