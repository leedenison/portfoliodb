/**
 * Fidelity registry entry and options UI.
 *
 * The converter itself is in ./fidelity-csv, which stays React-free so the
 * import extension can reuse it.
 */

import { Broker } from "@/gen/type/v1/type_pb";
import { convertFidelityToStandard } from "./fidelity-csv";
import { register } from "./registry";
import type { ConverterOptionsProps } from "./registry";

const CURRENCIES = ["GBP", "USD", "EUR", "CHF", "JPY"];

export function FidelityOptions({ onOptionsChange, options }: ConverterOptionsProps) {
  return (
    <div className="space-y-2">
      <label htmlFor="fidelity-currency" className="block text-sm font-medium text-text-primary">
        Currency
      </label>
      <select
        id="fidelity-currency"
        value={(options?.currency as string) ?? ""}
        onChange={(e) => onOptionsChange({ currency: e.target.value || undefined })}
        className="block w-full rounded-lg border border-border bg-surface px-3 py-2 text-text-primary"
      >
        <option value="">Select currency</option>
        {CURRENCIES.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </select>
    </div>
  );
}

register({
  broker: Broker.FIDELITY,
  label: "Fidelity",
  sourcePrefix: "Fidelity",
  formats: [
    {
      id: "fidelity-csv",
      label: "Fidelity CSV",
      convert: convertFidelityToStandard,
      OptionsComponent: FidelityOptions,
    },
  ],
});
