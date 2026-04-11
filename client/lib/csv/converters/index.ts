/**
 * Converter registry and all registered converters.
 *
 * Each broker module self-registers on import. Fidelity and IBKR
 * contribute tx converters.
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
export type {
  ConverterOptionsProps,
  FormatEntry,
  BrokerEntry,
} from "./registry";
export { FidelityOptions, convertFidelityToStandard } from "./fidelity";
