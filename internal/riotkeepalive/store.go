// Package riotkeepalive keeps saved Riot accounts from expiring.
//
// Current Riot Clients do not persist an SSO "ssid" cookie. They persist an OAuth2
// refresh token under psl.authorization.riot-client in RiotGamesPrivateSettings.yaml:
//
//	psl:
//	    authorization:
//	        riot-client:
//	            id_token: <RS256 JWT, 1h lifetime>
//	            refresh_token: <JWE, opaque>
//	            is_dpop_bound: false
//	            refresh_token_write_count: 1
//
// Keeping an account alive therefore means running the refresh_token grant against
// https://auth.riotgames.com/token and storing the rotated token back.
//
// IMPORTANT: Riot rotates the refresh token on every use (hence the write count).
// Replaying a superseded refresh token is, under normal OAuth2 reuse detection,
// treated as token theft and can revoke the whole token family. Everything in this
// package is therefore written to (a) never refresh an account whose files are
// currently live, and (b) write the rotated token back atomically or not at all.
package riotkeepalive

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// SettingsFileName is the file that actually carries the session. Note this is
// RiotGames..., not RiotClient... — the latter is what older tooling looks for and
// no longer exists on current clients.
const SettingsFileName = "RiotGamesPrivateSettings.yaml"

var (
	// ErrNoSession means the file parsed but carries no persisted login. The usual
	// cause is that "Stay signed in" was not ticked, in which case there is nothing
	// to keep alive — see the package docs.
	ErrNoSession = errors.New("riotkeepalive: no persisted session in settings file")

	// ErrDPoPBound means the refresh token is cryptographically bound to a key we
	// do not hold, so it cannot be refreshed out-of-process.
	ErrDPoPBound = errors.New("riotkeepalive: refresh token is DPoP-bound")
)

// Session is the persisted OAuth state for one saved account.
type Session struct {
	IDToken      string
	RefreshToken string
	IsDPoPBound  bool
	WriteCount   int
	LastCreation int64 // unix millis
}

type privateSettings struct {
	PSL struct {
		Authorization struct {
			RiotClient *struct {
				IDToken      string `yaml:"id_token"`
				RefreshToken string `yaml:"refresh_token"`
				IsDPoPBound  bool   `yaml:"is_dpop_bound"`
				WriteCount   int    `yaml:"refresh_token_write_count"`
				LastCreation int64  `yaml:"last_token_creation_time"`
			} `yaml:"riot-client"`
		} `yaml:"authorization"`
	} `yaml:"psl"`
}

// Parse reads the persisted session out of a RiotGamesPrivateSettings.yaml.
func Parse(data []byte) (*Session, error) {
	var ps privateSettings
	if err := yaml.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("riotkeepalive: parse settings: %w", err)
	}
	rc := ps.PSL.Authorization.RiotClient
	if rc == nil || rc.RefreshToken == "" {
		return nil, ErrNoSession
	}
	if rc.IsDPoPBound {
		return nil, ErrDPoPBound
	}
	return &Session{
		IDToken:      rc.IDToken,
		RefreshToken: rc.RefreshToken,
		IsDPoPBound:  rc.IsDPoPBound,
		WriteCount:   rc.WriteCount,
		LastCreation: rc.LastCreation,
	}, nil
}

// Apply writes a refreshed token set back into the settings bytes.
//
// It deliberately does NOT re-serialise the document. Riot writes this file with CRLF
// line endings and four-space indentation; a yaml round-trip would silently rewrite
// both. We replace the scalar values in place so every other byte survives untouched.
func Apply(data []byte, tok *TokenResponse, nowMillis int64) ([]byte, error) {
	if tok == nil || tok.RefreshToken == "" {
		return nil, errors.New("riotkeepalive: refusing to apply an empty refresh token")
	}
	// Validate before mutating: if the document is not the shape we expect, do nothing.
	cur, err := Parse(data)
	if err != nil {
		return nil, err
	}

	out := data
	if out, err = setScalar(out, "refresh_token", tok.RefreshToken); err != nil {
		return nil, err
	}
	if tok.IDToken != "" {
		if out, err = setScalar(out, "id_token", tok.IDToken); err != nil {
			return nil, err
		}
	}
	if out, err = setScalar(out, "last_token_creation_time", strconv.FormatInt(nowMillis, 10)); err != nil {
		return nil, err
	}
	if out, err = setScalar(out, "refresh_token_write_count", strconv.Itoa(cur.WriteCount+1)); err != nil {
		return nil, err
	}

	// Re-parse as a self-check: never hand back a file we just broke.
	if _, err := Parse(out); err != nil {
		return nil, fmt.Errorf("riotkeepalive: rewrite produced an unparseable file: %w", err)
	}
	return out, nil
}

// setScalar replaces the value of `key: value` on the single line that defines it,
// preserving indentation and any trailing carriage return. It requires exactly one
// match so an ambiguous document is an error rather than a silent half-edit.
func setScalar(data []byte, key, value string) ([]byte, error) {
	prefix := []byte(key + ":")
	lines := bytes.Split(data, []byte("\n"))
	found := -1
	for i, line := range lines {
		trimmed := bytes.TrimLeft(line, " \t")
		if bytes.HasPrefix(trimmed, prefix) {
			if found != -1 {
				return nil, fmt.Errorf("riotkeepalive: %q appears more than once", key)
			}
			found = i
		}
	}
	if found == -1 {
		return nil, fmt.Errorf("riotkeepalive: key %q not found", key)
	}

	line := lines[found]
	indent := line[:len(line)-len(bytes.TrimLeft(line, " \t"))]
	var cr []byte
	if bytes.HasSuffix(line, []byte("\r")) {
		cr = []byte("\r")
	}
	lines[found] = append(append(append(append([]byte{}, indent...),
		[]byte(key+": ")...), []byte(value)...), cr...)
	return bytes.Join(lines, []byte("\n")), nil
}
