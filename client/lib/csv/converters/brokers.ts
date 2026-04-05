/**
 * Register brokers that have no converter yet (display only in holdings/portfolios).
 */

import { Broker } from "@/gen/api/v1/api_pb";
import { register } from "./registry";

register({
  broker: Broker.SCHB,
  label: "Charles Schwab",
  sourcePrefix: "SCHB",
  formats: [],
});
