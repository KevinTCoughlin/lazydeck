// Package notify posts job completion events to chat webhooks (Discord and
// Slack incoming webhooks, or a generic JSON endpoint) so a deploy/log-sync
// success or failure is visible outside the TUI/CLI, e.g. in a team channel.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Event is the information a webhook notification is built from. It mirrors
// the subset of jobs.Snapshot that's meaningful outside the process.
type Event struct {
	DeviceID  string
	Operation string
	Succeeded bool
	Message   string
	Time      time.Time
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
		return fmt.Errorf("building webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func (w *Webhook) payload(ev Event) any {
	text := formatText(ev)
	switch {
	case strings.Contains(w.URL, "discord.com/api/webhooks") || strings.Contains(w.URL, "discordapp.com/api/webhooks"):
		return discordPayload{Content: text}
	case strings.Contains(w.URL, "hooks.slack.com"):
		return slackPayload{Text: text}
	default:
		return ev
	}
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
