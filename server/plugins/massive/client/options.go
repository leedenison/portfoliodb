package client

import (
	"context"
	"fmt"
	"net/url"
)

// OptionsContract calls GET /v3/reference/options/contracts/{options_ticker} and returns contract details.
func (c *Client) OptionsContract(ctx context.Context, optionsTicker string) (*OptionsContractResult, error) {
	path := fmt.Sprintf("/v3/reference/options/contracts/%s", url.PathEscape(optionsTicker))
	var resp APIResponse[OptionsContractResult]
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return &resp.Results, nil
}
