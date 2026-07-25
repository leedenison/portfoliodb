# Normalize MIC_TICKER exchange codes to operating MICs

MIC_TICKER identifier domains are always stored as operating MICs (ISO 10383
mic_type 'O'); a supplied segment MIC (mic_type 'S') is silently normalized to
its operating MIC via the `exchanges` table before storage (e.g. XNGS to XNAS).
Different data providers disagree about which segment is "primary" for an
instrument, so normalizing to the operating MIC means the same instrument is
always identified by the same MIC regardless of provider, and consistency checks
between plugins and import hints can compare at the operating-MIC level without
treating differing segments as a conflict.

Provider-specific exchange codes that a provider's own API still requires (segment
MICs for Polygon/Massive, EODHD exchange codes, venue FIGIs) are kept separately
in `provider_instrument_identifiers`, not in the canonical identifier vocabulary,
so provider quirks never pollute canonical identity.
