/**
 * Converter registry and all registered converters.
 * Brokers with no converter (SCHB) are registered first for display;
 * then broker-specific converters (Fidelity, IBKR OFX).
 */

import "./brokers";
import "./fidelity";
import "./ibkr-ofx";

export {
  getBrokerOptionsForUpload,
  getBrokerEntry,
  getFormatsForBroker,
  getBrokerLabel,
  getSourcePrefix,
  register,
} from "./registry";
export type { ConverterOptionsProps, FormatEntry, BrokerEntry } from "./registry";
export { FidelityOptions, convertFidelityToStandard } from "./fidelity";
