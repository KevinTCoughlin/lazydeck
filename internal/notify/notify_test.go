package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhookSendFormatsDiscordPayload(t *testing.T) {
	var gotBody discordPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	w := NewWebhook(srv.URL + "/discord.com/api/webhooks/123/abc")
	ev := Event{DeviceID: "deck-1", Operation: "deploy", Succeeded: true}
	if err := w.Send(context.Background(), ev); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotBody.Content == "" {
		t.Fatalf("expected non-empty Discord content field")
	}
}

func TestWebhookSendFormatsGenericPayload(t *testing.T) {
	var gotBody Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := NewWebhook(srv.URL)
	ev := Event{DeviceID: "deck-1", Operation: "logs-sync", Succeeded: false, Message: "ssh timed out"}
	if err := w.Send(context.Background(), ev); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotBody.DeviceID != ev.DeviceID || gotBody.Message != ev.Message {
		t.Fatalf("gotBody = %#v, want fields from %#v", gotBody, ev)
	}
}

func TestWebhookSendNonSuccessStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	w := NewWebhook(srv.URL)
	err := w.Send(context.Background(), Event{})
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestPayloadDetection(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://discord.com/api/webhooks/1/token", "discord"},
		{"https://discordapp.com/api/webhooks/1/token", "discord"},
		{"https://hooks.slack.com/services/T/B/X", "slack"},
		{"https://example.com/hook", "generic"},
	}
	for _, tc := range cases {
		w := &Webhook{URL: tc.url}
		payload := w.payload(Event{DeviceID: "d", Operation: "deploy", Succeeded: true})
		switch tc.want {
		case "discord":
			if _, ok := payload.(discordPayload); !ok {
				t.Errorf("%s: payload = %T, want discordPayload", tc.url, payload)
			}
		case "slack":
			if _, ok := payload.(slackPayload); !ok {
				t.Errorf("%s: payload = %T, want slackPayload", tc.url, payload)
			}
		case "generic":
			if _, ok := payload.(Event); !ok {
				t.Errorf("%s: payload = %T, want Event", tc.url, payload)
			}
		}
	}
}

func TestFanoutReturnsFirstErrorButSendsToAll(t *testing.T) {
	var calls int
	okSender := senderFunc(func(ctx context.Context, ev Event) error {
		calls++
		return nil
	})
	failSender := senderFunc(func(ctx context.Context, ev Event) error {
		calls++
		return errors.New("unreachable")
	})

	f := Fanout{okSender, failSender, okSender}
	err := f.Send(context.Background(), Event{})
	if err == nil {
		t.Fatal("expected an error from the failing sender")
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (fanout must not short-circuit)", calls)
	}
}

type senderFunc func(ctx context.Context, ev Event) error

func (f senderFunc) Send(ctx context.Context, ev Event) error { return f(ctx, ev) }
