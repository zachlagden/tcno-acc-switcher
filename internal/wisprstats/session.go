package wisprstats

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	SessionKey      = "sb-dodjkfqhwrzqjwkfnthl-auth-token"
	SessionFileName = "session.json"
	ConfigFileName  = "config.json"
)

var ErrNoSession = errors.New("wisprstats: no signed-in session")

type Session struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	UserID       string
	Email        string
}

type storedSession struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	User         struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

func (s *Session) ValidAt(nowUnix, marginSeconds int64) bool {
	return s != nil && s.AccessToken != "" && s.ExpiresAt-marginSeconds > nowUnix
}

func ParseSession(data []byte) (*Session, error) {
	_, inner, err := decodeSessionFile(data)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(inner)
	if err != nil {
		return nil, err
	}
	var s storedSession
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("wisprstats: parse session: %w", err)
	}
	if strings.TrimSpace(s.AccessToken) == "" && strings.TrimSpace(s.RefreshToken) == "" {
		return nil, ErrNoSession
	}
	return &Session{
		AccessToken:  s.AccessToken,
		RefreshToken: s.RefreshToken,
		ExpiresAt:    s.ExpiresAt,
		UserID:       s.User.ID,
		Email:        s.User.Email,
	}, nil
}

func ApplyRefresh(data []byte, tok *TokenResponse, nowUnix int64) ([]byte, error) {
	if tok == nil || tok.AccessToken == "" || tok.RefreshToken == "" {
		return nil, errors.New("wisprstats: refusing to apply an incomplete token set")
	}
	before, err := ParseSession(data)
	if err != nil {
		return nil, err
	}
	outer, inner, err := decodeSessionFile(data)
	if err != nil {
		return nil, err
	}

	expiresAt := tok.ExpiresAt
	if expiresAt == 0 && tok.ExpiresIn > 0 {
		expiresAt = nowUnix + tok.ExpiresIn
	}
	values := map[string]any{
		"access_token":  tok.AccessToken,
		"refresh_token": tok.RefreshToken,
		"token_type":    "bearer",
		"expires_in":    tok.ExpiresIn,
		"expires_at":    expiresAt,
	}
	for key, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		inner[key] = encoded
	}
	if len(tok.User) > 0 && string(tok.User) != "null" {
		inner["user"] = tok.User
	}

	innerRaw, err := json.Marshal(inner)
	if err != nil {
		return nil, err
	}
	encodedInner, err := json.Marshal(string(innerRaw))
	if err != nil {
		return nil, err
	}
	outer[SessionKey] = encodedInner
	out, err := json.MarshalIndent(outer, "", "\t")
	if err != nil {
		return nil, err
	}

	after, err := ParseSession(out)
	if err != nil {
		return nil, fmt.Errorf("wisprstats: rewrite produced an unreadable session: %w", err)
	}
	if before.UserID != "" && after.UserID != before.UserID {
		return nil, errors.New("wisprstats: refreshed session belongs to a different user")
	}
	return out, nil
}

func decodeSessionFile(data []byte) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(data, &outer); err != nil {
		return nil, nil, fmt.Errorf("wisprstats: parse session file: %w", err)
	}
	raw, ok := outer[SessionKey]
	if !ok {
		return nil, nil, ErrNoSession
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, nil, fmt.Errorf("wisprstats: session value is not a string: %w", err)
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &inner); err != nil {
		return nil, nil, fmt.Errorf("wisprstats: parse session value: %w", err)
	}
	if inner == nil {
		return nil, nil, ErrNoSession
	}
	return outer, inner, nil
}
