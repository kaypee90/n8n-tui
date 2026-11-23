package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/kaypee90/n8n-tui/internal/models"
)

// ListCredentials returns credential metadata. Secret values are never part
// of the response. Older n8n versions do not expose this endpoint; use
// IsUnsupported to detect that.
func (c *Client) ListCredentials(ctx context.Context) ([]models.Credential, error) {
	return listAll[models.Credential](ctx, c, "/credentials")
}

// ListVariables returns n8n variables (an Enterprise/Pro feature).
func (c *Client) ListVariables(ctx context.Context) ([]models.Variable, error) {
	return listAll[models.Variable](ctx, c, "/variables")
}

func listAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	cursor := ""
	for {
		q := map[string]string{"limit": strconv.Itoa(pageSize)}
		if cursor != "" {
			q["cursor"] = cursor
		}
		var page models.Page[T]
		if err := do(ctx, c.api, http.MethodGet, path, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if page.NextCursor == "" || len(all) >= maxListResult {
			return all, nil
		}
		cursor = page.NextCursor
	}
}
