package client

import (
	"context"
	"fmt"
	"net/url"
)

// TickerOverview calls GET /v3/reference/tickers/{ticker} and returns reference data.
func (c *Client) TickerOverview(ctx context.Context, ticker string) (*TickerOverviewResult, error) {
	path := fmt.Sprintf("/v3/reference/tickers/%s", url.PathEscape(ticker))
	var resp APIResponse[TickerOverviewResult]
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return &resp.Results, nil
}
