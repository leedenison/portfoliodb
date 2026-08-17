## **Project Overview**

PortfolioDB is portfolio tracking software which consists of backend services hosted in docker containers, and which serve a web based front end.

PortfolioDBs purpose is to track the holdings (the quantity held) of equities, options and futures for users portfolios.  In addition, PortfolioDB tries to automatically identify the instruments held in the portfolio and, if successful, it can fetch current and historical prices for those instruments in order to provide current and historical portfolio values.  It can also calculate performance metrics such as the time weighted return and the money weighted return.

## **Project Status**

This project is pre-release.  Datamodels, APIs, protobuf definitions, plugin APIs, etc are not considered stable.  Changes to these artifacts should not create migrations or account for backwards compatibility.

Retiring a proto field therefore does not reserve its number. Delete the field
outright, renumber the message so its field numbers run from 1 without gaps, and
put the fields in whatever order reads best rather than the order they were
added. Nothing persists encoded protobuf across a deployment, so no reader can
be holding the old numbering. Why a field went belongs in an ADR or the spec,
not in a `reserved` comment.

## Tech Stack

### Front End

* Next.js (Typescript)
* Tailwind CSS
* Recharts (charting)

### Browser Extension

* Chrome MV3 (Typescript)
* Vite (build), Vitest (unit tests)

### Back End

* APIs will be implemented using Protobuf and gRPC over HTTP/1.1.  
* Envoy for TLS termination and HTTP handling.  
* Back end services will be implemented in Go and implement native gRPC.  
* Data storage will be implemented using Postgresql with TimescaleDB timeseries extensions.  

## Architecture

Directory layout and which component lives where are described in **docs/layout.md**. In short: Next.js front end in **client/**; Go backend in **server/** (service, DB abstraction layer, plugins); shared API definitions in **proto/**; protobuf-generated code under **proto/** (Go) and **client/gen** (TypeScript); migrations in **server/migrations**; docs in **docs/**.

The PortfolioDB service implements a database abstraction layer (in **server/db**): all SQL is confined there so that other server code can be unit tested with mocks. Identity, price-fetcher, and corporate-event plugins are Go libraries under **server/plugins/** compiled into the service binary.

A Chrome MV3 extension in **extension/** automates transaction import from broker websites. It is a client of the same gRPC-Web API as the SPA and imports the client's CSV converters and generated protobuf types through a tsconfig path alias, so converters under **client/lib/csv/converters/** must stay free of React. Build with `make extension`; behaviour is specified in docs/spec/broker-import-extension.md.

## Development Setup

1. Development is done in the local file system with locally run unit tests.  
2. Testing of the database abstraction layer should be executed in a development docker container running Postgresql with the PortfolioDB datamodel loaded.  
3. A development docker container (see docs/layout.md) should also be available with the running PortfolioDB service and Postgresql database to allow for human QA testing.

## Key Documentation

* docs/layout.md \- Repository directory layout (where each component lives)  
* docs/spec/portfoliodb-spec.md \- Full specification (behaviour); sub-specs live alongside it in docs/spec/  
* docs/issues/ \- Milestones and issues (see docs/issues/milestones.md); governed by the `issue-tracker` skill
* docs/adr/ \- Architecture decision records (why decisions were made, kept out of the spec); governed by the `adr` skill

Important: Before implementing any feature or making architectural decisions, consult docs/spec/portfoliodb-spec.md to ensure alignment with the project specification. The spec contains detailed requirements, expected behaviors, and design decisions that should guide implementation.

## Pull Request Guidelines

Prefer smaller, focused PRs to reduce review burden:

* Target size: 500-800 lines changed
* Maximum: Going over is acceptable when necessary, but avoid PRs exceeding 1000 lines if they can be split
* Approach: Break large features into logical increments (e.g., models first, then implementation, then tests)

Smaller PRs are easier to review, less likely to introduce bugs, and create cleaner git history.

### Before opening a PR

Run `make e2e-test` and get it passing, alongside `make check` and `make test`.
CI runs neither the e2e suite nor anything that would notice it rotting, so a
break there is invisible until someone runs it by hand.

A spec that fails for a reason the change did not cause is still a result to
act on: say so in the PR description, with the evidence that it fails on an
unmodified tree.

### Merging

Always squash: `gh pr merge <n> --squash`. **Never pass `--delete-branch`.** The
repository has `delete_branch_on_merge` enabled, so the branch is removed as part
of the merge, and that merge-linked deletion is what retargets any PR based on
the branch. An explicit ref deletion is a plain branch deletion instead, which
**closes** dependent PRs rather than retargeting them.

Merge a stack parent first, one at a time, and let each merge retarget the next.

CI runs on `pull_request` against `main`, and a retarget is an `edited` event the
workflow does not listen for. So when a merge retargets the next PR in the stack
and it needs no conflict resolution, its required checks will not have run --
push to the branch to start them. When the retarget does leave conflicts,
resolving them produces a push and CI runs on its own.

### Branching Workflow

When a plan calls for multiple PRs, create and complete each PR on its own feature branch before starting the next. Do not implement all changes on a single branch or on main and attempt to separate them afterward -- this is error-prone and creates unnecessary rework.

Workflow for multi-PR plans:

1. Create a feature branch from main for PR 1
2. Implement, commit, push, and open the PR
3. Switch back to main before starting PR 2
4. If PR 2 depends on PR 1, branch from the PR 1 branch instead and note the dependency in the PR description

If the changes cannot be cleanly separated into independent PRs (e.g., extensive cross-cutting modifications), it is acceptable to use a single PR. State in the PR description why it was not split.

### Worktrees

Whenever you begin work in a new worktree you should:
1. Copy the .env file from the root of the repo
2. Copy the local directory from the root of the repo
3. run `make generate` to generate protobuf bindings and go mocks.

## User Interface

The informantion architecture of the user interface is described in docs/spec/information-architecture.md.  It describes key concepts for users (and admin users), how they relate to each other, the relative importance they carry for the user and gives the example of how the information architecture should impact global navigation.

The archive format -- the single import and export format that replaced the
transaction CSV, the price CSV, the instrument JSON and the corporate event JSON
-- is documented in docs/spec/archive-format.md. Broker-specific files, which
PortfolioDB reads but does not define, are converted into it on the way in.

Automated transaction import via the browser extension is specified in docs/spec/broker-import-extension.md.

Use text placeholders for unimplemented functionality as development progresses.  It should always be possible to see where UI elements will be displayed even if they are not yet implemented.

## Naming

Prefer terse names when naming functions and variables.  In particular here are some names that should be made terse:

* Transaction (when referring to financial transactions, not database transactions) should be shortened to Tx.

## Documentation

Prefer to use comments in the code, protobufs and sql files rather than creating separate files when documenting APIs, datamodels and functions.

Do not refer to project tasks or milestones in comments.

Never use smart quotes when generating documentation or plans.

## Style Guides

Use language specific, idiomatic solutions to problems when possible.

Language style and conventions live in the `go`, `protobuf`, and `typescript`
skills (`.claude/skills/`).

## Testing

Testing guidance lives in the `unit-testing`, `integration-testing`, and
`e2e-testing` skills (`.claude/skills/`).

## Personal Data

The repository must contain no personal data belonging to a real person. This
applies to code, tests, fixtures, comments, commit messages, issues, specs and
plans alike. `local/` is gitignored and is where real broker exports live; it is
the only place they belong.

Never commit any of the following:

* Real people's names, including in a comment describing where test data came
  from, in an issue discussing it, and in the name of a file that holds it. Refer
  to the account or the export rather than to whoever owns it.
* Real broker account numbers, and any identifier that embeds one.
* Real transaction, order or statement reference numbers issued by a broker.
* Real email addresses, postal addresses and telephone numbers.

Public security identifiers -- tickers, ISINs, CUSIPs, MICs, exchange codes --
are reference data rather than personal data, and are fine to use as they are.

Test data modelled on a real broker export is the normal way to pin down a
format, and it stays welcome: copy the shape, the column order and the quirks,
then replace every account number and reference with an invented one before
committing. Invent them so that the properties the test depends on survive --
distinctness, ordering, and the numeric distance between references where a test
compares them -- and say in a comment that the file is modelled on a real export
with its identifiers replaced, so the next reader does not restore them from the
original. Amounts, dates and instruments carry no identifier and may stay as they
are.
