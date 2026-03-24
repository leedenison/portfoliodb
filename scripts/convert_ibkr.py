#!/usr/bin/env python3
"""Convert IBKR CSV export to PortfolioDB standard CSV format.

Generates test data by:
- Mapping IBKR transaction types to standard OFX types
- Splitting BUY/SELL into security + CASHFLOW rows
- Randomly distributing dates over a configurable range
- Replacing expired options with valid ones via Massive.com API
- Leaving identifier hint fields blank for testing
"""

import argparse
import csv
import json
import random
import sys
import time
import urllib.request
import urllib.error
from datetime import date, timedelta

# IBKR column indices (positional due to duplicate "Type" header)
COL_DATE = 0
COL_ACCOUNT = 1
COL_TYPE = 2
COL_SUBTYPE = 3
COL_DESC = 4
COL_TICKER = 5
COL_UNITS = 6
COL_PRICE = 7
COL_AMOUNT = 8
COL_CURRENCY = 9
COL_EXCHANGE = 10
COL_SYMBOL = 11
COL_OPTION = 12
COL_ASSET_CLASS = 13

# Standard CSV header
STANDARD_HEADER = [
    "date", "instrument_description", "type", "quantity",
    "trading_currency", "settlement_currency", "unit_price",
    "account", "exchange_code_hint", "mic_hint",
    "isin", "ticker", "openfigi_share_class", "occ",
]

# IBKR types to skip
SKIP_TYPES = {
    "CASH", "ExchTrade", "Deposits/Withdrawals",
    "Broker Interest Paid", "Broker Interest Received",
    "Withholding Tax", "Payment In Lieu Of Dividends",
}

# Types that produce a CASHFLOW companion row
TRADE_SUBTYPES = {"BUYSTOCK", "BUYOPT", "SELLSTOCK", "SELLOPT"}


def parse_args():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("input_file", help="Path to IBKR CSV")
    p.add_argument("-o", "--output", help="Output file (default: stdout)")
    p.add_argument("--start-date", help="Start of date range (YYYY-MM-DD)")
    p.add_argument("--end-date", help="End of date range (YYYY-MM-DD)")
    p.add_argument("--api-key", help="Massive.com API key for option lookups")
    p.add_argument("--seed", type=int, default=42, help="Random seed")
    return p.parse_args()


def parse_date(s):
    return date.fromisoformat(s.replace("/", "-"))


def strip_commas(s):
    return s.replace(",", "")


def map_type(ibkr_type, ibkr_subtype):
    """Map IBKR Type/Subtype to standard transaction type."""
    if ibkr_type == "INITIALISE":
        return "TRANSFER"
    if ibkr_type == "STOCKSPLIT":
        return "SPLIT"
    if ibkr_type == "TRANSFER":
        return "TRANSFER"
    if ibkr_type == "INCOME":
        return "INCOME"
    if ibkr_subtype in TRADE_SUBTYPES:
        return ibkr_subtype
    return None


def is_option_row(row):
    return row[COL_ASSET_CLASS] == "OPTION"


def option_type(row):
    """Return 'P' or 'C' from the OCC symbol in the Option column."""
    occ = row[COL_OPTION].strip()
    if not occ:
        return None
    for ch in occ:
        if ch in ("P", "C"):
            return ch
    return None


def _fetch_contracts(underlying, contract_type, api_key):
    """Fetch option contracts of a given type for an underlying. Returns list of ticker strings."""
    url = (
        f"https://api.massive.com/v3/reference/options/contracts"
        f"?underlying_ticker={underlying}&contract_type={contract_type}&apiKey={api_key}"
    )
    max_retries = 3
    for attempt in range(max_retries):
        try:
            req = urllib.request.Request(url)
            with urllib.request.urlopen(req, timeout=30) as resp:
                data = json.loads(resp.read().decode())
            time.sleep(2)
            results = data.get("results", [])
            return [c.get("ticker", "") for c in results if c.get("ticker")]
        except urllib.error.HTTPError as e:
            if e.code == 429 and attempt < max_retries - 1:
                wait = 15 * (attempt + 1)
                print(f"  Rate limited, waiting {wait}s...", file=sys.stderr)
                time.sleep(wait)
                continue
            print(f"WARNING: Failed to fetch {contract_type} options for {underlying}: {e}", file=sys.stderr)
            return []
        except urllib.error.URLError as e:
            print(f"WARNING: Failed to fetch {contract_type} options for {underlying}: {e}", file=sys.stderr)
            return []
    return []


def fetch_options(underlying, api_key, cache, needed_types):
    """Fetch available option contracts for an underlying ticker from Massive.com."""
    if underlying in cache:
        return cache[underlying]

    result = {"P": [], "C": []}
    if "P" in needed_types:
        result["P"] = _fetch_contracts(underlying, "put", api_key)
    if "C" in needed_types:
        result["C"] = _fetch_contracts(underlying, "call", api_key)

    cache[underlying] = result
    if not result["P"] and not result["C"]:
        print(f"WARNING: No valid options found for {underlying}", file=sys.stderr)
    return result


def pick_replacement_option(opt_type, options_by_type, rng):
    """Pick any valid option of the given type."""
    candidates = options_by_type.get(opt_type, [])
    if not candidates:
        return None
    return rng.choice(candidates)


def make_row(dt, desc, tx_type, qty, currency, price, account, exchange):
    return [
        dt.isoformat(),  # date
        desc,             # instrument_description
        tx_type,          # type
        qty,              # quantity
        currency,         # trading_currency
        "",               # settlement_currency
        price,            # unit_price
        account,          # account
        exchange,         # exchange_code_hint
        "",               # mic_hint
        "",               # isin
        "",               # ticker
        "",               # openfigi_share_class
        "",               # occ
    ]


def convert(args):
    rng = random.Random(args.seed)

    today = date.today()
    start = parse_date(args.start_date) if args.start_date else today - timedelta(days=365)
    end = parse_date(args.end_date) if args.end_date else today
    date_range_days = (end - start).days
    if date_range_days <= 0:
        print("ERROR: end-date must be after start-date", file=sys.stderr)
        sys.exit(1)

    # Read input
    with open(args.input_file, newline="", encoding="utf-8") as f:
        reader = csv.reader(f)
        header = next(reader)  # skip header
        rows = list(reader)

    # Filter convertible rows
    convertible = []
    for row in rows:
        if len(row) < 14:
            continue
        ibkr_type = row[COL_TYPE].strip()
        if ibkr_type in SKIP_TYPES:
            continue
        ibkr_subtype = row[COL_SUBTYPE].strip()
        # Skip CASH INITIALISE
        if ibkr_type == "CASH":
            continue
        tx_type = map_type(ibkr_type, ibkr_subtype)
        if tx_type is None:
            print(f"WARNING: Unmapped type {ibkr_type}/{ibkr_subtype}, skipping", file=sys.stderr)
            continue
        convertible.append((row, tx_type))

    # Collect unique underlyings and which option types are needed
    options_cache = {}
    if args.api_key:
        needed = {}  # underlying -> set of "P"/"C"
        for row, tx_type in convertible:
            if is_option_row(row):
                ul = row[COL_SYMBOL].strip()
                ot = option_type(row)
                if ot:
                    needed.setdefault(ul, set()).add(ot)
        for underlying in sorted(needed):
            types = needed[underlying]
            print(f"Fetching options for {underlying} ({','.join(sorted(types))})...", file=sys.stderr)
            fetch_options(underlying, args.api_key, options_cache, types)

    # Build output rows
    output_rows = []
    for row, tx_type in convertible:
        dt = start + timedelta(days=rng.randint(0, date_range_days))
        account = row[COL_ACCOUNT].strip()
        currency = row[COL_CURRENCY].strip()
        exchange = row[COL_EXCHANGE].strip()
        units = strip_commas(row[COL_UNITS].strip())
        price = strip_commas(row[COL_PRICE].strip())
        amount = strip_commas(row[COL_AMOUNT].strip())
        desc = row[COL_TICKER].strip()

        # Replace expired options with valid ones
        if is_option_row(row) and args.api_key:
            underlying = row[COL_SYMBOL].strip()
            opt_t = option_type(row)
            if opt_t and underlying in options_cache:
                replacement = pick_replacement_option(opt_t, options_cache[underlying], rng)
                if replacement:
                    desc = replacement
                else:
                    print(
                        f"WARNING: No replacement {opt_t} option for {underlying}, "
                        f"keeping original description",
                        file=sys.stderr,
                    )

        # Security row
        output_rows.append(make_row(dt, desc, tx_type, units, currency, price, account, exchange))

        # CASHFLOW companion for trades
        if tx_type in TRADE_SUBTYPES and amount:
            cash_desc = currency
            output_rows.append(
                make_row(dt, cash_desc, "CASHFLOW", amount, currency, "1", account, exchange)
            )

    # Sort by date
    output_rows.sort(key=lambda r: r[0])

    # Write output
    out = open(args.output, "w", newline="", encoding="utf-8") if args.output else sys.stdout
    try:
        writer = csv.writer(out)
        writer.writerow(STANDARD_HEADER)
        for r in output_rows:
            writer.writerow(r)
    finally:
        if args.output:
            out.close()


if __name__ == "__main__":
    convert(parse_args())
