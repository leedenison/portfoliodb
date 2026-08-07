/**
 * Fidelity UK (fidelity.co.uk).
 *
 * The site's own CSV export is produced by fetching these rows and posting them
 * back to a formatting endpoint, so this reads the same data one step earlier.
 * The request needs nothing but the session cookie and the XMLHttpRequest header
 * -- no CSRF token and none of the fingerprint headers the page also sends.
 */

import { Broker } from "@/gen/type/v1/type_pb";
import { convertFidelityJson } from "./fidelity-json";
import type { BrokerRecipe } from "./types";

export const fidelityUk: BrokerRecipe = {
  id: "fidelity-uk",
  broker: Broker.FIDELITY,
  sourcePrefix: "Fidelity",
  // Deliberately not "fidelity-json": the web client's Fidelity uploads already
  // resolve under this source, and changing it would fork those instruments.
  sourceFormatId: "fidelity-csv",
  origins: ["https://www.fidelity.co.uk/*"],
  homeUrl: "https://www.fidelity.co.uk/secure/accounts/",
  timeZone: "Europe/London",
  dateFormat: "dd/MM/yyyy",
  export: {
    method: "GET",
    url: "https://www.fidelity.co.uk/gateway/ei/txnhsty/v1/secure/customer/transactions?&fromDate={{from}}&toDate={{to}}",
    headers: { "x-requested-with": "XMLHttpRequest" },
  },
  convert: convertFidelityJson,
};
