package config

// Telegram contains only preferences. The token belongs in Windows Credential Manager.
type Telegram struct {
	Enabled        bool   `json:"enabled"`
	OwnerID        int64  `json:"owner_id"`
	AutoRecap      bool   `json:"auto_recap"`
	NotifyOrders   bool   `json:"notify_orders"`
	PublishChannel string `json:"publish_channel"`
}
