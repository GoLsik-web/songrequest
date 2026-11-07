package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

type relayStream struct {
	mu         sync.Mutex
	connection *websocket.Conn
}

func (c *Client) Close() {
	c.stream.mu.Lock()
	defer c.stream.mu.Unlock()
	if c.stream.connection != nil {
		c.stream.connection.CloseNow()
		c.stream.connection = nil
	}
}

// Постоянное соединение не создаёт новый HTTP-запрос на каждый опрос.
func (c *Client) relayUpdates(ctx context.Context, data []byte, out any) error {
	c.stream.mu.Lock()
	defer c.stream.mu.Unlock()
	if c.stream.connection == nil {
		u, err := url.Parse(strings.TrimRight(c.BaseURL, "/") + "/stream")
		if err != nil {
			return fmt.Errorf("Telegram relay: invalid address")
		}
		if u.Scheme == "https" {
			u.Scheme = "wss"
		} else {
			u.Scheme = "ws"
		}
		connection, response, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: c.HTTP, HTTPHeader: http.Header{"Authorization": []string{"Bearer " + c.RelayKey}}})
		if err != nil {
			if response != nil && response.StatusCode >= 400 {
				return &APIError{Code: response.StatusCode}
			}
			return fmt.Errorf("Telegram relay: connection unavailable")
		}
		connection.SetReadLimit(4 << 20)
		c.stream.connection = connection
	}
	failed := func() error {
		c.stream.connection.CloseNow()
		c.stream.connection = nil
		return fmt.Errorf("Telegram relay: connection interrupted")
	}
	if err := c.stream.connection.Write(ctx, websocket.MessageText, data); err != nil {
		return failed()
	}
	_, raw, err := c.stream.connection.Read(ctx)
	if err != nil {
		return failed()
	}
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		Code       int             `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return failed()
	}
	if !envelope.OK {
		return &APIError{envelope.Code, envelope.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
