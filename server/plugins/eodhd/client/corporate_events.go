package client

import (
	"context"
	"net/url"
)

// Splits fetches stock splits for the given symbol over the inclusive
// [from, to] date range. Symbol is in EODHD format: "{ticker}.{exchange}"
// (e.g. "AAPL.US"). from and to are YYYY-MM-DD strings.
func (c *Client) Splits(ctx context.Context, symbol, from, to string) ([]SplitRow, error) {
	path := "/api/splits/" + url.PathEscape(symbol)
	q := url.Values{}
	q.Set("fmt", "json")
	q.Set("from", from)
	q.Set("to", to)
	var rows []SplitRow
	if err := c.get(ctx, path, q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// Dividends fetches cash dividends for the given symbol over the inclusive
// [from, to] date range. Symbol is in EODHD format: "{ticker}.{exchange}".
// from and to are YYYY-MM-DD strings.
func (c *Client) Dividends(ctx context.Context, symbol, from, to string) ([]DividendRow, error) {
	path := "/api/div/" + url.PathEscape(symbol)
	q := url.Values{}
	q.Set("fmt", "json")
	q.Set("from", from)
	q.Set("to", to)
	var rows []DividendRow
	if err := c.get(ctx, path, q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
