## **Project Overview**

PortfolioDB is portfolio tracking software which consists of backend services hosted in docker containers, and which serve a web based front end.

PortfolioDBs purpose is to track the holdings (the quantity held) of equities, options and futures for users portfolios.  In addition, PortfolioDB tries to automatically identify the instruments held in the portfolio and, if successful, it can fetch current and historical prices for those instruments in order to provide current and historical portfolio values.  It can also calculate performance metrics such as the time weighted return and the money weighted return.

## **Project Status**

This project is pre-release.  Datamodels, APIs, protobuf definitions, plugin APIs, etc are not considered stable.  Changes to these artifacts should not create migrations or account for backwards compatibility.

## Tech Stack

### Front End

* Next.js (Typescript)  
* Tailwind CSS

### Back End

* APIs will be implemented using Protobuf and gRPC over HTTP/1.1.  
* Envoy for TLS termination and HTTP handling.  
* Back end services will be implemented in Go and implement native gRPC.  
* Data storage will be implemented using Postgresql with TimescaleDB timeseries extensions.  

## Architecture

Directory layout and which component lives where are described in **docs/layout.md**. In short: Next.js front end in **client/**; Go backend in **server/** (service, DB abstraction layer, plugins); shared API definitions in **proto/**; protobuf-generated code under **proto/** (Go) and **client/gen** (TypeScript); migrations in **server/migrations**; docs in **docs/**.

The PortfolioDB service implements a database abstraction layer (in **server/db**): all SQL is confined there so that other server code can be unit tested with mocks. Identity, price-fetcher, and corporate-event plugins are Go libraries under **server/plugins/** compiled into the service binary.

## Development Setup

1. Development is done in the local file system with locally run unit tests.  
2. Testing of the database abstraction layer should be executed in a development docker container running Postgresql with the PortfolioDB datamodel loaded.  
3. A development docker container (see docs/layout.md) should also be available with the running PortfolioDB service and Postgresql database to allow for human QA testing.

## Key Documentation

* docs/layout.md \- Repository directory layout (where each component lives)  
* docs/portfoliodb-spec.md \- Full specification  
* docs/plan.md \- Project plan with milestones

Important: Before implementing any feature or making architectural decisions, consult docs/portfoliodb-spec.md to ensure alignment with the project specification. The spec contains detailed requirements, expected behaviors, and design decisions that should guide implementation.

## Pull Request Guidelines

Prefer smaller, focused PRs to reduce review burden:

* Target size: 500-800 lines changed
* Maximum: Going over is acceptable when necessary, but avoid PRs exceeding 1000 lines if they can be split
* Approach: Break large features into logical increments (e.g., models first, then implementation, then tests)

Smaller PRs are easier to review, less likely to introduce bugs, and create cleaner git history.

When merging PRs always squash the commits and remove the feature branch.

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
3. run `make tools` to install tool depdendencies
4. run `make generate` to generate protobuf bindings and go mocks.

## User Interface

The informantion architecture of the user interface is described in docs/ui/information-architecture.md.  It describes key concepts for users (and admin users), how they relate to each other, the relative importance they carry for the user and gives the example of how the information architecture should impact global navigation.

The CSV format specification for transaction uploads is documented in docs/csv-format.md.

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

Refer to docs/style/\<language\>.md for style guidance on specific languages.

## Testing

Refer to docs/testing.md for guidance on unit, functional and integration testing.
