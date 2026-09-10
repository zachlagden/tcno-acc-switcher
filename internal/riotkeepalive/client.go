package riotkeepalive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Endpoint values come from Riot's public OIDC discovery document at
// https://auth.riotgames.com/.well-known/openid-configuration, which advertises
// grant_types_supported including "refresh_token" and an auth method of "none"
// (riot-client is a public client, so no client secret is involved).
const (
	TokenEndpoint  = "https://auth.riotgames.com/token"
	RevokeEndpoint = "https://auth.riotgames.com/token/revoke"
	ClientID       = "riot-client"

	// defaultUserAgent mirrors the real client's rso-auth agent. The version drifts
	// with client releases; a slightly stale one has historically still been accepted.
	defaultUserAgent = "RiotClient/DEFAULT rso-auth (Windows;10;;Professional, x64)"
)

// ErrSessionExpired means Riot rejected the refresh token outright. The account needs
// a manual sign-in; no amount of retrying will fix it.
var ErrSessionExpired = errors.New("riotkeepalive: refresh token rejected, manual sign-in required")

// ErrBlocked means we got an edge/CDN rejection rather than an OAuth answer. The most
// likely cause is TLS fingerprinting: Go's default ClientHello is as distinctive as
// Python's, and auth.riotgames.com sits behind a CDN that has historically filtered on
// it. If this shows up in the wild, swap Doer for a uTLS-backed transport — that is the
// whole reason this is an interface.
var ErrBlocked = errors.New("riotkeepalive: request rejected before reaching the token endpoint")

// Doer is the seam for both tests and a future uTLS transport.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// TokenResponse is the token endpoint's success payload.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// Client performs the refresh_token grant.
type Client struct {
	HTTP      Doer
	Endpoint  string
	UserAgent string
}

// NewClient returns a Client with sane timeouts and redirects disabled — the token
// endpoint should answer directly, and a redirect means something is intercepting us.
func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		Endpoint:  TokenEndpoint,
		UserAgent: defaultUserAgent,
	}
}

// Refresh exchanges a refresh token for a fresh token set, including a rotated
// refresh token that the caller MUST persist — the one passed in is dead afterwards.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, errors.New("riotkeepalive: empty refresh token")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ClientID},
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = TokenEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("riotkeepalive: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("riotkeepalive: read token response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		var tok TokenResponse
		if err := json.Unmarshal(body, &tok); err != nil {
			return nil, fmt.Errorf("riotkeepalive: decode token response: %w", err)
		}
		if tok.RefreshToken == "" {
			// Without a rotated token we have nothing safe to persist.
			return nil, errors.New("riotkeepalive: token response carried no refresh_token")
		}
		return &tok, nil

	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized:
		var oe oauthError
		if json.Unmarshal(body, &oe) == nil && oe.Error != "" {
			if oe.Error == "invalid_grant" || oe.Error == "invalid_request" {
				return nil, fmt.Errorf("%w (%s)", ErrSessionExpired, oe.Error)
			}
			return nil, fmt.Errorf("riotkeepalive: token endpoint error %q", oe.Error)
		}
		return nil, fmt.Errorf("riotkeepalive: token endpoint returned %d", resp.StatusCode)

	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
			return nil, fmt.Errorf("%w (status %d)", ErrBlocked, resp.StatusCode)
		}
		return nil, fmt.Errorf("riotkeepalive: token endpoint returned %d", resp.StatusCode)

	default:
		return nil, fmt.Errorf("riotkeepalive: unexpected status %d", resp.StatusCode)
	}
}
