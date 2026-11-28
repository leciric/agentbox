package api

import (
	"context"
	"encoding/json"
	"net/http"
)

// Customization is the app's look and layout as the user changed it. The
// lead's and the Home chat's sockets answer it at the same path.
func (c *Client) Customization(ctx context.Context) (CustomizationStatus, error) {
	var out CustomizationStatus
	return out, c.do(ctx, http.MethodGet, "/v1/customization", nil, &out)
}

// PatchCustomization changes it by a JSON merge patch: what changes names
// is set, a null puts AgentBox's own back. A change the schema refuses is an
// error that says why, and changes nothing.
func (c *Client) PatchCustomization(ctx context.Context, changes json.RawMessage) (CustomizationStatus, error) {
	var out CustomizationStatus
	return out, c.do(ctx, http.MethodPatch, "/v1/customization", changes, &out)
}

// ResetCustomization puts AgentBox's own look back.
func (c *Client) ResetCustomization(ctx context.Context) (CustomizationStatus, error) {
	var out CustomizationStatus
	return out, c.do(ctx, http.MethodDelete, "/v1/customization", nil, &out)
}

// Theme is the appearance setting and the desktop theme it follows.
func (c *Client) Theme(ctx context.Context) (Theme, error) {
	var out Theme
	return out, c.do(ctx, http.MethodGet, "/v1/theme", nil, &out)
}

// SetAppearance sets the window's mode: AppearanceFollow, AppearanceLight,
// AppearanceDark or AppearanceDracula.
func (c *Client) SetAppearance(ctx context.Context, appearance string) (Theme, error) {
	var out Theme
	return out, c.do(ctx, http.MethodPatch, "/v1/theme", UpdateThemeRequest{Appearance: &appearance}, &out)
}
