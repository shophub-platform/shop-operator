// Package discord is a minimal Discord REST API client used by the
// DiscordChannel reconciler (spec 10.2): it creates text channels and webhooks
// and deletes channels on cleanup.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// defaultAPIBase is the Discord REST API base URL. Overridable via the
// DISCORD_API_BASE environment variable (useful for tests / mock servers).
const defaultAPIBase = "https://discord.com/api/v10"

// channelTypeText is the Discord channel type for a guild text channel.
const channelTypeText = 0

// Client talks to the Discord REST API using a bot token.
type Client struct {
	token string
	base  string
	http  *http.Client
}

// NewClient builds a Discord client for the given bot token.
func NewClient(token string) *Client {
	base := os.Getenv("DISCORD_API_BASE")
	if base == "" {
		base = defaultAPIBase
	}
	return &Client{
		token: token,
		base:  base,
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

// CreateChannel creates a guild text channel and returns its ID.
// POST /guilds/{guild.id}/channels
func (c *Client) CreateChannel(ctx context.Context, guildID, name string) (string, error) {
	payload := map[string]interface{}{"name": name, "type": channelTypeText}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/guilds/%s/channels", guildID), payload, &out); err != nil {
		return "", fmt.Errorf("create channel: %w", err)
	}
	if out.ID == "" {
		return "", fmt.Errorf("create channel: empty id in response")
	}
	return out.ID, nil
}

// CreateWebhook creates a webhook on the channel and returns its ID and URL.
// POST /channels/{channel.id}/webhooks
func (c *Client) CreateWebhook(ctx context.Context, channelID, name string) (id, url string, err error) {
	payload := map[string]interface{}{"name": name}
	var out struct {
		ID    string `json:"id"`
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/channels/%s/webhooks", channelID), payload, &out); err != nil {
		return "", "", fmt.Errorf("create webhook: %w", err)
	}
	if out.ID == "" {
		return "", "", fmt.Errorf("create webhook: empty id in response")
	}
	// Discord does not always return a "url" field; construct the execution URL
	// from the id and token when needed.
	if out.URL == "" {
		out.URL = fmt.Sprintf("%s/webhooks/%s/%s", c.base, out.ID, out.Token)
	}
	return out.ID, out.URL, nil
}

// FindChannelByName returns the ID of an existing guild channel with the given
// name, if one exists. Used to make reconciliation idempotent (avoid creating
// duplicate channels).
// GET /guilds/{guild.id}/channels
func (c *Client) FindChannelByName(ctx context.Context, guildID, name string) (string, bool, error) {
	var channels []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/guilds/%s/channels", guildID), nil, &channels); err != nil {
		return "", false, fmt.Errorf("list channels: %w", err)
	}
	for _, ch := range channels {
		if ch.Name == name {
			return ch.ID, true, nil
		}
	}
	return "", false, nil
}

// FindWebhook returns an existing webhook on the channel with the given name.
// GET /channels/{channel.id}/webhooks
func (c *Client) FindWebhook(ctx context.Context, channelID, name string) (id, url string, found bool, err error) {
	var hooks []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/channels/%s/webhooks", channelID), nil, &hooks); err != nil {
		return "", "", false, fmt.Errorf("list webhooks: %w", err)
	}
	for _, h := range hooks {
		if h.Name == name {
			u := h.URL
			if u == "" {
				u = fmt.Sprintf("%s/webhooks/%s/%s", c.base, h.ID, h.Token)
			}
			return h.ID, u, true, nil
		}
	}
	return "", "", false, nil
}

// DeleteChannel deletes a channel. A 404 is treated as success (already gone).
// DELETE /channels/{channel.id}
func (c *Client) DeleteChannel(ctx context.Context, channelID string) error {
	if err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/channels/%s", channelID), nil, nil); err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	return nil
}

// do performs an authenticated request and decodes a JSON response into out
// (when non-nil). A 404 on DELETE is not treated as an error.
func (c *Client) do(ctx context.Context, method, path string, body, out interface{}) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	if method == http.MethodDelete && resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord API %s %s: status %d: %s", method, path, resp.StatusCode, string(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
