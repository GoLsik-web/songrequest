// Package telegram implements the small Bot API subset used by the companion.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	Token, BaseURL string
	RelayKey       string
	HTTP           *http.Client
	stream         relayStream
}
type APIError struct{ Code, RetryAfter int }

func (e *APIError) Error() string { return fmt.Sprintf("Telegram: HTTP/API %d", e.Code) }
func New(token string) *Client {
	return &Client{Token: token, BaseURL: "https://api.telegram.org", HTTP: &http.Client{Timeout: 40 * time.Second}}
}

func NewRelay(baseURL, key string) *Client {
	c := New("")
	c.BaseURL = strings.TrimRight(baseURL, "/")
	c.RelayKey = key
	c.HTTP.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// Never return transport errors: their URLs contain the bot token.
func (c *Client) request(ctx context.Context, method, contentType string, body io.Reader, out any) error {
	endpoint := c.BaseURL + "/bot" + c.Token + "/" + method
	if c.RelayKey != "" {
		endpoint = c.BaseURL + "/api/telegram/" + method
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("Telegram: invalid request")
	}
	req.Header.Set("Content-Type", contentType)
	if c.RelayKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.RelayKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Telegram: connection unavailable")
	}
	defer resp.Body.Close()
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		Code       int             `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope) != nil {
		return fmt.Errorf("Telegram: invalid response (%d)", resp.StatusCode)
	}
	if !envelope.OK {
		code := envelope.Code
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{code, envelope.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
func (c *Client) Call(ctx context.Context, method string, payload, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("Telegram: invalid payload")
	}
	if c.RelayKey != "" && method == "getUpdates" {
		return c.relayUpdates(ctx, data, out)
	}
	return c.request(ctx, method, "application/json", bytes.NewReader(data), out)
}
func (c *Client) Photo(ctx context.Context, chatID, caption string, png []byte) error {
	return c.upload(ctx, "sendPhoto", "photo", "recap.png", chatID, caption, png)
}

func (c *Client) Document(ctx context.Context, chatID, caption, filename string, data []byte) error {
	return c.upload(ctx, "sendDocument", "document", filename, chatID, caption, data)
}

func (c *Client) upload(ctx context.Context, method, field, filename, chatID, caption string, data []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("chat_id", chatID); err != nil {
		return err
	}
	if err := w.WriteField("caption", caption); err != nil {
		return err
	}
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		return err
	}
	if _, err = part.Write(data); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return c.request(ctx, method, w.FormDataContentType(), &buf, nil)
}

type User struct {
	ID int64 `json:"id"`
}
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type Message struct {
	ID   int64  `json:"message_id"`
	Date int64  `json:"date"`
	From User   `json:"from"`
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
}
type Callback struct {
	ID      string  `json:"id"`
	From    User    `json:"from"`
	Message Message `json:"message"`
	Data    string  `json:"data"`
}
type Update struct {
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}

func ValidToken(token string) bool {
	a, b, ok := strings.Cut(token, ":")
	if !ok || len(a) < 5 || len(b) < 20 || len(token) > 200 {
		return false
	}
	for _, r := range a {
		if r < '0' || r > '9' {
			return false
		}
	}
	for _, r := range b {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
