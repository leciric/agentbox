package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// The connectors client. A target is "<project>" or "<project>/<agent>", the
// way the secrets client takes one.

// ConnectorsPath is the API path of a scope's connectors.
func ConnectorsPath(target string) (string, error) {
	path, err := SecretsPath(target)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, "/secrets") + "/connectors", nil
}

func (c *Client) connectorDo(ctx context.Context, method, target, suffix string, body, out any) error {
	path, err := ConnectorsPath(target)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path+suffix, body, out)
}

// Connectors lists a scope's connectors: a project's own, or everything an
// agent is given.
func (c *Client) Connectors(ctx context.Context, target string) ([]Connector, error) {
	var out []Connector
	return out, c.connectorDo(ctx, http.MethodGet, target, "", nil, &out)
}

// Connector is one connector and where it stands.
func (c *Client) Connector(ctx context.Context, target, name string) (Connector, error) {
	var out Connector
	return out, c.connectorDo(ctx, http.MethodGet, target, "/"+url.PathEscape(name), nil, &out)
}

// SetConnector adds a connector or changes one.
func (c *Client) SetConnector(ctx context.Context, target, name string, req SetConnectorRequest) (Connector, error) {
	var out Connector
	return out, c.connectorDo(ctx, http.MethodPut, target, "/"+url.PathEscape(name), req, &out)
}

// RemoveConnector forgets a connector, sign-in and all.
func (c *Client) RemoveConnector(ctx context.Context, target, name string) error {
	return c.connectorDo(ctx, http.MethodDelete, target, "/"+url.PathEscape(name), nil, nil)
}

// ConnectConnector starts a sign-in, and answers with the page to open.
func (c *Client) ConnectConnector(ctx context.Context, target, name string) (ConnectResult, error) {
	var out ConnectResult
	return out, c.connectorDo(ctx, http.MethodPost, target, "/"+url.PathEscape(name)+"/connect", nil, &out)
}

// DisconnectConnector forgets a connector's sign-in and keeps the connector.
func (c *Client) DisconnectConnector(ctx context.Context, target, name string) (Connector, error) {
	var out Connector
	return out, c.connectorDo(ctx, http.MethodPost, target, "/"+url.PathEscape(name)+"/disconnect", nil, &out)
}

// SelfConnectors are the connectors an agent is given, from inside it.
func (c *Client) SelfConnectors(ctx context.Context) ([]SelfConnector, error) {
	var out []SelfConnector
	return out, c.do(ctx, http.MethodGet, "/v1/self/connectors", nil, &out)
}

// SelfConnectorURL is where a connector's relay is on the in-agent socket,
// for connectors.Relay.
func (c *Client) SelfConnectorURL(name string) string {
	return c.base + "/v1/self/connectors/" + url.PathEscape(name) + "/mcp"
}

// RequestConnector asks the user, from inside an agent, to connect a
// connector it needs, and waits for their answer.
func (c *Client) RequestConnector(ctx context.Context, req ConnectorRequest) (Question, error) {
	var out Question
	return out, c.do(ctx, http.MethodPost, "/v1/self/connector", req, &out)
}

// ProjectConnectors lists the connectors of the project behind the lead
// socket, and where each stands. Never a token.
func (c *Client) ProjectConnectors(ctx context.Context) ([]Connector, error) {
	var out []Connector
	return out, c.do(ctx, http.MethodGet, c.leadPath("/connectors"), nil, &out)
}
