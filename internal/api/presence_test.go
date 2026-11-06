package api

import (
	"context"
	"testing"
)

func TestPresenceEndpoints(t *testing.T) {
	ctx := context.Background()
	runEndpointCases(t, []endpointCase{
		{
			name: "RenewPresence", method: "PUT", path: "/v1/presence",
			body: `{"client":"w1","kind":"desktop","views":[{"project":"p","agent":"p/a","resources":true}],"power":{"onBattery":true}}`,
			resp: `{"ttlSeconds":45,"idle":true}`,
			run: func(t *testing.T, c *Client) {
				got, err := c.RenewPresence(ctx, PresenceRequest{Client: "w1", Kind: "desktop", Views: []PresenceView{{Project: "p", Agent: "p/a", Resources: true}}, Power: &HostPower{OnBattery: true}})
				if err != nil || got.TTLSeconds != 45 || !got.Idle {
					t.Fatalf("got %+v, %v", got, err)
				}
			},
		},
		{
			name: "EndPresence", method: "DELETE", path: "/v1/presence/w%201",
			run: func(t *testing.T, c *Client) {
				if err := c.EndPresence(ctx, "w 1"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "AgentResources", method: "GET", path: "/v1/agents/p/a/resources",
			resp: `{"ref":"p/a","intervalSeconds":2,"windowSeconds":600,"sampling":true,"points":[{"at":"2026-11-06T12:00:00Z","cpu":50,"memory":1,"swap":0,"diskRead":2,"diskWrite":3}]}`,
			run: func(t *testing.T, c *Client) {
				got, err := c.AgentResources(ctx, "p", "a")
				if err != nil || !got.Sampling || len(got.Points) != 1 || got.Points[0].CPU != 50 {
					t.Fatalf("got %+v, %v", got, err)
				}
			},
		},
	})
	if !(HostPower{Thermal: "critical"}).Saving() || (HostPower{Thermal: "fair"}).Saving() {
		t.Fatal("Saving")
	}
}
