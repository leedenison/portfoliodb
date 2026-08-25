// Package identification resolves what a source said about an instrument into
// one security and one of its currency lines, by calling the identifier plugins
// and merging what they answer.
//
// # The agreement predicates
//
// Most of this package is predicates about whether two things agree, and they
// are three stages rather than a heap. A result reaching the merge has passed
// the first two; the third grades what came out.
//
//  1. Line admission -- may this result be merged into the winner's at all?
//     lineMismatch asks whether the two described different lines, which the
//     currency decides and the venue decides only where the currency is silent.
//     idMismatch asks whether their identifiers contradict each other about the
//     security. consistentWith runs the two and logs whichever said no.
//
//  2. Security corroboration -- did anything name the security both described?
//     securityClaim narrows a result to what it said about which security,
//     using identifier.CorroboratesSecurity to decide which types carry that.
//     corroborated looks for one value in common. Agreeing about the line is
//     the query restated rather than evidence, so admission and corroboration
//     are separate gates and both must pass (adr/0078).
//
//  3. Field confirmation -- what did the answer that won argue with, and what
//     did it hold up against? CompareHints reports what a result contradicted;
//     confirmedFields reports what it corroborated. The two are not
//     complements: a field neither side supplied appears in neither list, which
//     is what tells a result that was checked and held from one that said too
//     little to check.
//
// Stages 1 and 2 are asked of one plugin result against another. Stage 3 is
// asked of the chosen answer against what the caller knew independently, and so
// exists twice, once per grain of answer: CompareHints and confirmedFields take
// a plugin result, which is about one line; CompareDBMeta and ConfirmedDBFields
// take what the database returned on its own, which is about whatever lines the
// identifier reached. Each pair is one question at two grains, and the grain is
// the whole of the difference -- it is why the database pair tests currency by
// membership where the plugin pair tests it by equality.
//
// Under all of them is sameSubject, which decides whether two identifiers may
// be compared at all: the domain scopes the value, so a ticker at NASDAQ and a
// ticker in London are two subjects and not two answers about one.
//
// The venue predicates here are micStated and micAmongStated, and their doc
// comments say which nearby questions they are not.
package identification
