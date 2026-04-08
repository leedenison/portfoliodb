package client

import (
	"context"
	"fmt"
	"net/url"
)

const corporateEventPageLimit = 1000

// Splits calls GET /v3/reference/splits filtered by ticker and
// execution_date. The Massive API returns at most corporateEventPageLimit
// rows per page; this method follows next_url until exhausted.
func (c *Client) Splits(ctx context.Context, ticker, fromDate, toDate string) ([]SplitResult, error) {
	q := url.Values{}
	q.Set("ticker", ticker)
	q.Set("execution_date.gte", fromDate)
	q.Set("execution_date.lte", toDate)
	q.Set("limit", fmt.Sprintf("%d", corporateEventPageLimit))
	path := "/v3/reference/splits?" + q.Encode()

	var all []SplitResult
	for path != "" {
		var resp APIResponse[[]SplitResult]
		if err := c.get(ctx, path, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Results...)
		path = c.nextPath(resp.NextURL)
	}
	return all, nil
}

// Dividends calls GET /v3/reference/dividends filtered by ticker and
// ex_dividend_date. Paginated via next_url.
func (c *Client) Dividends(ctx context.Context, ticker, fromDate, toDate string) ([]DividendResult, error) {
	q := url.Values{}
	q.Set("ticker", ticker)
	q.Set("ex_dividend_date.gte", fromDate)
	q.Set("ex_dividend_date.lte", toDate)
	q.Set("limit", fmt.Sprintf("%d", corporateEventPageLimit))
	path := "/v3/reference/dividends?" + q.Encode()

	var all []DividendResult
	for path != "" {
		var resp APIResponse[[]DividendResult]
		if err := c.get(ctx, path, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Results...)
		path = c.nextPath(resp.NextURL)
	}
	return all, nil
}
