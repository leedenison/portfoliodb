# PortfolioDB

A gRPC server for portfolio database operations.

## Prerequisites

- Rust 1.70+
- Make

### Build the service

```bash
make all
```

## Running the service

After building, you can run the service with:

```bash
./target/release/portfoliodb --port 50001
```

## Service endpoints

The service provides the following gRPC endpoints:

- `UpdateTxs` - Update transactions for an account
- `GetHoldings` - Get holdings timeseries
- `UpdatePrices` - Update price data
- `GetPrices` - Get price timeseries
- `UpdateInstrument` - Update instrument metadata
