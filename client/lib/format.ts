/** Format a currency value with compact notation (e.g. "$1.2M", "€45.3K"). */
export function formatCurrencyCompact(value: number, currencyCode = "USD"): string {
  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: currencyCode,
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(value);
}

/** Format a currency value with full precision (e.g. "$1,234.56"). */
export function formatCurrency(value: number, currencyCode = "USD"): string {
  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: currencyCode,
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value);
}
