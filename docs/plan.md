## Project Plan

### Milestones


| Milestone ID | Description                                                                                                          | Status |
| ------------ | -------------------------------------------------------------------------------------------------------------------- | ------ |
| M01          | Implement PortfolioDB for holdings only with TimescaleDB extensions (ie. without instrument identification, price fetching or corporate events). Users and authentication are stubbed. | Done   |
| M02          | Implement instrument identification.                                                                                 |        |
| M03          | Implement authentication and admin role.                                                                             |        |
| M04          | Implement price fetching.                                                                                            |        |
| M05          | Implement corporate events.                                                                                          |        |
| M06          | Implement portfolio performance analysis UI.                                                                         |        |
| M07          | Implement portfolio sharing between users and aggregates which combine portfolios (incl. shared portfolios).         |        |


### Tasks


| Task ID | Description                                                                         | Depends on         | Milestone | Status |
| ------- | ----------------------------------------------------------------------------------- | ------------------ | --------- | ------ |
| T01     | Design gRPC ingestion API for M01.                                                  |                    | M01       | Done   |
| T02     | Design gRPC front end / back end API for M01.                                       |                    | M01       | Done   |
| T03     | Design Postgresql datamodel for M01.                                                |                    | M01       | Done   |
| T04     | Implement PortfolioDB backend service for M01.                                      | T01, T02, T03      | M01       | Done   |
| T05     | Implement basic SPA with a landing page.                                            |                    | M01       | Done   |
| T06     | Implement front end for M01.                                                        | T04, T05           | M01       |        |
| T07     | Design Go instrument identification plugin API.                                     |                    | M02       | Done   |
| T08     | Extend gRPC ingestion API for M02.                                                  | T01                | M02       | Done   |
| T09     | Extend gRPC front end / back end API for M02.                                       | T02                | M02       | Done   |
| T10     | Extend Postgresql datamodel for M02.                                                | T03                | M02       | Done   |
| T11     | Implement instrument identification plugin based on local reference data.           | T07                | M02       | Done   |
| T12     | Extend PortfolioDB service to implement the plugin API and use the plugin from T10. | T11                | M02       | Done   |
| T13     | Implement export/import API for instrument information.                             | T12                | M02       | Done   |
| T14     | Implement support for underlying instruments for options and futures.               | T07, T08, T09, T10 | M02       | Done   |
| T15     | Implement network based identification plugin based on IBKR data.                   | T07                | M02       |        |
| T16     | Implement UI for configuring plugins.                                               | T11, T12           | M02       |        |
| T17     | Implement UI for showing instrument identities and errors.                          | T11, T12, T14, T15 | M02       |        |
| T18     | Implement UI for exporting / importing instruments.                                 | T13                | M02       |        |
| T19     | Implement authentication in the backend.                                            |                    | M03       | Done   |
| T20     | Create Envoy and Redis docker containers and configuration.                         |                    | M03       | Done   |
| T21     | Implement initial SPA with authentication flow.                                     | T19                | M03       | Done   |




