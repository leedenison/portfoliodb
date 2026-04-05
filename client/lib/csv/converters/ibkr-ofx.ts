/**
 * Register IBKR with OFX/QFX format support.
 */

import { Broker } from "@/gen/api/v1/api_pb";
import { parseOfxStatement } from "@/lib/ofx/parser";
import { register } from "./registry";

register({
  broker: Broker.IBKR,
  label: "IBKR",
  sourcePrefix: "IBKR",
  formats: [
    {
      id: "ibkr-ofx",
      label: "OFX / QFX",
      accept: ".ofx,.qfx",
      convert: parseOfxStatement,
    },
  ],
});
