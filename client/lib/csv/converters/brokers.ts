/**
 * Register brokers that have no converter yet (display only in holdings and
 * portfolios).
 *
 * Empty: every broker in the Broker enum that has data to convert has its own
 * module. SCHB does not -- see docs/issues/0073-schwab-client-converter.md --
 * so a Schwab upload has only the standard format to go through.
 */

export {};
