// Package push sends notifications to the phone app (mobile/) through Expo's
// push service, which hands them to Apple's and Google's: the daemon posts to
// it directly, with no relay of AgentBox's own. A phone registers the Expo
// push token its app was given (expo-notifications), which names the app's
// EAS project and the device; Expo holds the APNs and FCM credentials of that
// project, so the daemon needs none.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ExpoURL is Expo's send endpoint.
const ExpoURL = "https://exp.host/--/api/v2/push/send"

// maxBatch is the most messages Expo takes in one request.
const maxBatch = 100

// Message is one notification to one phone.
type Message struct {
	To    string            `json:"to"`
	Title string            `json:"title"`
	Body  string            `json:"body,omitempty"`
	Data  map[string]string `json:"data,omitempty"`
	// Sound "default" plays the phone's notification sound.
	Sound string `json:"sound,omitempty"`
	// Priority "high" wakes an Android phone that dozes.
	Priority string `json:"priority,omitempty"`
	// ChannelID is the Android notification channel, which the app creates.
	ChannelID string `json:"channelId,omitempty"`
	// ThreadID groups an iPhone's notifications, here by agent.
	ThreadID string `json:"threadId,omitempty"`
}

// Ticket is Expo's answer for one message, in the order they were sent.
type Ticket struct {
	Status  string `json:"status"` // "ok" or "error"
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details"`
}

// Gone says the token no longer reaches a phone: forget it.
func (t Ticket) Gone() bool { return t.Details.Error == "DeviceNotRegistered" }

// Client sends to Expo. The zero value posts to ExpoURL.
type Client struct {
	URL  string
	HTTP *http.Client
}

// IsToken says s looks like an Expo push token.
func IsToken(s string) bool {
	return (strings.HasPrefix(s, "ExponentPushToken[") || strings.HasPrefix(s, "ExpoPushToken[")) && strings.HasSuffix(s, "]") && len(s) < 256
}

// Send posts msgs and returns a ticket for each. An error is the request
// failing as a whole; a message Expo refused is its ticket's.
func (c Client) Send(ctx context.Context, msgs []Message) ([]Ticket, error) {
	var out []Ticket
	for len(msgs) > 0 {
		batch := msgs[:min(len(msgs), maxBatch)]
		msgs = msgs[len(batch):]
		tickets, err := c.send(ctx, batch)
		if err != nil {
			return out, err
		}
		out = append(out, tickets...)
	}
	return out, nil
}

func (c Client) send(ctx context.Context, msgs []Message) ([]Ticket, error) {
	body, err := json.Marshal(msgs)
	if err != nil {
		return nil, err
	}
	url := c.URL
	if url == "" {
		url = ExpoURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data   []Ticket `json:"data"`
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &parsed)
	if res.StatusCode != http.StatusOK || len(parsed.Errors) > 0 {
		if len(parsed.Errors) > 0 {
			return nil, fmt.Errorf("expo push: %s: %s", parsed.Errors[0].Code, parsed.Errors[0].Message)
		}
		return nil, fmt.Errorf("expo push: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(parsed.Data) != len(msgs) {
		return nil, errors.New("expo push: an answer that doesn't match what was sent")
	}
	return parsed.Data, nil
}
