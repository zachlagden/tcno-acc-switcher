package wisprstats

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	SourceLive     = "live"
	SourceSnapshot = "snapshot"
	SourceNone     = "none"

	ProblemNoData         = "no_data"
	ProblemSignedOut      = "signed_out"
	ProblemSessionExpired = "session_expired"
	ProblemCurrentExpired = "current_expired"
	ProblemNotInitialized = "not_initialized"
	ProblemSessionFile    = "session_file"
	ProblemNetwork        = "network"

	tokenMarginSeconds = 60
)

type Account struct {
	UniqueID    string
	DisplayName string
	Current     bool
	Dir         string
}

type AccountReport struct {
	UniqueID       string `json:"uniqueId"`
	DisplayName    string `json:"displayName"`
	Current        bool   `json:"current"`
	Source         string `json:"source"`
	Stats          *Stats `json:"stats"`
	Problem        string `json:"problem"`
	TokenRefreshed bool   `json:"tokenRefreshed"`
}

type Report struct {
	Accounts    []AccountReport `json:"accounts"`
	Totals      Totals          `json:"totals"`
	Live        bool            `json:"live"`
	GeneratedAt string          `json:"generatedAt"`
}

type Collector struct {
	Client    *Client
	Now       func() time.Time
	WriteFile func(path string, data []byte) error
}

func (c *Collector) Collect(ctx context.Context, accounts []Account, live bool) Report {
	rows := make([]AccountReport, 0, len(accounts))
	for _, a := range accounts {
		rows = append(rows, c.collectOne(ctx, a, live))
	}
	return Report{
		Accounts:    rows,
		Totals:      Summarize(rows),
		Live:        live,
		GeneratedAt: c.now().UTC().Format(time.RFC3339),
	}
}

func (c *Collector) collectOne(ctx context.Context, a Account, live bool) AccountReport {
	row := AccountReport{
		UniqueID:    a.UniqueID,
		DisplayName: a.DisplayName,
		Current:     a.Current,
		Source:      SourceNone,
	}
	if data, err := os.ReadFile(filepath.Join(a.Dir, ConfigFileName)); err == nil {
		if s, err := ParseSnapshot(data, c.now()); err == nil {
			row.Stats = s
			row.Source = SourceSnapshot
		}
	}
	if row.Stats == nil {
		row.Problem = ProblemNoData
	}
	if !live || ctx.Err() != nil {
		return row
	}

	stats, problem, refreshed := c.fetchLive(ctx, a)
	row.TokenRefreshed = refreshed
	if stats != nil {
		row.Stats = stats
		row.Source = SourceLive
		row.Problem = ""
		return row
	}
	row.Problem = problem
	return row
}

func (c *Collector) fetchLive(ctx context.Context, a Account) (*Stats, string, bool) {
	path := filepath.Join(a.Dir, SessionFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ProblemSignedOut, false
	}
	session, err := ParseSession(data)
	if err != nil {
		return nil, ProblemSignedOut, false
	}

	now := c.now().Unix()
	if session.ValidAt(now, tokenMarginSeconds) {
		stats, err := c.Client.FetchStats(ctx, session.AccessToken)
		if err == nil {
			return stats, "", false
		}
		if !errors.Is(err, ErrUnauthorized) {
			return nil, problemFor(err), false
		}
	}
	if a.Current {
		return nil, ProblemCurrentExpired, false
	}

	tok, err := c.Client.Refresh(ctx, session.RefreshToken)
	if err != nil {
		return nil, problemFor(err), false
	}
	updated, err := ApplyRefresh(data, tok, now)
	if err != nil {
		return nil, ProblemSessionFile, false
	}
	if err := c.writeFile(path, updated); err != nil {
		return nil, ProblemSessionFile, false
	}
	stats, err := c.Client.FetchStats(ctx, tok.AccessToken)
	if err != nil {
		return nil, problemFor(err), true
	}
	return stats, "", true
}

func problemFor(err error) string {
	switch {
	case errors.Is(err, ErrSessionExpired), errors.Is(err, ErrUnauthorized):
		return ProblemSessionExpired
	case errors.Is(err, ErrNotInitialized):
		return ProblemNotInitialized
	default:
		return ProblemNetwork
	}
}

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Collector) writeFile(path string, data []byte) error {
	if c.WriteFile != nil {
		return c.WriteFile(path, data)
	}
	return os.WriteFile(path, data, 0o600)
}
