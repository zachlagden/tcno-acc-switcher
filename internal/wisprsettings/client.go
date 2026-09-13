package wisprsettings

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

	"TcNo-Acc-Switcher/internal/wisprstats"
)

const (
	PrefsEndpoint      = "https://api.wisprflow.ai/api/v1/user/preferences"
	DictionaryEndpoint = "https://api.wisprflow.ai/api/v1/dictionary/personal"

	maxResponseBytes = 16 << 20
)

var (
	ErrConflict       = errors.New("wisprsettings: preferences changed on the server")
	ErrNoServerPrefs  = errors.New("wisprsettings: the server has no preferences for this account")
	ErrBadServerReply = errors.New("wisprsettings: unexpected server reply")
)

type Client struct {
	HTTP          wisprstats.Doer
	PrefsURL      string
	DictionaryURL string
}

type ServerPrefs struct {
	Preferences map[string]json.RawMessage
	ModifiedAt  string
}

type RemoteItem = map[string]json.RawMessage

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		PrefsURL:      PrefsEndpoint,
		DictionaryURL: DictionaryEndpoint,
	}
}

func (c *Client) GetPrefs(ctx context.Context, token string) (ServerPrefs, error) {
	body, err := c.call(ctx, http.MethodGet, c.PrefsURL, token, nil)
	if err != nil {
		return ServerPrefs{}, err
	}
	var reply struct {
		Preferences map[string]json.RawMessage `json:"preferences"`
		ModifiedAt  *string                    `json:"modified_at"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return ServerPrefs{}, fmt.Errorf("%w: %v", ErrBadServerReply, err)
	}
	if reply.Preferences == nil || reply.ModifiedAt == nil {
		return ServerPrefs{}, ErrNoServerPrefs
	}
	return ServerPrefs{Preferences: reply.Preferences, ModifiedAt: *reply.ModifiedAt}, nil
}

func (c *Client) PostPrefs(ctx context.Context, token string, payload []byte) (string, error) {
	body, err := c.call(ctx, http.MethodPost, c.PrefsURL, token, payload)
	if err != nil {
		return "", err
	}
	var reply struct {
		ModifiedAt *string `json:"modified_at"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadServerReply, err)
	}
	if reply.ModifiedAt == nil {
		return "", nil
	}
	return *reply.ModifiedAt, nil
}

func (c *Client) GetDictionary(ctx context.Context, token string) ([]RemoteItem, error) {
	body, err := c.call(ctx, http.MethodGet, c.DictionaryURL, token, nil)
	if err != nil {
		return nil, err
	}
	var items []RemoteItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadServerReply, err)
	}
	return items, nil
}

func (c *Client) PostDictionary(ctx context.Context, token string, items []RemoteItem) error {
	payload, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = c.call(ctx, http.MethodPost, c.DictionaryURL, token, payload)
	return err
}

func (c *Client) call(ctx context.Context, method, url, token string, payload []byte) ([]byte, error) {
	if strings.TrimSpace(token) == "" {
		return nil, wisprstats.ErrUnauthorized
	}
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, wisprstats.ErrUnauthorized
	case resp.StatusCode == http.StatusConflict:
		return nil, ErrConflict
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("wisprsettings: %s %s returned HTTP %d", method, url, resp.StatusCode)
	}
	return body, nil
}
