## Project Plan

### Milestones


| Milestone ID | Description                                                                                                          | Status |
| ------------ | -------------------------------------------------------------------------------------------------------------------- | ------ |
| M01          | Implement PortfolioDB for holdings only (ie. without instrument identification, price fetching or corporate events). |        |
| M02          | Implement instrument identification.                                                                                 |        |
| M03          | Implement price fetching.                                                                                            |        |
| M04          | Implement corporate events.                                                                                          |        |
| M05          | Implement portfolio performance analysis UI.                                                                         |        |


### Tasks


| Task ID | Description                                                                         | Depends on         | Milestone | Status |
| ------- | ----------------------------------------------------------------------------------- | ------------------ | --------- | ------ |
| T01     | Design gRPC ingestion API for M01.                                                  |                    | M01       | Done   |
| T02     | Design gRPC front end / back end API for M01.                                       |                    | M01       |        |
| T03     | Design Postgresql datamodel for M01.                                                |                    | M01       |        |
| T04     | Implement PortfolioDB service for M01.                                              | T01, T02, T03      | M01       |        |
| T05     | Implement front end for M01.                                                        | T04                | M01       |        |
| T06     | Design Go instrument identification plugin API.                                     |                    | M02       |        |
| T07     | Extend gRPC ingestion API for M02.                                                  | T01                | M02       |        |
| T08     | Extend gRPC front end / back end API for M02.                                       | T02                | M02       |        |
| T09     | Extend Postgresql datamodel for M02.                                                | T03                | M02       |        |
| T10     | Implement instrument identification plugin based on local reference data.           | T06                | M02       |        |
| T11     | Extend PortfolioDB service to implement the plugin API and use the plugin from T10. | T06                | M02       |        |
| T12     | Implement network based identification plugin based on IBKR data.                   | T06                | M02       |        |
| T13     | Implement UI for configuring plugins.                                               | T10, T11           | M02       |        |
| T14     | Implement UI for showing instrument identities and errors.                          | T10, T11, T12, T13 | M02       |        |


