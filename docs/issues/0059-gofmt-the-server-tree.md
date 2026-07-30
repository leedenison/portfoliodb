---
status: open
title: Format the Go tree with gofmt and keep it formatted
---

`gofmt -l ./server` lists 51 files. Nothing enforces it, so the count only
grows.

## Motivation

The drift is entirely mechanical -- misaligned struct field comments, stray
trailing blank lines -- so every one of those files produces spurious diff
noise the moment anything near the affected lines is touched, and reviewers
cannot tell reformatting from intent.

It is also invisible: `make test` runs the four test targets and nothing else,
and CI runs exactly those targets, so an unformatted file is never reported.

## Design

Run `gofmt -w ./server` once, in a commit that does nothing else so it can be
skipped when reading history.

`server/plugins/eodhd/exchangemap/codes_generated.go` is generated; either
format it at generation time or exclude it, rather than letting a regeneration
undo the fix.

Then add the check to CI so it cannot drift again. It belongs alongside the
existing matrix in `.github/workflows/ci.yml` and as a make target, so the same
command is available locally. `gofmt -l` exits 0 with output rather than
failing, so the target needs to fail on non-empty output itself.

Consider `go vet` at the same time -- it is in the standard toolchain, needs no
new dependency, and catches a different class of problem to the linting in
0060.
