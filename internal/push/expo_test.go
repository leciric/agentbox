package push_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/internal/push"
)

func TestSend(t *testing.T) {
	var batches []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msgs []push.Message
		if err := json.NewDecoder(r.Body).Decode(&msgs); err != nil || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request: %v, %q", err, r.Header.Get("Content-Type"))
		}
		batches = append(batches, len(msgs))
		var data []string
		for _, m := range msgs {
			if m.To == "ExponentPushToken[gone]" {
				data = append(data, `{"status":"error","message":"not registered","details":{"error":"DeviceNotRegistered"}}`)
			} else {
				data = append(data, `{"status":"ok","id":"x"}`)
			}
		}
		_, _ = fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(data, ","))
	}))
	defer srv.Close()

	msgs := make([]push.Message, 150)
	for i := range msgs {
		msgs[i] = push.Message{To: "ExponentPushToken[ok]", Title: "t"}
	}
	msgs[120].To = "ExponentPushToken[gone]"
	tickets, err := push.Client{URL: srv.URL}.Send(context.Background(), msgs)
	if err != nil || len(tickets) != 150 {
		t.Fatalf("Send = %d tickets, %v", len(tickets), err)
	}
	if fmt.Sprint(batches) != "[100 50]" {
		t.Errorf("batches = %v, want at most 100 a request", batches)
	}
	if !tickets[120].Gone() || tickets[0].Gone() || tickets[0].Status != "ok" {
		t.Errorf("tickets = %+v, %+v", tickets[0], tickets[120])
	}
}

func TestSendRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"errors":[{"code":"VALIDATION_ERROR","message":"bad"}]}`)
	}))
	defer srv.Close()
	_, err := push.Client{URL: srv.URL}.Send(context.Background(), []push.Message{{To: "ExponentPushToken[a]"}})
	if err == nil || !strings.Contains(err.Error(), "VALIDATION_ERROR") {
		t.Fatalf("Send = %v", err)
	}
}

func TestIsToken(t *testing.T) {
	for s, want := range map[string]bool{
		"ExponentPushToken[abc]": true, "ExpoPushToken[abc]": true,
		"": false, "abc": false, "ExponentPushToken[abc": false, "https://fcm.googleapis.com/x": false,
	} {
		if push.IsToken(s) != want {
			t.Errorf("IsToken(%q) = %v", s, !want)
		}
	}
}
