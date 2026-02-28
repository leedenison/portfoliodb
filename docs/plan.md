## Project Plan

### Milestones


| Milestone ID | Description                                                                                                          | Status |
| ------------ | -------------------------------------------------------------------------------------------------------------------- | ------ |
| M01          | Implement PortfolioDB for holdings only with TimescaleDB extensions (ie. without instrument identification, price fetching or corporate events). Users and authentication are stubbed. | Done   |
| M02          | Implement instrument identification.                                                                                 |        |
| M03          | Implement admin role.                                                                                                |        |
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
| T05     | Implement front end for M01.                                                        | T04                | M01       |        |
| T06     | Design Go instrument identification plugin API.                                     |                    | M02       | Done   |
| T07     | Extend gRPC ingestion API for M02.                                                  | T01                | M02       | Done   |
| T08     | Extend gRPC front end / back end API for M02.                                       | T02                | M02       | Done   |
| T09     | Extend Postgresql datamodel for M02.                                                | T03                | M02       | Done   |
| T10     | Implement instrument identification plugin based on local reference data.           | T06                | M02       | Done   |
| T11     | Extend PortfolioDB service to implement the plugin API and use the plugin from T10. | T10                | M02       | Done   |
| T12     | Implement export/import API for instrument information.                             | T11                | M02       | Done   |
| T13     | Implement network based identification plugin based on IBKR data.                   | T06                | M02       |        |
| T14     | Implement UI for configuring plugins.                                               | T10, T11           | M02       |        |
| T15     | Implement UI for showing instrument identities and errors.                          | T10, T11, T13, T14 | M02       |        |
| T16     | Implement UI for exporting / importing instruments.                                 | T12                | M02       |        |



