package telegram

import (
	"context"
	"os"
	"testing"
	"time"
)

// Запускается Python-тестом рядом с настоящим локальным обработчиком.
func TestRelayIntegration(t *testing.T) {
	address, key := os.Getenv("SONGREQUEST_TEST_RELAY_URL"), os.Getenv("SONGREQUEST_TEST_RELAY_KEY")
	if address == "" || key == "" {
		t.Skip("requires local relay test harness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := NewRelay(address, key)
	defer c.Close()
	var me struct {
		Username string `json:"username"`
	}
	if err := c.Call(ctx, "getMe", map[string]any{}, &me); err != nil || me.Username != "songrequest_streamer_bot" {
		t.Fatal("identity bridge failed", err)
	}
	var updates []Update
	if err := c.Call(ctx, "getUpdates", map[string]any{"offset": 0}, &updates); err != nil || len(updates) != 1 || updates[0].Message.From.ID != 11 {
		t.Fatal("update routing failed", err)
	}
	if err := c.Call(ctx, "sendMessage", map[string]any{"chat_id": 11, "text": "Integration report"}, nil); err != nil {
		t.Fatal("report bridge failed", err)
	}
	if err := c.Document(ctx, "11", "History", "orders.csv", []byte("user;song\nviewer;track\n")); err != nil {
		t.Fatal("document bridge failed", err)
	}
}
