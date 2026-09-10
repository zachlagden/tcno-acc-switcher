package wisprstats

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

const (
	StatsEndpoint = "https://api.wisprflow.ai/history/stats"
	TokenEndpoint = "https://dodjkfqhwrzqjwkfnthl.supabase.co/auth/v1/token?grant_type=refresh_token"
	PublicAnonKey = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6ImRvZGprZnFod3J6cWp3a2ZudGhsIiwicm9sZSI6ImFub24iLCJpYXQiOjE3MTk4ODQzMDcsImV4cCI6MjAzNTQ2MDMwN30.h6EeQ_6kqFeznH25icVUX0Szn9__kc8HoSXAsxxBWG8"

	maxResponseBytes = 4 << 20
)

var (
	ErrUnauthorized   = errors.New("wisprstats: access token rejected")
	ErrSessionExpired = errors.New("wisprstats: refresh token rejected, sign in again")
)

type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

type TokenResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	TokenType    string          `json:"token_type"`
	ExpiresIn    int64           `json:"expires_in"`
	ExpiresAt    int64           `json:"expires_at"`
	User         json.RawMessage `json:"user"`
}

type Client struct {
	HTTP     Doer
	StatsURL string
	TokenURL string
	AnonKey  string
}

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		StatsURL: StatsEndpoint,
		TokenURL: TokenEndpoint,
		AnonKey:  PublicAnonKey,
	}
}

func (c *Client) FetchStats(ctx context.Context, accessToken string) (*Stats, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, ErrUnauthorized
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.StatsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")

	status, body, err := c.do(req)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, ErrUnauthorized
	case status < 200 || status >= 300:
		return nil, fmt.Errorf("wisprstats: stats endpoint returned HTTP %d", status)
	}
	return ParseAPIStats(body)
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, ErrSessionExpired
	}
	payload, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Authorization", "Bearer "+c.AnonKey)

	status, body, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, ErrSessionExpired
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("wisprstats: token endpoint returned HTTP %d", status)
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("wisprstats: parse token response: %w", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		return nil, errors.New("wisprstats: token response is missing tokens")
	}
	return &tok, nil
}

func (c *Client) do(req *http.Request) (int, []byte, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}
