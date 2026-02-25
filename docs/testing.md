# Testing Guidance

## Unit Testing

Unit tests should use mocks (either using a mocking library or as ephemeral structs) to limit dependencies on code that is not under test (except in the case of the database abstraction layer, see below).  Unit tests should focus on the behaviour of the code under test, not the behaviour of depdendencies.

Unit tests for the database abstraction layer should require a running postgres instance with an initialised datamodel.  These should be executed separately from regular unit tests via a make command.  The make command should create a docker test environment with a running postgres instance so that the tests can be run.  The datamodel should be reset after every test by rolling back a transaction.