package client

import (
	"context"
	"fmt"
	"net/url"
)

// DailyBars calls GET /v2/aggs/ticker/{ticker}/range/1/day/{from}/{to}
// and returns unadjusted daily OHLCV bars sorted ascending by date.
// from and to are YYYY-MM-DD strings. The range is inclusive on both ends.
// For options, ticker should include the O: prefix (e.g. "O:AAPL250321C00150000").
func (c *Client) DailyBars(ctx context.Context, ticker, from, to string) ([]AggBar, error) {
	path := fmt.Sprintf("/v2/aggs/ticker/%s/range/1/day/%s/%s",
		url.PathEscape(ticker), url.PathEscape(from), url.PathEscape(to))
	path += "?adjusted=false&sort=asc"
	var resp APIResponse[[]AggBar]
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}
