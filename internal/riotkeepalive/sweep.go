package riotkeepalive

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// DefaultInterval is how often saved accounts are topped up while the app runs.
//
// The id_token is good for an hour, but that is not what we are protecting — the
// refresh token's own lifetime is opaque (it is a JWE) and is what eventually
// expires. Refreshing more often than necessary is not free: every refresh rotates
// the token, and every rotation is another chance to race a running client. Twelve
// hours keeps accounts warm without churning.
const DefaultInterval = 12 * time.Hour

// DefaultSpacing separates per-account requests so a sweep across many accounts does
// not arrive as a burst.
const DefaultSpacing = 3 * time.Second

// ErrNoGuard is returned when a Sweeper is used without a running-client check.
// This is deliberately fatal rather than defaulting to "no guard": refreshing an
// account while Riot Client holds the same token is the one way this tool could
// actually cost someone a session.
var ErrNoGuard = errors.New("riotkeepalive: Sweeper.ClientRunning must be set")

// ErrClientRunning means the sweep was skipped because Riot was up.
var ErrClientRunning = errors.New("riotkeepalive: Riot Client is running, skipping sweep")

// Target is one saved account carrying a persisted session.
type Target struct {
	AccountName  string
	SettingsPath string
}

// Result is the per-account outcome of a sweep.
type Result struct {
	Target
	Refreshed bool
	// NeedsLogin is set when Riot rejected the token outright. Callers should
	// surface this in the UI — no retry will recover it.
	NeedsLogin bool
	Err        error
}

// CollectTargets walks a platform's login cache and returns every saved account that
// holds a Riot settings file. Accounts without one are skipped silently: an account
// saved before "Stay signed in" was ticked simply has nothing to refresh.
func CollectTargets(cacheRoot string) ([]Target, error) {
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("riotkeepalive: read login cache: %w", err)
	}

	var targets []Target
	for _, e := range entries {
		if !e.IsDir() {
			continue // ids.json, ordering files, etc.
		}
		accountDir := filepath.Join(cacheRoot, e.Name())
		found := ""
		_ = filepath.WalkDir(accountDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || found != "" {
				return nil
			}
			if !d.IsDir() && d.Name() == SettingsFileName {
				found = path
			}
			return nil
		})
		if found != "" {
			targets = append(targets, Target{AccountName: e.Name(), SettingsPath: found})
		}
	}
	return targets, nil
}

// Sweeper periodically refreshes every saved Riot account.
type Sweeper struct {
	CacheRoot string
	Client    *Client
	Interval  time.Duration
	Spacing   time.Duration
	Log       *slog.Logger

	// ClientRunning reports whether any Riot process is up. Required — see ErrNoGuard.
	ClientRunning func() bool

	// now and sleep exist so tests do not have to wait.
	now   func() time.Time
	sleep func(context.Context, time.Duration)
}

func (s *Sweeper) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Sweeper) timeNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Sweeper) pause(ctx context.Context, d time.Duration) {
	if s.sleep != nil {
		s.sleep(ctx, d)
		return
	}
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Run sweeps on a ticker until ctx is cancelled. It does not sweep immediately on
// start: the app has just launched, which is exactly when a client is most likely
// to be starting up alongside it.
func (s *Sweeper) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			results, err := s.SweepOnce(ctx)
			if err != nil {
				if errors.Is(err, ErrClientRunning) {
					s.logger().Debug("riot keepalive skipped", "reason", "client running")
					continue
				}
				s.logger().Warn("riot keepalive sweep failed", "err", err)
				continue
			}
			var ok, expired, failed int
			for _, r := range results {
				switch {
				case r.Refreshed:
					ok++
				case r.NeedsLogin:
					expired++
				case r.Err != nil:
					failed++
				}
			}
			s.logger().Info("riot keepalive sweep complete",
				"refreshed", ok, "needs_login", expired, "failed", failed)
		}
	}
}

// SweepOnce refreshes every saved account exactly once.
//
// It refuses to run at all while Riot is up. Riot rotates the refresh token on every
// use, so a client refreshing concurrently would leave one side holding a superseded
// token — which reuse detection can read as theft and answer by revoking the family.
func (s *Sweeper) SweepOnce(ctx context.Context) ([]Result, error) {
	if s.ClientRunning == nil {
		return nil, ErrNoGuard
	}
	if s.ClientRunning() {
		return nil, ErrClientRunning
	}

	targets, err := CollectTargets(s.CacheRoot)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, nil
	}

	client := s.Client
	if client == nil {
		client = NewClient()
	}
	spacing := s.Spacing
	if spacing <= 0 {
		spacing = DefaultSpacing
	}

	results := make([]Result, 0, len(targets))
	for i, t := range targets {
		if err := ctx.Err(); err != nil {
			break
		}
		if i > 0 {
			s.pause(ctx, spacing)
		}
		// Re-check between accounts: the user may have launched a game mid-sweep.
		if s.ClientRunning() {
			s.logger().Debug("riot keepalive halted mid-sweep", "reason", "client started")
			break
		}
		results = append(results, s.refreshOne(ctx, client, t))
	}
	return results, nil
}

func (s *Sweeper) refreshOne(ctx context.Context, client *Client, t Target) Result {
	res := Result{Target: t}

	data, err := os.ReadFile(t.SettingsPath)
	if err != nil {
		res.Err = err
		return res
	}
	sess, err := Parse(data)
	if err != nil {
		// No session or a DPoP-bound one: nothing to do, and not an error worth alarming about.
		res.Err = err
		return res
	}

	tok, err := client.Refresh(ctx, sess.RefreshToken)
	if err != nil {
		res.Err = err
		res.NeedsLogin = errors.Is(err, ErrSessionExpired)
		return res
	}

	updated, err := Apply(data, tok, s.timeNow().UnixMilli())
	if err != nil {
		// We hold a rotated token we cannot persist. Say so loudly: the saved copy is
		// now stale, and restoring it later may trip reuse detection.
		res.Err = fmt.Errorf("refreshed but could not persist (saved token is now stale): %w", err)
		return res
	}
	if err := writeFileAtomic(t.SettingsPath, updated); err != nil {
		res.Err = fmt.Errorf("refreshed but could not persist (saved token is now stale): %w", err)
		return res
	}

	res.Refreshed = true
	return res
}

// writeFileAtomic writes via a temp file in the same directory and renames over the
// target, so a crash mid-write cannot leave a saved account holding half a token.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".riotkeepalive-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		_ = os.Chmod(tmp, info.Mode())
	}
	return os.Rename(tmp, path)
}
