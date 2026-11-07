package twitch

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// StreamStatus uses the existing authenticated Twitch connection.
func (c *Client) StreamStatus(ctx context.Context, userID string) (bool, time.Time, error) {
	var result struct {
		Data []struct {
			StartedAt time.Time `json:"started_at"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/streams?user_id="+url.QueryEscape(userID), nil, &result); err != nil {
		return false, time.Time{}, err
	}
	if len(result.Data) == 0 {
		return false, time.Time{}, nil
	}
	return true, result.Data[0].StartedAt, nil
}
