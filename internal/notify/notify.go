// Package notify posts job completion events to chat webhooks (Discord and
// Slack incoming webhooks, or a generic JSON endpoint) so a deploy/logs-sync
// success or failure is visible outside the TUI/CLI, e.g. in a team channel.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Event is the information a webhook notification is built from. It mirrors
// the subset of jobs.Snapshot that's meaningful outside the process. Field
// names use JSON tags matching the snake_case used elsewhere in the API
// (e.g. jobs.Snapshot) since this struct is sent as-is to any non-Discord/
// Slack endpoint.
type Event struct {
	DeviceID  string    `json:"device_id"`
	Operation string    `json:"operation"`
	Succeeded bool      `json:"succeeded"`
	Message   string    `json:"message,omitempty"`
	Time      time.Time `json:"time"`
}

// Sender delivers an Event to one destination.
type Sender interface {
	Send(ctx context.Context, ev Event) error
}

// Webhook posts Events as an incoming webhook. The JSON payload shape is
// picked from the URL's host: Discord and Slack both expect their own
// non-interchangeable schema, and anything else gets a plain generic
// object so a self-hosted endpoint (e.g. a custom IRC bridge) can still
// consume it.
type Webhook struct {
	URL        string
	HTTPClient *http.Client
}

// NewWebhook returns a Webhook posting to url with a bounded-timeout client.
func NewWebhook(url string) *Webhook {
	return &Webhook{
		URL:        url,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Send posts ev to w.URL. It reports a non-2xx response or transport error
// so callers can log it, but a notification failure never fails the job it
// describes — see how callers in internal/server invoke this in a
// best-effort goroutine.
func (w *Webhook) Send(ctx context.Context, ev Event) error {
	body, err := json.Marshal(w.payload(ev))
	if err != nil {
		return fmt.Errorf("encoding webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building webhook request: %w", redactURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")

	client := w.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending webhook: %w", redactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// redactURLError strips the target URL out of a *url.Error before it's
// logged: http.Client.Do wraps transport failures in *url.Error, which
// embeds the full request URL (including the webhook's secret token) in
// both its URL field and its Error() string. Any other error is returned
// unchanged.
func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return &url.Error{Op: urlErr.Op, URL: "[redacted]", Err: urlErr.Err}
	}
	return err
}

func (w *Webhook) payload(ev Event) any {
	text := formatText(ev)
	switch {
	case isDiscordWebhook(w.URL):
		return discordPayload{Content: text}
	case isSlackWebhook(w.URL):
		return slackPayload{Text: text}
	default:
		return ev
	}
}

// isDiscordWebhook and isSlackWebhook match on the URL's parsed host (and,
// for Discord, its path prefix) rather than substring-matching the whole
// URL, so a self-hosted endpoint whose path or query happens to contain
// "hooks.slack.com" or "discord.com/api/webhooks" isn't misdetected as the
// real thing and sent the wrong payload schema. An unparseable URL matches
// neither and falls through to the generic payload.
func isDiscordWebhook(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return (host == "discord.com" || host == "discordapp.com") && strings.HasPrefix(u.Path, "/api/webhooks/")
}

func isSlackWebhook(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.ToLower(u.Hostname()) == "hooks.slack.com"
}

// discordPayload is the minimal shape a Discord incoming webhook accepts:
// https://discord.com/developers/docs/resources/webhook#execute-webhook.
type discordPayload struct {
	Content string `json:"content"`
}

// slackPayload is the minimal shape a Slack incoming webhook accepts.
type slackPayload struct {
	Text string `json:"text"`
}

func formatText(ev Event) string {
	status := "succeeded"
	if !ev.Succeeded {
		status = "failed"
	}
	msg := fmt.Sprintf("lazydeck: %s %s on %s", ev.Operation, status, ev.DeviceID)
	if ev.Message != "" {
		msg += ": " + ev.Message
	}
	return msg
}

// Fanout dispatches an Event to every sender, ignoring individual failures
// (a single unreachable webhook must not block, or be conflated with, the
// others).
type Fanout []Sender

// Send delivers ev to every sender in turn (sequential is fine at this
// volume — one event per finished job) and returns the first error
// encountered, after attempting all of them.
func (f Fanout) Send(ctx context.Context, ev Event) error {
	var firstErr error
	for _, s := range f {
		if err := s.Send(ctx, ev); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
