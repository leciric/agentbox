package api

import (
	"context"
	"net/http"
	"net/url"
)

// TakeSnap hands a capture to the daemon, which tells the app.
func (c *Client) TakeSnap(ctx context.Context, req SnapRequest) (Snap, error) {
	var out Snap
	return out, c.do(ctx, http.MethodPost, "/v1/snaps", req, &out)
}

func (c *Client) Snaps(ctx context.Context) ([]Snap, error) {
	var out []Snap
	return out, c.do(ctx, http.MethodGet, "/v1/snaps", nil, &out)
}

// SendSnap sends a capture as a bug report, and answers with the chat
// message it became.
func (c *Client) SendSnap(ctx context.Context, id string, req SnapSendRequest) (ChatItem, error) {
	var out ChatItem
	return out, c.do(ctx, http.MethodPost, "/v1/snaps/"+url.PathEscape(id)+"/send", req, &out)
}

func (c *Client) DropSnap(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/snaps/"+url.PathEscape(id), nil, nil)
}

func browserCookiesPath(project string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/browser-cookies"
}

func (c *Client) BrowserCookies(ctx context.Context, project string) (BrowserCookies, error) {
	var out BrowserCookies
	return out, c.do(ctx, http.MethodGet, browserCookiesPath(project), nil, &out)
}

func (c *Client) PreviewBrowserCookies(ctx context.Context, project, export string) (BrowserCookiesPreview, error) {
	var out BrowserCookiesPreview
	return out, c.do(ctx, http.MethodPost, browserCookiesPath(project)+"/preview", BrowserCookiesPreviewRequest{Export: export}, &out)
}

func (c *Client) ImportBrowserCookies(ctx context.Context, project, export string, domains []string) (BrowserCookies, error) {
	var out BrowserCookies
	return out, c.do(ctx, http.MethodPut, browserCookiesPath(project), ImportBrowserCookiesRequest{Export: export, Domains: domains}, &out)
}

func (c *Client) RemoveBrowserCookies(ctx context.Context, project string) error {
	return c.do(ctx, http.MethodDelete, browserCookiesPath(project), nil, nil)
}

// BrowserProfiles lists the browsers installed on the host, to import from.
func (c *Client) BrowserProfiles(ctx context.Context, project string) (BrowserProfiles, error) {
	var out BrowserProfiles
	return out, c.do(ctx, http.MethodGet, browserCookiesPath(project)+"/profiles", nil, &out)
}

// ImportFromBrowser imports every cookie of one installed browser profile.
func (c *Client) ImportFromBrowser(ctx context.Context, project, profileID, keyringSecret string) (BrowserCookies, error) {
	var out BrowserCookies
	return out, c.do(ctx, http.MethodPost, browserCookiesPath(project)+"/from-browser",
		ImportFromBrowserRequest{ProfileID: profileID, KeyringSecret: keyringSecret}, &out)
}
