/**
 * Converter registry and all registered converters.
 *
 * Each broker module self-registers on import. Schwab contributes a
 * split extractor only (no tx converter); Fidelity and IBKR contribute
 * tx converters and IBKR additionally contributes a split extractor.
 */

import "./brokers";
import "./fidelity";
import "./ibkr-ofx";
import "./schwab";

export {
  getBrokerOptionsForUpload,
  getBrokerOptionsForSplitExtraction,
  getSplitExtractorsForBroker,
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
  SplitExtractor,
} from "./registry";
export type { ExtractedSplit, SplitParseResult, SplitIdentifier } from "./splits";
export { FidelityOptions, convertFidelityToStandard } from "./fidelity";
export { extractIbkrSplits } from "./ibkr-ofx";
export { extractSchwabSplits } from "./schwab";
