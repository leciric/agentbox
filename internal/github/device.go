package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ClientIDPlaceholder stands in for the client ID of AgentBox's OAuth App
// until it exists. Replace it with the app's client ID (Settings → Developer
// settings → OAuth Apps on github.com, with "Enable Device Flow" ticked):
// it is public, and the device flow needs no client secret.
const ClientIDPlaceholder = "REPLACE_WITH_AGENTBOX_OAUTH_APP_CLIENT_ID"

// clientID is the OAuth App the device flow signs in to.
const clientID = ClientIDPlaceholder

// DeviceScopes are what `gh auth login` asks for, so agents' gh can do what
// it does on a developer's own machine.
var DeviceScopes = []string{"repo", "read:org", "workflow", "gist"}

// What ends a device flow without a token, so a caller can offer the right
// next step: a new code for an expired one, a token for a refusal.
var (
	// ErrDeviceExpired is the code timing out before anyone entered it.
	ErrDeviceExpired = errors.New("the code expired before it was entered on GitHub: start again for a new one")
	// ErrDeviceDenied is the user pressing Cancel on GitHub's page.
	ErrDeviceDenied = errors.New("the sign-in was denied on GitHub")
	// ErrNoClientID is a build with no OAuth App to sign in to.
	ErrNoClientID = errors.New("this build of AgentBox has no GitHub OAuth App: set AGENTBOX_GITHUB_CLIENT_ID, or use a token instead")
)

// ClientID is the OAuth App's client ID: AGENTBOX_GITHUB_CLIENT_ID when set,
// the built-in one otherwise, and "" while that is still the placeholder.
func ClientID() string {
	if id := strings.TrimSpace(os.Getenv("AGENTBOX_GITHUB_CLIENT_ID")); id != "" {
		return id
	}
	if clientID == ClientIDPlaceholder {
		return ""
	}
	return clientID
}

// DeviceFlow signs in to GitHub with OAuth's device flow: GitHub shows a code
// on this machine, the user types it in at github.com/login/device in any
// browser, and the token arrives here. Nothing listens on a port, so it works
// from inside a VM, and nothing needs a client secret.
type DeviceFlow struct {
	ClientID string
	HTTP     *http.Client
	// BaseURL is github.com's root; empty is AGENTBOX_GITHUB_WEB, or
	// github.com itself.
	BaseURL string
	// Wait sleeps between polls; tests make it return at once.
	Wait func(ctx context.Context, d time.Duration) error
}

// DeviceCode is what the user needs to finish a sign-in, and what the
// daemon needs to poll for it.
type DeviceCode struct {
	DeviceCode      string    `json:"device_code"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"-"`
	Interval        int       `json:"interval"`
	ExpiresIn       int       `json:"expires_in"`
}

const defaultWebURL = "https://github.com"

// Start asks GitHub for a code to show the user.
func (d DeviceFlow) Start(ctx context.Context, scopes []string) (DeviceCode, error) {
	if d.ClientID == "" {
		return DeviceCode{}, ErrNoClientID
	}
	var code DeviceCode
	var fail oauthError
	form := url.Values{"client_id": {d.ClientID}, "scope": {strings.Join(scopes, " ")}}
	if err := d.post(ctx, "/login/device/code", form, &code, &fail); err != nil {
		return DeviceCode{}, err
	}
	if fail.Code != "" {
		return DeviceCode{}, fail.err()
	}
	if code.DeviceCode == "" || code.UserCode == "" {
		return DeviceCode{}, errors.New("GitHub sent no device code")
	}
	if code.VerificationURI == "" {
		code.VerificationURI = d.base() + "/login/device"
	}
	if code.ExpiresIn <= 0 {
		code.ExpiresIn = 900
	}
	code.ExpiresAt = time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	return code, nil
}

// Poll waits for the user to enter the code and returns the token, polling
// no faster than GitHub asks: its interval to begin with, five seconds more
// for every slow_down. It ends with ErrDeviceExpired or ErrDeviceDenied
// when the user doesn't finish, and with ctx's error when it is cancelled.
func (d DeviceFlow) Poll(ctx context.Context, code DeviceCode) (string, error) {
	interval := time.Duration(max(code.Interval, 1)) * time.Second
	form := url.Values{
		"client_id":   {d.ClientID},
		"device_code": {code.DeviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	for {
		if !code.ExpiresAt.IsZero() && time.Now().After(code.ExpiresAt) {
			return "", ErrDeviceExpired
		}
		if err := d.wait(ctx, interval); err != nil {
			return "", err
		}
		var got struct {
			AccessToken string `json:"access_token"`
			oauthError
			Interval int `json:"interval"`
		}
		if err := d.post(ctx, "/login/oauth/access_token", form, &got, nil); err != nil {
			return "", err
		}
		switch got.Code {
		case "":
			if got.AccessToken == "" {
				return "", errors.New("GitHub sent no token")
			}
			return got.AccessToken, nil
		case "authorization_pending":
		case "slow_down":
			// GitHub says how long to wait from now on; RFC 8628 says five
			// seconds more when it doesn't.
			if got.Interval > 0 {
				interval = time.Duration(got.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
		case "expired_token":
			return "", ErrDeviceExpired
		case "access_denied":
			return "", ErrDeviceDenied
		default:
			return "", got.err()
		}
	}
}

// oauthError is GitHub's answer when it says no: these endpoints answer 200
// with an error field rather than an error status.
type oauthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e oauthError) err() error {
	switch e.Code {
	case "device_flow_disabled":
		return errors.New("GitHub's device flow is off for AgentBox's OAuth App: turn on Enable Device Flow in its settings")
	case "incorrect_client_credentials":
		return errors.New("GitHub doesn't know AgentBox's OAuth App: check AGENTBOX_GITHUB_CLIENT_ID")
	}
	if e.Description != "" {
		return fmt.Errorf("GitHub refused the sign-in: %s (%s)", e.Description, e.Code)
	}
	return fmt.Errorf("GitHub refused the sign-in: %s", e.Code)
}

func (d DeviceFlow) base() string {
	if d.BaseURL != "" {
		return strings.TrimRight(d.BaseURL, "/")
	}
	// AGENTBOX_GITHUB_WEB moves it as AGENTBOX_GITHUB_API moves the API.
	if base := strings.TrimRight(os.Getenv("AGENTBOX_GITHUB_WEB"), "/"); base != "" {
		return base
	}
	return defaultWebURL
}

func (d DeviceFlow) wait(ctx context.Context, interval time.Duration) error {
	if d.Wait != nil {
		return d.Wait(ctx, interval)
	}
	t := time.NewTimer(interval)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// post sends a form and decodes the JSON answer into out, and into fail too
// when fail is given, for an endpoint whose refusals have another shape.
func (d DeviceFlow) post(ctx context.Context, path string, form url.Values, out any, fail *oauthError) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.base()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := d.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var e oauthError
		if json.Unmarshal(body, &e) == nil && e.Code != "" {
			return e.err()
		}
		return fmt.Errorf("GitHub answered %s for %s", res.Status, path)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GitHub's answer for %s isn't JSON: %w", path, err)
	}
	if fail != nil {
		_ = json.Unmarshal(body, fail)
	}
	return nil
}
